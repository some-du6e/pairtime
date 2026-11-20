// SPDX-License-Identifier: AGPL-3.0-or-later

package features

import (
	"regexp"
	"strings"
)

// The patterns of spec 10.4 step 2. Each is matched against a whole line,
// so each is anchored here; Python's re.fullmatch does the same in the
// reference. They use ASCII classes only and need no lookaround or
// backreference, so they mean the same in Go's regexp and Python's re, and
// '.' matches any code point but LF, which no line holds.
var (
	// fenceOpen is F1, a fence and the rest of its line.
	fenceOpen = regexp.MustCompile("^ {0,3}(`{3,}|~{3,})(.*)$")
	// fenceClose is F2.
	fenceClose = regexp.MustCompile("^ {0,3}(`{3,}|~{3,})[ \t]*$")
	// pasteOpen is P1, the wrapper Claude Code puts around pastes (SM 1.2).
	pasteOpen = regexp.MustCompile(`^<pasted_content(?: [^>]*)?>$`)
	// errorLine is E1 to E6, SM 4.2's first error lines: an exception in
	// Python, JavaScript or Java, Rust's error[E0xxx], a compiler's
	// file:line:col: error:, pytest's FAILED, go test's --- FAIL: and a Go
	// panic.
	errorLine = regexp.MustCompile(`^(?:` + strings.Join([]string{
		`(?:Uncaught )?(?:[A-Za-z_$][A-Za-z0-9_$]*\.)*[A-Za-z0-9_$]*(?:Error|Exception)(?::.*)?`,
		`error\[E[0-9]{4}\](?::.*)?`,
		`[^ \t]+:[0-9]+:[0-9]+: (?:fatal )?error:.*`,
		`FAILED .+`,
		`--- FAIL: .+`,
		`panic: .+`,
	}, "|") + `)$`)
)

const (
	pasteClose = "</pasted_content>"
	traceback  = "Traceback (most recent call last):"
)

// splitLines splits text at every LF and removes one CR from the end of
// each line that ends with one (spec 10.4 step 1).
func splitLines(text string) []string {
	lines := strings.Split(text, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimSuffix(l, "\r")
	}
	return lines
}

// openingFence returns the fence that line opens, or "" when line opens
// none: a backtick fence whose line holds another backtick, such as
// ```js x = 1```, isn't one.
func openingFence(line string) string {
	m := fenceOpen.FindStringSubmatch(line)
	if m == nil || m[1][0] == '`' && strings.Contains(m[2], "`") {
		return ""
	}
	return m[1]
}

// closesFence reports whether line closes a block opened by fence: a fence
// of the same character, at least as long.
func closesFence(line, fence string) bool {
	m := fenceClose.FindStringSubmatch(line)
	return m != nil && m[1][0] == fence[0] && len(m[1]) >= len(fence)
}

// isErrorLine reports whether s, a stripped line, matches one of E1 to E6.
func isErrorLine(s string) bool { return errorLine.MatchString(s) }

func indented(line string) bool {
	return strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t")
}

// removedLines returns the indices of the lines that spec 10.4 step 2
// removes: fenced blocks, pasted blocks, tracebacks and error lines with
// the indented lines after them. At each line the first rule that applies
// removes it and the lines it names, and the walk goes on after them.
func removedLines(lines []string) []int {
	var removed []int
	for i := 0; i < len(lines); {
		start, s := i, strip(lines[i])
		switch fence := openingFence(lines[i]); {
		case fence != "":
			i++
			for i < len(lines) && !closesFence(lines[i], fence) {
				i++
			}
			i = min(i+1, len(lines))
		case pasteOpen.MatchString(s):
			i++
			for i < len(lines) && strip(lines[i]) != pasteClose {
				i++
			}
			i = min(i+1, len(lines))
		case s == traceback || isErrorLine(s):
			i++
			for i < len(lines) && indented(lines[i]) {
				i++
			}
		default:
			i++
			continue
		}
		for j := start; j < i; j++ {
			removed = append(removed, j)
		}
	}
	return removed
}

// Normalized is a text after spec 10.4's normalization.
type Normalized struct {
	// RemovedLines are the indices, from 0, of the lines step 2 removed,
	// in the lines of step 1.
	RemovedLines []int
	// Words are the normalized words.
	Words []string
}

// Text returns the normalized text: the words joined by single spaces.
func (n Normalized) Text() string { return strings.Join(n.Words, " ") }

// Normalize applies spec 10.4 to text, a committed text (spec 10.1): it
// splits the lines, removes pasted blocks and error text by line, takes
// the words of the lines that stay, lowercases them with Unicode 15.0.0's
// simple mapping, applies the substitutions within each word, and marks
// retry phrases. text should be valid UTF-8, as every committed text is.
func Normalize(text string) Normalized {
	lines := splitLines(text)
	removed := removedLines(lines)
	gone := make([]bool, len(lines))
	for _, i := range removed {
		gone[i] = true
	}
	var words []string
	for i, line := range lines {
		if gone[i] {
			continue
		}
		for _, w := range Words(line) {
			words = append(words, substitute(strings.Map(Lower, w)))
		}
	}
	return Normalized{RemovedLines: removed, Words: replaceRetries(words)}
}

// ErrorSignature returns the error signature of text (spec 10.6): its first
// line, searching from the top and inside fenced and pasted blocks too,
// whose stripped form matches one of E1 to E6, with each word passed
// through spec 10.4's substitutions and the words joined by single spaces.
// It isn't lowercased and gets no retry step. ok is false when text has no
// such line. A traceback's signature is its exception line.
func ErrorSignature(text string) (sig string, ok bool) {
	for _, line := range splitLines(text) {
		s := strip(line)
		if !isErrorLine(s) {
			continue
		}
		words := Words(s)
		for i, w := range words {
			words[i] = substitute(w)
		}
		return strings.Join(words, " "), true
	}
	return "", false
}

// CommandText returns a command line as spec 10.6 keys it: each run of
// whitespace collapsed to one space, none at either end, and everything
// else kept as run. It is "" for a command that is all whitespace.
func CommandText(cmd string) string { return strings.Join(Words(cmd), " ") }

// The substitutions of spec 10.4 step 5.
var (
	urlStart = regexp.MustCompile(`[A-Za-z][A-Za-z0-9+.-]*://`)
	ident    = regexp.MustCompile("`[^`]+`")
)

// substitute applies spec 10.4 step 5 within one word, in order: a URL to
// the end of the word, backticked identifiers, paths, hex runs, numbers.
// Every class here is ASCII, so the runs are found byte by byte and the
// bytes of other code points pass through.
func substitute(w string) string {
	if loc := urlStart.FindStringIndex(w); loc != nil {
		w = w[:loc[0]] + "<url>"
	}
	w = ident.ReplaceAllLiteralString(w, "<id>")
	w = replaceRuns(w, isPathChar, func(run string) string {
		if isPath(run) {
			return "<path>"
		}
		return run
	})
	w = replaceRuns(w, isAlnum, func(run string) string {
		if isHex(run) {
			return "<hex>"
		}
		return run
	})
	return replaceRuns(w, isDigit, func(string) string { return "<n>" })
}

// replaceRuns returns w with each maximal run of bytes that in accepts
// replaced by fn(run). in accepts only ASCII bytes.
func replaceRuns(w string, in func(byte) bool, fn func(string) string) string {
	var b strings.Builder
	for i := 0; i < len(w); {
		j := i
		for j < len(w) && in(w[j]) {
			j++
		}
		if j > i {
			b.WriteString(fn(w[i:j]))
			i = j
			continue
		}
		b.WriteByte(w[i])
		i++
	}
	return b.String()
}

func isAlpha(c byte) bool   { return 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' }
func isDigit(c byte) bool   { return '0' <= c && c <= '9' }
func isAlnum(c byte) bool   { return isAlpha(c) || isDigit(c) }
func isSep(c byte) bool     { return c == '/' || c == '\\' }
func isHexByte(c byte) bool { return isDigit(c) || 'a' <= c && c <= 'f' || 'A' <= c && c <= 'F' }

// isPathChar accepts ASCII letters and digits and . _ - ~ $ % + @ : / \.
func isPathChar(c byte) bool { return isAlnum(c) || strings.IndexByte(`._-~$%+@:/\`, c) >= 0 }

var pathStarts = []string{"/", `\`, "./", `.\`, "../", `..\`, "~/", `~\`}

// isPath reports whether run, a maximal run of path characters, is a path
// (spec 10.4 step 5.3): it holds a separator and a letter or digit, and it
// starts like a path, starts with a drive, holds two or more separators,
// or its last component holds a '.' followed by a letter or digit.
func isPath(run string) bool {
	seps := strings.Count(run, "/") + strings.Count(run, `\`)
	if seps == 0 || !containsByte(run, isAlnum) {
		return false
	}
	for _, p := range pathStarts {
		if strings.HasPrefix(run, p) {
			return true
		}
	}
	if len(run) >= 3 && isAlpha(run[0]) && run[1] == ':' && isSep(run[2]) {
		return true
	}
	if seps >= 2 {
		return true
	}
	last := run[strings.LastIndexAny(run, `/\`)+1:]
	for k := 0; k+1 < len(last); k++ {
		if last[k] == '.' && isAlnum(last[k+1]) {
			return true
		}
	}
	return false
}

// isHex reports whether run, a maximal run of ASCII letters and digits, is
// hex (spec 10.4 step 5.4): 0x or 0X and one or more hex digits, or seven
// or more hex digits with at least one digit and one letter among them.
func isHex(run string) bool {
	if len(run) >= 3 && run[0] == '0' && (run[1] == 'x' || run[1] == 'X') && allBytes(run[2:], isHexByte) {
		return true
	}
	return len(run) >= 7 && allBytes(run, isHexByte) && containsByte(run, isDigit) && containsByte(run, isAlpha)
}

func allBytes(s string, f func(byte) bool) bool {
	for i := 0; i < len(s); i++ {
		if !f(s[i]) {
			return false
		}
	}
	return true
}

func containsByte(s string, f func(byte) bool) bool {
	for i := 0; i < len(s); i++ {
		if f(s[i]) {
			return true
		}
	}
	return false
}

// retryPhrases are SM 4.2's retry phrases, longest first, as spec 10.4
// step 6 tries them.
var retryPhrases = [][]string{
	{"still", "not", "working"},
	{"try", "again"}, {"fix", "it"}, {"still", "broken"}, {"doesnt", "work"},
	{"again"}, {"retry"},
}

// replaceRetries walks the words from the left and turns each retry phrase
// into the single word <retry> (spec 10.4 step 6).
func replaceRetries(words []string) []string {
	out := make([]string, 0, len(words))
	for i := 0; i < len(words); {
		k := retryAt(words[i:])
		if k == 0 {
			out = append(out, words[i])
			i++
			continue
		}
		out = append(out, "<retry>")
		i += k
	}
	return out
}

// retryAt returns the number of words of the retry phrase that words
// starts with, trying the longest phrases first, or 0 for none.
func retryAt(words []string) int {
	for _, p := range retryPhrases {
		if len(words) < len(p) {
			continue
		}
		match := true
		for j, pw := range p {
			if bare(words[j]) != pw {
				match = false
				break
			}
		}
		if match {
			return len(p)
		}
	}
	return 0
}

// bare returns a word as retry phrases compare it: without its leading and
// trailing punctuation, then without every ' and U+2019 left inside, so
// doesn't, doesn’t and doesnt compare equal.
func bare(w string) string {
	w = strings.TrimFunc(w, isEdgePunct)
	w = strings.ReplaceAll(w, "'", "")
	return strings.ReplaceAll(w, "\U00002019", "")
}

// isEdgePunct accepts the 32 printable ASCII characters that are neither
// letters, digits nor space, and U+2018, U+2019, U+201C and U+201D.
func isEdgePunct(r rune) bool {
	switch {
	case 0x21 <= r && r <= 0x2f, 0x3a <= r && r <= 0x40, 0x5b <= r && r <= 0x60, 0x7b <= r && r <= 0x7e:
		return true
	}
	return r == 0x2018 || r == 0x2019 || r == 0x201c || r == 0x201d
}
