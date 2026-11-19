// SPDX-License-Identifier: AGPL-3.0-or-later

package redact

import (
	"cmp"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Type is the marker a removed span carries. It says what kind of thing was
// there, never what it was.
type Type string

// The types in pipeline order (DESIGN 7.5 step 6): known values, secrets,
// personal data, then code blocks and the teen's own removals. Merge uses
// the order to break ties.
const (
	TypeAccount  Type = "account"   // OS account name
	TypeHome     Type = "home"      // home directory
	TypeHost     Type = "host"      // hostname
	TypeGitName  Type = "git_name"  // git config user.name
	TypeGitEmail Type = "git_email" // git config user.email
	TypeGitHub   Type = "github"    // GitHub username
	TypeHCA      Type = "hca"       // Hack Club Account username
	TypePrivate  Type = "private"   // name of a private repo or folder
	TypeDeny     Type = "deny"      // an entry on the teen's deny list

	// TypeSecret is a match of one of gitleaks' rules; Span.Rule names it.
	TypeSecret Type = "secret"

	TypeCard    Type = "card"       // payment card number, Luhn-checked
	TypeIBAN    Type = "iban"       // IBAN, mod-97-checked
	TypeUSSSN   Type = "us_ssn"     // US Social Security number
	TypeUKNINO  Type = "uk_nino"    // UK National Insurance number
	TypeAadhaar Type = "in_aadhaar" // Indian Aadhaar number, Verhoeff-checked
	TypeEmail   Type = "email"      // email address

	// TypeCode is a fenced code block in an agent message (CodeBlocks).
	TypeCode Type = "code"
	// TypeManual is a span the teen removed by hand in the preview.
	TypeManual Type = "manual"
)

var typeOrder = []Type{
	TypeAccount, TypeHome, TypeHost, TypeGitName, TypeGitEmail, TypeGitHub, TypeHCA, TypePrivate, TypeDeny,
	TypeSecret,
	TypeCard, TypeIBAN, TypeUSSSN, TypeUKNINO, TypeAadhaar, TypeEmail,
	TypeCode, TypeManual,
}

// Valid reports whether t is one of the types above.
func (t Type) Valid() bool { return slices.Contains(typeOrder, t) }

// Reveal is the type a ledger reveal gives a span with type t: "known",
// "secret", "pii", "code" or "teen", the closed list of the ledger spec's
// redacted reveal (docs/spec/ledger-v1.md 10.3, WP-LG3). A reveal shows
// that coarser type, so it can't carry more than which step removed the
// span. Reveal returns "" for a type that isn't valid.
func (t Type) Reveal() string {
	switch t {
	case TypeAccount, TypeHome, TypeHost, TypeGitName, TypeGitEmail, TypeGitHub, TypeHCA, TypePrivate, TypeDeny:
		return "known"
	case TypeSecret:
		return "secret"
	case TypeCard, TypeIBAN, TypeUSSSN, TypeUKNINO, TypeAadhaar, TypeEmail:
		return "pii"
	case TypeCode:
		return "code"
	case TypeManual:
		return "teen"
	}
	return ""
}

func (t Type) rank() int {
	if i := slices.Index(typeOrder, t); i >= 0 {
		return i
	}
	return len(typeOrder)
}

// Span is a byte range of the original text to remove, text[Start:End],
// and the marker that stands in for it.
type Span struct {
	Start int  `json:"start"`
	End   int  `json:"end"`
	Type  Type `json:"type"`
	// Rule is the id of the gitleaks rule behind a TypeSecret span, such as
	// "github-pat", so the coach can tell the teen which key to rotate
	// (DESIGN 7.11).
	Rule string `json:"rule,omitempty"`
	// Lines is how many lines of code a TypeCode span holds, and Lang the
	// language its opening fence named, when that was a plain word.
	Lines int    `json:"lines,omitempty"`
	Lang  string `json:"lang,omitempty"`
}

// Marker is the text a reader sees in place of the span, such as "[SECRET]"
// or "[CODE go: 12 lines]".
func (s Span) Marker() string {
	if s.Type != TypeCode {
		return "[" + strings.ToUpper(string(s.Type)) + "]"
	}
	var b strings.Builder
	b.WriteString("[CODE")
	if s.Lang != "" {
		b.WriteString(" " + s.Lang)
	}
	b.WriteString(": " + strconv.Itoa(s.Lines) + " line")
	if s.Lines != 1 {
		b.WriteString("s")
	}
	b.WriteString("]")
	return b.String()
}

// Merge turns spans that may overlap into the sorted, disjoint set a reveal
// removes. Spans that overlap become one span covering all of them, which
// takes the type, rule and code details of its longest member; a tie goes
// to the type that comes first in pipeline order, then to the earlier span.
// Spans that only touch stay apart. Empty and inverted spans are dropped.
func Merge(spans []Span) []Span {
	in := make([]Span, 0, len(spans))
	for _, s := range spans {
		if s.Start < s.End && s.Start >= 0 {
			in = append(in, s)
		}
	}
	slices.SortFunc(in, func(a, b Span) int {
		return cmp.Or(cmp.Compare(a.Start, b.Start), cmp.Compare(b.End, a.End), cmp.Compare(a.Type.rank(), b.Type.rank()))
	})
	var out []Span
	for i := 0; i < len(in); {
		best, end := in[i], in[i].End
		j := i + 1
		for ; j < len(in) && in[j].Start < end; j++ {
			s := in[j]
			if l, bl := s.End-s.Start, best.End-best.Start; l > bl || l == bl && s.Type.rank() < best.Type.rank() {
				best = s
			}
			end = max(end, s.End)
		}
		best.Start, best.End = in[i].Start, end
		out = append(out, best)
		i = j
	}
	return out
}

// Render returns text with the spans, merged, replaced by their markers.
// It's for people reading a redaction, such as the teen's preview and
// `pairtime fixtures scrub`; a reveal sends the spans themselves. Spans
// that run past the end of text are cut at the end.
func Render(text string, spans []Span) string {
	var b strings.Builder
	at := 0
	for _, s := range Merge(spans) {
		if s.Start >= len(text) {
			break
		}
		s.End = min(s.End, len(text))
		b.WriteString(text[at:s.Start])
		b.WriteString(s.Marker())
		at = s.End
	}
	b.WriteString(text[at:])
	return b.String()
}

// Tokens rounds each span out to the whole tokens it touches, which is
// what a reveal can remove: a token is a maximal run of whitespace, as
// unicode.IsSpace has it, or of other characters (docs/spec/ledger-v1.md
// 10.2). So an email address inside "email:teen@example.com," takes the
// whole token with it. Spans that overlap once rounded are merged as Merge
// does; spans outside text are dropped.
func Tokens(text string, spans []Span) []Span {
	out := make([]Span, 0, len(spans))
	for _, s := range spans {
		s.End = min(s.End, len(text))
		if s.Start < 0 || s.Start >= s.End {
			continue
		}
		s.Start = tokenStart(text, s.Start)
		s.End = tokenEnd(text, s.End)
		out = append(out, s)
	}
	return Merge(out)
}

// tokenStart returns where the token holding byte i starts.
func tokenStart(text string, i int) int {
	for i > 0 && !utf8.RuneStart(text[i]) {
		i--
	}
	r, _ := utf8.DecodeRuneInString(text[i:])
	space := unicode.IsSpace(r)
	for i > 0 {
		p, size := utf8.DecodeLastRuneInString(text[:i])
		if unicode.IsSpace(p) != space {
			break
		}
		i -= size
	}
	return i
}

// tokenEnd returns where the token holding byte e-1 ends.
func tokenEnd(text string, e int) int {
	last, _ := utf8.DecodeLastRuneInString(text[:e])
	space := unicode.IsSpace(last)
	for e < len(text) {
		r, size := utf8.DecodeRuneInString(text[e:])
		if unicode.IsSpace(r) != space {
			break
		}
		e += size
	}
	return e
}

// sortSpans sorts spans by start, longest first, and drops exact repeats.
func sortSpans(spans []Span) []Span {
	slices.SortFunc(spans, func(a, b Span) int {
		return cmp.Or(cmp.Compare(a.Start, b.Start), cmp.Compare(b.End, a.End),
			cmp.Compare(a.Type.rank(), b.Type.rank()), strings.Compare(a.Rule, b.Rule))
	})
	return slices.Compact(spans)
}
