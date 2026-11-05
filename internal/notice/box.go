package notice

import "strings"

// BoxWidth is the text width inside the box.
const BoxWidth = 72

// Box frames text in an ASCII box: greedy word-wrap at spaces to BoxWidth
// (a longer word is split at BoxWidth), every line padded so the right "|"
// aligns, each output line ending "\n". It is the only home of the frame.
func Box(text string) string {
	var lines []string
	cur := ""
	for _, w := range strings.Fields(text) {
		for len(w) > BoxWidth {
			if cur != "" {
				lines, cur = append(lines, cur), ""
			}
			lines, w = append(lines, w[:BoxWidth]), w[BoxWidth:]
		}
		switch {
		case cur == "":
			cur = w
		case len(cur)+1+len(w) <= BoxWidth:
			cur += " " + w
		default:
			lines, cur = append(lines, cur), w
		}
	}
	if cur != "" {
		lines = append(lines, cur)
	}
	rule := "+" + strings.Repeat("-", BoxWidth+2) + "+\n"
	var b strings.Builder
	b.WriteString(rule)
	for _, l := range lines {
		b.WriteString("| " + l + strings.Repeat(" ", BoxWidth-len(l)) + " |\n")
	}
	b.WriteString(rule)
	return b.String()
}
