// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build windows

package presence

import (
	"github.com/some-du6e/pairtime/internal/platform/windows"
)

// thisOS reads Windows: GetLastInputInfo for the idle time, with no key
// idle time, since that would take a keyboard listener; WTS for the lock
// state; PowerRegisterSuspendResumeNotification for suspend and resume
// (DESIGN 3.3).
func thisOS() OS {
	return OS{
		Idle: func() (Idle, error) {
			d, err := windows.IdleTime()
			return Idle{Any: d}, err
		},
		Locked:       windows.SessionLocked,
		WatchSuspend: windows.WatchSuspend,
		WatchLock:    windows.WatchLock,
	}
}
