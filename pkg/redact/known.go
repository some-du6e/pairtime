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

// knownValue is v as Find looks for it: without surrounding space, and a
// home without a trailing separator.
func knownValue(t Type, v string) string {
	v = strings.TrimSpace(v)
	if t == TypeHome && !isDriveRoot(v) {
		v = strings.TrimRight(v, `/\`)
	}
	return v
}

func compileKnown(k Known) []knownPattern {
	var out []knownPattern
	seen := make(map[string]bool)
	for _, tv := range k.typed() {
		v := knownValue(tv.t, tv.v)
		if !keep(tv.t, v) {
			continue
		}
		for _, sp := range spellings(tv.t, v) {
			s := sp.s
			key := strings.ToLower(s)
			// A value that isn't UTF-8, as a git config saved in another
			// encoding gives, can't be in text, which is, and regexp
			// refuses it.
			if seen[key] || !utf8.ValidString(s) {
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

// spelling is one way a file can write a known value: the value written
// that way, and how to write another value the same way, so that a
// placeholder can take its place in the same form.
type spelling struct {
	s  string
	as func(string) string
}

// spellings returns v as it can appear in a prompt, an agent message or a
// raw capture file, as is first:
//
//   - for a Windows home, also with backslashes and with forward slashes,
//     as Git Bash and WSL write it (/c/Users/..., /mnt/c/Users/...), and
//     as VS Code writes it in a file URI, with the drive in lower case and
//     its colon escaped (/c%3A/Users/...);
//   - each of those URL-escaped, as Go writes a path (%20 for a space) and
//     as VS Code does (C%3A too), and as Claude Code names a project
//     folder after its working directory, with every character but an
//     ASCII letter or digit turned into a dash (C--Users-Maya-Chen; HS 3.2);
//   - the value in a query string, with + for a space;
//   - and each of those escaped inside a JSON string: with "/" escaped or
//     not, with non-ASCII as \u escapes or not, and escaped twice, as in
//     JSON that was itself put in a JSON string.
func spellings(t Type, v string) []spelling {
	var out []spelling
	add := func(as func(string) string) {
		s := as(v)
		if s == "" || slices.ContainsFunc(out, func(sp spelling) bool { return sp.s == s }) {
			return
		}
		out = append(out, spelling{s, as})
	}
	same := func(s string) string { return s }
	bases := []func(string) string{same}
	drive := t == TypeHome && windowsDrive(v)
	if drive {
		bases = append(bases, backSlashes, forwardSlashes, gitBashPath, wslPath)
	}
	var forms []func(string) string
	for _, b := range bases {
		forms = append(forms, b, then(b, goEscapedPath), then(b, uriEscape), then(b, dashed))
	}
	forms = append(forms, url.QueryEscape)
	if drive {
		forms = append(forms, vscodePath)
	}
	jsons := []func(string) string{
		same,
		func(s string) string { return jsonEscape(s, false) },
		func(s string) string { return jsonEscape(s, true) },
		func(s string) string { return strings.ReplaceAll(jsonEscape(s, false), "/", `\/`) },
		func(s string) string { return jsonEscape(jsonEscape(s, false), false) },
	}
	for _, f := range forms {
		for _, j := range jsons {
			add(then(f, j))
		}
	}
	return out
}

// Respell writes replacement the way matched writes value, a known value of
// type t, when matched is one of the spellings Find looks for: for the home
// C:\Users\Maya Chen and the replacement C:\Users\capture, it turns
// C:\\Users\\Maya Chen into C:\\Users\\capture and C--Users-Maya-Chen into
// C--Users-capture. Case doesn't matter, as it doesn't to Find. It reports
// false when matched isn't such a spelling, such as when a merged span
// holds more than one value. `pairtime fixtures scrub` uses it to put a
// fixture placeholder in place of each value, in the same form.
func Respell(t Type, value, matched, replacement string) (string, bool) {
	v := knownValue(t, value)
	if !keep(t, v) {
		return "", false
	}
	for _, sp := range spellings(t, v) {
		if strings.EqualFold(sp.s, matched) {
			if r := sp.as(replacement); r != "" {
				return r, true
			}
			return replacement, true
		}
	}
	return "", false
}

// then returns f followed by g.
func then(f, g func(string) string) func(string) string {
	return func(s string) string { return g(f(s)) }
}

func backSlashes(s string) string    { return strings.ReplaceAll(s, "/", `\`) }
func forwardSlashes(s string) string { return strings.ReplaceAll(s, `\`, "/") }

// gitBashPath writes a Windows path as Git Bash does: C:\Users becomes
// /c/Users.
func gitBashPath(s string) string {
	if !windowsDrive(s) {
		return ""
	}
	return "/" + strings.ToLower(s[:1]) + forwardSlashes(s[2:])
}

// wslPath writes a Windows path as WSL does: C:\Users becomes /mnt/c/Users.
func wslPath(s string) string {
	if !windowsDrive(s) {
		return ""
	}
	return "/mnt/" + strings.ToLower(s[:1]) + forwardSlashes(s[2:])
}

// vscodePath writes a Windows path as the path of a VS Code file URI:
// C:\Users\Maya Chen becomes /c%3A/Users/Maya%20Chen.
func vscodePath(s string) string {
	if !windowsDrive(s) {
		return ""
	}
	return "/" + strings.ToLower(s[:1]) + "%3A" + uriEscape(forwardSlashes(s[2:]))
}

// goEscapedPath escapes s as Go's net/url writes a path, which keeps the
// colon of C:/Users and turns a space into %20.
func goEscapedPath(s string) string { return (&url.URL{Path: s}).EscapedPath() }

// uriEscape escapes s as VS Code writes a path in a URI: every byte but an
// ASCII letter or digit, -, ., _, ~ and / becomes %XX.
func uriEscape(s string) string {
	const hexDigits = "0123456789ABCDEF"
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if isASCIIAlnum(c) || strings.IndexByte("-._~/", c) >= 0 {
			b.WriteByte(c)
			continue
		}
		b.WriteByte('%')
		b.WriteByte(hexDigits[c>>4])
		b.WriteByte(hexDigits[c&0xf])
	}
	return b.String()
}

// dashed writes s as Claude Code names a project folder after its working
// directory: every character but an ASCII letter or digit becomes a dash,
// and one outside the BMP two, since JavaScript counts it as two. A value
// with fewer than three letters and digits has no such spelling, because
// it would be mostly dashes.
func dashed(s string) string {
	var b strings.Builder
	n := 0
	for _, r := range s {
		switch {
		case r < utf8.RuneSelf && isASCIIAlnum(byte(r)):
			b.WriteRune(r)
			n++
		case r > 0xffff:
			b.WriteString("--")
		default:
			b.WriteByte('-')
		}
	}
	if n < 3 {
		return ""
	}
	return b.String()
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

// edgesOK checks the word edges a match needs. Right after a JSON escape
// such as \n or \t is an edge, though the byte before it is a letter: in a
// raw capture, "USER\nmaya" holds the account at the start of a line.
func (p knownPattern) edgesOK(text string, s, e int) bool {
	if p.left && s > 0 && !afterEscape(text, s) {
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
