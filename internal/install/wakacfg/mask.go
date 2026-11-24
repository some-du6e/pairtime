// SPDX-License-Identifier: AGPL-3.0-or-later

package wakacfg

import (
	"bytes"
	"unicode"
)

// masked stands in for text pairtime didn't write, in what Masked shows.
const masked = "***"

// Masked returns data as pairtime's install shows it in a diff: every
// value and comment masked but pairtime's own, since a config holds the
// teen's API keys and the consent screen's diff shows the lines around
// pairtime's, which teens screenshot for help and share. Sections and key
// names stay, pairtime's line and a config pairtime made stay as they are,
// and every line stays a line, so the diff of two masked files lines up as
// the files do. A file the package can't read has every line masked.
func Masked(data []byte) []byte {
	f, err := parse(data)
	if err != nil {
		return maskLines(data, 0, len(data))
	}
	keep := map[int]bool{}
	for i, e := range f.entries {
		if _, _, ok := ownLine(e); ok {
			keep[i] = true
		}
	}
	if u, k, ok := f.ownConfig(""); ok {
		keep[u], keep[k] = true, true
	}
	if len(f.entries) == 0 {
		return data // nothing but a byte order mark, if that
	}
	var b bytes.Buffer
	b.Write(data[:f.entries[0].start]) // a byte order mark
	for i, e := range f.entries {
		switch {
		case keep[i] || e.kind == blank:
			b.Write(data[e.start:e.end])
		case e.kind == header:
			// The section's name, with what follows its ] masked.
			text := data[e.start:e.end]
			c := bytes.LastIndexByte(text, ']')
			b.Write(text[:c+1])
			b.Write(maskRest(text[c+1:]))
		case e.kind == comment:
			text := data[e.start:e.end]
			n := len(text) - len(bytes.TrimLeftFunc(text, unicode.IsSpace))
			b.Write(text[:n+1]) // the white space and the # or ;
			b.Write(maskRest(text[n+1:]))
		default:
			// The key and its = or :, then the value masked, and each line
			// that goes on with it masked whole.
			s := splitLines(data[:e.end], e.start)
			first := data[s[0].start:s[0].end]
			n := len(first) - len(bytes.TrimLeftFunc(first, unicode.IsSpace))
			d := n + bytes.IndexAny(first[n:], "=:")
			b.Write(first[:d+1])
			b.Write(maskRest(first[d+1:]))
			for _, l := range s[1:e.lines] {
				b.Write(maskLines(data, l.start, l.end))
			}
		}
	}
	return b.Bytes()
}

// maskRest masks what text, the rest of a line, holds before its line
// break, if anything.
func maskRest(text []byte) []byte {
	brk := lineBreak(text)
	body := text[:len(text)-len(brk)]
	if len(bytes.TrimSpace(body)) == 0 {
		return text
	}
	return []byte(" " + masked + brk)
}

// maskLines masks the text of each line of data from start to end,
// keeping the white space before it and its line break.
func maskLines(data []byte, start, end int) []byte {
	var b bytes.Buffer
	for _, l := range splitLines(data[:end], start) {
		text := data[l.start:l.end]
		brk := lineBreak(text)
		body := text[:len(text)-len(brk)]
		trimmed := bytes.TrimLeftFunc(body, unicode.IsSpace)
		if len(trimmed) == 0 {
			b.Write(text)
			continue
		}
		b.Write(body[:len(body)-len(trimmed)])
		b.WriteString(masked + brk)
	}
	return b.Bytes()
}

// HasAPIURLs reports whether data, a config, has an [api_urls] section as
// wakatime-cli reads section names. A file the package can't read has
// none it knows of.
func HasAPIURLs(data []byte) bool {
	f, err := parse(data)
	if err != nil {
		return false
	}
	for _, e := range f.entries {
		if e.kind == header && isSection(e.section, apiURLs) {
			return true
		}
	}
	return false
}
