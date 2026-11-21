// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build windows

package agent

import (
	"github.com/some-du6e/pairtime/internal/agent/focus"
	"github.com/some-du6e/pairtime/internal/agent/presence"
)

// openOS gives presence and focus their own Windows sources (WP-OS1,
// WP-OS2), whose names GET /v1/status reports once each runs.
func (a *agent) openOS(Options) osSources {
	return osSources{
		presence: presence.ThisOS(),
		focus:    focus.ThisOS(),
		signals: func() signalsReply {
			return a.componentSignals("GetLastInputInfo", "PowerRegisterSuspendResumeNotification", "WTSRegisterSessionNotification")
		},
	}
}
