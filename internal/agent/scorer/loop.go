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
// batches it hasn't scored from the ledger's files, from the first batch
// they still hold when state.json has no place for it. Every record the
// device writes takes the next number, checkpoints and upload-failure
// notes included, and only sealed records keep theirs, so no gap is ever
// legitimate (decision E-sm-1.1-seq-gaps). A batch whose numbers don't
// follow the last one's fails: the loop doesn't score its records, says so
// to doctor, and goes on from the batch's own row, so one gap costs only
// the batch it shows in, whose minutes the server's CC7 holds. A batch the
// files hold damaged, or don't hold, fails the same way. The writer seals
// every BATCH_S while there are new records and at once after a prompt or
// a stop, so the loop steps every 60 s and after each prompt or stop hook
// while anything is recorded, and never wakes while nothing is.
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
// that a Step derives with no credit and no agent time gets an empty
// revision, so the one before it is superseded. One that a Step doesn't
// derive at all keeps its latest revision, and stays pending unless a later
// Step derives it, so no Step zeroes a minute it never looked at.
//
// # The window
//
// Until WP-SC5 adds checkpoints, a score.State holds every record it is
// given, and Step derives all of them again each time. So the loop keeps
// the records from Lookback (6 h) before FinalBefore on, and stands for
// the ones before with the carry: the last record of each kind whose
// effect lasts until a later one ends it (window.go). A turn's run has no
// bound in time (SM 2.6), so a turn that ran into the window holds the
// window's start at the turn's submit, for up to holdMaxMS: its run, its
// origin and its class come from its own records. Past that, and once a
// run is over, the carry keeps the session's last prompted turn's latest
// agent record and whether its run was cut or it stopped, and dates what
// it carries so the run goes on, or stays over, as it does with every
// record. Each Step scores the window and the carry in a new State. What
// that derives at and after FinalBefore is what a State holding every
// record would, because no rule bounded in time reaches back further than
// BUCKET_TTL_S (2 h), and the window and the carry stand for the rules
// that aren't bounded. What it misses is a browser run longer than
// Lookback, whose lookup credit (SM 2.5) rests on a bracket before the
// run; a prompted turn that runs longer than holdMaxMS and whose origin
// rests on input it carries no record of; and a turn a stop hook continued
// without a prompt that runs longer than holdMaxMS, which the carry keeps
// by its latest output and stop alone.
//
// # Its place
//
// Each Step is a function of the batches so far. A restart reads the
// window's batches again; a lost state.json scores every batch again from
// the first the ledger holds; and in both the loop writes a revision only
// where the store's latest one for the minute came from an earlier batch,
// so the store ends up as if the loop had never stopped.
//
// The place saved in state.json never runs ahead of what is on disk: the
// loop saves it only once every revision up to it is stored and flushed.
// A revision whose write fails stays to be written again at the next save,
// a rollup whose write fails is written again at the next save, after a
// restart too, and a flush that fails is tried again at the next save, so
// none is dropped. Until a save stores and flushes everything, the place
// stays where it was and doctor hears why; a restart then scores from
// there again. A revision found written after the saved place, as a crash
// between a Step's appends and its save leaves one, has its day's rollup
// rewritten and its file flushed.
package scorer

import (
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"path/filepath"
	"slices"
	"strings"
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

// holdMaxMS bounds how far before Lookback a running turn holds the
// window's start: a day.
const holdMaxMS = 24 * 3600 * 1000

// dayMS is a day in milliseconds.
const dayMS = 24 * 3600 * 1000

var (
	// errEnough ends a read of the ledger early.
	errEnough = errors.New("scorer: read enough")
	// errStopping ends a read of the ledger because the loop is stopping.
	errStopping = errors.New("scorer: stopping")
)

// BucketStore is what the loop needs of the bucket store, *store.Buckets.
type BucketStore interface {
	Latest(ms int64) (store.Bucket, bool, error)
	Put(b store.Bucket) (store.Bucket, bool, error)
	Unflushed(ms int64) error
	Range(from, to int64) ([]store.Bucket, error)
	Pending(from int64) ([]store.Bucket, error)
	Sync() error
	Forget(before int64) error
	// KeptFrom is the first UTC day whose minutes the store keeps, "" for
	// all (store.Buckets.KeptFrom).
	KeptFrom() string
}

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
	Buckets BucketStore
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
	// Batch is the last batch taken, scored or failed, and Seq the number
	// of its last record.
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
	// Days are the local days whose rollups a save couldn't write, which
	// the next save writes, after a restart too.
	Days []string `json:"days,omitempty"`
}

// Status is what the loop reports about itself.
type Status struct {
	// Device is the device whose chain the loop scores, Batch the last
	// batch it took and FinalBefore, in Unix ms, the time before which
	// every minute is final. All are zero before the loop has read the
	// ledger.
	Device      string
	Batch       uint64
	FinalBefore int64
	// Records counts the records in the window, and Skipped the records
	// since the start that the loop couldn't read or that were in a batch
	// that failed, and didn't score. Failed counts the batches that
	// failed: missing from the ledger's files, damaged in them, or
	// numbered out of turn (decision E-sm-1.1-seq-gaps).
	Records int
	Skipped uint64
	Failed  uint64
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
	gaps     runGaps
	// writeDayFile writes a day's rollup, store.WriteDay but in tests.
	writeDayFile func(dir string, d store.Day) error

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
	cur cursor
	// base is the last batch the saved place covers: every revision up to
	// it is flushed and its rollups written. A stored revision from a later
	// batch was written after it, by this run or by one a crash stopped.
	base    uint64
	win     window
	pending map[int64]bool  // minutes whose latest revision is pending
	days    map[string]bool // local days whose rollups are out of date
	// unwritten holds the revisions whose writes failed, by minute, which
	// the next save writes again.
	unwritten map[int64]store.Bucket
	stuck     error // why the loop stopped scoring
	opened    bool  // open read the loop's place
	skipped   uint64
	failed    uint64
	lastBreak int64
	// damage is why the last batch the ledger's files couldn't give
	// didn't read, which apply names when it passes over that batch.
	// failedTo is the last batch counted as failed while the loop had no
	// place, so a read again doesn't count it twice.
	damage   error
	failedTo uint64
	// batching is set while the loop scores the batches it found in the
	// ledger at start. It saves its place and writes rollups once at the
	// end, rather than at every Step.
	batching bool
	// What doctor hears: why the loop stopped, the last batch that failed,
	// why the ledger's files didn't read, until a read works, and the last
	// write that failed, until a save stores everything.
	stuckMsg, gapMsg, readMsg, faultMsg string
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
		opts:         opts,
		log:          log,
		lookback:     lookback.Milliseconds(),
		dayCap:       opts.Params.Int(params.DAY_CAP_S),
		gaps:         gapsOf(opts.Params),
		writeDayFile: store.WriteDay,
		kick:         make(chan struct{}, 1),
		stop:         make(chan struct{}),
		done:         make(chan struct{}),
		pending:      make(map[int64]bool),
		days:         make(map[string]bool),
		unwritten:    make(map[int64]store.Bucket),
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
	l.base = c.Batch
	l.win = window{from: c.From, carry: c.Carry, gaps: l.gaps}
	for _, date := range c.Days {
		l.days[date] = true
	}
	if c.Batch > 0 && c.FromBatch > 0 && c.FromBatch <= c.Batch {
		// A batch that failed when the loop took it stays out of the window
		// (follower). FromBatch holds window records, so it didn't fail.
		var f follower
		err := l.readBatches(c.FromBatch, func(b ledger.Batch) error {
			if b.Row.B > c.Batch {
				return errEnough
			}
			if !f.next(b) {
				return nil
			}
			for _, rec := range b.Records {
				ev, ok, err := eventOf(rec, c.Device)
				if err == nil && ok && ev.T >= l.win.from {
					l.win.recs = append(l.win.recs, held{ev: ev, b: b.Row.B})
				}
			}
			return nil
		}, func(first, _ uint64, err error) {
			l.log.Warn("a batch of the scorer's window doesn't read, and the window goes on without it", "batch", first, "err", err)
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
	l.opened = true
	l.report()
	return nil
}

// catchUp scores the batches in the ledger after the loop's place.
func (l *Loop) catchUp() {
	l.readLedger(0)
	l.save()
	l.batching = false
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

// take scores b, reading any batch between the last one taken and b from
// the ledger first.
func (l *Loop) take(b ledger.Batch) {
	if l.stuck != nil || b.Row.B <= l.cur.Batch {
		return
	}
	if b.Row.B == l.cur.Batch+1 && l.readMsg != "" {
		// The read that failed missed nothing, since b follows the last
		// batch taken.
		l.readMsg = ""
		l.publish()
	}
	if b.Row.B > l.cur.Batch+1 {
		// The batches between were sealed before the queue had them, or
		// the queue dropped them. Those the files don't hold fail in apply.
		if err := l.readLedger(b.Row.B - 1); err != nil || l.stuck != nil || b.Row.B <= l.cur.Batch {
			return
		}
	}
	l.apply(b)
}

// readLedger scores the sealed batches in the ledger's files after the
// last one taken, up to batch upTo, or every one when upTo is 0; with no
// place, from the first batch the files hold. It returns errStopping when
// the loop is stopping, the error that halted the loop when one did, and
// an error the files gave, which the loop tries again with its next batch.
func (l *Loop) readLedger(upTo uint64) error {
	if l.stuck != nil {
		return l.stuck
	}
	from := l.cur.Batch + 1
	if l.cur.Batch == 0 {
		// Retention deletes the oldest segments (DESIGN 4.4), so the
		// batches before the first the files hold are no gap.
		first, err := ledger.FirstHeld(l.opts.Ledger, 1)
		if err != nil {
			l.unread(err)
			return err
		}
		if first == 0 {
			return nil
		}
		from = first
	}
	err := l.readBatches(from, func(b ledger.Batch) error {
		switch {
		case upTo != 0 && b.Row.B > upTo:
			return errEnough
		case l.stopping():
			return errStopping
		}
		l.apply(b)
		return l.stuck
	}, func(first, resume uint64, err error) {
		if upTo != 0 && first > upTo {
			return // a batch this read doesn't need
		}
		l.log.Warn("a ledger batch doesn't read, and the scorer goes on after it", "batch", first, "err", err)
		l.damage = err
		if l.cur.Batch == 0 && first > l.failedTo {
			// With no place, apply counts no batch before the first it
			// takes, so the batches passed over are counted here.
			to := first
			if resume > first {
				to = resume - 1
			}
			l.failed += to - first + 1
			l.failedTo = to
		}
		l.gapMsg = fmt.Sprintf("ledger batch %d doesn't read, so the time in it isn't counted: %v", first, err)
		l.publish()
	})
	switch {
	case err == nil || errors.Is(err, errEnough):
		if l.readMsg != "" {
			l.readMsg = ""
			l.publish()
		}
		return nil
	case errors.Is(err, errStopping) || l.stuck != nil:
		return err
	}
	l.unread(err)
	return err
}

// unread reports that the ledger's files didn't read, which the next read
// that works clears.
func (l *Loop) unread(err error) {
	l.log.Warn("the scorer couldn't read the ledger", "err", err)
	l.readMsg = fmt.Sprintf("the scorer couldn't read the ledger, and tries again with its next batch: %v", err)
	l.publish()
}

// readBatches calls fn with each batch from from on that the ledger's
// files hold whole, in order, as ledger.ReadBatches does, until fn returns
// an error, which it returns. Where a batch doesn't read, or a run of them
// is in a segment that is gone, it tells damaged the first one, the batch
// it goes on from, 0 for none, and why, and goes on from there: the next
// batch the files hold.
func (l *Loop) readBatches(from uint64, fn func(ledger.Batch) error, damaged func(first, resume uint64, err error)) error {
	for {
		next := max(from, 1) // the batch ReadBatches hands fn next
		err := ledger.ReadBatches(l.opts.Ledger, from, func(b ledger.Batch) error {
			next = b.Row.B + 1
			return fn(b)
		})
		if !errors.Is(err, ledger.ErrDamaged) {
			return err
		}
		resume, herr := ledger.FirstHeld(l.opts.Ledger, next+1)
		if herr != nil {
			return herr
		}
		damaged(next, resume, err)
		if resume == 0 {
			return nil
		}
		from = resume
	}
}

// apply scores batch b, which comes after the last batch taken. When
// batches before b are missing from the ledger's files, they fail, and b is
// checked against its own first record (decision E-sm-1.1-seq-gaps). When
// b's numbers don't follow the last batch's, b fails: its records aren't
// scored, doctor hears it, and the loop goes on from b's own row, so later
// batches still count. The server's CC7 holds the minutes either kind of
// gap touches.
func (l *Loop) apply(b ledger.Batch) {
	if b.Row.B > l.cur.Batch+1 {
		if l.cur.Batch > 0 {
			why := "aren't in the ledger's files"
			if l.damage != nil {
				why = fmt.Sprintf("don't read (%v)", l.damage)
			}
			l.gap(fmt.Sprintf("ledger batches %d to %d %s, so the time in them isn't counted", l.cur.Batch+1, b.Row.B-1, why),
				b.Row.B-1-l.cur.Batch)
		}
		l.cur.Batch = b.Row.B - 1
		if b.Row.FirstSeq > 0 {
			l.cur.Seq = b.Row.FirstSeq - 1
		}
	}
	l.damage = nil
	if err := checkBatch(b, l.cur.Batch, l.cur.Seq); err != nil {
		l.gap(fmt.Sprintf("ledger batch %d isn't scored, so the time in it isn't counted: %v", b.Row.B, err), 1)
		l.skipped += uint64(len(b.Records))
		l.cur.Batch, l.cur.Seq = b.Row.B, b.Row.FirstSeq+b.Row.N-1
		if !l.batching {
			l.save()
		}
		l.report()
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

// follower follows batches as apply takes them, to rebuild the window
// from the ledger's files: the first batch starts it, a batch after ones
// the files don't hold starts it again from its own row, and a batch whose
// numbers don't follow the last one's fails and moves it on to its own row.
type follower struct{ b, s uint64 }

// next reports whether apply scored batch b, and moves on to it.
func (f *follower) next(b ledger.Batch) bool {
	if f.b == 0 || b.Row.B > f.b+1 {
		f.b = b.Row.B - 1
		if b.Row.FirstSeq > 0 {
			f.s = b.Row.FirstSeq - 1
		}
	}
	ok := checkBatch(b, f.b, f.s) == nil
	f.b, f.s = b.Row.B, b.Row.FirstSeq+b.Row.N-1
	return ok
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
	meta := bucketMeta{v: score.Version(), params: l.opts.Params.Hash(), device: l.cur.Device}
	var all score.Derived
	all.Append(out.Final)
	all.Append(out.Pending)
	view := minuteBuckets(meta, all, recs)
	// The minutes this Step derived: pkg/score gives every slot it derives
	// a credit slot, empty ones included.
	derived := make(map[int64]bool, len(all.Credits)/4+1)
	for _, c := range all.Credits {
		derived[c.Slot-floorMod(c.Slot, minuteMS)] = true
	}
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
	for _, m := range slices.Sorted(maps.Keys(visit)) {
		bk, ok := view[m]
		if !ok {
			if !l.pending[m] {
				continue
			}
			if !derived[m] {
				// A pending minute this Step didn't derive, such as one whose
				// records the window no longer holds, keeps its latest
				// revision as it is: an empty one would zero the credit and
				// agent time no Step has taken back. A later Step that
				// derives it writes it again. Once FinalBefore passes it, no
				// Step can make it final, and the loop stops looking at it,
				// as pkg/score's Revisions does with a minute it no longer
				// derives, so it stays pending (WP-HL1).
				if m+minuteMS <= fb {
					delete(l.pending, m)
				}
				continue
			}
			bk = emptyBucket(meta, m)
		}
		bk.State = event.StatePending
		if m+minuteMS <= fb {
			bk.State = event.StateFinal
		}
		bk.Batch = b
		// A write that fails keeps bk for the next save to write again.
		l.put(bk)
		if bk.State == event.StatePending {
			l.pending[m] = true
		} else {
			delete(l.pending, m)
		}
	}
	l.cur.FinalBefore = fb
	l.win.slide(holdFor(all.Turns, fb-l.lookback))
	if !l.batching {
		l.save()
	} else {
		// Scoring a long ledger again touches every day it covers. The
		// days whose minutes are all final can go from memory.
		l.forget()
	}
}

// holdFor returns where the window starts when it would start at from:
// from, or the earliest submit of a turn that ran past from, if no more than
// holdMaxMS before it. A State that holds such a turn's records gives its
// run, origin and class as one holding every record does.
func holdFor(turns []score.Turn, from int64) int64 {
	start := from
	for _, tn := range turns {
		if tn.RunUntil > from && tn.TSubmit < start && tn.TSubmit >= from-holdMaxMS {
			start = tn.TSubmit
		}
	}
	return start
}

// forget drops from the bucket store's memory the days that ended two days
// before FinalBefore: every minute of them is final, so the loop never
// writes to them again. A day whose flush fails stays, and the next save
// flushes it again.
func (l *Loop) forget() {
	if err := l.opts.Buckets.Forget(l.cur.FinalBefore - 2*dayMS); err != nil {
		l.fault(fmt.Sprintf("the scorer couldn't flush an old day's buckets, and tries again at its next save: %v", err))
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
// from a later batch, as it does when the loop scores batches again, and
// marks the minute's day for a new rollup when it writes. A write that
// fails keeps bk in l.unwritten for the next save. A stored revision from a
// batch after the saved place has its day marked and its file flushed at
// the next save, so a crash between a Step's appends and its save leaves no
// day behind its buckets.
func (l *Loop) put(bk store.Bucket) {
	m := bk.At()
	cur, ok, err := l.opts.Buckets.Latest(m)
	if err != nil {
		l.unwritten[m] = bk
		l.fault(fmt.Sprintf("the scorer couldn't read a minute's bucket, and writes it again at its next save: %v", err))
		return
	}
	if ok && cur.Device == bk.Device && cur.Batch > l.base {
		l.days[store.DateOf(m, l.zone())] = true
		if err := l.opts.Buckets.Unflushed(m); err != nil {
			l.log.Warn("couldn't mark a minute to flush", "minute", bk.Minute, "err", err)
		}
	}
	if ok && cur.Device == bk.Device && cur.Batch > bk.Batch {
		delete(l.unwritten, m)
		return
	}
	_, wrote, err := l.opts.Buckets.Put(bk)
	switch {
	case errors.Is(err, store.ErrFinal):
		// Scoring batches again meets the final revisions it wrote the
		// first time. A final one that comes out different would be a bug.
		if bk.State == event.StateFinal {
			l.log.Warn("a final minute scored again came out different, and keeps its first bucket", "minute", bk.Minute)
		}
		delete(l.unwritten, m)
	case err != nil:
		l.unwritten[m] = bk
		l.fault(fmt.Sprintf("the scorer couldn't store a minute's bucket, and writes it again at its next save: %v", err))
	default:
		delete(l.unwritten, m)
		if wrote {
			l.days[store.DateOf(m, l.zone())] = true
		}
	}
}

// save writes again the revisions whose writes failed, writes the
// out-of-date day rollups, flushes the bucket files and then saves the
// loop's place, so the place never runs ahead of the buckets on disk. It
// keeps the place where it was while a revision isn't stored or the flush
// fails, and keeps a day whose rollup it couldn't write for the next save.
// A save that stores everything clears the problem a failed write gave.
func (l *Loop) save() {
	if !l.opened {
		return
	}
	for _, m := range slices.Sorted(maps.Keys(l.unwritten)) {
		l.put(l.unwritten[m])
	}
	clean := len(l.unwritten) == 0
	loc := l.zone()
	for _, date := range slices.Sorted(maps.Keys(l.days)) {
		if err := l.writeDay(date, loc); err != nil {
			clean = false
			l.fault(fmt.Sprintf("the scorer couldn't write the rollup of %s, and writes it again at its next save: %v", date, err))
		} else {
			delete(l.days, date)
		}
		l.forget() // after a rebuild there may be months of them
	}
	if err := l.opts.Buckets.Sync(); err != nil {
		l.fault(fmt.Sprintf("the scorer couldn't flush its buckets, so it keeps its last place until a flush works: %v", err))
		return
	}
	if len(l.unwritten) > 0 {
		// Saving the place now would leave the failed revisions behind it.
		return
	}
	c := l.cur
	c.From, c.FromBatch, c.Carry = l.win.from, l.win.firstBatch(), l.win.carry
	if c.FromBatch == 0 {
		c.FromBatch = c.Batch + 1
	}
	c.Days = slices.Sorted(maps.Keys(l.days))
	if err := l.opts.State.Put(StateKey, c); err != nil {
		l.fault(fmt.Sprintf("the scorer couldn't keep its place: %v", err))
		return
	}
	if err := l.opts.State.Save(); err != nil {
		l.fault(fmt.Sprintf("the scorer couldn't save its place in state.json: %v", err))
		return
	}
	l.base = c.Batch
	l.forget()
	if clean && len(l.days) == 0 && l.faultMsg != "" {
		l.faultMsg = ""
		l.publish()
	}
}

// writeDay rewrites the rollup of the local day date, unless the day
// starts before the first UTC day whose minutes the bucket store keeps:
// retention dropped some of them (DESIGN 4.4), so the rollup written while
// all were there stays as it is, as DESIGN 4.4 keeps rollups until the
// teen deletes them.
func (l *Loop) writeDay(date string, loc *time.Location) error {
	start, end, err := store.DayBounds(date, loc)
	if err != nil {
		return err
	}
	if kept := l.opts.Buckets.KeptFrom(); kept != "" && store.DayOf(start.UnixMilli()) < kept {
		return nil
	}
	bs, err := l.opts.Buckets.Range(start.UnixMilli(), end.UnixMilli())
	if err != nil {
		return err
	}
	d, err := store.Rollup(date, loc, bs, l.dayCap)
	if err != nil {
		return err
	}
	return l.writeDayFile(l.opts.Days, d)
}

// halt stops scoring: the loop keeps what it stored, takes no more
// batches, and says why until the agent restarts.
func (l *Loop) halt(err error) {
	if l.stuck != nil {
		return
	}
	l.stuck = err
	l.log.Warn("the scorer stopped", "batch", l.cur.Batch, "err", err)
	l.stuckMsg = fmt.Sprintf("the scorer stopped after ledger batch %d, so time after that isn't counted: %v", l.cur.Batch, err)
	l.publish()
	l.report()
}

// gap reports n batches that failed, described by msg. Doctor hears of
// the last gap until the agent restarts.
func (l *Loop) gap(msg string, n uint64) {
	l.failed += n
	l.log.Warn("the scorer failed a ledger batch", "batch", l.cur.Batch, "problem", msg)
	l.gapMsg = msg
	l.publish()
}

// fault reports a failed write, which the next save that stores
// everything clears.
func (l *Loop) fault(msg string) {
	if msg != l.faultMsg {
		l.log.Warn("the scorer couldn't write", "problem", msg)
	}
	l.faultMsg = msg
	l.publish()
}

// publish tells doctor what is wrong with the loop, "" when nothing is.
func (l *Loop) publish() {
	var msgs []string
	for _, m := range []string{l.stuckMsg, l.gapMsg, l.readMsg, l.faultMsg} {
		if m != "" {
			msgs = append(msgs, m)
		}
	}
	if l.opts.Problem != nil {
		l.opts.Problem(strings.Join(msgs, "; "))
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
		Skipped: l.skipped, Failed: l.failed, LastBreak: l.lastBreak}
	if l.stuck != nil {
		s.Stopped = l.stuck.Error()
	}
	l.status.Store(s)
}
