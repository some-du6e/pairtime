// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build unix

package inbox

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
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
// end could hold open, is dated by its write time, at once, unread. An
// inbox reached
// through a symbolic link to a directory this user doesn't own isn't swept,
// as Drain leaves it alone.
func TestExpireUnread(t *testing.T) {
	day := 24 * time.Hour
	// spoolMode writes a spool whose one call was sent 7 days ago, last
	// written written ago, with mode mode, into dir, which others may write
	// in.
	spoolMode := func(t *testing.T, dir string, written time.Duration, mode os.FileMode) string {
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
		if err := os.Chmod(p, mode); err != nil {
			t.Fatal(err)
		}
		at := wall.Add(-written)
		if err := os.Chtimes(p, at, at); err != nil {
			t.Fatal(err)
		}
		return p
	}
	spool := func(t *testing.T, dir string, written time.Duration) string {
		t.Helper()
		return spoolMode(t, dir, written, 0o600)
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
		{"a spool file of this user's left group-writable, 0o620, as by chmod -R g+w, is read whatever its mode, and deleted by its Sent", func(t *testing.T, dir string) {
			expire(t, dir, spoolMode(t, dir, day, 0o620), 1)
		}},
		{"one left readable to all, 0o664, as a copy under umask 002 makes it, is read and deleted by its Sent too", func(t *testing.T, dir string) {
			expire(t, dir, spoolMode(t, dir, day, 0o664), 1)
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
		{"a FIFO that took a spool file's place after Expire listed it is dated by its write time and left alone, and the dating doesn't wait on its write end", func(t *testing.T, dir string) {
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
			type result struct {
				gone bool
				err  error
			}
			done := make(chan result, 1)
			go func() {
				gone, err := expireFile(dir, SpoolName, time.Now().Add(-SpoolKeep))
				done <- result{gone, err}
			}()
			select {
			case r := <-done:
				if r.gone || r.err != nil {
					t.Errorf("expireFile = %v, %v; want the FIFO, made just now, left alone", r.gone, r.err)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("dating the FIFO waited on its write end")
			}
			if fi, err := os.Lstat(p); err != nil || fi.Mode()&fs.ModeNamedPipe == 0 {
				t.Errorf("the FIFO is gone or changed: %v, %v", fi, err)
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
		{"Expire leaves an inbox reached through a symbolic link to a directory this user doesn't own as it is: a week-old spool file there stays", func(t *testing.T, dir string) {
			if os.Geteuid() == 0 {
				t.Skip("root owns the directory the link names")
			}
			const shared = "/tmp"
			if fi, err := os.Stat(shared); err != nil || mine(shared, fi) == nil {
				t.Skipf("%s isn't another user's directory here", shared)
			}
			f, err := os.CreateTemp(shared, "spool-pairtime-test-*.bad")
			if err != nil {
				t.Skipf("can't make a file in %s: %v", shared, err)
			}
			old := f.Name()
			f.Close()
			t.Cleanup(func() { os.Remove(old) })
			then := time.Now().Add(-8 * day)
			if err := os.Chtimes(old, then, then); err != nil {
				t.Fatal(err)
			}
			link := filepath.Join(dir, "inbox")
			if err := os.Symlink(shared, link); err != nil {
				t.Fatal(err)
			}
			if n, err := Expire(link, time.Now()); n != 0 || err != nil {
				t.Errorf("Expire = %d, %v; want 0, nil", n, err)
			}
			if _, err := os.Lstat(old); err != nil {
				t.Errorf("%s went: %v", old, err)
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

// TestExposedPaths checks which paths doctor names as open to other
// accounts on this computer (ExposedPaths): a folder they may write in and
// a file they may read or write, each with the chmod that closes it and
// its path escaped for a shell, and none of a private folder or file, a
// symbolic link or a path that isn't there.
func TestExposedPaths(t *testing.T) {
	// mk makes name in dir, a folder when mode is one, with mode mode past
	// the umask, and returns its path.
	mk := func(t *testing.T, dir, name string, mode os.FileMode) string {
		t.Helper()
		p := filepath.Join(dir, name)
		var err error
		if mode.IsDir() {
			err = os.Mkdir(p, 0o700)
		} else {
			err = os.WriteFile(p, []byte("\n"), 0o600)
		}
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(p, mode.Perm()); err != nil {
			t.Fatal(err)
		}
		return p
	}
	cases := []struct {
		name string
		path func(t *testing.T, dir string) string
		// fix is the command wanted for the path, as shellEscape writes it,
		// "" for none.
		fix string
	}{
		{"a folder others may write in, as after chmod -R g+w, needs chmod go-w", func(t *testing.T, dir string) string {
			return mk(t, dir, "data", os.ModeDir|0o775)
		}, "chmod go-w "},
		{"a file left group-writable, 0o620, needs chmod 600", func(t *testing.T, dir string) string {
			return mk(t, dir, "held.jsonl", 0o620)
		}, "chmod 600 "},
		{"a file readable to all, 0o644, as a copy under umask 022 leaves it, needs chmod 600", func(t *testing.T, dir string) string {
			return mk(t, dir, "held.jsonl", 0o644)
		}, "chmod 600 "},
		{"a folder with a space in its name has the space escaped, so the shell reads the command whole", func(t *testing.T, dir string) string {
			return mk(t, dir, "Application Support", os.ModeDir|0o775)
		}, "chmod go-w "},
		{"a private folder is passed over", func(t *testing.T, dir string) string {
			return mk(t, dir, "data", os.ModeDir|0o700)
		}, ""},
		{"a private file is passed over", func(t *testing.T, dir string) string {
			return mk(t, dir, "held.jsonl", 0o600)
		}, ""},
		{"a symbolic link is passed over", func(t *testing.T, dir string) string {
			p := filepath.Join(dir, "link")
			if err := os.Symlink(mk(t, dir, "held.jsonl", 0o666), p); err != nil {
				t.Fatal(err)
			}
			return p
		}, ""},
		{"a path that isn't there is passed over", func(t *testing.T, dir string) string {
			return filepath.Join(dir, "gone")
		}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := c.path(t, t.TempDir())
			got := ExposedPaths(p)
			if c.fix == "" {
				if len(got) != 0 {
					t.Errorf("ExposedPaths(%q) = %+v, want none", p, got)
				}
				return
			}
			want := c.fix + shellEscape(p)
			if len(got) != 1 || got[0].Path != p || got[0].Fix != want {
				t.Fatalf("ExposedPaths(%q) = %+v, want %q", p, got, want)
			}
			if strings.Contains(p, " ") && strings.Contains(got[0].Fix, "Application Support") {
				t.Errorf("the fix %q leaves the space in its path unescaped", got[0].Fix)
			}
		})
	}
}
