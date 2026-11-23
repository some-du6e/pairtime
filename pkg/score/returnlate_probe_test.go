// SPDX-License-Identifier: AGPL-3.0-or-later

package score

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/some-du6e/pairtime/pkg/event"
)

// TestProbeReturnUndoneByLateGap is a probe, not for merging: SM 2.1's
// return example with the samples stopping at the prompt that ends the
// silence, the turn's stop at 1,400 s sealed alone, and the sample taken
// at that stop sealed in the next batch, 0 s late. It reports what a Step
// a batch finalizes and what one Step over every record derives.
func TestProbeReturnUndoneByLateGap(t *testing.T) {
	b := newTL(false)
	b.focus(at(0), event.AppTerminal, "p_1", "s_1")
	b.start(at(0), "s_1", "p_1", event.EntrypointCLI, event.ParentTerminalApp, true)
	b.mouse(every(at(1), at(96), 5000)...)
	b.mouse(at(100))
	b.prompt(at(30), event.ViaHook, "s_1", "p_1", "t_1", 30)
	b.harness(at(102), event.ViaLog, "s_1", "p_1", &event.AgentOutput{TurnKey: "t_1", Words: 160, IsFinal: true})
	b.harness(at(102), event.ViaHook, "s_1", "p_1", &event.AgentStop{TurnKey: "t_1", StopReason: event.StopEndTurn})
	b.press(every(atMS(451_500), atMS(469_500), 1000)...)
	b.samples(at(0), at(465))
	b.prompt(at(470), event.ViaHook, "s_1", "p_1", "t_2", 30)
	b.sample(at(470))
	stops(b, at(1400), "s_1", "p_1", "t_2")
	b.sample(at(1400))
	recs := b.records()

	// A batch a minute, with the sample at 1,400 s in a batch of its own
	// after the stop's.
	var batches [][]event.Event
	var late event.Event
	for i := 0; i < len(recs); {
		j := i
		for j < len(recs) && floorMinute(recs[j].T) == floorMinute(recs[i].T) {
			j++
		}
		var kept []event.Event
		for _, e := range recs[i:j] {
			if _, ok := e.Data.(*event.OSPresence); ok && e.T == at(1400) {
				late = e
				continue
			}
			kept = append(kept, e)
		}
		batches = append(batches, kept)
		i = j
	}
	if late.Data == nil {
		t.Fatal("no sample at 1,400 s")
	}
	batches = append(batches, []event.Event{late})

	s := NewState(testTable(t), Device{})
	var finals Derived
	var out Output
	for k, batch := range batches {
		s, out = Step(s, batch)
		finals.Append(out.Final)
		if k >= len(batches)-2 {
			t.Logf("batch %d (newest %d s): FinalBefore %d s, returns final %d, pending %d", k,
				(batch[len(batch)-1].T-base)/1000, (out.FinalBefore-base)/1000, len(out.Final.Returns), len(out.Pending.Returns))
		}
	}
	var all Derived
	all.Append(finals)
	all.Append(out.Pending)
	one := scoreAll(t, Device{}, recs)
	desc := func(rs []Return) string {
		var b bytes.Buffer
		for _, r := range rs {
			fmt.Fprintf(&b, "[run %d-%d s, credit %d ms] ", (r.RunStart-base)/1000, (r.RunEnd-base)/1000, r.CreditMS)
		}
		return b.String()
	}
	t.Logf("a Step a batch: returns %s; one Step: returns %s", desc(all.Returns), desc(one.Returns))
	for m := int64(0); m < 1440; m += 60 {
		got, want := sumIn(all, at(m), at(m+60)), sumIn(one, at(m), at(m+60))
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Logf("minute %d-%d s: a Step a batch %v, one Step %v", m, m+60, got, want)
		}
	}
	if got, want := marshal(t, all), marshal(t, one); !bytes.Equal(got, want) {
		t.Errorf("a Step a batch differs from one Step %s", firstDiff(got, want))
	}
}
