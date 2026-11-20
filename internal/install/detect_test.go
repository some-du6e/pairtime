// SPDX-License-Identifier: AGPL-3.0-or-later

package install

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestDetect(t *testing.T) {
	tests := []struct {
		name string
		// make are folders and files to make in the home directory; a name
		// ending in / is a folder.
		make []string
		// env is the environment, with <home> standing for the home
		// directory.
		env map[string]string
		// found maps each harness found to what showed it, relative to the
		// home directory; every other harness must be not found.
		found map[string]string
	}{
		{name: "nothing in the home directory finds nothing"},
		{name: "Claude Code's folder finds it", make: []string{".claude/"}, found: map[string]string{"claude_code": ".claude"}},
		{name: "Claude Code's ~/.claude.json finds it when its folder isn't there", make: []string{".claude.json"}, found: map[string]string{"claude_code": ".claude.json"}},
		{name: "CLAUDE_CONFIG_DIR moves the folder that finds Claude Code", make: []string{"cc/"}, env: map[string]string{"CLAUDE_CONFIG_DIR": "<home>/cc"}, found: map[string]string{"claude_code": "cc"}},
		{name: "a claude program on the PATH finds Claude Code", make: []string{"bin/" + claudeProgram()}, env: map[string]string{"PATH": "<home>/bin"}, found: map[string]string{"claude_code": filepath.Join("bin", claudeProgram())}},
		{name: "a relative PATH entry finds nothing", make: []string{"bin/" + claudeProgram()}, env: map[string]string{"PATH": "bin"}},
		{name: "Cursor's folder finds Cursor, which reads Claude Code's hooks", make: []string{".cursor/"}, found: map[string]string{"cursor": ".cursor"}},
		{name: "CODEX_HOME moves the folder that finds Codex", make: []string{"codex-home/"}, env: map[string]string{"CODEX_HOME": "<home>/codex-home"}, found: map[string]string{"codex": "codex-home"}},
		{name: "every folder finds its harness", make: []string{".claude/", ".cursor/", ".copilot/", ".codex/", ".gemini/"},
			found: map[string]string{"claude_code": ".claude", "cursor": ".cursor", "copilot": ".copilot", "codex": ".codex", "gemini": ".gemini"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := t.TempDir()
			// From the home directory, a relative PATH entry such as bin
			// names the folder the case plants a program in, so a lookPath
			// that followed relative entries would find it.
			t.Chdir(h)
			for _, m := range tt.make {
				p := filepath.Join(h, filepath.FromSlash(m))
				if m[len(m)-1] == '/' {
					if err := os.MkdirAll(p, 0o700); err != nil {
						t.Fatal(err)
					}
					continue
				}
				if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(p, []byte("x"), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			env := Env{GOOS: runtime.GOOS, Home: h, Getenv: func(k string) string {
				v, ok := tt.env[k]
				if !ok {
					return ""
				}
				if len(v) >= 6 && v[:6] == "<home>" {
					return filepath.Join(h, filepath.FromSlash(v[6:]))
				}
				return v
			}}
			got := Detect(env)
			if len(got) != 5 || got[0].Name != "claude_code" || got[0].Item != ItemClaudeCode {
				t.Fatalf("Detect returned %+v, want five harnesses, Claude Code first with its hooks item", got)
			}
			for _, hs := range got {
				want, ok := tt.found[hs.Name]
				switch {
				case ok && hs.Where != filepath.Join(h, want):
					t.Errorf("%s was found at %q, want %q", hs.Title, hs.Where, filepath.Join(h, want))
				case !ok && hs.Found():
					t.Errorf("%s was found at %q, want not found", hs.Title, hs.Where)
				}
				if hs.ReadsClaudeHooks != (hs.Name == "cursor") {
					t.Errorf("%s reads Claude Code's hooks: %v", hs.Title, hs.ReadsClaudeHooks)
				}
			}
		})
	}
}

// claudeProgram is Claude Code's program's file name on this OS.
func claudeProgram() string {
	if runtime.GOOS == "windows" {
		return "claude.exe"
	}
	return "claude"
}

func TestBinaryPath(t *testing.T) {
	home := t.TempDir()
	tests := []struct {
		name    string
		goos    string
		env     map[string]string
		want    string
		wantErr bool
	}{
		{name: "on Windows the binary goes in LOCALAPPDATA's pairtime folder", goos: "windows", env: map[string]string{"LOCALAPPDATA": `C:\Users\teen\AppData\Local`}, want: filepath.Join(`C:\Users\teen\AppData\Local`, "pairtime", "bin", "pairtime.exe")},
		{name: "on Windows without LOCALAPPDATA there is nowhere to put it", goos: "windows", wantErr: true},
		{name: "on macOS the binary goes in ~/.local/bin", goos: "darwin", want: filepath.Join(home, ".local", "bin", "pairtime")},
		{name: "on Linux the binary goes in ~/.local/bin", goos: "linux", want: filepath.Join(home, ".local", "bin", "pairtime")},
		{name: "another OS has no place for it", goos: "plan9", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := BinaryPath(Env{GOOS: tt.goos, Home: home, Getenv: func(k string) string { return tt.env[k] }})
			if (err != nil) != tt.wantErr || got != tt.want {
				t.Errorf("BinaryPath = %q, %v; want %q, error %v", got, err, tt.want, tt.wantErr)
			}
		})
	}
}

func TestPrintable(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"a plain path stays as it is", "/home/teen/.claude", "/home/teen/.claude"},
		{"a line break is written out, so the path stays on its line", "/home/teen/a\nb", "/home/teen/a<U+000A>b"},
		{"a carriage return and a tab are written out too", "/home/teen/a\r\tb", "/home/teen/a<U+000D><U+0009>b"},
		{"an escape character is written out as a diff writes it", "/home/teen/\x1b[2Jx", "/home/teen/<U+001B>[2Jx"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Printable(tt.in); got != tt.want {
				t.Errorf("Printable(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
