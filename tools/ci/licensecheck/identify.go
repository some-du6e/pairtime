// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"embed"
	"errors"
	"fmt"
	"regexp"
	"regexp/syntax"
	"strings"
)

// Allowed lists the licenses a dependency may carry: MIT, BSD-2-Clause,
// BSD-3-Clause, Apache-2.0 and ISC (DESIGN 3.12; docs/PLAN.md, ground rule
// 4). Each has a template in templates/.
var Allowed = []string{"MIT", "BSD-2-Clause", "BSD-3-Clause", "Apache-2.0", "ISC"}

// The templates follow the SPDX License List's matching templates: the
// license text, with <<var:REGEX>> where real copies differ (copyright
// lines, the holder's name, list bullets) and <<begin>>...<<end>> around
// text a copy may leave out (titles, Apache's appendix). Literal text is
// matched without regard to case, spacing, quote style or the spelling
// "licence"; a REGEX is matched against the text after that normalization.
//
//go:embed templates/*.txt
var templateFS embed.FS

// otherLicenses are phrases that name a license outside Allowed. A file has
// to match a template as a whole, so extra clauses fail. But a template's
// variable parts accept any text, so these phrases are looked for anywhere,
// and a reviewer still reads a new dependency's license (AGENTS.md).
var otherLicenses = []string{
	"general public license", // GPL, LGPL and AGPL
	"affero",
	"mozilla public license",
	"eclipse public license",
	"european union public license",
	"common development and distribution license",
	"commons clause",
	"server side public license",
	"business source license",
	"elastic license",
	"polyform",
	"creative commons",
	"non-commercial",
	"noncommercial",
	"good, not evil", // the JSON license
}

type template struct {
	id string
	re *regexp.Regexp
}

var templates = mustLoadTemplates()

func mustLoadTemplates() []template {
	var ts []template
	for _, id := range Allowed {
		data, err := templateFS.ReadFile("templates/" + id + ".txt")
		if err != nil {
			panic(err)
		}
		re, err := compileTemplate(string(data))
		if err != nil {
			panic(fmt.Sprintf("templates/%s.txt: %v", id, err))
		}
		ts = append(ts, template{id: id, re: re})
	}
	return ts
}

// identify names the allowed license a license file holds. When the file
// holds none of them, id is empty and reason says why.
func identify(text string) (id, reason string) {
	n := normalize(text)
	lower := strings.ToLower(n)
	for _, phrase := range otherLicenses {
		if strings.Contains(lower, phrase) {
			return "", fmt.Sprintf("it mentions %q, so it isn't only %s", phrase, orList(Allowed))
		}
	}
	var ids []string
	for _, t := range templates {
		if t.re.MatchString(n) {
			ids = append(ids, t.id)
		}
	}
	switch len(ids) {
	case 1:
		return ids[0], ""
	case 0:
		return "", "its text isn't " + orList(Allowed)
	default:
		return "", "its text matches more than one license: " + strings.Join(ids, ", ")
	}
}

func orList(ids []string) string {
	if len(ids) < 2 {
		return strings.Join(ids, "")
	}
	return strings.Join(ids[:len(ids)-1], ", ") + " or " + ids[len(ids)-1]
}

var (
	// A bare URL, optionally in angle brackets. Copies differ in http and
	// https, and some add the license's URL at the end, so URLs are dropped.
	urlRE     = regexp.MustCompile(`<?https?://[^\s<>]*>?`)
	licenceRE = regexp.MustCompile(`(?i)licen[cs]e`)
	spaceRE   = regexp.MustCompile(`[\s\p{Z}]+`)
	charFixer = strings.NewReplacer(
		"\ufeff", "",
		"\u201c", "'", "\u201d", "'", "\u201e", "'", "\u201f", "'", "\u2033", "'",
		"\u2018", "'", "\u2019", "'", "\u201a", "'", "\u201b", "'", "\u2032", "'",
		`"`, "'", "`", "'",
		"\u2010", "-", "\u2011", "-", "\u2012", "-", "\u2013", "-", "\u2014", "-", "\u2015", "-", "\u2212", "-",
		"\u00a9", "(c)",
	)
	tokenRE = regexp.MustCompile(`[\p{L}\p{N}_]+|[^\p{L}\p{N}_\s\p{Z}]`)
)

// normalize puts a license text in the form templates are matched against:
// one quote character, one dash, (c) for ©, "license" for "licence", no
// URLs, and single spaces.
func normalize(s string) string {
	s = urlRE.ReplaceAllString(s, " ")
	s = charFixer.Replace(s)
	s = strings.ReplaceAll(s, "''", "'")
	s = licenceRE.ReplaceAllString(s, "license")
	return strings.TrimSpace(spaceRE.ReplaceAllString(s, " "))
}

// literal turns template text into regular-expression tokens: each word and
// each punctuation mark, to be joined by optional space.
func literal(s string) []string {
	var parts []string
	for _, tok := range tokenRE.FindAllString(normalize(s), -1) {
		parts = append(parts, regexp.QuoteMeta(tok))
	}
	return parts
}

var markerRE = regexp.MustCompile(`(?s)<<(begin|end|var:.*?)>>`)

// compileTemplate turns a template into one anchored, case-insensitive
// regular expression.
func compileTemplate(t string) (*regexp.Regexp, error) {
	stack := [][]string{nil}
	pos := 0
	for _, m := range markerRE.FindAllStringSubmatchIndex(t, -1) {
		top := len(stack) - 1
		stack[top] = append(stack[top], literal(t[pos:m[0]])...)
		pos = m[1]
		switch tag := t[m[2]:m[3]]; tag {
		case "begin":
			stack = append(stack, nil)
		case "end":
			if top == 0 {
				return nil, errors.New("<<end>> without <<begin>>")
			}
			inner := stack[top]
			stack = stack[:top]
			stack[top-1] = append(stack[top-1], `(?:`+strings.Join(inner, `\s*`)+`)?`)
		default:
			expr := strings.TrimPrefix(tag, "var:")
			if _, err := syntax.Parse(expr, syntax.Perl); err != nil {
				return nil, fmt.Errorf("<<var:%s>>: %v", expr, err)
			}
			stack[top] = append(stack[top], `(?:`+expr+`)`)
		}
	}
	if len(stack) != 1 {
		return nil, errors.New("<<begin>> without <<end>>")
	}
	stack[0] = append(stack[0], literal(t[pos:])...)
	return regexp.Compile(`(?i)^\s*` + strings.Join(stack[0], `\s*`) + `\s*$`)
}
