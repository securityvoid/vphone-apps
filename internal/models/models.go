// Package models resolves the unfairplay model directory shared with the
// unfairplay CLI.
package models

import (
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strings"
)

// Test hooks.
var (
	geteuid    = os.Geteuid
	sudoUser   = func() string { return os.Getenv("SUDO_USER") }
	lookupUser = user.Lookup
)

// Dir returns <UserConfigDir>/unfairplay/models for the invoking user: the
// current user, or SUDO_USER only when running as root under sudo.
func Dir() (string, error) {
	base, err := os.UserConfigDir()
	if geteuid() == 0 {
		if name := sudoUser(); name != "" {
			u, lerr := lookupUser(name)
			if lerr != nil {
				return "", fmt.Errorf("cannot resolve SUDO_USER %s", name)
			}
			// Same layout as os.UserConfigDir on the target user's home.
			base = filepath.Join(u.HomeDir, "Library", "Application Support")
			err = nil
		}
	}
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "unfairplay", "models"), nil
}

// Exists reports whether dir holds a regular, non-empty *.bin file. The error
// is set only for a read failure other than not-exist.
func Exists(dir string) (bool, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".bin") {
			continue
		}
		info, err := os.Stat(filepath.Join(dir, e.Name()))
		if err == nil && info.Mode().IsRegular() && info.Size() > 0 {
			return true, nil
		}
	}
	return false, nil
}
