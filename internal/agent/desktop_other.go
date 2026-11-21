// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build !windows && !darwin && !linux

package agent

import (
	"github.com/some-du6e/pairtime/internal/agent/focus"
	"github.com/some-du6e/pairtime/internal/agent/presence"
)

// openOS has no sources to open on an OS pairtime doesn't ship for, so
// every slot's presence is unknown (SM 2.1).
func (a *agent) openOS(Options) osSources {
	return osSources{
		presence: presence.ThisOS(),
		focus:    focus.ThisOS(),
		signals:  func() signalsReply { return a.componentSignals("", "", "") },
	}
}
