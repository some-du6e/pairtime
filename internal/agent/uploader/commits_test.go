// SPDX-License-Identifier: AGPL-3.0-or-later

package uploader

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/agent/home"
	"github.com/some-du6e/pairtime/internal/agent/ledger"
	"github.com/some-du6e/pairtime/internal/agent/sched"
	"github.com/some-du6e/pairtime/internal/agent/state"
	"github.com/some-du6e/pairtime/internal/agent/store"
	pkgledger "github.com/some-du6e/pairtime/pkg/ledger"
)

// testSHA is the n-th commit id of the tests: 40 lowercase hex digits.
func testSHA(n int) string { return fmt.Sprintf("%040x", n) }

// commitRecord is a commits/ line as internal/adapters/git's Commit
// marshals it, members in its order: commit n, observed at m in Unix ms,
// with one parent, the counts of a read shortstat, and a human actor. edit
// replaces parts of it, as strings.Replacer's pairs.
func commitRecord(n int, m int64, edit ...string) string {
	line := fmt.Sprintf(`{"sha":"%s","parents":["%s"],"project":"p_web","observed_at":%d,"author_time":%d,"committer_time":%d,`+
		`"files_changed":2,"insertions":10,"deletions":3,"actor":"human"}`, testSHA(n), testSHA(n+1000), m, m/1000-60, m/1000-30)
	if len(edit) > 0 {
		line = strings.NewReplacer(edit...).Replace(line)
	}
	return line
}

// writeCommitLines appends lines to day's file in dir as the git host's
// commitStore does, each with a newline before it and after it, and
// returns where each line ends, just past its newline.
func writeCommitLines(t *testing.T, dir, day string, lines ...string) []int64 {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	name := filepath.Join(dir, day+".jsonl")
	off := int64(0)
	if fi, err := os.Stat(name); err == nil {
		off = fi.Size()
	}
	var b []byte
	var ends []int64
	for _, l := range lines {
		b = append(b, '\n')
		b = append(b, l...)
		b = append(b, '\n')
		off += int64(len(l) + 2)
		ends = append(ends, off)
	}
	f, err := os.OpenFile(name, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(b); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return ends
}

// atS is second s of day0, in Unix ms.
func atS(s float64) int64 { return day0.Add(time.Duration(s * float64(time.Second))).UnixMilli() }

// TestCommitPlan checks which commit records of commits/ a plan sends
// (commitsOf, judgeCommit): each sha once, as the first line commits/
// holds for it (D-ag5-D2), never one that local-only mode may have seen
// (D-ag5-D1), and only once the root of the first batch sealed after it
// has been answered (D-ag5-D3), through the uploader's own 2.3.3 type
// (D-ag5-D4, D5). The batches are sealed at minutes 1, 2 and 3 of day0
// unless a case says otherwise, and the place has answered every root but
// those from Next on.
func TestCommitPlan(t *testing.T) {
	day := day0.Format(time.DateOnly)
	nextDay := day0.Add(24 * time.Hour).Format(time.DateOnly)
	tests := []struct {
		name string
		// sealed are the batches' seal minutes of day0, {1, 2, 3} when nil.
		sealed []float64
		st     saved
		// setup writes commits/ in dir and edits the rows and the input.
		setup func(t *testing.T, dir string, ix *rowIndex, in *planInput) []int64
		test  func(t *testing.T, p plan, ends []int64, in planInput)
	}{
		{"a sha's first line goes once the root of the first batch sealed at or after it was answered, as the line has it", nil, saved{Next: 4},
			func(t *testing.T, dir string, ix *rowIndex, in *planInput) []int64 {
				return writeCommitLines(t, dir, day, commitRecord(1, atS(90)))
			}, func(t *testing.T, p plan, ends []int64, in planInput) {
				if len(p.body.Commits) != 1 {
					t.Fatalf("%d records went", len(p.body.Commits))
				}
				js, err := json.Marshal(p.body.Commits[0])
				if err != nil {
					t.Fatal(err)
				}
				if string(js) != commitRecord(1, atS(90)) {
					t.Errorf("the record went as\n%s\nnot as the line has it\n%s", js, commitRecord(1, atS(90)))
				}
				if p.commitMarks[day].Off != ends[0] {
					t.Errorf("the mark is %+v, not past the line at %d", p.commitMarks[day], ends[0])
				}
			}},
		{"a line waits while its first batch's root is unanswered, and the request that carries that root carries none of its commits", nil, saved{Next: 2},
			func(t *testing.T, dir string, ix *rowIndex, in *planInput) []int64 {
				return writeCommitLines(t, dir, day, commitRecord(1, atS(90)))
			}, func(t *testing.T, p plan, ends []int64, in planInput) {
				if len(p.body.Batches) == 0 || p.body.Batches[0].B != 2 {
					t.Fatalf("the request carries roots %v, not batch 2's first", p.body.Batches)
				}
				if len(p.body.Commits) != 0 || p.commitMarks[day].Off != 0 {
					t.Errorf("%d records went beside their batch's root, mark %+v", len(p.body.Commits), p.commitMarks[day])
				}
			}},
		{"a line goes in the next request once Next is past its first batch", nil, saved{Next: 3},
			func(t *testing.T, dir string, ix *rowIndex, in *planInput) []int64 {
				return writeCommitLines(t, dir, day, commitRecord(1, atS(90)))
			}, func(t *testing.T, p plan, ends []int64, in planInput) {
				if len(p.body.Commits) != 1 || p.commitMarks[day].Off != ends[0] {
					t.Errorf("%d records went, mark %+v", len(p.body.Commits), p.commitMarks[day])
				}
			}},
		{"no line goes while the link's first batch isn't stored", nil, saved{Next: 4, Linking: true},
			func(t *testing.T, dir string, ix *rowIndex, in *planInput) []int64 {
				return writeCommitLines(t, dir, day, commitRecord(1, atS(90)))
			}, func(t *testing.T, p plan, ends []int64, in planInput) {
				if len(p.body.Commits) != 0 || p.commitMarks[day].Off != 0 {
					t.Errorf("%d records went while linking, mark %+v", len(p.body.Commits), p.commitMarks[day])
				}
			}},
		{"a line whose first batch after it was sealed in local-only mode never goes", nil, saved{Next: 4, Gaps: []gap{{From: 2, To: 2}}},
			func(t *testing.T, dir string, ix *rowIndex, in *planInput) []int64 {
				return writeCommitLines(t, dir, day, commitRecord(1, atS(90)))
			}, func(t *testing.T, p plan, ends []int64, in planInput) {
				if len(p.body.Commits) != 0 || p.commitMarks[day].Off != ends[0] {
					t.Errorf("%d records went, mark %+v: the line isn't withheld for good", len(p.body.Commits), p.commitMarks[day])
				}
			}},
		{"a line whose first batch after it comes before the link's first batch never goes", nil, saved{Next: 4, Head: 1, From: 3},
			func(t *testing.T, dir string, ix *rowIndex, in *planInput) []int64 {
				return writeCommitLines(t, dir, day, commitRecord(1, atS(90)), commitRecord(2, atS(150)))
			}, func(t *testing.T, p plan, ends []int64, in planInput) {
				if len(p.body.Commits) != 1 || p.body.Commits[0].SHA != testSHA(2) || p.commitMarks[day].Off != ends[1] {
					t.Errorf("records %v went, mark %+v: want only the one after the link's first batch's start", p.body.Commits, p.commitMarks[day])
				}
			}},
		{"a line from before the chain began never goes", nil, saved{Next: 4},
			func(t *testing.T, dir string, ix *rowIndex, in *planInput) []int64 {
				ix.created = atS(100)
				return writeCommitLines(t, dir, day, commitRecord(1, atS(90)), commitRecord(2, atS(110)))
			}, func(t *testing.T, p plan, ends []int64, in planInput) {
				if len(p.body.Commits) != 1 || p.body.Commits[0].SHA != testSHA(2) || p.commitMarks[day].Off != ends[1] {
					t.Errorf("records %v went, mark %+v: want only the one after the chain began", p.body.Commits, p.commitMarks[day])
				}
			}},
		{"a line from before a retention sweep's oldest kept batch was sealed never goes", nil, saved{Next: 4},
			func(t *testing.T, dir string, ix *rowIndex, in *planInput) []int64 {
				// The sweep dropped batch 1: the index starts at batch 2.
				swept := &rowIndex{device: ix.device, base: 2}
				for _, r := range ix.queue[1:] {
					if err := swept.add(r); err != nil {
						t.Fatal(err)
					}
				}
				in.rows = swept
				return writeCommitLines(t, dir, day, commitRecord(1, atS(100)), commitRecord(2, atS(150)))
			}, func(t *testing.T, p plan, ends []int64, in planInput) {
				if len(p.body.Commits) != 1 || p.body.Commits[0].SHA != testSHA(2) || p.commitMarks[day].Off != ends[1] {
					t.Errorf("records %v went, mark %+v: want only the one after batch 2's seal", p.body.Commits, p.commitMarks[day])
				}
			}},
		{"a line after a run's last seal and before its stretch ended never goes", nil, saved{Next: 4, Gaps: []gap{{From: 1, To: 2}}},
			func(t *testing.T, dir string, ix *rowIndex, in *planInput) []int64 {
				// Batch 3 says config.toml was last known to say local-only at
				// 2.5 minutes, after batch 2's seal at 2.
				ix.at(3).localUntil = atS(150)
				return writeCommitLines(t, dir, day, commitRecord(1, atS(130)), commitRecord(2, atS(150)), commitRecord(3, atS(160)))
			}, func(t *testing.T, p plan, ends []int64, in planInput) {
				if len(p.body.Commits) != 1 || p.body.Commits[0].SHA != testSHA(3) || p.commitMarks[day].Off != ends[2] {
					t.Errorf("records %v went, mark %+v: want only the one after the stretch's end", p.body.Commits, p.commitMarks[day])
				}
			}},
		{"a line waits while no batch is sealed at or after it, and so do the lines after it in its day", nil, saved{Next: 4},
			func(t *testing.T, dir string, ix *rowIndex, in *planInput) []int64 {
				return writeCommitLines(t, dir, day, commitRecord(1, atS(90)), commitRecord(2, atS(200)), commitRecord(3, atS(100)))
			}, func(t *testing.T, p plan, ends []int64, in planInput) {
				if len(p.body.Commits) != 1 || p.body.Commits[0].SHA != testSHA(1) || p.commitMarks[day].Off != ends[0] {
					t.Errorf("records %v went, mark %+v: want only the first, and the mark before the line that waits", p.body.Commits, p.commitMarks[day])
				}
			}},
		{"a line observed before 2020 or from 2100 on never goes", nil, saved{Next: 4},
			func(t *testing.T, dir string, ix *rowIndex, in *planInput) []int64 {
				return writeCommitLines(t, dir, day, commitRecord(1, 1_577_836_799_999), commitRecord(2, 4_102_444_800_000), commitRecord(3, atS(90)))
			}, func(t *testing.T, p plan, ends []int64, in planInput) {
				if len(p.body.Commits) != 1 || p.body.Commits[0].SHA != testSHA(3) || p.commitMarks[day].Off != ends[2] {
					t.Errorf("records %v went, mark %+v", p.body.Commits, p.commitMarks[day])
				}
			}},
		{"a sha's later line never goes, in its own day or a later one", []float64{1, 2, 3, 24*60 + 2}, saved{Next: 5},
			func(t *testing.T, dir string, ix *rowIndex, in *planInput) []int64 {
				ends := writeCommitLines(t, dir, day, commitRecord(1, atS(90)), commitRecord(1, atS(100)))
				return append(ends, writeCommitLines(t, dir, nextDay, commitRecord(1, atS(24*3600+60)))...)
			}, func(t *testing.T, p plan, ends []int64, in planInput) {
				if len(p.body.Commits) != 1 || p.body.Commits[0].ObservedAt != atS(90) {
					t.Errorf("records %v went: want the first line's alone", p.body.Commits)
				}
				if p.commitMarks[day].Off != ends[1] || p.commitMarks[nextDay].Off != ends[2] {
					t.Errorf("marks %+v: the later lines aren't passed over", p.commitMarks)
				}
			}},
		{"a sha whose first line was withheld never goes in a later line, as another clone's arrival", nil, saved{Next: 4, Gaps: []gap{{From: 2, To: 2}}},
			func(t *testing.T, dir string, ix *rowIndex, in *planInput) []int64 {
				return writeCommitLines(t, dir, day, commitRecord(1, atS(90)), commitRecord(1, atS(150), `"actor":"human"`, `"actor":"unknown"`))
			}, func(t *testing.T, p plan, ends []int64, in planInput) {
				if len(p.body.Commits) != 0 || p.commitMarks[day].Off != ends[1] {
					t.Errorf("records %v went, mark %+v", p.body.Commits, p.commitMarks[day])
				}
			}},
		{"a crash re-scan's second line of a sha, with a later observed_at and actor unknown, never goes", nil, saved{Next: 4},
			func(t *testing.T, dir string, ix *rowIndex, in *planInput) []int64 {
				return writeCommitLines(t, dir, day, commitRecord(1, atS(90)), commitRecord(1, atS(95), `"actor":"human"`, `"actor":"unknown"`))
			}, func(t *testing.T, p plan, ends []int64, in planInput) {
				if len(p.body.Commits) != 1 || p.body.Commits[0].Actor != "human" || p.body.Commits[0].ObservedAt != atS(90) {
					t.Errorf("records %v went: want the first line's alone", p.body.Commits)
				}
			}},
		{"a crash re-scan's second line of a sha with an earlier observed_at than the first, after a step back of the wall clock, never goes", nil, saved{Next: 4},
			func(t *testing.T, dir string, ix *rowIndex, in *planInput) []int64 {
				return writeCommitLines(t, dir, day, commitRecord(1, atS(110)), commitRecord(1, atS(70)))
			}, func(t *testing.T, p plan, ends []int64, in planInput) {
				if len(p.body.Commits) != 1 || p.body.Commits[0].ObservedAt != atS(110) || p.commitMarks[day].Off != ends[1] {
					t.Errorf("records %v went, mark %+v: want the first line's alone", p.body.Commits, p.commitMarks[day])
				}
			}},
		{"while a kept day file doesn't read, no line goes in that plan, and a sha whose first line is in that file never goes from another day's line",
			[]float64{1, 2, 3, 24*60 + 2}, saved{Next: 5},
			func(t *testing.T, dir string, ix *rowIndex, in *planInput) []int64 {
				ends := writeCommitLines(t, dir, day, commitRecord(1, atS(90)))
				ends = append(ends, writeCommitLines(t, dir, nextDay, commitRecord(1, atS(24*3600+60)), commitRecord(2, atS(24*3600+61)))...)
				name := filepath.Join(dir, day+".jsonl")
				if err := os.Chmod(name, 0); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { os.Chmod(name, 0o600) })
				if _, err := os.ReadFile(name); err == nil {
					t.Skip("this user reads a file with no permissions, as root does")
				}
				return ends
			}, func(t *testing.T, p plan, ends []int64, in planInput) {
				if len(p.body.Commits) != 0 || len(p.commitMarks) != 0 || len(p.commitsUnread) != 1 || p.commitsUnread[0] != day {
					t.Fatalf("records %v went, marks %+v, unread %v: want none and %s unread", p.body.Commits, p.commitMarks, p.commitsUnread, day)
				}
				if err := os.Chmod(filepath.Join(in.commitDir, day+".jsonl"), 0o600); err != nil {
					t.Fatal(err)
				}
				p, err := makePlan(in)
				if err != nil {
					t.Fatal(err)
				}
				if len(p.body.Commits) != 2 || p.body.Commits[0].ObservedAt != atS(90) || p.body.Commits[1].SHA != testSHA(2) {
					t.Errorf("once the file reads, records %v went: want commit 1's first line and commit 2", p.body.Commits)
				}
			}},
		{"a line after one that waits in its day keeps its sha from going in a later day's line, which waits with it", nil, saved{Next: 4},
			func(t *testing.T, dir string, ix *rowIndex, in *planInput) []int64 {
				ends := writeCommitLines(t, dir, day, commitRecord(1, atS(200)), commitRecord(2, atS(201)))
				return append(ends, writeCommitLines(t, dir, nextDay, commitRecord(2, atS(24*3600+60)))...)
			}, func(t *testing.T, p plan, ends []int64, in planInput) {
				if len(p.body.Commits) != 0 || p.commitMarks[day].Off != 0 || p.commitMarks[nextDay].Off != 0 {
					t.Errorf("records %v went, marks %+v: want nothing decided, the later day's line of commit 2 waiting", p.body.Commits, p.commitMarks)
				}
			}},
		{"a sha's first line that waited goes in a later plan, and its later line in a later day is then passed over", nil, saved{Next: 4},
			func(t *testing.T, dir string, ix *rowIndex, in *planInput) []int64 {
				ends := writeCommitLines(t, dir, day, commitRecord(1, atS(200)))
				return append(ends, writeCommitLines(t, dir, nextDay, commitRecord(1, atS(24*3600+60), `"actor":"human"`, `"actor":"unknown"`))...)
			}, func(t *testing.T, p plan, ends []int64, in planInput) {
				if len(p.body.Commits) != 0 || len(p.commitMarks) != 0 {
					t.Fatalf("records %v went, marks %+v, while the first line waits for a batch sealed after it", p.body.Commits, p.commitMarks)
				}
				// A batch sealed after both lines, whose root has been answered.
				in.rows = indexOf(t, []float64{1, 2, 3, 24*60 + 2}, []float64{1.1, 2.1, 3.1, 24*60 + 2.1})
				in.st.Next, in.st.CommitMarks = 5, p.commitMarks
				p, err := makePlan(in)
				if err != nil {
					t.Fatal(err)
				}
				if len(p.body.Commits) != 1 || p.body.Commits[0].ObservedAt != atS(200) || p.body.Commits[0].Actor != "human" {
					t.Errorf("the later plan sends %v: want the first line's record", p.body.Commits)
				}
				if p.commitMarks[day].Off != ends[0] || p.commitMarks[nextDay].Off != ends[1] {
					t.Errorf("marks %+v: the later line isn't passed over", p.commitMarks)
				}
			}},
		{"a later day's line that would be withheld, whose sha's line was held back behind one that waits on Next, waits too, so the sha goes once Next passes its first batch",
			[]float64{1, 2, 3, 24*60 + 2}, saved{Next: 2, Gaps: []gap{{From: 4, To: 4}}},
			func(t *testing.T, dir string, ix *rowIndex, in *planInput) []int64 {
				// Commit 1's first batch is batch 2, unanswered, so it waits,
				// and commit 2's line after it is held back with its day. The
				// later day's line of commit 2 has batch 4, sealed in local-only
				// mode, for its first batch.
				ends := writeCommitLines(t, dir, day, commitRecord(1, atS(90)), commitRecord(2, atS(91)))
				return append(ends, writeCommitLines(t, dir, nextDay, commitRecord(2, atS(24*3600+60)))...)
			}, func(t *testing.T, p plan, ends []int64, in planInput) {
				if len(p.body.Commits) != 0 || p.commitMarks[day].Off != 0 || p.commitMarks[nextDay].Off != 0 {
					t.Fatalf("records %v went, marks %+v: want nothing decided, the later day's line of commit 2 waiting with its first line", p.body.Commits, p.commitMarks)
				}
				in.st.Next, in.st.CommitMarks = 4, p.commitMarks
				p, err := makePlan(in)
				if err != nil {
					t.Fatal(err)
				}
				if len(p.body.Commits) != 2 || p.body.Commits[0].SHA != testSHA(1) || p.body.Commits[1].SHA != testSHA(2) || p.body.Commits[1].ObservedAt != atS(91) {
					t.Errorf("once Next passes batch 2, records %v went: want commits 1 and 2, by their first lines", p.body.Commits)
				}
				if p.commitMarks[nextDay].Off != ends[2] {
					t.Errorf("mark %+v: the later day's line of commit 2 isn't passed over", p.commitMarks[nextDay])
				}
			}},
		{"a line without counts goes without them, and a read 0 goes as 0", nil, saved{Next: 4},
			func(t *testing.T, dir string, ix *rowIndex, in *planInput) []int64 {
				return writeCommitLines(t, dir, day,
					commitRecord(1, atS(90), `"files_changed":2,"insertions":10,"deletions":3,`, ""),
					commitRecord(2, atS(91), `"files_changed":2,"insertions":10,"deletions":3,`, `"files_changed":0,"insertions":0,"deletions":0,`))
			}, func(t *testing.T, p plan, ends []int64, in planInput) {
				if len(p.body.Commits) != 2 {
					t.Fatalf("%d records went", len(p.body.Commits))
				}
				a, _ := json.Marshal(p.body.Commits[0])
				b, _ := json.Marshal(p.body.Commits[1])
				if bytes.Contains(a, []byte("files_changed")) || bytes.Contains(a, []byte("insertions")) || bytes.Contains(a, []byte("deletions")) {
					t.Errorf("a record without counts went with them: %s", a)
				}
				if !bytes.Contains(b, []byte(`"files_changed":0,"insertions":0,"deletions":0`)) {
					t.Errorf("a record of a commit that changed nothing went without its 0s: %s", b)
				}
			}},
		{"a member outside the uploader's 2.3.3 type, such as path_hmacs or credited_seconds_since_parent in a line, stays on the device", nil, saved{Next: 4},
			func(t *testing.T, dir string, ix *rowIndex, in *planInput) []int64 {
				return writeCommitLines(t, dir, day, commitRecord(1, atS(90),
					`"actor":"human"`, `"path_hmacs":["k_path"],"credited_seconds_since_parent":{"prompt":3},"message":"fix the bug","actor":"human"`))
			}, func(t *testing.T, p plan, ends []int64, in planInput) {
				if len(p.body.Commits) != 1 {
					t.Fatalf("%d records went", len(p.body.Commits))
				}
				for _, member := range []string{"path_hmacs", "credited_seconds_since_parent", "message", "k_path", "fix the bug"} {
					if bytes.Contains(p.js, []byte(member)) {
						t.Errorf("the body carries %q: %s", member, p.js)
					}
				}
			}},
		{"a line that doesn't read as a record, or whose sha isn't a commit id, is passed over", nil, saved{Next: 4},
			func(t *testing.T, dir string, ix *rowIndex, in *planInput) []int64 {
				return writeCommitLines(t, dir, day, `{"sha":"`+testSHA(1)+`","parents":[`,
					commitRecord(0xabcdef, atS(90), testSHA(0xabcdef), strings.ToUpper(testSHA(0xabcdef))),
					commitRecord(3, atS(91), testSHA(3), "main"), commitRecord(4, atS(92)))
			}, func(t *testing.T, p plan, ends []int64, in planInput) {
				if len(p.body.Commits) != 1 || p.body.Commits[0].SHA != testSHA(4) || p.commitMarks[day].Off != ends[3] {
					t.Errorf("records %v went, mark %+v", p.body.Commits, p.commitMarks[day])
				}
			}},
		{"a last line with no newline yet waits for the rest of it", nil, saved{Next: 4},
			func(t *testing.T, dir string, ix *rowIndex, in *planInput) []int64 {
				ends := writeCommitLines(t, dir, day, commitRecord(1, atS(90)))
				f, err := os.OpenFile(filepath.Join(dir, day+".jsonl"), os.O_WRONLY|os.O_APPEND, 0)
				if err != nil {
					t.Fatal(err)
				}
				defer f.Close()
				if _, err := f.WriteString("\n" + commitRecord(2, atS(91))); err != nil {
					t.Fatal(err)
				}
				return ends
			}, func(t *testing.T, p plan, ends []int64, in planInput) {
				if len(p.body.Commits) != 1 || p.body.Commits[0].SHA != testSHA(1) || p.commitMarks[day].Off != ends[0] {
					t.Errorf("records %v went, mark %+v", p.body.Commits, p.commitMarks[day])
				}
			}},
		{"at most the limit of records go, and the rest wait for the next request", nil, saved{Next: 4},
			func(t *testing.T, dir string, ix *rowIndex, in *planInput) []int64 {
				in.maxCommits = 2
				return writeCommitLines(t, dir, day, commitRecord(1, atS(90)), commitRecord(2, atS(91)), commitRecord(3, atS(92)))
			}, func(t *testing.T, p plan, ends []int64, in planInput) {
				if len(p.body.Commits) != 2 || !p.more || p.commitMarks[day].Off != ends[1] {
					t.Fatalf("records %v went, more %v, mark %+v", p.body.Commits, p.more, p.commitMarks[day])
				}
				in.st.CommitMarks = p.commitMarks
				next, err := makePlan(in)
				if err != nil {
					t.Fatal(err)
				}
				if len(next.body.Commits) != 1 || next.body.Commits[0].SHA != testSHA(3) || next.more {
					t.Errorf("the next request carries %v, more %v", next.body.Commits, next.more)
				}
			}},
		{"a plan made from the marks a request's answer leaves sends nothing again", nil, saved{Next: 4},
			func(t *testing.T, dir string, ix *rowIndex, in *planInput) []int64 {
				return writeCommitLines(t, dir, day, commitRecord(1, atS(90)), commitRecord(1, atS(95)), commitRecord(2, atS(96)))
			}, func(t *testing.T, p plan, ends []int64, in planInput) {
				if len(p.body.Commits) != 2 {
					t.Fatalf("%d records went", len(p.body.Commits))
				}
				in.st.CommitMarks = p.commitMarks
				next, err := makePlan(in)
				if err != nil {
					t.Fatal(err)
				}
				if len(next.body.Commits) != 0 || !sameMarks(next.commitMarks, p.commitMarks) {
					t.Errorf("the next plan sends %v, marks %+v", next.body.Commits, next.commitMarks)
				}
			}},
		{"a mark of a day commits/ no longer has goes", nil, saved{Next: 4, CommitMarks: map[string]store.Mark{"2020-01-01": {Off: 10, Line: "x"}}},
			func(t *testing.T, dir string, ix *rowIndex, in *planInput) []int64 {
				return writeCommitLines(t, dir, day, commitRecord(1, atS(90)))
			}, func(t *testing.T, p plan, ends []int64, in planInput) {
				if _, ok := p.commitMarks["2020-01-01"]; ok {
					t.Error("the mark stayed")
				}
			}},
		{"a line a recorded stretch holds never goes, though the batch sealed after it is online and in no run", nil, saved{Next: 4},
			func(t *testing.T, dir string, ix *rowIndex, in *planInput) []int64 {
				ix.stretches = []ledger.Stretch{{From: atS(70), To: atS(100)}}
				return writeCommitLines(t, dir, day, commitRecord(1, atS(90)), commitRecord(2, atS(100)), commitRecord(3, atS(101)))
			}, func(t *testing.T, p plan, ends []int64, in planInput) {
				if len(p.body.Commits) != 1 || p.body.Commits[0].SHA != testSHA(3) || p.commitMarks[day].Off != ends[2] {
					t.Errorf("records %v went, mark %+v: want only the one after the stretch, its edge held", p.body.Commits, p.commitMarks[day])
				}
			}},
		{"a line a stretch holds never goes when a step back of the wall clock makes sealedAfter find a batch sealed before the step", []float64{3, 1, 4}, saved{Next: 4},
			func(t *testing.T, dir string, ix *rowIndex, in *planInput) []int64 {
				// Batch 2 was sealed after a step back to minute 1, so a line at
				// 1.5 minutes finds batch 1, sealed at minute 3, before the step.
				ix.stretches = []ledger.Stretch{{From: atS(80), To: atS(100)}}
				return writeCommitLines(t, dir, day, commitRecord(1, atS(90)))
			}, func(t *testing.T, p plan, ends []int64, in planInput) {
				if b, _ := in.rows.sealedAfter(atS(90)); b != 1 {
					t.Fatalf("sealedAfter finds batch %d: the case wants batch 1, sealed before the step", b)
				}
				if len(p.body.Commits) != 0 || p.commitMarks[day].Off != ends[0] {
					t.Errorf("records %v went, mark %+v: want the line withheld for good", p.body.Commits, p.commitMarks[day])
				}
			}},
		{"a line a step back of the wall clock dates before an open stretch's start waits while the rows hold the stretch open, and never goes once they hold its end",
			[]float64{1, 2, 3, 4}, saved{Next: 5},
			func(t *testing.T, dir string, ix *rowIndex, in *planInput) []int64 {
				ix.stretches = []ledger.Stretch{{From: atS(150), Open: true}}
				return writeCommitLines(t, dir, day, commitRecord(1, atS(100)), commitRecord(2, atS(210)))
			}, func(t *testing.T, p plan, ends []int64, in planInput) {
				if len(p.body.Commits) != 0 || len(p.commitMarks) != 0 {
					t.Fatalf("records %v went, marks %+v, while the stretch goes on", p.body.Commits, p.commitMarks)
				}
				// The seal after the switch back records the stretch's end, its
				// start moved back by the step its edges' clock measured.
				in.rows.stretches = []ledger.Stretch{{From: atS(90), To: atS(200)}}
				p, err := makePlan(in)
				if err != nil {
					t.Fatal(err)
				}
				if len(p.body.Commits) != 1 || p.body.Commits[0].SHA != testSHA(2) || p.commitMarks[day].Off != ends[1] {
					t.Errorf("once the rows hold the end, records %v went, mark %+v: want only the line after the stretch", p.body.Commits, p.commitMarks[day])
				}
			}},
		{"every line waits while a row's local_until rose past the newest list, as an older version's rows do, and is judged by the list the next seal records",
			nil, saved{Next: 4},
			func(t *testing.T, dir string, ix *rowIndex, in *planInput) []int64 {
				ix.behind = true
				return writeCommitLines(t, dir, day, commitRecord(1, atS(90)), commitRecord(2, atS(150)))
			}, func(t *testing.T, p plan, ends []int64, in planInput) {
				if len(p.body.Commits) != 0 || len(p.commitMarks) != 0 {
					t.Fatalf("records %v went, marks %+v, while the rows lack a stretch", p.body.Commits, p.commitMarks)
				}
				// The next seal records the list Open rebuilt from those rows.
				if err := in.rows.add(ledger.Row{BatchRow: pkgledger.BatchRow{B: 4, FirstSeq: 4, N: 1}, SealedAt: day0.Add(4 * time.Minute),
					Online: true, Stretches: []ledger.Stretch{{From: 0, To: atS(100)}}}); err != nil {
					t.Fatal(err)
				}
				p, err := makePlan(in)
				if err != nil {
					t.Fatal(err)
				}
				if len(p.body.Commits) != 1 || p.body.Commits[0].SHA != testSHA(2) || p.commitMarks[day].Off != ends[1] {
					t.Errorf("by the new list, records %v went, mark %+v: want only the line after the rebuilt stretch", p.body.Commits, p.commitMarks[day])
				}
			}},
		{"a day whose mark names no line that ends there is read again from its first line", nil, saved{Next: 4},
			func(t *testing.T, dir string, ix *rowIndex, in *planInput) []int64 {
				ends := writeCommitLines(t, dir, day, commitRecord(1, atS(90)), commitRecord(2, atS(91)))
				in.st.CommitMarks = map[string]store.Mark{day: {Off: ends[0], Line: "0000000000000000"}}
				return ends
			}, func(t *testing.T, p plan, ends []int64, in planInput) {
				if len(p.body.Commits) != 2 || p.commitMarks[day].Off != ends[1] {
					t.Errorf("records %v went, mark %+v: want both from the day's start", p.body.Commits, p.commitMarks[day])
				}
			}},
		{"a withheld line whose day the prune deleted keeps its sha decided: the same sha seen later, with a first batch sealed online, sends nothing", nil, saved{Next: 4, Gaps: []gap{{From: 1, To: 1}}},
			func(t *testing.T, dir string, ix *rowIndex, in *planInput) []int64 {
				// Commit 1 was seen on a day 180 days are past, and its first
				// batch, batch 1, was sealed in local-only mode, so a plan
				// withheld it.
				writeCommitLines(t, dir, "2026-05-01", commitRecord(1, atS(-202*24*3600)))
				p, err := makePlan(*in)
				if err != nil {
					t.Fatal(err)
				}
				if len(p.body.Commits) != 0 || p.commitMarks["2026-05-01"].Off == 0 {
					t.Fatalf("records %v went, marks %+v: want commit 1 withheld", p.body.Commits, p.commitMarks)
				}
				in.st.CommitMarks = p.commitMarks
				if err := PruneCommits(dir, "2026-05-21"); err != nil {
					t.Fatal(err)
				}
				// Seen again, as after another clone's pull, with batch 2 for
				// its first batch, and a new commit after it.
				return writeCommitLines(t, dir, day, commitRecord(1, atS(90)), commitRecord(3, atS(92)))
			}, func(t *testing.T, p plan, ends []int64, in planInput) {
				if len(p.body.Commits) != 1 || p.body.Commits[0].SHA != testSHA(3) || p.commitMarks[day].Off != ends[1] {
					t.Errorf("records %v went, mark %+v: want only commit 3, commit 1's line passed over", p.body.Commits, p.commitMarks[day])
				}
				if _, ok := p.commitMarks["2026-05-01"]; ok {
					t.Errorf("the pruned day's mark stays: %+v", p.commitMarks)
				}
			}},
		{"a prune with the clock 181 days ahead deletes every day's file and keeps its shas decided, so a sha of theirs seen again sends nothing", nil, saved{Next: 4},
			func(t *testing.T, dir string, ix *rowIndex, in *planInput) []int64 {
				writeCommitLines(t, dir, day, commitRecord(1, atS(90)), commitRecord(2, atS(91)))
				ahead := day0.Add(181 * 24 * time.Hour)
				if err := PruneCommits(dir, ahead.Add(-180*24*time.Hour).Format(time.DateOnly)); err != nil {
					t.Fatal(err)
				}
				if _, err := os.Stat(filepath.Join(dir, day+".jsonl")); !errors.Is(err, fs.ErrNotExist) {
					t.Fatalf("the day's file is still there: %v", err)
				}
				return writeCommitLines(t, dir, nextDay, commitRecord(2, atS(24*3600+60)), commitRecord(3, atS(24*3600+61)))
			}, func(t *testing.T, p plan, ends []int64, in planInput) {
				for _, c := range p.body.Commits {
					if c.SHA == testSHA(1) || c.SHA == testSHA(2) {
						t.Errorf("records %v went: a pruned sha went again", p.body.Commits)
					}
				}
				if p.commitMarks[nextDay].Off == 0 {
					t.Errorf("mark %+v: the line of the pruned sha isn't decided", p.commitMarks[nextDay])
				}
			}},
		{"a list of pruned shas that can't be read stops every line, as a day's file that can't be read does", nil, saved{Next: 4},
			func(t *testing.T, dir string, ix *rowIndex, in *planInput) []int64 {
				writeCommitLines(t, dir, "2026-05-01", commitRecord(1, atS(-202*24*3600)))
				if err := PruneCommits(dir, "2026-05-21"); err != nil {
					t.Fatal(err)
				}
				ends := writeCommitLines(t, dir, day, commitRecord(1, atS(90)), commitRecord(2, atS(91)))
				name := filepath.Join(dir, PrunedCommits)
				if err := os.Chmod(name, 0); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { os.Chmod(name, 0o600) })
				if _, err := os.ReadFile(name); err == nil {
					t.Skip("this user reads a file with no permissions, as root does")
				}
				return ends
			}, func(t *testing.T, p plan, ends []int64, in planInput) {
				if len(p.body.Commits) != 0 || len(p.commitMarks) != 0 || len(p.commitsUnread) != 1 || p.commitsUnread[0] != "any day" {
					t.Errorf("records %v went, marks %+v, unread %v: want none, and every day unread", p.body.Commits, p.commitMarks, p.commitsUnread)
				}
			}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sealed := tc.sealed
			if sealed == nil {
				sealed = []float64{1, 2, 3}
			}
			got := make([]float64, len(sealed))
			for i, s := range sealed {
				got[i] = s + 0.1
			}
			ix := indexOf(t, sealed, got)
			dir := filepath.Join(t.TempDir(), "commits")
			in := planInput{rows: ix, st: tc.st, commitDir: dir, lag: 900_000}
			ends := tc.setup(t, dir, ix, &in)
			p, err := makePlan(in)
			if err != nil {
				t.Fatal(err)
			}
			tc.test(t, p, ends, in)
		})
	}
}

// TestCommitUpload sends commits/ to fakeIngest through the uploader's
// loop. The first upload after the upgrade sends every first line that may
// go, oldest day first, in requests of at most the limit, halving them
// after too_many_commits, and the marks move past every line (D-ag5-D6). A
// line seen before the sign-in, and a sha's later lines, never go
// (D-ag5-D1, D2). A restart sends nothing again. And privacy show prints
// the request that carries a record byte for byte, once its batch's root
// has gone (D-ag5-D3, D7).
func TestCommitUpload(t *testing.T) {
	dir := t.TempDir()
	dirs := home.Dirs{Data: dir, Config: dir, Runtime: dir}
	table := newestTable()
	// The batches run across a UTC midnight, two hours in, so commits/ has
	// two days.
	start := time.Now().UTC().Truncate(24 * time.Hour).Add(-26 * time.Hour)
	kDev := sha256.Sum256([]byte("uploader commit test K_dev"))
	keys, err := pkgledger.DeriveKeys(kDev[:])
	if err != nil {
		t.Fatal(err)
	}
	var (
		clockMu   sync.Mutex
		ledgerNow = start
		up        atomic.Pointer[Uploader]
	)
	w, err := ledger.Open(ledger.Options{
		Dir: dirs.Ledger(), Keys: keys, KeyID: "0123456789abcdef", Params: table,
		Now: func() time.Time {
			clockMu.Lock()
			defer clockMu.Unlock()
			return ledgerNow
		},
		Sealed: func(b ledger.Batch) {
			if u := up.Load(); u != nil {
				u.Sealed(b)
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { w.Close() })
	seal := func(t *testing.T, at time.Time) {
		t.Helper()
		clockMu.Lock()
		ledgerNow = at
		clockMu.Unlock()
		if _, err := w.Checkpoint("p_test", uint64(at.UnixMilli()), json.RawMessage(`{}`)); err != nil {
			t.Fatal(err)
		}
		if _, ok, err := w.Seal(); err != nil || !ok {
			t.Fatalf("seal at %v: %v, %v", at, ok, err)
		}
	}
	// A batch every 15 minutes for three hours.
	const batches = 12
	for k := 1; k <= batches; k++ {
		seal(t, start.Add(time.Duration(k)*15*time.Minute))
	}
	device := w.DeviceID()
	minute := func(m int) int64 { return start.Add(time.Duration(m) * time.Minute).UnixMilli() }
	dayA, dayB := start.Format(time.DateOnly), start.Add(2*time.Hour).Format(time.DateOnly)
	cdir := dirs.Commits()
	endsA := writeCommitLines(t, cdir, dayA,
		commitRecord(10, minute(5)),  // before the sign-in: its batch is in the link's gap
		commitRecord(11, minute(25)), // in the link's first batch
		commitRecord(12, minute(50)),
		commitRecord(13, minute(80)),
		commitRecord(12, minute(85), `"actor":"human"`, `"actor":"unknown"`), // a crash's second scan
	)
	endsB := writeCommitLines(t, cdir, dayB,
		commitRecord(14, minute(130)),
		commitRecord(15, minute(160)),
		commitRecord(13, minute(161), `"actor":"human"`, `"actor":"unknown"`), // another clone's pull
	)
	first := map[int]int64{11: minute(25), 12: minute(50), 13: minute(80), 14: minute(130), 15: minute(160)}

	devKey := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{7}, 32))
	fake := newFakeIngest(device, testToken, devKey.Public().(ed25519.PublicKey))
	// The server takes one record a request, so the uploader's requests of
	// two are refused as too_many_commits and split.
	fake.commitMax = 1
	srv := httptest.NewServer(fake)
	t.Cleanup(srv.Close)
	st, _, err := state.Open(dirs.StateFile())
	if err != nil {
		t.Fatal(err)
	}
	creds := Credentials{Server: srv.URL, Token: testToken, DeviceID: device, Sign: KeySigner(devKey), LinkedAt: start.Add(20 * time.Minute)}
	// A scheduler that never runs takes the place of the uploader's own
	// ticker, so only the test wakes it.
	opts := Options{
		Ledger: w, LedgerDir: dirs.Ledger(), BucketDir: dirs.Buckets(), DataDir: dirs.Data, State: st,
		Login:  LoginFunc(func() (Credentials, bool) { return creds, true }),
		Params: table, Client: srv.Client(), Draw: func(int64) int64 { return 0 }, Sched: sched.New(),
	}
	// start2 starts an uploader as the agent does, which records its start
	// in state.json: its first request carries the labels, as privacy show
	// knows from that.
	starts := 0
	start2 := func(t *testing.T) *Uploader {
		t.Helper()
		starts++
		opts.Started = time.Now()
		if err := opts.State.Put("agent", map[string]any{"version": "dev", "starts": starts, "started": opts.Started.UTC()}); err != nil {
			t.Fatal(err)
		}
		if err := opts.State.Save(); err != nil {
			t.Fatal(err)
		}
		u, err := New(opts)
		if err != nil {
			t.Fatal(err)
		}
		u.maxCommits = 2
		u.pace = newPacer(1000, time.Millisecond)
		up.Store(u)
		u.Start()
		return u
	}
	u := start2(t)
	t.Cleanup(func() { up.Load().Close() })
	preview := func(t *testing.T) Preview {
		t.Helper()
		p, err := Next(PreviewOptions{
			LedgerDir: dirs.Ledger(), BucketDir: dirs.Buckets(), DataDir: dirs.Data, StateFile: dirs.StateFile(),
			Params: table, Credentials: creds, maxCommits: 2,
		})
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	// shasIn returns the shas the bodies carry, each once, in the order
	// they first went.
	shasIn := func(t *testing.T, bodies [][]byte) (order []string, most int) {
		t.Helper()
		for _, js := range bodies {
			var b Body
			if err := json.Unmarshal(js, &b); err != nil {
				t.Fatal(err)
			}
			most = max(most, len(b.Commits))
			for _, c := range b.Commits {
				if !slices.Contains(order, c.SHA) {
					order = append(order, c.SHA)
				}
			}
		}
		return order, most
	}

	steps := []struct {
		name string
		run  func(t *testing.T)
	}{
		{"the first upload sends every first line that may go, oldest day first, halved after too_many_commits, and the marks pass every line", func(t *testing.T) {
			waitFor(t, "every record that may go", func() bool {
				u.Wake()
				fake.mu.Lock()
				defer fake.mu.Unlock()
				return len(fake.commitRecs) == len(first)
			})
			fake.mu.Lock()
			recs, codes := maps.Clone(fake.commitRecs), maps.Clone(fake.commitCodes)
			fake.mu.Unlock()
			for n, m := range first {
				var c struct {
					ObservedAt int64  `json:"observed_at"`
					Actor      string `json:"actor"`
				}
				if err := json.Unmarshal(recs[testSHA(n)].rec, &c); err != nil || c.ObservedAt != m || c.Actor != "human" {
					t.Errorf("commit %d is stored as %s (%v): want its first line's record", n, recs[testSHA(n)].rec, err)
				}
			}
			if _, ok := recs[testSHA(10)]; ok {
				t.Error("the commit seen before the sign-in went")
			}
			if !maps.Equal(codes, map[string]int{codeAccepted: len(first)}) {
				t.Errorf("the records got %v: want each accepted once", codes)
			}
			_, bodies, _ := fake.snapshot()
			order, most := shasIn(t, bodies)
			want := []string{testSHA(11), testSHA(12), testSHA(13), testSHA(14), testSHA(15)}
			if !slices.Equal(order, want) {
				t.Errorf("the records went in the order %v, not oldest day first", order)
			}
			if most != 2 {
				t.Errorf("the most records a request carried is %d: want the limit, 2, before the split", most)
			}
			waitFor(t, "the uploader's place on disk", func() bool {
				s, _, err := readState(dirs.StateFile(), time.Now())
				return err == nil && s.CommitMarks[dayA].Off == endsA[len(endsA)-1] && s.CommitMarks[dayB].Off == endsB[len(endsB)-1]
			})
			if p := preview(t); p.Commits != 0 {
				t.Errorf("privacy show still has %d records to send", p.Commits)
			}
		}},
		{"a restart sends nothing again, and no commit comes back updated", func(t *testing.T) {
			up.Load().Close()
			_, before, _ := fake.snapshot()
			st2, _, err := state.Open(dirs.StateFile())
			if err != nil {
				t.Fatal(err)
			}
			opts.State = st2
			u = start2(t)
			for range 5 {
				u.Wake()
				time.Sleep(100 * time.Millisecond)
			}
			_, bodies, _ := fake.snapshot()
			if order, _ := shasIn(t, bodies[len(before):]); len(order) != 0 {
				t.Errorf("the restart sent the records %v again", order)
			}
			fake.mu.Lock()
			codes := maps.Clone(fake.commitCodes)
			fake.mu.Unlock()
			if !maps.Equal(codes, map[string]int{codeAccepted: len(first)}) {
				t.Errorf("the records got %v", codes)
			}
		}},
		{"privacy show prints the request that carries a record byte for byte, once the record's batch's root has gone", func(t *testing.T) {
			ends := writeCommitLines(t, cdir, dayB, commitRecord(16, start.Add(3*time.Hour+5*time.Minute).UnixMilli()))
			seal(t, start.Add(3*time.Hour+15*time.Minute))
			u.Wake()
			waitFor(t, "the new batch's root", func() bool { _, _, head := fake.snapshot(); return head == batches+1 })
			var pv Preview
			waitFor(t, "the record in privacy show", func() bool { pv = preview(t); return pv.Commits == 1 })
			if pv.Batches != 0 {
				t.Errorf("the record's request carries %d roots: want its batch's root to have gone before it", pv.Batches)
			}
			_, before, _ := fake.snapshot()
			if order, _ := shasIn(t, before); slices.Contains(order, testSHA(16)) {
				t.Fatal("the record went beside its batch's root")
			}
			u.Wake()
			waitFor(t, "the record's request", func() bool { _, bodies, _ := fake.snapshot(); return len(bodies) > len(before) })
			_, bodies, _ := fake.snapshot()
			if got := bodies[len(before)]; !bytes.Equal(got, pv.JSON) {
				t.Errorf("the request's body differs from privacy show's:\n%s\n%s", got, pv.JSON)
			}
			waitFor(t, "the uploader's place on disk", func() bool {
				s, _, err := readState(dirs.StateFile(), time.Now())
				return err == nil && s.CommitMarks[dayB].Off == ends[0]
			})
		}},
	}
	for _, s := range steps {
		if !t.Run(s.name, s.run) {
			break
		}
	}
}

// TestCommitStretchEnd is a switch back online, by hand, that finds nothing
// pending, so no seal frame records the stretch's end until the next
// batch's (D-ag5-D1). A commit seen between the install's stretch and this
// one, whose line a catch-up scan wrote during this one, waits while the
// rows hold the stretch open, though its batch's root went, and goes once
// the next seal frame records the end. The chain's first stretch begins at
// the beginning of time, as every install starts in local mode, so the
// stretch here is the second.
func TestCommitStretchEnd(t *testing.T) {
	r := newLiveRig(t, 90)
	// A new install's mode, until the first sign-in, which the watch notes
	// 300 ms later with nothing pending.
	r.linked.Store(false)
	r.local.Store(true)
	r.u.Start()
	r.seal(t, 1)
	login := r.sealedAt(1).Add(time.Minute)
	r.link(login)
	r.local.Store(false)
	r.setClock(login.Add(300 * time.Millisecond))
	r.w.NoteMode(true)
	r.seal(t, 2)
	r.seal(t, 3)
	r.wait(t, "batches 2 and 3", func(f *fakeIngest) bool { return f.head != nil && f.head.b == 3 })
	// config.toml says local again, as the watch notes, and the stretch's
	// first record seals at once as batch 4.
	r.local.Store(true)
	r.w.NoteMode(false)
	r.record(t, 4)
	waitFor(t, "the stretch's first record to seal at once", func() bool { return r.w.Status().Batches == 4 })
	// A commit seen between the two stretches, whose first batch after it
	// is 3.
	seen := r.sealedAt(2).Add(5 * time.Minute)
	writeCommitLines(t, r.dirs.Commits(), seen.UTC().Format(time.DateOnly), commitRecord(1, seen.UnixMilli()))
	// The teen sets mode = "online" by hand, and the watch's note finds
	// nothing pending, so nothing is sealed.
	r.local.Store(false)
	r.setClock(r.sealedAt(4).Add(5 * time.Minute))
	r.w.NoteMode(true)
	r.drain()
	r.f.mu.Lock()
	_, early := r.f.commitRecs[testSHA(1)]
	r.f.mu.Unlock()
	// The next batch's seal frame records the stretch's end.
	r.seal(t, 5)
	r.wait(t, "the commit", func(f *fakeIngest) bool { _, ok := f.commitRecs[testSHA(1)]; return ok })
	checks := []struct {
		name string
		ok   bool
	}{
		{"after a switch back that finds nothing pending, a line waits until the next seal frame records the stretch's end", !early},
	}
	for _, ch := range checks {
		t.Run(ch.name, func(t *testing.T) {
			if !ch.ok {
				t.Errorf("no: the commit went while the rows held the stretch open; the uploader says %+v", r.u.Status())
			}
		})
	}
}

// TestCommitAfterRestoredMark is a restart whose restored mark the batch
// holding a catch-up git change can't take (D-ag5-C2). After a re-link, a
// hook call of the stretch reaches the writer late, so the first batch
// after the switch back is sealed local-only and records the mark due, and
// a kill -9 follows. After the restart, the git change a scan finds is
// sealed online alone: a mark Open restored never marks a batch holding a
// git change, so the batch ends the mark and stays in the run of
// local-only batches, and its commit line never goes. The next batch's
// line does.
func TestCommitAfterRestoredMark(t *testing.T) {
	r := newLiveRig(t, 30)
	r.u.Start()
	r.seal(t, 1)
	r.seal(t, 2)
	r.wait(t, "batches 1 and 2", func(f *fakeIngest) bool { return f.head != nil && f.head.b == 2 })
	// pairtime logout sets mode = "local", which the watch notes, and the
	// stretch's first record seals at once as batch 3.
	r.linked.Store(false)
	r.local.Store(true)
	r.w.NoteMode(false)
	r.record(t, 3)
	waitFor(t, "the stretch's first record to seal at once", func() bool { return r.w.Status().Batches == 3 })
	made := r.sealedAt(3).Add(2 * time.Minute)
	login := made.Add(time.Minute)
	// pairtime login, which the watch notes 300 ms later, with nothing
	// pending. A hook call of the stretch reaches the writer 3 s after the
	// note: batch 4, local-only, which records the mark due.
	r.link(login)
	r.local.Store(false)
	r.setClock(login.Add(300 * time.Millisecond))
	r.w.NoteMode(true)
	r.setClock(login.Add(3300 * time.Millisecond))
	if _, err := r.w.Write(hookCall(made)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.w.Seal(); err != nil {
		t.Fatal(err)
	}
	killed := copyDir(t, r.dirs.Data)
	r.restart(t, killed)
	r.u.Start()
	// The startup scan finds a commit: its line, then its git change,
	// sealed at once as batch 5.
	scan := login.Add(time.Minute)
	scanned := "1a2b3c4d5e6f7a8b9c0d1e2f3a4b5c6d7e8f9a0b" // gitChange's
	r.setClock(scan)
	writeCommitLines(t, r.dirs.Commits(), scan.UTC().Format(time.DateOnly), commitRecord(1, scan.UnixMilli(), testSHA(1), scanned))
	if _, err := r.w.Write(gitChange(scan)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.w.Seal(); err != nil {
		t.Fatal(err)
	}
	// A commit after it, and the next batch, 6.
	next := scan.Add(time.Minute)
	writeCommitLines(t, r.dirs.Commits(), next.UTC().Format(time.DateOnly), commitRecord(2, next.UnixMilli()))
	r.recordAt(t, next.Add(30*time.Second))
	if _, _, err := r.w.Seal(); err != nil {
		t.Fatal(err)
	}
	last := r.w.Status().Batches
	_, rows, err := ledger.ReadRows(r.dirs.Ledger(), 4)
	if err != nil {
		t.Fatal(err)
	}
	r.u.Wake()
	r.wait(t, fmt.Sprintf("batch %d", last), func(f *fakeIngest) bool { return f.head != nil && f.head.b == last })
	r.wait(t, "the next batch's commit", func(f *fakeIngest) bool { _, ok := f.commitRecs[testSHA(2)]; return ok })
	r.drain()
	r.f.mu.Lock()
	defer r.f.mu.Unlock()
	_, scanWent := r.f.commitRecs[scanned]
	_, nextWent := r.f.commitRecs[testSHA(2)]
	scanSent := slices.ContainsFunc(r.f.bodies, func(b []byte) bool { return bytes.Contains(b, []byte(scanned)) })
	checks := []struct {
		name string
		ok   bool
	}{
		{"the first batch after the switch back was sealed local-only, and the git change's batch online and unmarked",
			len(rows) == 3 && !rows[0].Online && rows[1].Online && !rows[1].Back},
		{"after a restart whose restored mark the batch holding a catch-up git change can't take, that change's commit lines never go", !scanWent && !scanSent},
		{"a commit of the next batch goes", nextWent},
	}
	for _, ch := range checks {
		t.Run(ch.name, func(t *testing.T) {
			if !ch.ok {
				t.Errorf("no: the rows from batch 4 are %+v; the server holds head %d from %d, gaps %v, after %d POSTs", rows, r.f.head.b, r.f.start, r.f.gaps, r.f.posts)
			}
		})
	}
}

// TestCommitInStretch is D-ag5-D1's first case, with a real writer: a
// stretch no batch was sealed in, as when the watch notes local-only mode
// with nothing pending and a seal's read finds config.toml online again,
// then a batch sealed online, whose seal frame records the stretch. A git
// change made in the stretch that reaches the writer after that batch is
// dropped (ledger.ErrLateLocal), and its commit line has that online batch
// for its first batch, in no run of local-only batches, so only the
// stretch the rows hold withholds it. A commit made after the stretch goes.
func TestCommitInStretch(t *testing.T) {
	r := newLiveRig(t, 30)
	r.u.Start()
	r.seal(t, 1)
	r.seal(t, 2)
	r.wait(t, "batches 1 and 2 and their minutes", func(f *fakeIngest) bool { return f.head != nil && f.head.b == 2 && len(f.buckets) == 30 })
	// config.toml says local-only, and the watch notes it with nothing
	// pending, so no batch is sealed in local-only mode.
	begin := r.sealedAt(2).Add(time.Minute)
	r.setClock(begin)
	r.local.Store(true)
	r.w.NoteMode(false)
	// config.toml says online again, which no note says: a seal with
	// nothing pending reads it, and the read ends the stretch.
	r.setClock(begin.Add(4 * time.Minute))
	r.local.Store(false)
	if _, _, err := r.w.Seal(); err != nil {
		t.Fatal(err)
	}
	// Batch 3, sealed online after the stretch, records it.
	r.seal(t, 3)
	// The git change made in the stretch, which the watcher held past batch
	// 3, has its line in commits/ before it, and so does a commit made
	// after batch 3.
	made := begin.Add(2 * time.Minute)
	heldSHA, after := "1a2b3c4d5e6f7a8b9c0d1e2f3a4b5c6d7e8f9a0b", testSHA(2)
	for _, l := range []struct {
		sha string
		at  time.Time
	}{{heldSHA, made}, {after, r.sealedAt(3).Add(time.Minute)}} {
		writeCommitLines(t, r.dirs.Commits(), l.at.UTC().Format(time.DateOnly), commitRecord(1, l.at.UnixMilli(), testSHA(1), l.sha))
	}
	r.setClock(r.sealedAt(3).Add(2 * time.Minute))
	if _, err := r.w.Write(gitChange(made)); !errors.Is(err, ledger.ErrLateLocal) {
		t.Fatalf("Write = %v; want the git change dropped, as batch 3 was sealed online since its stretch ended", err)
	}
	r.seal(t, 4)
	r.wait(t, "the commit made after the stretch", func(f *fakeIngest) bool { _, ok := f.commitRecs[after]; return ok })
	r.drain()
	_, rows, err := ledger.ReadRows(r.dirs.Ledger(), 1)
	if err != nil {
		t.Fatal(err)
	}
	line, _ := os.ReadFile(filepath.Join(r.dirs.Commits(), made.UTC().Format(time.DateOnly)+".jsonl"))
	r.f.mu.Lock()
	defer r.f.mu.Unlock()
	_, heldWent := r.f.commitRecs[heldSHA]
	heldSent := slices.ContainsFunc(r.f.bodies, func(b []byte) bool { return bytes.Contains(b, []byte(heldSHA)) })
	checks := []struct {
		name string
		ok   bool
	}{
		{"every batch was sealed online, so no run withholds the line", len(rows) == 4 && !slices.ContainsFunc(rows, func(row ledger.Row) bool { return !row.Online })},
		{"the commit made in the stretch never reached the fake", !heldWent && !heldSent},
		{"its line stays in commits/", bytes.Contains(line, []byte(heldSHA))},
	}
	for _, c := range checks {
		t.Run(c.name, func(t *testing.T) {
			if !c.ok {
				t.Errorf("no: the rows are %+v, and the server holds head %d after %d POSTs", rows, r.f.head.b, r.f.posts)
			}
		})
	}
}
