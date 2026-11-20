// SPDX-License-Identifier: AGPL-3.0-or-later

// Package scorer is the agent's scorer loop (DESIGN 3.3, 3.7; docs/PLAN.md,
// WP-AG4). It steps pkg/score over the device's sealed ledger batches, one
// Step per batch in batch order, writes the minute buckets each Step
// changes to the bucket store and the day rollups they change to days/,
// and keeps its place in state.json, so a restart, a crash or a lost
// state.json picks up again from the ledger.
//
// # What it scores
//
// Only sealed records. The ledger writer hands the loop each batch once it
// is on disk (ledger.Options.Sealed), and at start the loop reads the
// batches it hasn't scored from the ledger's files. Every record the
// device writes takes the next number, checkpoints and upload-failure
// notes included, and only sealed records keep theirs, so a batch whose
// numbers don't follow the last one's is damage: the loop stops scoring
// there and says so to doctor (decision E-sm-1.1-seq-gaps). The writer
// seals every BATCH_S while there are new records and at once after a
// prompt or a stop, so the loop steps every 60 s and after each prompt or
// stop hook while anything is recorded, and never wakes while nothing is.
//
// # Final and pending
//
// A minute's bucket is pending until FinalBefore passes the minute's end,
// and final from then on, where FinalBefore is the largest that any Step
// gave, so it never goes back. pkg/score puts FinalBefore its settle
// behind its present, and once settle is PENDING_WINDOW_S a minute stays
// pending for PENDING_WINDOW_S and is final after it (decision
// E-return-lookahead; DESIGN 3.7). A final bucket never changes again. A
// pending one gets a new revision each time a Step changes it, the change
// to final included, with rev one higher (DESIGN 6.2 D1). A pending minute
// that turns out to have no credit and no agent time gets an empty
// revision, so the one before it is superseded.
//
// # The window
//
// Until WP-SC5 adds checkpoints, a score.State holds every record it is
// given, and Step derives all of them again each time. So the loop keeps
// the records from Lookback (6 h) before FinalBefore on, and stands for
// the ones before with the carry: the last record of each kind whose
// effect lasts until a later one ends it (window.go). Each Step scores the
// window and the carry in a new State. What that derives at and after
// FinalBefore is what a State holding every record would, because no rule
// bounded in time reaches back further than BUCKET_TTL_S (2 h), and the
// carry stands for the rules that aren't bounded. A browser run longer
// than Lookback is what it misses: its lookup credit (SM 2.5) rests on a
// bracket before the run.
//
// Each Step is a function of the batches so far. A restart reads the
// window's batches again; a lost state.json scores every batch again from
// the first; and in both the loop writes a revision only where the store's
// latest one for the minute came from an earlier batch, so the store ends
// up as if the loop had never stopped.
package scorer

import (
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/some-du6e/pairtime/internal/agent/ledger"
	"github.com/some-du6e/pairtime/internal/agent/state"
	"github.com/some-du6e/pairtime/internal/agent/store"
	"github.com/some-du6e/pairtime/pkg/event"
	"github.com/some-du6e/pairtime/pkg/params"
	"github.com/some-du6e/pairtime/pkg/score"
)

// StateKey is the loop's key in state.json.
const StateKey = "scorer"

// DefaultLookback is how far before FinalBefore the window starts: three
// times BUCKET_TTL_S, the longest reach of a rule bounded in time.
const DefaultLookback = 6 * time.Hour

// queueMax bounds the batches waiting for the loop. Past it the loop drops
// them and reads them from the ledger's files instead.
const queueMax = 1024

// dayMS is a day in milliseconds.
const dayMS = 24 * 3600 * 1000

var (
	// errEnough ends a read of the ledger early.
	errEnough = errors.New("scorer: read enough")
	// errStopping ends a read of the ledger because the loop is stopping.
	errStopping = errors.New("scorer: stopping")
)

// Options configure New.
type Options struct {
	// Ledger is the ledger directory, home.Dirs.Ledger().
	Ledger string
	// Params is the table the scorer runs with, the one the ledger writer
	// and presence get too.
	Params *params.Table
	// Device says what pkg/score needs to know about the machine.
	Device score.Device
	// State is state.json, where the loop keeps its place under StateKey.
	State *state.Store
	// Buckets is the bucket store, and Days the days directory,
	// home.Dirs.Days().
	Buckets *store.Buckets
	Days    string
	// Zone returns the time zone a day is counted in: the device's, since
	// SM 5.3's day is the person's local day. Nil means time.Local.
	Zone func() *time.Location
	// Problem gets a description of what doctor should hear, and "" once
	// it is solved.
	Problem func(msg string)
	// Log gets the loop's log. Nil discards it.
	Log *slog.Logger
	// Lookback, when set, replaces DefaultLookback.
	Lookback time.Duration
}

// cursor is the loop's place, which it keeps in state.json.
type cursor struct {
	// Device is the device whose chain the loop scores.
	Device string `json:"device"`
	// Batch is the last batch scored, and Seq the number of its last
	// record.
	Batch uint64 `json:"batch"`
	Seq   uint64 `json:"seq"`
	// FinalBefore is the largest FinalBefore a Step gave, in Unix ms.
	FinalBefore int64 `json:"final_before"`
	// From is where the window starts, in Unix ms, and FromBatch the first
	// batch that holds one of its records: where a restart reads from.
	From      int64  `json:"from"`
	FromBatch uint64 `json:"from_batch"`
	// Carry stands for the records before the window.
	Carry carry `json:"carry"`
}

// Status is what the loop reports about itself.
type Status struct {
	// Device is the device whose chain the loop scores, Batch the last
	// batch it scored and FinalBefore, in Unix ms, the time before which
	// every minute is final. All are zero before the loop has read the
	// ledger.
	Device      string
	Batch       uint64
	FinalBefore int64
	// Records counts the records in the window, and Skipped the records
	// since the start that the loop couldn't read and didn't score.
	Records int
	Skipped uint64
	// LastBreak is the time, in Unix ms, of the latest lock or suspend the
	// loop has seen, 0 for none: a work session that ended before it is
	// over (DESIGN 3.8).
	LastBreak int64
	// Stopped says why the loop stopped scoring, when it did.
	Stopped string
}

// Loop is the scorer loop. Make one with New, hand it every sealed batch
// with Sealed, start it with Start once the ledger is open, and stop it
// with Close after the ledger writer is closed.
type Loop struct {
	opts     Options
	log      *slog.Logger
	lookback int64
	dayCap   int64

	mu      sync.Mutex
	queue   []ledger.Batch
	lost    bool // the queue dropped batches
	waiters []chan struct{}
	started bool
	closed  bool

	kick chan struct{}
	stop chan struct{}
	done chan struct{}

	status atomic.Pointer[Status]

	// The rest belongs to the loop's goroutine.
	cur     cursor
	win     window
	pending map[int64]bool  // minutes whose latest revision is pending
	days    map[string]bool // local days whose rollups are out of date
	stuck   error           // why the loop stopped scoring
	opened  bool            // open read the loop's place
	// rebuild is set while the loop scores the ledger from its first
	// batch because state.json had no place for it. It then rewrites the
	// rollup of every day it scores, which a lost days/ needs.
	rebuild   bool
	skipped   uint64
	lastBreak int64
	// batching is set while the loop scores the batches it found in the
	// ledger at start. It saves its place and writes rollups once at the
	// end, rather than at every Step.
	batching bool
	troubled bool // a write failed since the last save that worked
}

// New returns a loop that hasn't started.
func New(opts Options) (*Loop, error) {
	switch {
	case opts.Params == nil:
		return nil, errors.New("scorer: New needs the parameter table")
	case opts.State == nil || opts.Buckets == nil:
		return nil, errors.New("scorer: New needs state.json and the bucket store")
	case !filepath.IsAbs(opts.Ledger) || !filepath.IsAbs(opts.Days):
		return nil, errors.New("scorer: the ledger and days directories must be absolute paths")
	}
	lookback := opts.Lookback
	if lookback <= 0 {
		lookback = DefaultLookback
	}
	log := opts.Log
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	l := &Loop{
		opts:     opts,
		log:      log,
		lookback: lookback.Milliseconds(),
		dayCap:   opts.Params.Int(params.DAY_CAP_S),
		kick:     make(chan struct{}, 1),
		stop:     make(chan struct{}),
		done:     make(chan struct{}),
		pending:  make(map[int64]bool),
		days:     make(map[string]bool),
	}
	l.status.Store(&Status{})
	return l, nil
}

// Sealed takes a batch the ledger writer sealed. It never blocks, so it
// can be ledger.Options.Sealed. Batches it takes after Close wait in the
// ledger for the next start.
func (l *Loop) Sealed(b ledger.Batch) {
	l.mu.Lock()
	switch {
	case l.closed:
	case len(l.queue) >= queueMax:
		l.lost = true
	default:
		l.queue = append(l.queue, b)
	}
	l.mu.Unlock()
	l.wake()
}

func (l *Loop) wake() {
	select {
	case l.kick <- struct{}{}:
	default: // a wakeup is already due
	}
}

// Start starts the loop on a goroutine of its own. It reads where the loop
// was from state.json and scores the batches the ledger holds that it
// hasn't, then every batch Sealed hands it. Only the first call does
// anything, and none after Close.
func (l *Loop) Start() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.started || l.closed {
		return
	}
	l.started = true
	go l.run()
}

// Close scores the batches Sealed took before it, saves the loop's place
// and stops the loop. A loop that never started just stops.
func (l *Loop) Close() {
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return
	}
	l.closed = true
	started := l.started
	l.mu.Unlock()
	close(l.stop)
	if started {
		<-l.done
	}
}

// Flush waits until the loop has scored every batch Sealed took before
// the call, or has stopped.
func (l *Loop) Flush() {
	ch := make(chan struct{})
	l.mu.Lock()
	if !l.started {
		l.mu.Unlock()
		return
	}
	l.waiters = append(l.waiters, ch)
	l.mu.Unlock()
	l.wake()
	select {
	case <-ch:
	case <-l.done:
	}
}

// Status returns what the loop last reported.
func (l *Loop) Status() Status { return *l.status.Load() }

func (l *Loop) run() {
	defer close(l.done)
	if err := l.open(); err != nil {
		l.halt(fmt.Errorf("it couldn't start: %w", err))
	} else {
		l.catchUp()
	}
	for {
		l.drain()
		select {
		case <-l.stop:
			l.drain()
			l.save()
			l.release()
			return
		case <-l.kick:
		}
	}
}

// stopping reports whether Close was called.
func (l *Loop) stopping() bool {
	select {
	case <-l.stop:
		return true
	default:
		return false
	}
}

// release lets every Flush caller go.
func (l *Loop) release() {
	l.mu.Lock()
	ws := l.waiters
	l.waiters = nil
	l.mu.Unlock()
	for _, w := range ws {
		close(w)
	}
}

// open reads the loop's place from state.json and builds the window again
// from the batches it names, and the pending minutes from the store.
func (l *Loop) open() error {
	chain, err := ledger.ReadChain(l.opts.Ledger)
	if err != nil {
		return fmt.Errorf("reading the ledger's chain: %w", err)
	}
	var c cursor
	ok, err := l.opts.State.Get(StateKey, &c)
	switch {
	case err != nil:
		l.log.Warn("the scorer's place in state.json doesn't read, so it scores the ledger from its first batch", "err", err)
		c = cursor{}
	case !ok:
		l.log.Info("state.json has no scorer place, so the scorer scores the ledger from its first batch")
	case c.Device != chain.DeviceID:
		l.log.Info("the ledger is a new device's, so the scorer scores it from its first batch")
		c = cursor{}
	}
	c.Device = chain.DeviceID
	l.cur = c
	l.win = window{from: c.From, carry: c.Carry}
	if c.Batch > 0 && c.FromBatch > 0 && c.FromBatch <= c.Batch {
		err := ledger.ReadBatches(l.opts.Ledger, c.FromBatch, func(b ledger.Batch) error {
			if b.Row.B > c.Batch {
				return errEnough
			}
			for _, rec := range b.Records {
				ev, ok, err := eventOf(rec, c.Device)
				if err == nil && ok && ev.T >= l.win.from {
					l.win.recs = append(l.win.recs, held{ev: ev, b: b.Row.B})
				}
			}
			return nil
		})
		if err != nil && !errors.Is(err, errEnough) {
			return fmt.Errorf("reading the window's batches again: %w", err)
		}
	}
	pending, err := l.opts.Buckets.Pending(c.FinalBefore - minuteMS + 1)
	if err != nil {
		return fmt.Errorf("reading the pending minutes: %w", err)
	}
	for _, b := range pending {
		if b.Device == c.Device {
			l.pending[b.At()] = true
			continue
		}
		// A minute of a chain this ledger no longer writes, as after a
		// restore: nothing scores it again.
		b.State = event.StateFinal
		if _, _, err := l.opts.Buckets.Put(b); err != nil {
			l.log.Warn("couldn't close a minute of the old device", "err", err)
		}
	}
	l.batching = true
	l.rebuild = c.Batch == 0
	l.opened = true
	l.report()
	return nil
}

// catchUp scores the batches in the ledger after the loop's place.
func (l *Loop) catchUp() {
	l.readLedger(0)
	l.save()
	l.batching, l.rebuild = false, false
}

// drain scores the batches Sealed took, until there are none.
func (l *Loop) drain() {
	for {
		l.mu.Lock()
		q, lost := l.queue, l.lost
		l.queue, l.lost = nil, false
		if len(q) == 0 && !lost {
			ws := l.waiters
			l.waiters = nil
			l.mu.Unlock()
			for _, w := range ws {
				close(w)
			}
			return
		}
		l.mu.Unlock()
		if lost {
			l.readLedger(0)
		}
		for _, b := range q {
			l.take(b)
		}
	}
}

// take scores b, reading any batch between the last one scored and b from
// the ledger first.
func (l *Loop) take(b ledger.Batch) {
	if l.stuck != nil || b.Row.B <= l.cur.Batch {
		return
	}
	if b.Row.B > l.cur.Batch+1 {
		// The batches between were sealed before the queue had them, or
		// the queue dropped them.
		if err := l.readLedger(b.Row.B - 1); err != nil || l.stuck != nil {
			return
		}
		if l.cur.Batch != b.Row.B-1 {
			l.halt(fmt.Errorf("%w: batches %d to %d aren't in the ledger's files", errGap, l.cur.Batch+1, b.Row.B-1))
			return
		}
	}
	l.apply(b)
}

// readLedger scores the sealed batches in the ledger's files after the
// last one scored, up to batch upTo, or every one when upTo is 0. It
// returns errStopping when the loop is stopping, and the error that halted
// the loop when one did.
func (l *Loop) readLedger(upTo uint64) error {
	if l.stuck != nil {
		return l.stuck
	}
	err := ledger.ReadBatches(l.opts.Ledger, l.cur.Batch+1, func(b ledger.Batch) error {
		switch {
		case upTo != 0 && b.Row.B > upTo:
			return errEnough
		case l.stopping():
			return errStopping
		}
		l.apply(b)
		if l.stuck != nil {
			return l.stuck
		}
		return nil
	})
	switch {
	case err == nil || errors.Is(err, errEnough):
		return nil
	case errors.Is(err, errStopping) || l.stuck != nil:
		return err
	}
	l.halt(fmt.Errorf("reading the ledger: %w", err))
	return err
}

// apply scores batch b, which must follow the last batch scored.
func (l *Loop) apply(b ledger.Batch) {
	if err := checkBatch(b, l.cur.Batch, l.cur.Seq); err != nil {
		l.halt(err)
		return
	}
	added := 0
	for _, rec := range b.Records {
		ev, ok, err := eventOf(rec, l.cur.Device)
		switch {
		case err != nil:
			l.skipped++
			l.log.Warn("the scorer skipped a record it couldn't read", "batch", b.Row.B, "err", err)
		case ok:
			l.win.add(ev, b.Row.B)
			added++
		}
	}
	l.cur.Batch, l.cur.Seq = b.Row.B, b.Row.FirstSeq+b.Row.N-1
	if added > 0 {
		l.step(b.Row.B)
	} else if !l.batching {
		l.save()
	}
	l.report()
}

// step scores the window after batch b and stores what changed.
func (l *Loop) step(b uint64) {
	recs := l.win.records()
	out, err := l.score(recs)
	if err != nil {
		l.halt(fmt.Errorf("batch %d: %w", b, err))
		return
	}
	for _, e := range recs {
		if p, ok := e.Data.(*event.OSPower); ok && (p.Op == event.PowerLock || p.Op == event.PowerSuspend) {
			l.lastBreak = max(l.lastBreak, e.T)
		}
	}
	prev := l.cur.FinalBefore
	fb := max(prev, out.FinalBefore)
	meta := bucketMeta{v: l.opts.Params.Version(), params: l.opts.Params.Hash(), device: l.cur.Device}
	var all score.Derived
	all.Append(out.Final)
	all.Append(out.Pending)
	view := minuteBuckets(meta, all, recs)
	// The minutes this Step can change: every one that wasn't final before
	// it, as far as the view or the store has anything for them.
	visit := make(map[int64]bool, len(l.pending))
	for m := range view {
		if m+minuteMS > prev {
			visit[m] = true
		}
	}
	for m := range l.pending {
		visit[m] = true
	}
	loc := l.zone()
	for _, m := range slices.Sorted(maps.Keys(visit)) {
		bk, ok := view[m]
		if !ok {
			if !l.pending[m] {
				continue
			}
			bk = emptyBucket(meta, m)
		}
		bk.State = event.StatePending
		if m+minuteMS <= fb {
			bk.State = event.StateFinal
		}
		bk.Batch = b
		if l.put(bk) || l.rebuild {
			l.days[time.UnixMilli(m).In(loc).Format(time.DateOnly)] = true
		}
		if bk.State == event.StatePending {
			l.pending[m] = true
		} else {
			delete(l.pending, m)
		}
	}
	l.cur.FinalBefore = fb
	l.win.slide(fb - l.lookback)
	if !l.batching {
		l.save()
	} else {
		// Scoring a long ledger again touches every day it covers. The
		// days whose minutes are all final can go from memory.
		l.forget()
	}
}

// forget drops from the bucket store's memory the days that ended two days
// before FinalBefore: every minute of them is final, so the loop never
// writes to them again.
func (l *Loop) forget() {
	if err := l.opts.Buckets.Forget(l.cur.FinalBefore - 2*dayMS); err != nil {
		l.log.Warn("closing old bucket files", "err", err)
	}
}

// score steps a new State over recs, the window's records. pkg/score has
// no I/O and no errors, so a panic is a bug in it; the loop stops scoring
// rather than take the agent down.
func (l *Loop) score(recs []event.Event) (out score.Output, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("pkg/score panicked: %v", r)
		}
	}()
	_, out = score.Step(score.NewState(l.opts.Params, l.opts.Device), recs)
	return out, nil
}

// put stores bk, unless the store's latest revision of the minute came
// from a later batch, as it does when the loop scores batches again. It
// reports whether it wrote a revision.
func (l *Loop) put(bk store.Bucket) bool {
	cur, ok, err := l.opts.Buckets.Latest(bk.At())
	if err == nil && ok && cur.Device == bk.Device && cur.Batch > bk.Batch {
		return false
	}
	_, wrote, err := l.opts.Buckets.Put(bk)
	switch {
	case errors.Is(err, store.ErrFinal):
		// Scoring batches again meets the final revisions it wrote the
		// first time. A final one that comes out different would be a bug.
		if bk.State == event.StateFinal {
			l.log.Warn("a final minute scored again came out different, and keeps its first bucket", "minute", bk.Minute)
		}
		return false
	case err != nil:
		l.fault(fmt.Sprintf("the scorer couldn't store a minute's bucket: %v", err))
		return false
	}
	return wrote
}

// save writes the out-of-date day rollups, flushes the bucket files and
// then saves the loop's place, so the place never runs ahead of the
// buckets on disk.
func (l *Loop) save() {
	if !l.opened {
		return
	}
	ok := true
	loc := l.zone()
	for _, date := range slices.Sorted(maps.Keys(l.days)) {
		if err := l.writeDay(date, loc); err != nil {
			ok = false
			l.fault(fmt.Sprintf("the scorer couldn't write the rollup of %s: %v", date, err))
		}
		l.forget() // after a rebuild there may be months of them
	}
	clear(l.days)
	if err := l.opts.Buckets.Sync(); err != nil {
		l.fault(fmt.Sprintf("the scorer couldn't flush its buckets, so it scores from its last place again after a restart: %v", err))
		return
	}
	c := l.cur
	c.From, c.FromBatch, c.Carry = l.win.from, l.win.firstBatch(), l.win.carry
	if c.FromBatch == 0 {
		c.FromBatch = c.Batch + 1
	}
	if err := l.opts.State.Put(StateKey, c); err != nil {
		l.fault(fmt.Sprintf("the scorer couldn't keep its place: %v", err))
		return
	}
	if err := l.opts.State.Save(); err != nil {
		l.fault(fmt.Sprintf("the scorer couldn't save its place in state.json: %v", err))
		return
	}
	l.forget()
	if ok && l.troubled && l.stuck == nil {
		l.troubled = false
		l.problem("")
	}
}

// writeDay rewrites the rollup of the local day date.
func (l *Loop) writeDay(date string, loc *time.Location) error {
	start, end, err := store.DayBounds(date, loc)
	if err != nil {
		return err
	}
	bs, err := l.opts.Buckets.Range(start.UnixMilli(), end.UnixMilli())
	if err != nil {
		return err
	}
	d, err := store.Rollup(date, loc, bs, l.dayCap)
	if err != nil {
		return err
	}
	return store.WriteDay(l.opts.Days, d)
}

// halt stops scoring: the loop keeps what it stored, takes no more
// batches, and says why until the agent restarts.
func (l *Loop) halt(err error) {
	if l.stuck != nil {
		return
	}
	l.stuck = err
	l.log.Warn("the scorer stopped", "batch", l.cur.Batch, "err", err)
	l.problem(fmt.Sprintf("the scorer stopped after ledger batch %d, so time after that isn't counted: %v", l.cur.Batch, err))
	l.report()
}

// fault reports a failed write, which the next save that works clears.
func (l *Loop) fault(msg string) {
	l.troubled = true
	l.problem(msg)
}

func (l *Loop) problem(msg string) {
	if l.opts.Problem != nil {
		l.opts.Problem(msg)
	}
}

func (l *Loop) zone() *time.Location {
	if l.opts.Zone != nil {
		if loc := l.opts.Zone(); loc != nil {
			return loc
		}
	}
	return time.Local
}

// report publishes the loop's status.
func (l *Loop) report() {
	s := &Status{Device: l.cur.Device, Batch: l.cur.Batch, FinalBefore: l.cur.FinalBefore, Records: len(l.win.recs),
		Skipped: l.skipped, LastBreak: l.lastBreak}
	if l.stuck != nil {
		s.Stopped = l.stuck.Error()
	}
	l.status.Store(s)
}
