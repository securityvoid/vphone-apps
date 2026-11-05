package main

import (
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// procHelper stands in for sudo and open (VPA_HELPER=proc, behaviour in
// VPA_MODE, files under VPA_MARK).
func procHelper() int {
	mark := os.Getenv("VPA_MARK")
	switch os.Getenv("VPA_MODE") {
	case "mark":
		os.WriteFile(mark, []byte(strings.Join(os.Args[1:], " ")), 0o600)
		return 0
	case "sleep":
		os.WriteFile(mark, []byte(strconv.Itoa(os.Getpid())), 0o600)
		time.Sleep(time.Minute)
	case "short":
		os.WriteFile(mark, nil, 0o600)
		time.Sleep(300 * time.Millisecond)
	case "signal":
		ch := make(chan os.Signal, 1)
		signalNotify(ch)
		os.WriteFile(mark, nil, 0o600)
		s := <-ch
		os.WriteFile(mark+".sig", []byte(s.String()), 0o600)
		return 7
	}
	return 0
}

type helperEnv struct {
	r    *procRunner
	mark string
}

func newHelper(t *testing.T, mode string) *helperEnv {
	t.Helper()
	mark := filepath.Join(t.TempDir(), "mark")
	t.Setenv("VPA_HELPER", "proc")
	t.Setenv("VPA_MODE", mode)
	t.Setenv("VPA_MARK", mark)
	return &helperEnv{&procRunner{sudoBin: os.Args[0], openBin: os.Args[0]}, mark}
}

func (h *helperEnv) started() bool { _, err := os.Stat(h.mark); return err == nil }

func waitFile(t *testing.T, path string) {
	t.Helper()
	for end := time.Now().Add(5 * time.Second); time.Now().Before(end); time.Sleep(10 * time.Millisecond) {
		if _, err := os.Stat(path); err == nil {
			return
		}
	}
	t.Fatalf("%s never appeared", path)
}

func TestExecAllowlist(t *testing.T) {
	self, _ := os.Executable()
	const url = "macappstore://apps.apple.com/app/id12"
	h := newHelper(t, "mark")
	for _, c := range []struct {
		path string
		args []string
	}{
		{"/bin/ls", nil},
		{"/bin/ls", []string{self, "setup"}},
		{sudoPath, []string{"/bin/sh", "-c", "x"}},
		{sudoPath, []string{self, "other"}},
		{sudoPath, []string{self, "setup"}},
		{sudoPath, []string{"-p", "other", self, "setup"}},
		{sudoPath, nil},
		{openPath, []string{"-a", "Calculator"}},
		{openPath, []string{url, url}},
		{openPath, []string{"https://apps.apple.com/app/id1"}},
		{openPath, []string{"macappstore://apps.apple.com/app/id12;x"}},
		{openPath, []string{"macappstore://apps.apple.com/app/id"}},
	} {
		if _, err := h.r.Run(context.Background(), c.path, c.args, false); err == nil || h.started() {
			t.Errorf("%s %v: err %v started %v", c.path, c.args, err, h.started())
		}
	}
	for path, args := range map[string][]string{sudoPath: {"-p", sudoPrompt, self, "setup", "--elevated"}, openPath: {url}} {
		os.Remove(h.mark)
		if code, err := h.r.Run(context.Background(), path, args, false); err != nil || code != 0 || !h.started() {
			t.Errorf("%s: %d %v", path, code, err)
		}
	}
}

func TestExecAllowlistSignals(t *testing.T) {
	self, _ := os.Executable()
	for _, sig := range []syscall.Signal{syscall.SIGINT, syscall.SIGTERM} {
		t.Run(sig.String(), func(t *testing.T) {
			h := newHelper(t, "signal")
			done := make(chan int, 1)
			go func() {
				code, _ := h.r.Run(context.Background(), sudoPath, []string{"-p", sudoPrompt, self, "setup"}, true)
				done <- code
			}()
			waitFile(t, h.mark)
			time.Sleep(50 * time.Millisecond) // the runner registered before Wait
			syscall.Kill(os.Getpid(), sig)
			select {
			case code := <-done:
				if code != 7 {
					t.Fatalf("exit %d", code)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("Run did not return")
			}
			got, _ := os.ReadFile(h.mark + ".sig")
			if string(got) != sig.String() {
				t.Fatalf("child got %q", got)
			}
		})
	}
}

func TestExecAllowlistContext(t *testing.T) {
	const url = "macappstore://apps.apple.com/app/id12"
	self, _ := os.Executable()
	gone := func(t *testing.T, h *helperEnv) {
		b, _ := os.ReadFile(h.mark)
		pid, _ := strconv.Atoi(string(b))
		for end := time.Now().Add(2 * time.Second); time.Now().Before(end); time.Sleep(10 * time.Millisecond) {
			if syscall.Kill(pid, 0) != nil {
				return
			}
		}
		t.Fatalf("helper %d still running", pid)
	}
	t.Run("open deadline", func(t *testing.T) {
		h := newHelper(t, "sleep")
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()
		start := time.Now()
		_, err := h.r.Run(ctx, openPath, []string{url}, false)
		if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > 2*time.Second {
			t.Fatal(err)
		}
		gone(t, h)
	})
	t.Run("open cancel", func(t *testing.T) {
		h := newHelper(t, "sleep")
		ctx, cancel := context.WithCancel(context.Background())
		go func() { waitFile(t, h.mark); cancel() }()
		_, err := h.r.Run(ctx, openPath, []string{url}, false)
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		gone(t, h)
	})
	t.Run("sudo survives cancel", func(t *testing.T) {
		h := newHelper(t, "short")
		ctx, cancel := context.WithCancel(context.Background())
		go func() { waitFile(t, h.mark); cancel() }()
		if code, err := h.r.Run(ctx, sudoPath, []string{"-p", sudoPrompt, self, "setup"}, true); code != 0 || err != nil {
			t.Fatal(code, err)
		}
	})
}

// TestExecAllowlistSource fails when a non-test file other than proc.go
// imports os/exec or any file names a /usr/bin program besides the two.
func TestExecAllowlistSource(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == ".git" || d.Name() == ".claude") && p != root {
			return filepath.SkipDir
		}
		if d.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(token.NewFileSet(), p, nil, 0)
		if err != nil {
			return err
		}
		checked++
		for _, imp := range f.Imports {
			if imp.Path.Value == `"os/exec"` && filepath.Base(p) != "proc.go" {
				t.Errorf("%s imports os/exec", p)
			}
		}
		ast.Inspect(f, func(n ast.Node) bool {
			if l, ok := n.(*ast.BasicLit); ok && l.Kind == token.STRING && strings.Contains(l.Value, "/usr/bin/") {
				if l.Value != `"/usr/bin/sudo"` && l.Value != `"/usr/bin/open"` {
					t.Errorf("%s: program literal %s", p, l.Value)
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if checked == 0 {
		t.Fatal("no Go files found; root is wrong")
	}
}

func signalNotify(ch chan<- os.Signal) { signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM) }
