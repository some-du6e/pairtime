// SPDX-License-Identifier: AGPL-3.0-or-later

package inbox

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/platform"
	"github.com/some-du6e/pairtime/internal/platform/clock"
	"github.com/some-du6e/pairtime/pkg/event"
)

func TestJournal(t *testing.T) {
	day := time.Date(2026, 11, 19, 15, 0, 0, 0, time.UTC)
	deliver := func(t *testing.T, j *Journal, m Message, at time.Time) {
		t.Helper()
		if err := j.Deliver(Delivery{Message: m, Via: ViaSocket, PeerPID: 42, Received: clock.Stamp{Wall: at}}); err != nil {
			t.Fatal(err)
		}
	}
	open := func(t *testing.T, dir string, now time.Time) *Journal {
		t.Helper()
		j, err := OpenJournal(dir, now)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { j.Close() })
		return j
	}
	tests := []struct {
		name string
		run  func(t *testing.T, dir string)
	}{
		{
			name: "a second delivery of an id stores nothing",
			run: func(t *testing.T, dir string) {
				j := open(t, dir, day)
				m := newMessage(t, 1)
				deliver(t, j, m, day)
				deliver(t, j, m, day.Add(time.Minute))
				if stored, dups := j.Stats(); stored != 1 || dups != 1 {
					t.Errorf("stored %d and dropped %d, want 1 and 1", stored, dups)
				}
				if got := journalIDs(t, dir); got[m.ID] != 1 {
					t.Errorf("the journal has %v, want %s once", got, m.ID)
				}
			},
		},
		{
			name: "Store says it stored the first delivery of an id and not the second",
			run: func(t *testing.T, dir string) {
				j := open(t, dir, day)
				m := newMessage(t, 1)
				d := Delivery{Message: m, Via: ViaSocket, Received: clock.Stamp{Wall: day}}
				first, err1 := j.Store(d)
				second, err2 := j.Store(d)
				if !first || second || err1 != nil || err2 != nil {
					t.Errorf("Store = %v, %v then %v, %v; want true then false, with no error", first, err1, second, err2)
				}
			},
		},
		{
			name: "ids stored before a restart are known after it",
			run: func(t *testing.T, dir string) {
				m := newMessage(t, 1)
				j := open(t, dir, day)
				deliver(t, j, m, day)
				j.Close()
				j = open(t, dir, day)
				deliver(t, j, m, day)
				if stored, dups := j.Stats(); stored != 0 || dups != 1 {
					t.Errorf("after the restart: stored %d and dropped %d, want 0 and 1", stored, dups)
				}
			},
		},
		{
			name: "a line cut short by a killed agent doesn't swallow the line after it",
			run: func(t *testing.T, dir string) {
				if err := os.MkdirAll(dir, 0o700); err != nil {
					t.Fatal(err)
				}
				name := filepath.Join(dir, "2026-11-19.jsonl")
				if err := os.WriteFile(name, []byte(`{"id":"0123456789abcdef0123456789abcdef","ki`), 0o600); err != nil {
					t.Fatal(err)
				}
				m := newMessage(t, 1)
				j := open(t, dir, day)
				deliver(t, j, m, day)
				j.Close()
				j = open(t, dir, day)
				deliver(t, j, m, day)
				if stored, dups := j.Stats(); stored != 0 || dups != 1 {
					t.Errorf("stored %d and dropped %d, want 0 and 1: the id after the cut line was lost", stored, dups)
				}
			},
		},
		{
			name: "day files older than KeepDays are deleted at open, and their ids forgotten",
			run: func(t *testing.T, dir string) {
				m := newMessage(t, 1)
				old := day.AddDate(0, 0, -KeepDays-1)
				j := open(t, dir, old)
				deliver(t, j, m, old)
				j.Close()
				j = open(t, dir, day)
				if _, err := os.Stat(filepath.Join(dir, old.Format(time.DateOnly)+".jsonl")); !os.IsNotExist(err) {
					t.Errorf("the old day file is still there: %v", err)
				}
				deliver(t, j, m, day)
				if stored, _ := j.Stats(); stored != 1 {
					t.Errorf("stored %d, want 1: the forgotten id was taken for a duplicate", stored)
				}
			},
		},
		{
			name: "a journal running into a new day forgets the days that fall out of the window",
			run: func(t *testing.T, dir string) {
				m := newMessage(t, 1)
				j := open(t, dir, day)
				deliver(t, j, m, day)
				later := day.AddDate(0, 0, KeepDays+1)
				deliver(t, j, newMessage(t, 2), later)
				deliver(t, j, m, later)
				if stored, dups := j.Stats(); stored != 3 || dups != 0 {
					t.Errorf("stored %d and dropped %d, want 3 and 0", stored, dups)
				}
				if _, err := os.Stat(filepath.Join(dir, "2026-11-19.jsonl")); !os.IsNotExist(err) {
					t.Errorf("the day file that fell out of the window is still there: %v", err)
				}
			},
		},
		{
			name: "a message goes in the file of the UTC day the agent received it",
			run: func(t *testing.T, dir string) {
				j := open(t, dir, day)
				// 23:30 in New York on the 19th is 04:30 UTC on the 20th.
				ny := time.FixedZone("EST", -5*60*60)
				deliver(t, j, newMessage(t, 1), time.Date(2026, 11, 19, 23, 30, 0, 0, ny))
				if _, err := os.Stat(filepath.Join(dir, "2026-11-20.jsonl")); err != nil {
					t.Errorf("no file for the UTC day: %v", err)
				}
			},
		},
		{
			name: "the journal keeps no message body",
			run: func(t *testing.T, dir string) {
				j := open(t, dir, day)
				m := newMessage(t, 1)
				m.Body = json.RawMessage(`{"prompt":"a secret plan"}`)
				deliver(t, j, m, day)
				b, err := os.ReadFile(filepath.Join(dir, "2026-11-19.jsonl"))
				if err != nil {
					t.Fatal(err)
				}
				if strings.Contains(string(b), "secret") {
					t.Errorf("the journal has the body: %s", b)
				}
				var l journalLine
				if err := json.Unmarshal([]byte(strings.TrimSpace(string(b))), &l); err != nil {
					t.Fatal(err)
				}
				if l.ID != m.ID || l.Via != ViaSocket || l.PeerPID != 42 || l.Bytes != len(m.Body) {
					t.Errorf("the journal line is %+v", l)
				}
			},
		},
		{
			name: "the journal records how long the agent's own walk was and what the agent made of it",
			run: func(t *testing.T, dir string) {
				j := open(t, dir, day)
				m := newMessage(t, 1)
				walk := []platform.Proc{{PID: 42, Name: "pairtime"}, {PID: 41, Name: "claude"}}
				d := Delivery{Message: m, Via: ViaSocket, PeerPID: 42, Walk: walk, EventVia: event.ViaHook, Harness: event.HarnessClaudeCode, Received: clock.Stamp{Wall: day}}
				if err := j.Deliver(d); err != nil {
					t.Fatal(err)
				}
				b, err := os.ReadFile(filepath.Join(dir, "2026-11-19.jsonl"))
				if err != nil {
					t.Fatal(err)
				}
				var l journalLine
				if err := json.Unmarshal([]byte(strings.TrimSpace(string(b))), &l); err != nil {
					t.Fatal(err)
				}
				if l.Walk != 2 || l.EventVia != event.ViaHook || l.Harness != event.HarnessClaudeCode {
					t.Errorf("the journal line is %+v, want a walk of 2, via hook and claude_code", l)
				}
				if strings.Contains(string(b), "claude\"") {
					t.Errorf("the journal has the walk's process names: %s", b)
				}
			},
		},
		{
			name: "a message whose id the journal can't keep is rejected for good",
			run: func(t *testing.T, dir string) {
				j := open(t, dir, day)
				m := newMessage(t, 1)
				m.ID = "nope"
				if err := j.Deliver(Delivery{Message: m, Received: clock.Stamp{Wall: day}}); !errors.Is(err, ErrRejected) {
					t.Errorf("Deliver = %v, want ErrRejected", err)
				}
			},
		},
		{
			name: "Deliver after Close fails",
			run: func(t *testing.T, dir string) {
				j := open(t, dir, day)
				j.Close()
				if err := j.Deliver(Delivery{Message: newMessage(t, 1), Received: clock.Stamp{Wall: day}}); err == nil {
					t.Error("Deliver after Close returned nil")
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.run(t, filepath.Join(t.TempDir(), "received"))
		})
	}
}
