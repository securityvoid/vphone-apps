package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/majd/ipatool/v2/pkg/appstore"
)

const (
	savePrompt = "Save credentials to Keychain for reuse? [y/N] "
	savedJSON  = `{"email":"saved@example.com","password":"saved-pw"}`
	typedJSON  = `{"email":"me@example.com","password":"SENTINEL-PW"}`
)

func (e *testEnv) asked() string { return strings.Join(e.prompt.asked, "|") }

// seedStale saves an item whose sign-in fails, so the typed path runs with hadItem.
func seedStale(e *testEnv) {
	e.ring.seed(staleJSON)
	e.as.loginFn = func(in appstore.LoginInput) error {
		if in.Email == "old@example.com" {
			return errors.New("stale")
		}
		return nil
	}
}

const staleJSON = `{"email":"old@example.com","password":"old"}`

func TestLoginCredsSaveAnswers(t *testing.T) {
	for _, tc := range []struct {
		ans   string
		saved bool
	}{{"y", true}, {"YES", true}, {"yes", true}, {"n", false}, {"", false}, {"maybe", false}} {
		for _, had := range []bool{false, true} {
			t.Run(fmt.Sprintf("%q item=%v", tc.ans, had), func(t *testing.T) {
				e := newEnv(t, happy)
				e.prompt.lines = append(e.prompt.lines, tc.ans)
				if had {
					// A stale item: signing in with it fails, so the typed path runs.
					seedStale(e)
					e.prompt.lines = []string{"me@example.com", tc.ans}
				}
				wantExit(t, e.run("login"), 0, e)
				want := ""
				if tc.saved {
					want = typedJSON
				}
				if got := e.ring.item(); got != want {
					t.Fatalf("item %q want %q", got, want)
				}
			})
		}
	}
}

func TestLoginCredsEOFAtSavePrompt(t *testing.T) {
	e := newEnv(t, happy) // the fake prompter answers EOF
	seedStale(e)
	wantExit(t, e.run("login"), 0, e)
	if e.ring.item() != "" {
		t.Fatal("EOF must delete the item")
	}
	wantErr(t, e, "error: login: stale\n")
}

func TestLoginCredsSetAndRemoveFailures(t *testing.T) {
	e := newEnv(t, happy)
	e.prompt.lines = append(e.prompt.lines, "y")
	e.ring.setErr = errors.New("denied")
	wantExit(t, e.run("login"), 1, e)
	wantErr(t, e, "error: login: cannot save credentials: denied\n")
	if e.run_.count() != 0 {
		t.Fatal("auto-setup ran")
	}

	e = newEnv(t, happy)
	e.ring.seed("not json")
	e.ring.removeErr = errors.New("locked")
	e.prompt.lines = append(e.prompt.lines, "n")
	wantExit(t, e.run("login"), 1, e)
	wantErr(t, e, "error: login: "+"saved credentials are malformed or incomplete\n"+"error: login: cannot delete saved credentials: locked\n")
	if e.run_.count() != 0 {
		t.Fatal("auto-setup ran")
	}
}

func TestLoginCredsSavedSignInNoQuestion(t *testing.T) {
	e := newEnv(t, happy)
	e.ring.seed(savedJSON)
	var got appstore.LoginInput
	e.as.loginFn = func(in appstore.LoginInput) error { got = in; return nil }
	e.prompt.lines, e.prompt.secrets = nil, nil
	wantExit(t, e.run("login"), 0, e)
	if got.Email != "saved@example.com" || got.Password != "saved-pw" || e.asked() != "" || e.errb.Len() != 0 {
		t.Fatalf("%+v asked=%q err=%q", got, e.asked(), e.errb)
	}
	if e.ring.item() != savedJSON {
		t.Fatal("item changed")
	}
}

func TestLoginCredsSavedSignInTwoFactor(t *testing.T) {
	e := newEnv(t, happy)
	e.ring.seed(savedJSON)
	e.prompt.lines, e.prompt.secrets = nil, []string{"123456"}
	e.as.loginFn = func(in appstore.LoginInput) error {
		if in.AuthCode == "" {
			return appstore.ErrAuthCodeRequired
		}
		return nil
	}
	wantExit(t, e.run("login"), 0, e)
	if e.asked() != "Apple 2FA code: " {
		t.Fatalf("%q", e.asked())
	}
}

func TestLoginCredsSavedSignInFailuresKeepItem(t *testing.T) {
	cases := []struct {
		name  string
		setup func(e *testEnv) context.Context
		want  string
	}{
		{"rejected code", func(e *testEnv) context.Context {
			e.prompt.secrets = []string{"1", "2"}
			e.as.loginFn = func(appstore.LoginInput) error { return appstore.ErrAuthCodeRequired }
			return context.Background()
		}, "error: login: two-factor code rejected\n"},
		{"deadline", func(e *testEnv) context.Context {
			e.a.to.Login = 50 * time.Millisecond
			e.as.loginFn = func(appstore.LoginInput) error { time.Sleep(time.Second); return nil }
			return context.Background()
		}, "error: login: timed out after 50ms\n"},
		{"interrupt", func(e *testEnv) context.Context {
			ctx, cancel := context.WithCancel(context.Background())
			e.as.loginFn = func(appstore.LoginInput) error { cancel(); time.Sleep(time.Second); return nil }
			return ctx
		}, "error: login: interrupted\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t, happy)
			e.ring.seed(savedJSON)
			e.prompt.lines, e.prompt.secrets = nil, nil
			ctx := tc.setup(e)
			wantExit(t, e.runCtx(ctx, "login"), 1, e)
			wantErr(t, e, tc.want)
			if strings.Contains(e.asked(), "email") || strings.Contains(e.asked(), "password") || strings.Contains(e.asked(), "Save") {
				t.Fatalf("typed prompts shown: %q", e.asked())
			}
			if e.ring.item() != savedJSON || e.run_.count() != 0 {
				t.Fatal("item changed or setup ran")
			}
		})
	}
}

func TestLoginCredsFallbackAfterFailedSavedSignIn(t *testing.T) {
	for name, loginErr := range map[string]error{
		"stale password": errors.New("Your Apple Account or password is incorrect"),
		"network error":  errors.New("dial tcp: no route to host"),
	} {
		for _, ans := range []string{"y", "n"} {
			t.Run(name+" "+ans, func(t *testing.T) {
				e := newEnv(t, happy)
				e.ring.seed(savedJSON)
				e.prompt.lines = []string{"me@example.com", ans}
				e.as.loginFn = func(in appstore.LoginInput) error {
					if in.Email == "saved@example.com" {
						return loginErr
					}
					return nil
				}
				wantExit(t, e.run("login"), 0, e)
				wantErr(t, e, "error: login: "+loginErr.Error()+"\n")
				if e.asked() != "Apple Account email: |Apple Account password: |"+savePrompt {
					t.Fatalf("%q", e.asked())
				}
				want := ""
				if ans == "y" {
					want = typedJSON
				}
				if e.ring.item() != want {
					t.Fatalf("item %q", e.ring.item())
				}
			})
		}
	}
}

func TestLoginCredsFallbackTypedFailureExitsOne(t *testing.T) {
	e := newEnv(t, happy)
	e.ring.seed(savedJSON)
	e.as.loginFn = func(appstore.LoginInput) error { return errors.New("nope") }
	wantExit(t, e.run("login"), 1, e)
	wantErr(t, e, "error: login: nope\nerror: login: nope\n")
	if strings.Contains(e.asked(), "Save") || e.ring.item() != savedJSON {
		t.Fatal("save question after failed typed sign-in, or item changed")
	}
}

func TestLoginCredsBadItems(t *testing.T) {
	bad := []string{"not json", `{"email":"","password":"x"}`, `{"email":"a","password":""}`}
	for _, item := range bad {
		for _, ans := range []string{"y", "n"} {
			t.Run(item+ans, func(t *testing.T) {
				e := newEnv(t, happy)
				e.ring.seed(item)
				e.prompt.lines = []string{"me@example.com", ans}
				wantExit(t, e.run("login"), 0, e)
				wantErr(t, e, "error: login: saved credentials are malformed or incomplete\n")
				if e.asked() != "Apple Account email: |Apple Account password: |"+savePrompt {
					t.Fatalf("%q", e.asked())
				}
				want := ""
				if ans == "y" {
					want = typedJSON
				}
				if e.ring.item() != want {
					t.Fatalf("item %q", e.ring.item())
				}
			})
		}
	}
}

func TestLoginCredsKeyringReadError(t *testing.T) {
	e := newEnv(t, happy)
	e.ring.getErr = errors.New("keychain locked")
	wantExit(t, e.run("login"), 1, e)
	wantErr(t, e, "error: login: keychain locked\n")
	if e.as.callList() != "" || e.asked() != "" {
		t.Fatalf("store=%q asked=%q", e.as.callList(), e.asked())
	}
}

func TestLoginCredsFailedTypedSignInNoQuestion(t *testing.T) {
	e := newEnv(t, happy)
	e.as.loginFn = func(appstore.LoginInput) error { return errors.New("bad") }
	wantExit(t, e.run("login"), 1, e)
	if strings.Contains(e.asked(), "Save") {
		t.Fatal("asked the save question")
	}
}
