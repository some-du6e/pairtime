// SPDX-License-Identifier: AGPL-3.0-or-later

package scorer

import (
	"encoding/json"
	"maps"
	"slices"
	"testing"

	"github.com/some-du6e/pairtime/pkg/event"
	pkgledger "github.com/some-du6e/pairtime/pkg/ledger"
	"github.com/some-du6e/pairtime/pkg/ledger/features"
)

const testDevice = "d_test1"

// fixturePrompt returns 6.2's prompt.submit as the ledger writer stamps
// it: record number s, on testDevice.
func fixturePrompt(t *testing.T, s uint64) event.Event {
	t.Helper()
	for _, e := range fixture(t, "6.2").Events {
		if e.Kind() == event.KindPromptSubmit {
			p := *e.Data.(*event.PromptSubmit)
			e.Data = &p
			e.Seq, e.DeviceID = s, testDevice
			return e
		}
	}
	t.Fatal("6.2 has no prompt")
	return event.Event{}
}

func TestEventOf(t *testing.T) {
	k := testKeys(t)
	const errorText = "TypeError: x is undefined\nwhy does the page crash when it loads"
	prompt := func(t *testing.T, text string, device string) pkgledger.Record {
		t.Helper()
		ev := fixturePrompt(t, 7)
		ev.DeviceID = device
		p := ev.Data.(*event.PromptSubmit)
		p.LenChars, p.TypedLenEst, p.PastedChars = len(text), len(text), 0
		if es, ok := k.Features().ErrorSig(text); ok {
			p.ErrorSig = es.String()
		}
		rec, err := pkgledger.EventRecord(k, ev, pkgledger.Content{Text: []byte(text), Class: features.Substantive})
		if err != nil {
			t.Fatal(err)
		}
		return rec
	}
	// track is a track call of label, recorded as the writer records it,
	// with the error_sig it derives from the label.
	track := func(t *testing.T, label string) pkgledger.Record {
		t.Helper()
		ev := fixturePrompt(t, 7)
		ev.Source, ev.Via = event.SourceCLI, event.ViaTrack
		p := &event.CLITrack{Label: label, TextLen: len([]rune(label)), LogMatch: event.LogNoReader}
		if es, ok := k.Features().ErrorSig(label); ok {
			p.ErrorSig = es.String()
		}
		ev.Data = p
		rec, err := pkgledger.EventRecord(k, ev, pkgledger.Content{Text: []byte(label), Class: features.ErrorPaste})
		if err != nil {
			t.Fatal(err)
		}
		return rec
	}
	tests := []struct {
		name    string
		rec     func(t *testing.T) pkgledger.Record
		isEvent bool
		fails   bool
		check   func(t *testing.T, ev event.Event)
	}{
		{
			name:    "a prompt's error signature comes back from the keyed value in the body's f",
			rec:     func(t *testing.T) pkgledger.Record { return prompt(t, errorText, testDevice) },
			isEvent: true,
			check: func(t *testing.T, ev event.Event) {
				want, _ := k.Features().ErrorSig(errorText)
				if got := ev.Data.(*event.PromptSubmit).ErrorSig; got != want.String() {
					t.Errorf("error_sig %q, want %q", got, want.String())
				}
			},
		},
		{
			name:    "a prompt with no error line has no error signature",
			rec:     func(t *testing.T) pkgledger.Record { return prompt(t, "make the page load faster", testDevice) },
			isEvent: true,
			check: func(t *testing.T, ev event.Event) {
				if got := ev.Data.(*event.PromptSubmit).ErrorSig; got != "" {
					t.Errorf("error_sig %q, want none", got)
				}
			},
		},
		{
			name:    "a track call's error signature comes back from the keyed value in the body's f",
			rec:     func(t *testing.T) pkgledger.Record { return track(t, "TypeError: x is undefined") },
			isEvent: true,
			check: func(t *testing.T, ev event.Event) {
				want, _ := k.Features().ErrorSig("TypeError: x is undefined")
				if got := ev.Data.(*event.CLITrack).ErrorSig; got != want.String() {
					t.Errorf("error_sig %q, want %q", got, want.String())
				}
			},
		},
		{
			name:    "a track label with no error line has no error_sig",
			rec:     func(t *testing.T) pkgledger.Record { return track(t, "add tests for the parser please") },
			isEvent: true,
			check: func(t *testing.T, ev event.Event) {
				if got := ev.Data.(*event.CLITrack).ErrorSig; got != "" {
					t.Errorf("error_sig %q, want none", got)
				}
			},
		},
		{
			name:  "an event of another device is refused",
			rec:   func(t *testing.T) pkgledger.Record { return prompt(t, errorText, "d_other") },
			fails: true,
		},
		{
			name: "an upload-failure note holds no event",
			rec: func(t *testing.T) pkgledger.Record {
				rec, err := pkgledger.UploadFailureRecord(k, 8, 1795524000000, "connect")
				if err != nil {
					t.Fatal(err)
				}
				return rec
			},
		},
		{
			name: "a checkpoint holds no event",
			rec: func(t *testing.T) pkgledger.Record {
				rec, err := pkgledger.CheckpointRecord(k, 9, "p_be4901c04a84", 1795524300000, json.RawMessage(`{"v":1}`))
				if err != nil {
					t.Fatal(err)
				}
				return rec
			},
		},
		{
			name: "a record whose header number isn't its event's seq is refused",
			rec: func(t *testing.T) pkgledger.Record {
				rec := prompt(t, errorText, testDevice)
				h, err := pkgledger.NewHeader(12, rec.Header.RType(), fixturePrompt(t, 7).ProjectKey, fixturePrompt(t, 7).SessionKey)
				if err != nil {
					t.Fatal(err)
				}
				rec.Header = h
				return rec
			},
			fails: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ev, ok, err := eventOf(tt.rec(t), testDevice)
			switch {
			case tt.fails:
				if err == nil {
					t.Fatalf("eventOf took it: %v", ev.Kind())
				}
				return
			case err != nil:
				t.Fatal(err)
			case ok != tt.isEvent:
				t.Fatalf("eventOf says event %v, want %v", ok, tt.isEvent)
			}
			if tt.check != nil {
				tt.check(t, ev)
			}
		})
	}
}

// ev is a record for the carry's tests, which read only what it is, its
// time and order, and its session.
func ev(t int64, seq uint64, session string, data event.Payload) event.Event {
	return event.Event{Envelope: event.Envelope{EventID: "e" + string(rune('a'+seq)), Seq: seq, T: t, SessionKey: session}, Data: data}
}

func TestCarry(t *testing.T) {
	focus := func(project string) event.Payload {
		return &event.OSFocus{AppClass: event.AppTerminal, FocusProject: project}
	}
	tests := []struct {
		name  string
		fold  []event.Event
		check func(t *testing.T, c carry)
	}{
		{
			name: "the carry keeps the latest focus in record order, whatever order the records fold in",
			fold: []event.Event{
				ev(300, 3, event.NoSession, focus("p_c")),
				ev(100, 1, event.NoSession, focus("p_a")),
				ev(300, 2, event.NoSession, focus("p_b")),
			},
			check: func(t *testing.T, c carry) {
				if c.Focus == nil || c.Focus.Data.(*event.OSFocus).FocusProject != "p_c" {
					t.Errorf("focus %+v, want p_c's", c.Focus)
				}
			},
		},
		{
			name: "a session keeps its start, last prompt, last output and last stop, and the output its last prompt read",
			fold: []event.Event{
				ev(100, 1, "s_1", &event.SessionStart{}),
				ev(200, 2, "s_1", &event.PromptSubmit{}),
				ev(300, 3, "s_1", &event.AgentOutput{}),
				ev(400, 4, "s_1", &event.PromptSubmit{}),
				ev(500, 5, "s_1", &event.AgentStop{}),
				ev(600, 6, "s_1", &event.AgentOutput{}),
			},
			check: func(t *testing.T, c carry) {
				s := c.Sessions["s_1"]
				if s == nil || s.Start.T != 100 || s.Prompt.T != 400 || s.Output.T != 600 || s.Stop.T != 500 || s.Last != 600 {
					t.Errorf("session carry %+v", s)
				}
				if got := len(c.records(nil, 0, testGaps, 0, 0)); got != 5 {
					t.Errorf("the carry gives %d records, want 5", got)
				}
			},
		},
		{
			name: "of a session's outputs and stops the carry keeps the last ones and the last before each record it carries, and no others",
			fold: []event.Event{
				ev(100, 1, "s_1", &event.SessionStart{}),
				ev(200, 2, "s_1", &event.AgentOutput{}),
				ev(300, 3, "s_1", &event.AgentOutput{}),
				ev(400, 4, "s_1", &event.PromptSubmit{}),
				ev(500, 5, "s_1", &event.AgentOutput{}),
				ev(600, 6, "s_1", &event.AgentStop{}),
				ev(700, 7, "s_1", &event.AgentOutput{}),
			},
			check: func(t *testing.T, c carry) {
				var seqs []uint64
				for _, e := range c.Sessions["s_1"].Asks {
					seqs = append(seqs, e.Seq)
				}
				if want := []uint64{3, 5, 6, 7}; !slices.Equal(seqs, want) {
					t.Errorf("the carry keeps the outputs and stops numbered %v, want %v", seqs, want)
				}
			},
		},
		{
			name: "a session whose end is its latest record is dropped",
			fold: []event.Event{
				ev(100, 1, "s_1", &event.SessionStart{}),
				ev(200, 2, "s_1", &event.SessionEnd{}),
				ev(100, 3, "s_2", &event.SessionStart{}),
			},
			check: func(t *testing.T, c carry) {
				if _, ok := c.Sessions["s_1"]; ok {
					t.Error("the ended session is still carried")
				}
				if _, ok := c.Sessions["s_2"]; !ok {
					t.Error("the open session was dropped")
				}
			},
		},
		{
			name: "a session that went on after its end is kept",
			fold: []event.Event{
				ev(200, 2, "s_1", &event.SessionEnd{}),
				ev(300, 3, "s_1", &event.SessionStart{}),
			},
			check: func(t *testing.T, c carry) {
				if _, ok := c.Sessions["s_1"]; !ok {
					t.Error("the resumed session was dropped")
				}
			},
		},
		{
			name: "a session that hasn't ended keeps its start, last prompt, output and stop after a month with no record, each dated carryGraceMS before the window, and the question its output and stop asked",
			fold: []event.Event{
				ev(100, 1, "s_1", &event.SessionStart{}),
				ev(200, 2, "s_1", &event.PromptSubmit{}),
				ev(300, 3, "s_1", &event.AgentOutput{AsksQuestion: true}),
				ev(400, 4, "s_1", &event.AgentStop{StopReason: event.StopQuestion}),
			},
			check: func(t *testing.T, c carry) {
				// The window a month on, which prunes the carry as it slides.
				from := int64(100 + 30*dayMS)
				w := &window{carry: c, gaps: testGaps, ttl: testGaps.native}
				w.slide(from)
				s := w.carry.Sessions["s_1"]
				if s == nil {
					t.Fatal("the session isn't carried a month on")
				}
				got := map[uint64]int64{}
				for _, e := range w.records() {
					got[e.Seq] = e.T
				}
				grace := from - carryGraceMS
				if want := map[uint64]int64{1: grace, 2: grace, 3: grace, 4: grace}; !maps.Equal(got, want) {
					t.Errorf("a Step a month on gets the records numbered and dated %v, want %v", got, want)
				}
				var asks []uint64
				for _, e := range s.Asks {
					asks = append(asks, e.Seq)
				}
				if want := []uint64{3, 4}; !slices.Equal(asks, want) {
					t.Errorf("the session's question state holds the outputs and stops numbered %v, want %v", asks, want)
				}
			},
		},
		{
			name: "past carrySessions sessions the ones heard from longest ago are dropped",
			fold: func() []event.Event {
				var out []event.Event
				for i := range carrySessions + 2 {
					out = append(out, ev(int64(1000+i), uint64(i+1), "s_"+string(rune('A'+i)), &event.SessionStart{}))
				}
				return out
			}(),
			check: func(t *testing.T, c carry) {
				if len(c.Sessions) != carrySessions {
					t.Fatalf("carries %d sessions, want %d", len(c.Sessions), carrySessions)
				}
				keys := slices.Sorted(maps.Keys(c.Sessions))
				if slices.Contains(keys, "s_A") || slices.Contains(keys, "s_B") {
					t.Errorf("kept the two oldest sessions: %q", keys)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var c carry
			for _, e := range tt.fold {
				c.fold(e, testGaps, testGaps.native)
			}
			c.prune()
			for _, s := range c.Sessions {
				s.keepAsks(nil) // as a window with no record of the session does
			}
			tt.check(t, c)
		})
	}
}

// testGaps are the runs' gaps of the newest parameter table: BUCKET_TTL_S
// in a hooked session, 2 h, and the scorer's reach elsewhere, 20 min.
var testGaps = gapsOf(testTable())

func TestCarryRuns(t *testing.T) {
	const h, m = 3600 * 1000, 60 * 1000
	via := func(v event.Via, e event.Event) event.Event {
		e.Via = v
		return e
	}
	prompt := func(at int64, seq uint64, key string) event.Event {
		return via(event.ViaHook, ev(at, seq, "s_1", &event.PromptSubmit{TurnKey: key}))
	}
	tool := func(at int64, seq uint64) event.Event {
		return via(event.ViaHook, ev(at, seq, "s_1", &event.AgentTool{}))
	}
	// dated returns the time records gives each of the session's records,
	// by seq.
	dated := func(c *carry, from int64) map[uint64]int64 {
		out := map[uint64]int64{}
		for _, e := range c.records(nil, from, testGaps, 0, 0) {
			if e.SessionKey == "s_1" {
				out[e.Seq] = e.T
			}
		}
		return out
	}
	tests := []struct {
		name  string
		fold  []event.Event
		from  int64
		check func(t *testing.T, c *carry, s *sessionCarry)
	}{
		{
			name: "a hooked turn's run keeps its latest tool call, at its own time, and its prompt goes no later than that",
			fold: []event.Event{prompt(0, 1, "t_1"), tool(90*m, 2), tool(3*h, 3), tool(270*m, 4)},
			from: 5 * h,
			check: func(t *testing.T, c *carry, s *sessionCarry) {
				if s.Agent == nil || s.Agent.T != 270*m || s.Cut || s.Stopped {
					t.Fatalf("session carry %+v, want the 4:30 tool call as the run's latest", s)
				}
				if got := dated(c, 5*h); got[4] != 270*m || got[1] != 4*h {
					t.Errorf("dated %v, want the tool call at 4:30 and the prompt an hour before the window", got)
				}
			},
		},
		{
			name: "a hooked turn's run whose last record is more than an hour before the window has its prompt dated with that record",
			fold: []event.Event{prompt(0, 1, "t_1"), tool(90*m, 2), tool(3*h, 3)},
			from: 4*h + 30*m,
			check: func(t *testing.T, c *carry, s *sessionCarry) {
				if got := dated(c, 4*h+30*m); got[3] != 3*h || got[1] != 3*h {
					t.Errorf("dated %v, want the prompt and the tool call both at 3:00", got)
				}
			},
		},
		{
			name: "a silence longer than BUCKET_TTL_S ends a hooked turn's run for good, and its records go further back than any gap",
			fold: []event.Event{prompt(0, 1, "t_1"), tool(h, 2), tool(3*h+1, 3), tool(3*h+10*m, 4)},
			from: 4 * h,
			check: func(t *testing.T, c *carry, s *sessionCarry) {
				if !s.Cut || s.Agent == nil || s.Agent.T != h {
					t.Fatalf("session carry %+v, want the run cut after the 1:00 tool call", s)
				}
				for seq, at := range dated(c, 4*h) {
					if at != 2*h-1 {
						t.Errorf("record %d is dated %d, want %d, longer than BUCKET_TTL_S before the window", seq, at, 2*h-1)
					}
				}
			},
		},
		{
			name: "a hooked turn with no record for BUCKET_TTL_S before the window is over",
			fold: []event.Event{prompt(0, 1, "t_1"), tool(h, 2)},
			from: 3*h + 1,
			check: func(t *testing.T, c *carry, s *sessionCarry) {
				for seq, at := range dated(c, 3*h+1) {
					if at != h {
						t.Errorf("record %d is dated %d, want %d", seq, at, h)
					}
				}
			},
		},
		{
			name: "a side-door turn's records go within the scorer's reach of its latest, since a longer silence ends its run",
			fold: []event.Event{
				via(event.ViaPlugin, ev(0, 1, "s_1", &event.PromptSubmit{TurnKey: "t_1"})),
				via(event.ViaPlugin, ev(10*m, 2, "s_1", &event.AgentOutput{TurnKey: "t_1"})),
				via(event.ViaPlugin, ev(25*m, 3, "s_1", &event.AgentOutput{TurnKey: "t_1"})),
			},
			from: 40 * m,
			check: func(t *testing.T, c *carry, s *sessionCarry) {
				if s.Native || s.Cut || s.Agent == nil || s.Agent.T != 25*m {
					t.Fatalf("session carry %+v, want a side-door run going on from its 0:25 output", s)
				}
				if got := dated(c, 40*m); got[3] != 25*m || got[1] != 5*m {
					t.Errorf("dated %v, want the prompt at 0:05 and the last output at 0:25", got)
				}
			},
		},
		{
			name: "a stop ends the run, and the session's records go back to the usual dating",
			fold: []event.Event{prompt(0, 1, "t_1"), tool(h, 2), via(event.ViaHook, ev(2*h, 3, "s_1", &event.AgentStop{TurnKey: "t_1"}))},
			from: 10 * h,
			check: func(t *testing.T, c *carry, s *sessionCarry) {
				if !s.Stopped || s.running() {
					t.Fatalf("session carry %+v, want the turn stopped", s)
				}
				for seq, at := range dated(c, 10*h) {
					if at != 9*h {
						t.Errorf("record %d is dated %d, want carryGraceMS before the window", seq, at)
					}
				}
			},
		},
		{
			name: "the turn's stop ends it after a silence cut its run, so the session's records, a continuation's among them, go back to the usual dating",
			fold: []event.Event{
				prompt(0, 1, "t_1"), tool(h, 2), tool(3*h+10*m, 3),
				via(event.ViaHook, ev(4*h, 4, "s_1", &event.AgentStop{TurnKey: "t_1"})),
				via(event.ViaHook, ev(5*h, 5, "s_1", &event.AgentOutput{TurnKey: "t_1b"})),
			},
			from: 10 * h,
			check: func(t *testing.T, c *carry, s *sessionCarry) {
				if !s.Cut || !s.Stopped || s.running() {
					t.Fatalf("session carry %+v, want the cut turn stopped", s)
				}
				for seq, at := range dated(c, 10*h) {
					if at != 9*h {
						t.Errorf("record %d is dated %d, want carryGraceMS before the window", seq, at)
					}
				}
			},
		},
		{
			name: "a later prompt of another turn starts a run of its own",
			fold: []event.Event{prompt(0, 1, "t_1"), tool(h, 2), tool(4*h, 3), prompt(5*h, 4, "t_2")},
			from: 5*h + 30*m,
			check: func(t *testing.T, c *carry, s *sessionCarry) {
				if s.Cut || s.Agent != nil || turnKey(s.Prompt) != "t_2" {
					t.Errorf("session carry %+v, want t_2's run from its prompt", s)
				}
			},
		},
		{
			name: "a record folded out of turn changes nothing",
			fold: []event.Event{prompt(0, 1, "t_1"), tool(h, 2), tool(2*h, 3), tool(30*m, 4)},
			from: 3 * h,
			check: func(t *testing.T, c *carry, s *sessionCarry) {
				if s.Agent == nil || s.Agent.T != 2*h || s.Cut {
					t.Errorf("session carry %+v, want the 2:00 tool call as the run's latest", s)
				}
			},
		},
		{
			name: "an output of another turn doesn't carry this turn's run on",
			fold: []event.Event{prompt(0, 1, "t_1"), via(event.ViaHook, ev(h, 2, "s_1", &event.AgentOutput{TurnKey: "t_0"}))},
			from: 2 * h,
			check: func(t *testing.T, c *carry, s *sessionCarry) {
				if s.Agent != nil {
					t.Errorf("the run's latest is %+v, want none", s.Agent)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var c carry
			for _, e := range tt.fold {
				c.fold(e, testGaps, testGaps.native)
			}
			s := c.Sessions["s_1"]
			if s == nil {
				t.Fatal("the session isn't carried")
			}
			tt.check(t, &c, s)
		})
	}
}

func TestWindow(t *testing.T) {
	focus := &event.OSFocus{AppClass: event.AppTerminal, FocusProject: "p_a"}
	tests := []struct {
		name  string
		run   func(w *window)
		check func(t *testing.T, w *window)
	}{
		{
			name: "a record dated before the window goes to the carry, and one in it stays",
			run: func(w *window) {
				w.from = 1000
				w.add(ev(500, 1, event.NoSession, focus), 1)
				w.add(ev(1500, 2, event.NoSession, &event.OSPresence{}), 2)
			},
			check: func(t *testing.T, w *window) {
				if w.carry.Focus == nil || len(w.recs) != 1 || w.recs[0].ev.T != 1500 {
					t.Errorf("carry focus %v, window %d records", w.carry.Focus, len(w.recs))
				}
			},
		},
		{
			name: "slide folds the records before the new start into the carry and keeps the rest",
			run: func(w *window) {
				w.add(ev(100, 1, event.NoSession, focus), 1)
				w.add(ev(200, 2, event.NoSession, &event.OSPresence{}), 2)
				w.add(ev(300, 3, event.NoSession, &event.OSPresence{}), 3)
				w.slide(250)
			},
			check: func(t *testing.T, w *window) {
				if w.from != 250 || len(w.recs) != 1 || w.carry.Presence == nil || w.carry.Presence.T != 200 || w.carry.Focus == nil {
					t.Errorf("from %d, %d records, carry %+v", w.from, len(w.recs), w.carry)
				}
				if w.firstBatch() != 3 {
					t.Errorf("first batch %d, want 3", w.firstBatch())
				}
				if got := len(w.records()); got != 3 {
					t.Errorf("a Step gets %d records, want the window's 1 and the carry's 2", got)
				}
			},
		},
		{
			name: "a slide back changes nothing",
			run: func(w *window) {
				w.add(ev(100, 1, event.NoSession, focus), 1)
				w.slide(50)
				w.slide(-10)
			},
			check: func(t *testing.T, w *window) {
				if w.from != 50 || len(w.recs) != 1 || w.carry.Focus != nil {
					t.Errorf("from %d, %d records, carry focus %v", w.from, len(w.recs), w.carry.Focus)
				}
			},
		},
		{
			name: "a carried record older than carryGraceMS before the window is scored as if it came then",
			run: func(w *window) {
				w.add(ev(100, 1, event.NoSession, focus), 1)
				w.add(ev(10*carryGraceMS, 2, event.NoSession, &event.OSPresence{}), 2)
				w.slide(10*carryGraceMS - 1)
			},
			check: func(t *testing.T, w *window) {
				recs := w.records()
				if len(recs) != 2 || recs[0].T != 9*carryGraceMS-1 || recs[1].T != 10*carryGraceMS {
					t.Errorf("records %+v", recs)
				}
				if w.carry.Focus.T != 100 {
					t.Errorf("the carry itself now dates the focus at %d, want 100", w.carry.Focus.T)
				}
			},
		},
		{
			name: "an empty window has no first batch",
			run:  func(*window) {},
			check: func(t *testing.T, w *window) {
				if w.firstBatch() != 0 {
					t.Errorf("first batch %d", w.firstBatch())
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := &window{}
			tt.run(w)
			tt.check(t, w)
		})
	}
}
