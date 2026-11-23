// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"syscall"

	"golang.org/x/sys/windows"

	platwin "github.com/some-du6e/pairtime/internal/platform/windows"
)

// lowerPriority puts lookback's reader in background processing mode
// (platwin.Background).
func lowerPriority() error { return platwin.Background() }

// lookbackAttr starts lookback's reader with no console window, as the
// agent, which has none, would otherwise give it one, and in the idle
// priority class until it enters background mode.
func lookbackAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW | windows.IDLE_PRIORITY_CLASS}
}
