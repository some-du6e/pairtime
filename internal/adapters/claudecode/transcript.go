// SPDX-License-Identifier: AGPL-3.0-or-later

package claudecode

import (
	"bytes"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"time"

	"github.com/some-du6e/pairtime/internal/adapters"
	"github.com/some-du6e/pairtime/internal/install"
)

// A Claude Code transcript is ~/.claude/projects/<project>/<session>.jsonl,
// one JSON entry per line, and a subagent's is <session>/subagents/
// agent-<id>.jsonl beside it, whose entries carry the session's id and
// isSidechain (HS 3.2; WP-AD1's capture). The format is Claude Code's own
// and changes between versions, so the reader reads only the fields below
// and leaves every other alone, and it reads Claude Code's labels in every
// version whose entries carry them: entrypoint, promptSource and
// turnOrigin, with the values a capture has shown (E-cc-transcript-labels).

// entry is one transcript line as the reader decodes it: what the entry
// types it makes records of hold that it reads, and nothing of the rest.
// Content stays in memory: prompt text, assistant text and the commands and
// paths of tool calls.
type entry struct {
	Type      string `json:"type"`
	Subtype   string `json:"subtype"`
	SessionID string `json:"sessionId"`
	PromptID  string `json:"promptId"`
	Timestamp string `json:"timestamp"`
	Version   string `json:"version"`
	CWD       string `json:"cwd"`
	// Entrypoint, PromptSource and TurnOrigin are nil when the entry
	// doesn't have them.
	Entrypoint     *string  `json:"entrypoint"`
	PromptSource   *string  `json:"promptSource"`
	TurnOrigin     *string  `json:"turnOrigin"`
	PermissionMode string   `json:"permissionMode"`
	IsSidechain    bool     `json:"isSidechain"`
	Message        *message `json:"message"`
	// IsMeta marks a user entry Claude Code wrote for the model, such as a
	// slash command's expanded instructions, which no person sent, and
	// IsCompactSummary the summary a compaction puts in place of the
	// conversation, which IsVisibleInTranscriptOnly marks too.
	IsMeta                    bool `json:"isMeta"`
	IsCompactSummary          bool `json:"isCompactSummary"`
	IsVisibleInTranscriptOnly bool `json:"isVisibleInTranscriptOnly"`
	// ToolUseResult is a tool result entry's structured result, or the
	// text Claude Code put in its place, such as "User rejected tool use".
	ToolUseResult  toolResult `json:"toolUseResult"`
	ToolDenialKind string     `json:"toolDenialKind"`
	// PermissionDecision says who let a tool call run: config, a hook, or
	// the person (user_temporary, user_permanent).
	PermissionDecision permissionDecision `json:"permissionDecision"`
	// Operation and Content are a queue-operation's: enqueue, dequeue,
	// remove or popAll, and the message it queues.
	Operation  string      `json:"operation"`
	Content    stringValue `json:"content"`
	Attachment *attachment `json:"attachment"`

	// t is Timestamp in Unix milliseconds, 0 when it has none or it
	// doesn't parse; decodeEntry fills it.
	t int64
}

// message is an entry's message: the user's or the model's.
type message struct {
	ID         string     `json:"id"`
	StopReason string     `json:"stop_reason"`
	Content    msgContent `json:"content"`
}

// msgContent is a message's content: a string, or a list of parts.
type msgContent struct {
	Text  string
	Parts []msgPart
	// IsText says the content was a string.
	IsText bool
}

func (c *msgContent) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	switch {
	case len(b) == 0 || bytes.Equal(b, []byte("null")):
		return nil
	case b[0] == '"':
		c.IsText = true
		return json.Unmarshal(b, &c.Text)
	case b[0] == '[':
		return json.Unmarshal(b, &c.Parts)
	}
	return errors.New("a message's content is neither a string nor a list")
}

// text returns the content's text: the string, or its text parts one
// after another. A message with no text, such as one holding only tool
// results, gives "".
func (c msgContent) text() string {
	if c.IsText {
		return c.Text
	}
	var b strings.Builder
	for _, p := range c.Parts {
		if p.Type == "text" {
			b.WriteString(p.Text)
		}
	}
	return b.String()
}

// msgPart is one part of a message's content: text, a tool use, a tool
// result and others the reader leaves alone, such as thinking.
type msgPart struct {
	Type string `json:"type"`
	Text string `json:"text"`
	// ID, Name and Input are a tool use's.
	ID    string   `json:"id"`
	Name  string   `json:"name"`
	Input useInput `json:"input"`
	// ToolUseID, IsError and Content are a tool result's: the text Claude
	// Code gave the model, which for a failed command starts "Exit code N".
	ToolUseID string      `json:"tool_use_id"`
	IsError   bool        `json:"is_error"`
	Content   resultValue `json:"content"`
}

// Each tool gives its input and its result fields of its own, under
// names other tools use with other types: a synchronous Agent call's
// result holds a list as its content, where a Write's holds the file's
// text, and an MCP tool's input can hold anything as its command. So the
// reader decodes those objects field by field (objectFields), and a field
// of another type costs it that field, never the entry.

// objectFields returns the fields of b, a JSON object, and false for
// anything else.
func objectFields(b []byte) (map[string]json.RawMessage, bool) {
	var m map[string]json.RawMessage
	if json.Unmarshal(b, &m) != nil {
		return nil, false
	}
	return m, true
}

// fieldString returns field name of m when it is a string, and "" for
// anything else.
func fieldString(m map[string]json.RawMessage, name string) string {
	var s string
	if raw, ok := m[name]; !ok || json.Unmarshal(raw, &s) != nil {
		return ""
	}
	return s
}

// fieldBool returns field name of m when it is a boolean, and false and
// not ok for anything else.
func fieldBool(m map[string]json.RawMessage, name string) (v, ok bool) {
	raw, found := m[name]
	if !found || json.Unmarshal(raw, &v) != nil || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return false, false
	}
	return v, true
}

// useInput holds the tool_use input fields the reader reads: a Bash
// call's command and an edit's file. An edit's own text it leaves alone,
// since its result has the hunks.
type useInput struct {
	FilePath string `json:"file_path"`
	Command  string `json:"command"`
}

func (in *useInput) UnmarshalJSON(b []byte) error {
	m, _ := objectFields(b)
	in.FilePath, in.Command = fieldString(m, "file_path"), fieldString(m, "command")
	return nil
}

// toolResult is an entry's toolUseResult: the structured result, as the
// PostToolUse hook's tool_response holds it, or a string.
type toolResult struct {
	Resp toolResponse
	Text string
	// Set says the entry has one, and BadPatch that its structuredPatch
	// isn't a list of hunks.
	Set, BadPatch bool
}

func (r *toolResult) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	switch {
	case len(b) == 0 || bytes.Equal(b, []byte("null")):
		return nil
	case b[0] == '"':
		r.Set = true
		return json.Unmarshal(b, &r.Text)
	case b[0] == '{':
		r.Set = true
		m, _ := objectFields(b)
		r.Resp = toolResponse{Type: fieldString(m, "type"), FilePath: fieldString(m, "filePath"),
			Content: fieldString(m, "content"), Stdout: fieldString(m, "stdout"), Stderr: fieldString(m, "stderr")}
		if raw, ok := m["structuredPatch"]; ok && !bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			if json.Unmarshal(raw, &r.Resp.StructuredPatch) != nil {
				r.Resp.StructuredPatch, r.BadPatch = nil, true
			}
		}
		return nil
	}
	// A list or a number: no tool's result the reader reads.
	r.Set = true
	return nil
}

// permissionDecision is a tool result entry's permissionDecision, of which
// the reader reads who decided.
type permissionDecision struct {
	Source string
}

func (p *permissionDecision) UnmarshalJSON(b []byte) error {
	m, _ := objectFields(b)
	p.Source = fieldString(m, "source")
	return nil
}

// resultValue is a tool result's content: a string, or a list of text
// parts, whose text it joins.
type resultValue string

func (v *resultValue) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	switch {
	case len(b) == 0 || bytes.Equal(b, []byte("null")):
		return nil
	case b[0] == '"':
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		*v = resultValue(s)
	case b[0] == '[':
		var parts []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if err := json.Unmarshal(b, &parts); err != nil {
			return err
		}
		var sb strings.Builder
		for _, p := range parts {
			if p.Type == "text" {
				sb.WriteString(p.Text)
			}
		}
		*v = resultValue(sb.String())
	}
	return nil
}

// stringValue is a field the reader reads only when it is a string: a
// queue-operation's content.
type stringValue string

func (v *stringValue) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if len(b) == 0 || b[0] != '"' {
		return nil
	}
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	*v = stringValue(s)
	return nil
}

// attachment holds the attachment fields the reader reads: a goal_status
// verdict's (HS 3.5). Every kind of attachment has fields of its own, so
// it is decoded field by field too.
type attachment struct {
	Type     string `json:"type"`
	Met      *bool  `json:"met"`
	Sentinel bool   `json:"sentinel"`
}

func (a *attachment) UnmarshalJSON(b []byte) error {
	m, _ := objectFields(b)
	a.Type = fieldString(m, "type")
	if v, ok := fieldBool(m, "met"); ok {
		a.Met = &v
	}
	a.Sentinel, _ = fieldBool(m, "sentinel")
	return nil
}

// readTypes are the entry types the reader makes records of, which
// decodeEntry decodes whole. Every other line costs it one pass for its
// type.
var readTypes = []string{"user", "assistant", "attachment", "queue-operation", "system"}

// otherTypes are the entry types Claude Code 2.1.327 writes that hold
// nothing the reader reads (WP-AD1's capture), and the two HS 3.2 lists
// beside them. Any other type is a parse failure: a version that writes one
// may have moved something the reader needs.
var otherTypes = []string{
	"summary", "progress", "file-history-snapshot", "file-history-delta", "last-prompt", "ai-title", "mode",
	"permission-mode", "atis-latch", "tools-used", "cost-state", "api-request", "api-request-blob",
	"api-request-shape",
}

// decodeEntry decodes one transcript line. It reads the line's type first,
// and the rest only for the types the reader makes records of.
func decodeEntry(line []byte) (*entry, error) {
	var head struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(line, &head); err != nil {
		return nil, errors.New("a line isn't a JSON object")
	}
	if !contains(readTypes, head.Type) {
		return &entry{Type: head.Type}, nil
	}
	var e entry
	if err := json.Unmarshal(line, &e); err != nil {
		return nil, errors.New("a " + safeName(head.Type) + " entry doesn't have the fields Claude Code 2.1.327 gives it")
	}
	if e.Timestamp != "" {
		if ts, err := time.Parse(time.RFC3339Nano, e.Timestamp); err == nil && ts.UnixMilli() >= minUnixMS {
			e.t = ts.UnixMilli()
		}
	}
	return &e, nil
}

// DecodeLog reads one line of a transcript (adapters.LogReader). It keeps
// nothing, so the tailers may run it on any goroutine.
func (*Adapter) DecodeLog(line []byte) (any, error) { return decodeEntry(line) }

// LogRoots returns Claude Code's projects folder, which holds its
// transcripts: in $CLAUDE_CONFIG_DIR when that is an absolute path, else in
// ~/.claude, the folder Detect looks at (HS 3.2).
func (*Adapter) LogRoots(env adapters.Env) []adapters.LogRoot {
	return []adapters.LogRoot{projectsRoot(filepath.Join(filepath.Dir(install.ClaudeSettings(env)), "projects"))}
}

// projectsRoot is the LogRoot of a Claude Code projects folder.
func projectsRoot(dir string) adapters.LogRoot {
	return adapters.LogRoot{Dir: dir, Depth: 4, Match: transcriptFile, Hot: transcriptFolder}
}

// transcriptFile reports whether rel, under a projects folder, is a
// transcript: <project>/<session>.jsonl, or a subagent's
// <project>/<session>/subagents/<agent>.jsonl.
func transcriptFile(rel string) bool {
	parts := strings.Split(rel, "/")
	if !strings.HasSuffix(rel, ".jsonl") || len(parts[len(parts)-1]) <= len(".jsonl") {
		return false
	}
	switch len(parts) {
	case 2:
		return true
	case 4:
		return parts[2] == "subagents"
	}
	return false
}

// transcriptFolder reports whether rel, under a projects folder, is a
// folder transcripts are written in: a project's, or a session's subagents
// folder.
func transcriptFolder(rel string) bool {
	parts := strings.Split(rel, "/")
	switch len(parts) {
	case 1:
		return rel != ""
	case 3:
		return parts[2] == "subagents"
	}
	return false
}

// LogHint returns the transcript a hook call names in transcript_path, with
// the projects folder that holds it (adapters.LogReader), when it is a
// transcript's path: <projects>/<project>/<session>.jsonl, absolute.
func (a *Adapter) LogHint(raw adapters.RawRecord) (string, adapters.LogRoot, bool) {
	if raw.Source != adapters.SourceHook || raw.Incomplete {
		return "", adapters.LogRoot{}, false
	}
	f, ok := decode(raw.Payload, &adapters.ParseError{})
	if !ok {
		return "", adapters.LogRoot{}, false
	}
	p, _ := f.str("transcript_path")
	if p == "" || !filepath.IsAbs(p) || filepath.Ext(p) != ".jsonl" {
		return "", adapters.LogRoot{}, false
	}
	p = filepath.Clean(p)
	root := filepath.Dir(filepath.Dir(p))
	if filepath.Base(root) != "projects" {
		return "", adapters.LogRoot{}, false
	}
	return p, projectsRoot(root), true
}
