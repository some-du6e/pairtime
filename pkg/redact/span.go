// SPDX-License-Identifier: AGPL-3.0-or-later

package redact

import (
	"cmp"
	"slices"
	"strconv"
	"strings"
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

// sortSpans sorts spans by start, longest first, and drops exact repeats.
func sortSpans(spans []Span) []Span {
	slices.SortFunc(spans, func(a, b Span) int {
		return cmp.Or(cmp.Compare(a.Start, b.Start), cmp.Compare(b.End, a.End),
			cmp.Compare(a.Type.rank(), b.Type.rank()), strings.Compare(a.Rule, b.Rule))
	})
	return slices.Compact(spans)
}
