// SPDX-License-Identifier: AGPL-3.0-or-later

package hookpath

import "time"

// Stamp is when a hook call came in, by the wall clock and by the OS's
// monotonic clock: an event's t, mono and boot_id (SM 1).
type Stamp struct {
	// Wall is the wall-clock time in UTC, without Go's own monotonic
	// reading, which means nothing outside this process.
	Wall time.Time
	// Mono is the OS's monotonic clock in milliseconds, which counts from
	// an arbitrary point in the boot BootID names and goes on counting
	// while the machine sleeps, so the difference between two readings in
	// one boot is the real time between them, whatever the wall clock did
	// (DESIGN 7.4). Mono is 0 and BootID "" when this OS gives neither.
	Mono   int64
	BootID string
}

// Now reads the boot id and both clocks, the two clocks back to back.
//
// The boot id is Linux's boot_id, macOS's kern.bootsessionuuid, and on
// Windows the time the boot began by SystemTimeOfDayInformation, less the
// steps the wall clock has taken since, as hex. The monotonic clock is
// CLOCK_BOOTTIME on Linux, CLOCK_MONOTONIC on macOS and GetTickCount64 on
// Windows, the three that go on counting during sleep.
func Now() Stamp {
	mono, boot := monoNow()
	if mono <= 0 || !validBootID(boot) {
		mono, boot = 0, ""
	}
	return Stamp{Wall: time.Now().UTC(), Mono: mono, BootID: boot}
}

// validBootID reports whether id is a boot id the inbox takes: 1 to 128
// letters, digits, '_' and '-' (inbox.Message.Check).
func validBootID(id string) bool {
	if len(id) == 0 || len(id) > 128 {
		return false
	}
	for i := range len(id) {
		switch c := id[i]; {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '_', c == '-':
		default:
			return false
		}
	}
	return true
}
