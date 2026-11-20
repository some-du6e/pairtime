// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"github.com/some-du6e/pairtime/internal/agent/procs"
)

// startProcs starts the harness process scan (DESIGN 3.3, WP-OS6) and
// returns the step that stops it. startPresence must have run, because
// every scan tells presence how many harness session processes run.
func (a *agent) startProcs() func() {
	a.procs = procs.New(a.procsOptions(procs.Options{}))
	return a.procs.Start(a.sched)
}

// procsOptions adds to opts, in which tests give the scan a process table
// of their own, what the agent wires the scan to.
//
//   - What the scan remembers lives in state.json, so a restarted agent
//     doesn't report again what it reported or what hooks claimed.
//   - Its count of this user's harness session processes goes to
//     presence's HarnessProcesses after every scan: while one runs, a
//     project context is live (DESIGN 3.3), including for a hookless
//     `claude --bare` session that nothing else would show.
//   - Until the ledger writer exists (WP-LG5), a session the scan finds,
//     or withdraws because a hook claimed it late, goes to the log. The
//     hook path (WP-AG3) will claim the processes its hooks come from
//     through a.procs.
func (a *agent) procsOptions(opts procs.Options) procs.Options {
	opts.Log = a.log
	opts.Store = a.state
	opts.Report = func(s procs.Session) {
		a.log.Info("found a harness session that fires no hooks",
			"key", s.Key, "harness", s.Harness, "parent_class", s.Event.ParentClass,
			"entrypoint", s.Event.Entrypoint, "has_tty", s.Event.HasTTY)
	}
	opts.Withdraw = func(s procs.Session) {
		a.log.Info("a harness session the scan found fires hooks after all",
			"key", s.Key, "harness", s.Harness)
	}
	opts.Running = a.presence.HarnessProcesses
	opts.Scanned = func(err error) {
		if err != nil {
			a.setProblem("procs", "the harness process scan failed: "+err.Error())
		} else {
			a.setProblem("procs", "")
		}
	}
	return opts
}
