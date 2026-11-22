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
// Where focus can't be read, the component records an os.focus with
// app_class unknown, whose slots credit at level C (SM 2.5). On Wayland,
// which tells other programs nothing about focus without pairtime's GNOME
// Shell extension (WP-OS14), and with no graphical session, d has no Focus
// channel, so focus is unknown from the start and d's Status says why. On
// X11, d reports on its channel each time focus turns unknown, while no
// window manager keeps _NET_ACTIVE_WINDOW or while a lost X connection is
// re-made, and its next window report ends it.
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
// report that focus is unknown is unknown. A window whose process isn't
// known, because the window doesn't say or belongs to another host, is
// other, and no window at all is none, as linux.Focus.Payload has them:
// neither hosts a session, and neither is a guess.
func linuxChange(f linux.Focus) Change {
	switch {
	case f.Unknown != "":
		return Change{At: f.At, Unknown: f.Unknown}
	case f.Window == 0:
		return Change{At: f.At, Class: event.AppNone}
	case f.PID <= 0:
		return Change{At: f.At, Class: event.AppOther}
	}
	return Change{At: f.At, PID: f.PID}
}
