// SPDX-License-Identifier: AGPL-3.0-or-later

package text

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/pkg/event"
)

// The enum values the catalog must cover, from pkg/event's own lists, so a
// value added there fails TestCatalogCoversEveryValue until en.json names
// it. cmd/pairtime's TestTodaySchemaMatchesGo then holds
// cli-today.schema.json's enums to en.json.
var (
	allHarnesses     = slices.DeleteFunc(event.Harnesses(), func(h event.Harness) bool { return h == event.HarnessNone })
	allActivities    = event.Activities()
	allUncredited    = event.UncreditedReasons()
	allCreditReasons = event.CreditReasons()
	allLevels        = []event.Evidence{event.EvidenceA, event.EvidenceB, event.EvidenceC}
	allKeys          = []Key{
		KeyYou, KeyYouLevelC, KeyYouCalibrating, KeyAgent, KeySeparator, KeyYourTime, KeyCalibrating,
		KeyCalibratingNote, KeyLevelCNote, KeyPendingNote, KeyNothingToday, KeyNothingOn,
		KeySessionLast, KeySessionOpen, KeySessionSpan, KeySessionSince, KeyNoSession,
		KeyLevelsHead, KeyLevelLine, KeyAgentDown, KeyAgentOld, KeyAgentSlow, KeyAgentError, KeyStoreError,
		KeyFocusUnknown, KeyFocusUnknownLevelC, KeyGnomeNeedsLogin, KeyGnomeUnticked,
	}
)

// casesJSON holds the test cases this package shares with its TypeScript
// port, web/src/local/text.ts. text.test.ts and copy.test.ts read the same
// file, so a case changed here fails there too until the port follows.
// Cases JSON can't hold, such as bytes that aren't UTF-8, stay in this file.
//
//go:embed testdata/cases.json
var casesJSON []byte

// caseFile is testdata/cases.json.
type caseFile struct {
	Comment  string `json:"$comment"`
	Duration []struct {
		Name string  `json:"name"`
		S    float64 `json:"s"`
		Want string  `json:"want"`
	} `json:"duration"`
	Clock []struct {
		Name        string  `json:"name"`
		You         float64 `json:"you"`
		LevelC      float64 `json:"level_c"`
		Calibrating bool    `json:"calibrating"`
		Agents      []struct {
			Name string  `json:"name"`
			S    float64 `json:"s"`
		} `json:"agents"`
		Want string `json:"want"`
	} `json:"clock"`
	Percentages []struct {
		Name  string    `json:"name"`
		Parts []float64 `json:"parts"`
		Want  []int     `json:"want"`
	} `json:"percentages"`
	// Each words case calls Func, a function of this package, with Args
	// (wordFuncs).
	Words []struct {
		Name string   `json:"name"`
		Func string   `json:"func"`
		Args []string `json:"args"`
		Want string   `json:"want"`
	} `json:"words"`
	Scolding struct {
		Alternatives []string `json:"alternatives"`
		Marks        []string `json:"marks"`
		Cases        []struct {
			Name string   `json:"name"`
			S    string   `json:"s"`
			Want []string `json:"want"`
		} `json:"cases"`
	} `json:"scolding"`

	// scolding matches words that would make a view nag, shame or set a
	// target (DESIGN 3.8; PA 4.3, 4.4): streak pressure, judgment, goals and
	// ratios between you and the agent, which Scolding.Alternatives lists.
	// It matches whole words in any case, so "permission" doesn't trip
	// "miss" nor "close" "lose", and a word right after a slash is a
	// command's name, such as Claude Code's /goal (SM 6.4). copy.test.ts
	// builds the dashboard's matcher from the same list the same way.
	scolding *regexp.Regexp
}

// loadCases decodes testdata/cases.json once. It refuses a field it doesn't
// know, so a misspelled one can't leave a case's value at zero.
var loadCases = sync.OnceValues(func() (*caseFile, error) {
	dec := json.NewDecoder(bytes.NewReader(casesJSON))
	dec.DisallowUnknownFields()
	var c caseFile
	if err := dec.Decode(&c); err != nil {
		return nil, err
	}
	re, err := regexp.Compile(`(?i)(?:^|[^\w/])(` + strings.Join(c.Scolding.Alternatives, "|") + `)\b`)
	if err != nil {
		return nil, err
	}
	c.scolding = re
	return &c, nil
})

// shared returns testdata/cases.json, or stops t.
func shared(t *testing.T) *caseFile {
	t.Helper()
	c, err := loadCases()
	if err != nil {
		t.Fatalf("testdata/cases.json: %v", err)
	}
	return c
}

func TestSharedCases(t *testing.T) {
	c := shared(t)
	cases := []struct {
		name string
		n    int
	}{
		{"the duration table isn't empty", len(c.Duration)},
		{"the clock table isn't empty", len(c.Clock)},
		{"the percentages table isn't empty", len(c.Percentages)},
		{"the words table isn't empty", len(c.Words)},
		{"the scolding list has words", len(c.Scolding.Alternatives)},
		{"the scolding list has marks", len(c.Scolding.Marks)},
		{"the scolding table isn't empty", len(c.Scolding.Cases)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.n == 0 {
				t.Error("testdata/cases.json has none, so the tests that read it would pass with nothing checked")
			}
		})
	}
}

// entry is one string of the catalog, with the placeholders it may use.
type entry struct {
	where   string
	s       string
	allowed []string
}

func entries(c *Catalog) []entry {
	var out []entry
	for _, h := range slices.Sorted(maps.Keys(c.Agents)) {
		out = append(out, entry{"agents." + string(h), c.Agents[h], nil})
	}
	for _, a := range slices.Sorted(maps.Keys(c.Kinds)) {
		out = append(out, entry{"kinds." + string(a), c.Kinds[a], []string{"agent"}})
	}
	out = append(out, entry{"agent_time", c.AgentTime, []string{"agent"}})
	for _, r := range slices.Sorted(maps.Keys(c.Other)) {
		out = append(out, entry{"other." + string(r), c.Other[r], []string{"agent"}})
	}
	out = append(out, entry{"on_hold", c.OnHold, nil})
	for _, l := range slices.Sorted(maps.Keys(c.Levels)) {
		out = append(out, entry{"levels." + string(l), c.Levels[l], nil})
	}
	for _, k := range slices.Sorted(maps.Keys(c.Phrases)) {
		out = append(out, entry{"phrases." + string(k), c.Phrases[k], placeholders[k]})
	}
	return out
}

func TestCatalogCoversEveryValue(t *testing.T) {
	c := English()
	kindsOfReasons := map[event.Activity]bool{}
	for _, r := range allCreditReasons {
		kindsOfReasons[r.Activity()] = true
	}
	cases := []struct {
		name string
		got  []string
		want []string
	}{
		{"every harness but none names its agent", keys(c.Agents), names(allHarnesses)},
		{"every activity kind is described, and they are the kinds of SM 5.2's credit reasons", keys(c.Kinds), names(slices.Collect(maps.Keys(kindsOfReasons)))},
		{"every activity kind pkg/event declares is described", keys(c.Kinds), names(allActivities)},
		{"every uncredited reason but the agent's time is described as other time", keys(c.Other), names(slices.DeleteFunc(slices.Clone(allUncredited), IsAgentTime))},
		{"every evidence level is described", keys(c.Levels), names(allLevels)},
		{"every phrase has a Key and every Key a phrase", keys(c.Phrases), names(allKeys)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			slices.Sort(tc.got)
			slices.Sort(tc.want)
			if !slices.Equal(tc.got, tc.want) {
				t.Errorf("got %q\nwant %q", tc.got, tc.want)
			}
		})
	}
}

func TestEveryUncreditedReasonIsAgentTimeOrOther(t *testing.T) {
	for _, r := range allUncredited {
		t.Run(string(r)+" is described exactly once, as the agent's time or as other time", func(t *testing.T) {
			if !r.Uncredited() {
				t.Fatalf("%s isn't an uncredited reason in pkg/event", r)
			}
			other := Other(r, "Claude") != ""
			if other == IsAgentTime(r) {
				t.Errorf("IsAgentTime = %v and Other gives words = %v; want exactly one", IsAgentTime(r), other)
			}
		})
	}
	t.Run("a credit reason is neither", func(t *testing.T) {
		if Other(event.ReasonRead, "Claude") != "" || IsAgentTime(event.ReasonRead) {
			t.Error("READ is described as uncredited time")
		}
	})
}

var placeholder = regexp.MustCompile(`\{([^{}]*)\}`)

func TestPlaceholders(t *testing.T) {
	for _, e := range entries(English()) {
		t.Run(e.where+" uses only its own placeholders, each closed", func(t *testing.T) {
			if e.s == "" {
				t.Fatal("is empty")
			}
			for _, m := range placeholder.FindAllStringSubmatch(e.s, -1) {
				if !slices.Contains(e.allowed, m[1]) {
					t.Errorf("uses {%s}; may use %q", m[1], e.allowed)
				}
			}
			if rest := placeholder.ReplaceAllString(e.s, ""); strings.ContainsAny(rest, "{}") {
				t.Errorf("has a brace outside a placeholder: %q", e.s)
			}
		})
	}
}

// scolds returns what in s scolds: a scolding word, or a mark, "%" or "!",
// which turn time into a ratio or a cheer.
func (c *caseFile) scolds(s string) []string {
	var out []string
	for _, m := range c.scolding.FindAllStringSubmatch(s, -1) {
		out = append(out, m[1])
	}
	for _, mark := range c.Scolding.Marks {
		if strings.Contains(s, mark) {
			out = append(out, mark)
		}
	}
	return out
}

func TestNothingScolds(t *testing.T) {
	c := shared(t)
	for _, e := range entries(English()) {
		t.Run(e.where+" doesn't scold, set a target or count a streak", func(t *testing.T) {
			if got := c.scolds(e.s); len(got) > 0 {
				t.Errorf("%q has %q", e.s, got)
			}
		})
	}
}

func TestScolds(t *testing.T) {
	c := shared(t)
	for _, tc := range c.Scolding.Cases {
		t.Run(tc.Name, func(t *testing.T) {
			if got := c.scolds(tc.S); !slices.Equal(got, tc.Want) {
				t.Errorf("scolds(%q) = %q, want %q", tc.S, got, tc.Want)
			}
		})
	}
}

// TestFocusWordsAreTheDashboards holds DESIGN 5.9's focus-unknown words,
// which pairtime today takes from en.json, to the dashboard's Today page,
// whose words are its own literals in web/src/local/copy.ts (focusWords):
// each phrase must stand there word for word, so the two can't drift.
func TestFocusWordsAreTheDashboards(t *testing.T) {
	copyTS, err := os.ReadFile(filepath.Join("..", "..", "..", "web", "src", "local", "copy.ts"))
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []Key{KeyFocusUnknown, KeyFocusUnknownLevelC, KeyGnomeNeedsLogin, KeyGnomeUnticked} {
		t.Run("phrase "+string(k)+" is the Today page's, word for word", func(t *testing.T) {
			if p := Phrase(k); !strings.Contains(string(copyTS), `"`+p+`"`) {
				t.Errorf("web/src/local/copy.ts has no %q", p)
			}
		})
	}
}

func TestEnglishParses(t *testing.T) {
	cases := []struct {
		name    string
		data    string
		wantErr bool
	}{
		{"en.json parses", string(enJSON), false},
		{"an unknown section is refused", `{"agents": {}, "hints": {}}`, true},
		{"a section of the wrong type is refused", `{"agents": []}`, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse([]byte(tc.data))
			if (err != nil) != tc.wantErr {
				t.Errorf("err = %v, want an error: %v", err, tc.wantErr)
			}
		})
	}
}

func TestDuration(t *testing.T) {
	for _, tc := range shared(t).Duration {
		t.Run(tc.Name, func(t *testing.T) {
			if got := Duration(tc.S); got != tc.Want {
				t.Errorf("Duration(%v) = %q, want %q", tc.S, got, tc.Want)
			}
		})
	}
}

func TestClock(t *testing.T) {
	for _, tc := range shared(t).Clock {
		t.Run(tc.Name, func(t *testing.T) {
			agents := make([]AgentClock, len(tc.Agents))
			for i, a := range tc.Agents {
				agents[i] = AgentClock{Name: a.Name, S: a.S}
			}
			got := Clock(tc.You, tc.LevelC, tc.Calibrating, agents)
			if got != tc.Want {
				t.Errorf("Clock = %q, want %q", got, tc.Want)
			}
			if strings.ContainsAny(got, "%/") {
				t.Errorf("Clock = %q shows a ratio", got)
			}
		})
	}
}

// oneThenPairs is wordFuncs' argument count for a function that takes one
// argument and then pairs of name and value, as Phrase and Fill do.
const oneThenPairs = -1

// wordFuncs calls the function a words case in testdata/cases.json names,
// with the case's arguments, which are strings there: Day takes a
// YYYY-MM-DD date and Clocktime an RFC 3339 time. text.test.ts calls
// text.ts's port of each the same way.
var wordFuncs = map[string]struct {
	args int
	call func(a []string) (string, error)
}{
	"Agent":     {1, func(a []string) (string, error) { return Agent(event.Harness(a[0])), nil }},
	"Kind":      {2, func(a []string) (string, error) { return Kind(event.Activity(a[0]), a[1]), nil }},
	"AgentTime": {1, func(a []string) (string, error) { return AgentTime(a[0]), nil }},
	"Other":     {2, func(a []string) (string, error) { return Other(event.Reason(a[0]), a[1]), nil }},
	"OnHold":    {0, func([]string) (string, error) { return OnHold(), nil }},
	"Level":     {1, func(a []string) (string, error) { return Level(event.Evidence(a[0])), nil }},
	"Printable": {1, func(a []string) (string, error) { return Printable(a[0]), nil }},
	"Phrase":    {oneThenPairs, func(a []string) (string, error) { return Phrase(Key(a[0]), a[1:]...), nil }},
	"Fill":      {oneThenPairs, func(a []string) (string, error) { return Fill(a[0], a[1:]...), nil }},
	"Sentence":  {1, func(a []string) (string, error) { return Sentence(a[0]), nil }},
	"Day": {1, func(a []string) (string, error) {
		d, err := time.Parse(time.DateOnly, a[0])
		return Day(d), err
	}},
	"Clocktime": {1, func(a []string) (string, error) {
		at, err := time.Parse(time.RFC3339, a[0])
		return Clocktime(at), err
	}},
}

func TestWords(t *testing.T) {
	for _, tc := range shared(t).Words {
		t.Run(tc.Name, func(t *testing.T) {
			f, ok := wordFuncs[tc.Func]
			if !ok {
				t.Fatalf("this package has no function %s for the case", tc.Func)
			}
			fits := len(tc.Args) == f.args
			if f.args == oneThenPairs {
				fits = len(tc.Args)%2 == 1
			}
			if !fits {
				t.Fatalf("%s doesn't take the arguments %q", tc.Func, tc.Args)
			}
			got, err := f.call(tc.Args)
			if err != nil {
				t.Fatalf("%s(%q): %v", tc.Func, tc.Args, err)
			}
			if got != tc.Want {
				t.Errorf("%s(%q) = %q, want %q", tc.Func, tc.Args, got, tc.Want)
			}
		})
	}
	// What JSON can't hold, so text.test.ts can't run it.
	goOnly := []struct {
		name string
		got  string
		want string
	}{
		{"Printable replaces bytes that aren't UTF-8", Printable("a\xffb"), "a\uFFFDb"},
	}
	for _, tc := range goOnly {
		t.Run(tc.name, func(t *testing.T) {
			if tc.got != tc.want {
				t.Errorf("got %q, want %q", tc.got, tc.want)
			}
		})
	}
}

func TestPercentages(t *testing.T) {
	for _, tc := range shared(t).Percentages {
		t.Run(tc.Name, func(t *testing.T) {
			got := Percentages(tc.Parts)
			if !slices.Equal(got, tc.Want) {
				t.Errorf("Percentages(%v) = %v, want %v", tc.Parts, got, tc.Want)
			}
			sum := 0
			for _, p := range got {
				sum += p
			}
			if sum != 100 && sum != 0 {
				t.Errorf("Percentages(%v) adds up to %d", tc.Parts, sum)
			}
		})
	}
}

func keys[K ~string, V any](m map[K]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, string(k))
	}
	return out
}

func names[T ~string](list []T) []string {
	out := make([]string, len(list))
	for i, v := range list {
		out[i] = string(v)
	}
	return out
}
