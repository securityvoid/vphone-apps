package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DrWhax/unfairplay/pkg/setup"
	"github.com/securityvoid/vphone-apps/internal/notice"
)

type setupEnv struct {
	*testEnv
	app, ipa string
	runs     []setup.Options
}

func newSetupEnv(t *testing.T, euid int) *setupEnv {
	e := &setupEnv{testEnv: newEnv(t, happy)}
	d := t.TempDir()
	e.app, e.ipa = filepath.Join(d, "X.app"), filepath.Join(d, "x.ipa")
	os.Mkdir(e.app, 0o755)
	os.WriteFile(e.ipa, []byte("z"), 0o644)
	e.a.geteuid = func() int { return euid }
	e.a.runSetup = func(_ context.Context, o setup.Options) (setup.Result, error) {
		e.runs = append(e.runs, o)
		o.Progress("progress-1")
		return setup.Result{ModelPath: "/m/x.bin"}, nil
	}
	return e
}

func (e *setupEnv) runnerExit(code int, err error) {
	e.run_.fn = func(context.Context, runCall) (int, error) { return code, err }
}

const sudoFailedHint = "hint: sudo needs an administrator account and its password; fix that, then re-run vphone-apps setup\n"

func TestElevate(t *testing.T) {
	e := newSetupEnv(t, 501)
	wantExit(t, e.run("setup", "--app", e.app, "--ipa", e.ipa), 0, e.testEnv)
	want := "/usr/bin/sudo -p Mac login password (for sudo):  /bin/vphone-apps setup --app " + e.app + " --ipa " + e.ipa + " --elevated"
	if len(e.run_.calls) != 1 || e.run_.calls[0].String() != want || !e.run_.calls[0].inherit {
		t.Fatal(e.run_.calls)
	}
	if e.out.Len() != 0 || e.errb.String() != doneBox || len(e.runs) != 0 {
		t.Fatalf("parent output %q %q", e.out, e.errb)
	}

	for _, c := range []struct {
		code int
		err  error
		want string
		exit int
	}{
		{0, nil, doneBox, 0},
		{3, nil, "", 1},
		{1, nil, "error: setup: sudo failed (exit 1)\n" + sudoFailedHint, 1},
		{5, nil, "error: setup: sudo failed (exit 5)\n" + sudoFailedHint, 1},
		{-1, nil, "error: setup: sudo failed (killed by signal)\n" + sudoFailedHint, 1},
		{-1, errors.New("nope"), "error: setup: cannot elevate: nope\nhint: re-run as root\n", 1},
	} {
		e = newSetupEnv(t, 501)
		e.runnerExit(c.code, c.err)
		wantExit(t, e.run("setup", "--app", e.app, "--ipa", e.ipa), c.exit, e.testEnv)
		wantErr(t, e.testEnv, c.want)
		if len(e.runs) != 0 {
			t.Fatal("Run called by the unprivileged process")
		}
	}

	e = newSetupEnv(t, 501)
	e.a.executable = func() (string, error) { return "", errors.New("nope") }
	wantExit(t, e.run("setup", "--app", e.app, "--ipa", e.ipa), 1, e.testEnv)
	wantErr(t, e.testEnv, "error: setup: cannot elevate: nope\nhint: re-run as root\n")
	if strings.Contains(e.errb.String(), "Setup is complete.") {
		t.Fatal("box on failure")
	}
	if e.run_.count() != 0 || len(e.runs) != 0 {
		t.Fatal("something ran")
	}

	// interrupt: ctx done and a non-0/3 child
	e = newSetupEnv(t, 501)
	e.runnerExit(130, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	wantExit(t, e.runCtx(ctx, "setup", "--app", e.app, "--ipa", e.ipa), 1, e.testEnv)
	wantErr(t, e.testEnv, "error: setup: interrupted\n")
}

func TestElevateRootNeverRunsRunner(t *testing.T) {
	e := newSetupEnv(t, 0)
	wantExit(t, e.run("setup", "--app", e.app, "--ipa", e.ipa), 0, e.testEnv)
	if e.run_.count() != 0 {
		t.Fatal("runner called as root")
	}
	for _, c := range []struct {
		args []string
		exit int
	}{{nil, 1}, {[]string{"--elevated"}, 3}} {
		e = newSetupEnv(t, 0)
		e.a.runSetup = func(context.Context, setup.Options) (setup.Result, error) { return setup.Result{}, errors.New("boom") }
		wantExit(t, e.run(append([]string{"setup", "--app", e.app, "--ipa", e.ipa}, c.args...)...), c.exit, e.testEnv)
		if n := strings.Count(e.errb.String(), "error: setup:"); n != 1 || e.run_.count() != 0 {
			t.Fatalf("%q", e.errb)
		}
	}
}

// Modes and ownership of model files belong to the real setup.Run (stubbed
// here); the command itself must add nothing to the model directory.
func TestSetupRoot(t *testing.T) {
	e := newSetupEnv(t, 0)
	models, before := e.models, tree(t, e.models)
	defer func() {
		if tree(t, models) != before {
			t.Error("setup command changed the model directory")
		}
	}()
	wantExit(t, e.run("setup", "--app", e.app, "--ipa", e.ipa), 0, e.testEnv)
	if e.out.Len() != 0 {
		t.Fatal(e.out)
	}
	if want := doneBox; e.errb.String() != want {
		t.Fatalf("%q", e.errb)
	}
	o := e.runs[0]
	if o.ModelDirectory != e.models || o.HardwareInterface != "en0" || o.KeepWork || o.AppPath != e.app || o.IPAPath != e.ipa {
		t.Fatalf("%+v", o)
	}
	e = newSetupEnv(t, 0)
	wantExit(t, e.run("setup", "--app", e.app, "--ipa", e.ipa, "--keep-work", "--interface", "abcdefghijklmnop"), 0, e.testEnv)
	if !e.runs[0].KeepWork || e.runs[0].HardwareInterface != "abcdefghijklmnop" {
		t.Fatalf("%+v", e.runs[0])
	}
}

func TestSetupErrors(t *testing.T) {
	e := newSetupEnv(t, 0)
	e.a.runSetup = func(context.Context, setup.Options) (setup.Result, error) { return setup.Result{}, errors.New("boom") }
	wantExit(t, e.run("setup", "--app", e.app, "--ipa", e.ipa), 1, e.testEnv)
	if !strings.HasSuffix(e.errb.String(), "error: setup: boom\n") || e.out.Len() != 0 {
		t.Fatal(e.errb)
	}
	e = newSetupEnv(t, 0)
	e.a.modelDir = func() (string, error) { return "", errors.New("cannot resolve SUDO_USER zz") }
	wantExit(t, e.run("setup", "--app", e.app, "--ipa", e.ipa), 1, e.testEnv)
	wantErr(t, e.testEnv, "error: setup: cannot resolve SUDO_USER zz\n")
}

func TestSetupValidation(t *testing.T) {
	d := t.TempDir()
	file := filepath.Join(d, "f.app")
	os.WriteFile(file, nil, 0o644)
	plainDir := filepath.Join(d, "x")
	os.Mkdir(plainDir, 0o755)
	zip := filepath.Join(d, "x.zip")
	os.WriteFile(zip, nil, 0o644)
	ipaDir := filepath.Join(d, "d.ipa")
	os.Mkdir(ipaDir, 0o755)

	e := newSetupEnv(t, 501)
	missing := filepath.Join(d, "nope.app")
	wantExit(t, e.run("setup", "--app", missing, "--ipa", e.ipa), 1, e.testEnv)
	wantErr(t, e.testEnv, "error: setup: "+missing+" not found\n")
	for _, c := range [][]string{{file, e.ipa}, {plainDir, e.ipa}, {e.app, ipaDir}, {e.app, zip}} {
		e = newSetupEnv(t, 501)
		wantExit(t, e.run("setup", "--app", c[0], "--ipa", c[1]), 1, e.testEnv)
		if !strings.Contains(e.errb.String(), "is not a valid app/ipa\n") || e.run_.count() != 0 {
			t.Errorf("%v: %q", c, e.errb)
		}
	}
}

func TestSetupUsageErrors(t *testing.T) {
	const om = "/Applications/Organic Maps.app"
	for _, c := range []struct {
		args []string
		msg  string
	}{
		{[]string{"--app", "a.app"}, "--app and --ipa must be given together"},
		{[]string{"--ipa", "b.ipa"}, "--app and --ipa must be given together"},
		{[]string{"--interface", "abcdefghijklmnopq"}, "--interface must match [a-z][a-z0-9]{0,15}"},
		{[]string{"--interface", "en0;id"}, "--interface must match [a-z][a-z0-9]{0,15}"},
		{[]string{"--interface", "../x"}, "--interface must match [a-z][a-z0-9]{0,15}"},
		{[]string{"x"}, "unexpected argument x"},
		{[]string{"--app", "a.app", "--ipa", "b.ipa", "x"}, "unexpected argument x"},
		{[]string{"--remove-app", "--app", "other.app", "--ipa", "b.ipa"}, `--remove-app needs --app "` + om + `" and --ipa`},
		{[]string{"--remove-app"}, `--remove-app needs --app "` + om + `" and --ipa`},
		{[]string{"--bogus"}, "flag provided but not defined: -bogus"},
	} {
		for _, euid := range []int{501, 0} {
			e := newSetupEnv(t, euid)
			wantExit(t, e.run(append([]string{"setup"}, c.args...)...), 2, e.testEnv)
			if !strings.HasPrefix(e.errb.String(), "error: usage: "+c.msg+"\n"+setupUsage) || e.run_.count()+len(e.runs)+len(e.removed) != 0 {
				t.Errorf("%v: %q", c.args, e.errb)
			}
		}
	}
	// 16 characters are accepted
	e := newSetupEnv(t, 0)
	wantExit(t, e.run("setup", "--app", e.app, "--ipa", e.ipa, "--interface", "abcdefghijklmnop"), 0, e.testEnv)
}

func TestSetupHelp(t *testing.T) {
	e := newSetupEnv(t, 501)
	wantExit(t, e.run("setup", "--help"), 0, e.testEnv)
	if e.out.String() != setupUsage || strings.Count(setupUsage, "\n") != 4 ||
		strings.Contains(setupUsage, "--remove-app") || strings.Contains(setupUsage, "--elevated") || e.run_.count() != 0 {
		t.Fatalf("%q", e.out)
	}
}

func TestSetupInterrupt(t *testing.T) {
	e := newSetupEnv(t, 0)
	ctx, cancel := context.WithCancel(context.Background())
	e.a.runSetup = func(ctx context.Context, _ setup.Options) (setup.Result, error) {
		go func() { time.Sleep(50 * time.Millisecond); cancel() }()
		<-ctx.Done()
		return setup.Result{}, ctx.Err()
	}
	wantExit(t, e.runCtx(ctx, "setup", "--app", e.app, "--ipa", e.ipa), 1, e.testEnv)
	if !strings.HasSuffix(e.errb.String(), "error: setup: interrupted\n") || e.out.Len() != 0 {
		t.Fatal(e.errb)
	}
}

const omPath = "/Applications/Organic Maps.app"

// removeEnv is a root setup environment where omPath passes the path check.
func newRemoveEnv(t *testing.T, euid int) *setupEnv {
	e := newSetupEnv(t, euid)
	stat := e.a.stat
	e.a.stat = func(p string) (os.FileInfo, error) {
		if p == omPath {
			p = e.app
		}
		return stat(p)
	}
	return e
}

func (e *setupEnv) removeRun(extra ...string) int {
	return e.run(append([]string{"setup", "--app", omPath, "--ipa", e.ipa, "--remove-app"}, extra...)...)
}

func TestRemoveApp(t *testing.T) {
	// removal after success: stdout empty, the model path only with --verbose
	e := newRemoveEnv(t, 0)
	wantExit(t, e.removeRun(), 0, e.testEnv)
	if len(e.removed) != 1 || e.removed[0] != omPath || e.out.Len() != 0 {
		t.Fatal(e.removed, e.out)
	}
	v := newRemoveEnv(t, 0)
	wantExit(t, v.removeRun("--verbose"), 0, v.testEnv)
	if v.out.String() != "/m/x.bin\n" {
		t.Fatal(v.out)
	}
	if !strings.Contains(e.errb.String(), notice.SetupRemoving+"\n") || strings.Contains(e.errb.String(), "progress-1") {
		t.Fatalf("%q", e.errb)
	}
	// removal after a Run error
	for _, extra := range [][]string{nil, {"--elevated"}} {
		e = newRemoveEnv(t, 0)
		e.a.runSetup = func(context.Context, setup.Options) (setup.Result, error) { return setup.Result{}, errors.New("boom") }
		want := 1
		if extra != nil {
			want = 3
		}
		wantExit(t, e.removeRun(extra...), want, e.testEnv)
		if len(e.removed) != 1 || e.out.Len() != 0 || !strings.HasSuffix(e.errb.String(), "error: setup: boom\n") {
			t.Fatal(e.removed, e.out, e.errb)
		}
	}
	// failing remover after a successful Run
	for _, extra := range [][]string{nil, {"--elevated"}} {
		e = newRemoveEnv(t, 0)
		e.a.removeApp = func(string) error { return errors.New("cannot remove " + omPath + ": busy") }
		want := 1
		if extra != nil {
			want = 3
		}
		wantExit(t, e.removeRun(extra...), want, e.testEnv)
		if !strings.HasSuffix(e.errb.String(), "error: cleanup: cannot remove "+omPath+": busy\n") || e.out.Len() != 0 {
			t.Fatalf("%q %q", e.out, e.errb)
		}
	}
	// without the flag the remover is never called
	e = newSetupEnv(t, 0)
	wantExit(t, e.run("setup", "--app", e.app, "--ipa", e.ipa), 0, e.testEnv)
	if len(e.removed) != 0 {
		t.Fatal("removed without flag")
	}
}

func TestRemoveAppInterrupt(t *testing.T) {
	for name, slowRemover := range map[string]bool{"run": false, "remover": true} {
		e := newRemoveEnv(t, 0)
		ctx, cancel := context.WithCancel(context.Background())
		if slowRemover {
			e.a.removeApp = func(p string) error {
				cancel()
				time.Sleep(50 * time.Millisecond)
				e.removed = append(e.removed, p)
				return nil
			}
		} else {
			e.a.runSetup = func(ctx context.Context, _ setup.Options) (setup.Result, error) {
				go func() { time.Sleep(50 * time.Millisecond); cancel() }()
				<-ctx.Done()
				return setup.Result{}, ctx.Err()
			}
		}
		wantExit(t, e.runCtx(ctx, "setup", "--app", omPath, "--ipa", e.ipa, "--remove-app", "--elevated"), 3, e.testEnv)
		if len(e.removed) != 1 || e.out.Len() != 0 || !strings.HasSuffix(e.errb.String(), "error: setup: interrupted\n") {
			t.Fatal(name, e.removed, e.out, e.errb)
		}
	}
}

// writeApp builds an app bundle in dir with the given Info.plist and wrapper
// plist contents ("" = absent).
func writeApp(t *testing.T, dir, info, wrapper string) string {
	t.Helper()
	app := filepath.Join(dir, "Organic Maps.app")
	os.MkdirAll(filepath.Join(app, "Contents"), 0o755)
	os.MkdirAll(filepath.Join(app, "Wrapper"), 0o755)
	if info != "" {
		os.WriteFile(filepath.Join(app, "Contents", "Info.plist"), []byte(info), 0o644)
	}
	if wrapper != "" {
		os.WriteFile(filepath.Join(app, "Wrapper", "iTunesMetadata.plist"), []byte(wrapper), 0o644)
	}
	return app
}

func xmlPlist(kv ...string) string {
	s := `<?xml version="1.0" encoding="UTF-8"?><plist version="1.0"><dict>`
	for i := 0; i < len(kv); i += 2 {
		s += "<key>" + kv[i] + "</key>" + kv[i+1]
	}
	return s + "</dict></plist>"
}

func bundleInfo(id string) string { return xmlPlist("CFBundleIdentifier", "<string>"+id+"</string>") }

func wrapperPlist(id, ext string) string {
	return xmlPlist("softwareVersionBundleId", "<string>"+id+"</string>", "softwareVersionExternalIdentifier", ext)
}

// pad grows a plist to exactly n bytes with trailing spaces.
func pad(s string, n int) string { return s + strings.Repeat(" ", n-len(s)) }

func TestRealRemoveApp(t *testing.T) {
	valid := wrapperPlist("app.organicmaps", "<integer>1</integer>")
	for name, c := range map[string]struct {
		build func(t *testing.T, d string) string
		want  string // "" = removed
	}{
		"organicmaps": {func(t *testing.T, d string) string { return writeApp(t, d, bundleInfo("app.organicmaps"), valid) }, ""},
		"1MiB": {func(t *testing.T, d string) string {
			return writeApp(t, d, pad(bundleInfo("app.organicmaps"), 1<<20), valid)
		}, ""},
		"wrapper only": {func(t *testing.T, d string) string { return writeApp(t, d, "", valid) }, ""},
		"other id":     {func(t *testing.T, d string) string { return writeApp(t, d, bundleInfo("com.other"), valid) }, "refusing to remove"},
		"wrapper other id": {func(t *testing.T, d string) string {
			return writeApp(t, d, "", wrapperPlist("com.other", "<integer>1</integer>"))
		}, "refusing to remove"},
		"garbage": {func(t *testing.T, d string) string { return writeApp(t, d, "garbage", valid) }, "refusing to remove"},
		"no key":  {func(t *testing.T, d string) string { return writeApp(t, d, xmlPlist("x", "<string>y</string>"), valid) }, "refusing to remove"},
		"1MiB+1": {func(t *testing.T, d string) string {
			return writeApp(t, d, pad(bundleInfo("app.organicmaps"), 1<<20+1), valid)
		}, "refusing to remove"},
		"10MiB": {func(t *testing.T, d string) string {
			return writeApp(t, d, pad(bundleInfo("app.organicmaps"), 10<<20), valid)
		}, "refusing to remove"},
		"missing": {func(t *testing.T, d string) string { return filepath.Join(d, "nope.app") }, "cannot remove"},
		"plain file": {func(t *testing.T, d string) string {
			p := filepath.Join(d, "f.app")
			os.WriteFile(p, []byte("x"), 0o644)
			return p
		}, "refusing to remove"},
		"symlink": {func(t *testing.T, d string) string {
			target := writeApp(t, filepath.Join(d, "t"), bundleInfo("app.organicmaps"), valid)
			p := filepath.Join(d, "link.app")
			os.Symlink(target, p)
			return p
		}, "refusing to remove"},
		"info symlink to /dev/zero": {func(t *testing.T, d string) string {
			app := writeApp(t, d, "", valid)
			os.Symlink("/dev/zero", filepath.Join(app, "Contents", "Info.plist"))
			return app
		}, "refusing to remove"},
		"info fifo": {func(t *testing.T, d string) string {
			app := writeApp(t, d, "", valid)
			mkfifo(t, filepath.Join(app, "Contents", "Info.plist"))
			return app
		}, "refusing to remove"},
		"info symlink to fifo": {func(t *testing.T, d string) string {
			app := writeApp(t, d, "", valid)
			mkfifo(t, filepath.Join(d, "fifo"))
			os.Symlink(filepath.Join(d, "fifo"), filepath.Join(app, "Contents", "Info.plist"))
			return app
		}, "refusing to remove"},
	} {
		t.Run(name, func(t *testing.T) {
			d := t.TempDir()
			p := c.build(t, d)
			start := time.Now()
			err := removeApp(p)
			if time.Since(start) > 2*time.Second {
				t.Fatal("slow")
			}
			_, serr := os.Lstat(p)
			if c.want == "" {
				if err != nil || serr == nil {
					t.Fatal(err, serr)
				}
				return
			}
			if err == nil || !strings.HasPrefix(err.Error(), c.want+" "+p) {
				t.Fatal(err)
			}
			if name != "missing" && serr != nil {
				t.Fatal("target removed")
			}
			if name == "symlink" {
				if _, err := os.Stat(filepath.Join(d, "t", "Organic Maps.app")); err != nil {
					t.Fatal("symlink target removed")
				}
			}
		})
	}
}

// Unfairplay progress lines show only with --verbose; error lines always.
func TestSetupProgressVerbose(t *testing.T) {
	for _, c := range []struct {
		args []string
		show bool
	}{{nil, false}, {[]string{"--verbose"}, true}, {[]string{"--elevated", "--verbose"}, true}, {[]string{"--elevated"}, false}} {
		e := newSetupEnv(t, 0)
		wantExit(t, e.run(append([]string{"setup", "--app", e.app, "--ipa", e.ipa}, c.args...)...), 0, e.testEnv)
		if got := strings.Contains(e.errb.String(), "progress-1\n"); got != c.show {
			t.Errorf("%v: %q", c.args, e.errb)
		}
	}
	e := newSetupEnv(t, 0)
	e.a.runSetup = func(context.Context, setup.Options) (setup.Result, error) { return setup.Result{}, errors.New("boom") }
	wantExit(t, e.run("setup", "--app", e.app, "--ipa", e.ipa, "--elevated"), 3, e.testEnv)
	if !strings.Contains(e.errb.String(), "error: setup: boom\n") {
		t.Fatalf("%q", e.errb)
	}
}

// Prerequisites is printed only with --verbose.
func TestSetupVerbosePrerequisites(t *testing.T) {
	e := newSetupEnv(t, 0)
	wantExit(t, e.run("setup", "--app", e.app, "--ipa", e.ipa, "--verbose"), 0, e.testEnv)
	if want := notice.Prerequisites + "\nprogress-1\n" + doneBox; e.errb.String() != want {
		t.Fatalf("%q", e.errb)
	}
	e = newSetupEnv(t, 0)
	wantExit(t, e.run("setup", "--app", e.app, "--ipa", e.ipa, "--elevated", "--verbose"), 0, e.testEnv)
	if want := "progress-1\n"; e.errb.String() != want {
		t.Fatalf("elevated: %q", e.errb)
	}
	// non-root: Prerequisites is already on stderr when the runner first runs, and appears once.
	e = newSetupEnv(t, 501)
	var atRun string
	e.run_.fn = func(context.Context, runCall) (int, error) {
		if atRun == "" {
			atRun = e.errb.String()
		}
		return 0, nil
	}
	wantExit(t, e.run("setup", "--app", e.app, "--ipa", e.ipa, "--verbose"), 0, e.testEnv)
	if atRun != notice.Prerequisites+"\n" || strings.Count(e.errb.String(), notice.Prerequisites) != 1 {
		t.Fatalf("at runner %q, final %q", atRun, e.errb)
	}
}
