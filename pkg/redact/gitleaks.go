// SPDX-License-Identifier: AGPL-3.0-or-later

package redact

import (
	"bytes"
	"compress/gzip"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"regexp"
	"strings"
	"sync"
)

// rules/gitleaks.json.gz is gitleaks' default config at a pinned tag,
// converted to JSON by rules/convert.py. rules/README.md has the details.
//
//go:embed rules/gitleaks.json.gz
var gitleaksJSON []byte

// The shape rules/convert.py writes.
type ruleFile struct {
	Source struct {
		Repo   string `json:"repo"`
		Tag    string `json:"tag"`
		Commit string `json:"commit"`
		Path   string `json:"path"`
		SHA256 string `json:"sha256"`
	} `json:"source"`
	Allowlists []allowlistJSON `json:"allowlists"`
	Rules      []ruleJSON      `json:"rules"`
}

type allowlistJSON struct {
	Condition   string   `json:"condition"`
	RegexTarget string   `json:"regex_target"`
	Regexes     []string `json:"regexes"`
	StopWords   []string `json:"stopwords"`
	Paths       []string `json:"paths"`
	Commits     []string `json:"commits"`
}

type ruleJSON struct {
	ID          string          `json:"id"`
	Description string          `json:"description"`
	Regex       string          `json:"regex"`
	Path        string          `json:"path"`
	SecretGroup int             `json:"secret_group"`
	Entropy     float64         `json:"entropy"`
	Keywords    []string        `json:"keywords"`
	Allowlists  []allowlistJSON `json:"allowlists"`
}

// ruleset is the loaded config.
type ruleset struct {
	tag    string
	global []*allowlist
	rules  []*rule // the rules with a regex, in the config's order
	byID   map[string]*rule
}

// allowlist is a gitleaks allowlist (config/allowlist.go).
type allowlist struct {
	and       bool
	target    string         // "", "match" or "line"
	re        *regexp.Regexp // the regexes joined, as gitleaks' joinRegexOr does; nil when none
	stopwords []string       // lowercased
	paths     bool           // whether it has path checks; text has no path, so they never pass
	commits   bool           // the same for commits
}

// rule is one gitleaks rule. Its regex compiles on first use, so a process
// that never sees a keyword never pays for the big patterns.
type rule struct {
	id          string
	description string
	pattern     string
	secretGroup int
	entropy     float64
	keywords    []string // lowercased
	allowlists  []*allowlist

	once sync.Once
	re   *regexp.Regexp
}

var loadRules = sync.OnceValues(func() (*ruleset, error) { return parseRules(gitleaksJSON) })

func parseRules(gz []byte) (*ruleset, error) {
	zr, err := gzip.NewReader(bytes.NewReader(gz))
	if err != nil {
		return nil, fmt.Errorf("redact: gitleaks rules: %w", err)
	}
	raw, err := io.ReadAll(zr)
	if err != nil {
		return nil, fmt.Errorf("redact: gitleaks rules: %w", err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var f ruleFile
	if err := dec.Decode(&f); err != nil {
		return nil, fmt.Errorf("redact: gitleaks rules: %w", err)
	}
	rs := &ruleset{tag: f.Source.Tag, byID: make(map[string]*rule)}
	for i, a := range f.Allowlists {
		al, err := newAllowlist(a)
		if err != nil {
			return nil, fmt.Errorf("redact: gitleaks global allowlist %d: %w", i, err)
		}
		rs.global = append(rs.global, al)
	}
	for _, r := range f.Rules {
		if r.Regex == "" {
			// A path-only rule, such as pkcs12-file, flags a file by its
			// name. Text has no name.
			continue
		}
		ru := &rule{
			id:          r.ID,
			description: r.Description,
			pattern:     r.Regex,
			secretGroup: r.SecretGroup,
			entropy:     r.Entropy,
		}
		// gitleaks only applies a rule with a path to files that match
		// it. A prompt has no path, and a pasted .tf or .yaml file is
		// still a secret, so here such rules apply to all text.
		for _, k := range r.Keywords {
			ru.keywords = append(ru.keywords, strings.ToLower(k))
		}
		for i, a := range r.Allowlists {
			al, err := newAllowlist(a)
			if err != nil {
				return nil, fmt.Errorf("redact: gitleaks rule %s allowlist %d: %w", r.ID, i, err)
			}
			ru.allowlists = append(ru.allowlists, al)
		}
		if _, dup := rs.byID[r.ID]; dup || r.ID == "" {
			return nil, fmt.Errorf("redact: gitleaks rule %q: missing or repeated id", r.ID)
		}
		rs.byID[r.ID] = ru
		rs.rules = append(rs.rules, ru)
	}
	return rs, nil
}

func newAllowlist(a allowlistJSON) (*allowlist, error) {
	al := &allowlist{target: a.RegexTarget, paths: len(a.Paths) > 0, commits: len(a.Commits) > 0}
	switch a.Condition {
	case "OR":
	case "AND":
		al.and = true
	default:
		return nil, fmt.Errorf("unknown condition %q", a.Condition)
	}
	switch a.RegexTarget {
	case "", "match", "line":
	default:
		return nil, fmt.Errorf("unknown regex target %q", a.RegexTarget)
	}
	if len(a.Regexes) > 0 {
		re, err := regexp.Compile("(?:" + strings.Join(a.Regexes, "|") + ")")
		if err != nil {
			return nil, err
		}
		al.re = re
	}
	for _, w := range a.StopWords {
		al.stopwords = append(al.stopwords, strings.ToLower(w))
	}
	return al, nil
}

// compiled compiles the rule's pattern. Every shipped pattern compiles, which
// TestGitleaksRulesCompile checks, so a failure here is a broken build.
func (r *rule) compiled() *regexp.Regexp {
	r.once.Do(func() { r.re = regexp.MustCompile(r.pattern) })
	return r.re
}

// wanted is gitleaks' keyword prefilter: a rule with keywords runs only on
// text that contains one, ignoring case.
func (r *rule) wanted(lower string) bool {
	if len(r.keywords) == 0 {
		return true
	}
	for _, k := range r.keywords {
		if strings.Contains(lower, k) {
			return true
		}
	}
	return false
}

// find calls emit for every finding of the rule in text, with the span of
// the secret, which is what gitleaks reports, and the span to remove. It
// follows gitleaks' detectRule (detect/detect.go at the pinned tag) for a
// fragment with no path, no commit and no decoding, except that a
// "gitleaks:allow" comment doesn't hide a secret.
//
// The span to remove is the secret, unless the secret is a piece of a
// longer token, which happens when a rule's first group is structure rather
// than the secret: jwt-base64's groups name a JWT header field, and
// microsoft-teams-webhook's is one block of a UUID in the URL. A letter or
// digit of the match touching the secret gives that away, and then the
// whole match goes, or most of the token would stay readable. So does the
// match when the secret is empty.
func (r *rule) find(text, lower string, global []*allowlist, emit func(secret, remove [2]int)) {
	if !r.wanted(lower) {
		return
	}
	re := r.compiled()
	for _, m := range re.FindAllStringIndex(text, -1) {
		raw := text[m[0]:m[1]]
		match := strings.Trim(raw, "\n")
		off := m[0] + len(raw) - len(strings.TrimLeft(raw, "\n"))

		// gitleaks takes the secret from a second match against the
		// trimmed match: the rule's secretGroup, or else its first
		// non-empty group, or else the whole match.
		start, end := off, off+len(match)
		if g := re.FindStringSubmatchIndex(match); len(g) >= 4 {
			if r.secretGroup > 0 {
				if len(g)/2 <= r.secretGroup {
					continue
				}
				if gs := g[2*r.secretGroup]; gs >= 0 {
					start, end = off+gs, off+g[2*r.secretGroup+1]
				} else {
					start, end = off, off
				}
			} else {
				for i := 2; i < len(g); i += 2 {
					if g[i] >= 0 && g[i+1] > g[i] {
						start, end = off+g[i], off+g[i+1]
						break
					}
				}
			}
		}
		secret := text[start:end]
		if r.entropy != 0 && shannonEntropy(secret) <= r.entropy {
			continue
		}
		// gitleaks computes the line from the untrimmed match start and the
		// trimmed length.
		line := gitleaksLine(text, m[0], m[0]+len(match))
		if allowed(global, secret, match, line) || allowed(r.allowlists, secret, match, line) {
			continue
		}
		remove := [2]int{start, end}
		if start == end || start > off && isASCIIAlnum(text[start-1]) || end < off+len(match) && isASCIIAlnum(text[end]) {
			remove = [2]int{off, off + len(match)}
		}
		emit([2]int{start, end}, remove)
	}
}

func isASCIIAlnum(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}

// allowed is gitleaks' checkFindingAllowed for a finding with no path and
// no commit.
func allowed(lists []*allowlist, secret, match, line string) bool {
	for _, a := range lists {
		target := secret
		switch a.target {
		case "match":
			target = match
		case "line":
			target = line
		}
		regexOK := a.re != nil && target != "" && a.re.MatchString(target)
		stopOK := secret != "" && a.hasStopword(secret)
		ok := regexOK || stopOK
		if a.and {
			// Every check the allowlist has must pass. Path and commit
			// checks can't, because text has neither.
			ok = !a.paths && !a.commits &&
				(a.re == nil || regexOK) &&
				(len(a.stopwords) == 0 || stopOK)
		}
		if ok {
			return true
		}
	}
	return false
}

func (a *allowlist) hasStopword(secret string) bool {
	s := strings.ToLower(secret)
	for _, w := range a.stopwords {
		if strings.Contains(s, w) {
			return true
		}
	}
	return false
}

// shannonEntropy is gitleaks' (detect/utils.go): it counts runes but
// divides by the length in bytes.
func shannonEntropy(data string) float64 {
	if data == "" {
		return 0
	}
	counts := make(map[rune]int)
	for _, c := range data {
		counts[c]++
	}
	inv := 1 / float64(len(data))
	var e float64
	for _, n := range counts {
		f := float64(n) * inv
		e -= f * math.Log2(f)
	}
	return e
}

// gitleaksLine returns what gitleaks calls a finding's line
// (detect/location.go): from the line break before start, which it
// includes, to the line break after end.
func gitleaksLine(raw string, start, end int) string {
	var newlines []int
	for i := 0; i < len(raw); i++ {
		if raw[i] == '\n' {
			newlines = append(newlines, i)
		}
	}
	if len(newlines) == 0 {
		newlines = []int{len(raw)}
	}
	prev, from, to, set := 0, 0, 0, false
	for _, nl := range newlines {
		if prev <= start && start < nl {
			set = true
			from, to = prev, nl
		}
		if prev < end && end <= nl {
			to = nl
		}
		prev = nl
	}
	if !set {
		i := 0
		for end+i < len(raw) && raw[end+i] != '\n' && raw[end+i] != '\r' {
			i++
		}
		to = end + i
	}
	to = max(to, end)
	if from > to || to > len(raw) {
		return ""
	}
	return raw[from:to]
}

// proseView returns text with sentence punctuation that directly follows a
// token turned into spaces, and whether it changed anything. gitleaks'
// token rules end in (?:[\x60'"\s;]|\\[nr]|$), so a key at the end of a
// sentence, "my key is sk-...AA.", doesn't match. Running the rules over
// this view as well catches it. Every byte keeps its offset.
//
// Punctuation counts when whitespace, a quote or a backslash follows it, or
// nothing does. The last two are how a sentence ends inside a raw JSON
// string: "...AA.\"" or "...AA.\nthanks".
func proseView(text string) (string, bool) {
	var b []byte
	for i := len(text) - 1; i >= 0; i-- {
		if !strings.ContainsRune(".,!?:)]}>*", rune(text[i])) {
			continue
		}
		next := byte(' ')
		if i+1 < len(text) {
			next = text[i+1]
			if b != nil {
				next = b[i+1]
			}
		}
		if next != ' ' && next != '\t' && next != '\n' && next != '\r' && next != '"' && next != '\\' {
			continue
		}
		if b == nil {
			b = []byte(text)
		}
		b[i] = ' '
	}
	if b == nil {
		return text, false
	}
	return string(b), true
}
