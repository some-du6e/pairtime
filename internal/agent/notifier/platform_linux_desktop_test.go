// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build linux && desktop

package notifier

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/platform/linux"
	"github.com/some-du6e/pairtime/internal/platform/linux/fakenotify"
)

// The whole path on Linux, in platform.yml's private session bus: Notify,
// the rules, and the real D-Bus call, answered by fakenotify (docs/PLAN.md,
// WP-OS8: the Linux job delivers to a fake notification service on a
// private bus).
func TestDesktopLinuxNoteReachesTheNotificationServer(t *testing.T) {
	const text = "52 min on remind-bot. You fixed the timezone bug yourself. See recap."
	tests := []struct {
		name        string
		server      bool
		backend     Backend // nil means Platform()
		want        Outcome
		wantProblem string // a substring; "" means a cleared problem
	}{
		{
			name:   "a session's note reaches the server as one Notify call with the specification's signature",
			server: true,
			want:   Shown,
		},
		{
			name: "a bus with no notification server drops the note and tells doctor",
			// NoAutoStart, so a server the runner image happens to have
			// installed isn't started instead.
			backend:     linuxBackend{n: linux.Notifications{NoAutoStart: true}},
			want:        Unavailable,
			wantProblem: "no notification server",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var fake *fakenotify.Server
			if tt.server {
				var err error
				if fake, err = fakenotify.Start("body"); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { fake.Close() })
			}
			var mu sync.Mutex
			var problems []string
			done := make(chan Outcome, 1)
			n := New(Options{
				Backend: tt.backend,
				Problem: func(msg string) {
					mu.Lock()
					defer mu.Unlock()
					problems = append(problems, msg)
				},
				Result: func(_ Note, o Outcome) { done <- o },
			})
			defer n.Close()

			if got := n.Notify(Note{Session: "desktop-test", Ended: time.Now(), Text: text}); got != Queued {
				t.Fatalf("Notify() = %v, want %v", got, Queued)
			}
			select {
			case o := <-done:
				if o != tt.want {
					t.Fatalf("outcome = %v, want %v", o, tt.want)
				}
			case <-time.After(20 * time.Second):
				t.Fatal("no outcome after 20 s")
			}

			mu.Lock()
			defer mu.Unlock()
			if len(problems) != 1 || (tt.wantProblem == "") != (problems[0] == "") || !strings.Contains(problems[0], tt.wantProblem) {
				t.Errorf("problems = %q, want one like %q", problems, tt.wantProblem)
			}
			if fake == nil {
				return
			}
			got := fake.Notifications()
			if len(got) != 1 {
				t.Fatalf("the server got %d Notify calls, want 1", len(got))
			}
			if got[0].Signature != fakenotify.NotifySignature {
				t.Errorf("Notify's signature = %q, want %q", got[0].Signature, fakenotify.NotifySignature)
			}
			if got[0].AppName != "pairtime" || got[0].Summary != Title || got[0].Body != text {
				t.Errorf("app_name, summary, body = %q, %q, %q, want %q, %q, %q", got[0].AppName, got[0].Summary, got[0].Body, "pairtime", Title, text)
			}
		})
	}
}
