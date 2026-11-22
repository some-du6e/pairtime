// SPDX-License-Identifier: AGPL-3.0-or-later

package inbox

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/platform/clock"
)

func newMessage(t *testing.T, n int) Message {
	t.Helper()
	return New("test", json.RawMessage(fmt.Sprintf(`{"n":%d}`, n)))
}

func utcNow() time.Time { return time.Now().UTC() }

// stampNow is the drain's clock in tests: the wall time, with no monotonic
// reading.
func stampNow() clock.Stamp { return clock.Stamp{Wall: utcNow()} }

// spoolFiles lists what is in the inbox directory.
func spoolFiles(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

// journalIDs reads every id in a journal directory, with how often each
// appears, the way a test that trusts nothing in this package would.
func journalIDs(t *testing.T, dir string) map[string]int {
	t.Helper()
	ids := make(map[string]int)
	names, err := filepath.Glob(filepath.Join(dir, "*.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		f, err := os.Open(name)
		if err != nil {
			t.Fatal(err)
		}
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			var l struct {
				ID string `json:"id"`
			}
			if json.Unmarshal(sc.Bytes(), &l) == nil && l.ID != "" {
				ids[l.ID]++
			}
		}
		f.Close()
		if err := sc.Err(); err != nil {
			t.Fatal(err)
		}
	}
	return ids
}

func openJournal(t *testing.T, dir string) *Journal {
	t.Helper()
	j, err := OpenJournal(dir, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { j.Close() })
	return j
}

// keepSink keeps every delivery it gets.
type keepSink struct{ got []Delivery }

func (k *keepSink) Deliver(d Delivery) error {
	k.got = append(k.got, d)
	return nil
}

func drain(t *testing.T, dir string, sink Sink) DrainResult {
	t.Helper()
	res, err := Drain(dir, sink, stampNow)
	if err != nil {
		t.Fatalf("Drain: %v", err)
	}
	return res
}

func TestAppendDrain(t *testing.T) {
	tests := []struct {
		name string
		run  func(t *testing.T, inbox, journal string)
	}{
		{
			name: "a spooled message reaches the sink once, and the drained file is deleted",
			run: func(t *testing.T, inbox, journal string) {
				m := newMessage(t, 1)
				if err := Append(inbox, m); err != nil {
					t.Fatal(err)
				}
				j := openJournal(t, journal)
				if res := drain(t, inbox, j); res.Delivered != 1 || res.Files != 1 || res.Bad != 0 {
					t.Errorf("Drain = %+v, want one file and one message", res)
				}
				if res := drain(t, inbox, j); res.Delivered != 0 || res.Files != 0 {
					t.Errorf("a second Drain = %+v, want nothing", res)
				}
				if left := spoolFiles(t, inbox); len(left) != 0 {
					t.Errorf("the inbox still has %v", left)
				}
				if got := journalIDs(t, journal); got[m.ID] != 1 || len(got) != 1 {
					t.Errorf("the journal has %v, want %s once", got, m.ID)
				}
			},
		},
		{
			name: "a drained message keeps its sender's clock and parent chain, has no peer pid, and was received when the drain read it",
			run: func(t *testing.T, inbox, journal string) {
				m := newMessage(t, 1)
				m.Mono, m.BootID = 81163000, "6f0f6f6e-2b1c-4c5e-9d07-0e3c1b2a4f55"
				m.Chain = []Proc{
					{PID: 4242, Name: "pairtime", Start: m.Sent.Add(-time.Second)},
					{PID: 4100, Name: "claude", Start: m.Sent.Add(-time.Hour)},
					{PID: 1},
				}
				appendAll(t, inbox, m)
				read := clock.Stamp{Wall: m.Sent.Add(36 * time.Hour), Mono: 9000, BootID: "b-drain"} // the agent was down for a day and a half
				k := &keepSink{}
				if _, err := Drain(inbox, k, func() clock.Stamp { return read }); err != nil {
					t.Fatal(err)
				}
				if len(k.got) != 1 {
					t.Fatalf("the sink got %d messages, want 1", len(k.got))
				}
				d := k.got[0]
				if d.ID != m.ID || !d.Sent.Equal(m.Sent) || d.Mono != m.Mono || d.BootID != m.BootID || !slices.EqualFunc(d.Chain, m.Chain, sameProc) {
					t.Errorf("the drain delivered %+v, want the sender's id, Sent, Mono, BootID and Chain in %+v", d.Message, m)
				}
				if d.Via != ViaInbox || d.PeerPID != 0 || d.Received != read {
					t.Errorf("Via, PeerPID, Received = %q, %d, %+v; want the inbox, 0 and the agent's stamp from when the drain read it, %+v", d.Via, d.PeerPID, d.Received, read)
				}
			},
		},
		{
			name: "a file that a killed drain left behind is drained again, and its messages count once",
			run: func(t *testing.T, inbox, journal string) {
				m1, m2 := newMessage(t, 1), newMessage(t, 2)
				for _, m := range []Message{m1, m2} {
					if err := Append(inbox, m); err != nil {
						t.Fatal(err)
					}
				}
				// The killed drain took the spool and stored m1, but never
				// got to m2 or to deleting the file.
				if _, err := take(inbox); err != nil {
					t.Fatal(err)
				}
				j := openJournal(t, journal)
				if err := j.Deliver(Delivery{Message: m1, Via: ViaInbox, Received: stampNow()}); err != nil {
					t.Fatal(err)
				}
				if res := drain(t, inbox, j); res.Delivered != 2 || res.Files != 1 {
					t.Errorf("Drain = %+v, want both messages of the one file", res)
				}
				if stored, dups := j.Stats(); stored != 2 || dups != 1 {
					t.Errorf("the journal stored %d and dropped %d, want 2 and 1", stored, dups)
				}
				if got := journalIDs(t, journal); got[m1.ID] != 1 || got[m2.ID] != 1 || len(got) != 2 {
					t.Errorf("the journal has %v, want each message once", got)
				}
			},
		},
		{
			name: "a record cut short by a killed sender loses only that record",
			run: func(t *testing.T, inbox, journal string) {
				if err := os.MkdirAll(inbox, 0o700); err != nil {
					t.Fatal(err)
				}
				cut := "\n{\"id\":\"0123456789abcdef0123456789abcdef\",\"kind\":\"te"
				if err := os.WriteFile(filepath.Join(inbox, SpoolName), []byte(cut), 0o600); err != nil {
					t.Fatal(err)
				}
				m := newMessage(t, 1)
				if err := Append(inbox, m); err != nil {
					t.Fatal(err)
				}
				j := openJournal(t, journal)
				if res := drain(t, inbox, j); res.Delivered != 1 || res.Bad != 1 {
					t.Errorf("Drain = %+v, want one message and one bad line", res)
				}
				if got := journalIDs(t, journal); got[m.ID] != 1 || len(got) != 1 {
					t.Errorf("the journal has %v, want only %s", got, m.ID)
				}
			},
		},
		{
			name: "a line too long to be a message is skipped, and the next one is read",
			run: func(t *testing.T, inbox, journal string) {
				if err := os.MkdirAll(inbox, 0o700); err != nil {
					t.Fatal(err)
				}
				long := "\n" + strings.Repeat("x", MaxMessage+100) + "\n"
				if err := os.WriteFile(filepath.Join(inbox, SpoolName), []byte(long), 0o600); err != nil {
					t.Fatal(err)
				}
				m := newMessage(t, 1)
				if err := Append(inbox, m); err != nil {
					t.Fatal(err)
				}
				j := openJournal(t, journal)
				if res := drain(t, inbox, j); res.Delivered != 1 || res.Bad != 1 {
					t.Errorf("Drain = %+v, want one message and one bad line", res)
				}
			},
		},
		{
			name: "a sender whose write lands after a drain took the spool writes the message again, into the new spool",
			run: func(t *testing.T, inbox, journal string) {
				if runtime.GOOS == "windows" {
					t.Skip("on Windows a drain can't take the spool while a sender has it open, so the write can't land late")
				}
				took := false
				afterWrite = func() {
					if !took {
						took = true
						if _, err := take(inbox); err != nil {
							t.Error(err)
						}
					}
				}
				t.Cleanup(func() { afterWrite = nil })
				m := newMessage(t, 1)
				if err := Append(inbox, m); err != nil {
					t.Fatal(err)
				}
				afterWrite = nil
				names, err := NewDrainer(inbox, nil, stampNow).taken()
				if err != nil {
					t.Fatal(err)
				}
				if len(names) != 1 {
					t.Fatalf("taken files: %v, want one", names)
				}
				for _, name := range []string{names[0], filepath.Join(inbox, SpoolName)} {
					b, err := os.ReadFile(name)
					if err != nil {
						t.Fatal(err)
					}
					if !strings.Contains(string(b), m.ID) {
						t.Errorf("%s doesn't have the message", name)
					}
				}
				j := openJournal(t, journal)
				if res := drain(t, inbox, j); res.Delivered != 2 || res.Files != 2 {
					t.Errorf("Drain = %+v, want two files with a copy each", res)
				}
				if stored, dups := j.Stats(); stored != 1 || dups != 1 {
					t.Errorf("the journal stored %d and dropped %d, want 1 and 1", stored, dups)
				}
			},
		},
		{
			name: "a full spool refuses another message",
			run: func(t *testing.T, inbox, journal string) {
				old := maxSpool
				// One message is about 110 bytes, so the second won't fit.
				maxSpool = 150
				t.Cleanup(func() { maxSpool = old })
				if err := Append(inbox, newMessage(t, 1)); err != nil {
					t.Fatal(err)
				}
				if err := Append(inbox, newMessage(t, 2)); !errors.Is(err, ErrSpoolFull) {
					t.Errorf("Append to a full spool = %v, want ErrSpoolFull", err)
				}
			},
		},
		{
			name: "many senders appending while the agent drains lose nothing and count nothing twice",
			run: func(t *testing.T, inbox, journal string) {
				const senders, each = 8, 150
				j := openJournal(t, journal)
				ids := make([][]string, senders)
				var wg sync.WaitGroup
				for g := range senders {
					wg.Add(1)
					go func() {
						defer wg.Done()
						for n := range each {
							m := newMessage(t, n)
							if err := Append(inbox, m); err != nil {
								t.Error(err)
								return
							}
							ids[g] = append(ids[g], m.ID)
						}
					}()
				}
				done := make(chan struct{})
				drained := make(chan error, 1)
				go func() {
					for {
						select {
						case <-done:
							drained <- nil
							return
						default:
						}
						if _, err := Drain(inbox, j, stampNow); err != nil {
							drained <- err
							return
						}
						time.Sleep(time.Millisecond)
					}
				}()
				wg.Wait()
				close(done)
				if err := <-drained; err != nil {
					t.Fatalf("Drain: %v", err)
				}
				drain(t, inbox, j)
				got := journalIDs(t, journal)
				total := 0
				for _, list := range ids {
					for _, id := range list {
						total++
						if got[id] != 1 {
							t.Errorf("message %s is in the journal %d times, want once", id, got[id])
						}
					}
				}
				if total != senders*each || len(got) != total {
					t.Errorf("sent %d messages and the journal has %d ids, want %d of each", total, len(got), senders*each)
				}
				if left := spoolFiles(t, inbox); len(left) != 0 {
					t.Errorf("the inbox still has %v", left)
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

// appendAll spools each message in turn.
func appendAll(t *testing.T, inbox string, ms ...Message) {
	t.Helper()
	for _, m := range ms {
		if err := Append(inbox, m); err != nil {
			t.Fatal(err)
		}
	}
}

// script is a Sink over a Journal that a test can make fail. It refuses
// the ids in reject for good, fails every message while down is set, and
// fails each id in failOnce the first time it is handed it. It counts how
// often it is handed each id.
type script struct {
	j        *Journal
	mu       sync.Mutex
	reject   map[string]bool
	failOnce map[string]bool
	down     error
	calls    map[string]int
}

func newScript(j *Journal) *script {
	return &script{j: j, reject: map[string]bool{}, failOnce: map[string]bool{}, calls: map[string]int{}}
}

func (s *script) Deliver(d Delivery) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls[d.ID]++
	switch {
	case s.down != nil:
		return s.down
	case s.reject[d.ID]:
		return fmt.Errorf("%w: the test says so", ErrRejected)
	case s.failOnce[d.ID]:
		delete(s.failOnce, d.ID)
		return errors.New("the disk is full, for now")
	}
	return s.j.Deliver(d)
}

func (s *script) setDown(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.down = err
}

func (s *script) handed(id string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls[id]
}

func TestDrainer(t *testing.T) {
	tests := []struct {
		name string
		run  func(t *testing.T, inbox, journal string)
	}{
		{
			name: "a message the sink rejects for good is skipped, and the rest of its file goes in",
			run: func(t *testing.T, inbox, journal string) {
				a, x, b := newMessage(t, 1), newMessage(t, 2), newMessage(t, 3)
				appendAll(t, inbox, a, x, b)
				s := newScript(openJournal(t, journal))
				s.reject[x.ID] = true
				res, err := Drain(inbox, s, stampNow)
				if err != nil {
					t.Fatal(err)
				}
				if res.Delivered != 2 || res.Rejected != 1 || res.Files != 1 {
					t.Errorf("Drain = %+v, want 2 delivered, 1 rejected and the file deleted", res)
				}
				if got := journalIDs(t, journal); got[a.ID] != 1 || got[b.ID] != 1 || got[x.ID] != 0 {
					t.Errorf("the journal has %v, want the two messages around the rejected one", got)
				}
				if left := spoolFiles(t, inbox); len(left) != 0 {
					t.Errorf("the inbox still has %v", left)
				}
			},
		},
		{
			name: "a sink that fails stops the drain and leaves the spool where it is, so MaxSpool still bounds the inbox",
			run: func(t *testing.T, inbox, journal string) {
				s := newScript(openJournal(t, journal))
				s.setDown(errors.New("received/ can't be written"))
				d := NewDrainer(inbox, s, stampNow)
				appendAll(t, inbox, newMessage(t, 1))
				if _, err := d.Drain(); err == nil {
					t.Fatal("Drain = nil with a sink that fails")
				}
				appendAll(t, inbox, newMessage(t, 2))
				for range 3 {
					if _, err := d.Drain(); err == nil {
						t.Fatal("Drain = nil with a sink that fails")
					}
				}
				// The first drain took the spool. None took it after that.
				if left := spoolFiles(t, inbox); len(left) != 2 || !slices.Contains(left, SpoolName) {
					t.Fatalf("the inbox has %v, want one taken file and the spool", left)
				}
				// So the spool fills, and Append says so. A message is
				// about 110 bytes.
				old := maxSpool
				maxSpool = 400
				t.Cleanup(func() { maxSpool = old })
				var err error
				for n := 3; err == nil && n < 20; n++ {
					err = Append(inbox, newMessage(t, n))
				}
				if !errors.Is(err, ErrSpoolFull) {
					t.Fatalf("Append = %v, want ErrSpoolFull once the spool fills", err)
				}
				// Once the sink is back, all of it goes in.
				s.setDown(nil)
				res, err := d.Drain()
				if err != nil {
					t.Fatal(err)
				}
				if res.Files != 2 || res.Delivered < 2 {
					t.Errorf("Drain = %+v, want both files in", res)
				}
				if left := spoolFiles(t, inbox); len(left) != 0 {
					t.Errorf("the inbox still has %v", left)
				}
			},
		},
		{
			name: "a drain that the sink stopped is picked up where it stopped",
			run: func(t *testing.T, inbox, journal string) {
				m1, m2, m3 := newMessage(t, 1), newMessage(t, 2), newMessage(t, 3)
				appendAll(t, inbox, m1, m2, m3)
				s := newScript(openJournal(t, journal))
				s.failOnce[m2.ID] = true
				d := NewDrainer(inbox, s, stampNow)
				if res, err := d.Drain(); err == nil || res.Delivered != 1 {
					t.Fatalf("the first Drain = %+v, %v; want one message in and the sink's failure", res, err)
				}
				res, err := d.Drain()
				if err != nil || res.Delivered != 2 || res.Files != 1 {
					t.Fatalf("the second Drain = %+v, %v; want the other two messages in and the file deleted", res, err)
				}
				for _, c := range []struct {
					name string
					id   string
					want int
				}{
					{"the message before the failure", m1.ID, 1},
					{"the message the sink failed", m2.ID, 2},
					{"the message after it", m3.ID, 1},
				} {
					if got := s.handed(c.id); got != c.want {
						t.Errorf("the sink was handed %s %d times, want %d", c.name, got, c.want)
					}
				}
			},
		},
		{
			name: "a file that can't be read is passed over, and set aside once it has failed MaxFileFailures drains",
			run: func(t *testing.T, inbox, journal string) {
				if runtime.GOOS == "windows" {
					t.Skip("on Windows a file's mode doesn't keep its owner from reading it")
				}
				if os.Geteuid() == 0 {
					t.Skip("root reads a file whatever its mode")
				}
				m1, m2 := newMessage(t, 1), newMessage(t, 2)
				appendAll(t, inbox, m1)
				first, err := take(inbox)
				if err != nil || first == "" {
					t.Fatalf("take = %q, %v", first, err)
				}
				if err := os.Chmod(first, 0); err != nil {
					t.Fatal(err)
				}
				appendAll(t, inbox, m2)
				d := NewDrainer(inbox, openJournal(t, journal), stampNow)
				for i := 1; i <= MaxFileFailures; i++ {
					res, err := d.Drain()
					if err == nil || res.Failed != 1 {
						t.Fatalf("drain %d = %+v, %v; want the unreadable file to fail", i, res, err)
					}
					if aside := res.SetAside == 1; aside != (i == MaxFileFailures) {
						t.Errorf("drain %d set aside %d files, want 1 only at drain %d", i, res.SetAside, MaxFileFailures)
					}
				}
				if got := journalIDs(t, journal); got[m2.ID] != 1 || got[m1.ID] != 0 {
					t.Errorf("the journal has %v, want only the message spooled after the unreadable file", got)
				}
				bad := strings.TrimSuffix(first, takenSuffix) + badSuffix
				if _, err := os.Lstat(bad); err != nil {
					t.Errorf("the unreadable file wasn't set aside: %v", err)
				}
				if res, err := d.Drain(); err != nil || res.Failed != 0 {
					t.Errorf("a drain after that = %+v, %v; want the file set aside left alone", res, err)
				}
			},
		},
		{
			name: "a file set aside is deleted KeepDays after it was set aside",
			run: func(t *testing.T, inbox, journal string) {
				if err := os.MkdirAll(inbox, 0o700); err != nil {
					t.Fatal(err)
				}
				old := filepath.Join(inbox, "spool-00000000000000000001-00000001"+badSuffix)
				fresh := filepath.Join(inbox, "spool-00000000000000000002-00000002"+badSuffix)
				for _, name := range []string{old, fresh} {
					if err := os.WriteFile(name, []byte("\n"), 0o600); err != nil {
						t.Fatal(err)
					}
				}
				then := time.Now().Add(-(KeepDays*24 + 1) * time.Hour)
				if err := os.Chtimes(old, then, then); err != nil {
					t.Fatal(err)
				}
				if _, err := Drain(inbox, openJournal(t, journal), stampNow); err != nil {
					t.Fatal(err)
				}
				if _, err := os.Lstat(old); !os.IsNotExist(err) {
					t.Errorf("the file set aside over KeepDays ago is still there: %v", err)
				}
				if _, err := os.Lstat(fresh); err != nil {
					t.Errorf("the file set aside today is gone: %v", err)
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

// TestScan checks what Scan hands its func: the spool's messages first,
// then those of the files drains took, oldest first, each file's in the
// order they were written and up to the limit, until the func takes one,
// passing over lines that aren't a message and files no drain reads.
func TestScan(t *testing.T) {
	cases := []struct {
		name string
		// files are the inbox's files by name, each a list of lines: n for
		// the message newMessage(n) makes, and -1 for a line that isn't a
		// message.
		files map[string][]int
		// perm is the files' mode, 0o600 when zero, dirPerm the inbox
		// directory's, 0o700 when zero, and link makes spool.jsonl a
		// symbolic link to a file of the messages files gives it. locked
		// names a file no one but root may open.
		perm, dirPerm os.FileMode
		link          bool
		locked        string
		// limit is Scan's, and stop the message the func takes, 0 for none.
		limit, stop int
		// read are the messages the func is handed, in order, want what Scan
		// reports, and unread whether it reports a file it couldn't read to
		// its end.
		read         []int
		want, unread bool
	}{
		{"the spool is read first, then the taken files, oldest first, each in order",
			map[string][]int{SpoolName: {1, 2}, "spool-2.drain": {5}, "spool-1.drain": {3, 4}}, 0, 0, false, "", 10, 0, []int{1, 2, 3, 4, 5}, false, false},
		{"the read ends at the first message the func takes, which Scan reports",
			map[string][]int{SpoolName: {1, 2}, "spool-1.drain": {3, 4}}, 0, 0, false, "", 10, 3, []int{1, 2, 3}, true, false},
		{"a file's messages past the limit aren't read, the next file's are, and Scan reports the file it couldn't read to its end",
			map[string][]int{SpoolName: {1, 2, 3}, "spool-1.drain": {4}}, 0, 0, false, "", 2, 0, []int{1, 2, 4}, false, true},
		{"a file of as many messages as the limit, with lines that aren't messages after them, is read to its end",
			map[string][]int{SpoolName: {1, 2, -1}}, 0, 0, false, "", 2, 0, []int{1, 2}, false, false},
		{"a line that isn't a message is passed over, and counts for nothing toward the limit",
			map[string][]int{SpoolName: {-1, 1, 2}}, 0, 0, false, "", 2, 0, []int{1, 2}, false, false},
		{"a file set aside is no drain's, and isn't read",
			map[string][]int{"spool-1.bad": {1}, "spool-2.drain": {2}}, 0, 0, false, "", 10, 0, []int{2}, false, false},
		{"a file that can't be opened, which a drain tries again, may hold any message, so Scan reports it, and reads the others on",
			map[string][]int{"spool-1.drain": {1}, "spool-2.drain": {2}}, 0, 0, false, "spool-1.drain", 10, 0, []int{2}, false, true},
		{"a spool others may open is no drain's, and isn't read",
			map[string][]int{SpoolName: {1}}, 0o644, 0, false, "", 10, 0, nil, false, false},
		{"a file Scan can't look up, in an inbox directory it may list but not search, may hold any message, so Scan reports it",
			map[string][]int{SpoolName: {1}}, 0, 0o600, false, "", 10, 0, nil, false, true},
		{"an inbox directory others may write in is no drain's, and nothing in it is read",
			map[string][]int{SpoolName: {1}}, 0, 0o775, false, "", 10, 0, nil, false, false},
		{"a spool that is a symbolic link is no drain's, and isn't read",
			map[string][]int{SpoolName: {1}}, 0, 0, true, "", 10, 0, nil, false, false},
		{"an inbox with nothing in it hands over nothing", map[string][]int{}, 0, 0, false, "", 10, 0, nil, false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if (c.perm != 0 || c.dirPerm != 0 || c.locked != "") && runtime.GOOS == "windows" {
				t.Skip("on Windows the data directory's DACL, not a file's mode, says who may open it")
			}
			if (c.locked != "" || c.dirPerm != 0 && c.dirPerm&0o100 == 0) && os.Geteuid() == 0 {
				t.Skip("root opens a file whatever its mode")
			}
			dir := filepath.Join(t.TempDir(), "inbox")
			if err := os.Mkdir(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			perm := c.perm
			if perm == 0 {
				perm = 0o600
			}
			for name, lines := range c.files {
				var b []byte
				for _, n := range lines {
					line := []byte("{not a message")
					if n >= 0 {
						var err error
						if line, err = newMessage(t, n).Encode(); err != nil {
							t.Fatal(err)
						}
					}
					b = append(append(b, line...), '\n')
				}
				path := filepath.Join(dir, name)
				if c.link {
					// The messages are in a file beside the inbox, which the link
					// names.
					path = filepath.Join(filepath.Dir(dir), name+".target")
				}
				if err := os.WriteFile(path, b, perm); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(path, perm); err != nil { // past the umask
					t.Fatal(err)
				}
				if c.link {
					if err := os.Symlink(path, filepath.Join(dir, name)); err != nil {
						t.Skipf("can't make a symbolic link here: %v", err)
					}
				}
			}
			if c.locked != "" {
				if err := os.Chmod(filepath.Join(dir, c.locked), 0); err != nil {
					t.Fatal(err)
				}
			}
			if c.dirPerm != 0 {
				if err := os.Chmod(dir, c.dirPerm); err != nil {
					t.Fatal(err)
				}
				// t.TempDir's cleanup removes what is in it.
				t.Cleanup(func() { os.Chmod(dir, 0o700) })
			}
			var read []int
			got, err := Scan(dir, c.limit, func(m Message) bool {
				var body struct{ N int }
				if err := json.Unmarshal(m.Body, &body); err != nil {
					t.Fatal(err)
				}
				read = append(read, body.N)
				return body.N == c.stop
			})
			if (err != nil) != c.unread || got != c.want || !slices.Equal(read, c.read) {
				t.Errorf("Scan handed over %v and reported %v, %v; want %v and %v, with an error %v", read, got, err, c.read, c.want, c.unread)
			}
		})
	}
}

func TestReadLine(t *testing.T) {
	tests := []struct {
		name      string
		in        string
		limit     int
		wantLines []string // a long line shows as "LONG"
	}{
		{name: "lines come back without their newline", in: "a\nbb\n", limit: 10, wantLines: []string{"a", "bb"}},
		{name: "a carriage return before the newline goes too", in: "a\r\nb\r\n", limit: 10, wantLines: []string{"a", "b"}},
		{name: "a last line without a newline comes back", in: "a\nb", limit: 10, wantLines: []string{"a", "b"}},
		{name: "blank lines come back empty", in: "\na\n\n", limit: 10, wantLines: []string{"", "a", ""}},
		{name: "a line over the limit is skipped to its end", in: "aaaaaaaaaaaa\nb\n", limit: 5, wantLines: []string{"LONG", "b"}},
		{name: "a last line over the limit without a newline is skipped", in: "a\nbbbbbbbbbbbb", limit: 5, wantLines: []string{"a", "LONG"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// A small buffer, so long lines come in several pieces.
			r := bufio.NewReaderSize(strings.NewReader(tt.in), 16)
			var got []string
			for {
				line, long, err := readLine(r, tt.limit)
				if errors.Is(err, io.EOF) {
					break
				}
				if err != nil {
					t.Fatal(err)
				}
				if long {
					got = append(got, "LONG")
				} else {
					got = append(got, string(line))
				}
			}
			if strings.Join(got, "|") != strings.Join(tt.wantLines, "|") || len(got) != len(tt.wantLines) {
				t.Errorf("lines = %q, want %q", got, tt.wantLines)
			}
		})
	}
}
