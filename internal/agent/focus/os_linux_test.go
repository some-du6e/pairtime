// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build linux

package focus

import (
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/platform"
	"github.com/some-du6e/pairtime/internal/platform/linux"
	"github.com/some-du6e/pairtime/pkg/event"
)

// fakeDesktop stands in for a *linux.Desktop: what its Status says about
// focus, and the X11 watcher's reports on a channel the test feeds. No
// test moves real focus here; the desktop tests in cmd/pairtime do, under
// Xvfb (platform.yml).
type fakeDesktop struct {
	focus linux.Signal
	ch    chan linux.Focus
}

func (d *fakeDesktop) Status() linux.Status { return linux.Status{Focus: d.focus} }

func (d *fakeDesktop) Focus() <-chan linux.Focus {
	if d.ch == nil {
		return nil
	}
	return d.ch
}

// linuxProc is a Linux process that started mins minutes after t0, named
// as linux.Processes names it.
func linuxProc(pid, ppid int, name string, mins int) platform.Proc {
	return platform.Proc{PID: pid, PPID: ppid, Name: name, Start: t0.Add(time.Duration(mins) * time.Minute), Owner: "uid:1000"}
}

// linuxDesktop is an X11 desktop with two xterms, one running Claude Code,
// and Firefox.
func linuxDesktop() []platform.Proc {
	return []platform.Proc{
		linuxProc(1, 0, "systemd", -600),
		linuxProc(900, 1, "xterm", -60),
		linuxProc(910, 900, "bash", -60),
		linuxProc(920, 910, "claude", -30),
		linuxProc(950, 1, "xterm", -50),
		linuxProc(960, 950, "bash", -50),
		linuxProc(1000, 1, "firefox", -300),
	}
}

func TestLinuxOS(t *testing.T) {
	at := func(s int) time.Time { return t0.Add(time.Duration(s) * time.Second) }
	x11 := linux.Signal{Via: "_NET_ACTIVE_WINDOW, with each window's process from X-Resource"}
	tests := []struct {
		name       string
		focus      linux.Signal
		watcher    bool // the desktop runs an X11 focus watcher
		noSessions bool // no harness runs anywhere
		reports    []linux.Focus
		want       []Record
		// via, noSource and unknown are what Status should say after.
		via, noSource, unknown string
	}{
		{
			name:    "each window the X11 watcher reports becomes an os.focus by its process, and the other xterm, which hosts no session, gets a record of its own",
			focus:   x11,
			watcher: true,
			reports: []linux.Focus{
				{At: at(0), Window: 0x400001, PID: 900},
				{At: at(5), Window: 0x600001, PID: 1000},
				{At: at(9), Window: 0x400001, PID: 900},
				{At: at(12), Window: 0x500001, PID: 950},
			},
			want: []Record{
				focusRec(at(0), event.AppTerminal, "s_xterm"),
				focusRec(at(5), event.AppBrowser),
				focusRec(at(9), event.AppTerminal, "s_xterm"),
				focusRec(at(12), event.AppTerminal),
			},
			via: x11.Via,
		},
		{
			name:       "a switch between two xterms that host no session writes nothing new, since the record would say the same",
			focus:      x11,
			watcher:    true,
			noSessions: true,
			reports: []linux.Focus{
				{At: at(0), Window: 0x400001, PID: 900},
				{At: at(4), Window: 0x500001, PID: 950},
			},
			want: []Record{focusRec(at(0), event.AppTerminal)},
			via:  x11.Via,
		},
		{
			name:    "no window with focus is none, and a window whose process isn't known is other, as linux.Focus.Payload has them",
			focus:   x11,
			watcher: true,
			reports: []linux.Focus{
				{At: at(0), Window: 0x400001, PID: 900},
				{At: at(3), Window: 0},
				{At: at(6), Window: 0x700001, PID: 0},
			},
			want: []Record{
				focusRec(at(0), event.AppTerminal, "s_xterm"),
				focusRec(at(3), event.AppNone),
				focusRec(at(6), event.AppOther),
			},
			via: x11.Via,
		},
		{
			name:    "while no window manager keeps _NET_ACTIVE_WINDOW the watcher runs and the source is named by the property it waits for",
			focus:   linux.Signal{Unknown: "the window manager doesn't keep _NET_ACTIVE_WINDOW"},
			watcher: true,
			via:     "_NET_ACTIVE_WINDOW",
		},
		{
			name:     "on Wayland there is no focus source, nothing is recorded, and Status gives the desktop's reason (D-focus-unknown)",
			focus:    linux.Signal{Unknown: "Wayland gives other programs no focus signal"},
			noSource: "Wayland gives other programs no focus signal",
			unknown:  "Wayland gives other programs no focus signal",
		},
		{
			name:     "with no graphical session there is no focus source either",
			focus:    linux.Signal{Unknown: "no graphical session"},
			noSource: "no graphical session",
			unknown:  "no graphical session",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := &fakeDesktop{focus: tt.focus}
			if tt.watcher {
				d.ch = make(chan linux.Focus)
			}
			src := LinuxOS(d)
			src.Read = func() ([]platform.Proc, error) { return linuxDesktop(), nil }
			tab := platform.NewTable(platform.Linux, linuxDesktop())
			sessions := keyed(tab, map[int]string{920: "s_xterm"})
			if tt.noSessions {
				sessions = nil
			}
			var mu sync.Mutex
			var got []Record
			w := New(Options{
				OS:       src,
				Sessions: func() map[platform.ID]string { return sessions },
				Record: func(r Record) {
					mu.Lock()
					defer mu.Unlock()
					got = append(got, r)
				},
			})
			w.Start()
			defer w.Stop()
			for i, f := range tt.reports {
				d.ch <- f
				// The watch hands the report over after the send returns.
				deadline := time.Now().Add(10 * time.Second)
				for w.Status().Changes < uint64(i+1) {
					if time.Now().After(deadline) {
						t.Fatalf("the Watcher didn't get report %d", i)
					}
					time.Sleep(time.Millisecond)
				}
				w.sync()
			}
			mu.Lock()
			recs := slices.Clone(got)
			mu.Unlock()
			if !sameRecords(recs, tt.want) {
				t.Errorf("records %s, want %s", show(recs), show(tt.want))
			}
			st := w.Status()
			if st.Via != tt.via || st.NoSource != tt.noSource || st.Unknown != tt.unknown {
				t.Errorf("Status says via %q, no source %q, unknown %q; want %q, %q, %q", st.Via, st.NoSource, st.Unknown, tt.via, tt.noSource, tt.unknown)
			}
			w.Stop()
			if d.ch != nil {
				// The watch let go of the channel: a report now waits for no
				// one, and the desktop's Close would end it.
				select {
				case d.ch <- linux.Focus{At: at(30), Window: 0x400001, PID: 900}:
					t.Error("after Stop, the watch still reads the desktop's reports")
				case <-time.After(50 * time.Millisecond):
				}
			}
		})
	}
}
