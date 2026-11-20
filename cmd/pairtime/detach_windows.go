// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"syscall"

	"golang.org/x/sys/windows"
)

// detachedAttr starts the agent with CREATE_NO_WINDOW, so it gets a
// console that has no window. A console program the agent runs later, such
// as git, shares that console and opens no window either, which it would
// if the agent had no console at all (DETACHED_PROCESS; Windows ignores
// CREATE_NO_WINDOW beside it). A process group of its own keeps a Ctrl+C
// in the console of whoever ran `agent --detach` away from the agent.
func detachedAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{CreationFlags: windows.CREATE_NO_WINDOW | windows.CREATE_NEW_PROCESS_GROUP}
}
