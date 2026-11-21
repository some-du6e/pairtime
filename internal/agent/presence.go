// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"github.com/some-du6e/pairtime/internal/agent/presence"
)

// startPresence starts the presence and power component (WP-OS1) and
// returns the step that stops it. It records nothing until a project
// context goes live. The process scan makes one live by reporting its
// count of harness session processes through HarnessProcesses (procscan.go,
// WP-OS6), and so do focus on a terminal, editor or agent app, which the
// focus component reports through FocusAt (focus.go, WP-OS2), and every hook
// call that comes over the socket or the pipe, which hooks.go reports
// through Hook (WP-AG3). docs/PLAN.md names who wires the rest: WP-AD18
// each track message through Hook (its entry's "Left by WP-AG3" line,
// since track messages came after WP-AG3). It gets the parameter table the
// scorer runs with as Params, so it slows down where the scorer expects it
// to (WP-AG4; DESIGN 6.2 D14). Its records go to the ledger, which seals a
// batch after every suspend and lock (startLedger, WP-LG5), and once it is
// sealed the uploader sends at once (WP-AG5), so startLedger must run
// first. src is the OS's sources, from openOS, or a fake in tests.
func (a *agent) startPresence(src presence.OS) func() {
	a.presence = presence.New(presence.Options{
		Sched:   a.sched,
		Params:  a.table,
		OS:      src,
		Record:  a.ledgerIn.Record,
		Seal:    a.sealAndUpload,
		Problem: func(msg string) { a.setProblem("presence", msg) },
	})
	a.presence.Start()
	return a.presence.Stop
}
