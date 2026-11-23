// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"github.com/some-du6e/pairtime/internal/agent/procs"
	"github.com/some-du6e/pairtime/internal/platform"
)

// startProcs starts the harness process scan (DESIGN 3.3, WP-OS6) and
// returns the step that stops it. startPresence must have run, because
// every scan tells presence how many harness session processes run.
func (a *agent) startProcs(opts Options) func() {
	a.procs = procs.New(a.procsOptions(procs.Options{Read: opts.Procs}))
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
//   - After every scan the focus component looks again at the window in
//     front, which may host a session the scan just found (focus.go), and
//     forgets the hooked sessions of processes the scan saw exit
//     (hookTies.prune).
//   - Until the ledger writer exists (WP-LG5), a session the scan finds,
//     or withdraws because a hook claimed it late, goes to the log. The
//     hook sink (hooks.go, WP-AG3) claims the harness process each hook
//     came from, as the agent's own walk of the connection found it.
//   - Every scan's process table goes to the apps component, which looks
//     there for T3 Code's server: with its store's -wal and -shm files,
//     that is what makes T3 run, for the reader's cadence, presence's
//     project context and doctor (DESIGN 3.3; apps.go). A scan that failed
//     finds none.
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
	opts.Running = func(n int) {
		a.presence.HarnessProcesses(n)
		if a.procs != nil {
			a.hooked.prune(a.procs.Claimed)
		}
		a.refreshFocus()
	}
	opts.Scanned = func(err error) {
		if err != nil {
			a.setProblem("procs", "the harness process scan failed: "+err.Error())
		} else {
			a.setProblem("procs", "")
		}
	}
	opts.Listed = func(goos string, ps []platform.Proc, self string) { a.appsHost().noteServer(goos, ps, self) }
	return opts
}
