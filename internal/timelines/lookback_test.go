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
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/some-du6e/pairtime/pkg/event"
)

// The lookback cases of WP-FX5 (docs/PLAN.md) are timelines under
// testdata/timelines/lookback, one directory per case: SM 2.8's lookback
// mode, where presence is unknown, applied by hand to transcript records.
// List doesn't descend into lookback, so the tests in timelines_test.go
// don't see these cases. The tests below check them the same way, check
// what SM 2.8 lets lookback credit, and check that each case's README works
// out the same rows and totals as its expected.json.
// testdata/timelines/lookback/README.md describes the cases.

const lookbackDir = "lookback"

// lookbackCases are the cases WP-FX5 asks for: SM 6.1 and 6.4 without their
// OS, editor and git events, and two sessions running in parallel.
var lookbackCases = []string{"6.1-transcript-only", "6.4-transcript-only", "two-sessions"}

// The SM 8 parameters the lookback rules read. WP-PA1's pkg/params will
// hold them.
const (
	lookbackComposeCPS  = 1.5 // COMPOSE_CPS
	lookbackComposeMaxS = 300 // COMPOSE_MAX_S
	lookbackPAMaxS      = 60  // UNKNOWN_PA_MAX_S
	lookbackPATrailS    = 10  // PA_TRAIL_S
	lookbackPAMinMS     = 300 // PA_MIN_LATENCY_MS
)

// lookbackReasons are the credit reasons lookback can give. A transcript
// has no editor, shell or git record and no presence, so nothing gives E,
// NAV, CMD, TEST, G or LOOKUP, and SM 2.8 credits reading only after a stop,
// so never SUP.
var lookbackReasons = []event.Reason{
	event.ReasonPrompt, event.ReasonAnswer, event.ReasonGoAhead, event.ReasonQueued,
	event.ReasonPermission, event.ReasonInterrupt, event.ReasonRead,
}

// lookbackKinds are the event kinds a transcript reader writes.
var lookbackKinds = []event.Kind{
	event.KindSessionStart, event.KindSessionEnd, event.KindPromptSubmit, event.KindAgentOutput,
	event.KindAgentTool, event.KindAgentPermission, event.KindAgentInterrupt, event.KindAgentQueue,
	event.KindAgentStop, event.KindAgentSubagent,
}

// The headers of the two kinds of table a lookback README works its rows
// out in.
const (
	lookbackMinuteHeader = "| Minute | Credited | Reasons | Arithmetic |"
	lookbackTotalHeader  = "| Total | Seconds | Arithmetic |"
)

func loadLookback(t *testing.T) []*Fixture {
	t.Helper()
	root, err := Root()
	if err != nil {
		t.Fatal(err)
	}
	var out []*Fixture
	for _, c := range lookbackCases {
		f, err := Load(root, lookbackDir+"/"+c)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, f)
	}
	return out
}

// TestLookbackCasesPresent pins the cases WP-FX5 asks for, so one can't
// vanish from the scorer's oracle without a test failing.
func TestLookbackCasesPresent(t *testing.T) {
	root, err := Root()
	if err != nil {
		t.Fatal(err)
	}
	t.Run("every case directory under lookback is one of WP-FX5's cases", func(t *testing.T) {
		dirs, err := List(filepath.Join(root, lookbackDir))
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(dirs, lookbackCases) {
			t.Errorf("lookback holds %q, the cases are %q", dirs, lookbackCases)
		}
	})
	for _, c := range lookbackCases {
		t.Run(c+" has every file of a lookback case", func(t *testing.T) {
			for _, name := range []string{EventsFile, ExpectedFile, AnchorsFile, PromptsFile, "README.md", "gen.py"} {
				if _, err := os.Stat(filepath.Join(root, lookbackDir, c, name)); err != nil {
					t.Error(err)
				}
			}
		})
	}
}

// TestLookbackEvents checks each case's events as TestEventsValidate,
// TestEventsMatchSchema, TestEventsRoundTrip and TestEventStream check the
// SM fixtures', and that they are what a transcript reader writes.
func TestLookbackEvents(t *testing.T) {
	sv := loadEventSchema(t)
	for _, f := range loadLookback(t) {
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
		t.Run(f.Name+" events are one device's gapless stream from seq 1, in time order", func(t *testing.T) {
			ids := map[string]bool{}
			for i, e := range f.Events {
				where := fmt.Sprintf("%s line %d", EventsFile, i+1)
				if e.DeviceID != f.Expected.Device {
					t.Errorf("%s: device_id %q, want the case's device %q", where, e.DeviceID, f.Expected.Device)
				}
				if i == 0 && e.Seq != 1 {
					t.Errorf("%s: seq %d; lookback runs at the agent's first start, so its records start at 1", where, e.Seq)
				}
				if i > 0 {
					prev := f.Events[i-1]
					if e.Seq != prev.Seq+1 {
						t.Errorf("%s: seq %d follows %d; seq is gapless", where, e.Seq, prev.Seq)
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
		t.Run(f.Name+" a transcript holds only harness events, and they arrive via log", func(t *testing.T) {
			for i, e := range f.Events {
				where := fmt.Sprintf("%s line %d", EventsFile, i+1)
				if !slices.Contains(lookbackKinds, e.Kind()) {
					t.Errorf("%s: a %s event; lookback reads transcripts, which hold none", where, e.Kind())
				}
				if e.Source != event.SourceLog || e.Via != event.ViaLog {
					t.Errorf("%s: source %q and via %q; no hook ran before pairtime, so the log reader wrote it", where, e.Source, e.Via)
				}
				if e.Harness == event.HarnessNone || e.SessionKey == event.NoSession {
					t.Errorf("%s: every transcript record belongs to a harness session", where)
				}
			}
		})
	}
}

// TestLookbackRows checks the rows and totals as TestRows and TestTotals do
// for the SM fixtures, and the lookback rules for what a row may hold.
func TestLookbackRows(t *testing.T) {
	root, err := Root()
	if err != nil {
		t.Fatal(err)
	}
	errata := filepath.Join(root, "..", "..", "docs", "errata")
	for _, f := range loadLookback(t) {
		exp := f.Expected
		t.Run(f.Name+" is level C and backfilled, with no presence data and no SM total", func(t *testing.T) {
			if exp.Fixture != f.Name {
				t.Errorf("%s names fixture %q", ExpectedFile, exp.Fixture)
			}
			if exp.Evidence != event.EvidenceC || exp.Witness != event.Backfilled {
				t.Errorf("evidence %q and witness %q; lookback credit is level C and backfilled (SM 2.8)", exp.Evidence, exp.Witness)
			}
			if exp.InputKeys {
				t.Error("input_keys is true, but no presence sample exists in lookback")
			}
			if len(exp.Stated) != 0 {
				t.Errorf("stated %q; SM states no lookback totals, so every total is a sum or a count", exp.Stated)
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
				span := 60 * float64(r.BucketMinutes)
				sum := 0.0
				for p, reasons := range r.Credit {
					if _, ok := exp.Projects[p]; !ok {
						t.Errorf("%s: project %q isn't in the case's projects", where, p)
					}
					for k, s := range reasons {
						if !slices.Contains(lookbackReasons, k) {
							t.Errorf("%s: %q, which lookback can't credit; it gives only %q", where, k, lookbackReasons)
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
					// SM decides no code for some of the row's seconds.
					if len(r.Codes) == 0 || r.Erratum == "" {
						t.Errorf("%s: a row whose codes SM doesn't decide lists the candidates and names the erratum that says so", where)
					}
					continue
				}
				if len(r.Codes) > 0 {
					t.Errorf("%s: codes are only for rows whose uncredited seconds SM doesn't decide", where)
				}
				unc := 0.0
				for k, s := range r.Uncredited {
					if !k.Uncredited() || s <= 0 {
						t.Errorf("%s: uncredited %s with %v s", where, k, s)
					}
					unc += s
				}
				if r.CreditedS+unc != span {
					t.Errorf("%s: %v credited + %v uncredited = %v, want %v", where, r.CreditedS, unc, r.CreditedS+unc, span)
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
			a, ok := tot.Get("agent_only_s")
			if !ok || a > tot.AgentRunningS || a < unc[event.ReasonAgentOnly] {
				t.Errorf("agent_only_s %v (present: %v) must be between the rows' X.agent_only %v and agent_running_s %v",
					a, ok, unc[event.ReasonAgentOnly], tot.AgentRunningS)
			}
		})
		t.Run(f.Name+" every erratum named exists", func(t *testing.T) {
			if len(exp.Errata) == 0 {
				t.Error("no errata; SM 2.8 leaves the codes of some lookback seconds open, so every case names one")
			}
			for _, name := range exp.Errata {
				if _, err := os.Stat(filepath.Join(errata, name+".md")); err != nil {
					t.Errorf("erratum %q: %v", name, err)
				}
			}
		})
	}
}

// TestLookbackAnchors checks anchors.json against the events, as TestAnchors
// does, and against SM 2.8: every anchor is a prompt or a permission answer
// at level C, a prompt's lead is its typing time only, and a permission
// answer keeps its wait up to UNKNOWN_PA_MAX_S.
func TestLookbackAnchors(t *testing.T) {
	for _, f := range loadLookback(t) {
		t.Run(f.Name+" every anchor is a prompt or a permission answer, at level C", func(t *testing.T) {
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
				if a.Level != event.EvidenceC {
					t.Errorf("%s: level %q; everything lookback credits is level C (SM 2.8)", where, a.Level)
				}
				if a.LeadFrom != "params" {
					t.Errorf("%s: lead_from %q; SM states no lookback lead, so each comes from SM 2.8 and SM 8", where, a.LeadFrom)
				}
				if i > 0 && a.T < f.Anchors.Anchors[i-1].T {
					t.Errorf("%s: anchors are listed in time order", where)
				}
				switch p := e.Data.(type) {
				case *event.PromptSubmit:
					if a.Origin != event.OriginHumanTyped {
						t.Errorf("%s: origin %q; SM 2.8 treats every prompt rules 1 to 5 don't label as human_typed", where, a.Origin)
					}
					if !slices.Contains(promptAnchorClasses[a.Kind], a.PromptClass) {
						t.Errorf("%s: class %q doesn't make a %s", where, a.PromptClass, a.Kind)
					}
					typing := math.Min(float64(p.TypedLenEst)/lookbackComposeCPS, lookbackComposeMaxS)
					if want := math.Ceil(typing); a.LeadS != want {
						t.Errorf("%s: lead_s %v; SM 2.8 leaves only the typing time, %d / 1.5 = %v s, rounded up to %v",
							where, a.LeadS, p.TypedLenEst, typing, want)
					}
					if a.TrailS != 0 {
						t.Errorf("%s: trail_s %v; a prompt has no trail (SM 2.3)", where, a.TrailS)
					}
				case *event.AgentPermission:
					wait := float64(p.AnsweredAt-p.RequestedAt) / 1000
					if want := math.Min(wait, lookbackPAMaxS); a.LeadS != want {
						t.Errorf("%s: lead_s %v; SM 2.8 keeps the %v s wait up to UNKNOWN_PA_MAX_S, %v s", where, a.LeadS, wait, want)
					}
					if a.TrailS != lookbackPATrailS {
						t.Errorf("%s: trail_s %v; the case keeps SM 2.3's PA_TRAIL_S (the lookback erratum's point 5)", where, a.TrailS)
					}
					if a.Origin != "" || a.PromptClass != "" {
						t.Errorf("%s: only prompt anchors have an origin and a class", where)
					}
				default:
					t.Errorf("%s: a %s anchor; from a transcript, lookback has only prompts and permission answers", where, a.Kind)
				}
			}
		})
		t.Run(f.Name+" every human prompt and every person's permission answer is an anchor", func(t *testing.T) {
			anchored := map[string]bool{}
			for _, a := range f.Anchors.Anchors {
				anchored[a.EventID] = true
			}
			automated := map[string]bool{}
			for _, tr := range f.Anchors.Turns {
				if !tr.Origin.Human() {
					automated[tr.TurnKey] = true
				}
			}
			for i, e := range f.Events {
				switch p := e.Data.(type) {
				case *event.PromptSubmit:
					if !automated[p.TurnKey] && !anchored[e.EventID] {
						t.Errorf("%s line %d: a prompt with no anchor; SM 2.8 makes every prompt rules 1 to 5 don't label human_typed", EventsFile, i+1)
					}
				case *event.AgentPermission:
					if p.DecidedBy == event.DecidedByUser && p.AnsweredAt-p.RequestedAt >= lookbackPAMinMS && !anchored[e.EventID] {
						t.Errorf("%s line %d: a permission answer with no anchor; SM 2.8 keeps their waits", EventsFile, i+1)
					}
				}
			}
		})
		t.Run(f.Name+" every turn is in the events", func(t *testing.T) {
			for i, tr := range f.Anchors.Turns {
				where := fmt.Sprintf("turn %d (%s)", i, tr.LocalSubmit)
				if tr.Origin.Human() != (tr.PromptClass != "") {
					t.Errorf("%s: a prompt class goes with a human origin, got %q and %q", where, tr.Origin, tr.PromptClass)
				}
				if tr.Origin != event.OriginHumanTyped && tr.Origin != event.OriginSelfContinue {
					t.Errorf("%s: origin %q; these cases have human_typed prompts and stop-hook continuations only", where, tr.Origin)
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

// TestLookbackPrompts checks prompts.json against the events, as
// TestPrompts does.
func TestLookbackPrompts(t *testing.T) {
	for _, f := range loadLookback(t) {
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
					t.Errorf("%s: source missing; it says where the text comes from", where)
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

// TestLookbackGenerators runs each case's gen.py into a temporary directory
// and checks that it writes the checked-in files byte for byte. gen.py also
// checks its rows against a second-by-second replay of SM 2.8 and its
// README's tables against its rows, and fails if either disagrees. Without
// python3 the test is skipped, except in CI, where it fails.
func TestLookbackGenerators(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		if os.Getenv("CI") != "" {
			t.Fatal("python3 is needed to check the lookback generators")
		}
		t.Skip("python3 not found")
	}
	for _, f := range loadLookback(t) {
		t.Run(f.Name+" gen.py writes the checked-in files", func(t *testing.T) {
			gen := filepath.Join(f.Dir, "gen.py")
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

// TestLookbackREADME checks each case's hand arithmetic, the oracle WP-FX5
// asks for: every row of the README's minute tables credits what
// expected.json's row of that minute credits, and every total's arithmetic
// gives its seconds, which are expected.json's.
func TestLookbackREADME(t *testing.T) {
	for _, f := range loadLookback(t) {
		exp := f.Expected
		readme := filepath.Join(f.Dir, "README.md")
		byName := map[string]string{}
		for k, n := range exp.Projects {
			byName[n] = k
		}
		t.Run(f.Name+" every minute the README works out is expected.json's", func(t *testing.T) {
			rows := lookbackTables(t, readme, lookbackMinuteHeader)
			if len(rows) == 0 {
				t.Fatal("the README has no minute table")
			}
			byLocal := map[string]Row{}
			for _, r := range exp.Rows {
				byLocal[r.Local] = r
			}
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
				credited, err := strconv.ParseFloat(strings.ReplaceAll(cells[1], ",", ""), 64)
				if err != nil || credited != r.CreditedS {
					t.Errorf("README %s credits %q, %s %v", cells[0], cells[1], ExpectedFile, r.CreditedS)
				}
				got, err := lookbackReasonsCell(cells[2], exp.Projects, byName)
				if err != nil {
					t.Errorf("README %s: %v", cells[0], err)
					continue
				}
				if !lookbackCreditEqual(got, r.Credit) {
					t.Errorf("README %s credits %v, %s %v", cells[0], got, ExpectedFile, r.Credit)
				}
			}
		})
		t.Run(f.Name+" every total the README works out is expected.json's", func(t *testing.T) {
			rows := lookbackTables(t, readme, lookbackTotalHeader)
			seen := false
			for _, cells := range rows {
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

// lookbackTables returns the data rows of every table in the file at path
// whose header line starts with header, each as trimmed cells with the
// backticks taken out.
func lookbackTables(t *testing.T, path, header string) [][]string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var rows [][]string
	in := false
	for _, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(line, header) {
			in = true
			continue
		}
		if !strings.HasPrefix(line, "|") {
			in = false
			continue
		}
		if !in {
			continue
		}
		cells := strings.Split(strings.Trim(strings.TrimSpace(line), "|"), "|")
		for i, c := range cells {
			cells[i] = strings.TrimSpace(strings.ReplaceAll(c, "`", ""))
		}
		if strings.Trim(cells[0], "-: ") == "" {
			continue // the line under the header
		}
		rows = append(rows, cells)
	}
	return rows
}

// lookbackReasonsCell reads a minute table's Reasons cell: "none", or
// comma-separated items "REASON seconds", each with the project's name
// first when the case has more than one project.
func lookbackReasonsCell(cell string, projects, byName map[string]string) (map[string]map[event.Reason]float64, error) {
	out := map[string]map[event.Reason]float64{}
	if cell == "none" {
		return out, nil
	}
	for _, item := range strings.Split(cell, ",") {
		parts := strings.Fields(item)
		var p string
		switch {
		case len(parts) == 2 && len(projects) == 1:
			for k := range projects {
				p = k
			}
		case len(parts) == 3:
			k, ok := byName[parts[0]]
			if !ok {
				return nil, fmt.Errorf("%q names no project of the case", item)
			}
			p = k
		default:
			return nil, fmt.Errorf("%q is not REASON seconds, with the project first when there are several", item)
		}
		s, err := strconv.ParseFloat(parts[len(parts)-1], 64)
		if err != nil {
			return nil, fmt.Errorf("%q: %v", item, err)
		}
		if out[p] == nil {
			out[p] = map[event.Reason]float64{}
		}
		out[p][event.Reason(parts[len(parts)-2])] += s
	}
	return out, nil
}

// lookbackCreditEqual compares credit by project and reason, with a
// project that has no reasons the same as an absent one.
func lookbackCreditEqual(a, b map[string]map[event.Reason]float64) bool {
	trim := func(m map[string]map[event.Reason]float64) map[string]map[event.Reason]float64 {
		out := map[string]map[event.Reason]float64{}
		for p, r := range m {
			if len(r) > 0 {
				out[p] = r
			}
		}
		return out
	}
	a, b = trim(a), trim(b)
	if len(a) != len(b) {
		return false
	}
	for p, r := range a {
		if !maps.Equal(r, b[p]) {
			return false
		}
	}
	return true
}

// lookbackTotal returns the total a README's Total cell names: one of
// expected.json's totals, a credit reason's total, an uncredited code's or
// a project's, by its name in the case.
func lookbackTotal(exp Expected, key string) (float64, bool) {
	if v, ok := exp.Totals.Get(key); ok {
		return v, true
	}
	if v, ok := exp.Totals.CreditedByReason[event.Reason(key)]; ok {
		return v, true
	}
	if v, ok := exp.Totals.Uncredited[event.Reason(key)]; ok {
		return v, true
	}
	for k, name := range exp.Projects {
		if name == key {
			v, ok := exp.Totals.CreditedByProject[k]
			return v, ok
		}
	}
	return 0, false
}

// lookbackArithmetic evaluates a README total's arithmetic: whole numbers,
// with commas between thousands, joined by +, − or -, × and parentheses.
func lookbackArithmetic(expr string) (int64, error) {
	p := &lookbackArith{s: strings.NewReplacer("×", "*", "−", "-", ",", "").Replace(expr)}
	v, err := p.sum()
	if err != nil {
		return 0, fmt.Errorf("%q: %v", expr, err)
	}
	if p.peek() != 0 {
		return 0, fmt.Errorf("%q: unexpected %q", expr, p.s[p.i:])
	}
	return v, nil
}

// lookbackArith is a recursive-descent parser for lookbackArithmetic.
type lookbackArith struct {
	s string
	i int
}

// peek skips spaces and returns the next byte, or 0 at the end.
func (p *lookbackArith) peek() byte {
	for p.i < len(p.s) && p.s[p.i] == ' ' {
		p.i++
	}
	if p.i < len(p.s) {
		return p.s[p.i]
	}
	return 0
}

func (p *lookbackArith) sum() (int64, error) {
	v, err := p.product()
	if err != nil {
		return 0, err
	}
	for {
		op := p.peek()
		if op != '+' && op != '-' {
			return v, nil
		}
		p.i++
		w, err := p.product()
		if err != nil {
			return 0, err
		}
		if op == '+' {
			v += w
		} else {
			v -= w
		}
	}
}

func (p *lookbackArith) product() (int64, error) {
	v, err := p.factor()
	if err != nil {
		return 0, err
	}
	for p.peek() == '*' {
		p.i++
		w, err := p.factor()
		if err != nil {
			return 0, err
		}
		v *= w
	}
	return v, nil
}

func (p *lookbackArith) factor() (int64, error) {
	c := p.peek()
	if c == '(' {
		p.i++
		v, err := p.sum()
		if err != nil {
			return 0, err
		}
		if p.peek() != ')' {
			return 0, fmt.Errorf("no ) to close the ( before %q", p.s[p.i:])
		}
		p.i++
		return v, nil
	}
	if c < '0' || c > '9' {
		return 0, fmt.Errorf("want a number or ( at %q", p.s[p.i:])
	}
	j := p.i
	for p.i < len(p.s) && p.s[p.i] >= '0' && p.s[p.i] <= '9' {
		p.i++
	}
	return strconv.ParseInt(p.s[j:p.i], 10, 64)
}

// TestLookbackArithmetic checks the evaluator the README check relies on.
func TestLookbackArithmetic(t *testing.T) {
	for _, tc := range []struct {
		name string
		expr string
		want int64
		bad  bool
	}{
		{name: "a lone number is itself", expr: "345", want: 345},
		{name: "commas between thousands are ignored", expr: "5,055", want: 5055},
		{name: "× binds tighter than +", expr: "14 × 345 + 225", want: 5055},
		{name: "the minus sign subtracts, left to right", expr: "590 − 200 − 75", want: 315},
		{name: "a hyphen subtracts like the minus sign", expr: "590 - 275", want: 315},
		{name: "parentheses group", expr: "15 × (150 − 25)", want: 1875},
		{name: "a word is an error", expr: "15 × cycles", bad: true},
		{name: "an unclosed parenthesis is an error", expr: "15 × (150 − 25", bad: true},
		{name: "a trailing operator is an error", expr: "50 + 41 +", bad: true},
		{name: "a division sign is an error", expr: "61 / 1.5", bad: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := lookbackArithmetic(tc.expr)
			if tc.bad {
				if err == nil {
					t.Errorf("%q gave %d, want an error", tc.expr, got)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Errorf("%q = %d, %v; want %d", tc.expr, got, err, tc.want)
			}
		})
	}
}
