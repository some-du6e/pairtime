// SPDX-License-Identifier: AGPL-3.0-or-later

package procs

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/agent/sched"
	"github.com/some-du6e/pairtime/internal/agent/state"
	"github.com/some-du6e/pairtime/internal/platform"
	"github.com/some-du6e/pairtime/pkg/event"
	"github.com/some-du6e/pairtime/pkg/params"
)

var t0 = time.Date(2026, 11, 19, 22, 0, 0, 0, time.UTC)

func sec(s int) time.Time { return t0.Add(time.Duration(s) * time.Second) }

const (
	// me is the user the tests' scanners run as, and sibling another user
	// of the same machine.
	me      = "uid:1000"
	sibling = "uid:1001"
	root    = "uid:0"
	boot    = "4d1f4c2a-0d3b-4e1c-9a51-7f0e2b9c8d10"
)

// terminal is a Linux desktop with two claude sessions in a terminal and
// codex started through its npm launcher.
func terminal() []platform.Proc {
	return []platform.Proc{
		{PID: 1, Name: "systemd", PGID: 1, TTY: platform.TTYNone, Start: sec(-3600), Owner: root},
		{PID: 100, PPID: 1, Name: "gnome-terminal-server", PGID: 100, TTY: platform.TTYNone, Start: sec(-600), Owner: me},
		{PID: 110, PPID: 100, Name: "bash", PGID: 110, TTY: platform.TTYAttached, Start: sec(-590), Owner: me},
		{PID: 111, PPID: 110, Name: "claude", PGID: 111, TTY: platform.TTYAttached, Start: sec(-300), Owner: me},
		{PID: 120, PPID: 100, Name: "bash", PGID: 120, TTY: platform.TTYAttached, Start: sec(-580), Owner: me},
		{PID: 121, PPID: 120, Name: "claude", PGID: 121, TTY: platform.TTYAttached, Start: sec(-200), Owner: me},
		{PID: 130, PPID: 100, Name: "bash", PGID: 130, TTY: platform.TTYAttached, Start: sec(-570), Owner: me},
		{PID: 131, PPID: 130, Name: "node", Alias: "codex", PGID: 131, TTY: platform.TTYAttached, Start: sec(-100), Owner: me},
		{PID: 132, PPID: 131, Name: "codex", PGID: 131, TTY: platform.TTYAttached, Start: sec(-100), Owner: me},
	}
}

// familyPC is terminal() plus a sibling's session on the same machine,
// reached through root's sshd, and a harness whose owner the reader
// couldn't tell.
func familyPC() []platform.Proc {
	return append(terminal(),
		platform.Proc{PID: 200, PPID: 1, Name: "sshd", PGID: 200, TTY: platform.TTYNone, Start: sec(-3000), Owner: root},
		platform.Proc{PID: 210, PPID: 200, Name: "bash", PGID: 210, TTY: platform.TTYAttached, Start: sec(-900), Owner: sibling},
		platform.Proc{PID: 211, PPID: 210, Name: "claude", PGID: 211, TTY: platform.TTYAttached, Start: sec(-800), Owner: sibling},
		platform.Proc{PID: 220, PPID: 100, Name: "claude", PGID: 220, TTY: platform.TTYAttached, Start: sec(-700)},
	)
}

type fake struct {
	procs []platform.Proc
	err   error
	reads int
}

func (f *fake) read() ([]platform.Proc, error) {
	f.reads++
	return slices.Clone(f.procs), f.err
}

func newScanner(f *fake, now *time.Time, got *[]Session) *Scanner {
	return New(Options{
		Read:   f.read,
		OS:     platform.Linux,
		Self:   me,
		Boot:   boot,
		Now:    func() time.Time { return *now },
		Report: func(s Session) { *got = append(*got, s) },
	})
}

func pidsOf(ss []Session) []int {
	out := make([]int, len(ss))
	for i, s := range ss {
		out[i] = s.PID
	}
	return out
}

var keyRE = regexp.MustCompile(`^ps_[0-9a-f]{16}$`)

func TestScan(t *testing.T) {
	tests := []struct {
		name   string
		procs  []platform.Proc
		claims []int // pids claimed before the scan
		now    time.Time
		want   []int // pids reported
	}{
		{"every hookless harness is reported, a launcher's child once", terminal(), nil, sec(0), []int{111, 121, 132}},
		{"a harness a hook claimed isn't reported", terminal(), []int{111, 132}, sec(0), []int{121}},
		{"a harness younger than the grace isn't reported yet", terminal(), nil, sec(-200).Add(Grace - time.Second), []int{111}},
		{"a harness exactly the grace old is reported", terminal(), nil, sec(-200).Add(Grace), []int{111, 121}},
		{"a harness of unknown age is reported", []platform.Proc{{PID: 5, Name: "claude", Owner: me}}, nil, sec(0), []int{5}},
		{"another user's harness isn't reported, nor one whose owner is unknown", familyPC(), nil, sec(0), []int{111, 121, 132}},
		{"a native harness under the same native harness is a session of its own, and so is the outer one", []platform.Proc{
			{PID: 10, Name: "bash", PGID: 10, TTY: platform.TTYAttached, Start: sec(-900), Owner: me},
			{PID: 11, PPID: 10, Name: "codex", PGID: 11, TTY: platform.TTYAttached, Start: sec(-600), Owner: me},
			{PID: 12, PPID: 11, Name: "codex", PGID: 11, TTY: platform.TTYAttached, Start: sec(-60), Owner: me},
		}, nil, sec(0), []int{11, 12}},
		{"no harness, no report", []platform.Proc{{PID: 5, Name: "bash", Owner: me}, {PID: 6, PPID: 5, Name: "vim", Owner: me}}, nil, sec(0), []int{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &fake{procs: tt.procs}
			now := tt.now
			var got []Session
			s := newScanner(f, &now, &got)
			tab := platform.NewTable(platform.Linux, tt.procs)
			for _, pid := range tt.claims {
				p, _ := tab.Get(pid)
				s.Claim(boot, p)
			}
			found, err := s.Scan()
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(pidsOf(got), tt.want) || !slices.Equal(pidsOf(found), tt.want) {
				t.Errorf("reported %v, returned %v, want %v", pidsOf(got), pidsOf(found), tt.want)
			}
		})
	}
}

// TestScanCountsRunningHarnesses checks the count each scan gives
// Options.Running, which keeps presence's project context live while a
// harness session process runs (DESIGN 3.3).
func TestScanCountsRunningHarnesses(t *testing.T) {
	young := []platform.Proc{
		{PID: 5, Name: "bash", Owner: me, Start: sec(-60)},
		{PID: 6, PPID: 5, Name: "claude", Owner: me, Start: sec(-1)},
	}
	tests := []struct {
		name   string
		procs  []platform.Proc
		err    error
		noSelf bool
		claims []int
		want   int
	}{
		{"every harness process of this user counts, a launcher and its child once", terminal(), nil, false, nil, 3},
		{"a harness a hook claimed counts too", terminal(), nil, false, []int{111, 132}, 3},
		{"another user's harness doesn't count, nor one whose owner is unknown", familyPC(), nil, false, nil, 3},
		{"a harness younger than the grace counts, though it isn't reported yet", young, nil, false, nil, 1},
		{"with no harness the count is 0", []platform.Proc{{PID: 5, Name: "bash", Owner: me}}, nil, false, nil, 0},
		{"a scan that can't read the table counts 0", terminal(), errors.New("no /proc"), false, nil, 0},
		{"a scan that can't tell whose processes are whose counts 0", terminal(), nil, true, nil, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var counts []int
			s := New(Options{
				Read:    (&fake{procs: tt.procs, err: tt.err}).read,
				OS:      platform.Linux,
				Self:    me,
				Boot:    boot,
				Now:     func() time.Time { return sec(0) },
				Running: func(n int) { counts = append(counts, n) },
			})
			if tt.noSelf {
				s.self = ""
			}
			tab := platform.NewTable(platform.Linux, tt.procs)
			for _, pid := range tt.claims {
				p, _ := tab.Get(pid)
				s.Claim(boot, p)
			}
			s.Scan()
			if !slices.Equal(counts, []int{tt.want}) {
				t.Errorf("Running got %v, want [%d] once", counts, tt.want)
			}
		})
	}
}

func TestScanSession(t *testing.T) {
	f := &fake{procs: familyPC()}
	now := sec(0)
	var got []Session
	if _, err := newScanner(f, &now, &got).Scan(); err != nil {
		t.Fatal(err)
	}
	byPID := map[int]Session{}
	for _, s := range got {
		byPID[s.PID] = s
	}
	t.Run("a session carries the harness, the process and the classification", func(t *testing.T) {
		s := byPID[111]
		want := event.SessionStart{StartKind: event.StartStartup, Entrypoint: event.EntrypointUnknown, ParentClass: event.ParentTerminalApp, HasTTY: true}
		if s.Harness != event.HarnessClaudeCode || !s.Started.Equal(sec(-300)) || !s.Found.Equal(now) || !reflectEqual(s.Event, want) {
			t.Errorf("session = %+v", s)
		}
	})
	t.Run("the session.start payload is valid", func(t *testing.T) {
		for _, s := range got {
			if err := s.Event.Validate(); err != nil {
				t.Errorf("pid %d: %v", s.PID, err)
			}
		}
	})
	t.Run("the npm launcher is skipped, so codex is a terminal session", func(t *testing.T) {
		if s := byPID[132]; s.Harness != event.HarnessCodex || s.Event.ParentClass != event.ParentTerminalApp {
			t.Errorf("codex session = %+v", s)
		}
	})
	t.Run("keys are random opaque keys, one per process", func(t *testing.T) {
		seen := map[string]bool{}
		for _, s := range got {
			if !keyRE.MatchString(s.Key) || seen[s.Key] {
				t.Errorf("key %q is malformed or repeated", s.Key)
			}
			seen[s.Key] = true
		}
	})
}

func reflectEqual(a, b event.SessionStart) bool {
	return a.StartKind == b.StartKind && a.Entrypoint == b.Entrypoint && a.ParentClass == b.ParentClass &&
		a.HasTTY == b.HasTTY && slices.Equal(a.AutomationFlags, b.AutomationFlags) && a.PermissionMode == b.PermissionMode
}

func TestScanOverTime(t *testing.T) {
	f := &fake{procs: terminal()}
	var now time.Time
	var got, withdrawn []Session
	s := New(Options{
		Read:     f.read,
		OS:       platform.Linux,
		Self:     me,
		Boot:     boot,
		Now:      func() time.Time { return now },
		Report:   func(s Session) { got = append(got, s) },
		Withdraw: func(s Session) { withdrawn = append(withdrawn, s) },
	})
	scan := func() []int {
		t.Helper()
		found, err := s.Scan()
		if err != nil {
			t.Fatal(err)
		}
		return pidsOf(found)
	}
	proc := func(pid int) platform.Proc {
		p, _ := platform.NewTable(platform.Linux, f.procs).Get(pid)
		return p
	}
	// rename gives pid the name a read finds for it. A read that can't
	// follow a process's exe link names it by its comm, which for an image
	// started by its version path is the version file's name.
	rename := func(pid int, name string) {
		for i := range f.procs {
			if f.procs[i].PID == pid {
				f.procs[i].Name = name
			}
		}
	}
	// terminal()'s harnesses start at -300 s (111), -200 s (121) and
	// -100 s (132).
	steps := []struct {
		name     string
		change   func()
		want     []int
		withdraw []int // pids withdrawn by the step, in all
	}{
		{"the first scan reports the harness past the grace", func() { now = sec(-195) }, []int{111}, nil},
		{"a later scan reports the ones that have grown past it", func() { now = sec(-80) }, []int{121, 132}, nil},
		{"a scan with nothing new reports nothing", func() { now = sec(-20) }, []int{}, nil},
		{"a reported harness that one read can't name keeps its report", func() { rename(121, "2.1.328") }, []int{}, nil},
		{"named claude again, it isn't reported a second time", func() { rename(121, "claude") }, []int{}, nil},
		{"a claim after a report withdraws the session, and the scan doesn't report it again", func() { s.Claim(boot, proc(111)) }, []int{}, []int{111}},
		{"a second claim withdraws nothing more", func() { s.Claim(boot, proc(111)) }, []int{}, []int{111}},
		{"a claimed harness that one read can't name keeps its claim", func() { rename(111, "2.1.328") }, []int{}, []int{111}},
		{"named claude again, it still isn't reported", func() { rename(111, "claude") }, []int{}, []int{111}},
		{"a claim from another boot is dropped", func() { s.Claim("another-boot", proc(121)) }, []int{}, []int{111}},
		{"a pid reused by a new harness is a new session", func() {
			now = sec(40)
			f.procs = slices.DeleteFunc(f.procs, func(p platform.Proc) bool { return p.PID == 121 })
			f.procs = append(f.procs, platform.Proc{PID: 121, PPID: 120, Name: "claude", PGID: 122, TTY: platform.TTYAttached, Start: sec(-20), Owner: me})
		}, []int{121}, []int{111}},
		{"an exited process is forgotten", func() {
			now = sec(100)
			f.procs = slices.DeleteFunc(f.procs, func(p platform.Proc) bool { return p.PID == 111 })
		}, []int{}, []int{111}},
	}
	for _, st := range steps {
		t.Run(st.name, func(t *testing.T) {
			st.change()
			if got := scan(); !slices.Equal(got, st.want) {
				t.Errorf("reported %v, want %v", got, st.want)
			}
			if got := pidsOf(withdrawn); !slices.Equal(got, st.withdraw) {
				t.Errorf("withdrew %v, want %v", got, st.withdraw)
			}
		})
	}
	t.Run("the withdrawn session is the one the scan reported", func(t *testing.T) {
		if len(withdrawn) != 1 || len(got) == 0 || withdrawn[0].Key != got[0].Key {
			t.Errorf("withdrew %+v, first reported %+v", withdrawn, got)
		}
	})
	t.Run("the scanner's memory is as small as the live harness processes", func(t *testing.T) {
		s.mu.Lock()
		defer s.mu.Unlock()
		// The new 121 and 132 are left; 111 and the old 121 have exited.
		if len(s.reported) != 2 || len(s.claimed) != 0 {
			t.Errorf("remembers %d reported and %d claimed processes, want 2 and 0", len(s.reported), len(s.claimed))
		}
	})
}

// TestClaimDuringAScan has a hook claim a process while the scan that
// found it is still reporting, which the hook path can do from its own
// goroutine at any moment.
func TestClaimDuringAScan(t *testing.T) {
	f := &fake{procs: terminal()}
	var mu sync.Mutex
	var order []string
	note := func(what string, s Session) {
		mu.Lock()
		defer mu.Unlock()
		order = append(order, fmt.Sprintf("%s %d", what, s.PID))
	}
	claimed := make(chan struct{})
	first := true
	var s *Scanner
	s = New(Options{
		Read: f.read,
		OS:   platform.Linux,
		Self: me,
		Boot: boot,
		Now:  func() time.Time { return sec(0) },
		Report: func(sess Session) {
			note("report", sess)
			if !first {
				return
			}
			first = false
			// The hook for 121 comes while the scan reports 111, before it
			// has reported 121.
			go func() {
				defer close(claimed)
				p, _ := platform.NewTable(platform.Linux, f.procs).Get(121)
				s.Claim(boot, p)
			}()
			time.Sleep(50 * time.Millisecond)
		},
		Withdraw: func(sess Session) { note("withdraw", sess) },
	})
	if _, err := s.Scan(); err != nil {
		t.Fatal(err)
	}
	<-claimed
	t.Run("the session is withdrawn after Report has it, never before", func(t *testing.T) {
		mu.Lock()
		defer mu.Unlock()
		want := []string{"report 111", "report 121", "report 132", "withdraw 121"}
		if !slices.Equal(order, want) {
			t.Errorf("the scanner called %q, want %q", order, want)
		}
	})
}

// TestClaimWhileTheTableIsRead has hooks claim two processes while a scan
// reads the table: a claude that has just started, which that table
// doesn't have yet, and a process that never shows up.
func TestClaimWhileTheTableIsRead(t *testing.T) {
	late := platform.Proc{PID: 140, PPID: 100, Name: "claude", PGID: 140, TTY: platform.TTYAttached, Start: sec(1), Owner: me}
	ghost := platform.Proc{PID: 150, PPID: 100, Name: "claude", PGID: 150, TTY: platform.TTYAttached, Start: sec(1), Owner: me}
	f := &fake{procs: terminal()}
	now := sec(0)
	var got []Session
	var s *Scanner
	claims := []platform.Proc{late, ghost}
	s = New(Options{
		Read: func() ([]platform.Proc, error) {
			procs, err := f.read()
			for _, p := range claims {
				s.Claim(boot, p)
			}
			claims = nil
			return procs, err
		},
		OS:     platform.Linux,
		Self:   me,
		Boot:   boot,
		Now:    func() time.Time { return now },
		Report: func(sess Session) { got = append(got, sess) },
	})
	claimedPIDs := func() []int {
		s.mu.Lock()
		defer s.mu.Unlock()
		var pids []int
		for id := range s.claimed {
			pids = append(pids, id.PID)
		}
		slices.Sort(pids)
		return pids
	}
	steps := []struct {
		name    string
		change  func()
		report  []int
		claimed []int
	}{
		{"the scan whose table lacks the newly claimed processes keeps their claims", func() {}, []int{111, 121, 132}, []int{140, 150}},
		{"once the table has the process, its claim keeps the scan from reporting it", func() {
			now = sec(60)
			f.procs = append(terminal(), late)
		}, nil, []int{140}},
	}
	for _, st := range steps {
		t.Run(st.name, func(t *testing.T) {
			got = nil
			st.change()
			if _, err := s.Scan(); err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(pidsOf(got), st.report) {
				t.Errorf("reported %v, want %v", pidsOf(got), st.report)
			}
			if c := claimedPIDs(); !slices.Equal(c, st.claimed) {
				t.Errorf("claims kept for %v, want %v: a claim older than the scan's read goes once its process is gone", c, st.claimed)
			}
		})
	}
}

// TestScanAcrossRestarts runs scanners one after another on one
// state.json, as an agent restarted by its updater or by a hook would.
func TestScanAcrossRestarts(t *testing.T) {
	name := filepath.Join(t.TempDir(), "state.json")
	f := &fake{procs: terminal()}
	now := sec(0)
	var got, withdrawn []Session
	start := func(t *testing.T, bootID string) *Scanner {
		t.Helper()
		st, problem, err := state.Open(name)
		if err != nil || problem != "" {
			t.Fatalf("state.Open: %v %s", err, problem)
		}
		return New(Options{
			Read:     f.read,
			OS:       platform.Linux,
			Self:     me,
			Boot:     bootID,
			Store:    st,
			Now:      func() time.Time { return now },
			Report:   func(s Session) { got = append(got, s) },
			Withdraw: func(s Session) { withdrawn = append(withdrawn, s) },
		})
	}
	scan := func(t *testing.T, s *Scanner) []int {
		t.Helper()
		found, err := s.Scan()
		if err != nil {
			t.Fatal(err)
		}
		return pidsOf(found)
	}
	proc := func(pid int) platform.Proc {
		p, _ := platform.NewTable(platform.Linux, f.procs).Get(pid)
		return p
	}

	first := start(t, boot)
	first.Claim(boot, proc(111)) // a hooked session
	if got := scan(t, first); !slices.Equal(got, []int{121, 132}) {
		t.Fatalf("the first agent reported %v, want [121 132]", got)
	}
	reported := slices.Clone(got)

	t.Run("after a restart neither the hooked session nor the reported ones are reported", func(t *testing.T) {
		if got := scan(t, start(t, boot)); len(got) != 0 {
			t.Errorf("the restarted agent reported %v", got)
		}
	})
	t.Run("a hook that claims a reported session after a restart withdraws it by its first key", func(t *testing.T) {
		s := start(t, boot)
		s.Claim(boot, proc(121))
		if len(withdrawn) != 1 || withdrawn[0].Key != reported[0].Key || withdrawn[0].PID != 121 {
			t.Errorf("withdrew %+v, want the session reported as %q", withdrawn, reported[0].Key)
		}
	})
	t.Run("an exited process leaves state.json at the next scan", func(t *testing.T) {
		// codex exits, and its npm wrapper with it.
		f.procs = slices.DeleteFunc(f.procs, func(p platform.Proc) bool { return p.PID == 131 || p.PID == 132 })
		scan(t, start(t, boot))
		st, _, err := state.Open(name)
		if err != nil {
			t.Fatal(err)
		}
		var kept saved
		if ok, err := st.Get(stateKey, &kept); !ok || err != nil {
			t.Fatalf("state.json has no procs entry: %v %v", ok, err)
		}
		var pids []int
		for _, r := range kept.Reported {
			pids = append(pids, r.Proc.PID)
		}
		for _, id := range kept.Claimed {
			pids = append(pids, id.PID)
		}
		slices.Sort(pids)
		if !slices.Equal(pids, []int{111, 121}) || kept.Boot != boot {
			t.Errorf("state.json keeps pids %v on boot %q, want [111 121] (both claimed) on %q", pids, kept.Boot, boot)
		}
	})
	t.Run("after the machine restarts, what state.json holds is dropped", func(t *testing.T) {
		got = nil
		if got := scan(t, start(t, "a-later-boot")); !slices.Equal(got, []int{111, 121}) {
			t.Errorf("on a new boot the scan reported %v, want [111 121], the old boot's claims dropped", got)
		}
	})
	t.Run("a damaged entry is dropped and the scan starts afresh", func(t *testing.T) {
		st, _, err := state.Open(name)
		if err != nil {
			t.Fatal(err)
		}
		if err := st.Put(stateKey, "not an object"); err != nil {
			t.Fatal(err)
		}
		if err := st.Save(); err != nil {
			t.Fatal(err)
		}
		if got := scan(t, start(t, boot)); !slices.Equal(got, []int{111, 121}) {
			t.Errorf("after a damaged entry the scan reported %v, want [111 121]", got)
		}
	})
}

// TestReportBeforeItsState has another component save the shared
// state.json while Report runs, as the notifier may at any moment from its
// own goroutine. A session's report must not reach the file before Report
// has the session, or an agent killed in between would never report it.
func TestReportBeforeItsState(t *testing.T) {
	tests := []struct {
		name string
	}{
		{name: "a save of the shared state.json while Report runs holds no report of this scan's sessions, and the scan's own save does"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			name := filepath.Join(t.TempDir(), "state.json")
			st, problem, err := state.Open(name)
			if err != nil || problem != "" {
				t.Fatalf("state.Open: %v %s", err, problem)
			}
			onDisk := func() []int {
				t.Helper()
				disk, _, err := state.Open(name)
				if err != nil {
					t.Fatal(err)
				}
				var kept saved
				if _, err := disk.Get(stateKey, &kept); err != nil {
					t.Fatal(err)
				}
				var pids []int
				for _, r := range kept.Reported {
					pids = append(pids, r.Proc.PID)
				}
				return pids
			}
			var early []int // what a save made during Report wrote out
			s := New(Options{
				Read:  (&fake{procs: terminal()}).read,
				OS:    platform.Linux,
				Self:  me,
				Boot:  boot,
				Store: st,
				Now:   func() time.Time { return sec(0) },
				Report: func(Session) {
					if err := st.Save(); err != nil {
						t.Error(err)
						return
					}
					early = append(early, onDisk()...)
				},
			})
			found, err := s.Scan()
			if err != nil {
				t.Fatal(err)
			}
			if got := pidsOf(found); !slices.Equal(got, []int{111, 121, 132}) || len(early) != 0 {
				t.Errorf("reported %v, and saves during Report wrote reports of %v; want [111 121 132] and none", got, early)
			}
			if got := onDisk(); !slices.Equal(got, []int{111, 121, 132}) {
				t.Errorf("after the scan state.json reports %v, want [111 121 132]", got)
			}
		})
	}
}

// TestScanAcrossAClockStep checks that a step of the wall clock, which on
// Linux moves every Start, neither reports a session again nor loses a
// claim: IDs come from the start ticks.
func TestScanAcrossAClockStep(t *testing.T) {
	procs := func(step time.Duration) []platform.Proc {
		return []platform.Proc{
			{PID: 110, Name: "bash", PGID: 110, TTY: platform.TTYAttached, Start: sec(-590).Add(step), Ticks: 1000, Owner: me},
			{PID: 111, PPID: 110, Name: "claude", PGID: 111, TTY: platform.TTYAttached, Start: sec(-300).Add(step), Ticks: 30000, Owner: me},
			{PID: 121, PPID: 110, Name: "claude", PGID: 121, TTY: platform.TTYAttached, Start: sec(-200).Add(step), Ticks: 40000, Owner: me},
		}
	}
	f := &fake{procs: procs(0)}
	now := sec(0)
	var got []Session
	s := newScanner(f, &now, &got)
	hooked, _ := platform.NewTable(platform.Linux, f.procs).Get(111)
	s.Claim(boot, hooked)
	if found, err := s.Scan(); err != nil || !slices.Equal(pidsOf(found), []int{121}) {
		t.Fatalf("before the step the scan reported %v, %v; want [121]", pidsOf(found), err)
	}
	for _, step := range []time.Duration{3 * time.Second, -90 * time.Second} {
		t.Run("after the clock steps by "+step.String()+" nothing is reported again", func(t *testing.T) {
			f.procs = procs(step)
			if found, err := s.Scan(); err != nil || len(found) != 0 {
				t.Errorf("the scan reported %v, %v", pidsOf(found), err)
			}
		})
	}
}

func TestIntervalMatchesTheParameterTable(t *testing.T) {
	tables := params.Tables()
	newest := tables[len(tables)-1]
	t.Run("Interval is PROC_SCAN_S of the newest table", func(t *testing.T) {
		if want := time.Duration(newest.Int(params.PROC_SCAN_S)) * time.Second; Interval != want {
			t.Errorf("Interval = %s, but table %s has PROC_SCAN_S = %s", Interval, newest.Version(), want)
		}
	})
}

func TestScanError(t *testing.T) {
	f := &fake{err: errors.New("no /proc")}
	var scanned []error
	s := New(Options{Read: f.read, OS: platform.Linux, Self: me, Boot: boot, Scanned: func(err error) { scanned = append(scanned, err) }})
	t.Run("a read error comes back from Scan", func(t *testing.T) {
		if _, err := s.Scan(); err == nil {
			t.Error("Scan returned no error")
		}
	})
	t.Run("a scheduled scan hands its error to Scanned, then nil once it works", func(t *testing.T) {
		s.scan()
		f.err = nil
		s.scan()
		if len(scanned) != 2 || scanned[0] == nil || scanned[1] != nil {
			t.Errorf("Scanned got %v", scanned)
		}
	})
	t.Run("a scanner that can't tell whose processes are whose reports none and says so", func(t *testing.T) {
		var got []Session
		s := New(Options{Read: (&fake{procs: terminal()}).read, OS: platform.Linux, Boot: boot, Report: func(s Session) { got = append(got, s) }})
		s.self = "" // as when Windows won't say which session this is
		if _, err := s.Scan(); !errors.Is(err, errNoSelf) || len(got) != 0 {
			t.Errorf("Scan = %v, reported %v; want errNoSelf and nothing", err, pidsOf(got))
		}
	})
}

func TestStartAndKick(t *testing.T) {
	sch := sched.New()
	f := &fake{}
	s := New(Options{Read: f.read, OS: platform.Linux, Self: me, Boot: boot})
	jobs := func() map[string]sched.JobInfo {
		out := map[string]sched.JobInfo{}
		for _, j := range sch.Jobs() {
			out[j.Name] = j
		}
		return out
	}
	// The scheduler times its jobs by the real clock, so its due times are
	// checked to within a second.
	near := func(got, want time.Time) bool {
		d := got.Sub(want)
		return d > -time.Second && d < time.Second
	}
	t.Run("a kick before Start does nothing", func(t *testing.T) {
		s.Kick()
		if len(jobs()) != 0 {
			t.Errorf("jobs = %v", jobs())
		}
	})
	stop := s.Start(sch)
	t.Run("Start scans every PROC_SCAN_S", func(t *testing.T) {
		if j := jobs(); j["procs.scan"].Every != Interval {
			t.Errorf("jobs = %v, want procs.scan every %s", j, Interval)
		}
		if _, armed := jobs()["procs.kick"]; armed {
			t.Error("the kick job is armed before any kick")
		}
	})
	t.Run("a kick arms one scan KickDelay from now, and another kick moves it", func(t *testing.T) {
		s.Kick()
		first, armed := jobs()["procs.kick"]
		if !armed || !near(first.Next, time.Now().Add(KickDelay)) {
			t.Fatalf("procs.kick = %+v, armed %v; want it due about %s from now", first, armed, KickDelay)
		}
		time.Sleep(20 * time.Millisecond)
		s.Kick()
		if second := jobs()["procs.kick"]; !second.Next.After(first.Next) || !near(second.Next, time.Now().Add(KickDelay)) {
			t.Errorf("after a second kick procs.kick is due %s, want about %s from now and later than %s", second.Next, KickDelay, first.Next)
		}
	})
	t.Run("a kick soon after a kicked scan waits for KickGap", func(t *testing.T) {
		ran := time.Now().Add(-time.Second)
		s.mu.Lock()
		s.lastKick = ran // as the kick job sets it when it runs
		s.mu.Unlock()
		s.Kick()
		if j := jobs()["procs.kick"]; !near(j.Next, ran.Add(KickGap)) {
			t.Errorf("procs.kick is due %s, want about %s, KickGap after the last kicked scan", j.Next, ran.Add(KickGap))
		}
	})
	t.Run("a kick long after a kicked scan waits only KickDelay", func(t *testing.T) {
		s.mu.Lock()
		s.lastKick = time.Now().Add(-time.Minute)
		s.mu.Unlock()
		s.Kick()
		if j := jobs()["procs.kick"]; !near(j.Next, time.Now().Add(KickDelay)) {
			t.Errorf("procs.kick is due %s, want about %s from now", j.Next, KickDelay)
		}
	})
	t.Run("a kick after the wall clock was set back an hour waits no longer than KickGap", func(t *testing.T) {
		s.mu.Lock()
		s.lastKick = time.Now().Add(time.Hour) // the last kicked scan's time, by the clock before it went back
		s.mu.Unlock()
		s.Kick()
		if j := jobs()["procs.kick"]; !near(j.Next, time.Now().Add(KickGap)) {
			t.Errorf("procs.kick is due %s, want about %s from now", j.Next, KickGap)
		}
	})
	t.Run("stopping removes both jobs", func(t *testing.T) {
		stop()
		if len(jobs()) != 0 {
			t.Errorf("jobs = %v after stop", jobs())
		}
		s.Kick() // must not re-arm a stopped scanner
		if len(jobs()) != 0 {
			t.Errorf("jobs = %v after a kick on a stopped scanner", jobs())
		}
	})
}

// TestGoldenBare runs the scan over the --bare tables of SM 6.6, where no
// hook fires, and checks that it reports the session as the classifier's
// golden test says.
func TestGoldenBare(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "platform", "testdata", "procs", "sm66-bare.json"))
	if err != nil {
		t.Fatal(err)
	}
	var g struct {
		Bare   bool `json:"bare"`
		Tables []struct {
			OS         string `json:"os"`
			HarnessPID int    `json:"harness_pid"`
			Want       struct {
				ParentClass event.ParentClass `json:"parent_class"`
				Entrypoint  event.Entrypoint  `json:"entrypoint"`
				HasTTY      bool              `json:"has_tty"`
			} `json:"want"`
			Procs []platform.Proc `json:"procs"`
		} `json:"tables"`
	}
	if err := json.Unmarshal(data, &g); err != nil {
		t.Fatal(err)
	}
	if !g.Bare || len(g.Tables) == 0 {
		t.Fatal("sm66-bare.json isn't marked bare or has no tables")
	}
	// Casey's own owner in the tables: session 1 on the Windows laptop,
	// uid 501 on the Mac.
	casey := map[string]string{platform.Windows: "session:1", platform.Darwin: "uid:501"}
	for _, tab := range g.Tables {
		t.Run("the --bare loop on "+tab.OS+" is reported with its classification", func(t *testing.T) {
			now := time.Date(2026, 11, 19, 22, 0, 20, 0, time.UTC)
			var got []Session
			s := New(Options{
				Read:   func() ([]platform.Proc, error) { return tab.Procs, nil },
				OS:     tab.OS,
				Self:   casey[tab.OS],
				Boot:   boot,
				Now:    func() time.Time { return now },
				Report: func(s Session) { got = append(got, s) },
			})
			if _, err := s.Scan(); err != nil {
				t.Fatal(err)
			}
			if len(got) != 1 || got[0].PID != tab.HarnessPID {
				t.Fatalf("reported %v, want pid %d alone", pidsOf(got), tab.HarnessPID)
			}
			e := got[0].Event
			if e.ParentClass != tab.Want.ParentClass || e.Entrypoint != tab.Want.Entrypoint || e.HasTTY != tab.Want.HasTTY {
				t.Errorf("session.start = %+v, want %+v", e, tab.Want)
			}
		})
	}
}

// oneClaude is terminal() with one claude session and codex.
func oneClaude() []platform.Proc {
	return slices.DeleteFunc(terminal(), func(p platform.Proc) bool { return p.PID == 120 || p.PID == 121 })
}

// TestTie checks how a session a log reader reads live is tied to the
// process running it (WP-AD3): only when one process can be it, which a
// session's first line alone never shows, since a process that started
// and ended between two reads is in none. A process tied to one session
// never passes to another, and one that a read found, tied or not, and is
// gone now could have been the session's.
func TestTie(t *testing.T) {
	claude := event.HarnessClaudeCode
	keyOf := func(s *Scanner, pid int) string {
		for id, k := range s.Sessions() {
			if id.PID == pid {
				return k
			}
		}
		return ""
	}
	// young is oneClaude's claude started 3 s ago, younger than Grace.
	young := func() []platform.Proc {
		ps := oneClaude()
		for i := range ps {
			if ps[i].PID == 111 {
				ps[i].Start = sec(-3)
			}
		}
		return ps
	}
	// step is one thing done to the scanner: a clock to read, a table to
	// read from now on, a scan, a claim, a touch, or a line of a session
	// that tieLive hands Tie, touched first, in that order of its fields'
	// use.
	type step struct {
		now   time.Time
		table []platform.Proc
		scan  bool
		claim int
		touch string
		key   string
		at    time.Time
	}
	without := func(ps []platform.Proc, pid int) []platform.Proc {
		return slices.DeleteFunc(slices.Clone(ps), func(p platform.Proc) bool { return p.PID == pid })
	}
	// tied is a session's line at at, read at sec(0), whose try reads the
	// table, and its next, written at sec(1) after that read and read at
	// sec(2), whose try weighs it.
	tied := func(key string, at time.Time) []step {
		return []step{{now: sec(0), key: key, at: at}, {now: sec(2), key: key, at: sec(1)}}
	}
	// later is a session's two lines, at at and two seconds later, read a
	// second after each.
	later := func(key string, at time.Time) []step {
		return []step{{now: at.Add(time.Second), key: key, at: at}, {now: at.Add(3 * time.Second), key: key, at: at.Add(2 * time.Second)}}
	}
	with := func(table []platform.Proc, steps ...[]step) []step {
		out := []step{{table: table}}
		for _, st := range steps {
			out = append(out, st...)
		}
		return out
	}
	cases := []struct {
		name  string
		steps []step
		// want is the pid the last tie gave, 0 for none.
		want int
	}{
		{"a line written after the read the session's first line made ties the only claude running, and Sessions gives the session's key for it",
			with(oneClaude(), tied("s_one", sec(-100))), 111},
		{"a first line alone ties nothing: a claude that started and ended between two reads could have been the session's",
			with(oneClaude(), []step{{now: sec(0), key: "s_one", at: sec(-100)}}), 0},
		{"a line written before that read ties nothing", with(oneClaude(), []step{{now: sec(0), key: "s_one", at: sec(-100)},
			{now: sec(2), key: "s_one", at: sec(-50)}}), 0},
		{"a scan's read that began after the session's first line serves for that read",
			with(oneClaude(), []step{{now: sec(-90), touch: "s_one", at: sec(-100)}, {now: sec(-80), scan: true},
				{now: sec(0), key: "s_one", at: sec(-50)}}), 111},
		{"a claude younger than Grace is tied too", with(young(), tied("s_one", sec(-1))), 111},
		{"a claude that started after the session's lines isn't tied", with(oneClaude(), later("s_one", sec(-400))), 0},
		{"a claude that started after the session's first line isn't tied, though it started before the line that weighs it",
			with(oneClaude(), []step{{now: sec(-319), key: "s_one", at: sec(-320)}, {now: sec(-289), key: "s_one", at: sec(-290)}}), 0},
		{"a claude that started within TieSlack after the session's first line is tied", with(oneClaude(), []step{
			{now: sec(-299), key: "s_one", at: sec(-301)}, {now: sec(-297), key: "s_one", at: sec(-298)}}), 111},
		{"with two claudes that could be it, none is tied", with(terminal(), tied("s_one", sec(-100))), 0},
		{"a claude a hook claimed isn't tied", with(oneClaude(), []step{{claim: 111}}, tied("s_one", sec(-100))), 0},
		{"a codex isn't tied to a Claude Code session", with(without(oneClaude(), 111), tied("s_one", sec(-50))), 0},
		{"a session tied already keeps its process",
			with(oneClaude(), tied("s_one", sec(-100)), []step{{now: sec(4), key: "s_one", at: sec(3)}}), 111},
		{"a claude tied to a session that still writes isn't tied to another",
			with(oneClaude(), tied("s_one", sec(-100)), []step{{now: sec(21), key: "s_two", at: sec(20)},
				{now: sec(31), touch: "s_one", at: sec(30)}, {now: sec(41), key: "s_two", at: sec(40)}}), 0},
		{"a claude tied to a session that went quiet long ago isn't tied to a new one, as /clear or /resume would start there",
			with(oneClaude(), tied("s_one", sec(-100)), later("s_two", sec(400))), 0},
		{"with a claude a read found while the session wrote and gone now, nothing is tied: it could have been the session's",
			[]step{{now: sec(-10), table: terminal(), scan: true}, {now: sec(0), table: without(terminal(), 111), key: "s_one", at: sec(-5)},
				{now: sec(2), key: "s_one", at: sec(1)}}, 0},
		{"a claude tied to another session, which a read found and which is gone now, could have been the session's, so the only untied claude isn't tied",
			with(oneClaude(), tied("s_a", sec(-100)), []step{{table: terminal()}, {now: sec(21), key: "s_b", at: sec(20)},
				{table: without(terminal(), 111)}, {now: sec(23), key: "s_b", at: sec(22)}}), 0},
		{"a claude a read found gone before the session's first line doesn't count",
			[]step{{now: sec(-100), table: terminal(), scan: true}, {now: sec(-50), table: without(terminal(), 111), scan: true},
				{now: sec(0), key: "s_one", at: sec(-5)}, {now: sec(2), key: "s_one", at: sec(1)}}, 121},
		{"a claude last seen before the session's first line, and found gone only since, still could have been the session's",
			[]step{{now: sec(-50), table: terminal(), scan: true}, {now: sec(0), table: without(terminal(), 111), key: "s_one", at: sec(-5)},
				{now: sec(2), key: "s_one", at: sec(1)}}, 0},
		{"a session whose claude ended takes no other claude: its own could have written its late lines",
			with(oneClaude(), tied("s_one", sec(-100)), []step{{now: sec(10), table: without(terminal(), 111), scan: true},
				{now: sec(21), key: "s_one", at: sec(20)}}), 0},
		{"nor once the scan has forgotten that claude, and the ended tie with it",
			with(oneClaude(), tied("s_one", sec(-100)), []step{{now: sec(10), table: without(terminal(), 111), scan: true},
				{now: sec(200), scan: true}, {now: sec(701), key: "s_one", at: sec(700)}}), 0},
		{"a claude tied to a session that went quiet a minute before a new session's first line isn't tied to it, nor at its try a retry later, since a claude UntieQuiet frees ties to no one",
			with(oneClaude(), tied("s_one", sec(-100)), later("s_two", sec(60)), []step{{now: sec(101), key: "s_two", at: sec(100)}}), 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := &fake{}
			now := sec(0)
			var got []Session
			s := newScanner(f, &now, &got)
			var sess Session
			var ok bool
			var last string
			for _, st := range tc.steps {
				if !st.now.IsZero() {
					now = st.now
				}
				if st.table != nil {
					f.procs = st.table
				}
				if st.scan {
					if _, err := s.Scan(); err != nil {
						t.Fatal(err)
					}
				}
				if st.claim != 0 {
					p, _ := platform.NewTable(platform.Linux, f.procs).Get(st.claim)
					s.Claim(boot, p)
				}
				if st.touch != "" {
					s.Touch(st.touch, st.at, "")
				}
				if st.key != "" {
					s.Touch(st.key, st.at, "")
					sess, ok, _ = s.Tie(claude, st.key, st.at)
					last = st.key
				}
			}
			switch {
			case tc.want == 0 && ok:
				t.Errorf("tied process %d, want none", sess.PID)
			case tc.want != 0 && (!ok || sess.PID != tc.want || !sess.Tied || sess.Key != last):
				t.Errorf("tie %+v, %v; want process %d", sess, ok, tc.want)
			case tc.want != 0 && keyOf(s, tc.want) != last:
				t.Errorf("Sessions gives %q for process %d, want the session's key", keyOf(s, tc.want), tc.want)
			}
		})
	}
}

// tieTwice ties session key the way tieLive does: its line at at, read a
// second later, whose try reads the table, and its next, written at then,
// after that read, and read a second after it. now is the scanner's clock,
// which it leaves there.
func tieTwice(t *testing.T, s *Scanner, now *time.Time, key string, at, then time.Time) Session {
	t.Helper()
	*now = at.Add(time.Second)
	s.Touch(key, at, "")
	s.Tie(event.HarnessClaudeCode, key, at)
	*now = then.Add(time.Second)
	s.Touch(key, then, "")
	sess, ok, _ := s.Tie(event.HarnessClaudeCode, key, then)
	if !ok {
		t.Fatalf("no tie of %s", key)
	}
	return sess
}

// TestUntieQuiet checks that a tie ends where /clear or /resume may have
// ended its session (UntieQuiet): a session that Tie finds no untied claude
// for, or whose log opened with /clear, ends the ties of the sessions that
// wrote no line read live after its first, in its folder where both are
// known, and passes them to nothing.
func TestUntieQuiet(t *testing.T) {
	claude := event.HarnessClaudeCode
	cases := []struct {
		name string
		// run ties s_one to 111, in folder f, and has s_two, in folder
		// folder, find no untied claude.
		folder string
		// after is when s_one writes again, 0 for not; cleared says s_two's
		// log opened with /clear, so UntieQuiet comes from its line, not a
		// try; forget drops s_one's touch, as a restart does.
		after   time.Time
		cleared bool
		forget  bool
		// kept says s_one keeps its tie.
		kept bool
	}{
		{"a session no untied claude can be ends the tie of one that went quiet before its first line, in its folder", "f", time.Time{}, false, false, false},
		{"a session whose log opened with /clear does too, on its line", "f", time.Time{}, true, false, false},
		{"a tie whose session wrote after the new one's first line stays", "f", sec(61), false, false, true},
		{"a tie in another folder stays", "g", time.Time{}, false, false, true},
		{"a session whose folder the scan doesn't know ends it", "", time.Time{}, false, false, false},
		{"a tie whose session's touch the scan forgot, as after a restart, ends", "f", time.Time{}, false, true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := &fake{procs: oneClaude()}
			now := sec(0)
			var got []Session
			s := newScanner(f, &now, &got)
			now = sec(-99)
			s.Touch("s_one", sec(-100), "f")
			s.Tie(claude, "s_one", sec(-100))
			now = sec(-97)
			s.Touch("s_one", sec(-98), "f")
			if _, ok, _ := s.Tie(claude, "s_one", sec(-98)); !ok {
				t.Fatal("no tie of s_one")
			}
			if tc.forget {
				s.mu.Lock()
				delete(s.touched, "s_one")
				s.mu.Unlock()
			}
			now = sec(61)
			s.Touch("s_two", sec(60), tc.folder)
			if !tc.after.IsZero() {
				s.Touch("s_one", tc.after, "f")
			}
			if tc.cleared {
				s.UntieQuiet(claude, "s_two")
			} else {
				s.Tie(claude, "s_two", sec(60))
				now = sec(63)
				s.Touch("s_two", sec(62), tc.folder)
				if _, ok, _ := s.Tie(claude, "s_two", sec(62)); ok {
					t.Fatal("s_two was tied")
				}
			}
			_, kept := s.TiedTo("s_one")
			_, two := s.TiedTo("s_two")
			if kept != tc.kept || two {
				t.Errorf("s_one tied %v, s_two tied %v; want s_one tied %v, and s_two not", kept, two, tc.kept)
			}
			if tc.kept {
				return
			}
			// The claude it freed ties to no one: not s_two at a try a retry
			// later, nor s_one, which it lost, from a claude started since.
			now = sec(101)
			s.Touch("s_two", sec(100), tc.folder)
			s.Tie(claude, "s_two", sec(100))
			now = sec(103)
			s.Touch("s_two", sec(102), tc.folder)
			if _, ok, _ := s.Tie(claude, "s_two", sec(102)); ok {
				t.Error("s_two was tied at a later try")
			}
			f.procs = append(oneClaude(), platform.Proc{PID: 141, PPID: 110, Name: "claude", PGID: 141, TTY: platform.TTYAttached,
				Start: sec(90), Owner: me})
			now = sec(121)
			s.Touch("s_one", sec(120), "f")
			s.Tie(claude, "s_one", sec(120))
			now = sec(123)
			s.Touch("s_one", sec(122), "f")
			if _, ok, _ := s.Tie(claude, "s_one", sec(122)); ok {
				t.Error("s_one, whose tie UntieQuiet ended, was tied again")
			}
		})
	}
}

// TestUntieQuietOnReturn checks that a session Tie turns away as lost, as
// one whose process ended, ends the quiet ties of the process /resume may
// have brought it back to, judged from where its lines began coming again
// after a gap of more than ResumeGap, not from its first line of the run.
func TestUntieQuietOnReturn(t *testing.T) {
	claude := event.HarnessClaudeCode
	cases := []struct {
		name string
		// back is when the lost session's lines come again, and since the
		// last line s_one, tied, wrote. kept says s_one keeps its tie.
		back, since time.Time
		kept        bool
	}{
		{"a lost session's lines coming again after a gap end a tie quiet since then, in its folder", sec(60), sec(-98), false},
		{"a tie whose session wrote after they came again stays", sec(60), sec(61), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := &fake{procs: oneClaude()}
			now := sec(-499)
			var got []Session
			s := newScanner(f, &now, &got)
			// s_r wrote long ago, and the scan lost it, as when its claude ended.
			s.Touch("s_r", sec(-500), "f")
			s.mu.Lock()
			s.loseLocked("s_r", sec(-400))
			s.mu.Unlock()
			now = sec(-99)
			s.Touch("s_one", sec(-100), "f")
			s.Tie(claude, "s_one", sec(-100))
			now = sec(-97)
			s.Touch("s_one", sec(-98), "f")
			if _, ok, _ := s.Tie(claude, "s_one", sec(-98)); !ok {
				t.Fatal("no tie of s_one")
			}
			now = tc.back.Add(time.Second)
			s.Touch("s_r", tc.back, "f")
			if tc.since.After(sec(-98)) {
				s.Touch("s_one", tc.since, "f")
			}
			if _, ok, _ := s.Tie(claude, "s_r", tc.back); ok {
				t.Fatal("the lost s_r was tied")
			}
			if _, kept := s.TiedTo("s_one"); kept != tc.kept {
				t.Errorf("s_one tied %v, want %v", kept, tc.kept)
			}
		})
	}
}

// TestFreedAcrossRestarts checks that a claude UntieQuiet freed ties to no
// one after an agent restart either, as the scan's state keeps it, until a
// scan sees it gone.
func TestFreedAcrossRestarts(t *testing.T) {
	claude := event.HarnessClaudeCode
	name := filepath.Join(t.TempDir(), "state.json")
	f := &fake{procs: oneClaude()}
	now := sec(0)
	start := func(t *testing.T) *Scanner {
		t.Helper()
		st, problem, err := state.Open(name)
		if err != nil || problem != "" {
			t.Fatalf("state.Open: %v %s", err, problem)
		}
		return New(Options{Read: f.read, OS: platform.Linux, Self: me, Boot: boot, Store: st, Now: func() time.Time { return now }})
	}
	s := start(t)
	tieTwice(t, s, &now, "s_one", sec(1), sec(3))
	now = sec(61)
	s.Touch("s_two", sec(60), "")
	if !s.UntieQuiet(claude, "s_two") {
		t.Fatal("nothing was untied")
	}
	s = start(t)
	now = sec(200)
	s.Touch("s_three", sec(199), "")
	s.Tie(claude, "s_three", sec(199))
	now = sec(202)
	s.Touch("s_three", sec(201), "")
	_, tied, _ := s.Tie(claude, "s_three", sec(201))
	// The claude ends, and a scan sees it gone: the next one to start can
	// be tied.
	f.procs = slices.DeleteFunc(oneClaude(), func(p platform.Proc) bool { return p.PID == 111 })
	if _, err := s.Scan(); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	kept := len(s.freed)
	s.mu.Unlock()
	cases := []struct {
		name string
		ok   bool
	}{
		{"the restarted scan ties no session to the claude UntieQuiet freed", !tied},
		{"a scan that sees the claude gone forgets it", kept == 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if !tc.ok {
				t.Errorf("tied %v, %d freed claudes kept", tied, kept)
			}
		})
	}
}

// TestLostAcrossRestarts checks that a session whose tied claude ended is
// tied no more after an agent restart either, as the scan's state keeps it
// (Scanner.lost), though the restarted scan has forgotten the claude.
func TestLostAcrossRestarts(t *testing.T) {
	name := filepath.Join(t.TempDir(), "state.json")
	f := &fake{procs: oneClaude()}
	now := sec(0)
	start := func(t *testing.T) *Scanner {
		t.Helper()
		st, problem, err := state.Open(name)
		if err != nil || problem != "" {
			t.Fatalf("state.Open: %v %s", err, problem)
		}
		return New(Options{Read: f.read, OS: platform.Linux, Self: me, Boot: boot, Store: st, Now: func() time.Time { return now }})
	}
	s := start(t)
	tieTwice(t, s, &now, "s_one", sec(1), sec(3))
	// The claude ends, and another, untied, runs.
	f.procs = slices.DeleteFunc(terminal(), func(p platform.Proc) bool { return p.PID == 111 })
	now = sec(10)
	if _, err := s.Scan(); err != nil {
		t.Fatal(err)
	}
	s = start(t)
	cases := []struct {
		name string
		key  string
		want bool
	}{
		{"the session whose claude ended takes no other after the restart", "s_one", false},
		{"a new session takes the one untied claude", "s_two", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			now = sec(1000)
			s.Touch(tc.key, sec(999), "")
			s.Tie(event.HarnessClaudeCode, tc.key, sec(999))
			now = sec(1002)
			s.Touch(tc.key, sec(1001), "")
			if _, ok, _ := s.Tie(event.HarnessClaudeCode, tc.key, sec(1001)); ok != tc.want {
				t.Errorf("tied %v, want %v", ok, tc.want)
			}
		})
	}
}

// TestTieAcrossRestarts checks that a tie outlives an agent restart: the
// restarted scan gives the session's key for the process. A tied claude it
// loaded, which its first read finds gone, could have been the session a
// line names after the restart, so the only untied claude isn't tied to
// that session.
func TestTieAcrossRestarts(t *testing.T) {
	name := filepath.Join(t.TempDir(), "state.json")
	f := &fake{procs: oneClaude()}
	now := sec(0)
	start := func(t *testing.T) *Scanner {
		t.Helper()
		st, problem, err := state.Open(name)
		if err != nil || problem != "" {
			t.Fatalf("state.Open: %v %s", err, problem)
		}
		return New(Options{Read: f.read, OS: platform.Linux, Self: me, Boot: boot, Store: st, Now: func() time.Time { return now }})
	}
	s := start(t)
	if _, err := s.Scan(); err != nil {
		t.Fatal(err)
	}
	tieTwice(t, s, &now, "s_one", sec(1), sec(3))
	s = start(t)
	var restartedKey string
	for id, k := range s.Sessions() {
		if id.PID == 111 {
			restartedKey = k
		}
	}
	// The claude ends while the agent is down, and another, untied, runs.
	f.procs = slices.DeleteFunc(terminal(), func(p platform.Proc) bool { return p.PID == 111 })
	now = sec(21)
	s.Touch("s_two", sec(20), "")
	s.Tie(event.HarnessClaudeCode, "s_two", sec(20))
	now = sec(23)
	s.Touch("s_two", sec(22), "")
	_, twoTied, _ := s.Tie(event.HarnessClaudeCode, "s_two", sec(22))
	cases := []struct {
		name string
		ok   bool
	}{
		{"the restarted scan gives the session's key for the process", restartedKey == "s_one"},
		{"a tied claude the restarted scan loaded and found gone could have been a new session's, so the only untied claude isn't tied", !twoTied},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if !tc.ok {
				t.Errorf("after the restart: key %q, the new session tied %v", restartedKey, twoTied)
			}
		})
	}
}

// TestTieOf checks that a tie outlives its process for EndedKeep, for the
// session.start a short session writes after its process ended, while
// TiedTo, which focus's sessions follow, lets it go at once.
func TestTieOf(t *testing.T) {
	f := &fake{procs: oneClaude()}
	now := sec(0)
	var got []Session
	s := newScanner(f, &now, &got)
	tieTwice(t, s, &now, "s_one", sec(-100), sec(-97))
	now = sec(0)
	f.procs = slices.DeleteFunc(oneClaude(), func(p platform.Proc) bool { return p.PID == 111 })
	if _, err := s.Scan(); err != nil {
		t.Fatal(err)
	}
	_, tied := s.TiedTo("s_one")
	_, kept := s.TieOf("s_one")
	now = sec(0).Add(EndedKeep + time.Second)
	_, later := s.TieOf("s_one")
	cases := []struct {
		name string
		ok   bool
	}{
		{"TiedTo lets a tie go once a scan saw its process gone", !tied},
		{"TieOf keeps it for EndedKeep", kept},
		{"and not longer", !later},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if !tc.ok {
				t.Errorf("TiedTo %v, TieOf %v, then %v", tied, kept, later)
			}
		})
	}
}

// TestScanKeepsATieMadeDuringItsRead checks a tie Tie makes while a scan
// reads the table, of a process that started as the table was read and
// that its table lacks: the scan, whose read began before the tie, leaves
// it alone, as it leaves a claim that came during its read.
func TestScanKeepsATieMadeDuringItsRead(t *testing.T) {
	now := sec(0)
	table := oneClaude()
	var s *Scanner
	during := false
	read := func() ([]platform.Proc, error) {
		if !during {
			return slices.Clone(table), nil
		}
		during = false
		// The tie reads a table of its own, which has the claude.
		now = sec(3)
		s.Touch("s_one", sec(2), "")
		if _, ok, _ := s.Tie(event.HarnessClaudeCode, "s_one", sec(2)); !ok {
			t.Error("no tie during the scan's read")
		}
		return slices.DeleteFunc(slices.Clone(table), func(p platform.Proc) bool { return p.PID == 111 }), nil
	}
	s = New(Options{Read: read, OS: platform.Linux, Self: me, Boot: boot, Now: func() time.Time { return now }})
	// The session's first try reads the table.
	now = sec(1)
	s.Touch("s_one", sec(0), "")
	s.Tie(event.HarnessClaudeCode, "s_one", sec(0))
	during = true
	if _, err := s.Scan(); err != nil {
		t.Fatal(err)
	}
	if _, tied := s.TiedTo("s_one"); !tied {
		t.Error("the scan let go of a tie made after its read began")
	}
}

// TestUntie checks the undoing of a tie for a session whose lines now run
// in a folder out of pairtime (DESIGN 8.8): Sessions gives the process the
// key Report gave it, or, for a process no scan reported, no key until the
// next scan reports it, and TieOf gives the session no tie.
func TestUntie(t *testing.T) {
	cases := []struct {
		name string
		// scanned says a scan reported the claude before the tie.
		scanned bool
	}{
		{"a process a scan reported goes back to the key Report gave it", true},
		{"a process no scan reported goes to the next scan's report", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := &fake{procs: oneClaude()}
			now := sec(0)
			var got []Session
			s := newScanner(f, &now, &got)
			if tc.scanned {
				if _, err := s.Scan(); err != nil {
					t.Fatal(err)
				}
			}
			tieTwice(t, s, &now, "s_one", sec(1), sec(3))
			gone := s.Untie("s_one")
			keys := s.Sessions()
			_, tied := s.TiedTo("s_one")
			_, ended := s.TieOf("s_one")
			var key string
			for id, k := range keys {
				if id.PID == 111 {
					key = k
				}
			}
			switch {
			case !gone || tied || ended:
				t.Errorf("Untie %v, then TiedTo %v and TieOf %v", gone, tied, ended)
			case tc.scanned && (len(got) == 0 || key != got[0].Key):
				t.Errorf("Sessions gives %q for the claude, want the key Report gave it, %v", key, got)
			case !tc.scanned && key != "":
				t.Errorf("Sessions gives %q for a claude no scan reported", key)
			}
		})
	}
}

// TestWithdrawAfterATie checks Options.Withdraw for a tied process a hook
// claims: Withdraw gets the session under the key Report gave it, and
// nothing for a process Report never had, as one younger than Grace.
func TestWithdrawAfterATie(t *testing.T) {
	cases := []struct {
		name    string
		scanned bool
	}{
		{"a tied process a scan reported is withdrawn under the key Report gave it", true},
		{"a tied process no scan reported is withdrawn not at all", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := &fake{procs: oneClaude()}
			now := sec(0)
			var reported, withdrawn []Session
			s := New(Options{Read: f.read, OS: platform.Linux, Self: me, Boot: boot, Now: func() time.Time { return now },
				Report: func(sess Session) { reported = append(reported, sess) }, Withdraw: func(sess Session) { withdrawn = append(withdrawn, sess) }})
			if tc.scanned {
				if _, err := s.Scan(); err != nil {
					t.Fatal(err)
				}
			}
			tieTwice(t, s, &now, "s_one", sec(1), sec(3))
			p, _ := platform.NewTable(platform.Linux, f.procs).Get(111)
			s.Claim(boot, p)
			var want []string
			for _, r := range reported {
				if r.PID == 111 {
					want = append(want, r.Key)
				}
			}
			var gotKeys []string
			for _, w := range withdrawn {
				gotKeys = append(gotKeys, w.Key)
			}
			if !slices.Equal(gotKeys, want) {
				t.Errorf("Withdraw got %v, want %v, the keys Report gave", gotKeys, want)
			}
		})
	}
}
