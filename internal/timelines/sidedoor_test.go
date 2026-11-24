// SPDX-License-Identifier: AGPL-3.0-or-later

package timelines

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/some-du6e/pairtime/pkg/event"
)

// The side-door cases of WP-FX4 (docs/PLAN.md) are timelines under
// testdata/timelines/sidedoor, one directory per case. Each holds the four
// files of a timeline fixture, which Load reads, and sidedoor.json, which
// says what every record opens, flags and adds to logged_s and ai_logged_s,
// and what every turn puts in its reading bucket. List doesn't descend into
// sidedoor, so the tests in timelines_test.go don't see these cases. The
// tests below check them the same way and check sidedoor.json against the
// events. testdata/timelines/sidedoor/README.md describes the cases.

const (
	sideDoorDir  = "sidedoor"
	sideDoorFile = "sidedoor.json"
	// rTurnBaseS is R_TURN_BASE_S (SM 8.3): the most a turn may put in its
	// reading bucket when only plugin, bridge or track records stand behind
	// it (DESIGN 5.2, 5.4). WP-PA1's pkg/params will hold it.
	rTurnBaseS = 20
	// trackLogMatchMS and trackDedupeMS are TRACK_LOG_MATCH_S and
	// TRACK_DEDUPE_S (DESIGN 6.3), in milliseconds.
	trackLogMatchMS = 10_000
	trackDedupeMS   = 30_000
	// loggedTolerance absorbs float64 rounding when per-record seconds such
	// as 4.3 and 45.7 are summed.
	loggedTolerance = 1e-9
)

// sideDoorCases are the cases WP-FX4 lists, (a) to (g), in order.
var sideDoorCases = []string{
	"a-track-jiggler",
	"b-track-keys",
	"c-plugin-sizes",
	"d-editor-script",
	"e-bridge-lines",
	"f-track-matched",
	"g-track-without-prompt",
}

// sideDoor is sidedoor.json.
type sideDoor struct {
	Fixture string `json:"fixture"`
	// Case is the letter docs/PLAN.md gives the case.
	Case  string   `json:"case"`
	Title string   `json:"title"`
	WP    string   `json:"wp"`
	Rules []string `json:"rules"`
	// Records holds every record of the window except the OS ones, in
	// arrival order.
	Records []sideDoorRecord `json:"records"`
	// Turns holds every turn whose reading fill the case pins.
	Turns  []sideDoorTurn `json:"turns"`
	Errata []string       `json:"errata"`
	Notes  []string       `json:"notes"`
}

// sideDoorRecord is what one record does.
type sideDoorRecord struct {
	EventID string     `json:"event_id"`
	T       int64      `json:"t"`
	Local   string     `json:"local"`
	Kind    event.Kind `json:"kind"`
	Via     event.Via  `json:"via"`
	// Anchor says whether the record opens an anchor; anchors.json has it.
	Anchor bool `json:"anchor"`
	// Level and Labels are set for an anchor only.
	Level  event.Evidence `json:"level"`
	Labels []event.Label  `json:"labels"`
	// Track is set for cli.track records only: "anchor" when DESIGN 5.4's
	// four conditions open one, "matched" for track_matched, and
	// "without_prompt" when the call raises track_without_prompt.
	Track string `json:"track"`
	// Flags are the tier-2 flags the record raises.
	Flags []string `json:"flags"`
	// LoggedS and AILoggedS are what the record adds to logged_s and
	// ai_logged_s: min(gap to the counted record before it, 120 s), and 0 for
	// the first and for every record D19 leaves out.
	LoggedS   float64 `json:"logged_s"`
	AILoggedS float64 `json:"ai_logged_s"`
	Why       string  `json:"why"`
}

// sideDoorTurn is one turn's reading fill (SM 2.4).
type sideDoorTurn struct {
	// OpenedBy is the event_id of the prompt or track call that starts the
	// turn.
	OpenedBy   string    `json:"opened_by"`
	TurnKey    string    `json:"turn_key"`
	SessionKey string    `json:"session_key"`
	T          int64     `json:"t"`
	Local      string    `json:"local"`
	Via        event.Via `json:"via"`
	// Backed says whether a hook or transcript record backs the turn.
	Backed bool `json:"backed"`
	// OutputFillS is what the turn's reported output would add without the
	// side-door cap; FillS is the turn's whole fill, base included.
	OutputFillS float64 `json:"output_fill_s"`
	FillS       float64 `json:"fill_s"`
	Why         string  `json:"why"`
}

// sideDoorCase is a loaded case: the timeline fixture and its sidedoor.json.
type sideDoorCase struct {
	*Fixture
	Side sideDoor
}

func loadSideDoor(t *testing.T) []sideDoorCase {
	t.Helper()
	root, err := Root()
	if err != nil {
		t.Fatal(err)
	}
	var out []sideDoorCase
	for _, dir := range sideDoorCases {
		f, err := Load(root, sideDoorDir+"/"+dir)
		if err != nil {
			t.Fatal(err)
		}
		c := sideDoorCase{Fixture: f}
		if err := readJSON(filepath.Join(f.Dir, sideDoorFile), &c.Side); err != nil {
			t.Fatalf("timelines: %s/%s: %v", f.Name, sideDoorFile, err)
		}
		out = append(out, c)
	}
	return out
}

// countsTowardLogged reports whether D19 lets a record with via v count
// toward logged_s and ai_logged_s.
func countsTowardLogged(v event.Via) bool {
	switch v {
	case event.ViaHook, event.ViaLog, event.ViaOTel, event.ViaEditor, event.ViaBridge:
		return true
	}
	return false
}

// TestSideDoorCasesPresent pins the cases WP-FX4 asks for, so one can't
// vanish from the scorer's oracle without a test failing.
func TestSideDoorCasesPresent(t *testing.T) {
	root, err := Root()
	if err != nil {
		t.Fatal(err)
	}
	t.Run("every case directory under sidedoor is one of WP-FX4's cases", func(t *testing.T) {
		dirs, err := List(filepath.Join(root, sideDoorDir))
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(dirs, sideDoorCases) {
			t.Errorf("sidedoor holds %q, the cases are %q", dirs, sideDoorCases)
		}
	})
	for _, dir := range sideDoorCases {
		t.Run(dir+" has every file of a side-door case", func(t *testing.T) {
			for _, name := range []string{EventsFile, ExpectedFile, AnchorsFile, PromptsFile, sideDoorFile,
				"README.md", "gen.py"} {
				if _, err := os.Stat(filepath.Join(root, sideDoorDir, dir, name)); err != nil {
					t.Error(err)
				}
			}
		})
	}
}

// TestSideDoorEvents checks each case's events as TestEventsValidate,
// TestEventsMatchSchema, TestEventsRoundTrip and TestEventStream check the
// SM fixtures'.
func TestSideDoorEvents(t *testing.T) {
	sv := loadEventSchema(t)
	for _, c := range loadSideDoor(t) {
		t.Run(c.Name+" every event passes Event.Validate and the JSON Schema", func(t *testing.T) {
			for i, e := range c.Events {
				if err := e.Validate(); err != nil {
					t.Errorf("%s line %d (seq %d, %s): %v", EventsFile, i+1, e.Seq, e.Kind(), err)
				}
				v, err := decodeJSON(c.Lines[i])
				if err != nil {
					t.Fatalf("%s line %d: %v", EventsFile, i+1, err)
				}
				if errs := sv.validate(v); len(errs) > 0 {
					t.Errorf("%s line %d: %s", EventsFile, i+1, strings.Join(errs, "; "))
				}
			}
		})
		t.Run(c.Name+" every line is pkg/event's own encoding", func(t *testing.T) {
			for i, e := range c.Events {
				b, err := json.Marshal(e)
				if err != nil {
					t.Fatalf("%s line %d: %v", EventsFile, i+1, err)
				}
				if !bytes.Equal(b, c.Lines[i]) {
					t.Errorf("%s line %d re-encodes differently:\n got %s\nwant %s", EventsFile, i+1, b, c.Lines[i])
				}
			}
		})
		t.Run(c.Name+" events are one device's stream in time order, and seq rises by 1 since a fixture's ledger holds only events", func(t *testing.T) {
			ids := map[string]bool{}
			for i, e := range c.Events {
				where := fmt.Sprintf("%s line %d", EventsFile, i+1)
				if e.DeviceID != c.Expected.Device {
					t.Errorf("%s: device_id %q, want the case's device %q", where, e.DeviceID, c.Expected.Device)
				}
				if i > 0 {
					prev := c.Events[i-1]
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
	}
}

// TestSideDoorRows checks the rows and totals as TestRows and TestTotals do
// for the SM fixtures, and that the credit by level sums to the credit.
func TestSideDoorRows(t *testing.T) {
	root, err := Root()
	if err != nil {
		t.Fatal(err)
	}
	errata := filepath.Join(root, "..", "..", "docs", "errata")
	levelTotal := map[event.Evidence]string{event.EvidenceA: "level_a_s", event.EvidenceB: "level_b_s",
		event.EvidenceC: "level_c_s"}
	for _, c := range loadSideDoor(t) {
		exp := c.Expected
		t.Run(c.Name+" rows tile the window in minutes and account for every second", func(t *testing.T) {
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
				if i > 0 && !exp.Rows[i-1].End().Equal(r.Start) {
					t.Errorf("%s: starts at %s, but the row before ends at %s", where, r.Start, exp.Rows[i-1].End())
				}
				if r.Minutes != 1 {
					t.Errorf("%s: a side-door row is one minute, got %d", where, r.Minutes)
				}
				if r.Erratum != "" && !slices.Contains(exp.Errata, r.Erratum) {
					t.Errorf("%s: erratum %q isn't in the case's errata", where, r.Erratum)
				}
				for _, code := range r.Codes {
					if !code.Uncredited() {
						t.Errorf("%s: code %q is not an uncredited reason", where, code)
					}
				}
				if !r.Bucket {
					if r.BucketMinutes != 0 || r.CreditedS != 0 || len(r.Credit) > 0 || r.Uncredited != nil ||
						r.AgentRunningS != 0 {
						t.Errorf("%s: a minute without a bucket has no credit, no uncredited seconds and no agent time", where)
					}
					continue
				}
				if r.BucketMinutes != 1 {
					t.Errorf("%s: bucket_minutes %d for one minute", where, r.BucketMinutes)
				}
				sum := 0.0
				for p, reasons := range r.Credit {
					if _, ok := exp.Projects[p]; !ok {
						t.Errorf("%s: project %q isn't in the case's projects", where, p)
					}
					for k, s := range reasons {
						if !k.Credited() || s <= 0 {
							t.Errorf("%s: %s with %v s; rows list credit reasons that credited something", where, k, s)
						}
						sum += s
					}
				}
				if sum != r.CreditedS {
					t.Errorf("%s: reasons sum to %v, credited_s is %v", where, sum, r.CreditedS)
				}
				if r.Uncredited == nil {
					t.Errorf("%s: the rules decide every side-door minute's uncredited seconds", where)
					continue
				}
				unc := 0.0
				for k, s := range r.Uncredited {
					if !k.Uncredited() || s <= 0 {
						t.Errorf("%s: uncredited %s with %v s", where, k, s)
					}
					unc += s
				}
				if r.CreditedS+unc != 60 {
					t.Errorf("%s: %v credited + %v uncredited, want 60", where, r.CreditedS, unc)
				}
				if r.AgentRunningS < 0 || r.AgentRunningS > 60 {
					t.Errorf("%s: agent_running_s %v outside 0 to 60", where, r.AgentRunningS)
				}
			}
		})
		t.Run(c.Name+" totals are the rows' sums and the credit by level adds up", func(t *testing.T) {
			var credited, agent float64
			byReason := map[event.Reason]float64{}
			unc := map[event.Reason]float64{}
			for _, r := range exp.Rows {
				credited += r.CreditedS
				agent += r.AgentRunningS
				for k, s := range r.CreditedReasons() {
					byReason[k] += s
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
			if !maps.Equal(nonZero(tot.CreditedByReason), byReason) {
				t.Errorf("credited_by_reason = %v, rows sum to %v", tot.CreditedByReason, byReason)
			}
			if !maps.Equal(nonZero(tot.Uncredited), unc) {
				t.Errorf("uncredited = %v, rows sum to %v", tot.Uncredited, unc)
			}
			levels := 0.0
			for _, k := range levelTotal {
				v, ok := tot.Get(k)
				if !ok {
					t.Errorf("totals have no %s", k)
				}
				levels += v
			}
			if levels != tot.CreditedS {
				t.Errorf("credit by level sums to %v, credited_s is %v", levels, tot.CreditedS)
			}
			// Each case credits at one level, the one its expected.json names.
			if v, _ := tot.Get(levelTotal[exp.Evidence]); v != tot.CreditedS {
				t.Errorf("evidence %q, but %s is %v of %v", exp.Evidence, levelTotal[exp.Evidence], v, tot.CreditedS)
			}
			for _, k := range exp.Stated {
				if _, ok := tot.Get(k); !ok {
					t.Errorf("stated total %q isn't in totals", k)
				}
			}
			for _, name := range slices.Concat(exp.Errata, c.Side.Errata) {
				if _, err := os.Stat(filepath.Join(errata, name+".md")); err != nil {
					t.Errorf("erratum %q: %v", name, err)
				}
			}
		})
	}
}

// TestSideDoorAnchors checks anchors.json and prompts.json against the
// events, as TestAnchors and TestPrompts do. A track anchor's record is a
// cli.track, and since it isn't a prompt.submit it has no origin: DESIGN
// 5.4's four conditions are its evidence test, and it is only classed like a
// prompt.
func TestSideDoorAnchors(t *testing.T) {
	for _, c := range loadSideDoor(t) {
		t.Run(c.Name+" every anchor is an event of its kind", func(t *testing.T) {
			for i, a := range c.Anchors.Anchors {
				where := fmt.Sprintf("anchor %d (%s %s)", i, a.Kind, a.Local)
				e, ok := c.EventByID(a.EventID)
				if !ok {
					t.Errorf("%s: no event %s", where, a.EventID)
					continue
				}
				want := anchorEventKinds[a.Kind]
				if e.Via == event.ViaTrack {
					want = event.KindCLITrack
				}
				if e.Kind() != want {
					t.Errorf("%s: event %s is %s, want %s", where, a.EventID, e.Kind(), want)
				}
				if e.T != a.T || e.ProjectKey != a.ProjectKey || e.Via != a.Via {
					t.Errorf("%s: t, project_key and via are %d, %q, %q; the event has %d, %q, %q",
						where, a.T, a.ProjectKey, a.Via, e.T, e.ProjectKey, e.Via)
				}
				classes, isPrompt := promptAnchorClasses[a.Kind]
				switch {
				case isPrompt && e.Via == event.ViaTrack:
					if a.Origin != "" || !slices.Contains(classes, a.PromptClass) {
						t.Errorf("%s: a track anchor has a prompt class and no origin, got %q and %q", where, a.Origin, a.PromptClass)
					}
				case isPrompt:
					if !a.Origin.Human() || !slices.Contains(classes, a.PromptClass) {
						t.Errorf("%s: origin %q and class %q don't make a %s", where, a.Origin, a.PromptClass, a.Kind)
					}
				case a.Origin != "" || a.PromptClass != "":
					t.Errorf("%s: only prompt anchors have an origin and a class", where)
				}
				if a.LeadFrom != "sm" && a.LeadFrom != "params" {
					t.Errorf("%s: lead_from %q, want sm or params", where, a.LeadFrom)
				}
				if a.Level != c.Expected.Evidence {
					t.Errorf("%s: level %q, but the case's credit is level %q", where, a.Level, c.Expected.Evidence)
				}
				if i > 0 && a.T < c.Anchors.Anchors[i-1].T {
					t.Errorf("%s: anchors are listed in time order", where)
				}
			}
		})
		t.Run(c.Name+" every turn of anchors.json is a prompt and its stop", func(t *testing.T) {
			for i, tr := range c.Anchors.Turns {
				var submit, stop bool
				for _, e := range c.Events {
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
				if !submit || tr.TStop != 0 && !stop {
					t.Errorf("turn %d (%s): no prompt.submit at %d or no agent.stop at %d", i, tr.TurnKey, tr.TSubmit, tr.TStop)
				}
				if !tr.Origin.Human() || tr.PromptClass == "" {
					t.Errorf("turn %d (%s): every turn here starts with a human prompt, got %q", i, tr.TurnKey, tr.Origin)
				}
			}
		})
		t.Run(c.Name+" every prompt text belongs to a prompt or a track call", func(t *testing.T) {
			for i, p := range c.Prompts.Prompts {
				where := fmt.Sprintf("prompt %d (%s)", i, p.Local)
				n := -1
				for _, e := range c.Events {
					switch d := e.Data.(type) {
					case *event.PromptSubmit:
						if d.TurnKey == p.TurnKey && e.T == p.T {
							n = d.LenChars
						}
					case *event.CLITrack:
						opens := slices.ContainsFunc(c.Side.Turns, func(tr sideDoorTurn) bool {
							return tr.OpenedBy == e.EventID && tr.TurnKey == p.TurnKey
						})
						if opens && e.T == p.T {
							n = d.TextLen
						}
					}
				}
				if n < 0 {
					t.Errorf("%s: no prompt.submit or track turn %s at %d", where, p.TurnKey, p.T)
					continue
				}
				if n != p.LenChars {
					t.Errorf("%s: len_chars %d, the event says %d", where, p.LenChars, n)
				}
				if p.Text != nil && utf8.RuneCountInString(*p.Text) != p.LenChars {
					t.Errorf("%s: the text has %d characters, len_chars is %d", where, utf8.RuneCountInString(*p.Text), p.LenChars)
				}
				if p.Source == "" {
					t.Errorf("%s: source missing", where)
				}
			}
		})
	}
}

// TestSideDoorRecords checks sidedoor.json's records against the events,
// anchors.json and the rules each one rests on.
func TestSideDoorRecords(t *testing.T) {
	for _, c := range loadSideDoor(t) {
		t.Run(c.Name+" names its own fixture in every file", func(t *testing.T) {
			for file, got := range map[string]string{ExpectedFile: c.Expected.Fixture, AnchorsFile: c.Anchors.Fixture,
				PromptsFile: c.Prompts.Fixture, sideDoorFile: c.Side.Fixture} {
				if got != c.Name {
					t.Errorf("%s names fixture %q", file, got)
				}
			}
		})
		t.Run(c.Name+" lists every record of the window but the OS ones, in order", func(t *testing.T) {
			var want, got []string
			for _, e := range c.Events {
				if c.InWindow(e.T) && !strings.HasPrefix(string(e.Kind()), "os.") {
					want = append(want, e.EventID)
				}
			}
			for _, r := range c.Side.Records {
				got = append(got, r.EventID)
				e, ok := c.EventByID(r.EventID)
				if ok && (e.T != r.T || e.Kind() != r.Kind || e.Via != r.Via) {
					t.Errorf("record %s: t, kind and via are %d, %s, %s; the event has %d, %s, %s",
						r.EventID, r.T, r.Kind, r.Via, e.T, e.Kind(), e.Via)
				}
			}
			if !slices.Equal(got, want) {
				t.Errorf("records %q, want %q", got, want)
			}
		})
		t.Run(c.Name+" a record opens an anchor exactly when anchors.json has it, with valid labels", func(t *testing.T) {
			anchors := map[string]Anchor{}
			for _, a := range c.Anchors.Anchors {
				anchors[a.EventID] = a
			}
			opened := 0
			for _, r := range c.Side.Records {
				a, listed := anchors[r.EventID]
				if r.Anchor != listed {
					t.Errorf("record %s (%s %s): anchor %v, but anchors.json lists it: %v", r.EventID, r.Kind, r.Local, r.Anchor, listed)
					continue
				}
				if !r.Anchor {
					if r.Level != "" || r.Labels != nil {
						t.Errorf("record %s (%s %s): only an anchor has a level and labels", r.EventID, r.Kind, r.Local)
					}
					continue
				}
				opened++
				if r.Level != a.Level {
					t.Errorf("record %s (%s): level %q, anchors.json says %q", r.EventID, r.Local, r.Level, a.Level)
				}
				// event.Anchor checks the label rules: a track, plugin or
				// bridge anchor carries its via_* label, and no other does.
				ea := event.Anchor{AnchorID: a.EventID, Kind: a.Kind, T: a.T, ProjectKey: a.ProjectKey, LeadS: a.LeadS,
					TrailS: a.TrailS, Via: a.Via, Labels: r.Labels}
				if err := ea.Validate(); err != nil {
					t.Errorf("record %s (%s): %v", r.EventID, r.Local, err)
				}
			}
			if opened != len(anchors) {
				t.Errorf("%d records open an anchor, anchors.json has %d", opened, len(anchors))
			}
		})
		t.Run(c.Name+" D19's dropped records add nothing to logged_s or ai_logged_s, and the rest sum to the totals", func(t *testing.T) {
			var logged, ai float64
			for _, r := range c.Side.Records {
				if !countsTowardLogged(r.Via) && (r.LoggedS != 0 || r.AILoggedS != 0) {
					t.Errorf("record %s (%s, via %s) adds %v and %v; D19 drops it", r.EventID, r.Kind, r.Via, r.LoggedS, r.AILoggedS)
				}
				for _, s := range []float64{r.LoggedS, r.AILoggedS} {
					if s < 0 || s > 120 {
						t.Errorf("record %s adds %v s; a gap counts min(gap, 120 s)", r.EventID, s)
					}
				}
				logged += r.LoggedS
				ai += r.AILoggedS
			}
			for name, sum := range map[string]float64{"logged_s": logged, "ai_logged_s": ai} {
				want, ok := c.Expected.Totals.Get(name)
				if !ok || math.Abs(sum-want) > loggedTolerance {
					t.Errorf("records add %v to %s, totals say %v", sum, name, want)
				}
			}
		})
		t.Run(c.Name+" every track call's outcome follows DESIGN 5.4's rules", func(t *testing.T) {
			for _, r := range c.Side.Records {
				e, ok := c.EventByID(r.EventID)
				if !ok {
					continue
				}
				tr, isTrack := e.Data.(*event.CLITrack)
				if !isTrack {
					if r.Track != "" || len(r.Flags) > 0 {
						t.Errorf("record %s (%s): only a track call has a track outcome or a flag here", r.EventID, r.Kind)
					}
					continue
				}
				// Does pairtime see the session through hooks or a log reader,
				// and how far is the session's nearest hook or log prompt?
				seen, nearest := false, int64(-1)
				for _, o := range c.Events {
					if o.SessionKey != e.SessionKey || o.Via != event.ViaHook && o.Via != event.ViaLog {
						continue
					}
					seen = true
					if o.Kind() == event.KindPromptSubmit {
						d := o.T - e.T
						if d < 0 {
							d = -d
						}
						if nearest < 0 || d < nearest {
							nearest = d
						}
					}
				}
				where := fmt.Sprintf("track call %s (%s)", r.EventID, r.Local)
				switch r.Track {
				case "anchor":
					// Condition 4: no hooks and no log reader.
					if !r.Anchor || seen || tr.LogMatch != event.LogNoReader || len(r.Flags) > 0 {
						t.Errorf("%s: an anchor needs a session no hook or log reader sees", where)
					}
				case "matched":
					if r.Anchor || !seen || nearest < 0 || nearest > trackDedupeMS || len(r.Flags) > 0 {
						t.Errorf("%s: track_matched needs a hook or log prompt within TRACK_DEDUPE_S, and opens nothing", where)
					}
				case "without_prompt":
					if r.Anchor || !seen || nearest >= 0 && nearest <= trackLogMatchMS ||
						!slices.Equal(r.Flags, []string{"track_without_prompt"}) {
						t.Errorf("%s: track_without_prompt needs a seen session with no prompt within TRACK_LOG_MATCH_S", where)
					}
				default:
					t.Errorf("%s: track outcome %q", where, r.Track)
				}
			}
		})
	}
}

// TestSideDoorTurns checks sidedoor.json's turns: each starts at a prompt or
// a track call of the case, and a turn only side-door records stand behind
// fills its bucket by at most R_TURN_BASE_S (DESIGN 5.2, 5.4).
func TestSideDoorTurns(t *testing.T) {
	for _, c := range loadSideDoor(t) {
		t.Run(c.Name+" every turn starts at a prompt or a track call of its session", func(t *testing.T) {
			for i, tr := range c.Side.Turns {
				e, ok := c.EventByID(tr.OpenedBy)
				if !ok {
					t.Errorf("turn %d: no event %s", i, tr.OpenedBy)
					continue
				}
				if k := e.Kind(); k != event.KindPromptSubmit && k != event.KindCLITrack {
					t.Errorf("turn %d: starts at a %s", i, k)
				}
				if e.T != tr.T || e.SessionKey != tr.SessionKey || e.Via != tr.Via {
					t.Errorf("turn %d: t, session and via are %d, %s, %s; the event has %d, %s, %s",
						i, tr.T, tr.SessionKey, tr.Via, e.T, e.SessionKey, e.Via)
				}
				if p, ok := e.Data.(*event.PromptSubmit); ok && p.TurnKey != tr.TurnKey {
					t.Errorf("turn %d: turn_key %s, the prompt's is %s", i, tr.TurnKey, p.TurnKey)
				}
				if tr.FillS < 0 || tr.OutputFillS < 0 {
					t.Errorf("turn %d: negative fill", i)
				}
			}
		})
		t.Run(c.Name+" a turn no hook or transcript backs fills at most R_TURN_BASE_S", func(t *testing.T) {
			for i, tr := range c.Side.Turns {
				if tr.Backed != (tr.Via == event.ViaHook || tr.Via == event.ViaLog) {
					t.Errorf("turn %d: backed %v for a turn that arrived via %s", i, tr.Backed, tr.Via)
				}
				if !tr.Backed && tr.FillS > rTurnBaseS {
					t.Errorf("turn %d (%s): fills %v s, more than R_TURN_BASE_S", i, tr.Local, tr.FillS)
				}
			}
		})
	}
}

// TestSideDoorAcceptance checks the expected values the WP-FX4 entry in
// docs/PLAN.md states, and DESIGN 5.2's 0 submittable seconds for a track
// loop with a jiggler.
func TestSideDoorAcceptance(t *testing.T) {
	cases := map[string]sideDoorCase{}
	for _, c := range loadSideDoor(t) {
		cases[strings.TrimPrefix(c.Name, sideDoorDir+"/")] = c
	}
	total := func(c sideDoorCase, k string) float64 {
		v, _ := c.Expected.Totals.Get(k)
		return v
	}
	only := func(level string) func(sideDoorCase) error {
		return func(c sideDoorCase) error {
			if cr := c.Expected.Totals.CreditedS; cr <= 0 || total(c, level) != cr {
				return fmt.Errorf("%s is %v of %v credited", level, total(c, level), cr)
			}
			return nil
		}
	}
	addsNoLogged := func(c sideDoorCase) error {
		if l, a := total(c, "logged_s"), total(c, "ai_logged_s"); l != 0 || a != 0 {
			return fmt.Errorf("logged_s %v, ai_logged_s %v", l, a)
		}
		return nil
	}
	noSubmittable := func(c sideDoorCase) error {
		// min(level_c_s, ai_logged_s / 3) is the most any program's policy
		// lets through (DESIGN 5.2, 6.2 D5).
		if v, ok := c.Expected.Totals.Get("level_c_cap_s"); !ok || v != 0 {
			return fmt.Errorf("level_c_cap_s %v", v)
		}
		return nil
	}
	fillsAtMostBase := func(c sideDoorCase) error {
		if len(c.Side.Turns) == 0 {
			return errors.New("no turns")
		}
		for _, tr := range c.Side.Turns {
			if tr.FillS > rTurnBaseS {
				return fmt.Errorf("the turn at %s fills %v s", tr.Local, tr.FillS)
			}
		}
		return nil
	}
	trackCall := func(outcome string, flags []string) func(sideDoorCase) error {
		return func(c sideDoorCase) error {
			n := 0
			for _, r := range c.Side.Records {
				if r.Kind != event.KindCLITrack {
					continue
				}
				n++
				if r.Anchor || r.Track != outcome || !slices.Equal(r.Flags, flags) {
					return fmt.Errorf("the call at %s: anchor %v, outcome %q, flags %q", r.Local, r.Anchor, r.Track, r.Flags)
				}
			}
			if n != 1 {
				return fmt.Errorf("%d track calls, want 1", n)
			}
			if v := total(c, "via_track_s"); v != 0 {
				return fmt.Errorf("via_track_s %v", v)
			}
			return nil
		}
	}
	for _, tc := range []struct {
		name  string
		dir   string
		check func(sideDoorCase) error
	}{
		{"(a) a track loop with a jiggler earns level-C credit only", "a-track-jiggler", only("level_c_s")},
		{"(a) the track loop adds 0 to logged_s and ai_logged_s", "a-track-jiggler", addsNoLogged},
		{"(a) the track loop earns 0 submittable seconds whatever the program's policy", "a-track-jiggler", noSubmittable},
		{"(b) key presses in the host window before each call make the track loop level A", "b-track-keys", only("level_a_s")},
		{"(c) plugin output sizes earn level-C credit only", "c-plugin-sizes", only("level_c_s")},
		{"(c) a plugin turn fills at most R_TURN_BASE_S", "c-plugin-sizes", fillsAtMostBase},
		{"(c) plugin records add 0 to logged_s and ai_logged_s", "c-plugin-sizes", addsNoLogged},
		{"(d) scripted editor heartbeats earn level-C credit only", "d-editor-script", only("level_c_s")},
		{"(d) editor_unverified heartbeats add 0 to logged_s and ai_logged_s", "d-editor-script", addsNoLogged},
		{"(e) bridge records earn level-C credit only", "e-bridge-lines", only("level_c_s")},
		{"(e) a bridge turn fills at most R_TURN_BASE_S", "e-bridge-lines", fillsAtMostBase},
		{"(f) a track call 4 s after a hook prompt is track_matched and opens nothing", "f-track-matched",
			trackCall("matched", []string{})},
		{"(g) a track call with no prompt within 10 s opens nothing and raises track_without_prompt",
			"g-track-without-prompt", trackCall("without_prompt", []string{"track_without_prompt"})},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, ok := cases[tc.dir]
			if !ok {
				t.Fatalf("no case %s", tc.dir)
			}
			if err := tc.check(c); err != nil {
				t.Error(err)
			}
		})
	}
}

// TestSideDoorGenerators runs each case's gen.py into a temporary directory
// and checks that it writes the checked-in files byte for byte, as
// TestGeneratorsReproduce does for the SM fixtures.
func TestSideDoorGenerators(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		if os.Getenv("CI") != "" {
			t.Fatal("python3 is needed to check the side-door generators")
		}
		t.Skip("python3 not found")
	}
	for _, c := range loadSideDoor(t) {
		gen := filepath.Join(c.Dir, "gen.py")
		t.Run(c.Name+" gen.py writes the checked-in files", func(t *testing.T) {
			out := t.TempDir()
			cmd := exec.Command(python, "-I", "-B", gen, "--out", out)
			if b, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("%s: %v\n%s", gen, err, b)
			}
			for _, name := range []string{EventsFile, ExpectedFile, AnchorsFile, PromptsFile, sideDoorFile} {
				got, err := os.ReadFile(filepath.Join(out, name))
				if err != nil {
					t.Fatal(err)
				}
				want, err := os.ReadFile(filepath.Join(c.Dir, name))
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
