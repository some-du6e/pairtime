// SPDX-License-Identifier: AGPL-3.0-or-later

package timelines

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/some-du6e/pairtime/pkg/event"
)

func loadAll(t *testing.T) []*Fixture {
	t.Helper()
	fixtures, err := LoadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(fixtures) == 0 {
		t.Fatal("no fixtures under testdata/timelines")
	}
	return fixtures
}

// TestFixturesPresent pins the fixtures WP-FX1 writes, so one can't vanish
// from the scorer's oracle without a test failing.
func TestFixturesPresent(t *testing.T) {
	root, err := Root()
	if err != nil {
		t.Fatal(err)
	}
	names, err := List(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name    string
		fixture string
	}{
		{"SM 6.2, five minutes before school, is a fixture", "6.2"},
		{"SM 6.4, a /goal agent running overnight, is a fixture", "6.4"},
		{"SM 6.6, a scripted claude -p loop, is a fixture", "6.6"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if !slices.Contains(names, tc.fixture) {
				t.Errorf("testdata/timelines/%s is missing; found %q", tc.fixture, names)
			}
			if _, err := os.Stat(filepath.Join(root, tc.fixture, "README.md")); err != nil {
				t.Errorf("every fixture has a README.md: %v", err)
			}
		})
	}
}

// TestSMTotals checks the totals the plan names as WP-FX1's acceptance
// numbers, each copied from SM's text.
func TestSMTotals(t *testing.T) {
	fixtures := map[string]*Fixture{}
	for _, f := range loadAll(t) {
		fixtures[f.Name] = f
	}
	for _, tc := range []struct {
		name    string
		fixture string
		total   string
		want    float64
	}{
		{"6.2 credits 265 s of the 285 s the lid was open", "6.2", "credited_s", 265},
		{"6.2 leaves 325 s in the reading bucket", "6.2", "bucket_left_s", 325},
		{"6.4 credits 810 s, all of it AI-assisted", "6.4", "credited_s", 810},
		{"6.4's agent runs 23,400 s", "6.4", "agent_running_s", 23400},
		{"6.4's agent-only time is 23,180 s", "6.4", "agent_only_s", 23180},
		{"6.6 credits nothing in four hours", "6.6", "credited_s", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, ok := fixtures[tc.fixture]
			if !ok {
				t.Fatalf("no fixture %s", tc.fixture)
			}
			got, ok := f.Expected.Totals.Get(tc.total)
			if !ok {
				t.Fatalf("%s: totals have no %s", tc.fixture, tc.total)
			}
			if got != tc.want {
				t.Errorf("%s: %s = %v, SM says %v", tc.fixture, tc.total, got, tc.want)
			}
		})
	}
}

// TestEventsValidate checks every event against pkg/event's rules.
func TestEventsValidate(t *testing.T) {
	for _, f := range loadAll(t) {
		t.Run(f.Name+" every event passes Event.Validate", func(t *testing.T) {
			for i, e := range f.Events {
				if err := e.Validate(); err != nil {
					t.Errorf("%s line %d (seq %d, %s): %v", EventsFile, i+1, e.Seq, e.Kind(), err)
				}
			}
		})
	}
}

// TestEventsMatchSchema checks every event, as written, against
// docs/schema/event.schema.json.
func TestEventsMatchSchema(t *testing.T) {
	sv := loadEventSchema(t)
	for _, f := range loadAll(t) {
		t.Run(f.Name+" every event matches the JSON Schema", func(t *testing.T) {
			bad := 0
			for i, line := range f.Lines {
				v, err := decodeJSON(line)
				if err != nil {
					t.Fatalf("%s line %d: %v", EventsFile, i+1, err)
				}
				if errs := sv.validate(v); len(errs) > 0 {
					t.Errorf("%s line %d: %s", EventsFile, i+1, strings.Join(errs, "; "))
					if bad++; bad == 10 {
						t.Fatal("stopping after 10 bad lines")
					}
				}
			}
		})
	}
}

// TestEventsRoundTrip checks that each line is exactly what pkg/event
// writes for the event it decodes to. A field pkg/event doesn't know, or one
// with the wrong JSON type, would otherwise be dropped on decoding without
// a word.
func TestEventsRoundTrip(t *testing.T) {
	for _, f := range loadAll(t) {
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
	}
}

// TestEventStream checks what holds across the events: one device, seq
// gapless, arrival order matching time order, ids unique and carrying the
// event's time.
func TestEventStream(t *testing.T) {
	for _, f := range loadAll(t) {
		t.Run(f.Name+" events are one device's gapless stream in time order", func(t *testing.T) {
			ids := map[string]bool{}
			mono := map[string]int64{}
			for i, e := range f.Events {
				where := fmt.Sprintf("%s line %d", EventsFile, i+1)
				if e.DeviceID != f.Expected.Device {
					t.Errorf("%s: device_id %q, want the fixture's device %q", where, e.DeviceID, f.Expected.Device)
				}
				if i > 0 {
					prev := f.Events[i-1]
					if e.Seq != prev.Seq+1 {
						t.Errorf("%s: seq %d follows %d; seq is gapless", where, e.Seq, prev.Seq)
					}
					if e.T < prev.T {
						t.Errorf("%s: t %d is before the previous event's %d", where, e.T, prev.T)
					}
				}
				if ids[e.EventID] {
					t.Errorf("%s: event_id %s repeats", where, e.EventID)
				}
				ids[e.EventID] = true
				if got := uuidTime(e.EventID); got != e.T {
					t.Errorf("%s: event_id carries time %d, but t is %d", where, got, e.T)
				}
				if m, ok := mono[e.BootID]; ok && e.Mono < m {
					t.Errorf("%s: mono %d goes back from %d within boot %s", where, e.Mono, m, e.BootID)
				}
				mono[e.BootID] = e.Mono
			}
		})
	}
}

// uuidTime returns the Unix milliseconds in a UUIDv7's first 48 bits.
func uuidTime(id string) int64 {
	hex := strings.ReplaceAll(id, "-", "")
	if len(hex) != 32 {
		return -1
	}
	ms, err := strconv.ParseInt(hex[:12], 16, 64)
	if err != nil {
		return -1
	}
	return ms
}

// TestRows checks each row on its own: rows tile the window in whole
// minutes, and each bucket row's reasons and uncredited seconds account for
// every second of its minutes, or SM lists the codes without seconds.
func TestRows(t *testing.T) {
	for _, f := range loadAll(t) {
		exp := f.Expected
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
					t.Errorf("%s: erratum %q isn't in the fixture's errata", where, r.Erratum)
				}
				for _, c := range r.Codes {
					if !c.Uncredited() {
						t.Errorf("%s: code %q is not an uncredited reason", where, c)
					}
				}
				if !r.Bucket {
					if r.BucketMinutes != 0 || r.CreditedS != 0 || len(r.Credit) > 0 || r.Uncredited != nil || r.AgentRunningS != 0 {
						t.Errorf("%s: a minute without a bucket has no credit, no uncredited seconds and no agent time", where)
					}
					continue
				}
				if r.BucketMinutes < 1 || r.BucketMinutes > r.Minutes || r.Minutes == 1 && r.BucketMinutes != 1 {
					t.Errorf("%s: bucket_minutes %d for %d minutes", where, r.BucketMinutes, r.Minutes)
				}
				// Only minutes with a bucket report seconds.
				span := 60 * float64(r.BucketMinutes)
				sum := 0.0
				for p, reasons := range r.Credit {
					if _, ok := exp.Projects[p]; !ok {
						t.Errorf("%s: project %q isn't in the fixture's projects", where, p)
					}
					for k, s := range reasons {
						if !k.Credited() {
							t.Errorf("%s: %q is not a credit reason", where, k)
						}
						if s <= 0 {
							t.Errorf("%s: %s has %v s; rows list only reasons that credited something", where, k, s)
						}
						sum += s
					}
				}
				if sum != r.CreditedS {
					t.Errorf("%s: reasons sum to %v, credited_s is %v", where, sum, r.CreditedS)
				}
				if r.CreditedS > span {
					t.Errorf("%s: %v s credited in %v s", where, r.CreditedS, span)
				}
				if r.AgentRunningS < 0 || r.AgentRunningS > span {
					t.Errorf("%s: agent_running_s %v outside 0 to %v", where, r.AgentRunningS, span)
				}
				if r.Uncredited == nil {
					if len(r.Codes) == 0 {
						t.Errorf("%s: with no uncredited seconds, the row lists the codes SM gives", where)
					}
					continue
				}
				if len(r.Codes) > 0 {
					t.Errorf("%s: codes are only for rows whose uncredited seconds SM doesn't give", where)
				}
				unc := 0.0
				for k, s := range r.Uncredited {
					if !k.Uncredited() {
						t.Errorf("%s: %q is not an uncredited reason", where, k)
					}
					if s <= 0 {
						t.Errorf("%s: %s has %v s", where, k, s)
					}
					unc += s
				}
				if r.CreditedS+unc != span {
					t.Errorf("%s: %v credited + %v uncredited = %v, want %v", where, r.CreditedS, unc, r.CreditedS+unc, span)
				}
			}
		})
	}
}

// TestTotals checks that the totals are the sums of the rows, that every
// total SM states is there, and that every erratum named exists.
func TestTotals(t *testing.T) {
	root, err := Root()
	if err != nil {
		t.Fatal(err)
	}
	errata := filepath.Join(root, "..", "..", "docs", "errata")
	for _, f := range loadAll(t) {
		exp := f.Expected
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
			if a, ok := tot.Get("agent_only_s"); ok && a > tot.AgentRunningS {
				t.Errorf("agent_only_s %v is more than agent_running_s %v", a, tot.AgentRunningS)
			}
		})
		t.Run(f.Name+" every stated total and erratum exists", func(t *testing.T) {
			for _, k := range exp.Stated {
				if _, ok := exp.Totals.Get(k); !ok {
					t.Errorf("stated total %q isn't in totals", k)
				}
			}
			for k, a := range exp.Approx {
				if a.SM == "" {
					t.Errorf("approx %q doesn't quote SM", k)
				}
			}
			for _, name := range exp.Errata {
				if _, err := os.Stat(filepath.Join(errata, name+".md")); err != nil {
					t.Errorf("erratum %q: %v", name, err)
				}
			}
		})
	}
}

func nonZero[K comparable](m map[K]float64) map[K]float64 {
	out := map[K]float64{}
	for k, v := range m {
		if v != 0 {
			out[k] = v
		}
	}
	return out
}

// anchorEventKinds maps each anchor kind to the event kind behind it (SM
// 2.3).
var anchorEventKinds = map[event.AnchorKind]event.Kind{
	event.AnchorSubstantivePrompt: event.KindPromptSubmit,
	event.AnchorAnswer:            event.KindPromptSubmit,
	event.AnchorGoAhead:           event.KindPromptSubmit,
	event.AnchorQueuedMessage:     event.KindAgentQueue,
	event.AnchorPermissionAnswer:  event.KindAgentPermission,
	event.AnchorInterrupt:         event.KindAgentInterrupt,
	event.AnchorTypedEdit:         event.KindEditorActivity,
	event.AnchorEditorNavigation:  event.KindEditorActivity,
	event.AnchorTypedCommand:      event.KindShellCommand,
	event.AnchorHumanCommit:       event.KindGitChange,
}

// promptAnchorClasses lists the prompt classes each prompt and queued
// message anchor kind comes from (SM 2.3): a queued message's lead is that
// of a prompt of its class, so it has an origin and a class too.
var promptAnchorClasses = map[event.AnchorKind][]event.PromptClass{
	event.AnchorSubstantivePrompt: {event.ClassSubstantive, event.ClassErrorPaste},
	event.AnchorAnswer:            {event.ClassAnswer},
	event.AnchorGoAhead:           {event.ClassLowEffort},
	event.AnchorQueuedMessage:     {event.ClassSubstantive, event.ClassErrorPaste, event.ClassAnswer, event.ClassLowEffort},
}

// TestAnchors checks anchors.json against the events.
func TestAnchors(t *testing.T) {
	for _, f := range loadAll(t) {
		t.Run(f.Name+" every anchor is an event of its kind", func(t *testing.T) {
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
					LeadS: a.LeadS, TrailS: a.TrailS, Via: a.Via}
				if err := ea.Validate(); err != nil {
					t.Errorf("%s: %v", where, err)
				}
				if classes, isPrompt := promptAnchorClasses[a.Kind]; isPrompt {
					if !a.Origin.Human() || !slices.Contains(classes, a.PromptClass) {
						t.Errorf("%s: origin %q and class %q don't make a %s", where, a.Origin, a.PromptClass, a.Kind)
					}
				} else if a.Origin != "" || a.PromptClass != "" {
					t.Errorf("%s: only prompt and queued message anchors have an origin and a class", where)
				}
				if a.LeadFrom != "sm" && a.LeadFrom != "params" {
					t.Errorf("%s: lead_from %q, want sm or params", where, a.LeadFrom)
				}
				if a.Level != f.Expected.Evidence {
					t.Errorf("%s: level %q, but the fixture's credit is level %q", where, a.Level, f.Expected.Evidence)
				}
				if i > 0 && a.T < f.Anchors.Anchors[i-1].T {
					t.Errorf("%s: anchors are listed in time order", where)
				}
			}
		})
		t.Run(f.Name+" every turn is in the events", func(t *testing.T) {
			for i, tr := range f.Anchors.Turns {
				where := fmt.Sprintf("turn %d (%s)", i, tr.LocalSubmit)
				if !slices.Contains([]event.Origin{event.OriginScheduled, event.OriginSelfContinue, event.OriginSubagent,
					event.OriginHeadless, event.OriginCloud, event.OriginHumanTyped, event.OriginHumanPasted,
					event.OriginNoInput}, tr.Origin) {
					t.Errorf("%s: origin %q", where, tr.Origin)
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
				// A turn a stop hook continued has no prompt of its own (SM
				// 2.2 rule 2): it starts when the turn before it stops.
				if !submit && tr.Origin != event.OriginSelfContinue {
					t.Errorf("%s: no prompt.submit for turn %s at %d", where, tr.TurnKey, tr.TSubmit)
				}
				if tr.TStop != 0 && !stop {
					t.Errorf("%s: no agent.stop for turn %s at %d", where, tr.TurnKey, tr.TStop)
				}
			}
		})
	}
}

// TestPrompts checks prompts.json against the events.
func TestPrompts(t *testing.T) {
	for _, f := range loadAll(t) {
		t.Run(f.Name+" every prompt text belongs to a prompt.submit", func(t *testing.T) {
			if f.Prompts.Fixture != f.Name {
				t.Errorf("%s names fixture %q", PromptsFile, f.Prompts.Fixture)
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
					t.Errorf("%s: source missing; it says whether SM quotes the text", where)
				}
				if p.Text == nil {
					continue
				}
				if n := utf8.RuneCountInString(*p.Text); n != p.LenChars {
					if p.Erratum == "" || !slices.Contains(f.Expected.Errata, p.Erratum) {
						t.Errorf("%s: the text has %d characters but len_chars is %d, and no erratum explains it", where, n, p.LenChars)
					}
				} else if p.Erratum != "" {
					t.Errorf("%s: erratum %q, but the text and len_chars agree", where, p.Erratum)
				}
			}
		})
	}
}

// TestGeneratorsReproduce runs each fixture's gen.py into a temporary
// directory and checks that it writes the checked-in files byte for byte.
// The generators need python3; without it the test is skipped, except in
// CI, where it fails.
func TestGeneratorsReproduce(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		if os.Getenv("CI") != "" {
			t.Fatal("python3 is needed to check the fixture generators")
		}
		t.Skip("python3 not found")
	}
	for _, f := range loadAll(t) {
		gen := filepath.Join(f.Dir, "gen.py")
		if _, err := os.Stat(gen); errors.Is(err, os.ErrNotExist) {
			continue
		}
		t.Run(f.Name+" gen.py writes the checked-in files", func(t *testing.T) {
			out := t.TempDir()
			cmd := exec.Command(python, "-I", "-B", gen, "--out", out)
			if b, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("%s: %v\n%s", gen, err, b)
			}
			for _, name := range []string{EventsFile, ExpectedFile, AnchorsFile, PromptsFile} {
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
