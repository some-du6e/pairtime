// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/some-du6e/pairtime/pkg/redact"
)

const fixturesUsage = `Usage:
  pairtime fixtures scrub [flags] <dir>

Scrub takes out of captured harness fixtures what must not be committed
(DESIGN 5.1): this machine's account name, home directory, hostname and git
name and email, the values given with the flags below, anything one of
gitleaks' rules calls a secret, and personal data that passes Presidio's
checks, email addresses included. Every file under <dir> is rewritten in
place, with the fixed placeholders of testdata/fixtures/README.md written
the way the file writes each value: the home becomes C:\Users\capture,
/Users/capture or /home/capture, the account capture, the hostname
capture-host, the git name Capture User, git and other email addresses
capture@example.invalid, other listed values REDACTED, and a secret
REDACTED_ and its gitleaks rule, such as REDACTED_GITHUB_PAT. A tool's own
address, such as noreply@anthropic.com, and one at a domain reserved for
examples stay, though a known value or a key inside one is still replaced.
One line is printed per span, without the text it removed, for the review
by hand that has to follow. Run it with -n first to see what it would
change. PROVENANCE.md files are left alone.

It exits 1 when something needs that review before anything is committed: a
binary file it can't scrub, a symlink, a file or directory name that holds
a removed value, or JSON that its edits would break, in which case it
leaves the file as it was.

Flags (each value flag can be given more than once):
`

func init() {
	register(command{
		name:    "fixtures",
		summary: "Scrub captured harness fixtures: pairtime fixtures scrub <dir>",
		run:     runFixtures,
	})
}

// detectKnown reads this machine's known values. Tests replace it.
var detectKnown = machineKnown

func runFixtures(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 {
		switch args[0] {
		case "scrub":
			return runScrub(args[1:], stdout, stderr)
		case "help", "-h", "--help":
			fmt.Fprint(stdout, fixturesUsage)
			return 0
		}
		fmt.Fprintf(stderr, "pairtime fixtures: unknown command %q\n\n", args[0])
	}
	fmt.Fprint(stderr, fixturesUsage)
	return 2
}

type valueList []string

func (l *valueList) String() string { return strings.Join(*l, ", ") }

func (l *valueList) Set(v string) error {
	*l = append(*l, v)
	return nil
}

func runScrub(args []string, stdout, stderr io.Writer) int {
	var k redact.Known
	fl := flag.NewFlagSet("pairtime fixtures scrub", flag.ContinueOnError)
	fl.SetOutput(stderr)
	dry := fl.Bool("n", false, "print what would be removed, and change nothing")
	noDetect := fl.Bool("no-detect", false, "don't read this machine's account, home, hostname and git identity")
	fl.Var((*valueList)(&k.Accounts), "account", "an OS account `name` to remove")
	fl.Var((*valueList)(&k.Homes), "home", "a home `directory` to remove")
	fl.Var((*valueList)(&k.Hosts), "host", "a host`name` to remove")
	fl.Var((*valueList)(&k.GitNames), "git-name", "a git user.`name` to remove")
	fl.Var((*valueList)(&k.GitEmails), "git-email", "a git user.`email` to remove")
	fl.Var((*valueList)(&k.GitHub), "github", "a GitHub `username` to remove")
	fl.Var((*valueList)(&k.HCA), "hca", "a Hack Club Account `username` to remove")
	fl.Var((*valueList)(&k.Private), "private", "a private repo or folder `name` to remove")
	fl.Var((*valueList)(&k.Deny), "deny", "any other `word` or phrase to remove, such as a name")
	fl.Usage = func() {
		fmt.Fprint(stderr, fixturesUsage)
		fl.PrintDefaults()
	}
	if err := fl.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fl.NArg() != 1 {
		fmt.Fprintf(stderr, "pairtime fixtures scrub: want one directory, got %d arguments\n\n", fl.NArg())
		fl.Usage()
		return 2
	}
	root := fl.Arg(0)
	if info, err := os.Stat(root); err != nil || !info.IsDir() {
		fmt.Fprintf(stderr, "pairtime fixtures scrub: %s isn't a directory\n", root)
		return 2
	}
	if !*noDetect {
		m := detectKnown()
		k.Accounts = append(k.Accounts, m.Accounts...)
		k.Homes = append(k.Homes, m.Homes...)
		k.Hosts = append(k.Hosts, m.Hosts...)
		k.GitNames = append(k.GitNames, m.GitNames...)
		k.GitEmails = append(k.GitEmails, m.GitEmails...)
	}
	r, err := redact.New(k)
	if err != nil {
		fmt.Fprintf(stderr, "pairtime fixtures scrub: %v\n", err)
		return 1
	}
	s := &scrubber{r: r, k: k, root: root, dry: *dry, stdout: stdout}
	if err := filepath.WalkDir(root, s.visit); err != nil {
		fmt.Fprintf(stderr, "pairtime fixtures scrub: %v\n", err)
		return 1
	}
	verb := "removed"
	if s.dry {
		verb = "would remove"
	}
	fmt.Fprintf(stdout, "pairtime fixtures scrub: %s %d spans from %d of %d files\n", verb, s.spans, s.changed, s.files)
	if s.review > 0 {
		fmt.Fprintf(stdout, "pairtime fixtures scrub: %d entries need a look by hand, listed above\n", s.review)
		return 1
	}
	return 0
}

// machineKnown is what this machine says about its user. A value it can't
// read is left out.
func machineKnown() redact.Known { return machineKnownWith(user.Current) }

// machineKnownWith is machineKnown with the OS account from current, or
// with none when current is nil, as doctor --report has it on Windows.
func machineKnownWith(current func() (*user.User, error)) redact.Known {
	var k redact.Known
	if current == nil {
		current = func() (*user.User, error) { return nil, errors.ErrUnsupported }
	}
	if u, err := current(); err == nil {
		name := u.Username
		if i := strings.LastIndexByte(name, '\\'); i >= 0 {
			name = name[i+1:] // DOMAIN\name on Windows
		}
		k.Accounts = append(k.Accounts, name)
		if u.Name != "" && u.Name != name {
			k.GitNames = append(k.GitNames, u.Name) // the account's full name
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		k.Homes = append(k.Homes, home)
	}
	if host, err := os.Hostname(); err == nil {
		k.Hosts = append(k.Hosts, host)
		if short, _, ok := strings.Cut(host, "."); ok {
			k.Hosts = append(k.Hosts, short)
		}
	}
	for _, key := range []string{"user.name", "user.email"} {
		out, err := exec.Command("git", "config", "--get", key).Output()
		v := strings.TrimSpace(string(out))
		if err != nil || v == "" {
			continue
		}
		if key == "user.name" {
			k.GitNames = append(k.GitNames, v)
		} else {
			k.GitEmails = append(k.GitEmails, v)
		}
	}
	return k
}

type scrubber struct {
	r      *redact.Redactor
	k      redact.Known // the values r looks for, to pick each one's placeholder
	root   string
	dry    bool
	stdout io.Writer

	files, changed, spans, review int
}

// shown is how a path appears in the output: relative to the root, with
// slashes, and redacted itself, so the listing never holds a removed value.
func (s *scrubber) shown(path string) (string, []redact.Span) {
	rel, err := filepath.Rel(s.root, path)
	if err != nil {
		rel = filepath.Base(path)
	}
	rel = filepath.ToSlash(rel)
	spans := s.r.Find(rel)
	return redact.Render(rel, spans), spans
}

func (s *scrubber) visit(path string, d fs.DirEntry, err error) error {
	shown, _ := s.shown(path)
	if err != nil {
		fmt.Fprintf(s.stdout, "%s: can't read: %v\n", shown, pathless(err))
		s.review++
		if d != nil && d.IsDir() {
			return fs.SkipDir
		}
		return nil
	}
	if path != s.root {
		if spans := redact.Merge(s.r.Find(d.Name())); len(spans) > 0 {
			fmt.Fprintf(s.stdout, "%s: the name holds %s; rename it by hand\n", shown, kinds(spans))
			s.review++
		}
	}
	switch {
	case d.IsDir():
		return nil
	case d.Type()&fs.ModeSymlink != 0:
		fmt.Fprintf(s.stdout, "%s: a symlink, not followed; review it by hand\n", shown)
		s.review++
		return nil
	case !d.Type().IsRegular():
		fmt.Fprintf(s.stdout, "%s: not a regular file; review it by hand\n", shown)
		s.review++
		return nil
	case d.Name() == "PROVENANCE.md":
		return nil
	}
	s.file(path, shown)
	return nil
}

func (s *scrubber) file(path, shown string) {
	b, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(s.stdout, "%s: can't read: %v\n", shown, pathless(err))
		s.review++
		return
	}
	s.files++
	if bytes.IndexByte(b, 0) >= 0 || !utf8.Valid(b) {
		fmt.Fprintf(s.stdout, "%s: binary, not scrubbed; review it by hand\n", shown)
		s.review++
		return
	}
	text := string(b)
	// An address that can stay goes before the spans are merged, so a known
	// value or a URL's password inside it keeps a span of its own and is
	// still replaced. Merged first, the address would be the longest span
	// and take whatever it holds with it (D-email-redaction). Most keys
	// get no span of their own right before an @ or a colon, so localPart
	// and urlCredentials look for them.
	var found []redact.Span
	for _, sp := range slices.Concat(s.r.Find(text), telemetryIDs(text), s.urlCredentials(text)) {
		if sp.Type == redact.TypeEmail && publicAddress(text[sp.Start:sp.End]) {
			found = append(found, s.localPart(text, sp)...)
			continue
		}
		found = append(found, sp)
	}
	// A placeholder from an earlier run stays, even where a rule other than
	// the one that wrote it finds it, as url-password finds
	// REDACTED_STRIPE_ACCESS_TOKEN in a URL's password, or the capture of
	// capture@example.invalid. So scrubbing a scrubbed file changes nothing.
	found = slices.DeleteFunc(found, func(sp redact.Span) bool {
		return earlierPlaceholder(text[sp.Start:sp.End])
	})
	var edits []edit
	for _, sp := range redact.Merge(found) {
		with := s.placeholder(text, sp)
		if with == text[sp.Start:sp.End] {
			// A placeholder from an earlier run, so scrubbing a scrubbed
			// file changes nothing.
			continue
		}
		edits = append(edits, edit{sp, with})
	}
	if len(edits) == 0 {
		return
	}
	for _, e := range edits {
		line, col := position(text, e.Start)
		fmt.Fprintf(s.stdout, "%s:%d:%d: %s, %d bytes\n", shown, line, col, kinds([]redact.Span{e.Span}), e.End-e.Start)
	}
	if line, broken := breaksJSON(text, edits); broken {
		where := shown
		if line > 0 {
			where += ":" + strconv.Itoa(line)
		}
		fmt.Fprintf(s.stdout, "%s: these edits would leave invalid JSON, so the file is left as it was; review it by hand\n", where)
		s.review++
		return
	}
	s.spans += len(edits)
	s.changed++
	if s.dry {
		return
	}
	if err := replaceFile(path, []byte(apply(text, edits))); err != nil {
		fmt.Fprintf(s.stdout, "%s: can't write: %v\n", shown, pathless(err))
		s.review++
	}
}

// edit replaces a span of a file with a placeholder.
type edit struct {
	redact.Span
	with string
}

// apply returns text with each edit's span replaced. The edits are sorted
// and disjoint, as Merge leaves them.
func apply(text string, edits []edit) string {
	var b strings.Builder
	at := 0
	for _, e := range edits {
		b.WriteString(text[at:e.Start])
		b.WriteString(e.with)
		at = e.End
	}
	b.WriteString(text[at:])
	return b.String()
}

// breaksJSON reports whether the edits would break JSON that text holds,
// as removing a decimal's digits that a pattern took for a card's would
// (issue #104). Text that parses as JSON must still parse. In other text,
// such as a JSON Lines transcript, each line that is a JSON object or
// array must stay one, on a line of its own, and line is the first that
// wouldn't, counted from 1. It's 0 for text that parses as a whole.
func breaksJSON(text string, edits []edit) (line int, broken bool) {
	if json.Valid([]byte(text)) {
		return 0, !json.Valid([]byte(apply(text, edits)))
	}
	n, k := 1, 0
	for start := 0; start < len(text); {
		// A line, with the lines after it that an edit starting in it
		// runs on into.
		end, j := lineAfter(text, start), k
		for ; j < len(edits) && edits[j].Start < end; j++ {
			end = max(end, lineAfter(text, edits[j].End))
		}
		if unit := text[start:end]; j > k {
			if strings.Contains(strings.TrimSuffix(unit, "\n"), "\n") {
				// An edit across a line break joins lines, so a JSON line
				// among them no longer stands on its own.
				for i, l := range strings.Split(unit, "\n") {
					if jsonRecord(l) {
						return n + i, true
					}
				}
			} else if jsonRecord(unit) {
				here := slices.Clone(edits[k:j])
				for i := range here {
					here[i].Start -= start
					here[i].End -= start
				}
				if !jsonRecord(apply(unit, here)) {
					return n, true
				}
			}
		}
		n += strings.Count(text[start:end], "\n")
		start, k = end, j
	}
	return 0, false
}

// jsonRecord reports whether s is a JSON object or array, with nothing but
// white space around it. A line that is only a number or a quoted string
// is more likely plain text than a record.
func jsonRecord(s string) bool {
	t := strings.TrimLeft(s, " \t\r\n")
	return t != "" && (t[0] == '{' || t[0] == '[') && json.Valid([]byte(s))
}

// lineAfter is the offset just past the line break that ends the line
// holding offset i, or len(text) when no line break follows.
func lineAfter(text string, i int) int {
	if k := strings.IndexByte(text[i:], '\n'); k >= 0 {
		return i + k + 1
	}
	return len(text)
}

// The placeholders of testdata/fixtures/README.md, which tests of the
// adapters rely on. WP-AD0's scrub.py writes the same ones.
const (
	placeholderAccount = "capture"
	placeholderHost    = "capture-host"
	placeholderName    = "Capture User"
	placeholderEmail   = "capture@example.invalid"
	placeholderOther   = "REDACTED"
	placeholderID      = "00000000-0000-4000-8000-000000000000"
)

// typeTelemetryID marks the value of an OTLP attribute that identifies an
// account. It's the scrub's own type: pkg/redact has no reason to find
// these in a prompt, and in a fixture each one is listed like any span.
const typeTelemetryID redact.Type = "telemetry_id"

// The OTLP/JSON attributes that identify an account or an organization,
// which Claude Code puts on every metric and event by default
// (docs/research/ai-harness-signals.md 3.3). The same keys as scrub.py's.
const telemetryKeys = `user\.account_uuid|user\.account_id|user\.id|organization\.id|enduser\.id`

// telemetryValue is an attribute's value: a JSON string, whose contents
// are the first group, or a bare number, the second.
const telemetryValue = `\{\s*"(?:stringValue|intValue)"\s*:\s*(?:"((?:[^"\\]|\\.)*)"|(-?[0-9]+))`

// telemetryIDPatterns find the value of such an attribute, with "key"
// before "value" or after it, since a JSON object's fields have no order.
// The first has the key in group 1 and the value in groups 2 and 3, the
// second the value in groups 1 and 2 and the key in group 3.
var telemetryIDPatterns = []*regexp.Regexp{
	regexp.MustCompile(`"key"\s*:\s*"(` + telemetryKeys + `)"\s*,\s*"value"\s*:\s*` + telemetryValue),
	regexp.MustCompile(`"value"\s*:\s*` + telemetryValue + `\s*\}\s*,\s*"key"\s*:\s*"(` + telemetryKeys + `)"`),
}

// telemetryIDs returns a span for the value of each account or
// organization id attribute in an OTLP/JSON body, with the attribute's key
// as its Rule, so the listing names it.
func telemetryIDs(text string) []redact.Span {
	var spans []redact.Span
	for i, re := range telemetryIDPatterns {
		key, str, num := 1, 2, 3
		if i == 1 {
			key, str, num = 3, 1, 2
		}
		for _, m := range re.FindAllStringSubmatchIndex(text, -1) {
			v := str
			if m[2*v] < 0 {
				v = num
			}
			spans = append(spans, redact.Span{Start: m[2*v], End: m[2*v+1], Type: typeTelemetryID, Rule: text[m[2*key]:m[2*key+1]]})
		}
	}
	return spans
}

// isNumber reports whether s is an integer in JSON's form.
func isNumber(s string) bool {
	s = strings.TrimPrefix(s, "-")
	return s != "" && strings.Trim(s, "0123456789") == ""
}

// placeholder is what takes span sp's place in text. A known value gets
// its kind's placeholder written the way the text writes the value, so a
// JSON-escaped home gets a JSON-escaped placeholder and Claude Code's
// project folder for it, C--Users-Maya-Chen-robot, becomes
// C--Users-capture-robot. A span that holds more than one value, once
// merged, gets REDACTED.
func (s *scrubber) placeholder(text string, sp redact.Span) string {
	matched := text[sp.Start:sp.End]
	switch {
	case sp.Type == redact.TypeEmail:
		return placeholderEmail
	case sp.Type == redact.TypeSecret:
		if sp.Rule == "" {
			return "REDACTED_SECRET"
		}
		return "REDACTED_" + strings.ToUpper(strings.ReplaceAll(sp.Rule, "-", "_"))
	case sp.Type == typeTelemetryID:
		if isNumber(matched) {
			return "0" // an intValue, which has to stay a number
		}
		return placeholderID
	case sp.Type.Reveal() == "known":
		for _, v := range knownValues(s.k, sp.Type) {
			if p, ok := redact.Respell(sp.Type, v, matched, knownPlaceholder(sp.Type, v)); ok {
				return p
			}
		}
		return placeholderOther
	}
	return "REDACTED_" + strings.ToUpper(string(sp.Type))
}

// knownValues returns the values of type t in k.
func knownValues(k redact.Known, t redact.Type) []string {
	switch t {
	case redact.TypeAccount:
		return k.Accounts
	case redact.TypeHome:
		return k.Homes
	case redact.TypeHost:
		return k.Hosts
	case redact.TypeGitName:
		return k.GitNames
	case redact.TypeGitEmail:
		return k.GitEmails
	case redact.TypeGitHub:
		return k.GitHub
	case redact.TypeHCA:
		return k.HCA
	case redact.TypePrivate:
		return k.Private
	case redact.TypeDeny:
		return k.Deny
	}
	return nil
}

// knownPlaceholder is README's placeholder for the known value v of type t.
func knownPlaceholder(t redact.Type, v string) string {
	switch t {
	case redact.TypeHome:
		return homePlaceholder(v)
	case redact.TypeAccount:
		return placeholderAccount
	case redact.TypeHost:
		return placeholderHost
	case redact.TypeGitName:
		return placeholderName
	case redact.TypeGitEmail:
		return placeholderEmail
	}
	return placeholderOther
}

// homePlaceholder is the capture home for the system home belongs to:
// C:\Users\capture for a Windows home, with its separator, /Users/capture
// for a macOS one and /home/capture for any other.
func homePlaceholder(home string) string {
	home = strings.TrimSpace(home)
	switch {
	case len(home) >= 2 && home[1] == ':':
		if strings.Contains(home, `\`) || !strings.Contains(home, "/") {
			return `C:\Users\capture`
		}
		return "C:/Users/capture"
	case strings.HasPrefix(home, "/Users/"):
		return "/Users/capture"
	}
	return "/home/capture"
}

// publicAddresses are addresses tools write into commits and remotes, such
// as the Co-Authored-By trailers of Claude Code and Aider, which the git
// adapter reads to tell an agent's commit from a person's (HS 13.3). They
// name nobody, and keeping them keeps fixtures true to what the harness
// wrote. testdata/fixtures/README.md lists them, and scrub.py keeps the
// same ones.
var publicAddresses = map[string]bool{
	"noreply@anthropic.com": true,
	"aider@aider.chat":      true,
	"noreply@github.com":    true,
	"noreply@openai.com":    true,
	"git@github.com":        true,
	"git@gitlab.com":        true,
	"git@bitbucket.org":     true,
	"git@ssh.dev.azure.com": true,
}

// publicAddress reports whether an email address can stay in a fixture:
// one of publicAddresses, or one at a domain reserved for examples, which
// reaches nobody (RFC 2606 and RFC 6761). Under the reserved top-level
// domains, only a name of one label counts, such as example.invalid or
// myapp.test. A longer one, such as westfield.edu.invalid, is a real
// address with a reserved label appended, which is how sandbox anonymizers
// disable one, and it still names someone (D-email-redaction-tld).
func publicAddress(addr string) bool {
	a := strings.ToLower(addr)
	if publicAddresses[a] {
		return true
	}
	domain := a[strings.LastIndexByte(a, '@')+1:]
	for _, d := range []string{"example.com", "example.org", "example.net"} {
		if domain == d || strings.HasSuffix(domain, "."+d) {
			return true
		}
	}
	for _, tld := range []string{".example", ".invalid", ".test", ".localhost"} {
		if label, ok := strings.CutSuffix(domain, tld); ok && label != "" && !strings.Contains(label, ".") {
			return true
		}
	}
	return false
}

// Most of gitleaks' rules end a key at a quote, whitespace, a semicolon or
// the end of the text, never at an @ or a colon, so Find gives a key right
// before one no span. localPart and urlCredentials scan the places where
// a key sits that way on their own, where the end of the text ends it.

// localPart returns spans for a key that is a kept address's local part,
// as in sk_live_...@api.example.com. The address's span was the only one
// that covered it.
func (s *scrubber) localPart(text string, sp redact.Span) []redact.Span {
	i := strings.LastIndexByte(text[sp.Start:sp.End], '@')
	if i < 0 {
		// Not reached: jsonView blanks an escaped @, so every address Find
		// gives holds a real one.
		return nil
	}
	return s.findIn(text, sp.Start, sp.Start+i)
}

// urlCredentials returns spans for the user and the password of each URL
// in text that has them, whatever its host: USER and PASSWORD in
// https://USER:PASSWORD@host. A URL here is two or more slashes, among
// backslashes when the URL sits in JSON as \/\/ or \\/\\/, then an
// authority holding an @. The user runs to the first colon and the
// password from there to the last @, the split scrub.py's URL_PASSWORD
// makes. pkg/redact's url-password rule makes it too, but only when the
// user holds no @.
// The user is scanned on its own, since no span covers a key followed by
// the colon, or by the @ of a host that isn't an address. The password gets
// url-password's span, which the rule gives it only after scheme://, so a
// URL written in JSON or without its scheme would keep it at a kept
// address.
func (s *scrubber) urlCredentials(text string) []redact.Span {
	var found []redact.Span
	for i := 0; i < len(text); {
		if text[i] != '/' && text[i] != '\\' {
			i++
			continue
		}
		slash := i
		j, slashes := i, 0
		for ; j < len(text) && (text[j] == '/' || text[j] == '\\'); j++ {
			if text[j] == '/' {
				slashes++
			}
		}
		end := j
		for end < len(text) {
			if authorityByte(text[end]) {
				end++
			} else if n := escapedAuthorityByte(text, end); n > 0 {
				end += n
			} else {
				break
			}
		}
		i = end
		at := strings.LastIndexByte(text[j:end], '@')
		if slashes < 2 || at < 0 {
			continue
		}
		at += j
		userEnd := at
		if colon := strings.IndexByte(text[j:at], ':'); colon >= 0 {
			userEnd = j + colon
		}
		found = append(found, s.findIn(text, j, userEnd)...)
		// A run that goes on into a query or a fragment, as in
		// http://KEY@localhost?next=a@b, holds the host's @, or a ? or #,
		// in the user, and no rule ends a key at those. So the part before
		// the first of them is scanned on its own as well.
		if k := strings.IndexAny(text[j:userEnd], "@?#"); k >= 0 {
			found = append(found, s.findIn(text, j, j+k)...)
		}
		// RFC 3986 keeps [ and ] out of a user and a password, and two
		// slashes with no scheme before them are often something else, such
		// as the namespaced XPath //xs:element[@name]. So there a user
		// holding one has no password, and a password ends at the last @
		// before one, as in scrub.py.
		pwStart, pwEnd := userEnd+1, at
		if pwStart < pwEnd && !afterScheme(text, slash) {
			if strings.ContainsAny(text[j:userEnd], "[]") {
				pwEnd = pwStart
			} else if b := strings.IndexAny(text[pwStart:pwEnd], "[]"); b >= 0 {
				pwEnd = pwStart + max(strings.LastIndexByte(text[pwStart:pwStart+b], '@'), 0)
			}
		}
		if pwStart < pwEnd {
			found = append(found, redact.Span{Start: pwStart, End: pwEnd, Type: redact.TypeSecret, Rule: "url-password"})
		}
	}
	return found
}

// authorityByte reports whether c can be part of a URL's user, password or
// host as a capture writes it. A control character, a space, a slash, a
// backslash, a quote or an angle bracket ends the URL or the JSON string
// around it. ? and # end an authority in RFC 3986, but people put them in
// passwords, so they don't end it here.
func authorityByte(c byte) bool {
	return c > ' ' && c != 0x7f && !strings.ContainsRune(`/\'"<>`, rune(c))
}

// escapedAuthorityByte returns the length of the JSON \u escape at text[i:]
// when it stands for a character authorityByte accepts, as encoding/json
// writes the & of a password, or 0.
func escapedAuthorityByte(text string, i int) int {
	if i+6 > len(text) || text[i] != '\\' || text[i+1] != 'u' {
		return 0
	}
	v, err := strconv.ParseUint(text[i+2:i+6], 16, 16)
	if err != nil || v < 0x80 && !authorityByte(byte(v)) {
		return 0
	}
	return 6
}

// afterScheme reports whether text[:i] ends with a URL's scheme and its
// colon, such as postgres:.
func afterScheme(text string, i int) bool {
	if i == 0 || text[i-1] != ':' {
		return false
	}
	k := i - 1
	for k > 0 && strings.IndexByte("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789+.-", text[k-1]) >= 0 {
		k--
	}
	c := text[k] | 0x20
	return k < i-1 && c >= 'a' && c <= 'z'
}

// findIn returns Find's spans for text[start:end] alone, as offsets into
// text.
func (s *scrubber) findIn(text string, start, end int) []redact.Span {
	if start >= end {
		return nil
	}
	spans := s.r.Find(text[start:end])
	for i := range spans {
		spans[i].Start += start
		spans[i].End += start
	}
	return spans
}

// earlierPlaceholder reports whether s is a whole placeholder the scrub
// writes: one of README's, or REDACTED and a rule's or a type's name, in
// capitals with _ for -. A span that runs on past one, as the
// REDACTED_URL_PASSWORD@ssw0rd of a password with an @ that scrub.py
// ended at the first, isn't one.
func earlierPlaceholder(s string) bool {
	switch s {
	case placeholderAccount, placeholderHost, placeholderName, placeholderEmail, placeholderID, placeholderOther:
		return true
	}
	name, ok := strings.CutPrefix(s, placeholderOther+"_")
	return ok && name != "" && strings.Trim(name, "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_") == ""
}

// kinds names the spans' types, with the gitleaks rule for a secret.
func kinds(spans []redact.Span) string {
	var out []string
	for _, sp := range spans {
		k := string(sp.Type)
		if sp.Rule != "" {
			k += " " + sp.Rule
		}
		out = append(out, k)
	}
	return strings.Join(out, ", ")
}

// position is the 1-based line and byte column of offset i.
func position(text string, i int) (line, col int) {
	before := text[:i]
	return strings.Count(before, "\n") + 1, i - strings.LastIndexByte(before, '\n')
}

// pathless drops the path an fs error carries, since the path may hold a
// value the listing must not show.
func pathless(err error) error {
	var pe *fs.PathError
	if errors.As(err, &pe) {
		return pe.Err
	}
	var le *os.LinkError
	if errors.As(err, &le) {
		return le.Err
	}
	return err
}

// replaceFile writes data over path by renaming a temporary file into
// place, so an interrupted run never leaves half a file. It keeps the
// file's permission bits.
func replaceFile(path string, data []byte) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".pairtime-scrub-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(info.Mode().Perm()); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
