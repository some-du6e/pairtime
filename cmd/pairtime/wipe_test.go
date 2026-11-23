// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/agent/config"
	"github.com/some-du6e/pairtime/internal/agent/home"
	"github.com/some-du6e/pairtime/internal/agent/keys"
	"github.com/some-du6e/pairtime/internal/agent/localhttp"
)

// recordedEntries are what pairtime records in its data directory (DESIGN
// 3.5), which "delete everything" deletes, with a file in each.
var recordedEntries = []string{
	"ledger/2026-11-20.seg", "ledger/batches.seg", "ledger/old/0123456789abcdef/2026-10-01.seg",
	"vault/2026-11-20.seg", "buckets/2026-11-20.jsonl", "days/2026-11-20.json", "coach/recaps.jsonl",
	"commits/2026-11-20.jsonl", "inbox/spool.jsonl", "received/2026-11-20.jsonl",
	"held.jsonl", "held.jsonl.pending", "held.jsonl.replay", "state.json", "state.json.bad",
	"uploads.jsonl", "outbox.json", "port.json", "account.json", "signin-0123456789abcdef.html",
}

// keptEntries are pairtime itself, its settings and its locks, which stay.
var keptEntries = []string{"install/setup.json", "install.lock", "bin/pairtime", "account/agent.lock", "agent.lock", "config.lock"}

// fillHome makes a data directory holding every recorded and kept entry,
// a device key in a key file, and a config.toml that says mode.
func fillHome(t *testing.T, dir string, mode config.Mode) {
	t.Helper()
	for _, name := range slices.Concat(recordedEntries, keptEntries) {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("planted-wipe-7f3e "+name), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := keys.Open(keys.Options{Dir: dir, Stores: []keys.Store{}}); err != nil {
		t.Fatal(err)
	}
	cfg := "mode = \"" + string(mode) + "\"\n"
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
}

// answering makes the teen's answers to a question come from s.
func answering(t *testing.T, s string) {
	t.Helper()
	old := installStdin
	installStdin = strings.NewReader(s)
	t.Cleanup(func() { installStdin = old })
}

func exists(t *testing.T, dir, name string) bool {
	t.Helper()
	_, err := os.Lstat(filepath.Join(dir, filepath.FromSlash(name)))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		t.Fatal(err)
	}
	return err == nil
}

func TestPrivacyDelete(t *testing.T) {
	tests := []struct {
		name     string
		fill     bool
		mode     config.Mode
		args     []string
		answers  string
		wantCode int
		check    func(t *testing.T, dir, out string)
	}{
		{
			name: "delete everything deletes the device key and every store pairtime recorded",
			fill: true, mode: config.Local, args: []string{"--yes"},
			check: func(t *testing.T, dir, out string) {
				if _, err := keys.Inspect(dir); !errors.Is(err, fs.ErrNotExist) {
					t.Errorf("keys.Inspect after the wipe: %v, want fs.ErrNotExist", err)
				}
				for _, name := range recordedEntries {
					if top := strings.Split(name, "/")[0]; exists(t, dir, top) {
						t.Errorf("%s is still there", top)
					}
				}
				if !strings.Contains(out, "Deleted pairtime's device key and everything pairtime recorded") {
					t.Errorf("printed %q", out)
				}
			},
		},
		{
			name: "pairtime itself, its settings and its locks stay",
			fill: true, mode: config.Local, args: []string{"--yes"},
			check: func(t *testing.T, dir, out string) {
				for _, name := range append(slices.Clone(keptEntries), "config.toml") {
					if !exists(t, dir, name) {
						t.Errorf("%s was deleted", name)
					}
				}
			},
		},
		{
			name: "no planted byte of what was recorded is left in the data directory",
			fill: true, mode: config.Local, args: []string{"--yes"},
			check: func(t *testing.T, dir, out string) {
				filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
					if err != nil || d.IsDir() {
						return err
					}
					b, err := os.ReadFile(p)
					if err != nil {
						t.Fatal(err)
					}
					for _, name := range recordedEntries {
						if bytes.Contains(b, []byte("planted-wipe-7f3e "+name)) {
							t.Errorf("%s holds what %s held", p, name)
						}
					}
					return nil
				})
			},
		},
		{
			name: "a computer that was online goes back to local-only, since its sign-in is gone",
			fill: true, mode: config.Online, args: []string{"--yes"},
			check: func(t *testing.T, dir, out string) {
				cfg, _, err := config.Load(filepath.Join(dir, "config.toml"))
				if err != nil || cfg.Mode != config.Local {
					t.Errorf("config.toml says mode %q (%v), want local", cfg.Mode, err)
				}
			},
		},
		{
			name: "without --yes it asks first, and y goes ahead",
			fill: true, mode: config.Local, answers: "y\n",
			check: func(t *testing.T, dir, out string) {
				if !strings.Contains(out, "Type y to delete everything") || exists(t, dir, "ledger") {
					t.Errorf("printed %q, and the ledger is there: %v", out, exists(t, dir, "ledger"))
				}
				// What a backup or a synced folder holds, the deletion can't reach.
				if !strings.Contains(out, "Copies a backup or a synced folder already holds stay as they are") {
					t.Errorf("the question doesn't say backups keep their copies: %q", out)
				}
			},
		},
		{
			name: "answering no changes nothing",
			fill: true, mode: config.Local, answers: "n\n", wantCode: exitDeclined,
			check: func(t *testing.T, dir, out string) {
				for _, name := range recordedEntries {
					if !exists(t, dir, name) {
						t.Errorf("%s was deleted", name)
					}
				}
				if _, err := keys.Inspect(dir); err != nil {
					t.Errorf("keys.Inspect: %v, want the key still there", err)
				}
			},
		},
		{
			name: "a data directory with nothing recorded in it has nothing to delete",
			args: []string{"--yes"},
			check: func(t *testing.T, dir, out string) {
				if !strings.Contains(out, "nothing to delete") {
					t.Errorf("printed %q", out)
				}
			},
		},
		{
			name: "an argument besides the flags is a usage error",
			fill: true, mode: config.Local, args: []string{"--yes", "all"}, wantCode: 2,
			check: func(t *testing.T, dir, out string) {
				if !exists(t, dir, "ledger") {
					t.Error("the ledger was deleted")
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv(home.EnvHome, dir)
			if tt.fill {
				fillHome(t, dir, tt.mode)
			}
			answering(t, tt.answers)
			var out, errOut bytes.Buffer
			code := run(append([]string{"privacy", "delete"}, tt.args...), &out, &errOut)
			if code != tt.wantCode {
				t.Fatalf("exit %d, want %d; stdout:\n%s\nstderr:\n%s", code, tt.wantCode, out.String(), errOut.String())
			}
			tt.check(t, dir, out.String())
		})
	}
}

// TestPrivacyDeleteAgent runs "delete everything" while a real agent runs
// on the data directory: the agent stops before anything goes, and starts
// again afterwards under a new device key, with none of what it recorded.
func TestPrivacyDeleteAgent(t *testing.T) {
	ptHome := t.TempDir()
	// The agent delete starts again is this test binary too.
	t.Setenv(runEnv, ownArgs)
	t.Setenv(home.EnvHome, ptHome)
	var firstOut bytes.Buffer
	first := startPairtime(t, ptHome, "agent", &firstOut)
	// Registered after t.TempDir's cleanup, so they run first: Windows
	// can't delete the directory while an agent has files in it open.
	t.Cleanup(func() {
		first.Process.Kill()
		first.Wait()
		if t.Failed() {
			t.Logf("the first agent's output:\n%s", firstOut.String())
		}
	})
	waitForAgent(t, ptHome)
	before := waitForKey(t, ptHome, "")

	tests := []struct {
		name  string
		check func(t *testing.T, out string)
	}{
		{
			name: "the agent that ran was stopped before anything went",
			check: func(t *testing.T, out string) {
				if err := waitWithin(t, first, 30*time.Second); err != nil {
					t.Errorf("the first agent ended with %v", err)
				}
			},
		},
		{
			name: "the agent starts again under a new device key",
			check: func(t *testing.T, out string) {
				if !strings.Contains(out, "agent started again") {
					t.Errorf("printed %q", out)
				}
				after := waitForKey(t, ptHome, before)
				if after == before {
					t.Errorf("the key id is %s, as before the wipe", after)
				}
				pf, err := localhttp.ReadPortFile(filepath.Join(ptHome, "port.json"))
				if err != nil {
					t.Fatal(err)
				}
				stopAtCleanup(t, pf.PID)
			},
		},
	}
	var out, errOut bytes.Buffer
	if code := run([]string{"privacy", "delete", "--yes"}, &out, &errOut); code != 0 {
		t.Fatalf("exit %d; stdout:\n%s\nstderr:\n%s", code, out.String(), errOut.String())
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) { tt.check(t, out.String()) })
	}
}

// waitForKey waits up to 30 s for the keystore in dir to hold a device key
// whose id isn't not, and returns the id.
func waitForKey(t *testing.T, dir, not string) string {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		st, err := keys.Inspect(dir)
		if err == nil && st.KeyID != not {
			return st.KeyID
		}
		if time.Now().After(deadline) {
			t.Fatalf("no new device key in %s within 30 s: %v", dir, err)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
