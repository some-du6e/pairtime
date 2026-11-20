// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/some-du6e/pairtime/internal/adapters"
	"github.com/some-du6e/pairtime/internal/adapters/git"
	"github.com/some-du6e/pairtime/internal/agent/ledger"
	"github.com/some-du6e/pairtime/internal/agent/sched"
	"github.com/some-du6e/pairtime/pkg/event"
)

// The git adapter's keys in state.json: the resolver's project map, which
// keeps a project's key across a restart even when its root commits have
// changed since (DESIGN 3.5), and the watcher's cursors.
const (
	projectsKey = "projects"
	gitwatchKey = "gitwatch"
)

// maxResting bounds the folders that rest at once (trackRest).
const maxResting = 256

// gitPollEvery is how often the watcher looks at the repos it watches
// with a few stats each, which finds what file notifications missed
// (DESIGN 3.3; git.Watcher.Poll).
const gitPollEvery = time.Minute

// trackWait bounds how long a call waits for its folder to resolve to a
// project. A folder resolves in a few git calls, well within it, and once
// resolved its answer is cached. The first sight of a big repo can take
// minutes, walking its whole history for the root commits; its calls then
// go on without a project, and the walk goes on without them, for up to
// trackLimit, so the session's later calls find its answer cached.
// A folder whose resolve ran out of trackLimit isn't cached, and the
// resolver would walk the history again for its next call; it sits out
// trackRest first.
const (
	trackWait  = 3 * time.Second
	trackLimit = 5 * time.Minute
	trackRest  = 10 * time.Minute
)

// gitHost is the git adapter in the agent (DESIGN 5.8; PLAN WP-AD5,
// WP-AG7). Its resolver turns each hooked session's folder into a project
// once (git.Resolver caches the answer and looks again with a few stats),
// and Track starts watching the folder's repo: its .git, never its
// working tree. A git command an agent's bash tool ran marks the commit it
// made as the agent's (NoteAgentCommand), and every change the watcher
// finds becomes a git.change record in the ledger. Every repo it watches
// is one a session worked in; editors' project folders join when their
// heartbeats arrive (WP-AD16).
//
// The commit records of DESIGN 4.3 that each change also carries
// (git.Change.Commits) wait for the uploader (WP-AG5), which decides where
// they are kept until they go; the ledger keeps the git.change.
type gitHost struct {
	a   *agent
	res *git.Resolver
	w   *git.Watcher
	job *sched.Job

	// mu orders a change's record, and the seal that puts it on disk,
	// before the watcher state that says it went (put): the cursor never
	// moves past a change a crash could still cut from the ledger.
	mu      sync.Mutex
	writer  *ledger.Writer
	changes uint64 // git.change records written
	saved   []byte // the project map and cursors as last put in state.json
	// tracked are the git directories of the worktrees sessions worked in
	// this run, which the watcher reads, and slow the folders whose first
	// resolve outlasted trackWait and goes on (project).
	tracked map[string]bool
	slow    map[string]bool
	rest    map[string]time.Time // folders whose resolve timed out, until when they rest
	// logged are the watcher's own problems as last logged.
	logged map[string]string
}

// gitReply is the git adapter's part of GET /v1/status.
type gitReply struct {
	// Watched counts the worktrees sessions worked in this run whose
	// reflog the watcher has read.
	Watched int `json:"watched"`
	// Changes counts the git.change records written since the start.
	Changes uint64 `json:"changes"`
}

// gitState is what state.json keeps of the git adapter, as put compares
// it.
type gitState struct {
	Projects git.ProjectMap   `json:"projects"`
	Watch    git.WatcherState `json:"watch"`
}

// projectSecret derives the git adapter's K_project from K_dev
// (ledger-v1 3).
func projectSecret(kDev []byte) ([]byte, error) { return git.ProjectSecret(kDev) }

// newGitHost starts the git adapter with the project secret and the
// ledger writer, from what state.json kept.
func newGitHost(a *agent, secret []byte, w *ledger.Writer) (*gitHost, error) {
	var m git.ProjectMap
	var ws git.WatcherState
	if a.state != nil {
		if _, err := a.state.Get(projectsKey, &m); err != nil {
			a.log.Warn("the project map in state.json doesn't read, so projects are worked out afresh", "err", err)
			m = git.ProjectMap{}
		}
		if _, err := a.state.Get(gitwatchKey, &ws); err != nil {
			a.log.Warn("the git watcher's cursors in state.json don't read, so repos are read from their present", "err", err)
			ws = git.WatcherState{}
		}
	}
	res, err := git.NewResolver(git.ResolverOptions{Secret: secret}, m)
	if err != nil {
		return nil, err
	}
	g := &gitHost{a: a, res: res, writer: w, tracked: make(map[string]bool), slow: make(map[string]bool),
		rest: make(map[string]time.Time)}
	g.w, err = git.NewWatcher(git.WatcherOptions{
		Resolver: res,
		Sink:     g.sink,
		State:    ws,
		Log:      a.log.With("component", "git"),
	})
	if err != nil {
		return nil, err
	}
	g.saved, _ = json.Marshal(gitState{Projects: m, Watch: ws})
	g.problems()
	g.job = a.sched.Every("git.poll", gitPollEvery, func(time.Time) {
		g.w.Poll()
		g.problems()
	})
	return g, nil
}

// tracked is what one Track gave.
type tracked struct {
	loc git.Location
	err error
}

// project resolves dir, a session's working directory, to its project and
// starts watching its repo, once. It reports whether dir has one, and
// whether it is out of pairtime (a .pairtime-ignore, DESIGN 8.8). A folder
// that is gone has no project for this call, and the next call asks again.
// One git can't answer for within trackWait has none yet either: its
// resolve goes on, and until it ends the folder's calls don't wait again.
func (g *gitHost) project(dir string) (p adapters.Project, ok, ignored bool) {
	g.mu.Lock()
	slow := g.slow[dir]
	resting := time.Now().Before(g.rest[dir])
	g.mu.Unlock()
	if slow || resting {
		return adapters.Project{}, false, false
	}
	done := make(chan tracked, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), trackLimit)
		defer cancel()
		loc, err := g.w.Track(ctx, dir)
		g.mu.Lock()
		switch {
		case err == nil && loc.GitDir != "" && !loc.Bare:
			g.tracked[loc.GitDir] = true
		case errors.Is(err, context.DeadlineExceeded):
			if len(g.rest) >= maxResting {
				clear(g.rest)
			}
			g.rest[dir] = time.Now().Add(trackRest)
		}
		g.mu.Unlock()
		// Sent before slow is cleared: project then either takes the
		// answer or marks the folder slow, and never leaves it marked.
		done <- tracked{loc, err}
		g.mu.Lock()
		delete(g.slow, dir)
		g.mu.Unlock()
	}()
	t := time.NewTimer(trackWait)
	defer t.Stop()
	var r tracked
	select {
	case r = <-done:
	case <-t.C:
		g.mu.Lock()
		select {
		case r = <-done:
			g.mu.Unlock()
		default:
			g.slow[dir] = true
			g.mu.Unlock()
			return adapters.Project{}, false, false
		}
	}
	switch {
	case errors.Is(r.err, git.ErrIgnored):
		return adapters.Project{}, false, true
	case r.err != nil || r.loc.Project == "":
		return adapters.Project{}, false, false
	}
	return adapters.Project{Key: r.loc.Project, Root: r.loc.Dir}, true, false
}

// noteCommand tells the watcher about the command an agent's bash call
// ran, r's, with the call's span from its agent.tool, t: Claude Code's
// PostToolUse arrives as the call ends, and its duration_ms gives the
// start (git.AgentCommand).
func (g *gitHost) noteCommand(r adapters.Record, t *event.AgentTool) {
	c := git.AgentCommand{Call: r.Call, Dir: r.Dir, Line: r.Command}
	if t.TStart > 0 {
		c.Start = time.UnixMilli(t.TStart)
	}
	if t.TEnd > 0 {
		c.End = time.UnixMilli(t.TEnd)
	}
	g.w.NoteAgentCommand(c)
}

// sink writes a change's git.change record (git.WatcherOptions.Sink). The
// watcher gives it the envelope's t, source, harness, session and
// project; the agent adds the rest: the id, via git, and the run's mono
// and boot_id, as its other own events have them (ledger.RunClock). The
// mono is read now, from the clock presence's records are read from, less
// how long ago t was by the wall clock: a few seconds of hold, which no
// sleep or clock step falls in. The writer adds the device and the
// sequence number. The record is sealed at once, so a cursor saved after
// it never runs ahead of the ledger.
func (g *gitHost) sink(c git.Change) {
	g.mu.Lock()
	defer g.mu.Unlock()
	ev := c.Event
	now := time.Now()
	ev.EventID = event.NewEventID(time.UnixMilli(ev.T))
	ev.Via = event.ViaGit
	ev.Mono, ev.BootID = ledger.RunClock(now)
	ev.Mono = max(ev.Mono-max(now.UnixMilli()-ev.T, 0), 0)
	if _, err := g.writer.Write(ledger.Input{Event: ev}); err != nil {
		g.a.log.Warn("the ledger refused a git.change record", "op", c.Op(), "err", err)
		g.a.setProblem("git.ledger", fmt.Sprintf("the ledger refused a git.change record: %v", err))
		return
	}
	g.changes++
	if _, _, err := g.writer.Seal(); err != nil {
		g.a.log.Warn("couldn't seal a git.change record", "err", err)
	}
	g.a.adaptersHost().saveSoon()
}

// put puts the project map and the watcher's cursors in state.json's
// store, and reports whether they changed since the last put.
func (g *gitHost) put() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	st := gitState{Projects: g.res.Map(), Watch: g.w.State()}
	b, err := json.Marshal(st)
	if err != nil || bytes.Equal(b, g.saved) {
		return false
	}
	if err := g.a.state.Put(projectsKey, st.Projects); err != nil {
		g.a.log.Warn("couldn't keep the project map", "err", err)
		return false
	}
	if err := g.a.state.Put(gitwatchKey, st.Watch); err != nil {
		g.a.log.Warn("couldn't keep the git watcher's cursors", "err", err)
		return false
	}
	g.saved = b
	return true
}

// problems passes the watcher's problems on to doctor, in words of the
// agent's own: the watcher's carry git's errors, whose stderr can name a
// repo's folder (git.ExitError), and status and doctor show what they get.
// The watcher's own words go to the agent's log, which stays on the device,
// once each.
func (g *gitHost) problems() {
	ps := g.w.Problems()
	msgs := make([]string, 0, len(ps))
	for _, k := range slices.Sorted(maps.Keys(ps)) {
		msgs = append(msgs, gitProblem(k))
		if g.logged[k] != ps[k] {
			g.a.log.Warn("the git adapter has a problem", "kind", k, "problem", ps[k])
		}
	}
	g.logged = ps
	g.a.setProblem("git", strings.Join(msgs, "; "))
}

// gitProblem is what doctor hears of a problem of the watcher's, by its
// kind (git.Watcher.Problems).
func gitProblem(kind string) string {
	switch kind {
	case "notify":
		return "repos are looked at once a minute rather than watched, because file notifications didn't start"
	case "scan":
		return "reading a repo's reflog failed, and the agent tries again; its log says which repo"
	}
	return "the git adapter has a problem, and the agent's log says what"
}

// status is the git adapter's part of GET /v1/status.
func (g *gitHost) status() gitReply {
	worktrees := g.w.State().Worktrees
	g.mu.Lock()
	defer g.mu.Unlock()
	r := gitReply{Changes: g.changes}
	for dir := range g.tracked {
		if st, ok := worktrees[dir]; ok && st.Cursor.Set {
			r.Watched++
		}
	}
	return r
}

// close stops the poll and the watcher, whose held changes go to sink.
func (g *gitHost) close() {
	g.job.Stop()
	if err := g.w.Close(); err != nil {
		g.a.log.Warn("closing the git watcher", "err", err)
	}
}
