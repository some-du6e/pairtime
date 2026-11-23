// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build windows

package focus

import (
	"time"

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

// noWindowSettle is how long a moment with no foreground window waits
// before it counts as focus pairtime can't see, so that a switch, which
// passes through one, costs one os.focus record (settler). It is X11's
// focusSettle (internal/platform/linux).
const noWindowSettle = 50 * time.Millisecond

func watchWindows(fn func(Change)) (func(), error) {
	s := &settler{fn: fn, wait: noWindowSettle}
	stop, err := windows.WatchForeground(func(f windows.Foreground) {
		s.pass(changeOf(f), f.Unknown == windows.UnknownNoWindow)
	})
	if err != nil {
		return nil, err
	}
	return func() {
		stop()
		s.stop()
	}, nil
}

// changeOf turns the watch's report into a Change. A report with no window
// is focus pairtime can't name: a secure desktop, a window that closed at
// once, or a moment between two windows (WP-OS2 decision 2). The component
// records it as an os.focus with app_class unknown (SM 2.5).
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
