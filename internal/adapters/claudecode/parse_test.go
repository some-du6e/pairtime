// SPDX-License-Identifier: AGPL-3.0-or-later

package claudecode

import (
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

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

// TestQueuedMessage is E-cc-queued-turn's adapter rule: a prompt whose
// prompt_id an earlier call of the session carried is agent.queue in that
// turn, unless rule 1 or 3 marks its text as a machine's.
func TestQueuedMessage(t *testing.T) {
	const goal = "/goal Make the test pass."
	prompt := func(pid, text string) adapters.RawRecord {
		return hookRaw(EventUserPromptSubmit, map[string]any{"prompt_id": pid, "prompt": text}, nil)
	}
	stop := func(pid string, continued bool, crons ...string) adapters.RawRecord {
		list := []any{}
		for _, c := range crons {
			list = append(list, map[string]any{"id": "c1", "schedule": "*/5 * * * *", "recurring": true, "prompt": c})
		}
		return hookRaw(EventStop, map[string]any{"prompt_id": pid, "stop_hook_active": continued,
			"last_assistant_message": "", "session_crons": list}, nil)
	}
	tool := func(pid string) adapters.RawRecord {
		return hookRaw(EventPostToolUse, map[string]any{"prompt_id": pid, "tool_name": "Bash",
			"tool_input": map[string]any{"command": "go test ./..."}, "tool_response": map[string]any{"stdout": "ok\n", "stderr": ""},
			"tool_use_id": "toolu_q", "duration_ms": 1200}, nil)
	}
	cases := []struct {
		name  string
		calls []adapters.RawRecord
		// want are the kinds of the records the calls make, in order,
		// without the session.start the first call gets from its walk.
		want string
		// turn is the prompt_id the last call's record names.
		turn string
		// problem says the last call is a parse failure.
		problem bool
		// at are when the calls were made, in milliseconds after the
		// first; by default each comes a second after the one before.
		at []int64
	}{
		{name: "a second prompt with the prompt_id of a running turn is that turn's agent.queue, with its length and no text",
			calls: []adapters.RawRecord{prompt("p1", goal), prompt("p1", "Then say which test.")},
			want:  "prompt.submit,agent.queue", turn: "p1"},
		{name: "two messages queued in a row are two agent.queue records in the running turn",
			calls: []adapters.RawRecord{prompt("p1", goal), prompt("p1", "Then say which test."), prompt("p1", "And push it.")},
			want:  "prompt.submit,agent.queue,agent.queue", turn: "p1"},
		{name: "a prompt whose prompt_id only a PostToolUse carried is agent.queue in that turn",
			calls: []adapters.RawRecord{prompt("p1", goal), prompt("p1", "Then say which test."), stop("p1", false), tool("p2"),
				prompt("p2", "And push it.")},
			want: "prompt.submit,agent.queue,agent.stop,agent.tool,agent.queue", turn: "p2"},
		{name: "a prompt whose prompt_id only a Stop carried is agent.queue in that turn",
			calls: []adapters.RawRecord{prompt("p1", goal), stop("p1", false), stop("p2", true), prompt("p2", "And push it.")},
			want:  "prompt.submit,agent.stop,agent.stop,agent.queue", turn: "p2"},
		{name: "a prompt with a prompt_id no call carried is prompt.submit",
			calls: []adapters.RawRecord{prompt("p1", goal), stop("p1", false), prompt("p2", "Now add a test.")},
			want:  "prompt.submit,agent.stop,prompt.submit", turn: "p2"},
		{name: "a task notification with a running turn's prompt_id makes no record and is a parse failure",
			calls: []adapters.RawRecord{prompt("p1", goal), prompt("p1", "<task-notification>\n<task-id>a1</task-id>\n</task-notification>")},
			want:  "prompt.submit", problem: true},
		{name: "a scheduled prompt with a running turn's prompt_id makes no record and is a parse failure",
			calls: []adapters.RawRecord{prompt("p1", "/loop 5m Reply with tick."), stop("p1", false, "Reply with tick."),
				prompt("p1", "Reply with tick.")},
			want: "prompt.submit,agent.stop", problem: true},
		{name: "a message queued in a picked-up turn before any of that turn's calls is still prompt.submit, which WP-AD3 closes",
			calls: []adapters.RawRecord{prompt("p1", goal), prompt("p1", "Then say which test."), stop("p1", false),
				prompt("p2", "And push it.")},
			want: "prompt.submit,agent.queue,agent.stop,prompt.submit", turn: "p2"},
		{name: "a prompt that reaches the adapter after a later call of its turn, as through the spool, is still the turn's prompt.submit: earlier goes by the calls' own times",
			calls: []adapters.RawRecord{tool("p1"), prompt("p1", goal)}, at: []int64{2000, 1000},
			want: "agent.tool,prompt.submit", turn: "p1"},
		{name: "a message queued after such a prompt is still that turn's agent.queue",
			calls: []adapters.RawRecord{tool("p1"), prompt("p1", goal), prompt("p1", "Then say which test.")}, at: []int64{2000, 1000, 3000},
			want: "agent.tool,prompt.submit,agent.queue", turn: "p1"},
	}
	keys := adapters.FixtureKeys()
	const sid = "11111111-2222-4333-8444-555555555555" // hookRaw's
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := newTest()
			var got []string
			var last []adapters.Record
			var lastErr error
			for i, raw := range tc.calls {
				raw.Arrival.T = t0 + int64(i)*1000
				if tc.at != nil {
					raw.Arrival.T = t0 + tc.at[i]
				}
				recs, err := a.Parse(raw)
				if i < len(tc.calls)-1 && err != nil {
					t.Fatalf("call %d: %v", i+1, err)
				}
				for _, r := range recs {
					if r.Event.Kind() != event.KindSessionStart {
						got = append(got, string(r.Event.Kind()))
					}
				}
				last, lastErr = recs, err
			}
			if strings.Join(got, ",") != tc.want {
				t.Fatalf("records %v, want %s", got, tc.want)
			}
			var perr *adapters.ParseError
			switch {
			case tc.problem:
				if !errors.As(lastErr, &perr) || !slices.Contains(perr.Problems,
					"a scheduled prompt or a task notification came with a running turn's prompt_id, which Claude Code 2.1.327 doesn't send") {
					t.Errorf("err %v, want the parse failure", lastErr)
				}
				if len(last) != 0 {
					t.Errorf("the last call made %v", kinds(last))
				}
				return
			case lastErr != nil:
				t.Fatalf("the last call: %v", lastErr)
			}
			r := last[len(last)-1]
			text := map[string]any{}
			if err := json.Unmarshal(tc.calls[len(tc.calls)-1].Payload, &text); err != nil {
				t.Fatal(err)
			}
			body := text["prompt"].(string)
			switch d := r.Event.Data.(type) {
			case *event.AgentQueue:
				if d.TurnKey != keys.Turn(sid, tc.turn) || d.LenChars != utf8.RuneCountInString(body) || r.Text != nil {
					t.Errorf("queue %+v with text %q, want turn %s's key, %d chars and no text", *d, r.Text, tc.turn, utf8.RuneCountInString(body))
				}
			case *event.PromptSubmit:
				if d.TurnKey != keys.Turn(sid, tc.turn) || string(r.Text) != body {
					t.Errorf("prompt %+v, want turn %s's key and the prompt as its text", *d, tc.turn)
				}
			default:
				t.Errorf("the last record is %s", r.Event.Kind())
			}
		})
	}
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
		{name: "StopFailure's last_assistant_message is the API error's text, not Claude's reply, so it makes agent.stop alone and commits no text",
			event: EventStopFailure, fields: map[string]any{"prompt_id": "p1", "error": "rate_limit",
				"error_details": "429 Too Many Requests", "last_assistant_message": "API Error: Rate limit reached"},
			want: []string{"agent.stop"},
			check: func(t *testing.T, recs []adapters.Record) {
				if r := recs[0].Event.Data.(*event.AgentStop).StopReason; r != event.StopLimit {
					t.Errorf("stop_reason %s", r)
				}
				if recs[0].Text != nil {
					t.Errorf("the record commits %q", recs[0].Text)
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
	walked := func(e event.Entrypoint) *adapters.Arrival {
		return &adapters.Arrival{Via: event.ViaHook, T: t0, Mono: 1, BootID: "b", Harness: event.HarnessClaudeCode,
			Walk: &platform.Lineage{ParentClass: event.ParentTerminalApp, Entrypoint: e, HasTTY: true}}
	}
	cases := []struct {
		name string
		raw  adapters.RawRecord
		want event.SessionStart
		// problem says the call is a parse failure too.
		problem bool
	}{
		{name: "a walked SessionStart takes the walk's IDE entrypoint, parent class and terminal",
			raw:  hookRaw(EventSessionStart, map[string]any{"source": "resume", "permission_mode": "acceptEdits"}, ide),
			want: event.SessionStart{StartKind: event.StartResume, Entrypoint: event.EntrypointIDE, ParentClass: event.ParentIDE, PermissionMode: "acceptEdits"}},
		{name: "a plugin SessionStart takes nothing from a walk, even one it carries",
			raw:  hookRaw(EventSessionStart, map[string]any{"source": "startup"}, spool),
			want: event.SessionStart{StartKind: event.StartStartup, Entrypoint: event.EntrypointUnknown, ParentClass: event.ParentUnknown}},
		{name: "an unknown source is a startup, and a parse failure",
			raw:     hookRaw(EventSessionStart, map[string]any{"source": "teleport"}, ide),
			want:    event.SessionStart{StartKind: event.StartStartup, Entrypoint: event.EntrypointIDE, ParentClass: event.ParentIDE},
			problem: true},
		{name: "a walk whose lineage names sdk gives the hook's session.start entrypoint sdk",
			raw:  hookRaw(EventSessionStart, map[string]any{"source": "startup"}, walked(event.EntrypointSDK)),
			want: event.SessionStart{StartKind: event.StartStartup, Entrypoint: event.EntrypointSDK, ParentClass: event.ParentTerminalApp, HasTTY: true}},
		{name: "a walk whose lineage names exec gives the hook's session.start entrypoint exec",
			raw:  hookRaw(EventSessionStart, map[string]any{"source": "startup"}, walked(event.EntrypointExec)),
			want: event.SessionStart{StartKind: event.StartStartup, Entrypoint: event.EntrypointExec, ParentClass: event.ParentTerminalApp, HasTTY: true}},
		{name: "a walk whose lineage names cli gives entrypoint unknown: a walk gives only ide, desktop, sdk and exec",
			raw:  hookRaw(EventSessionStart, map[string]any{"source": "startup"}, walked(event.EntrypointCLI)),
			want: event.SessionStart{StartKind: event.StartStartup, Entrypoint: event.EntrypointUnknown, ParentClass: event.ParentTerminalApp, HasTTY: true}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			recs, err := newTest().Parse(tc.raw)
			var perr *adapters.ParseError
			if tc.problem != errors.As(err, &perr) || !tc.problem && err != nil {
				t.Errorf("err %v, want a parse failure: %v", err, tc.problem)
			}
			if len(recs) != 1 {
				t.Fatalf("records %v", kinds(recs))
			}
			if st := recs[0].Event.Data.(*event.SessionStart); !sameStart(st, &tc.want) {
				t.Errorf("session.start %+v, want %+v", *st, tc.want)
			}
		})
	}
}

// TestToolFailure is how the adapter reads PostToolUseFailure, which Claude
// Code runs for a tool call that started and failed, such as a command that
// exited non-zero (the hooks reference): agent.tool, as PostToolUse gives,
// from the text of the error, which is what Claude Code shows for it.
func TestToolFailure(t *testing.T) {
	opts := adapters.FixtureOptions()
	const goFail = "--- FAIL: TestSplit (0.00s)\n    split_test.go:9: got 2 words, want 3\nFAIL\nFAIL\texample.com/wc\t0.004s\nFAIL"
	const ccFail = "main.c:3:7: error: expected ';' before '}' token\nmake: *** [Makefile:2: main] Error 1"
	failure := func(tool string, input map[string]any, msg string, more map[string]any) adapters.RawRecord {
		p := map[string]any{"prompt_id": "p1", "cwd": "/home/capture/proj", "tool_name": tool, "tool_input": input,
			"tool_use_id": "toolu_f", "error": msg, "is_interrupt": false, "duration_ms": 4187}
		for k, v := range more {
			if v == nil {
				delete(p, k)
			} else {
				p[k] = v
			}
		}
		return hookRaw(EventPostToolUseFailure, p, nil)
	}
	command := func(cmd string) map[string]any { return map[string]any{"command": cmd, "description": "Run it"} }
	cases := []struct {
		name    string
		raw     adapters.RawRecord
		class   event.ToolClass
		exit    string // the exit code, "" for none
		output  string // what the call showed, for its visible lines and error signature
		visible int
		command string // the command line the record commits
		files   []string
		spanMS  int64
	}{
		{name: "a test run that exited non-zero is agent.tool with class test, its exit code, and the lines it printed with their error signature",
			raw:   failure("Bash", command("go test ./..."), "Exit code 1\n"+goFail, nil),
			class: event.ToolTest, exit: "1", output: goFail, visible: 5, command: "go test ./...", spanMS: 4187},
		{name: "a failed build is class build, with its exit code and its compiler's error",
			raw:   failure("Bash", command("make"), "Exit code 2\n"+ccFail, nil),
			class: event.ToolBuild, exit: "2", output: ccFail, visible: 2, command: "make", spanMS: 4187},
		{name: "a command whose shell didn't start has no exit code, and its message is what it shows",
			raw:   failure("Bash", command("ls"), "spawn /bin/bash ENOENT", nil),
			class: event.ToolBash, output: "spawn /bin/bash ENOENT", visible: 1, command: "ls", spanMS: 4187},
		{name: "an exit code past 32 bits, as Windows gives, is kept whole",
			raw:   failure("Bash", command("go test ./..."), "Exit code 3221225477\n", nil),
			class: event.ToolTest, exit: "3221225477", command: "go test ./...", spanMS: 4187},
		{name: "a subagent's failed run shows no output lines, as its PostToolUse wouldn't",
			raw:   failure("Bash", command("go test ./..."), "Exit code 1\n"+goFail, map[string]any{"agent_id": "a1", "agent_type": "general-purpose"}),
			class: event.ToolTest, exit: "1", output: goFail, command: "go test ./...", spanMS: 4187},
		{name: "a failed call without duration_ms starts and ends as its hook fires",
			raw:   failure("Bash", command("go test ./..."), "Exit code 1\n"+goFail, map[string]any{"duration_ms": nil}),
			class: event.ToolTest, exit: "1", output: goFail, visible: 5, command: "go test ./..."},
		{name: "a failed edit is agent.tool with no file and no lines, so DESIGN 5.7 can't take the person's next edits of the file for the agent's",
			raw: failure("Edit", map[string]any{"file_path": "/home/capture/proj/wc.py", "old_string": "a", "new_string": "b"},
				"EACCES: permission denied, open '/home/capture/proj/wc.py'", nil),
			class: event.ToolEdit, spanMS: 4187},
		{name: "a failed write names no file either",
			raw:   failure("Write", map[string]any{"file_path": "/home/capture/proj/notes.txt", "content": "x\n"}, "EACCES: permission denied", nil),
			class: event.ToolWrite, spanMS: 4187},
		{name: "an aborted command's text is Claude Code's, not the command's, so it shows no output and has no exit code or error signature",
			raw:   failure("Bash", command("go test ./..."), "AbortError: The operation was aborted", map[string]any{"is_interrupt": true}),
			class: event.ToolTest, command: "go test ./...", spanMS: 4187},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			recs, err := newTest().Parse(tc.raw)
			if err != nil {
				t.Fatal(err)
			}
			if len(recs) > 0 && recs[0].Event.Kind() == event.KindSessionStart {
				recs = recs[1:]
			}
			if len(recs) != 1 || recs[0].Event.Kind() != event.KindAgentTool {
				t.Fatalf("records %v", kinds(recs))
			}
			e := recs[0].Event
			got := e.Data.(*event.AgentTool)
			exit := ""
			if got.ExitCode != nil {
				exit = strconv.FormatInt(*got.ExitCode, 10)
			}
			var sig, cmd string
			if v, ok := opts.Features.ErrorSig(tc.output); ok && tc.output != "" {
				sig = v.String()
			}
			if v, ok := opts.Features.Command(tc.command); ok {
				cmd = v.String()
			}
			var files []string
			for _, f := range tc.files {
				v, err := opts.Features.Path(f)
				if err != nil {
					t.Fatal(err)
				}
				files = append(files, v.String())
			}
			if got.ToolClass != tc.class || exit != tc.exit || got.VisibleOutputLines != tc.visible || got.ErrorSig != sig ||
				got.CmdHash != cmd || !slices.Equal(got.Files, files) || got.LinesAdded != 0 || got.LinesRemoved != 0 ||
				got.TEnd != e.T || got.TEnd-got.TStart != tc.spanMS {
				t.Errorf("agent.tool %+v, exit code %q", *got, exit)
			}
			if recs[0].Command != tc.command || recs[0].Edits != nil || recs[0].Text != nil {
				t.Errorf("content: command %q, %d edits, text %q", recs[0].Command, len(recs[0].Edits), recs[0].Text)
			}
			e.EventID, e.Seq, e.DeviceID = event.NewEventID(time.UnixMilli(e.T)), 1, "d_test"
			if err := e.Validate(); err != nil {
				t.Error(err)
			}
		})
	}
	t.Run("a failed run's output has the error signature the same output gets from a PostToolUse", func(t *testing.T) {
		fail, err := newTest().Parse(failure("Bash", command("go test ./..."), "Exit code 1\n"+goFail, nil))
		if err != nil {
			t.Fatal(err)
		}
		ok, err := newTest().Parse(hookRaw(EventPostToolUse, map[string]any{"prompt_id": "p1", "cwd": "/home/capture/proj",
			"tool_name": "Bash", "tool_input": command("go test ./... | tail -5"), "tool_response": map[string]any{"stdout": goFail, "stderr": ""},
			"tool_use_id": "toolu_s", "duration_ms": 4187}, nil))
		if err != nil {
			t.Fatal(err)
		}
		f, s := fail[len(fail)-1].Event.Data.(*event.AgentTool), ok[len(ok)-1].Event.Data.(*event.AgentTool)
		if f.ErrorSig == "" || f.ErrorSig != s.ErrorSig || f.VisibleOutputLines != s.VisibleOutputLines {
			t.Errorf("PostToolUseFailure gives %+v, PostToolUse %+v", *f, *s)
		}
	})
}

// TestPendingCrons pins ledger-v1 section 3's cron key to what a Stop lists:
// prefix is the scheduled prompt without the marker of a cut and any U+FFFD
// that the cut left at its end, at most its first 1,000 code points. The
// vectors were computed apart from this code, with Python's hmac.
func TestPendingCrons(t *testing.T) {
	const sid = "617e16a3-ddb4-44ca-af68-72cf58ed1a34"
	cases := []struct{ name, listed, want string }{
		{"a prompt Stop didn't cut is keyed as listed", "Reply with the word tick and nothing else.", "c81715e31a20c0bb3f23196f4f3b0e3c5"},
		{"a cut prompt is keyed by what Stop kept, without the cut marker",
			strings.Repeat("x", 1000) + "\u2026 [+200 chars]", "cfa4b3ac233120822b7bb616717179fd8"},
		{"a cut that split a surrogate pair is keyed without the U+FFFD it left",
			strings.Repeat("x", 999) + "\uFFFD\u2026 [+201 chars]", "c054878035a20d2d6fc3c7a12b4847b7b"},
		{"a cut prompt of astral characters is keyed by the code points Stop kept",
			strings.Repeat("\U0001F600", 500) + "\u2026 [+200 chars]", "cc2e7043800329ceb5e9ea8e69f526d77"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			recs, err := newTest().Parse(hookRaw(EventStop, map[string]any{"session_id": sid, "prompt_id": "p1",
				"stop_hook_active": false, "last_assistant_message": "", "session_crons": []any{
					map[string]any{"id": "c1", "schedule": "*/5 * * * *", "recurring": true, "prompt": tc.listed}}}, nil))
			if err != nil {
				t.Fatal(err)
			}
			st := recs[len(recs)-1].Event.Data.(*event.AgentStop)
			if len(st.PendingCrons) != 1 || st.PendingCrons[0].PrefixHash != tc.want {
				t.Errorf("pending_crons %+v, want prefix_hash %s", st.PendingCrons, tc.want)
			}
		})
	}
}
