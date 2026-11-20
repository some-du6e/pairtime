// SPDX-License-Identifier: AGPL-3.0-or-later

// The Windows process table, from Toolhelp32 (WP-OS6).

package windows

import (
	"errors"
	"strings"
	"time"
	"unsafe"

	win "golang.org/x/sys/windows"

	"github.com/some-du6e/pairtime/internal/platform"
)

// Processes reads the process table: one Toolhelp32 snapshot for every
// process's ID, parent ID and executable file name, then, per process,
// OpenProcess with PROCESS_QUERY_LIMITED_INFORMATION and GetProcessTimes
// for its start time. That right is the least a query needs, and protected
// processes still grant it (CP 3.3). Only for an image called claude.exe,
// or named like a version number, is the full path read too, with
// QueryFullProcessImageNameW, because the base name alone can't tell the
// Claude desktop app from Claude Code (ExeName). Nothing else is read: not
// the command line or the environment, which sit in the process's memory.
//
// Windows has no process groups and doesn't say whether a process has a
// terminal, so PGID is 0 and TTY TTYUnknown. A process whose start time
// can't be read (another user's, or System) has a zero Start.
func Processes() ([]platform.Proc, error) {
	snap, err := win.CreateToolhelp32Snapshot(win.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, err
	}
	defer win.CloseHandle(snap)
	var e win.ProcessEntry32
	e.Size = uint32(unsafe.Sizeof(e))
	var procs []platform.Proc
	for err = win.Process32First(snap, &e); err == nil; err = win.Process32Next(snap, &e) {
		procs = append(procs, fromEntry(&e))
	}
	if !errors.Is(err, win.ERROR_NO_MORE_FILES) {
		return nil, err
	}
	return procs, nil
}

func fromEntry(e *win.ProcessEntry32) platform.Proc {
	p := platform.Proc{
		PID:  int(e.ProcessID),
		PPID: int(e.ParentProcessID),
		Name: platform.ExeName(platform.Windows, win.UTF16ToString(e.ExeFile[:])),
	}
	if e.ProcessID == 0 {
		return p // the System Idle Process
	}
	h, err := win.OpenProcess(win.PROCESS_QUERY_LIMITED_INFORMATION, false, e.ProcessID)
	if err != nil {
		return p
	}
	defer win.CloseHandle(h)
	var created, exited, kernel, user win.Filetime
	if win.GetProcessTimes(h, &created, &exited, &kernel, &user) == nil {
		p.Start = time.Unix(0, created.Nanoseconds())
	}
	if strings.EqualFold(p.Name, "claude") || platform.LooksLikeVersion(p.Name) {
		if path, ok := imagePath(h); ok {
			p.Name = platform.ExeName(platform.Windows, path)
		}
	}
	return p
}

// imagePath returns the full path of the process's executable image.
func imagePath(h win.Handle) (string, bool) {
	buf := make([]uint16, win.MAX_LONG_PATH)
	n := uint32(len(buf))
	if err := win.QueryFullProcessImageName(h, 0, &buf[0], &n); err != nil {
		return "", false
	}
	return win.UTF16ToString(buf[:n]), true
}
