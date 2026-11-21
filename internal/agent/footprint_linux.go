// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"bufio"
	"bytes"
	"os"
	"strconv"
	"syscall"
	"time"
)

// selfUsage reads the agent's resident set and its peak from
// /proc/self/status, and its CPU time from getrusage.
func selfUsage() (rss, peak uint64, cpu time.Duration) {
	var ru syscall.Rusage
	if syscall.Getrusage(syscall.RUSAGE_SELF, &ru) == nil {
		cpu = cpuOf(time.Duration(ru.Utime.Nano()), time.Duration(ru.Stime.Nano()))
	}
	b, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return 0, 0, cpu
	}
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		name, rest, ok := bytes.Cut(sc.Bytes(), []byte(":"))
		if !ok {
			continue
		}
		var into *uint64
		switch string(name) {
		case "VmRSS":
			into = &rss
		case "VmHWM":
			into = &peak
		default:
			continue
		}
		// The value is in kB: "VmRSS:	   12345 kB".
		f := bytes.Fields(rest)
		if len(f) == 2 && string(f[1]) == "kB" {
			if n, err := strconv.ParseUint(string(f[0]), 10, 64); err == nil {
				*into = n * 1024
			}
		}
	}
	return rss, peak, cpu
}
