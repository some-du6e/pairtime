// SPDX-License-Identifier: AGPL-3.0-or-later

package inbox

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// TestExpire checks inbox/'s bound (D-inbox-age-cap): a spool file is
// deleted unread once SpoolKeep has passed since appendSlack before its
// first call was sent, by the device's clock, with the calls spooled after
// it, and a younger one stays. A line that holds no call, as a record a
// sender's write cut short, is passed over for the call after it. A file
// none of whose lines holds a call is dated by its write time, and so is
// one last written before its first call was sent, as across a step back
// of the clock. A clock that jumps forward deletes early, and one that
// jumps back keeps. The files it deletes are the spool, a file a drain
// took, one it set aside and one no drain reads; the stopped mark and any
// other file stay.
func TestExpire(t *testing.T) {
	day := 24 * time.Hour
	spools := []string{SpoolName, "spool-00000000000000000001-00000001" + takenSuffix, "spool-00000000000000000002-00000002" + badSuffix,
		"spool-00000000000000000003-00000003" + partSuffix}
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
		{name: "a first call sent 7 days less 5 seconds ago dates its file appendSlack before it, past 7 days, so it is deleted: a call after it may have been sent that much earlier", records: func(t *testing.T, wall time.Time) []string {
			return []string{call(t, wall, 7*day-5*time.Second, false), call(t, wall, 0, false)}
		}, written: day, deleted: true},
		{name: "one sent 7 days less 11 seconds ago stays", records: func(t *testing.T, wall time.Time) []string {
			return []string{call(t, wall, 7*day-11*time.Second, false), call(t, wall, 0, false)}
		}, written: day},
		{name: "a first call MaxMessage bytes long is read whole: sent 7 days ago, its file written yesterday is deleted", records: func(t *testing.T, wall time.Time) []string {
			return []string{call(t, wall, 7*day, true), call(t, wall, 0, false)}
		}, written: day, deleted: true},
		{name: "a first record cut short is passed over: the call after it, sent 7 days ago, dates its file, written yesterday, which is deleted", records: func(t *testing.T, wall time.Time) []string {
			return []string{`{"id":"cut sh`, call(t, wall, 7*day, false), call(t, wall, 0, false)}
		}, written: day, deleted: true},
		{name: "a record cut short in what it holds of a call gives its own Sent: one sent 7 days ago dates its file, written yesterday, with no whole call in it", records: func(t *testing.T, wall time.Time) []string {
			c := call(t, wall, 7*day, false)
			return []string{c[:len(c)-2]} // cut in its body, which comes after its sent
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

// TestExpireDatedFile checks that Expire deletes only the spool file it
// dated (#388): a drain can take the spool and a sender start a fresh one
// in its place between the two, and the fresh one's calls then go to the
// drains, while the file the drain took waits for the next Expire.
func TestExpireDatedFile(t *testing.T) {
	cases := []struct {
		name string
		// between runs after Expire dates the spool as past its time and
		// before it moves the spool aside.
		between func(t *testing.T, dir string, fresh Message)
		deleted int
	}{
		{"a drain's take and a fresh spool between the two leave the fresh spool's call for the drains, and the old one for the next Expire", func(t *testing.T, dir string, fresh Message) {
			if name, err := take(dir); err != nil || name == "" {
				t.Fatalf("take = %q, %v", name, err)
			}
			writeSpool(t, dir, SpoolName, time.Now(), fresh)
		}, 0},
		{"with nothing between them, the spool Expire dated goes", func(t *testing.T, dir string, fresh Message) {}, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			now := time.Now()
			old := newMessage(t, 1)
			old.Sent = now.Add(-8 * 24 * time.Hour).UTC()
			writeSpool(t, dir, SpoolName, now.Add(-8*24*time.Hour), old)
			fresh := newMessage(t, 2)
			expireDated = func(name string) {
				expireDated = nil
				c.between(t, dir, fresh)
			}
			t.Cleanup(func() { expireDated = nil })
			if n, err := Expire(dir, now); n != c.deleted || err != nil {
				t.Errorf("Expire = %d, %v; want %d, nil", n, err, c.deleted)
			}
			held := func(m Message) bool {
				found, err := Scan(dir, 10, func(s Message) bool { return s.ID == m.ID })
				if err != nil {
					t.Fatal(err)
				}
				return found
			}
			if c.deleted == 0 {
				if !held(fresh) {
					t.Errorf("the fresh spool's call is gone; the inbox has %v", spoolFiles(t, dir))
				}
				if !held(old) {
					t.Errorf("the file the drain took went before an Expire dated it; the inbox has %v", spoolFiles(t, dir))
				}
				if n, err := Expire(dir, now); n != 1 || err != nil || held(old) || !held(fresh) {
					t.Errorf("the next Expire = %d, %v, with the old call there %v and the fresh one %v; want the old call's file deleted, the fresh one kept", n, err, held(old), held(fresh))
				}
				return
			}
			if left := spoolFiles(t, dir); len(left) != 0 {
				t.Errorf("the inbox has %v, want nothing", left)
			}
		})
	}
}

// TestAppendOrder checks that each spool file's first call stays its
// oldest (#388): a call sent before the spool's first, by more than
// appendSlack, as by a sender that slept between New and Append, goes in a
// spool file of its own, so Expire dates each file by its own oldest call.
// One sent within appendSlack of it, as by senders that wrote at once,
// goes in the spool.
func TestAppendOrder(t *testing.T) {
	day := 24 * time.Hour
	cases := []struct {
		name    string
		ago     []time.Duration // how long before now each call was sent, in the order appended
		files   int             // spool files after the appends
		expired int             // of them, how many Expire deletes now
	}{
		{"a call sent 8 days ago, appended after yesterday's, goes in a spool file of its own, which Expire deletes, keeping yesterday's spool", []time.Duration{day, 8 * day}, 2, 1},
		{"a call sent 5 seconds before the spool's first, as from a sender that wrote at the same time, goes in the spool", []time.Duration{0, 5 * time.Second}, 1, 0},
		{"a call sent after the spool's first goes in the spool", []time.Duration{time.Hour, 0}, 1, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "inbox")
			now := time.Now()
			var msgs []Message
			for i, ago := range c.ago {
				m := newMessage(t, i)
				m.Sent = now.Add(-ago).UTC()
				if err := Append(dir, m); err != nil {
					t.Fatal(err)
				}
				msgs = append(msgs, m)
			}
			if got := SpoolFiles(dir); len(got) != c.files {
				t.Fatalf("the inbox has %v, want %d spool files", got, c.files)
			}
			if n, err := Expire(dir, now); n != c.expired || err != nil {
				t.Errorf("Expire = %d, %v; want %d, nil", n, err, c.expired)
			}
			for i, m := range msgs {
				found, err := Scan(dir, 10, func(s Message) bool { return s.ID == m.ID })
				if err != nil {
					t.Fatal(err)
				}
				if want := c.ago[i] < SpoolKeep; found != want {
					t.Errorf("the call sent %v ago is in the inbox %v, want %v", c.ago[i], found, want)
				}
			}
		})
	}
	t.Run("a call spooled alone counts toward MaxSpool", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "inbox")
		first := newMessage(t, 1)
		if err := Append(dir, first); err != nil {
			t.Fatal(err)
		}
		old := maxSpool
		t.Cleanup(func() { maxSpool = old })
		maxSpool = 200 // a message is about 110 bytes
		late := newMessage(t, 2)
		late.Sent = first.Sent.Add(-time.Hour)
		if err := Append(dir, late); !errors.Is(err, ErrSpoolFull) {
			t.Errorf("Append = %v, want ErrSpoolFull", err)
		}
	})
}

// TestAppendExpires checks that Append deletes, unread, the spool files
// past their time before it spools a call, as a sender spools exactly
// while the agent is down (#388), and does so while the agent has stopped
// the spool too.
func TestAppendExpires(t *testing.T) {
	cases := []struct {
		name    string
		stopped bool
	}{
		{"before it spools, Append deletes the spool files whose first call was sent 8 days ago, so the call starts a fresh spool", false},
		{"with the spool stopped, Append still deletes them, though it spools nothing", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			then := time.Now().Add(-8 * 24 * time.Hour)
			old := newMessage(t, 1)
			old.Sent = then.UTC()
			for _, name := range []string{SpoolName, "spool-00000000000000000001-00000001" + takenSuffix} {
				writeSpool(t, dir, name, then, old)
			}
			if c.stopped {
				if err := StopSpooling(dir); err != nil {
					t.Fatal(err)
				}
			}
			fresh := newMessage(t, 2)
			err := Append(dir, fresh)
			if c.stopped != errors.Is(err, ErrSpoolStopped) || !c.stopped && err != nil {
				t.Fatalf("Append = %v", err)
			}
			var got []string
			if _, err := Scan(dir, 10, func(m Message) bool {
				got = append(got, m.ID)
				return false
			}); err != nil {
				t.Fatal(err)
			}
			want := []string{fresh.ID}
			if c.stopped {
				want = nil
			}
			if !slices.Equal(got, want) {
				t.Errorf("the inbox holds %v, want %v; files %v", got, want, spoolFiles(t, dir))
			}
		})
	}
}

// writeSpool writes the spool file name in dir holding msgs, as Append
// writes them, last written at at.
func writeSpool(t *testing.T, dir, name string, at time.Time, msgs ...Message) {
	t.Helper()
	var b []byte
	for _, m := range msgs {
		line, err := m.Encode()
		if err != nil {
			t.Fatal(err)
		}
		b = append(append(append(b, '\n'), line...), '\n')
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, b, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(p, at, at); err != nil {
		t.Fatal(err)
	}
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

// TestCutSent checks that a record a sender's write cut short anywhere in
// what it holds of a call still gives its sender's Sent (cutSent), as
// Encode writes a message's id, kind and sent before its chain and its
// body, and that one cut before its sent ended gives none, and holds
// nothing of the call. A change to Message that put any of the call before
// sent would fail it.
func TestCutSent(t *testing.T) {
	m := New("hook", json.RawMessage(`{"prompt":"what the teen asked","cwd":"/home/teen/project"}`))
	m.Mono, m.BootID = 1234, "boot_1"
	m.Chain = []Proc{{PID: 4242, Name: "claude"}}
	b, err := m.Encode()
	if err != nil {
		t.Fatal(err)
	}
	// content is where the record begins to hold the call: its chain, or
	// its body, whichever comes first.
	content := len(b)
	for _, member := range []string{`"chain":`, `"body":`} {
		if i := bytes.Index(b, []byte(member)); i >= 0 && i < content {
			content = i
		}
	}
	sent := bytes.Index(b, []byte(`"sent":"`))
	if sent < 0 || content == len(b) {
		t.Fatalf("the record %s has no sent, or no chain and body", b)
	}
	cases := []struct {
		name     string
		from, to int // the cuts, b[:from] to b[:to-1]
		ok       bool
	}{
		{"every cut from the first byte of the call's chain or body to a byte short of the record's end gives its Sent", content + 1, len(b), true},
		{"every cut inside its sent gives none", sent + len(`"sent":"`), sent + len(`"sent":"`) + 20, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			for n := c.from; n < c.to; n++ {
				s, ok := cutSent(b[:n])
				if ok != c.ok || ok && !s.Equal(m.Sent) {
					t.Fatalf("cutSent of the record cut at %d of %d, %q, = %v, %v; want %v", n, len(b), b[:n], s, ok, c.ok)
				}
			}
		})
	}
}
