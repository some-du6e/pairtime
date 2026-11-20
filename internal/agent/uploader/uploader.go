// SPDX-License-Identifier: AGPL-3.0-or-later

package uploader

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/some-du6e/pairtime/internal/agent/home"
	"github.com/some-du6e/pairtime/internal/agent/ledger"
	"github.com/some-du6e/pairtime/internal/agent/sched"
	"github.com/some-du6e/pairtime/internal/agent/state"
	"github.com/some-du6e/pairtime/internal/agent/store"
	"github.com/some-du6e/pairtime/internal/version"
	"github.com/some-du6e/pairtime/pkg/event"
	pkgledger "github.com/some-du6e/pairtime/pkg/ledger"
	"github.com/some-du6e/pairtime/pkg/params"
	"github.com/some-du6e/pairtime/pkg/score"
)

// Every is how often the uploader looks for anything new to send (DESIGN
// 3.3, 4.3).
const Every = 60 * time.Second

// SoonDelay is how long after a prompt or a stop the uploader sends: the
// ledger seals at once on both and the scorer steps right after, so their
// batch and its buckets are ready by then, and several hooks in a row go
// in one upload. DESIGN 3.3 asks for at most 10 s.
const SoonDelay = 5 * time.Second

// futureMargin is how far past the server's clock a bucket's minute may
// start before it waits: under the 300 s after which the server refuses it
// as future_minute (ingest-v1 2.7).
const futureMargin = 240 * time.Second

// errBodyUnreadable is the server's body_unreadable, a body that broke
// off on the way, which goes again as it is (ingest-v1 2.6).
var errBodyUnreadable = errors.New("uploader: the server couldn't read the whole body")

// OutboxName is the file in the data directory where a request waiting
// for its retry keeps its body.
const OutboxName = "outbox.json"

// Login is the device's sign-in, as `pairtime login` keeps it (WP-SV3).
// The uploader reads it through this interface until that work package
// merges, and asks before every upload, so signing in or out takes effect
// at the next one.
type Login interface {
	// Credentials returns the sign-in, and false while the device isn't
	// signed in or runs in local-only mode (DESIGN 4.2). Then the uploader
	// sends nothing at all, not even a DNS lookup.
	Credentials() (Credentials, bool)
}

// LoginFunc adapts a function to Login.
type LoginFunc func() (Credentials, bool)

// Credentials returns f().
func (f LoginFunc) Credentials() (Credentials, bool) { return f() }

// Credentials are what an upload needs from the sign-in.
type Credentials struct {
	// Server is the server's base URL, such as
	// https://pairtime.karimeltaib.com.
	Server string
	// Token is the device token, ptd_ and its characters.
	Token string
	// DeviceID is the device id the token belongs to: X-Pairtime-Device,
	// and the device every bucket names. It has to be the ledger chain's
	// device id, which every event and receipt names; the uploader sends
	// nothing while it isn't.
	DeviceID string
	// Sign signs with the device key the token registered.
	Sign Signer
}

// Ledger is the ledger writer as the uploader uses it, *ledger.Writer.
type Ledger interface {
	// DeviceID is the id of the device whose chain the writer writes.
	DeviceID() string
	// StoreReceipt keeps the server's receipt for batch b in batches.seg.
	StoreReceipt(b uint64, r pkgledger.Receipt) error
	// UploadFailure appends an upload-failure note (rtype 0x41).
	UploadFailure(t uint64, reason string) (uint64, error)
}

// Options configure New.
type Options struct {
	// Ledger is the open ledger writer, and LedgerDir its directory,
	// home.Dirs.Ledger(), whose batches.seg the uploader reads.
	Ledger    Ledger
	LedgerDir string
	// BucketDir is the scorer's bucket store, home.Dirs.Buckets(), which
	// the uploader reads and never writes.
	BucketDir string
	// DataDir is the data directory, where outbox.json goes.
	DataDir string
	// State is state.json, where the uploader keeps its place under
	// StateKey.
	State *state.Store
	// Login gives the sign-in. Nil means the device is never signed in.
	Login Login
	// Params is the table the scorer runs with: its version and hash go
	// in the labels, and WITNESS_LAG_S reads the witness labels.
	Params *params.Table
	// Device is what the scorer knows of the machine, for the labels'
	// typing_module and vm.
	Device score.Device
	// Started is when the agent started: the first request after it
	// carries the labels (ingest-v1 5 step 6). The zero time means now.
	Started time.Time
	// Sched, when set, wakes the uploader every Every. Without it the
	// uploader keeps a ticker of its own.
	Sched *sched.Scheduler
	// Flush, when set, waits until the scorer has scored every batch
	// sealed so far, so an upload carries their buckets.
	Flush func()
	// Client sends the requests. Nil means a client with the system's
	// proxy settings.
	Client *http.Client
	// Now is the clock, time.Now when nil.
	Now func() time.Time
	// Draw returns a random duration in [0, n) for the backoff's jitter,
	// rand.Int64N when nil.
	Draw func(n int64) int64
	// Version is the client build for the User-Agent and the labels,
	// version.Get().Version when "".
	Version string
	// Problem gets what doctor should hear about uploads, and "" once it
	// is solved.
	Problem func(msg string)
	// Log gets the uploader's log. Nil discards it.
	Log *slog.Logger
}

// Status is what the uploader reports about itself.
type Status struct {
	// SignedIn is set while the device has a sign-in to upload with.
	SignedIn bool `json:"signed_in"`
	// LastOK is when an upload last got its answer, and LastFailure the
	// reason the last one that didn't failed.
	LastOK      time.Time `json:"last_ok,omitzero"`
	LastFailure string    `json:"last_failure,omitempty"`
	// Next is the batch whose root goes next, and Fork the batch the
	// server's witness rows disagree at, 0 for none.
	Next uint64 `json:"next"`
	Fork uint64 `json:"fork,omitempty"`
	// Retrying is set while a request waits for its retry.
	Retrying bool `json:"retrying,omitempty"`
}

// Uploader sends the device's tier-1 data to pairtimed: the batch roots of
// its ledger, the scorer's minute-bucket revisions and the labels, signed
// over DESIGN 4.5's envelope in docs/spec/ingest-v1.md's format. It keeps
// each receipt in batches.seg, writes an upload-failure note for each
// upload that didn't reach the server, backs off with full jitter and
// Retry-After, and picks up from GET /api/v1/ingest/state after a
// restart. Make one with New, hand it every sealed batch with Sealed,
// start it with Start once the ledger is open, and stop it with Close.
type Uploader struct {
	opts      Options
	log       *slog.Logger
	now       func() time.Time
	client    *http.Client
	clock     *clock
	backoff   *backoff
	userAgent string
	build     string
	lag       int64 // WITNESS_LAG_S in ms
	started   time.Time

	ctx    context.Context
	cancel context.CancelFunc
	kick   chan struct{}
	stop   chan struct{}
	done   chan struct{}
	once   sync.Once
	soon   atomic.Bool // a SoonDelay wake is armed
	urgent atomic.Bool // the next cycle doesn't wait for the scorer
	job    *sched.Job

	mu     sync.Mutex
	sealed []sealedRow // batches Sealed took that the loop hasn't
	// lost is set when the queue was full and dropped batches, which the
	// loop then reads from batches.seg.
	lost    bool
	seen    map[event.Harness]string
	running bool
	status  Status

	// The rest belongs to the loop's goroutine.
	st        saved
	loaded    bool
	rows      *rowIndex
	synced    bool      // the state answer was read since the start or the last refusal
	lastReq   uint64    // the last counter used
	notBefore time.Time // no try before it: the backoff
	pending   *pendingReq
	blocked   string // the token the server refused, which isn't tried again
	problems  map[string]string
	retry     bool // a failure is waiting for its retry at notBefore
	resyncs   int  // state reads in a row after chain refusals that took no root

	// maxRoots and maxBuckets replace ingest-v1's limits when set, so
	// tests can split an upload into many requests.
	maxRoots, maxBuckets int
	// splitRoots and splitBuckets lower them after the server found a
	// request too large, until splitOK requests in a row have gone.
	splitRoots, splitBuckets, splitOK int
}

// sealedRow is what the uploader takes of a sealed batch: its row and when
// it was sealed, without its records.
type sealedRow struct {
	row      pkgledger.BatchRow
	sealedAt time.Time
}

// sealedQueueMax bounds the sealed batches waiting for the loop, as while the
// device isn't signed in. Past it the loop reads the rows from
// batches.seg instead.
const sealedQueueMax = 1024

// pendingReq is a request waiting for its retry: the same body goes again
// with the same Idempotency-Key (ingest-v1 2.10).
type pendingReq struct {
	plan plan
	key  string
}

// New returns an uploader that hasn't started.
func New(opts Options) (*Uploader, error) {
	switch {
	case opts.Ledger == nil:
		return nil, errors.New("uploader: New needs the ledger writer")
	case opts.State == nil:
		return nil, errors.New("uploader: New needs state.json")
	case opts.Params == nil:
		return nil, errors.New("uploader: New needs the parameter table")
	case !filepath.IsAbs(opts.LedgerDir) || !filepath.IsAbs(opts.BucketDir) || !filepath.IsAbs(opts.DataDir):
		return nil, errors.New("uploader: the ledger, bucket and data directories must be absolute paths")
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	log := opts.Log
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	client := opts.Client
	if client == nil {
		// No redirects: an upload goes to the server it was signed for.
		client = &http.Client{
			Transport:     http.DefaultTransport,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		}
	}
	build := opts.Version
	if build == "" {
		build = defaultBuild()
	}
	started := opts.Started
	if started.IsZero() {
		started = now()
	}
	ctx, cancel := context.WithCancel(context.Background())
	u := &Uploader{
		opts:      opts,
		log:       log,
		now:       now,
		client:    client,
		clock:     &clock{now: now},
		backoff:   newBackoff(opts.Draw),
		userAgent: "pairtime-agent/" + build,
		build:     build,
		lag:       opts.Params.Int(params.WITNESS_LAG_S) * 1000,
		started:   started,
		ctx:       ctx,
		cancel:    cancel,
		kick:      make(chan struct{}, 1),
		stop:      make(chan struct{}),
		done:      make(chan struct{}),
		problems:  map[string]string{},
	}
	if !buildForm(build) {
		u.build = "unknown"
	}
	return u, nil
}

// defaultBuild is the running binary's version, the client build the
// labels and the User-Agent name.
func defaultBuild() string { return version.Get().Version }

// Sealed takes a batch the ledger writer sealed. It never blocks, so it
// can be called from ledger.Options.Sealed. A batch that holds a prompt
// or a stop makes the uploader send within SoonDelay.
func (u *Uploader) Sealed(b ledger.Batch) {
	soon := false
	var learned map[event.Harness]string
	for _, rec := range b.Records {
		k, ok := rec.Header.RType().EventKind()
		if !ok {
			continue
		}
		switch k {
		case event.KindPromptSubmit, event.KindAgentStop:
			soon = true
		case event.KindOSPresence, event.KindOSFocus, event.KindOSPower:
			continue
		}
		if h, v, ok := harnessOf(rec.Body); ok {
			if learned == nil {
				learned = map[event.Harness]string{}
			}
			learned[h] = v
		}
	}
	u.mu.Lock()
	if len(u.sealed) < sealedQueueMax {
		u.sealed = append(u.sealed, sealedRow{row: b.Row, sealedAt: b.SealedAt})
	} else {
		u.sealed, u.lost = nil, true
	}
	for h, v := range learned {
		if u.seen == nil {
			u.seen = map[event.Harness]string{}
		}
		u.seen[h] = v
	}
	u.mu.Unlock()
	if soon {
		u.Soon()
	}
}

// harnessOf reads the harness and its version from a record's body, and
// false when the body names none the labels can carry.
func harnessOf(body []byte) (event.Harness, string, bool) {
	var b struct {
		E struct {
			Harness        event.Harness `json:"harness"`
			HarnessVersion string        `json:"harness_version"`
		} `json:"e"`
	}
	if json.Unmarshal(body, &b) != nil || !knownHarness(b.E.Harness) || !buildForm(b.E.HarnessVersion) {
		return "", "", false
	}
	return b.E.Harness, b.E.HarnessVersion, true
}

// Soon makes the uploader send within SoonDelay, as after a prompt or a
// stop hook. Calls while one is armed add nothing, so a burst of hooks
// goes in one upload.
func (u *Uploader) Soon() {
	if !u.soon.CompareAndSwap(false, true) {
		return
	}
	time.AfterFunc(SoonDelay, func() {
		u.soon.Store(false)
		u.Wake()
	})
}

// Urgent makes the uploader send at once without waiting for the scorer,
// as on lock and suspend, when the machine may sleep within seconds: the
// batch roots go first, and buckets the scorer hasn't written yet follow
// on the next wake (DESIGN 3.3).
func (u *Uploader) Urgent() {
	u.urgent.Store(true)
	u.Wake()
}

// Wake makes the uploader send at once, unless it is backing off after a
// failure.
func (u *Uploader) Wake() {
	select {
	case u.kick <- struct{}{}:
	default: // a wake is already due
	}
}

// Start starts the uploader on a goroutine of its own. Only the first
// call does anything, and none after Close.
func (u *Uploader) Start() {
	u.mu.Lock()
	defer u.mu.Unlock()
	select {
	case <-u.stop:
		return
	default:
	}
	if u.running {
		return
	}
	u.running = true
	if u.opts.Sched != nil {
		u.job = u.opts.Sched.Every("uploader", Every, func(time.Time) { u.Wake() })
	}
	go u.run()
	u.Wake()
}

// Close stops the uploader, cutting a request in flight short, which the
// next start sends again.
func (u *Uploader) Close() {
	u.once.Do(func() {
		u.mu.Lock()
		started := u.running
		u.mu.Unlock()
		if u.job != nil {
			u.job.Stop()
		}
		u.cancel()
		close(u.stop)
		if started {
			<-u.done
		}
	})
}

// Status returns what the uploader last reported.
func (u *Uploader) Status() Status {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.status
}

func (u *Uploader) run() {
	defer close(u.done)
	timer := time.NewTimer(time.Hour)
	timer.Stop()
	var tick <-chan time.Time
	if u.opts.Sched == nil {
		t := time.NewTicker(Every)
		defer t.Stop()
		tick = t.C
	}
	for {
		select {
		case <-u.stop:
			return
		case <-u.kick:
		case <-tick:
		case <-timer.C:
		}
		if wait := u.notBefore.Sub(u.now()); wait > 0 {
			timer.Reset(wait)
			continue
		}
		for u.cycle() {
			select {
			case <-u.stop:
				return
			default:
			}
		}
		if wait := u.notBefore.Sub(u.now()); wait > 0 || u.retry {
			// A failure tries again when its backoff ends, with no other
			// wake needed.
			u.retry = false
			timer.Reset(max(wait, time.Millisecond))
		}
	}
}

// cycle makes at most one request: the waiting retry, or the next plan
// when there is anything new. It returns true when there is more to send
// at once: a long upload goes request after request, and buckets that
// waited for the receipts a request brought go right after it.
func (u *Uploader) cycle() bool {
	var c Credentials
	ok := false
	if u.opts.Login != nil {
		c, ok = u.opts.Login.Credentials()
	}
	u.setStatus(func(s *Status) { s.SignedIn = ok })
	if !ok {
		// Nothing goes, and nothing is kept for later: a sign-in reads the
		// ledger again from batches.seg.
		u.mu.Lock()
		u.sealed, u.lost = nil, false
		u.mu.Unlock()
		u.loaded = false
		u.setProblem("login", "")
		return false
	}
	if c.Token == u.blocked {
		return false
	}
	if err := u.load(c); err != nil {
		u.setProblem("local", fmt.Sprintf("pairtime can't upload, because it can't read what to send: %v", err))
		return false
	}
	u.setProblem("local", "")
	if c.DeviceID != u.rows.device {
		u.setProblem("login", fmt.Sprintf("pairtime uploads nothing, because the server knows this computer as %s and its ledger is %s's; run pairtime login again", c.DeviceID, u.rows.device))
		return false
	}
	u.setProblem("login", "")
	if !u.synced && !u.sync(c) {
		return false
	}
	if !u.urgent.Swap(false) && !u.flush() {
		return false // stopping
	}
	u.takeSealed()
	p, key, err := u.next()
	if err != nil {
		u.setProblem("local", fmt.Sprintf("pairtime can't build its next upload: %v", err))
		return false
	}
	if p.body.Empty() {
		if !sameMarks(p.marks, u.st.Marks) {
			u.st.Marks = p.marks
			u.save()
		}
		return false
	}
	return u.send(c, p, key)
}

// flushWait bounds how long an upload waits for the scorer to score the
// batches sealed before it. A long rebuild of the scorer's place goes on
// meanwhile, and the batch roots shouldn't wait for it.
const flushWait = 10 * time.Second

// flush waits, up to flushWait, until the scorer has scored every batch
// sealed so far, so the upload carries their buckets. It returns false
// when the uploader is stopping.
func (u *Uploader) flush() bool {
	if u.opts.Flush == nil {
		return true
	}
	done := make(chan struct{})
	go func() {
		u.opts.Flush()
		close(done)
	}()
	t := time.NewTimer(flushWait)
	defer t.Stop()
	select {
	case <-done:
	case <-t.C:
	case <-u.stop:
		return false
	}
	return true
}

// load reads the uploader's place from state.json and the chain's rows
// from the ledger, the first time, and when the sign-in or the chain
// changed. Batches Sealed took meanwhile are added after.
func (u *Uploader) load(c Credentials) error {
	chain := u.opts.Ledger.DeviceID()
	if u.loaded && u.st.Chain == chain && u.st.Server == c.Server && u.st.Device == c.DeviceID {
		return nil
	}
	var st saved
	if _, err := u.opts.State.Get(StateKey, &st); err != nil {
		u.log.Warn("the uploader's place in state.json doesn't read, so it starts afresh", "err", err)
		st = saved{}
	}
	switch {
	case st.Chain != chain:
		st = saved{Chain: chain, Harnesses: st.Harnesses}
	case st.Server != c.Server || st.Device != c.DeviceID:
		// Another server or another sign-in holds none of this: it gets
		// the chain from its start, and every bucket.
		st = saved{Chain: chain, Harnesses: st.Harnesses, Offline: st.Offline}
	}
	st.Server, st.Device = c.Server, c.DeviceID
	rows, err := loadRows(u.opts.LedgerDir, max(st.Next, 1))
	if err != nil {
		return err
	}
	if rows.device != chain {
		return fmt.Errorf("the ledger's batches.seg is %s's chain, and the writer writes %s's", rows.device, chain)
	}
	u.st, u.rows, u.loaded, u.synced = st, rows, true, false
	u.lastReq = max(u.lastReq, st.Req)
	u.pending = u.readOutbox()
	u.setStatus(func(s *Status) { s.Next, s.Fork, s.Retrying = st.Next, st.Fork, u.pending != nil })
	return nil
}

// takeSealed adds the batches Sealed took to the rows, and the harnesses
// their records named to the labels.
func (u *Uploader) takeSealed() {
	u.mu.Lock()
	batches, lost := u.sealed, u.lost
	u.sealed, u.lost = nil, false
	seen := u.seen
	u.seen = nil
	u.mu.Unlock()
	if lost {
		if rows, err := loadRows(u.opts.LedgerDir, max(u.st.Next, 1)); err == nil {
			u.rows = rows
		}
	}
	for _, sb := range batches {
		switch {
		case sb.row.B <= u.rows.last():
			// Read from batches.seg already.
		case sb.row.B == u.rows.last()+1:
			r := ledger.Row{BatchRow: sb.row, Prev: u.rows.lastHead, SealedAt: sb.sealedAt}
			if err := u.rows.add(r); err != nil {
				u.log.Warn("a sealed batch doesn't follow the rows", "err", err)
			}
		default:
			// A batch went missing between the writer and here: read the
			// rows again.
			if rows, err := loadRows(u.opts.LedgerDir, max(u.st.Next, 1)); err == nil {
				u.rows = rows
			}
		}
	}
	changed := false
	for h, v := range seen {
		if u.st.Harnesses[h] != v {
			if u.st.Harnesses == nil {
				u.st.Harnesses = map[event.Harness]string{}
			}
			u.st.Harnesses[h] = v
			changed = true
		}
	}
	if changed {
		u.save()
	}
}

// next returns the request to send: the one waiting for its retry, or a
// new plan with a new Idempotency-Key.
func (u *Uploader) next() (plan, string, error) {
	if u.pending != nil {
		return u.pending.plan, u.pending.key, nil
	}
	p, err := makePlan(planInput{
		rows:       u.rows,
		st:         u.st,
		bucketDir:  u.opts.BucketDir,
		lag:        u.lag,
		labels:     u.labelsDue(),
		maxRoots:   lower(u.maxRoots, u.splitRoots),
		maxBuckets: lower(u.maxBuckets, u.splitBuckets),
		notAfter:   u.clock.serverNow().Add(futureMargin).UnixMilli(),
	})
	if err != nil {
		return plan{}, "", err
	}
	return p, event.NewEventID(u.now()), nil
}

// lower returns the lower of two limits, where 0 is no limit.
func lower(a, b int) int {
	switch {
	case a == 0:
		return b
	case b == 0:
		return a
	}
	return min(a, b)
}

// splitRun is how many requests in a row go before a split's lower limits
// are lifted again.
const splitRun = 20

// labels returns the labels the device sends now.
func (u *Uploader) labels() Labels {
	return currentLabels(u.build, u.opts.Params, u.opts.Device, u.st.Harnesses)
}

// currentLabels builds DESIGN 4.3's labels.
func currentLabels(build string, table *params.Table, dev score.Device, harnesses map[event.Harness]string) Labels {
	return Labels{
		ClientBuild:    build,
		ScoringVersion: table.Version(),
		Params:         table.Hash(),
		Harnesses:      sortedHarnesses(harnesses),
		TypingModule:   dev.TypingModule,
		VM:             dev.VM,
	}
}

// labelsDue returns the labels when they go in the next request: when
// they changed since the server last took them, in the first request
// since the agent started, and at least once a day (ingest-v1 5 step 6).
func (u *Uploader) labelsDue() *Labels {
	l := u.labels()
	if due, err := labelsDue(l, u.st, u.started, u.now()); err != nil || !due {
		return nil
	}
	return &l
}

// labelsDue is the rule of Uploader.labelsDue, for the labels l, the
// uploader's place st and the agent's start.
func labelsDue(l Labels, st saved, started, now time.Time) (bool, error) {
	h, err := labelsHash(l)
	if err != nil {
		return false, err
	}
	return h != st.Labels || st.LabelsAt < started.UnixMilli() || now.UnixMilli()-st.LabelsAt >= 24*time.Hour.Milliseconds(), nil
}

// sync reads GET /api/v1/ingest/state, which the uploader resumes from at
// each start and after a refusal that says its counter or its chain is
// behind (ingest-v1 5 steps 1 and 3): the counter goes on above the
// server's, and the next batch root is the first one after the server's
// head, or an earlier one the device holds no receipt for, which the
// server answers as a duplicate with its receipt.
func (u *Uploader) sync(c Credentials) bool {
	st, a, err := u.getState(u.ctx, c)
	switch {
	case err == nil && a.status == http.StatusOK:
	case err != nil && a.status == http.StatusOK:
		// The server answered, with something that doesn't read.
		u.setProblem("server", fmt.Sprintf("the server's state answer doesn't read: %v", err))
		u.notBefore = u.now().Add(u.backoff.fail(0))
		return false
	case err == nil && a.status != http.StatusTooManyRequests && a.status < 500:
		u.refusedState(c, a)
		return false
	default:
		u.failed(c, err, a, nil)
		return false
	}
	if st.Device != "" && st.Device != c.DeviceID {
		u.setProblem("server", fmt.Sprintf("the server answered for device %s, not %s", st.Device, c.DeviceID))
		u.notBefore = u.now().Add(time.Hour)
		return false
	}
	u.lastReq = max(u.lastReq, st.Req)
	u.st.ServerKey = st.ServerKey
	u.st.ChainStart = 0
	if st.ChainStart != nil {
		u.st.ChainStart = *st.ChainStart
	}
	if held := u.st.Next; held > 1 && (st.Head == nil || st.Head.B+1 < held) {
		// The server no longer holds batches it took, as after a restore
		// that lost its last writes, and it may have lost their buckets
		// too: every revision goes again, and it answers the ones it kept
		// as duplicates. The first receipts stay (receipt).
		u.st.Marks = nil
		u.log.Warn("the server's chain is behind what it took, so every minute goes again", "head", st.Head, "next", held)
	}
	next := uint64(1)
	if st.Head != nil {
		next = st.Head.B + 1
		if b := u.forkAt(st.Head); b != 0 {
			u.st.Fork = b
			u.setProblem("chain", forkProblem(b))
		}
		// A batch the server holds that the device has no receipt for
		// goes again, and its duplicate brings the receipt back. Batches
		// the server said it no longer holds (Gone) are skipped.
		for b := max(u.st.ChainStart, u.st.Gone+1, 1); b <= st.Head.B && b <= u.rows.last(); b++ {
			if _, ok := u.rows.receivedAt(b); !ok {
				next = b
				break
			}
		}
	}
	if next != u.st.Next && !u.rows.holds(next) {
		rows, err := loadRows(u.opts.LedgerDir, next)
		if err != nil {
			u.setProblem("local", fmt.Sprintf("pairtime can't read its ledger's batches: %v", err))
			return false
		}
		u.rows = rows
	}
	u.st.Next = next
	u.synced = true
	u.save()
	u.setStatus(func(s *Status) { s.Next = next })
	return true
}

// forkProblem is what doctor hears about a fork at batch b.
func forkProblem(b uint64) string {
	return fmt.Sprintf("the server's record of this computer's ledger differs from it at batch %d, so pairtime sends no more batch roots, and the minutes after it go up as backfilled; this is a bug, or the ledger was changed", b)
}

// forkAt returns the batch of the server's head when the device's own row
// of that batch has another H_b: there the server's chain and the
// device's part, and no root after it can link. It returns 0 when they
// agree, or the device doesn't hold that batch.
func (u *Uploader) forkAt(h *Head) uint64 {
	if h.B == 0 || h.B > u.rows.last() {
		return 0
	}
	row, ok := u.rows.row(h.B)
	if !ok {
		_, rows, err := ledger.ReadRows(u.opts.LedgerDir, h.B)
		if err != nil || len(rows) == 0 || rows[0].B != h.B {
			return 0
		}
		row = rows[0]
	}
	if row.Head != h.Head {
		return h.B
	}
	return 0
}

// reserve returns the next request counter, raising the reservation in
// state.json first when the counter would pass it.
func (u *Uploader) reserve() (uint64, error) {
	if u.lastReq+1 > u.st.Req {
		old := u.st.Req
		u.st.Req = u.lastReq + counterBlock
		if err := u.saveErr(); err != nil {
			u.st.Req = old // a reservation counts only once it is on disk
			return 0, err
		}
	}
	u.lastReq++
	return u.lastReq, nil
}

// send sends p, signed again for each try that a clock skew or a stale
// counter asks for, and handles its answer. It returns true when there is
// more to send at once.
func (u *Uploader) send(c Credentials, p plan, key string) bool {
	for try := 0; try < 3; try++ {
		req, err := u.reserve()
		if err != nil {
			u.setProblem("local", fmt.Sprintf("pairtime can't keep its request counter in state.json: %v", err))
			u.notBefore = u.now().Add(Every)
			return false
		}
		sg, err := sign(c.Sign, c.DeviceID, req, u.clock.serverNow().UnixMilli(), p.hash)
		if err != nil {
			u.setProblem("login", fmt.Sprintf("pairtime can't sign its uploads: %v", err))
			u.notBefore = u.now().Add(Every)
			return false
		}
		hr, err := u.newRequest(http.MethodPost, c, pathBatches, p.raw)
		if err != nil {
			u.setProblem("login", fmt.Sprintf("the server address %q doesn't work: %v", c.Server, err))
			u.notBefore = u.now().Add(Every)
			return false
		}
		setHeaders(hr.Header, c, key, p.hash, sg, u.userAgent)
		a, err := u.do(u.ctx, hr)
		if err != nil || a.status == http.StatusTooManyRequests || a.status >= 500 {
			u.failed(c, err, a, &pendingReq{plan: p, key: key})
			return false
		}
		switch {
		case a.status == http.StatusOK:
			return u.answered(c, p, a)
		case a.refusal.Error == refusalClockSkew:
			continue // the answer set the clock's offset
		case a.refusal.Error == refusalCounter:
			if !u.sync(c) {
				return false
			}
			continue
		case a.refusal.Error == refusalBodyHash:
			continue // the body changed on the way
		case a.refusal.Error == refusalBodyUnreadable:
			u.failed(c, errBodyUnreadable, a, &pendingReq{plan: p, key: key})
			return false
		}
		u.refused(c, p, a)
		return false
	}
	u.refused(c, p, answer{refusal: Refusal{Error: "retries"}})
	return false
}

// failed handles a try that didn't reach the server or got a 429 or a
// 5xx: it writes the upload-failure note, keeps the request for its retry
// when there is one, and backs off.
func (u *Uploader) failed(c Credentials, err error, a answer, retry *pendingReq) {
	if errors.Is(err, context.Canceled) {
		if retry != nil {
			u.keep(retry)
			u.save()
		}
		return // the agent is stopping
	}
	reason, ok := reasonOf(err, a.status)
	if !ok {
		return // only a failure to reach the server comes here
	}
	t := u.now().UnixMilli()
	if _, nerr := u.opts.Ledger.UploadFailure(uint64(t), string(reason)); nerr != nil {
		u.log.Warn("the ledger didn't take an upload-failure note", "err", nerr)
	}
	u.st.noteFailure(t, u.backoff.failing() > 0)
	if retry != nil {
		u.keep(retry)
	}
	u.save()
	wait := u.backoff.fail(retryAfter(a.header, u.now()))
	u.notBefore = u.now().Add(wait)
	u.retry = true
	msg := string(reason)
	if err != nil {
		msg += ": " + err.Error()
	}
	u.log.Info("an upload didn't reach the server", "reason", reason, "retry_in", wait, "err", err)
	u.setStatus(func(s *Status) { s.LastFailure, s.Retrying = msg, u.pending != nil })
}

// refused handles a request the server refused as a whole for a reason a
// retry of the same body doesn't fix (ingest-v1 2.6).
func (u *Uploader) refused(c Credentials, p plan, a answer) {
	code := a.refusal.Error
	if code == "" {
		code = fmt.Sprintf("status %d", a.status)
	}
	switch code {
	case refusalTokenMissing, refusalTokenInvalid, refusalRevoked, refusalMismatch:
		// Not this token again: signing in again gives a new one.
		u.blocked = c.Token
		u.setProblem("server", fmt.Sprintf("the server refused this computer's sign-in (%s), so pairtime uploads nothing until you run pairtime login again", code))
	case refusalKeyMissing:
		u.setProblem("server", "the server has no device key for this computer yet; pairtime login registers it")
		u.notBefore = u.now().Add(10 * time.Minute)
	case refusalKeyReused:
		// A new body needs a new key: the next try builds a new request.
		u.drop()
		u.notBefore = u.now().Add(SoonDelay)
	case refusalBodyTooLarge, refusalDecompressed, refusalTooManyBatches, refusalTooManyBuckets:
		// Split the upload (ingest-v1 2.6): the lists the request carried
		// go in halves, until requests have gone well for a while.
		if n := len(p.body.Batches); n > 1 {
			u.splitRoots = n / 2
		}
		if n := len(p.body.Buckets); n > 1 {
			u.splitBuckets = n / 2
		}
		u.splitOK = 0
		u.drop()
		u.notBefore = u.now().Add(SoonDelay)
	default:
		// The client's fault, which the same request won't fix: say so, and
		// try a new request in an hour rather than hammer the server.
		u.drop()
		u.setProblem("server", fmt.Sprintf("the server refused an upload as %s: %s", code, a.refusal.Detail))
		u.notBefore = u.now().Add(time.Hour)
	}
	u.log.Warn("the server refused an upload", "code", code, "detail", a.refusal.Detail)
}

// refusedState handles a refused GET /api/v1/ingest/state, which takes
// the token and the device id only.
func (u *Uploader) refusedState(c Credentials, a answer) {
	code := a.refusal.Error
	switch code {
	case refusalTokenMissing, refusalTokenInvalid, refusalRevoked, refusalMismatch:
		u.blocked = c.Token
		u.setProblem("server", fmt.Sprintf("the server refused this computer's sign-in (%s), so pairtime uploads nothing until you run pairtime login again", code))
	default:
		if code == "" {
			code = fmt.Sprintf("status %d", a.status)
		}
		u.setProblem("server", fmt.Sprintf("the server refused to say where this computer's uploads stand (%s)", code))
		u.notBefore = u.now().Add(10 * time.Minute)
	}
	u.log.Warn("the server refused the state request", "code", code, "detail", a.refusal.Detail)
}

// answered handles a 200: each item's result, every receipt, and the
// uploader's place. It returns true when there is more to send at once.
func (u *Uploader) answered(c Credentials, p plan, a answer) bool {
	var resp Response
	if err := json.Unmarshal(a.body, &resp); err != nil || len(resp.Batches) != len(p.body.Batches) ||
		len(resp.Buckets) != len(p.body.Buckets) || (p.body.Labels != nil) != (resp.Labels != nil) {
		// The server took it, but its answer doesn't fit what was sent:
		// read the state again before going on.
		u.drop()
		u.synced = false
		u.setProblem("server", "the server's answer to an upload didn't match what was sent")
		u.notBefore = u.now().Add(Every)
		return false
	}
	u.backoff.ok()
	more := false
	resync, took, refetched := false, false, false
	next := max(u.st.Next, 1)
	for i, res := range resp.Batches {
		root := p.body.Batches[i]
		passed := false
		switch res.Code {
		case codeAccepted, codeDuplicate:
			u.receipt(c, root, res.Receipt, &refetched)
			passed, took = true, true
		case codeBeforeChain:
			// The server holds no row for it any more: no receipt comes
			// for it, so no state read sends it again.
			u.st.Gone = max(u.st.Gone, root.B)
			passed, resync = true, true
		case codePrevMismatch, codeChainGap:
			resync = true
		default:
			// batch_conflict, or a root the server can't take as it is: no
			// root after it can link, so none goes until this is looked at.
			if u.st.Fork == 0 || root.B < u.st.Fork {
				u.st.Fork = root.B
			}
		}
		if passed && root.B == next {
			next = root.B + 1
		}
	}
	u.st.Next = next
	if took {
		more = true
	}
	if u.st.Fork != 0 {
		u.setProblem("chain", forkProblem(u.st.Fork))
	}
	refused := map[string]int{}
	marks, held := p.marks, false
	for i, res := range resp.Buckets {
		switch res.Code {
		case codeAccepted, codeDuplicate, codeSuperseded:
		case codeFutureMinute:
			// The server's clock hasn't reached the minute yet, so it goes
			// again later: its day keeps the mark it had (ingest-v1 2.9).
			// The lines of the day that went are sent again with it, and
			// come back duplicates.
			if !held {
				marks, held = maps.Clone(p.marks), true
			}
			day := store.DayOf(p.body.Buckets[i].Minute.UnixMilli())
			if m, ok := u.st.Marks[day]; ok {
				marks[day] = m
			} else {
				delete(marks, day)
			}
			refused[res.Code]++
		default:
			refused[res.Code]++
		}
	}
	if len(refused) > 0 {
		u.setProblem("buckets", fmt.Sprintf("the server refused some minutes: %v", refused))
		u.log.Warn("the server refused minute buckets", "codes", refused)
	} else {
		u.setProblem("buckets", "")
	}
	u.st.Marks = marks
	if off, ok := u.clock.Offset(); ok {
		u.st.Offset = off.Milliseconds()
	}
	if p.body.Labels != nil {
		u.st.Labels, u.st.LabelsAt = p.labels, u.now().UnixMilli()
		if resp.Labels.Code != codeAccepted {
			u.log.Warn("the server refused the labels", "code", resp.Labels.Code, "field", resp.Labels.Field)
		}
	}
	if len(p.body.Buckets) > 0 && refused[codeFutureMinute] == 0 {
		// More revisions may wait behind the ones that went. After a
		// future_minute they wait for the next wake instead, by when the
		// clock's offset holds them back.
		more = true
	}
	if u.splitRoots > 0 || u.splitBuckets > 0 {
		if u.splitOK++; u.splitOK >= splitRun {
			u.splitRoots, u.splitBuckets, u.splitOK = 0, 0, 0
		}
	}
	u.pending = nil
	u.removeOutbox()
	switch {
	case took:
		u.resyncs = 0
	case resync:
		u.resyncs++
	}
	if resync {
		u.synced = false
		more = true
		if u.resyncs >= 3 {
			// Reading the state didn't help three times: wait, rather than
			// ask the server again and again.
			u.setProblem("chain", "the server keeps refusing this computer's batch roots where it says its chain stands")
			u.notBefore = u.now().Add(10 * time.Minute)
			more = false
		}
	}
	u.rows.trim(u.st.Next)
	u.save()
	u.setProblem("server", "")
	u.setStatus(func(s *Status) {
		s.LastOK, s.LastFailure, s.Next, s.Fork, s.Retrying = u.now(), "", u.st.Next, u.st.Fork, false
	})
	return more
}

// receipt checks an accepted root's receipt and keeps it in batches.seg.
// A batch that has one keeps its first: it binds the server to when it
// first had the batch (ledger-v1 7), and a later one, as after a server
// restore, would date it later. When the receipt names a key the uploader
// doesn't have, it reads the state again, once per answer (refetched). A
// receipt it can't check isn't kept, but its received_at reads the
// batch's minutes until the agent stops, so they don't wait for a receipt
// that can't come.
func (u *Uploader) receipt(c Credentials, root Root, wr *WireReceipt, refetched *bool) {
	if _, ok := u.rows.receivedAt(root.B); ok && !u.rows.unchecked(root.B) {
		return
	}
	r, err := checkReceipt(u.st.ServerKey, u.rows.device, root.B, root.Head, wr)
	if errors.Is(err, errNoKey) && !*refetched {
		*refetched = true
		if st, a, gerr := u.getState(u.ctx, c); gerr == nil && a.status == http.StatusOK && st.ServerKey != nil {
			u.st.ServerKey = st.ServerKey
			r, err = checkReceipt(u.st.ServerKey, u.rows.device, root.B, root.Head, wr)
		}
	}
	if err != nil {
		if wr != nil {
			u.rows.receivedUnchecked(root.B, int64(wr.ReceivedAt))
		}
		u.setProblem("receipts", fmt.Sprintf("the server's receipt for batch %d doesn't check out, so it isn't kept: %v", root.B, err))
		u.log.Warn("a receipt doesn't check out", "batch", root.B, "err", err)
		return
	}
	if err := u.opts.Ledger.StoreReceipt(root.B, r); err != nil {
		u.setProblem("receipts", fmt.Sprintf("the ledger didn't keep the receipt for batch %d: %v", root.B, err))
		return
	}
	u.rows.received(root.B, int64(r.ReceivedAt))
	u.setProblem("receipts", "")
}

// keep makes p the request the next try sends, and writes its body to
// outbox.json, so a restart sends it again with the same key.
func (u *Uploader) keep(p *pendingReq) {
	if u.pending != nil && u.pending.key == p.key {
		return
	}
	u.pending = p
	sum := sha256.Sum256(p.plan.js)
	if err := home.WriteFile(filepath.Join(u.opts.DataDir, OutboxName), p.plan.js); err != nil {
		u.log.Warn("couldn't write the outbox", "err", err)
		u.st.Outbox = nil
		return
	}
	u.st.Outbox = &outbox{Key: p.key, Hash: hex.EncodeToString(sum[:]), Marks: p.plan.marks, Labels: p.plan.labels}
}

// drop forgets the request waiting for its retry.
func (u *Uploader) drop() {
	u.pending = nil
	u.removeOutbox()
	u.save()
}

// removeOutbox deletes outbox.json.
func (u *Uploader) removeOutbox() {
	u.st.Outbox = nil
	if err := os.Remove(filepath.Join(u.opts.DataDir, OutboxName)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		u.log.Warn("couldn't remove the outbox", "err", err)
	}
}

// readOutbox returns the request waiting in outbox.json, or nil when
// there is none or it doesn't match what state.json says of it.
func (u *Uploader) readOutbox() *pendingReq {
	p, err := readOutbox(u.opts.DataDir, u.st)
	if err != nil {
		u.log.Warn("the outbox doesn't read, so the next upload is a new request", "err", err)
		u.st.Outbox = nil
		return nil
	}
	return p
}

// readOutbox reads the request waiting in dir's outbox.json, as st says
// of it, and nil when none is waiting.
func readOutbox(dir string, st saved) (*pendingReq, error) {
	if st.Outbox == nil {
		return nil, nil
	}
	js, err := os.ReadFile(filepath.Join(dir, OutboxName))
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(js)
	if hex.EncodeToString(sum[:]) != st.Outbox.Hash {
		return nil, errors.New("outbox.json isn't the body state.json names")
	}
	var body Body
	dec := json.NewDecoder(bytes.NewReader(js))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		return nil, err
	}
	raw, err := compress(js)
	if err != nil {
		return nil, err
	}
	p := plan{body: body, js: js, raw: raw, hash: bodyHash(raw), marks: st.Outbox.Marks, labels: st.Outbox.Labels}
	return &pendingReq{plan: p, key: st.Outbox.Key}, nil
}

// save writes the uploader's place to state.json.
func (u *Uploader) save() {
	if err := u.saveErr(); err != nil {
		u.log.Warn("couldn't save the uploader's place in state.json", "err", err)
	}
}

func (u *Uploader) saveErr() error {
	if err := u.opts.State.Put(StateKey, u.st.clone()); err != nil {
		return err
	}
	return u.opts.State.Save()
}

// setProblem records a problem from one source, or clears it with "", and
// passes the sum to Options.Problem when it changes.
func (u *Uploader) setProblem(source, msg string) {
	if u.problems[source] == msg {
		return
	}
	if msg == "" {
		delete(u.problems, source)
	} else {
		u.problems[source] = msg
	}
	if u.opts.Problem == nil {
		return
	}
	var all []string
	for _, s := range []string{"login", "server", "chain", "receipts", "buckets", "local"} {
		if m := u.problems[s]; m != "" {
			all = append(all, m)
		}
	}
	u.opts.Problem(strings.Join(all, "; "))
}

func (u *Uploader) setStatus(fn func(*Status)) {
	u.mu.Lock()
	fn(&u.status)
	u.mu.Unlock()
}
