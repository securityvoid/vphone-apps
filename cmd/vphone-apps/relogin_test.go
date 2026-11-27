package main

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/majd/ipatool/v2/pkg/appstore"
	"github.com/securityvoid/vphone-apps/internal/store"
)

var errExpired = appstore.ErrPasswordTokenExpired

const expiredOut = "session expired\nhint: run vphone-apps login\n"

// reScenario is one command whose store call can answer "session expired".
type reScenario struct {
	name string
	args []string
	step string // the step in the error line
	// expire makes the call fail with an expired session until fix is called.
	expire func(e *testEnv) (fix func())
	// n is the stderr step number after which a failure is printed (install).
	n int
}

func reScenarios() []reScenario {
	return []reScenario{
		{name: "search", args: []string{"search", "x"}, step: "search", expire: func(e *testEnv) func() {
			e.as.searchErr = errExpired
			return func() { e.as.searchErr = nil }
		}},
		{name: "select search", args: []string{"install", "x", "--vm", "v1"}, step: "select", expire: func(e *testEnv) func() {
			e.as.searchErr = errExpired
			return func() { e.as.searchErr = nil }
		}},
		{name: "select lookup", args: []string{"install", "--id", "com.example.app", "--vm", "v1"}, step: "select", expire: func(e *testEnv) func() {
			e.as.lookupErr = errExpired
			return func() { e.as.lookupErr = nil }
		}},
		{name: "download", args: []string{"install", "x", "--vm", "v1"}, step: "download", n: 1, expire: func(e *testEnv) func() {
			expired := true
			e.as.downloadFn = func(in appstore.DownloadInput) error {
				if expired {
					return errExpired
				}
				return os.WriteFile(in.OutputPath, []byte("encrypted"), 0o644)
			}
			return func() { expired = false }
		}},
	}
}

func (s reScenario) wantFailure(t *testing.T, e *testEnv, line string) {
	t.Helper()
	if s.n > 0 {
		wantStepErr(t, e, s.n, line)
	} else {
		wantErr(t, e, line)
	}
}

func TestReloginSucceedsOnce(t *testing.T) {
	for _, s := range reScenarios() {
		t.Run(s.name, func(t *testing.T) {
			e := newEnv(t, happy)
			e.ring.seed(savedJSON)
			fix := s.expire(e)
			var got []appstore.LoginInput
			e.as.loginFn = func(in appstore.LoginInput) error { got = append(got, in); fix(); return nil }
			wantExit(t, e.run(s.args...), 0, e)
			if len(got) != 1 || got[0].Email != "saved@example.com" || got[0].Password != "saved-pw" {
				t.Fatalf("logins %+v", got)
			}
			if strings.Contains(e.errb.String(), "session expired") || e.asked() != "" {
				t.Fatalf("stderr %q asked %q", e.errb, e.asked())
			}
		})
	}
}

func TestReloginUnchangedErrorCases(t *testing.T) {
	cases := map[string]func(e *testEnv){
		"no item":         func(e *testEnv) { delete(e.ring.items, "apple-account") },
		"bad item":        func(e *testEnv) { e.ring.seed("not json") },
		"load error":      func(e *testEnv) { e.ring.getErr = errors.New("locked") },
		"failing relogin": func(e *testEnv) { e.as.loginFn = func(appstore.LoginInput) error { return errors.New("bad password") } },
		"2FA no terminal": func(e *testEnv) {
			e.prompt.tty = false
			e.as.loginFn = func(appstore.LoginInput) error { return appstore.ErrAuthCodeRequired }
		},
		"2FA rejected": func(e *testEnv) {
			e.prompt.secrets = []string{"1"}
			e.as.loginFn = func(appstore.LoginInput) error { return appstore.ErrAuthCodeRequired }
		},
	}
	for _, s := range reScenarios() {
		for name, setup := range cases {
			t.Run(s.name+" "+name, func(t *testing.T) {
				e := newEnv(t, happy)
				e.ring.seed(savedJSON)
				s.expire(e)
				setup(e)
				wantExit(t, e.run(s.args...), 1, e)
				s.wantFailure(t, e, "error: "+s.step+": "+expiredOut)
				if strings.Contains(e.asked(), "email") {
					t.Fatalf("typed prompts: %q", e.asked())
				}
			})
		}
	}
}

func TestReloginNoItemMakesNoLoginCall(t *testing.T) {
	e := newEnv(t, happy)
	e.as.searchErr = errExpired
	wantExit(t, e.run("search", "x"), 1, e)
	wantErr(t, e, "error: search: "+expiredOut)
	if e.as.callList() != "search" {
		t.Fatalf("calls %q", e.as.callList())
	}
}

func TestReloginSecondExpiryOneLoginCall(t *testing.T) {
	for _, s := range reScenarios() {
		t.Run(s.name, func(t *testing.T) {
			e := newEnv(t, happy)
			e.ring.seed(savedJSON)
			s.expire(e) // never fixed
			logins := 0
			e.as.loginFn = func(appstore.LoginInput) error { logins++; return nil }
			wantExit(t, e.run(s.args...), 1, e)
			s.wantFailure(t, e, "error: "+s.step+": "+expiredOut)
			if logins != 1 {
				t.Fatalf("login calls %d", logins)
			}
		})
	}
}

func TestReloginRepeatedResultReturnedAsIs(t *testing.T) {
	// Download: expired, good re-login, then ErrNotOwned.
	e := newEnv(t, happy)
	e.ring.seed(savedJSON)
	n := 0
	e.as.downloadFn = func(appstore.DownloadInput) error {
		if n++; n == 1 {
			return errExpired
		}
		return appstore.ErrLicenseRequired
	}
	wantExit(t, e.run("install", "x", "--vm", "v1"), 1, e)
	wantStepErr(t, e, 1, "error: download: app not owned\nhint: re-run with --purchase\n")

	// Search: expired, good re-login, then another error.
	e = newEnv(t, happy)
	e.ring.seed(savedJSON)
	e.as.searchFn = func(n int) error {
		if n == 1 {
			return errExpired
		}
		return errors.New("boom")
	}
	wantExit(t, e.run("search", "x"), 1, e)
	wantErr(t, e, "error: search: boom\n")
}

func TestReloginSecondAuthCodeRequiredIsSessionExpired(t *testing.T) {
	e := newEnv(t, happy)
	e.ring.seed(savedJSON)
	e.as.searchErr = errExpired
	e.prompt.secrets = []string{"1", "2"}
	e.as.loginFn = func(appstore.LoginInput) error { return appstore.ErrAuthCodeRequired }
	wantExit(t, e.run("search", "x"), 1, e)
	wantErr(t, e, "error: search: "+expiredOut)
	if e.asked() != "Apple 2FA code: " {
		t.Fatalf("asked %q", e.asked())
	}
}

func TestReloginTwoFactorCodeUsed(t *testing.T) {
	e := newEnv(t, happy)
	e.ring.seed(savedJSON)
	e.as.searchErr = errExpired
	e.prompt.secrets = []string{"123456"}
	var codes []string
	e.as.loginFn = func(in appstore.LoginInput) error {
		codes = append(codes, in.AuthCode)
		if in.AuthCode == "" {
			return appstore.ErrAuthCodeRequired
		}
		e.as.searchErr = nil
		return nil
	}
	wantExit(t, e.run("search", "x"), 0, e)
	if strings.Join(codes, ",") != ",123456" || e.asked() != "Apple 2FA code: " {
		t.Fatalf("codes %q asked %q", codes, e.asked())
	}
}

// A ctx cancel during the re-login Login call gives the step's interrupt line.
func TestReloginInterruptDuringLogin(t *testing.T) {
	for _, s := range reScenarios() {
		t.Run(s.name, func(t *testing.T) {
			e := newEnv(t, happy)
			e.ring.seed(savedJSON)
			s.expire(e)
			ctx, cancel := context.WithCancel(context.Background())
			release := make(chan struct{})
			defer close(release)
			e.as.loginFn = func(appstore.LoginInput) error { cancel(); <-release; return nil }
			wantExit(t, e.runCtx(ctx, s.args...), 1, e)
			if got := e.errLines(); len(got) != 1 || got[0] != "error: "+s.step+": interrupted" {
				t.Fatalf("%q", e.errb)
			}
		})
	}
}

// SIGINT (pty) at the re-login code prompt gives the step's interrupt line.
func TestReloginPtyInterruptAtCodePrompt(t *testing.T) {
	master, slave := openPty(t)
	go io.Copy(io.Discard, master)
	e := newEnv(t, happy)
	out := &syncBuf{}
	e.a.prompt = &ttyPrompter{in: slave, out: out}
	e.a.stderr = out
	e.ring.seed(savedJSON)
	e.as.searchErr = errExpired
	e.as.loginFn = func(appstore.LoginInput) error { return appstore.ErrAuthCodeRequired }
	ctx, cancel := context.WithCancel(context.Background())
	code := make(chan int, 1)
	go func() { code <- e.runCtx(ctx, "search", "x") }()
	waitOut(t, out, "Apple 2FA code: ")
	cancel()
	if c := waitCode(t, code); c != 1 {
		t.Fatalf("exit %d", c)
	}
	if want := "Apple 2FA code: \nerror: search: interrupted\n"; out.String() != want {
		t.Fatalf("stderr %q", out.String())
	}
	if !echoOn(t, slave) {
		t.Fatal("echo not restored")
	}
}

// slowCode is a prompter whose code prompt takes longer than any step deadline.
type slowCode struct {
	*fakePrompt
	d time.Duration
}

func (p slowCode) Secret(ctx context.Context, prompt string) (string, error) {
	p.fakePrompt.asked = append(p.fakePrompt.asked, prompt)
	select {
	case <-time.After(p.d):
		return "123456", nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

// The code prompt is outside the step deadline, and the repeated call gets a
// fresh one.
func TestReloginPromptOutsideDeadlineAndFreshDeadline(t *testing.T) {
	e := newEnv(t, happy)
	e.ring.seed(savedJSON)
	e.a.to.Search = 150 * time.Millisecond
	e.a.to.Login = 5 * time.Second
	e.a.prompt = slowCode{e.prompt, 400 * time.Millisecond}
	e.as.searchFn = func(n int) error {
		time.Sleep(100 * time.Millisecond) // two calls would exceed one shared deadline
		if n == 1 {
			return errExpired
		}
		return nil
	}
	e.as.loginFn = func(in appstore.LoginInput) error {
		if in.AuthCode == "" {
			return appstore.ErrAuthCodeRequired
		}
		return nil
	}
	wantExit(t, e.run("search", "x"), 0, e)
	if !strings.HasPrefix(e.out.String(), "NAME") || strings.Contains(e.errb.String(), "timed out") {
		t.Fatalf("out %q err %q", e.out, e.errb)
	}
}

// ErrAuthCodeRequired is handled in signIn only.
func TestReloginAuthCodeHandledOnlyInSignIn(t *testing.T) {
	out, err := exec.Command("grep", "-rl", "ErrAuthCodeRequired", "--include=*.go", "--exclude=*_test.go", ".").Output()
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(out)); got != "./login.go" {
		t.Fatalf("files: %q", got)
	}
}

// reStub is a Store whose Search is scripted; Login and credentials succeed.
type reStub struct {
	Store
	search func(ctx context.Context, n int) error
	n      int
}

func (r *reStub) Search(ctx context.Context, _ string, _ int) ([]appstore.App, error) {
	r.n++
	return nil, r.search(ctx, r.n)
}
func (r *reStub) Login(context.Context, string, string, string) error { return nil }
func (r *reStub) LoadCredentials() (string, string, error)            { return "a@b.c", "pw", nil }

// The repeat has its own deadline: its expiry is reported as a timeout, and a
// non-timeout error is not turned into one by the outer step deadline.
func TestReloginRepeatTimeoutClassification(t *testing.T) {
	cases := map[string]struct {
		first  time.Duration // how long the first call runs before "session expired"
		repeat func(ctx context.Context) error
		want   string
	}{
		"repeat times out":                  {0, func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }, "error: search: timed out after 50ms\n"},
		"repeat fails after outer deadline": {80 * time.Millisecond, func(context.Context) error { return errors.New("boom") }, "error: search: boom\n"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t, happy)
			e.a.to.Search = 50 * time.Millisecond
			stub := &reStub{search: func(ctx context.Context, n int) error {
				if n == 1 {
					time.Sleep(c.first)
					return store.ErrSessionExpired
				}
				return c.repeat(ctx)
			}}
			e.a.newStore = func() (Store, error) { return stub, nil }
			wantExit(t, e.run("search", "x"), 1, e)
			wantErr(t, e, c.want)
		})
	}
}
