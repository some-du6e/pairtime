// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build unix

package main

import (
	"os/exec"
	"syscall"
)

// detachedAttr starts the agent in a session of its own, without a
// controlling terminal, so closing the terminal it was started from
// doesn't stop it.
func detachedAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true}
}

// startDetached starts the command newCmd makes in a session of its own
// (detachedAttr). That is all a Unix but Linux needs. On macOS, launchd
// kills what is left of a job's process group when the job exits, unless
// the job sets AbandonProcessGroup, and a closing terminal hangs up its
// session; a new session is a new process group, with no terminal, so
// neither reaches the agent, and macOS has nothing like a cgroup that
// ends a whole tree of processes. On Linux a systemd scope can, which is
// what startInUnit is for.
func startDetached(newCmd func() *exec.Cmd) (*exec.Cmd, error) {
	cmd := newCmd()
	cmd.SysProcAttr = detachedAttr()
	return cmd, cmd.Start()
}
