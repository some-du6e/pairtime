// SPDX-License-Identifier: AGPL-3.0-or-later

package focus

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/platform"
	"github.com/some-du6e/pairtime/internal/platform/darwin"
	"github.com/some-du6e/pairtime/pkg/event"
)

// macITerm is a Mac with iTerm2 in front. One of its sessions runs under
// the iTermServer it started, and another under an iTermServer that an
// earlier iTerm2 started and launchd adopted when that iTerm2 quit. Another
// user of the Mac has an iTermServer of their own, and Terminal runs a
// session too. login runs as root.
func macITerm() []platform.Proc {
	as := func(owner string, p platform.Proc) platform.Proc {
		p.Owner = owner
		return p
	}
	return []platform.Proc{
		as("uid:0", macProc(1, 0, "launchd", -600)),
		macProc(300, 1, "iTerm2", -20),
		macProc(310, 300, "iTermServer-3.5.14", -19),
		as("uid:0", macProc(311, 310, "login", -19)),
		macProc(312, 311, "zsh", -19),
		macProc(313, 312, "claude", -18),
		macProc(200, 1, "iTermServer-3.5.14", -120),
		as("uid:0", macProc(201, 200, "login", -120)),
		macProc(202, 201, "zsh", -120),
		macProc(203, 202, "claude", -110),
		as("uid:502", macProc(250, 1, "iTermServer-3.5.14", -200)),
		as("uid:0", macProc(251, 250, "login", -200)),
		as("uid:502", macProc(252, 251, "zsh", -200)),
		as("uid:502", macProc(253, 252, "claude", -190)),
		macProc(400, 1, "Terminal", -60),
		as("uid:0", macProc(410, 400, "login", -60)),
		macProc(420, 410, "zsh", -60),
		macProc(430, 420, "claude", -30),
	}
}

func TestITerm2Helpers(t *testing.T) {
	tab := platform.NewTable(platform.Darwin, macITerm())
	sessions := keyed(tab, map[int]string{313: "s_tab", 203: "s_adopted", 253: "s_other_user", 430: "s_terminal"})
	tests := []struct {
		name   string
		table  *platform.Table
		pid    int
		hosted []string
	}{
		{"iTerm2 in front hosts the sessions under its own iTermServer and under one launchd adopted, but not under another user's (E-6.3-two-tabs)", tab, 300, []string{"s_adopted", "s_tab"}},
		{"Terminal in front hosts its own session and none of iTerm2's", tab, 400, []string{"s_terminal"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w, ok := windowOf(tt.table, tt.pid)
			if !ok {
				t.Fatalf("pid %d isn't in the table", tt.pid)
			}
			if w.class != event.AppTerminal {
				t.Errorf("class %s, want terminal", w.class)
			}
			if got := hostedSessions(tt.table, w, sessions); !slices.Equal(got, tt.hosted) {
				t.Errorf("hosted_sessions %v, want %v", got, tt.hosted)
			}
		})
	}

	t.Run("an iTerm2 whose owner the table doesn't know joins no iTermServer, since it can't tell whose they are", func(t *testing.T) {
		// The adopted iTermServer's owner is unknown too, so only the rule,
		// not a mismatch of owners, keeps it out.
		procs := macITerm()
		for i := range procs {
			if procs[i].PID == 300 || procs[i].PID == 200 {
				procs[i].Owner = ""
			}
		}
		unowned := platform.NewTable(platform.Darwin, procs)
		w, _ := windowOf(unowned, 300)
		if got := hostedSessions(unowned, w, keyed(unowned, map[int]string{313: "s_tab", 203: "s_adopted"})); !slices.Equal(got, []string{"s_tab"}) {
			t.Errorf("hosted_sessions %v, want only the session under iTerm2's own process", got)
		}
	})
	t.Run("iTerm2's helper prefix names processes appclass.json calls a macOS terminal, in the entry that lists iTerm2", func(t *testing.T) {
		prefix := helperPrefix(platform.Darwin, "iterm2")
		e, ok := platform.Lookup(platform.Darwin, prefix+"-3.7.7")
		if prefix == "" || !ok || e.AppClass != event.AppTerminal || !slices.Contains(e.Names, prefix+"*") || !slices.Contains(e.Names, "iterm2") {
			t.Errorf("iTerm2's helpers %q*: appclass.json gives %+v, %v; want the terminal entry that lists iterm2 and %s*", prefix, e, ok, prefix)
		}
	})
	t.Run("only iTerm2 on macOS has helpers", func(t *testing.T) {
		for _, c := range []struct{ goos, app string }{{platform.Darwin, "terminal"}, {platform.Linux, "iterm2"}, {platform.Windows, "iterm2"}} {
			if p := helperPrefix(c.goos, c.app); p != "" {
				t.Errorf("helperPrefix(%s, %s) = %q, want none", c.goos, c.app, p)
			}
		}
	})
}

// itermCaptureFile is the process table TestCaptureITerm2 captures on
// macos-latest, in testdata.
const itermCaptureFile = "iterm2-macos-latest.json"

// itermCapture is the file TestCaptureITerm2 writes: the process table it
// read on macos-latest, with what the test knows about it. Its stubs are
// copies of the focus test binary named claude, each in a session of its
// own: one under the running iTerm2, one under an iTermServer that doesn't
// descend from it, which TestCaptureITerm2 leaves by killing the iTerm2
// that started it, and one in Terminal.
type itermCapture struct {
	Case string `json:"case"`
	// Source says where and how the table was read.
	Source string `json:"source"`
	// Captured is when, in RFC 3339 and UTC.
	Captured string `json:"captured"`
	// MacOS and ITerm2 are the versions it was read on.
	MacOS  string `json:"macos"`
	ITerm2 string `json:"iterm2"`
	// Notes say what the test did and saw on the way, in order.
	Notes       []string `json:"notes"`
	ITerm2PID   int      `json:"iterm2_pid"`
	TerminalPID int      `json:"terminal_pid"`
	Stubs       struct {
		UnderITerm2      int `json:"under_iterm2"`
		UnderITermServer int `json:"under_itermserver"`
		InTerminal       int `json:"in_terminal"`
	} `json:"stubs"`
	Procs []platform.Proc `json:"procs"`
}

// TestITerm2Golden is decision E-6.3-two-tabs's golden test of WP-OS9: in
// a process table captured on macos-latest, with a harness stub in a
// session of iTerm2, one under an iTermServer that doesn't descend from
// iTerm2 and one in Terminal, an iTerm2 activation lists both iTerm2 stubs
// in hosted_sessions, and not Terminal's.
func TestITerm2Golden(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", itermCaptureFile))
	if err != nil {
		t.Fatalf("%v: platform.yml's iterm2-capture job writes the file (TestCaptureITerm2)", err)
	}
	var g itermCapture
	if err := json.Unmarshal(data, &g); err != nil {
		t.Fatalf("%s: %v", itermCaptureFile, err)
	}
	checkITermCapture(t, g)
}

// checkITermCapture checks the hosting rule against a captured table: the
// golden file in ci.yml, and the live table in TestCaptureITerm2.
func checkITermCapture(t *testing.T, g itermCapture) {
	t.Helper()
	tab := platform.NewTable(platform.Darwin, g.Procs)
	reaches := func(pid int, ok func(platform.Proc) bool) bool {
		return slices.ContainsFunc(tab.Chain(pid), ok)
	}
	is := func(pid int) func(platform.Proc) bool {
		return func(p platform.Proc) bool { return p.PID == pid }
	}
	server := func(p platform.Proc) bool { return strings.HasPrefix(appName(p), "itermserver") }
	named := func(pid int, name string) bool {
		p, ok := tab.Get(pid)
		return ok && p.Name == name
	}
	// Without the placements the decision names, the rest proves nothing.
	shape := []struct {
		name string
		ok   bool
	}{
		{"the table names iTerm2 and Terminal as darwin.Processes does", named(g.ITerm2PID, "iTerm2") && named(g.TerminalPID, "Terminal")},
		{"each stub is a process named claude", named(g.Stubs.UnderITerm2, "claude") && named(g.Stubs.UnderITermServer, "claude") && named(g.Stubs.InTerminal, "claude")},
		{"the stub in iTerm2's session descends from iTerm2", reaches(g.Stubs.UnderITerm2, is(g.ITerm2PID))},
		{"the other iTerm2 stub runs under an iTermServer that doesn't descend from iTerm2", reaches(g.Stubs.UnderITermServer, server) && !reaches(g.Stubs.UnderITermServer, is(g.ITerm2PID))},
		{"the stub in Terminal descends from Terminal and from no iTermServer", reaches(g.Stubs.InTerminal, is(g.TerminalPID)) && !reaches(g.Stubs.InTerminal, server)},
	}
	for _, s := range shape {
		if !s.ok {
			t.Fatalf("the captured table doesn't hold what the test needs: it isn't so that %s", s.name)
		}
	}

	sessions := keyed(tab, map[int]string{
		g.Stubs.UnderITerm2:      "s_iterm2",
		g.Stubs.UnderITermServer: "s_itermserver",
		g.Stubs.InTerminal:       "s_terminal",
	})
	tests := []struct {
		name   string
		pid    int
		hosted []string
	}{
		{"an iTerm2 activation lists both iTerm2 stubs, the one under the iTermServer too, and not Terminal's", g.ITerm2PID, []string{"s_iterm2", "s_itermserver"}},
		{"a Terminal activation lists Terminal's stub alone", g.TerminalPID, []string{"s_terminal"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w, ok := windowOf(tab, tt.pid)
			if !ok {
				t.Fatalf("pid %d isn't in the table", tt.pid)
			}
			if w.class != event.AppTerminal {
				t.Errorf("class %s, want terminal", w.class)
			}
			if got := hostedSessions(tab, w, sessions); !slices.Equal(got, tt.hosted) {
				t.Errorf("hosted_sessions %v, want %v", got, tt.hosted)
			}
		})
	}

	t.Run("the macOS source turns an iTerm2 activation and then a Terminal one into an os.focus each, with those sessions", func(t *testing.T) {
		mf := &MacFocus{}
		var mu sync.Mutex
		var recs []Record
		w := New(Options{
			OS:       MacOS(mf, func() ([]platform.Proc, error) { return slices.Clone(g.Procs), nil }),
			Sessions: func() map[platform.ID]string { return sessions },
			Record: func(r Record) {
				mu.Lock()
				defer mu.Unlock()
				recs = append(recs, r)
			},
		})
		defer w.Stop()
		w.Start()
		mf.Focus(t0, darwin.App{PID: g.ITerm2PID})
		w.sync()
		mf.Focus(t0.Add(time.Second), darwin.App{PID: g.TerminalPID})
		w.sync()
		mu.Lock()
		defer mu.Unlock()
		want := []Record{
			focusRec(t0, event.AppTerminal, "s_iterm2", "s_itermserver"),
			focusRec(t0.Add(time.Second), event.AppTerminal, "s_terminal"),
		}
		if !sameRecords(recs, want) {
			t.Errorf("records %s, want %s", show(recs), show(want))
		}
	})
}
