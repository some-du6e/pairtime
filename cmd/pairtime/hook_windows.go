// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/some-du6e/pairtime/internal/agent/home"
)

// TOKEN_ELEVATION_TYPE's values (GetTokenInformation, TokenElevationType).
const (
	// tokenElevationDefault is the token of a user without a split token:
	// UAC is off, or the user is no administrator, or is the built-in
	// Administrator, whom Admin Approval Mode leaves out by default.
	tokenElevationDefault = 1
	// tokenElevationFull is the elevated half of a split token, as in a
	// terminal started with "Run as administrator".
	tokenElevationFull = 2
	// tokenElevationLimited is the filtered half, what the user runs with.
	tokenElevationLimited = 3
)

// mayWriteData reports whether this hook may make and write pairtime's
// files in d. On Windows the data directory's DACL lets the user in, and
// an elevated token is still that user's, so a spooled call stays the
// user's to drain.
func mayWriteData(home.Dirs) bool { return true }

// mayStartAgent reports whether this hook may start the agent: not with
// the elevated half of a split token, which the agent would keep for the
// rest of the session, while DESIGN 3.2 has it run without admin. A token
// that isn't split is the only one the user has, and the login item starts
// the agent with it too.
func mayStartAgent(home.Dirs) bool { return startsWith(elevationType()) }

// startsWith reports whether a hook whose token has elevation type t may
// start the agent.
func startsWith(t uint32) bool { return t != tokenElevationFull }

// elevationType returns this process's TOKEN_ELEVATION_TYPE, or
// tokenElevationDefault when Windows won't say.
func elevationType() uint32 {
	var t, n uint32
	err := windows.GetTokenInformation(windows.GetCurrentProcessToken(), windows.TokenElevationType, (*byte)(unsafe.Pointer(&t)), uint32(unsafe.Sizeof(t)), &n)
	if err != nil || n != uint32(unsafe.Sizeof(t)) {
		return tokenElevationDefault
	}
	return t
}
