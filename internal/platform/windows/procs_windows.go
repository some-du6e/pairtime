// SPDX-License-Identifier: AGPL-3.0-or-later

// The Windows process table, from NtQuerySystemInformation (WP-OS6).

package windows

import (
	"errors"
	"os"
	"time"
	"unsafe"

	win "golang.org/x/sys/windows"

	"github.com/some-du6e/pairtime/internal/platform"
)

// Processes reads the process table with one
// NtQuerySystemInformation(SystemProcessInformation) call. It gives every
// process's ID, parent ID, image file name, creation time and session id
// without opening any process, so the processes of SYSTEM and of
// other users have a start time too, which Table.Parent needs to see that
// a parent's PID was reused.
//
// A process is opened, with PROCESS_QUERY_LIMITED_INFORMATION, only when
// it may be a harness or its name needs its path (platform.NeedsPath).
// QueryFullProcessImageNameW then reads the full path, because the base
// name alone can't tell the Claude desktop app from Claude Code (ExeName).
// A harness the reader can't open, or whose PID went to a newer process
// since the snapshot, or whose path it needed and couldn't read, gets no
// Owner, so the scan never reports it: the processes of other users are
// among those it can't open. Nothing else is read: not the command line
// or the environment, which sit in the process's memory.
//
// Windows has no process groups and doesn't say whether a process has a
// terminal, so PGID is 0 and TTY TTYUnknown. Owner is the session, the
// Remote Desktop Services session that each signed-in user has.
func Processes() ([]platform.Proc, error) {
	buf, err := processInformation()
	if err != nil {
		return nil, err
	}
	size := int(unsafe.Sizeof(win.SYSTEM_PROCESS_INFORMATION{}))
	var procs []platform.Proc
	for off := 0; off+size <= len(buf); {
		info := (*win.SYSTEM_PROCESS_INFORMATION)(unsafe.Pointer(&buf[off]))
		if info.UniqueProcessID != 0 { // not the System Idle Process
			procs = append(procs, fromInfo(info))
		}
		if info.NextEntryOffset == 0 {
			break
		}
		off += int(info.NextEntryOffset)
	}
	return procs, nil
}

// processInformation returns what SystemProcessInformation fills in. The
// size it asks for can be out of date by the next call, when processes
// start in between, so it tries a few times with room to spare.
func processInformation() ([]byte, error) {
	size := uint32(256 << 10)
	for range 8 {
		buf := make([]byte, size)
		var need uint32
		err := win.NtQuerySystemInformation(win.SystemProcessInformation, unsafe.Pointer(&buf[0]), size, &need)
		switch {
		case err == nil:
			return buf, nil
		case errors.Is(err, win.STATUS_INFO_LENGTH_MISMATCH):
			size = max(size, need) + 64<<10
		default:
			return nil, err
		}
	}
	return nil, errors.New("SystemProcessInformation: the process list kept outgrowing the buffer")
}

func fromInfo(info *win.SYSTEM_PROCESS_INFORMATION) platform.Proc {
	p := platform.Proc{
		PID:   int(info.UniqueProcessID),
		PPID:  int(info.InheritedFromUniqueProcessID),
		Name:  platform.ExeName(platform.Windows, info.ImageName.String()),
		Owner: platform.SessionOwner(info.SessionID),
	}
	if info.CreateTime > 0 {
		p.Start = fromFiletime(info.CreateTime)
	}
	_, harness := platform.HarnessOf(platform.Windows, p)
	needPath := platform.NeedsPath(p.Name)
	if !harness && !needPath {
		return p
	}
	// Whatever goes wrong below, a harness keeps no Owner.
	owner := p.Owner
	if harness {
		p.Owner = ""
	}
	h, err := win.OpenProcess(win.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(p.PID))
	if err != nil {
		return p
	}
	defer win.CloseHandle(h)
	var created, exited, kernel, user win.Filetime
	if win.GetProcessTimes(h, &created, &exited, &kernel, &user) != nil ||
		int64(created.HighDateTime)<<32|int64(created.LowDateTime) != info.CreateTime {
		return p
	}
	if needPath {
		path, ok := imagePath(h)
		if !ok {
			return p
		}
		p.Name = platform.ExeName(platform.Windows, path)
	}
	p.Owner = owner
	return p
}

// Self returns this process the way Processes reads it, so its ID is the
// one the table gives it: its pid, its executable's name, and its creation
// time from GetProcessTimes, which is the CreateTime the table reads from
// SystemProcessInformation (fromInfo checks the two against each other). A
// hook records itself with it, for the agent to check its own walk against
// (internal/agent/hookpath). It opens nothing: the handle is the pseudo
// handle of the current process.
func Self() (platform.Proc, error) {
	var created, exited, kernel, user win.Filetime
	if err := win.GetProcessTimes(win.CurrentProcess(), &created, &exited, &kernel, &user); err != nil {
		return platform.Proc{}, err
	}
	p := platform.Proc{PID: int(win.GetCurrentProcessId())}
	if ft := int64(created.HighDateTime)<<32 | int64(created.LowDateTime); ft > 0 {
		p.Start = fromFiletime(ft)
	}
	if exe, err := os.Executable(); err == nil {
		p.Name = platform.ExeName(platform.Windows, exe)
	}
	return p, nil
}

// fromFiletime converts a FILETIME count, 100 ns intervals since 1601.
func fromFiletime(ft int64) time.Time {
	f := win.Filetime{LowDateTime: uint32(ft), HighDateTime: uint32(ft >> 32)}
	return time.Unix(0, f.Nanoseconds())
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

// SelfOwner is this process's Owner, its session, or "" if Windows won't
// say.
func SelfOwner() string {
	var id uint32
	if err := win.ProcessIdToSessionId(win.GetCurrentProcessId(), &id); err != nil {
		return ""
	}
	return platform.SessionOwner(id)
}
