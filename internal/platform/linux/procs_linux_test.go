// SPDX-License-Identifier: AGPL-3.0-or-later

package linux

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/platform"
)

// statLine builds a /proc/<pid>/stat line with the fields readPid uses and
// zeros elsewhere: state, ppid, pgrp, session, tty_nr, flags, starttime.
func statLine(pid, comm, state, ppid, pgrp, ttyNr, flags, start string) string {
	f := make([]string, 50)
	for i := range f {
		f[i] = "0"
	}
	f[0], f[1], f[2], f[3], f[4], f[6], f[19] = state, ppid, pgrp, pgrp, ttyNr, flags, start
	return pid + " (" + comm + ") " + strings.Join(f, " ") + "\n"
}

func TestParseStat(t *testing.T) {
	tests := []struct {
		name    string
		line    string
		ok      bool
		comm    string
		ppid    int
		pgrp    int
		ttyNr   int64
		start   uint64
		state   byte
		kthread bool
	}{
		{"a plain line parses", statLine("42", "bash", "S", "7", "42", "34816", "4194560", "1234"), true, "bash", 7, 42, 34816, 1234, 'S', false},
		{"comm may hold spaces and parentheses", statLine("43", "tmux: server (x)", "S", "1", "43", "0", "0", "9"), true, "tmux: server (x)", 1, 43, 0, 9, 'S', false},
		{"a kernel thread carries PF_KTHREAD", statLine("2", "kthreadd", "S", "0", "0", "0", "2129984", "0"), true, "kthreadd", 0, 0, 0, 0, 'S', true},
		{"a zombie keeps its state", statLine("44", "defunct", "Z", "1", "44", "0", "0", "5"), true, "defunct", 1, 44, 0, 5, 'Z', false},
		{"a line without comm is an error", "42 bash S 7 42\n", false, "", 0, 0, 0, 0, 0, false},
		{"a cut-off line is an error", "42 (bash) S 7 42 42 0\n", false, "", 0, 0, 0, 0, 0, false},
		{"a pid that isn't a number is an error", "x (bash) S 7 42 42 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 1\n", false, "", 0, 0, 0, 0, 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, err := parseStat([]byte(tt.line))
			if (err == nil) != tt.ok {
				t.Fatalf("parseStat error = %v, want ok %v", err, tt.ok)
			}
			if !tt.ok {
				return
			}
			if s.comm != tt.comm || s.ppid != tt.ppid || s.pgrp != tt.pgrp || s.ttyNr != tt.ttyNr || s.start != tt.start || s.state != tt.state || (s.flags&pfKthread != 0) != tt.kthread {
				t.Errorf("parseStat = %+v", s)
			}
		})
	}
}

// fakeProc writes a /proc tree with the given stat lines and exe links.
func fakeProc(t *testing.T, btime string, stats map[string]string, exes map[string]string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "stat"), []byte("cpu 1 2 3\nbtime "+btime+"\nprocesses 99\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for pid, line := range stats {
		dir := filepath.Join(root, pid)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "stat"), []byte(line), 0o600); err != nil {
			t.Fatal(err)
		}
		if exe, ok := exes[pid]; ok {
			if err := os.Symlink(exe, filepath.Join(dir, "exe")); err != nil {
				t.Fatal(err)
			}
		}
	}
	// Entries that aren't processes.
	for _, name := range []string{"self", "sys", "0"} {
		if err := os.MkdirAll(filepath.Join(root, name), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestReadProc(t *testing.T) {
	root := fakeProc(t, "1763575080",
		map[string]string{
			"1":    statLine("1", "systemd", "S", "0", "1", "0", "4194560", "100"),
			"2":    statLine("2", "kthreadd", "S", "0", "0", "0", "2129984", "100"),
			"300":  statLine("300", "bash", "S", "1", "300", "34816", "4194560", "500"),
			"301":  statLine("301", "claude", "S", "300", "301", "34816", "4194560", "512"),
			"302":  statLine("302", "defunct", "Z", "300", "302", "0", "4194560", "513"),
			"303":  statLine("303", "farm.sh", "S", "300", "303", "34816", "4194560", "520"),
			"304":  statLine("304", "sshd", "S", "1", "304", "0", "4194560", "130"),
			"9999": "", // exited between the listing and the read
		},
		map[string]string{
			"1":   "/usr/lib/systemd/systemd",
			"300": "/usr/bin/bash",
			"301": "/home/u/.local/share/claude/versions/2.1.328",
			"303": "/usr/bin/bash",
		})
	procs, err := readProc(root)
	if err != nil {
		t.Fatal(err)
	}
	tab := platform.NewTable(platform.Linux, procs)
	boot := time.Unix(1763575080, 0)
	tests := []struct {
		name  string
		pid   int
		in    bool
		want  platform.Proc
		start time.Time
	}{
		{"init is read with its exe name", 1, true, platform.Proc{PID: 1, Name: "systemd", PGID: 1, TTY: platform.TTYNone}, boot.Add(time.Second)},
		{"a kernel thread is left out", 2, false, platform.Proc{}, time.Time{}},
		{"a shell on a terminal has a tty", 300, true, platform.Proc{PID: 300, PPID: 1, Name: "bash", PGID: 300, TTY: platform.TTYAttached}, boot.Add(5 * time.Second)},
		{"Claude Code's versioned image is claude and the version is no alias", 301, true, platform.Proc{PID: 301, PPID: 300, Name: "claude", PGID: 301, TTY: platform.TTYAttached}, boot.Add(5120 * time.Millisecond)},
		{"a zombie is left out", 302, false, platform.Proc{}, time.Time{}},
		{"a script run through #! is its interpreter, aliased by the script's name", 303, true, platform.Proc{PID: 303, PPID: 300, Name: "bash", Alias: "farm.sh", PGID: 303, TTY: platform.TTYAttached}, boot.Add(5200 * time.Millisecond)},
		{"without an exe link the name is comm", 304, true, platform.Proc{PID: 304, PPID: 1, Name: "sshd", PGID: 304, TTY: platform.TTYNone}, boot.Add(1300 * time.Millisecond)},
		{"a process that exited mid-read is left out", 9999, false, platform.Proc{}, time.Time{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := tab.Get(tt.pid)
			if ok != tt.in {
				t.Fatalf("pid %d in the table = %v, want %v", tt.pid, ok, tt.in)
			}
			if !ok {
				return
			}
			want := tt.want
			want.Start = tt.start
			if !got.Start.Equal(want.Start) {
				t.Errorf("Start = %s, want %s", got.Start, want.Start)
			}
			got.Start, want.Start = time.Time{}, time.Time{}
			if got != want {
				t.Errorf("got %+v, want %+v", got, want)
			}
		})
	}
	if tab.Len() != 5 {
		t.Errorf("the table has %d processes, want 5", tab.Len())
	}
}

func TestBootTime(t *testing.T) {
	tests := []struct {
		name string
		stat string
		ok   bool
	}{
		{"btime is read", "cpu 1\nbtime 1763575080\n", true},
		{"a missing btime is an error", "cpu 1\n", false},
		{"a bad btime is an error", "btime soon\n", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, "stat"), []byte(tt.stat), 0o600); err != nil {
				t.Fatal(err)
			}
			got, err := bootTime(root)
			if (err == nil) != tt.ok || tt.ok && !got.Equal(time.Unix(1763575080, 0)) {
				t.Errorf("bootTime = %s, %v", got, err)
			}
		})
	}
}
