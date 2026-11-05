// Package repo holds repository-wide invariants.
package repo

import (
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

// TestNoVendored fails when a .go file exists outside cmd/ and internal/, so
// no upstream source can be copied in without review.
func TestNoVendored(t *testing.T) {
	root := filepath.Join("..", "..")
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		top := strings.SplitN(filepath.ToSlash(rel), "/", 2)[0]
		if d.IsDir() {
			if rel != "." && (top == "cmd" || top == "internal" || strings.HasPrefix(d.Name(), ".")) {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(p, ".go") {
			t.Errorf("%s: Go source outside cmd/ and internal/", rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
