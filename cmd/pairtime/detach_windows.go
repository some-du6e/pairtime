// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"os/exec"
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

// startDetached starts the command newCmd makes as detachedAttr says, and
// outside the job object this process is in when the job lets it out
// (CREATE_BREAKAWAY_FROM_JOB). Every process left in a job whose limits
// include JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE ends when the job's last
// handle closes, as when the program that made the job exits, so an agent
// that a hook started inside such a job would end with whatever runs the
// hook. The job libuv puts a Node program's children in lets their own
// children out silently (JOB_OBJECT_LIMIT_SILENT_BREAKAWAY_OK), so the
// agent was already out of that one; a job with JOB_OBJECT_LIMIT_BREAKAWAY_OK
// alone lets out only a process started with the flag. Outside any job the
// flag does nothing.
//
// A job that lets no process out makes CreateProcess fail with
// ERROR_ACCESS_DENIED. Then, or on any other failure of the start with
// the flag, the command starts again without it, inside the job, as it did
// before the flag: an agent that ends with the job beats no agent.
func startDetached(newCmd func() *exec.Cmd) (*exec.Cmd, error) {
	cmd := newCmd()
	cmd.SysProcAttr = detachedAttr()
	cmd.SysProcAttr.CreationFlags |= windows.CREATE_BREAKAWAY_FROM_JOB
	if err := cmd.Start(); err == nil {
		return cmd, nil
	}
	// A Cmd starts only once, even when its start failed.
	cmd = newCmd()
	cmd.SysProcAttr = detachedAttr()
	return cmd, cmd.Start()
}
