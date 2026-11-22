// SPDX-License-Identifier: AGPL-3.0-or-later

package inbox

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// TestScanSharing checks Scan's opens of the spool's files on Windows,
// where a file's share mode decides who else may open, rename or delete
// it: a Scan never keeps a drain from renaming or deleting a file, it waits
// out another process's hold of a moment, as a sender does, and a file held
// longer stops Scan with an error, since it may hold any message.
func TestScanSharing(t *testing.T) {
	// exclusive opens spool sharing nothing, as another program can.
	exclusive := func(t *testing.T, spool string) windows.Handle {
		t.Helper()
		p, err := windows.UTF16PtrFromString(spool)
		if err != nil {
			t.Fatal(err)
		}
		h, err := windows.CreateFile(p, windows.GENERIC_READ, 0, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
		if err != nil {
			t.Fatal(err)
		}
		return h
	}
	cases := []struct {
		name  string
		check func(t *testing.T, dir, spool string)
	}{
		{"a drain renames and deletes a spool file while Scan reads it", func(t *testing.T, dir, spool string) {
			taken := filepath.Join(dir, "spool-00000000000000000001-0badc0de"+takenSuffix)
			var renamed, removed error
			found, err := Scan(dir, 10, func(Message) bool {
				// Scan has the file open as it hands over its message.
				renamed = os.Rename(spool, taken)
				removed = os.Remove(taken)
				return true
			})
			if !found || err != nil || renamed != nil || removed != nil {
				t.Errorf("Scan reported %v, %v; the rename a drain takes the spool with gave %v, the delete of a drained file %v; want true, and no errors",
					found, err, renamed, removed)
			}
		}},
		{"a spool another process holds for a moment is read once it lets go, as a sender's open waits", func(t *testing.T, dir, spool string) {
			h := exclusive(t, spool)
			go func() {
				time.Sleep(5 * time.Millisecond)
				windows.CloseHandle(h)
			}()
			found, err := Scan(dir, 10, func(Message) bool { return true })
			if !found || err != nil {
				t.Errorf("Scan reported %v, %v; want the message, read once the hold was over", found, err)
			}
		}},
		{"a spool another process holds without sharing doesn't keep Scan from reading the files after it", func(t *testing.T, dir, spool string) {
			h := exclusive(t, spool)
			defer windows.CloseHandle(h)
			b, err := newMessage(t, 2).Encode()
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "spool-00000000000000000001-0badc0de"+takenSuffix), append(b, '\n'), 0o600); err != nil {
				t.Fatal(err)
			}
			found, err := Scan(dir, 10, func(Message) bool { return true })
			if !found || err != nil {
				t.Errorf("Scan reported %v, %v; want the taken file's message, though the spool was busy", found, err)
			}
		}},
		{"a spool another process holds without sharing stops Scan with an error once a sender's tries are over", func(t *testing.T, dir, spool string) {
			h := exclusive(t, spool)
			defer windows.CloseHandle(h)
			found, err := Scan(dir, 10, func(Message) bool { return true })
			if found || err == nil || !busy(err) {
				t.Errorf("Scan of a spool held without sharing reported %v, %v; want false and a busy error", found, err)
			}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			spool := filepath.Join(dir, SpoolName)
			b, err := newMessage(t, 1).Encode()
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(spool, append(b, '\n'), 0o600); err != nil {
				t.Fatal(err)
			}
			c.check(t, dir, spool)
		})
	}
}
