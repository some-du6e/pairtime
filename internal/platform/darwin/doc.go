// SPDX-License-Identifier: AGPL-3.0-or-later

// Package darwin reads what macOS says about the person at the machine:
// idle time for presence, sleep, wake, screen lock and unlock for power,
// and the frontmost app for focus (DESIGN 3.3; CP 2.2, 3.3; WP-OS3). It
// calls Apple's frameworks through purego, so it builds with
// CGO_ENABLED=0 for darwin/amd64 and darwin/arm64 (DESIGN 3.12).
//
// # Presence
//
// Presence takes one os.presence sample. idle_any_s comes from
// CGEventSourceSecondsSinceLastEventType with kCGAnyInputEventType, and
// idle_keys_s from the same call with kCGEventKeyDown (SM 1.2), both for
// the HID system state, which is hardware input rather than events other
// apps post into the session. When CoreGraphics can't answer, idle_any_s
// falls back to IOKit's HIDIdleTime, read from the IOHIDSystem service
// without spawning ioreg, and idle_keys_s is null. locked comes from the
// session dictionary of CGSessionCopyCurrentDictionary. Each sample is a
// few calls into the OS with no thread or timer of its own, so the agent's
// scheduler decides how often it runs (DESIGN 3.3, D14).
//
// Whether the key-down variant needs the Input Monitoring permission is
// open (SM 1.2, DESIGN 5.7). The package reports what the call returns and
// never asks for the permission; the macOS test in platform.yml records
// the answer on macos-latest as an artifact (finding_darwin_test.go).
//
// # Power and focus
//
// A Loop runs one CFRunLoop on one locked OS thread and turns three kinds
// of notification into Handler calls:
//
//   - IOKit sleep and wake, from IORegisterForSystemPower (CP 2.2):
//     kIOMessageSystemWillSleep is os.power suspend, acknowledged with
//     IOAllowPowerChange once the handler has returned or SuspendWait has
//     passed, so the agent can seal a batch first (DESIGN 3.3), and
//     kIOMessageSystemHasPoweredOn is resume.
//   - Screen lock and unlock, from the com.apple.screenIsLocked and
//     com.apple.screenIsUnlocked distributed notifications.
//   - The frontmost app, from NSWorkspace's
//     NSWorkspaceDidActivateApplicationNotification (CP 3.3).
//
// Nothing here polls: between notifications the thread sleeps in the run
// loop, and the run loop has no timer of its own. There is no keyboard
// listener and no event tap of any kind (DESIGN 6.2 D16, 7.2).
//
// macOS delivers distributed notifications on the main thread, and
// NSWorkspace updates the frontmost app from the main run loop (Apple's
// Notification Programming Topics; the NSWorkspace and NSRunningApplication
// references). On macos-latest a distributed notification never reached a
// Loop off the main thread (TestFindingRunLoop). So the Loop has to run on
// the process's main thread to see locks and app switches. The agent has to
// arrange that: runtime.LockOSThread in an init function of its main
// package keeps the main goroutine on the main thread, and the main
// goroutine then calls Loop.Run while the rest of the agent runs elsewhere.
// IOKit's messages arrive on whatever thread runs the loop, so off the main
// thread a Loop still reports sleep and wake, and its Status says that locks
// and app switches may not arrive.
//
// # Focus granularity
//
// NSWorkspace reports app activations. A switch between two windows or
// two tabs of the same app, such as two iTerm2 windows, activates no app,
// so it produces no event: on macOS, focus is known per app, not per
// window (WP-FX2's finding). Window titles are never read.
//
// # What stays on the device
//
// An App's bundle id and executable path identify software on this
// device. They are content (AGENTS.md): the agent turns them into an
// app_class and a keyed app_hash for os.focus, and neither the App nor a
// plain hash of it leaves the device.
package darwin
