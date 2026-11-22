// SPDX-License-Identifier: AGPL-3.0-or-later

// Package tailers reads harnesses' log files as the harnesses write them,
// for the adapters that parse them (DESIGN 3.3; CP 3.2; docs/PLAN.md,
// WP-AD3). One Tailer serves every adapter that reads logs
// (adapters.LogReader): it watches each adapter's log roots and hands each
// complete new line of a log file to the agent, in the order the file holds
// them, which has the line's adapter parse it.
//
// It follows CP 3.2:
//
//   - It watches each root and the folders that hold a hot file, one a
//     harness wrote in the last HotWindow, through fsnotify, never a whole
//     tree, and lists every root again each RescanEvery for what the
//     notifications missed. On macOS, where fsnotify opens a descriptor
//     for every file of a watched folder, it watches fewer folders.
//   - It stats each hot file every HotPoll while it grew in the last
//     QuietAfter, then every QuietPoll, which covers Windows' late size
//     notifications and filesystems that send none.
//   - It keeps a cursor per file in state.json: the file's identity (its
//     device and inode, or volume and file index), how far it has handed
//     lines over (read), the hash of the last line it handed over, and
//     where the file's adapter needs it to start again after a restart
//     (resume), since an adapter can't save the content it holds while it
//     waits for a later line (adapters.LogReader.LogResume). After a
//     restart it hands the lines from resume to read over again as
//     replays, of which the adapter makes no record.
//   - A file that shrank, or whose identity changed, was truncated or
//     replaced. When the line that ended where the tailer had read still
//     ends there, the file kept what was read and it goes on; else it reads
//     the file from its start, and the lines up to and including one that
//     hashes like the last line it read come as replays, so nothing that
//     was there before is recorded twice.
//   - It opens a file only to read it and closes it at once, so a harness
//     on Windows can always rename or delete its file.
//   - A line is handed over only once its newline is written. A line longer
//     than MaxLine is handed over without its bytes, marked too long.
//
// The first time the tailer lists a root, it takes up every file there at
// the end of its last complete line: what came before is the time before
// pairtime read the root, which lookback reads, at low priority, for the
// files written in the last LookbackWindow (lookback.go). A file that
// appears after that is read from its start.
package tailers

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/some-du6e/pairtime/internal/adapters"
	"github.com/some-du6e/pairtime/internal/agent/sched"
	"github.com/some-du6e/pairtime/pkg/event"
)

// The tailer's timing (CP 3.2).
const (
	// HotWindow is how recently a file must have been written to be hot:
	// polled, its folder watched, and read from its start when the tailer
	// has no cursor for it.
	HotWindow = 10 * time.Minute
	// HotPoll is how often a hot file is looked at while one grew in the
	// last QuietAfter, and QuietPoll how often after that.
	HotPoll    = 5 * time.Second
	QuietPoll  = 30 * time.Second
	QuietAfter = 2 * time.Minute
	// RescanEvery is how often every root is listed again.
	RescanEvery = time.Minute
	// IdleAfter is how long a file goes without a new line before its
	// adapter, when it holds something back, hears that it is idle
	// (adapters.LogLine.Idle).
	IdleAfter = 10 * time.Second
)

// Bounds on what one read takes.
const (
	// MaxLine is the longest line handed over with its bytes.
	MaxLine = 8 << 20
	// batchLines and batchBytes bound one handover.
	batchLines = 256
	batchBytes = 1 << 20
	// readBuffer is the read buffer of one file.
	readBuffer = 64 << 10
)

// stateKey is the tailer's key in state.json, and stateVersion its format.
const (
	stateKey     = "tailers"
	stateVersion = 1
)

// maxWatched bounds what fsnotify watches at once: an inotify watch pins
// a folder's inode on Linux, and on macOS each watched file costs a
// descriptor (CP 3.1).
const maxWatched = 32

// watchFiles says fsnotify watches the hot files themselves, not their
// folders: on macOS, where its kqueue backend opens a descriptor for every
// entry of a watched folder, which for a folder of a month's transcripts is
// hundreds (CP 3.1, 3.2). New files there are found by the polls and the
// rescans.
var watchFiles = runtime.GOOS == "darwin"

// Root is a log root and the harness whose adapter reads it.
type Root struct {
	Harness event.Harness
	adapters.LogRoot
}

// Line is one line of a log file, handed to the agent with where it lies.
type Line struct {
	Harness event.Harness
	Log     adapters.LogLine
	// Bytes is the line without its newline, and nil for a line too long or
	// an idle marker.
	Bytes []byte
	// Hash names the line's content, which the cursor keeps for the last
	// line handed over (Advance).
	Hash string
	// Lookback, set for a line of a file's history (lookback.go) and for
	// the idle marker at its end, is the Unix ms its records may start at,
	// LookbackWindow before the tailer took the file up: the agent keeps
	// those of its records that fall between then and the start of its
	// ledger.
	Lookback int64
	// Fresh says the line's adapter is to start the file afresh at it:
	// what came before it, a history lookback gave up on, stays unread,
	// so an adapter with no memory of the file has none to rebuild.
	Fresh bool
}

// Batch is lines of one file, in the order the file holds them.
type Batch []Line

// Store keeps the cursors across agent restarts: state.json
// (internal/agent/state).
type Store interface {
	Get(key string, v any) (bool, error)
	Put(key string, v any) error
}

// Options configure a Tailer.
type Options struct {
	// Roots are the roots to watch. Hint adds more.
	Roots []Root
	// Store keeps the cursors; nil keeps them in memory only.
	Store Store
	// Sched runs the tailer's timers. Nil runs none, for tests, which call
	// Step.
	Sched *sched.Scheduler
	// Deliver hands lines of one file to the agent, on the tailer's
	// goroutine. The agent takes them in order and calls Advance for each
	// it took, and returns how many that was. Fewer than all leaves the
	// file waiting for its next line to be taken, which the tailer offers
	// again after Poke.
	Deliver func(Batch) int
	// Forget hears of a file that is gone, for its adapter to forget it.
	Forget func(h event.Harness, file string)
	// Ready, when set, holds the tailer's first look at its roots until it
	// is closed: the agent closes it once the hook calls that came while it
	// was down are with the adapters, so a session's hooks reach its
	// adapter before its transcript does.
	Ready <-chan struct{}
	// Now is the wall clock. Nil means time.Now.
	Now func() time.Time
	// Log gets what goes wrong. Nil discards it.
	Log *slog.Logger
	// NoNotify watches with polls and rescans alone, without fsnotify.
	NoNotify bool
	// Lookback starts lookback's reader, a child process that runs
	// ServeLookback at low priority. Nil reads no file's history: a
	// root's first listing takes its files up at their end, and that is
	// all.
	Lookback func() (Child, error)
	// Progress, when set, hears how far lookback has read, on the
	// goroutine that reads.
	Progress func(Progress)
	// LookbackUntil, when set, says when lookback's records end, at the
	// start of the agent's ledger, or the zero time while that isn't
	// known: a root's first listing gives no file a history when that end
	// is LookbackWindow ago or longer, since none of its records would
	// count.
	LookbackUntil func() time.Time
}

// Tailer watches log roots and hands new lines of their files over.
type Tailer struct {
	opts Options

	mu    sync.Mutex
	roots []Root
	files map[string]*file
	// scanDue says the roots are to be listed again, and checkAll that
	// every file with a cursor is to be looked at, as at the start.
	scanDue, checkAll bool
	// watching are the folders, or on macOS the files, fsnotify watches,
	// and listed the roots the tailer has listed once. unread are the
	// folders a root's first listing couldn't read: their files are taken
	// up at their end once the tailer can read them.
	watching map[string]bool
	listed   map[string]bool
	unread   map[string]bool
	// lastPoll is when the hot files were last looked at, and lastPut the
	// cursors as Put last handed them to Store.
	lastPoll time.Time
	lastPut  []byte
	// pokes counts the Pokes, so a read whose lines weren't all taken can
	// tell whether one came while it waited for Deliver.
	pokes uint64

	w       *fsnotify.Watcher
	kick    chan struct{}
	stop    chan struct{}
	done    chan struct{}
	stopped bool
	jobs    []*sched.Job
	poll    *sched.Job
	every   time.Duration // poll's interval, 0 while it is stopped

	lb lookbackState
}

// file is what the tailer knows of one log file.
type file struct {
	key, path string
	harness   event.Harness
	// id, size and mtime are the file as last seen.
	id    string
	size  int64
	mtime time.Time
	// read is how far the file's lines were handed over and taken, and
	// hash the last of them's Hash; resume is where its adapter needs a
	// restart to start (Advance).
	read, resume int64
	hash         string
	// pos is where the next line to hand over starts: resume after a
	// restart, read otherwise. drained is the size the file had when a
	// read last found no whole line after pos, so a last line still being
	// written doesn't keep its adapter from hearing the file is idle.
	pos, drained int64
	// grew is when the tailer saw the file grow last, statAt when it last
	// looked at it and idleAt when its adapter last heard it was idle; due
	// says it is to be read, waiting that its next line wasn't taken, and
	// rewound that its adapter has it read again from its start (Rewind).
	grew, statAt, idleAt  time.Time
	due, waiting, rewound bool
	// hist is where the file's history ends, the lines before it being
	// lookback's, and histFrom when their records may start (Line.Lookback),
	// both 0 for a file with none. histHash is the Hash of the line that
	// ends there, "" when unknown, which the cursor takes when lookback
	// gives the history up, so relocate can still tell the file. histDone
	// says lookback is done with it. rewindTo is where the lines a Rewind
	// gave lookback end, which it reads as replays, and 0 for none.
	hist, histFrom int64
	histHash       string
	histDone       bool
	rewindTo       int64
	// fresh is where a history lookback gave up on ended, at which the
	// file's next line starts its adapter afresh (Line.Fresh); 0 for none.
	fresh int64
}

// readAll says the tailer has handed over every whole line of f, as it
// last saw it. t.mu is held.
func (f *file) readAll() bool { return f.pos >= f.size || f.drained == f.size }

// active is when f was last written, by the tailer's sight or its mtime.
func (f *file) active() time.Time {
	if f.mtime.After(f.grew) {
		return f.mtime
	}
	return f.grew
}

// saved is what Store keeps under stateKey.
type saved struct {
	V     int         `json:"v"`
	Files []savedFile `json:"files"`
	// Roots are the roots the tailer has listed once, whose files were all
	// taken up then (scan), but those in Unread, folders the listing
	// couldn't read.
	Roots  []string `json:"roots,omitempty"`
	Unread []string `json:"unread,omitempty"`
}

type savedFile struct {
	Path    string        `json:"path"`
	Harness event.Harness `json:"harness"`
	ID      string        `json:"id,omitempty"`
	Size    int64         `json:"size"`
	MTime   int64         `json:"mtime"` // Unix ns, as the file system gives it
	Read    int64         `json:"read"`
	Resume  int64         `json:"resume"`
	Hash    string        `json:"hash,omitempty"`
	// Hist, HistFrom, HistHash, HistDone and RewindTo are the file's
	// history and what a Rewind gave lookback (file.hist).
	Hist     int64  `json:"hist,omitempty"`
	HistFrom int64  `json:"hist_from,omitempty"`
	HistHash string `json:"hist_hash,omitempty"`
	HistDone bool   `json:"hist_done,omitempty"`
	RewindTo int64  `json:"rewind_to,omitempty"`
	Fresh    int64  `json:"fresh,omitempty"`
}

// New makes a Tailer with the cursors Store kept. It reads nothing until
// Start or Step.
func New(opts Options) *Tailer {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Log == nil {
		opts.Log = slog.New(slog.DiscardHandler)
	}
	t := &Tailer{
		opts:     opts,
		roots:    slices.Clone(opts.Roots),
		files:    make(map[string]*file),
		watching: make(map[string]bool),
		listed:   make(map[string]bool),
		unread:   make(map[string]bool),
		scanDue:  true,
		checkAll: true,
		kick:     make(chan struct{}, 1),
		stop:     make(chan struct{}),
		done:     make(chan struct{}),
	}
	t.lb.poke = make(chan struct{}, 1)
	for i := range t.roots {
		t.roots[i].Dir = filepath.Clean(t.roots[i].Dir)
	}
	t.load()
	return t
}

// load takes the cursors Store kept. A damaged entry is dropped, like any
// damaged state (DESIGN 3.5), and the hot files are then read afresh.
func (t *Tailer) load() {
	if t.opts.Store == nil {
		return
	}
	var st saved
	ok, err := t.opts.Store.Get(stateKey, &st)
	switch {
	case err != nil:
		t.opts.Log.Warn("tailers: state.json's tailers entry doesn't parse, so the tailers start afresh", "err", err)
		return
	case !ok || st.V != stateVersion:
		return
	}
	for _, d := range st.Roots {
		t.listed[d] = true
	}
	for _, d := range st.Unread {
		t.unread[d] = true
	}
	for _, sf := range st.Files {
		if sf.Path == "" || !filepath.IsAbs(sf.Path) || t.files[sf.Path] != nil || sf.Read < 0 || sf.Resume < 0 || sf.Resume > sf.Read ||
			sf.Hist < 0 || sf.RewindTo < 0 {
			continue
		}
		// A file whose adapter needs lines again, or that has lines the
		// agent didn't take before it stopped, is read at once: no change to
		// the file may come to say so.
		f := &file{key: sf.Path, path: sf.Path, harness: sf.Harness, id: sf.ID, size: sf.Size,
			mtime: time.Unix(0, sf.MTime), read: sf.Read, resume: sf.Resume, hash: sf.Hash, pos: sf.Resume,
			due: sf.Resume < sf.Read || sf.Read < sf.Size, hist: sf.Hist, histFrom: sf.HistFrom, histHash: sf.HistHash,
			histDone: sf.HistDone, rewindTo: sf.RewindTo, fresh: sf.Fresh}
		if f.lookbacks() && t.opts.Lookback == nil {
			f.leaveHistory()
		}
		t.files[sf.Path] = f
	}
}

// Put hands the cursors to Store, in memory, and reports whether they
// changed since the last Put. The agent writes state.json with the
// adapters' memory in the same Save, so the two agree.
func (t *Tailer) Put() (bool, error) {
	if t.opts.Store == nil {
		return false, nil
	}
	t.mu.Lock()
	st := saved{V: stateVersion, Files: make([]savedFile, 0, len(t.files))}
	for _, f := range t.files {
		st.Files = append(st.Files, savedFile{Path: f.path, Harness: f.harness, ID: f.id, Size: f.size,
			MTime: f.mtime.UnixNano(), Read: f.read, Resume: f.resume, Hash: f.hash,
			Hist: f.hist, HistFrom: f.histFrom, HistHash: f.histHash, HistDone: f.histDone, RewindTo: f.rewindTo, Fresh: f.fresh})
	}
	for d := range t.listed {
		st.Roots = append(st.Roots, d)
	}
	for d := range t.unread {
		st.Unread = append(st.Unread, d)
	}
	last := t.lastPut
	t.mu.Unlock()
	slices.Sort(st.Roots)
	slices.Sort(st.Unread)
	slices.SortFunc(st.Files, func(a, b savedFile) int { return strings.Compare(a.Path, b.Path) })
	b, err := json.Marshal(st)
	if err != nil {
		return false, err
	}
	if bytes.Equal(b, last) {
		return false, nil
	}
	if err := t.opts.Store.Put(stateKey, json.RawMessage(b)); err != nil {
		return false, err
	}
	t.mu.Lock()
	t.lastPut = b
	t.mu.Unlock()
	return true, nil
}

// AddRoots watches roots too, from the next step on. A root whose folder
// the tailer watches already is left out.
func (t *Tailer) AddRoots(roots []Root) {
	t.mu.Lock()
	for _, r := range roots {
		r.Dir = filepath.Clean(r.Dir)
		if r.Dir == "" || !filepath.IsAbs(r.Dir) || slices.ContainsFunc(t.roots, func(o Root) bool { return o.Dir == r.Dir }) {
			continue
		}
		t.roots = append(t.roots, r)
	}
	t.scanDue = true
	t.mu.Unlock()
	t.wake()
}

// Advance records that the agent took a line of file ending at end, whose
// Hash is hash, and that the file's adapter needs a restart to start at
// resume. The agent calls it for each line it takes, before Deliver
// returns, so that what it saves of the cursors and of the adapter's memory
// is of the same point.
func (t *Tailer) Advance(key string, end int64, hash string, resume int64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	f := t.files[key]
	if f == nil {
		return
	}
	if end > f.read || end == f.read && hash != "" {
		f.read, f.hash = end, hash
	}
	f.resume = min(max(resume, 0), f.read)
	f.pos = max(f.pos, end)
}

// Poke has the tailer offer the lines that waited again, as when the
// project of their folder became known.
func (t *Tailer) Poke() {
	t.mu.Lock()
	t.pokes++
	for _, f := range t.files {
		if f.waiting {
			f.waiting, f.due = false, true
		}
	}
	t.mu.Unlock()
	select {
	case t.lb.poke <- struct{}{}:
	default:
	}
	t.wake()
}

// Hint has the tailer read the log file at path soon, which a hook call of
// the harness named: a session is being written there now. When no root
// holds the file, root is watched from now on.
func (t *Tailer) Hint(h event.Harness, path string, root adapters.LogRoot) {
	if !filepath.IsAbs(path) {
		return
	}
	path = filepath.Clean(path)
	if fi, err := os.Stat(path); err == nil && !fi.Mode().IsRegular() {
		// No log file: a FIFO there would hold up the read (openLog).
		return
	}
	t.mu.Lock()
	r, rel, ok := t.rootOf(path)
	if !ok {
		root.Dir = filepath.Clean(root.Dir)
		if rel, ok = relUnder(root.Dir, path); !ok || root.Match == nil {
			t.mu.Unlock()
			return
		}
		r = Root{Harness: h, LogRoot: root}
		t.roots = append(t.roots, r)
	}
	switch {
	case !r.Match(rel):
	case !t.listed[r.Dir]:
		// The root's first listing takes the file up, as every other there.
		t.scanDue = true
	default:
		f := t.files[path]
		if f == nil {
			f = &file{key: path, path: path, harness: r.Harness}
			t.files[path] = f
		}
		f.due = true
		f.grew = t.opts.Now()
	}
	t.mu.Unlock()
	t.wake()
}

// Rewind has the tailer read the file key from its start again, the lines
// it had handed over as replays: its adapter has no memory of the file, as
// after a restore, and rebuilds it from them. The agent calls it while it
// takes a batch of the file, instead of taking the line. When lookback's
// reader can read now, the lines up to where the tailer had read go
// through it, trimmed and at its priority, as a history of the file
// (lookback.go), since a file forgotten while lookback reads hundreds of
// others can be a long one; a job that runs starts again to take the file
// up. Otherwise the tailer reads them itself, but those of a history
// still to read, which wait for the next child.
func (t *Tailer) Rewind(key string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	f := t.files[key]
	if f == nil {
		return
	}
	f.pos, f.resume, f.rewound = 0, 0, true
	switch {
	case f.hist > 0 && !f.histDone && f.read < f.hist && t.lookbackUsable():
		// Its history is still to read: lookback reads it from the start.
	case f.read > 0 && t.lookbackNow():
		f.rewindTo = f.read
	case f.lookbacks():
		f.leaveHistory()
	}
	if f.inHistory() && t.lb.running {
		t.lb.again = true
		select {
		case t.lb.poke <- struct{}{}:
		default:
		}
	}
}

// rootOf returns the root that holds path, and path under it. t.mu is held.
func (t *Tailer) rootOf(path string) (Root, string, bool) {
	for _, r := range t.roots {
		if rel, ok := relUnder(r.Dir, path); ok {
			return r, rel, true
		}
	}
	return Root{}, "", false
}

// relUnder returns path under dir, with '/' between its parts, when dir
// holds it. filepath.Rel compares Windows paths without case, as Windows
// does.
func relUnder(dir, path string) (string, bool) {
	rel, err := filepath.Rel(dir, path)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", false
	}
	return filepath.ToSlash(rel), true
}

// Start runs the tailer on a goroutine of its own and returns the step
// that stops it.
func (t *Tailer) Start() func() {
	if !t.opts.NoNotify {
		if w, err := fsnotify.NewWatcher(); err != nil {
			t.opts.Log.Warn("tailers: file notifications don't start, so the tailers poll alone", "err", err)
		} else {
			t.w = w
		}
	}
	if s := t.opts.Sched; s != nil {
		t.jobs = append(t.jobs, s.Every("tailers.rescan", RescanEvery, func(time.Time) {
			t.mu.Lock()
			t.scanDue = true
			t.mu.Unlock()
			t.wake()
		}))
		t.poll = s.Every("tailers.poll", HotPoll, func(time.Time) { t.wake() })
		t.poll.Stop()
	}
	go t.run()
	return func() {
		t.mu.Lock()
		t.stopped = true
		child := t.lb.child
		t.mu.Unlock()
		close(t.stop)
		if child != nil {
			child.Kill()
		}
		t.lb.wg.Wait()
		<-t.done
		for _, j := range t.jobs {
			j.Stop()
		}
		if t.poll != nil {
			t.poll.Stop()
		}
		if t.w != nil {
			t.w.Close()
		}
	}
}

func (t *Tailer) wake() {
	select {
	case t.kick <- struct{}{}:
	default:
	}
}

func (t *Tailer) run() {
	defer close(t.done)
	if t.opts.Ready != nil {
		select {
		case <-t.opts.Ready:
		case <-t.stop:
			return
		}
	}
	var events <-chan fsnotify.Event
	var errs <-chan error
	if t.w != nil {
		events, errs = t.w.Events, t.w.Errors
	}
	for {
		t.Step()
		select {
		case <-t.stop:
			return
		case <-t.kick:
		case ev, ok := <-events:
			if !ok {
				events = nil
				continue
			}
			t.notified(ev)
		case err, ok := <-errs:
			if !ok {
				errs = nil
				continue
			}
			// An overflow drops events, so the roots are listed again.
			t.opts.Log.Warn("tailers: file notifications", "err", err)
			t.mu.Lock()
			t.scanDue = true
			t.mu.Unlock()
		}
	}
}

// notified marks what an fsnotify event names as due: the file, or, for a
// folder or a file the tailer doesn't know, the roots, to be listed again.
func (t *Tailer) notified(ev fsnotify.Event) {
	t.mu.Lock()
	defer t.mu.Unlock()
	name := filepath.Clean(ev.Name)
	if t.watching[name] && (ev.Has(fsnotify.Remove) || ev.Has(fsnotify.Rename)) {
		// fsnotify dropped its watch with the path, so the next step adds
		// it again, once something is there.
		delete(t.watching, name)
	}
	if f := t.files[name]; f != nil {
		f.due = true
		return
	}
	t.scanDue = true
}

// Step does what is due: lists the roots, looks at the hot files and reads
// what grew. The tailer's goroutine runs it on every wakeup, and tests call
// it.
func (t *Tailer) Step() {
	now := t.opts.Now()
	t.mu.Lock()
	scan, all := t.scanDue, t.checkAll
	t.scanDue, t.checkAll = false, false
	pollDue := now.Sub(t.lastPoll) >= HotPoll-time.Second/2
	if pollDue {
		t.lastPoll = now
	}
	t.mu.Unlock()
	if scan {
		t.scan(now)
	}
	t.look(now, all, pollDue)
	for _, f := range t.dueFiles() {
		if t.stopping() {
			return
		}
		t.read(f, now)
	}
	t.watch(now)
	t.pace(now)
	t.lookbackDue(now)
}

func (t *Tailer) stopping() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.stopped
}

// scan lists every root. A file the tailer has no cursor for gets one: at
// its end on the root's first listing, and at its start after that, when
// it is a new file. A known file that changed is due.
func (t *Tailer) scan(now time.Time) {
	t.mu.Lock()
	roots := slices.Clone(t.roots)
	// Every folder above a known file, so the walk goes down to it, and
	// the known files of each folder, so it finds those that are gone.
	known := make(map[string]bool)
	byDir := make(map[string][]*file)
	for _, f := range t.files {
		byDir[filepath.Dir(f.path)] = append(byDir[filepath.Dir(f.path)], f)
		for d := filepath.Dir(f.path); !known[d]; d = filepath.Dir(d) {
			known[d] = true
			if filepath.Dir(d) == d {
				break
			}
		}
	}
	t.mu.Unlock()
	for _, r := range roots {
		t.mu.Lock()
		first := !t.listed[r.Dir]
		t.mu.Unlock()
		if t.walk(r, r.Dir, "", 0, now, known, byDir, first) {
			t.mu.Lock()
			t.listed[r.Dir] = true
			t.mu.Unlock()
		}
	}
}

// walk lists dir, rel under root r, and the folders below it to r.Depth.
// Below the first level it goes only into folders written in the last
// HotWindow or holding a file it knows, so an idle root costs a listing of
// its top folders.
//
// On a root's first listing, first, it takes every file up at its end
// (adopt), and a folder it can't read then is one whose files it takes up
// so once it can (Tailer.unread). It reports whether it could list dir, or
// found it missing: a root that doesn't exist yet counts as listed, and
// its files, when they come, are new. A known file, of byDir, that its
// folder's listing no longer holds is gone.
func (t *Tailer) walk(r Root, dir, rel string, depth int, now time.Time, known map[string]bool, byDir map[string][]*file, first bool) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			if first {
				t.mu.Lock()
				t.unread[dir] = true
				t.mu.Unlock()
			}
			t.opts.Log.Debug("tailers: listing a log folder", "err", err)
			return false
		}
		return true
	}
	t.mu.Lock()
	if t.unread[dir] {
		first = true
		delete(t.unread, dir)
	}
	t.mu.Unlock()
	if mine := byDir[dir]; len(mine) > 0 {
		names := make(map[string]bool, len(entries))
		for _, de := range entries {
			names[de.Name()] = true
		}
		for _, f := range mine {
			// Not in its folder: deleted while nothing watched it. Lstat
			// says whether it is, since a path a hook spelled otherwise, on
			// a file system that ignores case, is still there.
			if !names[filepath.Base(f.path)] {
				if _, err := os.Lstat(f.path); errors.Is(err, fs.ErrNotExist) {
					t.gone(f)
				}
			}
		}
	}
	// Folders named for a hot file of this folder, as Claude Code names a
	// session's folder of subagents for its transcript, are read whatever
	// their own times say.
	stems := make(map[string]bool)
	for _, de := range entries {
		if fi, err := de.Info(); err == nil && !de.IsDir() && now.Sub(fi.ModTime()) <= HotWindow {
			stems[strings.TrimSuffix(de.Name(), filepath.Ext(de.Name()))] = true
		}
	}
	for _, de := range entries {
		name := de.Name()
		sub := name
		if rel != "" {
			sub = rel + "/" + name
		}
		full := filepath.Join(dir, name)
		if de.IsDir() {
			if depth+1 >= r.Depth {
				continue
			}
			if depth >= 1 && !known[full] && !stems[name] && !first && !t.isUnread(full) {
				fi, err := de.Info()
				if err != nil || now.Sub(fi.ModTime()) > HotWindow {
					continue
				}
			}
			t.walk(r, full, sub, depth+1, now, known, byDir, first)
			continue
		}
		if !de.Type().IsRegular() || r.Match == nil || !r.Match(sub) {
			continue
		}
		fi, err := de.Info()
		if err != nil {
			continue
		}
		t.mu.Lock()
		f := t.files[full]
		switch {
		case f == nil && first:
			t.mu.Unlock()
			t.adopt(r, full, now)
			continue
		case f == nil:
			// A file that came since the root was listed: a new session.
			t.files[full] = &file{key: full, path: full, harness: r.Harness, due: true, grew: now}
		case fi.Size() != f.size || !fi.ModTime().Equal(f.mtime):
			f.due = true
		}
		t.mu.Unlock()
	}
	return true
}

// isUnread reports whether a root's first listing couldn't read dir.
func (t *Tailer) isUnread(dir string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.unread[dir]
}

// adopt takes the file at path up at the end of its last complete line,
// as the tailer does for each file of a root it lists the first time. With
// lookback, a file written in the last LookbackWindow has what came before
// as its history, which the tailer leaves to lookback: its cursor starts
// at the file's start, and the tailer reads it live from where lookback
// gets to.
func (t *Tailer) adopt(r Root, path string, now time.Time) {
	fh, err := openLog(path)
	if err != nil {
		return
	}
	defer fh.Close()
	fi, err := fh.Stat()
	if err != nil {
		return
	}
	end, hash, err := lastLine(fh, fi.Size())
	if err != nil {
		return
	}
	// Before the lock: LookbackUntil is the agent's.
	history := t.opts.Lookback != nil && end > 0 && now.Sub(fi.ModTime()) <= LookbackWindow && t.lookbackCounts(now)
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.files[path] == nil {
		f := &file{key: path, path: path, harness: r.Harness, id: fileID(fh, fi), size: fi.Size(),
			mtime: fi.ModTime(), read: end, resume: end, pos: end, hash: hash}
		if history && t.lookbackUsable() {
			f.hist, f.histFrom, f.histHash = end, now.Add(-LookbackWindow).UnixMilli(), hash
			f.read, f.resume, f.pos, f.hash = 0, 0, 0, ""
		}
		t.files[path] = f
	}
}

// lastLine returns where the last complete line of a file of size bytes
// ends, and its Hash, reading back from the end: 0 and "" for a file with
// none. Blank lines after it are no lines: the tailers take them into the
// next line's span, so a file taken up there reads on as it would have. A
// last line longer than readLine reads has its end and no hash.
func lastLine(r io.ReaderAt, size int64) (int64, string, error) {
	// w holds the file from at to end, read back no further than the line
	// looked at needs, a buffer at a time, or as much as w holds when that
	// is more, so a long line costs a few reads and copies.
	at, end := size, int64(-1)
	var w []byte
	back := func() (int, error) {
		n := min(max(int64(readBuffer), int64(len(w))), at)
		chunk := make([]byte, n, n+int64(len(w)))
		if _, err := r.ReadAt(chunk, at-n); err != nil && !errors.Is(err, io.EOF) {
			return 0, err
		}
		at -= n
		w = append(chunk, w...)
		return int(n), nil
	}
	// The end, after the last newline, which a last line still being
	// written can put further back than MaxLine.
	for end < 0 && at > 0 {
		if _, err := back(); err != nil {
			return 0, "", err
		}
		if i := bytes.LastIndexByte(w, '\n'); i >= 0 {
			end, w = at+int64(i)+1, w[:i+1]
		} else {
			w = w[:0]
		}
	}
	for end > 0 {
		// The line that ends at end, without its newline: w ends with that
		// newline, and unseen is how much of w before it no search looked
		// at.
		var line []byte
		long := false
		for unseen := len(w) - 1; ; {
			if j := bytes.LastIndexByte(w[:unseen], '\n'); j >= 0 {
				line = w[j+1 : len(w)-1]
				break
			}
			if at == 0 {
				line = w[:len(w)-1]
				break
			}
			if int64(len(w)-1) > MaxLine+1 {
				long = true
				break
			}
			n, err := back()
			if err != nil {
				return 0, "", err
			}
			unseen = n
		}
		switch {
		case long || int64(len(line)) > MaxLine+1:
			// Longer than readLine reads: the file goes on from its end,
			// and a truncation is told by the size alone.
			return end, "", nil
		case len(bytes.TrimSpace(line)) > 0:
			return end, hashLine(bytes.TrimRight(line, "\r")), nil
		}
		end -= int64(len(line)) + 1
		w = w[:len(w)-len(line)-1]
	}
	return 0, "", nil
}

// look stats the files that want it: every known file at the start, and
// the hot ones when a poll is due. One that changed is due, one that is
// gone is forgotten, and one whose adapter holds something back hears it
// is idle once it hasn't grown for IdleAfter.
func (t *Tailer) look(now time.Time, all, poll bool) {
	t.mu.Lock()
	var check []*file
	for _, f := range t.files {
		last := f.active()
		every := HotPoll
		if now.Sub(last) > QuietAfter {
			every = QuietPoll
		}
		if all || poll && now.Sub(last) <= HotWindow && now.Sub(f.statAt) >= every-time.Second/2 {
			check = append(check, f)
			f.statAt = now
		}
		if f.resume < f.read && f.readAll() && !f.waiting && now.Sub(last) >= IdleAfter && now.Sub(f.idleAt) >= IdleAfter {
			f.due = true
		}
	}
	t.mu.Unlock()
	for _, f := range check {
		fi, err := os.Stat(f.path)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			t.gone(f)
		case err == nil:
			t.mu.Lock()
			if fi.Size() != f.size || !fi.ModTime().Equal(f.mtime) {
				f.due = true
			}
			t.mu.Unlock()
		}
	}
}

// gone forgets f, which is gone, and has its adapter forget it.
func (t *Tailer) gone(f *file) {
	t.mu.Lock()
	ours := t.files[f.key] == f
	if ours {
		delete(t.files, f.key)
	}
	t.mu.Unlock()
	if ours && t.opts.Forget != nil {
		t.opts.Forget(f.harness, f.key)
	}
}

// dueFiles returns the files to read, written last first.
func (t *Tailer) dueFiles() []*file {
	t.mu.Lock()
	defer t.mu.Unlock()
	var out []*file
	for _, f := range t.files {
		if f.due && !f.waiting && !f.inHistory() {
			out = append(out, f)
		}
	}
	slices.SortFunc(out, func(a, b *file) int { return b.mtime.Compare(a.mtime) })
	return out
}

// read reads f from where its next line starts to its last complete line
// and hands the lines over, a batch at a time.
func (t *Tailer) read(f *file, now time.Time) {
	t.mu.Lock()
	f.due = false
	t.mu.Unlock()
	fh, err := openLog(f.path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			t.gone(f)
		} else {
			t.opts.Log.Debug("tailers: opening a log file", "err", err)
		}
		return
	}
	defer fh.Close()
	fi, err := fh.Stat()
	if err != nil {
		return
	}
	id, size := fileID(fh, fi), fi.Size()
	t.mu.Lock()
	replaced := f.id != "" && id != "" && id != f.id
	moved := replaced || size < f.read
	t.mu.Unlock()
	if moved {
		t.relocate(fh, f, size, replaced)
	}
	t.mu.Lock()
	if size > f.size || moved {
		f.grew = now
	}
	f.id, f.size, f.mtime = id, size, fi.ModTime()
	pos, read, hist, histFrom, fresh := f.pos, f.read, f.hist, f.histFrom, f.fresh
	t.mu.Unlock()

	if pos < size {
		if _, err := fh.Seek(pos, io.SeekStart); err != nil {
			return
		}
		br := bufio.NewReaderSize(fh, readBuffer)
		for {
			batch, next, more, failed := t.lines(br, f, pos, read, hist, histFrom)
			for i := range batch {
				batch[i].Fresh = fresh > 0 && batch[i].Log.Offset == fresh
			}
			if len(batch) > 0 {
				t.mu.Lock()
				pokes := t.pokes
				t.mu.Unlock()
				n := t.opts.Deliver(batch)
				if n < len(batch) {
					t.mu.Lock()
					switch {
					case f.rewound:
						f.rewound, f.due = false, true
					case t.pokes != pokes:
						// A Poke came while Deliver ran, before the file
						// was waiting: it is offered again at once.
						f.due = true
					default:
						f.waiting = true
					}
					t.mu.Unlock()
					return
				}
			}
			pos = next
			t.mu.Lock()
			f.pos = max(f.pos, pos)
			if failed {
				// The read failed before the file's end: the next look
				// finds the file changed and reads on from pos.
				f.mtime = time.Time{}
			} else if !more {
				f.drained = size
			}
			t.mu.Unlock()
			if failed {
				t.opts.Log.Debug("tailers: reading a log file failed, and is tried again")
				return
			}
			if !more {
				break
			}
		}
	}
	t.idle(f, now)
}

// idle tells f's adapter that f is idle, every IdleAfter while the adapter
// holds something back and f hasn't grown for IdleAfter, or was written
// before that, as a file caught up on after a restart was.
func (t *Tailer) idle(f *file, now time.Time) {
	t.mu.Lock()
	send := f.resume < f.read && f.readAll() && !f.waiting && now.Sub(f.active()) >= IdleAfter &&
		now.Sub(f.idleAt) >= IdleAfter
	at, h, key, path, hash := f.pos, f.harness, f.key, f.path, f.hash
	var lookback int64
	if at <= f.hist {
		// At its history's end: what the marker resolves, such as the
		// history's last turn, is the history's.
		lookback = f.histFrom
	}
	if send {
		f.idleAt = now
	}
	t.mu.Unlock()
	if !send {
		return
	}
	marker := Line{Harness: h, Log: adapters.LogLine{File: key, Path: path, Offset: at, End: at, Idle: true}, Hash: hash,
		Lookback: lookback}
	t.mu.Lock()
	pokes := t.pokes
	t.mu.Unlock()
	if t.opts.Deliver(Batch{marker}) < 1 {
		t.mu.Lock()
		f.idleAt = time.Time{}
		if t.pokes != pokes {
			f.due = true
		} else {
			f.waiting = true
		}
		t.mu.Unlock()
	}
}

// lines reads the next lines of f from pos, which br reads from, up to a
// batch's worth. read is how far an earlier run handed lines over: those
// ending by then are replays. Those that end by hist, f's history, carry
// histFrom (Line.Lookback). It returns the lines, where the next one
// starts, whether there may be more after the batch, and whether the read
// failed before the file's end. A last line with no newline yet isn't
// one, and blank lines are taken into the next line's span, so each line
// starts where the one before it ended.
func (t *Tailer) lines(br *bufio.Reader, f *file, pos, read, hist, histFrom int64) (Batch, int64, bool, bool) {
	var batch Batch
	bytesIn := 0
	start := pos
	for len(batch) < batchLines && bytesIn < batchBytes {
		line, n, tooLong, err := readLine(br)
		if err != nil {
			// io.EOF before a newline: the line isn't whole yet. Any other
			// error is the read's.
			return batch, start, false, !errors.Is(err, io.EOF)
		}
		end := pos + n
		pos = end
		body := bytes.TrimRight(line, "\r\n")
		if !tooLong && len(bytes.TrimSpace(body)) == 0 {
			continue
		}
		l := Line{Harness: f.harness, Log: adapters.LogLine{File: f.key, Path: f.path, Offset: start, End: end,
			Replay: end <= read, TooLong: tooLong}}
		if end <= hist {
			l.Lookback = histFrom
		}
		if !tooLong {
			l.Bytes, l.Hash = body, hashLine(body)
		}
		batch = append(batch, l)
		bytesIn += len(body)
		start = end
	}
	return batch, start, true, false
}

// readLine reads one line through its newline, and how many bytes that
// was. A line longer than MaxLine comes back empty and marked too long,
// read through to its newline.
func readLine(br *bufio.Reader) (line []byte, n int64, tooLong bool, err error) {
	var buf []byte
	for {
		chunk, err := br.ReadSlice('\n')
		n += int64(len(chunk))
		if !tooLong {
			if len(buf)+len(chunk) > MaxLine+2 {
				tooLong, buf = true, nil
			} else {
				buf = append(buf, chunk...)
			}
		}
		switch {
		case err == nil:
			return buf, n, tooLong, nil
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		default:
			return nil, n, false, err
		}
	}
}

// hashLine names a line's content: the first 8 bytes of its SHA-256.
func hashLine(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:8])
}

// relocate handles a file that was truncated or replaced, which replaced
// says. When the line that ended at read still ends there with the same
// content, the file kept what was read, and nothing changes. Otherwise the
// file is read from its start: up to the end of a line that hashes like
// the last one read, which the file kept, the lines come as replays, and
// with no such line all of it is new, for an adapter that starts the file
// afresh (an adapters.LogLine at offset 0). Its history goes where the
// lines it was went: with the copy, to the cut, or, in another file,
// nowhere; and once it was read it is done.
func (t *Tailer) relocate(fh *os.File, f *file, size int64, replaced bool) {
	t.mu.Lock()
	read, hash := f.read, f.hash
	t.mu.Unlock()
	if read > 0 && read <= size && hash != "" {
		// The line that ended at read still ends there: the file kept what
		// was read where it was, and an earlier line like it is no sign of
		// a copy.
		if end, h, err := lastLine(fh, read); err == nil && end == read && h == hash {
			return
		}
	}
	if _, err := fh.Seek(0, io.SeekStart); err != nil {
		return
	}
	br := bufio.NewReaderSize(fh, readBuffer)
	// The first line that hashes like the last one read: the file is a
	// copy of what was read with more after it, or no copy at all.
	var pos, found int64 = 0, -1
	for pos < size && hash != "" {
		line, n, tooLong, err := readLine(br)
		if err != nil {
			break
		}
		pos += n
		if !tooLong && hashLine(bytes.TrimRight(line, "\r\n")) == hash {
			found = pos
			break
		}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if found == read && read > 0 {
		return // the file kept what was read where it was
	}
	f.histDone = f.histDone || f.hist > 0 && read >= f.hist
	switch {
	case found > 0:
		// A copy of what was read, at another offset: the history moves
		// with it, to end at that line at most.
		if f.hist > 0 {
			if moved := min(max(f.hist+found-read, 0), found); moved == found {
				f.hist, f.histHash = moved, hash
			} else {
				f.hist = moved
			}
		}
		f.read, f.resume, f.pos = found, 0, 0
	case replaced:
		// Another file, holding none of what was read: none of it is a
		// history's.
		f.read, f.resume, f.pos, f.hash, f.hist = 0, 0, 0, "", 0
	default:
		// The file was cut: what is left of the history, up to the cut, is
		// still the history.
		f.read, f.resume, f.pos, f.hash = 0, 0, 0, ""
		if size < f.hist {
			f.hist, f.histHash = size, ""
		}
	}
	f.rewindTo, f.fresh = 0, 0
	if f.hist == 0 {
		f.histFrom, f.histHash, f.histDone = 0, "", false
	}
}

// watch keeps fsnotify on the roots and on the folders of the hot files,
// the files grown most recently first, up to maxWatched, or on macOS on
// the hot files alone (watchFiles).
func (t *Tailer) watch(now time.Time) {
	if t.w == nil {
		return
	}
	t.mu.Lock()
	want := make(map[string]bool)
	if !watchFiles {
		for _, r := range t.roots {
			want[r.Dir] = true
		}
	}
	var hot []*file
	for _, f := range t.files {
		if now.Sub(f.active()) <= HotWindow {
			hot = append(hot, f)
		}
	}
	slices.SortFunc(hot, func(a, b *file) int { return b.active().Compare(a.active()) })
	for _, f := range hot {
		if len(want) >= maxWatched {
			break
		}
		if watchFiles {
			want[f.path] = true
		} else {
			want[filepath.Dir(f.path)] = true
		}
	}
	var add, drop []string
	for d := range want {
		if !t.watching[d] {
			add = append(add, d)
		}
	}
	for d := range t.watching {
		if !want[d] {
			drop = append(drop, d)
		}
	}
	t.mu.Unlock()
	// Without the lock: fsnotify's calls can wait on its goroutine.
	for _, d := range drop {
		t.w.Remove(d)
		t.mu.Lock()
		delete(t.watching, d)
		t.mu.Unlock()
	}
	for _, d := range add {
		if err := t.w.Add(d); err != nil {
			continue // a root that doesn't exist yet: the rescan finds it
		}
		t.mu.Lock()
		t.watching[d] = true
		t.mu.Unlock()
	}
}

// pace runs the poll while a file is hot: every HotPoll while one grew in
// the last QuietAfter, and not at all once none has grown in HotWindow.
func (t *Tailer) pace(now time.Time) {
	if t.poll == nil {
		return
	}
	t.mu.Lock()
	var every time.Duration
	for _, f := range t.files {
		if now.Sub(f.active()) <= HotWindow {
			every = HotPoll
			break
		}
	}
	change := every != t.every
	t.every = every
	t.mu.Unlock()
	switch {
	case !change:
	case every == 0:
		t.poll.Stop()
	default:
		t.poll.Reset(every)
	}
}
