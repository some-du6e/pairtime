// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"github.com/some-du6e/pairtime/internal/agent/presence"
)

// startPresence starts the presence and power component (WP-OS1) and
// returns the step that stops it. It records nothing until a project
// context goes live, and nothing makes one live yet: focus (WP-OS2), the
// process scanner (WP-OS6) and the hook path (WP-AG3) report to a.presence
// once they exist, and the ledger writer (WP-LG5) takes its records and
// its seal signal.
func (a *agent) startPresence() func() {
	a.presence = presence.New(presence.Options{
		Sched:   a.sched,
		OS:      presence.ThisOS(),
		Problem: func(msg string) { a.setProblem("presence", msg) },
	})
	a.presence.Start()
	return a.presence.Stop
}
