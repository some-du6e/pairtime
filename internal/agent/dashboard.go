// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

// The questions the CLI asks the agent over its socket or pipe (ipc.Ask),
// which go through the same checks of who runs each end as a hook's
// message, so they need no bearer token and their answers reach no other
// user. The agent answers ipc.AskDashboardLink here,
// ipc.AskLastSession with what GET /v1/sessions/last gives (scorer.go's
// answerQuestion, WP-AG4), and ipc.AskStatus with what GET /v1/status
// gives (http.go's status), for `pairtime doctor` (WP-AG6).

import (
	"encoding/json"
	"net"
	"net/url"
	"strconv"
	"time"

	"github.com/some-du6e/pairtime/internal/agent/ipc"
	"github.com/some-du6e/pairtime/internal/agent/localhttp"
)

// answer answers question q from the CLI (ipc.Options.Answer). It returns
// ipc.ErrUnknownQuestion for a question it has no answer for.
func (a *agent) answer(q string) (json.RawMessage, error) {
	switch q {
	case ipc.AskDashboardLink:
		return json.Marshal(a.dashboardLink())
	case ipc.AskLastSession:
		return a.answerQuestion(q)
	case ipc.AskStatus:
		return json.Marshal(a.status())
	}
	return nil, ipc.ErrUnknownQuestion
}

// dashboardLink is a one-time URL that signs a browser in to the dashboard
// (DESIGN 3.9), for `pairtime dashboard` to open (D-dashboard-link). Its
// code works once, for localhttp.CodeTTL. The socket listens before the
// HTTP server does, so a question can come in before there is a port to
// link to; the URL is "" then, and the CLI asks again.
func (a *agent) dashboardLink() ipc.DashboardLink {
	port := int(a.linkPort.Load())
	if port == 0 {
		return ipc.DashboardLink{}
	}
	u := url.URL{
		Scheme:   "http",
		Host:     net.JoinHostPort("127.0.0.1", strconv.Itoa(port)),
		Path:     "/signin",
		RawQuery: url.Values{"code": {a.sessions.NewCode()}}.Encode(),
	}
	return ipc.DashboardLink{URL: u.String(), ExpiresInS: int(localhttp.CodeTTL / time.Second)}
}
