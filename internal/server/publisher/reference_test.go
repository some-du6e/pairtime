// SPDX-License-Identifier: AGPL-3.0-or-later

package publisher_test

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/some-du6e/pairtime/internal/server/publisher"
	"github.com/some-du6e/pairtime/internal/server/store"
	"github.com/some-du6e/pairtime/internal/server/store/storetest"
	"github.com/some-du6e/pairtime/pkg/ledger"
)

// The vectors format of ledger-v1 11.3, its days alone, which
// tools/ledger_ref.py check reads.
type (
	vectorsFile struct {
		Format string   `json:"format"`
		Note   string   `json:"note"`
		Days   []dayVec `json:"days"`
	}
	dayVec struct {
		Day       uint64     `json:"day"`
		Date      string     `json:"date"`
		Prev      string     `json:"prev"`
		Count     uint64     `json:"count"`
		Root      string     `json:"root"`
		Head      string     `json:"head"`
		SignedMsg string     `json:"signed_msg"`
		Rows      []rowVec   `json:"rows"`
		Proofs    []proofVec `json:"proofs"`
	}
	rowVec struct {
		DeviceID   string `json:"device_id"`
		B          uint64 `json:"b"`
		Head       string `json:"head"`
		ReceivedAt uint64 `json:"received_at"`
		Leaf       string `json:"leaf"`
	}
	proofVec struct {
		Index uint64   `json:"index"`
		Path  []string `json:"path"`
	}
)

// trickyRows stores rows that a database could order otherwise than the
// day tree does, in an order of their own: ids whose byte order differs
// from their order with case folded or under a locale's collation, all in
// one millisecond, an offline flush of four batches in another, the first
// and last millisecond of a day, a 128-character id, and a day with no
// rows between two days with rows.
func trickyRows(t *testing.T, f *fixture) {
	t.Helper()
	long := strings.Repeat("Ab-_9", 25) + "xyz"
	for _, id := range []string{"w_", "a_", "Zq", "w", "aB", "a-"} {
		f.row(t, id, 1, "2026-11-19T15:00:00.500Z")
	}
	for b := uint64(4); b >= 1; b-- {
		f.row(t, "flush", b, "2026-11-19T18:30:00Z")
	}
	f.row(t, long, 1, "2026-11-19T23:59:59.999Z")
	f.row(t, "early", 1, "2026-11-19T00:00:00Z")
	f.row(t, "d1", 1, "2026-11-21T00:00:00Z")
	f.row(t, "d1", 2, "2026-11-21T09:00:00Z")
	f.row(t, "d2", 1, "2026-11-21T09:00:00Z")
}

// vectors writes the stored day heads, their rows in day order, and the
// proof Proof gives for each row, in the vectors format.
func vectors(t *testing.T, f *fixture) vectorsFile {
	t.Helper()
	ctx := context.Background()
	heads, err := publisher.Heads(ctx, f.db.Read)
	if err != nil {
		t.Fatal(err)
	}
	doc := vectorsFile{Format: "pairtime-ledger-vectors-v1", Note: "Written by internal/server/publisher's TestReferenceCheck."}
	for _, h := range heads {
		rows, err := publisher.DayRows(ctx, f.db.Read, h.D)
		if err != nil {
			t.Fatal(err)
		}
		slices.SortFunc(rows, ledger.CompareDayRows)
		dv := dayVec{Day: h.D, Date: publisher.Date(h.D), Prev: h.Prev.String(), Count: h.Count, Root: h.Root.String(), Head: h.Head.String(),
			SignedMsg: hex.EncodeToString(ledger.DayMessage(h.D, h.Head)), Rows: []rowVec{}, Proofs: []proofVec{}}
		for i, r := range rows {
			leaf, err := r.Leaf()
			if err != nil {
				t.Fatal(err)
			}
			dv.Rows = append(dv.Rows, rowVec{DeviceID: r.DeviceID, B: r.B, Head: r.Head.String(), ReceivedAt: r.ReceivedAt, Leaf: hex.EncodeToString(leaf)})
			in, err := publisher.Proof(ctx, f.db.Read, r.DeviceID, r.B)
			if err != nil {
				t.Fatal(err)
			}
			if in.Index != uint64(i) || in.Day.D != h.D {
				t.Fatalf("Proof puts batch %d of %s at leaf %d of day %d, and the day's order at leaf %d of day %d", r.B, r.DeviceID, in.Index, in.Day.D, i, h.D)
			}
			if err := ledger.VerifyInclusion(in.Index, h.Count, ledger.LeafHash(leaf), in.Path, h.Root); err != nil {
				t.Errorf("batch %d of %s: %v", r.B, r.DeviceID, err)
			}
			pv := proofVec{Index: in.Index, Path: []string{}}
			for _, p := range in.Path {
				pv.Path = append(pv.Path, p.String())
			}
			dv.Proofs = append(dv.Proofs, pv)
		}
		doc.Days = append(doc.Days, dv)
	}
	return doc
}

// TestReferenceCheck publishes days of rows chosen to trip a database's
// ordering, and has tools/ledger_ref.py, which shares no code with
// pkg/ledger, recompute every leaf, the order, T_d, count_d, G_d, D_d and
// each row's inclusion proof (ledger-v1 8, 11.3). It needs python3 and
// fails without it.
func TestReferenceCheck(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatalf("python3 is missing, and this test runs tools/ledger_ref.py with it: %v", err)
	}
	ref, err := filepath.Abs(filepath.Join("..", "..", "..", "tools", "ledger_ref.py"))
	if err != nil {
		t.Fatal(err)
	}
	tamper := func(doc vectorsFile) vectorsFile {
		var c vectorsFile
		b, _ := json.Marshal(doc)
		if err := json.Unmarshal(b, &c); err != nil {
			t.Fatal(err)
		}
		// Another hex digit, so the file still parses and only the
		// leaf's value is wrong.
		r := []byte(c.Days[0].Rows[0].Leaf)
		if r[len(r)-1] == '0' {
			r[len(r)-1] = '1'
		} else {
			r[len(r)-1] = '0'
		}
		c.Days[0].Rows[0].Leaf = string(r)
		return c
	}
	for _, engine := range storetest.Engines() {
		f := newFixture(t, engine)
		trickyRows(t, f)
		if _, err := publisher.Publish(context.Background(), f.options(), clock("2026-11-22T00:10:00Z")); err != nil {
			t.Fatal(err)
		}
		doc := vectors(t, f)
		if len(doc.Days) != 3 {
			t.Fatalf("published %d days, want three", len(doc.Days))
		}
		if n := doc.Days[1].Count; n != 0 {
			t.Fatalf("the second day has %d rows, and none were stored on it", n)
		}
		tests := []struct {
			name string
			doc  vectorsFile
			want int
		}{
			{name: "the published days, their rows in day order and every row's proof pass tools/ledger_ref.py check", doc: doc, want: 0},
			{name: "tools/ledger_ref.py check fails the same file with one leaf's last byte changed", doc: tamper(doc), want: 1},
		}
		for _, tt := range tests {
			t.Run(string(engine)+"/"+tt.name, func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "days.json")
				data, err := json.Marshal(tt.doc)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, data, 0o600); err != nil {
					t.Fatal(err)
				}
				out, err := exec.Command(python, "-I", ref, "check", path).CombinedOutput()
				code := 0
				if err != nil {
					var exit *exec.ExitError
					if !errors.As(err, &exit) {
						t.Fatalf("running tools/ledger_ref.py: %v", err)
					}
					code = exit.ExitCode()
				}
				if code != tt.want {
					t.Fatalf("tools/ledger_ref.py check exited %d, want %d:\n%s", code, tt.want, out)
				}
			})
		}
	}
}

func TestProof(t *testing.T) {
	deleteRows := func(devices ...string) func(t *testing.T, f *fixture) {
		return func(t *testing.T, f *fixture) {
			for _, d := range devices {
				f.exec(t, `DELETE FROM witness_rows WHERE device_id = $1`, d)
			}
		}
	}
	tests := []struct {
		name string
		// rows are stored, then the days due at 2026-11-20T00:10Z are
		// published, then after changes what the server holds.
		rows      [][2]string
		after     func(t *testing.T, f *fixture)
		device    string
		b         uint64
		wantIndex uint64
		wantPath  int
		wantIs    error
		wantErr   string
	}{
		{name: "the one batch of a day is leaf 0, with an empty proof", rows: [][2]string{{"d1", "2026-11-19T08:00:00Z"}}, device: "d1", b: 1},
		{name: "a batch's leaf follows the day order, with a proof of one hash a level",
			rows:   [][2]string{{"d1", "2026-11-19T08:00:00Z"}, {"d2", "2026-11-19T07:00:00Z"}, {"d3", "2026-11-19T09:00:00Z"}},
			device: "d1", b: 1, wantIndex: 1, wantPath: 2},
		{name: "a batch keeps its proof after the day's other witness rows are deleted, as retention or an account's erasure deletes them",
			rows:   [][2]string{{"d1", "2026-11-19T08:00:00Z"}, {"d2", "2026-11-19T07:00:00Z"}, {"d3", "2026-11-19T09:00:00Z"}},
			after:  deleteRows("d2", "d3"),
			device: "d1", b: 1, wantIndex: 1, wantPath: 2},
		{name: "a batch whose day has no head yet has no proof yet",
			rows:   [][2]string{{"d1", "2026-11-19T08:00:00Z"}},
			after:  func(t *testing.T, f *fixture) { f.row(t, "d2", 1, "2026-11-20T08:00:00Z") },
			device: "d2", b: 1, wantIs: publisher.ErrNotPublished},
		{name: "a batch the server holds no row of has no proof",
			rows: [][2]string{{"d1", "2026-11-19T08:00:00Z"}}, device: "d9", b: 1, wantIs: publisher.ErrNoBatch},
		{name: "a batch stored on a day after that day was published isn't in its tree, so it has no proof",
			rows:   [][2]string{{"d1", "2026-11-19T08:00:00Z"}},
			after:  func(t *testing.T, f *fixture) { f.row(t, "d2", 1, "2026-11-19T23:00:00Z") },
			device: "d2", b: 1, wantIs: publisher.ErrNotInTree},
		{name: "a batch stored late on a published day leaves the day's other batches their proofs",
			rows:   [][2]string{{"d1", "2026-11-19T08:00:00Z"}},
			after:  func(t *testing.T, f *fixture) { f.row(t, "d2", 1, "2026-11-19T23:00:00Z") },
			device: "d1", b: 1},
		{name: "stored leaves that don't give the head's root give no proof",
			rows: [][2]string{{"d1", "2026-11-19T08:00:00Z"}},
			after: func(t *testing.T, f *fixture) {
				f.exec(t, `UPDATE day_leaves SET leaves = $1 WHERE day = $2`, make([]byte, ledger.HashSize), int64(day("2026-11-19")))
			},
			device: "d1", b: 1, wantErr: "stored leaves give the root"},
	}
	for _, engine := range storetest.Engines() {
		for _, tt := range tests {
			t.Run(string(engine)+"/"+tt.name, func(t *testing.T) {
				f := newFixture(t, engine)
				for _, r := range tt.rows {
					f.row(t, r[0], 1, r[1])
				}
				if _, err := publisher.Publish(context.Background(), f.options(), clock("2026-11-20T00:10:00Z")); err != nil {
					t.Fatal(err)
				}
				if tt.after != nil {
					tt.after(t, f)
				}
				in, err := publisher.Proof(context.Background(), f.db.Read, tt.device, tt.b)
				switch {
				case tt.wantIs != nil || tt.wantErr != "":
					if err == nil || (tt.wantIs != nil && !errors.Is(err, tt.wantIs)) || !strings.Contains(err.Error(), tt.wantErr) {
						t.Fatalf("Proof error %v, want %v %q", err, tt.wantIs, tt.wantErr)
					}
					return
				case err != nil:
					t.Fatal(err)
				}
				if in.Index != tt.wantIndex || len(in.Path) != tt.wantPath || in.Path == nil {
					t.Errorf("Proof gives leaf %d with %d hashes, want leaf %d with %d", in.Index, len(in.Path), tt.wantIndex, tt.wantPath)
				}
				leaf, err := ledger.DayRow{DeviceID: tt.device, B: tt.b, Head: standIn(tt.device, tt.b), ReceivedAt: uint64(at(rowTime(tt.rows, tt.device)))}.Leaf()
				if err != nil {
					t.Fatal(err)
				}
				if err := ledger.VerifyInclusion(in.Index, in.Day.Count, ledger.LeafHash(leaf), in.Path, in.Day.Root); err != nil {
					t.Errorf("the proof doesn't verify against the stored head: %v", err)
				}
			})
		}
	}
}

// rowTime returns when the row of device in rows was received.
func rowTime(rows [][2]string, device string) string {
	for _, r := range rows {
		if r[0] == device {
			return r[1]
		}
	}
	return ""
}

// TestWitnessSidecar runs deploy/witness/push.sh, the witness sidecar's
// loop, one round at a time with PAIRTIME_WITNESS_ONCE=1 over the files the
// publisher writes, against a bare repository through a file:// remote.
func TestWitnessSidecar(t *testing.T) {
	for _, tool := range []string{"sh", "git"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Fatalf("%s is missing, and this test runs the witness sidecar's script with it: %v", tool, err)
		}
	}
	script, err := filepath.Abs(filepath.Join("..", "..", "..", "deploy", "witness", "push.sh"))
	if err != nil {
		t.Fatal(err)
	}
	type round struct {
		// publishAt runs the publisher first, at that time.
		publishAt string
		// before changes the directory after the publisher ran.
		before func(t *testing.T, dir string)
	}
	tests := []struct {
		name   string
		rounds []round
		// want are the dates whose files the repository holds, byte for
		// byte what the publisher wrote, and wantCommits how many commits
		// main has.
		want        []string
		wantCommits int
	}{
		{name: "a round pushes every day's file, and leaves out the dot-file of a root still being written",
			rounds: []round{{publishAt: "2026-11-22T00:10:00Z", before: func(t *testing.T, dir string) {
				if err := os.WriteFile(filepath.Join(dir, "2026", ".2026-11-22.json.tmp"), []byte("{\"da"), 0o644); err != nil {
					t.Fatal(err)
				}
			}}},
			want: []string{"2026-11-19", "2026-11-20", "2026-11-21"}, wantCommits: 1},
		{name: "the next day's file goes in a commit of its own, and the files published before stay as they were",
			rounds:      []round{{publishAt: "2026-11-21T00:10:00Z"}, {publishAt: "2026-11-22T00:10:00Z"}},
			want:        []string{"2026-11-19", "2026-11-20", "2026-11-21"},
			wantCommits: 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t, store.SQLite)
			f.row(t, "d1", 1, "2026-11-19T12:00:00Z")
			f.row(t, "d1", 2, "2026-11-21T12:00:00Z")
			tmp := t.TempDir()
			remote, work := filepath.Join(tmp, "remote.git"), filepath.Join(tmp, "repo")
			env := []string{
				"PATH=" + os.Getenv("PATH"),
				"GIT_CONFIG_NOSYSTEM=1",
				"GIT_CONFIG_GLOBAL=" + os.DevNull,
				"PAIRTIME_WITNESS_REPO=some-du6e/pairtime-witness",
				"PAIRTIME_WITNESS_REMOTE=file://" + filepath.ToSlash(remote),
				"PAIRTIME_WITNESS_SRC=" + f.dir,
				"PAIRTIME_WITNESS_WORK=" + work,
				"PAIRTIME_WITNESS_ONCE=1",
			}
			git := func(args ...string) string {
				t.Helper()
				cmd := exec.Command("git", args...)
				cmd.Env = env
				out, err := cmd.CombinedOutput()
				if err != nil {
					t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
				}
				return string(out)
			}
			git("init", "--quiet", "--bare", remote)
			for i, r := range tt.rounds {
				if _, err := publisher.Publish(context.Background(), f.options(), clock(r.publishAt)); err != nil {
					t.Fatal(err)
				}
				if r.before != nil {
					r.before(t, f.dir)
				}
				cmd := exec.Command("sh", script)
				cmd.Env = env
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("round %d of push.sh failed: %v\n%s", i+1, err, out)
				}
			}
			names := strings.Fields(git("--git-dir="+remote, "ls-tree", "-r", "--name-only", "main"))
			var want []string
			for _, date := range tt.want {
				want = append(want, publisher.FileName(day(date)))
			}
			if !slices.Equal(names, want) {
				t.Fatalf("the repository holds %v, want %v", names, want)
			}
			for _, date := range tt.want {
				if got := git("--git-dir="+remote, "show", "main:"+publisher.FileName(day(date))); !bytes.Equal([]byte(got), f.file(t, date)) {
					t.Errorf("the repository's %s is\n%s\nand the publisher wrote\n%s", date, got, f.file(t, date))
				}
			}
			if n, err := strconv.Atoi(strings.TrimSpace(git("--git-dir="+remote, "rev-list", "--count", "main"))); err != nil || n != tt.wantCommits {
				t.Errorf("main has %d commits (%v), want %d", n, err, tt.wantCommits)
			}
		})
	}
}
