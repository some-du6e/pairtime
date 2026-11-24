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
	"github.com/some-du6e/pairtime/internal/install/wakacfg"
)

// masked stands in, in what Diff shows, for text pairtime didn't write.
const masked = "***"

// shown is what Diff shows of content c of ch's target. A settings file
// that pairtime merges hooks into is masked (maskSettings), and so is
// ~/.wakatime.cfg, which holds the teen's API keys (wakacfg.Masked). Other
// targets hold an entry pairtime owns whole, shown as it is.
func (ch Change) shown(c Content) []byte {
	switch {
	case slices.Contains(ch.kinds, kindClaudeHooks) && c.Exists:
		return maskSettings(c.Data)
	case slices.Contains(ch.kinds, kindWakaTimeLine) && c.Exists:
		return wakacfg.Masked(c.Data)
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
// false and null stay, and so do the strings pairtime writes in its own
// hook handlers (keepPairtime), so the diff shows the file's shape and what
// pairtime adds or takes out. Every line stays a line, so the diff of two
// masked files lines up as the files do.
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
// settings file: in each of its hook handlers, "type": "command", the
// binary's path and the arguments of `pairtime hook`, and the matcher
// pairtime gives the event, on a group that holds only pairtime handlers.
// Anything else stays masked, even inside a pairtime handler: a handler
// the teen gave a status message, or an argument, is still pairtime's, and
// what was added to it isn't.
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
				keepHandler(h.Value, keep)
			}
			if m := g.Value.Get("matcher"); all && m != nil && m.Kind == jsonc.String && m.Text == claudeMatcher(ev.Key) {
				keep[m] = true
			}
		}
	}
}

// keepHandler marks in keep the strings pairtime writes in h, one of its
// hook handlers.
func keepHandler(h *jsonc.Value, keep map[*jsonc.Value]bool) {
	if typ := h.Get("type"); typ != nil && typ.Kind == jsonc.String && typ.Text == "command" {
		keep[typ] = true
	}
	if cmd := h.Get("command"); cmd != nil && cmd.Kind == jsonc.String {
		keep[cmd] = true // a pairtime binary's path, which isPairtimeHook checked
	}
	if args := h.Get("args"); args != nil && args.Kind == jsonc.Array {
		for _, a := range args.Items {
			if a.Value.Kind == jsonc.String && isHookArg(a.Value.Text) {
				keep[a.Value] = true
			}
		}
	}
}

// isHookArg reports whether s is one of the words of pairtime's hook
// arguments: hook, --harness, claude_code, --event and the names of the
// events pairtime installs.
func isHookArg(s string) bool {
	switch s {
	case "hook", "--harness", claudeHarness, "--event":
		return true
	}
	return slices.ContainsFunc(claudeHooks, func(hk claudeHook) bool { return hk.event == s })
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
