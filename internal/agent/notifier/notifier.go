// SPDX-License-Identifier: AGPL-3.0-or-later

// Package notifier shows the coach's notification when a work session ends:
// one per work session, never a polling loop, with quiet hours (DESIGN 3.3).
// The coach (WP-CO1) decides when a session has ended, writes the sentence,
// such as "52 min on remind-bot. You fixed the timezone bug yourself. See
// recap." (DESIGN 1.2), and calls Notify. Every rule below is applied here,
// the same way on every OS. The OS-specific part is one Show call
// (Platform): a balloon from a transient tray icon on Windows, osascript on
// macOS, and org.freedesktop.Notifications.Notify over the session D-Bus on
// Linux (TE WP4.6).
//
// The rules, in the order Notify applies them:
//
//   - A note is about one work session, and each session gets one decision.
//     Asking again about a session does nothing.
//   - Nothing is shown while the teen has notifications off, or during their
//     quiet hours (config.toml [notifications]; DESIGN 3.9; TE 11.1 risk 2).
//   - Nothing is shown about a session that was found to be over more
//     than Fresh ago (Note.Ended). By then the recap is old news, and it
//     waits in the dashboard.
//   - Nothing is shown within Fresh of the last notification. A session
//     ends SESSION_GAP_S after its last credited minute, or sooner at a lock
//     or a suspend, and each project has its own sessions (SM 5.3). So
//     short sessions cut by locks, or sessions in two projects ending
//     together, can't become a run of notifications. pairtime never nags
//     (PA 4.4).
//   - A note that finds the screen locked waits instead of appearing on the
//     lock screen, where anyone near the computer could read it. It goes out
//     on UserPresent, which the coach calls on unlock and resume, if it is
//     still fresh then. A newer note takes its place and asks the OS
//     afresh, even one that came while the OS was being asked about the
//     older one, so one missed unlock can't hold back more than one note.
//     Only the Windows backend can tell a locked screen so far; on macOS and
//     Linux the desktop's own lock-screen settings decide what a locked
//     screen shows.
//
// The rules that can change while a note waits its turn (the settings, the
// clock and the gap since the last notification) are checked again right
// before the note goes to the OS.
//
// A note is shown at most once, across restarts too. With Options.Memory
// set, a note's decision is saved before the OS gets the note, and a note
// whose decision can't be saved isn't shown. A crash between that save and
// the OS taking the note loses the note, which waits in the dashboard like
// any note that wasn't shown.
//
// Notify never blocks. Show runs on a goroutine that exists only while a
// note is on its way to the screen, under a deadline, and an idle notifier
// has no goroutine, no timer and no connection, so it costs nothing between
// sessions (DESIGN 9.7). When the desktop can't show notifications at all,
// such as a Linux machine with no session bus or no notification server,
// the note is dropped without a retry, and doctor hears why through
// Options.Problem.
package notifier

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"
	"unicode"

	"github.com/some-du6e/pairtime/internal/agent/config"
	"github.com/some-du6e/pairtime/internal/platform/localtime"
	"github.com/some-du6e/pairtime/pkg/params"
)

// Fresh returns SESSION_GAP_S (SM 8.6), the gap between credited minutes
// that ends a work session, from the newest parameter table pairtime
// embeds: how long after its session ended a note may still be shown, and
// the least time between two notifications. SESSION_GAP_S is for display
// only, so reading it needs no scoring version in force, and a new table
// that changes it needs no change here.
func Fresh() time.Duration {
	return sessionGap()
}

var sessionGap = sync.OnceValue(func() time.Duration {
	tables := params.Tables()
	return time.Duration(tables[len(tables)-1].Int(params.SESSION_GAP_S)) * time.Second
})

// MaxText is the most characters of a note's Text a notification shows.
const MaxText = 200

// Title is the title of every notification. Lock screens and notification
// lists show titles, so it never names a project or quotes anything; the
// coach's text goes in the body. It says a session recap is ready, so
// Notify is for work sessions only. DESIGN 3.3's other trigger, the weekly
// story (M5), needs a title of its own, and spacing of its own from the
// session notes. The WP that adds it can give Note a kind whose zero value
// is the session recap, which changes no caller.
const Title = "Your session recap is ready"

// fallbackText is the body when the coach gives no text.
const fallbackText = "Open the pairtime dashboard to see it."

// showTimeout bounds one attempt at showing a note: saving its decision,
// then the Show call. The Windows balloon keeps its icon up while the
// balloon is on screen, which it ends sooner on its own.
const showTimeout = 45 * time.Second

// closeWait is how long Close waits, for a Show to end and for Memory to
// take what the notifier remembers.
const closeWait = 3 * time.Second

// memoryKey is the notifier's key in Memory.
const memoryKey = "notifier"

// sessionsKept is how many of the newest sessions the notifier remembers
// whatever their age. Older ones are remembered for as long as a note about
// them could still pass the freshness rule (prune).
const sessionsKept = 16

// sessionsMax is the most sessions the notifier remembers. Sessions end
// one after another, so far fewer than this can end within Fresh; the bound
// keeps a coach gone wrong from growing state.json without end.
const sessionsMax = 256

// Note asks for the notification about one work session.
type Note struct {
	// Session is the work session's opaque key (AGENTS.md). At most one
	// notification is shown for each key.
	Session string
	// Ended is the moment the session was found to be over: the time of
	// the lock or suspend that ended it, or, for a session that ended in a
	// gap, the end of its last credited minute plus SESSION_GAP_S, when
	// the gap was complete (DESIGN 3.8; WP-CO1). It isn't SM 5.3's
	// work-session end, the end of the last credited minute: that is
	// SESSION_GAP_S earlier, so a note given it would be stale by the time
	// anyone could know the session was over. A note goes stale Fresh after
	// Ended. The scorer steps every 60 s (DESIGN 3.7), so the coach can call
	// Notify about a minute after Ended, well inside that.
	Ended time.Time
	// Text is the body: a sentence or two from the coach, such as "52 min
	// on remind-bot. You fixed the timezone bug yourself. See recap."
	// (DESIGN 1.2). It may name the project and say what happened in plain
	// words, and nothing more: never prompt text, agent output, code, file
	// paths, command lines or window titles, which stay in pairtime's own
	// stores (AGENTS.md). The OS keeps its own copy of each notification in
	// its notification center or history, out of reach of pairtime's
	// retention rules and of "delete everything". The notifier puts Text on
	// one line of at most MaxText characters.
	Text string
}

// Message is what a Backend shows.
type Message struct {
	// Title is always Title.
	Title string
	// Body is the note's text, on one line.
	Body string
}

// Backend shows a message through the OS's own notification path. Show
// returns nil once the OS has the message, or an error that wraps ErrAway,
// ErrBusy or ErrUnavailable when it didn't take it, and gives up when ctx is
// done. Whatever Show puts on screen of its own, such as the Windows tray
// icon, is gone when it returns.
//
// A Show that goes on after the OS took the message, as the Windows
// balloon's does while the balloon stays up, calls shown at that moment,
// once. The gap to the next notification counts from there, and an error
// Show returns after calling shown is about its own cleanup: the message
// still counts as shown. A Show that returns as soon as the OS has the
// message needn't call it.
type Backend interface {
	Show(ctx context.Context, m Message, shown func()) error
}

var (
	// ErrAway means nobody can see the screen now: it is locked, a screen
	// saver runs, or another user's session is in front. The note waits for
	// UserPresent.
	ErrAway = errors.New("notifier: the screen is locked")
	// ErrBusy means the OS says this isn't a time for notifications: a
	// full-screen game or app, a presentation, or a do-not-disturb mode.
	// The note is dropped, as the OS would drop it.
	ErrBusy = errors.New("notifier: the OS isn't showing notifications now")
	// ErrUnavailable means this desktop has no way to show a notification:
	// no session bus or notification server on Linux, no notification area
	// on Windows, no osascript on macOS. The note is dropped, and doctor
	// hears why.
	ErrUnavailable = errors.New("notifier: this desktop can't show notifications")
)

// Outcome says what became of a note.
type Outcome int

const (
	// Queued: the note is on its way to the screen. Not final.
	Queued Outcome = iota
	// Waiting: the note waits, for the screen to unlock or for the note
	// before it to leave the screen. Not final.
	Waiting
	// Shown: the OS has the notification.
	Shown
	// TurnedOff: the teen has notifications off.
	TurnedOff
	// InQuietHours: it is the teen's quiet hours.
	InQuietHours
	// Repeated: this session already had its decision, or the note has no
	// session.
	Repeated
	// Stale: the session was found to be over more than Fresh ago.
	Stale
	// TooSoon: the last notification was less than Fresh ago.
	TooSoon
	// Replaced: a newer note took this waiting note's place.
	Replaced
	// Busy: the OS isn't showing notifications now (ErrBusy).
	Busy
	// Unavailable: this desktop can't show notifications (ErrUnavailable).
	Unavailable
	// Failed: Show failed for another reason or timed out, or the note's
	// decision couldn't be saved before it went to the OS.
	Failed
	// Stopped: the notifier was closed first.
	Stopped
)

var outcomeNames = [...]string{
	Queued:       "queued",
	Waiting:      "waiting",
	Shown:        "shown",
	TurnedOff:    "notifications are off",
	InQuietHours: "quiet hours",
	Repeated:     "already decided for this session",
	Stale:        "the session ended too long ago",
	TooSoon:      "too soon after the last notification",
	Replaced:     "replaced by a newer note",
	Busy:         "the OS isn't showing notifications now",
	Unavailable:  "this desktop can't show notifications",
	Failed:       "failed",
	Stopped:      "the agent stopped",
}

func (o Outcome) String() string {
	if o < 0 || int(o) >= len(outcomeNames) {
		return fmt.Sprintf("Outcome(%d)", int(o))
	}
	return outcomeNames[o]
}

// Memory keeps what the notifier must remember across restarts. The
// agent's *state.Store is one.
type Memory interface {
	Get(key string, v any) (bool, error)
	Put(key string, v any) error
	Save() error
}

// remembered is what the notifier keeps in Memory.
type remembered struct {
	LastShown time.Time `json:"last_shown,omitzero"`
	// Sessions are the sessions a note came for, oldest first.
	Sessions []decided `json:"sessions,omitempty"`
}

// decided is a session that had its decision.
type decided struct {
	Session string `json:"session"`
	// At is the later of the note's Ended and the moment it was decided. A
	// note about the session with an Ended no later than At is Stale once
	// Fresh has passed since At, so the session is kept until then.
	At time.Time `json:"at"`
}

// Options configure New.
type Options struct {
	// Backend shows messages. Nil means Platform().
	Backend Backend
	// Settings returns the teen's settings in force. It is read for every
	// note, and again right before the note goes to the OS, so an edit to
	// config.toml applies to the next one, and to a note already on its
	// way. Nil means the defaults: on, with no quiet hours.
	Settings func() config.Notifications
	// Memory keeps the last notification time and recent sessions across
	// restarts. A note's decision is saved there before the OS gets the
	// note, on the notifier's own goroutine, and every other decision and
	// each notification shown a moment after it happens, also off the
	// caller's path. Nil keeps them for this process only.
	Memory Memory
	// Now is the clock. Quiet hours read the clock time in the location of
	// the times it returns. Nil means localtime.Now, which reads the
	// computer's time zone afresh each time, so quiet hours follow a laptop
	// that changed time zones while the agent ran. time.Now wouldn't: Go
	// reads time.Local once per process.
	Now func() time.Time
	// Log gets one line per note, without its text. Nil discards it.
	Log *slog.Logger
	// Problem, when set, hears why the last notification couldn't be shown,
	// or "" once one was. The agent passes it on to doctor.
	Problem func(msg string)
	// Result, when set, is called once for every note with its final
	// outcome, from Notify itself or from the notifier's own goroutine. It
	// must return quickly.
	Result func(Note, Outcome)
}

// Notifier applies the rules and hands notes to a Backend. Its methods are
// safe to call from any goroutine.
type Notifier struct {
	backend  Backend
	settings func() config.Notifications
	memory   Memory
	now      func() time.Time
	log      *slog.Logger
	problem  func(string)
	result   func(Note, Outcome)

	// wg counts the goroutines that show notes and save Memory, and the
	// one that hands Memory the last snapshot after Close, while there are
	// any.
	wg sync.WaitGroup
	// putMu is held from each snapshot of what Memory keeps to its Put, so
	// the Puts land in the order of their snapshots and Memory never goes
	// back to an older one. It is taken before mu, never while mu is held.
	putMu sync.Mutex

	mu        sync.Mutex
	closed    bool
	lastShown time.Time
	sessions  []decided
	showing   bool               // the worker is taking notes to the OS
	cancel    context.CancelFunc // ends the worker's attempt early
	// waiting is the newest note not yet shown. While showing, it came
	// during the worker's current attempt and hasn't been to the OS yet;
	// otherwise the OS found the screen locked for it.
	waiting *Note
	// present counts the UserPresent calls, so that an attempt can tell the
	// user came back while it was out.
	present uint64
	saving  bool // a goroutine is saving Memory
	dirty   bool // what Memory keeps changed since that goroutine last looked
	// outbox is what Log, Problem and Result have still to hear, in the
	// order it happened. One goroutine at a time hands it over, outside
	// mu (flush).
	outbox   []func()
	flushing bool
}

// New returns a notifier. It reads Memory once, here.
func New(opts Options) *Notifier {
	n := &Notifier{
		backend:  opts.Backend,
		settings: opts.Settings,
		memory:   opts.Memory,
		now:      opts.Now,
		log:      opts.Log,
		problem:  opts.Problem,
		result:   opts.Result,
	}
	if n.backend == nil {
		n.backend = Platform()
	}
	if n.settings == nil {
		n.settings = func() config.Notifications { return config.Notifications{} }
	}
	if n.now == nil {
		n.now = localtime.Now
	}
	if n.log == nil {
		n.log = slog.New(slog.DiscardHandler)
	}
	if n.memory != nil {
		var r remembered
		if ok, err := n.memory.Get(memoryKey, &r); ok && err == nil {
			n.lastShown = r.LastShown.Round(0)
			for _, s := range r.Sessions {
				n.sessions = append(n.sessions, decided{Session: s.Session, At: s.At.Round(0)})
			}
			n.prune(n.clock())
		}
	}
	return n
}

// clock returns the time now without a monotonic reading, so every gap the
// notifier measures is a wall-clock gap, which counts the time a laptop
// spent asleep on every OS (DESIGN 3.3; CP 2.2).
func (n *Notifier) clock() time.Time {
	return n.now().Round(0)
}

// Notify asks for note to be shown and returns at once, without waiting
// for the OS. Its result is Queued or Waiting when the note goes on, or the
// final outcome when it was dropped; Options.Result hears the final outcome
// either way.
func (n *Notifier) Notify(note Note) Outcome {
	set := n.settings()
	now := n.clock()
	note.Ended = note.Ended.Round(0)
	n.mu.Lock()
	o, launch := n.decide(note, set, now)
	if o != Queued && o != Waiting {
		n.tell(note, o)
	}
	n.mu.Unlock()
	n.flush()
	if launch != nil {
		launch()
	}
	return o
}

// decide applies the rules to a new note, and parks it or reserves the
// worker for it, which launch starts. n.mu is held.
func (n *Notifier) decide(note Note, set config.Notifications, now time.Time) (o Outcome, launch func()) {
	if n.closed {
		return Stopped, nil
	}
	if note.Session == "" {
		return Repeated, nil
	}
	for _, s := range n.sessions {
		if s.Session == note.Session {
			return Repeated, nil
		}
	}
	at := now
	if note.Ended.After(at) {
		at = note.Ended
	}
	n.sessions = append(n.sessions, decided{Session: note.Session, At: at})
	n.prune(now)
	if g := n.gate(note, set, now); g != Queued {
		// The decision is made, so a crash before the next clean stop
		// mustn't forget it.
		n.persist()
		return g, nil
	}
	if n.showing {
		n.park(note)
		n.persist()
		return Waiting, nil
	}
	// A note waiting for the screen gives way to this newer one, which
	// asks the OS afresh. So a lock that nobody reported the end of holds
	// back no more than one note.
	if n.waiting != nil {
		n.tell(*n.waiting, Replaced)
		n.waiting = nil
	}
	// The worker saves the decision before the OS gets the note.
	return Queued, n.start(note)
}

// prune forgets the sessions that no note can be shown about now: those
// whose At is more than Fresh ago, since a note about one of them would be
// Stale. The newest sessionsKept stay whatever their age, which covers a
// note with no Ended, as it never goes stale, and at most sessionsMax stay.
// n.mu is held.
func (n *Notifier) prune(now time.Time) {
	old := len(n.sessions) - sessionsKept
	kept := n.sessions[:0]
	for i, s := range n.sessions {
		if i >= old || now.Sub(s.At) <= Fresh() {
			kept = append(kept, s)
		}
	}
	if len(kept) > sessionsMax {
		kept = kept[len(kept)-sessionsMax:]
	}
	n.sessions = kept
}

// gate applies the rules that don't depend on the OS, and returns Queued
// when the note passes them. n.mu is held.
func (n *Notifier) gate(note Note, set config.Notifications, now time.Time) Outcome {
	switch {
	case set.Off:
		return TurnedOff
	case set.Quiet.Contains(now):
		return InQuietHours
	case !note.Ended.IsZero() && now.Sub(note.Ended) > Fresh():
		return Stale
	}
	// A last notification in the future means the clock was set back; it
	// stops nothing.
	if since := now.Sub(n.lastShown); !n.lastShown.IsZero() && since >= 0 && since < Fresh() {
		return TooSoon
	}
	return Queued
}

// park makes note the waiting note, replacing an older one. n.mu is held.
func (n *Notifier) park(note Note) {
	if n.waiting != nil {
		n.tell(*n.waiting, Replaced)
	}
	n.waiting = &note
}

// start reserves the worker for note and returns the step that starts it.
// The caller runs that step after it has let go of n.mu and flushed what
// it told, so the attempt applies the rules after the callbacks that ran
// first, which may have changed the settings. n.mu is held.
func (n *Notifier) start(note Note) (launch func()) {
	ctx, cancel := context.WithTimeout(context.Background(), showTimeout)
	n.showing = true
	n.cancel = cancel
	seen := n.present
	n.wg.Add(1)
	return func() { go n.run(ctx, cancel, note, seen) }
}

// run is the worker. It takes note to the OS, then each note that came
// while it did, one at a time, and returns when none is left. seen is
// n.present as note's attempt began.
func (n *Notifier) run(ctx context.Context, cancel context.CancelFunc, note Note, seen uint64) {
	defer n.wg.Done()
	for {
		t := n.attempt(ctx, note)
		cancel()

		n.mu.Lock()
		o := t.o
		if o == Shown {
			n.lastShown = t.at
			n.persist()
		}
		// After Close, a note that didn't reach the screen is Stopped.
		// Parking it would lose it: Close has already let go of the
		// waiting note, and nothing will look again.
		if n.closed && (o == Failed || o == Waiting) {
			o = Stopped
		}
		if t.asked {
			n.diagnose(o, t.err)
		}
		next, more := Note{}, false
		switch {
		case o == Waiting && n.waiting != nil:
			// A newer note came while this one was out. It takes this
			// one's place and asks the OS for itself: the screen may
			// have been unlocked meanwhile, and the unlock missed.
			n.tell(note, Replaced)
			next, more = *n.waiting, true
			n.waiting = nil
		case o == Waiting && n.present != seen:
			// The user came back while this note was out, so the OS's
			// answer may be older than the unlock: ask once more.
			next, more = note, true
		case o == Waiting:
			n.waiting = &note
		default:
			n.tell(note, o)
			if n.waiting != nil && !n.closed {
				next, more = *n.waiting, true
				n.waiting = nil
			}
		}
		if more {
			ctx, cancel = context.WithTimeout(context.Background(), showTimeout)
			n.cancel = cancel
			seen = n.present
		} else {
			n.showing = false
			n.cancel = nil
		}
		n.mu.Unlock()
		n.flush()
		if !more {
			return
		}
		note = next
	}
}

// tried is what one attempt came to.
type tried struct {
	o     Outcome
	at    time.Time // for Shown, when the OS took the note
	err   error     // for the log and doctor
	asked bool      // the OS was asked, or the save before it failed
}

// attempt takes note to the OS: it applies the rules with the settings
// and the time now, saves the note's decision, applies the rules once
// more, since the save takes a moment, and calls Show.
func (n *Notifier) attempt(ctx context.Context, note Note) tried {
	if o := n.recheck(note); o != Queued {
		return tried{o: o}
	}
	if n.memory != nil {
		if err := n.saveNow(); err != nil {
			return tried{o: Failed, err: fmt.Errorf("couldn't save the note's decision before showing it: %w", err), asked: true}
		}
		if o := n.recheck(note); o != Queued {
			return tried{o: o}
		}
	}
	acc := acceptance{clock: n.clock}
	err := n.backend.Show(ctx, Compose(note), acc.shown)
	switch at, ok := acc.end(); {
	case ok:
		return tried{o: Shown, at: at, err: err, asked: true}
	case err == nil:
		return tried{o: Shown, at: n.clock(), asked: true}
	}
	return tried{o: classify(err), err: err, asked: true}
}

// recheck applies the rules to note again, with the settings in force and
// the time now. n.mu isn't held.
func (n *Notifier) recheck(note Note) Outcome {
	set := n.settings()
	now := n.clock()
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.closed {
		return Stopped
	}
	return n.gate(note, set, now)
}

// acceptance is when the OS took a message, as Backend.Show's shown says.
// A call after Show has returned, from a balloon thread that gave up late,
// changes nothing.
type acceptance struct {
	clock func() time.Time

	mu     sync.Mutex
	at     time.Time
	marked bool
	over   bool
}

func (a *acceptance) shown() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.over && !a.marked {
		a.at, a.marked = a.clock(), true
	}
}

// end reports when shown was called, if it was, and stops later calls.
func (a *acceptance) end() (time.Time, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.over = true
	return a.at, a.marked
}

// UserPresent tells the notifier that someone is at the screen again: the
// screen was unlocked, or the computer woke. A note that waited for the
// screen goes out now, unless it went stale or quiet hours began meanwhile.
// One that is with the OS as the user comes back asks the OS once more if
// the OS found the screen locked.
//
// The coach calls it, on every unlock and resume (WP-CO1 in docs/PLAN.md).
// It hears them from internal/agent/presence, whose Sampler.Power gets
// every lock, unlock, suspend and resume from the OS's watchers, in or out
// of a project context: WP-CO1 adds a hook there beside Options.Seal, which
// hears suspends and locks the same way, and reads the same events to end
// work sessions. Until then nothing calls UserPresent, and a note that
// found the screen locked waits until a newer note takes its place. A
// screen saver that doesn't lock the session sends no unlock, so a note
// that met one waits for the next unlock or resume, or for a newer note.
func (n *Notifier) UserPresent() {
	var launch func()
	n.mu.Lock()
	n.present++
	if !n.closed && !n.showing && n.waiting != nil {
		note := *n.waiting
		n.waiting = nil
		// The worker applies the rules before the note goes out.
		launch = n.start(note)
	}
	n.mu.Unlock()
	if launch != nil {
		launch()
	}
}

// Close stops a Show in progress, which takes down whatever it put on
// screen, such as the Windows tray icon, and waits a moment for it and for
// a save of Memory to end. Notes after Close are Stopped. Close then puts
// what the notifier remembers in Memory without saving it; the agent saves
// state.json as it stops. Close returns within closeWait even when a Save
// is stuck on a slow disk and holds Memory's lock: the last Put then lands
// once that Save ends, still after every older Put.
func (n *Notifier) Close() {
	n.mu.Lock()
	if n.closed {
		n.mu.Unlock()
		return
	}
	n.closed = true
	if n.cancel != nil {
		n.cancel()
	}
	if n.waiting != nil {
		n.tell(*n.waiting, Stopped)
		n.waiting = nil
	}
	n.mu.Unlock()
	n.flush()

	ctx, cancel := context.WithTimeout(context.Background(), closeWait)
	defer cancel()
	if !within(ctx, n.wg.Wait) {
		n.log.Warn("a notification, or a save of what the notifier remembers, was still running when the agent stopped")
	}
	if n.memory == nil {
		return
	}
	// After the wait, so a Show that ended meanwhile is counted.
	n.wg.Add(1)
	put := func() {
		defer n.wg.Done()
		if err := n.putMemory(); err != nil {
			n.log.Warn("couldn't keep the notifier's state", "err", err)
		}
	}
	if !within(ctx, put) {
		n.log.Warn("state.json was still busy when the agent stopped, so the notifier's state goes in once it's free")
	}
}

// within runs f on a goroutine of its own and reports whether it returned
// before ctx was done.
func within(ctx context.Context, f func()) bool {
	done := make(chan struct{})
	go func() {
		f()
		close(done)
	}()
	select {
	case <-done:
		return true
	case <-ctx.Done():
		return false
	}
}

// snapshot copies what Memory keeps. n.mu is held.
func (n *Notifier) snapshot() remembered {
	return remembered{LastShown: n.lastShown, Sessions: append([]decided(nil), n.sessions...)}
}

// persist has what the notifier remembers saved to Memory soon after it
// changes, a decision or a notification shown, so a crash, a kill or a
// power cut after the save forgets none of it. The save runs a moment
// later on a goroutine, off the caller's path, since Memory's Save writes a
// file and Put may wait for another component's Save; a crash inside that
// moment can still forget the change. A note on its way to the OS doesn't
// rely on this: its attempt saves first (saveNow). Changes that come while
// the goroutine works go in its next round, so there is one such goroutine
// at most, and none once Memory has the latest. A failed Save is logged,
// and the next one, whoever makes it, writes every key. n.mu is held.
func (n *Notifier) persist() {
	if n.memory == nil {
		return
	}
	n.dirty = true
	if n.saving {
		return
	}
	n.saving = true
	n.wg.Add(1)
	go n.saveLoop()
}

// saveLoop puts what the notifier remembers in Memory and saves it, until
// nothing has changed since its last round.
func (n *Notifier) saveLoop() {
	defer n.wg.Done()
	for {
		n.mu.Lock()
		if !n.dirty {
			n.saving = false
			n.mu.Unlock()
			return
		}
		n.dirty = false
		n.mu.Unlock()
		if err := n.saveNow(); err != nil {
			n.log.Warn("couldn't save the notifier's state", "err", err)
		}
	}
}

// saveNow puts what the notifier remembers now in Memory and saves it.
func (n *Notifier) saveNow() error {
	if err := n.putMemory(); err != nil {
		return err
	}
	return n.memory.Save()
}

// putMemory puts what the notifier remembers now into Memory. The snapshot
// and the Put happen under putMu, so a Put never lands after a newer one.
// n.mu isn't held: Put may wait for a Save that holds Memory's lock, and
// Notify must not wait with it.
func (n *Notifier) putMemory() error {
	n.putMu.Lock()
	defer n.putMu.Unlock()
	n.mu.Lock()
	mem := n.snapshot()
	n.mu.Unlock()
	return n.memory.Put(memoryKey, mem)
}

// tell has Options.Result hear note's final outcome, after everything
// told before it. n.mu is held.
func (n *Notifier) tell(note Note, o Outcome) {
	n.outbox = append(n.outbox, func() {
		if o != Shown {
			n.log.Info("no notification for a work session", "why", o.String())
		}
		if n.result != nil {
			n.result(note, o)
		}
	})
}

// diagnose has the log and doctor hear what an attempt came to, after
// everything told before it. n.mu is held.
func (n *Notifier) diagnose(o Outcome, err error) {
	n.outbox = append(n.outbox, func() {
		n.logOutcome(o, err)
		n.tellProblem(o, err)
	})
}

// flush hands what the outbox holds to the callbacks, in order, outside
// n.mu. When another goroutine is already at it, that one hands this
// goroutine's part over too, so two goroutines finishing at once can't
// swap what doctor or Options.Result hear, and nobody waits for anybody's
// callbacks. n.mu isn't held.
func (n *Notifier) flush() {
	n.mu.Lock()
	if n.flushing {
		n.mu.Unlock()
		return
	}
	n.flushing = true
	for len(n.outbox) > 0 {
		batch := n.outbox
		n.outbox = nil
		n.mu.Unlock()
		for _, f := range batch {
			f()
		}
		n.mu.Lock()
	}
	n.flushing = false
	n.mu.Unlock()
}

// classify turns Show's error into an outcome.
func classify(err error) Outcome {
	switch {
	case err == nil:
		return Shown
	case errors.Is(err, ErrAway):
		return Waiting
	case errors.Is(err, ErrBusy):
		return Busy
	case errors.Is(err, ErrUnavailable):
		return Unavailable
	}
	return Failed
}

func (n *Notifier) logOutcome(o Outcome, err error) {
	switch {
	case o == Shown && err != nil:
		n.log.Warn("notification shown, but taking down what showed it failed", "err", err)
	case o == Shown:
		n.log.Info("notification shown")
	case o == Waiting:
		n.log.Info("the screen is locked, so the notification waits for the user")
	case o == Failed:
		n.log.Warn("notification failed", "err", err)
	}
}

// tellProblem passes on why notifications can't be shown, or that they
// can again. Busy and a locked screen aren't problems.
func (n *Notifier) tellProblem(o Outcome, err error) {
	if n.problem == nil {
		return
	}
	switch o {
	case Shown:
		n.problem("")
	case Unavailable:
		n.problem(fmt.Sprintf("notifications can't be shown on this desktop: %v", err))
	case Failed:
		n.problem(fmt.Sprintf("the last notification failed: %v", err))
	}
}

// Compose returns the message for note: Title, and the note's text on one
// line of at most MaxText characters, without control characters or the
// characters that change text direction.
func Compose(note Note) Message {
	body := oneLine(note.Text)
	if body == "" {
		body = fallbackText
	}
	return Message{Title: Title, Body: body}
}

// oneLine collapses runs of spaces and line breaks in s to one space, drops
// control and bidirectional formatting characters, and cuts the result to
// MaxText characters, ending a cut one with an ellipsis.
func oneLine(s string) string {
	var out []rune
	space := false
	for _, r := range s {
		switch {
		case unicode.IsSpace(r):
			space = true
			continue
		case unicode.IsControl(r), unicode.Is(unicode.Bidi_Control, r):
			continue
		}
		if space && len(out) > 0 {
			out = append(out, ' ')
		}
		space = false
		out = append(out, r)
	}
	if len(out) > MaxText {
		out = out[:MaxText-1]
		for len(out) > 0 && out[len(out)-1] == ' ' {
			out = out[:len(out)-1]
		}
		out = append(out, '…')
	}
	return string(out)
}
