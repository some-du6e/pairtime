// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"github.com/some-du6e/pairtime/internal/agent/presence"
)

// startPresence starts the presence and power component (WP-OS1) and
// returns the step that stops it. It records nothing until a project
// context goes live. The process scan makes one live by reporting its
// count of harness session processes through HarnessProcesses (procscan.go,
// WP-OS6), and so does every hook call that comes over the socket or the
// pipe, which hooks.go reports through Hook (WP-AG3). docs/PLAN.md names who
// wires the rest: WP-OS2 reports focus changes through Focus, WP-AD18 track
// messages through Hook, WP-LG5 sets Record and Seal, and WP-AG4 passes the
// parameter table it scores with as Params. Until then records are dropped
// and the sampler reads the newest table.
func (a *agent) startPresence() func() {
	a.presence = presence.New(presence.Options{
		Sched:   a.sched,
		OS:      presence.ThisOS(),
		Problem: func(msg string) { a.setProblem("presence", msg) },
	})
	a.presence.Start()
	return a.presence.Stop
}
