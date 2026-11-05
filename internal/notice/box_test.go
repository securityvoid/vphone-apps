package notice

import (
	"os"
	"strings"
	"testing"
)

func TestBox(t *testing.T) {
	rule := "+" + strings.Repeat("-", 74) + "+\n"
	long := strings.Repeat("x", 80)
	for _, c := range []struct{ in, want string }{
		{"a b", rule + "| a b" + strings.Repeat(" ", 69) + " |\n" + rule},
		{strings.Repeat("a", 72) + " b", rule + "| " + strings.Repeat("a", 72) + " |\n| b" + strings.Repeat(" ", 71) + " |\n" + rule},
		{long, rule + "| " + long[:72] + " |\n| " + long[72:] + strings.Repeat(" ", 64) + " |\n" + rule},
	} {
		if got := Box(c.in); got != c.want {
			t.Errorf("Box(%q)\n got %q\nwant %q", c.in, got, c.want)
		}
	}
	want := `+--------------------------------------------------------------------------+
| Setup is complete. Please remember to only use vphone-apps for legal and |
| lawful purposes allowed by the laws of your locality. Consult your       |
| attorney if you have any questions on what this does or does not         |
| include.                                                                 |
+--------------------------------------------------------------------------+
`
	if got := Box(SetupDone); got != want {
		t.Errorf("Box(SetupDone) = %q", got)
	}
	b, err := os.ReadFile("../../docs/specs/setup/design.md")
	if err != nil || !strings.Contains(string(b), want) {
		t.Error("design.md does not carry the exact box")
	}
}
