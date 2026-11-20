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
// license text, with <<var:REGEX>> where real copies word it differently,
// <<text:REGEX>> where they put free text (the holder's name, list
// bullets), <<head:REGEX>> for the copyright lines and title above the
// license, and <<begin>>...<<end>> around text a copy may leave out (titles,
// Apache's appendix). Literal text is matched without regard
// to case, spacing, quote style or the spelling "licence"; a REGEX is
// matched against the text after that normalization.
//
//go:embed templates/*.txt
var templateFS embed.FS

// otherLicenses are phrases that name a license outside Allowed. A file has
// to match a template as a whole, so extra clauses fail, but these phrases
// are looked for anywhere in it.
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

// conditionRE finds an added condition ("Redistribution is prohibited
// without written permission", "Commercial use is prohibited") in a
// template's free text, which should hold only copyright notices, names
// and bullets. It looks for phrases, not lone words, so holders such as
// "PayPal, Inc." or "Sally May" pass. Each line of the head must also be a
// copyright notice or a title (headLineOK). A clause that avoids these
// phrases and sits on a copyright line can still get through, so a
// reviewer reads a new dependency's license too.
var conditionRE = regexp.MustCompile(`\b(?:prohibit\w*|forbid\w*|unless|except\w*|cannot` +
	`|(?:is|are|be|being) (?:strictly )?(?:restricted|limited|not permitted|not allowed)` +
	`|(?:may|must|shall|can|will) not` +
	`|not (?:be )?(?:permitted|allowed|used|sold|resold|redistributed|distributed|modified|copied|for)` +
	`|no (?:commercial|redistribution|modification|sale|resale|use)` +
	`|without (?:the )?(?:express |prior )?(?:written )?(?:permission|consent|authori[sz]ation|license)` +
	`|(?:non-?)?commercial (?:use|purposes?|redistribution|distribution)` +
	`|for (?:non-?commercial|personal|educational|internal|evaluation|academic) (?:use|purposes?)` +
	`|(?:use|used) only|only (?:be )?(?:used|for|by|with|within|in)` +
	`|requires? (?:a |an )?(?:fee|payment|license|permission|consent|registration)` +
	`|royalt(?:y|ies)|license fee|subject to|terms (?:of|and conditions))\b`)

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
	why := "its text isn't " + orList(Allowed)
	for _, t := range templates {
		m := t.re.FindStringSubmatch(n)
		if m == nil {
			continue
		}
		if text, word := addedCondition(t.re, m); word != "" {
			why = fmt.Sprintf("it has the %s text, but the part that should be only copyright lines and names says %q, which reads as an added condition (%q)", t.id, shorten(text, 80), word)
			continue
		}
		if reason := badHead(text, t.re, m); reason != "" {
			why = fmt.Sprintf("it has the %s text, but %s", t.id, reason)
			continue
		}
		ids = append(ids, t.id)
	}
	switch len(ids) {
	case 1:
		return ids[0], ""
	case 0:
		return "", why
	default:
		return "", "its text matches more than one license: " + strings.Join(ids, ", ")
	}
}

// addedCondition returns the first <<text>> or <<head>> part of a match
// that contains a condition word, and the word.
func addedCondition(re *regexp.Regexp, m []string) (text, word string) {
	for i, name := range re.SubexpNames() {
		if !(strings.HasPrefix(name, "text") || name == "head") || m[i] == "" {
			continue
		}
		if w := conditionRE.FindString(strings.ToLower(m[i])); w != "" {
			return strings.TrimSpace(m[i]), w
		}
	}
	return "", ""
}

// badHead says what is wrong with a match's <<head>> part: a line that
// headLineOK rejects, or lines it can't tell apart. It returns "" when the
// head is fine.
func badHead(original string, re *regexp.Regexp, m []string) string {
	i := re.SubexpIndex("head")
	if i < 0 || strings.TrimSpace(m[i]) == "" {
		return ""
	}
	lines, ok := headLines(original, m[i])
	if !ok {
		// Unchecked lines could hide anything, so this fails closed.
		return fmt.Sprintf("the lines above it (%q) can't be told apart to check them", shorten(strings.TrimSpace(m[i]), 80))
	}
	for _, line := range lines {
		if !headLineOK(line) {
			return fmt.Sprintf("the line %q above it is neither a copyright notice nor a license title", shorten(line, 80))
		}
	}
	return ""
}

// headLines splits a template's matched head back into the file's lines,
// each normalized. The head is matched on the whole normalized text, which
// has no line breaks, so this walks the original lines until their
// normalized forms add up to the head. The head may start partway through a
// line, after a title the template matched itself, and the line that holds
// its end may go on into the license text; only the head's part of either
// is kept. ok is false when the lines don't add up to the head.
func headLines(original, head string) (lines []string, ok bool) {
	head = strings.TrimSpace(head)
	acc := ""
	for _, line := range strings.Split(original, "\n") {
		n := normalize(line)
		if n == "" {
			continue
		}
		if acc == head {
			break
		}
		if acc == "" {
			k := headStart(n, head)
			if k < 0 {
				continue
			}
			n = n[k:]
		}
		next := n
		if acc != "" {
			next = acc + " " + n
		}
		switch {
		case strings.HasPrefix(head, next):
			lines = append(lines, n)
			acc = next
		case strings.HasPrefix(next, head):
			lines = append(lines, strings.TrimSpace(strings.TrimPrefix(head, acc)))
			acc = head
		default:
			return nil, false
		}
	}
	return lines, acc == head
}

// headStart returns where in a normalized line the head begins, at the
// start of a word, or -1 when the line holds no start of it.
func headStart(line, head string) int {
	for k := 0; k < len(line); k++ {
		if k > 0 && line[k-1] != ' ' {
			continue
		}
		if rest := line[k:]; strings.HasPrefix(head, rest) || strings.HasPrefix(rest, head) {
			return k
		}
	}
	return -1
}

var (
	wordRE = regexp.MustCompile(`[\p{L}\p{N}]+`)
	// titleWords are the words of a license title ("The MIT License (MIT)",
	// "BSD 3-Clause License") and of "All rights reserved".
	titleWords = map[string]bool{
		"the": true, "mit": true, "isc": true, "iscl": true, "bsd": true, "expat": true, "x11": true,
		"license": true, "new": true, "simplified": true, "modified": true, "revised": true,
		"clause": true, "0": true, "2": true, "3": true, "spdx": true, "identifier": true,
		"all": true, "rights": true, "reserved": true,
	}
)

// headLineOK reports whether a line above a license's text is a copyright
// notice, a title, "All rights reserved", or a rule of punctuation.
func headLineOK(line string) bool {
	lower := strings.TrimLeft(strings.ToLower(line), "#*- ")
	for _, start := range []string{"copyright", "(c)", "portions copyright"} {
		if strings.HasPrefix(lower, start) {
			return true
		}
	}
	for _, w := range wordRE.FindAllString(lower, -1) {
		if !titleWords[w] {
			return false
		}
	}
	return true
}

// matchesAnyChar reports whether a parsed regexp has a . anywhere.
func matchesAnyChar(re *syntax.Regexp) bool {
	if re.Op == syntax.OpAnyChar || re.Op == syntax.OpAnyCharNotNL {
		return true
	}
	for _, sub := range re.Sub {
		if matchesAnyChar(sub) {
			return true
		}
	}
	return false
}

// shorten cuts s to at most n runes, marking the cut.
func shorten(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-3]) + "..."
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

var markerRE = regexp.MustCompile(`(?s)<<(begin|end|var:.*?|text:.*?|head:.*?)>>`)

// compileTemplate turns a template into one anchored, case-insensitive
// regular expression.
func compileTemplate(t string) (*regexp.Regexp, error) {
	stack := [][]string{nil}
	pos, texts, heads := 0, 0, 0
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
			kind, expr, _ := strings.Cut(tag, ":")
			parsed, err := syntax.Parse(expr, syntax.Perl)
			if err != nil {
				return nil, fmt.Errorf("<<%s>>: %v", tag, err)
			}
			if kind == "var" && matchesAnyChar(parsed) {
				return nil, fmt.Errorf("<<%s>> matches any character; free text belongs in <<text>> or <<head>>, which identify screens", tag)
			}
			// text and head parts are named, so identify can screen them.
			switch kind {
			case "text":
				texts++
				stack[top] = append(stack[top], fmt.Sprintf(`(?P<text%d>%s)`, texts, expr))
			case "head":
				if heads++; heads > 1 {
					return nil, errors.New("more than one <<head>>")
				}
				stack[top] = append(stack[top], `(?P<head>`+expr+`)`)
			default:
				stack[top] = append(stack[top], `(?:`+expr+`)`)
			}
		}
	}
	if len(stack) != 1 {
		return nil, errors.New("<<begin>> without <<end>>")
	}
	stack[0] = append(stack[0], literal(t[pos:])...)
	return regexp.Compile(`(?i)^\s*` + strings.Join(stack[0], `\s*`) + `\s*$`)
}
