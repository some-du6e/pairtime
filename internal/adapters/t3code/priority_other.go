// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build !windows

package t3code

import (
	"os"
	"os/exec"
	"syscall"
)

// lowestNice is the nicest a process can be.
const lowestNice = 19

// prepare does nothing outside Windows, where no console window opens and
// lower sets the priority.
func prepare(*exec.Cmd) {}

// lower puts sqlite3 at nice 19, since the reader's reads never need to
// beat what the person is doing. It runs before sqlite3 reads its SQL. A
// refusal leaves the read at the agent's own priority, which is still a
// few milliseconds of work.
func lower(p *os.Process) {
	if p != nil {
		_ = syscall.Setpriority(syscall.PRIO_PROCESS, p.Pid, lowestNice)
	}
}
