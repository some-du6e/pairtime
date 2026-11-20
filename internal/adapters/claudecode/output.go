// SPDX-License-Identifier: AGPL-3.0-or-later

package claudecode

import (
	"strings"

	"github.com/some-du6e/pairtime/pkg/ledger/features"
	"github.com/some-du6e/pairtime/pkg/redact"
)

// output is what agent.output says about an assistant message (SM 1.2).
type output struct {
	words, codeLines int
	asks, offers     bool
}

// outputOf reads an assistant message as SM 1.2 and 2.4 count it:
//
//   - code_lines are the lines inside its fenced code blocks, as
//     pkg/redact's CodeBlocks finds and counts them (CommonMark fences);
//   - words are the words of the prose outside them, by spec 10.2's
//     whitespace (features.Words), so fenced code isn't counted twice;
//   - asks_question is set when its last line of prose that isn't blank
//     ends with a question mark, once closing quotes, brackets and
//     Markdown emphasis are set aside;
//   - offers_options is set when it asks and its prose holds a numbered
//     list with items 1 and 2, the options it asks the person to pick
//     from.
func outputOf(msg string) output {
	var o output
	var prose strings.Builder
	at := 0
	for _, b := range redact.CodeBlocks(msg) {
		o.codeLines += b.Lines
		prose.WriteString(msg[at:b.Start])
		prose.WriteString("\n")
		at = b.End
	}
	prose.WriteString(msg[at:])
	text := prose.String()
	o.words = len(features.Words(text))
	lines := strings.Split(text, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		l := strings.TrimRight(lines[i], " \t\r*_`\"')]}\u201d\u2019")
		if strings.TrimSpace(l) == "" {
			continue
		}
		o.asks = strings.HasSuffix(l, "?") || strings.HasSuffix(l, "\uFF1F")
		break
	}
	if o.asks {
		one, two := false, false
		for _, l := range lines {
			switch listNumber(l) {
			case 1:
				one = true
			case 2:
				two = true
			}
		}
		o.offers = one && two
	}
	return o
}

// listNumber returns the number of a numbered list item, "1. " or "1) "
// after any indent, or 0 for any other line.
func listNumber(line string) int {
	l := strings.TrimLeft(line, " \t")
	n, i := 0, 0
	for i < len(l) && i < 3 && '0' <= l[i] && l[i] <= '9' {
		n = n*10 + int(l[i]-'0')
		i++
	}
	if i == 0 || i+1 >= len(l) || (l[i] != '.' && l[i] != ')') || l[i+1] != ' ' {
		return 0
	}
	return n
}
