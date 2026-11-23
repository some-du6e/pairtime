// SPDX-License-Identifier: AGPL-3.0-or-later

package ledger

import (
	"crypto/rand"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/some-du6e/pairtime/internal/agent/presence"
	"github.com/some-du6e/pairtime/pkg/event"
)

// presenceQueue is how many records Presence holds while the writer isn't
// open yet, as while the keystore waits for the user to unlock it.
// Presence samples at most every 15 s, so that is about an hour.
const presenceQueue = 256

// sealWait bounds how long Presence.Seal waits for its seal. Windows gives
// a suspend about two seconds before the machine sleeps (presence.Options).
const sealWait = 1500 * time.Millisecond

// Presence carries the presence component's records and seals to a Writer:
// its Record and Seal are presence.Options.Record and Seal (WP-OS1). It
// carries the focus component's os.focus records too, which the agent
// passes to Focus (WP-OS2), so every record the agent makes of the OS
// goes through one queue. It queues them on a goroutine of its own,
// because presence calls Record with its sampler locked and needs it to
// return at once, and because the writer may open after presence starts.
// Records go to the writer in the order they were queued, and a seal
// follows the records before it, so a suspend's os.power record is in the
// batch its seal makes.
type Presence struct {
	items   chan presenceItem
	attach  chan *Writer
	stop    chan struct{}
	done    chan struct{}
	once    sync.Once
	dropped atomic.Uint64
	problem func(string)

	// mu orders what Record and the goroutine say about dropped records,
	// so the last word is right: being dropped before the writer opens,
	// lost once it has.
	mu       sync.Mutex
	attached bool

	// queued counts the records queued and not yet written, by the times
	// their events carry, for Waiting.
	qmu    sync.Mutex
	queued map[queuedAt]int
}

// queuedAt is the times of a queued record's event (presenceEvent): its t
// and this run's mono.
type queuedAt struct{ t, mono int64 }

func queuedOf(r presence.Record) queuedAt { return queuedAt{t: r.At.UnixMilli(), mono: runMono(r.At)} }

// presenceItem is a record, or a seal when sealed is set, which the
// goroutine closes once the seal is done.
type presenceItem struct {
	rec    presence.Record
	sealed chan struct{}
}

// NewPresence starts a Presence. problem, when set, hears about records it
// had to drop and writes that failed. Its messages are about presence's
// queue only, so the agent files them apart from why the ledger isn't open.
func NewPresence(problem func(msg string)) *Presence {
	p := newPresence(problem)
	go p.run()
	return p
}

// newPresence is NewPresence without the goroutine, so a test can start
// run as late as a busy machine might.
func newPresence(problem func(msg string)) *Presence {
	return &Presence{
		items:   make(chan presenceItem, presenceQueue),
		attach:  make(chan *Writer, 1),
		stop:    make(chan struct{}),
		done:    make(chan struct{}),
		problem: problem,
		queued:  make(map[queuedAt]int),
	}
}

// Attach gives p the writer to write to, once it is open. Only the first
// call counts.
func (p *Presence) Attach(w *Writer) {
	select {
	case p.attach <- w:
	default:
	}
}

// Record queues r for the writer. It never blocks: when the queue is full,
// r is dropped and counted.
func (p *Presence) Record(r presence.Record) {
	q := queuedOf(r)
	p.count(q, 1)
	select {
	case p.items <- presenceItem{rec: r}:
	default:
		p.count(q, -1)
		p.drop()
	}
}

// count adds n records timed q to the ones queued.
func (p *Presence) count(q queuedAt, n int) {
	p.qmu.Lock()
	defer p.qmu.Unlock()
	if p.queued[q] += n; p.queued[q] <= 0 {
		delete(p.queued, q)
	}
}

// Waiting reports whether a record queued and not yet written is one that
// before says may have been made before config.toml was last seen
// local-only, as presence's records queued while a locked keyring kept the
// ledger shut through pairtime login are: the writer's Waiting asks it
// (agent.waiting), so the first batch after a switch back online takes its
// mark only once they are written, though presence's own records come in
// order, since the adapters' and the git watcher's can come between them.
func (p *Presence) Waiting(before func(event.Envelope) bool) bool {
	p.qmu.Lock()
	defer p.qmu.Unlock()
	for q := range p.queued {
		if before(event.Envelope{T: q.t, Mono: q.mono, BootID: runBoot}) {
			return true
		}
	}
	return false
}

// drop counts a record Record dropped and says so.
func (p *Presence) drop() {
	p.mu.Lock()
	defer p.mu.Unlock()
	n := p.dropped.Add(1)
	if p.problem == nil {
		return
	}
	switch {
	case p.attached:
		p.problem(fmt.Sprintf("the ledger fell behind, and %d presence and focus records were dropped", n))
	case n == 1:
		p.problem("the ledger isn't open, so presence and focus records are being dropped")
	}
}

// markAttached marks the writer open, and when records were dropped before
// it opened, says how many are lost.
func (p *Presence) markAttached() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.attached = true
	if n := p.dropped.Load(); n > 0 && p.problem != nil {
		p.problem(fmt.Sprintf("%d presence and focus records were dropped while the ledger wasn't open", n))
	}
}

// Focus queues an os.focus record of the focus component, which the agent
// passes on from focus.Options.Record (WP-OS2). The record of a focus
// change is queued while presence is locked, just before the sample
// presence takes for that change (presence.Sampler.FocusAt), so the two
// reach the writer in that order, with one time. Like Record, it never
// blocks.
func (p *Presence) Focus(at time.Time, d *event.OSFocus) {
	p.Record(presence.Record{At: at, Data: d})
}

// Seal seals a batch with every record presence took before it, and waits
// for the seal, at most sealWait, so a suspend's batch is on disk before
// the machine sleeps (DESIGN 3.3, 3.6).
func (p *Presence) Seal(event.PowerOp) {
	sealed := make(chan struct{})
	select {
	case p.items <- presenceItem{sealed: sealed}:
	default:
		return // full: no writer is taking them
	}
	t := time.NewTimer(sealWait)
	defer t.Stop()
	select {
	case <-sealed:
	case <-t.C:
	case <-p.done:
	}
}

// Dropped returns how many records Record had to drop.
func (p *Presence) Dropped() uint64 { return p.dropped.Load() }

// Close stops p once it has written what it holds, if Attach came before
// it. It doesn't close the writer.
func (p *Presence) Close() {
	p.once.Do(func() { close(p.stop) })
	<-p.done
}

func (p *Presence) run() {
	defer close(p.done)
	var w *Writer
	select {
	case w = <-p.attach:
	case <-p.stop:
		// When Attach and Close both come before this goroutine first
		// runs, as on a busy machine, the select above finds the writer
		// and the stop both ready and takes either at random. A writer
		// attached before Close still gets what p holds.
		select {
		case w = <-p.attach:
		default:
			return
		}
	}
	p.markAttached()
	for {
		select {
		case it := <-p.items:
			p.handle(w, it)
		case <-p.stop:
			for {
				select {
				case it := <-p.items:
					p.handle(w, it)
				default:
					return
				}
			}
		}
	}
}

func (p *Presence) handle(w *Writer, it presenceItem) {
	if it.sealed != nil {
		_, _, err := w.Seal()
		close(it.sealed)
		if err != nil && p.problem != nil {
			p.problem(fmt.Sprintf("the ledger couldn't seal a batch on lock or suspend: %v", err))
		}
		return
	}
	_, err := w.Write(Input{Event: presenceEvent(it.rec)})
	p.count(queuedOf(it.rec), -1)
	if err != nil && p.problem != nil {
		p.problem(fmt.Sprintf("the ledger couldn't write an %s record: %v", it.rec.Data.Kind(), err))
	}
}

// presenceEvent gives a presence or focus record the envelope of an OS
// event (SM 1.1, 1.2): it has no project and no session, and came from the
// OS.
func presenceEvent(r presence.Record) event.Event {
	return event.Event{
		Envelope: event.Envelope{
			EventID:    event.NewEventID(r.At),
			T:          r.At.UnixMilli(),
			Mono:       runMono(r.At),
			BootID:     runBoot,
			Source:     event.SourceOS,
			Via:        event.ViaOS,
			Harness:    event.HarnessNone,
			SessionKey: event.NoSession,
		},
		Data: r.Data,
	}
}

// The boot_id and mono of the events the agent makes itself, such as
// presence's. Until the agent reads the OS's boot clock, which WP-AG3
// brings for hooks, they are this run's own: a random id per agent
// process, and milliseconds of Go's monotonic clock since it started. SM
// 1.1 re-anchors a time only within one boot_id, so a run's own id keeps
// that sound; it re-anchors across a run rather than a whole boot.
var (
	runStart = time.Now()
	runBoot  = runPrefix + strings.ToLower(rand.Text())
)

// runPrefix starts every run's own boot_id (runBoot), which sets it apart
// from the OS's boot ids that hook calls carry.
const runPrefix = "run-"

// runMono is at's reading of this run's monotonic clock, in ms.
func runMono(at time.Time) int64 { return max(at.Sub(runStart).Milliseconds(), 0) }

// RunClock returns the mono and boot_id of an event the agent makes itself
// at at, such as the git watcher's git.change: this run's, as presence's
// records carry them, so all of the agent's own events re-anchor together
// (SM 1.1).
func RunClock(at time.Time) (mono int64, bootID string) { return runMono(at), runBoot }
