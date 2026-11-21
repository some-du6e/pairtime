// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build linux

package presence

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/agent/sched"
	"github.com/some-du6e/pairtime/internal/platform/linux"
	"github.com/some-du6e/pairtime/pkg/event"
)

// fakeDesktop stands in for a *linux.Desktop: what it says about each
// signal, its idle time and lock state, and logind's events on a channel
// the test feeds.
type fakeDesktop struct {
	status  linux.Status
	idle    time.Duration
	idleErr error
	lockErr error
	power   chan linux.Power
}

func (d *fakeDesktop) Status() linux.Status { return d.status }

func (d *fakeDesktop) Idle(context.Context) (linux.Idle, error) {
	if d.idleErr != nil {
		return linux.Idle{}, d.idleErr
	}
	return linux.Idle{At: time.Now(), Any: d.idle}, nil
}

func (d *fakeDesktop) Locked() (bool, error) { return false, d.lockErr }

func (d *fakeDesktop) Power() <-chan linux.Power {
	if d.power == nil {
		return nil
	}
	return d.power
}

// x11 is the Status of an X11 desktop where every signal is known.
var x11 = linux.Status{
	Session: linux.SessionX11,
	Idle:    linux.Signal{Via: "MIT-SCREEN-SAVER"},
	Focus:   linux.Signal{Via: "_NET_ACTIVE_WINDOW"},
	Power:   linux.Signal{Via: "logind PrepareForSleep"},
	Lock:    linux.Signal{Via: "logind LockedHint"},
}

func TestLinuxOS(t *testing.T) {
	kde := x11
	kde.Session = linux.SessionWayland
	kde.Idle = linux.Signal{Unknown: "this Wayland compositor has no org.gnome.Mutter.IdleMonitor"}
	tests := []struct {
		name     string
		status   linux.Status
		idleErr  error
		lockErr  error
		noLogind bool
		live     bool
		ops      []event.PowerOp
		want     []string
		problems int
		watching []string
	}{
		{
			name:     "a suspend from logind is recorded and sealed before logind's delay lock is let go",
			status:   x11,
			live:     true,
			ops:      []event.PowerOp{event.PowerSuspend, event.PowerResume},
			want:     []string{"presence idle=3", "power suspend", "presence idle=3", "seal suspend", "ready suspend", "power resume", "presence idle=3", "ready resume"},
			watching: []string{"suspend", "lock"},
		},
		{
			name:     "a lock and an unlock from logind become os.power records",
			status:   x11,
			live:     true,
			ops:      []event.PowerOp{event.PowerLock, event.PowerUnlock},
			want:     []string{"presence idle=3", "power lock", "presence idle=3 locked", "seal lock", "ready lock", "power unlock", "presence idle=3", "ready unlock"},
			watching: []string{"suspend", "lock"},
		},
		{
			name:     "outside a project context a suspend and a lock record nothing, are still sealed, and the sleep goes ahead",
			status:   x11,
			ops:      []event.PowerOp{event.PowerSuspend, event.PowerLock},
			want:     []string{"seal suspend", "ready suspend", "seal lock", "ready lock"},
			watching: []string{"suspend", "lock"},
		},
		{
			name:     "a desktop with no idle source gives unknown presence, so nothing is sampled or recorded, but each suspend and lock is sealed before logind's delay lock is let go (DESIGN 3.3, 3.6)",
			status:   kde,
			live:     true,
			ops:      []event.PowerOp{event.PowerSuspend, event.PowerResume, event.PowerLock, event.PowerUnlock},
			want:     []string{"seal suspend", "ready suspend", "ready resume", "seal lock", "ready lock", "ready unlock"},
			watching: []string{"suspend", "lock"},
		},
		{
			name:     "a desktop with no idle source and no logind has nothing to sample, record or seal",
			status:   kde,
			noLogind: true,
			live:     true,
			want:     []string{},
		},
		{
			name:     "a lock state logind can't tell is taken as unlocked, which Status already explains, so presence reports no problem",
			status:   x11,
			lockErr:  linux.ErrLockUnknown,
			live:     true,
			want:     []string{"presence idle=3"},
			watching: []string{"suspend", "lock"},
		},
		{
			name:     "a failed idle read leaves a gap, not a guess, and the problem is reported",
			status:   x11,
			idleErr:  errors.New("the X server didn't answer"),
			live:     true,
			want:     []string{},
			problems: 1,
			watching: []string{"suspend", "lock"},
		},
		{
			name:     "without logind no power watch runs, and the sampler still samples",
			status:   x11,
			noLogind: true,
			live:     true,
			want:     []string{"presence idle=3"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := &fakeDesktop{status: tt.status, idle: 3 * time.Second, idleErr: tt.idleErr, lockErr: tt.lockErr}
			if !tt.noLogind {
				d.power = make(chan linux.Power)
			}
			var mu sync.Mutex
			got := []string{}
			note := func(s string) {
				mu.Lock()
				defer mu.Unlock()
				got = append(got, s)
			}
			s := New(Options{
				Sched: sched.New(),
				OS:    LinuxOS(d),
				Record: func(r Record) {
					if p, ok := r.Data.(*event.OSPresence); ok && p.IdleKeysS != nil {
						t.Errorf("a Linux sample has idle_keys_s %v; Linux has none without a key counter", *p.IdleKeysS)
					}
					note(what(r))
				},
				Seal: func(op event.PowerOp) { note("seal " + string(op)) },
			})
			s.Start()
			defer s.Stop()
			if tt.live {
				s.Hook("s_linux", event.KindPromptSubmit)
			}
			for _, op := range tt.ops {
				ready := make(chan struct{})
				d.power <- linux.Power{Op: op, At: time.Now(), Ready: func() {
					note("ready " + string(op))
					close(ready)
				}}
				select {
				case <-ready:
				case <-time.After(10 * time.Second):
					t.Fatalf("Ready wasn't called for %s", op)
				}
			}
			if d.power != nil {
				close(d.power)
			}
			mu.Lock()
			defer mu.Unlock()
			if !slices.Equal(got, tt.want) {
				t.Errorf("records, seals and Ready calls:\n  %s\nwant:\n  %s", join(got), join(tt.want))
			}
			st := s.Status()
			if len(st.Problems) != tt.problems {
				t.Errorf("problems %q, want %d", st.Problems, tt.problems)
			}
			if !slices.Equal(st.Watching, tt.watching) {
				t.Errorf("watching %q, want %q", st.Watching, tt.watching)
			}
		})
	}
}
