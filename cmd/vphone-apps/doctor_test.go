package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/byteness/keyring"
	"github.com/majd/ipatool/v2/pkg/appstore"
	"github.com/securityvoid/vphone-apps/internal/vm"
)

func methodsReply(names ...string) func(string) string {
	return func(req string) string {
		var ms []string
		for _, n := range names {
			ms = append(ms, fmt.Sprintf(`{"name":%q}`, n))
		}
		return `{"ok":true,"result":{"methods":[` + strings.Join(ms, ",") + `]}}` + "\n"
	}
}

func doctorEnv(t *testing.T, h func(string) string) *testEnv {
	e := newEnv(t, h)
	e.a.to.DoctorDial, e.a.to.DoctorRPC = 50*time.Millisecond, 50*time.Millisecond
	os.MkdirAll(filepath.Join(e.srv.root, "v1"), 0o700)
	return e
}

func TestDoctorAllPass(t *testing.T) {
	e := doctorEnv(t, methodsReply("apps.install", "x"))
	wantExit(t, e.run("doctor", "--vm", "v1"), 0, e)
	if e.out.String() != "PASS session\nPASS model\nPASS vm\nPASS apps.install\nready\n" {
		t.Fatal(e.out)
	}
	if got := e.srv.requests(); len(got) != 1 || !strings.Contains(got[0], `"method":"methods.list"`) {
		t.Fatal(got)
	}
}

func TestDoctorNoVM(t *testing.T) {
	e := doctorEnv(t, happy)
	wantExit(t, e.run("doctor"), 0, e)
	if e.out.String() != "PASS session\nPASS model\nskipped: VM checks (no --vm)\nready\n" {
		t.Fatal(e.out)
	}
	e = doctorEnv(t, happy)
	e.as.acctErr = fmt.Errorf("x: %w", keyring.ErrKeyNotFound)
	wantExit(t, e.run("doctor"), 1, e)
	if e.out.String() != "FAIL session: run vphone-apps login\nPASS model\nskipped: VM checks (no --vm)\n" {
		t.Fatal(e.out)
	}
}

func TestDoctorSessionAndModel(t *testing.T) {
	e := doctorEnv(t, happy)
	e.as.acctErr = appstore.ErrPasswordTokenExpired
	wantExit(t, e.run("doctor"), 1, e)
	if !strings.Contains(e.out.String(), "FAIL session: ") || strings.Contains(e.out.String(), "FAIL session: run vphone-apps login") {
		t.Fatal(e.out)
	}
	e = doctorEnv(t, happy)
	e.as.acctErr = fmt.Errorf("boom")
	wantExit(t, e.run("doctor"), 1, e)
	if !strings.Contains(e.out.String(), "FAIL session: boom\n") {
		t.Fatal(e.out)
	}
	e = doctorEnv(t, happy)
	os.Remove(filepath.Join(e.models, "m.bin"))
	wantExit(t, e.run("doctor"), 1, e)
	if !strings.Contains(e.out.String(), "FAIL model: run vphone-apps setup\n") {
		t.Fatal(e.out)
	}
	e = doctorEnv(t, happy)
	os.Chmod(e.models, 0)
	defer os.Chmod(e.models, 0o700)
	wantExit(t, e.run("doctor"), 1, e)
	if !strings.Contains(e.out.String(), "FAIL model: ") || strings.Contains(e.out.String(), "ready") {
		t.Fatal(e.out)
	}
	e = doctorEnv(t, happy)
	e.a.modelDir = func() (string, error) { return "", fmt.Errorf("cannot resolve SUDO_USER nosuchuser") }
	wantExit(t, e.run("doctor"), 1, e)
	if !strings.Contains(e.out.String(), "FAIL model: cannot resolve SUDO_USER nosuchuser\n") {
		t.Fatal(e.out)
	}
}

func TestDoctorMethods(t *testing.T) {
	cases := map[string]struct {
		h    func(string) string
		want string
	}{
		"missing": {methodsReply("other"), "FAIL apps.install: update vphone-cli\n"},
		"notok":   {func(string) string { return `{"ok":false,"error":"x"}` + "\n" }, "FAIL apps.install: methods.list failed: x\n"},
		"garbage": {func(string) string { return "garbage\n" }, "FAIL apps.install: methods.list failed: "},
		"eof":     {func(string) string { return closeConn }, "FAIL apps.install: methods.list failed: "},
		"huge":    {func(string) string { return strings.Repeat("a", 2<<20) + "\n" }, "FAIL apps.install: methods.list failed: "},
		"timeout": {func(string) string { return "" }, "FAIL apps.install: methods.list timed out after 50ms\n"},
	}
	for name, c := range cases {
		e := doctorEnv(t, c.h)
		wantExit(t, e.run("doctor", "--vm", "v1"), 1, e)
		if !strings.Contains(e.out.String(), c.want) || strings.Contains(e.out.String(), "ready") {
			t.Errorf("%s: %q", name, e.out)
		}
	}
}

func TestDoctorVMFailures(t *testing.T) {
	for arg, want := range map[string]string{
		"nope": "FAIL vm: create it with vphone-cli vm create\nSKIP apps.install: needs vm\n",
		"":     "FAIL vm: invalid VM name\nSKIP apps.install: needs vm\n",
		"../x": "FAIL vm: invalid VM name\nSKIP apps.install: needs vm\n",
	} {
		e := doctorEnv(t, happy)
		wantExit(t, e.run("doctor", "--vm", arg), 1, e)
		if !strings.HasSuffix(e.out.String(), want) {
			t.Errorf("%q: %q", arg, e.out)
		}
	}
	e := doctorEnv(t, happy)
	e.a.dialVM = func(string, time.Duration) (*vm.Machine, error) { return nil, vm.ErrNotRunning }
	wantExit(t, e.run("doctor", "--vm", "v1"), 1, e)
	if !strings.Contains(e.out.String(), "FAIL vm: start the VM, then re-run\nSKIP") {
		t.Fatal(e.out)
	}
	e = doctorEnv(t, happy)
	block := make(chan struct{})
	defer close(block)
	e.a.dialVM = func(_ string, d time.Duration) (*vm.Machine, error) {
		select {
		case <-block:
		case <-time.After(d):
		}
		return nil, vm.ErrDialTimeout
	}
	wantExit(t, e.run("doctor", "--vm", "v1"), 1, e)
	if !strings.HasSuffix(e.out.String(), "FAIL vm: timed out connecting\nSKIP apps.install: needs vm\n") {
		t.Fatal(e.out)
	}
}

const doctorBlock = `usage: vphone-apps doctor [--vm <name>]
Exit codes: 0 ready, 1 not ready, 2 usage error.
`

func TestDoctorUsageAndReadOnly(t *testing.T) {
	e := doctorEnv(t, happy)
	wantExit(t, e.run("doctor", "--help"), 0, e)
	if e.out.String() != doctorBlock {
		t.Fatalf("doctor --help: %q", e.out)
	}
	e = doctorEnv(t, happy)
	wantExit(t, e.run("doctor", "x"), 2, e)
	wantErr(t, e, "error: usage: unexpected argument x\n"+doctorBlock)
	if got := e.as.callList(); got != "" {
		t.Fatalf("store called: %s", got)
	}
	e = doctorEnv(t, happy)
	wantExit(t, e.run("doctor", "--bogus"), 2, e)
	if !strings.HasPrefix(e.errb.String(), "error: usage:") {
		t.Fatal(e.errb)
	}
	e = doctorEnv(t, methodsReply("apps.install"))
	before := tree(t, e.models) + tree(t, e.cache)
	e.run("doctor", "--vm", "v1")
	if tree(t, e.models)+tree(t, e.cache) != before {
		t.Fatal("doctor changed state")
	}
}

func tree(t *testing.T, dir string) string {
	var sb strings.Builder
	filepath.Walk(dir, func(p string, fi os.FileInfo, err error) error {
		if err == nil {
			b, _ := os.ReadFile(p)
			fmt.Fprintf(&sb, "%s %v %s\n", p, fi.Mode(), b)
		}
		return nil
	})
	return sb.String()
}

// REQ-SETUP-006: the only store call is the local account lookup.
func TestDoctorStoreCalls(t *testing.T) {
	e := doctorEnv(t, methodsReply("apps.install"))
	wantExit(t, e.run("doctor", "--vm", "v1"), 0, e)
	if got := e.as.callList(); got != "" {
		t.Fatalf("doctor made store calls: %s", got)
	}
}

// REQ-SETUP-006: on a clean machine doctor reports the missing session and
// creates nothing under $HOME.
func TestDoctorCreatesNothingWithoutStateDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_STATE_HOME", "")
	t.Setenv("XDG_DATA_HOME", "")
	e := doctorEnv(t, happy)
	e.a.openStore = newApp().openStore
	before := tree(t, home)
	wantExit(t, e.run("doctor"), 1, e)
	if !strings.HasPrefix(e.out.String(), "FAIL session: run vphone-apps login\n") {
		t.Fatal(e.out)
	}
	if tree(t, home) != before {
		t.Fatal("doctor changed $HOME")
	}
	if _, err := os.Stat(filepath.Join(home, ".ipatool")); !os.IsNotExist(err) {
		t.Fatal("state dir created")
	}
}
