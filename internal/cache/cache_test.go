package cache

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func base(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "c")
}

func mode(t *testing.T, p string) os.FileMode {
	t.Helper()
	i, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	return i.Mode().Perm()
}

func TestModesAndClean(t *testing.T) {
	b := base(t)
	c, err := NewIn(b)
	if err != nil {
		t.Fatal(err)
	}
	if m := mode(t, b); m != 0o700 {
		t.Fatalf("base mode %o", m)
	}
	if m := mode(t, c.Dir()); m != 0o700 {
		t.Fatalf("run mode %o", m)
	}
	p, _ := c.Path("x")
	if err := os.WriteFile(p+".part", []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := c.Commit("x"); err != nil {
		t.Fatal(err)
	}
	if m := mode(t, p); m != 0o600 {
		t.Fatalf("file mode %o", m)
	}
	if err := c.Clean(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(c.Dir()); !os.IsNotExist(err) {
		t.Fatal("run dir remains")
	}
	if es, _ := os.ReadDir(b); len(es) != 0 {
		t.Fatalf("leftovers %v", es)
	}
}

func TestTwoCachesIsolated(t *testing.T) {
	b := base(t)
	c1, _ := NewIn(b)
	c2, _ := NewIn(b)
	if c1.Dir() == c2.Dir() {
		t.Fatal("same dir")
	}
	p, _ := c2.Path("f")
	os.WriteFile(p, []byte("x"), 0o600)
	c1.Clean()
	if _, err := os.Stat(p); err != nil {
		t.Fatal("c1.Clean deleted c2 file")
	}
}

func TestExistingBaseChmod(t *testing.T) {
	b := base(t)
	os.Mkdir(b, 0o755)
	os.Chmod(b, 0o755)
	if _, err := NewIn(b); err != nil {
		t.Fatal(err)
	}
	if m := mode(t, b); m != 0o700 {
		t.Fatalf("mode %o", m)
	}
}

func TestSymlinkRefused(t *testing.T) {
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "l")
	os.Symlink(real, link)
	_, err := NewIn(link)
	u, ok := err.(*UnsafeDirError)
	if !ok {
		t.Fatalf("got %v", err)
	}
	if u.Error() != link+" is a symlink or not owned by you" {
		t.Fatal(u.Error())
	}
}

func TestForeignOwnerRefused(t *testing.T) {
	b := base(t)
	os.Mkdir(b, 0o700)
	old := euid
	euid = func() int { return os.Geteuid() + 1 }
	defer func() { euid = old }()
	if _, err := NewIn(b); err == nil {
		t.Fatal("expected refusal")
	} else if _, ok := err.(*UnsafeDirError); !ok {
		t.Fatal(err)
	}
}

func TestPathRejects(t *testing.T) {
	c, _ := NewIn(base(t))
	for _, n := range []string{"../x", "a/b", "", ".."} {
		if _, err := c.Path(n); err == nil {
			t.Fatalf("%q accepted", n)
		}
	}
}

func TestSweep(t *testing.T) {
	b := base(t)
	os.Mkdir(b, 0o700)
	old := time.Now().Add(-25 * time.Hour)
	for _, n := range []string{"run-x", "run-y", "kept-z", "other", "run-old"} {
		os.Mkdir(filepath.Join(b, n), 0o700)
	}
	for _, n := range []string{"run-x", "kept-z", "other", "run-old"} {
		os.Chtimes(filepath.Join(b, n), old, old)
	}
	// undeletable stale run-old
	os.WriteFile(filepath.Join(b, "run-old", "f"), nil, 0o600)
	os.Chmod(filepath.Join(b, "run-old"), 0o500)
	os.Chtimes(filepath.Join(b, "run-old"), old, old)
	defer os.Chmod(filepath.Join(b, "run-old"), 0o700)
	if _, err := NewIn(b); err != nil {
		t.Fatal(err)
	}
	exists := func(n string) bool { _, err := os.Lstat(filepath.Join(b, n)); return err == nil }
	if exists("run-x") {
		t.Fatal("stale run-x kept")
	}
	for _, n := range []string{"run-y", "kept-z", "other", "run-old"} {
		if !exists(n) {
			t.Fatalf("%s removed", n)
		}
	}
}

func TestKeep(t *testing.T) {
	b := base(t)
	c, _ := NewIn(b)
	if _, err := c.Keep(); err == nil {
		t.Fatal("Keep without file should fail")
	}
	p, _ := c.Path("app.decrypted.ipa")
	os.WriteFile(p, []byte("d"), 0o600)
	o, _ := c.Path("other")
	os.WriteFile(o, []byte("d"), 0o600)
	k, err := c.Keep()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(k, "kept-") || filepath.Base(k) != "app.decrypted.ipa" {
		t.Fatal(k)
	}
	if es, _ := os.ReadDir(filepath.Dir(k)); len(es) != 1 {
		t.Fatalf("kept dir has %d entries", len(es))
	}
	if err := c.Clean(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(k); err != nil {
		t.Fatal("Clean removed kept file")
	}
}
