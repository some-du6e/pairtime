// SPDX-License-Identifier: AGPL-3.0-or-later

package linux

import (
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/platform"
)

// statLine builds a /proc/<pid>/stat line with the fields readPid uses and
// zeros elsewhere: state, ppid, pgrp, session, tty_nr, tpgid, flags,
// starttime. A process on a terminal is in the terminal's foreground group;
// one without has tpgid -1, as Linux writes it.
func statLine(pid, comm, state, ppid, pgrp, ttyNr, flags, start string) string {
	return statLineFg(pid, comm, state, ppid, pgrp, ttyNr, "", flags, start)
}

// statLineFg is statLine with the terminal's foreground group given; ""
// means the process's own group, or -1 without a terminal.
func statLineFg(pid, comm, state, ppid, pgrp, ttyNr, tpgid, flags, start string) string {
	f := make([]string, 50)
	for i := range f {
		f[i] = "0"
	}
	if tpgid == "" {
		tpgid = pgrp
		if ttyNr == "0" {
			tpgid = "-1"
		}
	}
	f[0], f[1], f[2], f[3], f[4], f[5], f[6], f[19] = state, ppid, pgrp, pgrp, ttyNr, tpgid, flags, start
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
		tpgid   int
		start   uint64
		state   byte
		kthread bool
	}{
		{"a plain line parses", statLine("42", "bash", "S", "7", "42", "34816", "4194560", "1234"), true, "bash", 7, 42, 34816, 42, 1234, 'S', false},
		{"the terminal's foreground group is field 8", statLineFg("45", "claude", "S", "42", "45", "34816", "42", "4194560", "1300"), true, "claude", 42, 45, 34816, 42, 1300, 'S', false},
		{"comm may hold spaces and parentheses", statLine("43", "tmux: server (x)", "S", "1", "43", "0", "0", "9"), true, "tmux: server (x)", 1, 43, 0, -1, 9, 'S', false},
		{"a kernel thread carries PF_KTHREAD", statLine("2", "kthreadd", "S", "0", "0", "0", "2129984", "0"), true, "kthreadd", 0, 0, 0, -1, 0, 'S', true},
		{"a zombie keeps its state", statLine("44", "defunct", "Z", "1", "44", "0", "0", "5"), true, "defunct", 1, 44, 0, -1, 5, 'Z', false},
		{"a line without comm is an error", "42 bash S 7 42\n", false, "", 0, 0, 0, 0, 0, 0, false},
		{"a cut-off line is an error", "42 (bash) S 7 42 42 0\n", false, "", 0, 0, 0, 0, 0, 0, false},
		{"a pid that isn't a number is an error", "x (bash) S 7 42 42 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 1\n", false, "", 0, 0, 0, 0, 0, 0, false},
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
			if s.comm != tt.comm || s.ppid != tt.ppid || s.pgrp != tt.pgrp || s.ttyNr != tt.ttyNr || s.tpgid != tt.tpgid || s.start != tt.start || s.state != tt.state || (s.flags&pfKthread != 0) != tt.kthread {
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
			"303":  statLineFg("303", "farm.sh", "S", "300", "303", "34816", "301", "4194560", "520"),
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
	// fakeProc made every directory, so this test's user owns them all.
	me := platform.UIDOwner(uint32(os.Geteuid()))
	tests := []struct {
		name  string
		pid   int
		in    bool
		want  platform.Proc
		start time.Time
	}{
		{"init is read with its exe name", 1, true, platform.Proc{PID: 1, Name: "systemd", PGID: 1, TTY: platform.TTYNone, Ticks: 100, Owner: me}, boot.Add(time.Second)},
		{"a kernel thread is left out", 2, false, platform.Proc{}, time.Time{}},
		{"a shell on a terminal has a tty, and its own group in the foreground", 300, true, platform.Proc{PID: 300, PPID: 1, Name: "bash", PGID: 300, TTY: platform.TTYAttached, TPGID: 300, Ticks: 500, Owner: me}, boot.Add(5 * time.Second)},
		{"Claude Code's versioned image is claude and the version is no alias", 301, true, platform.Proc{PID: 301, PPID: 300, Name: "claude", PGID: 301, TTY: platform.TTYAttached, TPGID: 301, Ticks: 512, Owner: me}, boot.Add(5120 * time.Millisecond)},
		{"a zombie is left out", 302, false, platform.Proc{}, time.Time{}},
		{"a script run through #! is its interpreter, aliased by the script's name, with its terminal's foreground group", 303, true, platform.Proc{PID: 303, PPID: 300, Name: "bash", Alias: "farm.sh", PGID: 303, TTY: platform.TTYAttached, TPGID: 301, Ticks: 520, Owner: me}, boot.Add(5200 * time.Millisecond)},
		{"without an exe link the name is comm, and without a terminal there is no foreground group", 304, true, platform.Proc{PID: 304, PPID: 1, Name: "sshd", PGID: 304, TTY: platform.TTYNone, Ticks: 130, Owner: me}, boot.Add(1300 * time.Millisecond)},
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

// TestChain holds Chain's read of one chain, process by process, to what
// Table.Chain gives from the whole table, on trees shaped to make the walk
// stop each way Table.Parent can.
func TestChain(t *testing.T) {
	const btime = "1763575080"
	proc := func(pid, comm, state, ppid, start string) string {
		return statLine(pid, comm, state, ppid, pid, "34816", "4194560", start)
	}
	base := map[string]string{
		"1":   proc("1", "systemd", "S", "0", "100"),
		"300": proc("300", "bash", "S", "1", "500"),
		"301": proc("301", "claude", "S", "300", "512"),
		"400": proc("400", "pairtime", "S", "301", "600"),
	}
	with := func(over map[string]string) map[string]string {
		m := make(map[string]string, len(base)+len(over))
		for k, v := range base {
			m[k] = v
		}
		for k, v := range over {
			if v == "" {
				delete(m, k)
			} else {
				m[k] = v
			}
		}
		return m
	}
	long := make(map[string]string)
	for i := range platform.MaxChain + 6 {
		pid := 1000 + i
		long[strconv.Itoa(pid)] = proc(strconv.Itoa(pid), "sh", "S", strconv.Itoa(pid+1), strconv.Itoa(5000-i))
	}
	tests := []struct {
		name  string
		stats map[string]string
		pid   int
		want  int // the chain's length
	}{
		{"a chain runs up to init", base, 400, 4},
		{"a pid that isn't a process gives no chain", base, 999, 0},
		{"a parent that exited ends the chain at its child", with(map[string]string{"301": ""}), 400, 1},
		{"a parent that started after its child holds a reused pid, and ends the chain", with(map[string]string{"301": proc("301", "claude", "S", "300", "900")}), 400, 1},
		{"a zombie parent ends the chain", with(map[string]string{"301": proc("301", "claude", "Z", "300", "512")}), 400, 1},
		{"a kernel thread's child gives no parent", with(map[string]string{"301": statLine("301", "kworker", "S", "300", "0", "0", "2129984", "512")}), 400, 1},
		{"a loop of parents ends at the first one seen again", with(map[string]string{"1": proc("1", "systemd", "S", "400", "100")}), 400, 4},
		{"a chain stops at MaxChain", long, 1000, platform.MaxChain},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := fakeProc(t, btime, tt.stats, nil)
			procs, err := readProc(root)
			if err != nil {
				t.Fatal(err)
			}
			want := platform.NewTable(platform.Linux, procs).Chain(tt.pid)
			sec, _ := strconv.ParseInt(btime, 10, 64)
			got := readChain(root, tt.pid, time.Unix(sec, 0))
			if !reflect.DeepEqual(got, want) {
				t.Errorf("readChain(%d) = %+v, want the table's %+v", tt.pid, got, want)
			}
			if len(got) != tt.want {
				t.Errorf("readChain(%d) has %d processes, want %d", tt.pid, len(got), tt.want)
			}
		})
	}
}

// TestReadProcAcrossAClockStep checks that a process keeps its ID when the
// wall clock is stepped, which moves btime and so every Start.
func TestReadProcAcrossAClockStep(t *testing.T) {
	stats := map[string]string{"301": statLine("301", "claude", "S", "1", "301", "34816", "4194560", "512")}
	before, err := readProc(fakeProc(t, "1763575080", stats, nil))
	if err != nil {
		t.Fatal(err)
	}
	after, err := readProc(fakeProc(t, "1763575083", stats, nil))
	if err != nil {
		t.Fatal(err)
	}
	t.Run("Start moves with the clock, and the ID doesn't", func(t *testing.T) {
		if len(before) != 1 || len(after) != 1 {
			t.Fatalf("read %d and %d processes, want 1 each", len(before), len(after))
		}
		if before[0].Start.Equal(after[0].Start) {
			t.Errorf("Start = %s both times, but btime moved by 3 s", before[0].Start)
		}
		if before[0].ID() != after[0].ID() {
			t.Errorf("ID = %+v, then %+v", before[0].ID(), after[0].ID())
		}
	})
}

func TestBootID(t *testing.T) {
	tests := []struct {
		name string
		file string // "" for no boot_id file
		want string
		ok   bool
	}{
		{"the boot id is read without its newline", "0f6f2d3e-5c1a-4b2e-9d7a-1c2b3a4d5e6f\n", "0f6f2d3e-5c1a-4b2e-9d7a-1c2b3a4d5e6f", true},
		{"an empty boot id is an error", "\n", "", false},
		{"a missing boot id is an error", "", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			if tt.file != "" {
				dir := filepath.Join(root, "sys", "kernel", "random")
				if err := os.MkdirAll(dir, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, "boot_id"), []byte(tt.file), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			got, err := bootID(root)
			if (err == nil) != tt.ok || got != tt.want {
				t.Errorf("bootID = %q, %v; want %q, ok %v", got, err, tt.want, tt.ok)
			}
		})
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
