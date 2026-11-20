// SPDX-License-Identifier: AGPL-3.0-or-later

package redact

import (
	"net/url"
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Known holds the exact strings that identify the teen on this device
// (DESIGN 7.5 step 6; TP B4 step 1). Regex can't find a friend's name or a
// school; the teen's own deny list can.
type Known struct {
	Accounts  []string // OS account names
	Homes     []string // home directories
	Hosts     []string // hostnames
	GitNames  []string // git config user.name values
	GitEmails []string // git config user.email values
	GitHub    []string // GitHub usernames
	HCA       []string // Hack Club Account usernames
	Private   []string // names of private repos and folders
	Deny      []string // the teen's deny list: friends' names, a school, anything
}

func (k Known) typed() []typedValue {
	var out []typedValue
	add := func(t Type, vs []string) {
		for _, v := range vs {
			out = append(out, typedValue{t, v})
		}
	}
	add(TypeAccount, k.Accounts)
	add(TypeHome, k.Homes)
	add(TypeHost, k.Hosts)
	add(TypeGitName, k.GitNames)
	add(TypeGitEmail, k.GitEmails)
	add(TypeGitHub, k.GitHub)
	add(TypeHCA, k.HCA)
	add(TypePrivate, k.Private)
	add(TypeDeny, k.Deny)
	return out
}

type typedValue struct {
	t Type
	v string
}

// genericNames are account, host and folder names that identify nobody and
// are common words in prompts, so redacting them would only wreck the text.
// They're skipped for every type but the deny list, which is the teen's
// own choice.
var genericNames = map[string]bool{
	"admin": true, "administrator": true, "api": true, "app": true, "code": true,
	"codespace": true, "codespaces": true, "computer": true, "default": true,
	"desktop": true, "dev": true, "developer": true, "git": true, "github": true,
	"guest": true, "home": true, "imac": true, "laptop": true, "linux": true,
	"local": true, "localhost": true, "mac": true, "macbook": true, "main": true,
	"master": true, "node": true, "owner": true, "project": true, "projects": true,
	"repo": true, "root": true, "runner": true, "school": true, "src": true,
	"student": true, "test": true, "ubuntu": true, "user": true, "users": true,
	"web": true, "windows": true, "work": true,
}

// keep says whether a known value is worth looking for.
func keep(t Type, v string) bool {
	n := utf8.RuneCountInString(v)
	switch t {
	case TypeDeny:
		return n >= 1
	case TypeHome:
		return n >= 3 && strings.Trim(v, `/\:.`) != "" && !isDriveRoot(v)
	case TypeGitEmail:
		return n >= 3
	default:
		return n >= 3 && !genericNames[strings.ToLower(v)]
	}
}

func isDriveRoot(v string) bool {
	return len(v) <= 3 && windowsDrive(v)
}

// windowsDrive reports whether v starts with a drive letter, as in C:\.
func windowsDrive(v string) bool {
	return len(v) >= 2 && v[1] == ':' && (v[0] >= 'A' && v[0] <= 'Z' || v[0] >= 'a' && v[0] <= 'z')
}

// knownPattern finds one spelling of one known value.
type knownPattern struct {
	t  Type
	re *regexp.Regexp
	// A value that starts or ends with a letter or digit only matches
	// where the text doesn't continue it: "maya" is found in "maya_dev"
	// and "/home/maya/", not in "mayan".
	left, right bool
}

func compileKnown(k Known) []knownPattern {
	var out []knownPattern
	seen := make(map[string]bool)
	for _, tv := range k.typed() {
		v := strings.TrimSpace(tv.v)
		if tv.t == TypeHome && !isDriveRoot(v) {
			v = strings.TrimRight(v, `/\`)
		}
		if !keep(tv.t, v) {
			continue
		}
		for _, s := range spellings(tv.t, v) {
			key := strings.ToLower(s)
			if seen[key] {
				continue
			}
			seen[key] = true
			first, _ := utf8.DecodeRuneInString(s)
			last, _ := utf8.DecodeLastRuneInString(s)
			out = append(out, knownPattern{
				t:     tv.t,
				re:    regexp.MustCompile("(?i)" + regexp.QuoteMeta(s)),
				left:  isAlnum(first),
				right: isAlnum(last),
			})
		}
	}
	return out
}

// spellings returns v as it can appear in a prompt, an agent message or a
// raw capture file: as is, escaped inside a JSON string (with "/" escaped
// or not, and with non-ASCII as \u escapes or not), and for a Windows home
// also with forward slashes and as Git Bash and WSL write it.
func spellings(t Type, v string) []string {
	out := []string{v}
	add := func(s string) {
		if s != "" && !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	if t == TypeHome {
		if windowsDrive(v) {
			fwd := strings.ReplaceAll(v, `\`, "/")
			drive := strings.ToLower(v[:1])
			add(fwd)
			add("/" + drive + fwd[2:])
			add("/mnt/" + drive + fwd[2:])
		}
		for _, s := range slices.Clone(out) {
			if strings.HasPrefix(s, "/") || windowsDrive(s) {
				add((&url.URL{Path: s}).EscapedPath())
			}
		}
	}
	for _, s := range slices.Clone(out) {
		add(jsonEscape(s, false))
		add(jsonEscape(s, true))
		add(strings.ReplaceAll(jsonEscape(s, false), "/", `\/`))
	}
	return out
}

// jsonEscape returns s as it appears between the quotes of a JSON string.
// With ascii set, characters outside ASCII become \u escapes, as Python's
// json module writes them by default.
func jsonEscape(s string, ascii bool) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == '"' || r == '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		case r < 0x20:
			b.WriteString(`\u00`)
			b.WriteByte("0123456789abcdef"[r>>4])
			b.WriteByte("0123456789abcdef"[r&0xf])
		case ascii && r > 0x7e:
			for _, u := range utf16Units(r) {
				b.WriteString(`\u`)
				for shift := 12; shift >= 0; shift -= 4 {
					b.WriteByte("0123456789abcdef"[(u>>shift)&0xf])
				}
			}
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func utf16Units(r rune) []rune {
	if r < 0x10000 {
		return []rune{r}
	}
	r -= 0x10000
	return []rune{0xd800 + (r>>10)&0x3ff, 0xdc00 + r&0x3ff}
}

func isAlnum(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsMark(r)
}

// find calls emit for every match whose edges pass.
func (p knownPattern) find(text string, emit func(start, end int)) {
	for at := 0; at < len(text); {
		loc := p.re.FindStringIndex(text[at:])
		if loc == nil {
			return
		}
		s, e := at+loc[0], at+loc[1]
		if e > s && p.edgesOK(text, s, e) {
			emit(s, e)
			at = e
			continue
		}
		// Try again one character on: a later start can still match.
		_, size := utf8.DecodeRuneInString(text[s:])
		at = s + max(size, 1)
	}
}

func (p knownPattern) edgesOK(text string, s, e int) bool {
	if p.left && s > 0 {
		if r, _ := utf8.DecodeLastRuneInString(text[:s]); isAlnum(r) {
			return false
		}
	}
	if p.right && e < len(text) {
		if r, _ := utf8.DecodeRuneInString(text[e:]); isAlnum(r) {
			return false
		}
	}
	return true
}
