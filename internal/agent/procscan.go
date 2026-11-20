// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"github.com/some-du6e/pairtime/internal/agent/procs"
)

// startProcs starts the harness process scan (DESIGN 3.3, WP-OS6) and
// returns the step that stops it. Until the ledger writer exists
// (WP-LG5), a session the scan finds goes to the log; the hook path
// (WP-AG3) will claim the processes its hooks come from through a.procs.
func (a *agent) startProcs() func() {
	a.procs = procs.New(procs.Options{
		Log: a.log,
		Report: func(s procs.Session) {
			a.log.Info("found a harness session that fires no hooks",
				"key", s.Key, "harness", s.Harness, "parent_class", s.Event.ParentClass,
				"entrypoint", s.Event.Entrypoint, "has_tty", s.Event.HasTTY)
		},
		Scanned: func(err error) {
			if err != nil {
				a.setProblem("procs", "the harness process scan failed: "+err.Error())
			} else {
				a.setProblem("procs", "")
			}
		},
	})
	return a.procs.Start(a.sched)
}
