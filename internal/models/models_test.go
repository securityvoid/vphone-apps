package models

import (
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"testing"
)

func TestDirSudo(t *testing.T) {
	defer func(a func() int, b func() string, c func(string) (*user.User, error)) {
		geteuid, sudoUser, lookupUser = a, b, c
	}(geteuid, sudoUser, lookupUser)

	cur, err := Dir()
	if err != nil {
		t.Fatal(err)
	}

	// euid 501 + SUDO_USER=other: current user's directory.
	geteuid, sudoUser = func() int { return 501 }, func() string { return "other" }
	if got, _ := Dir(); got != cur {
		t.Fatalf("got %s want %s", got, cur)
	}

	// root + SUDO_USER=nosuchuser: error, no fallback.
	geteuid, sudoUser = func() int { return 0 }, func() string { return "nosuchuser" }
	lookupUser = func(string) (*user.User, error) { return nil, user.UnknownUserError("nosuchuser") }
	if _, err := Dir(); err == nil || !strings.Contains(err.Error(), "cannot resolve SUDO_USER nosuchuser") {
		t.Fatalf("got %v", err)
	}

	// root + resolvable SUDO_USER: that user's directory.
	lookupUser = func(string) (*user.User, error) { return &user.User{HomeDir: "/Users/bob"}, nil }
	got, err := Dir()
	if err != nil || got != "/Users/bob/Library/Application Support/unfairplay/models" {
		t.Fatalf("got %s %v", got, err)
	}
}

func TestExists(t *testing.T) {
	d := t.TempDir()
	if ok, err := Exists(d); ok || err != nil {
		t.Fatal("empty dir", ok, err)
	}
	if ok, err := Exists(filepath.Join(d, "missing")); ok || err != nil {
		t.Fatal("missing dir", ok, err)
	}
	os.WriteFile(filepath.Join(d, "zero.bin"), nil, 0o600)
	os.Mkdir(filepath.Join(d, "x.bin"), 0o700)
	os.WriteFile(filepath.Join(d, "m.bin.tmp"), []byte("x"), 0o600)
	if ok, _ := Exists(d); ok {
		t.Fatal("zero-byte, dir and .bin.tmp must not count")
	}
	os.WriteFile(filepath.Join(d, "m.bin"), []byte("x"), 0o600)
	if ok, err := Exists(d); !ok || err != nil {
		t.Fatal(ok, err)
	}
}

func TestExistsUnreadable(t *testing.T) {
	d := filepath.Join(t.TempDir(), "m")
	os.Mkdir(d, 0o000)
	defer os.Chmod(d, 0o700)
	if _, err := Exists(d); err == nil {
		t.Fatal("want error for mode-0000 dir")
	}
}
