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

// TestExpireUnread checks which spool files Expire reads, and which
// inboxes it sweeps (D-inbox-age-cap). In an inbox others may write in, it
// reads a file that is this user's with one name, whatever its mode, as no
// other account can have put it there, and dates one that isn't by its
// write time. A FIFO in a spool file's place, which another user's write
// end could hold open, gives firstSent nothing, at once. An inbox reached
// through a symbolic link to a directory this user doesn't own isn't swept,
// as Drain leaves it alone.
func TestExpireUnread(t *testing.T) {
	day := 24 * time.Hour
	// spool writes a spool whose one call was sent 7 days ago, last written
	// written ago, into dir, which others may write in.
	spool := func(t *testing.T, dir string, written time.Duration) string {
		t.Helper()
		if err := os.Chmod(dir, 0o777); err != nil {
			t.Fatal(err)
		}
		p := filepath.Join(dir, SpoolName)
		wall := time.Now()
		b := append(append([]byte("\n"), encoded(t, wall.Add(-7*day), false)...), '\n')
		if err := os.WriteFile(p, b, 0o600); err != nil {
			t.Fatal(err)
		}
		at := wall.Add(-written)
		if err := os.Chtimes(p, at, at); err != nil {
			t.Fatal(err)
		}
		return p
	}
	// expire runs Expire on dir and checks how many files it deleted, and
	// whether p is gone.
	expire := func(t *testing.T, dir, p string, want int) {
		t.Helper()
		if n, err := Expire(dir, time.Now()); n != want || err != nil {
			t.Errorf("Expire = %d, %v; want %d, nil", n, err, want)
		}
		_, err := os.Lstat(p)
		if gone := os.IsNotExist(err); gone != (want > 0) {
			t.Errorf("the spool is gone %v, want %v (%v)", gone, want > 0, err)
		}
	}
	cases := []struct {
		name string
		run  func(t *testing.T, dir string)
	}{
		{"in an inbox others may write in, a spool file of this user's whose first call was sent 7 days ago, written yesterday, is deleted by its Sent", func(t *testing.T, dir string) {
			expire(t, dir, spool(t, dir, day), 1)
		}},
		{"in an inbox others may write in, a spool file written 7 days ago is deleted", func(t *testing.T, dir string) {
			expire(t, dir, spool(t, dir, 7*day), 1)
		}},
		{"a spool file with a second name, which another account could have linked into the inbox, is dated by its write time: written yesterday, it stays", func(t *testing.T, dir string) {
			p := spool(t, dir, day)
			if err := os.Link(p, filepath.Join(t.TempDir(), "other")); err != nil {
				t.Fatal(err)
			}
			expire(t, dir, p, 0)
		}},
		{"a FIFO in a spool file's place holds no call, and firstSent doesn't wait on its write end", func(t *testing.T, dir string) {
			p := filepath.Join(dir, SpoolName)
			if err := syscall.Mkfifo(p, 0o600); err != nil {
				t.Fatal(err)
			}
			// A write end held open, with nothing written, would keep a read
			// waiting.
			w, err := os.OpenFile(p, os.O_RDWR, 0)
			if err != nil {
				t.Fatal(err)
			}
			defer w.Close()
			done := make(chan bool, 1)
			go func() {
				_, ok := firstSent(p)
				done <- ok
			}()
			select {
			case ok := <-done:
				if ok {
					t.Error("firstSent found a call in a FIFO")
				}
			case <-time.After(10 * time.Second):
				t.Fatal("firstSent waited on the FIFO's write end")
			}
		}},
		{"an inbox reached through a symbolic link to a directory this user doesn't own isn't swept", func(t *testing.T, dir string) {
			if os.Geteuid() == 0 {
				t.Skip("root owns the directory the link names")
			}
			link := filepath.Join(dir, "inbox")
			if err := os.Symlink("/", link); err != nil {
				t.Fatal(err)
			}
			if sweepable(link) {
				t.Error("Expire would sweep another user's directory through the link")
			}
		}},
		{"an inbox reached through this user's symbolic link to a directory of this user's that others may write in is swept", func(t *testing.T, dir string) {
			p := spool(t, dir, 7*day)
			link := filepath.Join(t.TempDir(), "inbox")
			if err := os.Symlink(dir, link); err != nil {
				t.Fatal(err)
			}
			if !sweepable(link) {
				t.Error("Expire wouldn't sweep it")
			}
			expire(t, link, p, 1)
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) { c.run(t, t.TempDir()) })
	}
}
