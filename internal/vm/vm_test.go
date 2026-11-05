package vm

import (
	"bufio"
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// fakeVM serves machine "v1" under a short temp root (unix paths are short).
type fakeVM struct {
	root string
	ln   *net.UnixListener
	reqs chan string
}

func newFakeVM(t *testing.T, handle func(req string, c net.Conn)) *fakeVM {
	t.Helper()
	root, err := os.MkdirTemp("/tmp", "vmt")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	os.MkdirAll(filepath.Join(root, "v1"), 0o700)
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: filepath.Join(root, "v1", "vphone.sock"), Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeVM{root: root, ln: ln, reqs: make(chan string, 8)}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				// A bare connect/close (the Dial probe) sends nothing.
				line, err := bufio.NewReader(c).ReadString('\n')
				if err != nil {
					return
				}
				f.reqs <- line
				handle(line, c)
			}()
		}
	}()
	return f
}

func sayf(s string) func(string, net.Conn) {
	return func(_ string, c net.Conn) { c.Write([]byte(s)) }
}

func TestDialNames(t *testing.T) {
	root := t.TempDir()
	for _, n := range []string{"", ".", "a/b", "../x", "a..b", strings.Repeat("a", 65), "a\x00b"} {
		if _, err := DialInWithTimeout(root, n, 0); !errors.Is(err, ErrInvalidName) {
			t.Errorf("%q: %v", n, err)
		}
	}
}

func TestDialNoSuchAndNotRunning(t *testing.T) {
	root, _ := os.MkdirTemp("/tmp", "vmt")
	defer os.RemoveAll(root)
	if _, err := DialInWithTimeout(root, "nope", 0); !errors.Is(err, ErrNoSuchVM) {
		t.Fatal(err)
	}
	// stale socket file: listener closed without unlink
	os.Mkdir(filepath.Join(root, "v"), 0o700)
	sock := filepath.Join(root, "v", "vphone.sock")
	ln, _ := net.ListenUnix("unix", &net.UnixAddr{Name: sock, Net: "unix"})
	ln.SetUnlinkOnClose(false)
	ln.Close()
	if _, err := DialInWithTimeout(root, "v", 0); !errors.Is(err, ErrNotRunning) {
		t.Fatal(err)
	}
}

func TestDialFailsClosed(t *testing.T) {
	root, _ := os.MkdirTemp("/tmp", "vmt")
	defer os.RemoveAll(root)
	os.Mkdir(filepath.Join(root, "f"), 0o700)
	os.WriteFile(filepath.Join(root, "f", "vphone.sock"), []byte("x"), 0o600)
	if _, err := DialInWithTimeout(root, "f", 0); err == nil || errors.Is(err, ErrNotRunning) || errors.Is(err, ErrNoSuchVM) {
		t.Fatalf("regular file: %v", err)
	}
	os.Mkdir(filepath.Join(root, "p"), 0o700)
	sock := filepath.Join(root, "p", "vphone.sock")
	ln, _ := net.ListenUnix("unix", &net.UnixAddr{Name: sock, Net: "unix"})
	defer ln.Close()
	os.Chmod(sock, 0)
	if _, err := DialInWithTimeout(root, "p", 0); err == nil || errors.Is(err, ErrNotRunning) {
		t.Fatalf("mode 0000: %v", err)
	}
}

func TestPushWire(t *testing.T) {
	f := newFakeVM(t, sayf(`{"ok":true}`+"\n"))
	m, err := DialInWithTimeout(f.root, "v1", 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Push(context.Background(), "/tmp/a.ipa", "/private/var/tmp/x.ipa"); err != nil {
		t.Fatal(err)
	}
	want := `{"t":"push","local":"/tmp/a.ipa","path":"/private/var/tmp/x.ipa"}` + "\n"
	if got := <-f.reqs; got != want {
		t.Fatalf("got %q", got)
	}
}

func TestPushRemoteError(t *testing.T) {
	f := newFakeVM(t, sayf(`{"ok":false,"error":"x"}`+"\n"))
	m, _ := DialInWithTimeout(f.root, "v1", 0)
	err := m.Push(context.Background(), "/tmp/a", "/g")
	var re *RemoteError
	if !errors.As(err, &re) || !strings.Contains(err.Error(), "x") {
		t.Fatal(err)
	}
}

func TestBadReplies(t *testing.T) {
	for name, h := range map[string]func(string, net.Conn){
		"garbage": sayf("garbage\n"),
		"eof":     func(string, net.Conn) {},
	} {
		f := newFakeVM(t, h)
		m, _ := DialInWithTimeout(f.root, "v1", 0)
		if err := m.Push(context.Background(), "/tmp/a", "/g"); err == nil {
			t.Errorf("%s push: want error", name)
		}
		if _, err := m.RPC(context.Background(), "m", map[string]any{}, time.Second); err == nil {
			t.Errorf("%s rpc: want error", name)
		}
	}
}

func TestReplyTooLarge(t *testing.T) {
	f := newFakeVM(t, func(_ string, c net.Conn) {
		c.Write([]byte(strings.Repeat("a", maxReply+1) + "\n"))
	})
	m, _ := DialInWithTimeout(f.root, "v1", 0)
	if err := m.Push(context.Background(), "/tmp/a", "/g"); !errors.Is(err, ErrReplyTooLarge) {
		t.Fatalf("push %v", err)
	}
	if _, err := m.RPC(context.Background(), "m", map[string]any{}, time.Second); !errors.Is(err, ErrReplyTooLarge) {
		t.Fatalf("rpc %v", err)
	}
}

func TestPushTimeout(t *testing.T) {
	f := newFakeVM(t, func(_ string, c net.Conn) { time.Sleep(time.Second) })
	m, _ := DialInWithTimeout(f.root, "v1", 0)
	m.PushDeadline = 50 * time.Millisecond
	err := m.Push(context.Background(), "/tmp/a", "/g")
	var te *TimeoutError
	if !errors.As(err, &te) || te.D != 50*time.Millisecond || te.Error() != "timed out after 50ms" {
		t.Fatal(err)
	}
}

func TestRPCSuccessAndWire(t *testing.T) {
	f := newFakeVM(t, sayf(`{"ok":true,"result":{"bundle_id":"com.x","upgraded":true}}`+"\n"))
	m, _ := DialInWithTimeout(f.root, "v1", 0)
	res, err := m.RPC(context.Background(), "apps.install", map[string]string{"path": "/g"}, time.Second)
	if err != nil || !strings.Contains(string(res), "com.x") {
		t.Fatal(res, err)
	}
	want := `{"t":"rpc","method":"apps.install","params":{"path":"/g"}}` + "\n"
	if got := <-f.reqs; got != want {
		t.Fatalf("got %q", got)
	}
}

func TestRPCGuestError(t *testing.T) {
	f := newFakeVM(t, sayf(`{"ok":false,"error":"e","guest_error":{"code":"c","message":"m"}}`+"\n"))
	m, _ := DialInWithTimeout(f.root, "v1", 0)
	_, err := m.RPC(context.Background(), "x", map[string]any{}, time.Second)
	var re *RemoteError
	if !errors.As(err, &re) || re.Guest == nil || re.Guest.Message != "m" || re.Msg != "e" {
		t.Fatal(err)
	}
}

func TestRPCCancelClosesConnection(t *testing.T) {
	f := newFakeVM(t, func(_ string, c net.Conn) { time.Sleep(5 * time.Second) })
	m, _ := DialInWithTimeout(f.root, "v1", 0)
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(50 * time.Millisecond); cancel() }()
	start := time.Now()
	_, err := m.RPC(ctx, "x", map[string]any{}, time.Minute)
	if !errors.Is(err, context.Canceled) || time.Since(start) > 2*time.Second {
		t.Fatal(err)
	}
}

func TestNewGuestPath(t *testing.T) {
	re := regexp.MustCompile(`^/private/var/tmp/vphone-apps-[0-9a-f]{16}\.ipa$`)
	a, err := NewGuestPath(RandReader)
	b, _ := NewGuestPath(RandReader)
	if err != nil || !re.MatchString(a) || a == b {
		t.Fatal(a, b, err)
	}
	if _, err := NewGuestPath(strings.NewReader("")); err == nil {
		t.Fatal("failing reader must error")
	}
}
