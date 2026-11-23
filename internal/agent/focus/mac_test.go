// SPDX-License-Identifier: AGPL-3.0-or-later

package focus

import (
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/platform"
	"github.com/some-du6e/pairtime/internal/platform/darwin"
	"github.com/some-du6e/pairtime/pkg/event"
)

// macProc is a macOS process that started mins minutes after t0, named as
// darwin.Processes names it: its executable's base name.
func macProc(pid, ppid int, name string, mins int) platform.Proc {
	return platform.Proc{PID: pid, PPID: ppid, Name: name, Start: t0.Add(time.Duration(mins) * time.Minute), Owner: "uid:501"}
}

// macDesktop is a Mac with Terminal running Claude Code, Chrome and Cursor.
func macDesktop() []platform.Proc {
	return []platform.Proc{
		macProc(1, 0, "launchd", -600),
		macProc(400, 1, "Terminal", -60),
		macProc(410, 400, "login", -60),
		macProc(420, 410, "zsh", -60),
		macProc(430, 420, "claude", -30),
		macProc(500, 1, "Google Chrome", -300),
		macProc(600, 1, "Cursor", -120),
	}
}

// macRig is a Watcher on MacOS's source, fed the way the agent's
// darwin.Loop handler feeds it.
type macRig struct {
	loop *MacFocus
	w    *Watcher

	mu   sync.Mutex
	recs []Record
}

func newMacRig(t *testing.T) *macRig {
	t.Helper()
	r := &macRig{loop: &MacFocus{}}
	tab := platform.NewTable(platform.Darwin, macDesktop())
	sessions := keyed(tab, map[int]string{430: "s_term"})
	r.w = New(Options{
		OS:       MacOS(r.loop, func() ([]platform.Proc, error) { return macDesktop(), nil }),
		Sessions: func() map[platform.ID]string { return sessions },
		Record: func(rec Record) {
			r.mu.Lock()
			defer r.mu.Unlock()
			r.recs = append(r.recs, rec)
		},
	})
	t.Cleanup(r.w.Stop)
	return r
}

// activate is the Loop telling its handler that the app with pid became
// frontmost at the second s after t0. Once the Watcher runs, it waits for
// the Watcher to handle the change: changes that come faster than that
// are coalesced, and only the last is recorded (the package doc).
func (r *macRig) activate(s, pid int) {
	r.loop.Focus(t0.Add(time.Duration(s)*time.Second), darwin.App{PID: pid, BundleID: "com.example.app", Executable: "/Applications/Example.app/Contents/MacOS/Example"})
	r.w.sync()
}

func (r *macRig) records() []Record {
	r.w.sync()
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.recs)
}

func TestMacFocus(t *testing.T) {
	at := func(s int) time.Time { return t0.Add(time.Duration(s) * time.Second) }
	tests := []struct {
		name    string
		before  func(r *macRig) // before Start
		after   func(r *macRig)
		want    []Record
		unknown string
	}{
		{
			name:  "an activation from the Loop becomes one os.focus for the app it names, with the sessions running under it",
			after: func(r *macRig) { r.activate(1, 400) },
			want:  []Record{focusRec(at(1), event.AppTerminal, "s_term")},
		},
		{
			name:   "the Loop's report of the app in front, which comes before the Watcher starts, is the first change the Watcher gets",
			before: func(r *macRig) { r.activate(0, 600) },
			want:   []Record{focusRec(at(0), event.AppEditor)},
		},
		{
			name:   "only the latest activation before the Watcher starts counts, and those after it follow in order",
			before: func(r *macRig) { r.activate(0, 600); r.activate(1, 500) },
			after:  func(r *macRig) { r.activate(2, 400); r.activate(3, 500) },
			want: []Record{
				focusRec(at(1), event.AppBrowser),
				focusRec(at(2), event.AppTerminal, "s_term"),
				focusRec(at(3), event.AppBrowser),
			},
		},
		{
			name:  "activating the app that is already in front adds no record, as a switch between two of its windows activates nothing",
			after: func(r *macRig) { r.activate(1, 400); r.activate(2, 400) },
			want:  []Record{focusRec(at(1), event.AppTerminal, "s_term")},
		},
		{
			name:    "an activation that names no process is focus pairtime can't see: one os.focus unknown, and Status says why (SM 2.5)",
			after:   func(r *macRig) { r.activate(1, 0) },
			want:    []Record{focusRec(at(1), event.AppUnknown)},
			unknown: "NSWorkspace named no process for the app in front",
		},
		{
			name: "the MacLoop's word that no activation will come, given before the Watcher starts, is the first change it gets: one os.focus unknown (SM 2.5)",
			before: func(r *macRig) {
				r.loop.Unknown(at(0), "NSWorkspace has no notification center")
			},
			want:    []Record{focusRec(at(0), event.AppUnknown)},
			unknown: "NSWorkspace has no notification center",
		},
		{
			name: "after the MacLoop's word that focus is unknown, an activation that comes all the same ends it",
			after: func(r *macRig) {
				r.loop.Unknown(at(1), "NSWorkspace has no notification center")
				r.w.sync()
				r.activate(2, 400)
			},
			want: []Record{focusRec(at(1), event.AppUnknown), focusRec(at(2), event.AppTerminal, "s_term")},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newMacRig(t)
			if tt.before != nil {
				tt.before(r)
			}
			r.w.Start()
			r.w.sync()
			if tt.after != nil {
				tt.after(r)
			}
			got := r.records()
			if !sameRecords(got, tt.want) {
				t.Errorf("records %s, want %s", show(got), show(tt.want))
			}
			st := r.w.Status()
			if st.Unknown != tt.unknown {
				t.Errorf("Status.Unknown %q, want %q", st.Unknown, tt.unknown)
			}
			if st.Via != MacVia || st.NoSource != "" {
				t.Errorf("Status.Via %q and NoSource %q, want %q and none", st.Via, st.NoSource, MacVia)
			}
			r.w.Stop()
			r.activate(9, 500)
			if n := len(r.records()); n != len(got) {
				t.Errorf("an activation after Stop added %d records", n-len(got))
			}
		})
	}
}

func TestMonotonic(t *testing.T) {
	wall := time.Now().Add(-1500 * time.Millisecond).Round(0)
	tests := []struct {
		name string
		at   time.Time
	}{
		{name: "a wall-clock time from the Loop keeps its wall clock and gains a monotonic reading", at: wall},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := monotonic(tt.at)
			if !got.Equal(tt.at) || got.UnixMilli() != tt.at.UnixMilli() {
				t.Errorf("monotonic(%v) = %v, a different time", tt.at, got)
			}
			if got.String() == got.Round(0).String() {
				t.Errorf("monotonic(%v) = %v has no monotonic reading", tt.at, got)
			}
			// Measured on the monotonic clock, the time lies as far in the
			// past as its wall clock says.
			if d := time.Since(got); d < 1400*time.Millisecond || d > time.Minute {
				t.Errorf("time.Since(monotonic(at)) = %v, want about 1.5 s", d)
			}
		})
	}
}
