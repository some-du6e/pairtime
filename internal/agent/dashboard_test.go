// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"encoding/json"
	"errors"
	"net/url"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/agent/ipc"
	"github.com/some-du6e/pairtime/internal/agent/localhttp"
)

func TestAnswer(t *testing.T) {
	link := func(t *testing.T, a *agent) ipc.DashboardLink {
		t.Helper()
		ans, err := a.answer(ipc.AskDashboardLink)
		if err != nil {
			t.Fatal(err)
		}
		var l ipc.DashboardLink
		if err := json.Unmarshal(ans, &l); err != nil {
			t.Fatalf("the answer %s isn't a link: %v", ans, err)
		}
		return l
	}
	tests := []struct {
		name  string
		port  int32
		check func(t *testing.T, a *agent)
	}{
		{
			name: "before the HTTP server listens the link is empty, so the CLI asks again",
			check: func(t *testing.T, a *agent) {
				if l := link(t, a); l != (ipc.DashboardLink{}) {
					t.Errorf("link = %+v, want none yet", l)
				}
			},
		},
		{
			name: "the link is the agent's own sign-in page on 127.0.0.1, for CodeTTL",
			port: 7247,
			check: func(t *testing.T, a *agent) {
				l := link(t, a)
				u, err := url.Parse(l.URL)
				if err != nil || u.Scheme != "http" || u.Host != "127.0.0.1:7247" || u.Path != "/signin" || l.ExpiresInS != int(localhttp.CodeTTL/time.Second) {
					t.Errorf("link = %+v (%v)", l, err)
				}
			},
		},
		{
			name: "the link's code signs a browser in once",
			port: 7247,
			check: func(t *testing.T, a *agent) {
				u, err := url.Parse(link(t, a).URL)
				if err != nil {
					t.Fatal(err)
				}
				code := u.Query().Get("code")
				if _, ok := a.sessions.Redeem(code); !ok {
					t.Fatal("the code didn't sign in")
				}
				if _, ok := a.sessions.Redeem(code); ok {
					t.Error("the code signed in twice")
				}
			},
		},
		{
			name: "each question gets a code of its own",
			port: 7247,
			check: func(t *testing.T, a *agent) {
				if first, second := link(t, a), link(t, a); first.URL == second.URL {
					t.Errorf("two links are the same: %s", first.URL)
				}
			},
		},
		{
			name: "a question the agent has no answer for is unknown, as ipc says",
			check: func(t *testing.T, a *agent) {
				for _, q := range []string{"sessions/next", "dashboard"} {
					if _, err := a.answer(q); !errors.Is(err, ipc.ErrUnknownQuestion) {
						t.Errorf("answer(%q) = %v, want ipc.ErrUnknownQuestion", q, err)
					}
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := &agent{sessions: localhttp.NewSessions(nil)}
			a.linkPort.Store(tt.port)
			tt.check(t, a)
		})
	}
}
