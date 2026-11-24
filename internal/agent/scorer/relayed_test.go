// SPDX-License-Identifier: AGPL-3.0-or-later

package scorer

import (
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/agent/ledger"
	"github.com/some-du6e/pairtime/internal/agent/store"
	"github.com/some-du6e/pairtime/internal/timelines"
	"github.com/some-du6e/pairtime/pkg/event"
	pkgledger "github.com/some-du6e/pairtime/pkg/ledger"
	"github.com/some-du6e/pairtime/pkg/ledger/features"
	"github.com/some-du6e/pairtime/pkg/score"
)

// TestRelayedLeadAfterAQuietNight checks the loop's window on a server
// after a quiet night (SM 2.3; pkg/score's slots on a device that never
// samples). The evening's session ended, so once the window has slid past
// it the carry keeps nothing of it. In the morning T3 Code takes a
// 450-character message, a 300 s lead, at 40 s past a minute, and starts
// the session that runs it a second later, so every record of the run
// comes after the stamp, and the relay record comes in a batch after the
// prompt's. The Step that scores that batch has nothing older than the
// session's start, and must still derive the whole lead, as one Step over
// every record does, whose slots reach BUCKET_TTL_S back.
//
// The loop gets that batch the way the agent's writer hands it over,
// through Sealed, built here, so the test reads no ledger.
func TestRelayedLeadAfterAQuietNight(t *testing.T) {
	root, err := timelines.Root()
	if err != nil {
		t.Fatal(err)
	}
	f, err := timelines.Load(root, "relayed/t3-away")
	if err != nil {
		t.Fatal(err)
	}
	var start, prompt, relay *event.Event
	for i := range f.Events {
		e := &f.Events[i]
		switch e.Data.(type) {
		case *event.SessionStart:
			start = cmpOr(start, e)
		case *event.PromptSubmit:
			prompt = cmpOr(prompt, e)
		case *event.RelayMessage:
			relay = cmpOr(relay, e)
		}
	}
	if start == nil || prompt == nil || relay == nil {
		t.Fatal("t3-away has no session start, prompt or relay record")
	}
	// morning is a minute boundary a day after t3-away's first stamp, and
	// evening twelve hours before it.
	morning := relay.Data.(*event.RelayMessage).SentAt + dayMS
	evening := morning - dayMS/2
	n := 0
	clone := func(tmpl *event.Event, ts int64) event.Event {
		n++
		e := *tmpl
		e.EventID = fmt.Sprintf("01a20b48-0000-7000-8000-%012x", n)
		e.Mono += ts - e.T
		e.T = ts
		return e
	}
	// The evening: a session of T3's server that starts and ends.
	eve := clone(start, evening)
	eve.SessionKey = "s_0e0e0e0e0e0e"
	end := clone(start, evening+60_000)
	end.SessionKey, end.Data = eve.SessionKey, &event.SessionEnd{}
	// The morning: the stamp at 40 s, the session's start at 41 s, its
	// prompt at 42 s and the relay record, which names it, at 45 s.
	sent := morning + 40_000
	st := clone(start, morning+41_000)
	pr := clone(prompt, morning+42_000)
	p := *prompt.Data.(*event.PromptSubmit)
	p.LenChars, p.TypedLenEst, p.PastedChars = 450, 450, 0
	pr.Data = &p
	rl := clone(relay, morning+45_000)
	m := *relay.Data.(*event.RelayMessage)
	m.SentAt, m.LenChars, m.Prompt = sent, 450, pr.EventID
	rl.Data = &m

	r := newRig(t, score.Device{})
	r.write([]event.Event{eve, end, st, pr}, nil)
	row := r.lastRow()
	chain, err := ledger.ReadChain(r.path("ledger"))
	if err != nil {
		t.Fatal(err)
	}
	rl.Seq, rl.DeviceID = row.FirstSeq+row.N, chain.DeviceID
	rec, err := pkgledger.EventRecord(r.keys, rl, pkgledger.Content{Class: features.Substantive})
	if err != nil {
		t.Fatal(err)
	}
	l, bs := r.open()
	l.Start()
	l.Flush()
	l.Sealed(ledger.Batch{Row: pkgledger.BatchRow{B: row.B + 1, FirstSeq: rl.Seq, N: 1}, Records: []pkgledger.Record{rec}})
	l.Flush()
	status := l.Status()
	l.Close()
	if err := bs.Close(); err != nil {
		t.Fatal(err)
	}
	if status.Stopped != "" || status.Batch != row.B+1 {
		t.Fatalf("the loop is %+v, want it at batch %d", status, row.B+1)
	}

	// The minutes from ten before the morning's lead to the minute after
	// its stamp. One Step's slots before them are the BUCKET_TTL_S before
	// now, which no window after a night holds, and have no bucket.
	from, to := morning-10*minuteMS, morning+2*minuteMS
	all, _ := direct(r.table, r.dev, chain.DeviceID, []event.Event{eve, end, st, pr, rl})
	want := map[int64]store.Bucket{}
	for minute, b := range all {
		if minute >= from && minute < to {
			want[minute] = b
		}
	}
	got := r.stored(from, to)
	lead := func(buckets []store.Bucket) int64 {
		var halves int64
		for _, b := range buckets {
			for _, pm := range b.Projects {
				for _, c := range pm.Credited {
					if c.Why == event.ReasonPrompt {
						halves += c.Halves
					}
				}
			}
		}
		return halves / 2
	}
	var wantList []store.Bucket
	for _, b := range want {
		wantList = append(wantList, b)
	}
	for _, c := range []struct {
		name      string
		got, want int64
	}{
		{"one Step over every record credits the whole 300 s lead", lead(wantList), 300},
		{"so does the loop, whose window holds nothing older than the morning's session", lead(got), 300},
		{"the lead's minutes, from five before the stamp's, have their buckets in the loop", int64(len(got)), 6},
	} {
		t.Run(c.name, func(t *testing.T) {
			if c.got != c.want {
				t.Errorf("%d, want %d", c.got, c.want)
			}
		})
	}
	t.Run("the loop's buckets for those minutes are one Step's", func(t *testing.T) { compare(t, got, want) })
}

// afterADay is a case of TestSessionsAfterADay: the records the agent
// writes, in order; snap, the index of the record whose batch's Step
// slides the window more than a day past the session's last record, the
// Step before the one that scores the session's next prompt; sess, the
// session; and from and to, the minutes compared with one Step, [from,
// to).
type afterADay struct {
	recs     []event.Event
	snap     int
	sess     string
	from, to int64
}

// TestSessionsAfterADay checks the loop against one Step over every record
// where a session's next prompt comes more than a day after its last
// record (#265). The carry keeps the session's start, its last prompt,
// output and stop and its question state however long it has had no
// record, until carrySessions sessions heard from since push it out, as a
// State holding every record keeps them. Each case first checks that by
// the Step before the one that scores the next prompt the window has slid
// more than a day past the session's last record, where the carry of an
// agent before WP-ADE1's part 4 dropped the session, and that the carry
// still holds it; then that the loop's buckets are one Step's, minute by
// minute.
func TestSessionsAfterADay(t *testing.T) {
	cases := []struct {
		name    string
		records func(t *testing.T) afterADay
		// lead is the reason one Step credits the next prompt's lead under,
		// least seconds of it at least, with label on every slot of it when
		// label is set; not is a reason no minute compared credits, which a
		// carry that dropped the session gives the prompt's lead instead, ""
		// for none.
		lead  event.Reason
		least float64
		label event.Label
		not   event.Reason
		// asked says the session's last output and stop before the window
		// asked a question, which the carry must keep.
		asked bool
	}{
		{
			name: "a message sent through T3 Code to a thread whose session has had no record for a day is relayed, with one Step's lead: the carry keeps the session's start, which names T3's server, so the message witnesses its prompt",
			// 450 typed characters, a relayed lead of COMPOSE_MAX_S.
			records: threadDay, lead: event.ReasonPrompt, least: 300, label: event.LabelViaRelay,
		},
		{
			name: "an answer typed a day after the agent asked its question is an answer, with one Step's lead: the carry keeps the session's question state",
			// ANSWER_LEAD_S, and the typing on top.
			records: answerDay, lead: event.ReasonAnswer, least: 60, not: event.ReasonGoAhead, asked: true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			day := c.records(t)
			var last int64 // the session's last record before the snapshot
			for _, e := range day.recs[:day.snap] {
				if e.SessionKey == day.sess {
					last = max(last, e.T)
				}
			}
			r := newRig(t, score.Device{})
			var at cursor
			r.snapAt, r.snapshot = day.snap, func([]byte, map[string][]byte) { at = r.savedCursor() }
			r.write(day.recs, nil)
			if at.From-last <= dayMS {
				t.Fatalf("after the snapshot's Step the window starts at %s, %s after the session's last record; the case needs more than a day, after which an older carry dropped the session",
					time.UnixMilli(at.From).UTC(), time.Duration(at.From-last)*time.Millisecond)
			}
			switch s := at.Carry.Sessions[day.sess]; {
			case s == nil || s.Start == nil:
				t.Errorf("the carry then holds the session as %+v; want it with its start", s)
			case c.asked && !slices.ContainsFunc(s.Asks, asksQuestion):
				t.Errorf("the carry then holds none of the session's question records among its %d asks; want its last output and stop, which asked", len(s.Asks))
			}
			chain, err := ledger.ReadChain(r.path("ledger"))
			if err != nil {
				t.Fatal(err)
			}
			all, _ := direct(r.table, r.dev, chain.DeviceID, day.recs)
			want := map[int64]store.Bucket{}
			var wantList []store.Bucket
			for minute, b := range all {
				if minute >= day.from && minute < day.to {
					want[minute] = b
					wantList = append(wantList, b)
				}
			}
			got := r.stored(day.from, day.to)
			t.Run("one Step credits the next prompt's lead", func(t *testing.T) {
				if s := creditedS(wantList, c.lead); s < c.least {
					t.Errorf("one Step credits %v s of %s, want at least %v s", s, c.lead, c.least)
				}
			})
			t.Run("so does the loop, as many seconds as one Step", func(t *testing.T) {
				if g, w := creditedS(got, c.lead), creditedS(wantList, c.lead); g != w {
					t.Errorf("the loop credits %v s of %s, and one Step %v s", g, c.lead, w)
				}
			})
			if c.not != "" {
				t.Run("neither credits the lead the prompt gets from a carry without the session", func(t *testing.T) {
					if g, w := creditedS(got, c.not), creditedS(wantList, c.not); g != 0 || w != 0 {
						t.Errorf("the loop credits %v s of %s, and one Step %v s; want none", g, c.not, w)
					}
				})
			}
			if c.label != "" {
				t.Run("every slot of the lead in the loop carries the label", func(t *testing.T) {
					n := 0
					for _, b := range got {
						for _, s := range b.Slots {
							if s.Why != c.lead {
								continue
							}
							n++
							if !slices.Contains(s.Lbl, c.label) {
								t.Errorf("%s: a slot of %s has the labels %v, want %s among them", b.Minute.Format(time.RFC3339), c.lead, s.Lbl, c.label)
							}
						}
					}
					if n == 0 {
						t.Errorf("no slot of the loop's is %s", c.lead)
					}
				})
			}
			t.Run("the loop's buckets for those minutes are one Step's", func(t *testing.T) { compare(t, got, want) })
		})
	}
}

// asksQuestion reports whether e is an agent.output that asks a question
// or an agent.stop on one.
func asksQuestion(e *event.Event) bool {
	switch d := e.Data.(type) {
	case *event.AgentOutput:
		return d.AsksQuestion
	case *event.AgentStop:
		return d.StopReason == event.StopQuestion
	}
	return false
}

// creditedS returns the seconds bs credit under reason why, over their
// projects.
func creditedS(bs []store.Bucket, why event.Reason) float64 {
	var halves int64
	for _, b := range bs {
		for _, pm := range b.Projects {
			for _, c := range pm.Credited {
				if c.Why == why {
					halves += c.Halves
				}
			}
		}
	}
	return float64(halves) / 2
}

// threadAfterADay returns the records of a thread of T3 Code's on its
// server, which has no desktop, whose session has had no record for a day
// when the thread's next message comes. quiet is relayed/t3-away's
// session.start, which names T3's server with no TTY, and its first turn,
// a message from T3's web client with its prompt, relay record, outputs,
// tool call and stop, then, 31 hours after the stop, the os.focus the
// agent writes as it starts, saying focus is unknown. message is the
// thread's next message ten minutes later, with no new session.start: its
// prompt 2 s after its stamp, 450 typed characters under a turn key of its
// own, the relay record naming the prompt 5 s after the stamp, and an
// output and a stop 50 s after it. stamp is the message's, 40 s past a
// minute.
func threadAfterADay(t *testing.T) (quiet, message []event.Event, stamp int64) {
	t.Helper()
	root, err := timelines.Root()
	if err != nil {
		t.Fatal(err)
	}
	f, err := timelines.Load(root, "relayed/t3-away")
	if err != nil {
		t.Fatal(err)
	}
	var prompt, relay, output, stop *event.Event
	for i := range f.Events {
		e := &f.Events[i]
		quiet = append(quiet, *e)
		switch d := e.Data.(type) {
		case *event.PromptSubmit:
			prompt = cmpOr(prompt, e)
		case *event.RelayMessage:
			relay = cmpOr(relay, e)
		case *event.AgentOutput:
			if d.IsFinal {
				output = cmpOr(output, e)
			}
		case *event.AgentStop:
			stop = e
		}
		if stop != nil {
			break
		}
	}
	if _, ok := quiet[0].Data.(*event.SessionStart); !ok || prompt == nil || relay == nil || output == nil || stop == nil {
		t.Fatal("t3-away doesn't open with a session start and a turn with a prompt, a relay record, a final output and a stop")
	}
	n := 0
	clone := func(tmpl *event.Event, ts int64) event.Event {
		n++
		e := *tmpl
		e.EventID = fmt.Sprintf("01a20b49-0000-7000-8000-%012x", n)
		e.Mono += ts - e.T
		e.T = ts
		return e
	}
	focus := clone(stop, stop.T+31*hourMS)
	focus.Source, focus.Via, focus.Harness, focus.HarnessVersion = event.SourceOS, event.ViaOS, event.HarnessNone, ""
	focus.SessionKey, focus.ProjectKey, focus.Data = event.NoSession, "", &event.OSFocus{AppClass: event.AppUnknown}
	quiet = append(quiet, focus)
	stamp = focus.T - floorMod(focus.T, minuteMS) + 10*minuteMS + 40_000
	pr := clone(prompt, stamp+2_000)
	p := *prompt.Data.(*event.PromptSubmit)
	p.TurnKey, p.LenChars, p.TypedLenEst, p.PastedChars = "t_after_a_day", 450, 450, 0
	pr.Data = &p
	rl := clone(relay, stamp+5_000)
	m := *relay.Data.(*event.RelayMessage)
	m.SentAt, m.LenChars, m.LowEffort, m.Prompt = stamp, 450, false, pr.EventID
	rl.Data = &m
	out := clone(output, stamp+50_000)
	o := *output.Data.(*event.AgentOutput)
	o.TurnKey = p.TurnKey
	out.Data = &o
	st := clone(stop, stamp+50_000)
	s := *stop.Data.(*event.AgentStop)
	s.TurnKey = p.TurnKey
	st.Data = &s
	return quiet, []event.Event{pr, rl, out, st}, stamp
}

// threadDay is TestSessionsAfterADay's case of threadAfterADay's thread.
// It snaps at the os.focus, which comes in a batch of its own, and
// compares the message's minutes, from ten before its stamp to two after
// its stop (nextMinutes).
func threadDay(t *testing.T) afterADay {
	t.Helper()
	quiet, message, stamp := threadAfterADay(t)
	from, to := nextMinutes(stamp, message[len(message)-1].T)
	return afterADay{recs: append(slices.Clone(quiet), message...), snap: len(quiet) - 1, sess: quiet[0].SessionKey,
		from: from, to: to}
}

// nextMinutes returns the minutes a case of TestSessionsAfterADay compares
// for the session's next prompt, whose lead ends at at and whose turn
// stops at stop: from ten minutes before at's to two after stop's, [from,
// to).
func nextMinutes(at, stop int64) (from, to int64) {
	return at - floorMod(at, minuteMS) - 10*minuteMS, stop - floorMod(stop, minuteMS) + 3*minuteMS
}

// answerDay is TestSessionsAfterADay's case of a terminal session on a
// desktop whose agent asked a question a day before the person answered
// it. The evening, on 6.4's day: a sample every 15 s, each saying the last
// input came 3 s before it; an os.focus on the terminal, which hosts the
// session; the session's start, from the terminal with a TTY; a typed
// prompt; an agent.output that asks a question and an agent.stop on it;
// and, ten minutes later, an os.power suspend with the sample the agent
// takes with it. 31 hours later: the resume, with its sample, a sample
// every 15 s again, an os.focus on the terminal, and, five minutes after
// the resume, a typed "ok", the rig's text for a low-effort prompt, which
// the sample before it, at its time, says came 3 s after the last input;
// then the turn's output and stop a minute later, and samples for four
// minutes more. The case snaps at that sample: the first record of the
// device's own clock rules.late past the resume, which ends the hold the
// evening's last sample has on the clock (reach.held), so its Step slides
// the window past the evening. It compares the answer's minutes, from ten
// before it to two after its stop (nextMinutes).
func answerDay(t *testing.T) afterADay {
	t.Helper()
	tmpl := hookTurn(t, hookAt, []int64{minuteMS})
	var prompt event.Event
	for _, e := range tmpl {
		if _, ok := e.Data.(*event.PromptSubmit); ok {
			prompt = e
		}
	}
	sess := prompt.SessionKey
	n := 0
	id := func() string {
		n++
		return fmt.Sprintf("01a1fb1e-5318-78a1-a7e0-%012x", n)
	}
	// harness is a hook record of the session at ts, made from 6.4's prompt,
	// and own a record of the agent's own watchers at ts.
	harness := func(ts int64, d event.Payload) event.Event {
		e := prompt
		e.EventID = id()
		e.Mono += ts - e.T
		e.T, e.Data = ts, d
		return e
	}
	own := func(ts int64, d event.Payload) event.Event { return sampleAt(tmpl, ts, id(), d) }
	var recs []event.Event
	sample := func(ts int64, idle float64) {
		recs = append(recs, own(ts, &event.OSPresence{IdleAnyS: idle, IdleKeysS: &idle}))
	}
	focus := func(ts int64) {
		recs = append(recs, own(ts, &event.OSFocus{AppClass: event.AppTerminal, FocusProject: prompt.ProjectKey,
			HostedSessions: []string{sess}}))
	}
	evening := hookAt
	suspend := evening + 11*minuteMS
	focus(evening - 2*minuteMS)
	for ts := evening - 2*minuteMS; ts < suspend; ts += 15_000 {
		sample(ts, 3)
		switch ts {
		case evening - minuteMS:
			recs = append(recs, harness(ts, &event.SessionStart{StartKind: event.StartStartup, Entrypoint: event.EntrypointCLI,
				ParentClass: event.ParentTerminalApp, HasTTY: true}))
		case evening:
			recs = append(recs, harness(ts, &event.PromptSubmit{TurnKey: "t_asked", LenChars: 60, TypedLenEst: 60,
				OriginDeclared: event.DeclaredNone}))
		case evening + minuteMS:
			recs = append(recs, harness(ts, &event.AgentOutput{TurnKey: "t_asked", Words: 30, IsFinal: true, AsksQuestion: true}),
				harness(ts, &event.AgentStop{TurnKey: "t_asked", StopReason: event.StopQuestion}))
		}
	}
	recs = append(recs, own(suspend, &event.OSPower{Op: event.PowerSuspend}))
	sample(suspend, 3)
	resume := suspend + 31*hourMS
	answer := resume + 5*minuteMS
	recs = append(recs, own(resume, &event.OSPower{Op: event.PowerResume}))
	sample(resume, 0)
	snap := -1
	for ts := resume + 15_000; ts <= resume+10*minuteMS; ts += 15_000 {
		sample(ts, 3)
		switch ts {
		case resume + minuteMS:
			focus(ts + 1000)
		case answer:
			snap = len(recs) - 1
			recs = append(recs, harness(ts, &event.PromptSubmit{TurnKey: "t_answer", LenChars: 2, TypedLenEst: 2,
				OriginDeclared: event.DeclaredNone, LowEffort: true}))
		case answer + minuteMS:
			recs = append(recs, harness(ts, &event.AgentOutput{TurnKey: "t_answer", Words: 20, IsFinal: true}),
				harness(ts, &event.AgentStop{TurnKey: "t_answer", StopReason: event.StopEndTurn}))
		}
	}
	from, to := nextMinutes(answer, answer+minuteMS)
	return afterADay{recs: recs, snap: snap, sess: sess, from: from, to: to}
}
