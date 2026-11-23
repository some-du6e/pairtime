// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// jobStartEnv, set, makes the test binary wait for a line on stdin, then
// start a stand-in for the agent the way its value says, print the
// stand-in's process ID and exit (jobStart): "detached" with startDetached,
// as the agent starts now, and "plain" with detachedAttr alone, as it
// started before it left its starter's job.
const jobStartEnv = "PAIRTIME_TEST_JOB_START"

func init() { helpers[jobStartEnv] = jobStart }

// jobStart is jobStartEnv's helper. The stand-in is a copy of the test
// binary that sleeps for two minutes (sleepEnv).
func jobStart(how string) int {
	// TestAgentStartLeavesJob sends the line once this process is in its job.
	if _, err := bufio.NewReader(os.Stdin).ReadString('\n'); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	newCmd := func() *exec.Cmd {
		c := exec.Command(exe)
		// Without jobStartEnv, since TestMain runs the first helper whose
		// variable it finds, in no set order.
		c.Env = append(withoutEnv(os.Environ(), jobStartEnv), sleepEnv+"=2m")
		return c
	}
	var cmd *exec.Cmd
	switch how {
	case "detached":
		cmd, err = startDetached(newCmd)
	case "plain":
		cmd = newCmd()
		cmd.SysProcAttr = detachedAttr()
		err = cmd.Start()
	default:
		err = fmt.Errorf("no such start: %q", how)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Println(cmd.Process.Pid)
	return 0
}

// procIsProcessInJob is kernel32's IsProcessInJob, which x/sys/windows
// doesn't wrap.
var procIsProcessInJob = windows.NewLazySystemDLL("kernel32.dll").NewProc("IsProcessInJob")

// inJob reports whether the process is in the job.
func inJob(process, job windows.Handle) (bool, error) {
	var in int32
	if r, _, err := procIsProcessInJob.Call(uintptr(process), uintptr(job), uintptr(unsafe.Pointer(&in))); r == 0 {
		return false, err
	}
	return in != 0, nil
}

// TestAgentStartLeavesJob starts a stand-in for the agent from a process
// in a job object whose processes end when its last handle closes
// (JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE), as a hook's process is when a
// terminal or an app puts its children in such a job, and then closes the
// job, as that terminal or app does when it exits.
func TestAgentStartLeavesJob(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		limits uint32 // the job's, beside JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
		how    string // how the stand-in starts (jobStart)
		inJob  bool   // the stand-in is in the job, and ends with it
	}{
		{
			name:   "an agent started in a job that allows breakaway leaves the job and outlives it",
			limits: windows.JOB_OBJECT_LIMIT_BREAKAWAY_OK, how: "detached",
		},
		{
			name: "an agent started in a job like libuv's, which lets its processes' children out silently, outlives it",
			limits: windows.JOB_OBJECT_LIMIT_BREAKAWAY_OK | windows.JOB_OBJECT_LIMIT_SILENT_BREAKAWAY_OK |
				windows.JOB_OBJECT_LIMIT_DIE_ON_UNHANDLED_EXCEPTION,
			how: "detached",
		},
		{
			name: "an agent started in a job that allows no breakaway still starts, in the job, and ends with it",
			how:  "detached", inJob: true,
		},
		{
			name:   "an agent started without the breakaway flag, as before, stays in a job that allows breakaway and ends with it",
			limits: windows.JOB_OBJECT_LIMIT_BREAKAWAY_OK, how: "plain", inJob: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			job, err := windows.CreateJobObject(nil, nil)
			if err != nil {
				t.Fatalf("CreateJobObject: %v", err)
			}
			open := true
			closeJob := func() {
				if open {
					windows.CloseHandle(job)
					open = false
				}
			}
			t.Cleanup(closeJob)
			var info windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
			info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE | tt.limits
			if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
				t.Fatalf("SetInformationJobObject: %v", err)
			}

			helper := exec.Command(exe)
			helper.Env = append(os.Environ(), jobStartEnv+"="+tt.how)
			stdin, err := helper.StdinPipe()
			if err != nil {
				t.Fatal(err)
			}
			var out bytes.Buffer
			helper.Stdout, helper.Stderr = &out, &out
			if err := helper.Start(); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { helper.Process.Kill() })
			h, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(helper.Process.Pid))
			if err != nil {
				t.Fatalf("opening the helper, process %d: %v", helper.Process.Pid, err)
			}
			err = windows.AssignProcessToJobObject(job, h)
			windows.CloseHandle(h)
			if err != nil {
				t.Fatalf("AssignProcessToJobObject: %v", err)
			}
			io.WriteString(stdin, "go\n")
			stdin.Close()
			if err := waitWithin(t, helper, 30*time.Second); err != nil {
				t.Fatalf("the helper ended with %v; output:\n%s", err, out.String())
			}
			pid, err := strconv.Atoi(strings.TrimSpace(out.String()))
			if err != nil {
				t.Fatalf("the helper printed %q, which names no process", out.String())
			}

			p, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.SYNCHRONIZE|windows.PROCESS_TERMINATE, false, uint32(pid))
			if err != nil {
				t.Fatalf("opening the stand-in, process %d: %v", pid, err)
			}
			t.Cleanup(func() {
				windows.TerminateProcess(p, 1)
				windows.CloseHandle(p)
			})
			in, err := inJob(p, job)
			if err != nil {
				t.Fatalf("IsProcessInJob: %v", err)
			}
			if in != tt.inJob {
				t.Errorf("the stand-in, process %d, is in the job: %v; want %v", pid, in, tt.inJob)
			}

			// Its last handle closed, the job ends the processes in it.
			closeJob()
			wait := 2 * time.Second // for a stand-in that should go on
			if tt.inJob {
				wait = 10 * time.Second
			}
			ev, err := windows.WaitForSingleObject(p, uint32(wait.Milliseconds()))
			switch {
			case err != nil:
				t.Fatalf("WaitForSingleObject: %v", err)
			case tt.inJob && ev != syscall.WAIT_OBJECT_0:
				t.Errorf("the stand-in, process %d, still ran %v after its job closed", pid, wait)
			case !tt.inJob && ev != syscall.WAIT_TIMEOUT:
				t.Errorf("the stand-in, process %d, ended when its starter's job closed", pid)
			}
		})
	}
}
