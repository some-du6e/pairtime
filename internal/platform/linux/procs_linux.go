// SPDX-License-Identifier: AGPL-3.0-or-later

// The Linux process table, from /proc (WP-OS6).

package linux

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"

	"golang.org/x/sys/unix"

	"github.com/some-du6e/pairtime/internal/platform"
)

// userHZ is the unit of starttime in /proc/<pid>/stat. USER_HZ is 100 on
// every architecture pairtime ships for (amd64 and arm64).
const userHZ = 100

// pfKthread is PF_KTHREAD in a stat line's flags: a kernel thread.
const pfKthread = 0x00200000

// Processes reads the process table from /proc. Per process it reads
// three things: the owner of the /proc/<pid> directory, which is the user
// the process runs as (its effective uid, proc(5)); the stat line (parent,
// process group, controlling terminal and its foreground group, start
// time and comm); and the target of the exe link, which names the
// executable. Nothing else under /proc/<pid> is opened: not cmdline,
// environ, fd or maps. The exe link of another user's process can't be
// read, and then the name is comm. Kernel threads and zombies are left out.
//
// Start is the boot time plus the start ticks. The kernel derives the boot
// time from the wall clock, so a step of the clock moves every Start, while
// Ticks, which Proc.ID uses, stay put.
func Processes() ([]platform.Proc, error) {
	return readProc("/proc")
}

// BootID returns the kernel's random id for this boot, which names the
// boot that Proc.Ticks count from.
func BootID() (string, error) {
	return bootID("/proc")
}

func bootID(root string) (string, error) {
	data, err := os.ReadFile(root + "/sys/kernel/random/boot_id")
	if err != nil {
		return "", err
	}
	id := string(bytes.TrimSpace(data))
	if id == "" {
		return "", fmt.Errorf("%s/sys/kernel/random/boot_id is empty", root)
	}
	return id, nil
}

func readProc(root string) ([]platform.Proc, error) {
	boot, err := bootTime(root)
	if err != nil {
		return nil, err
	}
	d, err := os.Open(root)
	if err != nil {
		return nil, err
	}
	names, err := d.Readdirnames(-1)
	d.Close()
	if err != nil {
		return nil, err
	}
	buf := make([]byte, 2048)
	link := make([]byte, unix.PathMax)
	procs := make([]platform.Proc, 0, len(names))
	for _, n := range names {
		pid, err := strconv.Atoi(n)
		if err != nil || pid <= 0 {
			continue
		}
		if p, ok := readPid(root, pid, boot, buf, link); ok {
			procs = append(procs, p)
		}
	}
	return procs, nil
}

// readPid reads one process. It reports false when the process is gone,
// is a kernel thread or a zombie, or its stat line doesn't parse.
func readPid(root string, pid int, boot time.Time, buf, link []byte) (platform.Proc, bool) {
	dir := root + "/" + strconv.Itoa(pid)
	// The directory, not the files in it: the kernel hands the files of a
	// process that made itself undumpable to root, but the directory always
	// to the process's effective uid.
	var fi unix.Stat_t
	if err := unix.Stat(dir, &fi); err != nil {
		return platform.Proc{}, false
	}
	line, err := readSmall(dir+"/stat", buf)
	if err != nil {
		return platform.Proc{}, false
	}
	st, err := parseStat(line)
	if err != nil || st.pid != pid || st.flags&pfKthread != 0 || st.state == 'Z' || st.state == 'X' {
		return platform.Proc{}, false
	}
	p := platform.Proc{
		PID:   pid,
		PPID:  st.ppid,
		PGID:  st.pgrp,
		TTY:   platform.TTYNone,
		Start: boot.Add(time.Duration(st.start) * (time.Second / userHZ)),
		Ticks: st.start,
		Owner: platform.UIDOwner(fi.Uid),
	}
	if st.ttyNr != 0 {
		p.TTY = platform.TTYAttached
		if st.tpgid > 0 {
			p.TPGID = st.tpgid
		}
	}
	if n, err := unix.Readlink(dir+"/exe", link); err == nil && n > 0 {
		target := string(link[:n])
		p.Name = platform.ExeName(platform.Linux, target)
		p.Alias = platform.AliasOf(platform.Linux, target, p.Name, st.comm)
	}
	if p.Name == "" {
		p.Name = st.comm
	}
	return p, true
}

type stat struct {
	pid, ppid, pgrp int
	state           byte
	comm            string
	ttyNr           int64
	tpgid           int // -1 without a controlling terminal
	flags           uint64
	start           uint64 // clock ticks after boot
}

// parseStat parses a /proc/<pid>/stat line (proc(5)): pid, comm in
// parentheses, then space-separated fields. comm may hold spaces and
// parentheses itself, so it ends at the last ')'.
func parseStat(line []byte) (stat, error) {
	var s stat
	open := bytes.IndexByte(line, '(')
	end := bytes.LastIndexByte(line, ')')
	if open < 0 || end < open {
		return s, errors.New("stat: no comm")
	}
	pid, err := strconv.Atoi(string(bytes.TrimSpace(line[:open])))
	if err != nil {
		return s, fmt.Errorf("stat: pid: %w", err)
	}
	s.pid = pid
	s.comm = string(line[open+1 : end])
	f := bytes.Fields(line[end+1:])
	// f[0] is field 3 of proc(5), state; starttime is field 22.
	if len(f) < 20 || len(f[0]) != 1 {
		return s, errors.New("stat: too few fields")
	}
	s.state = f[0][0]
	if s.ppid, err = strconv.Atoi(string(f[1])); err != nil {
		return s, fmt.Errorf("stat: ppid: %w", err)
	}
	if s.pgrp, err = strconv.Atoi(string(f[2])); err != nil {
		return s, fmt.Errorf("stat: pgrp: %w", err)
	}
	if s.ttyNr, err = strconv.ParseInt(string(f[4]), 10, 64); err != nil {
		return s, fmt.Errorf("stat: tty_nr: %w", err)
	}
	if s.tpgid, err = strconv.Atoi(string(f[5])); err != nil {
		return s, fmt.Errorf("stat: tpgid: %w", err)
	}
	if s.flags, err = strconv.ParseUint(string(f[6]), 10, 64); err != nil {
		return s, fmt.Errorf("stat: flags: %w", err)
	}
	if s.start, err = strconv.ParseUint(string(f[19]), 10, 64); err != nil {
		return s, fmt.Errorf("stat: starttime: %w", err)
	}
	return s, nil
}

// bootTime reads btime, the boot time in seconds since the Unix epoch,
// from /proc/stat.
func bootTime(root string) (time.Time, error) {
	data, err := os.ReadFile(root + "/stat")
	if err != nil {
		return time.Time{}, err
	}
	for line := range bytes.SplitSeq(data, []byte("\n")) {
		if v, ok := bytes.CutPrefix(line, []byte("btime ")); ok {
			sec, err := strconv.ParseInt(string(bytes.TrimSpace(v)), 10, 64)
			if err != nil {
				return time.Time{}, fmt.Errorf("%s/stat: btime: %w", root, err)
			}
			return time.Unix(sec, 0), nil
		}
	}
	return time.Time{}, fmt.Errorf("%s/stat has no btime line", root)
}

// readSmall reads a small /proc file, which reports its size as 0, into
// buf, growing it when the file is longer.
func readSmall(name string, buf []byte) ([]byte, error) {
	fd, err := unix.Open(name, unix.O_RDONLY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	defer unix.Close(fd)
	n := 0
	for {
		if n == len(buf) {
			buf = append(buf, make([]byte, len(buf))...)
		}
		m, err := unix.Read(fd, buf[n:])
		if err == unix.EINTR {
			continue
		}
		if err != nil {
			return nil, err
		}
		if m == 0 {
			return buf[:n], nil
		}
		n += m
	}
}

// SelfOwner is this process's Owner: the user it runs as, its effective
// uid, as the reader records other processes' owners.
func SelfOwner() string { return platform.UIDOwner(uint32(os.Geteuid())) }
