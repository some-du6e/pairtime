// SPDX-License-Identifier: AGPL-3.0-or-later

package t3code

import (
	"os"
	"os/exec"
	"syscall"
)

const (
	createNoWindow           = 0x08000000 // CREATE_NO_WINDOW
	belowNormalPriorityClass = 0x00004000 // BELOW_NORMAL_PRIORITY_CLASS
)

// prepare keeps sqlite3 from opening a console window, which the agent,
// running without one, would otherwise flash on screen for every read, and
// starts it below normal priority, since the reader's reads never need to
// beat what the person is doing.
func prepare(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: createNoWindow | belowNormalPriorityClass,
	}
}

// lower does nothing on Windows, where prepare set the priority at start.
func lower(*os.Process) {}
