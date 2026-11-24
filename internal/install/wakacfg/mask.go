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
// names stay, pairtime's line for key stays as it is, and so do the mark
// and the api_url of a config pairtime made for it, and every line stays a
// line, so the diff of two masked files lines up as the files do. A config
// pairtime made has its api_key masked always: pairtime's own key never
// needs to be on screen, and the key of one the teen took over is theirs.
// With key "", every line of pairtime's stays, and a config pairtime made
// by its mark. A file the package can't read has every line masked.
func Masked(data []byte, key string) []byte {
	f, err := parse(data)
	if err != nil {
		return maskLines(data, 0, len(data))
	}
	keep := map[int]bool{}
	for i, e := range f.entries {
		if _, k, ok := ownLine(e); ok && (key == "" || k == key) {
			keep[i] = true
		}
	}
	if u, _, ok := f.ownConfig(key); ok {
		// The api_key stays masked: pairtime's own key never needs to be on
		// screen, and one that isn't pairtime's is the teen's.
		keep[u] = true
		if m, _ := f.mark(); m >= 0 {
			keep[m] = true
		}
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
			b.Write(data[e.start : e.delim+1])
			b.Write(maskRest(data[e.delim+1 : s[0].end]))
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
