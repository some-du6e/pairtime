// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build darwin

package agent

import (
	"github.com/some-du6e/pairtime/internal/agent/focus"
	"github.com/some-du6e/pairtime/internal/agent/presence"
	"github.com/some-du6e/pairtime/internal/platform"
	"github.com/some-du6e/pairtime/internal/platform/darwin"
)

// openOS builds the macOS sources (WP-OS9): presence samples idle time,
// key-down idle time and the lock state with darwin.Presence, which any
// thread may call, and takes suspend, resume, lock and unlock from the
// MacLoop in opts.Mac, which also gives focus each app activation. Without
// a MacLoop presence still samples, but no power event or app switch
// arrives, and GET /v1/status says so.
func (a *agent) openOS(opts Options) osSources {
	m := opts.Mac
	if m == nil {
		// TODO(D-focus-unknown): focus is unknown here; record an os.focus
		// with app_class unknown once pkg/event has it (WP-EV2).
		return osSources{
			presence: presence.MacOS(darwin.Presence, nil),
			focus:    focus.OS{Unknown: noMacLoop, Read: darwin.Processes, GOOS: platform.Darwin},
			signals:  m.signals,
		}
	}
	return osSources{
		presence: presence.MacOS(darwin.Presence, &m.power),
		focus:    focus.MacOS(&m.focus, darwin.Processes),
		signals:  m.signals,
	}
}
