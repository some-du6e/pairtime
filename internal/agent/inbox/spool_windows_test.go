// SPDX-License-Identifier: AGPL-3.0-or-later

package inbox

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

// TestScanSharing checks Scan's opens of the spool's files on Windows,
// where a file's share mode decides who else may open, rename or delete
// it: a Scan never keeps a drain from renaming or deleting a file, and a
// file another process holds without sharing stops Scan with an error,
// since it may hold any message.
func TestScanSharing(t *testing.T) {
	cases := []struct {
		name  string
		check func(t *testing.T, dir, spool string)
	}{
		{"a drain renames and deletes a spool file that Scan has open", func(t *testing.T, dir, spool string) {
			f, err := openShared(spool)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			taken := filepath.Join(dir, "spool-00000000000000000001-0badc0de"+takenSuffix)
			if err := os.Rename(spool, taken); err != nil {
				t.Fatalf("the rename a drain takes the spool with failed while Scan had it open: %v", err)
			}
			if err := os.Remove(taken); err != nil {
				t.Errorf("the delete of a drained file failed while Scan had it open: %v", err)
			}
		}},
		{"a spool another process holds without sharing stops Scan with an error once a sender's tries are over", func(t *testing.T, dir, spool string) {
			p, err := windows.UTF16PtrFromString(spool)
			if err != nil {
				t.Fatal(err)
			}
			h, err := windows.CreateFile(p, windows.GENERIC_READ, 0, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
			if err != nil {
				t.Fatal(err)
			}
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
