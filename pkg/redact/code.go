// SPDX-License-Identifier: AGPL-3.0-or-later

package redact

import (
	"strings"
)

// CodeBlocks returns a TypeCode span for every fenced code block in an
// agent message, so a reveal shows a marker with a line count in its place
// by default (DESIGN 7.5 step 6). The teen can add any block back, so these
// spans are kept apart from Find's: the secrets Find reports inside a block
// stay removed when the block is restored.
//
// Fences follow CommonMark 0.31.2 section 4.5: a run of three or more
// backticks or tildes opens a block, the info string after a backtick fence
// can't contain a backtick, and the block ends at a line holding only a run
// of the same character at least as long, or at the end of the text. A
// fence may follow one block-quote marker and one list marker, as in
// "1. ```sh" or "> ```", since CommonMark puts such a block inside the
// list item or the quote; the closing fence of a block in a quote comes
// after its own quote marker. Two rules are looser than CommonMark,
// because agents put code in nested lists: an opening fence may be
// indented by any amount, and a closing fence by up to three columns more
// than its opening fence, counting the columns of any markers.
//
// A span runs from the first fence character to the end of the closing
// fence. Lines counts the lines between the fences, as SM 1 counts
// code_lines. An unclosed block runs to the end of its last line that isn't
// blank, and its trailing blank lines aren't counted.
func CodeBlocks(text string) []Span {
	var spans []Span
	var open *fence
	var cur Span
	// For an unclosed block: where its last non-blank line ends, and how
	// many lines it has up to there.
	lastEnd, lastLines := 0, 0
	for at := 0; at < len(text); {
		end := strings.IndexByte(text[at:], '\n')
		next := len(text)
		if end < 0 {
			end = len(text)
		} else {
			end += at
			next = end + 1
		}
		line := strings.TrimSuffix(text[at:end], "\r")
		switch {
		case open == nil:
			if f, ok := openingFence(line); ok {
				open = &f
				cur = Span{Start: at + f.col, Type: TypeCode, Lang: f.lang}
				lastEnd, lastLines = at+len(strings.TrimRight(line, " \t")), 0
			}
		case closes(line, open):
			cur.End = at + len(strings.TrimRight(line, " \t"))
			spans = append(spans, cur)
			open = nil
		default:
			cur.Lines++
			if t := strings.TrimRight(line, " \t"); t != "" {
				lastEnd, lastLines = at+len(t), cur.Lines
			}
		}
		at = next
	}
	if open != nil {
		cur.End, cur.Lines = lastEnd, lastLines
		spans = append(spans, cur)
	}
	return spans
}

// fence is an opening code fence.
type fence struct {
	char   byte   // '`' or '~'
	n      int    // length of the run
	indent int    // columns before the run, markers included
	col    int    // byte offset of the run in its line
	lang   string // the info string's first word, if it's a plain language name
	quoted bool   // whether a block-quote marker comes before the run
}

func openingFence(line string) (fence, bool) {
	indent, col, quoted := contentStart(line)
	rest := line[col:]
	if rest == "" || rest[0] != '`' && rest[0] != '~' {
		return fence{}, false
	}
	n := run(rest, rest[0])
	if n < 3 {
		return fence{}, false
	}
	info := strings.Trim(rest[n:], " \t")
	if rest[0] == '`' && strings.Contains(info, "`") {
		return fence{}, false
	}
	return fence{char: rest[0], n: n, indent: indent, col: col, lang: language(info), quoted: quoted}, true
}

func closes(line string, f *fence) bool {
	indent, col := blanks(line, 0, 0)
	if f.quoted && col < len(line) && line[col] == '>' {
		indent, col = blanks(line, col+1, indent+1)
	}
	if indent > f.indent+3 {
		return false
	}
	rest := line[col:]
	n := run(rest, f.char)
	return n >= f.n && strings.Trim(rest[n:], " \t") == ""
}

// contentStart skips a line's indentation, then one block-quote marker and
// one list marker if they're there, each with the blanks after it, and
// returns the columns and the bytes it skipped and whether it skipped a
// quote marker.
func contentStart(line string) (cols, n int, quoted bool) {
	cols, n = blanks(line, 0, 0)
	if n < len(line) && line[n] == '>' {
		quoted = true
		cols, n = blanks(line, n+1, cols+1)
	}
	if m := listMarker(line[n:]); m > 0 {
		cols, n = blanks(line, n+m, cols+m)
	}
	return cols, n, quoted
}

// listMarker returns the length of the list marker that starts s: a
// bullet, -, * or +, or one to nine digits and a . or ). A marker needs a
// space or a tab after it, so "**bold**" and "1.5" have none.
func listMarker(s string) int {
	m := 0
	if s != "" && strings.IndexByte("-*+", s[0]) >= 0 {
		m = 1
	} else {
		for m < len(s) && m < 9 && isDigit(s[m]) {
			m++
		}
		if m == 0 || m == len(s) || s[m] != '.' && s[m] != ')' {
			return 0
		}
		m++
	}
	if m < len(s) && (s[m] == ' ' || s[m] == '\t') {
		return m
	}
	return 0
}

// blanks skips the spaces and tabs at line[n:], counting columns on from
// cols, with a tab moving to the next multiple of four as in CommonMark,
// and returns the column and the byte offset it stopped at.
func blanks(line string, n, cols int) (int, int) {
	for n < len(line) {
		switch line[n] {
		case ' ':
			cols++
		case '\t':
			cols += 4 - cols%4
		default:
			return cols, n
		}
		n++
	}
	return cols, n
}

func run(s string, c byte) int {
	n := 0
	for n < len(s) && s[n] == c {
		n++
	}
	return n
}

// language returns the first word of an info string, lowercased, when it
// looks like a language name such as "go", "c++" or "shell-session". A
// first word that could be a file name or anything longer comes back
// empty, because an info string is the agent's free text.
func language(info string) string {
	word := info
	if i := strings.IndexAny(info, " \t{"); i >= 0 {
		word = info[:i]
	}
	word = strings.ToLower(word)
	if word == "" || len(word) > 20 {
		return ""
	}
	for i := 0; i < len(word); i++ {
		c := word[i]
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '+' || c == '#' || c == '_' || c == '-') {
			return ""
		}
	}
	return word
}
