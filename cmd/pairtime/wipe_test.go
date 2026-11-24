// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/agent/config"
	"github.com/some-du6e/pairtime/internal/agent/home"
	"github.com/some-du6e/pairtime/internal/agent/keys"
	"github.com/some-du6e/pairtime/internal/agent/localhttp"
	"github.com/some-du6e/pairtime/internal/agent/store"
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
		name string
		fill bool
		mode config.Mode
		// prepare changes the data directory dir after fillHome.
		prepare  func(t *testing.T, dir string)
		args     []string
		answers  string
		wantCode int
		// stderr is a part of what privacy delete prints on standard error,
		// "" for anything.
		stderr string
		check  func(t *testing.T, dir, out string)
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
			name: "the question and the result say install's record and its copies of the files it edited stay, so that uninstall can put them back",
			fill: true, mode: config.Local, answers: "y\n",
			check: func(t *testing.T, dir, out string) {
				const stays = "so does install's record of what it changed, with its copies of the files it edited, so that pairtime uninstall can put them back"
				if n := strings.Count(out, stays); n != 2 {
					t.Errorf("the question and the result say %q %d times, want 2:\n%s", stays, n, out)
				}
			},
		},
		{
			name: "when some of the device key may stay, the result still says install's record and its copies stay",
			fill: true, mode: config.Local, args: []string{"--yes"}, wantCode: 1,
			// A keystore.json that doesn't parse can't say which of the OS's
			// secret stores holds the key, so Destroy deletes its files and
			// says what may be left.
			prepare: func(t *testing.T, dir string) {
				if err := os.WriteFile(filepath.Join(dir, "keys", "keystore.json"), []byte("{"), 0o600); err != nil {
					t.Fatal(err)
				}
			},
			check: func(t *testing.T, dir, out string) {
				const stays = " stay, and so does install's record of what it changed, with its copies of the files it edited, so that pairtime uninstall can put them back. It sent nothing to the pairtime server"
				if !strings.Contains(out, "Deleted everything pairtime recorded in ") || !strings.Contains(out, stays) || !strings.Contains(out, "\nLeft alone: ") {
					t.Errorf("the result doesn't say what stays and what was left alone:\n%s", out)
				}
			},
		},
		{
			name: "the names the teen gave the projects this computer knows of go from config.toml, mode goes back to local-only, and another project's table stays, counted but never named",
			fill: true, mode: config.Local, args: []string{"--yes"},
			prepare: func(t *testing.T, dir string) {
				knownProject(t, dir, wipeProjectKey)
				cfg := "mode = \"online\"\n\n[display]\nshadow = false\n\n[projects." + wipeProjectKey + "]\nname = \"chip8-emulator\"\nonline = true\n\n[projects." + farProjectKey + "]\nname = \"far-away\"\n"
				if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(cfg), 0o600); err != nil {
					t.Fatal(err)
				}
			},
			check: func(t *testing.T, dir, out string) {
				cfg := filepath.Join(dir, "config.toml")
				got, err := os.ReadFile(cfg)
				if err != nil {
					t.Fatal(err)
				}
				if want := "mode = \"local\"\n\n[display]\nshadow = false\n\n[projects." + farProjectKey + "]\nname = \"far-away\"\n"; string(got) != want {
					t.Errorf("config.toml is\n%q\nwant\n%q", got, want)
				}
				if !strings.Contains(out, ", and from "+cfg+" the names you gave the projects this computer knows of. "+cfg+" is back in local-only mode, so pairtime uploads nothing until you sign in again. Your other settings stay") {
					t.Errorf("the result doesn't say the names went from %s, and the mode back to local-only:\n%s", cfg, out)
				}
				if !strings.Contains(out, "Left alone: 1 table under projects in "+cfg+", for a project this computer doesn't know of") || strings.Contains(out, "far-away") {
					t.Errorf("the result doesn't count the table it left, or names it:\n%s", out)
				}
			},
		},
		{
			name: "a table of this computer's that spells projects in another case stays, named under Left alone, another project's table is counted, the rest of the wipe runs, and the exit status is 1",
			fill: true, mode: config.Local, args: []string{"--yes"}, wantCode: 1,
			prepare: func(t *testing.T, dir string) {
				knownProject(t, dir, wipeProjectKey)
				cfg := "mode = \"local\"\n\n[Projects." + wipeProjectKey + "]\nname = \"chip8-emulator\"\n\n[Projects." + farProjectKey + "]\nname = \"far-away\"\n"
				if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(cfg), 0o600); err != nil {
					t.Fatal(err)
				}
			},
			check: func(t *testing.T, dir, out string) {
				cfg := filepath.Join(dir, "config.toml")
				if got, err := os.ReadFile(cfg); err != nil || !strings.Contains(string(got), "[Projects."+wipeProjectKey+"]") {
					t.Errorf("config.toml is %q (%v), want it as it was", got, err)
				}
				if !strings.Contains(out, "Left alone: the names you gave some of the projects this computer knows of (") || !strings.Contains(out, "; delete [Projects."+wipeProjectKey+"] in "+cfg+" by hand).") {
					t.Errorf("the result doesn't name the table to delete by hand:\n%s", out)
				}
				if strings.Count(out, "by hand") != 2 || !strings.Contains(out, "Left alone: 1 table under projects in "+cfg) || strings.Contains(out, "far-away") {
					t.Errorf("the result says what to delete by hand other than once for the names and once for the other project's table, or doesn't count that table, or names it:\n%s", out)
				}
				for _, name := range recordedEntries {
					if top := strings.Split(name, "/")[0]; exists(t, dir, top) {
						t.Errorf("%s is still there, though only the names stayed", top)
					}
				}
			},
		},
		{
			name: "a data directory with nothing recorded and a table of a project this computer doesn't know of has nothing to delete, and says how many tables it left",
			args: []string{"--yes"},
			prepare: func(t *testing.T, dir string) {
				cfg := "mode = \"local\"\n\n[projects." + farProjectKey + "]\nname = \"far-away\"\n"
				if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(cfg), 0o600); err != nil {
					t.Fatal(err)
				}
			},
			check: func(t *testing.T, dir, out string) {
				cfg := filepath.Join(dir, "config.toml")
				if got, err := os.ReadFile(cfg); err != nil || !strings.Contains(string(got), "[projects."+farProjectKey+"]") {
					t.Errorf("config.toml is %q (%v), want it as it was", got, err)
				}
				if !strings.Contains(out, "nothing to delete") || !strings.Contains(out, "Left alone: 1 table under projects in "+cfg) || strings.Contains(out, "far-away") {
					t.Errorf("printed %q", out)
				}
			},
		},
		{
			name: "a refused edit names a header-less entry of this computer's by its key path as written, with the header it is under, prints no label, lets the rest of the wipe run, and exits 1",
			fill: true, mode: config.Local, args: []string{"--yes"}, wantCode: 1,
			prepare: func(t *testing.T, dir string) {
				knownProject(t, dir, wipeProjectKey)
				cfg := "mode = \"local\"\n\n[Projects]\n" + wipeProjectKey + " = { name = \"chip8-emulator\" } # the emulator\n"
				if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(cfg), 0o600); err != nil {
					t.Fatal(err)
				}
			},
			check: func(t *testing.T, dir, out string) {
				cfg := filepath.Join(dir, "config.toml")
				if !strings.Contains(out, "; delete "+wipeProjectKey+" under [Projects] in "+cfg+" by hand).") || strings.Contains(out, "chip8-emulator") || strings.Contains(out, "the emulator") {
					t.Errorf("the result doesn't name the entry by its key path alone, or shows its label or comment:\n%s", out)
				}
				for _, name := range recordedEntries {
					if top := strings.Split(name, "/")[0]; exists(t, dir, top) {
						t.Errorf("%s is still there, though only the name stayed", top)
					}
				}
			},
		},
		{
			name: "the question and the result say delete everything sets the mode back to local-only, so pairtime uploads nothing until the teen signs in again",
			fill: true, mode: config.Online, answers: "y\n",
			check: func(t *testing.T, dir, out string) {
				const mode = "local-only mode, so pairtime uploads nothing until you sign in again"
				if n := strings.Count(out, mode); n != 2 {
					t.Errorf("the question and the result say %q %d times, want 2:\n%s", mode, n, out)
				}
				if cfg, _, err := config.Load(filepath.Join(dir, "config.toml")); err != nil || cfg.Mode != config.Local {
					t.Errorf("config.toml says mode %q (%v), want local", cfg.Mode, err)
				}
			},
		},
		{
			name: "when some of what pairtime recorded can't be deleted once the key is gone, the names of this computer's projects still go, another project's table stays and is counted, and the exit status is 1",
			fill: true, mode: config.Local, args: []string{"--yes"}, wantCode: 1,
			stderr: "pairtime's device key is deleted, but some of what it recorded isn't",
			prepare: func(t *testing.T, dir string) {
				knownProject(t, dir, wipeProjectKey)
				cfg := "mode = \"local\"\n\n[projects." + wipeProjectKey + "]\nname = \"chip8-emulator\"\n\n[projects." + farProjectKey + "]\nname = \"far-away\"\n"
				if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(cfg), 0o600); err != nil {
					t.Fatal(err)
				}
				undeletable(t, filepath.Join(dir, "ledger", "old", "0123456789abcdef", "2026-10-01.seg"))
			},
			check: func(t *testing.T, dir, out string) {
				cfg := filepath.Join(dir, "config.toml")
				if got, err := os.ReadFile(cfg); err != nil || string(got) != "mode = \"local\"\n\n[projects."+farProjectKey+"]\nname = \"far-away\"\n" {
					t.Errorf("config.toml is %q (%v), want this computer's table gone and the other project's kept", got, err)
				}
				if !strings.Contains(out, "Took out of "+cfg+" the names you gave the projects this computer knows of.") || !strings.Contains(out, "Left alone: 1 table under projects in "+cfg) || strings.Contains(out, "far-away") {
					t.Errorf("the result doesn't say the names went and count the table it left, or names that table:\n%s", out)
				}
				if strings.Contains(out, "Deleted pairtime's device key and everything") {
					t.Errorf("the result says everything was deleted:\n%s", out)
				}
				if _, err := keys.Inspect(dir); !errors.Is(err, fs.ErrNotExist) {
					t.Errorf("keys.Inspect: %v, want the key gone", err)
				}
				if exists(t, dir, "state.json") || !exists(t, dir, "ledger/old/0123456789abcdef/2026-10-01.seg") {
					t.Errorf("state.json is there: %v, or the file that couldn't be deleted isn't", exists(t, dir, "state.json"))
				}
			},
		},
		{
			name: "with no project's key this computer knows of, a config.toml that doesn't parse fails nothing: the wipe exits 0 and leaves it as it is",
			fill: true, mode: config.Local, args: []string{"--yes"},
			prepare: func(t *testing.T, dir string) {
				if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte("mode = [\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			},
			check: func(t *testing.T, dir, out string) {
				if got, err := os.ReadFile(filepath.Join(dir, "config.toml")); err != nil || string(got) != "mode = [\n" {
					t.Errorf("config.toml is %q (%v), want it as it was", got, err)
				}
				if strings.Contains(out, "Left alone") || exists(t, dir, "ledger") {
					t.Errorf("the result leaves something alone, or the ledger is there (%v):\n%s", exists(t, dir, "ledger"), out)
				}
			},
		},
		{
			name: "a table whose key only a signed-in note in project-names.json holds stays, as another computer's sharing config.toml would, and is counted, never named",
			fill: true, mode: config.Local, args: []string{"--yes"},
			prepare: func(t *testing.T, dir string) {
				names := `{"v":1,"server":"","device":"","hidden":{"` + farProjectKey + `":{"label":"far-away","at":1,"reason":"signed_in"}}}`
				if err := os.WriteFile(filepath.Join(dir, "project-names.json"), []byte(names), 0o600); err != nil {
					t.Fatal(err)
				}
				cfg := "mode = \"local\"\n\n[projects." + farProjectKey + "]\nname = \"far-away\"\n"
				if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(cfg), 0o600); err != nil {
					t.Fatal(err)
				}
			},
			check: func(t *testing.T, dir, out string) {
				cfg := filepath.Join(dir, "config.toml")
				if got, err := os.ReadFile(cfg); err != nil || string(got) != "mode = \"local\"\n\n[projects."+farProjectKey+"]\nname = \"far-away\"\n" {
					t.Errorf("config.toml is %q (%v), want it as it was", got, err)
				}
				if !strings.Contains(out, "Left alone: 1 table under projects in "+cfg) || strings.Contains(out, "far-away") {
					t.Errorf("the result doesn't count the table it left, or names it:\n%s", out)
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
			if tt.prepare != nil {
				tt.prepare(t, dir)
			}
			answering(t, tt.answers)
			var out, errOut bytes.Buffer
			code := run(append([]string{"privacy", "delete"}, tt.args...), &out, &errOut)
			if code != tt.wantCode {
				t.Fatalf("exit %d, want %d; stdout:\n%s\nstderr:\n%s", code, tt.wantCode, out.String(), errOut.String())
			}
			if !strings.Contains(errOut.String(), tt.stderr) {
				t.Errorf("stderr is %q, want it to say %q", errOut.String(), tt.stderr)
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
	dirs, err := home.Default()
	if err != nil {
		t.Fatal(err)
	}
	// A name the teen gave a project this computer knows of, set while the
	// agent runs: project-names.json, which only an online uploader writes,
	// knows its key. The default config.toml's comments show an example
	// table, so this one's label is another.
	names := `{"v":1,"server":"","device":"","confirmed":{"` + wipeProjectKey + `":"robotics-club"}}`
	if err := os.WriteFile(filepath.Join(dirs.Data, "project-names.json"), []byte(names), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := config.ShowName(dirs.ConfigFile(), wipeProjectKey, "robotics-club"); err != nil {
		t.Fatal(err)
	}

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
		{
			// privacy delete stops the agent before the edit and starts it
			// again after, so no copy of config.toml an agent read writes
			// the names back.
			name: "the names the teen gave their projects are gone from config.toml, also once the agent runs again",
			check: func(t *testing.T, out string) {
				waitForKey(t, ptHome, before)
				data, err := os.ReadFile(dirs.ConfigFile())
				if err != nil {
					t.Fatal(err)
				}
				if cfg, _ := config.Parse(data); cfg.Projects != nil || strings.Contains(string(data), "robotics-club") {
					t.Errorf("config.toml still names a project:\n%s", data)
				}
				for _, line := range strings.Split(string(data), "\n") {
					if t2 := strings.TrimSpace(line); strings.HasPrefix(t2, "[projects.") {
						t.Errorf("config.toml still has the table %s:\n%s", t2, data)
					}
				}
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

// wipeProjectKey is the key of a project this computer knows of in the
// tests above, and farProjectKey one of another computer's sharing
// config.toml, in the form the git adapter's keys have.
const (
	wipeProjectKey = "g7c41e9a2d05b8f63e1a94c7d2b6f0e85"
	farProjectKey  = "g5e4d3c2b1a0f9e8d7c6b5a4f3e2d1c0b"
)

// knownProject writes a state.json in the data directory dir whose
// project map has a repo with key, so that this computer knows of it.
func knownProject(t *testing.T, dir, key string) {
	t.Helper()
	st := `{"v":1,"projects":{"repos":[{"common_dir":"/home/teen/code/chip8-emulator/.git","key":"` + key + `","kind":"repo"}]}}`
	if err := os.WriteFile(filepath.Join(dir, "state.json"), []byte(st), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestPrivacyDeleteXDG runs "delete everything" with pairtime's data and
// config folders apart, as XDG_DATA_HOME and XDG_CONFIG_HOME put them on
// Linux with PAIRTIME_HOME unset: the names go from the config folder's
// config.toml, which the result names by its own path.
func TestPrivacyDeleteXDG(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("XDG_DATA_HOME and XDG_CONFIG_HOME place pairtime's folders on Linux alone")
	}
	tests := []struct {
		name string
	}{
		{"with the data and config folders apart, the names go from the config folder's config.toml, which the result names"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			// Unset, not empty, as on a teen's computer: t.Setenv puts it
			// back after the test.
			t.Setenv(home.EnvHome, "")
			os.Unsetenv(home.EnvHome)
			t.Setenv("HOME", filepath.Join(root, "home"))
			t.Setenv("XDG_DATA_HOME", filepath.Join(root, "data"))
			t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
			dirs, err := home.Default()
			if err != nil {
				t.Fatal(err)
			}
			if dirs.Data == filepath.Dir(dirs.ConfigFile()) {
				t.Fatalf("the data folder %s holds config.toml", dirs.Data)
			}
			for _, d := range []string{dirs.Data, filepath.Dir(dirs.ConfigFile())} {
				if err := os.MkdirAll(d, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			knownProject(t, dirs.Data, wipeProjectKey)
			cfg := "mode = \"local\"\n\n[projects." + wipeProjectKey + "]\nname = \"chip8-emulator\"\n"
			if err := os.WriteFile(dirs.ConfigFile(), []byte(cfg), 0o600); err != nil {
				t.Fatal(err)
			}
			var out, errOut bytes.Buffer
			if code := run([]string{"privacy", "delete", "--yes"}, &out, &errOut); code != 0 {
				t.Fatalf("exit %d; stdout:\n%s\nstderr:\n%s", code, out.String(), errOut.String())
			}
			if got, err := os.ReadFile(dirs.ConfigFile()); err != nil || string(got) != "mode = \"local\"\n\n" {
				t.Errorf("config.toml is %q (%v), want it without the project's table", got, err)
			}
			if !strings.Contains(out.String(), "from "+dirs.ConfigFile()+" the names you gave the projects this computer knows of") {
				t.Errorf("the result doesn't name config.toml by its path, %s:\n%s", dirs.ConfigFile(), out.String())
			}
			if exists(t, dirs.Data, "state.json") {
				t.Error("state.json is still there")
			}
		})
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

// TestWipedKeys checks which keys "delete everything" counts as this
// computer's, whose tables it takes out of config.toml
// (D-wipe-project-labels): those of what this computer recorded itself,
// and of project-names.json but for a note of a name signed in elsewhere
// or one that couldn't be read, which a sign-in writes for every name
// config.toml has on, another computer's in a shared file included.
func TestWipedKeys(t *testing.T) {
	names := func(fields string) string { return `{"v":1,"server":"","device":"",` + fields + `}` }
	note := func(reason string) string {
		r := ""
		if reason != "" {
			r = `,"reason":"` + reason + `"`
		}
		return names(`"hidden":{"` + wipeProjectKey + `":{"label":"chip8-emulator","at":1` + r + `}}`)
	}
	tests := []struct {
		name string
		// state and alias put the key in state.json's project map, as a
		// repo's key or a key one had before; day puts it in a rollup.
		state, alias, day bool
		names             string // project-names.json, "" for none
		want              bool
	}{
		{name: "a key state.json's project map has is this computer's", state: true, want: true},
		{name: "a key a repo in state.json's project map had before is this computer's", alias: true, want: true},
		{name: "a key only a day's rollup has is this computer's", day: true, want: true},
		{name: "a key only project-names.json's codes hold is this computer's", names: names(`"codes":{"` + wipeProjectKey + `":"c8"}`), want: true},
		{name: "a key only its confirmed names hold is this computer's", names: names(`"confirmed":{"` + wipeProjectKey + `":"chip8-emulator"}`), want: true},
		{name: "a key only its sent names hold is this computer's", names: names(`"sent":{"` + wipeProjectKey + `":"chip8-emulator"}`), want: true},
		{name: "a key only the keys it deletes hold is this computer's", names: names(`"deleting":["` + wipeProjectKey + `"]`), want: true},
		{name: "a key only the keys it lists as hidden hold is this computer's", names: names(`"hides":["` + wipeProjectKey + `"]`), want: true},
		{name: "a key only a note of a name hidden on the dashboard holds is this computer's", names: note(""), want: true},
		{name: "a key only a note of a name the server dropped holds is this computer's", names: note("dropped"), want: true},
		{name: "a key only a note of a name signed in elsewhere holds isn't this computer's, as a config.toml computers share gives another computer's keys such notes", names: note("signed_in")},
		{name: "a key only a note of a name that couldn't be read holds isn't this computer's", names: note("unreadable")},
		{name: "a signed-in note's key that state.json's project map has too is this computer's", state: true, names: note("signed_in"), want: true},
		{name: "an unreadable note's key that a day's rollup has too is this computer's", day: true, names: note("unreadable"), want: true},
		{name: "a note with a reason this version doesn't know counts for nothing", names: note("later")},
		{name: "with nothing recorded, no key is this computer's"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv(home.EnvHome, dir)
			dirs, err := home.Default()
			if err != nil {
				t.Fatal(err)
			}
			if tt.state {
				knownProject(t, dir, wipeProjectKey)
			}
			if tt.alias {
				st := `{"v":1,"projects":{"repos":[{"common_dir":"/home/teen/code/chip8-emulator/.git","key":"` + farProjectKey + `","kind":"repo","aliases":["` + wipeProjectKey + `"]}]}}`
				if err := os.WriteFile(dirs.StateFile(), []byte(st), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if tt.day {
				if err := store.WriteDay(dirs.Days(), store.Day{Date: "2026-11-20", Zone: "UTC", Projects: []store.DayProject{{Project: wipeProjectKey}}}); err != nil {
					t.Fatal(err)
				}
			}
			if tt.names != "" {
				if err := os.WriteFile(filepath.Join(dirs.Data, "project-names.json"), []byte(tt.names), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if got := wipedKeys(dirs).has(wipeProjectKey); got != tt.want {
				t.Errorf("wipedKeys has the key: %v, want %v", got, tt.want)
			}
		})
	}
}
