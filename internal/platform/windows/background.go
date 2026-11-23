// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build windows

package windows

import (
	"errors"
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Background puts the calling process in background processing mode,
// PROCESS_MODE_BACKGROUND_BEGIN: Windows lowers the CPU, I/O and memory
// priority of the process and its threads "so that it can perform
// background work without significantly affecting activity in the
// foreground" (SetPriorityClass). The lookback reader runs so, since it
// reads 30 days of transcripts on what may be an old laptop's spinning
// disk (DESIGN 5.9 step 9; TE 4.7). A process already in the mode stays in
// it, which SetPriorityClass reports as an error that Background doesn't.
func Background() error {
	err := windows.SetPriorityClass(windows.CurrentProcess(), windows.PROCESS_MODE_BACKGROUND_BEGIN)
	if err != nil && !errors.Is(err, windows.ERROR_PROCESS_MODE_ALREADY_BACKGROUND) {
		return fmt.Errorf("SetPriorityClass(PROCESS_MODE_BACKGROUND_BEGIN): %w", err)
	}
	return nil
}

// threadIoPriority is ThreadIoPriority in THREADINFOCLASS.
const threadIoPriority = 22

// IoPriorityVeryLow is IoPriorityVeryLow in IO_PRIORITY_HINT, the I/O
// priority of a thread in background mode.
const IoPriorityVeryLow = 0

// ThreadIOPriority reads the calling thread's I/O priority hint, as
// NtQueryInformationThread gives it: IoPriorityVeryLow up to 4, critical,
// with 2 normal.
func ThreadIOPriority() (uint32, error) {
	proc := windows.NewLazySystemDLL("ntdll.dll").NewProc("NtQueryInformationThread")
	if err := proc.Find(); err != nil {
		return 0, err
	}
	var v uint32
	r, _, _ := proc.Call(uintptr(windows.CurrentThread()), threadIoPriority, uintptr(unsafe.Pointer(&v)), unsafe.Sizeof(v), 0)
	if r != 0 {
		return 0, fmt.Errorf("NtQueryInformationThread(ThreadIoPriority): %w", windows.NTStatus(r))
	}
	return v, nil
}
