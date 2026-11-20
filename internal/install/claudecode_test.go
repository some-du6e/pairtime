// SPDX-License-Identifier: AGPL-3.0-or-later

package install

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/some-du6e/pairtime/internal/install/jsonc"
)

// golden reads testdata/claudecode/<dir>/<name>, or returns nil when there
// is no such file.
func golden(t *testing.T, dir, name string) []byte {
	t.Helper()
	return readOrNil(t, filepath.Join("testdata", "claudecode", dir, name))
}

// checkLineEndings stops the test when a golden file doesn't have the line
// endings its directory's name says, which a checkout that converts line
// endings would cause.
func checkLineEndings(t *testing.T, dir string, files ...[]byte) {
	t.Helper()
	crlf := strings.Contains(dir, "crlf")
	for _, f := range files {
		if f != nil && (crlf != bytes.Contains(f, []byte("\r\n")) || (!crlf && bytes.Contains(f, []byte("\r")))) {
			t.Fatalf("testdata/claudecode/%s doesn't have the line endings its name says; was it checked out with line-ending conversion?", dir)
		}
	}
}

var bom = []byte("\xef\xbb\xbf")

// strictOrBlank reports whether data is strict JSON after a byte order
// mark, or holds nothing but white space, so that encoding/json can serve
// as an oracle for it.
func strictOrBlank(data []byte) bool {
	data = bytes.TrimPrefix(data, bom)
	return len(bytes.TrimSpace(data)) == 0 || json.Valid(data)
}

// mergedAsDecoded is the merge as encoding/json sees it, written from
// DESIGN 5.1 and not from the code: before without any pairtime hook, plus
// one group per event of pairtime's list, after the groups already there.
func mergedAsDecoded(t *testing.T, before []byte, exe string) any {
	t.Helper()
	root := map[string]any{}
	if len(bytes.TrimSpace(bytes.TrimPrefix(before, bom))) > 0 {
		root = decode(t, before).(map[string]any)
	}
	hooks, _ := root["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
		root["hooks"] = hooks
	}
	for event, list := range hooks {
		kept := []any{}
		for _, g := range list.([]any) {
			if !decodedPairtimeGroup(g) {
				kept = append(kept, g)
			}
		}
		hooks[event] = kept
	}
	for _, hk := range claudeHooks {
		list, _ := hooks[hk.event].([]any)
		hooks[hk.event] = append(list, decodedGroup(hk.event, hk.matcher, exe))
	}
	return root
}

func decodedGroup(event, matcher, exe string) any {
	g := map[string]any{"hooks": []any{map[string]any{
		"type":    "command",
		"command": exe,
		"args":    []any{"hook", "--harness", "claude_code", "--event", event},
	}}}
	if matcher != "" {
		g["matcher"] = matcher
	}
	return g
}

// decodedPairtimeGroup reports whether g, a decoded hook group, runs only
// a pairtime binary.
func decodedPairtimeGroup(g any) bool {
	hs, _ := g.(map[string]any)["hooks"].([]any)
	for _, h := range hs {
		cmd, _ := h.(map[string]any)["command"].(string)
		if !strings.HasSuffix(cmd, "/pairtime") && !strings.HasSuffix(cmd, `\pairtime.exe`) {
			return false
		}
	}
	return len(hs) > 0
}

// withoutPairtime is a decoded settings file without pairtime's hook
// groups, and without the lists that held only those and that before, the
// decoded file before install, doesn't have.
func withoutPairtime(settings, before any) any {
	root := settings.(map[string]any)
	hooks, _ := root["hooks"].(map[string]any)
	beforeHooks, _ := before.(map[string]any)["hooks"].(map[string]any)
	for event, list := range hooks {
		kept := []any{}
		for _, g := range list.([]any) {
			if !decodedPairtimeGroup(g) {
				kept = append(kept, g)
			}
		}
		if _, had := beforeHooks[event]; len(kept) == 0 && !had {
			delete(hooks, event)
		} else {
			hooks[event] = kept
		}
	}
	return root
}

func TestClaudeCodeGoldens(t *testing.T) {
	tests := []struct {
		name string
		dir  string // under testdata/claudecode
		goos string // the OS planned for, whose binary path after.json holds
	}{
		{"hooks go after git-ai's and other tools' entries in an LF file", "settings-lf", "linux"},
		{"a CRLF file keeps CRLF, in its own lines and in pairtime's", "settings-crlf", "linux"},
		{"comments, trailing commas and tabs stay as they were", "jsonc", "linux"},
		{"a one-line file stays on one line", "compact", "linux"},
		{"a four-space indent is used for pairtime's lines too", "four-spaces", "linux"},
		{"a byte order mark and tabs stay, and another tool's matcher group isn't joined", "tabs-bom", "linux"},
		{"a missing file is created, and uninstall deletes it", "new-file", "linux"},
		{"an empty object gets the hooks, and uninstall gives {} back", "empty-object", "linux"},
		{"a blank file gets the hooks after its blank line", "blank", "linux"},
		{"a file holding only a comment gets the hooks on the next line", "comment-only", "linux"},
		{"a Windows binary path is written with JSON's escaped backslashes", "windows-exe", "windows"},
		{"an older install's hooks are replaced, and uninstall takes out every pairtime hook", "old-install", "linux"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before, want := golden(t, tt.dir, "before.json"), golden(t, tt.dir, "after.json")
			gone := golden(t, tt.dir, "uninstalled.json") // what uninstall leaves, if not before.json
			if gone == nil {
				gone = before
			}
			if want == nil {
				t.Fatalf("testdata/claudecode/%s has no after.json", tt.dir)
			}
			checkLineEndings(t, tt.dir, before, want, gone)
			env := testEnv(t, tt.goos, nil)
			path := ClaudeSettings(env)
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil { // Claude Code is installed
				t.Fatal(err)
			}
			if before != nil {
				writeFile(t, path, before)
			}
			item, err := ClaudeCode(env)
			if err != nil {
				t.Fatal(err)
			}
			in := newInstaller(t)
			p, err := in.PlanInstall([]Item{item})
			if err != nil {
				t.Fatalf("PlanInstall: %v", err)
			}
			if len(p.Changes) != 1 || !bytes.Equal(p.Changes[0].After.Data, want) {
				var got []byte
				if len(p.Changes) > 0 {
					got = p.Changes[0].After.Data
				}
				t.Fatalf("the plan has %d changes; the first would write\n%s\nwant testdata/claudecode/%s/after.json:\n%s", len(p.Changes), got, tt.dir, want)
			}
			if d := p.Diff(); !strings.Contains(d, "+++ "+path+"\n") {
				t.Errorf("the dry-run diff doesn't name %s:\n%s", path, d)
			}
			if err := p.Apply(); err != nil {
				t.Fatalf("Apply: %v", err)
			}
			if got := readOrNil(t, path); !bytes.Equal(got, want) {
				t.Fatalf("after install the file holds\n%s\nwant\n%s", got, want)
			}
			if strictOrBlank(before) {
				if !json.Valid(bytes.TrimPrefix(want, bom)) {
					t.Errorf("before.json is strict JSON and after.json isn't")
				}
				if got, merged := decode(t, want), mergedAsDecoded(t, before, testExe[tt.goos]); !reflect.DeepEqual(got, merged) {
					t.Errorf("encoding/json reads after.json as\n%v\nwant before.json's settings with pairtime's groups added:\n%v", got, merged)
				}
			}

			again, err := in.PlanInstall([]Item{item})
			if err != nil {
				t.Fatalf("second PlanInstall: %v", err)
			}
			if !again.Empty() {
				t.Errorf("a second install plans changes:\n%s", again.Diff())
			}
			again.Close()

			// Taking pairtime's hooks out of after.json, without the backup,
			// gives what uninstall puts back, byte for byte.
			base := Content{Exists: gone != nil, Data: gone}
			undone, err := undoClaudeHooks(Content{Exists: true, Data: want}, base, nil)
			if err != nil {
				t.Fatalf("undoClaudeHooks: %v", err)
			}
			if !undone.Same(base) {
				t.Errorf("taking pairtime's hooks out of after.json gives\n%s\nwant\n%s", undone.Data, gone)
			}

			uninstall(t, in)
			if gone == nil {
				checkGone(t, path, "the settings file pairtime created")
			} else if got := readOrNil(t, path); !bytes.Equal(got, gone) {
				t.Errorf("after uninstall the file holds\n%s\nwant\n%s", got, gone)
			}
			checkGone(t, in.Dir, "pairtime's install directory")
			if _, err := os.Stat(filepath.Dir(path)); err != nil {
				t.Errorf("uninstall took away Claude Code's folder, which pairtime didn't make: %v", err)
			}
		})
	}
}

// otherGroup is a hook group another tool adds.
var otherGroup = jsonc.Obj{{Key: "hooks", Val: []any{jsonc.Obj{{Key: "type", Val: "command"}, {Key: "command", Val: "other-tool --event start"}}}}}

// appendGroup appends group to hooks.<event> in the settings file at path,
// in place, the way a careful tool edits it.
func appendGroup(t *testing.T, path, event string, group any) {
	t.Helper()
	src := readOrNil(t, path)
	root, err := jsonc.Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	list := root.Get("hooks").Get(event)
	if list == nil {
		t.Fatalf("no hooks.%s in %s", event, path)
	}
	writeFile(t, path, jsonc.AppendElement(src, list, group, jsonc.StyleOf(src, root)))
}

func TestClaudeCodeUninstallKeepsChangesMadeAfterInstall(t *testing.T) {
	before := golden(t, "settings-lf", "before.json")
	tests := []struct {
		name   string
		change func(t *testing.T, path string)
		// check checks what uninstall left at path.
		check func(t *testing.T, got []byte, changed []byte)
	}{
		{
			name: "a tool that rewrites the whole file keeps its changes, and uninstall takes out only pairtime's hooks",
			change: func(t *testing.T, path string) {
				m := decode(t, readOrNil(t, path)).(map[string]any)
				pre := m["hooks"].(map[string]any)["PreToolUse"].([]any)
				pre[0].(map[string]any)["hooks"].([]any)[0].(map[string]any)["command"] = "git-ai checkpoint claude --hook-input stdin --v2"
				data, err := json.MarshalIndent(m, "", "    ")
				if err != nil {
					t.Fatal(err)
				}
				writeFile(t, path, append(data, '\n'))
			},
			check: func(t *testing.T, got, changed []byte) {
				if !json.Valid(got) {
					t.Fatalf("uninstall left a file that isn't JSON:\n%s", got)
				}
				want := withoutPairtime(decode(t, changed), decode(t, before))
				if !reflect.DeepEqual(decode(t, got), want) {
					t.Errorf("uninstall left\n%s\nwant the rewritten settings without pairtime's hooks:\n%v", got, want)
				}
			},
		},
		{
			name: "a hook another tool added to a list pairtime made stays, and so does the list",
			change: func(t *testing.T, path string) {
				appendGroup(t, path, "SessionStart", otherGroup)
			},
			check: func(t *testing.T, got, _ []byte) {
				root, err := jsonc.Parse(before)
				if err != nil {
					t.Fatal(err)
				}
				want := jsonc.AddMember(before, root.Get("hooks"), "SessionStart", []any{otherGroup}, jsonc.StyleOf(before, root))
				if !bytes.Equal(got, want) {
					t.Errorf("uninstall left\n%s\nwant\n%s", got, want)
				}
			},
		},
		{
			name: "a pairtime hook the teen gave a timeout is still pairtime's, and goes",
			change: func(t *testing.T, path string) {
				src := readOrNil(t, path)
				root, err := jsonc.Parse(src)
				if err != nil {
					t.Fatal(err)
				}
				h := root.Get("hooks").Get("Stop").Items[1].Value.Get("hooks").Items[0].Value
				writeFile(t, path, jsonc.AddMember(src, h, "timeout", 5, jsonc.StyleOf(src, root)))
			},
			check: func(t *testing.T, got, _ []byte) {
				if !bytes.Equal(got, before) {
					t.Errorf("uninstall left\n%s\nwant before.json\n%s", got, before)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := testEnv(t, "linux", nil)
			path := ClaudeSettings(env)
			writeFile(t, path, before)
			item, err := ClaudeCode(env)
			if err != nil {
				t.Fatal(err)
			}
			in := newInstaller(t)
			install(t, in, item)
			tt.change(t, path)
			changed := readOrNil(t, path)
			p := uninstall(t, in)
			if len(p.Changes) != 1 || len(p.Changes[0].Notes) == 0 {
				t.Errorf("the uninstall plan doesn't say the file changed since install: %+v", p.Changes)
			}
			got := readOrNil(t, path)
			if bytes.Contains(got, []byte("--harness")) {
				t.Errorf("a pairtime hook is left:\n%s", got)
			}
			tt.check(t, got, changed)
			checkGone(t, in.Dir, "pairtime's install directory")
		})
	}
}

func TestClaudeCodeUninstallWhenTheFileIsGoneOrBroken(t *testing.T) {
	tests := []struct {
		name   string
		before string // "" for no file
		change func(t *testing.T, path string)
		// problem is true when uninstall must leave the file and its
		// record alone.
		problem bool
	}{
		{name: "a file the teen deleted after install stays deleted", before: "",
			change: func(t *testing.T, path string) {
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			}},
		{name: "a file that stopped being JSON is left as it is, and kept in the manifest", before: "{}\n",
			change: func(t *testing.T, path string) {
				writeFile(t, path, []byte("{ \"hooks\": "))
			}, problem: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := testEnv(t, "linux", nil)
			path := ClaudeSettings(env)
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if tt.before != "" {
				writeFile(t, path, []byte(tt.before))
			}
			item, err := ClaudeCode(env)
			if err != nil {
				t.Fatal(err)
			}
			in := newInstaller(t)
			install(t, in, item)
			after := readOrNil(t, path)
			tt.change(t, path)
			changed := readOrNil(t, path)
			p, err := in.PlanUninstall()
			if err != nil {
				t.Fatal(err)
			}
			if got := len(p.Problems) > 0; got != tt.problem {
				t.Fatalf("uninstall problems: %q, want problems: %v", p.Problems, tt.problem)
			}
			if err := p.Apply(); err != nil {
				t.Fatal(err)
			}
			if got := readOrNil(t, path); !bytes.Equal(got, changed) {
				t.Errorf("uninstall changed the file to\n%s\nwant it left as\n%s", got, changed)
			}
			if !tt.problem {
				checkGone(t, in.Dir, "pairtime's install directory")
				return
			}
			// Once the file is mended, uninstall finishes the job.
			writeFile(t, path, after)
			uninstall(t, in)
			if got := readOrNil(t, path); !bytes.Equal(got, []byte(tt.before)) {
				t.Errorf("after the second uninstall the file holds\n%s\nwant\n%s", got, tt.before)
			}
			checkGone(t, in.Dir, "pairtime's install directory")
		})
	}
}

// oldHooks returns a settings file with n hooks from an older pairtime
// install, at another binary path, each in a group of its own.
func oldHooks(n int) string {
	var b strings.Builder
	b.WriteString(`{"hooks": {"Stop": [`)
	for i := range n {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, `{"hooks": [{"type": "command", "command": "/opt/pairtime/bin/pairtime", "args": ["hook", "--harness", "claude_code", "--event", "Stop%d"]}]}`, i)
	}
	b.WriteString("]}}\n")
	return b.String()
}

func TestClaudeCodeLeavesFilesItCantMergeAlone(t *testing.T) {
	tests := []struct {
		name    string
		content string
		mode    fs.FileMode
		want    string // in the error
	}{
		{"a file bigger than 4 MiB is left alone", `{"x": "` + strings.Repeat("a", 4<<20) + `"}`, 0o644, "bigger than the 4 MiB"},
		{"a file with more pairtime hooks than pairtime ever writes is left alone", oldHooks(maxRemovals + 1), 0o644, "more pairtime hooks than pairtime ever writes"},
		{"a file that isn't JSON is left alone", `{"model": }`, 0o644, "isn't valid JSON"},
		{"a top level that isn't an object is left alone", `["model"]`, 0o644, "doesn't hold a JSON object"},
		{"hooks that aren't an object are left alone", `{"hooks": ["x"]}`, 0o644, `"hooks" that isn't an object`},
		{"an event whose hooks aren't a list is left alone", `{"hooks": {"Stop": {"hooks": []}}}`, 0o644, `"hooks.Stop" that isn't a list`},
		{"a UTF-16 file is left alone", "\xff\xfe{\x00}\x00", 0o644, "UTF-16"},
		{"a read-only file is left alone", "{}\n", 0o444, "read-only"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := testEnv(t, "linux", nil)
			path := ClaudeSettings(env)
			writeFile(t, path, []byte(tt.content))
			if err := os.Chmod(path, tt.mode); err != nil {
				t.Fatal(err)
			}
			item, err := ClaudeCode(env)
			if err != nil {
				t.Fatal(err)
			}
			in := newInstaller(t)
			_, err = in.PlanInstall([]Item{item})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("PlanInstall = %v, want an error saying %q", err, tt.want)
			}
			if got := readOrNil(t, path); string(got) != tt.content {
				t.Errorf("the file changed to %q", got)
			}
			checkGone(t, in.Dir, "pairtime's install directory")
		})
	}
}

func TestClaudeSettings(t *testing.T) {
	home, moved := t.TempDir(), t.TempDir()
	tests := []struct {
		name string
		vars map[string]string
		want string
	}{
		{"settings.json is in ~/.claude", nil, filepath.Join(home, ".claude", "settings.json")},
		{"CLAUDE_CONFIG_DIR moves it", map[string]string{"CLAUDE_CONFIG_DIR": moved}, filepath.Join(moved, "settings.json")},
		{"a relative CLAUDE_CONFIG_DIR is ignored", map[string]string{"CLAUDE_CONFIG_DIR": "claude"}, filepath.Join(home, ".claude", "settings.json")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := Env{GOOS: "linux", Home: home, Getenv: func(k string) string { return tt.vars[k] }, Exe: testExe["linux"]}
			if got := ClaudeSettings(env); got != tt.want {
				t.Errorf("ClaudeSettings = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestInstallMakesFoldersAndUninstallRemovesThem(t *testing.T) {
	tests := []struct {
		name string
		// made are the folders, under the home directory, that install has
		// to make.
		made []string
		item func(env Env) (Item, error)
		vars func(home string) map[string]string
	}{
		{
			name: "folders made for Claude Code's moved settings go with them",
			made: []string{"custom", filepath.Join("custom", "claude")},
			item: ClaudeCode,
			vars: func(home string) map[string]string {
				return map[string]string{"CLAUDE_CONFIG_DIR": filepath.Join(home, "custom", "claude")}
			},
		},
		{
			name: "folders made for the XDG autostart entry go with it",
			made: []string{".config", filepath.Join(".config", "autostart")},
			item: Autostart,
			vars: func(string) map[string]string { return nil },
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			vars := tt.vars(home)
			env := Env{GOOS: "linux", Home: home, Getenv: func(k string) string { return vars[k] }, Exe: testExe["linux"]}
			item, err := tt.item(env)
			if err != nil {
				t.Fatal(err)
			}
			in := newInstaller(t)
			install(t, in, item)
			for _, d := range tt.made {
				if info, err := os.Stat(filepath.Join(home, d)); err != nil || !info.IsDir() {
					t.Errorf("install didn't make %s: %v", d, err)
				}
			}
			uninstall(t, in)
			for _, d := range tt.made {
				checkGone(t, filepath.Join(home, d), "the folder")
			}
			if _, err := os.Stat(home); err != nil {
				t.Errorf("the home directory went: %v", err)
			}
		})
	}
}

func TestReinstallAfterAnotherToolChangedTheFile(t *testing.T) {
	tests := []struct {
		name   string
		newExe string
	}{
		{"a reinstall with a new binary path replaces the hooks, and uninstall keeps what changed in between", "/home/teen/bin/pairtime"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := golden(t, "settings-lf", "before.json")
			env := testEnv(t, "linux", nil)
			path := ClaudeSettings(env)
			writeFile(t, path, before)
			in := newInstaller(t)
			first, err := ClaudeCode(env)
			if err != nil {
				t.Fatal(err)
			}
			install(t, in, first)
			appendGroup(t, path, "Stop", otherGroup)

			env.Exe = tt.newExe
			second, err := ClaudeCode(env)
			if err != nil {
				t.Fatal(err)
			}
			install(t, in, second)
			got := readOrNil(t, path)
			if bytes.Contains(got, []byte(testExe["linux"])) || !bytes.Contains(got, []byte(tt.newExe)) || !bytes.Contains(got, []byte("other-tool")) {
				t.Fatalf("after the reinstall the file holds\n%s\nwant the new path's hooks and the other tool's, and not the old path's", got)
			}

			uninstall(t, in)
			root, err := jsonc.Parse(before)
			if err != nil {
				t.Fatal(err)
			}
			want := jsonc.AppendElement(before, root.Get("hooks").Get("Stop"), otherGroup, jsonc.StyleOf(before, root))
			if got := readOrNil(t, path); !bytes.Equal(got, want) {
				t.Errorf("after uninstall the file holds\n%s\nwant before.json with the other tool's hook\n%s", got, want)
			}
		})
	}
}

func TestInstallKeepsHowTheFileIsStored(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file modes and symbolic links work differently on Windows")
	}
	before := golden(t, "settings-lf", "before.json")
	after := golden(t, "settings-lf", "after.json")
	tests := []struct {
		name string
		link bool // settings.json links to a file in a dotfiles folder
		mode fs.FileMode
	}{
		{"a settings.json that links to a dotfiles folder stays a link, and the file it points to is edited", true, 0o644},
		{"the file's mode is kept through install and uninstall", false, 0o640},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := testEnv(t, "linux", nil)
			path := ClaudeSettings(env)
			real := path
			if tt.link {
				real = filepath.Join(env.Home, "dotfiles", "claude-settings.json")
			}
			writeFile(t, real, before)
			if err := os.Chmod(real, tt.mode); err != nil {
				t.Fatal(err)
			}
			if tt.link {
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(real, path); err != nil {
					t.Fatal(err)
				}
			}
			item, err := ClaudeCode(env)
			if err != nil {
				t.Fatal(err)
			}
			in := newInstaller(t)
			check := func(when string, want []byte) {
				t.Helper()
				if info, err := os.Lstat(path); err != nil || (info.Mode()&fs.ModeSymlink != 0) != tt.link {
					t.Errorf("%s, settings.json is a link: %v, want %v (%v)", when, err == nil && info.Mode()&fs.ModeSymlink != 0, tt.link, err)
				}
				info, err := os.Stat(real)
				if err != nil {
					t.Fatal(err)
				}
				if info.Mode().Perm() != tt.mode {
					t.Errorf("%s, the file's mode is %v, want %v", when, info.Mode().Perm(), tt.mode)
				}
				if got := readOrNil(t, real); !bytes.Equal(got, want) {
					t.Errorf("%s, the file holds\n%s\nwant\n%s", when, got, want)
				}
			}
			install(t, in, item)
			check("after install", after)
			uninstall(t, in)
			check("after uninstall", before)
		})
	}
}

func TestClaudeCodeKeepsHooksThatShareALineWithAnOldOne(t *testing.T) {
	old := `{"hooks": [{"type": "command", "command": "/opt/pairtime/bin/pairtime", "args": ["hook", "--harness", "claude_code", "--event", "Stop"]}]}`
	other := `{"hooks": [{"type": "command", "command": "say done"}]}`
	tests := []struct {
		name   string
		before string
	}{
		{"another tool's hook after an old pairtime hook on its line, below a comment, stays", "{\n  \"hooks\": {\n    \"Stop\": [ // when Claude stops\n      " + old + ", " + other + "\n    ]\n  }\n}\n"},
		{"another tool's hook before an old pairtime hook on its line, with the closing bracket, stays", "{\n  \"hooks\": {\n    \"Stop\": [ // when Claude stops\n      " + other + ", " + old + "]\n  }\n}\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := testEnv(t, "linux", nil)
			item, err := ClaudeCode(env)
			if err != nil {
				t.Fatal(err)
			}
			got, err := item.Edits[0].Apply(Content{Exists: true, Data: []byte(tt.before), Mode: 0o644})
			if err != nil {
				t.Fatal(err)
			}
			root, err := jsonc.Parse(got.Data)
			if err != nil {
				t.Fatalf("the merged file doesn't parse: %v\n%s", err, got.Data)
			}
			var cmds []string
			for _, g := range root.Get("hooks").Get("Stop").Items {
				for _, h := range g.Value.Get("hooks").Items {
					cmds = append(cmds, h.Value.Get("command").Text)
				}
			}
			if want := []string{"say done", testExe["linux"]}; !slices.Equal(cmds, want) {
				t.Errorf("Stop runs %q, want %q:\n%s", cmds, want, got.Data)
			}
		})
	}
}

// isLink reports whether path is a symbolic link.
func isLink(t *testing.T, path string) bool {
	t.Helper()
	info, err := os.Lstat(path)
	return err == nil && info.Mode()&fs.ModeSymlink != 0
}

// moveAndLink moves path into the dotfiles folder in env's home and puts
// a symbolic link to it where it was, as GNU stow does, and returns where
// it went.
func moveAndLink(t *testing.T, env Env, path string) string {
	t.Helper()
	moved := filepath.Join(env.Home, "dotfiles", filepath.Base(path))
	if err := os.MkdirAll(filepath.Dir(moved), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(path, moved); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(moved, path); err != nil {
		t.Fatal(err)
	}
	return moved
}

func TestUninstallKeepsLinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symbolic links need a privilege or developer mode on Windows")
	}
	tests := []struct {
		name string
		run  func(t *testing.T, env Env, in *Installer)
	}{
		{
			name: "a settings.json pairtime created that the teen linked into dotfiles stays a link, and the file it points to is left empty",
			run: func(t *testing.T, env Env, in *Installer) {
				path := ClaudeSettings(env)
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				item, err := ClaudeCode(env)
				if err != nil {
					t.Fatal(err)
				}
				install(t, in, item)
				moved := moveAndLink(t, env, path)
				uninstall(t, in)
				if !isLink(t, path) {
					t.Errorf("uninstall deleted the link %s", path)
				}
				if got := readOrNil(t, moved); string(got) != "{}\n" {
					t.Errorf("after uninstall the linked file holds\n%s\nwant {} and a newline", got)
				}
			},
		},
		{
			name: "an autostart entry pairtime created that the teen linked into dotfiles is a problem, and stays",
			run: func(t *testing.T, env Env, in *Installer) {
				item, err := Autostart(env)
				if err != nil {
					t.Fatal(err)
				}
				install(t, in, item)
				path := item.Edits[0].Target().Path
				moved := moveAndLink(t, env, path)
				p, err := in.PlanUninstall()
				if err != nil {
					t.Fatal(err)
				}
				if len(p.Problems) != 1 || !strings.Contains(p.Problems[0], "symbolic link") {
					t.Errorf("uninstall problems are %q, want one about the link", p.Problems)
				}
				if err := p.Apply(); err != nil {
					t.Fatal(err)
				}
				if !isLink(t, path) || readOrNil(t, moved) == nil {
					t.Errorf("uninstall took away the link %s or the file it points to", path)
				}
			},
		},
		{
			name: "a folder pairtime made that the teen turned into a link stays at uninstall, with what it points to",
			run: func(t *testing.T, env Env, in *Installer) {
				item, err := ClaudeCode(env)
				if err != nil {
					t.Fatal(err)
				}
				install(t, in, item) // makes ~/.claude
				dir := filepath.Dir(ClaudeSettings(env))
				writeFile(t, filepath.Join(dir, "history.jsonl"), []byte("{}\n"))
				moved := moveAndLink(t, env, dir)
				uninstall(t, in)
				if !isLink(t, dir) {
					t.Errorf("uninstall deleted the link %s", dir)
				}
				if readOrNil(t, filepath.Join(moved, "history.jsonl")) == nil {
					t.Errorf("the teen's file in the linked folder is gone")
				}
			},
		},
		{
			name: "a link to a folder that isn't there, where pairtime would make one, stops the plan",
			run: func(t *testing.T, env Env, in *Installer) {
				dir := filepath.Dir(ClaudeSettings(env))
				if err := os.Symlink(filepath.Join(env.Home, "dotfiles", "gone"), dir); err != nil {
					t.Fatal(err)
				}
				item, err := ClaudeCode(env)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := in.PlanInstall([]Item{item}); err == nil || !strings.Contains(err.Error(), "link to a folder that isn't there") {
					t.Errorf("PlanInstall = %v, want an error about the link", err)
				}
				if !isLink(t, dir) {
					t.Errorf("the link %s is gone", dir)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := testEnv(t, "linux", nil)
			tt.run(t, env, newInstaller(t))
		})
	}
}
