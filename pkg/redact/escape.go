// SPDX-License-Identifier: AGPL-3.0-or-later

package redact

import (
	"sort"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// Raw capture files are JSON. Inside a JSON string a line break is the two
// bytes \n, a tab is \t and a quote is \", so in a raw transcript the byte
// before a value that starts an embedded line is the letter n, and the byte
// after a quoted key is a backslash. That hides the word edges the known
// values and the PII patterns look for, and the terminators gitleaks' token
// rules need. The functions here let each step see through the escapes
// without moving an offset.

// jsonView returns text with its backslash escapes blanked out, so that it
// reads like the text inside its JSON strings, and whether that changed
// anything. Every byte keeps its offset, so a span found in the view is a
// span of text.
//
// Every backslash becomes a space, and so does the letter of \t, \r, \b and
// \f. The n of \n becomes a line break, so lines follow the embedded text.
// The quote of \" and the slash of \/ stay. A \u escape, or a pair of them
// for a character outside the BMP, becomes spaces, unless it stands for a
// letter, digit or mark: then it becomes that character repeated to fill
// its bytes, so a word that holds one, such as an email address with an
// accented letter, stays one word.
//
// A run of backslashes counts as one, whatever its length, so \\n, a line
// break in JSON that was itself put in a JSON string, breaks the line too.
// A JSON parser reads \\n as a backslash and an n. Reading it as a line
// break only adds places where a word can end, and every step still runs
// over the text itself as well.
func jsonView(text string) (string, bool) {
	if strings.IndexByte(text, '\\') < 0 {
		return text, false
	}
	b := []byte(text)
	for i := 0; i < len(text); {
		if text[i] != '\\' {
			i++
			continue
		}
		last := i
		for ; i < len(text) && text[i] == '\\'; i++ {
			b[i] = ' '
			last = i
		}
		if i == len(text) {
			break
		}
		switch text[i] {
		case 'n':
			b[i] = '\n'
			i++
		case 't', 'r', 'b', 'f':
			b[i] = ' '
			i++
		case 'u':
			if r, end, ok := unicodeEscape(text, last); ok {
				fill(b[last:end], r)
				i = end
			}
		}
	}
	return string(b), true
}

// afterEscape reports whether text[:i] ends with a JSON escape that stands
// for a character outside any word: \n, \t, \r, \b, \f or a \u escape. In
// a raw capture that's where an embedded line or field starts, so it's a
// word edge even though the byte before i is a letter or a hex digit. As
// in jsonView, a run of backslashes before the letter counts as one.
func afterEscape(text string, i int) bool {
	if i >= 2 && text[i-2] == '\\' && strings.IndexByte("ntrbf", text[i-1]) >= 0 {
		return true
	}
	return endsUnicodeEscape(text, i)
}

// escapedBefore reports whether the byte before i is part of a backslash
// escape: it follows a backslash, or it's the last hex digit of a \u
// escape. The email scan stops there, so an address never takes the n of
// \n or the digits of a \u escape with it.
func escapedBefore(text string, i int) bool {
	return i >= 2 && text[i-2] == '\\' || endsUnicodeEscape(text, i)
}

func endsUnicodeEscape(text string, i int) bool {
	_, ok := hex4(text, i-4)
	return ok && i >= 6 && text[i-6] == '\\' && text[i-5] == 'u'
}

// unicodeEscape decodes the \u escape at text[at:], with the second half of
// a surrogate pair when one follows, and returns the character and where
// the escape ends. A lone surrogate decodes to U+FFFD.
func unicodeEscape(text string, at int) (rune, int, bool) {
	u, ok := hex4(text, at+2)
	if !ok || text[at] != '\\' || text[at+1] != 'u' {
		return 0, 0, false
	}
	r, end := rune(u), at+6
	if utf16.IsSurrogate(r) {
		r = utf8.RuneError
		if lo, ok := hex4(text, end+2); ok && text[end] == '\\' && text[end+1] == 'u' {
			if d := utf16.DecodeRune(rune(u), rune(lo)); d != utf8.RuneError {
				r, end = d, end+6
			}
		}
	}
	return r, end, true
}

// hex4 reads the four hex digits at s[i:].
func hex4(s string, i int) (uint16, bool) {
	if i < 0 || i+4 > len(s) {
		return 0, false
	}
	var v uint16
	for _, c := range []byte(s[i : i+4]) {
		var d byte
		switch {
		case c >= '0' && c <= '9':
			d = c - '0'
		case c >= 'a' && c <= 'f':
			d = c - 'a' + 10
		case c >= 'A' && c <= 'F':
			d = c - 'A' + 10
		default:
			return 0, false
		}
		v = v<<4 | uint16(d)
	}
	return v, true
}

// fill writes r over b as many times as it fits, when r is part of a word
// and fits exactly, and spaces otherwise.
func fill(b []byte, r rune) {
	n := utf8.RuneLen(r)
	if !isAlnum(r) || n < 1 || len(b)%n != 0 {
		for k := range b {
			b[k] = ' '
		}
		return
	}
	for k := 0; k < len(b); k += n {
		utf8.EncodeRune(b[k:], r)
	}
}

// keepEscapesWhole widens each span that starts or ends inside a backslash
// escape of a JSON string so that it covers the whole escape. Taking the n
// of \n out of a JSON string would leave a backslash in front of whatever
// replaces the span, which makes another escape or a file that no longer
// parses.
//
// Escapes are read the way a JSON parser reads them, left to right, and
// only inside double quotes, which a line break always closes: \\ is one
// escape, so the n of \\n is plain text; \", \/, \b, \f, \n, \r and \t are
// two bytes; a \u escape is six, or twelve for a surrogate pair. Outside
// quotes a backslash is plain text, so in a Windows path such as
// C:\Users\tom a span that starts at tom keeps the separator before it.
func keepEscapesWhole(text string, spans []Span) {
	if strings.IndexByte(text, '\\') < 0 {
		return
	}
	var escs [][2]int
	quoted := false
	for i := 0; i < len(text); i++ {
		switch text[i] {
		case '\n':
			quoted = false
		case '"':
			quoted = !quoted
		case '\\':
			if !quoted || i+1 >= len(text) {
				continue
			}
			end := 0
			if _, e, ok := unicodeEscape(text, i); ok {
				end = e
			} else if strings.IndexByte(`"\/bfnrt`, text[i+1]) >= 0 {
				end = i + 2
			}
			if end > 0 {
				escs = append(escs, [2]int{i, end})
				i = end - 1
			}
		}
	}
	// inside returns the escape that p falls strictly inside.
	inside := func(p int) (int, bool) {
		k := sort.Search(len(escs), func(k int) bool { return escs[k][1] > p })
		return k, k < len(escs) && escs[k][0] < p
	}
	for i := range spans {
		if k, ok := inside(spans[i].Start); ok {
			spans[i].Start = escs[k][0]
		}
		if k, ok := inside(spans[i].End); ok {
			spans[i].End = escs[k][1]
		}
	}
}
