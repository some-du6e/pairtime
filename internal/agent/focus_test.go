// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"bytes"
	"log/slog"
	"maps"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/agent/focus"
	"github.com/some-du6e/pairtime/internal/agent/home"
	"github.com/some-du6e/pairtime/internal/agent/keys"
	"github.com/some-du6e/pairtime/internal/agent/ledger"
	"github.com/some-du6e/pairtime/internal/agent/presence"
	"github.com/some-du6e/pairtime/internal/agent/procs"
	"github.com/some-du6e/pairtime/internal/agent/sched"
	"github.com/some-du6e/pairtime/internal/agent/state"
	"github.com/some-du6e/pairtime/internal/platform"
	"github.com/some-du6e/pairtime/pkg/event"
	pkgledger "github.com/some-du6e/pairtime/pkg/ledger"
)

// TestFocusWiring checks how the agent wires the focus component (WP-OS2)
// to presence, the process scan and the ledger: focus on a terminal makes
// a project context live (DESIGN 3.3), a session the scan finds under the
// terminal in front becomes one of the sessions it hosts (SM 3.2) and
// stops being one when it exits, and each os.focus record reaches the
// ledger (WP-LG5) with app_hash keyed by K_app but kept out of the body.
func TestFocusWiring(t *testing.T) {
	store, problem, err := state.Open(filepath.Join(t.TempDir(), "state.json"))
	if err != nil || problem != "" {
		t.Fatalf("state.Open: %v %q", err, problem)
	}
	dirs := home.Dirs{Data: t.TempDir(), Config: t.TempDir()}
	dirs.Runtime = dirs.Data
	a := &agent{
		dirs:      dirs,
		log:       slog.New(slog.DiscardHandler),
		sched:     sched.New(),
		state:     store,
		problems:  make(map[string]string),
		keyStores: []keys.Store{}, // K_dev goes in a key file, off the OS's store
	}
	// The parts stop in the agent's order, once each, before the test reads
	// the ledger or, after a failure, at cleanup.
	var stops []func()
	var stopOnce sync.Once
	stopAll := func() {
		stopOnce.Do(func() {
			for i := len(stops) - 1; i >= 0; i-- {
				stops[i]()
			}
		})
	}
	t.Cleanup(stopAll)
	stops = append(stops, a.startLedger(), a.startPresence(presence.OS{}))

	// The keystore opens on the ledger's goroutine. Waiting for K_app means
	// every record below is made with it.
	deadline := time.Now().Add(20 * time.Second)
	for a.appSecret() == nil {
		if time.Now().After(deadline) {
			a.mu.Lock()
			problems := maps.Clone(a.problems)
			a.mu.Unlock()
			t.Fatalf("the agent had no K_app 20 s after its keystore started opening; problems %v", problems)
		}
		time.Sleep(10 * time.Millisecond)
	}

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
	stops = append(stops, a.startFocus())

	steps := []struct {
		name   string
		do     func(t *testing.T)
		class  event.AppClass
		hosted int
		live   bool
	}{
		{"focus on a Windows Terminal makes a project context live", func(*testing.T) { front(focus.Change{At: now, PID: 6100}) }, event.AppTerminal, 0, true},
		{"a session the scan finds under the terminal in front is hosted by it", func(t *testing.T) {
			setTable(append(slices.Clone(desk), claude))
			if _, err := a.procs.Scan(); err != nil {
				t.Fatal(err)
			}
		}, event.AppTerminal, 1, true},
		{"the session's exit leaves the terminal hosting none", func(t *testing.T) {
			setTable(desk)
			if _, err := a.procs.Scan(); err != nil {
				t.Fatal(err)
			}
		}, event.AppTerminal, 0, true},
		{"focus on the desktop, which is explorer's, makes no context, though the terminal's keeps it live a while", func(*testing.T) { front(focus.Change{At: now.Add(time.Minute), PID: 4520}) }, event.AppOther, 0, true},
	}
	for _, step := range steps {
		t.Run(step.name, func(t *testing.T) {
			step.do(t)
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

	// Stopping writes what presence's queue holds and seals it.
	stopAll()
	var recs []event.Event
	if err := ledger.ReadBatches(dirs.Ledger(), 1, func(b ledger.Batch) error {
		for _, r := range b.Records {
			body, err := pkgledger.ParseBody(r.Body)
			if err != nil {
				return err
			}
			ev, err := body.Event()
			if err != nil {
				return err
			}
			if ev.Kind() == event.KindOSFocus {
				recs = append(recs, ev)
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("reading the ledger back: %v", err)
	}
	var classes []event.AppClass
	var hosted []int
	inOrder, osEvents, noAppHash := true, true, true
	for i, ev := range recs {
		d := ev.Data.(*event.OSFocus)
		classes, hosted = append(classes, d.AppClass), append(hosted, len(d.HostedSessions))
		if i > 0 && ev.T < recs[i-1].T {
			inOrder = false
		}
		if ev.Source != event.SourceOS || ev.Via != event.ViaOS || ev.Harness != event.HarnessNone || ev.SessionKey != event.NoSession {
			osEvents = false
		}
		if d.AppHash != "" {
			noAppHash = false
		}
	}
	var want []byte
	ks, err := keys.Open(keys.Options{Dir: dirs.Data, Stores: []keys.Store{}})
	if err == nil {
		err = ks.WithDeviceKey(func(kDev []byte) error {
			var err error
			want, err = focus.AppSecret(kDev)
			return err
		})
	}
	if err != nil {
		t.Fatalf("reading this installation's K_dev back: %v", err)
	}

	checks := []struct {
		name string
		ok   bool
	}{
		{"the agent keys app_hash with the K_app of this installation's K_dev", bytes.Equal(a.appSecret(), want)},
		{"each os.focus record the component wrote is in the ledger, in the order written",
			slices.Equal(classes, []event.AppClass{event.AppTerminal, event.AppTerminal, event.AppTerminal, event.AppOther}) &&
				slices.Equal(hosted, []int{0, 1, 0, 0})},
		{"the os.focus records in the ledger never go back in time", inOrder},
		{"an os.focus record is an OS event with no session (SM 1.2)", osEvents},
		{"app_hash, a local field, is in no ledger body", noAppHash},
	}
	for _, c := range checks {
		t.Run(c.name, func(t *testing.T) {
			if !c.ok {
				t.Errorf("no: %d os.focus records, classes %v, hosted %v", len(recs), classes, hosted)
			}
		})
	}
}

// TestFocusLive checks the agent's word to the focus component on whether
// it may record the window in front (focus.Options.Live): in a live
// project context, but never while a running agent app alone keeps it live
// (D-relayed-context, as #225 amended it), when pairtime records nothing
// about which app is in front.
func TestFocusLive(t *testing.T) {
	cases := []struct {
		name string
		do   func(s *presence.Sampler)
		want bool
	}{
		{"with no project context nothing is recorded", func(*presence.Sampler) {}, false},
		{"a context only T3 Code's server keeps live records nothing about the window in front",
			func(s *presence.Sampler) { s.AgentApps(1) }, false},
		{"with a browser in front of T3 Code's context nothing is recorded either", func(s *presence.Sampler) {
			s.AgentApps(1)
			s.Focus(event.AppBrowser)
		}, false},
		{"T3 Code's own window in front is a sign of work, and the usual rules record it", func(s *presence.Sampler) {
			s.AgentApps(1)
			s.Focus(event.AppAgentApp)
		}, true},
		{"a terminal's context records as before", func(s *presence.Sampler) { s.Focus(event.AppTerminal) }, true},
		{"a harness process's context records too, with T3 Code running or not", func(s *presence.Sampler) {
			s.AgentApps(1)
			s.HarnessProcesses(1)
		}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := &agent{presence: presence.New(presence.Options{Sched: sched.New(), Params: newestTable()})}
			c.do(a.presence)
			if got := a.focusLive(); got != c.want {
				t.Errorf("focusLive = %v, want %v (presence %+v)", got, c.want, a.presence.Status())
			}
		})
	}
}

// TestFocusAppOnly checks the focus component's records against presence's
// word, wired as newFocus wires them: while a running agent app alone keeps
// the context live, a change of focus writes no os.focus, unknown focus
// included, and a change to a project app is written and ends the app's
// being the only sign (D-relayed-context, as #225 amended it).
func TestFocusAppOnly(t *testing.T) {
	now := time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)
	const me = "session:1"
	desk := []platform.Proc{
		{PID: 4, Name: "System", Start: now.Add(-time.Hour)},
		{PID: 4520, PPID: 4480, Name: "explorer", Start: now.Add(-50 * time.Minute), Owner: me},
		{PID: 6100, PPID: 4520, Name: "WindowsTerminal", Start: now.Add(-10 * time.Minute), Owner: me},
		{PID: 9600, PPID: 4520, Name: "chrome", Start: now.Add(-5 * time.Minute), Owner: me},
	}
	chrome := focus.Change{At: now, PID: 9600}
	cases := []struct {
		name    string
		changes []focus.Change
		// written are the classes of the os.focus records the component
		// writes, and appOnly and every what presence says after the last
		// change.
		written []event.AppClass
		appOnly bool
		every   time.Duration
	}{
		{"no os.focus is written while an agent app alone keeps the context live, and a change to a project app is written and ends it",
			[]focus.Change{chrome, {At: now.Add(time.Second), PID: 6100}}, []event.AppClass{event.AppTerminal}, false, 15 * time.Second},
		{"a browser in front of T3 Code's context alone writes nothing, and presence samples once a minute",
			[]focus.Change{chrome}, nil, true, time.Minute},
		{"focus that turns unknown writes nothing either, and presence samples at D14's cadence, 15 s before any input, since rule 6 passes there (WP-SC8)",
			[]focus.Change{chrome, {At: now.Add(time.Second), Unknown: "the secure desktop"}}, nil, true, 15 * time.Second},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := &agent{presence: presence.New(presence.Options{Sched: sched.New(), Params: newestTable()})}
			a.presence.AgentApps(1)
			var mu sync.Mutex
			var written []event.AppClass
			told := 0 // the changes presence has heard of
			var front func(focus.Change)
			w := focus.New(focus.Options{
				OS: focus.OS{
					Via:  "fake",
					GOOS: platform.Windows,
					Read: func() ([]platform.Proc, error) { return slices.Clone(desk), nil },
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
			w.Start()
			t.Cleanup(w.Stop)
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
			if !slices.Equal(got, c.written) {
				t.Errorf("os.focus records of %v, want %v", got, c.written)
			}
			if st := a.presence.Status(); st.AppOnly != c.appOnly || st.Every != c.every {
				t.Errorf("presence says AppOnly %v every %v, want %v every %v", st.AppOnly, st.Every, c.appOnly, c.every)
			}
		})
	}
}
