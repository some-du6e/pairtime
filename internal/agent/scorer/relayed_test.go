// SPDX-License-Identifier: AGPL-3.0-or-later

package scorer

import (
	"fmt"
	"testing"

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
// The ledger writer can't write a relay.message yet (the T3 Code reader
// brings its class), so the loop gets that batch the way the agent's
// writer hands it over, through Sealed, built here.
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
