package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/byteness/keyring"
	"github.com/majd/ipatool/v2/pkg/appstore"
)

func TestLoginSuccessPromptsOnlyAndNoStdout(t *testing.T) {
	e := newEnv(t, happy)
	wantExit(t, e.run("login"), 0, e)
	if e.out.Len() != 0 || e.errb.Len() != 0 {
		t.Fatalf("out=%q err=%q", e.out, e.errb)
	}
	if fmt.Sprint(e.prompt.asked) != "[Apple Account email:  Apple Account password: ]" {
		t.Fatalf("%q", e.prompt.asked)
	}
}

func TestLoginTwoFactorReprompt(t *testing.T) {
	e := newEnv(t, happy)
	e.prompt.secrets = append(e.prompt.secrets, "123456")
	n := 0
	e.as.loginFn = func(in appstore.LoginInput) error {
		n++
		if in.AuthCode == "" {
			return appstore.ErrAuthCodeRequired
		}
		if in.AuthCode != "123456" {
			t.Errorf("code %q", in.AuthCode)
		}
		return nil
	}
	wantExit(t, e.run("login"), 0, e)
	if n != 2 || e.prompt.asked[len(e.prompt.asked)-1] != "Apple 2FA code: " {
		t.Fatal(n, e.prompt.asked)
	}
}

func TestLoginTwoFactorRejected(t *testing.T) {
	e := newEnv(t, happy)
	e.prompt.secrets = append(e.prompt.secrets, "1", "2")
	n := 0
	e.as.loginFn = func(appstore.LoginInput) error { n++; return appstore.ErrAuthCodeRequired }
	wantExit(t, e.run("login"), 1, e)
	wantErr(t, e, "error: login: two-factor code rejected\n")
	if n != 2 || len(e.prompt.secrets) != 1 {
		t.Fatalf("n=%d secrets left %v", n, e.prompt.secrets)
	}
}

func TestLoginWrongPassword(t *testing.T) {
	e := newEnv(t, happy)
	e.as.loginFn = func(appstore.LoginInput) error { return errors.New("Your Apple Account or password is incorrect") }
	wantExit(t, e.run("login"), 1, e)
	wantErr(t, e, "error: login: Your Apple Account or password is incorrect\n")
}

func TestLoginNeedsTerminal(t *testing.T) {
	e := newEnv(t, happy)
	e.prompt.tty = false
	wantExit(t, e.run("login"), 1, e)
	wantErr(t, e, "error: login: a terminal is required\n")
	if e.as.callList() != "" || len(e.prompt.asked) != 0 {
		t.Fatal("store or prompt touched")
	}
}

func TestLoginInterruptAtPrompt(t *testing.T) {
	e := newEnv(t, happy)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	e.prompt.err = context.Canceled
	wantExit(t, e.runCtx(ctx, "login"), 1, e)
	wantErr(t, e, "error: login: interrupted\n")
}

func TestLoginInterruptAtNetwork(t *testing.T) {
	e := newEnv(t, happy)
	ctx, cancel := context.WithCancel(context.Background())
	release := make(chan struct{})
	defer close(release)
	e.as.loginFn = func(appstore.LoginInput) error { <-release; return nil }
	go func() { time.Sleep(50 * time.Millisecond); cancel() }()
	wantExit(t, e.runCtx(ctx, "login"), 1, e)
	wantErr(t, e, "error: login: interrupted\n")
}

func TestLoginTimeout(t *testing.T) {
	e := newEnv(t, happy)
	e.a.to.Login = 50 * time.Millisecond
	release := make(chan struct{})
	defer close(release)
	e.as.loginFn = func(appstore.LoginInput) error { <-release; return nil }
	wantExit(t, e.run("login"), 1, e)
	wantErr(t, e, "error: login: timed out after 50ms\n")
}

// Session-expired mapping through search and install.
func TestSessionExpiredThroughCommands(t *testing.T) {
	want := "error: %s: session expired\nhint: run vphone-apps login\n"
	e := newEnv(t, happy)
	e.as.searchErr = appstore.ErrPasswordTokenExpired
	wantExit(t, e.run("search", "x"), 1, e)
	wantErr(t, e, fmt.Sprintf(want, "search"))

	e = newEnv(t, happy)
	e.as.searchErr = appstore.ErrPasswordTokenExpired
	wantExit(t, e.run("install", "x", "--vm", "v1"), 1, e)
	wantErr(t, e, fmt.Sprintf(want, "select"))

	e = newEnv(t, happy)
	e.as.lookupErr = appstore.ErrPasswordTokenExpired
	wantExit(t, e.run("install", "--id", "com.example.app", "--vm", "v1"), 1, e)
	wantErr(t, e, fmt.Sprintf(want, "select"))

	e = newEnv(t, happy)
	e.as.downloadFn = func(appstore.DownloadInput) error { return appstore.ErrPasswordTokenExpired }
	wantExit(t, e.run("install", "x", "--vm", "v1"), 1, e)
	wantStepErr(t, e, 1, fmt.Sprintf(want, "download"))

	e = newEnv(t, happy)
	e.as.purchase = func() error { return appstore.ErrPasswordTokenExpired }
	wantExit(t, e.run("install", "x", "--vm", "v1", "--purchase"), 1, e)
	wantStepErr(t, e, 1, fmt.Sprintf(want, "download"))
	if strings.Contains(e.as.callList(), "download") {
		t.Fatal("download after failed purchase")
	}
}

func TestNotLoggedIn(t *testing.T) {
	e := newEnv(t, happy)
	e.as.acctErr = fmt.Errorf("failed to get account: %w", keyring.ErrKeyNotFound)
	wantExit(t, e.run("search", "x"), 1, e)
	wantErr(t, e, "error: search: not logged in\nhint: run vphone-apps login\n")
}
