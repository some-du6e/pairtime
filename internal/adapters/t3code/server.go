// SPDX-License-Identifier: AGPL-3.0-or-later

package t3code

import "github.com/some-du6e/pairtime/internal/platform"

// ServerIn reports whether procs, one read of the process table on goos as
// the agent's process scan reads it, holds T3 Code's server running for the
// user whose processes the scan reports, self (procs.Options.Self): a
// process of the app table's app_server role, which T3's own t3 executable
// and its desktop app, which runs the server as itself, have
// (internal/platform's appclass.json). On Linux and macOS it must run as
// self; on Windows it may run in any session, since T3's scheduled service
// can run outside the user's own. A scan that failed has no table, and
// finds none.
//
// With the -wal and -shm files of the store a read picks, that is what makes
// T3 run (Options.Server, Status.Running): the files alone stay behind when
// T3 is killed while it holds them (DESIGN 5.1). A T3 run from its source,
// as node, isn't named, so it gets no project context, though its messages
// are still read when a prompt's hook asks and at the agent's idle cadence.
func ServerIn(goos string, procs []platform.Proc, self string) bool {
	for _, p := range procs {
		e, ok := platform.Identify(goos, p)
		if !ok || e.Role != platform.RoleAppServer {
			continue
		}
		if goos == platform.Windows || self != "" && p.Owner == self {
			return true
		}
	}
	return false
}
