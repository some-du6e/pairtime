// SPDX-License-Identifier: AGPL-3.0-or-later

// Package text holds the words pairtime uses to show a teen their time.
// The coach (internal/coach), the CLI views (`pairtime today`, `recap` and
// `statusline`) and the local dashboard share them, so a minute reads the
// same everywhere it shows (DESIGN 3.8).
//
// The words live in en.json, which this package embeds and which the
// dashboard can import as it is. They follow DESIGN 3.8 and
// approach-teen.md D5:
//
//   - credited time is "your time";
//   - agent-only time is named after the agent that ran ("Claude, working
//     on its own"), and so are X.agent_only and X.automated, which are the
//     agent's time;
//   - other uncredited time is described for what it was ("away", "in
//     other apps"), and the no-progress half as "on hold: these come back
//     when you fix it" while progress can still bring it back, its minutes
//     still pending or provisional (SM 4.3, 5.1), and as what it was once
//     it can't;
//   - level-C time is labeled as such, on the chess clock too (DESIGN 6.2
//     D11, as D-focus-unknown amends it);
//   - nothing scolds, sets a target, turns you and the agent into a ratio,
//     or counts a streak (PA 4.3, 4.4).
//
// An entry may hold placeholders in braces, such as {agent} and {time},
// which Fill replaces in one pass. In TypeScript the same thing is
// s.replace(/\{(\w+)\}/g, (m, k) => vars[k] ?? m).
package text

import (
	"bytes"
	"cmp"
	_ "embed"
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/some-du6e/pairtime/pkg/event"
)

//go:embed en.json
var enJSON []byte

// Catalog is en.json: every word the views show about time.
type Catalog struct {
	Comment string `json:"$comment"`
	// Agents names the agent of each harness, as the chess clock does.
	Agents map[event.Harness]string `json:"agents"`
	// Kinds describes credited time of each activity kind (SM 5.1). An
	// entry may use {agent}.
	Kinds map[event.Activity]string `json:"kinds"`
	// AgentTime describes agent-only time. It uses {agent}.
	AgentTime string `json:"agent_time"`
	// Other describes uncredited time of each reason that isn't the
	// agent's time (SM 5.2; IsAgentTime). An entry may use {agent}.
	Other map[event.Reason]string `json:"other"`
	// OnHold describes the part of X.noprog that progress can still bring
	// back (SM 4.3; approach-teen.md D5), where Other's X.noprog entry
	// describes the rest.
	OnHold string `json:"on_hold"`
	// Levels describes each evidence level (SM 5.1, DESIGN 6.4).
	Levels map[event.Evidence]string `json:"levels"`
	// Phrases holds the views' other words, by Key.
	Phrases map[Key]string `json:"phrases"`
}

// Parse decodes a catalog and refuses a field it doesn't know.
func Parse(data []byte) (*Catalog, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var c Catalog
	if err := dec.Decode(&c); err != nil {
		return nil, err
	}
	return &c, nil
}

var english = sync.OnceValue(func() *Catalog {
	c, err := Parse(enJSON)
	if err != nil {
		// The tests parse en.json, so a build with a broken one never ships.
		panic("text: en.json: " + err.Error())
	}
	return c
})

// English returns the catalog in en.json.
func English() *Catalog { return english() }

// Key names a phrase.
type Key string

// The phrases, in en.json's order.
const (
	KeyYou             Key = "you"
	KeyYouLevelC       Key = "you_level_c"
	KeyYouCalibrating  Key = "you_calibrating"
	KeyAgent           Key = "agent"
	KeySeparator       Key = "separator"
	KeyYourTime        Key = "your_time"
	KeyCalibrating     Key = "calibrating"
	KeyCalibratingNote Key = "calibrating_note"
	KeyLevelCNote      Key = "level_c_note"
	KeyPendingNote     Key = "pending_note"
	KeyNothingToday    Key = "nothing_today"
	KeyNothingOn       Key = "nothing_on"
	KeySessionLast     Key = "session_last"
	KeySessionOpen     Key = "session_open"
	KeySessionSpan     Key = "session_span"
	KeySessionSince    Key = "session_since"
	KeyNoSession       Key = "no_session"
	KeyLevelsHead      Key = "levels_head"
	KeyLevelLine       Key = "level_line"
	KeyAgentDown       Key = "agent_down"
	KeyAgentOld        Key = "agent_old"
	KeyAgentSlow       Key = "agent_slow"
	KeyAgentError      Key = "agent_error"
	KeyStoreError      Key = "store_error"
	// KeyFocusUnknown and KeyFocusUnknownLevelC are DESIGN 5.9's
	// teen-facing focus-unknown sentence, which pairtime today and the
	// dashboard's Today page show while the running agent says focus is
	// unknown: the first stops at "in front", and the second, for once the
	// agent says that time counts at level C (WP-SC8), goes on to where it
	// counts. On GNOME, KeyGnomeNeedsLogin or KeyGnomeUnticked follows,
	// for why pairtime's GNOME extension doesn't report focus.
	KeyFocusUnknown       Key = "focus_unknown"
	KeyFocusUnknownLevelC Key = "focus_unknown_level_c"
	KeyGnomeNeedsLogin    Key = "gnome_needs_login"
	KeyGnomeUnticked      Key = "gnome_unticked"
)

// placeholders lists the placeholders each phrase may use. A phrase that
// isn't listed uses none.
var placeholders = map[Key][]string{
	KeyYou:          {"time"},
	KeyYouLevelC:    {"time", "level_c"},
	KeyAgent:        {"agent", "time"},
	KeyLevelCNote:   {"time"},
	KeyPendingNote:  {"time"},
	KeySessionLast:  {"project"},
	KeySessionOpen:  {"project"},
	KeySessionSpan:  {"day", "start", "end"},
	KeySessionSince: {"day", "start"},
	KeyLevelLine:    {"level", "words"},
	KeyAgentError:   {"error"},
	KeyStoreError:   {"error"},
}

// Phrase returns phrase key with its placeholders filled from vars, which
// are pairs of name and value: Phrase(KeyYou, "time", "1h 12m").
func Phrase(key Key, vars ...string) string {
	return Fill(english().Phrases[key], vars...)
}

// Fill replaces each {name} in s with the value vars gives for name, in
// one pass, so a value that holds braces, such as a project's name, stays
// as it is. vars are pairs of name and value. A placeholder vars has no
// value for stays in place.
func Fill(s string, vars ...string) string {
	if len(vars) < 2 || !strings.Contains(s, "{") {
		return s
	}
	pairs := make([]string, 0, len(vars)/2*2)
	for i := 0; i+1 < len(vars); i += 2 {
		pairs = append(pairs, "{"+vars[i]+"}", vars[i+1])
	}
	return strings.NewReplacer(pairs...).Replace(s)
}

// Agent returns the name the views give the agent of harness h, such as
// "Claude" for claude_code. A harness en.json doesn't name is "your
// agent".
func Agent(h event.Harness) string {
	if name, ok := english().Agents[h]; ok {
		return name
	}
	return english().Agents[event.HarnessOther]
}

// Kind describes credited time of activity a, which went with the agent
// named agent: "reading Claude's answers". A kind en.json doesn't describe
// is its code, made Printable.
func Kind(a event.Activity, agent string) string {
	s, ok := english().Kinds[a]
	if !ok {
		return Printable(string(a))
	}
	return Fill(s, "agent", agent)
}

// AgentTime describes the agent-only time of the agent named agent:
// "Claude, working on its own".
func AgentTime(agent string) string {
	return Fill(english().AgentTime, "agent", agent)
}

// IsAgentTime reports whether uncredited seconds with reason r are the
// agent's time, which the views name after the agent rather than list as
// other time (D5): an agent turn ran and nothing credited the second
// (X.agent_only), or nobody started the turn (X.automated). Both are
// agent-only time (SM 5.3).
func IsAgentTime(r event.Reason) bool {
	return r == event.ReasonAgentOnly || r == event.ReasonAutomated
}

// Other describes uncredited time with reason r, which went with the agent
// named agent: "away", "Claude rewriting the same lines". It returns ""
// for the agent's time and for a reason that isn't an uncredited one.
func Other(r event.Reason, agent string) string {
	s, ok := english().Other[r]
	if !ok {
		return ""
	}
	return Fill(s, "agent", agent)
}

// OnHold describes the X.noprog seconds that progress can still bring back:
// "on hold: these come back when you fix it" (D5).
func OnHold() string { return english().OnHold }

// Level describes evidence level e, or returns "" for a level that isn't
// one.
func Level(e event.Evidence) string { return english().Levels[e] }

// Printable returns s with each control character and each bidirectional
// formatting character replaced by U+FFFD, and so are bytes that aren't
// UTF-8. Text a view prints that didn't come from this catalog, such as a
// project's name, goes through it, so it can't move a terminal's cursor,
// write its clipboard, ring its bell or turn the rest of a line around.
func Printable(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.Is(unicode.Bidi_Control, r) {
			return utf8.RuneError
		}
		return r
	}, s)
}

// maxSeconds bounds what Duration spells, far past any real total, so a
// damaged number can't overflow it.
const maxSeconds = 1e12

// Duration spells s seconds as every view shows time: "45s", "4m 25s",
// "12m", "1h 12m", "2h", and "0m" for less than a second. It rounds down,
// to the second below an hour and to the minute from an hour up, so it
// never shows more time than was counted.
func Duration(s float64) string {
	if math.IsNaN(s) || s < 1 {
		return "0m"
	}
	n := int64(min(s, maxSeconds))
	h, m, sec := n/3600, n/60%60, n%60
	switch {
	case h > 0 && m > 0:
		return fmt.Sprintf("%dh %dm", h, m)
	case h > 0:
		return fmt.Sprintf("%dh", h)
	case m > 0 && sec > 0:
		return fmt.Sprintf("%dm %ds", m, sec)
	case m > 0:
		return fmt.Sprintf("%dm", m)
	default:
		return fmt.Sprintf("%ds", sec)
	}
}

// AgentClock is one agent's side of the chess clock: its name and its
// agent-only seconds.
type AgentClock struct {
	Name string
	S    float64
}

// Clock is the chess clock (DESIGN 3.8): "you 1h 12m · Claude 2h 40m",
// your time and then each agent's time on its own, as information and
// never as a ratio or a target. levelC is the part of your time at
// evidence level C, which the clock labels from a second up: "you 1h 12m,
// 20m level C · Claude 2h 40m" (DESIGN 6.2 D11). With calibrating set, your
// time reads "calibrating" (display.shadow), with no level, and the
// agents' times still show. An agent with less than a second is left out;
// the rest keep their order.
func Clock(you, levelC float64, calibrating bool, agents []AgentClock) string {
	parts := make([]string, 0, 1+len(agents))
	switch {
	case calibrating:
		parts = append(parts, Phrase(KeyYouCalibrating))
	case levelC >= 1:
		parts = append(parts, Phrase(KeyYouLevelC, "time", Duration(you), "level_c", Duration(levelC)))
	default:
		parts = append(parts, Phrase(KeyYou, "time", Duration(you)))
	}
	for _, a := range agents {
		if a.S >= 1 {
			parts = append(parts, Phrase(KeyAgent, "agent", a.Name, "time", Duration(a.S)))
		}
	}
	return strings.Join(parts, Phrase(KeySeparator))
}

// Sentence returns s with its first letter in upper case, for words that
// start a line: "Away", "Your agent, working on its own".
func Sentence(s string) string {
	r, n := utf8.DecodeRuneInString(s)
	if n == 0 || r == utf8.RuneError {
		return s
	}
	return string(unicode.ToUpper(r)) + s[n:]
}

// Day spells the local date of t as the views head a day: "Tuesday,
// November 24".
func Day(t time.Time) string { return t.Format("Monday, January 2") }

// Clocktime spells the local time of t as the views give a session's
// start and end: "07:40".
func Clocktime(t time.Time) string { return t.Format("15:04") }

// Percentages splits 100 among parts in proportion to their sizes, in
// whole numbers that add up to exactly 100: each part gets its share
// rounded down, and what is left goes one at a time to the parts that lost
// the most to rounding, the earlier part first on a tie. Parts that aren't
// positive get 0, and with nothing positive every part is 0. The views
// show evidence-level shares this way, and the dashboard rounds the same
// way so both show the same numbers.
func Percentages(parts []float64) []int {
	// In whole milliseconds, so the arithmetic is exact: each part's
	// remainder is below total, so what is left after rounding down is
	// less than the number of positive parts, and each gets at most one.
	out := make([]int, len(parts))
	units := make([]int64, len(parts))
	var total int64
	for i, p := range parts {
		if p > 0 {
			units[i] = int64(math.Round(min(p, maxSeconds) * 1000))
			total += units[i]
		}
	}
	if total == 0 {
		return out
	}
	type rest struct {
		i   int
		rem int64
	}
	var rests []rest
	left := 100
	for i, u := range units {
		if u == 0 {
			continue
		}
		out[i] = int(u * 100 / total)
		left -= out[i]
		rests = append(rests, rest{i, u * 100 % total})
	}
	slices.SortStableFunc(rests, func(a, b rest) int { return cmp.Compare(b.rem, a.rem) })
	for k := 0; k < left; k++ {
		out[rests[k].i]++
	}
	return out
}
