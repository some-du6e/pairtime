// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build !windows

package main

import "syscall"

// lookbackAttr starts lookback's reader in a session of its own: it lowers
// its own priority first thing (lowerPriority), and on Linux its session's
// autogroup with it, which mustn't be the agent's.
func lookbackAttr() *syscall.SysProcAttr { return &syscall.SysProcAttr{Setsid: true} }
