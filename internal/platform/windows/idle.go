// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build windows

// Package windows is pairtime's Win32 code. It calls Win32 through
// golang.org/x/sys/windows, loading what that module doesn't wrap from the
// system directory, so the client builds without cgo (DESIGN 3.12).
//
// This file and the others from WP-OS1 give presence what it reads: input
// idle time, suspend and resume, session lock and unlock, and EcoQoS for
// the agent's process (DESIGN 3.2, 3.3). There is no keyboard listener or
// low-level hook of any kind: DESIGN keeps one out until the binaries are
// signed (7.2 item 5; 11 risk 18).
package windows

import (
	"fmt"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	modKernel32 = windows.NewLazySystemDLL("kernel32.dll")
	modUser32   = windows.NewLazySystemDLL("user32.dll")
	modPowrprof = windows.NewLazySystemDLL("powrprof.dll")
	modWtsapi32 = windows.NewLazySystemDLL("wtsapi32.dll")

	procGetLastInputInfo = modUser32.NewProc("GetLastInputInfo")
	procGetTickCount     = modKernel32.NewProc("GetTickCount")
)

// lastInputInfo is LASTINPUTINFO.
type lastInputInfo struct {
	cbSize uint32
	dwTime uint32
}

// IdleTime returns the time since the last keyboard or mouse input in the
// calling process's session, from GetLastInputInfo and the tick count
// (CP 3.3). It is one user32 call and needs no permission. Windows can't
// tell keys from other input without a keyboard listener, so this is
// os.presence's idle_any_s only, and idle_keys_s stays null (SM 1.2).
func IdleTime() (time.Duration, error) {
	if err := procGetLastInputInfo.Find(); err != nil {
		return 0, err
	}
	if err := procGetTickCount.Find(); err != nil {
		return 0, err
	}
	info := lastInputInfo{cbSize: uint32(unsafe.Sizeof(lastInputInfo{}))}
	r, _, err := procGetLastInputInfo.Call(uintptr(unsafe.Pointer(&info)))
	if r == 0 {
		return 0, fmt.Errorf("GetLastInputInfo: %w", err)
	}
	// After GetLastInputInfo, so input arriving between the two calls
	// makes the idle time smaller, never negative.
	tick, _, _ := procGetTickCount.Call()
	return time.Duration(idleMillis(uint32(tick), info.dwTime)) * time.Millisecond, nil
}

// idleMillis returns the milliseconds from the last input's tick to the
// current tick. Both are GetTickCount values, which wrap to zero every
// 49.7 days, so the difference is taken in 32-bit arithmetic and is right
// across a wrap. Microsoft says the last input's tick "is not guaranteed
// to be incremental": input that SendInput injects carries its own tick,
// and the raw input thread can lag the desktop's, so it can be ahead of
// the current tick. A difference that is negative as a signed 32-bit
// value is that, and it clamps to zero (CP 3.3). The cost is that a real
// idle time over 24.8 days reads as zero too.
func idleMillis(tick, lastInput uint32) uint32 {
	d := tick - lastInput
	if int32(d) < 0 {
		return 0
	}
	return d
}
