// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"sync"
	"sync/atomic"
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

// jobAccounting is winnt.h's JOBOBJECT_BASIC_ACCOUNTING_INFORMATION, which
// golang.org/x/sys/windows doesn't define either: four LARGE_INTEGER times,
// in 100-nanosecond units, then four DWORD counts, one layout on every
// architecture.
type jobAccounting struct {
	totalUserTime             int64
	totalKernelTime           int64
	thisPeriodTotalUserTime   int64
	thisPeriodTotalKernelTime int64
	totalPageFaultCount       uint32
	totalProcesses            uint32
	activeProcesses           uint32
	totalTerminatedProcesses  uint32
}

// accountingJob is the job object the agent put itself in at its start
// (joinJob), or 0 before then and when it couldn't. Windows counts in a
// job's accounting the CPU time of every process the job has held, those
// that have exited too, and a process the agent starts is in the agent's
// jobs: T3 Code's reader's sqlite3 runs and the git adapter's git.
var (
	accountingJob  atomic.Uintptr
	accountingOnce sync.Once
)

// joinJob puts the agent in a job object of its own, with no limits, so
// that selfUsage counts the CPU time of the processes it starts from then
// on. Its job nests in any job the agent was started in, as Task
// Scheduler's or a CI runner's, which Windows allows since Windows 8, and
// it changes nothing about how the agent or its processes run. Only the
// first call in a process joins one. When the agent can't join one,
// selfUsage counts the agent's own CPU time alone.
func joinJob() {
	accountingOnce.Do(func() {
		j, err := windows.CreateJobObject(nil, nil)
		if err != nil {
			return
		}
		if err := windows.AssignProcessToJobObject(j, windows.CurrentProcess()); err != nil {
			windows.CloseHandle(j)
			return
		}
		accountingJob.Store(uintptr(j))
	})
}

// selfUsage reads the agent's working set and its peak with
// K32GetProcessMemoryInfo, which kernel32 has had since Windows 7, and its
// CPU time with GetProcessTimes: the numbers footprint.yml reads from
// outside the process. To the CPU time it adds that of the processes the
// agent started, from its job's accounting (childCPU).
func selfUsage() (rss, peak uint64, cpu time.Duration) {
	h := windows.CurrentProcess()
	var created, exited, kernel, user windows.Filetime
	if windows.GetProcessTimes(h, &created, &exited, &kernel, &user) == nil {
		cpu = cpuOf(filetime(user), filetime(kernel))
	}
	cpu += childCPU(cpu)
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

// childCPU is the CPU time of the processes the agent started since it
// joined its job: what the job's accounting counts beyond own, the agent's
// own CPU time, which the job counts too. 0 without a job. It calls
// QueryInformationJobObject through LazyProc.Call, which keeps info where
// the call writes it, as K32GetProcessMemoryInfo's is called.
func childCPU(own time.Duration) time.Duration {
	j := windows.Handle(accountingJob.Load())
	if j == 0 {
		return 0
	}
	proc := windows.NewLazySystemDLL("kernel32.dll").NewProc("QueryInformationJobObject")
	if proc.Find() != nil {
		return 0
	}
	var info jobAccounting
	if r, _, _ := proc.Call(uintptr(j), windows.JobObjectBasicAccountingInformation,
		uintptr(unsafe.Pointer(&info)), unsafe.Sizeof(info), 0); r == 0 {
		return 0
	}
	all := time.Duration(info.totalUserTime+info.totalKernelTime) * 100
	return max(all-own, 0)
}

// filetime is a FILETIME span, in 100-nanosecond units, as a Duration.
func filetime(ft windows.Filetime) time.Duration {
	return time.Duration(uint64(ft.HighDateTime)<<32|uint64(ft.LowDateTime)) * 100
}
