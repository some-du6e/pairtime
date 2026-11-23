// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build windows

package windows

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

// backgroundEnv makes this test binary a process that calls Background and
// reports what Windows says of it, since nothing takes a process out of
// background mode but the process itself.
const backgroundEnv = "PAIRTIME_TEST_BACKGROUND"

// backgroundReport is what the child reports.
type backgroundReport struct {
	Err string
	// Again is the error SetPriorityClass gives when asked for background
	// mode once more, which it refuses to a process in it.
	Again string
	// IO is a thread's I/O priority hint, Background's own and one's that
	// started after it, which LaterNew says was found.
	IO, LaterIO uint32
	LaterNew    bool
	IOErr       string
}

// maxLater bounds the threads the child holds while it looks for one that
// started after Background.
const maxLater = 16

// threadIDs returns the ids of this process's threads.
func threadIDs() (map[uint32]bool, error) {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPTHREAD, 0)
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(snap)
	pid := windows.GetCurrentProcessId()
	ids := map[uint32]bool{}
	e := windows.ThreadEntry32{Size: uint32(unsafe.Sizeof(windows.ThreadEntry32{}))}
	for err = windows.Thread32First(snap, &e); err == nil; err = windows.Thread32Next(snap, &e) {
		if e.OwnerProcessID == pid {
			ids[e.ThreadID] = true
		}
	}
	if !errors.Is(err, windows.ERROR_NO_MORE_FILES) {
		return nil, err
	}
	return ids, nil
}

func backgroundChild(string) int {
	var r backgroundReport
	defer func() {
		b, _ := json.Marshal(r)
		fmt.Println(string(b))
	}()
	if err := Background(); err != nil {
		r.Err = err.Error()
		return 1
	}
	if err := windows.SetPriorityClass(windows.CurrentProcess(), windows.PROCESS_MODE_BACKGROUND_BEGIN); err != nil {
		r.Again = err.Error()
		if !errors.Is(err, windows.ERROR_PROCESS_MODE_ALREADY_BACKGROUND) {
			return 1
		}
	}
	var err error
	if r.IO, err = ThreadIOPriority(); err != nil {
		r.IOErr = err.Error()
		return 1
	}
	before, err := threadIDs()
	if err != nil {
		r.IOErr = err.Error()
		return 1
	}
	// Each locked goroutine holds a thread of its own, so the runtime
	// starts new ones until one that wasn't there after Background reads
	// its I/O priority.
	type read struct {
		io  uint32
		err error
		new bool
	}
	hold := make(chan struct{})
	defer close(hold)
	for i := 0; i < maxLater && !r.LaterNew; i++ {
		got := make(chan read, 1)
		go func() {
			runtime.LockOSThread()
			if !before[windows.GetCurrentThreadId()] {
				io, err := ThreadIOPriority()
				got <- read{io, err, true}
				return
			}
			got <- read{}
			<-hold
		}()
		if l := <-got; l.new {
			if l.err != nil {
				r.IOErr = l.err.Error()
				return 1
			}
			r.LaterIO, r.LaterNew = l.io, true
		}
	}
	return 0
}

// TestBackground checks Background in a child process, which it can't be
// undone in: Windows takes the process into background mode, and its
// threads' I/O priority drops to very low.
func TestBackground(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe, "-test.run=^$")
	cmd.Env = append(os.Environ(), backgroundEnv+"=1")
	out, err := cmd.Output()
	var r backgroundReport
	if jerr := json.Unmarshal(out, &r); jerr != nil {
		t.Fatalf("the child said %q (%v): %v", out, err, jerr)
	}
	cases := []struct {
		name string
		ok   bool
	}{
		{"Background succeeds", r.Err == ""},
		{"a process in background mode is refused it again, which says it is in it",
			r.Again == windows.ERROR_PROCESS_MODE_ALREADY_BACKGROUND.Error()},
		{"the calling thread's I/O priority is very low", r.IOErr == "" && r.IO == IoPriorityVeryLow},
		{"a thread that starts after it has a very low I/O priority too", r.IOErr == "" && r.LaterNew && r.LaterIO == IoPriorityVeryLow},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if !tc.ok {
				t.Errorf("the child reported %+v (exit %v)", r, err)
			}
		})
	}
}
