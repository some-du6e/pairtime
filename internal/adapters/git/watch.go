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

// agentRunKeep is how long the watcher keeps an agent's git command for
// matching, which only needs ActorWindow plus the hold.
const agentRunKeep = 30 * time.Second

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
}

// WatcherState is what the watcher keeps in state.json: each worktree's
// scanner, by its git directory. The paths are local.
type WatcherState struct {
	Worktrees map[string]ScannerState `json:"worktrees,omitempty"`
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
	fsw  *fsnotify.Watcher

	work chan func()
	stop chan struct{}
	done chan struct{}

	mu    sync.Mutex
	trees map[string]*tree // by foldPath of the git directory
	// saved are the states scanners resume from, by foldPath of the git
	// directory: opts.State's, until a worktree is tracked or dropped.
	saved    map[string]ScannerState
	byDir    map[string]*tree // by foldPath of each watched directory
	runs     []gitRun
	held     []*heldChange
	timer    *time.Timer
	problems map[string]string
	closed   bool
}

type tree struct {
	key  string // foldPath of the git directory
	loc  Location
	scan *Scanner // used by the worker goroutine only
	// state is the scanner's state after its last scan, for State.
	state    ScannerState
	sig      signature
	watched  []string
	debounce *time.Timer
	// dropped is set once the worktree is out of pairtime, so a scan of it
	// that was already queued or running yields nothing.
	dropped bool
}

type heldChange struct {
	c       Change
	t       *tree
	common  string
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
		trees:    make(map[string]*tree),
		saved:    make(map[string]ScannerState),
		byDir:    make(map[string]*tree),
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
		w.saved[foldPath(gitDir)] = st
	}
	if !opts.NoNotify {
		fsw, err := fsnotify.NewWatcher()
		if err != nil {
			w.problems["notify"] = fmt.Sprintf("repos are polled, not watched, because file notifications didn't start: %v", err)
		} else {
			w.fsw = fsw
			go w.events()
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
		w.fsw.Close()
	}
	close(w.stop)
	<-w.done
	w.release(true)
	return nil
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

// State returns what to keep in state.json.
func (w *Watcher) State() WatcherState {
	w.mu.Lock()
	defer w.mu.Unlock()
	st := WatcherState{Worktrees: make(map[string]ScannerState, len(w.trees))}
	for _, t := range w.trees {
		st.Worktrees[t.loc.GitDir] = t.state
	}
	return st
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
	defer w.mu.Unlock()
	if w.closed {
		return loc, errors.New("the git watcher is closed")
	}
	if _, ok := w.trees[key]; ok {
		return loc, nil
	}
	st := w.saved[key]
	delete(w.saved, key)
	t := &tree{key: key, loc: loc, state: st}
	t.scan = NewScanner(w.git, loc, st, w.now, w.res.Rekey)
	w.trees[key] = t
	w.watchLocked(t)
	// The first scan sets the cursor, or catches up from a saved one.
	w.queueLocked(t)
	return loc, nil
}

// watchLocked adds file notifications for a worktree's git directory and
// its logs directory, and for a reftable repo its reftable directory:
// never the working tree, and never recursively (CP 3.2).
func (w *Watcher) watchLocked(t *tree) {
	if w.fsw == nil || len(w.byDir) >= 2*w.opts.MaxWatched {
		return
	}
	dirs := []string{t.loc.GitDir, filepath.Join(t.loc.GitDir, "logs")}
	if fi, err := os.Stat(filepath.Join(t.loc.CommonDir, "reftable")); err == nil && fi.IsDir() {
		dirs = append(dirs, filepath.Join(t.loc.CommonDir, "reftable"))
	}
	for _, d := range dirs {
		k := foldPath(d)
		if _, ok := w.byDir[k]; ok {
			continue
		}
		if err := w.fsw.Add(d); err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				w.log.Debug("git watch", "err", err)
			}
			continue
		}
		w.byDir[k] = t
		t.watched = append(t.watched, d)
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
		case ev, ok := <-w.fsw.Events:
			if !ok {
				return
			}
			if !relevant(ev.Name) {
				continue
			}
			w.mu.Lock()
			t := w.byDir[foldPath(filepath.Dir(ev.Name))]
			if t != nil && !w.closed {
				if filepath.Base(ev.Name) == "logs" && ev.Has(fsnotify.Create) {
					w.watchLocked(t) // a new repo's first commit makes logs/
				}
				if t.debounce == nil {
					tt := t
					t.debounce = time.AfterFunc(w.opts.Debounce, func() { w.poke(tt) })
				} else {
					t.debounce.Reset(w.opts.Debounce)
				}
			}
			w.mu.Unlock()
		case err, ok := <-w.fsw.Errors:
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
	if w.closed {
		return
	}
	if sig := signatureOf(t.loc); sig != t.sig {
		w.queueLocked(t)
	}
}

// Poll looks at every tracked worktree with a few stats each, and scans
// the ones whose HEAD, refs or reflog changed. The agent calls it from its
// scheduler, every minute or so (DESIGN 3.3); it is what finds changes on
// file systems without notifications and past MaxWatched. It also stops
// watching worktrees that are gone.
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
		if _, err := os.Stat(filepath.Join(t.loc.GitDir, "HEAD")); err != nil {
			w.untrackLocked(key, t)
			continue
		}
		if sig := signatureOf(t.loc); sig != t.sig {
			w.queueLocked(t)
		}
	}
}

// dropIgnoredLocked drops every tracked worktree that is now out of
// pairtime.
func (w *Watcher) dropIgnoredLocked() {
	for key, t := range w.trees {
		if w.res.ignoredLoc(t.loc) {
			w.dropLocked(key, t)
		}
	}
}

// dropLocked stops watching a worktree that is now out of pairtime, as
// DESIGN 8.8 asks of a .pairtime-ignore or an exclude pattern added while
// it was tracked: its held changes never reach Sink, and nothing of its
// state is kept.
func (w *Watcher) dropLocked(key string, t *tree) {
	w.untrackLocked(key, t)
	delete(w.saved, key)
	t.dropped = true
	w.held = slices.DeleteFunc(w.held, func(h *heldChange) bool { return h.t == t })
}

func (w *Watcher) untrackLocked(key string, t *tree) {
	for _, d := range t.watched {
		if w.fsw != nil {
			_ = w.fsw.Remove(d)
		}
		delete(w.byDir, foldPath(d))
	}
	if t.debounce != nil {
		t.debounce.Stop()
	}
	delete(w.trees, key)
}

// queueLocked asks the worker to scan t.
func (w *Watcher) queueLocked(t *tree) {
	select {
	case w.work <- func() { w.scan(t) }:
	default:
		// The worker is busy; the queued scans and the next poll catch up.
	}
}

// NoteAgentCommand tells the watcher that an agent's tool ran a shell
// command, line, in cwd, at t: what a Claude Code PostToolUse hook for
// Bash reports. A git command in it marks a git operation in the same repo
// within ActorWindow as the agent's (SM 1.2). The line is parsed here and
// not kept.
func (w *Watcher) NoteAgentCommand(t time.Time, cwd, line string) {
	runs := parseGitRuns(t, cwd, line)
	if len(runs) == 0 {
		return
	}
	select {
	case w.work <- func() { w.addRuns(runs) }:
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

func (w *Watcher) scan(t *tree) {
	w.mu.Lock()
	if t.dropped || w.closed {
		w.mu.Unlock()
		return
	}
	if w.res.ignoredLoc(t.loc) {
		w.dropLocked(t.key, t)
		w.mu.Unlock()
		return
	}
	w.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	sig := signatureOf(t.loc)
	changes, err := t.scan.Scan(ctx)
	loc, state := t.scan.Location(), t.scan.State()
	w.mu.Lock()
	if t.dropped || w.res.ignoredLoc(loc) {
		// Ignored while git was read: none of it counts.
		if !t.dropped {
			w.dropLocked(t.key, t)
		}
		w.mu.Unlock()
		return
	}
	t.sig = sig
	t.loc, t.state = loc, state
	if err != nil {
		w.problems["scan"] = fmt.Sprintf("reading a repo's reflog failed: %v", err)
	} else {
		delete(w.problems, "scan")
	}
	now := w.now()
	for _, c := range changes {
		h := &heldChange{c: c, t: t, common: t.loc.CommonDir, until: now.Add(w.opts.Hold)}
		w.matchLocked(h)
		w.held = append(w.held, h)
	}
	w.mu.Unlock()
	w.release(false)
}

func (w *Watcher) addRuns(runs []gitRun) {
	now := w.now()
	w.mu.Lock()
	w.runs = slices.DeleteFunc(w.runs, func(r gitRun) bool { return now.Sub(r.t) > agentRunKeep })
	w.mu.Unlock()
	// Resolving a folder can run git, so it happens here, without the lock.
	for i := range runs {
		if runs[i].dir == "" {
			continue
		}
		loc, err := w.res.Resolve(context.Background(), runs[i].dir)
		if err != nil || loc.CommonDir == "" {
			runs[i].dir = ""
			continue
		}
		runs[i].dir = loc.CommonDir
	}
	w.mu.Lock()
	w.runs = append(w.runs, runs...)
	for _, h := range w.held {
		w.matchLocked(h)
	}
	w.mu.Unlock()
	w.release(false)
}

// matchLocked marks h as the agent's when a kept agent run explains it.
// runs hold the common directory their folder resolved to.
func (w *Watcher) matchLocked(h *heldChange) {
	if h.decided {
		return
	}
	op := h.c.Op()
	for _, r := range w.runs {
		if runMatches(r, r.dir, h.common, op, h.c.GitStart, h.c.GitEnd) {
			h.c.SetActor(event.ActorAgent)
			h.decided = true
			return
		}
	}
}

// release sends held changes to Sink, in order, while the first is
// decided or has waited long enough; all of them when final is set. It
// arms a timer for the next one.
func (w *Watcher) release(final bool) {
	for {
		w.mu.Lock()
		if len(w.held) == 0 {
			w.mu.Unlock()
			return
		}
		h := w.held[0]
		now := w.now()
		if !final && !h.decided && now.Before(h.until) {
			wait := h.until.Sub(now)
			if w.timer == nil {
				w.timer = time.AfterFunc(wait, func() {
					select {
					case w.work <- func() { w.release(false) }:
					case <-w.stop:
					}
				})
			} else {
				w.timer.Reset(wait)
			}
			w.mu.Unlock()
			return
		}
		w.held = w.held[1:]
		w.mu.Unlock()
		w.opts.Sink(h.c)
	}
}

// signature is what Poll compares to tell whether a worktree's HEAD may
// have moved: the size and modification time of the files a HEAD move,
// a ref update or a reflog append writes.
type signature struct {
	files [8]fileSig
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
// loose ref, packed-refs, the reftable list, the shallow file and the
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
