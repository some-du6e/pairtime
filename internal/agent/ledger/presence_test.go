// SPDX-License-Identifier: AGPL-3.0-or-later

package ledger

import (
	"slices"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/some-du6e/pairtime/internal/agent/presence"
	"github.com/some-du6e/pairtime/pkg/event"
)

// TestPresenceWaiting checks presence's part of the writer's Waiting: a
// record queued and not yet written counts, by its event's times, until it
// is written, and one the full queue dropped never does.
func TestPresenceWaiting(t *testing.T) {
	// before is the writer's test of an event against a moment at testT,
	// by its t, of this run's clock, holding the moment's own millisecond.
	before := func(e event.Envelope) bool { return e.BootID == runBoot && e.T <= testT }
	cases := []struct {
		name string
		// times are when the records queued were made, in Unix ms, and
		// written has a writer take them all before Waiting is asked.
		times   []int64
		written bool
		want    bool
	}{
		{"a record made before the moment counts while it is queued", []int64{testT + 5, testT - 1}, false, true},
		{"a record made at the moment counts, since one of the moment's own millisecond can't be told from one made before it", []int64{testT}, false, true},
		{"a record made after the moment doesn't count", []int64{testT + 1}, false, false},
		{"a record counts no more once it is written", []int64{testT - 1}, true, false},
		{"a record the full queue dropped doesn't count", append(slices.Repeat([]int64{testT + 1}, presenceQueue), testT-1), false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := NewPresence(nil, nil)
			for _, ms := range c.times {
				p.Record(presence.Record{At: time.UnixMilli(ms), Data: &event.OSPresence{IdleAnyS: 1}})
			}
			if c.written {
				dir := t.TempDir()
				w := open(t, dir, newClock(testT))
				p.Attach(w)
				// Close waits for the goroutine, which writes what was queued
				// first.
				p.Close()
				if err := w.Close(); err != nil {
					t.Fatal(err)
				}
				// The writer took them, and didn't turn them away.
				if n := len(records(verify(t, dir, keysOf(t, testKDev)))); n != len(c.times) {
					t.Fatalf("the writer holds %d records, want %d", n, len(c.times))
				}
			}
			got := p.Waiting(before)
			p.Close()
			if got != c.want {
				t.Errorf("Waiting = %v, want %v", got, c.want)
			}
		})
	}
}

// TestPresence checks how presence's records and seals reach the writer
// (WP-OS1's presence.Options.Record and Seal).
func TestPresence(t *testing.T) {
	at := time.UnixMilli(testT)
	sample := presence.Record{At: at, Data: &event.OSPresence{IdleAnyS: 2.5}}
	suspend := presence.Record{At: at.Add(time.Second), Data: &event.OSPower{Op: event.PowerSuspend}}

	t.Run("records taken before the writer opens are written once it does, as OS events with no project or session", func(t *testing.T) {
		dir := t.TempDir()
		p := NewPresence(nil, nil)
		p.Record(sample)
		p.Record(suspend)
		w := open(t, dir, newClock(testT))
		p.Attach(w)
		p.Close()
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
		recs := records(verify(t, dir, keysOf(t, testKDev)))
		if len(recs) != 2 {
			t.Fatalf("%d records, want 2", len(recs))
		}
		for i, r := range recs {
			ev := eventOf(t, r)
			if ev.Source != event.SourceOS || ev.Via != event.ViaOS || ev.Harness != event.HarnessNone ||
				ev.SessionKey != event.NoSession || ev.ProjectKey != "" || ev.T != testT+int64(i)*1000 || ev.BootID != runBoot {
				t.Errorf("record %d's envelope is %+v", r.Header.Seq(), ev.Envelope)
			}
		}
		if _, ok := eventOf(t, recs[1]).Data.(*event.OSPower); !ok {
			t.Error("the records aren't in the order presence took them")
		}
	})

	t.Run("a writer attached before Close gets every record queued, even when presence's goroutine first runs during Close", func(t *testing.T) {
		// On a busy machine, the goroutine NewPresence starts can first
		// run only after Attach has returned and Close has closed stop,
		// while Close waits for it. Each round runs it that late, so its
		// first select finds the writer and the stop both ready. A select
		// picks between ready cases at random, so unless run prefers the
		// writer, each round loses its record half the time and all 16
		// pass once in 65,536 runs.
		for round := range 16 {
			dir := t.TempDir()
			w := open(t, dir, newClock(testT))
			p := newPresence(nil, nil)
			p.Record(sample)
			p.Attach(w)
			p.once.Do(func() { close(p.stop) }) // Close, short of its wait for the goroutine
			p.run()
			if err := w.Close(); err != nil {
				t.Fatal(err)
			}
			if n := len(records(verify(t, dir, keysOf(t, testKDev)))); n != 1 {
				t.Fatalf("round %d: %d records, want the 1 queued before Close", round, n)
			}
		}
	})

	t.Run("an os.focus record the agent passes on is written in the order queued, as an OS event, with its local app_hash left out", func(t *testing.T) {
		dir := t.TempDir()
		p := NewPresence(nil, nil)
		w := open(t, dir, newClock(testT))
		p.Attach(w)
		p.Focus(at, &event.OSFocus{AppClass: event.AppTerminal, AppHash: "a18ecfcc1abf15c0fce0859c46e557fa6", HostedSessions: []string{"s_tab1"}, FocusProject: "p_9f2c"})
		p.Record(sample)
		p.Close()
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
		recs := records(verify(t, dir, keysOf(t, testKDev)))
		if len(recs) != 2 {
			t.Fatalf("%d records, want 2", len(recs))
		}
		ev := eventOf(t, recs[0])
		d, ok := ev.Data.(*event.OSFocus)
		switch {
		case !ok:
			t.Fatalf("the first record is %T, want the os.focus queued first", ev.Data)
		case ev.Source != event.SourceOS || ev.Via != event.ViaOS || ev.Harness != event.HarnessNone ||
			ev.SessionKey != event.NoSession || ev.ProjectKey != "" || ev.T != testT || ev.BootID != runBoot:
			t.Errorf("the os.focus record's envelope is %+v", ev.Envelope)
		case d.AppHash != "":
			t.Errorf("app_hash %q reached the ledger body, which Event.Shareable leaves it out of", d.AppHash)
		case d.AppClass != event.AppTerminal || !slices.Equal(d.HostedSessions, []string{"s_tab1"}) || d.FocusProject != "p_9f2c":
			t.Errorf("the os.focus record holds %+v", d)
		}
	})

	t.Run("a seal on suspend waits for a batch that holds the suspend's record", func(t *testing.T) {
		dir := t.TempDir()
		sealed := make(chan Batch, 2)
		w := open(t, dir, newClock(testT), func(o *Options) { o.Sealed = func(b Batch) { sealed <- b } })
		// In a bubble, sealWait's timer can't fire while presence's
		// goroutine writes and seals, so a slow disk can't end the wait.
		// The writer and its directory stay outside, so that on Windows
		// their file retries still sleep in real time.
		synctest.Test(t, func(t *testing.T) {
			p := NewPresence(nil, nil)
			defer p.Close()
			p.Attach(w)
			p.Record(sample)
			p.Record(suspend)
			p.Seal(event.PowerSuspend)
			select {
			case b := <-sealed:
				if len(b.Records) != 2 {
					t.Errorf("the batch sealed on suspend holds %d records, want the 2 before it", len(b.Records))
				}
			default:
				t.Fatal("Seal returned before the batch was sealed")
			}
		})
	})

	t.Run("a seal with no writer to seal with returns within sealWait", func(t *testing.T) {
		// The bubble's clock moves only when every goroutine in it waits,
		// so Seal waits exactly sealWait, however busy the machine is.
		synctest.Test(t, func(t *testing.T) {
			p := NewPresence(nil, nil)
			defer p.Close()
			start := time.Now()
			p.Seal(event.PowerLock)
			if d := time.Since(start); d != sealWait {
				t.Errorf("Seal took %v, want sealWait, %v", d, sealWait)
			}
		})
	})

	t.Run("records dropped before the writer opens are reported as lost once it opens", func(t *testing.T) {
		var mu sync.Mutex
		var problems []string
		p := NewPresence(func(msg string) {
			mu.Lock()
			problems = append(problems, msg)
			mu.Unlock()
		}, nil)
		for range presenceQueue + 3 {
			p.Record(sample)
		}
		w := open(t, t.TempDir(), newClock(testT))
		p.Attach(w)
		p.Close()
		mu.Lock()
		defer mu.Unlock()
		want := []string{"the ledger isn't open, so presence and focus records are being dropped",
			"3 presence and focus records were dropped while the ledger wasn't open"}
		if !slices.Equal(problems, want) {
			t.Errorf("problems %q, want %q", problems, want)
		}
	})

	t.Run("records past the queue are dropped and counted, never blocking presence", func(t *testing.T) {
		var problems []string
		p := NewPresence(func(msg string) { problems = append(problems, msg) }, nil)
		defer p.Close()
		for range presenceQueue + 3 {
			p.Record(sample)
		}
		if p.Dropped() != 3 || len(problems) != 1 {
			t.Errorf("dropped %d with problems %q, want 3 and one problem", p.Dropped(), problems)
		}
	})
}
