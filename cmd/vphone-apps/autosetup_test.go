package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/majd/ipatool/v2/pkg/appstore"
	"github.com/securityvoid/vphone-apps/internal/cache"
	"github.com/securityvoid/vphone-apps/internal/notice"
)

const (
	listingURL   = "macappstore://apps.apple.com/app/id6666"
	hintNotOM    = "hint: delete or move " + omPath + " if it is a leftover or another app, then re-run vphone-apps setup\n"
	hintListing  = "hint: the App Store listing changed; run vphone-apps setup with --app and --ipa\n"
	hintOpen     = "hint: open the Mac App Store, install Organic Maps, then re-run vphone-apps setup\n"
	hintClick    = "hint: click Get/Install in the App Store window, then re-run vphone-apps setup\n"
	hintLogin    = "hint: run vphone-apps login\n"
	hintRunSetup = "hint: run vphone-apps setup\n"
	hintDelete   = "hint: if " + omPath + " is present, delete it manually (a re-run keeps an installed Organic Maps)\n"
)

// autoEnv is a non-root environment with a signed-in session, no model and
// fakes for every program, clock and path.
type autoEnv struct {
	*testEnv
	waits     int
	appearAt  int // waits before a missing app appears; 0 = never
	blockWait bool
	onWait    func()
	sudoFn    func(c runCall) (int, error) // nil: exit 0 and print the model path
	openFn    func(ctx context.Context) (int, error)
	ipaAtSudo bool
	ctx       context.Context
	cancel    context.CancelFunc
}

func newAutoEnv(t *testing.T, exists bool) *autoEnv {
	e := &autoEnv{testEnv: newEnv(t, happy)}
	os.Remove(filepath.Join(e.models, "m.bin"))
	e.ctx, e.cancel = context.WithCancel(context.Background())
	t.Cleanup(e.cancel)
	e.as.lookupApp = appstore.App{ID: 6666, Name: "Organic Maps", BundleID: "app.organicmaps"}
	e.exists[omPath] = exists
	e.a.geteuid = func() int { return 501 }
	e.a.pathExists = func(p string) bool {
		return p == omPath && (e.exists[omPath] || (e.appearAt > 0 && e.waits >= e.appearAt))
	}
	e.a.after = func(time.Duration) <-chan time.Time {
		e.waits++
		if e.onWait != nil {
			e.onWait()
		}
		c := make(chan time.Time, 1)
		if !e.blockWait {
			c <- time.Time{}
		}
		return c
	}
	e.run_.fn = func(ctx context.Context, c runCall) (int, error) {
		if c.path == openPath {
			if e.openFn != nil {
				return e.openFn(ctx)
			}
			return 0, nil
		}
		for i, a := range c.args {
			if a == "--ipa" {
				_, err := os.Stat(c.args[i+1])
				e.ipaAtSudo = err == nil
			}
		}
		if e.sudoFn != nil {
			return e.sudoFn(c)
		}
		e.childOut(c)
		return 0, nil
	}
	return e
}

// doneBox is the exact completion box of design § Output lines.
const doneBox = `+--------------------------------------------------------------------------+
| Setup is complete. Please remember to only use vphone-apps for legal and |
| lawful purposes allowed by the laws of your locality. Consult your       |
| attorney if you have any questions on what this does or does not         |
| include.                                                                 |
+--------------------------------------------------------------------------+
`

// childOut mimics the root child: the model path on stdout only with --verbose.
func (e *autoEnv) childOut(c runCall) {
	for _, a := range c.args {
		if a == "--verbose" {
			fmt.Fprintln(e.out, "/m/x.bin")
		}
	}
}

func (e *autoEnv) cancelSoon() { time.AfterFunc(50*time.Millisecond, e.cancel) }

func (e *autoEnv) runCmd(cmd string) int { return e.runCtx(e.ctx, cmd) }

// progress is the stderr up to and including the named stage.
func progress(exists bool, upto string) string {
	return progressV(exists, upto, false)
}

// progressV is progress with the verbose-only lines when verbose.
func progressV(exists bool, upto string, verbose bool) string {
	var l []string
	if verbose {
		kept := notice.SetupNew
		if exists {
			kept = notice.SetupKept
		}
		l = []string{notice.SetupIntro, kept, notice.Prerequisites}
	}
	names := []string{"Installing Organic Maps", "Waiting for the App Store: click Get/Install (up to 10 min)", "Downloading installer", "Enrolling with Unfairplay"}
	if exists {
		names = []string{"Checking Organic Maps", "Downloading installer", "Enrolling with Unfairplay"}
	}
	shown := map[string]int{"intro": 1, "opening": 1, "using": len(names) - 2, "waiting": len(names) - 2, "app": len(names) - 2,
		"download": len(names) - 1, "enroll": len(names)}[upto]
	for i, n := range names[:shown] {
		l = append(l, fmt.Sprintf("[%d/%d] %s", i+1, len(names), n))
	}
	return strings.Join(l, "\n") + "\n"
}

func blockUntilCleanup(t *testing.T) chan struct{} {
	ch := make(chan struct{})
	t.Cleanup(func() { close(ch) })
	return ch
}

func sudoReturns(code int, err error) func(c runCall) (int, error) {
	return func(c runCall) (int, error) { return code, err }
}

type failCase struct {
	name       string
	exists     bool
	appearAt   int
	stage      string
	line, hint string // line without trailing newline; %CACHE% = the cache directory
	prep       func(t *testing.T, e *autoEnv)
	waits      int // expected waits (checked when > 0)
	keepCache  bool
}

func TestAutoSetupFailures(t *testing.T) {
	notOM := "error: setup: " + omPath + " is not Organic Maps"
	verifyFails := func(t *testing.T, e *autoEnv) { e.a.verifyApp = func(string) error { return errors.New("bad") } }
	listing := "error: setup: unexpected App Store listing for app.organicmaps"
	expired := "error: setup: session expired"
	sudoFailed := func(n string) string { return "error: setup: sudo failed (" + n + ")" }
	sudoHint := "hint: sudo needs an administrator account and its password; fix that, then re-run vphone-apps setup\n"
	downloadFn := func(f func(in appstore.DownloadInput) error) func(*testing.T, *autoEnv) {
		return func(t *testing.T, e *autoEnv) { e.as.downloadFn = f }
	}
	blockDownload := func(in appstore.DownloadInput) error { <-in.Context.Done(); return in.Context.Err() }

	cases := []failCase{
		{name: "kept not OM", exists: true, stage: "intro", line: notOM, hint: hintNotOM, prep: verifyFails},
		{name: "new not OM", appearAt: 2, stage: "waiting", line: notOM, hint: hintNotOM, prep: verifyFails},
		{name: "listing other bundle", stage: "intro", line: listing, hint: hintListing,
			prep: func(t *testing.T, e *autoEnv) { e.as.lookupApp.BundleID = "com.other" }},
		{name: "listing paid", stage: "intro", line: listing, hint: hintListing,
			prep: func(t *testing.T, e *autoEnv) { e.as.lookupApp.Price = 4.99 }},
		{name: "listing no track id", stage: "intro", line: listing, hint: hintListing,
			prep: func(t *testing.T, e *autoEnv) { e.as.lookupApp.ID = 0 }},
		{name: "lookup expired", stage: "intro", line: expired, hint: hintLogin,
			prep: func(t *testing.T, e *autoEnv) { e.as.lookupErr = appstore.ErrPasswordTokenExpired }},
		{name: "lookup boom", stage: "intro", line: "error: setup: boom?",
			prep: func(t *testing.T, e *autoEnv) { e.as.lookupErr = errors.New("boom\n") }},
		{name: "lookup timeout", stage: "intro", line: "error: setup: timed out after 50ms", prep: func(t *testing.T, e *autoEnv) {
			e.as.lookupBlock = blockUntilCleanup(t)
			e.a.to.AutoLookup = 50 * time.Millisecond
		}},
		{name: "lookup interrupted", stage: "intro", line: "error: setup: interrupted", prep: func(t *testing.T, e *autoEnv) {
			e.as.lookupBlock = blockUntilCleanup(t)
			e.cancelSoon()
		}},
		{name: "open start error", stage: "opening", line: "error: setup: cannot open the App Store: boom", hint: hintOpen,
			prep: func(t *testing.T, e *autoEnv) {
				e.openFn = func(context.Context) (int, error) { return -1, errors.New("boom") }
			}},
		{name: "open exit 1", stage: "opening", line: "error: setup: cannot open the App Store: exit 1", hint: hintOpen,
			prep: func(t *testing.T, e *autoEnv) { e.openFn = func(context.Context) (int, error) { return 1, nil } }},
		{name: "open timeout", stage: "opening", line: "error: setup: cannot open the App Store: timed out after 50ms", hint: hintClick,
			prep: func(t *testing.T, e *autoEnv) {
				e.a.to.AutoOpen = 50 * time.Millisecond
				e.openFn = func(ctx context.Context) (int, error) { <-ctx.Done(); return -1, ctx.Err() }
			}},
		{name: "open interrupted", stage: "opening", line: "error: setup: interrupted", prep: func(t *testing.T, e *autoEnv) {
			e.openFn = func(ctx context.Context) (int, error) { e.cancel(); <-ctx.Done(); return -1, ctx.Err() }
		}},
		{name: "wait timeout", stage: "waiting", line: "error: setup: timed out waiting for Organic Maps after 10m", hint: hintClick, waits: 300},
		{name: "wait interrupted", stage: "waiting", line: "error: setup: interrupted", prep: func(t *testing.T, e *autoEnv) {
			e.blockWait = true
			e.cancelSoon()
		}},
		{name: "purchase expired", appearAt: 1, stage: "download", line: expired, hint: hintLogin,
			prep: func(t *testing.T, e *autoEnv) {
				e.as.purchase = func() error { return appstore.ErrPasswordTokenExpired }
			}},
		{name: "download expired kept", exists: true, stage: "download", line: expired, hint: hintLogin,
			prep: downloadFn(func(appstore.DownloadInput) error { return appstore.ErrPasswordTokenExpired })},
		{name: "download expired new", appearAt: 1, stage: "download", line: expired, hint: hintLogin,
			prep: downloadFn(func(appstore.DownloadInput) error { return appstore.ErrPasswordTokenExpired })},
		{name: "purchase boom", appearAt: 1, stage: "download", line: "error: setup: boom?",
			prep: func(t *testing.T, e *autoEnv) { e.as.purchase = func() error { return errors.New("boom\n") } }},
		{name: "download boom", appearAt: 1, stage: "download", line: "error: setup: boom?",
			prep: downloadFn(func(appstore.DownloadInput) error { return errors.New("boom\n") })},
		{name: "purchase timeout", appearAt: 1, stage: "download", line: "error: setup: timed out after 50ms", prep: func(t *testing.T, e *autoEnv) {
			ch := blockUntilCleanup(t)
			e.as.purchase = func() error { <-ch; return nil }
			e.purchaseTO = 50 * time.Millisecond
		}},
		{name: "download timeout", appearAt: 1, stage: "download", line: "error: setup: timed out after 50ms", prep: func(t *testing.T, e *autoEnv) {
			e.as.downloadFn = blockDownload
			e.downloadTO = 50 * time.Millisecond
		}},
		{name: "download interrupted", exists: true, stage: "download", line: "error: setup: interrupted", prep: func(t *testing.T, e *autoEnv) {
			e.as.downloadFn = blockDownload
			e.cancelSoon()
		}},
		{name: "purchase interrupted", exists: true, stage: "download", line: "error: setup: interrupted", prep: func(t *testing.T, e *autoEnv) {
			ch := blockUntilCleanup(t)
			e.as.purchase = func() error { <-ch; return nil }
			e.cancelSoon()
		}},
		{name: "cache refused", appearAt: 1, stage: "download", keepCache: true,
			line: "error: cache: %CACHE% is a symlink or not owned by you", hint: "hint: remove it and re-run\n",
			prep: func(t *testing.T, e *autoEnv) {
				os.Symlink(t.TempDir(), e.cache)
				e.a.newCache = func() (*cache.Cache, error) { return cache.NewIn(e.cache) }
			}},
		{name: "cannot elevate", appearAt: 1, stage: "enroll", line: "error: setup: cannot elevate: boom",
			prep: func(t *testing.T, e *autoEnv) { e.sudoFn = sudoReturns(-1, errors.New("boom")) }},
		{name: "no executable", exists: true, stage: "enroll", line: "error: setup: cannot elevate: nope",
			prep: func(t *testing.T, e *autoEnv) {
				e.a.executable = func() (string, error) { return "", errors.New("nope") }
			}},
		{name: "sudo exit 1", appearAt: 1, stage: "enroll", line: sudoFailed("exit 1"), hint: sudoHint,
			prep: func(t *testing.T, e *autoEnv) { e.sudoFn = sudoReturns(1, nil) }},
		{name: "sudo exit 5", exists: true, stage: "enroll", line: sudoFailed("exit 5"), hint: sudoHint,
			prep: func(t *testing.T, e *autoEnv) { e.sudoFn = sudoReturns(5, nil) }},
		{name: "sudo signalled", appearAt: 1, stage: "enroll", line: sudoFailed("killed by signal"), hint: sudoHint,
			prep: func(t *testing.T, e *autoEnv) { e.sudoFn = sudoReturns(-1, nil) }},
		{name: "child reported", appearAt: 1, stage: "enroll", hint: "hint: " + notice.EnrollHint + "\n",
			prep: func(t *testing.T, e *autoEnv) { e.sudoFn = sudoReturns(3, nil) }},
		{name: "sudo interrupted", exists: true, stage: "enroll", line: "error: setup: interrupted", prep: func(t *testing.T, e *autoEnv) {
			e.sudoFn = func(c runCall) (int, error) { e.cancel(); return 130, nil }
		}},
	}
	for _, c := range cases {
		for _, cmd := range []string{"setup", "login"} {
			t.Run(c.name+"/"+cmd, func(t *testing.T) {
				e := newAutoEnv(t, c.exists)
				e.appearAt = c.appearAt
				if c.prep != nil {
					c.prep(t, e)
				}
				want := progress(c.exists, c.stage)
				if c.line != "" {
					want += strings.ReplaceAll(c.line, "%CACHE%", e.cache) + "\n"
				}
				want += c.hint
				code := 1
				if cmd == "login" {
					code = 0
					if c.hint == "" {
						want += hintRunSetup
					}
				}
				if !c.exists {
					want += hintDelete
				}
				wantExit(t, e.runCmd(cmd), code, e.testEnv)
				wantErr(t, e.testEnv, want)
				if e.out.Len() != 0 {
					t.Errorf("stdout %q", e.out)
				}
				if !c.keepCache {
					if left := e.cacheEntries(t); len(left) != 0 {
						t.Errorf("cache not empty: %v", left)
					}
				}
				if c.waits > 0 && e.waits != c.waits {
					t.Errorf("waits %d want %d", e.waits, c.waits)
				}
				if strings.HasPrefix(c.name, "kept not OM") || strings.HasPrefix(c.name, "new not OM") || strings.HasPrefix(c.name, "listing") {
					for _, call := range e.run_.calls {
						if call.path == sudoPath {
							t.Error("sudo started")
						}
					}
					if strings.Contains(e.as.callList(), "download") || strings.Contains(e.as.callList(), "purchase") {
						t.Error("download started: " + e.as.callList())
					}
				}
			})
		}
	}
}

func TestAutoSetupKeptSuccess(t *testing.T) {
	for _, cmd := range []string{"setup", "login"} {
		e := newAutoEnv(t, true)
		code := 0
		e.sudoFn = func(c runCall) (int, error) {
			e.ipaAtSudo = true
			e.childOut(c)
			return 0, nil
		}
		wantExit(t, e.runCmd(cmd), code, e.testEnv)
		want := progress(true, "enroll") + doneBox
		wantErr(t, e.testEnv, want)
		if e.out.Len() != 0 || e.run_.count() != 1 || len(e.cacheEntries(t)) != 0 {
			t.Fatal(e.out, e.run_.calls, e.cacheEntries(t))
		}
		c := e.run_.calls[0]
		wantArgs := []string{"-p", sudoPrompt, "/bin/vphone-apps", "setup", "--app", omPath, "--ipa", c.args[7], "--interface", "en0", "--elevated"}
		if c.path != sudoPath || !c.inherit || !reflect.DeepEqual(c.args, wantArgs) || !strings.HasPrefix(c.args[7], e.cache+"/") || filepath.Base(c.args[7]) != "app.ipa" {
			t.Fatal(c)
		}
		if e.as.extID != "123456" || !strings.HasSuffix(e.as.callList(), "lookup,purchase,download,sinf") {
			t.Fatal(e.as.extID, e.as.callList())
		}
	}
}

func TestAutoSetupNewSuccess(t *testing.T) {
	e := newAutoEnv(t, false)
	e.appearAt = 2
	e.sudoFn = func(c runCall) (int, error) { // the root child prints SetupRemoving with --remove-app
		e.ipaAtSudo = true
		e.childOut(c)
		if slices.Contains(c.args, "--remove-app") {
			fmt.Fprintln(e.errb, notice.SetupRemoving)
		}
		return 0, nil
	}
	wantExit(t, e.run("setup", "--interface", "en1", "--keep-work"), 0, e.testEnv)
	wantErr(t, e.testEnv, progress(false, "enroll")+notice.SetupRemoving+"\n"+doneBox)
	if g := e.errb.String(); !strings.Contains(g, "[1/4] Installing Organic Maps\n[2/4] Waiting for the App Store: click Get/Install (up to 10 min)\n[3/4] Downloading installer\n[4/4] Enrolling with Unfairplay\n") {
		t.Fatalf("%q", g)
	}
	if e.waits != 2 || e.run_.count() != 2 {
		t.Fatal(e.waits, e.run_.calls)
	}
	if o := e.run_.calls[0]; o.path != openPath || o.inherit || !reflect.DeepEqual(o.args, []string{listingURL}) {
		t.Fatalf("%v", o)
	}
	a := e.run_.calls[1].args
	if strings.Join(a[4:7], " ") != "--app "+omPath+" --ipa" || strings.Join(a[8:], " ") != "--remove-app --interface en1 --keep-work --elevated" {
		t.Fatal(a)
	}
	if len(e.cacheEntries(t)) != 0 || !e.ipaAtSudo {
		t.Fatal("ipa lifecycle")
	}
}

func TestAutoSetupNotLoggedIn(t *testing.T) {
	e := newAutoEnv(t, true)
	e.as.acctErr = fmt.Errorf("x: %w", keyringNotFound)
	wantExit(t, e.run("setup"), 1, e.testEnv)
	wantErr(t, e.testEnv, "error: setup: not logged in\n"+hintLogin)
	if e.run_.count() != 0 || e.as.callList() != "" {
		t.Fatal("started something")
	}
}

func lockDir(t *testing.T, dir string) {
	sub := filepath.Join(dir, "locked")
	os.Mkdir(sub, 0o700)
	os.WriteFile(filepath.Join(sub, "f"), nil, 0o600)
	os.Chmod(sub, 0o500)
	t.Cleanup(func() { os.Chmod(sub, 0o700) })
}

func TestAutoSetupCleanFailure(t *testing.T) {
	for _, cmd := range []string{"setup", "login"} {
		// child succeeded, deletion failed: only the cleanup lines follow
		e := newAutoEnv(t, false)
		e.appearAt = 1
		e.sudoFn = func(c runCall) (int, error) {
			lockDir(t, filepath.Dir(c.args[7]))
			e.childOut(c)
			return 0, nil
		}
		code := 1
		if cmd == "login" {
			code = 0
		}
		wantExit(t, e.runCmd(cmd), code, e.testEnv)
		got := e.errb.String()
		pre := progress(false, "enroll")
		rest := strings.TrimPrefix(got, pre)
		lines := strings.Split(strings.TrimSuffix(rest, "\n"), "\n")
		if !strings.HasPrefix(got, pre) || len(lines) != 2 || !strings.HasPrefix(lines[0], "error: cleanup: ") ||
			!strings.HasPrefix(lines[1], "hint: delete ") || !strings.HasSuffix(lines[1], " manually") || strings.Contains(got, "Setup is complete") {
			t.Fatalf("%q", got)
		}
		if e.out.Len() != 0 {
			t.Fatal(e.out)
		}

		// child failed and deletion failed too
		e = newAutoEnv(t, false)
		e.appearAt = 1
		e.sudoFn = func(c runCall) (int, error) { lockDir(t, filepath.Dir(c.args[7])); return 1, nil }
		wantExit(t, e.runCmd(cmd), code, e.testEnv)
		got = e.errb.String()
		i := strings.Index(got, "error: cleanup: ")
		if i < 0 || !strings.Contains(got[:i], "sudo failed (exit 1)") || !strings.HasSuffix(got, hintDelete) {
			t.Fatalf("%q", got)
		}
	}
}

func TestLoginSetupExistingModel(t *testing.T) {
	e := newAutoEnv(t, false)
	os.WriteFile(filepath.Join(e.models, "m.bin"), []byte("model"), 0o600)
	wantExit(t, e.run("login"), 0, e.testEnv)
	if e.out.Len() != 0 || e.errb.Len() != 0 || e.run_.count() != 0 || e.as.callList() != "login" {
		t.Fatalf("%q %q %v %s", e.out, e.errb, e.run_.calls, e.as.callList())
	}
}

func TestAutoSetupChildReportedWithModel(t *testing.T) {
	for _, cmd := range []string{"setup", "login"} {
		t.Run(cmd, func(t *testing.T) {
			e := newAutoEnv(t, false)
			e.appearAt = 1
			e.sudoFn = func(c runCall) (int, error) {
				os.WriteFile(filepath.Join(e.models, "m.bin"), []byte("model"), 0o600)
				e.childOut(c)
				return 3, nil
			}
			code := 1
			if cmd == "login" {
				code = 0
			}
			wantExit(t, e.run(cmd), code, e.testEnv)
			wantErr(t, e.testEnv, progress(false, "enroll")+"hint: enrollment finished; delete "+omPath+" manually\n")
			if e.out.Len() != 0 {
				t.Fatal(e.out)
			}
		})
	}
}

func TestLoginSetupModelErrors(t *testing.T) {
	e := newAutoEnv(t, false)
	e.a.modelDir = func() (string, error) { return "", errors.New("cannot resolve SUDO_USER zz") }
	wantExit(t, e.run("login"), 0, e.testEnv)
	wantErr(t, e.testEnv, "error: setup: cannot resolve SUDO_USER zz\n"+hintRunSetup)
	if e.run_.count() != 0 {
		t.Fatal("auto-setup ran")
	}

	e = newAutoEnv(t, false)
	os.Chmod(e.models, 0)
	t.Cleanup(func() { os.Chmod(e.models, 0o700) })
	wantExit(t, e.run("login"), 0, e.testEnv)
	if !strings.HasPrefix(e.errb.String(), "error: setup: ") || !strings.HasSuffix(e.errb.String(), hintRunSetup) || e.run_.count() != 0 {
		t.Fatalf("%q", e.errb)
	}
}

func TestLoginSetupFailedLoginRunsNothing(t *testing.T) {
	e := newAutoEnv(t, false)
	e.as.loginFn = func(appstore.LoginInput) error { return errors.New("bad password") }
	wantExit(t, e.run("login"), 1, e.testEnv)
	wantErr(t, e.testEnv, "error: login: bad password\n")
	if e.run_.count() != 0 {
		t.Fatal("auto-setup ran")
	}
}

func TestLoginSetupUsage(t *testing.T) {
	e := newAutoEnv(t, false)
	wantExit(t, e.run("login", "x"), 2, e.testEnv)
	if !strings.HasPrefix(e.errb.String(), "error: usage: unexpected argument x\n"+loginUsage) || e.as.callList() != "" {
		t.Fatalf("%q", e.errb)
	}
	e = newAutoEnv(t, false)
	wantExit(t, e.run("login", "--bogus"), 2, e.testEnv)
	if !strings.HasPrefix(e.errb.String(), "error: usage: ") || !strings.HasSuffix(e.errb.String(), loginUsage) {
		t.Fatalf("%q", e.errb)
	}
	e = newAutoEnv(t, false)
	wantExit(t, e.run("login", "--help"), 0, e.testEnv)
	if e.out.String() != loginUsage || strings.Count(loginUsage, "\n") != 3 || e.as.callList() != "" || len(e.prompt.asked) != 0 {
		t.Fatalf("%q", e.out)
	}
}

// A real SIGINT during the wait ends auto-setup; login still exits 0.
func TestLoginSetupRealSIGINT(t *testing.T) {
	e := newAutoEnv(t, false)
	e.blockWait = true
	e.onWait = func() { go syscall.Kill(os.Getpid(), syscall.SIGINT) }
	wantExit(t, e.run("login"), 0, e.testEnv)
	wantErr(t, e.testEnv, progress(false, "waiting")+"error: setup: interrupted\n"+hintRunSetup+hintDelete)
	if e.out.Len() != 0 {
		t.Fatal(e.out)
	}
}

// The password reaches neither output nor any child argv, with the app kept or installed.
func TestAutoSetupSentinel(t *testing.T) {
	for _, exists := range []bool{true, false} {
		e := newAutoEnv(t, exists)
		if !exists {
			e.appearAt = 2
		}
		wantExit(t, e.run("login"), 0, e.testEnv)
		if e.run_.count() == 0 {
			t.Fatalf("exists=%v: no child run", exists)
		}
		if strings.Contains(e.out.String()+e.errb.String(), "SENTINEL-PW") {
			t.Fatalf("exists=%v: password leaked", exists)
		}
		for _, c := range e.run_.calls {
			if strings.Contains(c.path+" "+strings.Join(c.args, " "), "SENTINEL-PW") {
				t.Fatalf("exists=%v: password in argv %v", exists, c)
			}
		}
	}
}

// A real runner on a sleeping helper stands in for open.
func TestEnsureAppOpenTimeoutRealRunner(t *testing.T) {
	h := newHelper(t, "sleep")
	e := newAutoEnv(t, false)
	e.a.proc = h.r
	e.a.to.AutoOpen = 50 * time.Millisecond
	start := time.Now()
	wantExit(t, e.run("setup"), 1, e.testEnv)
	wantErr(t, e.testEnv, progress(false, "opening")+"error: setup: cannot open the App Store: timed out after 50ms\n"+hintClick+hintDelete)
	if time.Since(start) > 2*time.Second {
		t.Fatal("slow")
	}
}

func TestEnsureAppPollCounts(t *testing.T) {
	for _, c := range []struct {
		appearAt, waits int
		ok              bool
	}{{2, 2, true}, {4, 4, true}, {299, 299, true}, {300, 300, false}, {301, 300, false}, {0, 300, false}} {
		e := newAutoEnv(t, false)
		e.appearAt = c.appearAt
		_, serr := e.a.ensureApp(context.Background(), storeOf(e.as), newStepDisplay(io.Discard, false, 4))
		if (serr == nil) != c.ok || e.waits != c.waits {
			t.Errorf("appear %d: err %v waits %d", c.appearAt, serr, e.waits)
		}
	}
}

// Real plists that must never be ready nor hang the wait.
func TestEnsureAppUnsafeWrapperPlist(t *testing.T) {
	for name, mk := range map[string]func(t *testing.T, plist string){
		"fifo": func(t *testing.T, p string) { mkfifo(t, p) },
		"symlink to fifo": func(t *testing.T, p string) {
			mkfifo(t, p+".fifo")
			os.Symlink(p+".fifo", p)
		},
		"symlink to zero": func(t *testing.T, p string) { os.Symlink("/dev/zero", p) },
		"1MiB+1": func(t *testing.T, p string) {
			os.WriteFile(p, []byte(pad(wrapperPlist("app.organicmaps", "<integer>1</integer>"), 1<<20+1)), 0o600)
		},
	} {
		t.Run(name, func(t *testing.T) {
			e := newAutoEnv(t, false)
			app := writeApp(t, t.TempDir(), "", "")
			mk(t, filepath.Join(app, "Wrapper", "iTunesMetadata.plist"))
			e.a.pathExists = func(string) bool { return false }
			e.a.pathExists = func(p string) bool { return e.waits >= 0 && p == omPath && e.exists[omPath] }
			e.exists[omPath] = false
			// the app is "present" only for the readiness probe
			e.a.externalVersion = func(string) (string, bool) { return readExternalVersion(app) }
			e.appearAt = 0
			start := time.Now()
			_, serr := e.a.ensureApp(context.Background(), storeOf(e.as), newStepDisplay(io.Discard, false, 4))
			if serr == nil || !strings.Contains(serr.line, "timed out waiting") || time.Since(start) > 2*time.Second {
				t.Fatal(serr)
			}
		})
	}
}

func TestReadExternalVersion(t *testing.T) {
	write := func(t *testing.T, content string) string {
		return writeApp(t, t.TempDir(), "", content)
	}
	ext := func(v string) string { return wrapperPlist("app.organicmaps", v) }
	good := map[string]string{
		"integer": ext("<integer>123456</integer>"),
		"string":  ext("<string>123456</string>"),
		"1MiB":    pad(ext("<integer>123456</integer>"), 1<<20),
	}
	for name, content := range good {
		if v, ok := readExternalVersion(write(t, content)); v != "123456" || !ok {
			t.Errorf("%s: %q %v", name, v, ok)
		}
	}
	empty := map[string]string{
		"1MiB+1":   pad(ext("<integer>123456</integer>"), 1<<20+1),
		"10MiB":    pad(ext("<integer>123456</integer>"), 10<<20),
		"injected": ext("<string>1; x</string>"),
		"negative": ext("<integer>-1</integer>"),
		"21 digit": ext("<string>" + strings.Repeat("1", 21) + "</string>"),
		"no key":   xmlPlist("a", "<string>b</string>"),
	}
	for name, content := range empty {
		v, ok := readExternalVersion(write(t, content))
		if v != "" || (ok != (name != "1MiB+1" && name != "10MiB")) {
			t.Errorf("%s: %q %v", name, v, ok)
		}
	}
	if v, ok := readExternalVersion(write(t, "garbage")); v != "" || ok {
		t.Errorf("garbage: %q %v", v, ok)
	}
	if v, ok := readExternalVersion(filepath.Join(t.TempDir(), "missing")); v != "" || ok {
		t.Errorf("missing: %q %v", v, ok)
	}
	for name, mk := range map[string]func(p string){
		"fifo":            func(p string) { mkfifo(t, p) },
		"symlink to fifo": func(p string) { mkfifo(t, p+".fifo"); os.Symlink(p+".fifo", p) },
		"symlink to zero": func(p string) { os.Symlink("/dev/zero", p) },
	} {
		app := writeApp(t, t.TempDir(), "", "")
		p := filepath.Join(app, "Wrapper", "iTunesMetadata.plist")
		mk(p)
		start := time.Now()
		if v, ok := readExternalVersion(app); v != "" || ok || time.Since(start) > 2*time.Second {
			t.Errorf("%s: %q %v", name, v, ok)
		}
	}
}

func TestVerifyOrganicMapsWrapperOnly(t *testing.T) {
	if err := verifyOrganicMaps(writeApp(t, t.TempDir(), "", wrapperPlist("com.other", "<integer>1</integer>"))); err == nil {
		t.Fatal("other bundle id accepted")
	}
	if err := verifyOrganicMaps(writeApp(t, t.TempDir(), "", wrapperPlist("app.organicmaps", "<integer>1</integer>"))); err != nil {
		t.Fatal(err)
	}
}

func TestAutoSetupRootNeverCallsRunner(t *testing.T) {
	for _, c := range []struct {
		cmd  string
		code int
		want string
	}{
		{"setup", 1, "error: setup: running as root; run without sudo\n"},
		{"login", 0, "error: setup: running as root; run without sudo\n" + hintRunSetup}, // login's usual hint
	} {
		e := newAutoEnv(t, true)
		e.a.geteuid = func() int { return 0 }
		wantExit(t, e.runCmd(c.cmd), c.code, e.testEnv)
		wantErr(t, e.testEnv, c.want)
		calls := e.as.callList()
		if e.run_.count() != 0 || e.out.Len() != 0 || strings.Contains(calls, "lookup") || strings.Contains(calls, "download") || strings.Contains(calls, "purchase") {
			t.Fatalf("%s: started something as root: %v %q", c.cmd, e.run_.calls, calls)
		}
	}
}

// Root with no session: the refusal comes first, so no store is built or called.
func TestSetupRootNoSessionRefusesBeforeStore(t *testing.T) {
	e := newAutoEnv(t, true)
	e.a.geteuid = func() int { return 0 }
	e.as.acctErr = errors.New("no session")
	opened := 0
	for _, f := range []*func() (Store, error){&e.a.newStore, &e.a.openStore} {
		orig := *f
		*f = func() (Store, error) { opened++; return orig() }
	}
	wantExit(t, e.run("setup"), 1, e.testEnv)
	wantErr(t, e.testEnv, "error: setup: running as root; run without sudo\n")
	if opened != 0 || e.as.acctCalls != 0 || e.as.callList() != "" || e.run_.count() != 0 || e.out.Len() != 0 {
		t.Fatal(opened, e.as.acctCalls, e.as.callList(), e.run_.calls)
	}
}

// The terminal display: a byte bar while downloading, closed before the
// child starts, the result and the completion box.
func TestAutoSetupTTYDisplay(t *testing.T) {
	{
		cmd := "setup"
		e := newAutoEnv(t, true)
		e.a.stderrTTY = true
		seen := make(chan struct{})
		e.a.stderr = &notifyWriter{w: e.errb, sub: "40%", ch: seen}
		e.as.downloadFn = func(in appstore.DownloadInput) error {
			in.Progress.ChangeMax64(100)
			in.Progress.Set64(40)
			select {
			case <-seen:
			case <-time.After(5 * time.Second):
				return errors.New("no progress frame drawn")
			}
			return os.WriteFile(in.OutputPath, []byte("encrypted"), 0o644)
		}
		var atChild string
		e.sudoFn = func(c runCall) (int, error) {
			atChild = e.errb.String()
			e.childOut(c)
			return 0, nil
		}
		wantExit(t, e.runCmd(cmd), 0, e.testEnv)
		got := e.errb.String()
		if !strings.Contains(got, frame(true, 2, 3, "Downloading installer", 12, "40%")) {
			t.Fatalf("no bar in %q", got)
		}
		if !strings.HasSuffix(atChild, frame(true, 3, 3, "Enrolling with Unfairplay", 30, "100%")+"\n") {
			t.Fatalf("display open at child start: %q", atChild)
		}
		if !strings.HasSuffix(got, "100%\n"+doneBox) {
			t.Fatalf("%q", got)
		}
	}
}

// A terminal failure closes the display before the error line.
func TestAutoSetupTTYFailureClosesDisplay(t *testing.T) {
	e := newAutoEnv(t, true)
	e.a.stderrTTY = true
	e.as.lookupErr = errors.New("boom")
	wantExit(t, e.run("setup"), 1, e.testEnv)
	if !strings.HasSuffix(e.errb.String(), "1/3\nerror: setup: boom\n") {
		t.Fatalf("%q", e.errb)
	}
}

// --verbose reaches the child's argv for setup only.
func TestAutoSetupVerboseArg(t *testing.T) {
	for _, c := range []struct {
		args []string
		want bool
	}{{[]string{"setup", "--verbose"}, true}, {[]string{"setup"}, false}, {[]string{"login"}, false}} {
		e := newAutoEnv(t, true)
		wantExit(t, e.run(c.args...), 0, e.testEnv)
		a := e.run_.calls[0].args
		if got := a[len(a)-2] == "--verbose" && a[len(a)-1] == "--elevated"; got != c.want {
			t.Errorf("%v: %v", c.args, a)
		}
	}
}

// A download error after progress ends the output with the error line, no
// frame bytes after it.
func TestAutoSetupTTYDownloadErrorAfterProgress(t *testing.T) {
	e := newAutoEnv(t, true)
	e.a.stderrTTY = true
	seen := make(chan struct{})
	e.a.stderr = &notifyWriter{w: e.errb, sub: "40%", ch: seen}
	e.as.downloadFn = func(in appstore.DownloadInput) error {
		in.Progress.ChangeMax64(100)
		in.Progress.Set64(40)
		select {
		case <-seen:
		case <-time.After(5 * time.Second):
		}
		return errors.New("boom")
	}
	wantExit(t, e.run("setup"), 1, e.testEnv)
	got := e.errb.String()
	i := strings.Index(got, "\nerror: ")
	if !strings.Contains(got, "40%") || i < 0 || strings.Count(got[i:], "\n") > 3 || strings.Contains(got[i:], "\x1b") || strings.Contains(got[i:], "\r") {
		t.Fatalf("%q", got)
	}
}

// A child failure after the display closed adds no frame bytes.
func TestAutoSetupTTYChildFailureAfterDone(t *testing.T) {
	e := newAutoEnv(t, true)
	e.a.stderrTTY = true
	e.sudoFn = sudoReturns(1, errors.New("exit 1"))
	wantExit(t, e.run("setup"), 1, e.testEnv)
	got := e.errb.String()
	i := strings.Index(got, "100%\n")
	if i < 0 || strings.ContainsAny(got[i+5:], "\x1b\r") {
		t.Fatalf("%q", got)
	}
}

// --verbose adds the intro, kept/new and prerequisite lines (setup only).
func TestAutoSetupVerboseOutput(t *testing.T) {
	for _, exists := range []bool{true, false} {
		e := newAutoEnv(t, exists)
		e.appearAt = 1
		e.sudoFn = func(c runCall) (int, error) { e.childOut(c); return 0, nil }
		wantExit(t, e.run("setup", "--verbose"), 0, e.testEnv)
		wantErr(t, e.testEnv, progressV(exists, "enroll", true)+doneBox)
		if e.out.String() != "/m/x.bin\n" {
			t.Fatalf("verbose stdout %q", e.out)
		}
	}
}

// A failed run shows no box (success output is asserted by the Success tests).
func TestAutoSetupBoxOnlyOnSuccess(t *testing.T) {
	for _, cmd := range []string{"setup", "login"} {
		f := newAutoEnv(t, true)
		f.sudoFn = func(runCall) (int, error) { return 3, nil }
		f.runCmd(cmd)
		if strings.Contains(f.errb.String(), "Setup is complete") || strings.Contains(f.errb.String(), "+---") {
			t.Fatalf("%s failed run shows box: %q", cmd, f.errb)
		}
	}
}
