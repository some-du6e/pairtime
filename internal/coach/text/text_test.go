// SPDX-License-Identifier: AGPL-3.0-or-later

package text

import (
	"maps"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/pkg/event"
)

// The enum values the catalog must cover, as pkg/event declares them. A
// value added there and not here fails nothing, so these lists name every
// constant pkg/event has today, in its order.
var (
	allHarnesses = []event.Harness{
		event.HarnessClaudeCode, event.HarnessCursor, event.HarnessCopilot, event.HarnessCodex,
		event.HarnessGemini, event.HarnessDevin, event.HarnessCline, event.HarnessOpenCode, event.HarnessOther,
	}
	allActivities = []event.Activity{
		event.ActivityPrompting, event.ActivityReadingOutput, event.ActivitySupervising, event.ActivityReviewing,
		event.ActivityTesting, event.ActivityDebugging, event.ActivityHandEdit, event.ActivityLookup,
	}
	allUncredited = []event.Reason{
		event.ReasonAbsent, event.ReasonOtherFocus, event.ReasonAmbiguous, event.ReasonNoAnchor,
		event.ReasonAgentOnly, event.ReasonAutomated, event.ReasonNoInput, event.ReasonReroll,
		event.ReasonRewriteLoop, event.ReasonSameError, event.ReasonAutopilot, event.ReasonNoProgress,
		event.ReasonLookupCap, event.ReasonBucketCap, event.ReasonDayCap,
	}
	allCreditReasons = []event.Reason{
		event.ReasonPrompt, event.ReasonAnswer, event.ReasonGoAhead, event.ReasonQueued,
		event.ReasonPermission, event.ReasonInterrupt, event.ReasonEdit, event.ReasonNavigate,
		event.ReasonCommand, event.ReasonTest, event.ReasonCommit, event.ReasonSupervise,
		event.ReasonRead, event.ReasonLookup,
	}
	allLevels = []event.Evidence{event.EvidenceA, event.EvidenceB, event.EvidenceC}
	allKeys   = []Key{
		KeyYou, KeyYouCalibrating, KeyAgent, KeySeparator, KeyYourTime, KeyCalibrating,
		KeyCalibratingNote, KeyLevelCNote, KeyPendingNote, KeyNothingToday, KeyNothingOn,
		KeySessionLast, KeySessionOpen, KeySessionSpan, KeySessionSince, KeyNoSession,
		KeyLevelsHead, KeyLevelLine, KeyAgentDown, KeyAgentOld, KeyAgentSlow, KeyAgentError,
	}
)

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

// scolding lists words that would make a view nag, shame or set a target
// (DESIGN 3.8; PA 4.3, 4.4): streak pressure, judgment, goals and ratios
// between you and the agent.
var scolding = []string{
	"streak", "don't break", "keep it up", "lose", "lost", "losing", "miss", "behind",
	"lazy", "wast", "only counted", "should", "must", "have to", "need to", "failed",
	"goal", "target", "ratio", "%", "!", "uncredited", "not counted", "doesn't count",
}

func TestNothingScolds(t *testing.T) {
	for _, e := range entries(English()) {
		t.Run(e.where+" doesn't scold, set a target or count a streak", func(t *testing.T) {
			low := strings.ToLower(e.s)
			for _, w := range scolding {
				if strings.Contains(low, w) {
					t.Errorf("%q contains %q", e.s, w)
				}
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
	cases := []struct {
		name string
		s    float64
		want string
	}{
		{"nothing is 0m", 0, "0m"},
		{"less than a second is 0m", 0.5, "0m"},
		{"a negative number is 0m", -30, "0m"},
		{"seconds alone below a minute", 45, "45s"},
		{"a half second rounds down", 45.5, "45s"},
		{"minutes and seconds below an hour", 265, "4m 25s"},
		{"whole minutes drop the seconds", 720, "12m"},
		{"hours and minutes from an hour up, seconds dropped", 4339, "1h 12m"},
		{"whole hours drop the minutes", 7200, "2h"},
		{"an hour and a few seconds is the hour", 3605, "1h"},
		{"59 minutes 59 seconds stays below the hour", 3599, "59m 59s"},
		{"SM 6.4's agent-only night", 23180, "6h 26m"},
		{"a huge number doesn't overflow", 1e300, "277777777h 46m"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Duration(tc.s); got != tc.want {
				t.Errorf("Duration(%v) = %q, want %q", tc.s, got, tc.want)
			}
		})
	}
}

func TestClock(t *testing.T) {
	claude := AgentClock{Name: "Claude", S: 9600}
	cases := []struct {
		name        string
		you         float64
		calibrating bool
		agents      []AgentClock
		want        string
	}{
		{"your time then the agent's, as DESIGN 3.8 writes it", 4320, false, []AgentClock{claude}, "you 1h 12m · Claude 2h 40m"},
		{"no agent time shows your time alone", 265, false, nil, "you 4m 25s"},
		{"an agent with no time is left out", 265, false, []AgentClock{{Name: "Cursor", S: 0.5}}, "you 4m 25s"},
		{"two agents keep their order", 60, false, []AgentClock{claude, {Name: "Codex", S: 300}}, "you 1m · Claude 2h 40m · Codex 5m"},
		{"calibrating hides your time and keeps the agent's", 4320, true, []AgentClock{claude}, "you calibrating · Claude 2h 40m"},
		{"nothing yet is 0m", 0, false, nil, "you 0m"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Clock(tc.you, tc.calibrating, tc.agents)
			if got != tc.want {
				t.Errorf("Clock = %q, want %q", got, tc.want)
			}
			if strings.ContainsAny(got, "%/") {
				t.Errorf("Clock = %q shows a ratio", got)
			}
		})
	}
}

func TestWords(t *testing.T) {
	cases := []struct {
		name string
		got  string
		want string
	}{
		{"claude_code's agent is Claude", Agent(event.HarnessClaudeCode), "Claude"},
		{"a harness en.json doesn't know is your agent", Agent(event.Harness("zed")), "your agent"},
		{"reading names the agent", Kind(event.ActivityReadingOutput, "Claude"), "reading Claude's answers"},
		{"an unknown kind shows its code", Kind(event.Activity("juggling"), "Claude"), "juggling"},
		{"agent-only time names the agent", AgentTime("Cursor"), "Cursor, working on its own"},
		{"X.absent is away (D5)", Other(event.ReasonAbsent, "Claude"), "away"},
		{"X.noprog is on hold (D5)", Other(event.ReasonNoProgress, "Claude"), "on hold: these come back when you fix it"},
		{"the rewrite loop names the agent", Other(event.ReasonRewriteLoop, "Codex"), "Codex rewriting the same lines"},
		{"X.agent_only isn't other time", Other(event.ReasonAgentOnly, "Claude"), ""},
		{"level C says where it counts", Level(event.EvidenceC), "this counts only on your own dashboard"},
		{"a phrase fills its placeholders", Phrase(KeySessionSpan, "day", "Tuesday, November 24", "start", "07:40", "end", "07:45"), "Tuesday, November 24, 07:40 to 07:45"},
		{"a value with braces isn't filled again", Phrase(KeySessionLast, "project", "{time}"), "Your last session: {time}"},
		{"a placeholder with no value stays", Fill("you {time}"), "you {time}"},
		{"Sentence capitalizes the first letter", Sentence("your agent, working on its own"), "Your agent, working on its own"},
		{"Sentence leaves an empty string", Sentence(""), ""},
		{"Day heads a day", Day(time.Date(2026, 11, 24, 7, 40, 0, 0, time.UTC)), "Tuesday, November 24"},
		{"Clocktime is 24-hour with a leading zero", Clocktime(time.Date(2026, 11, 24, 7, 4, 0, 0, time.UTC)), "07:04"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.got != tc.want {
				t.Errorf("got %q, want %q", tc.got, tc.want)
			}
		})
	}
}

func TestPercentages(t *testing.T) {
	cases := []struct {
		name  string
		parts []float64
		want  []int
	}{
		{"one part is 100", []float64{265}, []int{100}},
		{"thirds give the extra point to the first", []float64{1, 1, 1}, []int{34, 33, 33}},
		{"the largest remainder gets the extra point", []float64{1, 2, 3.5}, []int{15, 31, 54}},
		{"a zero part stays 0 and the rest add up to 100", []float64{0, 3, 1}, []int{0, 75, 25}},
		{"a tiny part can round to 0", []float64{1, 999}, []int{0, 100}},
		{"nothing positive is all zeros", []float64{0, -1}, []int{0, 0}},
		{"no parts is no percentages", nil, []int{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Percentages(tc.parts)
			if !slices.Equal(got, tc.want) {
				t.Errorf("Percentages(%v) = %v, want %v", tc.parts, got, tc.want)
			}
			sum := 0
			for _, p := range got {
				sum += p
			}
			if sum != 100 && sum != 0 {
				t.Errorf("Percentages(%v) adds up to %d", tc.parts, sum)
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
