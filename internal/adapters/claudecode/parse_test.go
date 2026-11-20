// SPDX-License-Identifier: AGPL-3.0-or-later

package claudecode

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/some-du6e/pairtime/internal/adapters"
	"github.com/some-du6e/pairtime/internal/platform"
	"github.com/some-du6e/pairtime/pkg/event"
)

// t0 is a time in the capture's day, in Unix milliseconds.
const t0 = 1795193284378

// hookRaw is a hook call over the socket from under a claude in a terminal,
// unless arr says otherwise.
func hookRaw(name string, payload map[string]any, arr *adapters.Arrival) adapters.RawRecord {
	if _, ok := payload["session_id"]; !ok {
		payload["session_id"] = "11111111-2222-4333-8444-555555555555"
	}
	payload["hook_event_name"] = name
	b, _ := json.Marshal(payload)
	a := adapters.Arrival{Via: event.ViaHook, T: t0, Mono: 5000, BootID: "boot",
		Walk:    &platform.Lineage{ParentClass: event.ParentTerminalApp, Entrypoint: event.EntrypointUnknown, HasTTY: true},
		Harness: event.HarnessClaudeCode}
	if arr != nil {
		a = *arr
	}
	return adapters.RawRecord{Source: adapters.SourceHook, Event: name, Payload: b, Arrival: a}
}

func kinds(recs []adapters.Record) []string {
	var out []string
	for _, r := range recs {
		out = append(out, string(r.Event.Kind()))
	}
	return out
}

func TestPrompts(t *testing.T) {
	stop := func(crons ...map[string]any) adapters.RawRecord {
		list := make([]any, len(crons))
		for i, c := range crons {
			list[i] = c
		}
		return hookRaw(EventStop, map[string]any{"prompt_id": "p-loop", "stop_hook_active": false,
			"last_assistant_message": "", "session_crons": list}, nil)
	}
	cron := func(prompt string) map[string]any {
		return map[string]any{"id": "c1", "schedule": "*/5 * * * *", "recurring": true, "prompt": prompt}
	}
	long := strings.Repeat("x", 1200)
	cases := []struct {
		name    string
		before  []adapters.RawRecord
		prompt  string
		wantRec string
		check   func(t *testing.T, p *event.PromptSubmit)
	}{
		{name: "rule 1: a prompt equal to a scheduled prompt the last Stop listed matches it",
			before: []adapters.RawRecord{stop(cron("Reply with tick."))}, prompt: "Reply with tick.", wantRec: "prompt.submit",
			check: func(t *testing.T, p *event.PromptSubmit) {
				if !p.MatchesPendingCron {
					t.Error("no match")
				}
			}},
		{name: "rule 1: a prompt that only starts like a scheduled prompt Stop didn't cut doesn't match it",
			before: []adapters.RawRecord{stop(cron("Reply with tick."))}, prompt: "Reply with tick. And more.", wantRec: "prompt.submit",
			check: func(t *testing.T, p *event.PromptSubmit) {
				if p.MatchesPendingCron {
					t.Error("matched")
				}
			}},
		{name: "rule 1: a long prompt matches the scheduled prompt Stop cut at 1,000 characters with its marker",
			before: []adapters.RawRecord{stop(cron(long[:1000] + "\u2026 [+200 chars]"))}, prompt: long, wantRec: "prompt.submit",
			check: func(t *testing.T, p *event.PromptSubmit) {
				if !p.MatchesPendingCron {
					t.Error("no match")
				}
			}},
		{name: "rule 1: a later Stop with no scheduled prompt clears the ones before it",
			before: []adapters.RawRecord{stop(cron("Reply with tick.")), stop()}, prompt: "Reply with tick.", wantRec: "prompt.submit",
			check: func(t *testing.T, p *event.PromptSubmit) {
				if p.MatchesPendingCron {
					t.Error("matched a cron the last Stop didn't list")
				}
			}},
		{name: "a slash command is named without its arguments",
			prompt: "/loop 1m Reply with tick.", wantRec: "prompt.submit",
			check: func(t *testing.T, p *event.PromptSubmit) {
				if p.SlashCommand != "/loop" {
					t.Errorf("slash_command %q", p.SlashCommand)
				}
			}},
		{name: "a path isn't a slash command",
			prompt: "/home/capture/notes.txt is empty", wantRec: "prompt.submit",
			check: func(t *testing.T, p *event.PromptSubmit) {
				if p.SlashCommand != "" {
					t.Errorf("slash_command %q", p.SlashCommand)
				}
			}},
		{name: "rule 3: a background subagent's task notification is declared subagent",
			prompt: "<task-notification>\n<task-id>a1</task-id>\n</task-notification>", wantRec: "prompt.submit",
			check: func(t *testing.T, p *event.PromptSubmit) {
				if p.OriginDeclared != event.DeclaredSubagent {
					t.Errorf("origin_declared %q", p.OriginDeclared)
				}
			}},
		{name: "a go-ahead is low effort by SM 2.2's text test",
			prompt: "go ahead", wantRec: "prompt.submit",
			check: func(t *testing.T, p *event.PromptSubmit) {
				if !p.LowEffort {
					t.Error("not low effort")
				}
			}},
		{name: "len_chars counts code points, and nothing is counted as pasted from a hook",
			prompt: "h\u00e9llo \U0001F600", wantRec: "prompt.submit",
			check: func(t *testing.T, p *event.PromptSubmit) {
				if p.LenChars != 7 || p.PastedChars != 0 || p.TypedLenEst != 7 {
					t.Errorf("len_chars %d pasted %d typed %d", p.LenChars, p.PastedChars, p.TypedLenEst)
				}
			}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := newTest()
			for _, r := range tc.before {
				if _, err := a.Parse(r); err != nil {
					t.Fatal(err)
				}
			}
			recs, err := a.Parse(hookRaw(EventUserPromptSubmit, map[string]any{"prompt_id": "p-new", "prompt": tc.prompt,
				"permission_mode": "default"}, nil))
			if err != nil {
				t.Fatal(err)
			}
			last := recs[len(recs)-1]
			if string(last.Event.Kind()) != tc.wantRec {
				t.Fatalf("records %v", kinds(recs))
			}
			if string(last.Text) != tc.prompt {
				t.Error("the record's text isn't the prompt")
			}
			tc.check(t, last.Event.Data.(*event.PromptSubmit))
		})
	}
}

func TestQueuedMessage(t *testing.T) {
	a := newTest()
	first := hookRaw(EventUserPromptSubmit, map[string]any{"prompt_id": "p-goal", "prompt": "/goal Make the test pass."}, nil)
	queued := hookRaw(EventUserPromptSubmit, map[string]any{"prompt_id": "p-goal", "prompt": "Then say which test."}, nil)
	recs1, err := a.Parse(first)
	if err != nil {
		t.Fatal(err)
	}
	recs2, err := a.Parse(queued)
	if err != nil {
		t.Fatal(err)
	}
	t.Run("a second prompt with the prompt_id of a running turn is that turn's agent.queue, with its length and no text", func(t *testing.T) {
		if len(recs2) != 1 || recs2[0].Event.Kind() != event.KindAgentQueue {
			t.Fatalf("records %v", kinds(recs2))
		}
		q := recs2[0].Event.Data.(*event.AgentQueue)
		p := recs1[len(recs1)-1].Event.Data.(*event.PromptSubmit)
		if q.TurnKey != p.TurnKey || q.LenChars != len("Then say which test.") || recs2[0].Text != nil {
			t.Errorf("queue %+v, prompt turn %s", *q, p.TurnKey)
		}
	})
}

func TestStops(t *testing.T) {
	cases := []struct {
		name   string
		event  string
		fields map[string]any
		want   []string
		check  func(t *testing.T, recs []adapters.Record)
	}{
		{name: "Stop with a final message is agent.output then agent.stop, the message as content",
			event: EventStop, fields: map[string]any{"prompt_id": "p1", "stop_hook_active": true,
				"last_assistant_message": "Done. Want me to add tests?\n\n1. Yes\n2. No"},
			want: []string{"agent.output", "agent.stop"},
			check: func(t *testing.T, recs []adapters.Record) {
				o := recs[0].Event.Data.(*event.AgentOutput)
				if !o.IsFinal || o.Words != 10 || !o.AsksQuestion || !o.OffersOptions {
					t.Errorf("output %+v", *o)
				}
				if string(recs[0].Text) != "Done. Want me to add tests?\n\n1. Yes\n2. No" {
					t.Error("the record's text isn't the final message")
				}
				s := recs[1].Event.Data.(*event.AgentStop)
				if !s.StopHookActive || s.TurnKey != o.TurnKey || s.StopReason != event.StopQuestion {
					t.Errorf("stop %+v", *s)
				}
			}},
		{name: "Stop with no final message is agent.stop alone",
			event: EventStop, fields: map[string]any{"prompt_id": "p1", "stop_hook_active": false, "last_assistant_message": ""},
			want: []string{"agent.stop"}},
		{name: "StopFailure on a rate limit stops the turn with limit",
			event: EventStopFailure, fields: map[string]any{"prompt_id": "p1", "error": "rate_limit"},
			want: []string{"agent.stop"},
			check: func(t *testing.T, recs []adapters.Record) {
				if r := recs[0].Event.Data.(*event.AgentStop).StopReason; r != event.StopLimit {
					t.Errorf("stop_reason %s", r)
				}
			}},
		{name: "StopFailure on any other API error stops the turn with error",
			event: EventStopFailure, fields: map[string]any{"prompt_id": "p1", "error": "server_error"},
			want: []string{"agent.stop"},
			check: func(t *testing.T, recs []adapters.Record) {
				if r := recs[0].Event.Data.(*event.AgentStop).StopReason; r != event.StopError {
					t.Errorf("stop_reason %s", r)
				}
			}},
		{name: "a final message that ends on a question stops the turn on a question",
			event: EventStop, fields: map[string]any{"prompt_id": "p1", "last_assistant_message": "Which one?"},
			want: []string{"agent.output", "agent.stop"},
			check: func(t *testing.T, recs []adapters.Record) {
				if r := recs[1].Event.Data.(*event.AgentStop).StopReason; r != event.StopQuestion {
					t.Errorf("stop_reason %s", r)
				}
			}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			recs, err := newTest().Parse(hookRaw(tc.event, tc.fields, nil))
			if err != nil {
				t.Fatal(err)
			}
			got := kinds(recs)
			// The first call of a hooked session with no session.start gets
			// one from the walk first.
			if len(got) > 0 && got[0] == "session.start" {
				got = got[1:]
				recs = recs[1:]
			}
			if strings.Join(got, ",") != strings.Join(tc.want, ",") {
				t.Fatalf("records %v, want %v", got, tc.want)
			}
			if tc.check != nil {
				tc.check(t, recs)
			}
		})
	}
}

func TestOutputOf(t *testing.T) {
	cases := []struct {
		name string
		msg  string
		want output
	}{
		{name: "words are the prose's, by whitespace", msg: "It printed `nested`.", want: output{words: 3}},
		{name: "fenced code counts in code_lines, not words", msg: "Run this:\n\n```sh\ngo test ./...\ngo vet ./...\n```\nThen push.",
			want: output{words: 4, codeLines: 2}},
		{name: "a last line ending with a question mark asks", msg: "I fixed it.\nShall I commit?", want: output{words: 6, asks: true}},
		{name: "a question set off by emphasis and a closing quote still asks", msg: "Shall I commit **now?**\"", want: output{words: 4, asks: true}},
		{name: "a question before more prose doesn't ask", msg: "Why? Because the split was wrong.", want: output{words: 6}},
		{name: "a numbered list with 1 and 2 under a question offers options",
			msg:  "1. Keep it\n2. Drop it\n\nWhich do you want?",
			want: output{words: 10, asks: true, offers: true}},
		{name: "a numbered summary that asks nothing offers nothing", msg: "1. Fixed the split\n2. Added a test", want: output{words: 8}},
		{name: "a question followed by the choices it offers asks and offers them", msg: "Want tests?\n\n- maybe\n1. Yes\n2. No",
			want: output{words: 8, asks: true, offers: true}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := outputOf(tc.msg); got != tc.want {
				t.Errorf("outputOf = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestCommandClass(t *testing.T) {
	cases := []struct {
		name string
		cmd  string
		want event.ToolClass
	}{
		{"python -m unittest runs tests, flags before -m and a pipe after it included", "python3 -I -m unittest -v 2>&1 | tail -30", event.ToolTest},
		{"python -c runs a script, not tests", `python3 -c "import unittest"`, event.ToolBash},
		{"a test runner after cd and && runs tests", "cd app && pytest -q", event.ToolTest},
		{"go test runs tests", "go test ./...", event.ToolTest},
		{"npm run test:unit runs tests", "npm run test:unit", event.ToolTest},
		{"npm run build builds", "npm run build", event.ToolBuild},
		{"a bare make builds", "make -j4", event.ToolBuild},
		{"make test runs tests", "make test", event.ToolTest},
		{"variables and wrappers before the program are skipped", "CI=1 timeout 60 sudo -E go build ./cmd/...", event.ToolBuild},
		{"tests win over a build in the same command", "go build ./... && go test ./...", event.ToolTest},
		{"git and grep are plain commands", "git status --short; grep -rn x .", event.ToolBash},
		{"a Windows executable is named without .exe", `C:\Go\bin\go.exe test ./...`, event.ToolTest},
		{"an empty command is a plain one", "", event.ToolBash},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := commandClass(tc.cmd); got != tc.want {
				t.Errorf("commandClass(%q) = %s, want %s", tc.cmd, got, tc.want)
			}
		})
	}
}

func TestLedgerPath(t *testing.T) {
	cases := []struct {
		name, root, file, want string
		ok                     bool
	}{
		{"a file in the project is keyed relative to its root", "/home/capture/proj", "/home/capture/proj/src/a.py", "src/a.py", true},
		{"a file outside the project keeps its absolute path", "/home/capture/proj", "/etc/hosts", "/etc/hosts", true},
		{"a folder that only starts like the root is outside it", "/home/capture/proj", "/home/capture/project2/a.py", "/home/capture/project2/a.py", true},
		{"dot components are resolved first", "/home/capture/proj", "/home/capture/proj/./src/../a.py", "a.py", true},
		{"a Windows path gets slashes and an upper-case drive, and compares without case", `c:\Users\capture\Proj`, `C:\users\capture\proj\a.go`, "a.go", true},
		{"a Windows file outside the project keeps its drive", `C:\Users\capture\proj`, `d:\tmp\x.txt`, "D:/tmp/x.txt", true},
		{"with no project the path stays absolute", "", "/home/capture/a.py", "/home/capture/a.py", true},
		{"a relative path can't be keyed", "/home/capture/proj", "a.py", "", false},
		{"a POSIX path with a backslash can't be keyed", "", `/home/capture/a\b.py`, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := &call{proj: adapters.Project{Key: "p_x", Root: tc.root}}
			got, ok := c.ledgerPath(tc.file)
			if got != tc.want || ok != tc.ok {
				t.Errorf("ledgerPath(%q) = %q, %v, want %q, %v", tc.file, got, ok, tc.want, tc.ok)
			}
		})
	}
}

func TestParseFailures(t *testing.T) {
	cases := []struct {
		name    string
		raw     adapters.RawRecord
		records int
		other   bool
	}{
		{name: "a field Claude Code 2.1.327 doesn't send is a parse failure, and the event is still made",
			raw: hookRaw(EventSessionEnd, map[string]any{"reason": "logout", "brand_new": 1}, nil), records: 2},
		{name: "an event pairtime doesn't install is a parse failure with no records",
			raw: hookRaw("PreToolUse", map[string]any{"tool_name": "Bash"}, nil)},
		{name: "a payload that isn't a JSON object is a parse failure",
			raw: adapters.RawRecord{Source: adapters.SourceHook, Event: EventStop, Payload: []byte("[1,2]")}},
		{name: "a payload with no session_id is a parse failure",
			raw: hookRaw(EventStop, map[string]any{"session_id": ""}, nil)},
		{name: "a payload the hook cut is a parse failure",
			raw: func() adapters.RawRecord {
				r := hookRaw(EventStop, map[string]any{}, nil)
				r.Incomplete = true
				return r
			}()},
		{name: "a field of the wrong type is a parse failure that costs that field alone",
			raw: hookRaw(EventSessionEnd, map[string]any{"reason": 7}, nil), records: 2},
		{name: "a call that Cursor's process ran isn't a failure, and makes nothing",
			raw:   hookRaw(EventStop, map[string]any{}, &adapters.Arrival{Via: event.ViaPlugin, T: t0, BootID: "b", Harness: event.HarnessCursor}),
			other: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			recs, err := newTest().Parse(tc.raw)
			if tc.other {
				if !errors.Is(err, adapters.ErrOtherHarness) {
					t.Fatalf("err %v", err)
				}
			} else {
				var perr *adapters.ParseError
				if !errors.As(err, &perr) {
					t.Fatalf("err %v, want a ParseError", err)
				}
			}
			if len(recs) != tc.records {
				t.Errorf("records %v, want %d", kinds(recs), tc.records)
			}
		})
	}
}

func TestSessionFields(t *testing.T) {
	ide := &adapters.Arrival{Via: event.ViaHook, T: t0, Mono: 1, BootID: "b", Harness: event.HarnessClaudeCode,
		Walk: &platform.Lineage{ParentClass: event.ParentIDE, Entrypoint: event.EntrypointIDE, HasTTY: false}}
	spool := &adapters.Arrival{Via: event.ViaPlugin, T: t0, Mono: 1, BootID: "b", Harness: event.HarnessClaudeCode,
		Walk: &platform.Lineage{ParentClass: event.ParentIDE, Entrypoint: event.EntrypointIDE, HasTTY: true}}
	cases := []struct {
		name string
		raw  adapters.RawRecord
		want event.SessionStart
	}{
		{name: "a walked SessionStart takes the walk's IDE entrypoint, parent class and terminal",
			raw:  hookRaw(EventSessionStart, map[string]any{"source": "resume", "permission_mode": "acceptEdits"}, ide),
			want: event.SessionStart{StartKind: event.StartResume, Entrypoint: event.EntrypointIDE, ParentClass: event.ParentIDE, PermissionMode: "acceptEdits"}},
		{name: "a plugin SessionStart takes nothing from a walk, even one it carries",
			raw:  hookRaw(EventSessionStart, map[string]any{"source": "startup"}, spool),
			want: event.SessionStart{StartKind: event.StartStartup, Entrypoint: event.EntrypointUnknown, ParentClass: event.ParentUnknown}},
		{name: "an unknown source is a startup, and a parse failure",
			raw:  hookRaw(EventSessionStart, map[string]any{"source": "teleport"}, ide),
			want: event.SessionStart{StartKind: event.StartStartup, Entrypoint: event.EntrypointIDE, ParentClass: event.ParentIDE}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			recs, _ := newTest().Parse(tc.raw)
			if len(recs) != 1 {
				t.Fatalf("records %v", kinds(recs))
			}
			if st := recs[0].Event.Data.(*event.SessionStart); !sameStart(st, &tc.want) {
				t.Errorf("session.start %+v, want %+v", *st, tc.want)
			}
		})
	}
}
