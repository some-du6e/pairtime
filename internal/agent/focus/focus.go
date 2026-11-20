// SPDX-License-Identifier: AGPL-3.0-or-later

// Package focus is the agent's focus component (DESIGN 3.3). The OS tells
// it each time another window comes to the front, and for each change it
// walks the process table once to write an os.focus record: the window's
// app_class from the shared app-class table (internal/platform), a keyed
// app_hash of the app, the hosted_sessions whose harness processes run
// under the window's process, and the focus_project they resolve to
// (SM 1.2, 3.2). Each change also goes to presence, where focus on a
// terminal, editor or agent app makes a project context live (DESIGN 3.3),
// and asks the process scan to run soon (DESIGN 3.3, "on focus change").
//
// It is event-driven: between changes it holds a goroutine that sleeps on
// a channel and whatever the OS source holds, on Windows one thread asleep
// in GetMessage (internal/platform/windows). Changes that come faster than
// the process table can be read are coalesced, so a burst of switches
// costs one walk, for the window that ended in front.
//
// Window titles are never read, and nothing the OS says about a window
// leaves the device: not its title, its executable's name or path, or its
// process id. The app goes out only as app_hash, a local field keyed with
// a secret derived from the device key (AGENTS.md).
//
// Like presence, it records nothing outside a project context (DESIGN 3.3,
// D14): a change while none is live updates what the component knows, and
// is recorded only if a context goes live while the window is still in
// front.
package focus

import (
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/some-du6e/pairtime/internal/platform"
	"github.com/some-du6e/pairtime/pkg/event"
)

// Change is one report from the OS's focus source: another window came to
// the front, or pairtime can't tell which window is in front.
type Change struct {
	// At is when focus moved, from time.Now, with its monotonic reading.
	At time.Time
	// PID is the process of the window in front. It is 0 when Unknown is
	// set.
	PID int
	// Unknown says why pairtime can't tell which window is in front, such
	// as a secure desktop; it is empty when PID is set.
	Unknown string
}

// OS is what the component needs from the operating system.
type OS struct {
	// Via names the focus source, for Status, such as
	// "SetWinEventHook(EVENT_SYSTEM_FOREGROUND)".
	Via string
	// Watch calls fn with each change, the first one for the window in
	// front as it starts, until the returned stop is called. fn returns
	// at once. Nil means this OS has no focus source yet.
	Watch func(fn func(Change)) (stop func(), err error)
	// Read reads the process table. It must be set when Watch is.
	Read func() ([]platform.Proc, error)
	// GOOS is the operating system whose names Read returns.
	GOOS string
}

// ThisOS returns the focus source of the OS the agent runs on.
func ThisOS() OS {
	return thisOS()
}

// Record is one os.focus event, before the agent gives it an envelope.
type Record struct {
	// At is when focus moved to the window, or, for a record that the
	// sessions under a window changing or a project context going live
	// wrote, when that was noticed. Its wall clock goes in the event's t,
	// and its monotonic reading can go in mono.
	At   time.Time
	Data *event.OSFocus
}

// Options configure a Watcher.
type Options struct {
	OS OS
	// Sessions returns the session key of each harness process the agent
	// knows, by process: the sessions the process scan found
	// (procs.Scanner.Sessions) and the processes that hooks came from. The
	// window in front hosts those whose parent chain reaches its process
	// (SM 3.2). A process that has exited should leave the map, as the
	// scan's do: Refresh skips the walk while the map is unchanged. Nil
	// means none are known, so hosted_sessions stays empty.
	Sessions func() map[platform.ID]string
	// ProjectOf returns the project key of a harness session, or "" when
	// it isn't known. Nil leaves focus_project empty.
	ProjectOf func(session string) string
	// AppSecret is K_app (AppSecret), which keys app_hash. Nil leaves
	// app_hash out: a plain hash would let anyone with the ledger test a
	// guess of the app.
	AppSecret []byte
	// Live reports whether a project context is live, after Presence has
	// heard of the change: records are written only then (D14). Nil means
	// always.
	Live func() bool
	// Presence gets the app class of each change, or event.AppNone when
	// the window in front is unknown, before the record is written. The
	// agent passes presence.Sampler.Focus.
	Presence func(event.AppClass)
	// Kick asks the process scan to run soon. The agent passes
	// procs.Scanner.Kick, and calls Refresh after every scan, so a session
	// the scan finds under the window in front gets into a new record.
	Kick func()
	// Record gets every record, in order, on the component's goroutine.
	// Nil drops them.
	Record func(Record)
	// Problem gets a description of what is wrong, for doctor and the
	// status endpoint, each time it changes; "" means nothing is.
	Problem func(msg string)
	// Now is the wall clock. Nil means time.Now.
	Now func() time.Time
	// Log gets the failures Problem reports. Nil discards them.
	Log *slog.Logger
}

// Status describes the component, for doctor and the status endpoint.
type Status struct {
	// Via names the focus source; it is empty when there is none.
	Via string
	// Unknown says why the window in front is unknown now, or why there
	// is no focus source; it is empty while a window is known.
	Unknown string
	// Class is the app class of the window in front, "" while it is
	// unknown.
	Class event.AppClass
	// Hosted is the number of harness sessions the window in front hosts.
	Hosted int
	// Changes counts the changes the OS reported, and Records the os.focus
	// records written.
	Changes, Records uint64
}

// Watcher is the focus component. Make one with New and call Start. Its
// methods are safe for concurrent use.
type Watcher struct {
	opts Options

	mu       sync.Mutex
	pending  *Change // the latest change the worker hasn't taken
	refresh  bool
	barriers []chan struct{} // closed once the worker has caught up; tests use them
	started  bool
	running  bool // the worker was started
	stopped  bool
	stopOS   func()
	status   Status
	problems map[string]string

	wake     chan struct{} // capacity 1: the worker has something to do
	quit     chan struct{}
	quitOnce sync.Once
	done     chan struct{} // closed when the worker has returned

	// Only the worker touches these.
	cur     *front // the window in front; nil while unknown
	written *event.OSFocus
}

// front is the window in front, as the last change or refresh found it.
type front struct {
	at   time.Time
	pid  int
	id   platform.ID
	data *event.OSFocus
	// sessions are the sessions data was worked out from.
	sessions map[platform.ID]string
	written  bool // a record of data was written
}

// New returns a watcher that does nothing until Start.
func New(opts Options) *Watcher {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Log == nil {
		opts.Log = slog.New(slog.DiscardHandler)
	}
	return &Watcher{
		opts:     opts,
		status:   Status{Via: opts.OS.Via},
		problems: make(map[string]string),
		wake:     make(chan struct{}, 1),
		quit:     make(chan struct{}),
		done:     make(chan struct{}),
	}
}

// Start starts the OS's focus source. A source that fails to start is
// reported through Options.Problem, and the component records nothing.
func (w *Watcher) Start() {
	w.mu.Lock()
	if w.started || w.stopped {
		w.mu.Unlock()
		return
	}
	w.started = true
	if w.opts.OS.Watch == nil || w.opts.OS.Read == nil {
		// TODO(D-focus-unknown): record that focus is unknown here once
		// os.focus can say so.
		w.status.Unknown = "pairtime has no focus source on this OS yet"
		w.mu.Unlock()
		return
	}
	w.running = true
	w.mu.Unlock()

	go w.run()
	// Not under w.mu: the source reports the window in front before it
	// returns, and the report takes w.mu.
	stop, err := w.opts.OS.Watch(w.deliver)
	w.mu.Lock()
	if err != nil {
		w.status.Unknown = "the focus source didn't start: " + err.Error()
		w.setProblem("source", fmt.Sprintf("focus changes go unrecorded, because %s didn't start: %v", w.opts.OS.Via, err))
		w.mu.Unlock()
		w.quitOnce.Do(func() { close(w.quit) })
		return
	}
	if w.stopped {
		// Stop ran meanwhile and couldn't stop the source.
		w.mu.Unlock()
		stop()
		return
	}
	w.stopOS = stop
	w.mu.Unlock()
}

// Stop stops the OS's focus source and the component. Nothing is recorded
// after it returns.
func (w *Watcher) Stop() {
	w.mu.Lock()
	running := w.running
	if w.stopped {
		w.mu.Unlock()
		if running {
			<-w.done
		}
		return
	}
	w.stopped = true
	stop := w.stopOS
	w.stopOS = nil
	w.mu.Unlock()
	if stop != nil {
		// Not under w.mu: the source may be delivering a change, which
		// takes it.
		stop()
	}
	if running {
		w.quitOnce.Do(func() { close(w.quit) })
		<-w.done
	}
}

// Refresh asks the component to walk the process table again for the
// window in front, and to write a new record if the sessions it hosts
// have changed, or if a project context has gone live since the window
// came to the front. The agent calls it after every process scan and when
// a hook names a new session. The scorer reads a record for the same
// window as no change of focus (pkg/score), so a new one costs nothing
// there.
func (w *Watcher) Refresh() {
	w.mu.Lock()
	if w.stopped || !w.running {
		w.mu.Unlock()
		return
	}
	w.refresh = true
	w.mu.Unlock()
	w.signal()
}

// Status returns the component's state.
func (w *Watcher) Status() Status {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.status
}

// deliver is the OS source's callback. It keeps only the latest change and
// wakes the worker, and never blocks.
func (w *Watcher) deliver(c Change) {
	w.mu.Lock()
	if w.stopped {
		w.mu.Unlock()
		return
	}
	w.pending = &c
	w.status.Changes++
	w.mu.Unlock()
	w.signal()
}

func (w *Watcher) signal() {
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

// run is the worker: it takes the latest change, or a refresh, and
// handles it, until Stop.
func (w *Watcher) run() {
	defer close(w.done)
	for {
		select {
		case <-w.wake:
		case <-w.quit:
			w.release()
			return
		}
		w.mu.Lock()
		c, refresh, barriers := w.pending, w.refresh, w.barriers
		w.pending, w.refresh, w.barriers = nil, false, nil
		w.mu.Unlock()
		switch {
		case c != nil:
			w.handle(*c)
		case refresh:
			w.reconsider()
		}
		for _, b := range barriers {
			close(b)
		}
	}
}

// release lets go of tests waiting for the worker.
func (w *Watcher) release() {
	w.mu.Lock()
	barriers := w.barriers
	w.barriers = nil
	w.mu.Unlock()
	for _, b := range barriers {
		close(b)
	}
}

// handle turns a change into the window in front and, in a project
// context, a record.
func (w *Watcher) handle(c Change) {
	if c.Unknown != "" {
		w.unknown(c.Unknown)
		return
	}
	t, err := w.table()
	if err != nil {
		w.unknown("couldn't read the process table: " + err.Error())
		return
	}
	win, ok := windowOf(t, c.PID)
	if !ok {
		w.unknown("the process of the window in front exited before pairtime read the process table")
		return
	}
	sessions := w.sessions()
	f := &front{at: c.At, pid: c.PID, id: win.proc.ID(), data: w.payload(t, win, sessions), sessions: sessions}
	w.cur = f
	w.known(f)
	if w.opts.Presence != nil {
		w.opts.Presence(f.data.AppClass)
	}
	if w.live() {
		w.write(f, f.at)
		if w.opts.Kick != nil {
			w.opts.Kick()
		}
	}
}

// reconsider walks the process table again for the window in front and
// writes a new record when what it hosts has changed, or when a context
// has gone live since the window came to the front. While the sessions
// the agent knows are the ones the record was worked out from, no session
// can have started or ended under the window, so it skips the walk: the
// agent refreshes after every process scan, and on an old laptop a walk a
// minute for nothing is waste.
func (w *Watcher) reconsider() {
	f := w.cur
	if f == nil || !w.live() {
		return
	}
	sessions := w.sessions()
	if f.written && maps.Equal(sessions, f.sessions) {
		return
	}
	t, err := w.table()
	if err != nil {
		return
	}
	win, ok := windowOf(t, f.pid)
	if !ok || win.proc.ID() != f.id {
		// The window's process is gone. Windows reports the window that
		// comes to the front next, and that change takes over.
		return
	}
	data := w.payload(t, win, sessions)
	f.sessions = sessions
	if f.written && samePayload(data, f.data) {
		return
	}
	f.data = data
	w.known(f)
	w.write(f, w.opts.Now())
}

// table reads the process table.
func (w *Watcher) table() (*platform.Table, error) {
	procs, err := w.opts.OS.Read()
	if err != nil {
		w.setProblemLocked("table", fmt.Sprintf("couldn't read the process table, so focus changes go unrecorded: %v", err))
		return nil, err
	}
	w.setProblemLocked("table", "")
	return platform.NewTable(w.opts.OS.GOOS, procs), nil
}

// sessions returns the sessions the agent knows, by process.
func (w *Watcher) sessions() map[platform.ID]string {
	if w.opts.Sessions == nil {
		return nil
	}
	return w.opts.Sessions()
}

// payload is the os.focus record of window win, which hosts those of
// sessions that run under it.
func (w *Watcher) payload(t *platform.Table, win window, sessions map[platform.ID]string) *event.OSFocus {
	d := &event.OSFocus{AppClass: win.class}
	if w.opts.AppSecret != nil && win.name != "" {
		d.AppHash = appHash(w.opts.AppSecret, t.OS(), win.name)
	}
	d.HostedSessions = validKeys(hostedSessions(t, win, sessions))
	if p := focusProject(win.class, d.HostedSessions, w.opts.ProjectOf); validKey(p) {
		d.FocusProject = p
	}
	return d
}

// write records f's payload, dated at, unless it is the record written
// last: a change between two windows of one app, or a refresh that found
// nothing new, adds nothing.
func (w *Watcher) write(f *front, at time.Time) {
	f.written = true
	if w.written != nil && samePayload(w.written, f.data) {
		return
	}
	w.written = f.data
	w.mu.Lock()
	w.status.Records++
	w.mu.Unlock()
	if w.opts.Record != nil {
		w.opts.Record(Record{At: at, Data: f.data})
	}
}

// unknown is a change to a window pairtime can't name. It records nothing
// and says why in Status (WP-OS2 decision 2).
//
// TODO(D-focus-unknown): write an os.focus record of the class that says
// focus is unknown once pkg/event, the schema and SM have it, so the slots
// are ambiguous instead of crediting the window that was in front before.
func (w *Watcher) unknown(reason string) {
	w.cur = nil
	w.mu.Lock()
	w.status.Unknown, w.status.Class, w.status.Hosted = reason, "", 0
	w.mu.Unlock()
	if w.opts.Presence != nil {
		w.opts.Presence(event.AppNone)
	}
}

func (w *Watcher) known(f *front) {
	w.mu.Lock()
	w.status.Unknown, w.status.Class, w.status.Hosted = "", f.data.AppClass, len(f.data.HostedSessions)
	w.mu.Unlock()
}

func (w *Watcher) live() bool {
	return w.opts.Live == nil || w.opts.Live()
}

// setProblemLocked records a problem from source, or clears it when msg
// is "", taking w.mu.
func (w *Watcher) setProblemLocked(source, msg string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.setProblem(source, msg)
}

// setProblem is setProblemLocked with w.mu held.
func (w *Watcher) setProblem(source, msg string) {
	if w.problems[source] == msg {
		return
	}
	if msg == "" {
		delete(w.problems, source)
	} else {
		w.problems[source] = msg
		w.opts.Log.Warn("focus: " + msg)
	}
	if w.opts.Problem != nil {
		var msgs []string
		for _, s := range []string{"source", "table"} {
			if m := w.problems[s]; m != "" {
				msgs = append(msgs, m)
			}
		}
		w.opts.Problem(strings.Join(msgs, "; "))
	}
}

// samePayload reports whether two records say the same thing.
func samePayload(a, b *event.OSFocus) bool {
	return a.AppClass == b.AppClass && a.AppHash == b.AppHash && a.FocusProject == b.FocusProject &&
		slices.Equal(a.HostedSessions, b.HostedSessions)
}

// validKeys drops what isn't an opaque key from keys, which come from
// elsewhere in the agent, so that one bad key can't make the record fail
// validation.
func validKeys(keys []string) []string {
	out := keys[:0]
	for _, k := range keys {
		if validKey(k) {
			out = append(out, k)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func validKey(k string) bool {
	return k != "" && (&event.OSFocus{AppClass: event.AppOther, FocusProject: k}).Validate() == nil
}

// sync waits until the worker has handled everything delivered before it
// was called. Tests use it; the agent never needs to.
func (w *Watcher) sync() {
	b := make(chan struct{})
	w.mu.Lock()
	if !w.running || w.stopped {
		w.mu.Unlock()
		return
	}
	w.barriers = append(w.barriers, b)
	w.mu.Unlock()
	w.signal()
	select {
	case <-b:
	case <-w.done:
	}
}
