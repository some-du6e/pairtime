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

	"github.com/some-du6e/pairtime/internal/jsonschema"
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
				v, err := jsonschema.Decode(line)
				if err != nil {
					t.Fatalf("%s line %d: %v", EventsFile, i+1, err)
				}
				if errs := sv.Validate(v); len(errs) > 0 {
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

// TestEventStream checks what holds across the events: one device; seq
// rising by 1, because a fixture's ledger holds only events; arrival order
// matching time order; and ids unique and carrying the event's time.
func TestEventStream(t *testing.T) {
	for _, f := range loadAll(t) {
		t.Run(f.Name+" events are one device's stream in time order, and seq rises by 1 since a fixture's ledger holds only events", func(t *testing.T) {
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
						t.Errorf("%s: seq %d follows %d; a fixture's ledger holds only events, so seq rises by 1", where, e.Seq, prev.Seq)
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
		t.Run(f.Name+" rows tile the window and account for every second", func(t *testing.T) {
			for _, e := range rowErrors(f.Expected) {
				t.Error(e)
			}
		})
	}
}

// rowErrors returns every way exp's rows break the format: they tile the
// window, and each bucket row accounts for every second of its bucket
// minutes, its credit by reason or, where SM gives it without that split,
// by project with credit_codes for SM's reasons (README.md).
func rowErrors(exp Expected) []string {
	var errs []string
	errorf := func(format string, args ...any) { errs = append(errs, fmt.Sprintf(format, args...)) }
	if len(exp.Rows) == 0 {
		return []string{"no rows"}
	}
	if !exp.Rows[0].Start.Equal(exp.Window.Start) {
		errorf("rows start at %s, the window at %s", exp.Rows[0].Start, exp.Window.Start)
	}
	if end := exp.Rows[len(exp.Rows)-1].End(); !end.Equal(exp.Window.End) {
		errorf("rows end at %s, the window at %s", end, exp.Window.End)
	}
	for i, r := range exp.Rows {
		where := fmt.Sprintf("row %d (%s)", i, r.Local)
		if r.Minutes < 1 {
			errorf("%s: minutes = %d", where, r.Minutes)
		}
		if r.Start.Location() != time.UTC || !r.Start.Truncate(time.Minute).Equal(r.Start) {
			errorf("%s: start %s is not a whole UTC minute", where, r.Start)
		}
		if i > 0 && !exp.Rows[i-1].End().Equal(r.Start) {
			errorf("%s: starts at %s, but the row before ends at %s", where, r.Start, exp.Rows[i-1].End())
		}
		if r.Erratum != "" && !slices.Contains(exp.Errata, r.Erratum) {
			errorf("%s: erratum %q isn't in the fixture's errata", where, r.Erratum)
		}
		for _, c := range r.Codes {
			if !c.Uncredited() {
				errorf("%s: code %q is not an uncredited reason", where, c)
			}
		}
		if !r.Bucket {
			if r.BucketMinutes != 0 || r.CreditedS != 0 || len(r.Credit) > 0 || len(r.CreditCodes) > 0 ||
				r.CreditByProject != nil || r.Uncredited != nil || r.AgentRunningS != 0 {
				errorf("%s: a minute without a bucket has no credit, no uncredited seconds and no agent time", where)
			}
			continue
		}
		if r.BucketMinutes < 1 || r.BucketMinutes > r.Minutes || r.Minutes == 1 && r.BucketMinutes != 1 {
			errorf("%s: bucket_minutes %d for %d minutes", where, r.BucketMinutes, r.Minutes)
		}
		// Only minutes with a bucket report seconds.
		span := 60 * float64(r.BucketMinutes)
		sum := 0.0
		for p, reasons := range r.Credit {
			if _, ok := exp.Projects[p]; !ok {
				errorf("%s: project %q isn't in the fixture's projects", where, p)
			}
			for k, s := range reasons {
				if !k.Credited() {
					errorf("%s: %q is not a credit reason", where, k)
				}
				if s <= 0 {
					errorf("%s: %s has %v s; rows list only reasons that credited something", where, k, s)
				}
				sum += s
			}
		}
		if len(r.CreditCodes) > 0 {
			if len(r.Credit) > 0 {
				errorf("%s: a row with credit_codes gives its credit by project alone, so its credit is {}", where)
			}
			for j, k := range r.CreditCodes {
				if !k.Credited() {
					errorf("%s: credit code %q is not a credit reason", where, k)
				}
				if slices.Contains(r.CreditCodes[:j], k) {
					errorf("%s: credit code %q is listed twice", where, k)
				}
			}
			for p, s := range r.CreditByProject {
				if _, ok := exp.Projects[p]; !ok {
					errorf("%s: project %q isn't in the fixture's projects", where, p)
				}
				if s <= 0 {
					errorf("%s: project %q has %v s; credit_by_project lists only projects that credited something",
						where, p, s)
				}
				sum += s
			}
		} else if r.CreditByProject != nil {
			errorf("%s: credit_by_project goes only with credit_codes", where)
		}
		if sum != r.CreditedS {
			errorf("%s: the credit sums to %v, credited_s is %v", where, sum, r.CreditedS)
		}
		if r.CreditedS > span {
			errorf("%s: %v s credited in %v s", where, r.CreditedS, span)
		}
		if r.AgentRunningS < 0 || r.AgentRunningS > span {
			errorf("%s: agent_running_s %v outside 0 to %v", where, r.AgentRunningS, span)
		}
		if r.Uncredited == nil {
			if len(r.Codes) == 0 {
				errorf("%s: with no uncredited seconds, the row lists the codes SM gives", where)
			}
			continue
		}
		if len(r.Codes) > 0 {
			errorf("%s: codes are only for rows whose uncredited seconds SM doesn't give", where)
		}
		unc := 0.0
		for k, s := range r.Uncredited {
			if !k.Uncredited() {
				errorf("%s: %q is not an uncredited reason", where, k)
			}
			if s <= 0 {
				errorf("%s: %s has %v s", where, k, s)
			}
			unc += s
		}
		if r.CreditedS+unc != span {
			errorf("%s: %v credited + %v uncredited = %v, want %v", where, r.CreditedS, unc, r.CreditedS+unc, span)
		}
	}
	return errs
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
			for _, e := range totalsErrors(exp) {
				t.Error(e)
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

// totalsErrors returns every way exp's totals aren't the sums of its rows.
// Unknown seconds have no sum, so the uncredited total is null when a
// bucket row's uncredited seconds are, and the total by reason is null when
// a bucket row gives its credit only by credit_codes. Each is null only
// then.
func totalsErrors(exp Expected) []string {
	var errs []string
	errorf := func(format string, args ...any) { errs = append(errs, fmt.Sprintf(format, args...)) }
	var credited, agent float64
	byProject := map[string]float64{}
	byReason := map[event.Reason]float64{}
	unc := map[event.Reason]float64{}
	var unknown, unsplit bool
	for _, r := range exp.Rows {
		credited += r.CreditedS
		agent += r.AgentRunningS
		for p, reasons := range r.Credit {
			for k, s := range reasons {
				byProject[p] += s
				byReason[k] += s
			}
		}
		for p, s := range r.CreditByProject {
			byProject[p] += s
		}
		for k, s := range r.Uncredited {
			unc[k] += s
		}
		unknown = unknown || r.Bucket && r.Uncredited == nil
		unsplit = unsplit || r.Bucket && len(r.CreditCodes) > 0
	}
	tot := exp.Totals
	if tot.CreditedS != credited {
		errorf("credited_s = %v, rows sum to %v", tot.CreditedS, credited)
	}
	if tot.AgentRunningS != agent {
		errorf("agent_running_s = %v, rows sum to %v", tot.AgentRunningS, agent)
	}
	if !maps.Equal(nonZero(tot.CreditedByProject), byProject) {
		errorf("credited_by_project = %v, rows sum to %v", tot.CreditedByProject, byProject)
	}
	switch {
	case unsplit && tot.CreditedByReason != nil:
		errorf("credited_by_reason = %v, but a row gives its credit only by credit_codes, so the total is null",
			tot.CreditedByReason)
	case !unsplit && tot.CreditedByReason == nil:
		errorf("credited_by_reason is null, but every row gives its credit by reason")
	case !unsplit && !maps.Equal(nonZero(tot.CreditedByReason), byReason):
		errorf("credited_by_reason = %v, rows sum to %v", tot.CreditedByReason, byReason)
	}
	switch {
	case unknown && tot.Uncredited != nil:
		errorf("uncredited = %v, but a row's uncredited seconds are unknown, so the total is null", tot.Uncredited)
	case !unknown && tot.Uncredited == nil:
		errorf("uncredited is null, but every row gives its uncredited seconds")
	case !unknown && !maps.Equal(nonZero(tot.Uncredited), unc):
		errorf("uncredited = %v, rows sum to %v", tot.Uncredited, unc)
	}
	if a, ok := tot.Get("agent_only_s"); ok && a > tot.AgentRunningS {
		errorf("agent_only_s %v is more than agent_running_s %v", a, tot.AgentRunningS)
	}
	return errs
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
// of a prompt of its class, so it has an origin and a class too. Its
// record keeps no text, so its class comes from its length and the
// question before it, and is never an error paste.
var promptAnchorClasses = map[event.AnchorKind][]event.PromptClass{
	event.AnchorSubstantivePrompt: {event.ClassSubstantive, event.ClassErrorPaste},
	event.AnchorAnswer:            {event.ClassAnswer},
	event.AnchorGoAhead:           {event.ClassLowEffort},
	event.AnchorQueuedMessage:     {event.ClassSubstantive, event.ClassAnswer, event.ClassLowEffort},
}

// TestAnchors checks anchors.json against the events.
func TestAnchors(t *testing.T) {
	for _, f := range loadAll(t) {
		t.Run(f.Name+" every anchor is an event of its kind", func(t *testing.T) {
			for _, e := range anchorErrors(f) {
				t.Error(e)
			}
		})
		t.Run(f.Name+" every turn is in the events", func(t *testing.T) {
			for _, e := range turnErrors(f) {
				t.Error(e)
			}
		})
	}
}

// anchorErrors returns every way f's anchors don't match its events. A
// track call opens its anchor on its cli.track record, classed like a
// prompt from its label but never an answer, and with no origin, since it
// is no prompt; its reading fill is R_TURN_BASE_S (DESIGN 5.4). Labels and
// corroborated, where anchors.json gives them, go into event.Anchor's
// rules.
func anchorErrors(f *Fixture) []string {
	var errs []string
	errorf := func(format string, args ...any) { errs = append(errs, fmt.Sprintf(format, args...)) }
	if f.Anchors.Fixture != f.Name {
		errorf("%s names fixture %q", AnchorsFile, f.Anchors.Fixture)
	}
	for i, a := range f.Anchors.Anchors {
		where := fmt.Sprintf("anchor %d (%s %s)", i, a.Kind, a.Local)
		e, ok := f.EventByID(a.EventID)
		if !ok {
			errorf("%s: no event %s", where, a.EventID)
			continue
		}
		track := a.Via == event.ViaTrack
		want := anchorEventKinds[a.Kind]
		if track {
			want = event.KindCLITrack
		}
		if e.Kind() != want {
			errorf("%s: event %s is %s, want %s", where, a.EventID, e.Kind(), want)
		}
		if e.T != a.T || e.ProjectKey != a.ProjectKey || e.Via != a.Via {
			errorf("%s: t, project_key and via are %d, %q, %q; the event has %d, %q, %q",
				where, a.T, a.ProjectKey, a.Via, e.T, e.ProjectKey, e.Via)
		}
		ea := event.Anchor{AnchorID: a.EventID, Kind: a.Kind, T: a.T, ProjectKey: a.ProjectKey,
			LeadS: a.LeadS, TrailS: a.TrailS, Via: a.Via, Labels: a.Labels,
			Corroborated: a.Corroborated != nil && *a.Corroborated}
		if err := ea.Validate(); err != nil {
			errorf("%s: %v", where, err)
		}
		classes, isPrompt := promptAnchorClasses[a.Kind]
		switch {
		case track:
			if a.Kind != event.AnchorSubstantivePrompt && a.Kind != event.AnchorGoAhead || a.Origin != "" ||
				!slices.Contains(classes, a.PromptClass) {
				errorf("%s: a track anchor is a substantive prompt or a go-ahead with its class and no origin, "+
					"got origin %q and class %q", where, a.Origin, a.PromptClass)
			}
		case isPrompt:
			if !a.Origin.Human() || !slices.Contains(classes, a.PromptClass) {
				errorf("%s: origin %q and class %q don't make a %s", where, a.Origin, a.PromptClass, a.Kind)
			}
		case a.Origin != "" || a.PromptClass != "":
			errorf("%s: only prompt and queued message anchors have an origin and a class", where)
		}
		if a.FillS != nil && (!track || *a.FillS != rTurnBaseS) {
			errorf("%s: fill_s %v; only a track anchor gives its fill, which is R_TURN_BASE_S", where, *a.FillS)
		}
		if a.LeadFrom != "sm" && a.LeadFrom != "params" {
			errorf("%s: lead_from %q, want sm or params", where, a.LeadFrom)
		}
		if a.Level != f.Expected.Evidence {
			errorf("%s: level %q, but the fixture's credit is level %q", where, a.Level, f.Expected.Evidence)
		}
		if i > 0 && a.T < f.Anchors.Anchors[i-1].T {
			errorf("%s: anchors are listed in time order", where)
		}
	}
	return errs
}

// turnErrors returns every way f's turns don't match its events. A turn's
// fills, where anchors.json gives them, are seconds, and a turn whose
// prompt no hook or transcript record backs fills at most R_TURN_BASE_S
// (DESIGN 5.2).
func turnErrors(f *Fixture) []string {
	var errs []string
	errorf := func(format string, args ...any) { errs = append(errs, fmt.Sprintf(format, args...)) }
	for i, tr := range f.Anchors.Turns {
		where := fmt.Sprintf("turn %d (%s)", i, tr.LocalSubmit)
		if !slices.Contains([]event.Origin{event.OriginScheduled, event.OriginSelfContinue, event.OriginSubagent,
			event.OriginHeadless, event.OriginCloud, event.OriginHumanTyped, event.OriginHumanPasted,
			event.OriginNoInput}, tr.Origin) {
			errorf("%s: origin %q", where, tr.Origin)
		}
		if tr.Origin.Human() != (tr.PromptClass != "") {
			errorf("%s: a prompt class goes with a human origin, got %q and %q", where, tr.Origin, tr.PromptClass)
		}
		var submit, stop bool
		var via event.Via
		for _, e := range f.Events {
			if e.SessionKey != tr.SessionKey {
				continue
			}
			switch p := e.Data.(type) {
			case *event.PromptSubmit:
				if p.TurnKey == tr.TurnKey && e.T == tr.TSubmit {
					submit, via = true, e.Via
				}
			case *event.AgentStop:
				stop = stop || p.TurnKey == tr.TurnKey && e.T == tr.TStop
			}
		}
		// A turn a stop hook continued starts when the turn before it
		// stops, and these fixtures give it no prompt of its own, which HS
		// 3.5 infers and asks to check (README.md, Turns). SM 2.2 rule 2
		// makes it self_continue either way.
		if !submit && tr.Origin != event.OriginSelfContinue {
			errorf("%s: no prompt.submit for turn %s at %d", where, tr.TurnKey, tr.TSubmit)
		}
		if tr.TStop != 0 && !stop {
			errorf("%s: no agent.stop for turn %s at %d", where, tr.TurnKey, tr.TStop)
		}
		for _, fill := range []*float64{tr.FillS, tr.OutputFillS} {
			if fill != nil && *fill < 0 {
				errorf("%s: a negative fill, %v s", where, *fill)
			}
		}
		if submit && via != event.ViaHook && via != event.ViaLog && tr.FillS != nil && *tr.FillS > rTurnBaseS {
			errorf("%s: fills %v s, but no hook or transcript record backs a turn its %s prompt opens, so it "+
				"fills at most R_TURN_BASE_S", where, *tr.FillS, via)
		}
	}
	return errs
}

// The rule tests below run the checks on made-up fixtures, for the parts of
// the format no fixture uses yet: one device, one project and one session,
// with rows from ruleStart.
const (
	ruleProject = "p_0123456789ab"
	ruleSession = "s_0123456789ab"
)

var ruleStart = time.UnixMilli(1795524000000).UTC()

// ruleEvent is a made-up record of ruleSession in ruleProject.
func ruleEvent(id string, at time.Duration, via event.Via, data event.Payload) event.Event {
	return event.Event{Envelope: event.Envelope{EventID: id, T: ruleStart.Add(at).UnixMilli(), Via: via,
		SessionKey: ruleSession, ProjectKey: ruleProject}, Data: data}
}

// ruleRow is a one-minute bucket row n minutes after ruleStart.
func ruleRow(n int) Row {
	return Row{Start: ruleStart.Add(time.Duration(n) * time.Minute), Minutes: 1, Local: fmt.Sprintf("12:%02d", n),
		Bucket: true, BucketMinutes: 1}
}

// someErr reports whether errs is empty when want is "", and otherwise
// whether some error says want.
func someErr(errs []string, want string) bool {
	if want == "" {
		return len(errs) == 0
	}
	return slices.ContainsFunc(errs, func(e string) bool { return strings.Contains(e, want) })
}

func TestRowRules(t *testing.T) {
	p := ruleProject
	split := func(r Row) Row {
		r.Credit = map[string]map[event.Reason]float64{p: {event.ReasonPrompt: 40}}
		r.CreditedS, r.Uncredited = 40, map[event.Reason]float64{event.ReasonAbsent: 20}
		return r
	}
	coded := func(r Row) Row {
		r.Credit = map[string]map[event.Reason]float64{}
		r.CreditCodes = []event.Reason{event.ReasonPrompt, event.ReasonSupervise}
		r.CreditByProject = map[string]float64{p: 40}
		r.CreditedS, r.Uncredited = 40, map[event.Reason]float64{event.ReasonAbsent: 20}
		return r
	}
	cases := []struct {
		name string
		edit func(Row) Row
		want string
	}{
		{"a row that splits its credit by reason and accounts for its minute passes", split, ""},
		{"a row SM gives without its split lists credit_codes and its seconds by project", coded, ""},
		{"a row with credit_codes keeps no credit by reason", func(r Row) Row {
			r = coded(r)
			r.Credit = map[string]map[event.Reason]float64{p: {event.ReasonPrompt: 40}}
			return r
		}, "its credit is {}"},
		{"a credit code is a credit reason", func(r Row) Row {
			r = coded(r)
			r.CreditCodes = []event.Reason{event.ReasonAbsent}
			return r
		}, "is not a credit reason"},
		{"a credit code is listed once", func(r Row) Row {
			r = coded(r)
			r.CreditCodes = []event.Reason{event.ReasonPrompt, event.ReasonPrompt}
			return r
		}, "listed twice"},
		{"credit_by_project sums to credited_s", func(r Row) Row {
			r = coded(r)
			r.CreditByProject = map[string]float64{p: 30}
			return r
		}, "the credit sums to 30"},
		{"credit_by_project names the fixture's projects", func(r Row) Row {
			r = coded(r)
			r.CreditByProject = map[string]float64{"p_ffffffffffff": 40}
			return r
		}, "isn't in the fixture's projects"},
		{"credit_by_project goes only with credit_codes", func(r Row) Row {
			r = split(r)
			r.CreditByProject = map[string]float64{p: 40}
			return r
		}, "goes only with credit_codes"},
		{"a minute without a bucket lists no credit codes", func(r Row) Row {
			r.Bucket, r.BucketMinutes, r.CreditCodes = false, 0, []event.Reason{event.ReasonPrompt}
			return r
		}, "without a bucket"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := tc.edit(ruleRow(0))
			exp := Expected{Projects: map[string]string{p: "project"},
				Window: Window{Start: ruleStart, End: ruleStart.Add(time.Minute)}, Rows: []Row{r}}
			if errs := rowErrors(exp); !someErr(errs, tc.want) {
				t.Errorf("errors %q, want %q", errs, tc.want)
			}
		})
	}
}

func TestTotalsRules(t *testing.T) {
	p := ruleProject
	known := func() []Row {
		a, b := ruleRow(0), ruleRow(1)
		a.Credit, a.CreditedS = map[string]map[event.Reason]float64{p: {event.ReasonPrompt: 40}}, 40
		a.Uncredited = map[event.Reason]float64{event.ReasonAbsent: 20}
		b.Credit, b.CreditedS = map[string]map[event.Reason]float64{p: {event.ReasonSupervise: 60}}, 60
		b.Uncredited = map[event.Reason]float64{}
		return []Row{a, b}
	}
	sums := Totals{CreditedS: 100, CreditedByProject: map[string]float64{p: 100},
		CreditedByReason: map[event.Reason]float64{event.ReasonPrompt: 40, event.ReasonSupervise: 60},
		Uncredited:       map[event.Reason]float64{event.ReasonAbsent: 20}}
	unknownRow := func() []Row {
		rows := known()
		rows[1].Uncredited, rows[1].Codes = nil, []event.Reason{event.ReasonAbsent}
		return rows
	}
	codedRow := func() []Row {
		rows := known()
		rows[1].Credit = map[string]map[event.Reason]float64{}
		rows[1].CreditCodes = []event.Reason{event.ReasonSupervise}
		rows[1].CreditByProject = map[string]float64{p: 60}
		return rows
	}
	with := func(edit func(*Totals)) Totals {
		tot := sums
		edit(&tot)
		return tot
	}
	cases := []struct {
		name string
		rows []Row
		tot  Totals
		want string
	}{
		{"totals that sum their rows pass", known(), sums, ""},
		{"a row of unknown uncredited seconds makes the uncredited total null", unknownRow(),
			with(func(x *Totals) { x.Uncredited = nil }), ""},
		{"a row of unknown uncredited seconds leaves no empty total", unknownRow(),
			with(func(x *Totals) { x.Uncredited = map[event.Reason]float64{} }), "the total is null"},
		{"a row of unknown uncredited seconds leaves no partial sum", unknownRow(), sums, "the total is null"},
		{"rows that all give their uncredited seconds keep their total", known(),
			with(func(x *Totals) { x.Uncredited = nil }), "uncredited is null"},
		{"a minute without a bucket leaves the uncredited total known", append(known(), Row{
			Start: ruleStart.Add(2 * time.Minute), Minutes: 1, Local: "12:02"}), sums, ""},
		{"a row given only by credit_codes makes the total by reason null", codedRow(),
			with(func(x *Totals) { x.CreditedByReason = nil }), ""},
		{"a row given only by credit_codes leaves no total by reason", codedRow(), sums, "so the total is null"},
		{"rows that all split their credit by reason keep their total", known(),
			with(func(x *Totals) { x.CreditedByReason = nil }), "credited_by_reason is null"},
		{"credit_by_project counts toward credited_by_project", codedRow(), with(func(x *Totals) {
			x.CreditedByReason, x.CreditedByProject = nil, map[string]float64{p: 40}
		}), "credited_by_project"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if errs := totalsErrors(Expected{Rows: tc.rows, Totals: tc.tot}); !someErr(errs, tc.want) {
				t.Errorf("errors %q, want %q", errs, tc.want)
			}
		})
	}
}

func TestAnchorRules(t *testing.T) {
	const prompt, track = "019aa0b8-6000-7000-8000-000000000001", "019aa0b8-6000-7000-8000-000000000002"
	events := []event.Event{
		ruleEvent(prompt, time.Minute, event.ViaHook, &event.PromptSubmit{TurnKey: "t_000000000001"}),
		ruleEvent(track, 2*time.Minute, event.ViaTrack, &event.CLITrack{}),
	}
	fill, more, yes := float64(rTurnBaseS), float64(rTurnBaseS+10), true
	hook := Anchor{EventID: prompt, Kind: event.AnchorSubstantivePrompt, T: events[0].T, Local: "12:01",
		ProjectKey: ruleProject, Origin: event.OriginHumanTyped, PromptClass: event.ClassSubstantive, LeadS: 120,
		LeadFrom: "params", Via: event.ViaHook, Level: event.EvidenceC}
	call := Anchor{EventID: track, Kind: event.AnchorSubstantivePrompt, T: events[1].T, Local: "12:02",
		ProjectKey: ruleProject, PromptClass: event.ClassSubstantive, LeadS: 40, LeadFrom: "params",
		Via: event.ViaTrack, Level: event.EvidenceC, Labels: []event.Label{event.LabelViaTrack}, FillS: &fill}
	cases := []struct {
		name string
		a    Anchor
		edit func(*Anchor)
		want string
	}{
		{"a hook prompt with a human origin and its class passes", hook, func(*Anchor) {}, ""},
		{"a corroborated hook anchor passes", hook, func(a *Anchor) { a.Corroborated = &yes }, ""},
		{"a track anchor stands on its cli.track record, with a class, no origin, via_track and R_TURN_BASE_S",
			call, func(*Anchor) {}, ""},
		{"a track anchor has no origin", call, func(a *Anchor) { a.Origin = event.OriginHumanTyped }, "a track anchor"},
		{"a track anchor is never an answer", call, func(a *Anchor) {
			a.Kind, a.PromptClass = event.AnchorAnswer, event.ClassAnswer
		}, "a track anchor"},
		{"a track anchor lists its via_track label", call, func(a *Anchor) { a.Labels = nil }, `"via_track"`},
		{"a track anchor's fill is R_TURN_BASE_S", call, func(a *Anchor) { a.FillS = &more }, "fill_s"},
		{"only a track anchor gives its fill on the anchor", hook, func(a *Anchor) { a.FillS = &fill }, "fill_s"},
		{"a track anchor can't be corroborated", call, func(a *Anchor) { a.Corroborated = &yes }, "corroborated"},
		{"a hook anchor stands on its prompt.submit, not a cli.track", hook, func(a *Anchor) {
			a.EventID, a.T, a.Local = track, events[1].T, "12:02"
		}, "want prompt.submit"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := tc.a
			tc.edit(&a)
			f := &Fixture{Name: "rules", Events: events, Expected: Expected{Evidence: event.EvidenceC},
				Anchors: Anchors{Fixture: "rules", Anchors: []Anchor{a}}}
			if errs := anchorErrors(f); !someErr(errs, tc.want) {
				t.Errorf("errors %q, want %q", errs, tc.want)
			}
		})
	}
}

func TestTurnRules(t *testing.T) {
	const hookPrompt, pluginPrompt = "019aa0b8-6000-7000-8000-000000000001", "019aa0b8-6000-7000-8000-000000000003"
	events := []event.Event{
		ruleEvent(hookPrompt, time.Minute, event.ViaHook, &event.PromptSubmit{TurnKey: "t_000000000001"}),
		ruleEvent(pluginPrompt, 3*time.Minute, event.ViaPlugin, &event.PromptSubmit{TurnKey: "t_000000000003"}),
	}
	turn := func(n int, key string) Turn {
		return Turn{SessionKey: ruleSession, TurnKey: key, TSubmit: events[n].T, LocalSubmit: fmt.Sprintf("12:0%d", 2*n+1),
			Origin: event.OriginHumanTyped, PromptClass: event.ClassSubstantive}
	}
	hook, plugin := turn(0, "t_000000000001"), turn(1, "t_000000000003")
	secs := func(v float64) *float64 { return &v }
	cases := []struct {
		name string
		tr   Turn
		edit func(*Turn)
		want string
	}{
		{"a turn's fills, where given, pass", hook, func(tr *Turn) { tr.FillS, tr.OutputFillS = secs(320), secs(300) }, ""},
		{"a fill is never negative", hook, func(tr *Turn) { tr.OutputFillS = secs(-1) }, "a negative fill"},
		{"a turn a hook prompt opens may fill more than R_TURN_BASE_S", hook,
			func(tr *Turn) { tr.FillS = secs(rTurnBaseS + 1) }, ""},
		{"a turn a plugin prompt opens fills R_TURN_BASE_S at most", plugin,
			func(tr *Turn) { tr.FillS = secs(rTurnBaseS) }, ""},
		{"a turn a plugin prompt opens fills no more than R_TURN_BASE_S", plugin,
			func(tr *Turn) { tr.FillS, tr.OutputFillS = secs(rTurnBaseS+1), secs(90) }, "at most R_TURN_BASE_S"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tr := tc.tr
			tc.edit(&tr)
			f := &Fixture{Name: "rules", Events: events, Anchors: Anchors{Fixture: "rules", Turns: []Turn{tr}}}
			if errs := turnErrors(f); !someErr(errs, tc.want) {
				t.Errorf("errors %q, want %q", errs, tc.want)
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
