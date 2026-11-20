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
// arrives, and GET /v1/status says so. Power and lock are named only while
// the sampler watches them (watched).
func (a *agent) openOS(opts Options) osSources {
	m := opts.Mac
	signals := func() signalsReply { return a.watched(m.signals()) }
	if m == nil {
		// TODO(WP-OS15): focus is unknown here, and nothing records that
		// yet. WP-OS15 records an os.focus with app_class unknown when
		// watching starts with no source, once WP-EV2 adds the value
		// (D-focus-unknown).
		return osSources{
			presence: presence.MacOS(darwin.Presence, nil),
			focus:    focus.OS{Unknown: noMacLoop, Read: darwin.Processes, GOOS: platform.Darwin},
			signals:  signals,
		}
	}
	return osSources{
		presence: presence.MacOS(darwin.Presence, &m.power),
		focus:    focus.MacOS(&m.focus, darwin.Processes),
		signals:  signals,
	}
}
