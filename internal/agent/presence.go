// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"github.com/some-du6e/pairtime/internal/agent/presence"
)

// startPresence starts the presence and power component (WP-OS1) and
// returns the step that stops it. It records nothing until a project
// context goes live, and nothing makes one live yet. docs/PLAN.md names
// who wires the rest: WP-OS2 reports focus changes through Focus, WP-OS6
// the process scanner's count through HarnessProcesses, and WP-AG3 every
// hook and track message through Hook; WP-LG5 sets Record and Seal, and
// WP-AG4 passes the parameter table it scores with as Params. Until then
// records are dropped and the sampler reads the newest table.
func (a *agent) startPresence() func() {
	a.presence = presence.New(presence.Options{
		Sched:   a.sched,
		OS:      presence.ThisOS(),
		Problem: func(msg string) { a.setProblem("presence", msg) },
	})
	a.presence.Start()
	return a.presence.Stop
}
