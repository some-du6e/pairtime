// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/agent/inbox"
)

// TestInboxAgeCap checks inbox/'s bound at the agent's start
// (D-inbox-age-cap): a spool whose first call was sent inbox.SpoolKeep
// ago is deleted unread before the first drain, with the call spooled
// after it, so neither reaches the journal, and a younger one is drained.
func TestInboxAgeCap(t *testing.T) {
	cases := []struct {
		name    string
		age     time.Duration // how long ago the first call was sent
		drained bool
	}{
		{"a spool whose first call was sent 7 days ago is deleted unread at the start, before the first drain, with the call spooled after it yesterday", inbox.SpoolKeep, false},
		{"one whose first call was sent 6 days 23 hours ago is drained, with the call spooled after it", inbox.SpoolKeep - time.Hour, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a, dirs := modesAgent(t, "online")
			a.hooked = newHookTies()
			a.drained = make(chan struct{})
			now := time.Now()
			yesterday := now.Add(-24 * time.Hour)
			for _, at := range []time.Time{now.Add(-c.age), yesterday} {
				m := hookMessage(t, "UserPromptSubmit", `{"session_id":"s","hook_event_name":"UserPromptSubmit"}`, inbox.Proc{PID: 4242, Name: "claude"}, at)
				if err := inbox.Append(dirs.Inbox(), m); err != nil {
					t.Fatal(err)
				}
			}
			// The spool was last written as the second call came.
			spool := filepath.Join(dirs.Inbox(), inbox.SpoolName)
			if err := os.Chtimes(spool, yesterday, yesterday); err != nil {
				t.Fatal(err)
			}
			stop := a.startIPC(Options{DrainEvery: time.Hour})
			t.Cleanup(stop)
			select {
			case <-a.drained:
			case <-time.After(30 * time.Second):
				t.Fatal("the first drain never ran")
			}
			var want uint64
			if c.drained {
				want = 2
			}
			if stored, _ := a.journal.Stats(); stored != want {
				t.Errorf("the journal stored %d messages, want %d", stored, want)
			}
			// Drained or deleted, the calls no longer wait in the inbox.
			if _, err := os.Lstat(spool); !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("the spool is still there: %v", err)
			}
		})
	}
}
