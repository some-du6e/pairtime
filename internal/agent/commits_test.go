// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/adapters/git"
	"github.com/some-du6e/pairtime/internal/agent/home"
	"github.com/some-du6e/pairtime/internal/agent/ledger"
	"github.com/some-du6e/pairtime/pkg/event"
	pkgledger "github.com/some-du6e/pairtime/pkg/ledger"
)

// tierOneCommit lists the fields of DESIGN 4.3's tier-1 commit record that
// a line of commits/ may hold: git.Commit's exported ones. The scorer adds
// credited_seconds_since_parent, and path_hmacs wait for the git adapter.
var tierOneCommit = []string{"sha", "parents", "project", "observed_at", "author_time", "committer_time",
	"files_changed", "insertions", "deletions", "rewrites", "actor", "agent_trailer"}

// readCommitLines reads the records in the file of day in commits/, in
// order.
func readCommitLines(t *testing.T, dir, day string) []map[string]any {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, day+".jsonl"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var out []map[string]any
	for _, l := range bytes.Split(b, []byte("\n")) {
		if len(bytes.TrimSpace(l)) == 0 {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal(l, &m); err != nil {
			t.Fatalf("%s holds a line that isn't JSON: %q", day, l)
		}
		out = append(out, m)
	}
	return out
}

// TestCommitStore checks commits/, where the commit records of DESIGN 4.3
// wait for the uploader (D-commit-records-local).
func TestCommitStore(t *testing.T) {
	now := time.Date(2026, 11, 21, 4, 0, 0, 0, time.UTC)
	commit := func(sha string, observed time.Time) git.Commit {
		return git.Commit{SHA: sha, Parents: []string{strings.Repeat("0", 40)}, Project: "g0123456789abcdef",
			ObservedAt: observed.UnixMilli(), AuthorTime: observed.Unix() - 60, CommitterTime: observed.Unix() - 30,
			FilesChanged: 2, Insertions: 10, Deletions: 3, Rewrites: []git.Rewrite{{OldSHA: strings.Repeat("a", 40), NewSHA: sha}},
			Actor: event.ActorAgent, AgentTrailer: "claude",
			Local: git.CommitLocal{Op: event.GitCommit, Origin: git.MadeHere, AuthorTZ: "-0600", CommitterTZ: "+0530",
				ReflogTime: observed.Unix(), AI: &git.AINote{Schema: "authorship/3.0.0", Tools: []string{"claude"}, AILines: 7}}}
	}
	day := func(t time.Time) string { return t.UTC().Format(time.DateOnly) }
	cases := []struct {
		name string
		run  func(t *testing.T, s *commitStore)
	}{
		{"each record goes to the file of its observed_at's UTC day, a line each, in the order given", func(t *testing.T, s *commitStore) {
			late := time.Date(2026, 11, 20, 23, 59, 0, 0, time.UTC)
			a, b, c := commit(strings.Repeat("1", 40), late), commit(strings.Repeat("2", 40), now), commit(strings.Repeat("3", 40), now)
			if err := s.add([]git.Commit{a, b, c}, now); err != nil {
				t.Fatal(err)
			}
			got := func(d string) []string {
				var shas []string
				for _, m := range readCommitLines(t, s.dir, d) {
					sha, _ := m["sha"].(string)
					shas = append(shas, sha)
				}
				return shas
			}
			if g := got(day(late)); !slices.Equal(g, []string{a.SHA}) {
				t.Errorf("%s holds %q, want the commit observed that day", day(late), g)
			}
			if g := got(day(now)); !slices.Equal(g, []string{b.SHA, c.SHA}) {
				t.Errorf("%s holds %q, want the two observed that day, in order", day(now), g)
			}
		}},
		{"a line holds DESIGN 4.3's tier-1 fields and nothing of the commit's local part", func(t *testing.T, s *commitStore) {
			if err := s.add([]git.Commit{commit(strings.Repeat("1", 40), now)}, now); err != nil {
				t.Fatal(err)
			}
			raw, err := os.ReadFile(filepath.Join(s.dir, day(now)+".jsonl"))
			if err != nil {
				t.Fatal(err)
			}
			lines := readCommitLines(t, s.dir, day(now))
			if len(lines) != 1 {
				t.Fatalf("%d lines, want 1", len(lines))
			}
			for k := range lines[0] {
				if !slices.Contains(tierOneCommit, k) {
					t.Errorf("the line has %q, which isn't a tier-1 field", k)
				}
			}
			for _, local := range []string{"-0600", "+0530", "authorship", "here"} {
				if bytes.Contains(raw, []byte(local)) {
					t.Errorf("the line holds %q, of the commit's local part", local)
				}
			}
		}},
		{"a record with no observed_at goes to the file of the day it was kept", func(t *testing.T, s *commitStore) {
			c := commit(strings.Repeat("1", 40), now)
			c.ObservedAt = 0
			if err := s.add([]git.Commit{c}, now); err != nil {
				t.Fatal(err)
			}
			if n := len(readCommitLines(t, s.dir, day(now))); n != 1 {
				t.Errorf("%s holds %d lines, want 1", day(now), n)
			}
		}},
		{"a day's file goes once it is 180 days old, and younger days' files and other files stay", func(t *testing.T, s *commitStore) {
			if err := os.MkdirAll(s.dir, 0o700); err != nil {
				t.Fatal(err)
			}
			old, kept := day(now.Add(-181*24*time.Hour)), day(now.Add(-179*24*time.Hour))
			for _, name := range []string{old + ".jsonl", kept + ".jsonl", "notes.txt"} {
				if err := os.WriteFile(filepath.Join(s.dir, name), []byte("\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			s.prune(now)
			for name, want := range map[string]bool{old + ".jsonl": false, kept + ".jsonl": true, "notes.txt": true} {
				if _, err := os.Stat(filepath.Join(s.dir, name)); (err == nil) != want {
					t.Errorf("%s: there %v, want %v (%v)", name, err == nil, want, err)
				}
			}
		}},
		{"prune looks once a UTC day", func(t *testing.T, s *commitStore) {
			if err := os.MkdirAll(s.dir, 0o700); err != nil {
				t.Fatal(err)
			}
			s.prune(now)
			old := filepath.Join(s.dir, day(now.Add(-200*24*time.Hour))+".jsonl")
			if err := os.WriteFile(old, []byte("\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			s.prune(now.Add(time.Hour))
			if _, err := os.Stat(old); err != nil {
				t.Errorf("a second prune the same day deleted it (%v)", err)
			}
			s.prune(now.Add(24 * time.Hour))
			if _, err := os.Stat(old); !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("the next day's prune left it (%v)", err)
			}
		}},
		{"the folder and its files are this user's alone", func(t *testing.T, s *commitStore) {
			if runtime.GOOS == "windows" {
				t.Skip("the data directory's DACL decides on Windows")
			}
			if err := s.add([]git.Commit{commit(strings.Repeat("1", 40), now)}, now); err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{s.dir, filepath.Join(s.dir, day(now)+".jsonl")} {
				fi, err := os.Stat(name)
				if err != nil {
					t.Fatal(err)
				}
				if perm := fi.Mode().Perm(); perm&0o077 != 0 {
					t.Errorf("%s has mode %v", filepath.Base(name), perm)
				}
			}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			c.run(t, &commitStore{dir: filepath.Join(t.TempDir(), "commits")})
		})
	}
}

// TestGitSinkKeepsCommitLines checks when the git host's sink keeps a
// change's commit records in commits/ (gitHost.sink): only once the ledger
// has taken its git.change, as the uploader judges a commit by the batches
// sealed after it was observed. A change the ledger drops as made in a
// local-only stretch that a batch sealed online has passed
// (ledger.ErrLateLocal) leaves none, and so does one the ledger refuses.
func TestGitSinkKeepsCommitLines(t *testing.T) {
	const sha = "1a2b3c4d5e6f7a8b9c0d1e2f3a4b5c6d7e8f9a0b"
	// change is a commit's change made at made, which its scan found then,
	// with no monotonic reading, so the writer judges it by its t.
	change := func(made time.Time) git.Change {
		ms := made.UnixMilli()
		return git.Change{
			Event: event.Event{Envelope: event.Envelope{T: ms, Source: event.SourceGit, Harness: event.HarnessNone, SessionKey: event.NoSession},
				Data: &event.GitChange{Op: event.GitCommit, SHA: sha, FilesChanged: 1, Insertions: 1, Actor: event.ActorHuman}},
			Found: time.UnixMilli(ms),
			Commits: []git.Commit{{SHA: sha, Project: "d0123456789abcdef0123456789abcdef", ObservedAt: ms,
				AuthorTime: ms / 1000, CommitterTime: ms / 1000, FilesChanged: 1, Insertions: 1, Actor: event.ActorHuman}},
		}
	}
	cases := []struct {
		name string
		// writer opens dirs' ledger writer as the case has it, and returns
		// when the change was made.
		writer func(t *testing.T, dirs home.Dirs) (*ledger.Writer, time.Time)
		// kept is whether commits/ holds the change's record and the ledger
		// its git.change, and problem the source of the problem the sink
		// sets, "" for none.
		kept    bool
		problem string
	}{
		{"a change the ledger takes keeps its commit record, after its git.change",
			func(t *testing.T, dirs home.Dirs) (*ledger.Writer, time.Time) { return testWriter(t, dirs), time.Now() }, true, ""},
		{"a change the ledger drops as made in a local-only stretch that a batch sealed online has passed keeps none",
			func(t *testing.T, dirs home.Dirs) (*ledger.Writer, time.Time) {
				// A stretch from the install to 60 s in, and a batch sealed
				// online after it, which passes it, as in TestReplayHeld.
				t0 := time.Now().Add(-10 * time.Minute)
				var at atomic.Int64
				var local atomic.Bool
				at.Store(t0.UnixMilli())
				local.Store(true)
				w := optionsWriter(t, dirs, func(o *ledger.Options) {
					o.Now = func() time.Time { return time.UnixMilli(at.Load()) }
					o.Online = func() bool { return !local.Load() }
				})
				for _, step := range []struct {
					ms    int64
					local bool
				}{{2000, true}, {60_000, false}, {120_000, false}} {
					local.Store(step.local)
					at.Store(t0.UnixMilli() + step.ms)
					if _, err := w.Write(presenceAt(t0.Add(time.Duration(step.ms-1000) * time.Millisecond))); err != nil {
						t.Fatal(err)
					}
					sealNow(t, w, dirs.Ledger())
				}
				return w, t0.Add(30 * time.Second)
			}, false, ""},
		{"a change the ledger refuses, as once the writer has closed, keeps none",
			func(t *testing.T, dirs home.Dirs) (*ledger.Writer, time.Time) {
				w := testWriter(t, dirs)
				if err := w.Close(); err != nil {
					t.Fatal(err)
				}
				return w, time.Now()
			}, false, "git.ledger"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a, dirs := modesAgent(t, "online")
			w, made := c.writer(t, dirs)
			g := &gitHost{a: a, writer: w, commits: &commitStore{dir: dirs.Commits()}}
			g.sink(change(made))
			if err := w.Close(); err != nil && !errors.Is(err, ledger.ErrClosed) {
				t.Fatal(err)
			}
			lines := readCommitLines(t, dirs.Commits(), made.UTC().Format(time.DateOnly))
			changes := 0
			if err := ledger.ReadBatches(dirs.Ledger(), 1, func(b ledger.Batch) error {
				for _, rec := range b.Records {
					body, err := pkgledger.ParseBody(rec.Body)
					if err != nil {
						return err
					}
					if e, err := body.Event(); err == nil && e.Kind() == event.KindGitChange {
						changes++
					}
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			kept := len(lines) == 1 && lines[0]["sha"] == sha && changes == 1
			none := len(lines) == 0 && changes == 0
			if c.kept && !kept || !c.kept && !none {
				t.Errorf("commits/ holds %d records and the ledger %d git.change records; want the change's record kept %v", len(lines), changes, c.kept)
			}
			if got := a.currentProblems()[c.problem]; c.problem != "" && got == "" {
				t.Errorf("no %s problem: %v", c.problem, a.currentProblems())
			}
		})
	}
}
