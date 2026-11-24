// SPDX-License-Identifier: AGPL-3.0-or-later

package inbox

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestExpire checks inbox/'s bound (D-inbox-age-cap): a spool file that
// nothing has written for SpoolKeep, by its write time on the device's
// clock, is deleted unread, and a younger one stays. A clock that jumps
// forward deletes early, and one that jumps back keeps. The files it
// deletes are the spool, a file a drain took and one it set aside; the
// stopped mark and any other file stay.
func TestExpire(t *testing.T) {
	day := 24 * time.Hour
	spools := []string{SpoolName, "spool-00000000000000000001-00000001" + takenSuffix, "spool-00000000000000000002-00000002" + badSuffix}
	others := []string{StoppedName, "notes.txt"}
	cases := []struct {
		name string
		// age is how long before the real time each spool file was last
		// written, and jump how far the clock Expire reads is from the real
		// time.
		age, jump time.Duration
		deleted   bool
	}{
		{"a spool file last written 6 days 23 hours ago stays", 7*day - time.Hour, 0, false},
		{"one last written 7 days ago is deleted unread", 7 * day, 0, true},
		{"a clock that jumps forward 8 days deletes a file written an hour ago", time.Hour, 8 * day, true},
		{"a clock that jumps back a day keeps a file written 7 days before the real time", 7 * day, -day, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			wall := time.Now()
			write := func(name string, at time.Time) {
				p := filepath.Join(dir, name)
				if err := os.WriteFile(p, []byte("\n{}\n"), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Chtimes(p, at, at); err != nil {
					t.Fatal(err)
				}
			}
			for _, n := range spools {
				write(n, wall.Add(-c.age))
			}
			for _, n := range others {
				write(n, wall.Add(-30*day))
			}
			got, err := Expire(dir, wall.Add(c.jump))
			if err != nil {
				t.Fatal(err)
			}
			want := 0
			if c.deleted {
				want = len(spools)
			}
			if got != want {
				t.Errorf("Expire deleted %d files, want %d", got, want)
			}
			for _, n := range spools {
				_, err := os.Lstat(filepath.Join(dir, n))
				if gone := errors.Is(err, fs.ErrNotExist); gone != c.deleted {
					t.Errorf("%s is gone %v, want %v (%v)", n, gone, c.deleted, err)
				}
			}
			for _, n := range others {
				if _, err := os.Lstat(filepath.Join(dir, n)); err != nil {
					t.Errorf("%s, which isn't a spool file, went: %v", n, err)
				}
			}
		})
	}
	t.Run("an inbox that was never made has nothing to delete", func(t *testing.T) {
		if got, err := Expire(filepath.Join(t.TempDir(), "inbox"), time.Now()); got != 0 || err != nil {
			t.Errorf("Expire = %d, %v; want 0, nil", got, err)
		}
	})
}

// TestStopSpooling checks that a spool the agent stopped, as once its
// ledger can't open until pairtime restore runs, takes nothing, and takes
// messages again once the agent resumes it (D-inbox-age-cap).
func TestStopSpooling(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "inbox")
	steps := []struct {
		name string
		run  func(t *testing.T)
	}{
		{"a spool the agent stopped refuses a message, and nothing is written", func(t *testing.T) {
			if err := StopSpooling(dir); err != nil {
				t.Fatal(err)
			}
			if !SpoolStopped(dir) {
				t.Fatal("the spool isn't stopped")
			}
			if err := Append(dir, newMessage(t, 1)); !errors.Is(err, ErrSpoolStopped) {
				t.Fatalf("Append = %v, want ErrSpoolStopped", err)
			}
			if _, err := os.Lstat(filepath.Join(dir, SpoolName)); !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("the spool was written: %v", err)
			}
		}},
		{"once resumed, it takes messages again", func(t *testing.T) {
			if err := ResumeSpooling(dir); err != nil {
				t.Fatal(err)
			}
			if SpoolStopped(dir) {
				t.Fatal("the spool is still stopped")
			}
			if err := Append(dir, newMessage(t, 2)); err != nil {
				t.Fatal(err)
			}
		}},
		{"resuming a spool that isn't stopped does nothing", func(t *testing.T) {
			if err := ResumeSpooling(dir); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, s := range steps {
		if !t.Run(s.name, s.run) {
			break
		}
	}
}

// TestSetAside checks that a file set aside keeps its write time, so a
// drain and Expire delete it SpoolKeep after it was last written, not
// after it was set aside (D-inbox-age-cap).
func TestSetAside(t *testing.T) {
	cases := []struct {
		name string
		age  time.Duration
	}{
		{"a file a drain took 6 days after its last write keeps that write time once set aside", 6 * 24 * time.Hour},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			taken := filepath.Join(t.TempDir(), "spool-00000000000000000001-00000001"+takenSuffix)
			written := time.Now().Add(-c.age).Truncate(time.Second)
			if err := os.WriteFile(taken, []byte("\n{}\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Chtimes(taken, written, written); err != nil {
				t.Fatal(err)
			}
			if err := setAside(taken); err != nil {
				t.Fatal(err)
			}
			fi, err := os.Stat(strings.TrimSuffix(taken, takenSuffix) + badSuffix)
			if err != nil {
				t.Fatal(err)
			}
			if !fi.ModTime().Equal(written) {
				t.Errorf("the file set aside was last written at %v, want %v", fi.ModTime(), written)
			}
		})
	}
}
