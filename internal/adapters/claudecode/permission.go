// SPDX-License-Identifier: AGPL-3.0-or-later

package claudecode

import (
	"github.com/some-du6e/pairtime/internal/adapters"
	"github.com/some-du6e/pairtime/pkg/event"
)

// request is a PermissionRequest that no report has answered yet. Its
// envelope is the hook's: t is requested_at, and source and via are what
// the agent.permission record keeps (E-6.1-permission).
type request struct {
	env    event.Envelope
	tool   string
	agent  string
	prompt string
}

// post is a PostToolUse call, whose arrival may date the answer to the
// request before it: an accepted Edit or Write applies as soon as it is
// accepted (E-6.1-permission).
type post struct {
	t         int64
	toolUseID string
	tool      string
	agent     string
}

// permissionRequest holds the request for Decide. A hook alone can't say
// who answered: a PermissionRequest hook or a config rule can answer for
// the person, so it writes no record (SM 1.2; E-6.1-permission).
func (c *call) permissionRequest() {
	tool, _ := c.f.str("tool_name")
	agent, _ := c.f.str("agent_id")
	pid, _ := c.f.str("prompt_id")
	c.s.requests = append(c.s.requests, &request{env: c.env, tool: tool, agent: agent, prompt: pid})
	if len(c.s.requests) > maxHeld {
		c.s.requests = c.s.requests[1:]
	}
}

func (s *session) post(p post) {
	s.posts = append(s.posts, p)
	if len(s.posts) > maxHeld {
		s.posts = s.posts[1:]
	}
}

// ToolDecision is Claude Code's own report of who decided a tool call, its
// OTel claude_code.tool_decision event, as the OTLP receiver reads it
// (WP-AD4; HS 3.3). It names the call by session.id, agent_id, tool_name
// and tool_use_id, which its PostToolUse shares.
type ToolDecision struct {
	SessionID string
	AgentID   string
	ToolName  string
	ToolUseID string
	// Decision is the event's decision: accept or reject.
	Decision string
	// Source is the event's source: user_temporary, user_permanent,
	// user_reject or user_abort when the person answered, hook or config
	// when a hook or a rule did.
	Source string
	// Timestamp is the event's event.timestamp, and Arrival when the
	// receiver got the export, both in Unix milliseconds.
	Timestamp, Arrival int64
	// PeerChecked says the export came over a connection that the
	// peer-process check traced to the harness process whose hooks reported
	// the request (DESIGN 5.2, 5.7). An export that didn't is any process's
	// word, and decides nothing.
	PeerChecked bool
}

// verdict maps a tool_decision's source and decision to agent.permission's
// decision and decided_by (SM 1.2; E-6.1-permission).
func verdict(source, decision string) (event.Decision, event.DecidedBy, bool) {
	switch source {
	case "user_temporary", "user_permanent":
		return event.DecisionAccept, event.DecidedByUser, true
	case "user_reject":
		return event.DecisionReject, event.DecidedByUser, true
	case "user_abort":
		return event.DecisionCancel, event.DecidedByUser, true
	case "hook", "config":
		by := event.DecidedByHook
		if source == "config" {
			by = event.DecidedByConfig
		}
		switch decision {
		case "accept":
			return event.DecisionAccept, by, true
		case "reject":
			return event.DecisionReject, by, true
		}
	}
	return "", "", false
}

// Decide pairs Claude Code's own report of a tool call's decision with the
// PermissionRequest the adapter holds for it, and returns the
// agent.permission record E-6.1-permission defines (SM 1.2):
//
//   - Only a report over a peer-checked connection counts, and only its
//     source says who answered: user_temporary and user_permanent are
//     accept by user, user_reject reject by user, user_abort cancel by
//     user, and hook and config are decided_by hook and config, with the
//     report's own decision. Any other source writes no record.
//   - The request is the most recent one held of the same session,
//     subagent and tool that came before the report's event.timestamp, and
//     a report that matches none writes no record: a call Claude Code
//     allowed on its own asked nobody.
//   - requested_at is the request hook's arrival. answered_at is the
//     earliest of the report's event.timestamp, its arrival and the
//     arrival of the call's PostToolUse, by tool_use_id, and never before
//     requested_at.
//   - The record has t = answered_at, and the request hook's source and
//     via, so a spooled request takes a side door's level (D-spool-via).
//
// A request Decide pairs is no longer held. A request that no report
// answers is never recorded; the adapter forgets it once it holds
// maxHeld newer ones of its session.
func (a *Adapter) Decide(d ToolDecision) (adapters.Record, bool) {
	if !d.PeerChecked || d.SessionID == "" {
		return adapters.Record{}, false
	}
	decision, by, ok := verdict(d.Source, d.Decision)
	if !ok {
		return adapters.Record{}, false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	s := a.sessions[d.SessionID]
	if s == nil {
		return adapters.Record{}, false
	}
	at := -1
	for i := len(s.requests) - 1; i >= 0; i-- {
		r := s.requests[i]
		// A decision Claude Code made before a request came can't answer
		// it: OTLP exports come in batches, so a config rule's decision on
		// an earlier call can arrive while a later request waits (WP-AD1's
		// step 10, whose subagent asked 3 s after one).
		if r.tool == d.ToolName && r.agent == d.AgentID && (d.Timestamp == 0 || r.env.T <= d.Timestamp) {
			at = i
			break
		}
	}
	if at < 0 {
		return adapters.Record{}, false
	}
	r := s.requests[at]
	s.requests = append(s.requests[:at], s.requests[at+1:]...)

	answered := int64(0)
	consider := func(t int64) {
		if t > 0 && (answered == 0 || t < answered) {
			answered = t
		}
	}
	consider(d.Timestamp)
	consider(d.Arrival)
	if d.ToolUseID != "" {
		for _, p := range s.posts {
			if p.toolUseID == d.ToolUseID {
				consider(p.t)
			}
		}
	}
	requested := r.env.T
	if answered < requested {
		answered = requested
	}
	env := r.env
	if env.Mono > 0 {
		env.Mono += answered - requested
	}
	env.T = answered
	return adapters.Record{Event: event.Event{Envelope: env, Data: &event.AgentPermission{
		RequestedAt: requested, AnsweredAt: answered, Decision: decision, DecidedBy: by,
	}}}, true
}
