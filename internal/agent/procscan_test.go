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
	"github.com/some-du6e/pairtime/internal/agent/presence"
	"github.com/some-du6e/pairtime/internal/agent/procs"
	"github.com/some-du6e/pairtime/internal/agent/sched"
	"github.com/some-du6e/pairtime/internal/agent/state"
	"github.com/some-du6e/pairtime/internal/platform"
	"github.com/some-du6e/pairtime/pkg/event"
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
	t.Cleanup(a.startPresence(presence.OS{}))

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

// TestProcsUnderAnAgentApp checks the scan's count end to end, wired to
// presence and to the focus component as the agent wires them, while T3
// Code runs (D-relayed-context): a claude T3's server started, as the one
// T3 keeps running in a thread between its runs, is no sign of work on
// this computer, so with nothing else T3 alone keeps the context live, at
// a sample a minute and with no os.focus record; the same claude under a
// terminal counts, and gives the usual context, SAMPLE_S and os.focus
// records. A claude typed into T3's own terminal is T3's too (Q12), and
// T3's own window in front, or a hook from that claude, makes a context of
// its own.
func TestProcsUnderAnAgentApp(t *testing.T) {
	now := time.Date(2026, 11, 23, 21, 0, 0, 0, time.UTC)
	ago := func(d time.Duration) time.Time { return now.Add(-d) }
	const me = "uid:1000"
	// A Linux desktop with T3 Code's desktop app, which runs its server as
	// itself and owns the window T3's own terminal is in, a terminal with a
	// shell in it, and a browser.
	desk := []platform.Proc{
		{PID: 1, Name: "systemd", PGID: 1, TTY: platform.TTYNone, Start: ago(time.Hour), Owner: "uid:0"},
		{PID: 1900, PPID: 1, Name: "systemd", PGID: 1900, TTY: platform.TTYNone, Start: ago(59 * time.Minute), Owner: me},
		{PID: 4300, PPID: 1900, Name: "t3code", PGID: 4300, TTY: platform.TTYNone, Start: ago(50 * time.Minute), Owner: me},
		{PID: 4360, PPID: 4300, Name: "t3code", PGID: 4300, TTY: platform.TTYNone, Start: ago(50 * time.Minute), Owner: me},
		{PID: 100, PPID: 1900, Name: "kitty", PGID: 100, TTY: platform.TTYNone, Start: ago(40 * time.Minute), Owner: me},
		{PID: 110, PPID: 100, Name: "bash", PGID: 110, TTY: platform.TTYAttached, Start: ago(40 * time.Minute), Owner: me},
		{PID: 900, PPID: 1900, Name: "firefox", PGID: 900, TTY: platform.TTYNone, Start: ago(30 * time.Minute), Owner: me},
	}
	with := func(ps ...platform.Proc) []platform.Proc { return append(slices.Clone(desk), ps...) }
	// The same claude, started by T3's server, or typed into the terminal's
	// shell; and a shell of T3's own terminal with a claude typed into it.
	t3Claude := platform.Proc{PID: 6100, PPID: 4360, Name: "claude", PGID: 4300, TTY: platform.TTYNone, Start: ago(20 * time.Minute), Owner: me}
	termClaude := platform.Proc{PID: 6100, PPID: 110, Name: "claude", PGID: 6100, TTY: platform.TTYAttached, TPGID: 6100, Start: ago(20 * time.Minute), Owner: me}
	t3Shell := platform.Proc{PID: 4400, PPID: 4360, Name: "bash", PGID: 4400, TTY: platform.TTYAttached, Start: ago(15 * time.Minute), Owner: me}
	t3Typed := platform.Proc{PID: 4410, PPID: 4400, Name: "claude", PGID: 4410, TTY: platform.TTYAttached, TPGID: 4410, Start: ago(5 * time.Minute), Owner: me}
	inT3Terminal := with(t3Shell, t3Typed)
	browser := focus.Change{At: now, PID: 900}
	t3Window := focus.Change{At: now.Add(time.Second), PID: 4300}

	cases := []struct {
		name  string
		procs []platform.Proc
		// hook is a prompt hook from the claude typed into T3's terminal,
		// after the scan and before the changes of focus.
		hook    bool
		changes []focus.Change
		// counted is the count the scan gave presence, written the classes
		// of the os.focus records the focus component writes, and appOnly
		// and every what presence says after the last change.
		counted int
		written []event.AppClass
		appOnly bool
		every   time.Duration
	}{
		{"a claude T3 Code's server keeps running, alive with no input, doesn't count, so T3 alone keeps the context live: a sample a minute and no os.focus",
			with(t3Claude), false, []focus.Change{browser}, 0, nil, true, time.Minute},
		{"the same claude under a terminal counts, and gives the usual context: SAMPLE_S and os.focus records",
			with(termClaude), false, []focus.Change{browser}, 1, []event.AppClass{event.AppBrowser}, false, 15 * time.Second},
		{"a claude typed into T3 Code's own terminal doesn't count either, since its parent_class is agent_app (Q12)",
			inT3Terminal, false, []focus.Change{browser}, 0, nil, true, time.Minute},
		{"T3 Code's own window in front still makes a context, and its change is written",
			inT3Terminal, false, []focus.Change{browser, t3Window}, 0, []event.AppClass{event.AppAgentApp}, false, 15 * time.Second},
		{"so does a hook from the claude typed into T3 Code's terminal, and the window in front is written",
			inT3Terminal, true, []focus.Change{browser}, 0, []event.AppClass{event.AppBrowser}, false, 15 * time.Second},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			store, problem, err := state.Open(filepath.Join(t.TempDir(), "state.json"))
			if err != nil || problem != "" {
				t.Fatalf("state.Open: %v %q", err, problem)
			}
			a := &agent{
				log:      slog.New(slog.DiscardHandler),
				sched:    sched.New(),
				state:    store,
				problems: make(map[string]string),
				presence: presence.New(presence.Options{Sched: sched.New(), Params: newestTable()}),
			}
			// T3 Code runs, as the apps component found it.
			a.presence.AgentApps(1)

			var mu sync.Mutex
			var written []event.AppClass
			told := 0 // the changes presence has heard of
			var front func(focus.Change)
			read := func() ([]platform.Proc, error) { return slices.Clone(c.procs), nil }
			a.focus = focus.New(focus.Options{
				OS: focus.OS{
					Via:  "fake",
					GOOS: platform.Linux,
					Read: read,
					Watch: func(fn func(focus.Change)) (func(), error) {
						front = fn
						return func() {}, nil
					},
				},
				Live: a.focusLive,
				Presence: func(at time.Time, cls event.AppClass, write func(time.Time, bool)) {
					a.presence.FocusAt(at, cls, write)
					mu.Lock()
					defer mu.Unlock()
					told++
				},
				Record: func(r focus.Record) {
					mu.Lock()
					defer mu.Unlock()
					written = append(written, r.Data.AppClass)
				},
			})
			a.focus.Start()
			t.Cleanup(a.focus.Stop)

			var counted []int
			opts := a.procsOptions(procs.Options{
				Read: read,
				OS:   platform.Linux,
				Self: me,
				Boot: "test-boot",
				Now:  func() time.Time { return now },
			})
			running := opts.Running
			opts.Running = func(n int) {
				counted = append(counted, n)
				running(n)
			}
			if _, err := procs.New(opts).Scan(); err != nil {
				t.Fatal(err)
			}
			if c.hook {
				a.presence.Hook("s_t3_terminal", event.KindPromptSubmit)
			}
			for i, ch := range c.changes {
				front(ch)
				deadline := time.Now().Add(5 * time.Second)
				for {
					mu.Lock()
					n := told
					mu.Unlock()
					if n > i {
						break
					}
					if time.Now().After(deadline) {
						t.Fatalf("presence heard of %d changes 5 s after change %d came, want %d", n, i+1, i+1)
					}
					time.Sleep(5 * time.Millisecond)
				}
			}

			mu.Lock()
			got := slices.Clone(written)
			mu.Unlock()
			if !slices.Equal(counted, []int{c.counted}) {
				t.Errorf("the scan told presence %v harness processes, want [%d]", counted, c.counted)
			}
			if !slices.Equal(got, c.written) {
				t.Errorf("os.focus records of %v, want %v", got, c.written)
			}
			if st := a.presence.Status(); !st.Live || st.AppOnly != c.appOnly || st.Every != c.every {
				t.Errorf("presence says live %v, AppOnly %v, every %v; want live, AppOnly %v, every %v", st.Live, st.AppOnly, st.Every, c.appOnly, c.every)
			}
		})
	}
}
