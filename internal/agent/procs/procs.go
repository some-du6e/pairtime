// SPDX-License-Identifier: AGPL-3.0-or-later

// Package procs is the agent's harness process scan (DESIGN 3.3). Every
// PROC_SCAN_S, and soon after a focus change, it lists the processes,
// finds harness processes no hook has spoken for, and reports each one as
// a session.start with its parent_class, entrypoint and has_tty (SM 1.2,
// 2.2 rule 4). That is how `claude --bare`, `claude -p` and `codex exec`
// runs that fire no hooks (HS 3.1, 3.5) still get classified.
//
// The scan reads only what internal/platform's readers read: process ids,
// parent ids, executable names, start times, process groups and whether a
// terminal is attached. It never reads a command line or an environment,
// so it can't see -p or --bare; hooks and OTel report the entrypoint of a
// session that fires them, and the log readers that of one that doesn't.
package procs

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"runtime"
	"sync"
	"time"

	"github.com/some-du6e/pairtime/internal/agent/sched"
	"github.com/some-du6e/pairtime/internal/platform"
	"github.com/some-du6e/pairtime/pkg/event"
)

// Interval is PROC_SCAN_S, SM 8.4's interval of the harness process scan.
const Interval = 60 * time.Second

// Grace is how long a harness process runs before a scan may report it.
// A session that fires hooks claims its process with its first one,
// Claude Code's SessionStart, as it starts; Grace leaves that hook time to
// reach the agent, even through the inbox while the agent restarts.
const Grace = 10 * time.Second

// KickDelay is how long after a focus change the scan runs, so a burst of
// changes (alt-tab through five windows) costs one scan.
const KickDelay = 2 * time.Second

// KickGap is the least time between two scans that focus changes caused.
// Someone switching windows every few seconds still gets at most one scan
// per KickGap, which keeps the scan's cost bounded on an old laptop.
const KickGap = 10 * time.Second

// Session is a harness process that no hook claimed.
type Session struct {
	// Key is a random opaque key for the process (AGENTS.md). The scan
	// can't know the harness's own session id; whoever joins this to the
	// session a log reader finds maps the key.
	Key     string
	Harness event.Harness
	// PID and Started name the process on this device. They are local and
	// never leave it.
	PID     int
	Started time.Time
	// Found is when the scan that reported it ran.
	Found time.Time
	Event event.SessionStart
}

// Options configure a Scanner.
type Options struct {
	// Report gets each session, once, on the goroutine that ran the scan.
	Report func(Session)
	// Read reads the process table. Nil means this OS's reader.
	Read func() ([]platform.Proc, error)
	// OS is the GOOS whose names Read returns. Empty means runtime.GOOS.
	OS string
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

	mu       sync.Mutex
	claimed  map[procID]bool
	reported map[procID]string // the Session's Key
	lastKick time.Time
	kick     *sched.Job
}

// procID names one process across PID reuse.
type procID struct {
	pid   int
	start int64 // Unix seconds, 0 when unknown
}

func idOf(pid int, start time.Time) procID {
	id := procID{pid: pid}
	if !start.IsZero() {
		id.start = start.Unix()
	}
	return id
}

// New makes a Scanner. It doesn't scan until Scan or Start is called.
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
	return &Scanner{
		opts:     opts,
		claimed:  make(map[procID]bool),
		reported: make(map[procID]string),
	}
}

// Claim records that a hook arrived from inside the harness process pid,
// which started at start, so no scan reports it. The hook path claims the
// process that platform.HookHost finds in the hook's parent chain.
func (s *Scanner) Claim(pid int, start time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.claimed[idOf(pid, start)] = true
}

// Scan reads the process table once and reports every harness process
// that started at least Grace ago, that no hook claimed and that no
// earlier scan reported. A harness process whose direct child runs the
// same harness, such as the node wrapper npm puts in front of codex, isn't
// reported; its child is. Scan returns the sessions it reported.
func (s *Scanner) Scan() ([]Session, error) {
	procs, err := s.opts.Read()
	if err != nil {
		return nil, fmt.Errorf("reading the process table: %w", err)
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
	// A harness process whose direct child runs the same harness is that
	// child's launcher or an earlier copy that relaunched itself. The
	// child is the session, and the child is what a hook claims.
	launcher := make(map[int]bool)
	for _, p := range all {
		if h, ok := harness[p.PID]; ok {
			if parent, ok := t.Parent(p); ok && harness[parent.PID] == h {
				launcher[parent.PID] = true
			}
		}
	}

	s.mu.Lock()
	live := make(map[procID]bool)
	var found []Session
	for _, p := range all {
		h, ok := harness[p.PID]
		if !ok {
			continue
		}
		id := idOf(p.PID, p.Start)
		live[id] = true
		if launcher[p.PID] || s.claimed[id] || s.reported[id] != "" {
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
		s.reported[id] = sess.Key
		found = append(found, sess)
	}
	// Forget processes that have exited, so the maps stay as small as the
	// process table.
	for id := range s.claimed {
		if !live[id] {
			delete(s.claimed, id)
		}
	}
	for id := range s.reported {
		if !live[id] {
			delete(s.reported, id)
		}
	}
	s.mu.Unlock()

	for _, sess := range found {
		s.opts.Report(sess)
	}
	return found, nil
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
