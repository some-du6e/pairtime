// SPDX-License-Identifier: AGPL-3.0-or-later

// Package procs is the agent's harness process scan (DESIGN 3.3). Every
// PROC_SCAN_S, and soon after a focus change, it lists the processes,
// finds harness processes of this user that no hook has spoken for, and
// reports each one as a session.start with its parent_class, entrypoint
// and has_tty (SM 1.2, 2.2 rule 4). That is how `claude --bare`, `claude
// -p` and `codex exec` runs that fire no hooks (HS 3.1, 3.5) still get
// classified. Each scan also counts this user's harness processes, hooked
// or not, for presence, which keeps a project context live while one runs
// (DESIGN 3.3).
//
// The scan reads only what internal/platform's readers read: process ids,
// parent ids, executable names, start times, owners, process groups and
// whether a terminal is attached. It never reads a command line or an
// environment, so it can't see -p or --bare. No hook payload says them
// either (HS 3.1); for Claude Code only OTel's app.entrypoint does, an
// attribute sent only when OTEL_METRICS_INCLUDE_ENTRYPOINT is set (HS 3.3).
package procs

import (
	"cmp"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"runtime"
	"slices"
	"sync"
	"time"

	"github.com/some-du6e/pairtime/internal/agent/sched"
	"github.com/some-du6e/pairtime/internal/platform"
	"github.com/some-du6e/pairtime/pkg/event"
)

// Interval is PROC_SCAN_S, SM 8.4's interval of the harness process scan.
// A test checks it against the newest table in pkg/params.
const Interval = 60 * time.Second

// Grace is how long a harness process runs before a scan may report it.
// A session that fires hooks claims its process with its first hook that
// reaches the agent over the socket or the pipe, normally Claude Code's
// SessionStart as it starts; Grace leaves that hook time to arrive. A hook
// that only the spool delivered claims nothing (DESIGN 5.2). A first such
// hook that comes later, as when the agent was restarting or Claude Code's
// folder-trust dialog stays open (DESIGN 5.1), still claims the process,
// and the session the scan reported goes to Options.Withdraw.
const Grace = 10 * time.Second

// KickDelay is how long after a focus change the scan runs, so a burst of
// changes (alt-tab through five windows) costs one scan.
const KickDelay = 2 * time.Second

// KickGap is the least time between two scans that focus changes caused.
// Someone switching windows every few seconds still gets at most one scan
// per KickGap, which keeps the scan's cost bounded on an old laptop.
const KickGap = 10 * time.Second

// stateKey is the scanner's key in state.json.
const stateKey = "procs"

// Session is a harness process that no hook claimed.
type Session struct {
	// Key is a random opaque key for the process (AGENTS.md). The scan
	// can't know the harness's own session id; whoever joins this to the
	// session a log reader finds maps the key (Tie), and Tied says one
	// did: Key is then that session's.
	Key     string        `json:"key"`
	Tied    bool          `json:"tied,omitempty"`
	Harness event.Harness `json:"harness"`
	// PID and Started name the process on this device. They are local and
	// never leave it.
	PID     int       `json:"pid"`
	Started time.Time `json:"started,omitzero"`
	// Found is when the scan that reported it ran.
	Found time.Time          `json:"found"`
	Event event.SessionStart `json:"event"`
	// ReportKey is the key Report gave the session, which a tie keeps, so
	// Withdraw names the session Report had. It is empty for a tie of a
	// process no scan reported, such as one younger than Grace.
	ReportKey string `json:"report_key,omitempty"`
	// tiedRead is the number of table reads begun when Tie made the tie,
	// which a scan whose read began before it leaves alone (Scan).
	tiedRead uint64
}

// reportKey is the key Report gave s, or "" when Report never had it. A
// session saved by a build that kept no ReportKey is the scan's own when
// untied.
func (s Session) reportKey() string {
	if s.ReportKey == "" && !s.Tied {
		return s.Key
	}
	return s.ReportKey
}

// Store keeps what a Scanner remembers across agent restarts. The agent's
// is state.json (internal/agent/state).
type Store interface {
	Get(key string, v any) (bool, error)
	Put(key string, v any) error
	Save() error
}

// Options configure a Scanner.
type Options struct {
	// Report gets each session, once, on the goroutine that ran the scan.
	// It runs with the Scanner locked, so that a Claim of the same process
	// can't withdraw the session before Report has it, and so it must
	// return quickly and must not call the Scanner.
	Report func(Session)
	// Withdraw gets each reported session whose process a hook claimed
	// afterwards, on the goroutine that called Claim, after Report had it.
	// The session fires hooks after all, so the hookless session.start
	// Report gave for it should be withdrawn, or merged into the session
	// its hooks name. It comes under the key Report gave it, even once Tie
	// tied the process to a log reader's session, and a tie of a process
	// Report never had comes to Withdraw not at all.
	Withdraw func(Session)
	// Running, when set, gets the number of this user's harness session
	// processes after every scan, on the goroutine that ran it: hooked or
	// not, younger than Grace or not, with launchers left out. The agent
	// hands it to presence, where a running one keeps a project context
	// live (DESIGN 3.3). A scan that fails gives 0, so a reader that stops
	// working can't hold a context live, and the sampling with it, for good.
	Running func(n int)
	// Read reads the process table. Nil means this OS's reader.
	Read func() ([]platform.Proc, error)
	// OS is the GOOS whose names Read returns. Empty means runtime.GOOS.
	OS string
	// Self is the Proc.Owner whose harness processes the scan reports:
	// this user on Linux and macOS, this user's session on Windows. Empty
	// means this process's own, as this OS's reader names it. Processes
	// of other owners stay in the table, where parent chains pass through
	// them, but are never reported: on a family PC they are someone else's
	// sessions, whose hooks go to that person's agent (DESIGN 3.2, 3.5).
	Self string
	// Boot names the boot the scanner runs on: on Linux the kernel's boot
	// id, since process IDs there count from boot, and empty elsewhere.
	// Empty means this OS's own. What Store holds from another boot is
	// dropped, and so is a Claim from another boot.
	Boot string
	// Store, when set, keeps the claimed processes and the reported
	// sessions under the key "procs", so that after an agent restart the
	// scan neither reports a hooked session as hookless nor reports a
	// session again under a new key. Nil keeps them in memory only.
	Store Store
	// Now is the wall clock. Nil means time.Now.
	Now func() time.Time
	// Log gets scan failures. Nil discards them.
	Log *slog.Logger
	// Scanned, when set, gets each scheduled or kicked scan's error, nil
	// after a scan that worked, so the agent can tell doctor.
	Scanned func(error)
}

// Scanner finds hookless harness sessions.
type Scanner struct {
	opts Options
	self string
	boot string

	mu sync.Mutex
	// claimed holds the processes hooks claimed, each with the number of
	// table reads that had begun when its claim came. A scan forgets the
	// claim of a process its table lacks only if the claim came before
	// its own read began: a process that starts while the table is read
	// can be claimed by its first hook before the scan sees the table.
	claimed  map[platform.ID]uint64
	reported map[platform.ID]Session
	// touched is when each session no hook reports was read live (Touch),
	// seen the harness processes the table reads found and when last, and
	// ended the ties whose process a scan saw gone, by session key, with
	// when: all for Tie and TieOf, and none saved.
	touched  map[string]touch
	seen     map[platform.ID]seenProc
	ended    map[string]endedTie
	reads    uint64 // table reads begun
	lastKick time.Time
	kick     *sched.Job
}

// saved is what Store keeps under stateKey.
type saved struct {
	Boot     string        `json:"boot"`
	Claimed  []platform.ID `json:"claimed"`
	Reported []savedReport `json:"reported"`
}

type savedReport struct {
	Proc    platform.ID `json:"proc"`
	Session Session     `json:"session"`
}

// errNoSelf means the scan can't tell this user's processes from others'.
var errNoSelf = errors.New("procs: this OS won't say whose processes are this user's, so the scan reports none")

// New makes a Scanner, with what Store kept from this boot. It doesn't
// scan until Scan or Start is called.
func New(opts Options) *Scanner {
	if opts.Read == nil {
		opts.Read = readOS
	}
	if opts.OS == "" {
		opts.OS = runtime.GOOS
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Log == nil {
		opts.Log = slog.New(slog.DiscardHandler)
	}
	if opts.Report == nil {
		opts.Report = func(Session) {}
	}
	s := &Scanner{
		opts:     opts,
		self:     cmp.Or(opts.Self, selfOS()),
		boot:     cmp.Or(opts.Boot, bootOS()),
		claimed:  make(map[platform.ID]uint64),
		reported: make(map[platform.ID]Session),
		touched:  make(map[string]touch),
		seen:     make(map[platform.ID]seenProc),
		ended:    make(map[string]endedTie),
	}
	s.load()
	return s
}

// Claim records that a hook arrived from inside the harness process host,
// read on the boot boot (in Options.Boot's terms), so no scan reports the
// process. The hook path claims the process that platform.HookHost finds
// in the parent chain the agent walked from a socket or pipe connection,
// never in a chain a spooled message recorded (DESIGN 5.2). A claim from
// another boot is dropped. When a scan reported the process already, Claim
// hands that session to Options.Withdraw.
func (s *Scanner) Claim(boot string, host platform.Proc) {
	if boot != s.boot {
		return
	}
	id := host.ID()
	s.mu.Lock()
	if _, ok := s.claimed[id]; ok {
		s.mu.Unlock()
		return
	}
	s.claimed[id] = s.reads
	sess, wasReported := s.reported[id]
	delete(s.reported, id)
	delete(s.seen, id)
	s.putLocked()
	s.mu.Unlock()
	s.save()
	if key := sess.reportKey(); wasReported && key != "" && s.opts.Withdraw != nil {
		w := sess
		w.Key, w.Tied = key, false
		s.opts.Withdraw(w)
	}
}

// ClaimLive is Claim for a process read on the boot the scanner runs on, as
// the agent's own walk of a hook's connection reads it (DESIGN 3.4;
// internal/agent/hookpath).
func (s *Scanner) ClaimLive(host platform.Proc) { s.Claim(s.boot, host) }

// tableView is one read of the process table, as Scan and Tie read it.
type tableView struct {
	t   *platform.Table
	all []platform.Proc
	// harness is each harness process's harness, by pid, and launcher the
	// pids of the launchers harnesses run under (platform.LauncherOf).
	harness  map[int]event.Harness
	launcher map[int]bool
	// live holds every process in the table, harness or not: one that a
	// read can't name, as when its exe link can't be read, keeps its claim
	// and its report.
	live map[platform.ID]bool
}

// readTable reads the process table once.
func (s *Scanner) readTable() (tableView, error) {
	procs, err := s.opts.Read()
	if err != nil {
		return tableView{}, fmt.Errorf("reading the process table: %w", err)
	}
	if s.self == "" {
		return tableView{}, errNoSelf
	}
	goos := s.opts.OS
	t := platform.NewTable(goos, procs)
	v := tableView{t: t, all: t.Procs(), harness: make(map[int]event.Harness), launcher: make(map[int]bool)}
	for _, p := range v.all {
		if h, ok := platform.HarnessOf(goos, p); ok {
			v.harness[p.PID] = h
		}
	}
	for _, p := range v.all {
		if _, ok := v.harness[p.PID]; !ok {
			continue
		}
		if parent, ok := t.Parent(p); ok && platform.LauncherOf(goos, parent, p) {
			v.launcher[parent.PID] = true
		}
	}
	v.live = make(map[platform.ID]bool, len(v.all))
	for _, p := range v.all {
		v.live[p.ID()] = true
	}
	return v, nil
}

// own returns the harness of p when p is a harness session process of
// self's: a harness, no launcher, of that owner.
func (v tableView) own(p platform.Proc, self string) (event.Harness, bool) {
	h, ok := v.harness[p.PID]
	if !ok || v.launcher[p.PID] || p.Owner == "" || p.Owner != self {
		return "", false
	}
	return h, true
}

// Scan reads the process table once and reports every harness process of
// this user (Options.Self) that started at least Grace ago, that no hook
// claimed and that no earlier scan reported. A harness's own launcher,
// such as the node wrapper npm puts in front of codex, isn't reported; its
// child is (platform.LauncherOf). Scan returns the sessions it reported,
// and gives Options.Running the count of this user's harness processes.
func (s *Scanner) Scan() ([]Session, error) {
	s.mu.Lock()
	s.reads++
	read := s.reads
	s.mu.Unlock()
	began := s.opts.Now()
	v, err := s.readTable()
	if err != nil {
		s.running(0)
		return nil, err
	}
	now := s.opts.Now()
	goos := s.opts.OS
	t, all, live := v.t, v.all, v.live

	s.mu.Lock()
	s.noteSeen(v, began, now)
	var found []Session
	mine := 0 // this user's harness processes, launchers left out
	for _, p := range all {
		h, ok := v.own(p, s.self)
		if !ok {
			continue
		}
		id := p.ID()
		mine++
		if _, ok := s.claimed[id]; ok {
			continue
		}
		if _, done := s.reported[id]; done {
			continue
		}
		if !p.Start.IsZero() && now.Sub(p.Start) < Grace {
			continue
		}
		l := platform.Classify(goos, t.Chain(p.PID))
		sess := Session{
			Key:     newKey(),
			Harness: h,
			PID:     p.PID,
			Started: p.Start,
			Found:   now,
			Event: event.SessionStart{
				StartKind:   event.StartStartup,
				Entrypoint:  l.Entrypoint,
				ParentClass: l.ParentClass,
				HasTTY:      l.HasTTY,
			},
		}
		sess.ReportKey = sess.Key
		s.reported[id] = sess
		found = append(found, sess)
	}
	// Forget processes that have exited, so the maps stay as small as the
	// process table, but keep a claim that came since this table was read.
	changed := len(found) > 0
	for id, at := range s.claimed {
		if !live[id] && at < read {
			delete(s.claimed, id)
			changed = true
		}
	}
	for id, sess := range s.reported {
		// A tie Tie made since this read began may be of a process that
		// started while the table was read.
		if !live[id] && !(sess.Tied && sess.tiedRead >= read) {
			delete(s.reported, id)
			changed = true
			if sess.Tied {
				s.ended[sess.Key] = endedTie{sess: sess, at: now}
			}
		}
	}
	for key, e := range s.ended {
		if now.Sub(e.at) > EndedKeep {
			delete(s.ended, key)
		}
	}
	for _, sess := range found {
		s.opts.Report(sess)
	}
	// Store gets the reports only once Report has them. Others save the
	// same Store whenever they like, as the notifier does with state.json,
	// and a report saved before Report ran would be lost to an agent
	// killed in between.
	if changed {
		s.putLocked()
	}
	s.mu.Unlock()

	// After Report, so an agent killed in between reports the session
	// again rather than not at all.
	if changed {
		s.save()
	}
	s.running(mine)
	return found, nil
}

// Sessions returns the key of each session the scan has reported, by its
// process, until a scan sees the process gone or a hook claims it: the
// key of the session a log reader found running there once Tie tied it.
// The focus component reads it to find the sessions the window in front
// hosts (SM 3.2, DESIGN 3.3).
func (s *Scanner) Sessions() map[platform.ID]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[platform.ID]string, len(s.reported))
	for id, sess := range s.reported {
		out[id] = sess.Key
	}
	return out
}

// TieSlack is how much later than a session's log line its process may
// have started, by the process table's clock, to be tied to it: the two
// clocks are one, read at different moments.
const TieSlack = 2 * time.Second

// maxTouched bounds the sessions Touch remembers.
const maxTouched = 512

// touch is when a session's lines were read live, first and last (Touch);
// and read is when a table read that began after its first line ended
// (noteSeen): a line of it written after that shows its process ran at
// that read, and so was in its table (Tie).
type touch struct {
	first, last time.Time
	read        time.Time
}

// Touch records that a line of session key, which no hook reports, was
// written at at and read as it was written, so Tie knows when the session's
// first line read live was written, which its process must have started
// by, and which read came after it.
func (s *Scanner) Touch(key string, at time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.touchLocked(key, at)
}

// touchLocked is Touch with s.mu held.
func (s *Scanner) touchLocked(key string, at time.Time) {
	t, ok := s.touched[key]
	if !ok && len(s.touched) >= maxTouched {
		// The session written to longest ago goes.
		var oldest string
		for k, o := range s.touched {
			if oldest == "" || o.last.Before(s.touched[oldest].last) {
				oldest = k
			}
		}
		delete(s.touched, oldest)
	}
	if t.first.IsZero() || at.Before(t.first) {
		t.first = at
	}
	if at.After(t.last) {
		t.last = at
	}
	s.touched[key] = t
}

// FirstTouch returns when the first line of session key read live was
// written (Touch), and false when none is remembered.
func (s *Scanner) FirstTouch(key string) (time.Time, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.touched[key]
	return t.first, ok
}

// TiedTo returns the process session key is tied to (Tie), while the scan
// last saw it running.
func (s *Scanner) TiedTo(key string) (Session, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, sess := range s.reported {
		if sess.Tied && sess.Key == key {
			return sess, true
		}
	}
	return Session{}, false
}

// EndedKeep is how long after a scan saw a tied process gone TieOf still
// gives its tie, long enough for the session's session.start, which waits
// out its hooks' grace, to be written as the scan's.
const EndedKeep = 2 * time.Minute

// seenKeep is how long the scanner remembers a harness process a table
// read found.
const seenKeep = 10 * time.Minute

// seenProc is a harness process a table read found, when last, and when
// a read first found it gone, zero while none has.
type seenProc struct {
	harness     event.Harness
	start, last time.Time
	gone        time.Time
}

// endedTie is a tie whose process a scan saw gone, and when.
type endedTie struct {
	sess Session
	at   time.Time
}

// noteSeen records the harness processes of this user that v, a read that
// began at began and ended at now, holds and that could run a session no
// hook reports, tied to one or not, none claimed by a hook; when it first
// finds one of them gone; and that it began after the first line of each
// session touched before it (touch.read). A process Store kept from an
// earlier run, which no read of this run found, counts as found gone by v
// when v lacks it, since it may have ended since. It forgets the processes
// not seen for seenKeep. s.mu is held.
func (s *Scanner) noteSeen(v tableView, began, now time.Time) {
	for _, p := range v.all {
		h, ok := v.own(p, s.self)
		if !ok {
			continue
		}
		id := p.ID()
		if _, claimed := s.claimed[id]; claimed {
			delete(s.seen, id)
			continue
		}
		s.seen[id] = seenProc{harness: h, start: p.Start, last: now}
	}
	for id, sess := range s.reported {
		if _, ok := s.seen[id]; !ok && !v.live[id] {
			s.seen[id] = seenProc{harness: sess.Harness, start: sess.Started, last: now, gone: now}
		}
	}
	for id, sp := range s.seen {
		switch {
		case now.Sub(sp.last) > seenKeep:
			delete(s.seen, id)
		case !v.live[id] && sp.gone.IsZero():
			sp.gone = now
			s.seen[id] = sp
		}
	}
	for key, t := range s.touched {
		if t.read.IsZero() && !t.first.After(began) {
			t.read = now
			s.touched[key] = t
		}
	}
}

// TieOf returns the tie of session key while its process runs (TiedTo),
// and for EndedKeep after a scan saw the process gone, as a session.start
// written after a short session ended needs.
func (s *Scanner) TieOf(key string) (Session, bool) {
	if sess, ok := s.TiedTo(key); ok {
		return sess, true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.ended[key]
	if !ok || s.opts.Now().Sub(e.at) > EndedKeep {
		return Session{}, false
	}
	return e.sess, true
}

// Tie ties session key, of harness h, to the process running it, for a
// session no hook reports whose line, written at at, a log reader has just
// read (WP-AD3): the session then has the process's parent, TTY and
// entrypoint, which SM 2.2 rule 4 reads and no log states, and Sessions
// gives key for the process, so focus finds the session in the window that
// hosts it. Tie reads the process table, and ties key only when exactly one
// process can be the session's, since a wrong tie gives the session another
// session's parent and TTY and puts it in another's window, which is worse
// than no tie:
//
//   - A process that started and ended between two table reads is in none,
//     so the session is weighed only on a line written after a read that
//     began after its first line, when its process, if it still writes,
//     was running and so in that read: the first try only reads the table,
//     unless a scan's read did. judged is false when Tie waits so.
//   - A process of harness h that a read found, tied to a session or not,
//     and that is gone now could have been the session's, so nothing is
//     tied, unless a read found it gone before the session's first line
//     (TieSlack).
//   - Of the processes of harness h that run now, that no hook claimed and
//     no session is tied to, and that started by the session's first line
//     (TieSlack), the young ones Grace keeps from a scan's report included,
//     key is tied to the one there is. With none, or two or more, nothing
//     is.
//
// A process tied to a session never passes to another. A session that
// /clear starts runs in the process of the one it cleared, and the agent
// ties it to none (internal/agent's tieLive). One that /resume switches to
// in the process of a tied one, or one resumed in a new process that
// started after the first line of it this run remembers, has its own
// process left out: it stays untied, unless exactly one other untied
// process runs, which then takes it, a wrong tie this rule can't see. A
// hook that claims a tied process undoes its tie, as a scan that sees it
// gone and Untie do; a session tied already keeps its process until then.
func (s *Scanner) Tie(h event.Harness, key string, at time.Time) (sess Session, tied, judged bool) {
	if key == "" {
		return Session{}, false, true
	}
	if had, ok := s.TiedTo(key); ok {
		return had, true, true
	}
	s.mu.Lock()
	if _, ok := s.touched[key]; !ok {
		s.touchLocked(key, at)
	}
	read := s.touched[key].read
	s.mu.Unlock()
	if !read.IsZero() && !at.After(read) {
		// A line from before the read its tie waits for: the next one.
		return Session{}, false, false
	}
	began := s.opts.Now()
	v, err := s.readTable()
	if err != nil {
		return Session{}, false, true
	}
	now := s.opts.Now()
	s.mu.Lock()
	sess, tied, judged = s.tieLocked(v, h, key, at, read, began, now)
	if tied {
		s.putLocked()
	}
	s.mu.Unlock()
	if tied {
		s.save()
	}
	return sess, tied, judged
}

// tieLocked is Tie's weighing of the read v, which began at began and
// ended at now, for a line of key written at at, with read the moment the
// tie waited for. s.mu is held.
func (s *Scanner) tieLocked(v tableView, h event.Harness, key string, at, read, began, now time.Time) (Session, bool, bool) {
	s.noteSeen(v, began, now)
	if read.IsZero() {
		return Session{}, false, false
	}
	first := at
	if t := s.touched[key]; !t.first.IsZero() && t.first.Before(at) {
		first = t.first
	}
	// mayHaveRun says a process of h that started at start and that a read
	// found gone at gone could have run the line at at: it started by then,
	// and no read found it gone before the session's first line.
	mayHaveRun := func(start, gone time.Time) bool {
		startedBy := start.IsZero() || !start.After(at.Add(TieSlack))
		goneBefore := !gone.IsZero() && gone.Before(first.Add(-TieSlack))
		return startedBy && !goneBefore
	}
	for id, sp := range s.seen {
		if _, claimed := s.claimed[id]; claimed || v.live[id] || sp.harness != h {
			continue
		}
		if mayHaveRun(sp.start, sp.gone) {
			return Session{}, false, true
		}
	}
	for _, e := range s.ended {
		if e.sess.Harness == h && mayHaveRun(e.sess.Started, e.at) {
			return Session{}, false, true
		}
	}
	var untied []platform.Proc
	for _, p := range v.all {
		ph, ok := v.own(p, s.self)
		if !ok || ph != h {
			continue
		}
		id := p.ID()
		if _, claimed := s.claimed[id]; claimed || s.reported[id].Tied {
			continue
		}
		if !p.Start.IsZero() && p.Start.After(first.Add(TieSlack)) {
			continue
		}
		untied = append(untied, p)
	}
	if len(untied) != 1 {
		return Session{}, false, true
	}
	pick := untied[0]
	l := platform.Classify(s.opts.OS, v.t.Chain(pick.PID))
	sess := Session{Key: key, Tied: true, Harness: h, PID: pick.PID, Started: pick.Start, Found: now,
		Event:    event.SessionStart{StartKind: event.StartStartup, Entrypoint: l.Entrypoint, ParentClass: l.ParentClass, HasTTY: l.HasTTY},
		tiedRead: s.reads}
	if old, ok := s.reported[pick.ID()]; ok {
		sess.ReportKey = old.reportKey()
	}
	s.reported[pick.ID()] = sess
	return sess, true, true
}

// Untie undoes the tie of session key (Tie), as once a line of it runs in a
// folder out of pairtime (DESIGN 8.8): Sessions no longer gives key for its
// process, which goes back to the key Report gave it, or, if Report never
// had it, to the next scan's report, and TieOf gives no tie for key either.
// It reports whether a tie went.
func (s *Scanner) Untie(key string) bool {
	if key == "" {
		return false
	}
	s.mu.Lock()
	gone := false
	for id, sess := range s.reported {
		if !sess.Tied || sess.Key != key {
			continue
		}
		gone = true
		if rk := sess.ReportKey; rk != "" {
			sess.Key, sess.Tied = rk, false
			s.reported[id] = sess
		} else {
			delete(s.reported, id)
		}
	}
	if _, ok := s.ended[key]; ok {
		delete(s.ended, key)
		gone = true
	}
	if gone {
		s.putLocked()
	}
	s.mu.Unlock()
	if gone {
		s.save()
	}
	return gone
}

// Claimed reports whether a hook has claimed the process id, and no scan
// has seen it gone since. The hook path forgets the sessions of processes
// that aren't (internal/agent's hookTies).
func (s *Scanner) Claimed(id platform.ID) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.claimed[id]
	return ok
}

func (s *Scanner) running(n int) {
	if s.opts.Running != nil {
		s.opts.Running(n)
	}
}

// load takes what Store kept, if it is from this boot. A damaged entry is
// dropped, like any damaged state (DESIGN 3.5).
func (s *Scanner) load() {
	if s.opts.Store == nil {
		return
	}
	var st saved
	ok, err := s.opts.Store.Get(stateKey, &st)
	switch {
	case err != nil:
		s.opts.Log.Warn("procs: state.json's procs entry doesn't parse, so the scan starts afresh", "err", err)
		return
	case !ok || st.Boot != s.boot:
		return
	}
	for _, id := range st.Claimed {
		s.claimed[id] = 0 // older than any read this scanner begins
	}
	for _, r := range st.Reported {
		if r.Session.Key == "" {
			continue
		}
		s.reported[r.Proc] = r.Session
	}
}

// putLocked hands what the scanner remembers to Store, in memory; save
// writes it out. s.mu must be held.
func (s *Scanner) putLocked() {
	if s.opts.Store == nil {
		return
	}
	st := saved{
		Boot:     s.boot,
		Claimed:  make([]platform.ID, 0, len(s.claimed)),
		Reported: make([]savedReport, 0, len(s.reported)),
	}
	for id := range s.claimed {
		st.Claimed = append(st.Claimed, id)
	}
	for id, sess := range s.reported {
		st.Reported = append(st.Reported, savedReport{Proc: id, Session: sess})
	}
	slices.SortFunc(st.Claimed, compareIDs)
	slices.SortFunc(st.Reported, func(a, b savedReport) int { return compareIDs(a.Proc, b.Proc) })
	if err := s.opts.Store.Put(stateKey, st); err != nil {
		s.opts.Log.Warn("procs: couldn't keep the scan's state", "err", err)
	}
}

func (s *Scanner) save() {
	if s.opts.Store == nil {
		return
	}
	if err := s.opts.Store.Save(); err != nil {
		s.opts.Log.Warn("procs: couldn't write state.json", "err", err)
	}
}

func compareIDs(a, b platform.ID) int {
	return cmp.Or(cmp.Compare(a.PID, b.PID), cmp.Compare(a.Start, b.Start))
}

// Start scans every Interval on sch and returns the step that stops it.
func (s *Scanner) Start(sch *sched.Scheduler) func() {
	every := sch.Every("procs.scan", Interval, func(time.Time) { s.scan() })
	kick := sch.After("procs.kick", Interval, func(now time.Time) {
		s.mu.Lock()
		s.lastKick = now
		s.mu.Unlock()
		s.scan()
	})
	kick.Stop() // armed only by Kick
	s.mu.Lock()
	s.kick = kick
	s.mu.Unlock()
	return func() {
		// Without s.kick, a Kick from here on does nothing, and Stop undoes
		// one that came before.
		s.mu.Lock()
		s.kick = nil
		s.mu.Unlock()
		every.Stop()
		kick.Stop()
	}
}

// Kick asks for a scan soon, as DESIGN 3.3 has the scan run on each focus
// change. Kicks within KickDelay of each other run one scan, and a kicked
// scan runs at most once per KickGap. Kick does nothing before Start.
func (s *Scanner) Kick() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.kick == nil {
		return
	}
	// The gap is counted on the wall clock, the scheduler's time. Capped
	// at KickGap, a clock set back since the last kicked scan holds a kick
	// no longer than that.
	wait := min(s.lastKick.Add(KickGap).Sub(s.opts.Now()), KickGap)
	s.kick.Reset(max(wait, KickDelay))
}

func (s *Scanner) scan() {
	_, err := s.Scan()
	if err != nil {
		s.opts.Log.Warn("procs scan failed", "err", err)
	}
	if s.opts.Scanned != nil {
		s.opts.Scanned(err)
	}
}

// newKey returns a random opaque key: "ps_" and 16 hex digits.
func newKey() string {
	var b [8]byte
	rand.Read(b[:])
	return "ps_" + hex.EncodeToString(b[:])
}
