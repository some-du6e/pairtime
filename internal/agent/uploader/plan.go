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
	// answered.
	marks map[string]store.Mark
	// labels is the hash of the labels the request carries, "" when it
	// carries none.
	labels string
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
	// maxRoots and maxBuckets replace the spec's limits when set. Tests
	// use them to split an upload.
	maxRoots, maxBuckets int
}

// entry is one revision line a plan read from the bucket store: its day,
// the mark just past it, and the bucket it sends, nil for a line it only
// passes over.
type entry struct {
	day    string
	mark   store.Mark
	bucket *event.MinuteBucket
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
	entries, days, err := bucketsOf(in, nBuckets)
	if err != nil {
		return plan{}, err
	}
	for {
		p, err := assemble(in, roots, entries, days)
		if err != nil {
			return plan{}, err
		}
		if len(p.raw) <= maxRaw && len(p.js) <= maxJSON {
			return p, nil
		}
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
	for _, r := range in.rows.rowsFrom(max(in.st.Next, 1), n) {
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
func bucketsOf(in planInput, n int) ([]entry, map[string]bool, error) {
	files, err := store.BucketDays(in.bucketDir)
	if err != nil {
		return nil, nil, err
	}
	days := make(map[string]bool, len(files))
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
			w, ok := witnessOf(in.rows, b.At(), in.lag, in.st.ChainStart, in.st.Offline)
			if !ok {
				return false
			}
			mb := b.MinuteBucket(w).Shareable()
			out = append(out, entry{day: f.Day, mark: r.Mark, bucket: &mb})
			took++
			count++
			return count < n
		})
		if err != nil {
			return nil, nil, err
		}
		if restart && took == 0 {
			// The day was cut since its mark: read it from its start next
			// time, whatever its size is then.
			out = append(out, entry{day: f.Day})
		}
	}
	return out, days, nil
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
	p := plan{body: Body{Batches: roots, Labels: in.labels}, marks: maps.Clone(in.st.Marks)}
	if p.marks == nil {
		p.marks = map[string]store.Mark{}
	}
	maps.DeleteFunc(p.marks, func(day string, _ store.Mark) bool { return !days[day] })
	for _, e := range entries {
		if e.mark == (store.Mark{}) {
			delete(p.marks, e.day)
		} else {
			p.marks[e.day] = e.mark
		}
		if e.bucket != nil {
			p.body.Buckets = append(p.body.Buckets, *e.bucket)
		}
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
// named with a version in client_build's form, by name, at most
// harnessesMax, and an empty list, never null, when there are none.
func sortedHarnesses(seen map[event.Harness]string) []Harness {
	out := []Harness{}
	for _, name := range slices.Sorted(maps.Keys(seen)) {
		if v := seen[name]; knownHarness(name) && buildForm(v) && len(out) < harnessesMax {
			out = append(out, Harness{Name: name, Version: v})
		}
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
