// SPDX-License-Identifier: AGPL-3.0-or-later

package claudecode

import (
	"bytes"
	"encoding/json"
	"slices"
	"testing"

	"github.com/some-du6e/pairtime/internal/adapters"
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
