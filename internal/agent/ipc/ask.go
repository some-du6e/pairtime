// SPDX-License-Identifier: AGPL-3.0-or-later

package ipc

// Questions: the CLI asking the agent for something over the socket or
// pipe that hooks hand their messages to, through the same checks of who
// runs each end (doc.go). `pairtime recap` asks for the last work session
// this way rather than over the local HTTP API, which needs the bearer
// token and can't tell who listens on its port (CP 6.3; #120).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/some-du6e/pairtime/internal/agent/inbox"
)

// AskLastSession asks for the last work session, or the one still going
// (SM 5.3). The answer is the JSON GET /v1/sessions/last gives (WP-AG4),
// so one function can serve both.
const AskLastSession = "sessions/last"

// AskDashboardLink asks for a one-time URL that signs a browser in to the
// dashboard (DESIGN 3.9), which `pairtime dashboard` opens. The answer is
// a DashboardLink. Asked here, the link needs no bearer token, so the CLI
// sends the token nowhere (D-dashboard-link).
const AskDashboardLink = "dashboard/link"

// DashboardLink is the agent's answer to AskDashboardLink.
type DashboardLink struct {
	// URL is http://127.0.0.1:<port>/signin?code=<code>, and the code works
	// once, for ExpiresInS seconds. URL is "" while the agent's HTTP server
	// doesn't listen yet, just after the agent started: ask again then.
	URL        string `json:"url"`
	ExpiresInS int    `json:"expires_in_s"`
}

// ErrUnknownQuestion means the agent doesn't answer the question: it has
// no answer for it, or it predates questions and refused the frame.
var ErrUnknownQuestion = errors.New("ipc: the agent doesn't answer that question")

const (
	// maxQuestion bounds a question frame. A longer frame is never taken
	// for a question, so the agent doesn't parse a hook's message twice.
	maxQuestion = 4 << 10
	// maxAnswer bounds an answer frame, and answerRoom is what the reply
	// around an answer takes at most.
	maxAnswer  = 1 << 20
	answerRoom = 512
)

// question is the frame a question goes in: a new id, which the reply
// repeats, and the question's name. An agent that predates questions
// decodes it as a message without a kind, refuses it with a reply that has
// no id, stores nothing and hangs up.
type question struct {
	ID  string `json:"id"`
	Ask string `json:"ask"`
}

// Ask asks the agent at ep question q, such as AskLastSession, and returns
// its answer, one JSON value. Like Send, it checks that the agent runs as
// this user before it writes anything (Dial), and fails with ErrNotAgent
// when it doesn't. It fails with ErrUnknownQuestion when the agent doesn't
// answer q. ctx bounds all of it. Unlike Send, it spools nothing.
func Ask(ctx context.Context, ep Endpoint, q string) (json.RawMessage, error) {
	if !validQuestion(q) {
		return nil, fmt.Errorf("ipc: %q isn't a question's name", q)
	}
	c, err := Dial(ctx, ep)
	if err != nil {
		return nil, err
	}
	defer c.Close()
	return c.Ask(ctx, q)
}

// Ask asks the agent question q and waits for its answer until ctx is
// done. After an error the connection may be out of step, so the caller
// closes it.
func (c *Conn) Ask(ctx context.Context, q string) (json.RawMessage, error) {
	if !validQuestion(q) {
		return nil, fmt.Errorf("ipc: %q isn't a question's name", q)
	}
	ask := question{ID: inbox.NewID(), Ask: q}
	body, err := json.Marshal(ask)
	if err != nil {
		return nil, err
	}
	rb, err := c.roundTrip(ctx, body, maxQuestion, maxAnswer)
	if err != nil {
		return nil, err
	}
	var rep reply
	if err := json.Unmarshal(rb, &rep); err != nil {
		return nil, fmt.Errorf("ipc: the agent's reply isn't JSON: %w", err)
	}
	switch {
	case rep.ID == "" && !rep.OK:
		// An agent from before questions took the frame for a message
		// without a kind.
		return nil, fmt.Errorf("%w: %s", ErrUnknownQuestion, rep.Error)
	case rep.ID != ask.ID:
		return nil, errors.New("ipc: the agent answered another question")
	case rep.Unknown:
		return nil, fmt.Errorf("%w: %s", ErrUnknownQuestion, rep.Error)
	case !rep.OK:
		return nil, fmt.Errorf("ipc: the agent couldn't answer %s: %s", q, rep.Error)
	case len(rep.Answer) == 0:
		return nil, errors.New("ipc: the agent's answer is empty")
	}
	return rep.Answer, nil
}

// asQuestion reports whether body, a frame, is a question: short enough
// to be one, and with an "ask".
func asQuestion(body []byte) (question, bool) {
	if len(body) > maxQuestion {
		return question{}, false
	}
	var q question
	if json.Unmarshal(body, &q) != nil || q.Ask == "" {
		return question{}, false
	}
	return q, true
}

// answer returns the reply to q, and whether the connection may carry on:
// it may unless q is malformed.
func (s *Server) answer(q question) (reply, bool) {
	if !validHexID(q.ID) {
		return reply{Error: "a question needs an id of 32 lowercase hex digits"}, false
	}
	if !validQuestion(q.Ask) {
		return reply{ID: q.ID, Unknown: true, Error: "that isn't the name of a question"}, true
	}
	unknown := reply{ID: q.ID, Unknown: true, Error: fmt.Sprintf("this agent doesn't answer %s", q.Ask)}
	if s.answerer == nil {
		return unknown, true
	}
	ans, err := s.answerer(q.Ask)
	switch {
	case errors.Is(err, ErrUnknownQuestion):
		return unknown, true
	case err != nil:
		s.log.Warn("ipc: couldn't answer a question", "question", q.Ask, "err", err)
		return reply{ID: q.ID, Error: "the agent's log says why"}, true
	case !json.Valid(ans):
		s.log.Warn("ipc: an answer isn't JSON", "question", q.Ask)
		return reply{ID: q.ID, Error: "the agent's answer to " + q.Ask + " isn't JSON"}, true
	case len(ans) > maxAnswer-answerRoom:
		s.log.Warn("ipc: an answer is too long for a frame", "question", q.Ask, "bytes", len(ans))
		return reply{ID: q.ID, Error: "the agent's answer to " + q.Ask + " is too long"}, true
	}
	return reply{ID: q.ID, OK: true, Answer: ans}, true
}

// validQuestion reports whether q can name a question: 1 to 64 lowercase
// letters, digits and the characters / . _ -
func validQuestion(q string) bool {
	if len(q) == 0 || len(q) > 64 {
		return false
	}
	for i := range len(q) {
		c := q[i]
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9':
		case c == '/', c == '.', c == '_', c == '-':
		default:
			return false
		}
	}
	return true
}

// validHexID reports whether id has the shape of inbox.NewID's ids.
func validHexID(id string) bool {
	if len(id) != 32 {
		return false
	}
	for i := range len(id) {
		c := id[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
