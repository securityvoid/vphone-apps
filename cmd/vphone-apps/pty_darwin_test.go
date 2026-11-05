//go:build darwin

package main

import (
	"context"
	"io"
	"os"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

// openPty returns a master/slave pair using the macOS ptmx interface.
func openPty(t *testing.T) (master, slave *os.File) {
	t.Helper()
	m, err := os.OpenFile("/dev/ptmx", os.O_RDWR, 0)
	if err != nil {
		t.Skip("no pty available:", err)
	}
	fd := m.Fd()
	if err := unix.IoctlSetInt(int(fd), unix.TIOCPTYGRANT, 0); err != nil {
		t.Fatal(err)
	}
	if err := unix.IoctlSetInt(int(fd), unix.TIOCPTYUNLK, 0); err != nil {
		t.Fatal(err)
	}
	var name [128]byte
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, fd, uintptr(unix.TIOCPTYGNAME), uintptr(unsafe.Pointer(&name[0]))); e != 0 {
		t.Fatal(e)
	}
	n := 0
	for name[n] != 0 {
		n++
	}
	s, err := os.OpenFile(string(name[:n]), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Fatal(err)
	}
	// Not closed: closing a pty while reads are blocked on it hangs on macOS;
	// the descriptors die with the test process.
	return m, s
}

func echoOn(t *testing.T, f *os.File) bool {
	t.Helper()
	tio, err := unix.IoctlGetTermios(int(f.Fd()), unix.TIOCGETA)
	if err != nil {
		t.Fatal(err)
	}
	return tio.Lflag&unix.ECHO != 0
}

func TestLoginPtyInterruptRestoresEcho(t *testing.T) {
	master, slave := openPty(t)
	go io.Copy(io.Discard, master) // drain prompts and echo
	e := newEnv(t, happy)
	e.a.prompt = &ttyPrompter{in: slave, out: e.errb}
	if !e.a.prompt.IsTerminal() {
		t.Fatal("pty slave is not a terminal")
	}
	master.Write([]byte("me@example.com\n"))
	ctx, cancel := context.WithCancel(context.Background())
	code := make(chan int, 1)
	go func() { code <- e.runCtx(ctx, "login") }()
	// Wait for the password prompt: echo is switched off.
	deadline := time.Now().Add(5 * time.Second)
	for echoOn(t, slave) {
		if time.Now().After(deadline) {
			t.Fatal("never reached the password prompt")
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	select {
	case c := <-code:
		if c != 1 {
			t.Fatalf("exit %d", c)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("login did not return after interrupt")
	}
	if !echoOn(t, slave) {
		t.Fatal("echo not restored")
	}
	if got := e.errb.String(); got != "Apple Account email: Apple Account password: \nerror: login: interrupted\n" {
		t.Fatalf("stderr %q", got)
	}
}

// A no-echo read is followed by "\n" on stderr (the Enter was not echoed).
func TestLoginPtyNewlineAfterSecret(t *testing.T) {
	master, slave := openPty(t)
	go io.Copy(io.Discard, master)
	e := newEnv(t, happy)
	e.a.prompt = &ttyPrompter{in: slave, out: e.errb}
	master.Write([]byte("me@example.com\n"))
	code := make(chan int, 1)
	go func() { code <- e.run("login") }()
	deadline := time.Now().Add(5 * time.Second)
	for echoOn(t, slave) {
		if time.Now().After(deadline) {
			t.Fatal("never reached the password prompt")
		}
		time.Sleep(5 * time.Millisecond)
	}
	master.Write([]byte("SENTINEL-PW\n"))
	select {
	case c := <-code:
		wantExit(t, c, 0, e)
	case <-time.After(3 * time.Second):
		t.Fatal("login did not return")
	}
	if got := e.errb.String(); got != "Apple Account email: Apple Account password: \n" {
		t.Fatalf("stderr %q", got)
	}
}

// syncBuf is a buffer safe for the prompt goroutine and the test.
type syncBuf struct {
	mu sync.Mutex
	b  strings.Builder
}

func (s *syncBuf) Write(p []byte) (int, error) { s.mu.Lock(); defer s.mu.Unlock(); return s.b.Write(p) }
func (s *syncBuf) String() string              { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }

// An interrupt at the email (line) prompt ends the prompt line with "\n".
func TestLoginPtyInterruptAtEmail(t *testing.T) {
	_, slave := openPty(t)
	e := newEnv(t, happy)
	out := &syncBuf{}
	e.a.prompt = &ttyPrompter{in: slave, out: out}
	e.a.stderr = out
	ctx, cancel := context.WithCancel(context.Background())
	code := make(chan int, 1)
	go func() { code <- e.runCtx(ctx, "login") }()
	deadline := time.Now().Add(5 * time.Second)
	for out.String() != "Apple Account email: " {
		if time.Now().After(deadline) {
			t.Fatalf("no email prompt: %q", out.String())
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	select {
	case c := <-code:
		if c != 1 {
			t.Fatalf("exit %d", c)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("login did not return after interrupt")
	}
	if got := out.String(); got != "Apple Account email: \nerror: login: interrupted\n" {
		t.Fatalf("stderr %q", got)
	}
}
