// SPDX-License-Identifier: AGPL-3.0-or-later

package inbox

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestExpire checks inbox/'s bound (D-inbox-age-cap): a spool file is
// deleted unread once SpoolKeep has passed since its first call was sent,
// by the device's clock, with the calls spooled after it, and a younger
// one stays. A line that holds no call, as a record a sender's write cut
// short, is passed over for the call after it. A file none of whose lines
// holds a call is dated by its write time, and so is one last written
// before its first call was sent, as across a step back of the clock. A
// clock that jumps forward deletes early, and one that jumps back keeps.
// The files it deletes are the spool, a file a drain took and one it set
// aside; the stopped mark and any other file stay.
func TestExpire(t *testing.T) {
	day := 24 * time.Hour
	spools := []string{SpoolName, "spool-00000000000000000001-00000001" + takenSuffix, "spool-00000000000000000002-00000002" + badSuffix}
	others := []string{StoppedName, "notes.txt"}
	// call is a call sent ago before wall, MaxMessage bytes long if big.
	call := func(t *testing.T, wall time.Time, ago time.Duration, big bool) string {
		return string(encoded(t, wall.Add(-ago), big))
	}
	// cut is a record a sender's write cut short, as long as one can be.
	cut := func(t *testing.T, wall time.Time) string {
		return call(t, wall, 0, true)[:MaxMessage-1]
	}
	cases := []struct {
		name string
		// records are each spool file's records, for the real time wall,
		// each with the newlines Append writes around it. written is how
		// long before the real time each spool file was last written, and
		// jump how far the clock Expire reads is from the real time.
		records       func(t *testing.T, wall time.Time) []string
		written, jump time.Duration
		deleted       bool
	}{
		{name: "a spool file whose first call was sent 6 days 23 hours ago stays", records: func(t *testing.T, wall time.Time) []string {
			return []string{call(t, wall, 7*day-time.Hour, false), call(t, wall, 0, false)}
		}, written: 7*day - time.Hour},
		{name: "one whose first call was sent 7 days ago is deleted unread, though it was written yesterday", records: func(t *testing.T, wall time.Time) []string {
			return []string{call(t, wall, 7*day, false), call(t, wall, 0, false)}
		}, written: day, deleted: true},
		{name: "a first call MaxMessage bytes long is read whole: sent 7 days ago, its file written yesterday is deleted", records: func(t *testing.T, wall time.Time) []string {
			return []string{call(t, wall, 7*day, true), call(t, wall, 0, false)}
		}, written: day, deleted: true},
		{name: "a first record cut short is passed over: the call after it, sent 7 days ago, dates its file, written yesterday, which is deleted", records: func(t *testing.T, wall time.Time) []string {
			return []string{`{"id":"cut sh`, call(t, wall, 7*day, false), call(t, wall, 0, false)}
		}, written: day, deleted: true},
		{name: "a record cut short as long as one can be is passed over for a call MaxMessage bytes long after it", records: func(t *testing.T, wall time.Time) []string {
			return []string{cut(t, wall), call(t, wall, 7*day, true)}
		}, written: day, deleted: true},
		{name: "a file none of whose lines holds a call is dated by its write time: written 7 days ago, it is deleted", records: func(t *testing.T, wall time.Time) []string {
			return []string{`{"id":"cut sh`, "not a call"}
		}, written: 7 * day, deleted: true},
		{name: "a file none of whose lines holds a call, written 6 days 23 hours ago, stays", records: func(t *testing.T, wall time.Time) []string {
			return []string{`{"id":"cut sh`, "not a call"}
		}, written: 7*day - time.Hour},
		{name: "a file last written before its first call was sent, as across a step back of the clock, is dated by its write time", records: func(t *testing.T, wall time.Time) []string {
			return []string{call(t, wall, time.Hour, false)}
		}, written: 7 * day, deleted: true},
		{name: "a clock that jumps forward 8 days deletes a file whose first call was sent an hour ago", records: func(t *testing.T, wall time.Time) []string {
			return []string{call(t, wall, time.Hour, false)}
		}, written: time.Hour, jump: 8 * day, deleted: true},
		{name: "a clock that jumps back a day keeps a file whose first call was sent 7 days before the real time", records: func(t *testing.T, wall time.Time) []string {
			return []string{call(t, wall, 7*day, false)}
		}, written: 7 * day, jump: -day},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			wall := time.Now()
			var content []byte
			for _, r := range c.records(t, wall) {
				content = append(append(append(content, '\n'), r...), '\n')
			}
			write := func(name string, b []byte, at time.Time) {
				p := filepath.Join(dir, name)
				if err := os.WriteFile(p, b, 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Chtimes(p, at, at); err != nil {
					t.Fatal(err)
				}
			}
			for _, n := range spools {
				write(n, content, wall.Add(-c.written))
			}
			for _, n := range others {
				write(n, []byte("\n{}\n"), wall.Add(-30*day))
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

// encoded is a message sent at sent as Append writes it, without the
// newlines around it, MaxMessage bytes long if big.
func encoded(t *testing.T, sent time.Time, big bool) []byte {
	t.Helper()
	m := New("test", json.RawMessage(`""`))
	m.Sent = sent.UTC()
	b, err := m.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if !big {
		return b
	}
	m.Body = json.RawMessage(`"` + strings.Repeat("a", MaxMessage-len(b)) + `"`)
	if b, err = m.Encode(); err != nil {
		t.Fatal(err)
	}
	if len(b) != MaxMessage {
		t.Fatalf("the big message is %d bytes, want %d", len(b), MaxMessage)
	}
	return b
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

// TestSetAside checks that a file set aside keeps its write time, by which
// a drain deletes it, and Expire dates it when it holds no call Expire
// reads (spooledAt), so setting it aside never keeps its calls longer
// (D-inbox-age-cap).
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
