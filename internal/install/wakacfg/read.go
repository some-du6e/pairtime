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
	// key and value are a key's name and value as the parser reads them,
	// and delim where the = or : after the key is in the file.
	key, value string
	delim      int
}

// file is a config as wakatime-cli's parser reads it.
type file struct {
	data    []byte
	entries []entry
	// eol is the line break the package writes: CRLF in a file that has
	// one, and LF otherwise.
	eol string
	// peek is how far the parser looks past a key's line for the indented
	// lines that go on with its value (continued).
	peek int
}

// peekSize is the most go-ini looks past a key's line for the indented
// lines of its value: File.parse peeks its 4096-byte reader
// (minReaderBufferSize, as pkg/vipertools sets no ReaderBufferSize), and
// readPythonMultilines reads only what that peek holds. A file shorter
// than that after its byte order mark is peeked whole.
const peekSize = 4096

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
// value is part of it. A key may be in quotes, and a value in triple
// quotes or backticks, over as many lines as it takes, as go-ini's own
// SaveTo writes them, which wakatime-cli's --config-write rewrites the
// whole file with (pkg/ini). It refuses what the parser would fail on, and
// a value that goes on after a backslash, whose lines it doesn't follow.
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
	f.peek = min(peekSize, len(data)-off)
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
			e.kind, e.key, e.value, e.delim = pair, key, value, l.end-len(rest)-1
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
// the parser's readKeyName does, and returns what follows its = or :. A
// key in quotes, ", """ or `, ends at the first closing quote, and its = or
// : is the first after that.
func keyOf(t []byte) (key string, rest []byte, why string) {
	q := ""
	switch {
	case len(t) > 6 && bytes.HasPrefix(t, []byte(`"""`)):
		q = `"""`
	case t[0] == '"':
		q = `"`
	case t[0] == '`':
		q = "`"
	}
	if q != "" {
		end := bytes.Index(t[len(q):], []byte(q))
		if end < 0 {
			return "", nil, "has a key in quotes that aren't closed"
		}
		end += len(q)
		i := bytes.IndexAny(t[end+len(q):], "=:")
		if i < 0 {
			return "", nil, "is neither a section, a comment nor a key with = or : after it"
		}
		return strings.TrimSpace(string(t[len(q):end])), t[end+len(q)+i+1:], ""
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
			return f.continued("", next)
		}
		return "", 0, ""
	}
	q := ""
	switch {
	case len(v) > 3 && bytes.HasPrefix(v, []byte(`"""`)):
		q = `"""`
	case v[0] == '`':
		q = "`"
	}
	if q != "" {
		return f.inQuotes(v[len(q):], q, next)
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
		return f.continued(s, next)
	}
	return s, 0, ""
}

// inQuotes reads a value in quotes q from v, what follows its opening
// quotes, as the parser's readValue and readMultilines do: up to the last
// q on the line, or else on through the lines of next, line breaks
// included, up to the last q on the first line that has one. What follows
// the closing quotes is no part of it.
func (f *file) inQuotes(v []byte, q string, next []line) (value string, more int, why string) {
	if end := bytes.LastIndex(v, []byte(q)); end >= 0 {
		return string(v[:end]), 0, ""
	}
	value = string(v)
	for j, l := range next {
		text := f.data[l.start:l.end]
		end := bytes.LastIndex(text, []byte(q))
		if end < 0 {
			value += string(text)
			continue
		}
		if bytes.HasSuffix(bytes.TrimSpace(text[end+len(q):]), []byte(`\`)) {
			return "", 0, "has a value in quotes that goes on after a backslash"
		}
		return value + string(text[:end]), j + 1, ""
	}
	return "", 0, "has a value in quotes that aren't closed"
}

// continued adds to value the lines of next that go on with it, as the
// parser's readPythonMultilines does: each line that starts with a space,
// a tab or a form feed, without its LF. It refuses lines that go on past
// f.peek bytes after the key's line, where the parser ends the value, in
// the middle of a line if need be, and reads what is left of them as lines
// of their own.
func (f *file) continued(value string, next []line) (string, int, string) {
	n, size := 0, 0
	for _, l := range next {
		text := f.data[l.start:l.end]
		if !indented(text) {
			break
		}
		if size += len(text); size > f.peek {
			return "", 0, fmt.Sprintf("has indented lines after it that go on past the %d bytes wakatime-cli reads of them", f.peek)
		}
		value += "\n" + string(bytes.TrimSuffix(text, []byte("\n")))
		n++
	}
	return value, n, ""
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
