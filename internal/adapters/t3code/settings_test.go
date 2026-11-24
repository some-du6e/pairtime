// SPDX-License-Identifier: AGPL-3.0-or-later

package t3code

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// TestClaudeHomes holds ClaudeHomes to T3's own rules for a Claude
// instance's home (ClaudeHome.ts, serverSettings.ts at T3 d8f3feff), which
// doctor reads to say when a T3 Claude instance loads none of pairtime's
// hooks (DESIGN 5.1, 5.9).
func TestClaudeHomes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the cases use Unix paths")
	}
	const home = "/home/teen"
	claude := func(instance string) string {
		return `{"providerInstances":{"claudeAgent":` + instance + `}}`
	}
	cases := []struct {
		name string
		// settings is settings.json's content, "" for no file.
		settings string
		want     []string
		wantErr  bool
	}{
		{name: "no settings.json gives no homes"},
		{name: "settings with no Claude instance give none",
			settings: `{"providerInstances":{"codex":{"driver":"codex","config":{"homePath":"/srv/codex"}}}}`},
		{name: "a Claude instance's CLAUDE_CONFIG_DIR path is a home of its own",
			settings: claude(`{"driver":"claudeAgent","config":{"homePath":"/srv/claude-work"}}`), want: []string{"/srv/claude-work"}},
		{name: "a path that starts with ~ is in the home folder, as T3 expands it",
			settings: claude(`{"driver":"claudeAgent","config":{"homePath":"~/claude-work"}}`), want: []string{"/home/teen/claude-work"}},
		{name: "a Claude instance that is off runs nothing",
			settings: claude(`{"driver":"claudeAgent","enabled":false,"config":{"homePath":"/srv/off"}}`)},
		{name: "the enabled flag older settings kept in the config turns it off too",
			settings: claude(`{"driver":"claudeAgent","config":{"enabled":false,"homePath":"/srv/off"}}`)},
		{name: "with no path, the CLAUDE_CONFIG_DIR its environment sets is its home",
			settings: claude(`{"driver":"claudeAgent","environment":[{"name":"CLAUDE_CONFIG_DIR","value":"/srv/env-home","sensitive":false}],"config":{}}`),
			want:     []string{"/srv/env-home"}},
		{name: "the path wins over the environment, as T3 sets CLAUDE_CONFIG_DIR from it last",
			settings: claude(`{"driver":"claudeAgent","environment":[{"name":"CLAUDE_CONFIG_DIR","value":"/srv/env-home"}],"config":{"homePath":"/srv/path-home"}}`),
			want:     []string{"/srv/path-home"}},
		{name: "a secret's empty value sets no home",
			settings: claude(`{"driver":"claudeAgent","environment":[{"name":"CLAUDE_CONFIG_DIR","value":"","sensitive":true,"secret":{"id":"s1"}}],"config":{}}`)},
		{name: "a Claude instance with neither runs in ~/.claude, which is no home of its own",
			settings: claude(`{"driver":"claudeAgent","config":{"binaryPath":"/opt/claude"}}`)},
		{name: "homes come sorted, each one once",
			settings: `{"providerInstances":{"a":{"driver":"claudeAgent","config":{"homePath":"/srv/b"}},` +
				`"b":{"driver":"claudeAgent","config":{"homePath":"/srv/a"}},"c":{"driver":"claudeAgent","config":{"homePath":"/srv/b/"}}}}`,
			want: []string{"/srv/a", "/srv/b"}},
		{name: "the providers map older T3 versions wrote counts when no instance has its id",
			settings: `{"providers":{"claudeAgent":{"homePath":"/srv/legacy"}}}`, want: []string{"/srv/legacy"}},
		{name: "an instance of that id takes the old entry's place, as T3 moves the entry only when there is none",
			settings: `{"providerInstances":{"claudeAgent":{"driver":"claudeAgent","config":{}}},"providers":{"claudeAgent":{"homePath":"/srv/legacy"}}}`},
		{name: "an old entry that is off runs nothing",
			settings: `{"providers":{"claudeAgent":{"enabled":false,"homePath":"/srv/legacy"}}}`},
		{name: "an instance T3 couldn't decode is passed over, and the others still count",
			settings: `{"providerInstances":{"x":{"driver":"claudeAgent","enabled":"yes","config":{"homePath":"/srv/x"}},` +
				`"claudeAgent":{"driver":"claudeAgent","config":{"homePath":"/srv/ok"}}}}`,
			want: []string{"/srv/ok"}},
		{name: "settings that aren't JSON are an error", settings: `{"providerInstances":`, wantErr: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			userdata := filepath.Join(dir, "userdata")
			if c.settings != "" {
				if err := os.MkdirAll(userdata, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(userdata, settingsName), []byte(c.settings), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			got, err := ClaudeHomes(Store{Path: filepath.Join(userdata, "statev2.sqlite"), From: FromDefault}, home)
			if (err != nil) != c.wantErr {
				t.Fatalf("ClaudeHomes error %v, want an error: %v", err, c.wantErr)
			}
			if err != nil && strings.Contains(err.Error(), dir) {
				t.Errorf("the error %q names the settings' path", err)
			}
			if !slices.Equal(got, c.want) {
				t.Errorf("ClaudeHomes = %q, want %q", got, c.want)
			}
		})
	}
	t.Run("settings that don't open are an error that names no path", func(t *testing.T) {
		if os.Geteuid() == 0 {
			t.Skip("root opens a file whatever its mode")
		}
		dir := t.TempDir()
		userdata := filepath.Join(dir, "userdata")
		if err := os.MkdirAll(userdata, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(userdata, settingsName), []byte(`{}`), 0o000); err != nil {
			t.Fatal(err)
		}
		_, err := ClaudeHomes(Store{Path: filepath.Join(userdata, "statev2.sqlite"), From: FromDefault}, home)
		if err == nil || strings.Contains(err.Error(), dir) {
			t.Errorf("ClaudeHomes error %v, want one that names no path", err)
		}
	})
}
