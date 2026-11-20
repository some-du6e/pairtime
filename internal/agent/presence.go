// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"github.com/some-du6e/pairtime/internal/agent/presence"
)

// startPresence starts the presence and power component (WP-OS1) and
// returns the step that stops it. It records nothing until a project
// context goes live. So far only the process scan makes one live, by
// reporting its count of harness session processes through
// HarnessProcesses (procscan.go, WP-OS6). docs/PLAN.md names who wires the
// rest: WP-OS2 reports focus changes through Focus, and WP-AG3 every hook
// and track message through Hook; WP-AG4 passes the parameter table it
// scores with as Params, and until then the sampler reads the newest
// table. Its records go to the ledger, which seals a batch after every
// suspend and lock (startLedger, WP-LG5), so startLedger must run first.
func (a *agent) startPresence() func() {
	a.presence = presence.New(presence.Options{
		Sched:   a.sched,
		OS:      presence.ThisOS(),
		Record:  a.ledgerIn.Record,
		Seal:    a.ledgerIn.Seal,
		Problem: func(msg string) { a.setProblem("presence", msg) },
	})
	a.presence.Start()
	return a.presence.Stop
}
