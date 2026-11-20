// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build windows

package windows

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

// From processthreadsapi.h.
const (
	// processPowerThrottling is ProcessPowerThrottling in
	// PROCESS_INFORMATION_CLASS.
	processPowerThrottling = 4
	// powerThrottlingCurrentVersion is
	// PROCESS_POWER_THROTTLING_CURRENT_VERSION.
	powerThrottlingCurrentVersion = 1
	// powerThrottlingExecutionSpeed is
	// PROCESS_POWER_THROTTLING_EXECUTION_SPEED.
	powerThrottlingExecutionSpeed = 0x1
)

// powerThrottlingState is PROCESS_POWER_THROTTLING_STATE.
type powerThrottlingState struct {
	Version     uint32
	ControlMask uint32
	StateMask   uint32
}

var (
	procSetProcessInformation = modKernel32.NewProc("SetProcessInformation")
	procGetProcessInformation = modKernel32.NewProc("GetProcessInformation")
)

// SetEcoQoS opts the calling process into EcoQoS: Windows then "always
// selects most efficient CPU frequency and schedules to efficient cores"
// for it (CP 2.2; TE 3.2). The agent does this at start, because its work
// is small and never urgent. Windows 10 has the execution-speed throttle
// from version 1709; on a 2-core laptop with no efficiency cores it costs
// nothing, and it helps on newer ones.
func SetEcoQoS() error {
	if err := procSetProcessInformation.Find(); err != nil {
		return err
	}
	st := powerThrottlingState{
		Version:     powerThrottlingCurrentVersion,
		ControlMask: powerThrottlingExecutionSpeed,
		StateMask:   powerThrottlingExecutionSpeed,
	}
	r, _, err := procSetProcessInformation.Call(uintptr(windows.CurrentProcess()), processPowerThrottling,
		uintptr(unsafe.Pointer(&st)), unsafe.Sizeof(st))
	if r == 0 {
		return fmt.Errorf("SetProcessInformation(ProcessPowerThrottling): %w", err)
	}
	return nil
}

// EcoQoS reports whether the calling process has opted into EcoQoS, as
// GetProcessInformation reads it back: the execution-speed throttle is
// both controlled and on.
func EcoQoS() (bool, error) {
	if err := procGetProcessInformation.Find(); err != nil {
		return false, err
	}
	st := powerThrottlingState{Version: powerThrottlingCurrentVersion}
	r, _, err := procGetProcessInformation.Call(uintptr(windows.CurrentProcess()), processPowerThrottling,
		uintptr(unsafe.Pointer(&st)), unsafe.Sizeof(st))
	if r == 0 {
		return false, fmt.Errorf("GetProcessInformation(ProcessPowerThrottling): %w", err)
	}
	on := st.ControlMask&powerThrottlingExecutionSpeed != 0 && st.StateMask&powerThrottlingExecutionSpeed != 0
	return on, nil
}
