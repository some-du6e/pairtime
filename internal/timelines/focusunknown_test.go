// SPDX-License-Identifier: AGPL-3.0-or-later

package timelines

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/some-du6e/pairtime/pkg/event"
)

// The focus-unknown cases of WP-FX8 (docs/PLAN.md) are timelines under
// testdata/timelines/focus-unknown, one directory per case: SM 2.5's rule
// for desktops that don't say which window is in front, applied by hand.
// List doesn't descend into focus-unknown, so the tests in
// timelines_test.go don't see these cases. The tests below check them the
// same way, check what SM 2.5 lets a slot of unknown focus credit and at
// what level, check each case's slots.json, and check that each case's
// README works out the same rows and totals as its expected.json.
// testdata/timelines/focus-unknown/README.md describes the cases.

const (
	focusUnknownDir = "focus-unknown"
	// focusSlotsFile holds each slot's credit and level. Only the
	// focus-unknown cases have one.
	focusSlotsFile = "slots.json"
)

// focusUnknownCase is one of WP-FX8's cases and what its events hold.
type focusUnknownCase struct {
	name string
	// unknownRecords and knownRecords count its os.focus records with
	// app_class unknown and with any other class.
	unknownRecords, knownRecords int
	// allUnknown says that every slot of every bucket minute has unknown
	// focus. Otherwise unknownSlots lists the local starts of those that do.
	allUnknown   bool
	unknownSlots []string
}

// focusUnknownCases are the cases WP-FX8 asks for, (a) to (e), with (b)'s
// X11 twin, in List's order.
var focusUnknownCases = []focusUnknownCase{
	{name: "a-6.1-gnome", unknownRecords: 1, allUnknown: true},
	{name: "b-pty-game", unknownRecords: 1, allUnknown: true},
	{name: "b-pty-game-x11", knownRecords: 2},
	{name: "c-x11-reconnect", unknownRecords: 1, knownRecords: 3, unknownSlots: []string{"10:02:15", "10:02:30"}},
	{name: "d-two-projects", unknownRecords: 1, allUnknown: true},
	{name: "e-hour-before", unknownRecords: 1, allUnknown: true},
}

// focusTestedAnchors are the anchor kinds whose evidence test names focus
// (SM 2.2 rule 6 for prompts, SM 2.3 for the rest): the only ones that can
// pass it only because focus was unknown, and so carry focus_unknown.
var focusTestedAnchors = []event.AnchorKind{
	event.AnchorSubstantivePrompt, event.AnchorAnswer, event.AnchorGoAhead, event.AnchorQueuedMessage,
	event.AnchorTypedEdit, event.AnchorEditorNavigation,
}

// focusSlots is slots.json: every slot of every bucket minute of a case.
type focusSlots struct {
	Fixture string      `json:"fixture"`
	Slots   []focusSlot `json:"slots"`
}

// focusSlot is one slot: the window SM 2.5 gives it, the one project its
// credit goes to (D-split-slots), its credit by project and reason, its
// credited seconds by level, and the level and labels a minute bucket's
// slot gives it (SM 5.1): its weakest second's level, and focus_unknown
// when some of its seconds are level C because focus was unknown.
type focusSlot struct {
	Start     time.Time                           `json:"start"`
	Local     string                              `json:"local"`
	Focus     event.AppClass                      `json:"focus"`
	P         string                              `json:"p"`
	CreditedS float64                             `json:"credited_s"`
	Credit    map[string]map[event.Reason]float64 `json:"credit"`
	Levels    map[event.Evidence]float64          `json:"levels"`
	Ev        event.Evidence                      `json:"ev"`
	Lbl       []event.Label                       `json:"lbl"`
}

type focusUnknownFixture struct {
	*Fixture
	c     focusUnknownCase
	slots focusSlots
}

func loadFocusUnknown(t *testing.T) []focusUnknownFixture {
	t.Helper()
	root, err := Root()
	if err != nil {
		t.Fatal(err)
	}
	var out []focusUnknownFixture
	for _, c := range focusUnknownCases {
		f, err := Load(root, focusUnknownDir+"/"+c.name)
		if err != nil {
			t.Fatal(err)
		}
		var s focusSlots
		if err := readJSON(filepath.Join(f.Dir, focusSlotsFile), &s); err != nil {
			t.Fatalf("timelines: %s/%s: %v", f.Name, focusSlotsFile, err)
		}
		out = append(out, focusUnknownFixture{Fixture: f, c: c, slots: s})
	}
	return out
}

// TestFocusUnknownCasesPresent pins the cases WP-FX8 asks for, so one can't
// vanish from the scorer's oracle without a test failing.
func TestFocusUnknownCasesPresent(t *testing.T) {
	root, err := Root()
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, c := range focusUnknownCases {
		names = append(names, c.name)
	}
	t.Run("every case directory under focus-unknown is one of WP-FX8's cases", func(t *testing.T) {
		dirs, err := List(filepath.Join(root, focusUnknownDir))
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(dirs, names) {
			t.Errorf("focus-unknown holds %q, the cases are %q", dirs, names)
		}
	})
	for _, c := range names {
		t.Run(c+" has every file of a focus-unknown case", func(t *testing.T) {
			for _, name := range []string{EventsFile, ExpectedFile, AnchorsFile, PromptsFile, focusSlotsFile, "README.md", "gen.py"} {
				if _, err := os.Stat(filepath.Join(root, focusUnknownDir, c, name)); err != nil {
					t.Error(err)
				}
			}
		})
	}
}

// TestFocusUnknownEvents checks each case's events as TestEventsValidate,
// TestEventsMatchSchema, TestEventsRoundTrip and TestEventStream check the
// SM fixtures', and the focus and presence records each case is about.
func TestFocusUnknownEvents(t *testing.T) {
	sv := loadEventSchema(t)
	for _, f := range loadFocusUnknown(t) {
		t.Run(f.Name+" every event passes Event.Validate and the JSON Schema", func(t *testing.T) {
			for i, e := range f.Events {
				if err := e.Validate(); err != nil {
					t.Errorf("%s line %d (seq %d, %s): %v", EventsFile, i+1, e.Seq, e.Kind(), err)
				}
				v, err := decodeJSON(f.Lines[i])
				if err != nil {
					t.Fatalf("%s line %d: %v", EventsFile, i+1, err)
				}
				if errs := sv.validate(v); len(errs) > 0 {
					t.Errorf("%s line %d: %s", EventsFile, i+1, strings.Join(errs, "; "))
				}
			}
		})
		t.Run(f.Name+" every line is pkg/event's own encoding", func(t *testing.T) {
			for i, e := range f.Events {
				b, err := json.Marshal(e)
				if err != nil {
					t.Fatalf("%s line %d: %v", EventsFile, i+1, err)
				}
				if !bytes.Equal(b, f.Lines[i]) {
					t.Errorf("%s line %d re-encodes differently:\n got %s\nwant %s", EventsFile, i+1, b, f.Lines[i])
				}
			}
		})
		t.Run(f.Name+" events are one device's gapless stream in time order", func(t *testing.T) {
			ids := map[string]bool{}
			for i, e := range f.Events {
				where := fmt.Sprintf("%s line %d", EventsFile, i+1)
				if e.DeviceID != f.Expected.Device {
					t.Errorf("%s: device_id %q, want the case's device %q", where, e.DeviceID, f.Expected.Device)
				}
				if i > 0 {
					prev := f.Events[i-1]
					if e.Seq != prev.Seq+1 {
						t.Errorf("%s: seq %d follows %d; a fixture's ledger holds only events, so seq rises by 1", where, e.Seq, prev.Seq)
					}
					if e.T < prev.T {
						t.Errorf("%s: t %d is before the previous event's %d", where, e.T, prev.T)
					}
					if e.BootID != prev.BootID || e.Mono < prev.Mono {
						t.Errorf("%s: each case is one boot, and mono never goes back", where)
					}
				}
				if ids[e.EventID] {
					t.Errorf("%s: event_id %s repeats", where, e.EventID)
				}
				ids[e.EventID] = true
				if got := uuidTime(e.EventID); got != e.T {
					t.Errorf("%s: event_id carries time %d, but t is %d", where, got, e.T)
				}
			}
		})
		t.Run(f.Name+" every sample is a Linux machine's that can't tell keys from other input", func(t *testing.T) {
			if f.Expected.OS != "linux" || f.Expected.InputKeys {
				t.Errorf("os %q and input_keys %v; every case is a Linux desktop without a key counter",
					f.Expected.OS, f.Expected.InputKeys)
			}
			for i, e := range f.Events {
				if p, ok := e.Data.(*event.OSPresence); ok {
					if p.IdleKeysS != nil {
						t.Errorf("%s line %d: idle_keys_s is %v; Mutter and X11 without the key counter report none (SM 1.2)",
							EventsFile, i+1, *p.IdleKeysS)
					}
					if p.Locked {
						t.Errorf("%s line %d: a locked sample; no case locks the screen", EventsFile, i+1)
					}
				}
			}
		})
		t.Run(f.Name+" has its os.focus records, the first in force before the window", func(t *testing.T) {
			var unknown, known int
			first := int64(-1)
			for i, e := range f.Events {
				p, ok := e.Data.(*event.OSFocus)
				if !ok {
					continue
				}
				if first < 0 {
					first = e.T
				}
				if p.AppClass != event.AppUnknown {
					known++
					continue
				}
				unknown++
				if p.AppHash != "" || len(p.HostedSessions) > 0 || p.FocusProject != "" {
					t.Errorf("%s line %d: an os.focus of unknown focus has no app_hash, hosted_sessions or focus_project (SM 1.2)",
						EventsFile, i+1)
				}
			}
			if unknown != f.c.unknownRecords || known != f.c.knownRecords {
				t.Errorf("%d os.focus records of unknown focus and %d of known focus, want %d and %d",
					unknown, known, f.c.unknownRecords, f.c.knownRecords)
			}
			if first < 0 || first >= f.Expected.Window.Start.UnixMilli() {
				t.Errorf("the first os.focus is at %d; one must be in force when the window starts at %d",
					first, f.Expected.Window.Start.UnixMilli())
			}
		})
	}
}

// TestFocusUnknownRows checks the rows and totals as TestRows and TestTotals
// do for the SM fixtures, and SM 2.5's rules for what a row of unknown
// focus may hold.
func TestFocusUnknownRows(t *testing.T) {
	root, err := Root()
	if err != nil {
		t.Fatal(err)
	}
	errata := filepath.Join(root, "..", "..", "docs", "errata")
	for _, f := range loadFocusUnknown(t) {
		exp := f.Expected
		t.Run(f.Name+" is witnessed, at level B or C, with no SM total", func(t *testing.T) {
			if exp.Fixture != f.Name {
				t.Errorf("%s names fixture %q", ExpectedFile, exp.Fixture)
			}
			if exp.Witness != event.Witnessed {
				t.Errorf("witness %q; these are live desktops whose minutes reached the server", exp.Witness)
			}
			if exp.Evidence != event.EvidenceB && exp.Evidence != event.EvidenceC {
				t.Errorf("evidence %q; without a key counter credit is B where focus is known and C where it isn't", exp.Evidence)
			}
			if f.c.allUnknown && exp.Evidence != event.EvidenceC {
				t.Errorf("evidence %q; every slot's focus is unknown, so every credited second is level C (SM 2.5)", exp.Evidence)
			}
			if len(exp.Stated) != 0 {
				t.Errorf("stated %q; SM states no focus-unknown totals, so every total is a sum or a count", exp.Stated)
			}
		})
		t.Run(f.Name+" rows tile the window and account for every second", func(t *testing.T) {
			if len(exp.Rows) == 0 {
				t.Fatal("no rows")
			}
			if !exp.Rows[0].Start.Equal(exp.Window.Start) {
				t.Errorf("rows start at %s, the window at %s", exp.Rows[0].Start, exp.Window.Start)
			}
			if end := exp.Rows[len(exp.Rows)-1].End(); !end.Equal(exp.Window.End) {
				t.Errorf("rows end at %s, the window at %s", end, exp.Window.End)
			}
			for i, r := range exp.Rows {
				where := fmt.Sprintf("row %d (%s)", i, r.Local)
				if r.Minutes < 1 {
					t.Errorf("%s: minutes = %d", where, r.Minutes)
				}
				if r.Start.Location() != time.UTC || !r.Start.Truncate(time.Minute).Equal(r.Start) {
					t.Errorf("%s: start %s is not a whole UTC minute", where, r.Start)
				}
				if i > 0 && !exp.Rows[i-1].End().Equal(r.Start) {
					t.Errorf("%s: starts at %s, but the row before ends at %s", where, r.Start, exp.Rows[i-1].End())
				}
				if r.Erratum != "" && !slices.Contains(exp.Errata, r.Erratum) {
					t.Errorf("%s: erratum %q isn't in the case's errata", where, r.Erratum)
				}
				if !r.Bucket {
					if r.BucketMinutes != 0 || r.CreditedS != 0 || len(r.Credit) > 0 || r.Uncredited != nil || r.AgentRunningS != 0 || len(r.Codes) > 0 {
						t.Errorf("%s: a minute without a bucket has no credit, no uncredited seconds and no agent time", where)
					}
					continue
				}
				if r.Minutes != 1 || r.BucketMinutes != 1 {
					t.Errorf("%s: %d minutes and %d bucket minutes; every bucket row here is one minute", where, r.Minutes, r.BucketMinutes)
				}
				sum := 0.0
				for p, reasons := range r.Credit {
					if _, ok := exp.Projects[p]; !ok {
						t.Errorf("%s: project %q isn't in the case's projects", where, p)
					}
					for k, s := range reasons {
						if !k.Credited() || s <= 0 {
							t.Errorf("%s: credit %s with %v s", where, k, s)
						}
						if f.c.allUnknown && k == event.ReasonLookup {
							t.Errorf("%s: LOOKUP, but a slot of unknown focus is never a browser slot (SM 2.5)", where)
						}
						sum += s
					}
				}
				if sum != r.CreditedS {
					t.Errorf("%s: reasons sum to %v, credited_s is %v", where, sum, r.CreditedS)
				}
				if r.AgentRunningS < 0 || r.AgentRunningS > 60 {
					t.Errorf("%s: agent_running_s %v outside 0 to 60", where, r.AgentRunningS)
				}
				if r.Uncredited == nil || len(r.Codes) > 0 {
					t.Errorf("%s: every bucket row here gives its uncredited seconds, and lists no codes without them", where)
					continue
				}
				unc := 0.0
				for k, s := range r.Uncredited {
					if !k.Uncredited() || s <= 0 {
						t.Errorf("%s: uncredited %s with %v s", where, k, s)
					}
					if f.c.allUnknown && (k == event.ReasonOtherFocus || k == event.ReasonAmbiguous) {
						t.Errorf("%s: %s, which SM 5.2 skips where focus is unknown", where, k)
					}
					unc += s
				}
				if r.CreditedS+unc != 60 {
					t.Errorf("%s: %v credited + %v uncredited = %v, want 60", where, r.CreditedS, unc, r.CreditedS+unc)
				}
			}
		})
		t.Run(f.Name+" totals are the sums of the rows", func(t *testing.T) {
			var credited, agent float64
			byProject := map[string]float64{}
			byReason := map[event.Reason]float64{}
			unc := map[event.Reason]float64{}
			for _, r := range exp.Rows {
				credited += r.CreditedS
				agent += r.AgentRunningS
				for p, reasons := range r.Credit {
					for k, s := range reasons {
						byProject[p] += s
						byReason[k] += s
					}
				}
				for k, s := range r.Uncredited {
					unc[k] += s
				}
			}
			tot := exp.Totals
			if tot.CreditedS != credited {
				t.Errorf("credited_s = %v, rows sum to %v", tot.CreditedS, credited)
			}
			if tot.AgentRunningS != agent {
				t.Errorf("agent_running_s = %v, rows sum to %v", tot.AgentRunningS, agent)
			}
			if !maps.Equal(nonZero(tot.CreditedByProject), byProject) {
				t.Errorf("credited_by_project = %v, rows sum to %v", tot.CreditedByProject, byProject)
			}
			if !maps.Equal(nonZero(tot.CreditedByReason), byReason) {
				t.Errorf("credited_by_reason = %v, rows sum to %v", tot.CreditedByReason, byReason)
			}
			if !maps.Equal(nonZero(tot.Uncredited), unc) {
				t.Errorf("uncredited = %v, rows sum to %v", tot.Uncredited, unc)
			}
			// SM 5.3's agent_only_s counts the seconds a turn ran that nothing
			// credited, so each bucket row gives at least its running seconds
			// less its credited ones.
			floor := 0.0
			for _, r := range exp.Rows {
				if r.Bucket {
					floor += max(0, r.AgentRunningS-r.CreditedS)
				}
			}
			a, ok := tot.Get("agent_only_s")
			if !ok || a > tot.AgentRunningS || a < floor || a < unc[event.ReasonAgentOnly] {
				t.Errorf("agent_only_s %v (present: %v) must be at least %v, the rows' running seconds less their credit, and "+
					"the rows' X.agent_only %v, and at most agent_running_s %v", a, ok, floor, unc[event.ReasonAgentOnly], tot.AgentRunningS)
			}
			// SM 5.4's split. No case credits a hand reason inside an agent
			// turn, so hand_credited_s is the rows' E, NAV, CMD, TEST, G and
			// LOOKUP.
			hand := 0.0
			for _, k := range []event.Reason{event.ReasonEdit, event.ReasonNavigate, event.ReasonCommand, event.ReasonTest,
				event.ReasonCommit, event.ReasonLookup} {
				hand += byReason[k]
			}
			if v, ok := tot.Get("hand_credited_s"); !ok || v != hand {
				t.Errorf("hand_credited_s %v (present: %v), the rows' hand reasons sum to %v", v, ok, hand)
			}
			if v, ok := tot.Get("ai_credited_s"); !ok || v != tot.CreditedS-hand {
				t.Errorf("ai_credited_s %v (present: %v), credited_s less the hand reasons is %v", v, ok, tot.CreditedS-hand)
			}
			origins := map[event.Origin]float64{}
			for _, tr := range f.Anchors.Turns {
				origins[tr.Origin]++
			}
			for k, o := range map[string]event.Origin{"human_typed_prompts": event.OriginHumanTyped, "no_input_prompts": event.OriginNoInput} {
				if v, ok := tot.Get(k); ok && v != origins[o] {
					t.Errorf("%s %v, but %s's turns have %v of origin %s", k, v, AnchorsFile, origins[o], o)
				}
			}
			levels := map[string]float64{}
			for _, k := range []string{"level_a_s", "level_b_s", "level_c_s", "focus_unknown_s"} {
				v, ok := tot.Get(k)
				if !ok {
					t.Errorf("no %s total", k)
				}
				levels[k] = v
			}
			if s := levels["level_a_s"] + levels["level_b_s"] + levels["level_c_s"]; s != tot.CreditedS {
				t.Errorf("level_a_s + level_b_s + level_c_s = %v, credited_s %v", s, tot.CreditedS)
			}
			if levels["level_a_s"] != 0 {
				t.Errorf("level_a_s %v; no machine here has the key counter, so nothing is level A", levels["level_a_s"])
			}
		})
		t.Run(f.Name+" every erratum named exists", func(t *testing.T) {
			for _, name := range exp.Errata {
				if _, err := os.Stat(filepath.Join(errata, name+".md")); err != nil {
					t.Errorf("erratum %q: %v", name, err)
				}
			}
		})
	}
}

// TestFocusUnknownSlots checks slots.json against the rows and totals, and
// against SM 2.5: every second credited in a slot of unknown focus is level
// C with focus_unknown, a slot claims its weakest second's level, and focus
// is unknown in exactly the slots each case is about.
func TestFocusUnknownSlots(t *testing.T) {
	for _, f := range loadFocusUnknown(t) {
		exp := f.Expected
		t.Run(f.Name+" slots.json lists every slot of every bucket minute, in order", func(t *testing.T) {
			if f.slots.Fixture != f.Name {
				t.Errorf("%s names fixture %q", focusSlotsFile, f.slots.Fixture)
			}
			var want []time.Time
			for _, r := range exp.Rows {
				if !r.Bucket {
					continue
				}
				for i := range 4 {
					want = append(want, r.Start.Add(time.Duration(i)*15*time.Second))
				}
			}
			var got []time.Time
			for _, s := range f.slots.Slots {
				got = append(got, s.Start)
			}
			if !slices.EqualFunc(got, want, func(a, b time.Time) bool { return a.Equal(b) }) {
				t.Errorf("slots start at %v, the bucket minutes' slots at %v", got, want)
			}
		})
		t.Run(f.Name+" each slot's credit, levels, ev and lbl agree", func(t *testing.T) {
			for _, s := range f.slots.Slots {
				where := "slot " + s.Local
				if s.Focus == "" {
					t.Errorf("%s: no focus", where)
				}
				sum := 0.0
				for p, reasons := range s.Credit {
					if _, ok := exp.Projects[p]; !ok {
						t.Errorf("%s: project %q isn't in the case's projects", where, p)
					}
					for k, v := range reasons {
						if !k.Credited() || v <= 0 {
							t.Errorf("%s: credit %s with %v s", where, k, v)
						}
						sum += v
					}
				}
				lsum := 0.0
				weakest := event.Evidence("")
				for l, v := range s.Levels {
					if !slices.Contains(event.Evidences(), l) || v <= 0 {
						t.Errorf("%s: level %q with %v s", where, l, v)
					}
					lsum += v
					weakest = max(weakest, l)
				}
				if sum != s.CreditedS || lsum != s.CreditedS || s.CreditedS > 15 {
					t.Errorf("%s: credit sums to %v and levels to %v, credited_s %v, at most 15", where, sum, lsum, s.CreditedS)
				}
				if s.CreditedS == 0 {
					if s.Ev != "" || len(s.Lbl) > 0 {
						t.Errorf("%s: a slot with no credit has no level and no labels, got %q and %q", where, s.Ev, s.Lbl)
					}
					continue
				}
				if s.Ev != weakest {
					t.Errorf("%s: ev %q, but its weakest second is level %q (DESIGN 6.4)", where, s.Ev, weakest)
				}
				if len(s.Credit) != 1 || s.Credit[s.P] == nil {
					t.Errorf("%s: credit %v and p %q; a slot credits one project, its p (D-split-slots)", where, s.Credit, s.P)
				}
			}
		})
		t.Run(f.Name+" each slot's focus comes from the os.focus records, and its labels and levels from that focus", func(t *testing.T) {
			// No anchor in these cases passed its focus test only through
			// unknown focus while it sat in a slot of known focus, so a
			// second is level C with focus_unknown exactly when its slot's
			// focus is unknown (SM 2.5).
			var unknownS float64
			for _, s := range f.slots.Slots {
				where := "slot " + s.Local
				focus := slotFocusFromEvents(f.Events, s.Start.UnixMilli())
				if focus != s.Focus {
					t.Errorf("%s: focus %q, but the os.focus records give the slot %q (SM 2.5)", where, s.Focus, focus)
				}
				if s.CreditedS == 0 {
					continue
				}
				labeled := slices.Equal(s.Lbl, []event.Label{event.LabelFocusUnknown})
				switch {
				case focus == event.AppUnknown && (!labeled || s.Levels[event.EvidenceC] != s.CreditedS):
					t.Errorf("%s: labels %q and levels %v; every second credited in a slot of unknown focus is level C with focus_unknown",
						where, s.Lbl, s.Levels)
				case focus != event.AppUnknown && (len(s.Lbl) > 0 || s.Levels[event.EvidenceC] > 0):
					t.Errorf("%s: labels %q and levels %v; a slot of known focus here has no level-C second", where, s.Lbl, s.Levels)
				}
				if focus == event.AppUnknown {
					unknownS += s.CreditedS
				}
			}
			if got, _ := exp.Totals.Get("focus_unknown_s"); got != unknownS {
				t.Errorf("focus_unknown_s %v, but the slots of unknown focus credit %v", got, unknownS)
			}
		})
		t.Run(f.Name+" the slots of each bucket minute add up to its row", func(t *testing.T) {
			sums := map[int64]map[string]map[event.Reason]float64{}
			for _, s := range f.slots.Slots {
				m := s.Start.Truncate(time.Minute).UnixMilli()
				if sums[m] == nil {
					sums[m] = map[string]map[event.Reason]float64{}
				}
				for p, reasons := range s.Credit {
					if sums[m][p] == nil {
						sums[m][p] = map[event.Reason]float64{}
					}
					for k, v := range reasons {
						sums[m][p][k] += v
					}
				}
			}
			for _, r := range exp.Rows {
				if r.Bucket && !lookbackCreditEqual(sums[r.Start.UnixMilli()], r.Credit) {
					t.Errorf("row %s credits %v, its slots %v", r.Local, r.Credit, sums[r.Start.UnixMilli()])
				}
			}
		})
		t.Run(f.Name+" the slots' seconds by level are the level totals", func(t *testing.T) {
			got := map[event.Evidence]float64{}
			for _, s := range f.slots.Slots {
				for l, v := range s.Levels {
					got[l] += v
				}
			}
			for l, k := range map[event.Evidence]string{event.EvidenceA: "level_a_s", event.EvidenceB: "level_b_s", event.EvidenceC: "level_c_s"} {
				if want, _ := exp.Totals.Get(k); got[l] != want {
					t.Errorf("the slots hold %v s at level %s, %s is %v", got[l], l, k, want)
				}
			}
		})
		t.Run(f.Name+" focus is unknown in exactly the slots the case is about", func(t *testing.T) {
			var unknown []string
			for _, s := range f.slots.Slots {
				if s.Focus == event.AppUnknown {
					unknown = append(unknown, s.Local)
				}
			}
			switch {
			case f.c.allUnknown && len(unknown) != len(f.slots.Slots):
				t.Errorf("%d of %d slots have unknown focus; every slot should", len(unknown), len(f.slots.Slots))
			case !f.c.allUnknown && !slices.Equal(unknown, f.c.unknownSlots):
				t.Errorf("the slots of unknown focus are %q, want %q", unknown, f.c.unknownSlots)
			}
		})
	}
}

// TestFocusUnknownAnchors checks anchors.json against the events, as
// TestAnchors does, and against SM 2.5: an anchor whose evidence test names
// focus and passed only because focus was unknown carries focus_unknown and
// is level C, and every other anchor is level B on these machines.
func TestFocusUnknownAnchors(t *testing.T) {
	for _, f := range loadFocusUnknown(t) {
		t.Run(f.Name+" every anchor is an event of its kind, with its labels, level and lead", func(t *testing.T) {
			if f.Anchors.Fixture != f.Name {
				t.Errorf("%s names fixture %q", AnchorsFile, f.Anchors.Fixture)
			}
			for i, a := range f.Anchors.Anchors {
				where := fmt.Sprintf("anchor %d (%s %s)", i, a.Kind, a.Local)
				e, ok := f.EventByID(a.EventID)
				if !ok {
					t.Errorf("%s: no event %s", where, a.EventID)
					continue
				}
				if want := anchorEventKinds[a.Kind]; e.Kind() != want {
					t.Errorf("%s: event %s is %s, want %s", where, a.EventID, e.Kind(), want)
				}
				if e.T != a.T || e.ProjectKey != a.ProjectKey || e.Via != a.Via {
					t.Errorf("%s: t, project_key and via are %d, %q, %q; the event has %d, %q, %q",
						where, a.T, a.ProjectKey, a.Via, e.T, e.ProjectKey, e.Via)
				}
				ea := event.Anchor{AnchorID: a.EventID, Kind: a.Kind, T: a.T, ProjectKey: a.ProjectKey,
					LeadS: a.LeadS, TrailS: a.TrailS, Via: a.Via, Labels: a.Labels}
				if err := ea.Validate(); err != nil {
					t.Errorf("%s: %v", where, err)
				}
				if a.Labels == nil {
					t.Errorf("%s: no labels; every anchor here lists them, empty or not", where)
				}
				unknown := slices.Contains(a.Labels, event.LabelFocusUnknown)
				if slices.Contains(focusTestedAnchors, a.Kind) {
					// The os.focus in force at the act decides: unknown focus
					// passes the test only that way (SM 2.5).
					switch rec := focusInForce(f.Events, a.EventID); {
					case rec == nil:
						t.Errorf("%s: no os.focus is in force at the act", where)
					case (rec.AppClass == event.AppUnknown) != unknown:
						t.Errorf("%s: the os.focus in force is %q, and focus_unknown is %v; it goes with unknown focus at the act",
							where, rec.AppClass, unknown)
					}
				}
				switch {
				case unknown && !slices.Contains(focusTestedAnchors, a.Kind):
					t.Errorf("%s: focus_unknown, but a %s's evidence test names no focus", where, a.Kind)
				case unknown && a.Level != event.EvidenceC:
					t.Errorf("%s: level %q; an anchor that passed its focus test only on unknown focus is level C (SM 2.5)", where, a.Level)
				case !unknown && a.Level != event.EvidenceB:
					t.Errorf("%s: level %q; without a key counter an anchor that passed its tests on known focus, or has no focus test, is level B (DESIGN 6.4)",
						where, a.Level)
				}
				if a.LeadFrom != "params" {
					t.Errorf("%s: lead_from %q; SM states no focus-unknown lead, so each comes from SM 2.3 and SM 8", where, a.LeadFrom)
				}
				if i > 0 && a.T < f.Anchors.Anchors[i-1].T {
					t.Errorf("%s: anchors are listed in time order", where)
				}
				var lead, trail float64
				switch p := e.Data.(type) {
				case *event.PromptSubmit:
					if a.Kind != event.AnchorSubstantivePrompt || a.Origin != event.OriginHumanTyped || a.PromptClass != event.ClassSubstantive {
						t.Errorf("%s: kind %q, origin %q and class %q; every prompt anchor here is a typed substantive prompt",
							where, a.Kind, a.Origin, a.PromptClass)
					}
					// THINK_LEAD_S plus the typing part, rounded halves up
					// (SM 1.3), which math.Round does for these lengths.
					lead = 120 + math.Round(math.Min(float64(p.TypedLenEst)/1.5, 300))
				case *event.EditorActivity:
					lead = 120 // EDIT_LEAD_S
				case *event.AgentPermission:
					lead, trail = math.Min(float64(p.AnsweredAt-p.RequestedAt)/1000, 120), 10 // PA_MAX_S, PA_TRAIL_S
				case *event.GitChange:
					lead = 60 // COMMIT_LEAD_S
				default:
					t.Errorf("%s: a %s anchor, which no case has", where, e.Kind())
					continue
				}
				if a.LeadS != lead || a.TrailS != trail {
					t.Errorf("%s: lead %v and trail %v, SM 2.3 and SM 8 give %v and %v", where, a.LeadS, a.TrailS, lead, trail)
				}
				if _, isPrompt := promptAnchorClasses[a.Kind]; !isPrompt && (a.Origin != "" || a.PromptClass != "") {
					t.Errorf("%s: only prompt anchors have an origin and a class", where)
				}
			}
		})
		t.Run(f.Name+" every prompt that starts a person's turn is an anchor, and no other", func(t *testing.T) {
			anchored := map[string]bool{}
			for _, a := range f.Anchors.Anchors {
				anchored[a.EventID] = true
			}
			human := map[string]bool{}
			for _, tr := range f.Anchors.Turns {
				human[tr.TurnKey] = tr.Origin.Human()
			}
			for i, e := range f.Events {
				p, ok := e.Data.(*event.PromptSubmit)
				if !ok {
					continue
				}
				h, known := human[p.TurnKey]
				if !known {
					t.Errorf("%s line %d: a prompt whose turn isn't in %s", EventsFile, i+1, AnchorsFile)
				} else if h != anchored[e.EventID] {
					t.Errorf("%s line %d: a prompt of a turn with a human origin %v, and an anchor %v", EventsFile, i+1, h, anchored[e.EventID])
				}
			}
		})
		t.Run(f.Name+" every turn is in the events", func(t *testing.T) {
			for i, tr := range f.Anchors.Turns {
				where := fmt.Sprintf("turn %d (%s)", i, tr.LocalSubmit)
				if tr.Origin != event.OriginHumanTyped && tr.Origin != event.OriginNoInput {
					t.Errorf("%s: origin %q; these cases have human_typed prompts and the X11 twin's no_input ones", where, tr.Origin)
				}
				if tr.Origin.Human() != (tr.PromptClass != "") {
					t.Errorf("%s: a prompt class goes with a human origin, got %q and %q", where, tr.Origin, tr.PromptClass)
				}
				var submit, stop bool
				for _, e := range f.Events {
					if e.SessionKey != tr.SessionKey {
						continue
					}
					switch p := e.Data.(type) {
					case *event.PromptSubmit:
						submit = submit || p.TurnKey == tr.TurnKey && e.T == tr.TSubmit
					case *event.AgentStop:
						stop = stop || p.TurnKey == tr.TurnKey && e.T == tr.TStop
					}
				}
				if !submit || !stop {
					t.Errorf("%s: turn %s has a prompt.submit at %d: %v, and an agent.stop at %d: %v",
						where, tr.TurnKey, tr.TSubmit, submit, tr.TStop, stop)
				}
			}
		})
	}
}

// TestFocusUnknownPrompts checks prompts.json against the events, as
// TestPrompts does.
func TestFocusUnknownPrompts(t *testing.T) {
	for _, f := range loadFocusUnknown(t) {
		t.Run(f.Name+" every prompt text belongs to a prompt.submit", func(t *testing.T) {
			if f.Prompts.Fixture != f.Name {
				t.Errorf("%s names fixture %q", PromptsFile, f.Prompts.Fixture)
			}
			n := 0
			for _, e := range f.Events {
				if _, ok := e.Data.(*event.PromptSubmit); ok {
					n++
				}
			}
			if len(f.Prompts.Prompts) != n {
				t.Errorf("%s has %d prompts, the events %d", PromptsFile, len(f.Prompts.Prompts), n)
			}
			for i, p := range f.Prompts.Prompts {
				where := fmt.Sprintf("prompt %d (%s)", i, p.Local)
				var found *event.PromptSubmit
				for _, e := range f.Events {
					if ps, ok := e.Data.(*event.PromptSubmit); ok && ps.TurnKey == p.TurnKey && e.T == p.T {
						found = ps
					}
				}
				if found == nil {
					t.Errorf("%s: no prompt.submit for turn %s at %d", where, p.TurnKey, p.T)
					continue
				}
				if found.LenChars != p.LenChars {
					t.Errorf("%s: len_chars %d, the event says %d", where, p.LenChars, found.LenChars)
				}
				if p.Source == "" {
					t.Errorf("%s: source missing; it says where the text comes from", where)
				}
				if p.Erratum != "" {
					t.Errorf("%s: erratum %q; no case's text and length disagree", where, p.Erratum)
				}
				if p.Text != nil && utf8.RuneCountInString(*p.Text) != p.LenChars {
					t.Errorf("%s: the text has %d characters but len_chars is %d", where, utf8.RuneCountInString(*p.Text), p.LenChars)
				}
			}
		})
	}
}

// TestFocusUnknownGenerators runs each case's gen.py into a temporary
// directory and checks that it writes the checked-in files byte for byte.
// gen.py also checks its runs against a second-by-second replay of SM 2.5
// and its README's tables against its rows, and fails if either disagrees.
// Without python3 the test is skipped, except in CI, where it fails.
func TestFocusUnknownGenerators(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		if os.Getenv("CI") != "" {
			t.Fatal("python3 is needed to check the focus-unknown generators")
		}
		t.Skip("python3 not found")
	}
	root, err := Root()
	if err != nil {
		t.Fatal(err)
	}
	// go test caches a passing result by the files the test opens, and a
	// file python opens doesn't count, so the test reads the modules every
	// gen.py imports and each gen.py and README itself.
	for _, m := range []string{"focus.py", "ptygame.py", filepath.Join("..", "timeline.py")} {
		if _, err := os.ReadFile(filepath.Join(root, focusUnknownDir, m)); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range loadFocusUnknown(t) {
		t.Run(f.Name+" gen.py writes the checked-in files", func(t *testing.T) {
			gen := filepath.Join(f.Dir, "gen.py")
			for _, name := range []string{gen, filepath.Join(f.Dir, "README.md")} {
				if _, err := os.ReadFile(name); err != nil {
					t.Fatal(err)
				}
			}
			out := t.TempDir()
			cmd := exec.Command(python, "-I", "-B", gen, "--out", out)
			if b, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("%s: %v\n%s", gen, err, b)
			}
			for _, name := range []string{EventsFile, ExpectedFile, AnchorsFile, PromptsFile, focusSlotsFile} {
				got, err := os.ReadFile(filepath.Join(out, name))
				if err != nil {
					t.Fatal(err)
				}
				want, err := os.ReadFile(filepath.Join(f.Dir, name))
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(got, want) {
					t.Errorf("%s differs from what gen.py writes; run python3 -I %s", name, gen)
				}
			}
		})
	}
}

// focusNoBucket matches what a minute table's Arithmetic cell says of a row
// without a bucket.
var focusNoBucket = regexp.MustCompile(`(?i)\bno bucket\b`)

// TestFocusUnknownREADME checks each case's hand arithmetic, the oracle
// WP-FX8 asks for: the README's minute tables work out every row of
// expected.json and credit what it credits; each minute's Arithmetic cell
// names every uncredited code of its row with its seconds and no other
// code, and says when the row has no bucket; and every total's arithmetic
// gives its seconds, which are expected.json's.
func TestFocusUnknownREADME(t *testing.T) {
	for _, f := range loadFocusUnknown(t) {
		exp := f.Expected
		readme := filepath.Join(f.Dir, "README.md")
		byName := map[string]string{}
		for k, n := range exp.Projects {
			byName[n] = k
		}
		byLocal := map[string]Row{}
		for _, r := range exp.Rows {
			byLocal[r.Local] = r
		}
		t.Run(f.Name+" the README works out every row of expected.json, as expected.json has it", func(t *testing.T) {
			rows := lookbackTables(t, readme, lookbackMinuteHeader)
			if len(rows) == 0 {
				t.Fatal("the README has no minute table")
			}
			seen := map[string]bool{}
			for _, cells := range rows {
				if len(cells) != 4 {
					t.Errorf("README row %q: %d cells, want 4", cells, len(cells))
					continue
				}
				r, ok := byLocal[cells[0]]
				if !ok {
					t.Errorf("README minute %q is no row of %s", cells[0], ExpectedFile)
					continue
				}
				seen[cells[0]] = true
				credited, err := strconv.ParseFloat(strings.ReplaceAll(cells[1], ",", ""), 64)
				if err != nil || credited != r.CreditedS {
					t.Errorf("README %s credits %q, %s %v", cells[0], cells[1], ExpectedFile, r.CreditedS)
				}
				got, err := lookbackReasonsCell(cells[2], exp.Projects, byName)
				if err != nil {
					t.Errorf("README %s: %v", cells[0], err)
				} else if !lookbackCreditEqual(got, r.Credit) {
					t.Errorf("README %s credits %v, %s %v", cells[0], got, ExpectedFile, r.Credit)
				}
				codes, err := relayedReadmeCodes(cells[3])
				if err != nil {
					t.Errorf("README %s: %v", cells[0], err)
					continue
				}
				want := map[event.Reason]int64{}
				for k, s := range r.Uncredited {
					want[k] = int64(s)
				}
				if !maps.Equal(codes, want) {
					t.Errorf("README %s names the codes %v, %s has %v; a row's Arithmetic names each with its seconds",
						cells[0], codes, ExpectedFile, want)
				}
				if says := focusNoBucket.MatchString(cells[3]); says == r.Bucket {
					t.Errorf("README %s says no bucket: %v, but %s gives it a bucket: %v", cells[0], says, ExpectedFile, r.Bucket)
				}
			}
			for _, r := range exp.Rows {
				if !seen[r.Local] {
					t.Errorf("row %s of %s isn't in the README's minute tables", r.Local, ExpectedFile)
				}
			}
		})
		t.Run(f.Name+" every total the README works out is expected.json's", func(t *testing.T) {
			seen := false
			for _, cells := range lookbackTables(t, readme, lookbackTotalHeader) {
				if len(cells) != 3 {
					t.Errorf("README total %q: %d cells, want 3", cells, len(cells))
					continue
				}
				key := cells[0]
				seen = seen || key == "credited_s"
				n, err := strconv.ParseFloat(strings.ReplaceAll(cells[1], ",", ""), 64)
				if err != nil {
					t.Errorf("README total %s: seconds %q: %v", key, cells[1], err)
					continue
				}
				v, err := lookbackArithmetic(cells[2])
				if err != nil {
					t.Errorf("README total %s: %v", key, err)
				} else if float64(v) != n {
					t.Errorf("README total %s: %s is %d, not %v", key, cells[2], v, n)
				}
				want, ok := lookbackTotal(exp, key)
				if !ok {
					t.Errorf("README total %s names no total, reason, code or project of %s", key, ExpectedFile)
				} else if want != n {
					t.Errorf("README total %s is %v, %s says %v", key, n, ExpectedFile, want)
				}
			}
			if !seen {
				t.Error("the README's totals don't work out credited_s")
			}
		})
	}
}

// TestFocusUnknownAcceptance checks the numbers WP-FX8's entry in
// docs/PLAN.md gives each case.
func TestFocusUnknownAcceptance(t *testing.T) {
	fixtures := map[string]focusUnknownFixture{}
	for _, f := range loadFocusUnknown(t) {
		fixtures[f.c.name] = f
	}
	total := func(t *testing.T, f focusUnknownFixture, k string) float64 {
		t.Helper()
		v, ok := f.Expected.Totals.Get(k)
		if !ok {
			t.Errorf("%s has no %s total", f.Name, k)
		}
		return v
	}
	rowAt := func(t *testing.T, f focusUnknownFixture, local string) Row {
		t.Helper()
		for _, r := range f.Expected.Rows {
			if r.Local == local {
				return r
			}
		}
		t.Fatalf("%s has no row %s", f.Name, local)
		return Row{}
	}
	for _, tc := range []struct {
		name  string
		check func(t *testing.T)
	}{
		{"(a) credits SM 6.1's 695 of the cycle's 720 s, every one at level C with focus_unknown", func(t *testing.T) {
			f := fixtures["a-6.1-gnome"]
			if w := f.Expected.Window.End.Sub(f.Expected.Window.Start); w != 12*time.Minute {
				t.Errorf("the window is %s, SM 6.1's first cycle is 12 minutes", w)
			}
			if c := f.Expected.Totals.CreditedS; c != 695 || total(t, f, "level_c_s") != 695 || total(t, f, "focus_unknown_s") != 695 {
				t.Errorf("credited %v, level C %v, focus_unknown %v; want 695 each", c, total(t, f, "level_c_s"), total(t, f, "focus_unknown_s"))
			}
		}},
		{"(a) turns the browser run's 120 s of LOOKUP into READ 90, NAV 10 and G 20", func(t *testing.T) {
			f := fixtures["a-6.1-gnome"]
			// SM 6.1 credits 14:09 and 14:10 as LOOKUP 120. Here 14:09 is
			// READ 60 and 14:10 READ 30, NAV 10 and G 20.
			got := map[event.Reason]float64{}
			for _, local := range []string{"14:09", "14:10"} {
				for k, s := range rowAt(t, f, local).CreditedReasons() {
					got[k] += s
				}
			}
			want := map[event.Reason]float64{event.ReasonRead: 90, event.ReasonNavigate: 10, event.ReasonCommit: 20}
			if !maps.Equal(got, want) {
				t.Errorf("14:09 and 14:10 credit %v, want %v", got, want)
			}
			if _, ok := f.Expected.Totals.CreditedByReason[event.ReasonLookup]; ok {
				t.Error("LOOKUP is credited, but a slot of unknown focus holds no lookup (SM 2.5)")
			}
		}},
		{"(a) keeps 14:04's 25 s as X.agent_only", func(t *testing.T) {
			r := rowAt(t, fixtures["a-6.1-gnome"], "14:04")
			if !maps.Equal(r.Uncredited, map[event.Reason]float64{event.ReasonAgentOnly: 25}) {
				t.Errorf("14:04's uncredited seconds are %v", r.Uncredited)
			}
		}},
		{"(b) credits no second at level A or B, and its X11 twin credits nothing", func(t *testing.T) {
			f := fixtures["b-pty-game"]
			if total(t, f, "level_a_s") != 0 || total(t, f, "level_b_s") != 0 || f.Expected.Totals.CreditedS == 0 {
				t.Errorf("level A %v, level B %v, credited %v; the pty driver's prompts credit only level C",
					total(t, f, "level_a_s"), total(t, f, "level_b_s"), f.Expected.Totals.CreditedS)
			}
			twin := fixtures["b-pty-game-x11"]
			if twin.Expected.Totals.CreditedS != 0 || len(twin.Anchors.Anchors) != 0 {
				t.Errorf("the X11 twin credits %v s from %d anchors; with the browser in front it credits nothing",
					twin.Expected.Totals.CreditedS, len(twin.Anchors.Anchors))
			}
		}},
		{"(c) drops only the two slots' 30 s to level C", func(t *testing.T) {
			f := fixtures["c-x11-reconnect"]
			var c []string
			for _, s := range f.slots.Slots {
				if s.Levels[event.EvidenceC] > 0 {
					c = append(c, fmt.Sprintf("%s %v", s.Local, s.Levels[event.EvidenceC]))
				}
			}
			if !slices.Equal(c, []string{"10:02:15 15", "10:02:30 15"}) {
				t.Errorf("level-C seconds by slot: %q", c)
			}
			if b := total(t, f, "level_b_s"); b != f.Expected.Totals.CreditedS-30 {
				t.Errorf("level B %v of %v credited; every other second is B", b, f.Expected.Totals.CreditedS)
			}
		}},
		{"(d) gives each contested slot to the project of its first credited second, site 360 s and api 476", func(t *testing.T) {
			f := fixtures["d-two-projects"]
			byName := map[string]string{}
			for k, n := range f.Expected.Projects {
				byName[n] = k
			}
			if got := f.Expected.Totals.CreditedByProject; got[byName["site"]] != 360 || got[byName["api"]] != 476 {
				t.Errorf("credit by project %v, want site 360 and api 476 (D-split-slots)", got)
			}
			// One slot of each contest: leads (SM 3.3 rule 3), a lead against
			// a bucket (rule 2), and buckets (SM 2.5), with the two slots the
			// slot rule gives a project the nearest anchor alone wouldn't,
			// and the slots after them.
			want := map[string]string{"16:00:15": "api", "16:02:00": "site", "16:03:00": "site", "16:03:30": "api",
				"16:03:45": "site", "16:08:00": "site", "16:08:15": "api"}
			for _, s := range f.slots.Slots {
				p, ok := want[s.Local]
				if !ok {
					continue
				}
				delete(want, s.Local)
				if s.P != byName[p] || s.CreditedS != 15 || len(s.Credit) != 1 {
					t.Errorf("slot %s: p %q with %v s from %v; want all 15 s to %s", s.Local, f.Expected.Projects[s.P], s.CreditedS, s.Credit, p)
				}
			}
			if len(want) > 0 {
				t.Errorf("slots.json has no slot %v", slices.Sorted(maps.Keys(want)))
			}
		}},
		{"(e) has one os.focus, an hour before the work, and checkpoints with none after them", func(t *testing.T) {
			f := fixtures["e-hour-before"]
			var focus []int64
			for _, e := range f.Events {
				if p, ok := e.Data.(*event.OSFocus); ok {
					if p.AppClass != event.AppUnknown {
						t.Errorf("an os.focus of class %q; the case's only one is unknown", p.AppClass)
					}
					focus = append(focus, e.T)
				}
			}
			var work int64 = -1
			for _, r := range f.Expected.Rows {
				if r.CreditedS > 0 {
					work = r.Start.UnixMilli()
					break
				}
			}
			if len(focus) != 1 || work < 0 || work-focus[0] < 3_600_000 || focus[0] >= f.Expected.Window.Start.UnixMilli() {
				t.Fatalf("os.focus at %v and the first credited minute at %d; want one, before the window, at least an hour "+
					"before the work", focus, work)
			}
			// DESIGN 6.5's checkpoints fall on UTC minutes divisible by 15.
			// The one os.focus comes before the window, so every checkpoint in
			// it has none after it.
			n := 0
			for m := f.Expected.Window.Start; m.Before(f.Expected.Window.End); m = m.Add(time.Minute) {
				if m.Minute()%15 == 0 {
					n++
				}
			}
			if got := total(t, f, "checkpoints_after_focus"); n == 0 || float64(n) != got {
				t.Errorf("%d checkpoints after the os.focus, checkpoints_after_focus %v", n, got)
			}
		}},
	} {
		t.Run(tc.name, tc.check)
	}
}

// slotFocusFromEvents returns the focus class SM 2.5 gives the slot that
// starts at start, from the os.focus records alone: the window that held
// focus longest in the slot, records of one app_class and focus_project
// counting as one window, the one in front first on a tie. It is "" when no
// record is in force in the slot.
func slotFocusFromEvents(events []event.Event, start int64) event.AppClass {
	type window struct {
		class   event.AppClass
		project string
	}
	var recs []event.Event
	for _, e := range events {
		if _, ok := e.Data.(*event.OSFocus); ok {
			recs = append(recs, e)
		}
	}
	end := start + 15_000
	held, first := map[window]int64{}, map[window]int64{}
	for i, e := range recs {
		next := end
		if i+1 < len(recs) {
			next = min(end, recs[i+1].T)
		}
		a := max(e.T, start)
		if a >= next {
			continue
		}
		p := e.Data.(*event.OSFocus)
		w := window{p.AppClass, p.FocusProject}
		if _, seen := first[w]; !seen {
			first[w] = a
		}
		held[w] += next - a
	}
	var best window
	found := false
	for w, d := range held {
		if !found || d > held[best] || d == held[best] && first[w] < first[best] {
			best, found = w, true
		}
	}
	return best.class
}

// focusInForce returns the last os.focus written before the event with the
// given id, in the order the agent wrote them, or nil.
func focusInForce(events []event.Event, id string) *event.OSFocus {
	var last *event.OSFocus
	for _, e := range events {
		if e.EventID == id {
			return last
		}
		if p, ok := e.Data.(*event.OSFocus); ok {
			last = p
		}
	}
	return nil
}
