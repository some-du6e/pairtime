// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build linux

package presence

import (
	"context"
	"errors"

	"github.com/some-du6e/pairtime/internal/platform/linux"
)

// Desktop is what presence reads of a Linux desktop: the agent's
// *linux.Desktop, which it opens once for presence and focus both, or a
// fake in tests.
type Desktop interface {
	Status() linux.Status
	Idle(ctx context.Context) (linux.Idle, error)
	Locked() (bool, error)
	Power() <-chan linux.Power
}

// LinuxOS returns a Linux desktop's sources (DESIGN 3.3; PLAN WP-OS9): the
// idle time from d's Idle, which is the X server's MIT-SCREEN-SAVER counter
// or GNOME's Mutter IdleMonitor, the lock state from logind's LockedHint
// through d's Locked, and suspend, resume, lock and unlock from logind
// through d's Power. Linux gives no key-down idle time without a key
// counter, so idle_keys_s stays null (SM 1.2).
//
// Where d's Status says the desktop gives no idle time, as on KDE and
// wlroots and with no graphical session, Idle is nil: presence is unknown
// there (SM 2.1), so the sampler records nothing, and Status says why.
//
// LinuxOS reads d's Power on a goroutine of its own until d is closed, so
// every suspend lets logind's delay lock go whether or not a sampler
// watches: each event goes to the watch that wants it, and a suspend's
// Ready is called once that call has returned, after the sampler has
// sealed the open batch (DESIGN 3.3). A sampler that isn't running, as on
// a desktop with no idle time, holds no sleep up.
func LinuxOS(d Desktop) OS {
	var o OS
	if d.Status().Idle.Known() {
		o.Idle = func() (Idle, error) {
			i, err := d.Idle(context.Background())
			if err != nil {
				return Idle{}, err
			}
			return Idle{Any: i.Any}, nil
		}
	}
	o.Locked = func() (bool, error) {
		locked, err := d.Locked()
		if errors.Is(err, linux.ErrLockUnknown) {
			// Status says why, and the sampler takes the session as
			// unlocked until logind reports a lock.
			return false, nil
		}
		return locked, err
	}
	if ch := d.Power(); ch != nil {
		f := &feed{}
		f.watches(&o)
		go runPower(f, ch)
	}
	return o
}

// runPower passes each event on ch to f's watches, and lets a suspend go
// ahead once the sampler has handled it, until ch closes. Ready does
// nothing for the other ops.
func runPower(f *feed, ch <-chan linux.Power) {
	for p := range ch {
		f.pass(p.Op)
		if p.Ready != nil {
			p.Ready()
		}
	}
}
