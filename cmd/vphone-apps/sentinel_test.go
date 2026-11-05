package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/majd/ipatool/v2/pkg/appstore"
)

const sentinel = "SENTINEL-PW"

// grepTree reports every file under dir whose contents contain the sentinel.
func grepTree(t *testing.T, dir string) []string {
	t.Helper()
	var hits []string
	filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			if b, _ := os.ReadFile(p); strings.Contains(string(b), sentinel) {
				hits = append(hits, p)
			}
		}
		return nil
	})
	return hits
}

func TestSentinel(t *testing.T) {
	for name, loginErr := range map[string]error{"ok": nil, "wrong password": errors.New("Your Apple Account or password is incorrect")} {
		t.Run("login "+name, func(t *testing.T) {
			e := newEnv(t, happy)
			e.as.loginFn = func(in appstore.LoginInput) error {
				if in.Password != sentinel {
					t.Errorf("fake did not receive the password")
				}
				return loginErr
			}
			e.run("login")
			if strings.Contains(e.out.String()+e.errb.String(), sentinel) {
				t.Fatalf("password in output: %q %q", e.out, e.errb)
			}
			if hits := grepTree(t, e.cache); len(hits) != 0 {
				t.Fatal(hits)
			}
		})
	}
	t.Run("install after login", func(t *testing.T) {
		e := newEnv(t, happy)
		if e.run("login") != 0 {
			t.Fatal(e.errb.String())
		}
		e.out.Reset()
		e.errb.Reset()
		keep := e.run("install", "Foo", "--vm", "v1", "--keep")
		if keep != 0 {
			t.Fatal(e.errb.String())
		}
		if strings.Contains(e.out.String()+e.errb.String(), sentinel) {
			t.Fatal("password in install output")
		}
		if hits := grepTree(t, e.cache); len(hits) != 0 {
			t.Fatal(hits)
		}
		for _, r := range e.srv.requests() {
			if strings.Contains(r, sentinel) {
				t.Fatal("password sent to the VM")
			}
		}
	})
}
