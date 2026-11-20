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
)

func newMessage(t *testing.T, n int) Message {
	t.Helper()
	return New("test", json.RawMessage(fmt.Sprintf(`{"n":%d}`, n)))
}

func utcNow() time.Time { return time.Now().UTC() }

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

func drain(t *testing.T, dir string, sink Sink) DrainResult {
	t.Helper()
	res, err := Drain(dir, sink, utcNow)
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
				if err := j.Deliver(Delivery{Message: m1, Via: ViaInbox, Received: utcNow()}); err != nil {
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
				names, err := NewDrainer(inbox, nil, utcNow).taken()
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
						if _, err := Drain(inbox, j, utcNow); err != nil {
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
				res, err := Drain(inbox, s, utcNow)
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
				d := NewDrainer(inbox, s, utcNow)
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
				d := NewDrainer(inbox, s, utcNow)
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
			name: "a file that can't be read is passed over, and set aside once it has failed maxFileFailures drains",
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
				d := NewDrainer(inbox, openJournal(t, journal), utcNow)
				for i := 1; i <= maxFileFailures; i++ {
					res, err := d.Drain()
					if err == nil || res.Failed != 1 {
						t.Fatalf("drain %d = %+v, %v; want the unreadable file to fail", i, res, err)
					}
					if aside := res.SetAside == 1; aside != (i == maxFileFailures) {
						t.Errorf("drain %d set aside %d files, want 1 only at drain %d", i, res.SetAside, maxFileFailures)
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
				if _, err := Drain(inbox, openJournal(t, journal), utcNow); err != nil {
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
