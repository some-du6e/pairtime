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
	// Problem gets a description of what went wrong when a seal that no
	// caller waits for fails, and "" when one succeeds after that.
	Problem func(msg string)
	// Now is the clock, time.Now when nil. Segments are named after its
	// UTC day.
	Now func() time.Time
}

// Batch is a sealed batch as the writer stores it: its row, when it was
// sealed, and its records.
type Batch struct {
	Row      ledger.BatchRow
	SealedAt time.Time
	Records  []ledger.Record
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
	// (ledger-v1 10.1). It must be valid UTF-8.
	Text []byte
	// Edits are an agent.tool's edit hunks, in the order the patch lists
	// them.
	Edits []features.Edit
	// Command is the command line an agent.tool or shell.command ran.
	Command string
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
	problem  func(string)
	device   string

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
	head    ledger.Hash   // its H_b, zero(32) before the first
	pending []pending     // the records since the last seal
	q       score.Questions
	failed  error
	closed  bool

	kick     chan struct{}
	stop     chan struct{}
	done     chan struct{}
	stopOnce sync.Once
	job      *sched.Job
}

// pending is a record written since the last seal, with where its frame
// starts.
type pending struct {
	rec ledger.Record
	pos position
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
		problem:  opts.Problem,
		next:     1,
		kick:     make(chan struct{}, 1),
		stop:     make(chan struct{}),
		done:     make(chan struct{}),
	}
	if err := os.MkdirAll(w.dir, 0o700); err != nil {
		return nil, err
	}
	if err := w.recover(); err != nil {
		w.closeFiles()
		return nil, err
	}
	// Records a crash left without a batch go in one now, so that once
	// Open returns everything on disk is sealed.
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
// sequence number. The error leaves the event's content out.
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
	if err := w.append(rec); err != nil {
		return 0, err
	}
	w.q.Observe(ev)
	if k := ev.Kind(); k == event.KindPromptSubmit || k == event.KindAgentStop {
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
	if len(w.pending) >= pendingMax {
		w.RequestSeal()
	}
	return nil
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
}

// rowPayload is a batch frame's payload in batches.seg: DESIGN 3.5's row,
// {b, first_seq, last_seq, n, R_b, H_b, sealed_at}, with prev and where
// the batch's frames are. The receipt goes in a receipt frame of its own,
// when it arrives.
type rowPayload struct {
	sealPayload
	LastSeq uint64 `json:"last_seq"`
	// Start is where the batch's first record frame starts, and End where
	// its seal frame ends.
	Start position `json:"start"`
	End   position `json:"end"`
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

// chainVersion is the layout of the writer's files, chainPayload.V.
const chainVersion = 1

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
	sp := sealPayload{B: row.B, FirstSeq: row.FirstSeq, N: row.N, Prev: w.head, Root: row.Root, Head: row.Head,
		SealedAt: at.UnixMilli()}
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
	w.pending = nil
	w.b, w.head = row.B, row.Head
	w.mu.Unlock()

	// The one flush of the seal. Records written since the seal frame go
	// to disk with it, which does no harm.
	err = seg.syncTo(end.Off)
	closeAll(retired)
	if err != nil {
		w.fail(err)
		return Batch{}, false, err
	}
	rp := rowPayload{sealPayload: sp, LastSeq: row.FirstSeq + row.N - 1, Start: start, End: end}
	if err := w.writeRow(rp); err != nil {
		// The batch is sealed and on disk, and the next Open writes its
		// row from the seal frame. Until then nothing more goes in
		// batches.seg out of order.
		w.fail(err)
		return Batch{}, false, err
	}
	batch := Batch{Row: row, SealedAt: time.UnixMilli(sp.SealedAt), Records: recs}
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
	err, last := w.usable(), w.b
	w.mu.Unlock()
	if err != nil {
		return err
	}
	if b == 0 || b > last {
		return fmt.Errorf("ledger: batch %d isn't sealed; the last one is %d", b, last)
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
	// Failed says why the writer stopped writing, when it did.
	Failed string `json:"failed,omitempty"`
}

// Status returns the writer's state.
func (w *Writer) Status() Status {
	w.mu.Lock()
	defer w.mu.Unlock()
	s := Status{DeviceID: w.device, Next: w.next, Batches: w.b, Unsealed: len(w.pending)}
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
