// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"log/slog"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/agent/focus"
	"github.com/some-du6e/pairtime/internal/agent/procs"
	"github.com/some-du6e/pairtime/internal/agent/sched"
	"github.com/some-du6e/pairtime/internal/agent/state"
	"github.com/some-du6e/pairtime/internal/platform"
	"github.com/some-du6e/pairtime/pkg/event"
)

// TestFocusWiring checks how the agent wires the focus component (WP-OS2)
// to presence and the process scan: focus on a terminal makes a project
// context live (DESIGN 3.3), and a session the scan finds under the
// terminal in front becomes one of the sessions it hosts (SM 3.2), and
// stops being one when it exits.
func TestFocusWiring(t *testing.T) {
	store, problem, err := state.Open(filepath.Join(t.TempDir(), "state.json"))
	if err != nil || problem != "" {
		t.Fatalf("state.Open: %v %q", err, problem)
	}
	a := &agent{
		log:      slog.New(slog.DiscardHandler),
		sched:    sched.New(),
		state:    store,
		problems: make(map[string]string),
	}
	t.Cleanup(a.startPresence())

	now := time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)
	const me = "session:1"
	desk := []platform.Proc{
		{PID: 4, Name: "System", Start: now.Add(-time.Hour)},
		{PID: 4520, PPID: 4480, Name: "explorer", Start: now.Add(-50 * time.Minute), Owner: me},
		{PID: 6100, PPID: 4520, Name: "WindowsTerminal", Start: now.Add(-10 * time.Minute), Owner: me},
		{PID: 6144, PPID: 6100, Name: "OpenConsole", Start: now.Add(-10 * time.Minute), Owner: me},
		{PID: 6200, PPID: 6100, Name: "pwsh", Start: now.Add(-9 * time.Minute), Owner: me},
	}
	claude := platform.Proc{PID: 6210, PPID: 6200, Name: "claude", Start: now.Add(-time.Minute), Owner: me}

	var mu sync.Mutex
	table := desk
	read := func() ([]platform.Proc, error) {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(table), nil
	}
	setTable := func(procs []platform.Proc) {
		mu.Lock()
		defer mu.Unlock()
		table = procs
	}
	a.procs = procs.New(a.procsOptions(procs.Options{
		Read: read,
		OS:   platform.Windows,
		Self: me,
		Boot: "test-boot",
		Now:  func() time.Time { return now },
	}))
	var front func(focus.Change)
	a.newFocus(focus.OS{
		Via:  "fake",
		GOOS: platform.Windows,
		Read: read,
		Watch: func(fn func(focus.Change)) (func(), error) {
			front = fn
			return func() {}, nil
		},
	})
	t.Cleanup(a.startFocus())

	steps := []struct {
		name   string
		do     func()
		class  event.AppClass
		hosted int
		live   bool
	}{
		{"focus on a Windows Terminal makes a project context live", func() { front(focus.Change{At: now, PID: 6100}) }, event.AppTerminal, 0, true},
		{"a session the scan finds under the terminal in front is hosted by it", func() {
			setTable(append(slices.Clone(desk), claude))
			if _, err := a.procs.Scan(); err != nil {
				t.Fatal(err)
			}
		}, event.AppTerminal, 1, true},
		{"the session's exit leaves the terminal hosting none", func() {
			setTable(desk)
			if _, err := a.procs.Scan(); err != nil {
				t.Fatal(err)
			}
		}, event.AppTerminal, 0, true},
		{"focus on the desktop, which is explorer's, makes no context, though the terminal's keeps it live a while", func() { front(focus.Change{At: now.Add(time.Minute), PID: 4520}) }, event.AppOther, 0, true},
	}
	for _, step := range steps {
		t.Run(step.name, func(t *testing.T) {
			step.do()
			deadline := time.Now().Add(5 * time.Second)
			var st focus.Status
			for {
				st = a.focus.Status()
				if st.Class == step.class && st.Hosted == step.hosted {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("focus status %+v, want class %s hosting %d", st, step.class, step.hosted)
				}
				time.Sleep(5 * time.Millisecond)
			}
			if got := a.presence.Status().Live; got != step.live {
				t.Errorf("presence's project context live = %v, want %v", got, step.live)
			}
		})
	}
}
