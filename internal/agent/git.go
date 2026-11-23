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

// maxResolving bounds the Tracks that run at once: the first of a big
// repo walks its whole history, and the folders of one repo, its worktrees
// say, would each start one. The others wait in line (gitHost.known).
// maxFolders bounds the folders the host keeps an answer for.
const (
	maxResolving = 2
	maxFolders   = 1024
)

// gitPollEvery is how often the watcher looks at the repos it watches
// with a few stats each, which finds what file notifications missed
// (DESIGN 3.3; git.Watcher.Poll).
const gitPollEvery = time.Minute

// trackLimit bounds one Track of a session's folder. A folder resolves in
// a few git calls, and its answer is cached, but the first sight of a big
// repo can take minutes, walking its whole history for the root commits.
// Its calls wait meanwhile, parked by the adapter host, and the calls of
// other folders go on (D-park-unresolved). A folder whose Track runs out of
// trackLimit gives up: its calls go on without a project, and it sits out
// trackRest before it is tried again, since the resolver keeps no answer
// for it and would walk the history again. trackWait is how long a Track
// may run before a git command in its folder is left out of the watcher
// (noteCommand), and trackFresh how old an answer gets before the folder's
// next call has it looked at again, in the background, as often as the
// resolver itself looks again.
const (
	trackLimit = 5 * time.Minute
	trackRest  = 10 * time.Minute
	trackWait  = 3 * time.Second
	trackFresh = git.DefaultRecheck
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
// (git.Change.Commits) go to commits/, where they wait for the uploader
// (commitStore; D-commit-records-local), and the ledger keeps the
// git.change.
type gitHost struct {
	a       *agent
	res     *git.Resolver
	w       *git.Watcher
	job     *sched.Job
	commits *commitStore
	// ctx ends the Tracks still running when the host closes, and the git
	// they run with them.
	ctx    context.Context
	cancel context.CancelFunc

	// mu orders a change's record, and the seal that puts it on disk,
	// before the watcher state that says it went (put): the cursor never
	// moves past a change a crash could still cut from the ledger.
	mu      sync.Mutex
	writer  *ledger.Writer
	changes uint64 // git.change records written
	saved   []byte // the project map and cursors as last put in state.json
	// logged are the watcher's own problems as last logged.
	logged map[string]string

	// fmu guards what the host knows of the session folders, apart from mu,
	// so that looking up a call's folder never waits for a change's record
	// to be written. tracked are the git directories of the worktrees
	// sessions worked in this run, which the watcher reads; folders are the
	// session folders of this run, by path; waiting are the folders whose
	// Track waits for a slot, oldest first; running counts the Tracks going
	// on, and ended the Tracks that made a folder known, the only ones that
	// can let a call that waits for its folder go (adapterHost.admit): a
	// later Track of a known folder, as after one that failed, leaves it
	// known.
	fmu     sync.Mutex
	tracked map[string]bool
	folders map[string]*folder
	waiting []string
	running int
	ended   uint64
}

// folder is what the git host knows of one session folder in this run.
type folder struct {
	// known says a Track of it has ended, with loc and err: its project,
	// git.ErrIgnored, a timeout (trackLimit) or another error.
	known bool
	loc   git.Location
	err   error
	at    time.Time // when the last Track ended
	// queued says a Track of it waits for a slot, and inflight that one
	// runs, since started.
	queued, inflight bool
	started          time.Time
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
	g := &gitHost{a: a, res: res, writer: w, commits: &commitStore{dir: a.dirs.Commits()},
		tracked: make(map[string]bool), folders: make(map[string]*folder)}
	g.ctx, g.cancel = context.WithCancel(context.Background())
	g.commits.prune(time.Now())
	g.w, err = git.NewWatcher(git.WatcherOptions{
		Resolver: res,
		Sink:     g.sink,
		State:    ws,
		Log:      a.log.With("component", "git"),
	})
	if err != nil {
		g.cancel()
		return nil, err
	}
	g.saved, _ = json.Marshal(gitState{Projects: m, Watch: ws})
	g.problems()
	g.job = a.sched.Every("git.poll", gitPollEvery, func(now time.Time) {
		g.w.Poll()
		g.problems()
		g.commits.prune(now)
	})
	return g, nil
}

// known reports whether the calls of dir, a session's working directory,
// can be parsed now: whether a Track of it has ended in this run, so its
// project, or that it has none, is known (project). When none has, it
// starts one, or puts the folder in line for a slot, and reports false:
// the call waits, parked by the adapter host, which track pokes once the
// Track ends (D-park-unresolved). A folder whose answer is older than
// trackFresh, or was an error other than a timeout or git.ErrIgnored, such
// as a folder that was gone, gets a new Track in the background, and its
// calls go on with the answer they have. One whose Track timed out rests
// for trackRest first.
func (g *gitHost) known(dir string) bool {
	now := time.Now()
	g.fmu.Lock()
	defer g.fmu.Unlock()
	f := g.folders[dir]
	if f == nil {
		if len(g.folders) >= maxFolders {
			// Their next calls wait for a Track again, which the resolver's
			// cache makes quick.
			maps.DeleteFunc(g.folders, func(_ string, o *folder) bool { return o.known && !o.queued && !o.inflight })
		}
		f = &folder{}
		g.folders[dir] = f
	}
	if !f.queued && !f.inflight && f.due(now) {
		if g.running < maxResolving {
			g.startLocked(dir, f, now)
		} else {
			f.queued = true
			g.waiting = append(g.waiting, dir)
		}
	}
	return f.known
}

// due reports whether f wants a Track at now.
func (f *folder) due(now time.Time) bool {
	switch {
	case !f.known:
		return true
	case errors.Is(f.err, context.DeadlineExceeded):
		return !now.Before(f.at.Add(trackRest))
	case f.err != nil && !errors.Is(f.err, git.ErrIgnored):
		return true
	}
	return now.Sub(f.at) >= trackFresh
}

// resting reports whether f's last Track timed out less than trackRest
// before now.
func (f *folder) resting(now time.Time) bool {
	return errors.Is(f.err, context.DeadlineExceeded) && now.Before(f.at.Add(trackRest))
}

// startLocked starts a Track of dir. g.fmu is held.
func (g *gitHost) startLocked(dir string, f *folder, now time.Time) {
	g.running++
	f.queued, f.inflight, f.started = false, true, now
	go g.track(dir, f)
}

// track runs one Track of dir, which also starts watching its repo, keeps
// the answer, gives the slot to the next folder in line, and pokes the
// adapter host, which then parses the calls that waited for the folder.
func (g *gitHost) track(dir string, f *folder) {
	ctx, cancel := context.WithTimeout(g.ctx, trackLimit)
	loc, err := g.w.Track(ctx, dir)
	cancel()
	now := time.Now()
	g.fmu.Lock()
	if !f.known {
		g.ended++
	}
	f.known, f.loc, f.err, f.at, f.inflight = true, loc, err, now, false
	if err == nil && loc.GitDir != "" && !loc.Bare {
		g.tracked[loc.GitDir] = true
	}
	g.running--
	for g.running < maxResolving && len(g.waiting) > 0 {
		next := g.waiting[0]
		g.waiting = g.waiting[1:]
		if nf := g.folders[next]; nf != nil && nf.queued {
			g.startLocked(next, nf, now)
		}
	}
	g.fmu.Unlock()
	g.a.adaptersHost().poke()
}

// tracksEnded counts the Tracks that have made a folder known in this run
// (ended).
func (g *gitHost) tracksEnded() uint64 {
	g.fmu.Lock()
	defer g.fmu.Unlock()
	return g.ended
}

// project returns dir's project as the last Track of it found (known), and
// whether dir is out of pairtime (a .pairtime-ignore, DESIGN 8.8). It never
// waits: a call reaches Parse once its folder is known, or when the host
// stops waiting, at its stop, and then has no project. Every call looks
// for a .pairtime-ignore again, with a few stats, in its folder and above
// it and, for a linked worktree, in its repo's main one, so one counts from
// the moment it appears, as it does for Track (git.Resolver.Ignored,
// IgnoredLoc).
func (g *gitHost) project(dir string) (p adapters.Project, ok, ignored bool) {
	g.fmu.Lock()
	f := g.folders[dir]
	known := f != nil && f.known
	var loc git.Location
	var err error
	if known {
		loc, err = f.loc, f.err
	}
	g.fmu.Unlock()
	switch {
	case errors.Is(err, git.ErrIgnored), g.res.Ignored(dir), known && err == nil && g.res.IgnoredLoc(loc):
		return adapters.Project{}, false, true
	case !known || err != nil || loc.Project == "":
		return adapters.Project{}, false, false
	}
	return adapters.Project{Key: loc.Project, Root: loc.Dir}, true, false
}

// noteCommand tells the watcher about the command an agent's bash call
// ran, r's, with the call's span from its agent.tool, t: Claude Code's
// PostToolUse arrives as the call ends, and its duration_ms gives the
// start (git.AgentCommand). A command in a folder with no answer yet, one
// whose Track has run past trackWait or one that rests after a timeout is
// left out: the watcher resolves the folder on its only loop, which would
// wait for that resolve, so its commit goes by its trailers alone.
func (g *gitHost) noteCommand(r adapters.Record, t *event.AgentTool) {
	now := time.Now()
	g.fmu.Lock()
	f := g.folders[r.Dir]
	busy := f == nil || !f.known || f.inflight && now.Sub(f.started) > trackWait || f.resting(now)
	g.fmu.Unlock()
	if busy {
		return
	}
	c := git.AgentCommand{Call: r.Call, Dir: r.Dir, Line: r.Command}
	if t.TStart > 0 {
		c.Start = time.UnixMilli(t.TStart)
	}
	if t.TEnd > 0 {
		c.End = time.UnixMilli(t.TEnd)
	}
	g.w.NoteAgentCommand(c)
}

// sink keeps a change's commit records in commits/ and writes its
// git.change record (git.WatcherOptions.Sink), both before it returns,
// which is when the watcher moves the cursor past the change. The watcher
// gives it the envelope's t, source, harness, session and project; the
// agent adds the rest: the id, via git, and the run's mono and boot_id, as
// its other own events have them (ledger.RunClock). The mono is the run's
// clock when the scan that found the change began (git.Change.Found), which
// t gives by the wall clock, so a step of the wall clock during the hold
// moves neither. The writer adds the device and the sequence number. The record
// is sealed at once, so a cursor saved after it never runs ahead of the
// ledger. The commit records go first, so a ledger that refuses the
// git.change still leaves them for the uploader.
func (g *gitHost) sink(c git.Change) {
	g.mu.Lock()
	defer g.mu.Unlock()
	now := time.Now()
	if err := g.commits.add(c.Commits, now); err != nil {
		g.a.log.Warn("couldn't keep a change's commit records", "op", c.Op(), "commits", len(c.Commits), "err", err)
		// In words of the agent's own, as the git adapter's other problems:
		// the error names the data directory's folder.
		g.a.setProblem("git.commits", "a change's commit records couldn't be kept in commits/ for the uploader; the agent's log says why")
	} else if len(c.Commits) > 0 {
		g.a.setProblem("git.commits", "")
	}
	ev := c.Event
	ev.EventID = event.NewEventID(time.UnixMilli(ev.T))
	ev.Via = event.ViaGit
	ev.Mono, ev.BootID = ledger.RunClock(now)
	if c.Found.IsZero() {
		ev.Mono = max(ev.Mono-max(now.UnixMilli()-ev.T, 0), 0)
	} else {
		// The scan's own reading, which the hold, up to 10 minutes while an
		// agent's shell call runs, doesn't move by a step of the wall clock:
		// the ledger writer holds the event to it when config.toml said
		// local-only meanwhile (ledger.Writer.Write).
		ev.Mono, ev.BootID = ledger.RunClock(c.Found)
	}
	_, err := g.writer.Write(ledger.Input{Event: ev})
	if errors.Is(err, ledger.ErrLateLocal) {
		// Found in a local-only stretch, it came after the writer sealed a
		// batch online since, so it stays off the ledger, as it should.
		g.a.log.Info("a git change found while config.toml said local-only came after the computer uploaded again, so the ledger dropped it", "op", c.Op())
		return
	}
	if err != nil {
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
	r := gitReply{Changes: g.changes}
	g.mu.Unlock()
	g.fmu.Lock()
	defer g.fmu.Unlock()
	for dir := range g.tracked {
		if st, ok := worktrees[dir]; ok && st.Cursor.Set {
			r.Watched++
		}
	}
	return r
}

// close stops the poll, the Tracks still running and the watcher, whose
// held changes go to sink.
func (g *gitHost) close() {
	g.cancel()
	g.job.Stop()
	if err := g.w.Close(); err != nil {
		g.a.log.Warn("closing the git watcher", "err", err)
	}
}
