// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build unix

package main

import "syscall"

// detachedAttr starts the agent in a session of its own, without a
// controlling terminal, so closing the terminal it was started from
// doesn't stop it.
func detachedAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true}
}
