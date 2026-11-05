package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/DrWhax/unfairplay/pkg/fairplay"
	"github.com/majd/ipatool/v2/pkg/appstore"
	"github.com/securityvoid/vphone-apps/internal/cache"
	"github.com/securityvoid/vphone-apps/internal/models"
	"github.com/securityvoid/vphone-apps/internal/store"
	"github.com/securityvoid/vphone-apps/internal/vm"
)

const installUsage = `usage: vphone-apps install <term...> --vm <name> [flags]
       vphone-apps install --id <bundleId> --vm <name> [flags]

flags:
  --id <bundleId>      install the app with exactly this bundle id
  --vm <name>          target VM (required)
  --purchase           acquire a license first if the app is not owned
  --keep-account-data  keep Apple account data in the decrypted IPA
  --keep               keep the decrypted IPA and print its path
  --launch             launch the app after installing
Exit codes: 0 success, 1 failure, 2 usage error.
`

type installOpts struct {
	id, vmName, term                string
	purchase, keepAccountData, keep bool
	launch                          bool
}

func (a *app) install(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("install", flag.ContinueOnError)
	var o installOpts
	fs.StringVar(&o.id, "id", "", "")
	fs.StringVar(&o.vmName, "vm", "", "")
	fs.BoolVar(&o.purchase, "purchase", false, "")
	fs.BoolVar(&o.keepAccountData, "keep-account-data", false, "")
	fs.BoolVar(&o.keep, "keep", false, "")
	fs.BoolVar(&o.launch, "launch", false, "")
	words, code, done := a.parse(fs, installUsage, args)
	if done {
		return code
	}
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	o.term = joinTerm(words)
	switch {
	case set["id"] && o.id == "":
		return a.usageError(installUsage, "--id needs a value")
	case set["id"] && len(words) > 0:
		return a.usageError(installUsage, "--id and a search term are mutually exclusive")
	case !set["id"] && o.term == "":
		return a.usageError(installUsage, "missing search term")
	case !set["vm"]:
		return a.usageError(installUsage, "--vm is required")
	}

	rc := 0
	c, f := a.run(ctx, &o)
	if f != nil {
		rc = a.report(f)
	}
	return a.finish(c, rc)
}

// finish removes the run directory unless it was kept; a failure to do so is
// reported and forces exit code 1.
func (a *app) finish(c *cache.Cache, rc int) int {
	if c == nil {
		return rc
	}
	if err := c.Clean(); err != nil {
		a.report(fail("cleanup", "%s", err.Error()).hint("delete " + c.Dir() + " manually"))
		return 1
	}
	return rc
}

func (a *app) run(ctx context.Context, o *installOpts) (*cache.Cache, *failure) {
	// 1. Guest path, then the VM check, before any download.
	guest, err := a.guestPath()
	if err != nil {
		return nil, fail("push", "%s", err.Error())
	}
	m, f := a.checkVM(o.vmName, 0)
	if f != nil {
		return nil, f
	}
	c, err := a.newCache()
	if err != nil {
		var ue *cache.UnsafeDirError
		if errors.As(err, &ue) {
			return nil, fail("cache", "%s", err.Error()).hint("remove it and re-run")
		}
		return nil, fail("cache", "%s", err.Error())
	}
	st, err := a.newStore()
	if err != nil {
		return c, fail("select", "%s", err.Error())
	}

	// 2. Select.
	app, f := a.selectApp(ctx, st, o)
	if f != nil {
		return c, f
	}
	fmt.Fprintf(a.stderr, "Installing: %s (%s) %s\n", clean(app.Name), clean(app.BundleID), clean(app.Version))
	steps := 4
	if o.launch {
		steps = 5
	}
	pr := newStepDisplay(a.stderr, a.stderrTTY, steps)
	defer pr.done()

	// 3. Download.
	pr.step("Downloading")
	ipa, err := c.Path("app.ipa")
	if err != nil {
		return c, fail("cache", "%s", err.Error())
	}
	if err := st.Download(store.WithProgress(ctx, pr.progress()), app, ipa, o.purchase, ""); err != nil {
		return c, classify(ctx, ctx, 0, "download", err)
	}

	// 4. Decrypt.
	pr.step("Decrypting")
	decrypted, f := a.decryptStep(ctx, c, ipa, o.keepAccountData)
	if f != nil {
		return c, f
	}

	// 5. Push.
	pr.step("Copying to VM")
	if f := a.push(ctx, m, decrypted, guest); f != nil {
		return c, f
	}

	// 6. Install.
	pr.step("Installing")
	bundleID, f := a.appsInstall(ctx, m, guest, pr)
	if f != nil {
		return c, f
	}

	// 7. Launch.
	if o.launch {
		pr.step("Launching")
		if f := a.appsLaunch(ctx, m, bundleID, pr); f != nil {
			return c, f
		}
	}

	// 8. Keep.
	if o.keep {
		path, err := a.keep(c)
		if err != nil {
			return c, fail("cache", "cannot keep %s: %s", c.Dir(), err.Error())
		}
		pr.done()
		fmt.Fprintf(a.stdout, "kept: %s\n", path)
	}
	return c, nil
}

// checkVM dials the VM (timeout <= 0 means the vm package default).
func (a *app) checkVM(name string, timeout time.Duration) (*vm.Machine, *failure) {
	m, err := a.dialVM(name, timeout)
	switch {
	case err == nil:
		return m, nil
	case errors.Is(err, vm.ErrNoSuchVM):
		return nil, fail("vm", "no such VM").hint("create it with vphone-cli vm create")
	case errors.Is(err, vm.ErrNotRunning):
		return nil, fail("vm", "VM is not running").hint("start the VM, then re-run")
	}
	return nil, fail("vm", "%s", err.Error())
}

func (a *app) selectApp(ctx context.Context, st Store, o *installOpts) (appstore.App, *failure) {
	var app appstore.App
	noMatch := false
	f := bounded(ctx, a.to.Select, "select", func(cx context.Context) error {
		if o.id != "" {
			got, err := st.Lookup(cx, o.id)
			if errors.Is(err, store.ErrAppNotFound) || (err == nil && got.BundleID != o.id) {
				noMatch = true
				return nil
			}
			app = got
			return err
		}
		res, err := st.Search(cx, o.term, searchLimit)
		if err != nil {
			return err
		}
		if len(res) == 0 {
			noMatch = true
			return nil
		}
		app = res[0]
		return nil
	})
	switch {
	case f != nil:
		return app, f
	case noMatch && o.id != "":
		return app, fail("select", "no app with bundle id %s", o.id)
	case noMatch:
		return app, fail("select", `no results for "%s"`, o.term)
	}
	return app, nil
}

func (a *app) decryptStep(ctx context.Context, c *cache.Cache, ipa string, keepAccountData bool) (string, *failure) {
	dir, err := a.modelDir()
	if err != nil {
		return "", fail("decrypt", "%s", err.Error())
	}
	ok, err := models.Exists(dir)
	if err != nil {
		return "", fail("decrypt", "%s", err.Error())
	}
	if !ok {
		return "", fail("decrypt", "no unfairplay model").hint("run vphone-apps setup")
	}
	out, _ := c.Path("app.decrypted.ipa")
	report, _ := c.Path("decrypt-report.json")
	ch := make(chan error, 1)
	go func() {
		_, err := a.decrypt(fairplay.DecryptOptions{
			InputPath:        ipa,
			OutputPath:       out + ".part",
			ReportPath:       report,
			ModelPath:        dir,
			StripAccountData: !keepAccountData,
		})
		ch <- err
	}()
	select {
	case <-ctx.Done():
		_ = os.Remove(out + ".part")
		return "", fail("decrypt", "interrupted")
	case err := <-ch:
		_ = os.Remove(report)
		if err != nil {
			_ = os.Remove(out + ".part")
			return "", fail("decrypt", "%s", err.Error())
		}
	}
	if err := c.Commit("app.decrypted.ipa"); err != nil {
		return "", fail("decrypt", "%s", err.Error())
	}
	_ = os.Remove(ipa)
	return out, nil
}

func (a *app) push(ctx context.Context, m *vm.Machine, local, guest string) *failure {
	err := m.Push(ctx, local, guest)
	if err == nil {
		return nil
	}
	return vmFailure(ctx, "push", err).hint("staged file may remain at " + guest)
}

// remoteMessage renders `<error>: <guest_error.message>` or `<error>`.
func remoteMessage(re *vm.RemoteError) string {
	if re.Guest != nil && re.Guest.Message != "" {
		return re.Msg + ": " + re.Guest.Message
	}
	return re.Msg
}

const alreadyInstalled = "already installed and icli did not install it"

func (a *app) appsInstall(ctx context.Context, m *vm.Machine, guest string, pr *stepDisplay) (string, *failure) {
	// An interrupt before apps.install is sent still leaves the staged file.
	if ctx.Err() != nil {
		return "", fail("push", "interrupted").hint("staged file may remain at " + guest)
	}
	raw, err := m.RPC(ctx, "apps.install", map[string]string{"path": guest}, a.to.Install)
	if err != nil {
		f := vmFailure(ctx, "install", err)
		if f.waited {
			f.msg += "; the install may still be running in the guest"
			f.hint("check the guest app list before re-running")
		}
		if strings.Contains(f.msg, alreadyInstalled) {
			f.hint("remove the app in the guest, then re-run")
		}
		return "", f
	}
	var res struct {
		BundleID string `json:"bundle_id"`
		Upgraded bool   `json:"upgraded"`
	}
	if err := json.Unmarshal(raw, &res); err != nil || res.BundleID == "" {
		return "", fail("install", "reply has no result.bundle_id")
	}
	pr.finish()
	fmt.Fprintf(a.stdout, "installed: %s\n", clean(res.BundleID))
	if res.Upgraded {
		fmt.Fprintln(a.stdout, "upgraded: yes")
	}
	return res.BundleID, nil
}

func (a *app) appsLaunch(ctx context.Context, m *vm.Machine, bundleID string, pr *stepDisplay) *failure {
	raw, err := m.RPC(ctx, "apps.launch", map[string]string{"bundle_id": bundleID}, a.to.Launch)
	if err != nil {
		return vmFailure(ctx, "launch", err)
	}
	var res struct {
		PID *json.Number `json:"pid"`
	}
	if err := json.Unmarshal(raw, &res); err != nil || res.PID == nil {
		return fail("launch", "reply has no result.pid")
	}
	pr.finish()
	fmt.Fprintf(a.stdout, "launched: %s pid %s\n", clean(bundleID), clean(res.PID.String()))
	return nil
}
