// SPDX-License-Identifier: AGPL-3.0-or-later

package uploader

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"maps"
	"slices"

	"github.com/some-du6e/pairtime/internal/agent/store"
	"github.com/some-du6e/pairtime/pkg/event"
)

// The limits of one request (ingest-v1 2.4). A larger upload goes in
// several requests.
const (
	maxRaw     = 262_144   // bytes of gzip on the wire
	maxJSON    = 4_194_304 // bytes once inflated
	maxRoots   = 2_000
	maxBuckets = 2_000
)

// plan is one request's content, and what its answer moves.
type plan struct {
	body Body
	js   []byte // encodeBody(body): what `pairtime privacy show` prints
	raw  []byte // js in gzip: the raw body, what goes on the wire
	hash string // hex(SHA-256(raw))
	// marks are the bucket store's marks once the request's buckets are
	// answered, and sent what the pending minutes' credit rests on then.
	marks map[string]store.Mark
	sent  map[int64]sentRev
	// labels is the hash of the labels the request carries, "" when it
	// carries none.
	labels string
	// more is set when items wait behind the request's, past its limits.
	more bool
	// bumps are the rev bumps the request's buckets went out with, by
	// minute.
	bumps map[int64]uint32
	// unread lists the days whose bucket files couldn't be read: their
	// lines wait, and the rest go.
	unread []string
}

// planInput is everything a plan is made from. The agent's uploader and
// `pairtime privacy show` make it from the same files, the ledger, the
// bucket store and state.json, so they make the same plan.
type planInput struct {
	rows      *rowIndex
	st        saved
	bucketDir string
	// lag is WITNESS_LAG_S, in ms.
	lag int64
	// labels go when set.
	labels *Labels
	// notAfter holds back the buckets of minutes that start after it, in
	// Unix ms: the server refuses a minute that starts more than 300 s
	// after its clock (future_minute), as a device whose clock runs fast
	// would send. 0 holds back nothing.
	notAfter int64
	// maxRoots and maxBuckets replace the spec's limits when set. Tests
	// use them to split an upload.
	maxRoots, maxBuckets int
}

// entry is one revision line a plan read from the bucket store: its day,
// the mark just past it, and the bucket it sends, nil for a line it only
// passes over, with its minute, what its credit rests on, and whether the
// minute is still pending.
type entry struct {
	day     string
	mark    store.Mark
	bucket  *event.MinuteBucket
	minute  int64
	rev     sentRev
	pending bool
}

// sentKeep is how long before the newest pending minute an entry of
// saved.Sent stays. A minute is pending for 35 minutes; an entry this old
// belongs to a minute whose final revision a stopped scorer never wrote.
const sentKeep = 48 * 60 * 60 * 1000

// creditHash is a hash of what a revision credits and leaves out, which
// tells a revision that changes a minute's credit from one that only
// changes its state.
func creditHash(b store.Bucket) string {
	m := b.MinuteBucket(event.Witnessed)
	js, _ := json.Marshal(struct {
		Slots         []event.BucketSlot       `json:"slots"`
		CreditedS     float64                  `json:"credited_s"`
		Uncredited    map[event.Reason]float64 `json:"uncredited"`
		AgentRunningS float64                  `json:"agent_running_s"`
		AgentOnly     map[string]float64       `json:"agent_only"`
	}{m.Slots, m.CreditedS, m.Uncredited, m.AgentRunningS, m.AgentOnly})
	sum := sha256.Sum256(js)
	return hex.EncodeToString(sum[:8])
}

// makePlan makes the next request: the batch roots from st.Next on, unless
// a fork stopped them, then every bucket revision after the marks whose
// witness reading is known, and the labels when they go. It reads the
// bucket store and never writes to it. A request over ingest-v1's limits
// carries fewer items, and the rest go in the next one.
func makePlan(in planInput) (plan, error) {
	nRoots, nBuckets := in.maxRoots, in.maxBuckets
	if nRoots <= 0 {
		nRoots = maxRoots
	}
	if nBuckets <= 0 {
		nBuckets = maxBuckets
	}
	roots := rootsOf(in, nRoots)
	entries, days, unread, err := bucketsOf(in, nBuckets)
	if err != nil {
		return plan{}, err
	}
	more := len(roots) == nRoots || bucketsIn(entries) == nBuckets
	for {
		p, err := assemble(in, roots, entries, days)
		if err != nil {
			return plan{}, err
		}
		p.unread = unread
		if len(p.raw) <= maxRaw && len(p.js) <= maxJSON {
			p.more = more
			return p, nil
		}
		more = true
		// Too big: send half of the longer list now, the rest next.
		nb := bucketsIn(entries)
		switch {
		case nb > 1 && nb >= len(roots):
			entries = firstBuckets(entries, nb/2)
		case len(roots) > 1:
			roots = roots[:len(roots)/2]
		default:
			return plan{}, errors.New("uploader: one item is too large for a request")
		}
	}
}

// rootsOf returns the batch roots that go next: up to n from st.Next on,
// and none after a fork.
func rootsOf(in planInput, n int) []Root {
	if in.st.Fork != 0 {
		return nil
	}
	var roots []Root
	for _, r := range in.rows.rowsFrom(max(in.st.Next, in.st.From, 1), n) {
		roots = append(roots, Root{B: r.B, N: r.N, Root: r.Root, Head: r.Head, Prev: r.Prev})
	}
	return roots
}

// bucketsOf reads the bucket store's revisions after the marks, day by
// day, oldest first, and returns the entries of up to n buckets with the
// days the store has. A day is read until a revision whose witness reading
// isn't known yet, which waits with every line after it, so marks only
// ever move past lines that went. A revision of another chain's device
// isn't this device's to send: its line is passed over.
func bucketsOf(in planInput, n int) ([]entry, map[string]bool, []string, error) {
	files, err := store.BucketDays(in.bucketDir)
	if err != nil {
		// No bucket goes, and no mark moves: the roots go without them.
		return nil, nil, []string{"any day"}, nil
	}
	var unread []string
	// sent is what the credit of each pending minute rests on, as the
	// lines read so far leave it.
	sent := maps.Clone(in.st.Sent)
	if sent == nil {
		sent = map[int64]sentRev{}
	}
	days := make(map[string]bool, len(files))
	// Batches before the start never get a receipt: the server's chain
	// starts after them, or it no longer holds them.
	rd := reading{lag: in.lag, chainStart: in.st.ChainStart, fork: in.st.Fork, offset: in.st.Offset, offline: in.st.Offline}
	if g := in.st.Gone; g > 0 && g+1 > rd.chainStart {
		rd.chainStart = g + 1
	}
	var out []entry
	count := 0
	for _, f := range files {
		days[f.Day] = true
		if count >= n {
			continue
		}
		m := in.st.Marks[f.Day]
		if f.Size == m.Off {
			continue // nothing new
		}
		took := 0
		restart, err := store.TailDay(in.bucketDir, f.Day, m, func(r store.Revision) bool {
			b := r.Bucket
			if b.Device != in.rows.device {
				out = append(out, entry{day: f.Day, mark: r.Mark})
				took++
				return true
			}
			ms := b.At()
			if bs, ok := in.rows.sealedAfter(ms + 60_000); ok && bs < in.st.From {
				// Its records were sealed in local-only mode: it never goes
				// (ingest-v1 5.3).
				out = append(out, entry{day: f.Day, mark: r.Mark})
				took++
				return true
			}
			if in.notAfter != 0 && ms > in.notAfter {
				return false // it waits until the server's clock reaches it
			}
			// The credit rests on the batch that last changed it: a
			// revision that only changes the minute's state keeps it.
			rev := sentRev{H: creditHash(b), B: b.Batch}
			if prev, ok := sent[ms]; ok && prev.H == rev.H {
				rev.B = prev.B
			}
			w, ok := witnessOf(in.rows, ms, rev.B, rd)
			if !ok {
				return false
			}
			pending := b.State == event.StatePending
			if pending {
				sent[ms] = rev
			} else {
				delete(sent, ms)
			}
			mb := b.MinuteBucket(w).Shareable()
			mb.Rev += in.st.Bump[ms]
			out = append(out, entry{day: f.Day, mark: r.Mark, bucket: &mb, minute: ms, rev: rev, pending: pending})
			took++
			count++
			return count < n
		})
		if err != nil {
			// The day waits with its mark, and the others go.
			unread = append(unread, f.Day)
			continue
		}
		if restart && took == 0 {
			// The day was cut since its mark: read it from its start next
			// time, whatever its size is then.
			out = append(out, entry{day: f.Day})
		}
	}
	return out, days, unread, nil
}

// bucketsIn counts the entries that send a bucket.
func bucketsIn(entries []entry) int {
	n := 0
	for _, e := range entries {
		if e.bucket != nil {
			n++
		}
	}
	return n
}

// firstBuckets returns the entries up to and including the one that sends
// the k-th bucket.
func firstBuckets(entries []entry, k int) []entry {
	n := 0
	for i, e := range entries {
		if e.bucket == nil {
			continue
		}
		if n++; n == k {
			return entries[:i+1]
		}
	}
	return entries
}

// assemble builds the request of roots, the buckets of entries and the
// labels, and the marks its answer leaves. Marks of days the store no
// longer has go.
func assemble(in planInput, roots []Root, entries []entry, days map[string]bool) (plan, error) {
	p := plan{body: Body{Batches: roots, Labels: in.labels}, marks: maps.Clone(in.st.Marks), sent: maps.Clone(in.st.Sent)}
	if p.marks == nil {
		p.marks = map[string]store.Mark{}
	}
	if p.sent == nil {
		p.sent = map[int64]sentRev{}
	}
	if days != nil {
		maps.DeleteFunc(p.marks, func(day string, _ store.Mark) bool { return !days[day] })
	}
	for _, e := range entries {
		if e.mark == (store.Mark{}) {
			delete(p.marks, e.day)
		} else {
			p.marks[e.day] = e.mark
		}
		if e.bucket == nil {
			continue
		}
		p.body.Buckets = append(p.body.Buckets, *e.bucket)
		if bump := in.st.Bump[e.minute]; bump > 0 {
			if p.bumps == nil {
				p.bumps = map[int64]uint32{}
			}
			p.bumps[e.minute] = bump
		}
		if e.pending {
			p.sent[e.minute] = e.rev
		} else {
			delete(p.sent, e.minute)
		}
	}
	if len(p.sent) > 0 {
		newest := slices.Max(slices.Collect(maps.Keys(p.sent)))
		maps.DeleteFunc(p.sent, func(ms int64, _ sentRev) bool { return ms < newest-sentKeep })
	}
	if in.labels != nil {
		h, err := labelsHash(*in.labels)
		if err != nil {
			return plan{}, err
		}
		p.labels = h
	}
	js, err := encodeBody(p.body)
	if err != nil {
		return plan{}, err
	}
	raw, err := compress(js)
	if err != nil {
		return plan{}, err
	}
	p.js, p.raw, p.hash = js, raw, bodyHash(raw)
	return p, nil
}

// bump returns the rev bump the request's bucket of the minute starting at
// ms went out with.
func (p plan) bump(ms int64) uint32 { return p.bumps[ms] }

// labelsHash returns the hex SHA-256 of the labels' JSON, which tells
// whether they changed since the server last took them.
func labelsHash(l Labels) (string, error) {
	js, err := json.Marshal(l)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(js)
	return hex.EncodeToString(sum[:]), nil
}

// sameMarks reports whether two sets of marks are the same.
func sameMarks(a, b map[string]store.Mark) bool {
	return maps.Equal(a, b)
}

// sortedHarnesses returns the labels' harnesses: every harness the ledger
// named, once and by name, with the version it last named when that is in
// client_build's form and with none otherwise, at most harnessesMax, and
// an empty list, never null, when there are none (ingest-v1 2.3.4).
func sortedHarnesses(seen map[event.Harness]string) []Harness {
	out := []Harness{}
	for _, name := range slices.Sorted(maps.Keys(seen)) {
		if !knownHarness(name) || len(out) >= harnessesMax {
			continue
		}
		h := Harness{Name: name}
		if v := seen[name]; buildForm(v) {
			h.Version = v
		}
		out = append(out, h)
	}
	return out
}

// knownHarness reports whether h may be named in the labels: one of
// pkg/event's harnesses other than none (ingest-v1 2.3.4).
func knownHarness(h event.Harness) bool {
	switch h {
	case event.HarnessClaudeCode, event.HarnessCursor, event.HarnessCopilot, event.HarnessCodex,
		event.HarnessGemini, event.HarnessDevin, event.HarnessCline, event.HarnessOpenCode, event.HarnessOther:
		return true
	}
	return false
}

// buildForm reports whether s has client_build's form: 1 to 64 of A-Z a-z
// 0-9 . _ + -, starting with a letter or a digit (ingest-v1 2.3.4).
func buildForm(s string) bool {
	if len(s) == 0 || len(s) > 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		alnum := 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9'
		if !alnum && (i == 0 || c != '.' && c != '_' && c != '+' && c != '-') {
			return false
		}
	}
	return true
}
