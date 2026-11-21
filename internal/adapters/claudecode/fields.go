// SPDX-License-Identifier: AGPL-3.0-or-later

package claudecode

import (
	"bytes"
	"encoding/json"
	"slices"
	"strings"

	"github.com/some-du6e/pairtime/internal/adapters"
)

// common are the fields Claude Code 2.1.327 may put in any hook's payload
// (HS 3.1, "Common input fields"; WP-AD1's capture): scratchpad_dir only
// from 2.1.257, effort only on events with a tool-use context, agent_id and
// agent_type inside subagents and --agent sessions.
var common = []string{
	"session_id", "transcript_path", "cwd", "hook_event_name", "prompt_id",
	"permission_mode", "effort", "agent_id", "agent_type", "scratchpad_dir",
}

// own are the fields each event adds to common, from HS 3.1's table and
// the capture. StopFailure's come from the hooks reference alone, since the
// capture skipped step 15, which needs the network off, and so do
// PostToolUseFailure's, since every tool call the capture made succeeded.
var own = map[string][]string{
	EventSessionStart:       {"source", "model", "session_title", "seconds_since_last_response", "context_tokens"},
	EventUserPromptSubmit:   {"prompt", "session_title"},
	EventStop:               {"stop_hook_active", "last_assistant_message", "background_tasks", "session_crons"},
	EventStopFailure:        {"error", "error_details", "last_assistant_message"},
	EventNotification:       {"message", "title", "notification_type"},
	EventPermissionRequest:  {"tool_name", "tool_input", "permission_suggestions"},
	EventPostToolUse:        {"tool_name", "tool_input", "tool_response", "tool_use_id", "duration_ms"},
	EventPostToolUseFailure: {"tool_name", "tool_input", "tool_use_id", "error", "is_interrupt", "duration_ms"},
	EventSubagentStart:      nil,
	EventSubagentStop:       {"stop_hook_active", "last_assistant_message", "background_tasks", "session_crons", "agent_transcript_path"},
	EventSessionEnd:         {"reason"},
}

// fields is a hook payload, field by field, so that a field of an
// unexpected type costs that field alone and is counted (DESIGN 5.3).
type fields struct {
	m    map[string]json.RawMessage
	perr *adapters.ParseError
}

// decode reads payload as one JSON object.
func decode(payload []byte, perr *adapters.ParseError) (fields, bool) {
	f := fields{perr: perr}
	p := bytes.TrimSpace(payload)
	if len(p) == 0 || p[0] != '{' {
		perr.Problem("the payload isn't a JSON object")
		return f, false
	}
	if err := json.Unmarshal(p, &f.m); err != nil {
		perr.Problem("the payload isn't a JSON object")
		return f, false
	}
	return f, true
}

// unknown returns the payload's fields that event doesn't have in Claude
// Code 2.1.327, sorted. Their names are Claude Code's keys, which say
// nothing about the session.
func (f fields) unknown(event string) []string {
	var out []string
	for k := range f.m {
		if !slices.Contains(common, k) && !slices.Contains(own[event], k) {
			out = append(out, safeName(k))
		}
	}
	slices.Sort(out)
	return out
}

// has reports whether the payload has field name, null or not.
func (f fields) has(name string) bool {
	_, ok := f.m[name]
	return ok
}

// present reports whether field name is there and not null.
func (f fields) present(name string) bool {
	v, ok := f.m[name]
	return ok && !bytes.Equal(bytes.TrimSpace(v), []byte("null"))
}

// str returns string field name. A missing or null field is ("", false)
// with no problem; one of another type is a problem.
func (f fields) str(name string) (string, bool) {
	if !f.present(name) {
		return "", false
	}
	var s string
	if err := json.Unmarshal(f.m[name], &s); err != nil {
		f.perr.Problem("%s isn't a string", name)
		return "", false
	}
	return s, true
}

// boolean returns bool field name, false when it is missing.
func (f fields) boolean(name string) bool {
	if !f.present(name) {
		return false
	}
	var b bool
	if err := json.Unmarshal(f.m[name], &b); err != nil {
		f.perr.Problem("%s isn't a boolean", name)
		return false
	}
	return b
}

// integer returns integer field name.
func (f fields) integer(name string) (int64, bool) {
	if !f.present(name) {
		return 0, false
	}
	var n int64
	if err := json.Unmarshal(f.m[name], &n); err != nil {
		f.perr.Problem("%s isn't an integer", name)
		return 0, false
	}
	return n, true
}

// object decodes object field name into v.
func (f fields) object(name string, v any) bool {
	if !f.present(name) {
		return false
	}
	raw := bytes.TrimSpace(f.m[name])
	if len(raw) == 0 || raw[0] != '{' {
		f.perr.Problem("%s isn't an object", name)
		return false
	}
	if err := json.Unmarshal(raw, v); err != nil {
		f.perr.Problem("%s doesn't have the fields Claude Code 2.1.327 gives it", name)
		return false
	}
	return true
}

// list decodes array field name into v.
func (f fields) list(name string, v any) bool {
	if !f.present(name) {
		return false
	}
	raw := bytes.TrimSpace(f.m[name])
	if len(raw) == 0 || raw[0] != '[' {
		f.perr.Problem("%s isn't a list", name)
		return false
	}
	if err := json.Unmarshal(raw, v); err != nil {
		f.perr.Problem("%s doesn't hold what Claude Code 2.1.327 puts in it", name)
		return false
	}
	return true
}

// safeName returns s for a message when it looks like a field or event
// name, and a stand-in otherwise, so that no message carries content.
func safeName(s string) string {
	if s == "" || len(s) > 64 {
		return "(a name that isn't one)"
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !('a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9' || c == '_' || c == '-' || c == '.') {
			return "(a name that isn't one)"
		}
	}
	return s
}

// quoteNames joins names for a message.
func quoteNames(names []string) string { return strings.Join(names, ", ") }
