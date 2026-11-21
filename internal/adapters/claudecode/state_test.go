// SPDX-License-Identifier: AGPL-3.0-or-later

package claudecode

import (
	"bytes"
	"encoding/json"
	"slices"
	"testing"

	"github.com/some-du6e/pairtime/internal/adapters"
	"github.com/some-du6e/pairtime/pkg/event"
)

// promptsIn returns the texts a call's payload carries that only content
// can: its prompt, and the prompts its stop scheduled.
func promptsIn(t *testing.T, c adapters.Call) []string {
	t.Helper()
	var p struct {
		Prompt string `json:"prompt"`
		Crons  []struct {
			Prompt string `json:"prompt"`
		} `json:"session_crons"`
	}
	if err := json.Unmarshal(c.Stdin, &p); err != nil {
		t.Fatalf("hooks/%s: %v", c.Dir, err)
	}
	out := []string{p.Prompt}
	for _, cr := range p.Crons {
		out = append(out, cr.Prompt)
	}
	return out
}

// TestSavedMemory checks the memory the agent keeps in state.json across a
// restart (SaveState, LoadState): an adapter that starts from the memory
// another saved, partway through WP-AD1's capture, makes the records the
// capture's expected.jsonl gives, and the memory holds no prompt text.
func TestSavedMemory(t *testing.T) {
	calls := readCalls(t)
	want := readExpected(t)
	every := make([]string, 0, len(calls))
	for _, c := range calls {
		every = append(every, c.Dir)
	}
	cases := []struct {
		name string
		at   []string // the calls before which a new adapter takes over the memory
	}{
		{"an adapter loaded before the /loop prompts matches them against the scheduled prompt of the stop before them", []string{"000061"}},
		{"an adapter loaded before the queued message makes it the running turn's agent.queue", []string{"000076"}},
		{"an adapter loaded before every call makes the records of one that ran throughout", every},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := newTest()
			var recs []adapters.Replayed
			var prompts []string
			leaked := ""
			for _, c := range calls {
				if slices.Contains(tc.at, c.Dir) {
					raw, err := a.SaveState()
					if err != nil {
						t.Fatal(err)
					}
					for _, p := range prompts {
						if len(p) >= 8 && bytes.Contains(raw, []byte(p)) && leaked == "" {
							leaked = c.Dir
						}
					}
					b := newTest()
					if err := b.LoadState(raw); err != nil {
						t.Fatalf("before hooks/%s: %v", c.Dir, err)
					}
					a = b
				}
				got, fails := adapters.Replay(a, []adapters.Call{c}, adapters.ReplayOptions{GOOS: "linux", Way: adapters.Walked})
				for dir, err := range fails {
					t.Errorf("hooks/%s: %v", dir, err)
				}
				recs = append(recs, got...)
				prompts = append(prompts, promptsIn(t, c)...)
			}
			lines, err := adapters.Normalize(recs)
			if err != nil {
				t.Fatal(err)
			}
			checkDiff(t, lines, want)
			if leaked != "" {
				t.Errorf("the memory saved before hooks/%s holds the text of a prompt", leaked)
			}
		})
	}
}

// TestSavedTurns checks, on synthetic calls, that the turn ids the memory
// keeps are every one the session's calls named, with when each was first
// named (E-cc-queued-turn): an adapter loaded from it takes a message
// queued in a turn that took up an earlier queued message for that turn's
// agent.queue, and a prompt made before the call that named its turn for
// the turn's prompt.submit.
func TestSavedTurns(t *testing.T) {
	const (
		sid = "11111111-2222-4333-8444-555555555555" // hookRaw's session
		p1  = "aaaaaaaa-1111-4111-8111-111111111111"
		p2  = "bbbbbbbb-2222-4222-8222-222222222222"
	)
	prompt := func(pid, text string) adapters.RawRecord {
		return hookRaw(EventUserPromptSubmit, map[string]any{"prompt_id": pid, "prompt": text}, nil)
	}
	stop := hookRaw(EventStop, map[string]any{"prompt_id": p1, "stop_hook_active": false,
		"last_assistant_message": "Done, the test passes."}, nil)
	tool := hookRaw(EventPostToolUse, map[string]any{"prompt_id": p2, "tool_name": "Bash", "tool_use_id": "toolu_saved_turns",
		"tool_input": map[string]any{"command": "ls"}, "tool_response": map[string]any{"stdout": "a.txt\n", "stderr": ""},
		"duration_ms": 40}, nil)
	// at is r as made at t ms after t0, as a spooled call keeps its own time.
	at := func(r adapters.RawRecord, t int64) adapters.RawRecord {
		r.Arrival.T += t
		r.Arrival.Mono += t
		return r
	}
	cases := []struct {
		name   string
		before []adapters.RawRecord // parsed by the adapter that saves its memory
		after  adapters.RawRecord   // parsed by the one that loads it
		kind   event.Kind           // the record after makes
		turn   string               // its turn
	}{
		{name: "an adapter loaded after a picked-up turn's first PostToolUse makes a message queued then that turn's agent.queue",
			before: []adapters.RawRecord{prompt(p1, "Fix the failing test."), at(prompt(p1, "Then update the README."), 1000),
				at(stop, 2000), at(tool, 3000)},
			after: at(prompt(p2, "And bump the version too."), 4000), kind: event.KindAgentQueue, turn: p2},
		{name: "an adapter loaded after a turn's PostToolUse makes the prompt the spool brings late, made before it, the turn's prompt.submit",
			before: []adapters.RawRecord{at(tool, 5000)},
			after:  prompt(p2, "Start the release."), kind: event.KindPromptSubmit, turn: p2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := newTest()
			for _, r := range tc.before {
				if _, err := a.Parse(r); err != nil {
					t.Fatal(err)
				}
			}
			raw, err := a.SaveState()
			if err != nil {
				t.Fatal(err)
			}
			b := newTest()
			if err := b.LoadState(raw); err != nil {
				t.Fatal(err)
			}
			recs, err := b.Parse(tc.after)
			if err != nil {
				t.Fatal(err)
			}
			var got []adapters.Record
			for _, r := range recs {
				if r.Event.Kind() != event.KindSessionStart { // the walk's, for a session new to it
					got = append(got, r)
				}
			}
			if len(got) != 1 || got[0].Event.Kind() != tc.kind {
				t.Fatalf("records %v, want one %s", kinds(recs), tc.kind)
			}
			var turn string
			switch d := got[0].Event.Data.(type) {
			case *event.AgentQueue:
				turn = d.TurnKey
			case *event.PromptSubmit:
				turn = d.TurnKey
			}
			if turn != b.keys.Turn(sid, tc.turn) {
				t.Errorf("record %s %+v, want turn %s's", got[0].Event.Kind(), got[0].Event.Data, tc.turn)
			}
		})
	}
}

// TestLoadState checks that memory LoadState can't read is refused and
// leaves the adapter's own as it was.
func TestLoadState(t *testing.T) {
	calls := readCalls(t)
	cases := []struct {
		name string
		raw  string
	}{
		{"memory that isn't JSON is refused", "not json"},
		{"memory of another format is refused", `{"v":99,"sessions":[{"id":"s","hook_start":true}]}`},
		{"memory without a format is refused", `{"sessions":[]}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := newTest()
			adapters.Replay(a, calls[:1], adapters.ReplayOptions{GOOS: "linux", Way: adapters.Walked})
			before, err := a.SaveState()
			if err != nil {
				t.Fatal(err)
			}
			if err := a.LoadState(json.RawMessage(tc.raw)); err == nil {
				t.Error("LoadState took it")
			}
			after, err := a.SaveState()
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(before, after) {
				t.Errorf("the adapter's memory changed: %s, then %s", before, after)
			}
		})
	}
}
