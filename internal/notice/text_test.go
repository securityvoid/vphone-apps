package notice

import (
	"os"
	"strings"
	"testing"
)

// TestNoticeText compares the constants with the texts in the design's
// "Notice texts" and "Output lines" sections.
func TestNoticeText(t *testing.T) {
	b, err := os.ReadFile("../../docs/specs/setup/design.md")
	if err != nil {
		t.Fatal(err)
	}
	section := func(name string) string {
		_, sec, ok := strings.Cut(string(b), "### "+name)
		if !ok {
			t.Fatalf("design.md has no %s section", name)
		}
		sec, _, _ = strings.Cut(sec, "\n### ")
		return sec
	}
	texts := section("Notice texts")
	for name, c := range map[string]string{"notice.Prerequisites": Prerequisites, "notice.EnrollHint": EnrollHint} {
		if !strings.Contains(texts, "- `"+name+"` = `"+c+"`") {
			t.Errorf("%s does not match design.md: %q", name, c)
		}
	}
	lines := section("Output lines")
	for name, c := range map[string]string{
		"SetupIntro": SetupIntro, "SetupKept": SetupKept, "SetupNew": SetupNew,
		"SetupRemoving": SetupRemoving, "SetupDone": SetupDone,
	} {
		if !strings.Contains(lines, "| `notice."+name+"` | `"+c+"`") {
			t.Errorf("notice.%s does not match design.md: %q", name, c)
		}
	}
}
