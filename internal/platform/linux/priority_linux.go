// SPDX-License-Identifier: AGPL-3.0-or-later

// The lowest CPU and I/O priority, for the lookback reader (WP-AD3).

package linux

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"

	"golang.org/x/sys/unix"
)

// From linux/ioprio.h.
const (
	ioprioWhoProcess = 1  // IOPRIO_WHO_PROCESS: who is a thread id
	ioprioClassShift = 13 // IOPRIO_CLASS_SHIFT
	ioprioLevelMask  = 1<<ioprioClassShift - 1
	// IOClassIdle is IOPRIO_CLASS_IDLE: the thread gets the disk only
	// when no other wants it (ioprio_set(2)).
	IOClassIdle = 3
)

// LowestNice is the nicest a thread can be.
const LowestNice = 19

// Background puts every thread of the calling process at nice 19 and in
// the idle I/O class, the lowest CPU and I/O priority Linux gives a process
// without privileges: the lookback reader runs so, since it reads 30 days
// of transcripts on what may be an old laptop's spinning disk (DESIGN 5.9
// step 9; TE 4.7). Linux keeps both per thread, and a thread takes them
// from the thread that started it, so Background sets each thread that
// /proc/self/task lists until a pass finds none left to set. Nothing
// raises them again: the process runs so until it exits.
func Background() error {
	const passes = 100
	for range passes {
		ps, err := priorities("/proc/self/task")
		if err != nil {
			return err
		}
		set := 0
		for _, p := range ps {
			if p.Nice != LowestNice {
				if err := unix.Setpriority(unix.PRIO_PROCESS, p.TID, LowestNice); err != nil && !errors.Is(err, unix.ESRCH) {
					return fmt.Errorf("setpriority(%d, %d): %w", p.TID, LowestNice, err)
				}
				set++
			}
			if p.IOClass != IOClassIdle {
				if err := setIOPriority(p.TID, IOClassIdle, 0); err != nil && !errors.Is(err, unix.ESRCH) {
					return fmt.Errorf("ioprio_set(%d, idle): %w", p.TID, err)
				}
				set++
			}
		}
		if set == 0 {
			return nil
		}
	}
	return fmt.Errorf("background: threads were still starting after %d passes", passes)
}

// ThreadPriority is one thread's CPU and I/O priority, as the kernel gives
// them back.
type ThreadPriority struct {
	TID int
	// Nice is the thread's nice value, from field 19 of its stat line.
	Nice int
	// IOClass and IOLevel are its ioprio_get(2) value: IOPRIO_CLASS_NONE
	// (0), RT (1), BE (2) or IDLE (3), and the level within the class.
	IOClass, IOLevel int
}

// Priorities reads the priority of every thread of process pid.
func Priorities(pid int) ([]ThreadPriority, error) {
	return priorities(filepath.Join("/proc", strconv.Itoa(pid), "task"))
}

// priorities reads the threads under dir, a /proc/<pid>/task folder,
// leaving out those that ended while it read.
func priorities(dir string) ([]ThreadPriority, error) {
	des, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []ThreadPriority
	for _, de := range des {
		tid, err := strconv.Atoi(de.Name())
		if err != nil {
			continue
		}
		line, err := os.ReadFile(filepath.Join(dir, de.Name(), "stat"))
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, unix.ESRCH) {
			continue
		}
		if err != nil {
			return nil, err
		}
		nice, err := statNice(line)
		if err != nil {
			return nil, fmt.Errorf("thread %d: %w", tid, err)
		}
		class, level, err := ioPriority(tid)
		if errors.Is(err, unix.ESRCH) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("ioprio_get(%d): %w", tid, err)
		}
		out = append(out, ThreadPriority{TID: tid, Nice: nice, IOClass: class, IOLevel: level})
	}
	return out, nil
}

// statNice returns the nice value of a stat line, its field 19 (proc(5)).
func statNice(line []byte) (int, error) {
	end := bytes.LastIndexByte(line, ')')
	if end < 0 {
		return 0, errors.New("stat: no comm")
	}
	// f[0] is field 3, state.
	f := bytes.Fields(line[end+1:])
	if len(f) < 17 {
		return 0, errors.New("stat: too few fields")
	}
	n, err := strconv.Atoi(string(f[16]))
	if err != nil {
		return 0, fmt.Errorf("stat: nice: %w", err)
	}
	return n, nil
}

func ioPriority(tid int) (class, level int, err error) {
	r, _, errno := unix.Syscall(unix.SYS_IOPRIO_GET, ioprioWhoProcess, uintptr(tid), 0)
	if errno != 0 {
		return 0, 0, errno
	}
	return int(r) >> ioprioClassShift, int(r) & ioprioLevelMask, nil
}

func setIOPriority(tid, class, level int) error {
	v := class<<ioprioClassShift | level
	if _, _, errno := unix.Syscall(unix.SYS_IOPRIO_SET, ioprioWhoProcess, uintptr(tid), uintptr(v)); errno != 0 {
		return errno
	}
	return nil
}
