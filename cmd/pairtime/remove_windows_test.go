// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// commitEnv, when set, makes the test binary put a new copy of pairtime in
// place as install does, with commitBinary, and exit 0 if it went in
// (commitInOther). Its value is the new copy's path, a | (which no Windows
// path holds), and where the copy goes.
const commitEnv = "PAIRTIME_TEST_COMMIT"

func init() { helpers[commitEnv] = commitInOther }

// commitInOther is commitEnv's helper: another install, in a process of
// its own.
func commitInOther(v string) int {
	tmp, dst, ok := strings.Cut(v, "|")
	if !ok {
		fmt.Fprintf(os.Stderr, "%s=%q names no place for the copy\n", commitEnv, v)
		return 2
	}
	if err := commitBinary(tmp, dst); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

// TestPlacementsAtOnce checks that two installs that put pairtime in one
// folder at once never delete the copy the other moved aside, which that
// install puts back if its new copy can't go in, so that a pairtime is
// always left there (#270). The other install runs in a process of its
// own, since commitBinary names the copy it moves aside for its process.
// The test holds it between its two renames by keeping its new copy open,
// which Windows won't let anyone rename meanwhile (renameRetry tries again
// for five seconds), and does the case's steps in between.
func TestPlacementsAtOnce(t *testing.T) {
	const (
		otherCopy = "the other install's new pairtime"
		ourCopy   = "this install's new pairtime"
		oldCopy   = "old"
	)
	// cleanup is what pairtime install does before it stages its copy: it
	// deletes the copies earlier installs left.
	cleanup := func(dir string) {
		for _, p := range strayBinaries(dir, 10*time.Minute) {
			os.Remove(p)
		}
	}
	tests := []struct {
		name string
		// meanwhile runs once the other install has moved the old binary
		// aside and waits to put its new copy in. ours is this install's
		// new copy, staged beside dst.
		meanwhile func(t *testing.T, dir, dst, ours string)
		// release lets the other install's new copy go in. Without it the
		// copy stays open until that install gives up.
		release bool
		// want is what dst holds once both are done: oldCopy for the old
		// binary, or one of the new copies.
		want string
	}{
		{
			name: "an install that puts its copy in meanwhile leaves the copy the other moved aside, which goes once the other's new copy is in",
			meanwhile: func(t *testing.T, dir, dst, ours string) {
				cleanup(dir)
				if err := commitBinary(ours, dst); err != nil {
					t.Errorf("this install's commitBinary: %v", err)
				}
			},
			release: true,
			want:    otherCopy,
		},
		{
			name: "an install that puts its copy in meanwhile leaves the copy the other moved aside, which goes when the other's new copy can't go in, leaving this install's",
			meanwhile: func(t *testing.T, dir, dst, ours string) {
				cleanup(dir)
				if err := commitBinary(ours, dst); err != nil {
					t.Errorf("this install's commitBinary: %v", err)
				}
			},
			want: ourCopy,
		},
		{
			name:      "an install that stops after its cleanup leaves the copy the other moved aside, which the other puts back when its new copy can't go in",
			meanwhile: func(t *testing.T, dir, dst, ours string) { cleanup(dir) },
			want:      oldCopy,
		},
		{
			name:      "an agent that starts meanwhile leaves the copy an install moved aside, which that install puts back when its new copy can't go in",
			meanwhile: func(t *testing.T, dir, dst, ours string) { removeOldBinaries(dir) },
			want:      oldCopy,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			dst := filepath.Join(dir, "pairtime.exe")
			// The cleanups delete only pairtime's copies (isPairtimeBinary),
			// so the old binary is one.
			old := copyTestBinary(t, dst)
			other := stageFile(t, dir, otherCopy)
			ours := stageFile(t, dir, ourCopy)
			release := holdOpen(t, other)

			exe, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(exe)
			cmd.Env = append(os.Environ(), commitEnv+"="+other+"|"+dst)
			var out bytes.Buffer
			cmd.Stdout, cmd.Stderr = &out, &out
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			exited := make(chan struct{})
			var exitErr error
			go func() {
				exitErr = cmd.Wait()
				close(exited)
			}()
			t.Cleanup(func() {
				cmd.Process.Kill()
				<-exited
			})

			aside := waitAside(t, dir, dst, exited, &out)
			tt.meanwhile(t, dir, dst, ours)
			if _, err := os.Lstat(aside); err != nil {
				t.Errorf("the copy the other install moved aside is gone after this one's steps: %v", err)
			}
			select {
			case <-exited:
				t.Fatalf("the other install ended before this one's steps were done, so they didn't come between its two renames:\n%s", &out)
			default:
			}
			if tt.release {
				release()
			}
			select {
			case <-exited:
			case <-time.After(time.Minute):
				t.Fatal("the other install still runs a minute on")
			}
			release()
			if tt.release && exitErr != nil {
				t.Errorf("the other install: %v\n%s", exitErr, &out)
			}
			if !tt.release && exitErr == nil {
				t.Errorf("the other install says it put in a copy that it couldn't rename:\n%s", &out)
			}

			got, err := os.ReadFile(dst)
			if err != nil {
				t.Fatalf("no pairtime is left in place: %v", err)
			}
			want := []byte(tt.want)
			if tt.want == oldCopy {
				want = old
			}
			if !bytes.Equal(got, want) {
				t.Errorf("%s holds %.40q, want %.40q", dst, got, want)
			}
			if left := movedAside(t, dir); len(left) > 0 {
				t.Errorf("copies moved aside are left: %q", left)
			}
		})
	}
}

// TestCommitBinary checks how commitBinary puts a new copy of pairtime in
// place, and that the one copy it moves aside or deletes is named for its
// own process: any other may be one that another install still needs
// (#270).
func TestCommitBinary(t *testing.T) {
	self := "pairtime.old." + strconv.Itoa(os.Getpid()) + ".exe"
	other := "pairtime.old." + strconv.Itoa(os.Getpid()+4) + ".exe"
	tests := []struct {
		name   string
		before map[string]string // the folder's files, by name, and what each holds
		// running names a file of before's that a process runs from: a copy
		// of the test binary, which before and after show as "running".
		running string
		noCopy  bool // the new copy is gone before it can go in
		wantErr bool
		after   map[string]string
	}{
		{
			name:   "with no pairtime there, the new copy goes in",
			before: map[string]string{},
			after:  map[string]string{"pairtime.exe": "new"},
		},
		{
			name:   "the old pairtime moves aside and goes once the new copy is in",
			before: map[string]string{"pairtime.exe": "old"},
			after:  map[string]string{"pairtime.exe": "new"},
		},
		{
			name:   "a copy under pairtime.old.exe, the name earlier versions tried first, stays for the cleanups",
			before: map[string]string{"pairtime.exe": "old", "pairtime.old.exe": "another install's"},
			after:  map[string]string{"pairtime.exe": "new", "pairtime.old.exe": "another install's"},
		},
		{
			name:   "a copy that another process moved aside under its own id stays",
			before: map[string]string{"pairtime.exe": "old", other: "another install's"},
			after:  map[string]string{"pairtime.exe": "new", other: "another install's"},
		},
		{
			name:   "a copy that an earlier process with this one's id left goes",
			before: map[string]string{"pairtime.exe": "old", self: "an earlier process's"},
			after:  map[string]string{"pairtime.exe": "new"},
		},
		{
			name:    "a copy that an earlier process with this one's id left and that something still runs stays, and the old pairtime moves aside under another id and goes",
			before:  map[string]string{"pairtime.exe": "old", self: "running"},
			running: self,
			after:   map[string]string{"pairtime.exe": "new", self: "running"},
		},
		{
			name:    "when the new copy can't go in, the old pairtime goes back",
			before:  map[string]string{"pairtime.exe": "old"},
			noCopy:  true,
			wantErr: true,
			after:   map[string]string{"pairtime.exe": "old"},
		},
		{
			name:    "when the new copy can't go in where there was no pairtime, the folder stays empty",
			before:  map[string]string{},
			noCopy:  true,
			wantErr: true,
			after:   map[string]string{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			for name, data := range tt.before {
				if name == tt.running {
					continue
				}
				if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if tt.running != "" {
				runFrom(t, filepath.Join(dir, tt.running))
			}
			tmp := filepath.Join(dir, stagedPrefix+"test.exe")
			if !tt.noCopy {
				if err := os.WriteFile(tmp, []byte("new"), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			err := commitBinary(tmp, filepath.Join(dir, "pairtime.exe"))
			if (err != nil) != tt.wantErr {
				t.Errorf("commitBinary = %v, want an error: %v", err, tt.wantErr)
			}
			got := folderFiles(t, dir)
			if _, ok := got[tt.running]; ok {
				got[tt.running] = "running"
			}
			if !maps.Equal(got, tt.after) {
				t.Errorf("the folder holds %.40q, want %q", got, tt.after)
			}
		})
	}
}

// TestOldCopies checks which copies that an install moved aside the
// cleanups delete. pairtime agent's as it starts (removeOldBinaries) and
// pairtime install's (strayBinaries) leave one that an install still
// running may put back (placing). uninstall's (strayBinaries with age 0)
// takes every copy.
func TestOldCopies(t *testing.T) {
	ended := endedProcess(t)
	tests := []struct {
		name string
		file string // the copy's name
		// gone is whether agent start's and install's cleanups delete it.
		gone bool
	}{
		{"pairtime.old.exe, the name earlier versions tried first, goes", "pairtime.old.exe", true},
		{"pairtime.old.exe goes in any case", "PAIRTIME.OLD.EXE", true},
		{"a copy named for a process that has ended goes", "pairtime.old." + strconv.Itoa(ended) + ".exe", true},
		{"a copy named for a process that runs stays", "pairtime.old." + strconv.Itoa(os.Getpid()) + ".exe", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			p := filepath.Join(dir, tt.file)
			copyTestBinary(t, p)
			if !slices.Contains(strayBinaries(dir, 0), p) {
				t.Errorf("uninstall's strayBinaries leaves %s out", tt.file)
			}
			if got := slices.Contains(strayBinaries(dir, 10*time.Minute), p); got != tt.gone {
				t.Errorf("install's strayBinaries has %s: %v, want %v", tt.file, got, tt.gone)
			}
			removeOldBinaries(dir)
			_, err := os.Lstat(p)
			if gone := errors.Is(err, fs.ErrNotExist); gone != tt.gone {
				t.Errorf("removeOldBinaries deleted %s: %v (%v), want %v", tt.file, gone, err, tt.gone)
			}
		})
	}
}

// TestOldNames checks which names isOldName takes for a copy that an
// install moved aside, and which process oldPID reads in each.
func TestOldNames(t *testing.T) {
	tests := []struct {
		name  string
		file  string
		old   bool
		pid   uint32
		named bool // the name names a process
	}{
		{"pairtime.old.exe, the name earlier versions tried first, is one, named for no process", "pairtime.old.exe", true, 0, false},
		{"pairtime.old.<pid>.exe is one, named for that process", "pairtime.old.1234.exe", true, 1234, true},
		{"either is read in any case", "PAIRTIME.Old.1234.EXE", true, 1234, true},
		{"an id past 32 bits is no process's", "pairtime.old.4294967296.exe", false, 0, false},
		{"an id is digits only", "pairtime.old.12a.exe", false, 0, false},
		{"an id has no sign", "pairtime.old.+12.exe", false, 0, false},
		{"an empty id names nothing", "pairtime.old..exe", false, 0, false},
		{"the binary itself isn't one", "pairtime.exe", false, 0, false},
		{"another extension isn't one", "pairtime.old.1234.exe.bak", false, 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isOldName(tt.file); got != tt.old {
				t.Errorf("isOldName(%q) = %v, want %v", tt.file, got, tt.old)
			}
			pid, ok := oldPID(tt.file)
			if pid != tt.pid || ok != tt.named {
				t.Errorf("oldPID(%q) = %d, %v, want %d, %v", tt.file, pid, ok, tt.pid, tt.named)
			}
		})
	}
}

// waitAside waits for the other install, which runs until exited is
// closed, to have moved dst aside, and returns where it went: the one copy
// in dir named as commitBinary names those.
func waitAside(t *testing.T, dir, dst string, exited <-chan struct{}, out *bytes.Buffer) string {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		if _, err := os.Lstat(dst); errors.Is(err, fs.ErrNotExist) {
			if aside := movedAside(t, dir); len(aside) == 1 {
				return aside[0]
			}
		}
		select {
		case <-exited:
			t.Fatalf("the other install ended before it moved %s aside:\n%s", dst, out)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("the other install didn't move %s aside within 30 s", dst)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// movedAside returns the copies in dir named as commitBinary, or
// install.ps1, names those it moves aside.
func movedAside(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		if isOldName(e.Name()) {
			out = append(out, filepath.Join(dir, e.Name()))
		}
	}
	return out
}

// folderFiles returns the files in dir, by name, and what each holds.
func folderFiles(t *testing.T, dir string) map[string]string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, e := range entries {
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		out[e.Name()] = string(data)
	}
	return out
}

// copyTestBinary puts a copy of the test binary, which is a pairtime binary
// (isPairtimeBinary), at path, and returns what it holds.
func copyTestBinary(t *testing.T, path string) []byte {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o755); err != nil {
		t.Fatal(err)
	}
	return data
}

// stageFile writes data to a new file in dir, named as stageBinary names
// the copies it stages, and returns its path.
func stageFile(t *testing.T, dir, data string) string {
	t.Helper()
	f, err := os.CreateTemp(dir, stagedPrefix+"*.exe")
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.WriteString(data)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		t.Fatal(err)
	}
	return f.Name()
}

// holdOpen opens path as a virus scanner might, shared for reading but not
// for deleting, which a rename needs, so that no one may rename the file
// until the function holdOpen returns closes it again. Calling that
// function again does nothing.
func holdOpen(t *testing.T, path string) func() {
	t.Helper()
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	h, err := windows.CreateFile(name, windows.GENERIC_READ, windows.FILE_SHARE_READ, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatal(err)
	}
	var once sync.Once
	release := func() { once.Do(func() { windows.CloseHandle(h) }) }
	t.Cleanup(release)
	return release
}

// runFrom puts a copy of the test binary at path and starts a process
// from it that runs until the test ends, as a hook or an agent might, so
// that Windows won't let anyone delete or replace the file meanwhile.
func runFrom(t *testing.T, path string) {
	t.Helper()
	copyTestBinary(t, path)
	cmd := exec.Command(path)
	cmd.Env = append(os.Environ(), holdEnv+"=1")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		stdin.Close()
		if err := waitWithin(t, cmd, time.Minute); err != nil {
			t.Errorf("the process that ran %s: %v", path, err)
		}
	})
}

// endedProcess starts a process that ends at once, and returns its id. The
// test keeps a handle to the process, which keeps any other from getting
// that id until the test ends.
func endedProcess(t *testing.T) int {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe)
	cmd.Env = append(os.Environ(), holdEnv+"=1")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(cmd.Process.Pid))
	if err != nil {
		cmd.Process.Kill()
		cmd.Wait()
		t.Fatal(err)
	}
	t.Cleanup(func() { windows.CloseHandle(h) })
	stdin.Close()
	if err := waitWithin(t, cmd, time.Minute); err != nil {
		t.Fatal(err)
	}
	return cmd.Process.Pid
}
