// Package cache owns the per-run work directory under the vphone-apps cache.
package cache

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const staleAfter = 24 * time.Hour

// Test hooks.
var (
	euid = os.Geteuid
	now  = time.Now
)

// UnsafeDirError reports a cache directory that is a symlink or foreign-owned.
type UnsafeDirError struct{ Path string }

func (e *UnsafeDirError) Error() string {
	return e.Path + " is a symlink or not owned by you"
}

// Cache is one run's private subdirectory.
type Cache struct {
	dir  string
	kept bool
}

// New prepares the default cache directory and a fresh run subdirectory.
func New() (*Cache, error) {
	d, err := os.UserCacheDir()
	if err != nil {
		return nil, err
	}
	return NewIn(filepath.Join(d, "vphone-apps"))
}

// NewIn is New with an explicit cache directory.
func NewIn(base string) (*Cache, error) {
	info, err := os.Lstat(base)
	switch {
	case errors.Is(err, os.ErrNotExist):
		if err := os.MkdirAll(base, 0o700); err != nil {
			return nil, err
		}
	case err != nil:
		return nil, err
	default:
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() || !ownedByUser(info) {
			return nil, &UnsafeDirError{Path: base}
		}
	}
	if err := os.Chmod(base, 0o700); err != nil {
		return nil, err
	}
	sweep(base)
	dir, err := os.MkdirTemp(base, "run-")
	if err != nil {
		return nil, err
	}
	return &Cache{dir: dir}, nil
}

func ownedByUser(info os.FileInfo) bool {
	st, ok := info.Sys().(*syscall.Stat_t)
	return ok && int(st.Uid) == euid()
}

// sweep removes stale run-* directories; every error is ignored.
func sweep(base string) {
	entries, err := os.ReadDir(base)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), "run-") {
			continue
		}
		info, err := os.Lstat(filepath.Join(base, e.Name()))
		if err != nil || !ownedByUser(info) || now().Sub(info.ModTime()) <= staleAfter {
			continue
		}
		_ = os.RemoveAll(filepath.Join(base, e.Name()))
	}
}

// Dir returns the run directory.
func (c *Cache) Dir() string { return c.dir }

// Path returns the path of a file inside the run directory.
func (c *Cache) Path(name string) (string, error) {
	if name == "" || strings.ContainsAny(name, `/\`) || strings.Contains(name, "..") {
		return "", fmt.Errorf("invalid cache file name %q", name)
	}
	return filepath.Join(c.dir, name), nil
}

// Commit makes <name>.part private and renames it to <name>.
func (c *Cache) Commit(name string) error {
	p, err := c.Path(name)
	if err != nil {
		return err
	}
	if err := os.Chmod(p+".part", 0o600); err != nil {
		return err
	}
	return os.Rename(p+".part", p)
}

// Keep renames the run directory to kept-*, leaving only app.decrypted.ipa,
// and returns that file's path. Call it only when the whole command succeeded.
func (c *Cache) Keep() (string, error) {
	if c.kept {
		return "", errors.New("already kept")
	}
	const keepName = "app.decrypted.ipa"
	if _, err := os.Stat(filepath.Join(c.dir, keepName)); err != nil {
		return "", err
	}
	entries, err := os.ReadDir(c.dir)
	if err != nil {
		return "", err
	}
	for _, e := range entries {
		if e.Name() != keepName {
			if err := os.RemoveAll(filepath.Join(c.dir, e.Name())); err != nil {
				return "", err
			}
		}
	}
	kept := filepath.Join(filepath.Dir(c.dir), "kept-"+strings.TrimPrefix(filepath.Base(c.dir), "run-"))
	if err := os.Rename(c.dir, kept); err != nil {
		return "", err
	}
	c.dir, c.kept = kept, true
	return filepath.Join(kept, keepName), nil
}

// Clean removes the run directory (a no-op once kept). It retries once.
func (c *Cache) Clean() error {
	if c.kept {
		return nil
	}
	if err := os.RemoveAll(c.dir); err != nil {
		return os.RemoveAll(c.dir)
	}
	return nil
}
