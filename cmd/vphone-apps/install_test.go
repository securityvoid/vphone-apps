package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DrWhax/unfairplay/pkg/fairplay"
	"github.com/majd/ipatool/v2/pkg/appstore"
	"github.com/securityvoid/vphone-apps/internal/cache"
	"github.com/securityvoid/vphone-apps/internal/vm"
)

const guestPath = "/private/var/tmp/vphone-apps-0123456789abcdef.ipa"
const stagedHint = "hint: staged file may remain at " + guestPath + "\n"
const checkHint = "hint: check the guest app list before re-running\n"

func TestInstallSuccess(t *testing.T) {
	e := newEnv(t, happy)
	wantExit(t, e.run("install", "Foo", "--vm", "v1"), 0, e)
	want := "installed: com.example.app\n"
	wantSteps := "Installing: Foo (com.example.app) 1.2.3\n[1/4] Downloading\n[2/4] Decrypting\n[3/4] Copying to VM\n[4/4] Installing\n"
	if e.out.String() != want || e.errb.String() != wantSteps {
		t.Fatalf("out=%q err=%q", e.out, e.errb)
	}
	if got := e.cacheEntries(t); len(got) != 0 {
		t.Fatalf("cache not empty: %v", got)
	}
	if e.steps[0] != "vm" {
		t.Fatal("VM check must come first", e.steps)
	}
	if e.as.callList() != "search,download,sinf" {
		t.Fatal(e.as.callList())
	}
	reqs := e.srv.requests()
	if len(reqs) != 2 || !strings.HasPrefix(reqs[0], `{"t":"push","local":"`) || !strings.Contains(reqs[0], `"path":"`+guestPath+`"`) ||
		reqs[1] != `{"t":"rpc","method":"apps.install","params":{"path":"`+guestPath+`"}}`+"\n" {
		t.Fatalf("%q", reqs)
	}
}

func TestInstallUpgradedAndLaunch(t *testing.T) {
	e := newEnv(t, func(r string) string {
		if strings.Contains(r, "apps.install") {
			return `{"ok":true,"result":{"bundle_id":"com.example.app","upgraded":true}}` + "\n"
		}
		return happy(r)
	})
	wantExit(t, e.run("install", "Foo", "--vm", "v1", "--launch"), 0, e)
	want := "installed: com.example.app\nupgraded: yes\nlaunched: com.example.app pid 4242\n"
	if e.out.String() != want {
		t.Fatalf("%q", e.out)
	}
	reqs := e.srv.requests()
	if reqs[2] != `{"t":"rpc","method":"apps.launch","params":{"bundle_id":"com.example.app"}}`+"\n" {
		t.Fatal(reqs[2])
	}
}

func TestLaunchFailures(t *testing.T) {
	for name, reply := range map[string]string{
		"error":    `{"ok":false,"error":"nope","guest_error":{"message":"cannot"}}` + "\n",
		"garbage":  "garbage\n",
		"eof":      closeConn,
		"nopid":    `{"ok":true,"result":{}}` + "\n",
		"noresult": `{"ok":true}` + "\n",
	} {
		e := newEnv(t, func(r string) string {
			if strings.Contains(r, "apps.launch") {
				return reply
			}
			return happy(r)
		})
		wantExit(t, e.run("install", "Foo", "--vm", "v1", "--launch"), 1, e)
		if !strings.Contains(e.out.String(), "installed: com.example.app") || strings.Contains(e.out.String(), "launched:") {
			t.Errorf("%s: out %q", name, e.out)
		}
		if l := e.errLines(); len(l) != 1 || !strings.HasPrefix(l[0], "error: launch: ") {
			t.Errorf("%s: %q", name, e.errb)
		}
	}
}

func TestSelect(t *testing.T) {
	e := newEnv(t, happy)
	wantExit(t, e.run("install", "--id", "com.example.app", "--vm", "v1"), 0, e)
	if !strings.HasPrefix(e.errb.String(), "Installing: Foo (com.example.app) 1.2.3\n") || !strings.Contains(e.as.callList(), "lookup") {
		t.Fatal(e.out, e.as.callList())
	}

	e = newEnv(t, happy)
	e.as.lookupErr = appstore.ErrAppNotFound
	wantExit(t, e.run("install", "--id", "com.example.none", "--vm", "v1"), 1, e)
	wantErr(t, e, "error: select: no app with bundle id com.example.none\n")

	e = newEnv(t, happy)
	e.as.lookupApp = appstore.App{BundleID: "com.example.other"}
	wantExit(t, e.run("install", "--id", "com.example.app", "--vm", "v1"), 1, e)
	wantErr(t, e, "error: select: no app with bundle id com.example.app\n")

	e = newEnv(t, happy)
	e.as.searchRes = nil
	wantExit(t, e.run("install", "nothing", "--vm", "v1"), 1, e)
	wantErr(t, e, "error: select: no results for \"nothing\"\n")
	if strings.Contains(e.as.callList(), "download") {
		t.Fatal("download after failed select")
	}
}

func TestVMChecks(t *testing.T) {
	e := newEnv(t, happy)
	wantExit(t, e.run("install", "Foo", "--vm", "nope"), 1, e)
	wantErr(t, e, "error: vm: no such VM\nhint: create it with vphone-cli vm create\n")
	if e.as.callList() != "" {
		t.Fatal("network before VM check")
	}
	for _, n := range []string{"", ".", "a/b", "../x", strings.Repeat("a", 65)} {
		e = newEnv(t, happy)
		wantExit(t, e.run("install", "Foo", "--vm", n), 1, e)
		wantErr(t, e, "error: vm: invalid VM name\n")
	}
	// Refused connection.
	e = newEnv(t, happy)
	e.a.dialVM = func(string, time.Duration) (*vm.Machine, error) { return nil, vm.ErrNotRunning }
	wantExit(t, e.run("install", "Foo", "--vm", "v1"), 1, e)
	wantErr(t, e, "error: vm: VM is not running\nhint: start the VM, then re-run\n")
	e = newEnv(t, happy)
	e.a.dialVM = func(string, time.Duration) (*vm.Machine, error) { return nil, vm.ErrDialTimeout }
	wantExit(t, e.run("install", "Foo", "--vm", "v1"), 1, e)
	wantErr(t, e, "error: vm: timed out connecting\n")
}

func TestGuestPathFailure(t *testing.T) {
	e := newEnv(t, happy)
	e.a.guestPath = func() (string, error) { return "", errors.New("rand broke") }
	wantExit(t, e.run("install", "Foo", "--vm", "v1"), 1, e)
	wantErr(t, e, "error: push: rand broke\n")
	if len(e.steps) != 0 || len(e.srv.requests()) != 0 {
		t.Fatal("connection made")
	}
}

func TestDecryptMissingModel(t *testing.T) {
	e := newEnv(t, happy)
	e.models = t.TempDir()
	wantExit(t, e.run("install", "Foo", "--vm", "v1"), 1, e)
	wantStepErr(t, e, 2, "error: decrypt: no unfairplay model\nhint: run vphone-apps setup\n")
	if got := e.cacheEntries(t); len(got) != 0 {
		t.Fatal(got)
	}
}

func TestDecryptOptionsAndFailures(t *testing.T) {
	e := newEnv(t, happy)
	var got fairplay.DecryptOptions
	e.decryptFn = func(o fairplay.DecryptOptions) (fairplay.DecryptResult, error) {
		got = o
		os.WriteFile(o.OutputPath, []byte("partial"), 0o644)
		return fairplay.DecryptResult{}, errors.New("decrypt broke")
	}
	wantExit(t, e.run("install", "Foo", "--vm", "v1", "--keep"), 1, e)
	wantStepErr(t, e, 2, "error: decrypt: decrypt broke\n")
	if !got.StripAccountData || got.ModelPath != e.models || !strings.HasSuffix(got.OutputPath, "app.decrypted.ipa.part") {
		t.Fatalf("%+v", got)
	}
	if es := e.cacheEntries(t); len(es) != 0 {
		t.Fatalf("leftovers %v (no kept dir on failure)", es)
	}

	e = newEnv(t, happy)
	e.decryptFn = func(o fairplay.DecryptOptions) (fairplay.DecryptResult, error) {
		got = o
		os.WriteFile(o.OutputPath, nil, 0o644)
		return fairplay.DecryptResult{}, nil
	}
	wantExit(t, e.run("install", "Foo", "--vm", "v1", "--keep-account-data"), 0, e)
	if got.StripAccountData {
		t.Fatal("--keep-account-data must not strip")
	}
}

func TestDecryptCancelledMidWrite(t *testing.T) {
	e := newEnv(t, happy)
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	release := make(chan struct{})
	defer close(release)
	e.decryptFn = func(o fairplay.DecryptOptions) (fairplay.DecryptResult, error) {
		os.WriteFile(o.OutputPath, []byte("half"), 0o644)
		close(started)
		<-release
		return fairplay.DecryptResult{}, nil
	}
	go func() { <-started; cancel() }()
	t0 := time.Now()
	wantExit(t, e.runCtx(ctx, "install", "Foo", "--vm", "v1"), 1, e)
	wantStepErr(t, e, 2, "error: decrypt: interrupted\n")
	if time.Since(t0) > 2*time.Second || len(e.cacheEntries(t)) != 0 {
		t.Fatal("slow or leftovers", e.cacheEntries(t))
	}
}

func TestAppsInstallFailures(t *testing.T) {
	cases := []struct {
		name, reply, want string
	}{
		{"x", `{"ok":false,"error":"x"}` + "\n", "error: install: x\n"},
		{"guest", `{"ok":false,"error":"boom","guest_error":{"code":1,"message":"detail"}}` + "\n", "error: install: boom: detail\n"},
		{"already", `{"ok":false,"error":"already installed and icli did not install it","guest_error":{"message":"m"}}` + "\n",
			"error: install: already installed and icli did not install it: m\nhint: remove the app in the guest, then re-run\n"},
		{"garbage", "garbage\n", ""},
		{"empty", "\n", ""},
		{"eof", closeConn, ""},
		{"noresult", `{"ok":true,"result":{}}` + "\n", ""},
		{"noresult2", `{"ok":true}` + "\n", ""},
	}
	for _, c := range cases {
		e := newEnv(t, func(r string) string {
			if strings.Contains(r, "apps.install") {
				return c.reply
			}
			return happy(r)
		})
		wantExit(t, e.run("install", "Foo", "--vm", "v1"), 1, e)
		if c.want != "" {
			wantStepErr(t, e, 4, c.want)
		} else if l := e.errLines(); len(l) != 1 || !strings.HasPrefix(l[0], "error: install: ") {
			t.Errorf("%s: %q", c.name, e.errb)
		}
		if strings.Contains(e.out.String(), "installed:") {
			t.Errorf("%s: printed installed", c.name)
		}
	}
}

func TestAppsInstallTimeout(t *testing.T) {
	e := newEnv(t, func(r string) string {
		if strings.Contains(r, "apps.install") {
			return ""
		}
		return happy(r)
	})
	e.a.to.Install = 50 * time.Millisecond
	wantExit(t, e.run("install", "Foo", "--vm", "v1"), 1, e)
	wantStepErr(t, e, 4, "error: install: timed out after 50ms; the install may still be running in the guest\n"+checkHint)
	if defaultTimeouts().Install != 200*time.Second {
		t.Fatal("default deadline must print 200s")
	}
}

func TestPushFailures(t *testing.T) {
	for name, reply := range map[string]string{"garbage": "garbage\n", "eof": closeConn} {
		e := newEnv(t, func(r string) string {
			if strings.Contains(r, `"t":"push"`) {
				return reply
			}
			return happy(r)
		})
		wantExit(t, e.run("install", "Foo", "--vm", "v1"), 1, e)
		if l := e.errLines(); len(l) != 1 || !strings.HasPrefix(l[0], "error: push: ") || !strings.HasSuffix(e.errb.String(), stagedHint) {
			t.Errorf("%s: %q", name, e.errb)
		}
	}
	e := newEnv(t, func(r string) string {
		if strings.Contains(r, `"t":"push"`) {
			return `{"ok":false,"error":"disk full"}` + "\n"
		}
		return happy(r)
	})
	wantExit(t, e.run("install", "Foo", "--vm", "v1"), 1, e)
	wantStepErr(t, e, 3, "error: push: disk full\n"+stagedHint)
}

func TestDownloadFailureNotRetried(t *testing.T) {
	e := newEnv(t, happy)
	e.as.downloadFn = func(appstore.DownloadInput) error { return errors.New("network down") }
	wantExit(t, e.run("install", "Foo", "--vm", "v1"), 1, e)
	wantStepErr(t, e, 1, "error: download: network down\n")
	if n := strings.Count(e.as.callList(), "download"); n != 1 {
		t.Fatalf("download called %d times", n)
	}
	if len(e.srv.requests()) != 0 {
		t.Fatal("pushed after failed download")
	}
}

func TestUnownedApp(t *testing.T) {
	e := newEnv(t, happy)
	e.as.downloadFn = func(appstore.DownloadInput) error { return appstore.ErrLicenseRequired }
	wantExit(t, e.run("install", "Foo", "--vm", "v1"), 1, e)
	wantStepErr(t, e, 1, "error: download: app not owned\nhint: re-run with --purchase\n")
}

func TestPurchaseFlow(t *testing.T) {
	e := newEnv(t, happy)
	e.as.purchase = func() error { return appstore.ErrLicenseAlreadyExists }
	wantExit(t, e.run("install", "Foo", "--vm", "v1", "--purchase"), 0, e)
	if e.as.callList() != "search,purchase,download,sinf" {
		t.Fatal(e.as.callList())
	}
	e = newEnv(t, happy)
	e.as.purchase = func() error { return errors.New("card declined") }
	wantExit(t, e.run("install", "Foo", "--vm", "v1", "--purchase"), 1, e)
	wantStepErr(t, e, 1, "error: download: card declined\n")
}

func TestKeep(t *testing.T) {
	e := newEnv(t, happy)
	wantExit(t, e.run("install", "Foo", "--vm", "v1", "--keep", "--launch"), 0, e)
	lines := strings.Split(strings.TrimSpace(e.out.String()), "\n")
	last := lines[len(lines)-1]
	if !strings.HasPrefix(last, "kept: ") {
		t.Fatalf("%q", e.out)
	}
	path := strings.TrimPrefix(last, "kept: ")
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 || filepath.Base(path) != "app.decrypted.ipa" {
		t.Fatal(path, err)
	}
	es, _ := os.ReadDir(filepath.Dir(path))
	if len(es) != 1 || !strings.HasPrefix(filepath.Base(filepath.Dir(path)), "kept-") {
		t.Fatalf("%v", es)
	}
	if got := e.cacheEntries(t); len(got) != 1 {
		t.Fatalf("cache %v", got)
	}

	e = newEnv(t, happy)
	wantExit(t, e.run("install", "Foo", "--vm", "v1"), 0, e)
	if strings.Contains(e.out.String(), "kept:") {
		t.Fatal("kept without --keep")
	}
}

func TestKeepWithPushFailureKeepsNothing(t *testing.T) {
	e := newEnv(t, func(r string) string {
		if strings.Contains(r, `"t":"push"`) {
			return `{"ok":false,"error":"nope"}` + "\n"
		}
		return happy(r)
	})
	wantExit(t, e.run("install", "Foo", "--vm", "v1", "--keep"), 1, e)
	wantStepErr(t, e, 3, "error: push: nope\n"+stagedHint)
	if strings.Contains(e.out.String(), "kept:") || len(e.cacheEntries(t)) != 0 {
		t.Fatal(e.out, e.cacheEntries(t))
	}
}

func TestKeepFailure(t *testing.T) {
	e := newEnv(t, happy)
	e.a.keep = func(c *cache.Cache) (string, error) { return "", errors.New("rename failed") }
	wantExit(t, e.run("install", "Foo", "--vm", "v1", "--keep"), 1, e)
	if l := e.errLines(); len(l) != 1 || !strings.HasPrefix(l[0], "error: cache: cannot keep ") || !strings.HasSuffix(l[0], ": rename failed") {
		t.Fatalf("%q", e.errb)
	}
	if strings.Contains(e.out.String(), "kept:") || len(e.cacheEntries(t)) != 0 {
		t.Fatal(e.out, e.cacheEntries(t))
	}
}

func TestCleanupFailure(t *testing.T) {
	e := newEnv(t, happy)
	e.decryptFn = func(o fairplay.DecryptOptions) (fairplay.DecryptResult, error) {
		// Make the run directory undeletable: a read-only dir holding a file.
		dir := filepath.Dir(o.OutputPath)
		sub := filepath.Join(dir, "locked")
		os.Mkdir(sub, 0o700)
		os.WriteFile(filepath.Join(sub, "f"), nil, 0o600)
		os.Chmod(sub, 0o500)
		t.Cleanup(func() { os.Chmod(sub, 0o700) })
		os.WriteFile(o.OutputPath, []byte("d"), 0o644)
		return fairplay.DecryptResult{}, os.WriteFile(o.ReportPath, nil, 0o600)
	}
	wantExit(t, e.run("install", "Foo", "--vm", "v1"), 1, e)
	l := e.errLines()
	if len(l) != 1 || !strings.HasPrefix(l[0], "error: cleanup: ") || !strings.Contains(e.errb.String(), "hint: delete ") {
		t.Fatalf("%q", e.errb)
	}
}

func TestCacheSymlinkRefused(t *testing.T) {
	e := newEnv(t, happy)
	real := t.TempDir()
	os.Symlink(real, e.cache)
	wantExit(t, e.run("install", "Foo", "--vm", "v1"), 1, e)
	wantErr(t, e, "error: cache: "+e.cache+" is a symlink or not owned by you\nhint: remove it and re-run\n")
}

func TestEvilBundleIDWritesNothingOutside(t *testing.T) {
	e := newEnv(t, happy)
	e.as.searchRes = []appstore.App{{Name: "x", BundleID: "../../evil", Version: "1"}}
	parent := filepath.Dir(e.cache)
	wantExit(t, e.run("install", "x", "--vm", "v1"), 0, e)
	if _, err := os.Stat(filepath.Join(parent, "evil")); err == nil {
		t.Fatal("file outside run dir")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(parent), "evil")); err == nil {
		t.Fatal("file outside run dir")
	}
}

func TestSanitizedGuestError(t *testing.T) {
	evil := `bad\u001b[2J\nhint: x`
	for _, withLaunch := range []bool{false, true} {
		e := newEnv(t, func(r string) string {
			if strings.Contains(r, "apps.install") && !withLaunch {
				return `{"ok":false,"error":"` + evil + `"}` + "\n"
			}
			if strings.Contains(r, "apps.launch") {
				return `{"ok":false,"error":"` + evil + `"}` + "\n"
			}
			return happy(r)
		})
		args := []string{"install", "Foo", "--vm", "v1"}
		if withLaunch {
			args = append(args, "--launch")
		}
		wantExit(t, e.run(args...), 1, e)
		for _, b := range []byte(e.errb.String()) {
			if (b < 0x20 && b != '\n') || b == 0x7f {
				t.Fatalf("control byte in %q", e.errb)
			}
		}
		if l := e.errLines(); len(l) != 1 || strings.Contains(e.errb.String(), "\nhint: x") {
			t.Fatalf("%q", e.errb)
		}
	}
}

// Per-step timeouts.
func TestStepTimeouts(t *testing.T) {
	block := func(t *testing.T) (func(), chan struct{}) {
		ch := make(chan struct{})
		return func() { close(ch) }, ch
	}
	d := 50 * time.Millisecond
	t.Run("search", func(t *testing.T) {
		e := newEnv(t, happy)
		stop, ch := block(t)
		defer stop()
		e.as.searchRes = nil
		e.a.to.Search = d
		e.as.acctErr = nil
		e.a.newStore = blockingStore(e, ch)
		wantExit(t, e.run("search", "x"), 1, e)
		wantErr(t, e, "error: search: timed out after 50ms\n")
	})
	t.Run("select term", func(t *testing.T) {
		e := newEnv(t, happy)
		stop, ch := block(t)
		defer stop()
		e.a.to.Select = d
		e.a.newStore = blockingStore(e, ch)
		wantExit(t, e.run("install", "x", "--vm", "v1"), 1, e)
		wantErr(t, e, "error: select: timed out after 50ms\n")
	})
	t.Run("select id", func(t *testing.T) {
		e := newEnv(t, happy)
		stop, ch := block(t)
		defer stop()
		e.a.to.Select = d
		e.a.newStore = blockingStore(e, ch)
		wantExit(t, e.run("install", "--id", "x", "--vm", "v1"), 1, e)
		wantErr(t, e, "error: select: timed out after 50ms\n")
	})
	t.Run("purchase", func(t *testing.T) {
		e := newEnv(t, happy)
		stop, ch := block(t)
		defer stop()
		e.purchaseTO = d
		e.as.purchase = func() error { <-ch; return nil }
		wantExit(t, e.run("install", "x", "--vm", "v1", "--purchase"), 1, e)
		wantStepErr(t, e, 1, "error: download: timed out after 50ms\n")
	})
	t.Run("download", func(t *testing.T) {
		e := newEnv(t, happy)
		stop, ch := block(t)
		defer stop()
		e.downloadTO = d
		e.as.downloadFn = func(appstore.DownloadInput) error { <-ch; return nil }
		wantExit(t, e.run("install", "x", "--vm", "v1"), 1, e)
		wantStepErr(t, e, 1, "error: download: timed out after 50ms\n")
	})
	t.Run("push", func(t *testing.T) {
		e := newEnv(t, func(r string) string {
			if strings.Contains(r, `"t":"push"`) {
				return ""
			}
			return happy(r)
		})
		dial := e.a.dialVM
		e.a.dialVM = func(n string, to time.Duration) (*vm.Machine, error) {
			m, err := dial(n, to)
			if m != nil {
				m.PushDeadline = d
			}
			return m, err
		}
		wantExit(t, e.run("install", "x", "--vm", "v1"), 1, e)
		wantStepErr(t, e, 3, "error: push: timed out after 50ms\n"+stagedHint)
	})
	t.Run("launch", func(t *testing.T) {
		e := newEnv(t, func(r string) string {
			if strings.Contains(r, "apps.launch") {
				return ""
			}
			return happy(r)
		})
		e.a.to.Launch = d
		wantExit(t, e.run("install", "x", "--vm", "v1", "--launch"), 1, e)
		wantStepErr(t, e, 5, "error: launch: timed out after 50ms\n")
	})
}

type blockAS struct {
	appstore.AppStore
	ch chan struct{}
}

func (b *blockAS) AccountInfo() (appstore.AccountInfoOutput, error) {
	return appstore.AccountInfoOutput{}, nil
}
func (b *blockAS) Search(appstore.SearchInput) (appstore.SearchOutput, error) {
	<-b.ch
	return appstore.SearchOutput{}, nil
}
func (b *blockAS) Lookup(appstore.LookupInput) (appstore.LookupOutput, error) {
	<-b.ch
	return appstore.LookupOutput{}, nil
}

func blockingStore(e *testEnv, ch chan struct{}) func() (Store, error) {
	return func() (Store, error) { return storeOf(&blockAS{ch: ch}), nil }
}

var _ = fmt.Sprint

func TestReplyTooLargeThroughInstall(t *testing.T) {
	big := strings.Repeat("a", 1<<20+1) + "\n"
	e := newEnv(t, func(r string) string {
		if strings.Contains(r, `"t":"push"`) {
			return big
		}
		return happy(r)
	})
	wantExit(t, e.run("install", "Foo", "--vm", "v1"), 1, e)
	wantStepErr(t, e, 3, "error: push: reply too large\n"+stagedHint)

	e = newEnv(t, func(r string) string {
		if strings.Contains(r, "apps.install") {
			return big
		}
		return happy(r)
	})
	wantExit(t, e.run("install", "Foo", "--vm", "v1"), 1, e)
	wantStepErr(t, e, 4, "error: install: reply too large\n")
}

func TestUnresolvableSudoUser(t *testing.T) {
	e := newEnv(t, happy)
	e.a.modelDir = func() (string, error) { return "", errors.New("cannot resolve SUDO_USER bob") }
	wantExit(t, e.run("install", "Foo", "--vm", "v1"), 1, e)
	wantStepErr(t, e, 2, "error: decrypt: cannot resolve SUDO_USER bob\n")
}
