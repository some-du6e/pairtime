// SPDX-License-Identifier: AGPL-3.0-or-later

package home

import (
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// TestLockSpellings checks that the lock belongs to the data directory, not
// to the path that names it: while one Acquire holds it, an Acquire that
// names the same directory another way fails. The named mutex alone,
// which is named after the path, let each spelling run an agent of its own
// (#46).
func TestLockSpellings(t *testing.T) {
	tests := []struct {
		name string
		// spell returns another path to the directory dir, or "" and the
		// reason this machine has none.
		spell func(t *testing.T, dir string) (string, string)
	}{
		{name: "the path in other letter cases names the same lock", spell: otherCase},
		{name: "the extended-length form of the path names the same lock", spell: extendedLength},
		{name: "the 8.3 short form of the path names the same lock", spell: shortForm},
		{name: "the long form of a path with 8.3 names in it names the same lock", spell: longForm},
		{name: "a junction to the directory names the same lock", spell: junction},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			l := mustAcquire(t, dir)
			defer l.Release()
			other, none := tt.spell(t, dir)
			if other == "" {
				t.Skip(none)
			}
			if _, err := Acquire(other); !errors.Is(err, ErrLocked) {
				t.Fatalf("Acquire(%s) while Acquire(%s) holds the lock: err = %v, want ErrLocked", other, dir, err)
			}
		})
	}
}

// otherCase is dir in other letter cases, which NTFS ignores.
func otherCase(_ *testing.T, dir string) (string, string) {
	if up := strings.ToUpper(dir); up != dir {
		return up, ""
	}
	return strings.ToLower(dir), ""
}

// extendedLength is dir with the prefix that passes a path to the file
// system as it is, which os.Open also adds to paths too long for Windows'
// usual limit.
func extendedLength(_ *testing.T, dir string) (string, string) {
	if strings.HasPrefix(dir, `\\`) || filepath.VolumeName(dir) == "" {
		return "", dir + " doesn't start with a drive letter"
	}
	return `\\?\` + dir, ""
}

// shortForm is dir with the 8.3 name of each part that has one.
func shortForm(t *testing.T, dir string) (string, string) {
	return pathForm(t, windows.GetShortPathName, dir, "the volume gave no part of "+dir+" an 8.3 name")
}

// longForm is dir with each 8.3 name in it spelled out, such as RUNNER~1
// in a hosted runner's TEMP.
func longForm(t *testing.T, dir string) (string, string) {
	return pathForm(t, windows.GetLongPathName, dir, dir+" has no 8.3 names in it")
}

// pathForm returns convert's form of dir, or "" and none when it is dir
// itself.
func pathForm(t *testing.T, convert func(path, buf *uint16, n uint32) (uint32, error), dir, none string) (string, string) {
	t.Helper()
	p, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]uint16, windows.MAX_LONG_PATH)
	n, err := convert(p, &buf[0], uint32(len(buf)))
	if err != nil {
		t.Fatalf("converting %s: %v", dir, err)
	}
	if got := windows.UTF16ToString(buf[:n]); got != dir {
		return got, ""
	}
	return "", none
}

// junction is a directory junction to dir. mklink makes one without the
// privilege a symbolic link needs.
func junction(t *testing.T, dir string) (string, string) {
	link := filepath.Join(t.TempDir(), "link")
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", link, dir).CombinedOutput(); err != nil {
		t.Fatalf("mklink /J %s %s: %v\n%s", link, dir, err, out)
	}
	return link, ""
}

// TestLockSessions checks that the lock keeps out the same user's agent in
// another Windows session. The Local\ mutex alone, which exists in one
// session's namespace, didn't keep an agent in the desktop session from
// running beside one that Claude Code started over OpenSSH, or that a
// scheduled task set to run whether the user is logged on or not started,
// both of which run in session 0 (#46).
func TestLockSessions(t *testing.T) {
	tests := []struct {
		name string
		// logon is schtasks /Create's arguments for how the holder's task
		// logs the current user on.
		logon []string
	}{
		{
			// With /NP, no password is stored, so Task Scheduler logs the
			// user on with S4U, and runs the task in session 0.
			name:  "a lock the same user holds from session 0 makes Acquire in the desktop session fail with ErrLocked",
			logon: []string{"/NP"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mine := sessionOf(t, windows.GetCurrentProcessId())
			if mine == 0 {
				t.Skip("this test runs in session 0 itself, where the holder's task runs too")
			}
			dir := t.TempDir()
			pid := startTaskHolder(t, dir, tt.logon)
			theirs := sessionOf(t, pid)
			if theirs == mine {
				t.Skipf("the holder's task ran in this test's own session, %d", mine)
			}
			t.Logf("the holder, pid %d, runs in session %d and this test in session %d", pid, theirs, mine)
			if _, err := Acquire(dir); !errors.Is(err, ErrLocked) {
				t.Fatalf("Acquire in session %d while the same user holds the lock in session %d: err = %v, want ErrLocked", mine, theirs, err)
			}
		})
	}
}

// sessionOf returns the Windows session the process pid runs in.
func sessionOf(t *testing.T, pid uint32) uint32 {
	t.Helper()
	var s uint32
	if err := windows.ProcessIdToSessionId(pid, &s); err != nil {
		t.Fatalf("ProcessIdToSessionId(%d): %v", pid, err)
	}
	return s
}

// startTaskHolder starts this test binary as a lock holder on dir (see
// holderReportEnv) from a one-off scheduled task that runs as the current
// user, logged on as logon says. It returns the holder's pid once the
// holder holds the lock. At cleanup it stops the holder and deletes the
// task.
func startTaskHolder(t *testing.T, dir string, logon []string) uint32 {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	me, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	work := t.TempDir()
	report := filepath.Join(work, "report")
	// A task's command can be at most 261 characters long, so the task runs
	// a script that sets the holder's environment and starts it.
	script := filepath.Join(work, "holder.cmd")
	lines := []string{
		"@echo off",
		`set "` + holderEnv + `=` + dir + `"`,
		`set "` + holderReportEnv + `=` + report + `"`,
		`"` + exe + `"`,
	}
	if err := os.WriteFile(script, []byte(strings.Join(lines, "\r\n")+"\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	task := "pairtime-lock-test-" + rand.Text()
	// The task's one trigger, midnight today, has passed, so the task runs
	// only when /Run starts it. schtasks warns about that and creates it.
	create := []string{"/Create", "/F", "/TN", task, "/TR", script, "/SC", "ONCE", "/ST", "00:00", "/RU", me.Username}
	schtasks(t, append(create, logon...)...)
	t.Cleanup(func() {
		// Deleting the report tells the holder to release the lock and
		// exit. A virus scanner can hold the file open for a moment, so
		// keep trying until the lock is free.
		deadline := time.Now().Add(30 * time.Second)
		for {
			os.Remove(report)
			l, err := Acquire(dir)
			if err == nil {
				l.Release()
				break
			}
			if time.Now().After(deadline) {
				t.Errorf("the holder still held the lock 30 s after its report was deleted: %v", err)
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		exec.Command("schtasks", "/End", "/TN", task).Run()
		exec.Command("schtasks", "/Delete", "/F", "/TN", task).Run()
	})
	schtasks(t, "/Run", "/TN", task)

	deadline := time.Now().Add(time.Minute)
	for {
		b, err := os.ReadFile(report)
		if err == nil {
			var pid uint32
			if _, err := fmt.Sscanf(string(b), "locked %d", &pid); err != nil {
				t.Fatalf("the holder's report says %q, want \"locked <pid>\"", b)
			}
			return pid
		}
		if time.Now().After(deadline) {
			out, _ := exec.Command("schtasks", "/Query", "/V", "/FO", "LIST", "/TN", task).CombinedOutput()
			t.Fatalf("the holder's task wrote no report within a minute (%v); schtasks /Query says:\n%s", err, out)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// schtasks runs schtasks.exe with args and fails the test if it fails.
func schtasks(t *testing.T, args ...string) {
	t.Helper()
	if out, err := exec.Command("schtasks", args...).CombinedOutput(); err != nil {
		t.Fatalf("schtasks %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}
