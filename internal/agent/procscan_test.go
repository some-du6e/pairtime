// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"log/slog"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/agent/procs"
	"github.com/some-du6e/pairtime/internal/agent/sched"
	"github.com/some-du6e/pairtime/internal/agent/state"
	"github.com/some-du6e/pairtime/internal/platform"
)

// TestProcsKeepAContextLive checks how the agent wires the process scan
// (WP-OS6) to presence (WP-OS1): a hookless harness session the scan
// finds, such as a `claude --bare` run, makes a project context live, so
// presence samples while it works (DESIGN 3.3), and the context ends with
// the process.
func TestProcsKeepAContextLive(t *testing.T) {
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
	const me, sibling = "uid:1000", "uid:1001"
	desktop := []platform.Proc{
		{PID: 1, Name: "systemd", PGID: 1, TTY: platform.TTYNone, Start: now.Add(-time.Hour), Owner: "uid:0"},
		{PID: 100, PPID: 1, Name: "kitty", PGID: 100, TTY: platform.TTYNone, Start: now.Add(-10 * time.Minute), Owner: me},
		{PID: 110, PPID: 100, Name: "bash", PGID: 110, TTY: platform.TTYAttached, Start: now.Add(-9 * time.Minute), Owner: me},
	}
	with := func(p platform.Proc) []platform.Proc { return append(slices.Clone(desktop), p) }
	bare := platform.Proc{PID: 111, PPID: 110, Name: "claude", PGID: 111, TTY: platform.TTYAttached, Start: now.Add(-time.Minute), Owner: me}
	theirs := platform.Proc{PID: 211, PPID: 1, Name: "claude", PGID: 211, TTY: platform.TTYNone, Start: now.Add(-time.Minute), Owner: sibling}

	var table []platform.Proc
	var reported []procs.Session
	opts := a.procsOptions(procs.Options{
		Read: func() ([]platform.Proc, error) { return slices.Clone(table), nil },
		OS:   platform.Linux,
		Self: me,
		Boot: "test-boot",
		Now:  func() time.Time { return now },
	})
	logReport := opts.Report
	opts.Report = func(s procs.Session) {
		reported = append(reported, s)
		logReport(s)
	}
	scanner := procs.New(opts)

	steps := []struct {
		name     string
		procs    []platform.Proc
		reported []int // pids of the sessions reported so far
		live     bool
	}{
		{"with no harness running there is no project context", desktop, nil, false},
		{"a hookless claude the scan finds makes the context live", with(bare), []int{111}, true},
		{"the context ends when that claude exits", desktop, []int{111}, false},
		{"another user's claude makes no context live", with(theirs), []int{111}, false},
	}
	for _, step := range steps {
		t.Run(step.name, func(t *testing.T) {
			table = step.procs
			if _, err := scanner.Scan(); err != nil {
				t.Fatal(err)
			}
			var pids []int
			for _, s := range reported {
				pids = append(pids, s.PID)
			}
			if !slices.Equal(pids, step.reported) {
				t.Errorf("the scan has reported pids %v, want %v", pids, step.reported)
			}
			if got := a.presence.Status().Live; got != step.live {
				t.Errorf("presence's project context live = %v, want %v", got, step.live)
			}
		})
	}
}
