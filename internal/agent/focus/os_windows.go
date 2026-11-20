// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build windows

package focus

import (
	"github.com/some-du6e/pairtime/internal/platform"
	"github.com/some-du6e/pairtime/internal/platform/windows"
)

// thisOS reads Windows: SetWinEventHook(EVENT_SYSTEM_FOREGROUND) for the
// window in front and its process, and NtQuerySystemInformation's process
// table for the walk (DESIGN 3.3; CP 3.3).
func thisOS() OS {
	return OS{
		Via:   "SetWinEventHook(EVENT_SYSTEM_FOREGROUND)",
		Watch: watchWindows,
		Read:  windows.Processes,
		GOOS:  platform.Windows,
	}
}

func watchWindows(fn func(Change)) (func(), error) {
	return windows.WatchForeground(func(f windows.Foreground) {
		fn(changeOf(f))
	})
}

// changeOf turns the watch's report into a Change. A report with no window
// is focus pairtime can't name: a secure desktop, a window that closed at
// once, or a moment between two windows (WP-OS2 decision 2).
func changeOf(f windows.Foreground) Change {
	if f.Unknown != "" || f.PID == 0 {
		reason := f.Unknown
		if reason == "" {
			reason = windows.UnknownNoProcess
		}
		return Change{At: f.At, Unknown: reason}
	}
	return Change{At: f.At, PID: f.PID}
}
