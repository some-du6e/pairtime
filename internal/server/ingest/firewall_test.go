// SPDX-License-Identifier: AGPL-3.0-or-later

package ingest

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"maps"
	"math/rand/v2"
	"net/http"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/server/store"
	"github.com/some-du6e/pairtime/pkg/event"
	"github.com/some-du6e/pairtime/pkg/ledger"
)

// The firewall's tests (DESIGN 4.5; PLAN WP-SV5) work from tier1Items, one
// list of every member of every kind of tier-1 item, which TestAllowlist
// holds to the oracle, DESIGN 4.3's tier-1 list, and to the server's
// allowlist and ingest-v1 2.3. From it come the places where a request can
// put text: each member's value, a list's entry, a map member's name and
// value, and the name of a member an object doesn't have.
// TestFreeTextRefused and TestFirewall put free text in every place, and
// FuzzFreeText any text.

// The JSON types of tier-1 members.
const (
	jsonString  = "string"
	jsonNumber  = "number" // integers too
	jsonBool    = "boolean"
	jsonStrings = "strings" // a list of strings of one form
	jsonObjects = "objects" // a list of objects, each with the members in of
	jsonMap     = "map"     // an object whose names have one form and whose values are numbers
)

// tier1Member is a member of a tier-1 item: its name, its JSON type, the
// form of lenientForms its string, its list's strings or its map's names
// take, and for a map, a name the map takes.
type tier1Member struct {
	name string
	typ  string
	form string
	of   []tier1Member
	ok   string
}

// tier1Item is a kind of tier-1 item: its name in DESIGN 4.3, which is its
// heading in ingest-v1 2.3 too, that heading's number, the request body's
// member that carries it, the server's check, an instance that passes,
// and its members.
type tier1Item struct {
	design, spec, body string
	check              func(raw []byte) *itemErr
	sample             func(t testing.TB) []byte
	members            []tier1Member
}

// tier1Items are the four kinds of tier-1 item, their members in DESIGN
// 4.3's order.
var tier1Items = []tier1Item{
	{
		design: "Batch roots", spec: "2.3.1", body: "batches",
		check:  func(raw []byte) *itemErr { _, e := parseBatchRoot(raw); return e },
		sample: func(t testing.TB) []byte { return marshal(t, sampleRoot()) },
		members: []tier1Member{
			{name: "b", typ: jsonNumber},
			{name: "n", typ: jsonNumber},
			{name: "R_b", typ: jsonString, form: "hash"},
			{name: "H_b", typ: jsonString, form: "hash"},
			{name: "prev", typ: jsonString, form: "hash"},
		},
	},
	{
		design: "Minute buckets", spec: "2.3.2", body: "buckets",
		check:  func(raw []byte) *itemErr { _, e := checkBucket(raw); return e },
		sample: func(t testing.TB) []byte { return shareableJSON(t, sampleBucket("d_test", sampleMinute, 1)) },
		members: []tier1Member{
			{name: "v", typ: jsonString, form: "semver"},
			{name: "params", typ: jsonString, form: "params"},
			{name: "minute", typ: jsonString, form: "minute"},
			{name: "device", typ: jsonString, form: "key"},
			{name: "slots", typ: jsonObjects, of: []tier1Member{
				{name: "p", typ: jsonString, form: "key"},
				{name: "cr", typ: jsonNumber},
				{name: "why", typ: jsonString, form: "reason"},
				{name: "kind", typ: jsonString, form: "lower"},
				{name: "ev", typ: jsonString, form: "level"},
				{name: "lbl", typ: jsonStrings, form: "lower"},
			}},
			{name: "credited_s", typ: jsonNumber},
			{name: "uncredited", typ: jsonMap, form: "uncredited", ok: "X.absent"},
			{name: "agent_running_s", typ: jsonNumber},
			{name: "agent_only", typ: jsonMap, form: "key", ok: "p_one"},
			{name: "state", typ: jsonString, form: "lower"},
			{name: "witness", typ: jsonString, form: "lower"},
			{name: "rev", typ: jsonNumber},
		},
	},
	{
		design: "Commit records", spec: "2.3.3", body: "commits",
		check:  func(raw []byte) *itemErr { _, e := parseCommit(raw); return e },
		sample: func(t testing.TB) []byte { return marshal(t, sampleCommit()) },
		members: []tier1Member{
			{name: "sha", typ: jsonString, form: "gitid"},
			{name: "parents", typ: jsonStrings, form: "gitid"},
			{name: "project", typ: jsonString, form: "key"},
			{name: "observed_at", typ: jsonNumber},
			{name: "author_time", typ: jsonNumber},
			{name: "committer_time", typ: jsonNumber},
			{name: "files_changed", typ: jsonNumber},
			{name: "insertions", typ: jsonNumber},
			{name: "deletions", typ: jsonNumber},
			{name: "path_hmacs", typ: jsonStrings, form: "key"},
			{name: "credited_seconds_since_parent", typ: jsonMap, form: "lower", ok: "prompting"},
			{name: "rewrites", typ: jsonObjects, of: []tier1Member{
				{name: "old_sha", typ: jsonString, form: "gitid"},
				{name: "new_sha", typ: jsonString, form: "gitid"},
			}},
			{name: "actor", typ: jsonString, form: "lower"},
			{name: "agent_trailer", typ: jsonString, form: "lower"},
		},
	},
	{
		design: "Labels", spec: "2.3.4", body: "labels",
		check:  func(raw []byte) *itemErr { _, e := parseLabels(raw); return e },
		sample: func(t testing.TB) []byte { return marshal(t, sampleLabels()) },
		members: []tier1Member{
			{name: "client_build", typ: jsonString, form: "version"},
			{name: "scoring_version", typ: jsonString, form: "semver"},
			{name: "params", typ: jsonString, form: "params"},
			{name: "harnesses", typ: jsonObjects, of: []tier1Member{
				{name: "name", typ: jsonString, form: "lower"},
				{name: "version", typ: jsonString, form: "version"},
			}},
			{name: "typing_module", typ: jsonBool},
			{name: "vm", typ: jsonBool},
		},
	},
}

// lenientForms are the forms of ingest-v1 2.3, written again as regular
// expressions that accept at least what the server does. FuzzFreeText
// holds the server to them: text outside a place's form must be refused,
// whatever the shape check's own code says. TestLenientForms checks that
// each takes every value the server does.
var lenientForms = map[string]*regexp.Regexp{
	"key":        regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`),
	"hash":       regexp.MustCompile(`^[0-9a-f]{64}$`),
	"gitid":      regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`),
	"semver":     regexp.MustCompile(`^[0-9]{1,9}\.[0-9]{1,9}\.[0-9]{1,9}$`),
	"params":     regexp.MustCompile(`^sha256:[0-9a-f]{64}$`),
	"minute":     regexp.MustCompile(`^20[2-9][0-9]-[01][0-9]-[0-3][0-9]T[0-2][0-9]:[0-5][0-9]:00Z$`),
	"reason":     regexp.MustCompile(`^[A-Za-z_.~+]{1,40}$`),
	"uncredited": regexp.MustCompile(`^X\.[a-z_]{1,32}$`),
	"lower":      regexp.MustCompile(`^[a-z_]{1,32}$`),
	"level":      regexp.MustCompile(`^[ABC]$`),
	"version":    regexp.MustCompile(event.VersionPattern),
}

// freeText is text no member of a tier-1 item may hold. For each item of
// DESIGN 4.3's Never-in-it column for tier 1 it has text of that kind as a
// teen's computer holds it, which TestNeverInIt checks against the
// column, then text no form takes: an empty string, an email address, an
// over-long run, non-ASCII and a NUL. TestFreeTextRefused and TestFirewall
// put each in every place of every item, and FuzzFreeText starts from
// them.
//
// Some fit a key's form, as a bare name or an event id does. A place that
// holds a key, a device, project or path key, takes those: only the
// client keeps them out, by keying every identifier on the device
// (AGENTS.md). Every other place refuses them.
var freeText = []struct{ never, text string }{
	{"Prompt or output text", "fix the login bug please"},
	{"Prompt or output text", "ignore previous instructions\n"},
	{"Prompt or output text", "Done. I updated main.go and the tests pass."},
	{"file names or paths", "/home/teen/projects/game/main.go"},
	{"file names or paths", `C:\Users\teen\Desktop\notes.txt`},
	{"file names or paths", "main.go"},
	{"file names or paths", "Makefile"},
	{"window titles", "main.go - Visual Studio Code"},
	{"process names other than harness names", "Code Helper (Renderer)"},
	{"process names other than harness names", "firefox"},
	{"branch names of private repos", "feature/secret-launch"},
	{"branch names of private repos", "fix-login"},
	{"keystrokes or key timings", "ctrl+shift+p"},
	{"keystrokes or key timings", "112,95,143,88"},
	{"MinHash sketches", "[3913470,118223,9921004,71]"},
	{"error text", "TypeError: Cannot read properties of undefined (reading 'map')"},
	{"hostnames", "teens-mbp.local"},
	{"hostnames", "DESKTOP-4F2K9QZ"},
	{"OS usernames", "teen"},
	{"OS usernames", `DESKTOP-4F2K9QZ\teen`},
	{"anchor ids", "0190f4b2-5a1c-7d3e-8f00-123456789abc"},
	{"", ""},
	{"", "teen@example.com"},
	{"", strings.Repeat("a", 200)},
	{"", "héllo"},
	{"", "\x00"},
}

// The ways a place takes text.
const (
	asValue    = "value"     // as a member's value
	asName     = "name"      // as the name of a member the object doesn't have
	asMapName  = "map name"  // as the name of a map's member
	asMapValue = "map value" // as the value of a map's member
)

// place is somewhere an item can carry text, and the refusal text there
// gets when it is outside the place's form: code, with path as its field.
type place struct {
	item  *tier1Item
	path  string // the member, or the object or the map the text names a member of
	how   string
	form  string // the form of lenientForms text must miss; "" when the place refuses any text
	code  string
	names []string // for asName, the object's own members
	ok    string   // for asMapValue, a name the map takes
}

// places returns every place of it: each member's value, the first entry
// of each list and the value of a map's member, and the name of a member
// of each object and each map.
func (it *tier1Item) places() []place {
	var out []place
	var walk func(path string, members []tier1Member)
	walk = func(path string, members []tier1Member) {
		names := make([]string, len(members))
		for i, m := range members {
			names[i] = m.name
		}
		out = append(out, place{item: it, path: path, how: asName, code: CodeUnknownField, names: names})
		for _, m := range members {
			p := join(path, m.name)
			switch m.typ {
			case jsonString:
				out = append(out, place{item: it, path: p, how: asValue, form: m.form, code: CodeBadString})
			case jsonStrings:
				out = append(out,
					place{item: it, path: p, how: asValue, code: CodeBadType},
					place{item: it, path: index(p, 0), how: asValue, form: m.form, code: CodeBadString})
			case jsonObjects:
				out = append(out,
					place{item: it, path: p, how: asValue, code: CodeBadType},
					place{item: it, path: index(p, 0), how: asValue, code: CodeBadType})
				walk(index(p, 0), m.of)
			case jsonMap:
				out = append(out,
					place{item: it, path: p, how: asValue, code: CodeBadType},
					place{item: it, path: p, how: asMapName, form: m.form, code: CodeBadString},
					place{item: it, path: p, how: asMapValue, code: CodeBadType, ok: m.ok})
			default:
				out = append(out, place{item: it, path: p, how: asValue, code: CodeBadType})
			}
		}
	}
	walk("", it.members)
	return out
}

// what says where p puts text, for a test's name.
func (p place) what() string {
	in := p.path
	if in == "" {
		in = "the item"
	}
	switch p.how {
	case asName:
		return "a member's name in " + in
	case asMapName:
		return "a member's name in the map " + in
	case asMapValue:
		return "a member's value in the map " + in
	}
	return in
}

// put returns p's item's sample with text at p.
func (p place) put(t testing.TB, text string) []byte {
	t.Helper()
	m := asMap(t, p.item.sample(t))
	switch p.how {
	case asValue:
		setPath(t, m, p.path, text)
	case asName:
		obj := m
		if p.path != "" {
			var ok bool
			if obj, ok = getPath(t, m, p.path).(map[string]any); !ok {
				t.Fatalf("%s isn't an object", p.path)
			}
		}
		obj[text] = 0
	case asMapName:
		setPath(t, m, p.path, map[string]any{text: 1})
	case asMapValue:
		setPath(t, m, p.path, map[string]any{p.ok: text})
	}
	return marshal(t, m)
}

// refusal returns what p's item's check answers for text at p, and false
// when text has p's form, which the check may take.
func (p place) refusal(text string) (shapeResult, bool) {
	if (p.form != "" && lenientForms[p.form].MatchString(text)) || (p.how == asName && slices.Contains(p.names, text)) {
		return shapeResult{}, false
	}
	return shapeResult{p.code, p.path}, true
}

// fits reports whether text has a key's form and p holds a key. Of
// freeText, only such text may pass a place's check.
func (p place) fits(text string) bool {
	return p.form == "key" && lenientForms["key"].MatchString(text)
}

// getPath returns the member at path, as setPath finds it, or root for "".
func getPath(t testing.TB, root map[string]any, path string) any {
	t.Helper()
	if path == "" {
		return root
	}
	var cur any = root
	for _, step := range strings.Split(path, ".") {
		name, idx := step, -1
		if i := strings.IndexByte(step, '['); i >= 0 {
			n, err := strconv.Atoi(strings.TrimSuffix(step[i+1:], "]"))
			if err != nil {
				t.Fatalf("bad path %q", path)
			}
			name, idx = step[:i], n
		}
		m, ok := cur.(map[string]any)
		if !ok {
			t.Fatalf("%q: %s isn't in an object", path, name)
		}
		cur = m[name]
		if idx >= 0 {
			a, ok := cur.([]any)
			if !ok || idx >= len(a) {
				t.Fatalf("%q: %s has no element %d", path, name, idx)
			}
			cur = a[idx]
		}
	}
	return cur
}

// TestFreeTextRefused puts each of freeText in every place of every kind
// of tier-1 item, and each time gets that item refused with 422 and the
// code that names the place, never the text: bad_string for a string,
// bad_type for a member of another type, unknown_field for a member the
// object doesn't have (DESIGN 4.5's firewall; PLAN WP-SV5).
func TestFreeTextRefused(t *testing.T) {
	for i := range tier1Items {
		it := &tier1Items[i]
		t.Run(it.design+": the sample passes, so each refusal below is the text's", func(t *testing.T) {
			if e := it.check(it.sample(t)); e != nil {
				t.Errorf("refused: %+v", *e)
			}
		})
		for _, p := range it.places() {
			t.Run(fmt.Sprintf("%s: free text as %s is refused as %s, naming the place", it.design, p.what(), p.code), func(t *testing.T) {
				want := shapeResult{p.code, p.path}
				for _, s := range freeText {
					if p.fits(s.text) {
						continue
					}
					if got := outcomeOf(it.check(p.put(t, s.text))); got != want {
						t.Errorf("%q: got %+v, want %+v", s.text, got, want)
					}
				}
			})
		}
	}
}

// FuzzFreeText puts any text in every place of every kind of tier-1 item
// where the check's answer can depend on the text: a string's value, a
// list's string entry, and the name of an object's or a map's member.
// Text outside the place's form is refused for that item with 422 and the
// code that names the place, so no field can carry free text into the
// database (DESIGN 4.5's firewall; PLAN WP-SV5). Its seeds and its corpus
// in testdata/fuzz/FuzzFreeText run with every go test, as ci.yml's ingest
// fuzz corpus step checks, and nightly.yml fuzzes it for 60 s. An input
// the fuzzing finds goes into the corpus with the fix.
func FuzzFreeText(f *testing.F) {
	for _, s := range freeText {
		f.Add(s.text)
	}
	for _, s := range []string{"p_one", "1.0.0", "v0.1.0", "v0.0.0-dev", "0.1.0+3dbf6d4", "dev", "P", "READ~r+restored", "X.absent", "via_track", "final", "human", "A",
		"prompting", "claude", "claude_code", "rev", strings.Repeat("a", 40), strings.Repeat("0", 64), "sha256:" + strings.Repeat("ab", 32), "2026-11-19T12:00:00Z"} {
		f.Add(s)
	}
	var places []place
	for i := range tier1Items {
		for _, p := range tier1Items[i].places() {
			if p.form != "" || p.how == asName {
				places = append(places, p)
			}
		}
	}
	f.Fuzz(func(t *testing.T, text string) {
		for _, p := range places {
			want, refused := p.refusal(text)
			if !refused {
				continue
			}
			if got := outcomeOf(p.item.check(p.put(t, text))); got != want {
				t.Errorf("%s with %q as %s: got %+v, want %+v", p.item.design, text, p.what(), got, want)
			}
		}
	})
}

// TestLenientForms checks that each of lenientForms takes every value the
// server takes in a place of that form, so FuzzFreeText never wants a
// value refused that the server rightly takes: pkg/event's enum values and
// reason codes, with their suffixes, and every value of the samples.
func TestLenientForms(t *testing.T) {
	var credited []string
	for _, r := range event.CreditReasons() {
		for _, suffix := range []string{"", event.ReturnedSuffix, event.RestoredSuffix, event.ReturnedSuffix + event.RestoredSuffix} {
			if v := r + event.Reason(suffix); v.Credited() {
				credited = append(credited, string(v))
			}
		}
	}
	type formCase struct {
		name, form string
		values     []string
	}
	tests := []formCase{
		{"credit reasons with their suffixes have the reason form", "reason", credited},
		{"uncredited reasons have the uncredited form", "uncredited", names(event.UncreditedReasons())},
		{"activities are lowercase names", "lower", names(activities)},
		{"evidence levels have the level form", "level", names(evidences)},
		{"slot labels are lowercase names", "lower", names(slotLabels)},
		{"bucket states are lowercase names", "lower", names(bucketStates)},
		{"witness labels are lowercase names", "lower", names(witnesses)},
		{"actors are lowercase names", "lower", names(actors)},
		{"agent trailers are lowercase names", "lower", agentTrailers},
		{"label harnesses are lowercase names", "lower", names(labelHarnesses)},
	}
	for i := range tier1Items {
		it := &tier1Items[i]
		sample := asMap(t, it.sample(t))
		for _, p := range it.places() {
			if p.how != asValue || p.form == "" {
				continue
			}
			v, _ := getPath(t, sample, p.path).(string)
			tests = append(tests, formCase{fmt.Sprintf("%s: the sample's %s has its form", it.design, p.path), p.form, []string{v}})
		}
		for _, m := range it.members {
			if m.typ == jsonMap {
				tests = append(tests, formCase{fmt.Sprintf("%s: %s, a name %s takes, has its form", it.design, m.ok, m.name), m.form, []string{m.ok}})
			}
		}
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if len(tt.values) == 0 {
				t.Fatal("no values, so this checks nothing")
			}
			for _, v := range tt.values {
				if !lenientForms[tt.form].MatchString(v) {
					t.Errorf("%q isn't in the %s form", v, tt.form)
				}
			}
		})
	}
}

// designField is a member of an item in DESIGN 4.3's tier-1 list: its
// name, whether the list writes it as a list ("[]", "[4]") or as a map
// ("{code: s}"), and the members of its objects when the list spells them,
// as in slots[4]{p, cr, why, kind, ev, lbl}.
type designField struct {
	name   string
	list   bool
	isMap  bool
	fields []designField
}

// design43 returns the oracle, DESIGN 4.3's row for the client's tier-1
// uploads: the members of each kind of item its "Exactly what" cell lists,
// by the item's name there, and the items of its "Never in it" cell.
func design43(t *testing.T) (map[string][]designField, []string) {
	t.Helper()
	data, err := os.ReadFile("../../../docs/DESIGN.md")
	if err != nil {
		t.Fatal(err)
	}
	_, sec, ok := strings.Cut(string(data), "\n### 4.3 What crosses the network\n")
	if !ok {
		t.Fatal("docs/DESIGN.md has no section 4.3")
	}
	sec, _, _ = strings.Cut(sec, "\n### ")
	var cells []string
	for _, line := range strings.Split(sec, "\n") {
		if strings.HasPrefix(line, "| Client to `pairtimed`, tier 1 |") {
			cells = strings.Split(strings.Trim(line, "| "), " | ")
		}
	}
	if len(cells) != 4 {
		t.Fatalf("DESIGN 4.3's tier-1 row has %d cells, want 4: from and to, when, exactly what, and never in it", len(cells))
	}
	items := map[string][]designField{}
	for _, it := range tier1Items {
		_, rest, ok := strings.Cut(cells[2], it.design+" `")
		if !ok {
			t.Fatalf("DESIGN 4.3's tier-1 row lists no %s", it.design)
		}
		obj, _, _ := strings.Cut(rest, "`")
		fields, tail, err := parseDesignObject(obj)
		if err != nil || tail != "" {
			t.Fatalf("DESIGN 4.3's %s %q: %v, with %q after it", it.design, obj, err, tail)
		}
		items[it.design] = fields
	}
	return items, strings.Split(cells[3], ", ")
}

// designName reports whether c can be in a member's name in DESIGN 4.3's
// notation.
func designName(c byte) bool {
	return c == '_' || 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9'
}

// parseDesignObject reads one object of DESIGN 4.3's notation at the start
// of s, "{", its members separated by ", ", and "}", and returns what
// follows it. A member is a name, then "[]", "[N]", "[N]{...}" or
// "[{...}]" for a list, or "{name: s}" for a map.
func parseDesignObject(s string) ([]designField, string, error) {
	rest, ok := strings.CutPrefix(s, "{")
	if !ok {
		return nil, s, fmt.Errorf("no { at %q", s)
	}
	var fields []designField
	for {
		n := 0
		for n < len(rest) && designName(rest[n]) {
			n++
		}
		if n == 0 {
			return nil, rest, fmt.Errorf("no name at %q", rest)
		}
		f := designField{name: rest[:n]}
		rest = rest[n:]
		var err error
		switch {
		case strings.HasPrefix(rest, "["):
			f.list = true
			rest = strings.TrimLeft(rest[1:], "0123456789")
			if strings.HasPrefix(rest, "{") {
				if f.fields, rest, err = parseDesignObject(rest); err != nil {
					return nil, rest, err
				}
			}
			if rest, ok = strings.CutPrefix(rest, "]"); !ok {
				return nil, rest, fmt.Errorf("no ] at %q", rest)
			}
			if strings.HasPrefix(rest, "{") {
				if f.fields, rest, err = parseDesignObject(rest); err != nil {
					return nil, rest, err
				}
			}
		case strings.HasPrefix(rest, "{"):
			inner, after, found := strings.Cut(rest[1:], "}")
			if !found || !strings.Contains(inner, ": ") {
				return nil, rest, fmt.Errorf("no map at %q", rest)
			}
			f.isMap, rest = true, after
		}
		fields = append(fields, f)
		if after, found := strings.CutPrefix(rest, ", "); found {
			rest = after
			continue
		}
		if after, found := strings.CutPrefix(rest, "}"); found {
			return fields, after, nil
		}
		return nil, rest, fmt.Errorf("no , or } at %q", rest)
	}
}

// designPaths returns the member paths of DESIGN 4.3's fields, an object's
// members in a list under "name[].", as in slots[].p; the lists whose
// objects it doesn't spell, as rewrites[]; and the maps.
func designPaths(prefix string, fields []designField) (paths, open, mapped []string) {
	for _, f := range fields {
		p := prefix + f.name
		paths = append(paths, p)
		if f.list && f.fields == nil {
			open = append(open, p)
		}
		if f.isMap {
			mapped = append(mapped, p)
		}
		sub, subOpen, subMapped := designPaths(p+"[].", f.fields)
		paths, open, mapped = append(paths, sub...), append(open, subOpen...), append(mapped, subMapped...)
	}
	return paths, open, mapped
}

// memberPaths returns the member paths of members, as designPaths writes
// them, and those of the maps.
func memberPaths(prefix string, members []tier1Member) (paths, mapped []string) {
	for _, m := range members {
		p := prefix + m.name
		paths = append(paths, p)
		if m.typ == jsonMap {
			mapped = append(mapped, p)
		}
		sub, subMapped := memberPaths(p+"[].", m.of)
		paths, mapped = append(paths, sub...), append(mapped, subMapped...)
	}
	return paths, mapped
}

// allowlists are the server's allowlists, required and optional, for each
// kind of item by object: "" for the item itself, and a list's name for
// each of its objects.
func allowlists() map[string]map[string][2][]string {
	return map[string]map[string][2][]string{
		"Batch roots":    {"": {batchRequired, nil}},
		"Minute buckets": {"": {bucketRequired, bucketOptional}, "slots": {slotRequired, slotOptional}},
		"Commit records": {"": {commitRequired, commitOptional}, "rewrites": {rewriteRequired, nil}},
		"Labels":         {"": {labelsRequired, nil}, "harnesses": {harnessRequired, harnessOptional}},
	}
}

// allowedPaths returns the member paths of a kind of item's allowlists, as
// designPaths writes them.
func allowedPaths(objects map[string][2][]string) []string {
	var out []string
	for object, lists := range objects {
		prefix := ""
		if object != "" {
			prefix = object + "[]."
		}
		for _, name := range slices.Concat(lists[0], lists[1]) {
			out = append(out, prefix+name)
		}
	}
	slices.Sort(out)
	return out
}

// The inline objects of an ingest-v1 2.3 row, as rewrites' "objects
// `{"old_sha", "new_sha"}`", and the members such a row calls optional.
var (
	inlineObjects  = regexp.MustCompile("objects `\\{([^}]*)\\}`")
	inlineOptional = regexp.MustCompile("`([a-z_]+)` optional")
)

// markdownTables returns the tables in text, each as its rows of cells,
// without its header row and the line under it.
func markdownTables(text string) [][][]string {
	var tables [][][]string
	var rows [][]string
	flush := func() {
		if len(rows) > 2 {
			tables = append(tables, rows[2:])
		}
		rows = nil
	}
	for _, line := range strings.Split(text, "\n") {
		if !strings.HasPrefix(line, "|") {
			flush()
			continue
		}
		rows = append(rows, strings.Split(strings.Trim(line, "| "), " | "))
	}
	flush()
	return tables
}

// specMembers returns the members ingest-v1 2.3 lists for an object of a
// kind of item, "" for the item itself, and those it marks optional. The
// item's members are the rows of its section's table. A list's objects'
// members are spelled inline in its row, as rewrites' {"old_sha",
// "new_sha"} are, or else in the section's second table, as the slot's
// are.
func specMembers(t *testing.T, it tier1Item, object string) (all, optional []string) {
	t.Helper()
	data, err := os.ReadFile("../../../docs/spec/ingest-v1.md")
	if err != nil {
		t.Fatal(err)
	}
	_, sec, ok := strings.Cut(string(data), "\n#### "+it.spec+" "+it.design+"\n")
	if !ok {
		t.Fatalf("docs/spec/ingest-v1.md has no section %s %s", it.spec, it.design)
	}
	sec, _, _ = strings.Cut(sec, "\n#")
	tables := markdownTables(sec)
	if len(tables) == 0 {
		t.Fatalf("ingest-v1 %s has no table", it.spec)
	}
	fromTable := func(rows [][]string) {
		for _, row := range rows {
			name := strings.Trim(row[0], "`")
			all = append(all, name)
			if len(row) > 2 && strings.HasPrefix(row[2], "Optional") {
				optional = append(optional, name)
			}
		}
	}
	if object == "" {
		fromTable(tables[0])
		return all, optional
	}
	for _, row := range tables[0] {
		if strings.Trim(row[0], "`") != object || len(row) < 3 {
			continue
		}
		if m := inlineObjects.FindStringSubmatch(row[2]); m != nil {
			for _, name := range strings.Split(m[1], ", ") {
				all = append(all, strings.Trim(name, `"`))
			}
			for _, m := range inlineOptional.FindAllStringSubmatch(row[2], -1) {
				optional = append(optional, m[1])
			}
			return all, optional
		}
	}
	if len(tables) < 2 {
		t.Fatalf("ingest-v1 %s spells no members of %s's objects", it.spec, object)
	}
	fromTable(tables[1])
	return all, optional
}

// setDiff returns the strings of want that got lacks, and those of got
// that want lacks, each once.
func setDiff(got, want []string) (missing, extra []string) {
	for _, w := range want {
		if !slices.Contains(got, w) && !slices.Contains(missing, w) {
			missing = append(missing, w)
		}
	}
	for _, g := range got {
		if !slices.Contains(want, g) && !slices.Contains(extra, g) {
			extra = append(extra, g)
		}
	}
	return missing, extra
}

// repeats returns the strings that appear in vs more than once.
func repeats(vs []string) []string {
	var out []string
	for i, v := range vs {
		if slices.Contains(vs[:i], v) && !slices.Contains(out, v) {
			out = append(out, v)
		}
	}
	return out
}

// TestAllowlist diffs the server's allowlist of each kind of tier-1 item
// against the oracle, DESIGN 4.3's tier-1 list, and against ingest-v1 2.3,
// which the client builds to, its optional members included; the bucket's
// and the slot's against TestTier1Shape's lists too, event.Tier1BucketFields
// and event.Tier1SlotFields; and against tier1Items, so the tests here put
// free text in every member the server takes. A new tier-1 field needs
// Karim to add it to DESIGN 4.3 first (PLAN ground rule 10), and this test
// is the backstop: it fails until the field is in DESIGN 4.3, in the
// spec, in the allowlist and here.
func TestAllowlist(t *testing.T) {
	design, _ := design43(t)
	lists := allowlists()
	type setCase struct {
		name      string
		got, want []string
	}
	var tests []setCase
	var unknownForms []string
	for i := range tier1Items {
		it := &tier1Items[i]
		allowed := allowedPaths(lists[it.design])
		listed, open, designMapped := designPaths("", design[it.design])
		// DESIGN 4.3 writes rewrites[] without its objects' members, which
		// ingest-v1 2.3.3 spells, and which the spec's cases below check.
		inDesign := slices.DeleteFunc(slices.Clone(allowed), func(p string) bool {
			return slices.ContainsFunc(open, func(o string) bool { return strings.HasPrefix(p, o+"[].") })
		})
		members, memberMapped := memberPaths("", it.members)
		notMaps, _ := setDiff(memberMapped, designMapped)
		tests = append(tests,
			setCase{it.design + ": the allowlist is DESIGN 4.3's tier-1 list", inDesign, listed},
			setCase{it.design + ": a member DESIGN 4.3 writes as a map, as uncredited{code: s}, is a map here", notMaps, nil},
			setCase{it.design + ": the tests here put free text in every member of the allowlist", members, allowed},
		)
		for _, object := range slices.Sorted(maps.Keys(lists[it.design])) {
			l := lists[it.design][object]
			all, optional := specMembers(t, *it, object)
			where := it.design
			if object != "" {
				where += " " + object
			}
			tests = append(tests,
				setCase{where + ": the allowlist is ingest-v1 " + it.spec + "'s", slices.Concat(l[0], l[1]), all},
				setCase{where + ": the optional members are those ingest-v1 " + it.spec + " calls optional", l[1], optional},
				setCase{where + ": no member is listed twice", repeats(slices.Concat(l[0], l[1])), nil},
			)
		}
		for _, p := range it.places() {
			if p.form != "" && lenientForms[p.form] == nil {
				unknownForms = append(unknownForms, it.design+" "+p.path+": "+p.form)
			}
		}
	}
	tests = append(tests,
		setCase{"Minute buckets: the allowlist is TestTier1Shape's event.Tier1BucketFields", slices.Concat(bucketRequired, bucketOptional), event.Tier1BucketFields()},
		setCase{"Minute buckets slots: the allowlist is TestTier1Shape's event.Tier1SlotFields", slices.Concat(slotRequired, slotOptional), event.Tier1SlotFields()},
		setCase{"every form a member has is one of lenientForms", unknownForms, nil},
	)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if missing, extra := setDiff(tt.got, tt.want); len(missing) > 0 || len(extra) > 0 {
				t.Errorf("lacks %q and has %q beyond it", missing, extra)
			}
		})
	}
}

// TestNeverInIt checks freeText against the oracle, DESIGN 4.3's
// Never-in-it column for tier 1: each item of the column has text of its
// kind in freeText, so the firewall's tests put it in every place of every
// item, and each kind of text there is an item of the column.
func TestNeverInIt(t *testing.T) {
	_, never := design43(t)
	var kinds []string
	for _, s := range freeText {
		if s.never != "" && !slices.Contains(kinds, s.never) {
			kinds = append(kinds, s.never)
		}
	}
	missing, extra := setDiff(kinds, never)
	tests := []struct {
		name string
		got  []string
	}{
		{"every item of DESIGN 4.3's Never-in-it column for tier 1 has free text of its kind", missing},
		{"every kind of free text is an item of the column", extra},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if len(tt.got) != 0 {
				t.Errorf("%q", tt.got)
			}
		})
	}
}

// traces returns the forms text takes in a response or a row: as it is,
// and as JSON writes it in a string. It leaves out any shorter than 4
// bytes, which could turn up by chance.
func traces(text string) []string {
	var out []string
	b, _ := json.Marshal(text)
	for _, s := range []string{text, string(b[1 : len(b)-1])} {
		if len(s) >= 4 && !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	return out
}

// dumpRows returns every value of every row of the tables, as text.
func dumpRows(t *testing.T, db *sql.DB, tables ...string) string {
	t.Helper()
	var b strings.Builder
	for _, table := range tables {
		rows, err := db.Query(`SELECT * FROM ` + table)
		if err != nil {
			t.Fatal(err)
		}
		cols, err := rows.Columns()
		if err != nil {
			t.Fatal(err)
		}
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		for rows.Next() {
			if err := rows.Scan(ptrs...); err != nil {
				t.Fatal(err)
			}
			for _, v := range vals {
				if bs, ok := v.([]byte); ok {
					b.Write(bs)
				} else {
					fmt.Fprint(&b, v)
				}
				b.WriteByte('\n')
			}
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		rows.Close()
	}
	return b.String()
}

// resultsOf returns the results of a response's array of name.
func resultsOf(resp response, name string) []itemResult {
	switch name {
	case "batches":
		out := make([]itemResult, len(resp.Batches))
		for i, r := range resp.Batches {
			out[i] = itemResult{Status: r.Status, Code: r.Code, Field: r.Field}
		}
		return out
	case "buckets":
		return resp.Buckets
	case "commits":
		return resp.Commits
	}
	return nil
}

// refusedAs reports whether r is a 422 with want's code and field.
func refusedAs(r *itemResult, want shapeResult) bool {
	return r != nil && r.Status == http.StatusUnprocessableEntity && r.Code == want.code && r.Field != nil && *r.Field == want.field
}

// resultText writes r for a test's message.
func resultText(r *itemResult) string {
	if r == nil {
		return "null"
	}
	s := fmt.Sprintf("%d %s", r.Status, r.Code)
	if r.Field != nil {
		s += fmt.Sprintf(" field %q", *r.Field)
	}
	return s
}

// TestFirewall sends freeText through POST /api/v1/ingest/batches, in
// every place of every kind of tier-1 item, beside items that pass. Every
// request is processed. The item with the text gets 422 with the code
// that names its place, the items beside it are stored, and neither the
// response nor any row the server keeps holds the text, the stored reply
// and the device's row included (DESIGN 4.5's partial acceptance;
// ingest-v1 2.8). Labels come one to a request, so each request with
// labels that carry text holds a bucket too.
func TestFirewall(t *testing.T) {
	engines(t, func(t *testing.T, engine store.Engine) {
		h := newHarness(t, engine)
		dev := h.device("d_test", false)
		roots := chain(rand.New(rand.NewPCG(5, 6)), ledger.Hash{}, 1, 64)
		minute := exampleClock.Truncate(time.Minute)
		var stored struct{ roots, buckets, commits int }
		// good returns a new item for the body's member name that the
		// server stores.
		good := func(t *testing.T, name string) json.RawMessage {
			t.Helper()
			switch name {
			case "batches":
				x := roots[stored.roots]
				stored.roots++
				return marshal(t, x.json())
			case "buckets":
				minute = minute.Add(-time.Minute)
				stored.buckets++
				return shareableJSON(t, sampleBucket(dev.ID, minute, 0))
			case "commits":
				stored.commits++
				c := sampleCommit()
				c["sha"] = fmt.Sprintf("%040x", stored.commits)
				return marshal(t, c)
			}
			t.Fatalf("no items for %s", name)
			return nil
		}
		// send uploads body and returns its response, which must repeat
		// none of texts.
		send := func(t *testing.T, body map[string]any, texts []string) response {
			t.Helper()
			rec := h.send(t, upload{dev: dev, body: body})
			if rec.Code != http.StatusOK {
				t.Fatalf("status %d, want 200: %s", rec.Code, rec.Body)
			}
			for _, text := range texts {
				for _, tr := range traces(text) {
					if strings.Contains(rec.Body.String(), tr) {
						t.Errorf("the response repeats %q: %s", tr, rec.Body)
					}
				}
			}
			var resp response
			if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
				t.Fatal(err)
			}
			return resp
		}
		h.ok(t, upload{dev: dev, body: map[string]any{"labels": sampleLabels()}})
		var firstLabels string
		if err := h.db.Read.QueryRow(`SELECT labels FROM device_labels WHERE device_id = $1`, dev.ID).Scan(&firstLabels); err != nil {
			t.Fatal(err)
		}
		for i := range tier1Items {
			it := &tier1Items[i]
			for _, p := range it.places() {
				var texts []string
				for _, s := range freeText {
					if !p.fits(s.text) {
						texts = append(texts, s.text)
					}
				}
				want := shapeResult{p.code, p.path}
				t.Run(fmt.Sprintf("%s: free text as %s gets that item alone refused as %s", it.design, p.what(), p.code), func(t *testing.T) {
					if it.body == "labels" {
						for _, text := range texts {
							resp := send(t, map[string]any{"labels": json.RawMessage(p.put(t, text)), "buckets": []any{good(t, "buckets")}}, []string{text})
							if !refusedAs(resp.Labels, want) {
								t.Errorf("%q: the labels got %s, want 422 %+v", text, resultText(resp.Labels), want)
							}
							if len(resp.Buckets) != 1 {
								t.Errorf("%q: %d results for the one bucket beside them", text, len(resp.Buckets))
							} else if r := &resp.Buckets[0]; r.Status != http.StatusOK || r.Code != CodeAccepted {
								t.Errorf("%q: the bucket beside them got %s", text, resultText(r))
							}
						}
						return
					}
					items := []any{good(t, it.body)}
					for _, text := range texts {
						items = append(items, json.RawMessage(p.put(t, text)))
					}
					items = append(items, good(t, it.body))
					results := resultsOf(send(t, map[string]any{it.body: items}, texts), it.body)
					if len(results) != len(items) {
						t.Fatalf("%d results for %d items", len(results), len(items))
					}
					for k := range results {
						r := &results[k]
						switch {
						case k == 0 || k == len(results)-1:
							if r.Status != http.StatusOK || r.Code != CodeAccepted {
								t.Errorf("item %d, which passes, got %s", k, resultText(r))
							}
						case !refusedAs(r, want):
							t.Errorf("item %d, %q: got %s, want 422 %+v", k, texts[k-1], resultText(r), want)
						}
					}
				})
			}
		}
		t.Run("the items beside the free text are stored, the first labels stay, and no row holds the text", func(t *testing.T) {
			for _, c := range []struct {
				table string
				want  int
			}{{"witness_rows", stored.roots}, {"minute_buckets", stored.buckets}, {"commit_records", stored.commits}} {
				var n int
				if err := h.db.Read.QueryRow(`SELECT COUNT(*) FROM `+c.table+` WHERE device_id = $1`, dev.ID).Scan(&n); err != nil {
					t.Fatal(err)
				}
				if n != c.want {
					t.Errorf("%s holds %d rows of the device, want %d", c.table, n, c.want)
				}
			}
			var kept string
			if err := h.db.Read.QueryRow(`SELECT labels FROM device_labels WHERE device_id = $1`, dev.ID).Scan(&kept); err != nil {
				t.Fatal(err)
			}
			if kept != firstLabels {
				t.Errorf("stored labels %s, want the first, %s", kept, firstLabels)
			}
			rows := dumpRows(t, h.db.Read, append(slices.Clone(deviceTables), "request_origins", "devices")...)
			for _, s := range freeText {
				for _, tr := range traces(s.text) {
					if strings.Contains(rows, tr) {
						t.Errorf("a row holds %q", tr)
					}
				}
			}
		})
	})
}
