// SPDX-License-Identifier: AGPL-3.0-or-later

package git

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/pkg/event"
)

// flush waits until the worker has done everything queued before it.
func (w *Watcher) flush() {
	done := make(chan struct{})
	w.work <- func() { close(done) }
	<-done
}

type testSink chan Change

func (s testSink) put(c Change) { s <- c }

// next waits up to d for a change.
func (s testSink) next(t *testing.T, d time.Duration) Change {
	t.Helper()
	select {
	case c := <-s:
		return c
	case <-time.After(d):
		t.Fatalf("no change reached the sink within %v", d)
		return Change{}
	}
}

// none checks that nothing reached the sink.
func (s testSink) none(t *testing.T) {
	t.Helper()
	select {
	case c := <-s:
		t.Fatalf("got a %s change, want none", c.Op())
	default:
	}
}

func (g *gitEnv) watcher(res *Resolver, sink testSink, opts WatcherOptions) *Watcher {
	g.t.Helper()
	opts.Resolver = res
	opts.Sink = sink.put
	w, err := NewWatcher(opts)
	if err != nil {
		g.t.Fatal(err)
	}
	g.t.Cleanup(func() { w.Close() })
	return w
}

func track(t *testing.T, w *Watcher, dir string) Location {
	t.Helper()
	loc, err := w.Track(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	w.flush() // the first scan, which sets the cursor
	return loc
}

func TestWatcher(t *testing.T) {
	cases := []struct {
		name string
		run  func(t *testing.T, g *gitEnv)
	}{
		{
			name: "a commit in a tracked repo reaches the sink through file notifications, with no poll",
			run: func(t *testing.T, g *gitEnv) {
				g.realDates = true
				r := g.initRepo()
				r.commit("one", map[string]string{"a.txt": "a\n"})
				sink := make(testSink, 16)
				w := g.watcher(g.resolver(ProjectMap{}, Excludes{}), sink, WatcherOptions{Hold: 50 * time.Millisecond, Debounce: 50 * time.Millisecond})
				if p := w.Problems()["notify"]; p != "" {
					t.Skip(p)
				}
				loc := track(t, w, r.dir)
				sha := r.commit("two", map[string]string{"a.txt": "b\n"})
				c := sink.next(t, 20*time.Second)
				if c.Op() != event.GitCommit || len(c.Commits) != 1 || c.Commits[0].SHA != sha || c.Event.ProjectKey != loc.Project {
					t.Errorf("got %s %v in %s, want the commit %s in %s", c.Op(), shas(c.Commits), c.Event.ProjectKey, sha, loc.Project)
				}
			},
		},
		{
			name: "an agent's git commit reported before the scan makes the commit the agent's",
			run: func(t *testing.T, g *gitEnv) {
				g.realDates = true
				r := g.initRepo()
				r.commit("one", map[string]string{"a.txt": "a\n"})
				sink := make(testSink, 16)
				w := g.watcher(g.resolver(ProjectMap{}, Excludes{}), sink, WatcherOptions{NoNotify: true, Hold: time.Minute})
				track(t, w, r.dir)
				r.commit("by the agent", map[string]string{"a.txt": "b\n"})
				w.NoteAgentCommand(time.Now(), r.dir, `git add -A && git commit -m "by the agent"`)
				w.Poll()
				w.flush()
				c := sink.next(t, time.Second)
				if gitChange(t, c).Actor != event.ActorAgent || c.Commits[0].Actor != event.ActorAgent {
					t.Errorf("actor %s and %s, want agent", gitChange(t, c).Actor, c.Commits[0].Actor)
				}
			},
		},
		{
			name: "an agent's git commit reported while the change is held makes it the agent's",
			run: func(t *testing.T, g *gitEnv) {
				g.realDates = true
				r := g.initRepo()
				r.commit("one", map[string]string{"a.txt": "a\n"})
				sink := make(testSink, 16)
				w := g.watcher(g.resolver(ProjectMap{}, Excludes{}), sink, WatcherOptions{NoNotify: true, Hold: time.Minute})
				track(t, w, r.dir)
				r.commit("by the agent", map[string]string{"a.txt": "b\n"})
				w.Poll()
				w.flush()
				sink.none(t)
				w.NoteAgentCommand(time.Now(), r.dir, `git commit -am "by the agent"`)
				w.flush()
				if c := sink.next(t, time.Second); c.Commits[0].Actor != event.ActorAgent {
					t.Errorf("actor %s, want agent", c.Commits[0].Actor)
				}
			},
		},
		{
			name: "a commit no agent command explains goes out as the person's once the hold is over",
			run: func(t *testing.T, g *gitEnv) {
				g.realDates = true
				r := g.initRepo()
				r.commit("one", map[string]string{"a.txt": "a\n"})
				sink := make(testSink, 16)
				w := g.watcher(g.resolver(ProjectMap{}, Excludes{}), sink, WatcherOptions{NoNotify: true, Hold: 100 * time.Millisecond})
				track(t, w, r.dir)
				r.commit("by hand", map[string]string{"a.txt": "b\n"})
				w.Poll()
				if c := sink.next(t, 10*time.Second); c.Commits[0].Actor != event.ActorHuman {
					t.Errorf("actor %s, want human", c.Commits[0].Actor)
				}
			},
		},
		{
			name: "an agent's git commit in another repo leaves the commit the person's",
			run: func(t *testing.T, g *gitEnv) {
				g.realDates = true
				r := g.initRepo()
				r.commit("one", map[string]string{"a.txt": "a\n"})
				other := g.initRepo()
				other.commit("other", map[string]string{"o.txt": "o\n"})
				sink := make(testSink, 16)
				w := g.watcher(g.resolver(ProjectMap{}, Excludes{}), sink, WatcherOptions{NoNotify: true, Hold: 200 * time.Millisecond})
				track(t, w, r.dir)
				r.commit("by hand", map[string]string{"a.txt": "b\n"})
				w.NoteAgentCommand(time.Now(), other.dir, `git commit -m x`)
				w.Poll()
				if c := sink.next(t, 10*time.Second); c.Commits[0].Actor != event.ActorHuman {
					t.Errorf("actor %s, want human", c.Commits[0].Actor)
				}
			},
		},
		{
			name: "an ignored folder is never tracked, and its commits reach no sink",
			run: func(t *testing.T, g *gitEnv) {
				r := g.initRepo()
				r.commit("one", map[string]string{"a.txt": "a\n", IgnoreFile: ""})
				sink := make(testSink, 16)
				w := g.watcher(g.resolver(ProjectMap{}, Excludes{}), sink, WatcherOptions{NoNotify: true, Hold: time.Millisecond})
				if _, err := w.Track(context.Background(), r.dir); !errors.Is(err, ErrIgnored) {
					t.Fatalf("Track: %v, want ErrIgnored", err)
				}
				r.commit("two", map[string]string{"a.txt": "b\n"})
				w.Poll()
				w.flush()
				time.Sleep(20 * time.Millisecond)
				w.flush()
				sink.none(t)
				if n := len(w.State().Worktrees); n != 0 {
					t.Errorf("the watcher keeps %d worktrees, want none", n)
				}
			},
		},
		{
			name: "a .pairtime-ignore added to a tracked repo stops the watch at the next poll: the commit after it reaches no sink, and nothing of the repo is kept",
			run: func(t *testing.T, g *gitEnv) {
				r := g.initRepo()
				r.commit("one", map[string]string{"a.txt": "a\n"})
				sink := make(testSink, 16)
				w := g.watcher(g.resolver(ProjectMap{}, Excludes{}), sink, WatcherOptions{NoNotify: true, Hold: time.Millisecond})
				track(t, w, r.dir)
				r.write(map[string]string{IgnoreFile: ""})
				r.commit("two", map[string]string{"a.txt": "b\n"})
				w.Poll()
				w.flush()
				time.Sleep(20 * time.Millisecond)
				w.flush()
				sink.none(t)
				if n := len(w.State().Worktrees); n != 0 {
					t.Errorf("the watcher keeps %d worktrees, want none", n)
				}
				if _, err := w.Track(context.Background(), r.dir); !errors.Is(err, ErrIgnored) {
					t.Errorf("Track again: %v, want ErrIgnored", err)
				}
			},
		},
		{
			name: "a change held for an agent never goes out once its repo turns ignored, not even at Close",
			run: func(t *testing.T, g *gitEnv) {
				r := g.initRepo()
				r.commit("one", map[string]string{"a.txt": "a\n"})
				sink := make(testSink, 16)
				w := g.watcher(g.resolver(ProjectMap{}, Excludes{}), sink, WatcherOptions{NoNotify: true, Hold: time.Minute})
				track(t, w, r.dir)
				r.commit("two", map[string]string{"a.txt": "b\n"})
				w.Poll()
				w.flush()
				r.write(map[string]string{IgnoreFile: ""})
				w.Poll()
				w.flush()
				w.Close()
				sink.none(t)
			},
		},
		{
			name: "a .pairtime-ignore that comes with a commit stops the scan that commit's notification starts",
			run: func(t *testing.T, g *gitEnv) {
				g.realDates = true
				r := g.initRepo()
				r.commit("one", map[string]string{"a.txt": "a\n"})
				sink := make(testSink, 16)
				w := g.watcher(g.resolver(ProjectMap{}, Excludes{}), sink, WatcherOptions{Hold: 50 * time.Millisecond, Debounce: 50 * time.Millisecond})
				if p := w.Problems()["notify"]; p != "" {
					t.Skip(p)
				}
				track(t, w, r.dir)
				r.commit("two", map[string]string{"a.txt": "b\n", IgnoreFile: ""})
				deadline := time.Now().Add(20 * time.Second)
				for len(w.State().Worktrees) != 0 && time.Now().Before(deadline) {
					time.Sleep(20 * time.Millisecond)
				}
				if n := len(w.State().Worktrees); n != 0 {
					t.Fatalf("the watcher still keeps %d worktrees, want none", n)
				}
				w.Close()
				sink.none(t)
			},
		},
		{
			name: "a linked worktree that is removed stops being watched",
			run: func(t *testing.T, g *gitEnv) {
				r := g.initRepo()
				r.commit("one", map[string]string{"a.txt": "a\n"})
				wt := filepath.Join(t.TempDir(), "wt")
				r.git("worktree", "add", "-q", wt, "-b", "wt")
				sink := make(testSink, 16)
				w := g.watcher(g.resolver(ProjectMap{}, Excludes{}), sink, WatcherOptions{NoNotify: true})
				track(t, w, r.dir)
				track(t, w, wt)
				if n := len(w.State().Worktrees); n != 2 {
					t.Fatalf("watching %d worktrees, want 2", n)
				}
				r.git("worktree", "remove", wt)
				w.Poll()
				w.flush()
				if n := len(w.State().Worktrees); n != 1 {
					t.Errorf("watching %d worktrees after the removal, want 1", n)
				}
			},
		},
		{
			name: "a watcher started from saved state reports the commits made while none ran",
			run: func(t *testing.T, g *gitEnv) {
				r := g.initRepo()
				r.commit("one", map[string]string{"a.txt": "a\n"})
				res := g.resolver(ProjectMap{}, Excludes{})
				sink := make(testSink, 16)
				w := g.watcher(res, sink, WatcherOptions{NoNotify: true, Hold: time.Millisecond})
				track(t, w, r.dir)
				st := w.State()
				w.Close()
				sha := r.commit("while off", map[string]string{"a.txt": "b\n"})
				w2 := g.watcher(g.resolver(res.Map(), Excludes{}), sink, WatcherOptions{NoNotify: true, Hold: time.Millisecond, State: st})
				track(t, w2, r.dir)
				c := sink.next(t, 10*time.Second)
				if c.Op() != event.GitCommit || c.Commits[0].SHA != sha {
					t.Errorf("got %s %v, want the commit %s", c.Op(), shas(c.Commits), sha)
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.run(t, newGitEnv(t))
		})
	}
}

func TestRelevant(t *testing.T) {
	cases := []struct {
		name string
		file string
		want bool
	}{
		{"HEAD moving wakes the watch", "HEAD", true},
		{"a lock file for HEAD wakes the watch", "HEAD.lock", true},
		{"the reflog's folder wakes the watch", "logs", true},
		{"ORIG_HEAD wakes the watch", "ORIG_HEAD", true},
		{"the index wakes the watch, though only a signature change scans", "index.lock", true},
		{"a rebase's state folder wakes the watch", "rebase-merge", true},
		{"FETCH_HEAD doesn't", "FETCH_HEAD", false},
		{"COMMIT_EDITMSG doesn't", "COMMIT_EDITMSG", false},
		{"objects don't", "objects", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := relevant(filepath.Join("repo", ".git", tc.file)); got != tc.want {
				t.Errorf("relevant(%q) = %v, want %v", tc.file, got, tc.want)
			}
		})
	}
}
