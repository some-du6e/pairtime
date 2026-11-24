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

// The relayed cases of WP-ADE1 are timelines under
// testdata/timelines/relayed, one directory per case: SM 2.2 rule 9's
// relayed prompts as D-relayed decides them
// (docs/errata/2026-11-22-relayed-prompts.md), worked out by hand before
// the scorer. List doesn't descend into relayed, so the tests in
// timelines_test.go don't see these cases. The tests below check them the
// same way, with the relayed anchor convention, check the records an agent
// app's reader writes and the links that pair them with prompts, and check
// that each case's README works out the same rows and totals as its
// expected.json. testdata/timelines/relayed/README.md describes the cases.

const relayedDir = "relayed"

// relayedCases are D-relayed's eight cases, in the order List returns them.
var relayedCases = []string{
	"agent-day", "t3-away", "t3-here", "t3-here-agent", "t3-late", "t3-phone", "t3-queued", "t3-unattended",
}

// relayedDesktop are the cases on a computer whose desktop pairtime
// samples, one that runs T3 Code's desktop app, which runs its own server.
// The others run on T3 Code's server, which has no desktop and samples
// nothing.
var relayedDesktop = []string{"t3-here", "t3-here-agent", "t3-phone"}

// relayedTyped are the cases whose credit a person typed on the computer
// that runs the agent: t3-here's message, which rules 6 and 7 make
// human_typed at the app's stamp, at level A. Every other case's credit is
// relayed, level C with via_relay, and the two that credit nothing say C.
var relayedTyped = []string{"t3-here"}

// relayedErratum is the decision the relayed cases are the oracle for.
const relayedErratum = "2026-11-22-relayed-prompts"

// The SM 8.1 parameters SM 2.2's pairing checks read, in milliseconds.
// The lead rule reads COMPOSE_CPS and COMPOSE_MAX_S, lookbackComposeCPS
// and lookbackComposeMaxS.
const (
	relayMatchMS   = 600_000 // RELAY_MATCH_S
	relaySeenLagMS = 60_000  // RELAY_SEEN_LAG_S
)

// relayedReasons are the credit reasons relayed credit can give: a
// prompt's, an answer's or a go-ahead's typing lead, and reading after a
// stop. Never SUP, since a relayed session reads only after a stop (SM 2.3,
// 2.4).
var relayedReasons = []event.Reason{event.ReasonPrompt, event.ReasonAnswer, event.ReasonGoAhead, event.ReasonRead}

// relayedCodes are SM 5.2's codes for a live slot whose presence is
// unknown, in their order, but X.no_input, which needs a no_input turn,
// and no relayed case has one (relayed.py refuses a prompt rule 4 lets
// through). On the desktop cases the person is present with a window
// hosting the session in front throughout, so their codes are these too.
var relayedCodes = []event.Reason{event.ReasonBucketCap, event.ReasonAgentOnly, event.ReasonAutomated,
	event.ReasonNoAnchor}

// relayedCountTotals are the totals a relayed case counts: its prompts by
// the origin anchors.json gives their turns, the cross-session subagents
// among them, and the relay records no link names.
var relayedCountTotals = []string{"relayed_prompts", "headless_prompts", "scheduled_prompts", "typed_prompts",
	"cross_session_prompts", "unpaired_relay_records"}

func loadRelayed(t *testing.T) []*Fixture {
	t.Helper()
	root, err := Root()
	if err != nil {
		t.Fatal(err)
	}
	var out []*Fixture
	for _, c := range relayedCases {
		f, err := Load(root, relayedDir+"/"+c)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, f)
	}
	return out
}

// relayedCase returns a case's directory name under relayed.
func relayedCase(f *Fixture) string { return strings.TrimPrefix(f.Name, relayedDir+"/") }

// relayedLink is a link that pairs a relay.message with a prompt.submit
// (SM 2.2 rule 9): the record's prompt, or the prompt's relay, which the
// agent writes on whichever of the two arrives second.
type relayedLink struct {
	relay, prompt event.Event
}

// relayedLinks returns the case's links that name an event of the other
// kind, in record order.
func relayedLinks(f *Fixture) []relayedLink {
	var out []relayedLink
	for _, e := range f.Events {
		switch d := e.Data.(type) {
		case *event.RelayMessage:
			if d.Prompt == "" {
				continue
			}
			if p, ok := f.EventByID(d.Prompt); ok && p.Kind() == event.KindPromptSubmit {
				out = append(out, relayedLink{relay: e, prompt: p})
			}
		case *event.PromptSubmit:
			if d.Relay == "" {
				continue
			}
			if r, ok := f.EventByID(d.Relay); ok && r.Kind() == event.KindRelayMessage {
				out = append(out, relayedLink{relay: r, prompt: e})
			}
		}
	}
	return out
}

// relayedTurnPrompt returns the prompt.submit that starts a turn of
// anchors.json.
func relayedTurnPrompt(f *Fixture, tr Turn) (event.Event, bool) {
	for _, e := range f.Events {
		if p, ok := e.Data.(*event.PromptSubmit); ok && e.SessionKey == tr.SessionKey && p.TurnKey == tr.TurnKey &&
			e.T == tr.TSubmit {
			return e, true
		}
	}
	return event.Event{}, false
}

// relayedPrevStamp returns the stamp of the last relay.message of declared
// none in m's thread sent before m, paired or not, which caps m's relayed
// lead (SM 2.3), and whether there is one.
func relayedPrevStamp(f *Fixture, m *event.RelayMessage) (int64, bool) {
	var prev int64
	found := false
	for _, e := range f.Events {
		o, ok := e.Data.(*event.RelayMessage)
		if ok && o.ThreadKey == m.ThreadKey && o.Declared == event.RelayDeclaredNone && o.SentAt < m.SentAt &&
			(!found || o.SentAt > prev) {
			prev, found = o.SentAt, true
		}
	}
	return prev, found
}

// relayedZone returns the fixed zone of a case's utc_offset, such as
// "-06:00", which its local times are in.
func relayedZone(offset string) (*time.Location, error) {
	bad := fmt.Errorf("utc_offset %q is not ±hh:mm", offset)
	if len(offset) != 6 || offset[0] != '+' && offset[0] != '-' || offset[3] != ':' {
		return nil, bad
	}
	h, errH := strconv.Atoi(offset[1:3])
	m, errM := strconv.Atoi(offset[4:6])
	if errH != nil || errM != nil {
		return nil, bad
	}
	s := (h*60 + m) * 60
	if offset[0] == '-' {
		s = -s
	}
	return time.FixedZone(offset, s), nil
}

// relayedLocal is t, in Unix milliseconds, as a case's local time of day.
func relayedLocal(t int64, z *time.Location) string {
	return time.UnixMilli(t).In(z).Format("15:04:05")
}

// TestRelayedCasesPresent pins the cases D-relayed's decision lists, so one
// can't vanish from the scorer's oracle without a test failing.
func TestRelayedCasesPresent(t *testing.T) {
	root, err := Root()
	if err != nil {
		t.Fatal(err)
	}
	t.Run("every case directory under relayed is one of D-relayed's cases", func(t *testing.T) {
		dirs, err := List(filepath.Join(root, relayedDir))
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(dirs, relayedCases) {
			t.Errorf("relayed holds %q, the cases are %q", dirs, relayedCases)
		}
	})
	t.Run("relayed holds its README and the replay its generators check their rows against", func(t *testing.T) {
		for _, name := range []string{"README.md", "relayed.py"} {
			if _, err := os.Stat(filepath.Join(root, relayedDir, name)); err != nil {
				t.Error(err)
			}
		}
	})
	for _, c := range relayedCases {
		t.Run(c+" has every file of a relayed case", func(t *testing.T) {
			for _, name := range []string{EventsFile, ExpectedFile, AnchorsFile, PromptsFile, "README.md", "gen.py"} {
				if _, err := os.Stat(filepath.Join(root, relayedDir, c, name)); err != nil {
					t.Error(err)
				}
			}
		})
	}
}

// TestRelayedEvents checks each case's events as TestEventsValidate,
// TestEventsMatchSchema, TestEventsRoundTrip and TestEventStream check the
// SM fixtures', and that they are what the cases' machines record: a T3
// Code server's hook records and its app reader's relay records, and on a
// desktop presence and focus too, with every pairing link SM 2.2's checks
// let pair.
func TestRelayedEvents(t *testing.T) {
	sv := loadEventSchema(t)
	for _, f := range loadRelayed(t) {
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
		t.Run(f.Name+" events are one device's stream in time order, and seq rises by 1 since a fixture's ledger holds only events", func(t *testing.T) {
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
		t.Run(f.Name+" relay records come from an agent app's reader via app, and nothing else does", func(t *testing.T) {
			for i, e := range f.Events {
				where := fmt.Sprintf("%s line %d", EventsFile, i+1)
				relay := e.Kind() == event.KindRelayMessage
				if relay != (e.Via == event.ViaApp) {
					t.Errorf("%s: a %s via %q; only the app reader's relay.message records come via app (SM 1.2)", where, e.Kind(), e.Via)
				}
				if relay && e.Source != event.SourceLog {
					t.Errorf("%s: source %q; the reader reads the app's own store, source log", where, e.Source)
				}
			}
		})
		t.Run(f.Name+" every session is one T3 Code's server started: entrypoint unknown, parent_class agent_app, no TTY", func(t *testing.T) {
			n := 0
			for i, e := range f.Events {
				st, ok := e.Data.(*event.SessionStart)
				if !ok {
					continue
				}
				n++
				if st.Entrypoint != event.EntrypointUnknown || st.ParentClass != event.ParentAgentApp || st.HasTTY {
					t.Errorf("%s line %d: entrypoint %q, parent_class %q, has_tty %v; T3 Code's server starts every session "+
						"(DESIGN 5.1)", EventsFile, i+1, st.Entrypoint, st.ParentClass, st.HasTTY)
				}
			}
			if n == 0 {
				t.Error("no session.start")
			}
		})
		if slices.Contains(relayedDesktop, relayedCase(f)) {
			t.Run(f.Name+" T3 Code's desktop app runs on a computer pairtime samples presence and focus on", func(t *testing.T) {
				kinds := map[event.Kind]bool{}
				for _, e := range f.Events {
					kinds[e.Kind()] = true
				}
				if !kinds[event.KindOSPresence] || !kinds[event.KindOSFocus] {
					t.Errorf("os.presence %v, os.focus %v; a desktop case has both", kinds[event.KindOSPresence], kinds[event.KindOSFocus])
				}
			})
		} else {
			t.Run(f.Name+" T3 Code's server has no desktop, so pairtime samples no presence, focus or power there", func(t *testing.T) {
				for i, e := range f.Events {
					if strings.HasPrefix(string(e.Kind()), "os.") {
						t.Errorf("%s line %d: a %s on a server that samples nothing", EventsFile, i+1, e.Kind())
					}
				}
			})
		}
		t.Run(f.Name+" every link names a record of the other kind that came before it, and no record is linked twice", func(t *testing.T) {
			linked := map[string]int{}
			for i, e := range f.Events {
				var target string
				var want event.Kind
				switch d := e.Data.(type) {
				case *event.RelayMessage:
					target, want = d.Prompt, event.KindPromptSubmit
				case *event.PromptSubmit:
					target, want = d.Relay, event.KindRelayMessage
				}
				if target == "" {
					continue
				}
				where := fmt.Sprintf("%s line %d (%s)", EventsFile, i+1, e.Kind())
				j := slices.IndexFunc(f.Events, func(o event.Event) bool { return o.EventID == target })
				switch {
				case j < 0:
					t.Errorf("%s: names %s, which isn't in the case", where, target)
					continue
				case f.Events[j].Kind() != want:
					t.Errorf("%s: names a %s, want a %s", where, f.Events[j].Kind(), want)
				case j >= i:
					t.Errorf("%s: names line %d, which came after it; the agent writes the link on whichever of the two "+
						"records arrives second (SM 2.2)", where, j+1)
				}
				linked[e.EventID]++
				linked[target]++
			}
			for _, id := range slices.Sorted(maps.Keys(linked)) {
				if linked[id] > 1 {
					t.Errorf("%s is linked %d times; a message pairs with one prompt and a prompt with one message", id, linked[id])
				}
			}
		})
		t.Run(f.Name+" every link passes SM 2.2's pairing checks", func(t *testing.T) {
			for _, l := range relayedLinks(f) {
				m := l.relay.Data.(*event.RelayMessage)
				where := fmt.Sprintf("the relay record sent at %s and the prompt at %s", relayedLocalUTC(m.SentAt),
					relayedLocalUTC(l.prompt.T))
				if l.relay.SessionKey != event.NoSession && l.relay.SessionKey != l.prompt.SessionKey {
					t.Errorf("%s: sessions %s and %s; a record names its prompt's session, or none", where, l.relay.SessionKey,
						l.prompt.SessionKey)
				}
				if l.prompt.Via != event.ViaHook && l.prompt.Via != event.ViaLog {
					t.Errorf("%s: the prompt came via %s; a relay record witnesses only a hook's or a log's (DESIGN 5.2)", where, l.prompt.Via)
				}
				if l.prompt.T < m.SentAt || l.prompt.T > m.SentAt+relayMatchMS {
					t.Errorf("%s: the prompt comes %d ms after the stamp, outside 0 to RELAY_MATCH_S", where, l.prompt.T-m.SentAt)
				}
				if l.relay.T < m.SentAt || l.relay.T > m.SentAt+relaySeenLagMS {
					t.Errorf("%s: the record comes %d ms after the stamp, outside 0 to RELAY_SEEN_LAG_S", where, l.relay.T-m.SentAt)
				}
			}
		})
	}
}

// relayedLocalUTC is t, in Unix milliseconds, as a UTC time of day, for
// test messages.
func relayedLocalUTC(t int64) string { return time.UnixMilli(t).UTC().Format("15:04:05Z") }

// TestRelayedRows checks the rows and totals as TestRows and TestTotals do
// for the SM fixtures, and D-relayed's rules for what a row may hold: every
// second has a reason or one of SM 5.2's codes for a slot whose presence is
// unknown, relayed credit comes only from leads and reading, and every
// credited second of a relayed case is level C with via_relay.
func TestRelayedRows(t *testing.T) {
	root, err := Root()
	if err != nil {
		t.Fatal(err)
	}
	errata := filepath.Join(root, "..", "..", "docs", "errata")
	levelTotal := map[event.Evidence]string{event.EvidenceA: "level_a_s", event.EvidenceB: "level_b_s",
		event.EvidenceC: "level_c_s"}
	for _, f := range loadRelayed(t) {
		exp := f.Expected
		typed := slices.Contains(relayedTyped, relayedCase(f))
		t.Run(f.Name+" is witnessed, level C unless a person typed its credit here, and states no SM total", func(t *testing.T) {
			if exp.Fixture != f.Name {
				t.Errorf("%s names fixture %q", ExpectedFile, exp.Fixture)
			}
			want := event.EvidenceC
			if typed {
				want = event.EvidenceA
			}
			if exp.Evidence != want || exp.Witness != event.Witnessed {
				t.Errorf("evidence %q and witness %q, want %q and witnessed; the agent uploads live, and relayed credit is level C "+
					"(SM 2.3)", exp.Evidence, exp.Witness, want)
			}
			if exp.InputKeys && !slices.Contains(relayedDesktop, relayedCase(f)) {
				t.Error("input_keys is true on T3 Code's server, which samples nothing")
			}
			if len(exp.Stated) != 0 || len(exp.Approx) != 0 {
				t.Errorf("stated %q and approx %v; SM states no relayed numbers, so every total is a sum or a count", exp.Stated, exp.Approx)
			}
		})
		t.Run(f.Name+" rows tile the window in whole minutes, and every second has a reason or a code", func(t *testing.T) {
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
				if r.Erratum != "" || len(r.Codes) > 0 {
					t.Errorf("%s: erratum %q and codes %q; D-relayed decides every second's code", where, r.Erratum, r.Codes)
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
						if !k.Credited() || s <= 0 {
							t.Errorf("%s: %s with %v s; rows list credit reasons that credited something", where, k, s)
						}
						if !typed && !slices.Contains(relayedReasons, k) {
							t.Errorf("%s: %q, which relayed credit can't give; it gives only %q", where, k, relayedReasons)
						}
						sum += s
					}
				}
				if sum != r.CreditedS {
					t.Errorf("%s: reasons sum to %v, credited_s is %v", where, sum, r.CreditedS)
				}
				if r.CreditedS > span || r.AgentRunningS < 0 || r.AgentRunningS > span {
					t.Errorf("%s: %v s credited and agent_running_s %v in %v s", where, r.CreditedS, r.AgentRunningS, span)
				}
				if r.Uncredited == nil {
					t.Errorf("%s: no uncredited seconds; D-relayed gives every second its code", where)
					continue
				}
				unc := 0.0
				for k, s := range r.Uncredited {
					if !slices.Contains(relayedCodes, k) || s <= 0 {
						t.Errorf("%s: uncredited %s with %v s; a relayed case's codes are SM 5.2's for a slot whose presence is "+
							"unknown, but X.no_input, %q", where, k, s, relayedCodes)
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
		})
		t.Run(f.Name+" every credited second is at the case's level, and relayed unless a person typed it here", func(t *testing.T) {
			tot := exp.Totals
			levels := 0.0
			for _, k := range levelTotal {
				if v, ok := tot.Get(k); ok {
					levels += v
				}
			}
			if levels != tot.CreditedS {
				t.Errorf("credit by level sums to %v, credited_s is %v", levels, tot.CreditedS)
			}
			if v, ok := tot.Get(levelTotal[exp.Evidence]); !ok || v != tot.CreditedS {
				t.Errorf("evidence %q, but %s is %v of %v", exp.Evidence, levelTotal[exp.Evidence], v, tot.CreditedS)
			}
			want := tot.CreditedS
			if typed {
				want = 0
			}
			if v, ok := tot.Get("via_relay_s"); !ok || v != want {
				t.Errorf("via_relay_s %v (present: %v), want %v: the seconds relayed anchors credit", v, ok, want)
			}
		})
		t.Run(f.Name+" every count is anchors.json's turns by origin, or the relay records no link names", func(t *testing.T) {
			got := map[string]float64{}
			relayOf := map[string]*event.RelayMessage{}
			linked := map[string]bool{}
			for _, l := range relayedLinks(f) {
				relayOf[l.prompt.EventID] = l.relay.Data.(*event.RelayMessage)
				linked[l.relay.EventID] = true
			}
			byOrigin := map[event.Origin]string{event.OriginRelayed: "relayed_prompts", event.OriginHeadless: "headless_prompts",
				event.OriginScheduled: "scheduled_prompts", event.OriginHumanTyped: "typed_prompts"}
			for _, tr := range f.Anchors.Turns {
				if k, ok := byOrigin[tr.Origin]; ok {
					got[k]++
				}
				if p, ok := relayedTurnPrompt(f, tr); ok && tr.Origin == event.OriginSubagent && relayOf[p.EventID] != nil &&
					relayOf[p.EventID].Declared == event.RelayDeclaredCrossSession {
					got["cross_session_prompts"]++
				}
			}
			for _, e := range f.Events {
				if e.Kind() == event.KindRelayMessage && !linked[e.EventID] {
					got["unpaired_relay_records"]++
				}
			}
			if _, ok := exp.Totals.Get("relayed_prompts"); !ok {
				t.Error("no relayed_prompts; every case counts its relayed prompts")
			}
			for _, k := range slices.Sorted(maps.Keys(exp.Totals.Other)) {
				switch v := exp.Totals.Other[k]; {
				case slices.Contains(relayedCountTotals, k):
					if v != got[k] {
						t.Errorf("%s is %v, the case has %v", k, v, got[k])
					}
				case k == "via_relay_s" || slices.Contains(slices.Collect(maps.Values(levelTotal)), k):
				default:
					t.Errorf("totals.%s: a relayed case's totals are sums, levels, via_relay_s and %q", k, relayedCountTotals)
				}
			}
		})
		t.Run(f.Name+" names D-relayed and every erratum it names exists", func(t *testing.T) {
			if !slices.Contains(exp.Errata, relayedErratum) {
				t.Errorf("errata %q don't name %s", exp.Errata, relayedErratum)
			}
			for _, name := range exp.Errata {
				if _, err := os.Stat(filepath.Join(errata, name+".md")); err != nil {
					t.Errorf("erratum %q: %v", name, err)
				}
			}
		})
	}
}

// TestRelayedAnchors checks anchors.json against the events with the
// relayed convention (testdata/timelines/relayed/README.md): a relayed
// anchor's event is its relay.message, its t and local the message's
// sent_at, where its lead ends, and its lead typing time only (SM 2.3); a
// prompt that rules 6 and 7 made typed at the app's stamp keeps its own t,
// with the stamp in sent_at and the record in relay. Every turn a person
// started has its prompt's anchor.
func TestRelayedAnchors(t *testing.T) {
	for _, f := range loadRelayed(t) {
		zone, err := relayedZone(f.Expected.UTCOffset)
		if err != nil {
			t.Fatalf("%s: %v", f.Name, err)
		}
		t.Run(f.Name+" every anchor is its record's: a relayed one at its message's stamp, a typed one at its prompt", func(t *testing.T) {
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
				// event.Anchor checks the label rules: an anchor via app is
				// relayed and carries via_relay, and no other anchor does.
				ea := event.Anchor{AnchorID: a.EventID, Kind: a.Kind, T: a.T, ProjectKey: a.ProjectKey, LeadS: a.LeadS,
					TrailS: a.TrailS, Via: a.Via, Labels: a.Labels}
				if err := ea.Validate(); err != nil {
					t.Errorf("%s: %v", where, err)
				}
				if a.ProjectKey != e.ProjectKey || a.Via != e.Via {
					t.Errorf("%s: project_key and via are %q, %q; the event has %q, %q", where, a.ProjectKey, a.Via, e.ProjectKey, e.Via)
				}
				if a.Level != f.Expected.Evidence {
					t.Errorf("%s: level %q, but the case's credit is level %q", where, a.Level, f.Expected.Evidence)
				}
				if a.LeadFrom != "params" || a.TrailS != 0 {
					t.Errorf("%s: lead_from %q and trail_s %v; SM states no relayed lead, and a prompt has no trail (SM 2.3)", where,
						a.LeadFrom, a.TrailS)
				}
				if i > 0 && a.T < f.Anchors.Anchors[i-1].T {
					t.Errorf("%s: anchors are listed in time order", where)
				}
				if got := relayedLocal(a.T, zone); got != a.Local {
					t.Errorf("%s: local %q, but t is %s in the case's zone", where, a.Local, got)
				}
				if !slices.Contains(promptAnchorClasses[a.Kind], a.PromptClass) {
					t.Errorf("%s: class %q doesn't make a %s; every anchor here is a person's prompt", where, a.PromptClass, a.Kind)
				}
				switch d := e.Data.(type) {
				case *event.RelayMessage:
					if a.Origin != event.OriginRelayed {
						t.Errorf("%s: origin %q; an anchor on a relay record is a relayed prompt's (SM 2.2 rule 9)", where, a.Origin)
					}
					if a.T != d.SentAt {
						t.Errorf("%s: t %d; a relayed anchor stands at its message's stamp, sent_at %d, not at the record's t %d "+
							"(SM 2.3)", where, a.T, d.SentAt, e.T)
					}
					if a.SentAt != 0 || a.LocalSentAt != "" || a.Relay != "" {
						t.Errorf("%s: sent_at, local_sent_at and relay are a typed prompt's; a relayed anchor's t is the stamp", where)
					}
					p, ok := f.EventByID(a.Prompt)
					ps, isPrompt := p.Data.(*event.PromptSubmit)
					if !ok || !isPrompt {
						t.Errorf("%s: prompt %q names no prompt.submit", where, a.Prompt)
						continue
					}
					if d.Prompt != a.Prompt && ps.Relay != a.EventID {
						t.Errorf("%s: the record and its prompt %s name neither the other", where, a.Prompt)
					}
					// The record carries the go-ahead test and no error
					// signature; the answer class is read from the prompt.
					var classOK bool
					switch a.PromptClass {
					case event.ClassLowEffort:
						classOK = d.LowEffort
					case event.ClassAnswer:
						classOK = ps.AnswersQuestion
					case event.ClassSubstantive:
						classOK = !d.LowEffort && !ps.AnswersQuestion
					}
					if !classOK {
						t.Errorf("%s: class %q for a record with low_effort %v and a prompt with answers_question %v", where,
							a.PromptClass, d.LowEffort, ps.AnswersQuestion)
					}
					n := min(d.LenChars, ps.TypedLenEst)
					lead := math.Min(float64(n)/lookbackComposeCPS, lookbackComposeMaxS)
					if prev, ok := relayedPrevStamp(f, d); ok {
						lead = math.Min(lead, float64(d.SentAt-prev)/1000)
					}
					if a.LeadS != lead || lead != math.Round(lead) {
						t.Errorf("%s: lead_s %v; a relayed lead is the typing time of min(len_chars %d, typed_len_est %d) / 1.5, "+
							"at most COMPOSE_MAX_S and the seconds since the thread's previous message, %v s, and every length here "+
							"is divisible by 3, so it is whole (SM 2.3)", where, a.LeadS, d.LenChars, ps.TypedLenEst, lead)
					}
				case *event.PromptSubmit:
					if !a.Origin.Human() {
						t.Errorf("%s: origin %q; an anchor on a prompt is one rules 6 and 7 made a person's (SM 2.2)", where, a.Origin)
					}
					if a.T != e.T {
						t.Errorf("%s: t %d, the prompt's is %d; a typed prompt's anchor stays at its prompt", where, a.T, e.T)
					}
					if a.Prompt != "" || len(a.Labels) > 0 {
						t.Errorf("%s: prompt %q and labels %q are a relayed anchor's", where, a.Prompt, a.Labels)
					}
					if a.Relay == "" {
						if a.SentAt != 0 || a.LocalSentAt != "" {
							t.Errorf("%s: sent_at without the relay record whose stamp it is", where)
						}
						continue
					}
					r, ok := f.EventByID(a.Relay)
					m, isRelay := r.Data.(*event.RelayMessage)
					if !ok || !isRelay {
						t.Errorf("%s: relay %q names no relay.message", where, a.Relay)
						continue
					}
					if m.Prompt != a.EventID && d.Relay != a.Relay {
						t.Errorf("%s: the prompt and its relay record %s name neither the other", where, a.Relay)
					}
					if a.SentAt != m.SentAt || a.LocalSentAt != relayedLocal(m.SentAt, zone) {
						t.Errorf("%s: sent_at %d at %q; the record's stamp is %d, %s in the case's zone", where, a.SentAt,
							a.LocalSentAt, m.SentAt, relayedLocal(m.SentAt, zone))
					}
				default:
					t.Errorf("%s: a %s anchor; a relayed case's anchors are relay records and prompts", where, e.Kind())
				}
			}
		})
		t.Run(f.Name+" every turn a person started has its prompt's anchor, and every anchor is one's", func(t *testing.T) {
			byPrompt := map[string]Anchor{}
			for _, a := range f.Anchors.Anchors {
				p := a.EventID
				if a.Origin == event.OriginRelayed {
					p = a.Prompt
				}
				byPrompt[p] = a
			}
			n := 0
			for i, tr := range f.Anchors.Turns {
				if !tr.Origin.ByPerson() {
					continue
				}
				n++
				p, ok := relayedTurnPrompt(f, tr)
				if !ok {
					continue // TestRelayedTurns reports it.
				}
				if a, ok := byPrompt[p.EventID]; !ok || a.Origin != tr.Origin || a.PromptClass != tr.PromptClass {
					t.Errorf("turn %d (%s, %s %s): anchor %q of %q %q; a turn a person started has its prompt's anchor, of its "+
						"origin and class", i, tr.LocalSubmit, tr.Origin, tr.PromptClass, a.EventID, a.Origin, a.PromptClass)
				}
			}
			if n != len(f.Anchors.Anchors) {
				t.Errorf("%d anchors for %d turns a person started", len(f.Anchors.Anchors), n)
			}
		})
	}
}

// TestRelayedTurns checks anchors.json's turns against the events: it lists
// every prompt's turn, since the case's prompt counts are counts of them,
// and each turn's origin is the one SM 2.2 gives its prompt, rules 1 and 3
// for what the app declares, rule 4 for a prompt nothing witnesses or a
// guest's, and rule 9, or rules 6 and 7 at the stamp, for a message a
// person sent.
func TestRelayedTurns(t *testing.T) {
	for _, f := range loadRelayed(t) {
		relayOf := map[string]*event.RelayMessage{}
		for _, l := range relayedLinks(f) {
			relayOf[l.prompt.EventID] = l.relay.Data.(*event.RelayMessage)
		}
		desktop := slices.Contains(relayedDesktop, relayedCase(f))
		t.Run(f.Name+" anchors.json lists every prompt's turn, each a prompt and its stop", func(t *testing.T) {
			listed := map[string]bool{}
			for i, tr := range f.Anchors.Turns {
				where := fmt.Sprintf("turn %d (%s)", i, tr.LocalSubmit)
				p, ok := relayedTurnPrompt(f, tr)
				if !ok {
					t.Errorf("%s: no prompt.submit for turn %s at %d", where, tr.TurnKey, tr.TSubmit)
					continue
				}
				listed[p.EventID] = true
				stop := false
				for _, e := range f.Events {
					if s, ok := e.Data.(*event.AgentStop); ok && e.SessionKey == tr.SessionKey && s.TurnKey == tr.TurnKey &&
						e.T == tr.TStop {
						stop = true
					}
				}
				if tr.TStop != 0 && !stop {
					t.Errorf("%s: no agent.stop for turn %s at %d", where, tr.TurnKey, tr.TStop)
				}
			}
			for i, e := range f.Events {
				if e.Kind() == event.KindPromptSubmit && !listed[e.EventID] {
					t.Errorf("%s line %d: a prompt whose turn anchors.json doesn't list; the prompt counts need every turn", EventsFile, i+1)
				}
			}
		})
		t.Run(f.Name+" every turn has the origin its prompt and its prompt's relay record give it (SM 2.2)", func(t *testing.T) {
			for i, tr := range f.Anchors.Turns {
				where := fmt.Sprintf("turn %d (%s, %s)", i, tr.LocalSubmit, tr.Origin)
				if tr.Origin.ByPerson() != (tr.PromptClass != "") {
					t.Errorf("%s: class %q; a prompt class goes with a turn a person started", where, tr.PromptClass)
				}
				p, ok := relayedTurnPrompt(f, tr)
				if !ok {
					continue
				}
				ps := p.Data.(*event.PromptSubmit)
				m := relayOf[p.EventID]
				var declared event.RelayDeclared
				if m != nil {
					declared = m.Declared
				}
				// What the hook itself declares, rules 1 to 3, comes first.
				own := ps.MatchesPendingCron || ps.OriginDeclared != event.DeclaredNone
				var ok2 bool
				switch tr.Origin {
				case event.OriginScheduled:
					ok2 = declared == event.RelayDeclaredCron || ps.MatchesPendingCron || ps.OriginDeclared == event.DeclaredCron ||
						ps.OriginDeclared == event.DeclaredLoop
				case event.OriginSubagent:
					ok2 = declared == event.RelayDeclaredCrossSession || ps.OriginDeclared == event.DeclaredSubagent ||
						ps.OriginDeclared == event.DeclaredCrossSession
				case event.OriginHeadless:
					// Every session here is an agent app's, which rule 4 takes
					// unless a message witnesses its prompt, and it takes a
					// guest's prompt even then.
					ok2 = !own && (declared == "" || ps.Guest)
				case event.OriginRelayed:
					ok2 = !own && declared == event.RelayDeclaredNone && !ps.Guest
				case event.OriginHumanTyped:
					// Rules 6 and 7 at the stamp need this device's input,
					// and a message from the app's phone app skips them.
					ok2 = !own && declared == event.RelayDeclaredNone && !ps.Guest && desktop && m.Client == event.RelayClientWeb
				default:
					t.Errorf("%s: an origin no relayed case gives", where)
					continue
				}
				if !ok2 {
					t.Errorf("%s: its prompt has origin_declared %q, matches_pending_cron %v and guest %v, and its relay record "+
						"declared %q", where, ps.OriginDeclared, ps.MatchesPendingCron, ps.Guest, declared)
				}
			}
		})
	}
}

// TestRelayedPrompts checks prompts.json against the events, as
// TestPrompts does. A guest's prompt keeps no text (SM 1.2), so none is
// quoted for it.
func TestRelayedPrompts(t *testing.T) {
	for _, f := range loadRelayed(t) {
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
				if p.Erratum != "" {
					t.Errorf("%s: erratum %q; every relayed text is invented, so none questions SM", where, p.Erratum)
				}
				if p.Text == nil {
					continue
				}
				if found.Guest {
					t.Errorf("%s: a guest's prompt quotes text, which pairtime never keeps (SM 1.2)", where)
				}
				if n := utf8.RuneCountInString(*p.Text); n != p.LenChars {
					t.Errorf("%s: the text has %d characters but len_chars is %d", where, n, p.LenChars)
				}
			}
		})
	}
}

// TestRelayedGenerators runs each case's gen.py into a temporary directory
// and checks that it writes the checked-in files byte for byte. gen.py also
// checks its rows against relayed.py's Replay, or sidedoor.py's Sweep for
// the rows the live rules score, and its README's tables against its rows,
// and fails if either disagrees. Without python3 the test is skipped,
// except in CI, where it fails.
func TestRelayedGenerators(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		if os.Getenv("CI") != "" {
			t.Fatal("python3 is needed to check the relayed generators")
		}
		t.Skip("python3 not found")
	}
	for _, f := range loadRelayed(t) {
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

// relayedNum is a number in a README, with commas between thousands.
const relayedNum = `\d{1,3}(?:,\d{3})+|\d+`

// relayedCodeItem matches an uncredited code and its seconds in a README's
// Arithmetic cell, as relayed.py's readme_codes reads them: "X.agent_only
// 59", "X.agent_only 2 × 60", "X.no_anchor 1 + 9", "X.automated 30 + 24 ×
// 60 + 22 = 1,492".
var relayedCodeItem = regexp.MustCompile(`(X\.[a-z_]+) ((?:` + relayedNum + `)(?: ?[+×] ?(?:` + relayedNum + `))*)` +
	`(?: ?= ?(` + relayedNum + `))?`)

// relayedBucketMinutes and relayedNoBucket match what an Arithmetic cell
// says about a row's buckets: "N bucket minutes" or "Buckets in the N
// minutes", and "no bucket" in a one-minute row.
var (
	relayedBucketMinutes = regexp.MustCompile(`(?:(\d+) bucket minutes|[Bb]uckets in the (\d+) minutes)`)
	relayedNoBucket      = regexp.MustCompile(`\bno bucket\b`)
)

// relayedReadmeCodes returns the uncredited seconds an Arithmetic cell
// states, by code, summed over its mentions. A code named without seconds
// states nothing, and an item whose stated total its sum doesn't give is
// an error.
func relayedReadmeCodes(cell string) (map[event.Reason]int64, error) {
	out := map[event.Reason]int64{}
	for _, m := range relayedCodeItem.FindAllStringSubmatch(cell, -1) {
		n, err := lookbackArithmetic(m[2])
		if err != nil {
			return nil, err
		}
		if m[3] != "" {
			total, err := strconv.ParseInt(strings.ReplaceAll(m[3], ",", ""), 10, 64)
			if err != nil || total != n {
				return nil, fmt.Errorf("%s %s is %d, not %s", m[1], m[2], n, m[3])
			}
		}
		out[event.Reason(m[1])] += n
	}
	return out, nil
}

// TestRelayedReadmeCodes checks the reader of an Arithmetic cell's codes
// that the README check relies on.
func TestRelayedReadmeCodes(t *testing.T) {
	for _, tc := range []struct {
		name string
		cell string
		want map[event.Reason]int64
		bad  bool
	}{
		{name: "a code with its seconds states them", cell: "M2's turn runs: X.agent_only 60",
			want: map[event.Reason]int64{event.ReasonAgentOnly: 60}},
		{name: "× and + work out the seconds", cell: "X.no_anchor 1 + 9, X.agent_only 3 × 60",
			want: map[event.Reason]int64{event.ReasonNoAnchor: 10, event.ReasonAgentOnly: 180}},
		{name: "a stated total that the sum gives is accepted", cell: "X.automated 30 + 24 × 60 + 22 = 1,492",
			want: map[event.Reason]int64{event.ReasonAutomated: 1492}},
		{name: "a code's mentions add up", cell: "X.no_anchor 1. Then the turn runs. X.no_anchor 1",
			want: map[event.Reason]int64{event.ReasonNoAnchor: 2}},
		{name: "a code named without seconds states nothing", cell: "X.bucket_cap comes before X.automated",
			want: map[event.Reason]int64{}},
		{name: "a comma after a number ends it", cell: "X.no_anchor 10, X.automated 50",
			want: map[event.Reason]int64{event.ReasonNoAnchor: 10, event.ReasonAutomated: 50}},
		{name: "a stated total the sum doesn't give is an error", cell: "X.automated 30 + 24 × 60 + 22 = 1,493", bad: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := relayedReadmeCodes(tc.cell)
			if tc.bad {
				if err == nil {
					t.Errorf("%q gave %v, want an error", tc.cell, got)
				}
				return
			}
			if err != nil || !maps.Equal(got, tc.want) {
				t.Errorf("%q = %v, %v; want %v", tc.cell, got, err, tc.want)
			}
		})
	}
}

// TestRelayedREADME checks each case's hand arithmetic, the oracle the
// scorer PR is held to: the README's minute tables work out every row of
// expected.json and credit what it credits, what each minute's Arithmetic
// cell says of its uncredited codes and its buckets is expected.json's, and
// every total's arithmetic gives its seconds, which are expected.json's.
// The counterfactual tables of t3-phone and t3-here-agent have other
// headers, so they aren't read.
func TestRelayedREADME(t *testing.T) {
	for _, f := range loadRelayed(t) {
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
		t.Run(f.Name+" the README works out every row of expected.json, and each minute credits what its row does", func(t *testing.T) {
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
					continue
				}
				if !lookbackCreditEqual(got, r.Credit) {
					t.Errorf("README %s credits %v, %s %v", cells[0], got, ExpectedFile, r.Credit)
				}
			}
			for _, r := range exp.Rows {
				if !seen[r.Local] {
					t.Errorf("row %s of %s isn't in the README's minute tables", r.Local, ExpectedFile)
				}
			}
		})
		t.Run(f.Name+" what each minute's Arithmetic says of its codes and buckets is expected.json's", func(t *testing.T) {
			for _, cells := range lookbackTables(t, readme, lookbackMinuteHeader) {
				r, ok := byLocal[cells[0]]
				if len(cells) != 4 || !ok {
					continue // the test above reports it
				}
				codes, err := relayedReadmeCodes(cells[3])
				if err != nil {
					t.Errorf("README %s: %v", cells[0], err)
					continue
				}
				for _, code := range slices.Sorted(maps.Keys(codes)) {
					var want float64
					if r.Bucket {
						want = r.Uncredited[code]
					}
					if float64(codes[code]) != want {
						t.Errorf("README %s gives %s %d s, %s %v", cells[0], code, codes[code], ExpectedFile, want)
					}
				}
				if r.Minutes == 1 && r.Bucket && relayedNoBucket.MatchString(cells[3]) {
					t.Errorf("README %s says it has no bucket, but %s gives it one", cells[0], ExpectedFile)
				}
				for _, m := range relayedBucketMinutes.FindAllStringSubmatch(cells[3], -1) {
					if n, err := strconv.Atoi(m[1] + m[2]); err != nil || n != r.BucketMinutes {
						t.Errorf("README %s says %q, %s has %d bucket minutes", cells[0], m[0], ExpectedFile, r.BucketMinutes)
					}
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
