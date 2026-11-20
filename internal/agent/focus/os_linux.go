// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build linux

package focus

import (
	"sync"

	"github.com/some-du6e/pairtime/internal/platform"
	"github.com/some-du6e/pairtime/internal/platform/linux"
	"github.com/some-du6e/pairtime/pkg/event"
)

// Desktop is what the focus component reads of a Linux desktop: the
// agent's *linux.Desktop, which it opens once for presence and focus both,
// or a fake in tests.
type Desktop interface {
	Status() linux.Status
	Focus() <-chan linux.Focus
}

// linuxVia names the focus source while the desktop's own Status names
// none yet, as before a window manager that keeps _NET_ACTIVE_WINDOW
// starts.
const linuxVia = "_NET_ACTIVE_WINDOW"

// LinuxOS returns a Linux desktop's focus source (DESIGN 3.3; PLAN
// WP-OS9): the focus changes d reports, which on X11 follow the window
// manager's _NET_ACTIVE_WINDOW, and the process table from /proc.
//
// d reports no changes where focus can't be read: on Wayland, which tells
// other programs nothing about focus without pairtime's GNOME Shell
// extension (WP-OS14), with no graphical session, and on X11 while no
// window manager keeps _NET_ACTIVE_WINDOW or while a lost X connection is
// re-made. Then nothing is recorded, d's Status says why, and the window
// last recorded stays in front for the scorer.
//
// TODO(D-focus-unknown): record an os.focus with app_class unknown when
// focus is unknown from the start and whenever it turns unknown, once
// pkg/event has it (WP-EV2) and d reports each change between known and
// unknown focus, so those slots credit at level C (SM 2.5).
func LinuxOS(d Desktop) OS {
	o := OS{Read: linux.Processes, GOOS: platform.Linux}
	st := d.Status().Focus
	ch := d.Focus()
	if ch == nil {
		o.Unknown = st.Unknown
		if o.Unknown == "" {
			o.Unknown = "the desktop reports no focus changes"
		}
		return o
	}
	o.Via = st.Via
	if o.Via == "" {
		o.Via = linuxVia
	}
	o.Watch = func(fn func(Change)) (func(), error) {
		stop, done := make(chan struct{}), make(chan struct{})
		go func() {
			defer close(done)
			for {
				select {
				case f, ok := <-ch:
					if !ok {
						return
					}
					fn(linuxChange(f))
				case <-stop:
					return
				}
			}
		}()
		var once sync.Once
		return func() {
			once.Do(func() { close(stop) })
			<-done
		}, nil
	}
	return o
}

// linuxChange turns one of the desktop's focus reports into a Change. A
// window whose process isn't known, because the window doesn't say or
// belongs to another host, is other, and no window at all is none, as
// linux.Focus.Payload has them: neither hosts a session, and neither is a
// guess.
func linuxChange(f linux.Focus) Change {
	switch {
	case f.Window == 0:
		return Change{At: f.At, Class: event.AppNone}
	case f.PID <= 0:
		return Change{At: f.At, Class: event.AppOther}
	}
	return Change{At: f.At, PID: f.PID}
}
