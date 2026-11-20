// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build linux && desktop

package linux

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/platform/linux/fakenotify"
)

// These run in platform.yml's Linux job, inside the private session bus
// that tools/ci/desktop_session.sh starts, against fakenotify: Notify's real
// D-Bus calls, answered by a server that shows nothing. The oracle is the
// Desktop Notifications Specification's Notify signature, susssasa{sv}i,
// read from the call's own message header.

func TestDesktopNotifyAgainstAFakeServer(t *testing.T) {
	tests := []struct {
		name     string
		caps     []string
		n        Notifications
		summary  string
		body     string
		wantApp  string
		wantBody string
	}{
		{
			name:     "the call has the specification's signature and pairtime's arguments",
			caps:     []string{"body"},
			summary:  "Your session recap is ready",
			body:     "52 min on remind-bot. See recap.",
			wantApp:  "pairtime",
			wantBody: "52 min on remind-bot. See recap.",
		},
		{
			name:     "a server that reads body markup gets the markup characters escaped",
			caps:     []string{"body", "body-markup"},
			summary:  "Your session recap is ready",
			body:     "<b>fix</b> & ship",
			wantApp:  "pairtime",
			wantBody: "&lt;b&gt;fix&lt;/b&gt; &amp; ship",
		},
		{
			name:     "a server without body markup gets the body as written",
			caps:     []string{"body"},
			summary:  "Your session recap is ready",
			body:     "<b>fix</b> & ship",
			wantApp:  "pairtime",
			wantBody: "<b>fix</b> & ship",
		},
		{
			name:     "AppName replaces the app name",
			caps:     []string{"body"},
			n:        Notifications{AppName: "pairtime-test"},
			summary:  "s",
			body:     "b",
			wantApp:  "pairtime-test",
			wantBody: "b",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake, err := fakenotify.Start(tt.caps...)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { fake.Close() })

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			id, err := tt.n.Notify(ctx, tt.summary, tt.body)
			if err != nil {
				t.Fatalf("Notify() error = %v", err)
			}
			got := fake.Notifications()
			if len(got) != 1 {
				t.Fatalf("the server got %d Notify calls, want 1", len(got))
			}
			n := got[0]
			if n.Signature != fakenotify.NotifySignature {
				t.Errorf("Notify's signature = %q, want the specification's %q", n.Signature, fakenotify.NotifySignature)
			}
			if id != n.ID {
				t.Errorf("Notify() = id %d, want the server's %d", id, n.ID)
			}
			if n.AppName != tt.wantApp || n.Summary != tt.summary || n.Body != tt.wantBody {
				t.Errorf("app_name, summary, body = %q, %q, %q, want %q, %q, %q", n.AppName, n.Summary, n.Body, tt.wantApp, tt.summary, tt.wantBody)
			}
			if n.ReplacesID != 0 || n.AppIcon != "" || len(n.Actions) != 0 || n.ExpireTimeout != -1 {
				t.Errorf("replaces_id %d, app_icon %q, actions %q, expire_timeout %d, want a new notification with no icon, no actions and the server's expiry",
					n.ReplacesID, n.AppIcon, n.Actions, n.ExpireTimeout)
			}
			if v, ok := n.Hints["urgency"]; !ok || v.Value() != byte(1) {
				t.Errorf("hint urgency = %v, want byte 1 (normal)", v)
			}
			if v, ok := n.Hints["suppress-sound"]; !ok || v.Value() != true {
				t.Errorf("hint suppress-sound = %v, want true", v)
			}
			if len(n.Hints) != 2 {
				t.Errorf("hints = %v, want urgency and suppress-sound only", n.Hints)
			}
		})
	}
}

func TestDesktopNotifyWithNoServerOnTheBus(t *testing.T) {
	tests := []struct {
		name string
		n    Notifications
	}{
		// NoAutoStart, so a server the runner image happens to have
		// installed isn't started instead.
		{name: "a bus where nobody owns the name means no notification server", n: Notifications{NoAutoStart: true}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_, err := tt.n.Notify(ctx, "summary", "body")
			if !errors.Is(err, ErrNotifyNoServer) {
				t.Fatalf("Notify() error = %v, want ErrNotifyNoServer", err)
			}
		})
	}
}
