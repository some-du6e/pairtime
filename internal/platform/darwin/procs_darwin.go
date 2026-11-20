// SPDX-License-Identifier: AGPL-3.0-or-later

// The macOS process table, from sysctl and libproc (WP-OS6).

package darwin

import (
	"sync"
	"time"

	"github.com/ebitengine/purego"
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
// without cgo (CP 7). It is nil when libSystem won't load, and then names
// come from p_comm alone.
var pidPath = sync.OnceValue(func() func(pid int32, buf *byte, size uint32) int32 {
	lib, err := purego.Dlopen("/usr/lib/libSystem.B.dylib", purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		return nil
	}
	sym, err := purego.Dlsym(lib, "proc_pidpath")
	if err != nil {
		return nil
	}
	var fn func(pid int32, buf *byte, size uint32) int32
	purego.RegisterFunc(&fn, sym)
	return fn
})

// Processes reads the process table: one sysctl(kern.proc.all) for every
// process's parent, process group, controlling terminal, start time and
// p_comm, then proc_pidpath for the executable's path. p_comm holds at
// most 16 bytes and, for a script, names the script, so the path is what
// names the executable. Nothing else is read: not KERN_PROCARGS2, which
// holds the command line and the environment. Zombies are left out.
func Processes() ([]platform.Proc, error) {
	kps, err := unix.SysctlKinfoProcSlice("kern.proc.all")
	if err != nil {
		return nil, err
	}
	path := pidPath()
	buf := make([]byte, pathMax)
	procs := make([]platform.Proc, 0, len(kps))
	for i := range kps {
		kp := &kps[i]
		if kp.Proc.P_stat == sZomb {
			continue
		}
		p := platform.Proc{
			PID:  int(kp.Proc.P_pid),
			PPID: int(kp.Eproc.Ppid),
			PGID: int(kp.Eproc.Pgid),
			TTY:  platform.TTYNone,
		}
		if kp.Proc.P_flag&pControlT != 0 && kp.Eproc.Tdev != noDev {
			p.TTY = platform.TTYAttached
		}
		if sec, nsec := kp.Proc.P_starttime.Unix(); sec > 0 {
			p.Start = time.Unix(sec, nsec)
		}
		comm := cString(kp.Proc.P_comm[:])
		if path != nil && p.PID > 0 {
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
