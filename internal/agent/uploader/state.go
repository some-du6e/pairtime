// SPDX-License-Identifier: AGPL-3.0-or-later

package uploader

import (
	"cmp"
	"maps"
	"slices"
	"time"

	"github.com/some-du6e/pairtime/internal/agent/state"
	"github.com/some-du6e/pairtime/internal/agent/store"
	"github.com/some-du6e/pairtime/pkg/event"
)

// StateKey is the uploader's key in state.json: DESIGN 3.5's upload
// cursor, the request counter and what the labels and failure notes need.
const StateKey = "uploader"

// counterBlock is how many request counters the uploader reserves with
// one write of state.json. The counter has to keep rising across
// restarts (DESIGN 4.5), so a value is only used once a reservation that
// covers it is on disk, and the next start goes on above the reservation:
// one write per counterBlock requests rather than one per request, which
// suits a spinning disk. The server only asks for a higher counter each
// time, so the values a restart skips cost nothing.
const counterBlock = 64

// offlineSpans bounds the offline spans the uploader remembers. Each is
// one stretch of failed uploads; an older one only ever matters to minutes
// that were already labelled.
const offlineSpans = 256

// uncheckedMax bounds the receipts kept unchecked; past it the oldest go.
const uncheckedMax = 4096

// harnessesMax is how many harnesses the labels carry (ingest-v1 2.3.4).
const harnessesMax = 16

// gapsMax bounds the runs of local-only batches the uploader remembers;
// past it the two oldest become one, which counts the batches between them
// as local-only too, so nothing that was local-only goes.
const gapsMax = 1024

// saved is what the uploader keeps in state.json under StateKey.
// Everything in it can be rebuilt: from GET /api/v1/ingest/state, which
// gives the counter and the server's head, and from the ledger and the
// bucket store, which hold every item. A lost state.json sends the
// buckets again, which the server answers as duplicates.
type saved struct {
	// Chain is the device id of the ledger chain the cursor belongs to. A
	// new chain, which a new K_dev starts, starts the cursor afresh.
	Chain string `json:"chain"`
	// Server and Device are where the uploads go and the device id the
	// server knows: the counter and the head are theirs, so another server
	// or another sign-in starts them afresh.
	Server string `json:"server,omitempty"`
	Device string `json:"device,omitempty"`
	// Req is the counter reservation: the requests so far used values at
	// or below it, and the next one uses more.
	Req uint64 `json:"req"`
	// Next is the batch whose root goes next, from 1. Rows before it hold
	// receipts, or are before ChainStart.
	Next uint64 `json:"next,omitempty"`
	// From is where the device's chain goes on from under its sign-in: the
	// first batch sealed after the link that the recorded modes don't mark
	// local-only (firstLinked, settle). The batches before it that the
	// server doesn't hold were sealed in local-only mode, or before a
	// re-link: none of them goes, nor the buckets of the minutes they hold
	// (ingest-v1 5.3, 7.6; D-local-gap). 0 until the first load with this
	// sign-in sets it.
	From uint64 `json:"from,omitempty"`
	// LinkedAt is the sign-in's linked_at From was set for, in Unix ms: a
	// newer one is a re-link, after which From moves to the first batch
	// sealed online after it (D-local-gap).
	LinkedAt int64 `json:"linked_at,omitempty"`
	// Linking is set from a new link until the server holds a batch at or
	// after From. Until then the server takes any batch above its head, so
	// From moves past every run of batches sealed in local-only mode
	// meanwhile (settle), and a request carries one batch root at most
	// (rootsOf).
	Linking bool `json:"linking,omitempty"`
	// Gaps are the runs of batches sealed in local-only mode, merged
	// where they touch: before the first sign-in, between a logout and a
	// re-link (from the server's head then to the batch before From), and
	// while config.toml's mode was local (mode). Neither their roots nor
	// their minutes ever go (D-local-gap; DESIGN 4.2). They describe the
	// ledger, not a server, so they stay across a sign-in to another
	// server. The server takes a gap only at a re-link, so the roots stop
	// before a run sealed after the link's first stored batch until the
	// next link (localStop).
	Gaps []gap `json:"gaps,omitempty"`
	// LocalFrom is the first batch sealed while config.toml's mode was
	// local, while it still is: it and every batch after it count as
	// sealed in local-only mode, until a batch is sealed online again and
	// the run moves into Gaps (mode). 0 while the mode isn't local.
	LocalFrom uint64 `json:"local_from,omitempty"`
	// Modes is the newest batch whose mode Gaps and LocalFrom take in.
	// Each batch's seal frame records whether it was sealed online, on
	// disk with the seal, and its row says so (ledger.Row.Online). The
	// place takes the modes of the batches after Modes from their rows, in
	// order, before anything of theirs can be planned (foldModes). So a
	// crash loses no mode, and a state.json older than the ledger, or
	// lost, gets them back from it.
	Modes uint64 `json:"modes,omitempty"`
	// Head is the server's head, the newest batch of the chain it holds, as
	// the last state read and the answers since say, 0 while it holds none
	// or it isn't known yet.
	Head uint64 `json:"head,omitempty"`
	// ChainStart is the first batch of the device's witnessed chain on the
	// server, 0 while the server holds none or it isn't known yet. Rows
	// before it are never sent again: the server answers them
	// before_chain.
	ChainStart uint64 `json:"chain_start,omitempty"`
	// Gone is the highest batch the server answered before_chain for: it
	// holds no row for it any more, so no receipt comes for it or for the
	// batches before it, and their minutes read backfilled.
	Gone uint64 `json:"gone,omitempty"`
	// Mismatch is set once the server answered device_mismatch for Server
	// and Device: the device id is another account's, and nothing more
	// goes under it, not even a state read, until WP-AG8 makes a new device
	// (#150 R5). A sign-in to another server or device starts afresh.
	Mismatch bool `json:"mismatch,omitempty"`
	// Fork is the batch the server answered batch_conflict for: the
	// device's ledger and the server's witness rows disagree there, which
	// is a bug or tampering, and the uploader sends no batch root after it
	// (ingest-v1 5 step 3). 0 while there is none.
	Fork uint64 `json:"fork,omitempty"`
	// Marks says how far each day's bucket file was uploaded, by day.
	Marks map[string]store.Mark `json:"marks,omitempty"`
	// CommitMarks says how far each day's file of commits/ was decided, by
	// day: every line before a day's mark went, was withheld for good, or
	// was passed over as a sha that went before (commitsOf).
	CommitMarks map[string]store.Mark `json:"commit_marks,omitempty"`
	// Labels is the hash of the labels the server last took, and LabelsAt
	// when, in Unix ms.
	Labels   string `json:"labels,omitempty"`
	LabelsAt int64  `json:"labels_at,omitempty"`
	// Harnesses are the harnesses the ledger's records name, each with the
	// version it last named, for the labels.
	Harnesses map[event.Harness]string `json:"harnesses,omitempty"`
	// Offline are the stretches the uploader couldn't reach the server, as
	// its upload-failure notes say, oldest first.
	Offline []span `json:"offline,omitempty"`
	// Sent maps each minute still pending on the server, by its start in
	// Unix ms, to what its last revision's credit rests on (sentRev).
	Sent map[int64]sentRev `json:"sent,omitempty"`
	// Bump is what a minute's revs go up by on the way out, by its start in
	// Unix ms, for a minute the server holds a higher or another revision
	// of than the bucket store does, as after the store lost its last lines:
	// a minute's rev only rises (ingest-v1 5.5).
	Bump map[int64]uint32 `json:"bump,omitempty"`
	// Offset is the server's clock less the device's, in ms, as the last
	// answer measured: what X-Pairtime-Time is corrected by, and how far
	// ahead of the server's clock a minute may be before it waits.
	Offset int64 `json:"offset_ms,omitempty"`
	// Foreign is the newest batch whose batches.seg receipt may be another
	// server's, after a sign-in to this one: the receipts of the batches
	// from Next to it are forgotten whenever the rows are read, until this
	// server's own come in. 0 when there was no switch.
	Foreign uint64 `json:"foreign,omitempty"`
	// Unchecked are receipts the uploader couldn't check yet, because they
	// name a server key it doesn't have, by batch. They read the batches'
	// minutes, and go into batches.seg once their key checks them
	// (ingest-v1 5.4).
	Unchecked map[uint64]WireReceipt `json:"unchecked,omitempty"`
	// ServerKey is the receipt key GET /api/v1/ingest/state gave.
	ServerKey *ServerKey `json:"server_key,omitempty"`
	// Outbox is the request a retry sends again, when one is waiting.
	Outbox *outbox `json:"outbox,omitempty"`
}

// sentRev is what a minute's last revision sent rests on: a hash of its
// credit, and the batch whose reading it took. A later revision that
// changes no credit, as the flip from pending to final, keeps that batch's
// reading rather than take its own, whose batch may have come late for
// reasons that have nothing to do with the minute (DESIGN 7.4).
type sentRev struct {
	H string `json:"h"`
	B uint64 `json:"b"`
}

// gap is a run of batches, From to To, sealed in local-only mode.
type gap struct {
	From uint64 `json:"from"`
	To   uint64 `json:"to"`
}

// addGap records the batches from a to b as local-only, as one run with
// every run it overlaps or touches.
func (s *saved) addGap(a, b uint64) {
	if a == 0 || b < a {
		return
	}
	out := make([]gap, 0, len(s.Gaps)+1)
	for _, g := range s.Gaps {
		if g.To+1 < a || b+1 < g.From {
			out = append(out, g)
			continue
		}
		a, b = min(a, g.From), max(b, g.To)
	}
	out = append(out, gap{From: a, To: b})
	slices.SortFunc(out, func(x, y gap) int { return cmp.Compare(x.From, y.From) })
	for len(out) > gapsMax {
		out[1].From = out[0].From
		out = out[1:]
	}
	s.Gaps = out
}

// foldModes takes into the runs the modes of the batches rows holds that
// the place hasn't taken yet, from the next root's batch or the one after
// Modes, in batch order: a batch whose row doesn't say it was sealed
// online, as one an older pairtime version sealed, which recorded no mode
// (ledger.Row.NoMode), counts as sealed in local-only mode (DESIGN 4.2).
// The batches before the next root went or were passed over already. It
// reports whether a run changed. Modes moving alone needs no save: the
// runs then hold what the batches after the saved Modes say, so taking
// their modes again changes nothing.
func (s *saved) foldModes(rows *rowIndex) bool {
	changed := false
	for b := max(s.Modes+1, s.Next, rows.first); b <= rows.last(); b++ {
		r, ok := rows.row(b)
		if !ok {
			break
		}
		changed = s.mode(b, !r.Online, r.Back) || changed
	}
	s.Modes = max(s.Modes, rows.last())
	return changed
}

// localOnly reports whether batch b was sealed in local-only mode: in a
// recorded gap, at or after LocalFrom while the mode is local, or between
// the server's head and From, where the state read that records the gap
// hasn't run yet, as for `pairtime privacy show` before an agent's first
// upload. With no head known, Next stands for the batch after it, as in a
// state.json from before Head was kept.
func (s saved) localOnly(b uint64) bool {
	if s.LocalFrom > 0 && b >= s.LocalFrom {
		return true
	}
	for _, g := range s.Gaps {
		if g.From <= b && b <= g.To {
			return true
		}
	}
	base := s.Head + 1
	if s.Head == 0 {
		base = max(s.Next, 1)
	}
	return s.From > base && b >= base && b < s.From
}

// straddles reports whether the minute that starts at minute, in Unix ms,
// holds the end of a run of batches sealed in local-only mode: it starts
// before the stretch the run holds ended and ends after the run's last
// batch was sealed, as the minute pairtime login lands in does. The
// stretch ends where the batch after the run's last says config.toml was
// last known to say local-only (ledger.Row.LocalUntil), which comes after
// the run's last seal when nothing was pending at the switch back, and at
// it otherwise, though by its writer's clock no later than its own seal: a
// moment later than that is one an earlier run recorded ahead of a clock
// stepped back since, which the writer keeps for the records it judges by
// their t (ledger.Writer.earlier), and the minutes this run's clock reads
// before it are online ones. The first batch sealed after such a
// minute can be online, as one marked as the first after a noted switch
// back online is (mode), but part of the minute came in local-only mode,
// and a later revision's credit can reach back into it, as a first
// prompt's lead does (SM 2.3). So it never goes, though the marked batch's
// root and the minutes after it do. The cost is at most the switch's
// minute's credit, when the first prompt after the switch falls in it:
// the minutes before it came in local-only mode.
func (s saved) straddles(rows *rowIndex, minute int64) bool {
	for _, g := range s.Gaps {
		t, ok := rows.sealedAt(g.To)
		if !ok {
			continue
		}
		end := t
		if lu, ok := rows.localUntil(g.To + 1); ok {
			if sa, ok := rows.sealedAt(g.To + 1); ok {
				lu = min(lu, sa)
			}
			end = max(end, lu)
		}
		if minute < end && t < minute+60_000 {
			return true
		}
	}
	return false
}

// endsRun reports whether the moment m, in Unix ms, falls where a run of
// batches sealed in local-only mode ended: after the run's last seal, and at
// or before the end of the stretch it holds, the batch after it's
// LocalUntil, by that batch's writer's clock no later than its own seal.
// That is straddles' test for one moment rather than a minute
// (D-ag5-D1). A commit observed then was seen while config.toml may still
// have said local-only, though the first batch sealed after it is online.
// A run whose next batch the rows don't hold yet ends nowhere yet: a moment
// after its last seal has no batch sealed after it then.
func (s saved) endsRun(rows *rowIndex, m int64) bool {
	for _, g := range s.Gaps {
		t, ok := rows.sealedAt(g.To)
		if !ok {
			continue
		}
		end, ok := rows.localUntil(g.To + 1)
		if !ok {
			continue
		}
		if sa, ok := rows.sealedAt(g.To + 1); ok {
			end = min(end, sa)
		}
		if t < m && m <= end {
			return true
		}
	}
	return false
}

// mode records that batch b was sealed while config.toml's mode was local
// (local) or online. The first batch sealed in local mode opens a run at
// LocalFrom, and the first one sealed online after it closes the run into
// Gaps, itself included: its records may have begun while the mode was
// still local, as the first one's began before it was. A batch whose
// records all came after the agent's config watch noted the switch back
// online (back, ledger.Row.Back) closes the run before itself instead, so
// the first batch after a pairtime login can go, and the minutes after the
// one the switch lands in (straddles). A batch sealed in local
// mode before the open run, as an older writer's row below a run an older
// place recorded, takes the run down to it, and one sealed online before
// it leaves the run as it is. It reports whether anything changed.
func (s *saved) mode(b uint64, local, back bool) bool {
	switch {
	case b == 0:
		return false
	case local && (s.LocalFrom == 0 || b < s.LocalFrom):
		s.LocalFrom = b
		return true
	case !local && s.LocalFrom > 0 && b >= s.LocalFrom:
		to := b
		if back {
			to = b - 1
		}
		s.addGap(s.LocalFrom, to)
		s.LocalFrom = 0
		return true
	}
	return false
}

// settle moves From past the runs of batches the recorded modes mark
// local-only, while the link's first batch isn't stored (Linking): until
// then the server takes any batch above its head, so a run sealed in
// local-only mode after the link, as while config.toml still said local
// or before its change reached the agent, joins the link's gap rather
// than stopping the roots (D-local-gap). An open run takes in every batch
// from LocalFrom on, so From is then the next batch to be sealed, last + 1,
// until one is sealed online and closes the run (mode).
func (s *saved) settle(last uint64) {
	if !s.Linking || s.From == 0 {
		return
	}
	for {
		if s.LocalFrom > 0 && s.From >= s.LocalFrom {
			s.From = max(s.From, last+1)
			return
		}
		moved := false
		for _, g := range s.Gaps {
			if g.From <= s.From && s.From <= g.To {
				s.From, moved = g.To+1, true
			}
		}
		if !moved {
			return
		}
	}
}

// localStop returns the first batch, up to last, that the next batch roots
// can't pass: the first one sealed in local-only mode at or after where
// they go on from. The server takes no gap without a re-link, and none of
// those batches may go, so nothing after it goes until `pairtime logout`
// and `pairtime login` link the device again (D-local-gap). 0 when there
// is none.
func (s saved) localStop(last uint64) uint64 {
	start := max(s.Next, s.From, 1)
	stop := uint64(0)
	consider := func(b uint64) {
		if b <= last && (stop == 0 || b < stop) {
			stop = b
		}
	}
	if s.LocalFrom > 0 {
		consider(max(s.LocalFrom, start))
	}
	for _, g := range s.Gaps {
		if g.To >= start {
			consider(max(g.From, start))
		}
	}
	return stop
}

// span is a stretch of failed uploads: the first and the last failure's
// upload-failure note, in Unix ms.
type span struct {
	From int64 `json:"from"`
	To   int64 `json:"to"`
}

// outbox is a request that failed and is waiting for its retry: a retry
// sends the same body with the same Idempotency-Key (ingest-v1 2.10), so
// the body waits in outbox.json beside state.json, and this says what its
// answer moves.
type outbox struct {
	Key string `json:"key"`
	// Hash is the SHA-256 of outbox.json's JSON, in hex, so a damaged file
	// is noticed and a new request built instead.
	Hash string `json:"hash"`
	// Marks are the bucket marks once its buckets are answered, Sent what
	// their credit rests on, CommitMarks the commit marks once its commit
	// records are, and Labels the labels' hash when it carries them.
	Marks       map[string]store.Mark `json:"marks,omitempty"`
	Sent        map[int64]sentRev     `json:"sent,omitempty"`
	CommitMarks map[string]store.Mark `json:"commit_marks,omitempty"`
	Labels      string                `json:"labels,omitempty"`
}

// clone returns a copy of s that shares nothing with it.
func (s saved) clone() saved {
	s.Marks = maps.Clone(s.Marks)
	s.CommitMarks = maps.Clone(s.CommitMarks)
	s.Harnesses = maps.Clone(s.Harnesses)
	s.Offline = slices.Clone(s.Offline)
	s.Gaps = slices.Clone(s.Gaps)
	s.Sent = maps.Clone(s.Sent)
	s.Bump = maps.Clone(s.Bump)
	s.Unchecked = maps.Clone(s.Unchecked)
	if s.ServerKey != nil {
		k := *s.ServerKey
		s.ServerKey = &k
	}
	if s.Outbox != nil {
		o := *s.Outbox
		o.Marks = maps.Clone(o.Marks)
		o.Sent = maps.Clone(o.Sent)
		o.CommitMarks = maps.Clone(o.CommitMarks)
		s.Outbox = &o
	}
	return s
}

// spanGap is the longest wait between two failures of one outage: the
// longest Retry-After the uploader keeps, and a wake. A longer gap, as
// while the machine slept or the agent was stopped, ends the span: the
// uploader wasn't trying then.
const spanGap = int64((retryAfterMax + Every) / time.Millisecond)

// noteFailure adds a failure at t, in Unix ms, after which the uploader
// waits until until to try again, to the offline spans: the server was out
// of reach for all of it, as when it sent a Retry-After. It stretches the
// last span when the failure follows it with no success in between (open)
// and within spanGap, and starts a new one otherwise.
func (s *saved) noteFailure(t, until int64, open bool) {
	until = max(until, t)
	if n := len(s.Offline); open && n > 0 && t >= s.Offline[n-1].From && t-s.Offline[n-1].To <= spanGap {
		s.Offline[n-1].To = max(s.Offline[n-1].To, until)
		return
	}
	s.Offline = append(s.Offline, span{From: t, To: until})
	if len(s.Offline) > offlineSpans {
		s.Offline = slices.Clone(s.Offline[len(s.Offline)-offlineSpans:])
	}
}

// waitedOut reports whether a batch sealed at sealed, on the device's
// clock in Unix ms, was due to go while the uploader couldn't reach the
// server: a span of failures, or the wait after one, falls in the batch's
// witness window, the lag after its seal. Then its late receipt reads
// offline whatever the flush delay, as the teen who syncs at school the
// next morning gets (DESIGN 6.2 D6, 7.4).
func waitedOut(spans []span, sealed, lag int64) bool {
	for _, s := range spans {
		if s.From <= sealed+lag && s.To >= sealed {
			return true
		}
	}
	return false
}

// prepare puts what st says of the device's receipts into rows: it forgets
// the receipts that may be another server's (Foreign) and reads the
// unchecked ones. The uploader and `pairtime privacy show` both call it
// after reading the rows, so they read the same minutes.
func (st saved) prepare(rows *rowIndex) {
	if st.Foreign > 0 {
		for b := max(st.Next, rows.start()); b <= min(st.Foreign, rows.last()); b++ {
			rows.forgetReceipt(b)
		}
	}
	for b, wr := range st.Unchecked {
		rows.receivedUnchecked(b, int64(wr.ReceivedAt))
	}
}

// HeldFrom returns the first batch whose row the ledger in ledgerDir keeps
// for the uploads while this computer is linked, whatever its age, by the
// uploader's place in st (Writer.Sweep's floor;
// docs/errata/2026-11-23-unsent-batches-retention.md): the batch the roots
// go on from, as the place and the rows' modes say. A sweep keeps the rows
// from it on, so the server can still link the next root to its head.
// While the link's first batch isn't stored (Linking), the server takes
// any batch above its head, so the runs of batches sealed in local-only
// mode there, which never go, are passed over (settle). It is 0, nothing to
// keep, once no root of these rows can be stored: after a fork or the
// server's device_mismatch, which end the chain, and when the roots stop
// at a batch sealed in local-only mode while not linking (localStop), as
// they do until a re-link, after which they go on from the first batch
// sealed then (relink). A place that doesn't read, or is another chain's,
// counts as a new link's, as load takes it: what the server holds isn't
// known, so every row from the first one the modes don't pass over stays.
func HeldFrom(st *state.Store, ledgerDir string) (uint64, error) {
	rows, err := loadRows(ledgerDir, 0)
	if err != nil {
		return 1, err
	}
	var s saved
	if ok, err := st.Get(StateKey, &s); err != nil || !ok || s.Chain != rows.device {
		s = saved{Chain: rows.device, Linking: true}
	}
	if s.Fork != 0 || s.Mismatch {
		return 0, nil
	}
	// The modes of the batches sealed since the place last took one, as
	// load takes them in before anything of theirs is planned.
	s.foldModes(rows)
	b := max(s.Next, s.From, rows.start())
	for {
		if s.LocalFrom > 0 && b >= s.LocalFrom {
			return 0, nil
		}
		in := false
		for _, g := range s.Gaps {
			if g.From <= b && b <= g.To {
				b, in = g.To+1, true
			}
		}
		switch {
		case !in:
			return b, nil
		case !s.Linking:
			return 0, nil
		}
	}
}
