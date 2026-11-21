// SPDX-License-Identifier: AGPL-3.0-or-later

package focus

import (
	"sync"
	"time"

	"github.com/some-du6e/pairtime/internal/platform"
	"github.com/some-du6e/pairtime/internal/platform/darwin"
)

// MacVia is the focus source on macOS, as Status names it.
const MacVia = "NSWorkspace app activations, per app: macOS reports no switch between two windows of one app"

// MacFocus carries the app activations of the agent's darwin.Loop to a
// Watcher (WP-OS9). macOS delivers them only on the process's main thread,
// so the Loop runs there, started by cmd/pairtime before the agent and
// stopped after it (internal/platform/darwin). Its handler calls Focus with
// every activation, the first for the app in front as the Loop starts, and
// the Watcher takes them through the Watch MacOS gives it. A Watch that
// starts after the Loop gets the latest activation first. The zero value
// is ready to use. This file builds on every OS, so ci.yml tests it with
// fakes, as it does the darwin package's own logic.
type MacFocus struct {
	mu   sync.Mutex
	fn   func(Change)
	last *Change
}

// Focus is the focus half of the Loop's darwin.Handler: it passes the
// activation of app, which came at at, to the Watcher as a Change. macOS
// knows focus per app, not per window, so a switch between two windows of
// one app is no Change (the darwin package doc). The app's bundle id and
// executable path stay here: the Watcher finds the app in the process
// table by its process id, as on the other OSes.
//
// Focus calls the Watcher with its lock held, which keeps changes in the
// order they came even when a Watch starts meanwhile. The Watcher's
// callback never blocks (OS.Watch).
func (f *MacFocus) Focus(at time.Time, app darwin.App) {
	c := Change{At: monotonic(at), PID: app.PID}
	if app.PID <= 0 {
		c = Change{At: c.At, Unknown: "NSWorkspace named no process for the app in front"}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.last = &c
	if f.fn != nil {
		f.fn(c)
	}
}

// Watch is MacOS's OS.Watch. It calls fn with the latest activation at
// once, when there has been one, and then with each one until stop.
func (f *MacFocus) Watch(fn func(Change)) (func(), error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fn = fn
	if f.last != nil {
		fn(*f.last)
	}
	return func() {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.fn = nil
	}, nil
}

// MacOS returns macOS's focus source (DESIGN 3.3): the activations f
// carries, and the process table from read, which is darwin.Processes in
// the agent.
func MacOS(f *MacFocus, read func() ([]platform.Proc, error)) OS {
	return OS{Via: MacVia, Watch: f.Watch, Read: read, GOOS: platform.Darwin}
}

// monotonic gives at, a wall-clock time from the Loop, which carries no
// monotonic reading so that gaps count time asleep (CP 2.2), the reading
// of this process's monotonic clock it would have had, as a Change's time
// from time.Now does. Its wall clock is unchanged.
func monotonic(at time.Time) time.Time {
	now := time.Now()
	return now.Add(at.Sub(now))
}
