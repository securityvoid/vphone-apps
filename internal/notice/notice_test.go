package notice

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func goCmd(t *testing.T, args ...string) string {
	t.Helper()
	cmd := exec.Command("go", args...)
	cmd.Env = append(os.Environ(), "GOFLAGS=-mod=readonly", "GOPROXY=off")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go %v: %v\n%s", args, err, stderr.String())
	}
	return string(out)
}

func TestNoticeComplete(t *testing.T) {
	root, err := os.ReadFile("NOTICE")
	if err != nil {
		t.Fatal(err)
	}
	rootNotice, err := os.ReadFile(filepath.Join("..", "..", "NOTICE"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(root, rootNotice) {
		t.Fatal("internal/notice/NOTICE differs from the root NOTICE; run scripts/gen-notice.sh")
	}
	n := string(rootNotice)

	list := strings.TrimSpace(goCmd(t, "list", "-deps", "-f", "{{with .Module}}{{if not .Main}}{{.Path}}{{if .Replace}} => {{.Replace.Path}}{{end}}{{end}}{{end}}", "../../cmd/vphone-apps"))
	if list == "" {
		t.Fatal("go list -deps is empty")
	}
	lines := strings.Split(list, "\n")
	for _, ln := range lines {
		if strings.TrimSpace(ln) == "" {
			continue
		}
		f := strings.Fields(ln)
		want := []string{f[0]}
		for i, w := range f {
			if w == "=>" { // the replace target counts as the unfairplay entry
				want = append(want, f[i+1])
			}
		}
		for _, p := range want {
			if !regexp.MustCompile(`(?m)^--- (.*[ (])?` + regexp.QuoteMeta(p) + `[ )]`).MatchString(n) {
				t.Errorf("module %s has no header line in NOTICE", p)
			}
		}
	}

	dir := strings.TrimSpace(goCmd(t, "list", "-m", "-f", "{{.Dir}}", "github.com/DrWhax/unfairplay"))
	for _, p := range []string{
		filepath.Join(dir, "LICENSE"),
		filepath.Join(dir, "LICENSE-DARKPHOENIX.txt"),
		filepath.Join(dir, "LICENSE-MACOSFAIRFREE.txt"),
		filepath.Join("..", "..", "scripts", "licenses", "vphone-cli.LICENSE"),
	} {
		if !strings.Contains(squash(n), squash(licenseText(t, p))) {
			t.Errorf("NOTICE lacks the full text of %s", p)
		}
	}
}

// licenseText returns the file without the generator's "# Source:" lines.
func licenseText(t *testing.T, path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var keep []string
	for _, l := range strings.Split(string(b), "\n") {
		if !strings.HasPrefix(l, "# Source:") {
			keep = append(keep, l)
		}
	}
	return strings.Join(keep, "\n")
}

// squash collapses all whitespace runs to single spaces.
func squash(s string) string { return strings.Join(strings.Fields(s), " ") }

func TestNoticeLicenseAndCredits(t *testing.T) {
	b, err := os.ReadFile("../../NOTICE")
	if err != nil {
		t.Fatal(err)
	}
	n := string(b)
	hdr := n[:strings.Index(n, "====")] // header: before the vphone-apps block
	for _, w := range []string{"vphone-apps itself is released under the MIT License", "Inspired by the darkwirelabs.com article at https://www.darkwirelabs.com/articles/from-app-store-to-virtual-iphone-with-vphone/; it is not a dependency."} {
		if !strings.Contains(hdr, w) {
			t.Errorf("NOTICE header lacks %q", w)
		}
	}
	if strings.Index(n, "--- vphone-apps ---") < 0 || strings.Index(n, "--- vphone-apps ---") > strings.Index(n, "Direct upstreams\n====") {
		t.Error("vphone-apps block must precede the direct upstreams")
	}
	lic, err := os.ReadFile("../../LICENSE")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(lic), "Copyright (c) 2026 Doug Logan") {
		t.Error("LICENSE lacks the copyright line")
	}
	if !strings.Contains(n, string(lic)) {
		t.Error("NOTICE does not reproduce LICENSE")
	}
	mod, err := os.ReadFile("../../go.mod")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.ToLower(string(mod)), "replace") {
		t.Error("go.mod must not contain a replace directive")
	}
	if strings.Contains(n, "pull/28") || strings.Contains(n, "securityvoid/unfairplay") {
		t.Error("NOTICE must not reference the unfairplay fork")
	}
	emb, err := os.ReadFile("NOTICE")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(emb), "pull/28") || strings.Contains(string(emb), "securityvoid/unfairplay") {
		t.Error("internal/notice/NOTICE must not reference the unfairplay fork")
	}
}
