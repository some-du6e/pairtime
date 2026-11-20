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
				if err := take(inbox); err != nil {
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
						if err := take(inbox); err != nil {
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
				names, err := taken(inbox)
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
