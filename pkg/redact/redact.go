// SPDX-License-Identifier: AGPL-3.0-or-later

package redact

import (
	"strings"
)

// Redactor finds what to remove from one teen's text. It's safe for
// concurrent use.
type Redactor struct {
	rules *ruleset
	known []knownPattern
}

// New returns a Redactor for the teen's known values. The first call loads
// gitleaks' rules from the binary, and fails only if they're corrupt.
func New(k Known) (*Redactor, error) {
	rs, err := loadRules()
	if err != nil {
		return nil, err
	}
	return &Redactor{rules: rs, known: compileKnown(k)}, nil
}

// Find returns the spans of text to remove, in pipeline order (DESIGN 7.5
// step 6 and 8.7): the teen's known values, then whatever one of gitleaks'
// rules finds, then personal data that passes its checksum. Each step looks
// at the original text, so the order only decides which type a merged span
// gets on a tie. The spans are sorted by start and can overlap; Merge
// makes them disjoint. Find doesn't collapse code blocks, which is a
// separate choice for agent messages (CodeBlocks).
//
// In text with backslash escapes, such as a raw JSON capture, gitleaks'
// rules and the PII patterns also read its JSON view (jsonView), and a
// known value may start right after an escape such as \n. So a key quoted
// as \"...\", a name or a card number at the start of an embedded line,
// and an email address after one are found. A span never ends halfway
// through an escape, so removing it from JSON text leaves JSON.
func (r *Redactor) Find(text string) []Span {
	var spans []Span
	for _, p := range r.known {
		p.find(text, func(s, e int) {
			spans = append(spans, Span{Start: s, End: e, Type: p.t})
		})
	}
	views := []string{text}
	if view, ok := jsonView(text); ok {
		views = append(views, view)
	}
	for _, v := range views {
		r.secrets(v, func(s, e int, id string) {
			spans = append(spans, Span{Start: s, End: e, Type: TypeSecret, Rule: id})
		})
	}
	for _, v := range views {
		findPII(v, func(s, e int, t Type) {
			spans = append(spans, Span{Start: s, End: e, Type: t})
		})
	}
	keepEscapesWhole(text, spans)
	return sortSpans(spans)
}

// secrets runs every gitleaks rule, then pairtime's own (ownRules), over
// text, and again over its prose view when that differs.
func (r *Redactor) secrets(text string, emit func(start, end int, rule string)) {
	run := func(s string) {
		lower := strings.ToLower(s)
		for _, ru := range r.rules.rules {
			ru.find(s, lower, r.rules.global, func(_, remove [2]int) {
				if remove[0] < remove[1] {
					emit(remove[0], remove[1], ru.id)
				}
			})
		}
		ownSecrets(s, emit)
	}
	run(text)
	if view, ok := proseView(text); ok {
		run(view)
	}
}
