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
	"slices"
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

// errNotPairtimed is an answer without pairtimed's X-Pairtime-Time or its
// JSON refusal, such as a proxy's or Cloudflare's block page, which counts
// as a network error (ingest-v1 5.5).
var errNotPairtimed = errors.New("uploader: the answer isn't pairtimed's")

// errLocal is a request that couldn't be made on the device, as when the
// device key won't sign: no upload-failure note, which says the server
// was out of reach.
var errLocal = errors.New("uploader: the request couldn't be made on this computer")

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
	// LinkedAt is when `pairtime login` linked this computer, account.json's
	// linked_at. Batches sealed before it were sealed in local-only mode:
	// none of them goes, nor the buckets of their minutes (ingest-v1 5.3,
	// 7.6; DESIGN 4.2). Zero when unknown, when only batches sealed after
	// the uploader first sees the sign-in go.
	LinkedAt time.Time
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
	// LocalOnly reports whether config.toml's mode is local-only right
	// now (DESIGN 4.2), as the uploads obey it, so that a batch counts as
	// local-only exactly when nothing could have uploaded it, with known
	// false while that can't be told. Sealed asks it of every batch: none
	// sealed in local-only mode goes, neither its root nor its minutes
	// (D-local-gap). Nil means the mode is never local-only.
	LocalOnly func() (local, known bool)
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
	// Stopped is the first batch sealed in local-only mode after the
	// chain's last link, which no batch root passes until the next one, 0
	// for none (saved.localStop).
	Stopped uint64 `json:"stopped,omitempty"`
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
	pace      *pacer
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
	running bool
	status  Status
	// modeSeen is the mode Sealed last saw, and modes the changes it saw
	// that applyModes hasn't recorded yet. closed is set once Close has
	// stopped the loop, after which Sealed records them itself.
	modeSeen int8
	modes    []modeChange
	closed   bool

	// keepMu keeps applyModes to one caller at a time once the loop has
	// stopped.
	keepMu sync.Mutex

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
	// pruned is when uploads.jsonl last let go of its old entries.
	pruned time.Time
}

// sealedRow is what the uploader takes of a sealed batch: its row, when it
// was sealed, and the harnesses its records named, each with the version
// it last named, for the labels, without its records.
type sealedRow struct {
	row       pkgledger.BatchRow
	sealedAt  time.Time
	harnesses map[event.Harness]string
}

// sealedQueueMax bounds the sealed batches waiting for the loop, as while the
// device isn't signed in. Past it the loop reads the rows from
// batches.seg instead.
const sealedQueueMax = 1024

// The modes Sealed sees: modeUnknown before it has seen one, or after the
// uploader's place changed under it, so that the next batch records its
// mode again.
const (
	modeUnknown int8 = iota
	modeOnline
	modeLocal
)

// modeChange is a batch whose mode differs from the batch's before it.
type modeChange struct {
	b     uint64
	local bool
}

// modeQueueMax bounds the mode changes waiting for the loop.
const modeQueueMax = 64

// localOnlyProblem is what doctor hears while the batch roots stop at a
// batch sealed in local-only mode (saved.localStop).
const localOnlyProblem = `pairtime has stopped uploading, because config.toml's mode was "local" for a while after this computer was linked, and nothing it records in local-only mode may leave it; to upload again, run pairtime logout, then pairtime login`

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
		pace:      newPacer(paceBurst, paceEvery),
		userAgent: "pairtime-agent/" + clientBuild(build),
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
	u.build = clientBuild(build)
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
			if v != "" || learned[h] == "" {
				learned[h] = v
			}
		}
	}
	local, known := false, false
	if u.opts.LocalOnly != nil {
		local, known = u.opts.LocalOnly()
	}
	u.mu.Lock()
	if len(u.sealed) < sealedQueueMax {
		u.sealed = append(u.sealed, sealedRow{row: b.Row, sealedAt: b.SealedAt, harnesses: learned})
	} else {
		u.sealed, u.lost = nil, true
	}
	changed := false
	if m := modeOf(local); known && m != u.modeSeen {
		u.modeSeen, changed = m, true
		u.modes = append(u.modes, modeChange{b: b.Row.B, local: local})
		if len(u.modes) > modeQueueMax {
			// The loop hasn't taken them for a long while. Every batch
			// from the first change on counts as local-only, so none that
			// was goes, and the next batch records its mode again.
			u.modes = []modeChange{{b: u.modes[0].b, local: true}}
			u.modeSeen = modeUnknown
		}
	}
	closed := u.closed
	u.mu.Unlock()
	if changed {
		if closed {
			u.applyModes()
		} else {
			u.Wake()
		}
	}
	if soon {
		u.Soon()
	}
}

// modeOf is the mode Sealed records for a batch sealed in local-only
// mode (local) or not.
func modeOf(local bool) int8 {
	if local {
		return modeLocal
	}
	return modeOnline
}

// applyModes records the mode changes Sealed saw in the uploader's place
// (saved.mode): in u.st while it is loaded, and else in state.json, where
// the next sign-in reads them. The loop calls it before anything else
// after a wake, so a batch's mode is recorded before its root can be
// planned, and Close and Sealed call it once the loop has stopped.
func (u *Uploader) applyModes() {
	u.keepMu.Lock()
	defer u.keepMu.Unlock()
	u.mu.Lock()
	changes := u.modes
	u.modes = nil
	u.mu.Unlock()
	if len(changes) == 0 {
		return
	}
	record := func(st *saved) bool {
		changed := false
		for _, c := range changes {
			changed = st.mode(c.b, c.local) || changed
		}
		return changed
	}
	if u.loaded {
		if record(&u.st) {
			u.save()
		}
		return
	}
	var st saved
	if _, err := u.opts.State.Get(StateKey, &st); err != nil {
		st = saved{}
	}
	if chain := u.opts.Ledger.DeviceID(); st.Chain != chain {
		st = saved{Chain: chain, Harnesses: st.Harnesses}
	}
	if !record(&st) {
		return
	}
	if err := u.opts.State.Put(StateKey, st); err != nil {
		u.log.Warn("couldn't keep which batches were sealed in local-only mode", "err", err)
		return
	}
	if err := u.opts.State.Save(); err != nil {
		u.log.Warn("couldn't save which batches were sealed in local-only mode", "err", err)
	}
}

// harnessOf reads the harness and its version from a record's body, with
// "" for a version that isn't one (versionForm), and false when the body
// names no harness the labels can carry.
func harnessOf(body []byte) (event.Harness, string, bool) {
	var b struct {
		E struct {
			Harness        event.Harness `json:"harness"`
			HarnessVersion string        `json:"harness_version"`
		} `json:"e"`
	}
	if json.Unmarshal(body, &b) != nil || !knownHarness(b.E.Harness) {
		return "", "", false
	}
	v := b.E.HarnessVersion
	if !versionForm(v) {
		v = ""
	}
	return b.E.Harness, v, true
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

// Urgent makes the uploader send at once without waiting for the scorer
// or its pace, as on lock and suspend, when the machine may sleep within
// seconds: the batch roots go first, and buckets the scorer hasn't written
// yet follow on the next wake (DESIGN 3.3).
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
		u.mu.Lock()
		u.closed = true
		u.mu.Unlock()
		u.applyModes()
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
		u.applyModes()
		for {
			if wait := u.holdOff(); wait > 0 {
				// The cycle runs once the wait ends, with no other wake
				// needed.
				timer.Reset(wait)
				break
			}
			if !u.cycle() {
				break
			}
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

// holdOff returns how long the next cycle waits: the backoff after a
// failure, and the pace that keeps the requests under ingest's rate, which
// the upload lock and suspend ask for at once doesn't wait for.
func (u *Uploader) holdOff() time.Duration {
	wait := u.notBefore.Sub(u.now())
	if !u.urgent.Load() {
		wait = max(wait, u.pace.wait(time.Now()))
	}
	return wait
}

// cycle makes at most one request: the waiting retry, or the next plan
// when there is anything new. It returns true when there is more to send
// at once, as when an upload is larger than one request holds, which then
// goes request after request at the pacer's pace. Buckets that waited for
// the receipts a request brought go with the next wake's roots, so a
// steady session sends one request a wake.
func (u *Uploader) cycle() bool {
	u.applyModes()
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
		// Nothing tries while signed out, so no outage span runs on.
		u.backoff.ok()
		u.setProblem("login", "")
		u.setProblem("local-only", "")
		return false
	}
	if c.Token == u.blocked {
		u.backoff.ok()
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
	// A run of batches sealed in local-only mode that closed since the
	// state read, while the link's first batch isn't stored, moves From
	// past it rather than stop the roots.
	u.st.settle(u.rows.last())
	stop := u.st.localStop(u.rows.last())
	if stop > 0 {
		u.setProblem("local-only", localOnlyProblem)
	} else {
		u.setProblem("local-only", "")
	}
	u.setStatus(func(s *Status) { s.Stopped = stop })
	p, key, err := u.next()
	if err != nil {
		u.setProblem("local", fmt.Sprintf("pairtime can't build its next upload: %v", err))
		return false
	}
	if len(p.unread) > 0 {
		u.setProblem("local", fmt.Sprintf("pairtime can't read its minutes of %s, so they wait while the rest go", strings.Join(p.unread, ", ")))
	} else {
		u.setProblem("local", "")
	}
	if p.body.Empty() {
		// Lines that only passed over, and revisions withheld, move the
		// marks and what the minutes' credit rests on all the same.
		if !sameMarks(p.marks, u.st.Marks) || !maps.Equal(p.sent, u.st.Sent) {
			u.st.Marks, u.st.Sent = p.marks, p.sent
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
// changed, a re-link included: `pairtime logout` and `pairtime login`
// within one wake leave no signed-out wake between them, and only the new
// linked_at shows it. Batches Sealed took meanwhile are added after.
func (u *Uploader) load(c Credentials) error {
	chain := u.opts.Ledger.DeviceID()
	if u.loaded && u.st.Chain == chain && u.st.Server == c.Server && u.st.Device == c.DeviceID && !relinked(u.st, c) {
		return nil
	}
	var st saved
	if _, err := u.opts.State.Get(StateKey, &st); err != nil {
		u.log.Warn("the uploader's place in state.json doesn't read, so it starts afresh", "err", err)
		st = saved{}
	}
	// A sign-in to another server: batches.seg's receipts are the old
	// one's. A state.json that names no server, as a lost one, is no switch.
	foreign := st.Chain == chain && st.Server != "" && (st.Server != c.Server || st.Device != c.DeviceID)
	switch {
	case st.Chain != chain:
		st = saved{Chain: chain, Harnesses: st.Harnesses}
	case st.Server != c.Server || st.Device != c.DeviceID:
		// Another server or another sign-in holds none of this: it gets
		// the chain from the first batch sealed online after the sign-in,
		// and the buckets of its minutes. The old server's outages say
		// nothing about this one. The runs of batches sealed in local-only
		// mode describe the ledger, not a server, so they stay, as those
		// recorded while signed out do for a first sign-in (D-local-gap).
		st = saved{Chain: chain, Harnesses: st.Harnesses, LocalFrom: st.LocalFrom, Gaps: st.Gaps}
	}
	st.Server, st.Device = c.Server, c.DeviceID
	rows, err := loadRows(u.opts.LedgerDir, max(st.Next, 1))
	if err != nil {
		return err
	}
	if foreign {
		// batches.seg's receipts are the old server's, until this one's come.
		rows.forgetReceipts()
		st.Foreign = rows.last()
	}
	st.prepare(rows)
	if rows.device != chain {
		return fmt.Errorf("the ledger's batches.seg is %s's chain, and the writer writes %s's", rows.device, chain)
	}
	fresh := relinked(st, c)
	relink(&st, rows, c)
	u.st, u.rows, u.loaded, u.synced = st, rows, true, false
	// The place may have changed under what Sealed last saw, as after a
	// sign-in to another server: the next batch records its mode again.
	u.mu.Lock()
	u.modeSeen = modeUnknown
	u.mu.Unlock()
	u.lastReq = max(u.lastReq, st.Req)
	u.pending = u.readOutbox()
	if fresh && u.pending != nil {
		// A request from before the link may carry roots the gap now
		// holds, which the server never takes: its items are planned
		// again from where they stood.
		u.pending = nil
		u.removeOutbox()
		u.save()
	}
	u.setStatus(func(s *Status) { s.Next, s.Fork, s.Retrying = u.st.Next, u.st.Fork, u.pending != nil })
	return nil
}

// relinked reports whether c is a link st hasn't seen: a first sign-in, or
// a re-link with a new linked_at (relink).
func relinked(st saved, c Credentials) bool {
	return !c.LinkedAt.IsZero() && c.LinkedAt.UnixMilli() != st.LinkedAt
}

// relink sets where the device's chain goes on from for the sign-in c. A
// new sign-in or a re-link, a new token after pairtime logout, which
// account.json's new linked_at shows, starts at the first batch sealed
// after it that the recorded modes don't mark local-only, and stays
// Linking until the server holds a batch from there: the batches before
// it never go, nor their minutes, and the server takes the gap after a
// re-link (D-local-gap). The runs of batches sealed in local-only mode
// stay as they were recorded, whenever the link was. The same sign-in
// keeps its place: a batch skipped without a re-link is still chain_gap,
// so nothing is skipped then.
func relink(st *saved, rows *rowIndex, c Credentials) {
	if la := c.LinkedAt.UnixMilli(); !c.LinkedAt.IsZero() && la != st.LinkedAt {
		st.From, st.LinkedAt, st.Linking = firstLinked(rows, c.LinkedAt), la, true
	} else if st.From == 0 {
		st.From, st.Linking = firstLinked(rows, c.LinkedAt), true
	}
	st.settle(rows.last())
}

// firstLinked returns the first batch sealed after the sign-in at
// linkedAt, or the next one to be sealed when linkedAt is unknown or
// after every batch: the batches before it are local-only mode's. It
// compares seal times with linkedAt at full precision, and takes a
// linkedAt of whole seconds, as account.json's is, for the end of its
// second, so a batch sealed in the second the login finished counts as
// sealed before it.
func firstLinked(rows *rowIndex, linkedAt time.Time) uint64 {
	if !linkedAt.IsZero() {
		if linkedAt.Nanosecond() == 0 {
			linkedAt = linkedAt.Add(time.Second)
		}
		if b, ok := rows.sealedAfter(linkedAt.UnixMilli()); ok {
			return b
		}
	}
	return rows.last() + 1
}

// takeSealed adds the batches Sealed took to the rows, and the harnesses
// their records named to the labels. It takes the rows and the modes
// Sealed recorded with them under one hold, and records the modes first,
// so a batch's mode is in the place before its root or its minutes can be
// planned, however late in a cycle it was sealed. The harnesses of a batch
// sealed in local-only mode stay out of the labels, as everything it holds
// stays on the device (DESIGN 4.2).
func (u *Uploader) takeSealed() {
	u.mu.Lock()
	batches, lost, changes := u.sealed, u.lost, u.modes
	u.sealed, u.lost, u.modes = nil, false, nil
	u.mu.Unlock()
	changed := false
	for _, c := range changes {
		changed = u.st.mode(c.b, c.local) || changed
	}
	if lost {
		if rows, err := loadRows(u.opts.LedgerDir, max(u.st.Next, 1)); err == nil {
			u.st.prepare(rows)
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
				u.st.prepare(rows)
				u.rows = rows
			}
		}
	}
	for _, sb := range batches {
		if u.st.localOnly(sb.row.B) {
			continue
		}
		for h, v := range sb.harnesses {
			old, known := u.st.Harnesses[h]
			if v == "" && known {
				continue // a record that names no version keeps the one known
			}
			if !known || old != v {
				if u.st.Harnesses == nil {
					u.st.Harnesses = map[event.Harness]string{}
				}
				u.st.Harnesses[h] = v
				changed = true
			}
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
	if err == nil && a.refusal.Error == refusalClockSkew {
		// The refusal carried the server's time, which set the offset:
		// sign again with it.
		st, a, err = u.getState(u.ctx, c)
	}
	if err == nil && a.status != http.StatusTooManyRequests && a.status < 500 && !fromPairtimed(a) {
		err = errNotPairtimed
	}
	switch {
	case errors.Is(err, errLocal):
		u.setProblem("login", fmt.Sprintf("pairtime can't ask the server where this computer's uploads stand: %v", err))
		u.notBefore = u.now().Add(Every)
		return false
	case err == nil && a.status == http.StatusOK:
	case err != nil && a.status == http.StatusOK && !errors.Is(err, errNotPairtimed):
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
		// that lost its last writes, and it may have lost the buckets that
		// went with them: the days from an hour before its head's seal go
		// again, and it answers the revisions it kept as duplicates. The
		// first receipts stay (receipt).
		cutoff := int64(0)
		if st.Head != nil && st.Head.B >= 1 && st.Head.B <= u.rows.last() {
			cutoff = u.rows.times[st.Head.B-1].sealedMax - time.Hour.Milliseconds()
		}
		from := store.DayOf(cutoff)
		maps.DeleteFunc(u.st.Marks, func(day string, _ store.Mark) bool { return day >= from })
		u.st.Sent = nil
		// A request waiting for its retry was built from the marks before
		// the rewind, and its answer would put them back.
		if u.pending != nil {
			u.pending = nil
			u.removeOutbox()
		}
		u.log.Warn("the server's chain is behind what it took, so the minutes after its head go again", "head", st.Head, "next", held, "from", from)
	}
	// The server's own gaps go in too: a lost state.json forgets the
	// gaps of earlier re-links, whose batches would otherwise wait for
	// receipts that never come, and hold up the minutes after them.
	for _, g := range st.Gaps {
		u.st.addGap(g.First, g.Last)
	}
	if st.Head != nil && st.Head.B >= u.st.From {
		// The server holds a batch from where this link's chain goes on,
		// as after a request whose answer was lost: no later batch skips.
		u.st.Linking = false
	}
	// Until then From is worked out again at each state read, past the
	// batches sealed in local-only mode since the link.
	u.st.settle(u.rows.last())
	// While the server holds no batch of the device, its chain starts at
	// the first batch sealed after the sign-in (ingest-v1 5.3), and after a
	// re-link it goes on from there (D-local-gap): the batches between are
	// local-only mode's.
	base := uint64(1)
	if st.Head != nil {
		base = st.Head.B + 1
	}
	if u.st.From > base {
		u.st.addGap(base, u.st.From-1)
	}
	next := max(u.st.From, 1)
	if st.Head != nil {
		next = max(st.Head.B+1, u.st.From)
		switch b := u.forkAt(st.Head); {
		case b != 0:
			// The lower fork stays: no root goes after either.
			if u.st.Fork == 0 || b < u.st.Fork {
				u.st.Fork = b
			}
			u.setProblem("chain", forkProblem(u.st.Fork))
		case u.st.Fork != 0 && u.st.Fork <= st.Head.B && u.rows.holdsRow(st.Head.B):
			// The server's head is the device's own, past the batch the
			// fork was at, and every head chains the batches before it:
			// there was no fork there after all.
			u.st.Fork = 0
			u.setProblem("chain", "")
		}
		// A batch the server holds that the device has no receipt for
		// goes again, and its duplicate brings the receipt back. Batches
		// the server said it no longer holds (Gone) are skipped.
		for b := max(u.st.ChainStart, u.st.Gone+1, u.st.From, 1); b <= st.Head.B && b <= u.rows.last(); b++ {
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
		u.st.prepare(rows)
		u.rows = rows
	}
	u.st.Next = next
	u.recheck()
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
// of that batch has another H_b, and the device's next batch when it has
// none that high: there the server's chain and the device's part, and no
// root after it can link. It returns 0 when they agree.
func (u *Uploader) forkAt(h *Head) uint64 {
	if h.B == 0 {
		return 0
	}
	if h.B > u.rows.last() {
		// The server holds batches the device doesn't, as after a home
		// folder restored from an old backup: the next one it seals takes
		// a number the server already holds (ingest-v1 5.3).
		return u.rows.last() + 1
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
		// The sign-in may have gone while the upload waited for the scorer,
		// or between tries: nothing goes then (device-auth-v1 8).
		if now, ok := u.opts.Login.Credentials(); !ok || now.Token != c.Token || now.Server != c.Server || now.DeviceID != c.DeviceID {
			return false
		}
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
		if err == nil && a.status != http.StatusTooManyRequests && a.status < 500 && !fromPairtimed(a) {
			err = errNotPairtimed
		}
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
	if a.status == http.StatusTooManyRequests {
		// The server's bucket ran dry, as after a burst the pace didn't
		// see, such as a restart's: the requests after this go at the pace
		// alone, so no stream of 429s and notes follows.
		u.pace.drain(time.Now())
	}
	open := u.backoff.failing() > 0
	wait := u.backoff.fail(retryAfter(a.header, u.now()))
	u.st.noteFailure(t, t+wait.Milliseconds(), open)
	if retry != nil {
		u.keep(retry)
	}
	u.save()
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
	if a.status != 0 {
		e := pastUpload(u.now(), c.Server, p, a)
		e.Refused = code
		u.recordUpload(e)
	}
	switch code {
	case refusalMismatch:
		// The device id belongs to another account: this computer signs in
		// again as a new device, with a new device id and K_dev
		// (D-device-account-binding).
		u.blocked = c.Token
		u.setProblem("server", "the server says this computer's device id belongs to another account, so pairtime uploads nothing until it signs in again as a new device")
	case refusalTokenMissing, refusalTokenInvalid, refusalRevoked, refusalKeyMissing:
		// Not this token again: signing in again gives a new one, and binds
		// the device key with it (ingest-v1 2.6; D-key-at-grant).
		u.blocked = c.Token
		u.setProblem("server", fmt.Sprintf("the server refused this computer's sign-in (%s), so pairtime uploads nothing until you run pairtime login again", code))
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

// fromPairtimed reports whether an answer is pairtimed's: it carries the
// server's time and, unless it is a 200, pairtimed's JSON refusal.
func fromPairtimed(a answer) bool {
	if _, ok := serverTime(a.header); !ok {
		return false
	}
	return a.status == http.StatusOK || a.refusal.Error != ""
}

// refusedState handles a refused GET /api/v1/ingest/state, which takes
// the token and the device id only.
func (u *Uploader) refusedState(c Credentials, a answer) {
	code := a.refusal.Error
	switch code {
	case refusalTokenMissing, refusalTokenInvalid, refusalRevoked, refusalMismatch, refusalKeyMissing, refusalSignature:
		// None of them passes with the same sign-in: signing in again binds
		// a new token and device key (ingest-v1 2.6).
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
	e := pastUpload(u.now(), c.Server, p, a)
	if err := json.Unmarshal(a.body, &resp); err != nil || len(resp.Batches) != len(p.body.Batches) ||
		len(resp.Buckets) != len(p.body.Buckets) || (p.body.Labels != nil) != (resp.Labels != nil) {
		// The server took it, but its answer doesn't fit what was sent:
		// read the state again before going on.
		u.recordUpload(e)
		u.drop()
		u.synced = false
		u.setProblem("server", "the server's answer to an upload didn't match what was sent")
		u.notBefore = u.now().Add(Every)
		return false
	}
	e.Results = resultsOf(resp)
	u.recordUpload(e)
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
			if root.B >= u.st.From {
				u.st.Linking = false // the link's first batch is stored
			}
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
	if u.st.Foreign > 0 && u.st.Next > u.st.Foreign {
		u.st.Foreign = 0 // this server's receipts are in for every batch
	}
	if u.st.Fork != 0 {
		u.setProblem("chain", forkProblem(u.st.Fork))
	}
	refused := map[string]int{}
	marks := p.marks
	heldDays := map[string]bool{}
	// newest is the highest rev the request carried for each minute.
	newest := map[int64]uint32{}
	for _, b := range p.body.Buckets {
		ms := b.Minute.UnixMilli()
		newest[ms] = max(newest[ms], b.Rev)
	}
	for i, res := range resp.Buckets {
		b := p.body.Buckets[i]
		ms := b.Minute.UnixMilli()
		switch res.Code {
		case codeAccepted, codeDuplicate:
		case codeSuperseded, codeRevConflict:
			// The server holds a revision of the minute above the newest one
			// sent, or another bucket at its rev, as after the store lost its
			// last lines: the minute's revs go up past the server's, and its
			// day is read again (ingest-v1 5.5).
			if res.Rev == nil || (res.Code == codeSuperseded && *res.Rev <= newest[ms]) {
				break // a newer revision of ours, which the server already has
			}
			if u.st.Bump == nil {
				u.st.Bump = map[int64]uint32{}
			}
			u.st.Bump[ms] = max(u.st.Bump[ms], *res.Rev+1-(b.Rev-p.bump(ms)))
			heldDays[store.DayOf(ms)] = true
			if res.Code == codeRevConflict {
				refused[res.Code]++
			}
		case codeFutureMinute:
			// The server's clock hasn't reached the minute yet, so it goes
			// again later: its day keeps the mark it had (ingest-v1 2.9).
			// The lines of the day that went are sent again with it, and
			// come back duplicates.
			heldDays[store.DayOf(ms)] = true
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
	sent := p.sent
	if len(heldDays) > 0 {
		// A held day's lines go again from its old mark, read against what
		// was sent before them.
		marks, sent = maps.Clone(p.marks), maps.Clone(p.sent)
		for day := range heldDays {
			if m, ok := u.st.Marks[day]; ok {
				marks[day] = m
			} else {
				delete(marks, day)
			}
		}
		maps.DeleteFunc(sent, func(ms int64, _ sentRev) bool { return heldDays[store.DayOf(ms)] })
		for ms, v := range u.st.Sent {
			if heldDays[store.DayOf(ms)] {
				sent[ms] = v
			}
		}
	}
	u.st.Marks, u.st.Sent = marks, sent
	if off, ok := u.clock.Offset(); ok {
		u.st.Offset = off.Milliseconds()
	}
	if p.body.Labels != nil {
		u.st.Labels, u.st.LabelsAt = p.labels, u.now().UnixMilli()
		if resp.Labels.Code != codeAccepted {
			u.log.Warn("the server refused the labels", "code", resp.Labels.Code, "field", resp.Labels.Field)
		}
	}
	if p.more && refused[codeFutureMinute] == 0 {
		// The request held as much as one does, and more waits behind it,
		// which goes at once, at the pacer's pace. What waited for the
		// receipts this answer brought goes with the next wake's roots
		// instead. After a future_minute the rest waits for the next wake
		// too, by when the clock's offset holds it back.
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
	if got, ok := u.rows.receivedAt(root.B); ok && !u.rows.unchecked(root.B) {
		if wr != nil && int64(wr.ReceivedAt) != got {
			// A later receipt for the same batch: the server re-dated it. The
			// first one stays (ingest-v1 5.4).
			u.setProblem("receipts", fmt.Sprintf("the server re-dated batch %d, which it first took at %d and now says it took at %d; pairtime keeps the first receipt", root.B, got, wr.ReceivedAt))
		}
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
			if errors.Is(err, errNoKey) {
				// Kept, unchecked, until its key checks it.
				if u.st.Unchecked == nil {
					u.st.Unchecked = map[uint64]WireReceipt{}
				}
				u.st.Unchecked[root.B] = *wr
				if len(u.st.Unchecked) > uncheckedMax {
					delete(u.st.Unchecked, slices.Min(slices.Collect(maps.Keys(u.st.Unchecked))))
				}
			}
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
	delete(u.st.Unchecked, root.B)
	u.setProblem("receipts", "")
}

// recheck checks the receipts kept unchecked under the state's server key,
// and keeps in batches.seg the ones it checks (ingest-v1 5.4).
func (u *Uploader) recheck() {
	for b, wr := range u.st.Unchecked {
		row, ok := u.rows.row(b)
		if !ok {
			_, rows, err := ledger.ReadRows(u.opts.LedgerDir, b)
			if err != nil || len(rows) == 0 || rows[0].B != b {
				continue
			}
			row = rows[0]
		}
		r, err := checkReceipt(u.st.ServerKey, u.rows.device, b, row.Head, &wr)
		if err != nil {
			continue
		}
		if err := u.opts.Ledger.StoreReceipt(b, r); err == nil {
			u.rows.received(b, int64(r.ReceivedAt))
			delete(u.st.Unchecked, b)
		}
	}
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
	u.st.Outbox = &outbox{Key: p.key, Hash: hex.EncodeToString(sum[:]), Marks: p.plan.marks, Sent: p.plan.sent, Labels: p.plan.labels}
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
	p := plan{body: body, js: js, raw: raw, hash: bodyHash(raw), marks: st.Outbox.Marks, sent: st.Outbox.Sent, labels: st.Outbox.Labels}
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
	for _, s := range []string{"login", "local-only", "server", "chain", "receipts", "buckets", "local"} {
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
