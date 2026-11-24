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
// (D-inbox-age-cap): a spool file nothing has written for inbox.SpoolKeep
// is deleted unread before the first drain, so none of its calls reaches
// the journal, and a younger one is drained.
func TestInboxAgeCap(t *testing.T) {
	cases := []struct {
		name    string
		age     time.Duration
		drained bool
	}{
		{"a spooled call 7 days old is deleted unread at the start, before the first drain", inbox.SpoolKeep, false},
		{"one 6 days 23 hours old is drained", inbox.SpoolKeep - time.Hour, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a, dirs := modesAgent(t, "online")
			a.hooked = newHookTies()
			a.drained = make(chan struct{})
			at := time.Now().Add(-c.age)
			m := hookMessage(t, "UserPromptSubmit", `{"session_id":"s","hook_event_name":"UserPromptSubmit"}`, inbox.Proc{PID: 4242, Name: "claude"}, at)
			if err := inbox.Append(dirs.Inbox(), m); err != nil {
				t.Fatal(err)
			}
			spool := filepath.Join(dirs.Inbox(), inbox.SpoolName)
			if err := os.Chtimes(spool, at, at); err != nil {
				t.Fatal(err)
			}
			stop := a.startIPC(Options{DrainEvery: time.Hour})
			t.Cleanup(stop)
			select {
			case <-a.drained:
			case <-time.After(30 * time.Second):
				t.Fatal("the first drain never ran")
			}
			stored, _ := a.journal.Stats()
			if got := stored > 0; got != c.drained {
				t.Errorf("the journal stored %d messages; want the call drained %v", stored, c.drained)
			}
			// Drained or deleted, the call no longer waits in the inbox.
			if _, err := os.Lstat(spool); !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("the spool is still there: %v", err)
			}
		})
	}
}
