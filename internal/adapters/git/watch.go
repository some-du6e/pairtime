// SPDX-License-Identifier: AGPL-3.0-or-later

package git

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/some-du6e/pairtime/pkg/event"
)

// Defaults for WatcherOptions.
const (
	// DefaultHold is how long a change waits for an agent's tool event
	// before it goes out with the actor it has. The PostToolUse hook that
	// reports an agent's git command fires when the command ends, a moment
	// after git wrote its reflog.
	DefaultHold = 3 * time.Second
	// DefaultDebounce is how long the watcher waits after a file event
	// before it looks, since one git command writes several files.
	DefaultDebounce = 300 * time.Millisecond
	// DefaultMaxWatched caps the worktrees watched with file
	// notifications. Each costs two inotify watches on Linux and a file
	// descriptor per entry of .git on macOS; worktrees past the cap are
	// only polled.
	DefaultMaxWatched = 64
)

// StaleAfter is how long after a change's last reflog entry a scan may
// find it for the change to stay the person's. A change found later, by
// the poll of a worktree without notifications or by the catch-up after a
// restart, carries the scan's time as its event time (SM 1.1), so as a
// human commit it would anchor credit at the moment pairtime noticed it
// rather than when the person committed (SM 2.3). It goes out with actor
// unknown instead, unless an agent's command explains it.
const StaleAfter = 10 * time.Second

const (
	// agentRunKeep is how long an agent's finished git command stays
	// matchable: longer than the slowest way to a scan, a poll about every
	// minute plus Hold.
	agentRunKeep = 3 * time.Minute
	// maxCallHold is how long a change waits for an agent's shell call that
	// started before it and hasn't reported its end. Claude Code's Bash
	// tool stops a command after 10 minutes at most.
	maxCallHold = 10 * time.Minute
	// maxRuns bounds the agent commands kept.
	maxRuns = 1024
	// maxIdle bounds the worktrees kept after Poll lost sight of them.
	maxIdle = 256
	// firstRetry and maxRetry bound how long a failed scan waits before
	// it is tried again; the wait doubles with each failure.
	firstRetry = 5 * time.Second
	maxRetry   = 5 * time.Minute
)

// WatcherOptions configure a Watcher.
type WatcherOptions struct {
	Resolver *Resolver
	// Git runs git. Nil means the resolver's.
	Git *Runner
	// Sink gets every change, from one goroutine, each worktree's in the
	// order git made them.
	Sink func(Change)
	// State is where the scanners resume.
	State WatcherState
	// Hold, Debounce and MaxWatched default to DefaultHold,
	// DefaultDebounce and DefaultMaxWatched.
	Hold       time.Duration
	Debounce   time.Duration
	MaxWatched int
	// NoNotify turns file notifications off, so only Poll finds changes.
	NoNotify bool
	Now      func() time.Time
	Log      *slog.Logger

	// newNotifier replaces fsnotify in tests.
	newNotifier func() (notifier, error)
}

// notifier is the part of fsnotify the watcher uses.
type notifier interface {
	Add(name string) error
	Remove(name string) error
	Close() error
	eventCh() <-chan fsnotify.Event
	errCh() <-chan error
}

type fsnotifier struct{ *fsnotify.Watcher }

func (f fsnotifier) eventCh() <-chan fsnotify.Event { return f.Events }
func (f fsnotifier) errCh() <-chan error            { return f.Errors }

func newFsnotifier() (notifier, error) {
	f, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	return fsnotifier{f}, nil
}

// WatcherState is what the watcher keeps in state.json: each worktree's
// cursor, by its git directory, and each repo's recorded commits, by its
// common git directory. The paths are local.
type WatcherState struct {
	Worktrees map[string]ScannerState `json:"worktrees,omitempty"`
	Repos     map[string]RepoState    `json:"repos,omitempty"`
}

// RepoState is what the scanners of one repo's worktrees share.
type RepoState struct {
	// Recent are the newest commits recorded in the repo, so that a merge
	// in one worktree of a branch committed in another doesn't record them
	// again as made elsewhere.
	Recent []string `json:"recent,omitempty"`
}

// Watcher watches the git directories of the worktrees it is told about
// and turns what happens in them into Changes (DESIGN 3.3's gitwatch).
//
// Track starts watching a folder's worktree. A file event in its git
// directory, or Poll finding a change, schedules a scan; a scan's changes
// wait up to Hold for an agent's git command to explain them, then go to
// Sink.
type Watcher struct {
	opts WatcherOptions
	res  *Resolver
	git  *Runner
	log  *slog.Logger
	now  func() time.Time
	fsw  notifier

	work chan func()
	stop chan struct{}
	done chan struct{}

	// The watch goroutine alone calls fsw.Add and fsw.Remove (see
	// watches). wkick wakes it for w.wreqs; wstop and wdone stop it.
	wkick chan struct{}
	wstop chan struct{}
	wdone chan struct{}

	mu    sync.Mutex
	trees map[string]*tree // tracked worktrees, by foldPath of the git directory
	// idle are worktrees Poll stopped watching because their HEAD couldn't
	// be read, as when a network or USB drive drops out. Track resumes
	// them where they were, instead of from the state the watcher started
	// with, which would report their changes a second time.
	idle map[string]*tree
	// saved are the states from opts.State that no Track has taken up
	// yet, by foldPath of the git directory.
	saved map[string]savedState
	// recent are the commits recorded in each repo, by foldPath of its
	// common directory, shared by the scanners of its worktrees.
	recent   map[string]*recentSet
	byDir    map[string]*watchDir      // by foldPath of each watched directory
	wreqs    []*tree                   // worktrees whose watches the watch goroutine should bring up to date
	wwait    map[*tree][]chan struct{} // closed once a worktree's queued request is done
	runs     []gitRun
	held     []*heldChange
	timer    *time.Timer
	problems map[string]string
	closed   bool
	// delivering is the event time of the change release took from held
	// and is giving to Sink, in Unix ms, 0 for none (Waiting).
	delivering int64
}

type savedState struct {
	gitDir string
	st     ScannerState
}

type tree struct {
	key  string // foldPath of the git directory
	loc  Location
	scan *Scanner // used by the worker goroutine only
	// state is what State reports: the scanner's state as of the last of
	// the worktree's changes that went to Sink. pending are the states of
	// the scans after it, each waiting for its scan's changes to go out,
	// so that an exit while changes are held can't lose them.
	state    ScannerState
	pending  []*pendingState
	sig      signature
	failures int       // scans in a row that failed
	retryAt  time.Time // when a failed scan may be tried again
	watched  []string
	debounce *time.Timer
	// dropped is set once the worktree is out of pairtime, so a scan of it
	// that was already queued or running yields nothing.
	dropped bool
	// scanning is set while the worker scans the worktree, so a scan of a
	// sibling it starts (see scanSiblings) doesn't start this one again,
	// and scanFrom is when that scan began, which the changes it finds are
	// timed after (Waiting).
	scanning bool
	scanFrom time.Time
}

type pendingState struct {
	st   ScannerState
	left int // the scan's changes not yet given to Sink
}

// watchDir is a directory with a file notification, and the worktrees it
// serves: a repo's reftable directory serves every worktree of the repo.
type watchDir struct {
	path  string
	trees []*tree
}

type heldChange struct {
	c       Change
	t       *tree
	p       *pendingState
	gitDir  string // the worktree's git directory, which agent runs match
	until   time.Time
	decided bool
}

// NewWatcher starts a watcher. If file notifications can't start, it
// polls only, and Problems says so.
func NewWatcher(opts WatcherOptions) (*Watcher, error) {
	if opts.Resolver == nil || opts.Sink == nil {
		return nil, errors.New("a git watcher needs a resolver and a sink")
	}
	w := &Watcher{
		opts:     opts,
		res:      opts.Resolver,
		git:      opts.Git,
		log:      opts.Log,
		now:      opts.Now,
		work:     make(chan func(), 64),
		stop:     make(chan struct{}),
		done:     make(chan struct{}),
		wkick:    make(chan struct{}, 1),
		wstop:    make(chan struct{}),
		wdone:    make(chan struct{}),
		trees:    make(map[string]*tree),
		idle:     make(map[string]*tree),
		saved:    make(map[string]savedState),
		recent:   make(map[string]*recentSet),
		byDir:    make(map[string]*watchDir),
		wwait:    make(map[*tree][]chan struct{}),
		problems: make(map[string]string),
	}
	if w.git == nil {
		w.git = opts.Resolver.git
	}
	if w.log == nil {
		w.log = slog.New(slog.DiscardHandler)
	}
	if w.now == nil {
		w.now = time.Now
	}
	if w.opts.Hold <= 0 {
		w.opts.Hold = DefaultHold
	}
	if w.opts.Debounce <= 0 {
		w.opts.Debounce = DefaultDebounce
	}
	if w.opts.MaxWatched <= 0 {
		w.opts.MaxWatched = DefaultMaxWatched
	}
	for gitDir, st := range opts.State.Worktrees {
		w.saved[foldPath(gitDir)] = savedState{gitDir: gitDir, st: st}
	}
	for common, rs := range opts.State.Repos {
		w.recent[foldPath(common)] = newRecentSet(common, rs.Recent)
	}
	if !opts.NoNotify {
		newNotifier := opts.newNotifier
		if newNotifier == nil {
			newNotifier = newFsnotifier
		}
		fsw, err := newNotifier()
		if err != nil {
			w.problems["notify"] = fmt.Sprintf("repos are polled, not watched, because file notifications didn't start: %v", err)
		} else {
			w.fsw = fsw
			go w.events()
			go w.watches()
		}
	}
	go w.loop()
	return w, nil
}

// Close stops the watcher. Changes still held go to Sink first.
func (w *Watcher) Close() error {
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return nil
	}
	w.closed = true
	for _, t := range w.trees {
		if t.debounce != nil {
			t.debounce.Stop()
		}
	}
	if w.timer != nil {
		w.timer.Stop()
	}
	w.mu.Unlock()
	if w.fsw != nil {
		// An Add or Remove in flight finishes first, since events() keeps
		// draining; closing fsnotify under it could leave it waiting.
		close(w.wstop)
		<-w.wdone
		w.fsw.Close()
	}
	close(w.stop)
	<-w.done
	w.release(true)
	return nil
}

// Waiting reports whether a change made before before, in Unix ms, may
// still be on its way to Sink: held for an agent's tool event, being given
// to Sink, or in a scan that began before then, whose changes are timed at
// its start or later. The agent's ledger writer asks, as its seals do while
// the first batch after a switch back online is due its mark, so that a
// change made while config.toml said local-only reaches the ledger before
// that batch (ledger.Options.Waiting). It takes only the watcher's lock,
// which release never holds while it calls Sink.
func (w *Watcher) Waiting(before int64) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.delivering != 0 && w.delivering < before {
		return true
	}
	for _, h := range w.held {
		if h.c.Event.T < before {
			return true
		}
	}
	for _, t := range w.trees {
		if t.scanning && t.scanFrom.UnixMilli() < before {
			return true
		}
	}
	return false
}

// Problems returns what doctor should hear about.
func (w *Watcher) Problems() map[string]string {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make(map[string]string, len(w.problems))
	for k, v := range w.problems {
		out[k] = v
	}
	return out
}

// State returns what to keep in state.json. Each worktree's cursor is
// where it stood after the last of its changes Sink got, and the commits
// of changes still held don't count as recorded yet, so after an exit
// that skips Close the next start reports those changes again rather than
// never. A worktree Poll lost sight of is kept while its git directory is
// there.
func (w *Watcher) State() WatcherState {
	w.mu.Lock()
	defer w.mu.Unlock()
	st := WatcherState{Worktrees: make(map[string]ScannerState), Repos: make(map[string]RepoState)}
	for _, s := range w.saved {
		if hasHEAD(s.gitDir) {
			st.Worktrees[s.gitDir] = s.st
		}
	}
	for _, t := range w.idle {
		if hasHEAD(t.loc.GitDir) {
			st.Worktrees[t.loc.GitDir] = t.state
		}
	}
	for _, t := range w.trees {
		st.Worktrees[t.loc.GitDir] = t.state
	}
	held := make(map[string]bool)
	for _, h := range w.held {
		for _, c := range h.c.Commits {
			held[c.SHA] = true
		}
	}
	for _, rs := range w.recent {
		ids := slices.DeleteFunc(rs.list(), func(id string) bool { return held[id] })
		if len(ids) > 0 {
			st.Repos[rs.dir] = RepoState{Recent: ids}
		}
	}
	return st
}

func hasHEAD(gitDir string) bool {
	_, err := os.Stat(filepath.Join(gitDir, "HEAD"))
	return err == nil
}

// Track resolves dir and starts watching its worktree, once. It returns
// the location, or ErrIgnored for a folder that is out of pairtime, which
// is then never watched. A folder without git, and a bare repo, has
// nothing to watch.
func (w *Watcher) Track(ctx context.Context, dir string) (Location, error) {
	loc, err := w.res.Resolve(ctx, dir)
	if err == nil && w.res.ignoredLoc(loc) {
		// The resolver trusts its answer for a minute; a .pairtime-ignore
		// counts from the moment it appears.
		err = ErrIgnored
	}
	if errors.Is(err, ErrIgnored) {
		// A folder that turned ignored may be a tracked worktree's.
		w.mu.Lock()
		w.dropIgnoredLocked()
		w.mu.Unlock()
		return Location{}, err
	}
	if err != nil {
		return loc, err
	}
	if loc.GitDir == "" || loc.Bare {
		return loc, nil
	}
	key := foldPath(loc.GitDir)
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return loc, errors.New("the git watcher is closed")
	}
	if _, ok := w.trees[key]; ok {
		w.mu.Unlock()
		return loc, nil
	}
	t, ok := w.idle[key]
	delete(w.idle, key)
	if !ok {
		st := w.saved[key].st
		delete(w.saved, key)
		t = &tree{key: key, loc: loc, state: st}
		t.scan = NewScanner(w.git, loc, st, w.now, w.res.Rekey)
		t.scan.share(w.recentLocked(loc.CommonDir))
		tt := t
		t.scan.beforeArrivals = func(context.Context) { w.scanSiblings(tt) }
	}
	w.trees[key] = t
	watched := w.requestWatchLocked(t)
	// The first scan sets the cursor, or catches up from a saved one.
	w.queueLocked(t)
	w.mu.Unlock()
	// The worktree is watched when Track returns, as a caller expects. The
	// wait holds no lock, so the watch goroutine's fsnotify calls can't
	// block on it.
	if watched != nil {
		select {
		case <-watched:
		case <-w.stop:
		case <-ctx.Done():
		}
	}
	return loc, nil
}

// recentLocked returns the set of commits recorded in the repo whose
// common directory is commonDir.
func (w *Watcher) recentLocked(commonDir string) *recentSet {
	k := foldPath(commonDir)
	rs, ok := w.recent[k]
	if !ok {
		rs = newRecentSet(commonDir, nil)
		w.recent[k] = rs
	}
	return rs
}

// requestWatchLocked asks the watch goroutine to bring t's file
// notifications in line with whether t is tracked. The channel it returns
// is closed once that is done; it is nil without notifications.
func (w *Watcher) requestWatchLocked(t *tree) <-chan struct{} {
	if w.fsw == nil {
		return nil
	}
	if !slices.Contains(w.wreqs, t) {
		w.wreqs = append(w.wreqs, t)
	}
	done := make(chan struct{})
	w.wwait[t] = append(w.wwait[t], done)
	select {
	case w.wkick <- struct{}{}:
	default:
	}
	return done
}

// watches runs every fsw.Add and fsw.Remove. fsnotify's Windows backend
// hands both to the one goroutine that also sends Events, and that
// goroutine blocks on a full Events channel, which only events() drains.
// So neither may run in events() or under w.mu, which events() takes:
// once 50 events were queued, each side would wait for the other.
func (w *Watcher) watches() {
	defer close(w.wdone)
	for {
		select {
		case <-w.wkick:
		case <-w.wstop:
			return
		}
		for {
			w.mu.Lock()
			if len(w.wreqs) == 0 || w.closed {
				w.mu.Unlock()
				break
			}
			t := w.wreqs[0]
			w.wreqs = w.wreqs[1:]
			// A request that comes while this one runs queues t again,
			// with its own channel.
			done := w.wwait[t]
			delete(w.wwait, t)
			w.mu.Unlock()
			w.syncWatches(t)
			for _, c := range done {
				close(c)
			}
		}
	}
}

// watchDirs are the directories a worktree's notifications cover: its git
// directory and logs directory, and in a reftable repo the reftable
// directories, its own (a linked worktree keeps its HEAD there) and the
// repo's. Never the working tree, and never recursively (CP 3.2).
func watchDirs(loc Location) []string {
	dirs := []string{loc.GitDir, filepath.Join(loc.GitDir, "logs")}
	for _, d := range []string{loc.GitDir, loc.CommonDir} {
		rt := filepath.Join(d, "reftable")
		if fi, err := os.Stat(rt); err == nil && fi.IsDir() && !slices.Contains(dirs, rt) {
			dirs = append(dirs, rt)
		}
	}
	return dirs
}

// syncWatches adds and removes t's file notifications, never with w.mu
// held across a call into fsnotify. A worktree past MaxWatched gets none
// and is only polled.
func (w *Watcher) syncWatches(t *tree) {
	w.mu.Lock()
	tracked := !w.closed && w.trees[t.key] == t
	loc := t.loc
	have := slices.Clone(t.watched)
	room := len(have) > 0 || len(w.byDir) < 2*w.opts.MaxWatched
	w.mu.Unlock()
	var want []string
	if tracked && room {
		want = watchDirs(loc)
	}
	for _, d := range have {
		if !slices.Contains(want, d) {
			w.unwatch(t, d)
		}
	}
	for _, d := range want {
		if !slices.Contains(have, d) {
			w.watch(t, d)
		}
	}
}

// watch adds directory d for t, or joins t to the worktrees it serves.
func (w *Watcher) watch(t *tree, d string) {
	k := foldPath(d)
	w.mu.Lock()
	if w.closed || w.trees[t.key] != t {
		w.mu.Unlock()
		return
	}
	if wd, ok := w.byDir[k]; ok {
		if !slices.Contains(wd.trees, t) {
			wd.trees = append(wd.trees, t)
			t.watched = append(t.watched, d)
		}
		w.mu.Unlock()
		return
	}
	wd := &watchDir{path: d, trees: []*tree{t}}
	w.byDir[k] = wd // events for d find t while Add runs
	t.watched = append(t.watched, d)
	w.mu.Unlock()
	err := w.fsw.Add(d)
	if err == nil {
		return
	}
	if !errors.Is(err, os.ErrNotExist) {
		w.log.Debug("git watch", "err", err)
	}
	w.mu.Lock()
	if w.byDir[k] == wd {
		delete(w.byDir, k)
	}
	for _, wt := range wd.trees {
		wt.watched = slices.DeleteFunc(wt.watched, func(x string) bool { return foldPath(x) == k })
	}
	w.mu.Unlock()
}

// unwatch takes t off directory d, and removes d's notification once no
// worktree is left on it.
func (w *Watcher) unwatch(t *tree, d string) {
	k := foldPath(d)
	w.mu.Lock()
	t.watched = slices.DeleteFunc(t.watched, func(x string) bool { return foldPath(x) == k })
	wd, ok := w.byDir[k]
	if !ok {
		w.mu.Unlock()
		return
	}
	wd.trees = slices.DeleteFunc(wd.trees, func(x *tree) bool { return x == t })
	last := len(wd.trees) == 0
	if last {
		delete(w.byDir, k)
	}
	w.mu.Unlock()
	if last {
		_ = w.fsw.Remove(wd.path) // fails for a directory that is gone, which needs nothing
	}
}

// relevant reports whether a file event in a git directory may mean HEAD
// moved. The index changes whenever an editor runs `git status`, so it
// wakes the watch, but only a signature change makes a scan.
func relevant(name string) bool {
	switch strings.TrimSuffix(filepath.Base(name), ".lock") {
	case "HEAD", "ORIG_HEAD", "index", "packed-refs", "shallow", "logs", "rebase-merge", "rebase-apply", "tables.list":
		return true
	}
	return false
}

// events turns file notifications into debounced scans.
func (w *Watcher) events() {
	for {
		select {
		case ev, ok := <-w.fsw.eventCh():
			if !ok {
				return
			}
			if !relevant(ev.Name) {
				continue
			}
			w.mu.Lock()
			if wd := w.byDir[foldPath(filepath.Dir(ev.Name))]; wd != nil && !w.closed {
				for _, t := range wd.trees {
					if filepath.Base(ev.Name) == "logs" && ev.Has(fsnotify.Create) {
						// A new repo's first commit makes logs/; the watch
						// goroutine adds it, never this one (see watches).
						w.requestWatchLocked(t)
					}
					if t.debounce == nil {
						tt := t
						t.debounce = time.AfterFunc(w.opts.Debounce, func() { w.poke(tt) })
					} else {
						t.debounce.Reset(w.opts.Debounce)
					}
				}
			}
			w.mu.Unlock()
		case err, ok := <-w.fsw.errCh():
			if !ok {
				return
			}
			// An overflowed queue lost events; the next poll finds what
			// they were about.
			w.log.Warn("git watch", "err", err)
		}
	}
}

// poke scans t if its signature changed.
func (w *Watcher) poke(t *tree) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.closed && w.trees[t.key] == t {
		w.queueIfChangedLocked(t)
	}
}

// queueIfChangedLocked asks for a scan of t when its signature isn't the
// one its last good scan saw, unless a failed scan of it is still waiting
// out its retry.
func (w *Watcher) queueIfChangedLocked(t *tree) {
	if signatureOf(t.loc) != t.sig && !w.now().Before(t.retryAt) {
		w.queueLocked(t)
	}
}

// Poll looks at every tracked worktree with a few stats each, and scans
// the ones whose HEAD, refs or reflog changed. The agent calls it from its
// scheduler, every minute or so (DESIGN 3.3); it is what finds changes on
// file systems without notifications and past MaxWatched. It also stops
// watching worktrees that are gone, and drops ones that are now out of
// pairtime.
func (w *Watcher) Poll() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return
	}
	for key, t := range w.trees {
		if w.res.ignoredLoc(t.loc) {
			w.dropLocked(key, t)
			continue
		}
		if !hasHEAD(t.loc.GitDir) {
			w.untrackLocked(key, t)
			if len(w.idle) >= maxIdle {
				for k := range w.idle {
					delete(w.idle, k)
					break
				}
			}
			w.idle[key] = t
			continue
		}
		w.queueIfChangedLocked(t)
	}
}

// dropIgnoredLocked drops every tracked or idle worktree that is now out
// of pairtime.
func (w *Watcher) dropIgnoredLocked() {
	for _, m := range []map[string]*tree{w.trees, w.idle} {
		for key, t := range m {
			if w.res.ignoredLoc(t.loc) {
				w.dropLocked(key, t)
			}
		}
	}
}

// dropLocked stops watching a worktree that is now out of pairtime, as
// DESIGN 8.8 asks of a .pairtime-ignore or an exclude pattern added while
// it was tracked: its held changes never reach Sink, and nothing of its
// state is kept, nor its repo's recorded commits once no worktree of the
// repo is left.
func (w *Watcher) dropLocked(key string, t *tree) {
	w.untrackLocked(key, t)
	delete(w.idle, key)
	delete(w.saved, key)
	t.dropped = true
	w.held = slices.DeleteFunc(w.held, func(h *heldChange) bool { return h.t == t })
	ck := foldPath(t.loc.CommonDir)
	for _, m := range []map[string]*tree{w.trees, w.idle} {
		for _, o := range m {
			if foldPath(o.loc.CommonDir) == ck {
				return
			}
		}
	}
	delete(w.recent, ck)
}

func (w *Watcher) untrackLocked(key string, t *tree) {
	if t.debounce != nil {
		t.debounce.Stop()
	}
	delete(w.trees, key)
	w.requestWatchLocked(t) // the watch goroutine removes its notifications
}

// queueLocked asks the worker to scan t.
func (w *Watcher) queueLocked(t *tree) {
	select {
	case w.work <- func() { w.scan(t) }:
	default:
		// The worker is busy; the queued scans and the next poll catch up.
	}
}

// NoteAgentCommand tells the watcher about a shell command an agent's tool
// ran. A git command in it marks a git operation in the same repo within
// the call's span, widened by ActorWindow, as the agent's (SM 1.2). A call
// reported as it starts holds the changes in its repo that come after its
// start until it is reported again with its end. The command line is
// parsed here and not kept.
func (w *Watcher) NoteAgentCommand(c AgentCommand) {
	runs := parseGitRuns(c)
	ends := c.Call != "" && !c.End.IsZero()
	if (len(runs) == 0 && !ends) || (c.Start.IsZero() && c.End.IsZero()) {
		return
	}
	select {
	case w.work <- func() { w.addRuns(c.Call, c.End, runs) }:
	case <-w.stop:
	}
}

// loop runs scans and matching, one at a time.
func (w *Watcher) loop() {
	defer close(w.done)
	for {
		select {
		case f := <-w.work:
			f()
		case <-w.stop:
			return
		}
	}
}

// scanSiblings scans the other worktrees of t's repo whose HEAD moved
// since their last scan. t's scanner calls it, on the worker goroutine,
// before it looks for the commits a merge or a pull brought: a merge here
// of a branch committed in another worktree, an agent's say, has to find
// that branch's commits recorded, as made there, whichever worktree's
// notification came first.
func (w *Watcher) scanSiblings(t *tree) {
	w.mu.Lock()
	var due []*tree
	ck := foldPath(t.loc.CommonDir)
	now := w.now()
	for _, o := range w.trees {
		if o != t && !o.scanning && foldPath(o.loc.CommonDir) == ck && signatureOf(o.loc) != o.sig && !now.Before(o.retryAt) {
			due = append(due, o)
		}
	}
	w.mu.Unlock()
	for _, o := range due {
		w.scan(o)
	}
}

func (w *Watcher) scan(t *tree) {
	w.mu.Lock()
	if t.dropped || w.closed || w.trees[t.key] != t || t.scanning {
		w.mu.Unlock()
		return
	}
	t.scanning, t.scanFrom = true, w.now()
	defer func() {
		w.mu.Lock()
		t.scanning = false
		w.mu.Unlock()
	}()
	if w.res.ignoredLoc(t.loc) {
		w.dropLocked(t.key, t)
		w.mu.Unlock()
		return
	}
	loc := t.loc
	w.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	sig := signatureOf(loc)
	changes, err := t.scan.Scan(ctx)
	loc, state := t.scan.Location(), t.scan.State()
	now := w.now()
	w.mu.Lock()
	if t.dropped || w.res.ignoredLoc(loc) {
		// Ignored while git was read: none of it counts.
		if !t.dropped {
			w.dropLocked(t.key, t)
		}
		w.mu.Unlock()
		return
	}
	t.loc = loc
	if err != nil {
		// The signature stays as it was, so the next look after the retry
		// time scans again; the cursor stayed put.
		t.failures++
		t.retryAt = now.Add(retryAfter(t.failures))
		w.problems["scan"] = fmt.Sprintf("reading a repo's reflog failed: %v", err)
	} else {
		t.failures, t.retryAt = 0, time.Time{}
		t.sig = sig
		delete(w.problems, "scan")
	}
	if len(changes) == 0 && len(t.pending) == 0 {
		t.state = state
	} else {
		p := &pendingState{st: state, left: len(changes)}
		t.pending = append(t.pending, p)
		// The commits count as recorded from the moment their changes are
		// held, under the same lock State takes, so State never saves one
		// ahead of its worktree's cursor.
		rs := w.recentLocked(loc.CommonDir)
		for _, c := range changes {
			for _, cm := range c.Commits {
				rs.add(cm.SHA)
			}
			if c.actor() == event.ActorHuman && now.Sub(time.Unix(c.GitEnd+1, 0)) > StaleAfter {
				c.SetActor(event.ActorUnknown)
			}
			h := &heldChange{c: c, t: t, p: p, gitDir: loc.GitDir, until: now.Add(w.opts.Hold)}
			w.matchLocked(h)
			w.held = append(w.held, h)
		}
	}
	w.mu.Unlock()
	w.release(false)
}

// retryAfter is how long a worktree waits after failures failed scans in
// a row: firstRetry, doubling up to maxRetry.
func retryAfter(failures int) time.Duration {
	d := firstRetry
	for i := 1; i < failures && d < maxRetry; i++ {
		d *= 2
	}
	return min(d, maxRetry)
}

// addRuns keeps an agent's git runs for matching. A call that reports its
// end replaces the runs it reported at its start, and takes their start
// when the end came without one.
func (w *Watcher) addRuns(call string, end time.Time, runs []gitRun) {
	// Resolving a folder can run git, so it happens here, without the lock.
	for i := range runs {
		if runs[i].dir == "" {
			continue
		}
		loc, err := w.res.Resolve(context.Background(), runs[i].dir)
		if err != nil || loc.GitDir == "" {
			runs[i].dir = ""
			continue
		}
		// A worktree's git directory: an agent's commit in one worktree
		// says nothing about the person's in another of the same repo.
		runs[i].dir = loc.GitDir
	}
	now := w.now()
	w.mu.Lock()
	if call != "" && !end.IsZero() {
		var start time.Time
		w.runs = slices.DeleteFunc(w.runs, func(r gitRun) bool {
			if r.call != call || !r.end.IsZero() {
				return false
			}
			if start.IsZero() || r.start.Before(start) {
				start = r.start
			}
			return true
		})
		for i := range runs {
			if runs[i].start.IsZero() {
				runs[i].start = start
			}
		}
	}
	w.runs = slices.DeleteFunc(w.runs, func(r gitRun) bool {
		if r.end.IsZero() {
			return now.Sub(r.start) > maxCallHold
		}
		return now.Sub(r.end) > agentRunKeep
	})
	w.runs = append(w.runs, runs...)
	if len(w.runs) > maxRuns {
		w.runs = slices.Delete(w.runs, 0, len(w.runs)-maxRuns)
	}
	for _, h := range w.held {
		w.matchLocked(h)
	}
	w.mu.Unlock()
	w.release(false)
}

// matchLocked marks h as the agent's when a kept agent run explains it.
// runs hold the git directory of the worktree their folder resolved to.
func (w *Watcher) matchLocked(h *heldChange) {
	if h.decided {
		return
	}
	op := h.c.Op()
	for _, r := range w.runs {
		if runMatches(r, r.dir, h.gitDir, op, h.c.GitStart, h.c.GitEnd) {
			h.c.SetActor(event.ActorAgent)
			h.decided = true
			return
		}
	}
}

// openCallLocked returns how long h waits for an agent's shell call that
// is still running: a call in h's worktree that can run h's git command
// and started before h's operation did, give or take ActorWindow.
func (w *Watcher) openCallLocked(h *heldChange, now time.Time) (time.Time, bool) {
	op := h.c.Op()
	var until time.Time
	for _, r := range w.runs {
		if !r.end.IsZero() || r.dir == "" || foldPath(r.dir) != foldPath(h.gitDir) || !slices.Contains(r.ops, op) {
			continue
		}
		if !time.Unix(h.c.GitStart+1, 0).After(r.start.Add(-ActorWindow)) {
			continue // the operation began before the call did
		}
		if u := r.start.Add(maxCallHold); u.After(now) && u.After(until) {
			until = u
		}
	}
	return until, !until.IsZero()
}

// release sends held changes to Sink: each one that is decided, or has
// waited long enough and isn't waiting on an agent's running call, once
// every earlier change of its worktree went; all of them when final is
// set. It arms a timer for the next one due.
func (w *Watcher) release(final bool) {
	for {
		w.mu.Lock()
		now := w.now()
		idx := -1
		var next time.Time
		waiting := make(map[*tree]bool)
		for i, h := range w.held {
			if waiting[h.t] {
				continue // a worktree's changes go out in the order git made them
			}
			due := h.until
			if u, open := w.openCallLocked(h, now); open && u.After(due) {
				due = u
			}
			if final || h.decided || !now.Before(due) {
				idx = i
				break
			}
			waiting[h.t] = true
			if next.IsZero() || due.Before(next) {
				next = due
			}
		}
		if idx < 0 {
			if !next.IsZero() && !w.closed {
				w.armLocked(next.Sub(now))
			}
			w.mu.Unlock()
			return
		}
		h := w.held[idx]
		if w.res.ignoredLoc(h.t.loc) {
			// A .pairtime-ignore that came during the hold, with no poll or
			// scan since to see it: the worktree goes, held changes and all.
			w.dropLocked(h.t.key, h.t)
			w.mu.Unlock()
			continue
		}
		w.held = slices.Delete(w.held, idx, idx+1)
		w.delivering = h.c.Event.T
		w.mu.Unlock()
		w.opts.Sink(h.c)
		w.mu.Lock()
		w.delivering = 0
		w.deliveredLocked(h)
		w.mu.Unlock()
	}
}

// armLocked has the worker look at the held changes again after wait.
func (w *Watcher) armLocked(wait time.Duration) {
	if w.timer == nil {
		w.timer = time.AfterFunc(wait, func() {
			select {
			case w.work <- func() { w.release(false) }:
			case <-w.stop:
			}
		})
		return
	}
	w.timer.Reset(wait)
}

// deliveredLocked moves h's worktree's reported state on past each scan
// whose changes have all gone to Sink.
func (w *Watcher) deliveredLocked(h *heldChange) {
	if h.p != nil {
		h.p.left--
	}
	t := h.t
	for len(t.pending) > 0 && t.pending[0].left <= 0 {
		t.state = t.pending[0].st
		t.pending = t.pending[1:]
	}
}

// signature is what Poll compares to tell whether a worktree's HEAD may
// have moved: the size and modification time of the files a HEAD move,
// a ref update or a reflog append writes.
type signature struct {
	files [9]fileSig
	head  string
}

type fileSig struct {
	size  int64
	mtime int64
	ok    bool
}

func statSig(name string) fileSig {
	fi, err := os.Stat(name)
	if err != nil {
		return fileSig{}
	}
	return fileSig{size: fi.Size(), mtime: fi.ModTime().UnixNano(), ok: true}
}

// signatureOf stats HEAD, logs/HEAD, ORIG_HEAD, the current branch's
// loose ref, packed-refs, the reftable lists, the shallow file and the
// rebase state. The index isn't in it: it changes on every `git status`.
func signatureOf(loc Location) signature {
	var s signature
	headFile := filepath.Join(loc.GitDir, "HEAD")
	if data, err := readSmall(headFile, 512); err == nil {
		s.head = string(bytes.TrimSpace(data))
	}
	s.files[0] = statSig(headFile)
	s.files[1] = statSig(filepath.Join(loc.GitDir, "logs", "HEAD"))
	s.files[2] = statSig(filepath.Join(loc.GitDir, "ORIG_HEAD"))
	if ref, ok := strings.CutPrefix(s.head, "ref: "); ok && strings.HasPrefix(ref, "refs/") && !strings.Contains(ref, "..") {
		s.files[3] = statSig(filepath.Join(loc.CommonDir, filepath.FromSlash(ref)))
	}
	s.files[4] = statSig(filepath.Join(loc.CommonDir, "packed-refs"))
	s.files[5] = statSig(filepath.Join(loc.CommonDir, "reftable", "tables.list"))
	s.files[6] = statSig(filepath.Join(loc.CommonDir, "shallow"))
	s.files[7] = fileSig{ok: rebaseInProgress(loc.GitDir)}
	if foldPath(loc.GitDir) != foldPath(loc.CommonDir) {
		// A linked worktree of a reftable repo keeps its HEAD in a stack
		// of its own.
		s.files[8] = statSig(filepath.Join(loc.GitDir, "reftable", "tables.list"))
	}
	return s
}

// readSmall reads at most max bytes of name.
func readSmall(name string, max int) ([]byte, error) {
	f, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	buf := make([]byte, max)
	n, err := f.Read(buf)
	if n > 0 {
		return buf[:n], nil
	}
	return nil, err
}
