// SPDX-License-Identifier: AGPL-3.0-or-later

package git

import (
	"os/exec"
	"syscall"
)

const (
	createNoWindow           = 0x08000000 // CREATE_NO_WINDOW
	belowNormalPriorityClass = 0x00004000 // BELOW_NORMAL_PRIORITY_CLASS
)

// hideWindow keeps git from opening a console window: the agent runs
// without one, so each git.exe would otherwise flash a window on screen.
// git runs below normal priority, because the adapter's reads never need
// to beat what the person is doing.
func hideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: createNoWindow | belowNormalPriorityClass,
	}
}
