// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build unix

package inbox

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// TestPrivateSpool checks, as this test's user, that Append and Drain keep
// to a spool that this user alone can reach. TestOtherUser in package ipc
// checks, as root, files and directories that another uid owns.
func TestPrivateSpool(t *testing.T) {
	// mkdir makes dir with exactly mode perm, past the umask.
	mkdir := func(t *testing.T, dir string, perm os.FileMode) {
		t.Helper()
		if err := os.Mkdir(dir, perm); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(dir, perm); err != nil {
			t.Fatal(err)
		}
	}
	// size is the size of the file name.
	size := func(t *testing.T, name string) int64 {
		t.Helper()
		fi, err := os.Stat(name)
		if err != nil {
			t.Fatal(err)
		}
		return fi.Size()
	}
	tests := []struct {
		name string
		run  func(t *testing.T, inbox, journal string)
	}{
		{
			name: "Append writes nothing through a symbolic link in the spool's place",
			run: func(t *testing.T, inbox, journal string) {
				mkdir(t, inbox, 0o700)
				target := filepath.Join(filepath.Dir(inbox), "target")
				if err := os.WriteFile(target, nil, 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, filepath.Join(inbox, SpoolName)); err != nil {
					t.Fatal(err)
				}
				if err := Append(inbox, newMessage(t, 1)); err == nil {
					t.Error("Append wrote through the link")
				}
				if n := size(t, target); n != 0 {
					t.Errorf("the link's target has %d bytes, want 0", n)
				}
			},
		},
		{
			name: "Append refuses a FIFO in the spool's place without waiting for a reader",
			run: func(t *testing.T, inbox, journal string) {
				mkdir(t, inbox, 0o700)
				if err := syscall.Mkfifo(filepath.Join(inbox, SpoolName), 0o600); err != nil {
					t.Fatal(err)
				}
				done := make(chan error, 1)
				go func() { done <- Append(inbox, newMessage(t, 1)) }()
				select {
				case err := <-done:
					if err == nil {
						t.Error("Append took a FIFO for the spool")
					}
				case <-time.After(10 * time.Second):
					t.Fatal("Append is still waiting for something to read the FIFO")
				}
			},
		},
		{
			name: "Append refuses a spool that others may read",
			run: func(t *testing.T, inbox, journal string) {
				mkdir(t, inbox, 0o700)
				spool := filepath.Join(inbox, SpoolName)
				if err := os.WriteFile(spool, nil, 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(spool, 0o644); err != nil {
					t.Fatal(err)
				}
				if err := Append(inbox, newMessage(t, 1)); err == nil {
					t.Error("Append wrote to a spool that others may read")
				}
				if n := size(t, spool); n != 0 {
					t.Errorf("the spool has %d bytes, want 0", n)
				}
			},
		},
		{
			name: "Append refuses a spool with a second name, which could be any other file of this user's",
			run: func(t *testing.T, inbox, journal string) {
				mkdir(t, inbox, 0o700)
				notes := filepath.Join(filepath.Dir(inbox), "notes")
				if err := os.WriteFile(notes, nil, 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Link(notes, filepath.Join(inbox, SpoolName)); err != nil {
					t.Fatal(err)
				}
				if err := Append(inbox, newMessage(t, 1)); err == nil {
					t.Error("Append wrote to a file with two names")
				}
				if n := size(t, notes); n != 0 {
					t.Errorf("the linked file has %d bytes, want 0", n)
				}
			},
		},
		{
			name: "Append writes nothing into an inbox that others may write in",
			run: func(t *testing.T, inbox, journal string) {
				mkdir(t, inbox, 0o777)
				if err := Append(inbox, newMessage(t, 1)); err == nil {
					t.Error("Append wrote into an inbox that others may write in")
				}
				if left := spoolFiles(t, inbox); len(left) != 0 {
					t.Errorf("the inbox has %v, want nothing", left)
				}
			},
		},
		{
			name: "Drain reads nothing from an inbox that others may write in",
			run: func(t *testing.T, inbox, journal string) {
				mkdir(t, inbox, 0o700)
				if err := Append(inbox, newMessage(t, 1)); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(inbox, 0o777); err != nil {
					t.Fatal(err)
				}
				j := openJournal(t, journal)
				if _, err := Drain(inbox, j, stampNow); err == nil {
					t.Error("Drain read an inbox that others may write in")
				}
				if stored, _ := j.Stats(); stored != 0 {
					t.Errorf("the journal stored %d messages, want 0", stored)
				}
			},
		},
		{
			name: "Drain delivers nothing from a taken file that others may write",
			run: func(t *testing.T, inbox, journal string) {
				mkdir(t, inbox, 0o700)
				if err := Append(inbox, newMessage(t, 1)); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(filepath.Join(inbox, SpoolName), 0o666); err != nil {
					t.Fatal(err)
				}
				j := openJournal(t, journal)
				res, err := Drain(inbox, j, stampNow)
				if err == nil {
					t.Error("Drain passed over a file that others may write without a word")
				}
				if stored, _ := j.Stats(); stored != 0 || res.Delivered != 0 || res.SetAside != 1 {
					t.Errorf("Drain = %+v and the journal stored %d, want nothing delivered and the file set aside", res, stored)
				}
				if bad, _ := filepath.Glob(filepath.Join(inbox, "*"+badSuffix)); len(bad) != 1 {
					t.Errorf("the inbox has %v set aside, want the one file", bad)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			tt.run(t, filepath.Join(dir, "inbox"), filepath.Join(dir, "received"))
		})
	}
}
