// SPDX-License-Identifier: AGPL-3.0-or-later

package ledger

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/some-du6e/pairtime/internal/agent/sched"
	osclock "github.com/some-du6e/pairtime/internal/platform/clock"
	"github.com/some-du6e/pairtime/pkg/event"
	"github.com/some-du6e/pairtime/pkg/ledger"
	"github.com/some-du6e/pairtime/pkg/ledger/features"
	"github.com/some-du6e/pairtime/pkg/params"
	"github.com/some-du6e/pairtime/pkg/score"
)

var (
	// ErrClosed is a call on a Writer after Close.
	ErrClosed = errors.New("ledger: the writer is closed")
	// ErrDamaged is a ledger directory whose files disagree in a way no
	// crash or power cut leaves them: a frame that fails its CRC in a
	// part of a file that was flushed, a record out of sequence, a seal
	// that doesn't match its records. Open refuses to write to such a
	// ledger, because the only ways to go on would drop sealed records or
	// number records again, which repeats nonces (ledger-v1 3).
	ErrDamaged = errors.New("ledger: the ledger's files are damaged")
	// ErrKeyReuse is a device key that already has a chain the writer
	// can't go on with here: the chain Options.Chains records for the key
	// isn't the one this ledger directory holds, as when the directory was
	// deleted or emptied, or the directory kept an old chain of the key in
	// old/. A new chain under that key would start again from s = 1 and
	// repeat every nonce and token salt of the old one (ledger-v1 3), so
	// going on takes a new device key.
	ErrKeyReuse = errors.New("ledger: this device key already has a chain, and a second one would repeat its nonces")
	// ErrLateLocal is a record Write leaves off the ledger: an event made in
	// a local-only stretch the writer knows (stretch), which reached it only
	// after a batch was sealed online since the stretch ended. Nothing
	// recorded in local-only mode may leave the device, and a batch sealed
	// local-only after an online one would stop the batch roots until the
	// next sign-in (ingest-v1 5.3), so the record is dropped, counted
	// (Status.LateLocal) and never uploaded (D-late-local;
	// docs/errata/2026-11-23-late-local-records.md). It takes no sequence
	// number, and its caller counts it as handled, not as a failure.
	ErrLateLocal = errors.New("ledger: the record was made while config.toml said local-only and reached the ledger after a batch was sealed online since, so it stays off the ledger")
)

// ChainRecord keeps, next to a device key and out of the ledger directory,
// the device id of the one chain the key writes, so deleting the directory
// doesn't delete the record. *keys.Keystore is one.
type ChainRecord interface {
	// Chain returns the device id of the key's chain, or "" when the key
	// has started none.
	Chain() string
	// RecordChain records deviceID as the key's chain.
	RecordChain(deviceID string) error
}

// Vault keeps, on the device, the content of the records the writer
// writes, by their numbers: the text each commits, which the ledger holds
// only a commitment to, and the paths of an agent's edits, for recaps
// (DESIGN 3.5; WP-LG6). *vault.Vault is one. Open it before the writer:
// vault.Open flushes what a killed run kept, before the writer's Open
// flushes the batches it restores.
type Vault interface {
	// Reconcile drops every entry from s = next on. Open calls it once,
	// after it has cut what a crash left and before any record takes
	// next: the records that held those numbers are gone, and an entry
	// left for one would pass for the content of the next record to take
	// its number. A vault whose Reconcile fails keeps nothing more.
	Reconcile(next uint64) error
	// Keep keeps the content of record s, which Write has just appended
	// to the segment of the UTC day day: text, the bytes its c commits,
	// nil when it has no c, and paths, the paths of its edits. Write calls
	// it under the lock records take, in the order of their numbers, so it
	// must be quick, must never call the Writer, and reports its own
	// problems.
	Keep(day string, s uint64, text []byte, paths []string)
	// Sync flushes what Keep wrote. Each seal calls it before it flushes
	// the batch, so the content of a sealed record is on disk once its
	// batch is.
	Sync()
}

// sealReads bounds how often a seal reads config.toml without the record
// lock for the records written while it read it (Writer.seal). Notes
// always make it read again. After that many, it reads the file once more
// under the lock.
const sealReads = 3

// pendingMax is how many records the writer holds unsealed before it seals
// at once rather than wait for BATCH_S: a long backfill or an inbox drain
// gives batches of at most about this many records.
const pendingMax = 4096

// Options configure Open.
type Options struct {
	// Dir is the ledger directory, home.Dirs.Ledger(). Open makes it.
	Dir string
	// Keys are the ledger keys of this installation's K_dev, from the
	// device keystore (internal/agent/keys).
	Keys *ledger.Keys
	// KeyID is K_dev's public id, keys.Status.KeyID: 16 to 64 lowercase
	// hex digits. It names the chain the keys write. A ledger directory
	// whose chain another key wrote gets its files moved to old/<key id>,
	// and a new chain starts at s = 1 under a new device id: a restore, or
	// anything else that gives the installation a new K_dev, starts a new
	// device (ledger-v1 3; decision E-sm-1.1-seq-gaps, decided; spec
	// wording pending).
	KeyID string
	// Chains, when set, records next to K_dev which chain the key writes:
	// the agent passes its keystore. Open refuses with ErrKeyReuse to start
	// a chain for a key that has one on record, or to write to a chain of
	// the key that isn't that one, and it records the chain it writes
	// before the chain's first record and before it writes to a chain it
	// finds unrecorded. So no record exists under a key whose chain isn't
	// on record, and a ledger directory that is deleted or emptied can't
	// make the key number records from s = 1 again. Without it, Open can
	// tell a used key only from the directory's own old/.
	Chains ChainRecord
	// Params is the table BATCH_S, SUBSTANTIVE_CHARS and
	// ERROR_PASTE_TEXT_MAX come from, which should be the one the scorer
	// runs with. Nil means the newest table pkg/params embeds.
	Params *params.Table
	// Sched, when set, runs a seal every BATCH_S while there are new
	// records. Without it only Seal, prompts, stops, a full batch and
	// Close seal.
	Sched *sched.Scheduler
	// Sealed, when set, gets every batch this Writer seals once it is on
	// disk, in batch order, on the goroutine that sealed it, so it must
	// return quickly. The scorer takes its records from here, and only
	// from sealed batches, so nothing it scores or uploads rests on a
	// record a crash could still cut (decision E-sm-1.1-seq-gaps,
	// decided; spec wording pending).
	// Batches sealed before Open are on disk for ReadBatches.
	Sealed func(Batch)
	// Vault, when set, keeps the content of the records Write writes
	// (Vault): Open reconciles it before any record takes a number a crash
	// freed, Write hands it each record's content, and each seal flushes it
	// before the batch.
	Vault Vault
	// Online, when set, reports whether config.toml's mode lets the
	// computer upload right now (DESIGN 4.2). Each seal asks it as it
	// begins, before it takes the lock records take, so a slow read holds
	// up no record. A seal through whose reads records keep coming asks it
	// once more under that lock (sealReads), and every Write waits for
	// that one read, so it must be quick, must never call the Writer, and
	// must take no lock that is held around a call of the Writer. The seal frame
	// records the batch's mode: online only when Online says so, no record
	// in the batch came while the mode NoteMode last noted was local-only,
	// and none is an event made in a local-only stretch the writer knows
	// that had ended, by its own time, or for lookback's by when the writer
	// took it in (stretch, Writer.Write, Writer.lookback). Each read that
	// finds the file online, or local-only, is a moment the writer knew it
	// said so, where a stretch ends or may begin. So a batch's mode is on
	// disk with the seal, in the same flush, before Sealed hears of it,
	// and no crash can lose it. Every seal frame the writer writes records
	// the mode, online or local-only, and why a local-only batch is
	// (Batch.Why); a batch whose seal frame records none, as an older
	// pairtime version's doesn't, counts as sealed in local-only mode
	// (Batch.Online, Row.NoMode). Nil means every batch is sealed online,
	// but for NoteMode and the events made in a local-only stretch it noted.
	Online func() bool
	// Waiting, when set, reports whether an event that before says may
	// have been made in a local-only stretch is still on its way to the
	// writer: a hook call parked behind its folder's first Track, or held
	// while the adapters' keys or the writer weren't there, a git change the
	// watcher holds, or a record presence queued. before takes the event's
	// times, its t and its mono in its boot, and says yes for one made in a
	// stretch that ended and no batch sealed online has passed since, by the
	// clocks Write holds it to (stretch.holds), so it counts exactly what
	// Write would seal local-only rather than drop. A hook call counts by its
	// records' times and by the hook's own stamp, as Write takes them
	// (Input.Sent). While the first batch after a switch back online is due
	// its mark (Batch.Back), and while the newest batch counts as local-only,
	// each seal asks it as it begins, outside the lock records take, and it
	// must never call the Writer; a seal whose own read of config.toml ends a
	// stretch asks it with the stretch ended at that read. While it says yes,
	// the batch is sealed local-only and the mark waits for the next one: so
	// an event made in local-only mode that reaches the writer late is sealed
	// in the run of local-only batches before the first batch the server can
	// take, and not after it, where its batch would stop the batch roots
	// until the next sign-in (ingest-v1 5.3). Nil means nothing waits.
	Waiting func(before func(event.Envelope) bool) bool
	// Clock reads the OS's monotonic clock, which hook calls carry, at the
	// moments config.toml is seen local-only (moment): osclock.Now when nil.
	Clock func() osclock.Stamp
	// Seen, when set, gives what the agent saw of config.toml's mode before
	// the writer opened. Open takes it as NoteSeen does, before it seals
	// what a crash left, so the records the ledger kept through a stretch
	// the agent saw are judged by it too. It must never call the Writer.
	Seen func() Seen
	// Problem gets a description of what went wrong when a seal that no
	// caller waits for fails, and "" when one succeeds after that.
	Problem func(msg string)
	// Now is the clock, time.Now when nil. Segments are named after its
	// UTC day.
	Now func() time.Time
}

// Batch is a sealed batch as the writer stores it: its row, when it was
// sealed, whether it was sealed online, and its records.
type Batch struct {
	Row      ledger.BatchRow
	SealedAt time.Time
	// Online is set when the batch was sealed while config.toml's mode let
	// the computer upload, as its seal frame records (Options.Online).
	// Nothing a batch without it holds may leave the device, neither its
	// root nor its minutes (DESIGN 4.2; D-local-gap).
	Online bool
	// Back marks the first batch sealed online whose records all came
	// after the agent's config watch noted config.toml back online, with
	// no seal since that read the file as local-only (NoteMode): the run of
	// batches sealed in local-only mode before it ends before it, so it can
	// go, where an unmarked batch sealed online just after the run counts
	// as part of it. A record came after the note when the writer got it
	// after the note and, for an event, no local-only stretch the writer
	// knows holds its own time, but for lookback's, which count by when
	// the writer took them in alone (Writer.lookback). A git change the
	// watcher held past the note, or a hook call parked behind its folder's
	// first Track, made in the stretch, may have come in local-only mode,
	// so its batch is sealed local-only, and the mark waits for the next
	// batch whose records all came after the note, sealed while no event of
	// the stretch is still on its way (Options.Waiting).
	Back bool
	// LocalUntil is when config.toml was last known, or taken, to say
	// local-only as the batch was sealed (sealPayload.LocalUntil), the
	// zero time for never: the newest local-only stretch the writer knew
	// ended then, and the part of a minute before it counts as in it.
	LocalUntil time.Time
	// Why says why a batch sealed local-only is (LocalWhy), as its seal
	// frame records it, and is nil for one sealed online.
	Why     *LocalWhy
	Records []ledger.Record
}

// Input is an event as the agent received it, and what its record commits
// about its content (ledger-v1 10.1 and 10.6). Text, Edits and Command
// stay on the device: the record holds only the commitment and the keyed
// features made from them.
type Input struct {
	Event event.Event
	// Text is the text the record commits: for prompt.submit, the prompt
	// as the harness delivered it, pasted blocks expanded; for an
	// agent.output with is_final set, the turn's final message, which the
	// record cuts to its first 16 KiB; for cli.track, the label after
	// pkg/redact (DESIGN 5.4). It is what the vault keeps. It is nil, not
	// empty, for a bridge prompt and for a plugin's prompt or final
	// message that came without its text, whose records commit nothing
	// (ledger-v1 10.1). A prompt marked guest commits nothing either:
	// Write drops whatever text comes with it. It must be valid UTF-8.
	Text []byte
	// Edits are an agent.tool's edit hunks, in the order the patch lists
	// them.
	Edits []features.Edit
	// Command is the command line an agent.tool or shell.command ran.
	Command string
	// Late says the record tells of a time long past, as lookback's do: a
	// prompt or a stop that is Late asks for no seal of its own, and waits
	// for BATCH_S's, since no one waits for it (WP-AD3).
	Late bool
	// Sent is when the event's sender says it made the event, by the
	// wall clock and the OS's monotonic clock, where that may be earlier
	// than the event's own times: a hook call's own stamp, which
	// hookpath.Times sets aside for the agent's when the hook's is later
	// than the agent took the call by the wall clock, as after a step back
	// of it, or has no monotonic reading. Write holds the event to the
	// local-only stretches by these times too, so a sender's stamp can make
	// a batch local-only, or the record dropped, and never online. It is
	// never recorded. Zero for none.
	Sent osclock.Stamp
}

// lookback reports whether e is lookback's: an event that came through a
// log reader (event.ViaLog) and is dated before the chain began (Created).
// The log readers start only once the ledger has opened, so such an event
// describes time before pairtime ran on this computer, whatever read it:
// the lookback reader (WP-AD3), or a tailer that read an old line again.
// By its own time, the chain's first local-only stretch, which every
// install starts in, would hold it, so Write holds it to the stretches by
// when the writer took it in instead, and leaves Sent unread. Its batch is
// then local-only when config.toml said local-only at any time from then
// to its seal, as Online and the notes tell the writer, which they also
// say until the computer is linked, and online otherwise. A record a crash
// left that is lookback's counts by the earliest time it can have reached
// the writer (Open). An event that carries a reading of a run's clock is
// none, though a step back of the wall clock dates it before the chain
// began: it was made while an agent ran, this one or one a kill ended,
// whose record recovery takes in, and the stretches hold it to that
// reading for this run's and to its t for another's (moment.before).
func (w *Writer) lookback(e event.Envelope) bool {
	return e.Via == event.ViaLog && e.T < w.created && !(e.Mono > 0 && strings.HasPrefix(e.BootID, runPrefix))
}

// Writer is a device's ledger writer. It appends each record to the day's
// segment, seals the records since the last batch into the next batch
// every BATCH_S and at once on lock, suspend, prompt and stop, and keeps
// the batch rows in batches.seg (DESIGN 3.5, 3.6). Its methods are safe
// for concurrent use.
//
// A write is one write call and no flush, so it never waits for the disk.
// A seal writes the batch's seal frame into the segment that holds its
// records, flushes that one file, and only then writes the row to
// batches.seg: one fsync per seal, which suits a spinning disk. Writes go
// on while a seal waits for its fsync. The row needs no flush of its own,
// because Open rebuilds a lost row from the seal frame. Records a crash
// cuts were never in a sealed batch, and the next record takes the number
// after the last one that survived (decision E-sm-1.1-seq-gaps, decided;
// spec wording pending).
type Writer struct {
	dir      string
	keys     *ledger.Keys
	feat     *features.Key
	keyID    string
	chains   ChainRecord
	table    *params.Table
	batchS   time.Duration
	now      func() time.Time
	sealedFn func(Batch)
	vault    Vault
	online   func() bool
	waiting  func(func(event.Envelope) bool) bool
	osNow    func() osclock.Stamp
	// boot is the OS's boot the writer runs in, as osNow read it at Open,
	// "" when the OS gave none.
	boot    string
	problem func(string)
	device  string
	// created is when the chain began, in Unix ms (chainPayload.Created).
	created int64
	// opened is the newest batch an earlier run sealed, as Open found the
	// ledger before it sealed what a crash left (Opened).
	opened uint64

	// sealMu orders seals and every write to batches.seg. It is taken
	// before mu.
	sealMu   sync.Mutex
	batches  *appendFile // batches.seg
	troubled bool        // the last seal no caller waited for failed

	mu      sync.Mutex
	seg     *appendFile // the segment records go to, nil before the first
	segDay  string
	retired []*appendFile // segments rolled over that a seal may still flush
	next    uint64        // s of the next record
	b       uint64        // the last sealed batch, 0 before the first
	base    uint64        // the first batch batches.seg keeps (Sweep), 1 before a sweep drops any
	head    ledger.Hash   // its H_b, zero(32) before the first
	sealed  int64         // its sealed_at, in Unix ms, 0 before the first
	pending []pending     // the records since the last seal
	q       score.Questions
	snap    position // where the newest seal frame that holds q starts
	failed  error
	closed  bool
	// local is set while a local-only stretch goes on, as the mode
	// NoteMode noted last says, or since a seal read config.toml as
	// local-only with nothing pending, and taint once a record in pending
	// came then. seen is set while the newest of the notes and the seals'
	// reads says local-only, which is while a stretch goes on in
	// stretches. stretchSealed is set once a batch holding a record of the
	// current local-only stretch is sealed. lastLocal is set while the
	// newest batch, sealed by this writer or found by Open, counts as
	// sealed in local-only mode. back is the s of the first record written
	// after NoteMode noted a switch back online, until the batch it marks
	// is sealed (Batch.Back) or a seal reads config.toml as local-only, and
	// 0 while there is none. until is the moment config.toml was last
	// known, or taken, to say local-only: when a stretch ended, by a note or
	// a seal's read, or when the agent saw one end (NoteSeen), as the newest
	// seal frame recorded it across a restart. late is set once a record in
	// pending is an event made in a stretch that ended, by its own time, or
	// lookback's by when the writer took it in (Write), as found at the
	// write and again wherever a stretch ends or the writer learns of one
	// (lateLocked). unnoted is set once a seal's read ended a stretch that
	// no note had ended, until a batch is sealed online: the watch's note of
	// that switch back, which can come after the read, as after Open's own
	// seal, still arms the mark (noteLocked). notes counts NoteMode's calls.
	local, taint, seen, stretchSealed, lastLocal, late, unnoted bool
	back, notes                                                 uint64
	until                                                       moment
	// backFrom is when Open made the mark due again from a seal frame or
	// the base frame (restoreStretches), zero for a mark a note or the
	// agent's Seen armed: a batch takes that mark only when each of its
	// records was made after it (madeAfter), and the first batch sealed
	// online that it can't mark ends it. A stretch the running writer began
	// after its newest seal frame, as at a seal's local-only read with
	// nothing pending, is in memory only, and a restart erases it with the
	// mark it cleared, but each record of it was made before Open.
	backFrom moment
	// localAt is when the writer last took config.toml as local-only: a
	// seal's local-only read, a note of a switch to it, or a Seen that says
	// it (seenLocked).
	localAt moment
	// stretches are the local-only stretches the writer knows, oldest
	// first, the last one open while one goes on (stretch), as the seal
	// frames record them across a restart (sealPayload.Stretches); dirty is
	// set while they changed since the newest seal frame recorded them.
	// onlineAt is the last moment the writer knew config.toml said online:
	// just before a seal's read that found it so, or a stretch's end, where
	// the next stretch begins. onlineRun is when this run last sealed a batch
	// online, and onlineWall when an earlier one did, in Unix ms, 0 for none,
	// by which a stretch the writer learns of later counts as passed
	// (sealedSince).
	stretches  []stretch
	dirty      bool
	onlineAt   edge
	onlineRun  moment
	onlineWall int64

	kick     chan struct{}
	stop     chan struct{}
	done     chan struct{}
	stopOnce sync.Once
	job      *sched.Job
}

// pending is a record written since the last seal, with where its frame
// starts. One Write wrote keeps the times Write held it to, with has set:
// its event's times and its sender's stamp (Input.Sent), or for
// lookback's when the writer took it in (Writer.lookback), so that a
// stretch end the writer hears of later holds it to the stretch too
// (lateLocked), and t, its event's own time. One recovery read back gives
// its event's times from its body (envelopeOf), or for lookback's the
// earliest time it can have come (arrivedLocked). late is set once a
// stretch that ended holds it, which makes its batch local-only.
type pending struct {
	rec  ledger.Record
	pos  position
	env  event.Envelope
	sent osclock.Stamp
	has  bool
	late bool
	// scan is set for a git change, whose times are the scan's that found
	// it, not its commit's (madeAfter).
	scan bool
	t    int64
}

// position is a place in the segments: a segment's day and an offset in it.
type position struct {
	Seg string `json:"seg"`
	Off int64  `json:"off"`
}

// Open opens the ledger in opts.Dir, making it if there is none, and
// recovers from whatever a crash left: it cuts a torn frame off the end of
// the newest segment, rebuilds the rows of batches whose seal frames
// reached the disk before their rows did, and seals the records that
// survived without a batch. It returns ErrDamaged for files no crash
// leaves behind, and ErrKeyReuse as Options.KeyID says.
func Open(opts Options) (*Writer, error) {
	if !filepath.IsAbs(opts.Dir) {
		return nil, fmt.Errorf("ledger: the ledger directory must be an absolute path, not %q", opts.Dir)
	}
	if opts.Keys == nil {
		return nil, errors.New("ledger: Open needs the device's ledger keys")
	}
	if !isKeyID(opts.KeyID) {
		return nil, errors.New("ledger: the key id must be 16 to 64 lowercase hex digits")
	}
	table := opts.Params
	if table == nil {
		tables := params.Tables()
		table = tables[len(tables)-1]
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	osNow := opts.Clock
	if osNow == nil {
		osNow = osclock.Now
	}
	w := &Writer{
		dir:      filepath.Clean(opts.Dir),
		keys:     opts.Keys,
		feat:     opts.Keys.Features(),
		keyID:    opts.KeyID,
		chains:   opts.Chains,
		table:    table,
		batchS:   time.Duration(table.Int(params.BATCH_S)) * time.Second,
		now:      now,
		sealedFn: opts.Sealed,
		vault:    opts.Vault,
		online:   opts.Online,
		waiting:  opts.Waiting,
		osNow:    osNow,
		problem:  opts.Problem,
		next:     1,
		kick:     make(chan struct{}, 1),
		stop:     make(chan struct{}),
		done:     make(chan struct{}),
	}
	if s := osNow(); s.HasMono() {
		w.boot = s.BootID
	}
	if err := os.MkdirAll(w.dir, 0o700); err != nil {
		return nil, err
	}
	if err := w.recover(); err != nil {
		w.closeFiles()
		return nil, err
	}
	w.opened = w.b
	if w.vault != nil {
		// Before any record takes a number the crash freed. A vault that
		// can't reconcile says so itself and keeps nothing more, so the
		// ledger goes on without it.
		w.vault.Reconcile(w.next)
	}
	var seen Seen
	if opts.Seen != nil {
		seen = opts.Seen()
	}
	w.mu.Lock()
	// Lookback's records a crash left count by when they can have come
	// (arrivedLocked), before anything holds them to a moment.
	w.arrivedLocked()
	// The newest seal frame's moment and stretches are an earlier run's,
	// read back from disk, so every event of this run came after them (the
	// stretch going on aside). Their wall times stay as they were recorded,
	// though the clock may read earlier now, as after a step back of the
	// wall clock while the agent was stopped: an event of an earlier run or
	// boot, as a call held.jsonl kept, is still held to them by its t, and
	// a hook call of a boot they were recorded in by that boot's clock
	// (earlierEdge).
	w.until = w.earlier(w.until)
	if n := len(w.stretches); n > 0 && w.stretches[n-1].open {
		// The newest seal frame took config.toml as local-only, so the
		// stretch goes on as far as the writer knows, until a note, the
		// agent's Seen or a seal's read that finds the file online ends it.
		w.seen = true
	}
	w.seenLocked(seen, w.momentNow(), false)
	if w.seen && !w.local && w.online == nil {
		// No seal reads config.toml to end it, and the noted mode doesn't
		// outlast a run, as the writer's notes start over: it ends here.
		now := w.momentNow()
		w.raiseUntil(now)
		w.sawOnline(edgeAt(now))
		w.seen = false
	}
	// An event a crash left without a batch, made in a stretch that ended,
	// as the newest seal frame recorded the stretches or the agent saw one
	// end, is sealed local-only, as it would have been without the crash.
	w.lateLocked()
	w.mu.Unlock()
	// Records a crash left without a batch go in one now, so that once
	// Open returns everything on disk is sealed. The uploader keeps an
	// unmarked online batch after a local-only one in its run, so those of
	// a stretch a crash cut short stay on the device too.
	w.sealMu.Lock()
	_, _, err := w.seal()
	w.sealMu.Unlock()
	if err != nil {
		w.closeFiles()
		return nil, err
	}
	go w.loop()
	if opts.Sched != nil && w.batchS > 0 {
		w.job = opts.Sched.Every("ledger.seal", w.batchS, func(time.Time) { w.RequestSeal() })
	}
	return w, nil
}

// isKeyID reports whether s can be a key id: 16 to 64 lowercase hex
// digits, which also makes it safe as a directory name.
func isKeyID(s string) bool {
	if len(s) < 16 || len(s) > 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		if c := s[i]; !('0' <= c && c <= '9' || 'a' <= c && c <= 'f') {
			return false
		}
	}
	return true
}

// newDeviceID makes the id of a new device: random, never derived from
// hardware (pkg/event's Envelope.DeviceID), and an opaque key.
func newDeviceID() string { return "d_" + strings.ToLower(rand.Text()) }

// DeviceID returns the id of the device whose chain the writer writes. It
// goes in every event's device_id.
func (w *Writer) DeviceID() string { return w.device }

// Created returns when the writer's chain began, at its first Open under
// its key (ChainInfo.Created). A record dated before it is lookback's,
// from a harness's logs (DESIGN 6.2 D11), or one that waited for that
// Open, as presence's queue and the hook calls the adapters hold do while
// a locked keyring keeps the ledger shut, so Write tells lookback's by via
// = log as well as by the date (lookback).
func (w *Writer) Created() time.Time { return time.UnixMilli(w.created) }

// Opened is the newest batch an earlier run of the writer sealed, 0 for
// none: the last one Open found in the ledger, the seal frames a crash left
// without a row included, before it sealed in a batch of its own the
// records a crash left without one. A batch with a higher number was
// sealed by this writer.
func (w *Writer) Opened() uint64 { return w.opened }

// Write appends the record of in.Event and returns its sequence number s,
// which is also the event's seq (ledger-v1 4.1). It sets the event's seq
// and device_id, whatever they were, and builds the body with pkg/ledger's
// EventRecord from the event's shareable fields and the content in (spec
// 4.5, 10). For a prompt.submit with its text, it first derives
// low_effort and error_sig from the text, as the scorer reads them; and it
// gives every prompt.submit and cli.track record its class, from pkg/score,
// as the record's f.pc. A prompt.submit or agent.stop seals at once,
// without waiting for the seal to finish.
//
// A record Write refuses, such as an event that fails Validate, takes no
// sequence number. The error leaves the event's content out. Nor does one
// it drops: an event made in a local-only stretch the writer knows, which
// reaches it after a batch was sealed online since the stretch ended
// (ErrLateLocal). One made in such a stretch that comes before then makes
// its batch local-only, and one no stretch holds was made while
// config.toml said online, so it is sealed online however late it comes
// (stretch; D-late-local).
//
// The number is final only once Options.Sealed delivers the record's
// batch. A crash or a power cut before the next seal can cut the record,
// and after the restart the next record takes its number (decision
// E-sm-1.1-seq-gaps, decided; spec wording pending). So whatever is kept
// by the number, such as a vault entry, whose associated data it is
// (WP-LG6), is keyed from Sealed, or checked against the sealed record's
// c before it is revealed.
func (w *Writer) Write(in Input) (uint64, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := w.usable(); err != nil {
		return 0, err
	}
	ev, c := w.prepare(in)
	rec, err := ledger.EventRecord(w.keys, ev, c)
	if err != nil {
		return 0, err
	}
	// The times the record is held to: its event's and its sender's own
	// stamp (Input.Sent), or, for lookback's record of a time before the
	// chain began, when the writer took it in, now (lookback): so are the
	// stretch ends the writer hears of while it is pending.
	held, sent := ev.Envelope, in.Sent
	if w.lookback(ev.Envelope) {
		held, sent = arrival(w.now()), osclock.Stamp{}
	}
	// Made in a local-only stretch, as a git change the watcher held, a
	// hook call parked behind its folder's first Track or held while the
	// keys or the writer weren't there, presence's records queued
	// meanwhile, or a transcript line a log reader read late: it was made
	// while nothing could upload it (DESIGN 4.2). A record counts by the
	// stretch it was made in, not by whether it waited through one, which
	// is the rule D-late-local put in place of #174's. By the monotonic
	// clock where the event carries one a stretch's edge holds too, so a
	// step of the wall clock while it waited can't hide it, and by its
	// sender's own stamp too, which a step can have put after the agent's
	// (Input.Sent).
	late, drop := w.judge(held, sent)
	if drop != nil {
		// A batch was sealed online since the stretch ended, so a batch
		// sealed local-only now would stop the batch roots until the next
		// sign-in (ingest-v1 5.3): the record stays off the ledger.
		drop.dropped++
		w.dirty = true
		return 0, fmt.Errorf("%w: a %s record made at %s", ErrLateLocal, ev.Kind(), time.UnixMilli(ev.T).UTC().Format(time.RFC3339))
	}
	if err := w.append(rec); err != nil {
		return 0, err
	}
	if w.vault != nil {
		if text, paths := kept(ev, in, c); text != nil || len(paths) > 0 {
			w.vault.Keep(w.segDay, rec.Header.Seq(), text, paths)
		}
	}
	p := &w.pending[len(w.pending)-1]
	p.env, p.sent, p.has, p.t = held, sent, true, ev.T
	p.scan = ev.Kind() == event.KindGitChange
	if late {
		// No batch was sealed online since the stretch ended, so the record
		// goes in the run of local-only batches before the first one the
		// server can take: its batch is sealed local-only, and a mark waits
		// for the next batch (Batch.Back).
		p.late, w.late = true, true
	}
	w.q.Observe(ev)
	if k := ev.Kind(); (k == event.KindPromptSubmit || k == event.KindAgentStop) && !in.Late {
		w.RequestSeal()
	}
	return rec.Header.Seq(), nil
}

// prepare stamps the event with the next sequence number and the device,
// and works out the content EventRecord takes.
func (w *Writer) prepare(in Input) (event.Event, ledger.Content) {
	ev := in.Event
	ev.Seq, ev.DeviceID = w.next, w.device
	c := ledger.Content{Text: in.Text, Edits: in.Edits, Command: in.Command}
	switch p := ev.Data.(type) {
	case *event.PromptSubmit:
		if p == nil {
			break
		}
		// A copy, so the caller's event isn't changed.
		cp := *p
		switch {
		case cp.Guest:
			// A guest's words never stay on the device (SM 1.2, DESIGN 5.1):
			// whatever text came with the prompt, its record commits none
			// and its event keeps none (ledger-v1 10.1).
			c.Text, c.NoText, cp.SealedText = nil, true, nil
		case in.Text != nil:
			text := string(in.Text)
			cp.LowEffort = score.LowEffort(text)
			cp.ErrorSig = ""
			if es, ok := w.feat.ErrorSig(text); ok {
				cp.ErrorSig = es.String()
			}
		case ev.Via == event.ViaBridge || ev.Via == event.ViaBridgeUnverified || ev.Via == event.ViaPlugin:
			c.NoText = true
		}
		ev.Data = &cp
		c.Class = features.Class(score.PromptClass(w.table, &cp, w.q.Asked(ev.SessionKey)))
	case *event.CLITrack:
		if p != nil {
			c.Class = features.Class(score.TrackClass(w.table, p))
		}
	case *event.AgentOutput:
		if p != nil && p.IsFinal && in.Text == nil && ev.Via == event.ViaPlugin {
			c.NoText = true
		}
	}
	return ev, c
}

// kept returns what the vault keeps of the record EventRecord made of ev
// with c: text, the bytes its c commits, which for a final agent message
// is its first 16 KiB (ledger-v1 10.1), and nil when it has no c; and
// paths, the paths of an agent.tool's edits.
func kept(ev event.Event, in Input, c ledger.Content) (text []byte, paths []string) {
	switch p := ev.Data.(type) {
	case *event.PromptSubmit, *event.CLITrack:
		if !c.NoText {
			text = committed(in.Text)
		}
	case *event.AgentOutput:
		if p != nil && p.IsFinal && !c.NoText {
			text = committed(ledger.CommittedMessage(in.Text))
		}
	case *event.AgentTool:
		for _, e := range in.Edits {
			paths = append(paths, e.Path)
		}
	}
	return text, paths
}

// committed is text as a record commits it: a record without text commits
// the empty text, which the vault keeps as such.
func committed(text []byte) []byte {
	if text == nil {
		return []byte{}
	}
	return text
}

// Checkpoint appends a scorer checkpoint (rtype 0x40): projectKey's
// serialized score.ProjectState at the UTC minute t, in Unix ms, a
// multiple of 900,000 (ledger-v1 4.7; DESIGN 3.6, 6.5). It returns the
// record's sequence number, which is final only once Options.Sealed
// delivers its batch, as Write's is: a reference to the checkpoint takes
// its number from Sealed.
func (w *Writer) Checkpoint(projectKey string, t uint64, state json.RawMessage) (uint64, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := w.usable(); err != nil {
		return 0, err
	}
	rec, err := ledger.CheckpointRecord(w.keys, w.next, projectKey, t, state)
	if err != nil {
		return 0, err
	}
	if err := w.append(rec); err != nil {
		return 0, err
	}
	return rec.Header.Seq(), nil
}

// UploadFailure appends an upload-failure note (rtype 0x41): an upload
// failed at t, in Unix ms, for reason, one of dns, connect, tls, http_5xx
// and http_429 (ledger-v1 4.7; DESIGN 3.6, 7.4). It returns the record's
// sequence number, which is final only once Options.Sealed delivers its
// batch, as Write's is.
func (w *Writer) UploadFailure(t uint64, reason string) (uint64, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := w.usable(); err != nil {
		return 0, err
	}
	rec, err := ledger.UploadFailureRecord(w.keys, w.next, t, reason)
	if err != nil {
		return 0, err
	}
	if err := w.append(rec); err != nil {
		return 0, err
	}
	return rec.Header.Seq(), nil
}

// usable reports why the writer can't write, or nil. w.mu is held.
func (w *Writer) usable() error {
	switch {
	case w.closed:
		return ErrClosed
	case w.failed != nil:
		return fmt.Errorf("ledger: the writer stopped after a failure, and the next Open recovers: %w", w.failed)
	}
	return nil
}

// append writes rec's frame to the segment of today, or of the newest day
// a segment has if the clock went back. A record too large for a frame,
// such as a checkpoint of a huge state, is refused before anything is
// written, so it takes no number. w.mu is held.
func (w *Writer) append(rec ledger.Record) error {
	payload := make([]byte, 0, ledger.HeaderSize+len(rec.Body))
	payload = append(payload, rec.Header[:]...)
	payload = append(payload, rec.Body...)
	fr, err := frameOf(kindRecord, payload)
	if err != nil {
		return err
	}
	if err := w.toSegment(); err != nil {
		return err
	}
	pos := position{Seg: w.segDay, Off: w.seg.size}
	if err := w.seg.write(fr); err != nil {
		return w.writeFailed(err)
	}
	w.pending = append(w.pending, pending{rec: rec, pos: pos})
	w.next++
	if w.local {
		// The stretch's first record seals at once, so its batch is on disk
		// as local-only before a crash can cut the records after it.
		if !w.taint && !w.stretchSealed {
			w.RequestSeal()
		}
		w.taint = true
	}
	if len(w.pending) >= pendingMax {
		w.RequestSeal()
	}
	return nil
}

// NoteMode takes config.toml's mode as the agent's config watch reads it
// each time the file changes, online or local-only (Options.Online). A
// record written while the mode noted last is local-only makes its batch
// local-only, whatever Online says at the seal, as when the teen switches
// back online before the next seal. The switch to local-only seals what it
// finds pending as local-only, since the watch notes it a moment after the
// file changed, and with nothing pending the stretch's first record seals
// at once: either way the stretch reaches the disk before a crash can cut
// the records after it, and the batch sealed online after it counts as
// local-only too (the uploader's saved.mode). The switch back online, after
// a noted stretch or while the newest batch counts as local-only, as at
// pairtime login after batches sealed while config.toml said local, seals
// what it finds pending, and the first batch after it whose records all
// came after the note is marked as one (Batch.Back): the run of local-only
// batches ends before it, so its root and the minutes after the switch can
// go. An event made in the stretch that the writer gets after the note, as
// a git change the watcher held, makes its batch local-only until a batch
// is sealed online, and is dropped after that (Write), where lookback's
// count by when the writer took them in (Writer.lookback), and the mark
// goes on the next batch whose records all came after the note, sealed
// while no such event is still on its way (Options.Waiting). A switch back
// the watch misses, as to a symlinked config.toml's target, ends the
// stretch at the next seal that reads the file online, and marks no batch.
// The watch's note of a switch back that a seal's read found first, as
// when pairtime login renames config.toml into place while Open seals,
// still arms the mark, unless a batch was sealed online in between, which
// ended the run of local-only batches already. Each note is a moment the
// writer knew the file's mode, where a stretch begins or ends (stretch).
func (w *Writer) NoteMode(online bool) { w.NoteModeThen(online, nil) }

// NoteModeThen is NoteMode, and runs then, when set, with the record lock
// still held: a seal that read config.toml before the note reads it again
// after both, as after any note, and none reads it between the two, as the
// agent's Online, which then tells when it may say online, needs.
func (w *Writer) NoteModeThen(online bool, then func()) {
	at := w.momentNow()
	w.mu.Lock()
	seal := w.noteLocked(online, edgeAt(at))
	if then != nil {
		then()
	}
	w.mu.Unlock()
	if seal {
		w.RequestSeal()
	}
}

// noteLocked is NoteMode's change of state for a note made at, and
// reports whether it asks for a seal. w.mu is held.
func (w *Writer) noteLocked(online bool, at edge) bool {
	w.notes++
	// A switch back follows a stretch the writer had: as the mode noted
	// last says, as the newest batch counts as local-only, or as a seal's
	// read ended unnoted.
	back := online && (w.local || w.lastLocal || w.unnoted)
	begins := !online && !w.local
	if begins {
		w.stretchSealed = false
		w.taint = w.taint || len(w.pending) > 0
	}
	if online && (w.local || w.seen) {
		// The stretch ends as the note comes, which is the first the
		// writer knows of config.toml back online: an event made before the
		// note may have been made while it said local-only. A note that
		// only repeats online moves nothing, so the watch's reload after the
		// note its first event made takes no credit. What pending holds of
		// the stretch is held to it too, as Write holds what comes after.
		w.raiseUntil(at.m)
		w.sawOnline(at)
		w.lateLocked()
	}
	switch {
	case back && w.back == 0:
		w.back, w.backFrom = w.next, moment{}
	case !online:
		w.back, w.backFrom = 0, moment{}
	}
	if !online {
		w.localAt = at.m
		// A stretch begins, from where the writer last knew config.toml
		// said online, unless one goes on.
		w.sawLocal()
	}
	w.local, w.seen = !online, !online
	return back || (begins && w.taint)
}

// Seen is what the agent saw of config.toml's mode before the writer
// could hear its config watch's notes (Options.Seen, NoteSeen): whether
// the file says local-only, and when the newest local-only stretch the
// agent saw ended, the moment it saw the file back online, by Go's clock
// with its monotonic reading and by the OS's, or the zero time for none.
// The agent starts seeing at its start, which is where every install's
// first stretch begins, and a link it didn't see, as one made while it was
// stopped, ends the stretch where the agent finds it.
// Earlier says an earlier run of the agent saw Ended, as state.json kept
// it, so every event of this run came after it (Writer.earlier). Until,
// when later than Ended by the wall clock, is how long the stretch may
// have lasted by the wall clock alone, as to the end of the second of a
// link the agent didn't see (D-relink-boundary): an event the moment holds
// by no monotonic clock it carries is held to it.
type Seen struct {
	Local   bool
	Ended   time.Time
	OS      osclock.Stamp
	Earlier bool
	Until   time.Time
}

// NoteSeen takes what the agent saw of config.toml's mode before it
// published the writer to its config watch: a local-only stretch it saw
// end, which the writer records when it didn't know of it, makes every
// event made in it local-only, or dropped once a batch is sealed online
// (Write), those the writer took already included, and every lookback
// record the writer took in during it (Writer.lookback); and the first
// batch after it that has none, sealed while no such event is still on
// its way, is marked (Batch.Back), as after a noted switch back. A file
// that says local-only now begins a stretch, as NoteMode(false) does.
// Open takes the same from Options.Seen before it seals what a crash
// left, and the agent calls NoteSeen as it publishes the writer, for what
// it saw since, and as it takes in a pairtime login it didn't see.
func (w *Writer) NoteSeen(s Seen) { w.NoteSeenThen(s, nil) }

// NoteSeenThen is NoteSeen, and runs then, when set, with the record lock
// still held, as NoteModeThen does.
func (w *Writer) NoteSeenThen(s Seen, then func()) {
	at := w.momentNow()
	w.mu.Lock()
	seal := w.seenLocked(s, at, false)
	if then != nil {
		then()
	}
	w.mu.Unlock()
	if seal {
		w.RequestSeal()
	}
}

// NoteSeenWhile is NoteSeenThen for a stretch end the agent found by its
// own read of config.toml, as for a switch back a seal read and the config
// watch didn't note, which online reads again with the record lock held:
// the end is that read, by each clock, so no local-only read a seal took
// before it outlasts it; and a read that finds config.toml local-only
// again begins a stretch, as a seal's would, and ends none.
func (w *Writer) NoteSeenWhile(s Seen, online func() bool, then func()) {
	w.mu.Lock()
	on := online()
	now, stamp := w.now(), w.osNow()
	if on {
		s.Ended, s.OS, s.Earlier = now, stamp, false
	} else {
		s.Ended, s.Local = time.Time{}, true
	}
	seal := w.seenLocked(s, momentAt(now, stamp), on)
	if then != nil {
		then()
	}
	w.mu.Unlock()
	if seal {
		w.RequestSeal()
	}
}

// seenLocked is NoteSeen's change of state, now at at, and reports
// whether it asks for a seal. A stretch end the writer has already, as
// the newest seal frame recorded it, arms no mark: the agent sees the same
// end at every start, and a mark due then would hold the first batch for
// what is still on its way after any restart. Nor, when the seal frames
// recorded no moment, as an older version's didn't, does one an earlier
// run saw before the newest batch was sealed: that run marked the batch
// after the end, or sealed the ones since local-only, after which each
// seal asks Waiting while the newest is (lastLocal), and the server may
// hold those after, so a mark due now would only hold back credit. Where
// the frames record a moment, an end later than it is one the writer never
// had, so no batch was sealed after it. One the agent saw in this run is
// newer by this run's clock, and one of the boot the moment's OS reading
// is of by that clock, whatever the wall clock says. An end that ends no
// stretch the writer takes as going on, and that moves the moment or is
// of a boot whose clock orders it with no stretch end the writer has, is a
// stretch the writer didn't know of: it is recorded from where the writer
// last knew config.toml said online (learned). A record the writer took
// before it heard of the end, as one the agent took in before it found a
// pairtime login it hadn't seen, was made in the stretch if the stretch
// holds it, or for lookback's if it was taken in then, so its batch is
// local-only, as Write would have sealed it. w.mu is held.
func (w *Writer) seenLocked(s Seen, at moment, sure bool) bool {
	// A seal reading config.toml meanwhile reads it again, as after a note.
	w.notes++
	seal := false
	if !s.Ended.IsZero() {
		end := edgeAt(s.moment())
		if s.Earlier {
			end = w.earlierEdge(s.moment())
		}
		ended := end.m
		// Frames that recorded no moment leave only the newest batch's seal
		// to tell an earlier run's end from one the writer never had.
		sealedSince := s.Earlier && w.until.wall == 0 && w.until.os == 0 && w.sealed > ended.wall
		// An end that isn't surely after the writer's last local-only take,
		// as one the agent read before a seal's local-only read, or one of
		// the same millisecond, ends nothing that came after that take, and
		// arms no mark for it: privacy wins what can't be told. sure is set
		// for an end the writer read itself under the record lock, after
		// every take it has (NoteSeenWhile).
		after := sure || w.localAt == (moment{}) || w.localAt.earlierThan(ended)
		later := ended.run > w.until.run || ended.wall > w.until.wall ||
			ended.os > 0 && ended.boot == w.until.boot && ended.os > w.until.os
		newer := !sealedSince && after && later
		was := w.until
		ends := (w.local || w.seen) && newer
		if ends {
			// A stretch the writer took as going on, or one a seal's
			// local-only read saw, which the agent saw end: the next online
			// read then moves the moment no further than this end. An end the
			// writer had already came before the stretch began, so it ends
			// nothing.
			seal = w.noteLocked(true, end)
		}
		if later {
			// Only an end later than the moment by a clock the two hold moves
			// it. One earlier by each of them, as a stretch end the agent kept
			// from before a sweep folded the moment to the wall clock alone,
			// adds none of its readings: it is no stretch the writer didn't
			// know of, and brings no old reading back (Sweep).
			w.raiseUntil(ended)
		}
		if moved, cross := w.until != was, w.crossBoot(end); !ends && !w.openHolds(ended) && (moved || cross) {
			// A stretch the writer didn't know of: later than every end it
			// had, or of a boot it has no end of, as one a run whose ledger
			// stayed shut saw, which only that boot's clock orders.
			w.learned(end, !moved)
			w.lateLocked()
		}
		if newer && w.back == 0 {
			w.back, w.backFrom = w.next, moment{}
		}
	}
	if s.Local {
		seal = w.noteLocked(false, edgeAt(at)) || seal
	}
	return seal
}

// crossBoot reports whether end, a stretch end the agent saw, was taken in
// another boot than the writer's, and than the newest stretch end the
// writer has, which has a reading of the OS's clock too, so no clock
// orders the two. Without such readings the wall clock orders them. w.mu
// is held.
func (w *Writer) crossBoot(end edge) bool {
	if end.other.os == 0 {
		return false
	}
	newest, ok := w.newestEnd()
	b := newest.osOf().Boot
	return ok && b != "" && b != end.other.boot
}

// moment is the stretch end s gives, by each clock it was read by, and by
// the wall clock to Until when that is later.
func (s Seen) moment() moment {
	m := momentAt(s.Ended, s.OS)
	if !s.Until.IsZero() {
		m.wall = max(m.wall, s.Until.UnixMilli())
	}
	return m
}

// momentNow is now by each clock a moment holds.
func (w *Writer) momentNow() moment { return momentAt(w.now(), w.osNow()) }

// madeAfter reports whether each record pending was made after m, Open's
// moment, by every time Write holds it to (pending): its event's times and
// its sender's own stamp. A monotonic reading of another boot, when m holds
// this boot's, or of another run, was taken before m whatever the wall
// clock says, since Open runs in this boot and this run: the converse of
// what Writer.earlier holds. A git change goes by the scan that found it,
// which can come after m whenever its commit was made, so none counts as
// made after m. A record that isn't an event, as a scorer checkpoint, was
// made as it was written. A zero m holds nothing back. w.mu is held.
func (w *Writer) madeAfter(m moment) bool {
	if m == (moment{}) {
		return true
	}
	before := func(e event.Envelope) bool {
		if e.Mono > 0 && e.BootID != runBoot && (strings.HasPrefix(e.BootID, runPrefix) || m.os > 0 && e.BootID != m.boot) {
			return true
		}
		return m.before(e)
	}
	for _, p := range w.pending {
		if !p.has {
			continue
		}
		if p.scan || before(p.env) {
			return false
		}
		if !p.sent.Wall.IsZero() && before(event.Envelope{T: p.sent.Wall.UnixMilli(), Mono: p.sent.Mono, BootID: p.sent.BootID}) {
			return false
		}
	}
	return true
}

// earlier is m, a moment an earlier run of the agent recorded, with the
// bounds of this run and this boot: every event of this run was made
// after it, so it holds them to this run's clock from its start, and when
// it was recorded in another boot, every hook call of this boot was made
// after it too. A moment recorded by the wall clock alone says nothing of
// its boot, so the hook calls of this boot are held to it by their t.
func (w *Writer) earlier(m moment) moment {
	if m.wall == 0 && m.os == 0 {
		return m
	}
	m.run = max(m.run, 1)
	if w.boot != "" && m.os > 0 && m.boot != w.boot {
		m.os, m.boot = 1, w.boot
	}
	return m
}

// raiseUntil moves until on to m, by each clock, as moment.raise does.
// When it moves, the next seal frame records the stretches too, so that a
// row whose moment rose past the newest list's, with no list of its own,
// is an older version's (restoreStretches). w.mu is held.
func (w *Writer) raiseUntil(m moment) {
	was := w.until
	w.until.raise(m, w.boot)
	if w.until != was {
		w.dirty = true
	}
}

// writeFailed passes on a failed write. A write that couldn't be undone
// stops the writer. w.mu is held.
func (w *Writer) writeFailed(err error) error {
	if errors.Is(err, errStuck) {
		w.failed = err
	}
	return err
}

// toSegment makes w.seg the segment of the current UTC day, flushing and
// retiring the one before. It never goes back to an older day, so the
// segments' names stay in the order of their records. w.mu is held.
func (w *Writer) toSegment() error {
	day := dayOf(w.now())
	if w.seg != nil && day <= w.segDay {
		return nil
	}
	if w.seg != nil {
		// A segment is complete once the day is over, so recovery need
		// only ever cut the newest one.
		if err := w.seg.sync(); err != nil {
			w.failed = err
			return err
		}
	}
	f, err := openAppend(segPath(w.dir, day), true)
	if err != nil {
		return err
	}
	syncDir(w.dir)
	if w.seg != nil {
		w.retired = append(w.retired, w.seg)
	}
	w.seg, w.segDay = f, day
	return nil
}

// sealPayload is a seal frame's payload: the batch it closes.
type sealPayload struct {
	B        uint64      `json:"b"`
	FirstSeq uint64      `json:"first_seq"`
	N        uint64      `json:"n"`
	Prev     ledger.Hash `json:"prev"`
	Root     ledger.Hash `json:"root"`
	Head     ledger.Hash `json:"head"`
	// SealedAt is when the batch was sealed, in Unix ms.
	SealedAt int64 `json:"sealed_at"`
	// Online says whether the batch was sealed online (Options.Online). It
	// is in the seal frame, so the batch's mode reaches the disk in the
	// seal's flush, and in the row, which Open rebuilds from that frame.
	// The writer writes it in every seal frame, false as well as true, so a
	// frame without it is one an older pairtime version wrote, which
	// recorded no mode (Row.NoMode).
	Online *bool `json:"online,omitempty"`
	// Back marks the first batch sealed online whose records all came
	// after the noted switch back online, events by their own time too,
	// lookback's by when the writer took them in, with no seal since that
	// read config.toml as local-only (Batch.Back).
	Back bool `json:"back,omitempty"`
	// Due is the first record of the batch the writer had a mark due for as
	// it sealed this one, which didn't take it (Writer.back), 0 for none, so
	// the mark outlasts a restart: Open keeps it due from the newest row
	// until a batch sealed Back takes it (restoreStretches).
	Due uint64 `json:"due,omitempty"`
	// LocalUntil is when config.toml was last known, or taken, to say
	// local-only as the batch was sealed, in Unix ms (Writer.until), the
	// newest stretch end: Open takes it back from the newest batch, and the
	// stretches of a ledger whose seal frames recorded none from every
	// row's (stretchesFromRows). 0 before any. LocalUntilOS is the same
	// moment by the OS's monotonic clock, when the writer read that clock
	// then, so a hook call of that boot is held to it by its mono after a
	// restart too, as the running writer did.
	LocalUntil   int64    `json:"local_until,omitempty"`
	LocalUntilOS *untilOS `json:"local_until_os,omitempty"`
	// Stretches are every local-only stretch the writer knew as the batch
	// was sealed (Writer.stretches), in the seal frame of each batch sealed
	// after they changed, and absent from the rest: Open takes them back
	// from the newest frame that has them, or from batches.seg's base frame
	// after a sweep dropped its row (basePayload.Stretches).
	Stretches []stretchPayload `json:"stretches,omitempty"`
	// Why says why a batch sealed local-only is (Batch.Why), absent from
	// one sealed online.
	Why *whyPayload `json:"why,omitempty"`
	// Q is what the prompt classes' questions remember after the batch's
	// last record (score.Questions), in the seal frame of every
	// questionRows-th batch, so Open can restore them exactly rather than
	// read every record again. Rows leave it out.
	Q *score.QuestionsState `json:"q,omitempty"`
}

// online reports whether the batch was sealed online, as its seal frame
// records: one that records no mode counts as sealed in local-only mode.
func (sp sealPayload) online() bool { return sp.Online != nil && *sp.Online }

// untilOS is a moment by the OS's monotonic clock: its reading in ms, and
// the boot it counts (sealPayload.LocalUntilOS).
type untilOS struct {
	Mono int64  `json:"mono"`
	Boot string `json:"boot"`
}

// until is the moment the seal frame recorded (LocalUntil, LocalUntilOS),
// with no reading of this run's clock, which a restart starts again.
func (sp sealPayload) until() moment {
	m := moment{wall: sp.LocalUntil}
	if o := sp.LocalUntilOS; o != nil && o.Mono > 0 && o.Boot != "" {
		m.os, m.boot = o.Mono, o.Boot
	}
	return m
}

// msTime is the time of ms, in Unix ms, and the zero time for 0.
func msTime(ms int64) time.Time {
	if ms <= 0 {
		return time.Time{}
	}
	return time.UnixMilli(ms)
}

// rowPayload is a batch frame's payload in batches.seg: DESIGN 3.5's row,
// {b, first_seq, last_seq, n, R_b, H_b, sealed_at}, with prev, online,
// back, local_until and where the batch's frames are. The receipt goes in
// a receipt frame of its own, when it arrives.
type rowPayload struct {
	sealPayload
	LastSeq uint64 `json:"last_seq"`
	// Start is where the batch's first record frame starts, and End where
	// its seal frame ends.
	Start position `json:"start"`
	End   position `json:"end"`
	// Snap is where the newest seal frame that holds Q starts, this
	// batch's or an earlier one's, or zero before the first.
	Snap position `json:"snap,omitzero"`
}

// receiptPayload is a receipt frame's payload in batches.seg.
type receiptPayload struct {
	B          uint64 `json:"b"`
	ReceivedAt uint64 `json:"received_at"`
	Signature  string `json:"signature"` // hex
}

// chainPayload is batches.seg's first frame: the chain the file belongs
// to.
type chainPayload struct {
	V        int    `json:"v"`
	DeviceID string `json:"device_id"`
	KeyID    string `json:"key_id"`
	Created  int64  `json:"created"`
}

// chainVersion is the layout of the writer's files, chainPayload.V, and
// chainVersionBase the layout of a batches.seg that a retention sweep wrote
// with a base frame after its chain frame (Writer.Sweep). A pairtime that
// reads only chainVersion refuses such a file at its chain frame, before it
// reads on to the base frame, which it doesn't know, so it never takes the
// frame for a crash's torn tail and cuts the rows after it off
// (docs/errata/2026-11-23-unsent-batches-retention.md).
const (
	chainVersion     = 1
	chainVersionBase = 2
)

// Seal seals the records written since the last batch into the next batch
// and returns it with ok set, once the batch is on disk and its row is in
// batches.seg; ok is false when there was nothing to seal. A failed flush
// stops the writer, because after one the kernel may have dropped what it
// couldn't write, and only the next Open can tell what reached the disk.
func (w *Writer) Seal() (b Batch, ok bool, err error) {
	w.sealMu.Lock()
	defer w.sealMu.Unlock()
	return w.seal()
}

// RequestSeal asks for a seal without waiting for it.
func (w *Writer) RequestSeal() {
	select {
	case w.kick <- struct{}{}:
	default: // a seal is already due
	}
}

// seal is Seal. w.sealMu is held.
func (w *Writer) seal() (Batch, bool, error) {
	w.mu.Lock()
	// asked is whether Waiting was asked about the batch as it was due its
	// mark, with config.toml online. ended is when a read that found it
	// online ended a stretch the writer took as going on, zero for none,
	// and read when the read began, the last moment the writer knew the
	// file said what it said before the read.
	var online, waiting, asked bool
	var ended, read moment
	for reads := 1; ; reads++ {
		notes, next := w.notes, w.next
		// The first batch after a switch back online is due its mark. So,
		// short of the mark, is any batch while the newest counts as
		// local-only, as after a restart before the batch after a switch
		// back took its mark: a run of local-only batches is open, and an
		// event of the stretch that reaches the writer late must land in it,
		// not after the online batch that closes it.
		due := w.waiting != nil && (w.back > 0 || w.lastLocal) && len(w.pending) > 0
		wait := w.waitForLocked()
		var going stretch
		var goingPrev int64  // the end of the stretch before it (stretch.widen)
		var others []stretch // the rest, for orderWall
		if n := len(w.stretches); n > 0 && w.stretches[n-1].open {
			going, goingPrev = w.stretches[n-1], prevEnd(w.stretches, n-1)
			others = append([]stretch(nil), w.stretches[:n-1]...)
		}
		ends := w.online != nil && (w.local || w.seen)
		w.mu.Unlock()
		// A switch to local-only mode that came before the seal makes the
		// batch local-only, whenever its records came.
		read = w.momentNow()
		online = w.online == nil || w.online()
		ended = moment{}
		if online && ends {
			// The read ends the stretch, as taken just after it: Waiting is
			// asked with the stretch ended there, as Write holds what comes
			// later to it.
			ended = w.momentNow()
			if going.open {
				going.to, going.open = edgeAt(ended), false
				going.widen(goingPrev)
				orderWall(&going, others, -1)
				wait = append(wait, going)
			}
		}
		// An event made in the stretch that is still on its way would
		// reach the writer after the mark, so the batch is local-only and
		// the mark waits (Options.Waiting).
		asked = due && online
		waiting = asked && w.waiting(wait.holds)
		w.mu.Lock()
		if w.notes == notes && w.next == next {
			break
		}
		// A note came while config.toml was read, as the config watch's of
		// a switch back online, after which records are online: it is read
		// again, so the batch isn't sealed in the mode from before the note.
		// So are records, which may have come once the file changed, as to
		// local-only, while the read still saw it as it was: a read after
		// them seals them in the mode they came in.
		if w.notes == notes && reads >= sealReads {
			// Records came through every read, as in a drain: the last
			// read is made under the record lock, so none can come during
			// it, and Write waits for one read of a small file. Waiting
			// takes other locks, so it can't be asked under this one: a
			// batch these records made due its mark, or one whose read here
			// ends a stretch later than the moment Waiting was asked by, is
			// sealed as while one of the stretch is on its way, and the mark
			// waits for the next.
			read = w.momentNow()
			online = w.online == nil || w.online()
			ended = moment{}
			if online && w.online != nil && (w.local || w.seen) {
				ended = w.momentNow()
			}
			waiting = waiting || online && (!asked || ended != moment{}) && w.waiting != nil && (w.back > 0 || w.lastLocal)
			break
		}
	}
	if w.online != nil {
		switch {
		case online && (w.local || w.seen):
			// config.toml says online, and no note came since it was read: a
			// stretch the noted mode, or a read, still says is local-only
			// ended unnoted, by the read, so an event made in it may have come
			// in it, those pending included, as Write holds what comes after.
			// The batch's own records keep their taint. Without Online, only
			// NoteMode ends a stretch.
			if ended == (moment{}) {
				ended = w.momentNow()
			}
			w.raiseUntil(ended)
			w.sawOnline(edgeAt(ended))
			w.lateLocked()
			w.local, w.seen = false, false
			// The watch's note of the switch, which this read can come
			// before, still arms the mark (noteLocked).
			w.unnoted = true
		case !online:
			// config.toml says local-only, which the watch may not have
			// noted, as in its debounce or for a symlinked config.toml's
			// target. A batch sealed now is local-only, and the uploader keeps
			// the online batch after it in its run, records written after
			// this read with it. With nothing pending there is no such batch,
			// so the stretch begins here, as at a noted switch: the records
			// after this read count as made in it, its first sealed at once,
			// until a note or a read says online. In the stretches it began
			// where the writer last knew the file said online, unless one goes
			// on.
			w.seen, w.localAt = true, w.momentNow()
			w.sawLocal()
			if len(w.pending) == 0 && !w.local {
				w.local, w.stretchSealed = true, false
			}
		}
		if online {
			// A stretch that begins after this read begins at it at the
			// latest.
			w.raiseOnline(edgeAt(read))
		}
	}
	if !online {
		// config.toml says local-only since the switch back online was
		// noted, as before the watch notes a switch to it: the records
		// after the note may have come in local-only mode, so no batch
		// after it is marked (Batch.Back).
		w.back, w.backFrom = 0, moment{}
	}
	if err := w.usable(); err != nil {
		w.mu.Unlock()
		return Batch{}, false, err
	}
	if len(w.pending) == 0 {
		retired := w.takeRetired()
		w.mu.Unlock()
		closeAll(retired)
		return Batch{}, false, nil
	}
	recs := make([]ledger.Record, len(w.pending))
	entries := make([]ledger.Entry, len(w.pending))
	for i, p := range w.pending {
		recs[i] = p.rec
		entries[i] = p.rec.Entry()
	}
	start := w.pending[0].pos
	row, err := ledger.Seal(w.head, w.b+1, entries)
	if err != nil {
		w.mu.Unlock()
		return Batch{}, false, err
	}
	if err := w.toSegment(); err != nil {
		w.mu.Unlock()
		return Batch{}, false, err
	}
	at := w.now()
	// A batch holding an event made in a local-only stretch that ended is
	// local-only in its own right: the uploader takes it into the open run,
	// or opens one at it when the switch-back seal's batch closed the run,
	// and the next batch's mark closes that one before itself. So is a
	// batch sealed while such an event is still on its way, so that it
	// lands before the marked batch. No batch sealed online has passed the
	// stretch yet, or Write would have dropped the event (ErrLateLocal).
	on := online && !w.taint && !w.late && !waiting
	takes := on && w.back > 0 && row.FirstSeq >= w.back
	sp := sealPayload{B: row.B, FirstSeq: row.FirstSeq, N: row.N, Prev: w.head, Root: row.Root, Head: row.Head,
		SealedAt: at.UnixMilli(), Online: &on, Back: takes && w.madeAfter(w.backFrom), LocalUntil: w.until.wall}
	if takes && !sp.Back {
		// A batch sealed online that a mark Open restored can't mark, as one
		// holding a record made before Open or a git change, closes the run of
		// local-only batches with itself inside, so the mark has nothing left
		// to mark: it ends here, its frame records none, and no later seal
		// asks Waiting for it.
		w.back, w.backFrom = 0, moment{}
	}
	if !sp.Back {
		sp.Due = w.back
	}
	if w.until.os > 0 {
		sp.LocalUntilOS = &untilOS{Mono: w.until.os, Boot: w.until.boot}
	}
	if !on {
		sp.Why = w.whyLocked(online, waiting)
	}
	// A batch sealed online passes every stretch that ended (passAll), so
	// its seal frame records them as passed.
	if w.dirty || on && w.passes() {
		sp.Stretches = w.stretchPayloads(on)
	}
	snap := w.snap
	if row.B%questionRows == 0 {
		// w.q has taken every record the batch holds and none after.
		st := w.q.State()
		sp.Q = &st
		snap = position{Seg: w.segDay, Off: w.seg.size}
	}
	payload, err := json.Marshal(sp)
	if err != nil {
		w.mu.Unlock()
		return Batch{}, false, err
	}
	fr, err := frameOf(kindSeal, payload)
	if err != nil {
		w.mu.Unlock()
		return Batch{}, false, err
	}
	if err := w.seg.write(fr); err != nil {
		err = w.writeFailed(err)
		w.mu.Unlock()
		return Batch{}, false, err
	}
	end := position{Seg: w.segDay, Off: w.seg.size}
	seg := w.seg
	retired := w.takeRetired()
	if on {
		// A record of a stretch that comes from now on would stop the batch
		// roots in a batch of its own, so it is dropped (Write).
		w.passAll()
		w.onlineRun = momentAt(at, osclock.Stamp{})
		// This batch ended the run of local-only batches, unmarked, so a
		// note of a switch back a seal's read found arms no mark now.
		w.unnoted = false
	}
	if sp.Stretches != nil {
		w.dirty = false
	}
	w.pending = nil
	w.stretchSealed = w.stretchSealed || w.taint
	w.taint, w.late = false, false
	w.lastLocal = !on
	if sp.Back {
		w.back, w.backFrom = 0, moment{}
	}
	w.b, w.head, w.snap, w.sealed = row.B, row.Head, snap, sp.SealedAt
	w.mu.Unlock()

	if w.vault != nil {
		// The content of the batch's records goes to disk before the batch
		// does (Options.Vault).
		w.vault.Sync()
	}
	// The one flush of the seal. Records written since the seal frame go
	// to disk with it, which does no harm.
	err = seg.syncTo(end.Off)
	closeAll(retired)
	if err != nil {
		w.fail(err)
		return Batch{}, false, err
	}
	rp := rowPayload{sealPayload: sp, LastSeq: row.FirstSeq + row.N - 1, Start: start, End: end, Snap: snap}
	rp.Q = nil
	if err := w.writeRow(rp); err != nil {
		// The batch is sealed and on disk, and the next Open writes its
		// row from the seal frame. Until then nothing more goes in
		// batches.seg out of order.
		w.fail(err)
		return Batch{}, false, err
	}
	batch := Batch{Row: row, SealedAt: time.UnixMilli(sp.SealedAt), Online: on, Back: sp.Back,
		LocalUntil: msTime(sp.LocalUntil), Why: localWhy(sp.Why), Records: recs}
	if w.sealedFn != nil {
		w.sealedFn(batch)
	}
	return batch, true, nil
}

// takeRetired returns the retired segments for the caller to close once
// its flush is done, and forgets them. w.mu is held.
func (w *Writer) takeRetired() []*appendFile {
	r := w.retired
	w.retired = nil
	return r
}

func closeAll(files []*appendFile) {
	for _, f := range files {
		f.close()
	}
}

// fail stops the writer after err.
func (w *Writer) fail(err error) {
	w.mu.Lock()
	if w.failed == nil {
		w.failed = err
	}
	w.mu.Unlock()
}

// writeRow appends a batch frame to batches.seg, without a flush. w.sealMu
// is held.
func (w *Writer) writeRow(rp rowPayload) error {
	payload, err := json.Marshal(rp)
	if err != nil {
		return err
	}
	fr, err := frameOf(kindBatch, payload)
	if err != nil {
		return err
	}
	return w.batches.write(fr)
}

// StoreReceipt keeps the server's receipt for batch b in batches.seg, with
// its received_at (ledger-v1 7; DESIGN 3.5). The uploader checks it under
// the server key before it calls StoreReceipt (WP-AG5). A receipt can't be
// made again from what the device holds, so it is flushed at once. So is
// everything before it, first: a frame before a receipt that isn't whole
// is then damage, which Open refuses, and never a crash's torn tail, which
// Open would cut off with the receipts after it (readBatchesFile).
func (w *Writer) StoreReceipt(b uint64, r ledger.Receipt) error {
	w.sealMu.Lock()
	defer w.sealMu.Unlock()
	w.mu.Lock()
	err, last, base := w.usable(), w.b, w.base
	w.mu.Unlock()
	if err != nil {
		return err
	}
	if b == 0 || b > last {
		return fmt.Errorf("ledger: batch %d isn't sealed; the last one is %d", b, last)
	}
	if b < base {
		// A sweep dropped the batch, and its receipt has no row to go with.
		return nil
	}
	payload, err := json.Marshal(receiptPayload{B: b, ReceivedAt: r.ReceivedAt, Signature: hex.EncodeToString(r.Signature[:])})
	if err != nil {
		return err
	}
	fr, err := frameOf(kindReceipt, payload)
	if err != nil {
		return err
	}
	if err := w.batches.sync(); err != nil {
		w.fail(err)
		return err
	}
	if err := w.batches.write(fr); err != nil {
		w.fail(err)
		return err
	}
	if err := w.batches.sync(); err != nil {
		w.fail(err)
		return err
	}
	return nil
}

// Status is what the writer reports about itself.
type Status struct {
	DeviceID string `json:"device_id"`
	// Next is the sequence number the next record gets, and Batches the
	// number of sealed batches.
	Next    uint64 `json:"next_seq"`
	Batches uint64 `json:"batches"`
	// Unsealed counts the records written since the last seal.
	Unsealed int `json:"unsealed"`
	// LateLocal counts the records Write dropped over the chain's life as
	// made in a local-only stretch that a batch sealed online had passed
	// (ErrLateLocal), as the seal frames recorded the stretches since.
	LateLocal uint64 `json:"late_local,omitempty"`
	// Failed says why the writer stopped writing, when it did.
	Failed string `json:"failed,omitempty"`
}

// Status returns the writer's state.
func (w *Writer) Status() Status {
	w.mu.Lock()
	defer w.mu.Unlock()
	s := Status{DeviceID: w.device, Next: w.next, Batches: w.b, Unsealed: len(w.pending), LateLocal: w.lateDropped()}
	if w.failed != nil {
		s.Failed = w.failed.Error()
	}
	return s
}

// loop seals whenever RequestSeal asks, until Close.
func (w *Writer) loop() {
	defer close(w.done)
	for {
		select {
		case <-w.stop:
			return
		case <-w.kick:
			_, _, err := w.Seal()
			w.report(err)
		}
	}
}

// report passes a failed seal no caller waited for to Options.Problem, and
// clears the problem once a seal succeeds.
func (w *Writer) report(err error) {
	if w.problem == nil || errors.Is(err, ErrClosed) {
		return
	}
	w.sealMu.Lock()
	defer w.sealMu.Unlock()
	switch {
	case err != nil:
		w.troubled = true
		w.problem(fmt.Sprintf("the ledger couldn't seal a batch: %v", err))
	case w.troubled:
		w.troubled = false
		w.problem("")
	}
}

// Close seals what is unsealed, stops the writer and closes its files. It
// returns the seal's error. Calls after the first return ErrClosed.
func (w *Writer) Close() error {
	first := false
	w.stopOnce.Do(func() {
		first = true
		if w.job != nil {
			w.job.Stop()
		}
		close(w.stop)
	})
	if !first {
		return ErrClosed
	}
	<-w.done
	w.sealMu.Lock()
	defer w.sealMu.Unlock()
	_, _, err := w.seal()
	w.mu.Lock()
	w.closed = true
	w.mu.Unlock()
	w.closeFiles()
	return err
}

// closeFiles closes every file the writer holds.
func (w *Writer) closeFiles() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.seg != nil {
		w.seg.close()
		w.seg = nil
	}
	closeAll(w.takeRetired())
	if w.batches != nil {
		w.batches.close()
		w.batches = nil
	}
}
