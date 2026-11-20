// SPDX-License-Identifier: AGPL-3.0-or-later

package features

import (
	"strings"
	"unicode/utf8"
)

// IsSpace reports whether r is whitespace in the sense of spec 10.2: one of
// the 25 code points of Unicode's White_Space property, which is the same
// set in Unicode 15.0.0 and 17.0.0. It is the set unicode.IsSpace tests,
// written out so that no rule here leans on a library's tables. Python's
// str.isspace isn't this set: it adds U+001C to U+001F.
func IsSpace(r rune) bool {
	switch {
	case 0x09 <= r && r <= 0x0d, r == 0x20, r == 0x85, r == 0xa0, r == 0x1680:
		return true
	case 0x2000 <= r && r <= 0x200a, r == 0x2028, r == 0x2029, r == 0x202f, r == 0x205f, r == 0x3000:
		return true
	}
	return false
}

// Tokens returns the tokens of text (spec 10.2): its maximal runs of
// whitespace and its maximal runs of other code points, in order. So
// neighbouring tokens are of different kinds, no token is empty, and the
// tokens concatenate to text. The empty text has none. text should be
// valid UTF-8, as every committed text is; an invalid byte counts as a
// code point that isn't whitespace.
func Tokens(text string) []string {
	var out []string
	start, space := 0, false
	for i, r := range text {
		s := IsSpace(r)
		if i > 0 && s != space {
			out = append(out, text[start:i])
			start = i
		}
		space = s
	}
	if text != "" {
		out = append(out, text[start:])
	}
	return out
}

// Words returns the tokens of text that aren't whitespace, in order.
func Words(text string) []string {
	var out []string
	for _, t := range Tokens(text) {
		if !isSpaceToken(t) {
			out = append(out, t)
		}
	}
	return out
}

// isSpaceToken reports whether a token is a whitespace token. A token is
// all of one kind, so its first code point decides.
func isSpaceToken(t string) bool {
	r, _ := utf8.DecodeRuneInString(t)
	return t != "" && IsSpace(r)
}

// strip returns s with its leading and trailing whitespace removed, in the
// sense of IsSpace.
func strip(s string) string { return strings.TrimFunc(s, IsSpace) }
