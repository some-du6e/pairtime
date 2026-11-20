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
	"github.com/some-du6e/pairtime/internal/platform"
	"github.com/some-du6e/pairtime/pkg/event"
	"github.com/some-du6e/pairtime/pkg/params"
)

var t0 = time.Date(2026, 11, 19, 22, 0, 0, 0, time.UTC)

func sec(s int) time.Time { return t0.Add(time.Duration(s) * time.Second) }

// terminal is a Linux desktop with two claude sessions in a terminal and
// codex started through its npm launcher.
func terminal() []platform.Proc {
	return []platform.Proc{
		{PID: 1, Name: "systemd", PGID: 1, TTY: platform.TTYNone, Start: sec(-3600)},
		{PID: 100, PPID: 1, Name: "gnome-terminal-server", PGID: 100, TTY: platform.TTYNone, Start: sec(-600)},
		{PID: 110, PPID: 100, Name: "bash", PGID: 110, TTY: platform.TTYAttached, Start: sec(-590)},
		{PID: 111, PPID: 110, Name: "claude", PGID: 111, TTY: platform.TTYAttached, Start: sec(-300)},
		{PID: 120, PPID: 100, Name: "bash", PGID: 120, TTY: platform.TTYAttached, Start: sec(-580)},
		{PID: 121, PPID: 120, Name: "claude", PGID: 121, TTY: platform.TTYAttached, Start: sec(-200)},
		{PID: 130, PPID: 100, Name: "bash", PGID: 130, TTY: platform.TTYAttached, Start: sec(-570)},
		{PID: 131, PPID: 130, Name: "node", Alias: "codex", PGID: 131, TTY: platform.TTYAttached, Start: sec(-100)},
		{PID: 132, PPID: 131, Name: "codex", PGID: 131, TTY: platform.TTYAttached, Start: sec(-100)},
	}
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
		{"a harness of unknown age is reported", []platform.Proc{{PID: 5, Name: "claude"}}, nil, sec(0), []int{5}},
		{"no harness, no report", []platform.Proc{{PID: 5, Name: "bash"}, {PID: 6, PPID: 5, Name: "vim"}}, nil, sec(0), []int{}},
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
				s.Claim(p.PID, p.Start)
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

func TestScanSession(t *testing.T) {
	f := &fake{procs: terminal()}
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
	var got []Session
	s := newScanner(f, &now, &got)
	scan := func() []int {
		t.Helper()
		found, err := s.Scan()
		if err != nil {
			t.Fatal(err)
		}
		return pidsOf(found)
	}
	// terminal()'s harnesses start at -300 s (111), -200 s (121) and
	// -100 s (132).
	steps := []struct {
		name   string
		change func()
		want   []int
	}{
		{"the first scan reports the harness past the grace", func() { now = sec(-195) }, []int{111}},
		{"a later scan reports the ones that have grown past it", func() { now = sec(-80) }, []int{121, 132}},
		{"a scan with nothing new reports nothing", func() { now = sec(-20) }, []int{}},
		{"a claim after a report doesn't report again", func() { s.Claim(111, sec(-300)) }, []int{}},
		{"a pid reused by a new harness is a new session", func() {
			now = sec(40)
			f.procs = slices.DeleteFunc(f.procs, func(p platform.Proc) bool { return p.PID == 121 })
			f.procs = append(f.procs, platform.Proc{PID: 121, PPID: 120, Name: "claude", PGID: 122, TTY: platform.TTYAttached, Start: sec(-20)})
		}, []int{121}},
		{"an exited process is forgotten", func() {
			now = sec(100)
			f.procs = slices.DeleteFunc(f.procs, func(p platform.Proc) bool { return p.PID == 111 })
		}, []int{}},
	}
	for _, st := range steps {
		t.Run(st.name, func(t *testing.T) {
			st.change()
			if got := scan(); !slices.Equal(got, st.want) {
				t.Errorf("reported %v, want %v", got, st.want)
			}
		})
	}
	t.Run("the scanner's memory is as small as the live harness processes", func(t *testing.T) {
		s.mu.Lock()
		defer s.mu.Unlock()
		// The new 121 and 132 are left; 111 and the old 121 have exited.
		if len(s.reported) != 2 || len(s.claimed) != 0 {
			t.Errorf("remembers %d reported and %d claimed processes, want 2 and 0", len(s.reported), len(s.claimed))
		}
	})
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
	s := New(Options{Read: f.read, OS: platform.Linux, Scanned: func(err error) { scanned = append(scanned, err) }})
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
}

func TestStartAndKick(t *testing.T) {
	sch := sched.New()
	f := &fake{}
	s := New(Options{Read: f.read, OS: platform.Linux})
	jobs := func() map[string]time.Duration {
		out := map[string]time.Duration{}
		for _, j := range sch.Jobs() {
			out[j.Name] = j.Every
		}
		return out
	}
	t.Run("a kick before Start does nothing", func(t *testing.T) {
		s.Kick()
		if len(jobs()) != 0 {
			t.Errorf("jobs = %v", jobs())
		}
	})
	stop := s.Start(sch)
	t.Run("Start scans every PROC_SCAN_S", func(t *testing.T) {
		if j := jobs(); j["procs.scan"] != Interval {
			t.Errorf("jobs = %v, want procs.scan every %s", j, Interval)
		}
		if _, armed := jobs()["procs.kick"]; armed {
			t.Error("the kick job is armed before any kick")
		}
	})
	t.Run("a kick arms one scan soon", func(t *testing.T) {
		s.Kick()
		s.Kick()
		if _, armed := jobs()["procs.kick"]; !armed {
			t.Errorf("jobs = %v, want procs.kick armed", jobs())
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
	for _, tab := range g.Tables {
		t.Run("the --bare loop on "+tab.OS+" is reported with its classification", func(t *testing.T) {
			now := time.Date(2026, 11, 19, 22, 0, 20, 0, time.UTC)
			var got []Session
			s := New(Options{
				Read:   func() ([]platform.Proc, error) { return tab.Procs, nil },
				OS:     tab.OS,
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
