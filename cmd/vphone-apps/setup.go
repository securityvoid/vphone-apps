package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"regexp"
	"strings"
	"syscall"

	"github.com/DrWhax/unfairplay/pkg/setup"
	"github.com/securityvoid/vphone-apps/internal/notice"
	"github.com/securityvoid/vphone-apps/internal/store"
)

const setupUsage = `usage: vphone-apps setup [--app <path.app> --ipa <path.ipa>] [--interface <name>] [--keep-work] [--verbose]
With no --app and --ipa, setup installs Organic Maps if needed, downloads its IPA and enrolls automatically.
--app and --ipa must be given together or not at all.
Exit codes: 0 success, 1 failure, 2 usage error.
`

const (
	sudoHint = "sudo needs an administrator account and its password; fix that, then re-run vphone-apps setup"
	// elevatedExit is the exit code of a root `setup --elevated` that printed its own error.
	elevatedExit = 3
)

var ifaceRE = regexp.MustCompile(`^[a-z][a-z0-9]{0,15}$`)

// errChildReported means the elevated child exited 3: it printed its own error.
var errChildReported = errors.New("elevated setup reported its own error")

// elevError is a failure to run the elevated child; hint is the sudo hint.
type elevError struct {
	msg, hint string
	cannot    bool // the child never started
}

func (e *elevError) Error() string { return e.msg }

// runElevated runs `sudo <self> setup <args> --elevated` and maps its exit
// code per the setup design: nil for 0, errChildReported for 3, else an
// *elevError. args has no leading "setup".
func (a *app) runElevated(ctx context.Context, args []string) error {
	cannot := func(err error) error { return &elevError{msg: "cannot elevate: " + err.Error(), cannot: true} }
	self, err := a.executable()
	if err != nil {
		return cannot(err)
	}
	argv := append(append([]string{"-p", sudoPrompt, self, "setup"}, args...), "--elevated")
	code, err := a.proc.Run(ctx, sudoPath, argv, true)
	switch {
	case err == nil && code == 0:
		return nil
	case err == nil && code == elevatedExit:
		return errChildReported
	case ctx.Err() != nil:
		return &elevError{msg: "interrupted"}
	case err != nil:
		return cannot(err)
	case code < 0:
		return &elevError{msg: "sudo failed (killed by signal)", hint: sudoHint}
	}
	return &elevError{msg: fmt.Sprintf("sudo failed (exit %d)", code), hint: sudoHint}
}

type setupOpts struct {
	app, ipa, iface              string
	keepWork, removeApp, verbose bool
	elevated                     bool
}

func (a *app) setup(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("setup", flag.ContinueOnError)
	var o setupOpts
	fs.StringVar(&o.app, "app", "", "")
	fs.StringVar(&o.ipa, "ipa", "", "")
	fs.StringVar(&o.iface, "interface", "en0", "")
	fs.BoolVar(&o.keepWork, "keep-work", false, "")
	fs.BoolVar(&o.verbose, "verbose", false, "")
	fs.BoolVar(&o.removeApp, "remove-app", false, "")
	fs.BoolVar(&o.elevated, "elevated", false, "")
	words, code, done := a.parse(fs, setupUsage, args)
	if done {
		return code
	}
	switch {
	case !ifaceRE.MatchString(o.iface):
		return a.usageError(setupUsage, "--interface must match [a-z][a-z0-9]{0,15}")
	case (o.app == "") != (o.ipa == ""):
		return a.usageError(setupUsage, "--app and --ipa must be given together")
	case o.removeApp && (o.app != organicMapsPath || o.ipa == ""):
		return a.usageError(setupUsage, `--remove-app needs --app "`+organicMapsPath+`" and --ipa`)
	case len(words) > 0:
		return a.usageError(setupUsage, "unexpected argument "+clean(words[0]))
	}
	ctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if o.app == "" {
		return a.setupAuto(ctx, &o)
	}
	for _, c := range []struct {
		path, ext string
		dir       bool
	}{{o.app, ".app", true}, {o.ipa, ".ipa", false}} {
		fi, err := a.stat(c.path)
		if err != nil {
			return a.setupFail(&o, fail("setup", "%s not found", c.path))
		}
		if fi.IsDir() != c.dir || (!c.dir && !fi.Mode().IsRegular()) || !strings.HasSuffix(c.path, c.ext) {
			return a.setupFail(&o, fail("setup", "%s is not a valid app/ipa", c.path))
		}
	}
	if !o.elevated && o.verbose {
		fmt.Fprintln(a.stderr, notice.Prerequisites)
	}
	var rc int
	if a.geteuid() != 0 {
		rc = a.setupElevate(ctx, args)
	} else {
		rc = a.setupRoot(ctx, &o)
	}
	if rc == 0 && !o.elevated {
		a.complete()
	}
	return rc
}

// complete shows the completion box (success only, from the invoking process).
func (a *app) complete() {
	fmt.Fprint(a.stderr, notice.Box(notice.SetupDone))
}

// setupFail reports f; the exit code is 3 for a root child, else 1.
func (a *app) setupFail(o *setupOpts, f *failure) int {
	a.report(f)
	return a.exitCode(o)
}

func (a *app) exitCode(o *setupOpts) int {
	if o.elevated {
		return elevatedExit
	}
	return 1
}

// setupElevate runs the manual form through sudo.
func (a *app) setupElevate(ctx context.Context, args []string) int {
	err := a.runElevated(ctx, args)
	var ee *elevError
	switch {
	case err == nil:
		return 0
	case errors.As(err, &ee):
		f := fail("setup", "%s", ee.msg)
		if ee.hint != "" {
			f.hint(ee.hint)
		} else if ee.cannot {
			f.hint("re-run as root")
		}
		return a.report(f)
	}
	return 1 // errChildReported
}

// setupAuto is `setup` with neither --app nor --ipa.
func (a *app) setupAuto(ctx context.Context, o *setupOpts) int {
	if a.geteuid() == 0 { // before any store access
		a.reportSetup(newSetupError("", "running as root; run without sudo"), false)
		return 1
	}
	st, err := a.newStore()
	if err == nil {
		_, err = st.Account()
	}
	var e *setupError
	switch {
	case errors.Is(err, store.ErrNotLoggedIn):
		e = newSetupError(loginHint, "not logged in")
	case err != nil:
		e = newSetupError("", "%s", err.Error())
	default:
		e = a.autoSetup(ctx, st, o.iface, o.keepWork, o.verbose)
	}
	if e == nil {
		return 0
	}
	a.reportSetup(e, false)
	return 1
}

// setupRoot enrolls as root: setup.Run, optional removal of the app, the
// model path on stdout only with --verbose and only when everything succeeded.
func (a *app) setupRoot(ctx context.Context, o *setupOpts) int {
	dir, err := a.modelDir()
	if err != nil {
		return a.setupFail(o, fail("setup", "%s", err.Error()))
	}
	res, runErr := a.runSetup(ctx, setup.Options{
		AppPath: o.app, IPAPath: o.ipa, ModelDirectory: dir,
		HardwareInterface: o.iface, KeepWork: o.keepWork,
		Progress: func(s string) {
			if o.verbose {
				fmt.Fprintln(a.stderr, clean(s))
			}
		},
	})
	var rmErr error
	if o.removeApp { // a signal never aborts the removal
		fmt.Fprintln(a.stderr, notice.SetupRemoving)
		rmErr = a.removeApp(o.app)
	}
	switch {
	case ctx.Err() != nil:
		a.report(fail("setup", "interrupted"))
	case runErr != nil:
		a.report(fail("setup", "%s", runErr.Error()))
	}
	if rmErr != nil {
		fmt.Fprintf(a.stderr, "error: cleanup: %s\n", clean(rmErr.Error()))
	}
	if ctx.Err() != nil || runErr != nil || rmErr != nil {
		return a.exitCode(o)
	}
	if o.verbose {
		fmt.Fprintln(a.stdout, res.ModelPath)
	}
	return 0
}

// removeApp deletes the Organic Maps bundle after verifyOrganicMaps; the
// error text is what follows `error: cleanup: `.
func removeApp(path string) error {
	if _, err := os.Lstat(path); err != nil {
		return fmt.Errorf("cannot remove %s: %w", path, err)
	}
	if err := verifyOrganicMaps(path); err != nil {
		return fmt.Errorf("refusing to remove %s: %v", path, err)
	}
	if err := os.RemoveAll(path); err != nil {
		return fmt.Errorf("cannot remove %s: %w", path, err)
	}
	return nil
}
