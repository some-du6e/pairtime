// SPDX-License-Identifier: AGPL-3.0-or-later

package doctor

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
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

// DiskKind says whether the disk that holds dir spins, from its block device's
// queue/rotational in sysfs, or its parent's for a partition: "hdd",
// "ssd", or "unknown" for a file system with no block device of its own,
// such as btrfs or tmpfs. A virtual machine's disk often says it spins
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
		return "unknown"
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
