// SPDX-License-Identifier: AGPL-3.0-or-later

// Package localtime reads the time zone the computer is set to now. Go
// reads it once per process, into time.Local, and the agent runs from
// login to logout while a laptop sleeps through a flight or a teen changes
// the zone in Settings. So the agent's clock times that follow the
// computer's clock, such as quiet hours, come from Now instead of
// time.Now.
package localtime

import "time"

// Now returns the time now, in the time zone the computer is set to at
// this moment. It reads the zone each time, which costs a file read or a
// system call, so it is for things that happen now and then, such as the
// notification at the end of a work session, not for tight loops.
func Now() time.Time {
	return time.Now().In(Zone())
}
