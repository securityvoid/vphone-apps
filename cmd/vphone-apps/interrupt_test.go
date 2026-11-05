package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/DrWhax/unfairplay/pkg/fairplay"
	"github.com/majd/ipatool/v2/pkg/appstore"
	"github.com/securityvoid/vphone-apps/internal/cache"
	"github.com/securityvoid/vphone-apps/internal/store"
	"github.com/securityvoid/vphone-apps/internal/vm"
)

// TestMain doubles as the helper process for TestInstallInterrupt: the test
// binary re-executes itself so real signals hit a real process.
func TestMain(m *testing.M) {
	switch os.Getenv("VPA_HELPER") {
	case "1":
		os.Exit(helperMain())
	case "proc":
		os.Exit(procHelper())
	}
	os.Exit(m.Run())
}

func helperMain() int {
	mark := os.Getenv("VPA_MARK")
	as := &fakeAS{searchRes: []appstore.App{appFoo}}
	as.downloadFn = func(in appstore.DownloadInput) error {
		os.WriteFile(in.OutputPath, []byte("partial"), 0o600)
		os.WriteFile(mark, nil, 0o600)
		select {} // a download that never finishes and ignores ctx
	}
	a := newApp()
	a.newStore = func() (Store, error) { return store.NewWithAppStore(as), nil }
	a.newCache = func() (*cache.Cache, error) { return cache.NewIn(os.Getenv("VPA_CACHE")) }
	a.dialVM = func(n string, d time.Duration) (*vm.Machine, error) {
		return vm.DialInWithTimeout(os.Getenv("VPA_ROOT"), n, d)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	return run(a, ctx, []string{"install", "Foo", "--vm", "v1"})
}

func TestInstallInterrupt(t *testing.T) {
	for _, sig := range []syscall.Signal{syscall.SIGINT, syscall.SIGTERM} {
		t.Run(sig.String(), func(t *testing.T) {
			e := newEnv(t, happy)
			mark := filepath.Join(t.TempDir(), "mark")
			cmd := exec.Command(os.Args[0], "-test.run=NoSuchTest")
			cmd.Env = append(os.Environ(), "VPA_HELPER=1", "VPA_MARK="+mark, "VPA_CACHE="+e.cache, "VPA_ROOT="+e.srv.root)
			var stderr strings.Builder
			cmd.Stderr = &stderr
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			deadline := time.Now().Add(10 * time.Second)
			for {
				if _, err := os.Stat(mark); err == nil {
					break
				}
				if time.Now().After(deadline) {
					cmd.Process.Kill()
					t.Fatal("helper never reached download")
				}
				time.Sleep(10 * time.Millisecond)
			}
			cmd.Process.Signal(sig)
			done := make(chan error, 1)
			go func() { done <- cmd.Wait() }()
			select {
			case err := <-done:
				var ee *exec.ExitError
				if !errors.As(err, &ee) || ee.ExitCode() != 1 {
					t.Fatalf("exit: %v\n%s", err, stderr.String())
				}
			case <-time.After(5 * time.Second):
				cmd.Process.Kill()
				t.Fatal("did not exit after signal")
			}
			if want := installing + "[1/4] Downloading\nerror: download: interrupted\n"; stderr.String() != want {
				t.Fatalf("stderr %q", stderr.String())
			}
			if es := e.cacheEntries(t); len(es) != 0 {
				t.Fatalf("leftovers %v", es)
			}
		})
	}
}

// cancelAfter cancels ctx shortly after the signal channel fires.
func cancelSoon(cancel context.CancelFunc) {
	go func() { time.Sleep(60 * time.Millisecond); cancel() }()
}

func within2s(t *testing.T, f func()) {
	t.Helper()
	t0 := time.Now()
	f()
	if time.Since(t0) > 2*time.Second {
		t.Fatalf("took %v", time.Since(t0))
	}
}

func TestInterruptRows(t *testing.T) {
	never := func(marker string) func(string) string {
		return func(r string) string {
			if strings.Contains(r, marker) {
				return ""
			}
			return happy(r)
		}
	}
	cases := []struct {
		name   string
		handle func(string) string
		args   []string
		step   int // step running when the interrupt lands
		want   string
	}{
		{"push", never(`"t":"push"`), []string{"install", "Foo", "--vm", "v1"}, 3,
			"error: push: interrupted\n" + stagedHint},
		{"install", never("apps.install"), []string{"install", "Foo", "--vm", "v1"}, 4,
			"error: install: interrupted; the install may still be running in the guest\n" + checkHint},
		{"launch", never("apps.launch"), []string{"install", "Foo", "--vm", "v1", "--launch"}, 5,
			"error: launch: interrupted\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newEnv(t, c.handle)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			cancelSoon(cancel)
			within2s(t, func() { wantExit(t, e.runCtx(ctx, c.args...), 1, e) })
			wantStepErr(t, e, c.step, c.want)
			if es := e.cacheEntries(t); len(es) != 0 {
				t.Fatalf("leftovers %v", es)
			}
		})
	}
}

func TestInterruptInGapAfterPushReply(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	e := newEnv(t, func(r string) string {
		if strings.Contains(r, `"t":"push"`) {
			cancel() // the interrupt lands as the push is answered
			return `{"ok":true}` + "\n"
		}
		return happy(r)
	})
	within2s(t, func() { wantExit(t, e.runCtx(ctx, "install", "Foo", "--vm", "v1"), 1, e) })
	// The cancel races the push reply, so "Copying to VM" or "Installing" is the last step line.
	tail := "error: push: interrupted\n" + stagedHint
	if got := e.errb.String(); got != installing+"[1/4] Downloading\n[2/4] Decrypting\n[3/4] Copying to VM\n"+tail &&
		got != installing+"[1/4] Downloading\n[2/4] Decrypting\n[3/4] Copying to VM\n[4/4] Installing\n"+tail {
		t.Fatalf("stderr %q", got)
	}
	if len(e.srv.requests()) != 1 {
		t.Fatal("apps.install must not be sent after the interrupt")
	}
}

func TestInterruptSearchAndSelect(t *testing.T) {
	for _, args := range [][]string{{"search", "x"}, {"install", "x", "--vm", "v1"}} {
		e := newEnv(t, happy)
		ch := make(chan struct{})
		defer close(ch)
		e.a.newStore = blockingStore(e, ch)
		ctx, cancel := context.WithCancel(context.Background())
		cancelSoon(cancel)
		within2s(t, func() { wantExit(t, e.runCtx(ctx, args...), 1, e) })
		step := map[string]string{"search": "search", "install": "select"}[args[0]]
		wantErr(t, e, "error: "+step+": interrupted\n")
	}
}

func TestInterruptSlowDecrypt(t *testing.T) {
	e := newEnv(t, happy)
	release := make(chan struct{})
	defer close(release)
	e.decryptFn = func(o fairplay.DecryptOptions) (fairplay.DecryptResult, error) {
		os.WriteFile(o.OutputPath, []byte("x"), 0o644)
		<-release
		return fairplay.DecryptResult{}, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancelSoon(cancel)
	within2s(t, func() { wantExit(t, e.runCtx(ctx, "install", "Foo", "--vm", "v1"), 1, e) })
	wantStepErr(t, e, 2, "error: decrypt: interrupted\n")
	if es := e.cacheEntries(t); len(es) != 0 {
		t.Fatalf("leftovers %v", es)
	}
}

// A download that ignores ctx and keeps writing must not wedge cleanup.
func TestInterruptDownloadIgnoringCtx(t *testing.T) {
	e := newEnv(t, happy)
	var stop atomic.Bool
	defer stop.Store(true)
	e.as.downloadFn = func(in appstore.DownloadInput) error {
		for !stop.Load() {
			os.WriteFile(in.OutputPath, []byte("more"), 0o600)
			time.Sleep(time.Millisecond)
		}
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancelSoon(cancel)
	within2s(t, func() { wantExit(t, e.runCtx(ctx, "install", "Foo", "--vm", "v1"), 1, e) })
	wantStepErr(t, e, 1, "error: download: interrupted\n")
}
