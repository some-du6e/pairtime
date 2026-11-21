// SPDX-License-Identifier: AGPL-3.0-or-later

package install

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// claudeSettings writes settings.json in env's Claude Code folder: a
// pairtime hook running exe for each event in events, then the extra
// top-level members, as raw JSON.
func claudeSettings(t *testing.T, env Env, exe string, events []string, extra map[string]any) {
	t.Helper()
	hooks := map[string]any{}
	for _, ev := range events {
		hooks[ev] = []any{map[string]any{"hooks": []any{map[string]any{
			"type": "command", "command": exe, "args": hookArgs(ev),
		}}}}
	}
	top := map[string]any{"hooks": hooks}
	for k, v := range extra {
		top[k] = v
	}
	b, err := json.MarshalIndent(top, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	writeSettings(t, env, b)
}

func writeSettings(t *testing.T, env Env, b []byte) {
	t.Helper()
	path := ClaudeSettings(env)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestReadClaudeHooks(t *testing.T) {
	var all []string
	for _, hk := range claudeHooks {
		all = append(all, hk.event)
	}
	exe := testExe["linux"]
	tests := []struct {
		name         string
		write        func(t *testing.T, env Env)
		wantEvents   []string
		wantMissing  []string
		wantExes     []string
		wantDisabled bool
		wantErr      string
	}{
		{
			name:  "a settings file that isn't there holds no hooks, and that's no error",
			write: func(*testing.T, Env) {},
		},
		{
			name:       "every hook install writes is found, with the binary it runs, and none is missing",
			write:      func(t *testing.T, env Env) { claudeSettings(t, env, exe, all, nil) },
			wantEvents: all,
			wantExes:   []string{exe},
		},
		{
			name: "a hook taken out of the file is missing, in the order install writes them",
			write: func(t *testing.T, env Env) {
				claudeSettings(t, env, exe, slices.DeleteFunc(slices.Clone(all), func(ev string) bool { return ev == "Stop" || ev == "SessionEnd" }), nil)
			},
			wantEvents:  slices.DeleteFunc(slices.Clone(all), func(ev string) bool { return ev == "Stop" || ev == "SessionEnd" }),
			wantMissing: []string{"Stop", "SessionEnd"},
			wantExes:    []string{exe},
		},
		{
			name: "another tool's hooks are neither pairtime's nor missing ones",
			write: func(t *testing.T, env Env) {
				writeSettings(t, env, []byte(`{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"git-ai","args":["checkpoint"]}]}]}}`))
			},
		},
		{
			name: "disableAllHooks set to true turns every hook off",
			write: func(t *testing.T, env Env) {
				claudeSettings(t, env, exe, all, map[string]any{"disableAllHooks": true})
			},
			wantEvents:   all,
			wantExes:     []string{exe},
			wantDisabled: true,
		},
		{
			name: "disableAllHooks set to false, or to a string, leaves the hooks on",
			write: func(t *testing.T, env Env) {
				writeSettings(t, env, []byte(`{"disableAllHooks": "true", "hooks": {}}`))
			},
		},
		{
			name: "hooks two installs left behind name both binaries, sorted",
			write: func(t *testing.T, env Env) {
				b := []byte(`{"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"/opt/pairtime","args":["hook","--harness","claude_code","--event","SessionStart"]}]},` +
					`{"hooks":[{"type":"command","command":"` + exe + `","args":["hook","--harness","claude_code","--event","SessionStart"]}]}]}}`)
				writeSettings(t, env, b)
			},
			wantEvents:  []string{"SessionStart"},
			wantMissing: all[1:],
			wantExes:    []string{exe, "/opt/pairtime"},
		},
		{
			name:    "a file that isn't JSON is an error that names it",
			write:   func(t *testing.T, env Env) { writeSettings(t, env, []byte(`{"hooks": `)) },
			wantErr: "settings.json isn't valid JSON",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := testEnv(t, "linux", nil)
			tt.write(t, env)
			got, err := ReadClaudeHooks(env)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("ReadClaudeHooks = %+v, %v; want an error with %q", got, err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got.Path != ClaudeSettings(env) || !slices.Equal(got.Events, tt.wantEvents) || !slices.Equal(got.Missing, tt.wantMissing) ||
				!slices.Equal(got.Exes, tt.wantExes) || got.Disabled != tt.wantDisabled {
				t.Errorf("ReadClaudeHooks = %+v, want events %q, missing %q, binaries %q, disabled %v", got, tt.wantEvents, tt.wantMissing, tt.wantExes, tt.wantDisabled)
			}
		})
	}
}
