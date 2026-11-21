// SPDX-License-Identifier: AGPL-3.0-or-later

package uploader

import (
	"bytes"
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/agent/store"
	"github.com/some-du6e/pairtime/pkg/event"
	"github.com/some-du6e/pairtime/pkg/ledger"
	"github.com/some-du6e/pairtime/pkg/params"
	"github.com/some-du6e/pairtime/pkg/score"
)

// planFixture is a chain of batches sealed at minutes 1, 2 and 3 of day0,
// the first two received at once, and a bucket store with minutes 0, 1
// and 2 of day0, minute 0 twice.
type planFixture struct {
	rows *rowIndex
	dir  string
}

func newPlanFixture(t *testing.T) planFixture {
	t.Helper()
	ix := indexOf(t, []float64{1, 2, 3}, []float64{1.1, 2.1, -1})
	var prev ledger.Hash
	for b := uint64(1); b <= 3; b++ {
		r, _ := ix.row(b)
		r.Root[0], r.Head[0], r.Prev = byte(b), byte(b), prev
		prev = r.Head
		ix.queue[b-1] = r
	}
	dir := t.TempDir()
	bs, err := store.OpenBuckets(dir)
	if err != nil {
		t.Fatal(err)
	}
	table := newestTable()
	puts := []store.Bucket{
		testBucket(table, "d_test", day0, 15, event.StatePending),
		testBucket(table, "d_other", day0.Add(time.Minute), 30, event.StateFinal),
		testBucket(table, "d_test", day0, 30, event.StateFinal),
		testBucket(table, "d_test", day0.Add(2*time.Minute), 30, event.StateFinal),
	}
	for _, b := range puts {
		if _, _, err := bs.Put(b); err != nil {
			t.Fatal(err)
		}
	}
	if err := bs.Close(); err != nil {
		t.Fatal(err)
	}
	return planFixture{rows: ix, dir: dir}
}

func TestPlan(t *testing.T) {
	day := day0.Format(time.DateOnly)
	tests := []struct {
		name string
		st   saved
		edit func(in *planInput)
		test func(t *testing.T, p plan, in planInput)
	}{
		{"roots go from Next on", saved{Next: 2}, nil, func(t *testing.T, p plan, in planInput) {
			if len(p.body.Batches) != 2 || p.body.Batches[0].B != 2 || p.body.Batches[1].Prev != p.body.Batches[0].Head {
				t.Errorf("roots %+v", p.body.Batches)
			}
		}},
		{"while the link's first batch isn't stored, its root goes alone", saved{Next: 2, From: 2, Linking: true}, nil, func(t *testing.T, p plan, in planInput) {
			if len(p.body.Batches) != 1 || p.body.Batches[0].B != 2 {
				t.Errorf("roots %+v", p.body.Batches)
			}
		}},
		{"no root goes after a fork", saved{Next: 2, Fork: 2}, nil, func(t *testing.T, p plan, in planInput) {
			if len(p.body.Batches) != 0 {
				t.Errorf("%d roots after a fork", len(p.body.Batches))
			}
		}},
		{"buckets go once the batch after their minute has a receipt, and the minute after waits", saved{Next: 4}, nil, func(t *testing.T, p plan, in planInput) {
			// Minutes 0 and 1 rest on batches 1 and 2, which have receipts;
			// minute 2 rests on batch 3, which has none.
			if len(p.body.Buckets) != 2 {
				t.Fatalf("%d buckets", len(p.body.Buckets))
			}
			if p.body.Buckets[0].Rev != 0 || p.body.Buckets[1].Rev != 1 || !p.body.Buckets[1].Minute.Equal(day0) {
				t.Errorf("revisions %d and %d of %v", p.body.Buckets[0].Rev, p.body.Buckets[1].Rev, p.body.Buckets[1].Minute)
			}
			if p.marks[day].Off == 0 {
				t.Error("the day's mark didn't move")
			}
		}},
		{"a bucket of another chain's device is passed over, and its line too", saved{Next: 4}, nil, func(t *testing.T, p plan, in planInput) {
			for _, b := range p.body.Buckets {
				if b.Device != "d_test" {
					t.Errorf("a bucket of %s went", b.Device)
				}
			}
		}},
		{"buckets go without their anchors, which stay on the device", saved{Next: 4}, nil, func(t *testing.T, p plan, in planInput) {
			if bytes.Contains(p.js, []byte(`"anchors"`)) {
				t.Error("the body names anchors")
			}
		}},
		{"lines after the marks are all that go", saved{Next: 4}, func(in *planInput) {
			first, err := makePlan(*in)
			if err != nil {
				t.Fatal(err)
			}
			in.st.Marks = first.marks
		}, func(t *testing.T, p plan, in planInput) {
			if len(p.body.Buckets) != 0 {
				t.Errorf("%d buckets went twice", len(p.body.Buckets))
			}
		}},
		{"a plan over the bucket limit carries the first ones and marks after the last", saved{Next: 4}, func(in *planInput) {
			in.maxBuckets = 1
		}, func(t *testing.T, p plan, in planInput) {
			if len(p.body.Buckets) != 1 || p.body.Buckets[0].Rev != 0 {
				t.Fatalf("%d buckets", len(p.body.Buckets))
			}
			in.maxBuckets = 0
			all, err := makePlan(in)
			if err != nil {
				t.Fatal(err)
			}
			if p.marks[day].Off >= all.marks[day].Off {
				t.Error("the split plan's mark isn't before the whole plan's")
			}
		}},
		{"labels go when set, with an empty harness list rather than null", saved{Next: 4}, func(in *planInput) {
			l := currentLabels("v0.1.0", newestTable(), score.Device{}, nil)
			in.labels = &l
		}, func(t *testing.T, p plan, in planInput) {
			if p.labels == "" || !bytes.Contains(p.js, []byte(`"harnesses":[]`)) {
				t.Errorf("labels %q in %s", p.labels, p.js)
			}
		}},
		{"an empty plan sends nothing", saved{Next: 4, Fork: 9}, func(in *planInput) {
			in.st.Marks = map[string]store.Mark{}
			first, _ := makePlan(*in)
			in.st.Marks = first.marks
		}, func(t *testing.T, p plan, in planInput) {
			if !p.body.Empty() {
				t.Errorf("body %s", p.js)
			}
		}},
		{"a minute that starts after notAfter waits, and the lines after it with it", saved{Next: 4}, func(in *planInput) {
			in.notAfter = at(0) - 1
		}, func(t *testing.T, p plan, in planInput) {
			if len(p.body.Buckets) != 0 || p.marks[day].Off != 0 {
				t.Errorf("%d buckets went, mark %+v", len(p.body.Buckets), p.marks[day])
			}
		}},
		{"a mark of a day the store no longer has goes", saved{Next: 4, Marks: map[string]store.Mark{"2020-01-01": {Off: 10, Line: "x"}}}, nil, func(t *testing.T, p plan, in planInput) {
			if _, ok := p.marks["2020-01-01"]; ok {
				t.Error("the mark stayed")
			}
		}},
		{"the body has only ingest-v1's members", saved{Next: 1}, nil, func(t *testing.T, p plan, in planInput) {
			var top map[string]json.RawMessage
			if err := json.Unmarshal(p.js, &top); err != nil {
				t.Fatal(err)
			}
			for k := range top {
				if k != "batches" && k != "buckets" && k != "commits" && k != "labels" {
					t.Errorf("member %q", k)
				}
			}
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newPlanFixture(t)
			in := planInput{rows: f.rows, st: tc.st, bucketDir: f.dir, lag: 900_000}
			if tc.edit != nil {
				tc.edit(&in)
			}
			p, err := makePlan(in)
			if err != nil {
				t.Fatal(err)
			}
			tc.test(t, p, in)
		})
	}
}

func TestHarnesses(t *testing.T) {
	tests := []struct {
		name string
		seen map[event.Harness]string
		want []Harness
	}{
		{"none is an empty list", nil, []Harness{}},
		{"harnesses go by name", map[event.Harness]string{event.HarnessCodex: "0.9.1", event.HarnessClaudeCode: "2.1.327"},
			[]Harness{{event.HarnessClaudeCode, "2.1.327"}, {event.HarnessCodex, "0.9.1"}}},
		{"a harness whose version isn't a version goes without it", map[event.Harness]string{event.HarnessCursor: "1.2 (beta)"},
			[]Harness{{Name: event.HarnessCursor}}},
		{"a harness that named no version goes without one", map[event.Harness]string{event.HarnessOther: ""}, []Harness{{Name: event.HarnessOther}}},
		{"none, the OS's harness, stays out", map[event.Harness]string{event.HarnessNone: "1"}, []Harness{}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := sortedHarnesses(tc.seen)
			if got == nil || len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("got %v, want %v", got, tc.want)
				}
			}
		})
	}
	builds := []struct {
		name string
		s    string
		ok   bool
	}{
		{"a release version is a version", "v0.1.0", true},
		{"a harness's dotted version is a version", "2.1.327", true},
		{"a pre-release suffix is allowed", "v0.1.0-alpha.1", true},
		{"a build suffix is allowed", "0.1.0+3dbf6d4", true},
		{"four numeric parts are allowed", "1.2.3.4", true},
		{"a fifth numeric part isn't", "1.2.3.4.5", false},
		{"dev is a word, not a version", "dev", false},
		{"a file name is not a version", "notes.txt", false},
		{"a version has no spaces", "1.0 beta", false},
		{"a suffix starts with a letter or digit", "1.0-.x", false},
		{"a dot ends no part", "1.", false},
		{"a version has at most 64 characters", "1.0+" + string(bytes.Repeat([]byte("a"), 61)), false},
		{"an empty version isn't one", "", false},
	}
	for _, tc := range builds {
		t.Run(tc.name, func(t *testing.T) {
			if got := versionForm(tc.s); got != tc.ok {
				t.Errorf("versionForm(%q) = %v", tc.s, got)
			}
		})
	}
	clients := []struct {
		name, build, want string
	}{
		{"a release sends its version", "v0.1.0", "v0.1.0"},
		{"a build from a checkout sends v0.0.0-dev, as ingest-v1 2.3.4 has it", "dev", "v0.0.0-dev"},
	}
	for _, tc := range clients {
		t.Run(tc.name, func(t *testing.T) {
			if got := clientBuild(tc.build); got != tc.want || !versionForm(got) {
				t.Errorf("clientBuild(%q) = %q, want %q", tc.build, got, tc.want)
			}
		})
	}
}

func TestLabelsDue(t *testing.T) {
	l := currentLabels("v0.1.0", newestTable(), score.Device{}, nil)
	h, err := labelsHash(l)
	if err != nil {
		t.Fatal(err)
	}
	started := time.Date(2026, 11, 20, 8, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		st   saved
		now  time.Time
		want bool
	}{
		{"labels go when the server never took them", saved{}, started.Add(time.Minute), true},
		{"labels go in the first request after the agent starts", saved{Labels: h, LabelsAt: started.Add(-time.Minute).UnixMilli()}, started.Add(time.Minute), true},
		{"the same labels don't go twice in a run", saved{Labels: h, LabelsAt: started.Add(time.Minute).UnixMilli()}, started.Add(time.Hour), false},
		{"changed labels go", saved{Labels: "other", LabelsAt: started.Add(time.Minute).UnixMilli()}, started.Add(time.Hour), true},
		{"labels go at least once a day", saved{Labels: h, LabelsAt: started.Add(time.Minute).UnixMilli()}, started.Add(25 * time.Hour), true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := labelsDue(l, tc.st, started, tc.now)
			if err != nil || got != tc.want {
				t.Errorf("labelsDue = %v, %v; want %v", got, err, tc.want)
			}
		})
	}
	// The newest table under a version that isn't the scorer's, as a
	// parameter change makes it before a scorer takes it in.
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(newestTable().Canonical(), &doc); err != nil {
		t.Fatal(err)
	}
	doc["version"] = json.RawMessage(`"9.9.9"`)
	js, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	table, err := params.Parse(js)
	if err != nil {
		t.Fatal(err)
	}
	if table.Version() == score.Version() {
		t.Fatalf("the table's version is the scorer's, %s", score.Version())
	}
	got := currentLabels("v0.1.0", table, score.Device{}, nil)
	names := []struct {
		name string
		ok   bool
	}{
		{"the labels name the scorer's version, which every bucket's v is, not the table's", got.ScoringVersion == score.Version()},
		{"the labels name the table's hash", got.Params == table.Hash()},
	}
	for _, c := range names {
		t.Run(c.name, func(t *testing.T) {
			if !c.ok {
				t.Errorf("scoring_version %q and params %q; want %q, not the table's %q, and %q", got.ScoringVersion, got.Params, score.Version(), table.Version(), table.Hash())
			}
		})
	}
}

// TestCreditBatch checks what a revision's reading rests on: a revision
// that only flips its minute from pending to final keeps the batch its
// credit rests on, however late the batch that wrote the flip reached the
// server, and one whose credit a late batch changed reads that batch.
func TestCreditBatch(t *testing.T) {
	// Batches 1 and 2 reached the server at once; batch 3, sealed at minute
	// 40, an hour and twenty minutes late, with no failure while it was due.
	ix := indexOf(t, []float64{1, 7, 40}, []float64{1.1, 7.1, 120})
	dir := t.TempDir()
	bs, err := store.OpenBuckets(dir)
	if err != nil {
		t.Fatal(err)
	}
	table := newestTable()
	rev := func(minute int, credited float64, st event.BucketState, batch uint64) store.Bucket {
		b := testBucket(table, "d_test", day0.Add(time.Duration(minute)*time.Minute), credited, st)
		b.Batch = batch
		return b
	}
	for _, b := range []store.Bucket{
		rev(0, 30, event.StatePending, 1),
		rev(5, 15, event.StatePending, 2),
		rev(0, 30, event.StateFinal, 3),   // the flip, written by the late batch
		rev(5, 30, event.StatePending, 3), // new credit, from the late batch
	} {
		if _, _, err := bs.Put(b); err != nil {
			t.Fatal(err)
		}
	}
	if err := bs.Close(); err != nil {
		t.Fatal(err)
	}
	want := []event.Witness{event.Witnessed, event.Witnessed, event.Witnessed, event.Backfilled}
	readings := func(p plan) []event.Witness {
		var ws []event.Witness
		for _, b := range p.body.Buckets {
			ws = append(ws, b.Witness)
		}
		return ws
	}
	tests := []struct {
		name string
		run  func(t *testing.T)
	}{
		{"in one plan, the flip keeps the reading and the new credit takes the late batch's", func(t *testing.T) {
			p, err := makePlan(planInput{rows: ix, st: saved{Next: 4}, bucketDir: dir, lag: 900_000})
			if err != nil {
				t.Fatal(err)
			}
			if got := readings(p); !slices.Equal(got, want) {
				t.Errorf("readings %v, want %v", got, want)
			}
			if _, ok := p.sent[at(0)]; ok {
				t.Error("a final minute is still remembered")
			}
			if r := p.sent[at(5)]; r.B != 3 {
				t.Errorf("minute 5's credit rests on batch %d, not 3", r.B)
			}
		}},
		{"across two plans, the second reads the flip from what the first sent", func(t *testing.T) {
			first, err := makePlan(planInput{rows: ix, st: saved{Next: 4}, bucketDir: dir, lag: 900_000, maxBuckets: 2})
			if err != nil {
				t.Fatal(err)
			}
			second, err := makePlan(planInput{rows: ix, st: saved{Next: 4, Marks: first.marks, Sent: first.sent}, bucketDir: dir, lag: 900_000})
			if err != nil {
				t.Fatal(err)
			}
			if got := append(readings(first), readings(second)...); !slices.Equal(got, want) {
				t.Errorf("readings %v, want %v", got, want)
			}
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, tc.run)
	}
}
