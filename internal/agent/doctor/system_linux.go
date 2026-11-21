// SPDX-License-Identifier: AGPL-3.0-or-later

package doctor

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

// OSBuild is the distribution's name from os-release and the kernel's
// release, such as "Ubuntu 24.04.1 LTS, Linux 6.8.0-49-generic". It never
// reads uname's node name, which is the hostname.
func OSBuild() string {
	name := "Linux"
	for _, f := range []string{"/etc/os-release", "/usr/lib/os-release"} {
		if b, err := os.ReadFile(f); err == nil {
			if n := osRelease(b); n != "" {
				name = n
			}
			break
		}
	}
	if b, err := os.ReadFile("/proc/sys/kernel/osrelease"); err == nil {
		if k := strings.TrimSpace(string(b)); k != "" {
			return name + ", Linux " + k
		}
	}
	return name
}

// osRelease is os-release's PRETTY_NAME, or its NAME and VERSION_ID.
func osRelease(b []byte) string {
	vals := map[string]string{}
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		k, v, ok := strings.Cut(strings.TrimSpace(sc.Text()), "=")
		if !ok || strings.HasPrefix(k, "#") {
			continue
		}
		if u, err := strconv.Unquote(v); err == nil {
			v = u
		} else {
			v = strings.Trim(v, `'`)
		}
		vals[k] = v
	}
	if p := vals["PRETTY_NAME"]; p != "" {
		return p
	}
	return strings.TrimSpace(vals["NAME"] + " " + vals["VERSION_ID"])
}

// DiskKind says whether the disk that holds dir spins, from its block
// device's queue/rotational in sysfs, or its parent's for a partition:
// "hdd", "ssd", or "unknown" when sysfs doesn't say. A file system whose
// files carry a device number that is no disk's, as btrfs's do, one for
// each subvolume, is looked up by the mount that holds dir in
// /proc/self/mountinfo, whose source names the disk; one with no disk,
// such as tmpfs, is unknown. A virtual machine's disk often says it spins
// whatever the host's does.
func DiskKind(dir string) string {
	fi, err := os.Stat(dir)
	if err != nil {
		return "unknown"
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return "unknown"
	}
	dev := uint64(st.Dev)
	major, minor := unix.Major(dev), unix.Minor(dev)
	if major == 0 {
		b, err := os.ReadFile("/proc/self/mountinfo")
		if err != nil {
			return "unknown"
		}
		path := dir
		if p, err := filepath.EvalSymlinks(dir); err == nil {
			path = p
		}
		if p, err := filepath.Abs(path); err == nil {
			path = p
		}
		src := mountSource(b, path)
		var ds syscall.Stat_t
		if src == "" || syscall.Stat(src, &ds) != nil || ds.Mode&syscall.S_IFMT != syscall.S_IFBLK {
			return "unknown"
		}
		dev = uint64(ds.Rdev)
		major, minor = unix.Major(dev), unix.Minor(dev)
	}
	base := fmt.Sprintf("/sys/dev/block/%d:%d", major, minor)
	for _, p := range []string{base + "/queue/rotational", base + "/../queue/rotational"} {
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		switch strings.TrimSpace(string(b)) {
		case "0":
			return "ssd"
		case "1":
			return "hdd"
		}
	}
	return "unknown"
}

// mountSource returns the source of the mount that holds path, an
// absolute path with no links, when it is a device under /dev, and ""
// otherwise. The mount is the one whose mount point is the longest that
// path is in, and the last listed of those, which covers the ones before
// it. A line of mountinfo is "36 35 0:32 /@home /home rw,relatime -
// btrfs /dev/nvme0n1p3 rw,ssd": the fifth field is the mount point and
// the second after " - " the source, with spaces and the like written as
// octal escapes. The third field, a device number, isn't the one a btrfs
// subvolume's files carry, so it can't be matched.
func mountSource(mountinfo []byte, path string) string {
	best, src := -1, ""
	sc := bufio.NewScanner(bytes.NewReader(mountinfo))
	sc.Buffer(nil, 1<<20)
	for sc.Scan() {
		head, tail, ok := strings.Cut(sc.Text(), " - ")
		f, t := strings.Fields(head), strings.Fields(tail)
		if !ok || len(f) < 5 || len(t) < 2 {
			continue
		}
		mp := unescapeMount(f[4])
		if mp != "/" && path != mp && !strings.HasPrefix(path, mp+"/") {
			continue
		}
		if len(mp) >= best {
			best, src = len(mp), unescapeMount(t[1])
		}
	}
	if strings.HasPrefix(src, "/dev/") {
		return src
	}
	return ""
}

// unescapeMount undoes mountinfo's octal escapes, such as \040 for a space.
func unescapeMount(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+3 < len(s) {
			if n, err := strconv.ParseUint(s[i+1:i+4], 8, 8); err == nil {
				b.WriteByte(byte(n))
				i += 3
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}
