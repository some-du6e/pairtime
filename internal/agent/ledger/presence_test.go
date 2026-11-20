// SPDX-License-Identifier: AGPL-3.0-or-later

package ledger

import (
	"slices"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/agent/presence"
	"github.com/some-du6e/pairtime/pkg/event"
)

// TestPresence checks how presence's records and seals reach the writer
// (WP-OS1's presence.Options.Record and Seal).
func TestPresence(t *testing.T) {
	at := time.UnixMilli(testT)
	sample := presence.Record{At: at, Data: &event.OSPresence{IdleAnyS: 2.5}}
	suspend := presence.Record{At: at.Add(time.Second), Data: &event.OSPower{Op: event.PowerSuspend}}

	t.Run("records taken before the writer opens are written once it does, as OS events with no project or session", func(t *testing.T) {
		dir := t.TempDir()
		p := NewPresence(nil)
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

	t.Run("an os.focus record the agent passes on is written in the order queued, as an OS event, with its local app_hash left out", func(t *testing.T) {
		dir := t.TempDir()
		p := NewPresence(nil)
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
		p := NewPresence(nil)
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

	t.Run("a seal with no writer to seal with returns within sealWait", func(t *testing.T) {
		p := NewPresence(nil)
		defer p.Close()
		start := time.Now()
		p.Seal(event.PowerLock)
		if d := time.Since(start); d < sealWait/2 || d > sealWait+5*time.Second {
			t.Errorf("Seal took %v, want about %v", d, sealWait)
		}
	})

	t.Run("records past the queue are dropped and counted, never blocking presence", func(t *testing.T) {
		var problems []string
		p := NewPresence(func(msg string) { problems = append(problems, msg) })
		defer p.Close()
		for range presenceQueue + 3 {
			p.Record(sample)
		}
		if p.Dropped() != 3 || len(problems) != 1 {
			t.Errorf("dropped %d with problems %q, want 3 and one problem", p.Dropped(), problems)
		}
	})
}
