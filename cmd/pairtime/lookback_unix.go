// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build !windows

package main

import "syscall"

// lookbackAttr starts lookback's reader as any child: it lowers its own
// priority first thing (lowerPriority).
func lookbackAttr() *syscall.SysProcAttr { return nil }
