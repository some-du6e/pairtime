// SPDX-License-Identifier: AGPL-3.0-or-later

// The macOS process table, from sysctl and libproc (WP-OS6).

package darwin

import (
	"os"
	"sync"
	"time"

	"golang.org/x/sys/unix"

	"github.com/some-du6e/pairtime/internal/platform"
)

const (
	// pathMax is PROC_PIDPATHINFO_MAXSIZE, the buffer proc_pidpath fills.
	pathMax = 4 * 1024
	// sZomb is SZOMB in kinfo_proc's p_stat: a zombie.
	sZomb = 5
	// pControlT is P_CONTROLT in p_flag: the process has a controlling
	// terminal.
	pControlT = 0x00000002
	// noDev is NODEV, e_tdev when there is no controlling terminal.
	noDev = -1
)

// pidPath is libproc's proc_pidpath, through purego since pairtime builds
// without cgo (CP 7), bound the first time a scan needs it. It is nil when
// libSystem won't load, and then names come from p_comm alone.
var pidPath = sync.OnceValue(func() func(pid int32, buf *byte, size uint32) int32 {
	var fn func(pid int32, buf *byte, size uint32) int32
	sys := dlopen(pathLibSystem)
	sys.fn(&fn, "proc_pidpath")
	if sys.err != nil {
		return nil
	}
	return fn
})

// Processes reads the process table: one sysctl(kern.proc.all) for every
// process's parent, process group, controlling terminal and its foreground
// group, start time, effective uid and p_comm, then proc_pidpath for the
// executable's path of each process that runs as this user. p_comm holds
// at most 16 bytes and, for a script, names the script, so the path is
// what names the executable; another user's processes, such as root's
// login and sshd, go by p_comm, which is all their place in a chain needs.
// Nothing else is read: not KERN_PROCARGS2, which holds the command line
// and the environment, and not e_login, a login name. Zombies are left
// out.
func Processes() ([]platform.Proc, error) {
	kps, err := unix.SysctlKinfoProcSlice("kern.proc.all")
	if err != nil {
		return nil, err
	}
	path := pidPath()
	buf := make([]byte, pathMax)
	me := uint32(os.Geteuid())
	procs := make([]platform.Proc, 0, len(kps))
	for i := range kps {
		kp := &kps[i]
		if kp.Proc.P_stat == sZomb {
			continue
		}
		uid := kp.Eproc.Ucred.Uid
		p := platform.Proc{
			PID:   int(kp.Proc.P_pid),
			PPID:  int(kp.Eproc.Ppid),
			PGID:  int(kp.Eproc.Pgid),
			TTY:   platform.TTYNone,
			Owner: platform.UIDOwner(uid),
		}
		if kp.Proc.P_flag&pControlT != 0 && kp.Eproc.Tdev != noDev {
			p.TTY = platform.TTYAttached
			if kp.Eproc.Tpgid > 0 {
				p.TPGID = int(kp.Eproc.Tpgid)
			}
		}
		if sec, nsec := kp.Proc.P_starttime.Unix(); sec > 0 {
			p.Start = time.Unix(sec, nsec)
		}
		comm := cString(kp.Proc.P_comm[:])
		if path != nil && p.PID > 0 && uid == me {
			if n := path(int32(p.PID), &buf[0], uint32(len(buf))); n > 0 && int(n) <= len(buf) {
				exe := string(buf[:n])
				p.Name = platform.ExeName(platform.Darwin, exe)
				p.Alias = platform.AliasOf(platform.Darwin, exe, p.Name, comm)
			}
		}
		if p.Name == "" {
			p.Name = comm
		}
		procs = append(procs, p)
	}
	return procs, nil
}

// cString returns the bytes of b up to its first NUL.
func cString(b []byte) string {
	for i, c := range b {
		if c == 0 {
			return string(b[:i])
		}
	}
	return string(b)
}

// SelfOwner is this process's Owner: the user it runs as, its effective
// uid, as the reader records other processes' owners.
func SelfOwner() string { return platform.UIDOwner(uint32(os.Geteuid())) }
