// SPDX-License-Identifier: AGPL-3.0-or-later

package wakacfg

import (
	"bytes"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// kind is what an entry is.
type kind int

const (
	blank kind = iota
	comment
	header
	pair // a key and its value
)

// entry is one thing wakatime-cli's parser reads in a config: a blank
// line, a comment, a section's header, or a key and its value, which takes
// in the indented lines after it.
type entry struct {
	kind kind
	// start and end are the entry's bytes in the file, its last line's
	// line break included, and lines the lines it takes.
	start, end, lines int
	// section is the section a key is in, or a header's section.
	section string
	// key and value are a key's name and value as the parser reads them.
	key, value string
}

// file is a config as wakatime-cli's parser reads it.
type file struct {
	data    []byte
	entries []entry
	// eol is the line break the package writes: CRLF in a file that has
	// one, and LF otherwise.
	eol string
}

// ReadError says why the package won't change a config: at Line, counted
// from 1, it holds something wakatime-cli's parser reads in a way the
// package doesn't follow line by line, or can't read at all.
type ReadError struct {
	Line int
	Why  string
}

func (e *ReadError) Error() string { return fmt.Sprintf("line %d %s", e.Line, e.Why) }

// bom is the UTF-8 byte order mark, which the parser skips.
var bom = []byte("\xef\xbb\xbf")

// parse reads data as gopkg.in/ini.v1 v1.67.3 does with wakatime-cli's
// options: only AllowPythonMultilineValues set (pkg/vipertools.New), so
// = and : end a key, a # or ; ends a value, and an indented line after a
// value is part of it. It refuses what the parser would fail on, and the
// forms whose lines it doesn't follow here: a key in quotes, a value in
// triple quotes or backticks, and one that goes on after a backslash.
func parse(data []byte) (*file, error) {
	f := &file{data: data, eol: "\n"}
	if bytes.Contains(data, []byte("\r\n")) {
		f.eol = "\r\n"
	}
	off := 0
	switch {
	case bytes.HasPrefix(data, bom):
		off = len(bom)
	case bytes.HasPrefix(data, []byte{0xfe, 0xff}), bytes.HasPrefix(data, []byte{0xff, 0xfe}):
		return nil, &ReadError{Line: 1, Why: "starts the file as UTF-16 text"}
	}
	lines := splitLines(data, off)
	for i, l := range lines {
		text := data[l.start:l.end]
		if !utf8.Valid(text) {
			return nil, &ReadError{Line: i + 1, Why: "isn't UTF-8 text"}
		}
		if bytes.IndexByte(text, 0) >= 0 {
			return nil, &ReadError{Line: i + 1, Why: "holds a NUL byte"}
		}
	}
	section := ""
	for i := 0; i < len(lines); i++ {
		l := lines[i]
		t := bytes.TrimLeftFunc(f.data[l.start:l.end], unicode.IsSpace)
		e := entry{start: l.start, end: l.end, lines: 1, section: section}
		switch {
		case len(t) == 0:
			e.kind = blank
		case t[0] == '#' || t[0] == ';':
			e.kind = comment
		case t[0] == '[':
			c := bytes.LastIndexByte(t, ']')
			if c < 0 {
				return nil, &ReadError{Line: i + 1, Why: "opens a section with [ and doesn't close it with ]"}
			}
			if c == 1 {
				return nil, &ReadError{Line: i + 1, Why: "has a section with no name"}
			}
			e.kind, e.section = header, string(t[1:c])
			section = e.section
		default:
			key, rest, why := keyOf(t)
			if why != "" {
				return nil, &ReadError{Line: i + 1, Why: why}
			}
			value, more, why := f.valueOf(rest, lines[i+1:])
			if why != "" {
				return nil, &ReadError{Line: i + 1, Why: why}
			}
			e.kind, e.key, e.value = pair, key, value
			e.lines += more
			e.end = lines[i+more].end
			i += more
		}
		f.entries = append(f.entries, e)
	}
	return f, nil
}

// line is one line of a file: its bytes from start to end, its line break
// included.
type line struct{ start, end int }

// splitLines splits data from off into lines, as the parser reads them, up
// to each LF.
func splitLines(data []byte, off int) []line {
	var out []line
	for off < len(data) {
		end := len(data)
		if n := bytes.IndexByte(data[off:], '\n'); n >= 0 {
			end = off + n + 1
		}
		out = append(out, line{off, end})
		off = end
	}
	return out
}

// keyOf reads a key's name from t, a line without the space before it, as
// the parser's readKeyName does, and returns what follows its = or :.
func keyOf(t []byte) (key string, rest []byte, why string) {
	if t[0] == '"' || t[0] == '`' {
		return "", nil, "has a key in quotes"
	}
	i := bytes.IndexAny(t, "=:")
	switch {
	case i < 0:
		return "", nil, "is neither a section, a comment nor a key with = or : after it"
	case i == 0:
		return "", nil, "has a value with no key before it"
	}
	return strings.TrimSpace(string(t[:i])), t[i+1:], ""
}

// valueOf reads a value from in, what follows a key's = or :, as the
// parser's readValue does, and returns how many of the next lines are part
// of it.
func (f *file) valueOf(in []byte, next []line) (value string, more int, why string) {
	v := bytes.TrimLeftFunc(in, unicode.IsSpace)
	if len(v) == 0 {
		if len(in) > 0 && in[len(in)-1] == '\n' {
			value, more = f.continued("", next)
		}
		return value, more, ""
	}
	if (len(v) > 3 && bytes.HasPrefix(v, []byte(`"""`))) || v[0] == '`' {
		return "", 0, "has a value in triple quotes or backticks"
	}
	last := v[len(v)-1]
	s := strings.TrimSpace(string(v))
	if s[len(s)-1] == '\\' {
		return "", 0, "has a value that goes on to the next line after a backslash"
	}
	if i := strings.IndexAny(s, "#;"); i >= 0 {
		s = strings.TrimSpace(s[:i])
	}
	if quoted(s, '\'') || quoted(s, '"') {
		return s[1 : len(s)-1], 0, ""
	}
	if last == '\n' {
		value, more = f.continued(s, next)
		return value, more, ""
	}
	return s, 0, ""
}

// continued adds to value the lines of next that go on with it, as the
// parser's readPythonMultilines does: each line that starts with a space,
// a tab or a form feed, without its LF.
func (f *file) continued(value string, next []line) (string, int) {
	n := 0
	for _, l := range next {
		text := f.data[l.start:l.end]
		if !indented(text) {
			break
		}
		value += "\n" + string(bytes.TrimSuffix(text, []byte("\n")))
		n++
	}
	return value, n
}

// indented reports whether text starts as a line that goes on with the
// value before it does (pythonMultiline in the parser).
func indented(text []byte) bool {
	return len(text) > 0 && (text[0] == ' ' || text[0] == '\t' || text[0] == '\f')
}

// quoted reports whether s is in quotes q, which the parser takes off, as
// its hasSurroundedQuote does.
func quoted(s string, q byte) bool {
	return len(s) >= 2 && s[0] == q && s[len(s)-1] == q && strings.IndexByte(s[1:], q) == len(s)-2
}

// isSection reports whether a section's name is name as wakatime-cli reads
// it: viper lowercases the names of sections and keys.
func isSection(section, name string) bool { return strings.ToLower(section) == name }

// pairs returns the keys and values the file holds, in order.
func (f *file) pairs() []entry {
	var out []entry
	for _, e := range f.entries {
		if e.kind == pair {
			out = append(out, e)
		}
	}
	return out
}
