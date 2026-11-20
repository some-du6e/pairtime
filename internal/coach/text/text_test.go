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

// scolding matches words that would make a view nag, shame or set a target
// (DESIGN 3.8; PA 4.3, 4.4): streak pressure, judgment, goals and ratios
// between you and the agent. It matches whole words in any case, so
// "permission" doesn't trip "miss" nor "close" "lose", and a word right
// after a slash is a command's name, such as Claude Code's /goal (SM 6.4).
var scolding = regexp.MustCompile(`(?i)(?:^|[^\w/])(streaks?|don't break|keep it up|los(?:e|es|ing|t)|miss(?:ed|es|ing)?|behind|lazy|wast(?:e|ed|es|ing)|only counted|should|must|have to|need to|fail(?:ed|s|ing)?|goals?|targets?|ratios?|uncredited|not counted|doesn't count)\b`)

// scolds returns what in s scolds: a scolding word, or "%" or "!", which
// turn time into a ratio or a cheer.
func scolds(s string) []string {
	var out []string
	for _, m := range scolding.FindAllStringSubmatch(s, -1) {
		out = append(out, m[1])
	}
	for _, c := range []string{"%", "!"} {
		if strings.Contains(s, c) {
			out = append(out, c)
		}
	}
	return out
}

func TestNothingScolds(t *testing.T) {
	for _, e := range entries(English()) {
		t.Run(e.where+" doesn't scold, set a target or count a streak", func(t *testing.T) {
			if got := scolds(e.s); len(got) > 0 {
				t.Errorf("%q has %q", e.s, got)
			}
		})
	}
}

func TestScolds(t *testing.T) {
	cases := []struct {
		name string
		s    string
		want []string
	}{
		{"a streak scolds", "Keep your streak", []string{"streak"}},
		{"a word in capitals scolds as well", "You MISSED a day", []string{"MISSED"}},
		{"a target and a ratio scold", "your target ratio", []string{"target", "ratio"}},
		{"a percent sign scolds", "40% of it", []string{"%"}},
		{"an exclamation mark scolds", "Well done!", []string{"!"}},
		{"permission doesn't trip miss", "you said no to a permission Claude asked for", nil},
		{"submission doesn't trip miss", "your submission", nil},
		{"close doesn't trip lose", "close the editor", nil},
		{"duration and calibration don't trip ratio", "the duration while calibration runs", nil},
		{"a slash command named goal is its name, not a goal", "the /goal prompt", nil},
		{"goal as a word scolds", "your goal for today", []string{"goal"}},
		{"wasted scolds as waste does", "wasted time", []string{"wasted"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := scolds(tc.s); !slices.Equal(got, tc.want) {
				t.Errorf("scolds(%q) = %q, want %q", tc.s, got, tc.want)
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
		you, levelC float64
		calibrating bool
		agents      []AgentClock
		want        string
	}{
		{"your time then the agent's, as DESIGN 3.8 writes it", 4320, 0, false, []AgentClock{claude}, "you 1h 12m · Claude 2h 40m"},
		{"no agent time shows your time alone", 265, 0, false, nil, "you 4m 25s"},
		{"an agent with no time is left out", 265, 0, false, []AgentClock{{Name: "Cursor", S: 0.5}}, "you 4m 25s"},
		{"two agents keep their order", 60, 0, false, []AgentClock{claude, {Name: "Codex", S: 300}}, "you 1m · Claude 2h 40m · Codex 5m"},
		{"level-C time is labeled on the clock (D11)", 4320, 1200, false, []AgentClock{claude}, "you 1h 12m, 20m level C · Claude 2h 40m"},
		{"under a second of level C isn't labeled", 4320, 0.5, false, []AgentClock{claude}, "you 1h 12m · Claude 2h 40m"},
		{"calibrating hides your time and keeps the agent's", 4320, 0, true, []AgentClock{claude}, "you calibrating · Claude 2h 40m"},
		{"calibrating hides your level-C time too", 4320, 1200, true, []AgentClock{claude}, "you calibrating · Claude 2h 40m"},
		{"nothing yet is 0m", 0, 0, false, nil, "you 0m"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Clock(tc.you, tc.levelC, tc.calibrating, tc.agents)
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
		{"an unknown kind's code can't reach the terminal raw", Kind(event.Activity("jug\x1b]52;c;x\x07"), "Claude"), "jug\uFFFD]52;c;x\uFFFD"},
		{"agent-only time names the agent", AgentTime("Cursor"), "Cursor, working on its own"},
		{"X.absent is away (D5)", Other(event.ReasonAbsent, "Claude"), "away"},
		{"X.noprog that can no longer come back says what it was", Other(event.ReasonNoProgress, "Claude"), "past two hours without progress, counted at half"},
		{"X.noprog that progress can still bring back is on hold (D5)", OnHold(), "on hold: these come back when you fix it"},
		{"the rewrite loop names the agent", Other(event.ReasonRewriteLoop, "Codex"), "Codex rewriting the same lines"},
		{"X.agent_only isn't other time", Other(event.ReasonAgentOnly, "Claude"), ""},
		{"level C says where it shows, as D-focus-unknown words it (D5, D11)", Level(event.EvidenceC), "this shows on your own dashboard, not on your profile, build days or most programs"},
		{"Printable keeps plain text", Printable("remind-bot {x} café"), "remind-bot {x} café"},
		{"Printable replaces control characters", Printable("a\x1b[2Jb\x07c\nd\te"), "a\uFFFD[2Jb\uFFFDc\uFFFDd\uFFFDe"},
		{"Printable replaces bidirectional formatting", Printable("abc\u202Edcba\u2066x"), "abc\uFFFDdcba\uFFFDx"},
		{"Printable replaces bytes that aren't UTF-8", Printable("a\xffb"), "a\uFFFDb"},
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
