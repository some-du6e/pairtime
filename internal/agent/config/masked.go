// SPDX-License-Identifier: AGPL-3.0-or-later

package config

import "strings"

// masked stands in, in what Masked shows, for text pairtime didn't write.
const masked = "***"

// Masked returns the text of a config.toml as pairtime install shows it in
// a diff, on its consent screen and with --dry-run: every comment and
// every string that pairtime didn't write, a quoted key's included, is
// ***, as the hooks' diff masks Claude Code's settings
// (internal/install/consent). The lines around the change can hold the
// teen's own notes or a project's name (D-owner-names), and teens
// screenshot that screen to ask for help. A line as pairtime's default
// config.toml has it, or as pairtime login writes the mode, is pairtime's
// and stays, and so do table headers, bare keys, numbers, true and false,
// so the diff shows the file's shape and the line pairtime changes. Every
// line stays a line, with its ending, and nil, for a file that isn't
// there, stays nil.
func Masked(text []byte) []byte {
	if text == nil {
		return nil
	}
	ours := map[string]bool{`mode = "online"`: true}
	for _, line := range strings.Split(defaultFile, "\n") {
		ours[line] = true
	}
	s := string(text)
	// A multi-line string can hold any line, so a file with one is masked
	// line by line, whole. edited refuses to change such a file anyway.
	whole := strings.Contains(s, `"""`) || strings.Contains(s, `'''`)
	var b strings.Builder
	for _, line := range strings.SplitAfter(s, "\n") {
		body := strings.TrimRight(line, "\r\n")
		switch {
		case whole:
			b.WriteString(maskWhole(body))
		case ours[body]:
			b.WriteString(body)
		default:
			b.WriteString(maskLine(body))
		}
		b.WriteString(line[len(body):])
	}
	return []byte(b.String())
}

// maskLine masks the comment and the strings of s, a line of TOML without
// its ending, keeping their quotes and the # that starts the comment.
func maskLine(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		c := s[i]
		switch c {
		case '#':
			b.WriteByte('#')
			b.WriteString(maskWhole(s[i+1:]))
			return b.String()
		case '"', '\'':
			j := i + 1
			for j < len(s) && s[j] != c {
				if c == '"' && s[j] == '\\' {
					j++ // an escape, such as \", which doesn't end the string
				}
				j++
			}
			b.WriteByte(c)
			if j > i+1 {
				b.WriteString(masked)
			}
			if j < len(s) {
				b.WriteByte(c)
			}
			i = j + 1
		default:
			b.WriteByte(c)
			i++
		}
	}
	return b.String()
}

// maskWhole masks all of the text of s, keeping the blanks before it.
func maskWhole(s string) string {
	text := strings.TrimLeft(s, " \t")
	if strings.TrimSpace(text) == "" {
		return s
	}
	return s[:len(s)-len(text)] + masked
}
