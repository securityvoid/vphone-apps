package main

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/majd/ipatool/v2/pkg/appstore"
)

func waitOut(t *testing.T, out *syncBuf, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !strings.HasSuffix(out.String(), want) {
		if time.Now().After(deadline) {
			t.Fatalf("never saw %q: %q", want, out.String())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func waitCode(t *testing.T, code chan int) int {
	t.Helper()
	select {
	case c := <-code:
		return c
	case <-time.After(3 * time.Second):
		t.Fatal("login did not return")
		return -1
	}
}

// ptyLoginToSavePrompt runs a typed sign-in on a real pty up to the save prompt.
func ptyLoginToSavePrompt(t *testing.T, ctx context.Context, stale bool) (*testEnv, *os.File, *syncBuf, chan int) {
	master, slave := openPty(t)
	go io.Copy(io.Discard, master)
	e := newEnv(t, happy)
	if stale {
		seedStale(e)
	}
	out := &syncBuf{}
	e.a.prompt = &ttyPrompter{in: slave, out: out}
	e.a.stderr = out
	code := make(chan int, 1)
	go func() { code <- e.runCtx(ctx, "login") }()
	waitOut(t, out, "Apple Account email: ")
	master.Write([]byte("me@example.com\n"))
	waitOut(t, out, "Apple Account password: ")
	master.Write([]byte("SENTINEL-PW\n"))
	waitOut(t, out, savePrompt)
	return e, master, out, code
}

func TestLoginCredsPtyCtrlCAtSavePrompt(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	e, _, out, code := ptyLoginToSavePrompt(t, ctx, true)
	cancel()
	if c := waitCode(t, code); c != 1 {
		t.Fatalf("exit %d", c)
	}
	want := "error: login: stale\nApple Account email: Apple Account password: \n" + savePrompt + "\nerror: login: interrupted\n"
	if out.String() != want {
		t.Fatalf("stderr %q", out.String())
	}
	if e.ring.item() != staleJSON || e.run_.count() != 0 {
		t.Fatal("item changed or setup ran")
	}
}

func TestLoginCredsPtyEOFAtSavePrompt(t *testing.T) {
	e, master, out, code := ptyLoginToSavePrompt(t, context.Background(), false)
	master.Write([]byte{4}) // Ctrl-D
	if c := waitCode(t, code); c != 0 {
		t.Fatalf("exit %d", c)
	}
	if want := "Apple Account email: Apple Account password: \n" + savePrompt; out.String() != want {
		t.Fatalf("stderr %q", out.String())
	}
	if e.ring.item() != "" {
		t.Fatal("saved on EOF")
	}
}

// An interrupt at the 2FA code prompt of a saved sign-in exits 1 and keeps the item.
func TestLoginCredsPtyInterruptAtSavedTwoFactor(t *testing.T) {
	master, slave := openPty(t)
	go io.Copy(io.Discard, master)
	e := newEnv(t, happy)
	out := &syncBuf{}
	e.a.prompt = &ttyPrompter{in: slave, out: out}
	e.a.stderr = out
	e.ring.seed(savedJSON)
	e.as.loginFn = func(appstore.LoginInput) error { return appstore.ErrAuthCodeRequired }
	ctx, cancel := context.WithCancel(context.Background())
	code := make(chan int, 1)
	go func() { code <- e.runCtx(ctx, "login") }()
	waitOut(t, out, "Apple 2FA code: ")
	cancel()
	if c := waitCode(t, code); c != 1 {
		t.Fatalf("exit %d", c)
	}
	if want := "Apple 2FA code: \nerror: login: interrupted\n"; out.String() != want {
		t.Fatalf("stderr %q", out.String())
	}
	if e.ring.item() != savedJSON || !echoOn(t, slave) {
		t.Fatal("item changed or echo not restored")
	}
}
