// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// processMemoryCounters is psapi.h's PROCESS_MEMORY_COUNTERS, which
// golang.org/x/sys/windows doesn't define. Its sizes are pointer-sized, so
// one layout serves windows/386 too.
type processMemoryCounters struct {
	cb                         uint32
	pageFaultCount             uint32
	peakWorkingSetSize         uintptr
	workingSetSize             uintptr
	quotaPeakPagedPoolUsage    uintptr
	quotaPagedPoolUsage        uintptr
	quotaPeakNonPagedPoolUsage uintptr
	quotaNonPagedPoolUsage     uintptr
	pagefileUsage              uintptr
	peakPagefileUsage          uintptr
}

// selfUsage reads the agent's working set and its peak with
// K32GetProcessMemoryInfo, which kernel32 has had since Windows 7, and its
// CPU time with GetProcessTimes: the numbers footprint.yml reads from
// outside the process.
func selfUsage() (rss, peak uint64, cpu time.Duration) {
	h := windows.CurrentProcess()
	var created, exited, kernel, user windows.Filetime
	if windows.GetProcessTimes(h, &created, &exited, &kernel, &user) == nil {
		cpu = cpuOf(filetime(user), filetime(kernel))
	}
	// Found when asked, not at package init, which every hook process runs.
	proc := windows.NewLazySystemDLL("kernel32.dll").NewProc("K32GetProcessMemoryInfo")
	if proc.Find() != nil {
		return 0, 0, cpu
	}
	pmc := processMemoryCounters{cb: uint32(unsafe.Sizeof(processMemoryCounters{}))}
	if r, _, _ := proc.Call(uintptr(h), uintptr(unsafe.Pointer(&pmc)), uintptr(pmc.cb)); r != 0 {
		rss, peak = uint64(pmc.workingSetSize), uint64(pmc.peakWorkingSetSize)
	}
	return rss, peak, cpu
}

// filetime is a FILETIME span, in 100-nanosecond units, as a Duration.
func filetime(ft windows.Filetime) time.Duration {
	return time.Duration(uint64(ft.HighDateTime)<<32|uint64(ft.LowDateTime)) * 100
}
