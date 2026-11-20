// SPDX-License-Identifier: AGPL-3.0-or-later

package install

import (
	"bytes"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/some-du6e/pairtime/internal/install/jsonc"
)

// masked stands in, in what Diff shows, for text pairtime didn't write.
const masked = "***"

// shown is what Diff shows of content c of ch's target. A settings file
// that pairtime merges hooks into is masked (maskSettings). Other targets
// hold an entry pairtime owns whole, shown as it is.
func (ch Change) shown(c Content) []byte {
	if slices.Contains(ch.kinds, kindClaudeHooks) && c.Exists {
		return maskSettings(c.Data)
	}
	return c.text()
}

// maskSettings returns a settings file as Diff shows it, with every string
// value and comment that pairtime didn't write masked. The consent screen
// and `--dry-run` print the lines around each change, which teens
// screenshot for help or share from a stream, and an agent that sets
// pairtime up sends them to its provider. Those lines can hold the token in
// an env block, as the setup guides of third-party providers have it, or a
// webhook URL in another tool's hook, in any form, so the mask takes every
// value rather than what looks like a secret. Member names, numbers, true,
// false and null stay, and so do pairtime's own hook handlers, so the diff
// shows the file's shape and what pairtime adds or takes out. Every line
// stays a line, so the diff of two masked files lines up as the files do.
func maskSettings(src []byte) []byte {
	root, err := jsonc.Parse(src)
	if err != nil && !errors.Is(err, jsonc.ErrEmpty) {
		return maskLines(src)
	}
	var spans [][2]int // what to mask
	if root != nil {
		keep := map[*jsonc.Value]bool{}
		keepPairtime(root, keep)
		eachString(root, func(v *jsonc.Value) {
			if !keep[v] && v.End-v.Start > 2 {
				spans = append(spans, [2]int{v.Start + 1, v.End - 1})
			}
		})
	}
	for _, c := range jsonc.Comments(src) {
		if c[1]-c[0] > 2 {
			spans = append(spans, [2]int{c[0] + 2, c[1]})
		}
	}
	slices.SortFunc(spans, func(a, b [2]int) int { return a[0] - b[0] })
	var b bytes.Buffer
	at := 0
	for _, s := range spans {
		b.Write(src[at:s[0]])
		b.Write(maskLines(src[s[0]:s[1]]))
		at = s[1]
	}
	b.Write(src[at:])
	return b.Bytes()
}

// maskLines masks the text of each line of b, keeping the white space
// around it, its line breaks and the end of a block comment.
func maskLines(b []byte) []byte {
	var out bytes.Buffer
	for i, line := range bytes.Split(b, []byte("\n")) {
		if i > 0 {
			out.WriteByte('\n')
		}
		var end []byte
		if bytes.HasSuffix(line, []byte("*/")) {
			line, end = line[:len(line)-2], []byte("*/")
		}
		cr := bytes.HasSuffix(line, []byte("\r"))
		if cr {
			line = line[:len(line)-1]
		}
		if text := bytes.TrimSpace(line); len(text) > 0 {
			lead := bytes.Index(line, text)
			out.Write(line[:lead])
			out.WriteString(masked)
			out.Write(line[lead+len(text):])
		} else {
			out.Write(line)
		}
		out.Write(end)
		if cr {
			out.WriteByte('\r')
		}
	}
	return out.Bytes()
}

// keepPairtime marks in keep the string values pairtime writes in a
// settings file: those of its hook handlers, and the matcher of a group
// that holds only pairtime handlers.
func keepPairtime(root *jsonc.Value, keep map[*jsonc.Value]bool) {
	hooks := root.Get("hooks")
	if hooks == nil || hooks.Kind != jsonc.Object {
		return
	}
	for _, ev := range hooks.Items {
		if ev.Value.Kind != jsonc.Array {
			continue
		}
		for _, g := range ev.Value.Items {
			hs := g.Value.Get("hooks")
			if hs == nil || hs.Kind != jsonc.Array || len(hs.Items) == 0 {
				continue
			}
			all := true
			for _, h := range hs.Items {
				if !isPairtimeHook(h.Value) {
					all = false
					continue
				}
				eachString(h.Value, func(v *jsonc.Value) { keep[v] = true })
			}
			if m := g.Value.Get("matcher"); all && m != nil {
				keep[m] = true
			}
		}
	}
}

// eachString calls f for every string value under v, v included. Member
// names aren't values.
func eachString(v *jsonc.Value, f func(*jsonc.Value)) {
	switch v.Kind {
	case jsonc.String:
		f(v)
	case jsonc.Object, jsonc.Array:
		for _, it := range v.Items {
			eachString(it.Value, f)
		}
	}
}

// escapeControls returns s with the characters a terminal acts on written
// out, so that a file can't move the cursor, clear the consent screen or
// reorder what it shows: C0 controls but tab and the line feed, a carriage
// return that doesn't end a CRLF line, DEL, C1 controls, the bidirectional
// formatting characters and bytes that aren't UTF-8.
func escapeControls(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == utf8.RuneError && size == 1:
			fmt.Fprintf(&b, "<%02X>", s[i])
		case r == '\t' || r == '\n' || (r == '\r' && strings.HasPrefix(s[i+1:], "\n")):
			b.WriteRune(r)
		case r < 0x20 || r == 0x7f || (0x80 <= r && r <= 0x9f) || isBidiControl(r):
			fmt.Fprintf(&b, "<U+%04X>", r)
		default:
			b.WriteString(s[i : i+size])
		}
		i += size
	}
	return b.String()
}

// isBidiControl reports whether r is one of Unicode's bidirectional
// formatting characters, which can make a terminal show text in another
// order than the file holds it.
func isBidiControl(r rune) bool {
	return r == 0x061c || r == 0x200e || r == 0x200f || (0x202a <= r && r <= 0x202e) || (0x2066 <= r && r <= 0x2069)
}
