// SPDX-License-Identifier: AGPL-3.0-or-later

package procs

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"slices"
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
		{"a claim after a report withdraws the session, and the scan doesn't report it again", func() { s.Claim(boot, proc(111)) }, []int{}, []int{111}},
		{"a second claim withdraws nothing more", func() { s.Claim(boot, proc(111)) }, []int{}, []int{111}},
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
	// Casey's own owner in the tables: logon session 1 on the Windows
	// laptop, uid 501 on the Mac.
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
