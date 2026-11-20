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
// A session that fires hooks claims its process with its first one,
// Claude Code's SessionStart, as it starts; Grace leaves that hook time to
// reach the agent, even through the inbox while the agent restarts. A
// first hook that comes later, as when Claude Code's folder-trust dialog
// stays open (DESIGN 5.1), still claims the process, and the session the
// scan reported goes to Options.Withdraw.
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
	// session a log reader finds maps the key.
	Key     string        `json:"key"`
	Harness event.Harness `json:"harness"`
	// PID and Started name the process on this device. They are local and
	// never leave it.
	PID     int       `json:"pid"`
	Started time.Time `json:"started,omitzero"`
	// Found is when the scan that reported it ran.
	Found time.Time          `json:"found"`
	Event event.SessionStart `json:"event"`
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
	// its hooks name.
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
	// this user on Linux and macOS, this logon session on Windows. Empty
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

	mu       sync.Mutex
	claimed  map[platform.ID]bool
	reported map[platform.ID]Session
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
		claimed:  make(map[platform.ID]bool),
		reported: make(map[platform.ID]Session),
	}
	s.load()
	return s
}

// Claim records that a hook arrived from inside the harness process host,
// read on the boot boot (in Options.Boot's terms), so no scan reports the
// process. The hook path claims the process that platform.HookHost finds
// in the hook's parent chain. A claim from another boot, such as a hook
// spooled before the machine restarted, is dropped. When a scan reported
// the process already, Claim hands that session to Options.Withdraw.
func (s *Scanner) Claim(boot string, host platform.Proc) {
	if boot != s.boot {
		return
	}
	id := host.ID()
	s.mu.Lock()
	if s.claimed[id] {
		s.mu.Unlock()
		return
	}
	s.claimed[id] = true
	sess, wasReported := s.reported[id]
	delete(s.reported, id)
	s.putLocked()
	s.mu.Unlock()
	s.save()
	if wasReported && s.opts.Withdraw != nil {
		s.opts.Withdraw(sess)
	}
}

// Scan reads the process table once and reports every harness process of
// this user (Options.Self) that started at least Grace ago, that no hook
// claimed and that no earlier scan reported. A harness's own launcher,
// such as the node wrapper npm puts in front of codex, isn't reported; its
// child is (platform.LauncherOf). Scan returns the sessions it reported,
// and gives Options.Running the count of this user's harness processes.
func (s *Scanner) Scan() ([]Session, error) {
	procs, err := s.opts.Read()
	if err != nil {
		s.running(0)
		return nil, fmt.Errorf("reading the process table: %w", err)
	}
	if s.self == "" {
		s.running(0)
		return nil, errNoSelf
	}
	now := s.opts.Now()
	goos := s.opts.OS
	t := platform.NewTable(goos, procs)

	all := t.Procs()
	harness := make(map[int]event.Harness)
	for _, p := range all {
		if h, ok := platform.HarnessOf(goos, p); ok {
			harness[p.PID] = h
		}
	}
	launcher := make(map[int]bool)
	for _, p := range all {
		if _, ok := harness[p.PID]; !ok {
			continue
		}
		if parent, ok := t.Parent(p); ok && platform.LauncherOf(goos, parent, p) {
			launcher[parent.PID] = true
		}
	}

	s.mu.Lock()
	live := make(map[platform.ID]bool)
	var found []Session
	mine := 0 // this user's harness processes, launchers left out
	for _, p := range all {
		h, ok := harness[p.PID]
		if !ok {
			continue
		}
		id := p.ID()
		live[id] = true
		if launcher[p.PID] || p.Owner == "" || p.Owner != s.self {
			continue
		}
		mine++
		if s.claimed[id] {
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
		s.reported[id] = sess
		found = append(found, sess)
	}
	// Forget processes that have exited, so the maps stay as small as the
	// process table.
	changed := len(found) > 0
	for id := range s.claimed {
		if !live[id] {
			delete(s.claimed, id)
			changed = true
		}
	}
	for id := range s.reported {
		if !live[id] {
			delete(s.reported, id)
			changed = true
		}
	}
	if changed {
		s.putLocked()
	}
	for _, sess := range found {
		s.opts.Report(sess)
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
		s.claimed[id] = true
	}
	for _, r := range st.Reported {
		if r.Session.Key != "" {
			s.reported[r.Proc] = r.Session
		}
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
		every.Stop()
		kick.Stop()
		s.mu.Lock()
		s.kick = nil
		s.mu.Unlock()
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
	d := KickDelay
	if wait := s.lastKick.Add(KickGap).Sub(s.opts.Now()); wait > d {
		d = wait
	}
	s.kick.Reset(d)
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
