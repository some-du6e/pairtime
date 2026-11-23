// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/some-du6e/pairtime/internal/adapters"
	"github.com/some-du6e/pairtime/internal/agent/keys"
	"github.com/some-du6e/pairtime/internal/agent/vault"
	"github.com/some-du6e/pairtime/pkg/event"
	pkgledger "github.com/some-du6e/pairtime/pkg/ledger"
)

// What the planted calls carry, each a string that nothing else on the
// device holds: a prompt that doesn't start with a slash, which the ledger
// would keep as its slash_command, a final agent message, and the name of
// the file the agent edits.
const (
	plantedPrompt = "planted-prompt-5b0e2c9d4f71 what does count_words do"
	plantedReply  = "planted-reply-a83f01c6e2d9 it counts the words"
	plantedFile   = "planted-path-7c41d9e0b2a6.py"
)

// plantedCalls returns the capture's first ten calls, with the prompt of
// the first UserPromptSubmit, the message of the first Stop and the file
// of the first Edit planted: a session start, a prompt, a command, the
// turn's stop, then a turn with an edit.
func plantedCalls(t *testing.T) []adapters.Call {
	t.Helper()
	calls := readCapture(t)[:10]
	var prompt, reply, edit bool
	for i, c := range calls {
		var m map[string]any
		dec := json.NewDecoder(bytes.NewReader(c.Stdin))
		dec.UseNumber() // so the numbers go back as they came
		if err := dec.Decode(&m); err != nil {
			t.Fatalf("hooks/%s: %v", c.Dir, err)
		}
		switch name, _ := m["hook_event_name"].(string); {
		case name == "UserPromptSubmit" && !prompt:
			m["prompt"], prompt = plantedPrompt, true
		case name == "Stop" && !reply:
			m["last_assistant_message"], reply = plantedReply, true
		case name == "PostToolUse" && m["tool_name"] == "Edit" && !edit:
			in, _ := m["tool_input"].(map[string]any)
			out, _ := m["tool_response"].(map[string]any)
			for _, v := range []struct {
				m   map[string]any
				key string
			}{{in, "file_path"}, {out, "filePath"}} {
				p, _ := v.m[v.key].(string)
				if !strings.HasSuffix(p, "/wordcount.py") {
					t.Fatalf("hooks/%s: %s is %q", c.Dir, v.key, p)
				}
				v.m[v.key] = strings.TrimSuffix(p, "wordcount.py") + plantedFile
			}
			edit = true
		default:
			continue
		}
		b, err := json.Marshal(m)
		if err != nil {
			t.Fatal(err)
		}
		calls[i].Stdin = b
	}
	if !prompt || !reply || !edit {
		t.Fatalf("the capture's first calls have a prompt %v, a stop %v and an edit %v", prompt, reply, edit)
	}
	return calls
}

// rawHolders returns the files under dir whose bytes hold any of planted.
func rawHolders(t *testing.T, dir string, planted ...string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		for _, s := range planted {
			if bytes.Contains(b, []byte(s)) {
				rel, _ := filepath.Rel(dir, p)
				// Its first two words, such as planted-prompt.
				out = append(out, rel+" holds the "+strings.Join(strings.SplitN(s, "-", 3)[:2], "-")+" string")
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// keptOf opens the stopped agent's vault and returns the texts it keeps
// for the ledger's prompt and final message records, and the paths it
// keeps for its agent.tool records.
func keptOf(t *testing.T, r *hookReplay) (texts []string, paths []string) {
	t.Helper()
	k, _ := deviceKeys(t, r.dirs, []keys.Store{})
	st, err := keys.Inspect(r.dirs.Data)
	if err != nil {
		t.Fatal(err)
	}
	v, err := vault.Open(vault.Options{Dir: r.dirs.Vault(), Keys: k, KeyID: st.KeyID})
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	for _, rec := range readLedger(t, r.dirs, event.SourceHook) {
		s := rec.ev.Seq
		switch {
		case rec.body.C != nil:
			text, err := v.Text(s, *rec.body.C)
			switch {
			case err == nil:
				texts = append(texts, string(text))
			case !errors.Is(err, vault.ErrNotKept):
				t.Errorf("record %d's text: %v", s, err)
			}
		case rec.ev.Kind() == event.KindAgentTool:
			rt, _ := pkgledger.RTypeOf(event.KindAgentTool)
			f, _, err := rec.body.Features(rt)
			if err != nil {
				t.Fatal(err)
			}
			p, err := v.Paths(s, f.H)
			switch {
			case err == nil:
				paths = append(paths, p...)
			case !errors.Is(err, vault.ErrNotKept):
				t.Errorf("record %d's paths: %v", s, err)
			}
		}
	}
	return texts, paths
}

// writeConfig gives the replay's agent a config.toml that says keep.
func writeConfig(t *testing.T, r *hookReplay, keep bool) {
	t.Helper()
	cfg := "mode = \"local\"\n[privacy]\nkeep_prompt_text = false\n"
	if keep {
		cfg = "mode = \"local\"\n[privacy]\nkeep_prompt_text = true\n"
	}
	if err := os.MkdirAll(r.dirs.Config, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(r.dirs.ConfigFile(), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
}

// replay runs an agent on r through calls and stops it.
func replay(t *testing.T, r *hookReplay, calls []adapters.Call) {
	t.Helper()
	a := startAgent(t, r.options([]keys.Store{}))
	for _, c := range calls {
		r.send(t, event.HarnessClaudeCode, c, calls[0].Arrived)
	}
	waitParsed(t, a, uint64(len(calls)))
	a.cancel()
	if err := a.waitExit(t); err != nil {
		t.Fatalf("stopping the agent: %v", err)
	}
}

// TestPlantedContent checks DESIGN 3.5's split between the ledger and the
// vault on a real agent fed captured Claude Code hook calls (docs/PLAN.md,
// WP-LG6): a planted prompt, final agent message and edited file reach the
// ledger only as commitments and keys, so once the agent has stopped no
// file in the data directory holds their bytes, the vault included, which
// encrypts them; and the vault opens to them, unless "keep prompt text" is
// off.
func TestPlantedContent(t *testing.T) {
	cases := []struct {
		name string
		// keep is "keep prompt text" in each of the agent's runs: the
		// calls go to the first, and the others only start and stop.
		keep []bool
		// wantText is whether the vault keeps the planted texts at the end.
		wantText bool
	}{
		{"with keep prompt text on, the vault opens to the planted prompt and reply, and no file holds their bytes", []bool{true}, true},
		{"with keep prompt text off, the vault keeps neither, and no file holds their bytes", []bool{false}, false},
		{"turning keep prompt text off deletes the text the vault kept, at the agent's next start", []bool{true, false}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			calls := plantedCalls(t)
			r := newHookReplay(t)
			writeConfig(t, r, c.keep[0])
			replay(t, r, calls)
			for i, keep := range c.keep[1:] {
				writeConfig(t, r, keep)
				// One call the agent hasn't had, since its journal drops one
				// it took before, so the ledger and its vault have opened and
				// the switch is in force before the stop.
				replay(t, r, readCapture(t)[len(calls)+i:][:1])
			}
			t.Run("no file in the data directory holds the planted bytes", func(t *testing.T) {
				for _, h := range rawHolders(t, r.dirs.Data, plantedPrompt, plantedReply, plantedFile) {
					t.Error(h)
				}
			})
			texts, paths := keptOf(t, r)
			t.Run("the vault keeps the planted texts exactly when keep prompt text is on", func(t *testing.T) {
				for _, want := range []string{plantedPrompt, plantedReply} {
					if got := slices.Contains(texts, want); got != c.wantText {
						t.Errorf("the vault keeps %q: %v, want %v", want, got, c.wantText)
					}
				}
				if !c.wantText && len(texts) > 0 {
					t.Errorf("the vault keeps %d texts", len(texts))
				}
			})
			t.Run("the vault keeps the edited file's path, whatever the switch says", func(t *testing.T) {
				if !slices.ContainsFunc(paths, func(p string) bool { return strings.HasSuffix(p, plantedFile) }) {
					t.Errorf("the vault keeps paths %q, want one of %q among them", paths, plantedFile)
				}
			})
			t.Run("the ledger commits the planted prompt", func(t *testing.T) {
				k, _ := deviceKeys(t, r.dirs, []keys.Store{})
				found := false
				for _, rec := range readLedger(t, r.dirs, event.SourceHook) {
					if rec.ev.Kind() != event.KindPromptSubmit || rec.body.C == nil {
						continue
					}
					if cm, err := k.Commit(rec.ev.Seq, []byte(plantedPrompt)); err == nil && cm == *rec.body.C {
						found = true
					}
				}
				if !found {
					t.Error("no prompt record commits the planted prompt")
				}
			})
		})
	}
}
