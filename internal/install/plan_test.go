// SPDX-License-Identifier: AGPL-3.0-or-later

package install

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// linuxItems returns the autostart and Claude Code items for env, with
// settings.json holding before.
func linuxItems(t *testing.T, env Env, before []byte) []Item {
	t.Helper()
	writeFile(t, ClaudeSettings(env), before)
	auto, err := Autostart(env)
	if err != nil {
		t.Fatal(err)
	}
	claude, err := ClaudeCode(env)
	if err != nil {
		t.Fatal(err)
	}
	return []Item{auto, claude}
}

func TestApplyRefusesAStalePlan(t *testing.T) {
	tests := []struct {
		name string
		// between runs between planning and Apply.
		between func(t *testing.T, env Env, in *Installer, items []Item)
	}{
		{
			name: "a target that changed after planning stops Apply before it writes anything",
			between: func(t *testing.T, env Env, _ *Installer, _ []Item) {
				writeFile(t, ClaudeSettings(env), []byte("{\"model\": \"haiku\"}\n"))
			},
		},
		{
			name: "a manifest that changed after planning stops Apply before it writes anything",
			between: func(t *testing.T, _ Env, in *Installer, items []Item) {
				other, err := in.PlanInstall(items[:1])
				if err != nil {
					t.Fatal(err)
				}
				if err := other.Apply(); err != nil {
					t.Fatal(err)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := testEnv(t, "linux", nil)
			items := linuxItems(t, env, []byte("{}\n"))
			in := newInstaller(t)
			p, err := in.PlanInstall(items)
			if err != nil {
				t.Fatal(err)
			}
			tt.between(t, env, in, items)
			settings := readOrNil(t, ClaudeSettings(env))
			if err := p.Apply(); !errors.Is(err, ErrStale) {
				t.Fatalf("Apply = %v, want ErrStale", err)
			}
			if got := readOrNil(t, ClaudeSettings(env)); !bytes.Equal(got, settings) {
				t.Errorf("Apply wrote settings.json:\n%s", got)
			}
		})
	}
}

// errStopped is the error a test's Plan.stop returns to stop Apply.
var errStopped = errors.New("stopped by the test")

func TestRerunMakesFoldersThatAreGone(t *testing.T) {
	settings := []byte("{}\n")
	tests := []struct {
		name string
		// damage leaves the state the rerun starts from.
		damage func(t *testing.T, env Env, in *Installer, items []Item)
		// want is what settings.json holds after uninstall; nil for no
		// file and no ~/.claude.
		want []byte
	}{
		{
			name: "an Apply that stopped after writing the manifest, before it made the entry's folders, is finished by a rerun",
			damage: func(t *testing.T, _ Env, in *Installer, items []Item) {
				p, err := in.PlanInstall(items)
				if err != nil {
					t.Fatal(err)
				}
				p.stop = func(Target) error { return errStopped }
				if err := p.Apply(); !errors.Is(err, errStopped) {
					t.Fatalf("Apply = %v, want it stopped by the test", err)
				}
				if _, err := os.Stat(in.manifestPath()); err != nil {
					t.Fatalf("the stopped Apply wrote no manifest: %v", err)
				}
			},
			want: settings,
		},
		{
			name: "a ~/.claude the teen deleted after install is made again by a reinstall, and uninstall takes it away",
			damage: func(t *testing.T, env Env, in *Installer, items []Item) {
				install(t, in, items...)
				if err := os.RemoveAll(filepath.Dir(ClaudeSettings(env))); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "an uninstall that stopped after removing the entry's folders, before it wrote the manifest, is followed by a working reinstall",
			damage: func(t *testing.T, env Env, in *Installer, items []Item) {
				install(t, in, items...)
				autostart := filepath.Join(env.Home, ".config", "autostart")
				for _, p := range []string{filepath.Join(autostart, DesktopFile), autostart, filepath.Dir(autostart)} {
					if err := os.Remove(p); err != nil {
						t.Fatal(err)
					}
				}
			},
			want: settings,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := testEnv(t, "linux", nil)
			items := linuxItems(t, env, settings)
			desktop := filepath.Join(env.Home, ".config", "autostart", DesktopFile)
			in := newInstaller(t)
			tt.damage(t, env, in, items)

			install(t, in, items...)
			if got := readOrNil(t, desktop); !bytes.Equal(got, autostartGolden(t, DesktopFile)) {
				t.Errorf("after the rerun the autostart entry holds\n%s", got)
			}
			if got := readOrNil(t, ClaudeSettings(env)); !bytes.Contains(got, []byte("--harness")) {
				t.Errorf("after the rerun settings.json has no pairtime hooks:\n%s", got)
			}

			uninstall(t, in)
			checkGone(t, filepath.Join(env.Home, ".config"), "the folder install made for the autostart entry")
			if tt.want == nil {
				checkGone(t, filepath.Dir(ClaudeSettings(env)), "the folder the reinstall made for settings.json")
			} else if got := readOrNil(t, ClaudeSettings(env)); !bytes.Equal(got, tt.want) {
				t.Errorf("after uninstall settings.json holds\n%s\nwant\n%s", got, tt.want)
			}
			checkGone(t, in.Dir, "pairtime's install directory")
		})
	}
}

func TestPlanInstallRefusesBadItems(t *testing.T) {
	env := testEnv(t, "linux", nil)
	auto, err := Autostart(env)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name  string
		items []Item
	}{
		{"an item without an ID is refused", []Item{{Summary: "x", Edits: auto.Edits}}},
		{"an item listed twice is refused", []Item{auto, auto}},
		{"an edit uninstall can't undo is refused", []Item{{ID: "odd", Edits: []Edit{oddEdit{}}}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := newInstaller(t)
			if _, err := in.PlanInstall(tt.items); err == nil {
				t.Error("PlanInstall accepted the items")
			}
		})
	}
}

// oddEdit is an edit of a kind uninstall has no undo for.
type oddEdit struct{}

func (oddEdit) Target() Target                     { return File(filepath.Join(os.TempDir(), "odd")) }
func (oddEdit) Kind() string                       { return "odd" }
func (oddEdit) Apply(cur Content) (Content, error) { return cur, nil }

func TestPlanDiff(t *testing.T) {
	tests := []struct {
		name string
		// wantInstall and wantUninstall are pieces each diff must hold, with
		// DESKTOP and SETTINGS for the two files' paths.
		wantInstall, wantUninstall []string
	}{
		{
			name: "the dry run shows a new file from /dev/null and a merge with its context, and uninstall the reverse",
			wantInstall: []string{
				"--- /dev/null\n+++ DESKTOP\n@@ -0,0 +1,7 @@\n+[Desktop Entry]\n+Type=Application\n",
				"--- SETTINGS\n+++ SETTINGS\n@@ -1 +1,175 @@\n-{}\n+{\n+  \"hooks\": {\n",
			},
			wantUninstall: []string{
				"--- DESKTOP\n+++ /dev/null\n@@ -1,7 +0,0 @@\n-[Desktop Entry]\n",
				"--- SETTINGS\n+++ SETTINGS\n@@ -1,175 +1 @@\n-{\n-  \"hooks\": {\n",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := testEnv(t, "linux", nil)
			items := linuxItems(t, env, []byte("{}\n"))
			desktop := filepath.Join(env.Home, ".config", "autostart", "pairtime.desktop")
			names := strings.NewReplacer("DESKTOP", desktop, "SETTINGS", ClaudeSettings(env))
			in := newInstaller(t)
			p, err := in.PlanInstall(items)
			if err != nil {
				t.Fatal(err)
			}
			d := p.Diff()
			for _, w := range tt.wantInstall {
				if w = names.Replace(w); !strings.Contains(d, w) {
					t.Errorf("the install diff\n%s\ndoesn't hold\n%s", d, w)
				}
			}
			if !strings.HasPrefix(d, names.Replace("--- /dev/null\n+++ DESKTOP\n")) {
				t.Errorf("the install diff doesn't start with the first item's file:\n%s", d)
			}
			if err := p.Apply(); err != nil {
				t.Fatal(err)
			}
			u, err := in.PlanUninstall()
			if err != nil {
				t.Fatal(err)
			}
			d = u.Diff()
			for _, w := range tt.wantUninstall {
				if w = names.Replace(w); !strings.Contains(d, w) {
					t.Errorf("the uninstall diff\n%s\ndoesn't hold\n%s", d, w)
				}
			}
		})
	}
}

func TestUninstallWorksAroundATargetItCantUndo(t *testing.T) {
	tests := []struct {
		name   string
		damage func(t *testing.T, env Env, in *Installer)
		want   string // in the problem
	}{
		{
			name: "a settings file that stopped being JSON stays, and the autostart entry still goes",
			damage: func(t *testing.T, env Env, _ *Installer) {
				writeFile(t, ClaudeSettings(env), []byte("{ oops"))
			},
			want: "isn't valid JSON",
		},
		{
			name: "a backup that changed stops its own target's undo, and the autostart entry still goes",
			damage: func(t *testing.T, _ Env, in *Installer) {
				backups, err := filepath.Glob(filepath.Join(in.backupsDir(), "*"))
				if err != nil || len(backups) != 1 {
					t.Fatalf("want one backup, found %q (%v)", backups, err)
				}
				writeFile(t, backups[0], []byte("{\"tampered\": true}\n"))
			},
			want: "has changed",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := testEnv(t, "linux", nil)
			items := linuxItems(t, env, []byte("{\"model\": \"opus\"}\n"))
			in := newInstaller(t)
			install(t, in, items...)
			tt.damage(t, env, in)
			settings := readOrNil(t, ClaudeSettings(env))

			u, err := in.PlanUninstall()
			if err != nil {
				t.Fatal(err)
			}
			if len(u.Problems) != 1 || !strings.Contains(u.Problems[0], tt.want) || !strings.Contains(u.Problems[0], ClaudeSettings(env)) {
				t.Fatalf("uninstall problems are %q, want one about %s saying %q", u.Problems, ClaudeSettings(env), tt.want)
			}
			if err := u.Apply(); err != nil {
				t.Fatal(err)
			}
			checkGone(t, filepath.Join(env.Home, ".config"), "the autostart entry's folder")
			if got := readOrNil(t, ClaudeSettings(env)); !bytes.Equal(got, settings) {
				t.Errorf("uninstall changed the file it couldn't undo:\n%s", got)
			}
			again, err := in.PlanUninstall()
			if err != nil {
				t.Fatal(err)
			}
			if len(again.Problems) != 1 {
				t.Errorf("the manifest no longer records the settings file: problems %q", again.Problems)
			}
		})
	}
}

func TestDamagedManifest(t *testing.T) {
	tests := []struct {
		name     string
		manifest string
		want     string
	}{
		{"a manifest that isn't JSON stops install and uninstall", "{ not json", "damaged"},
		{"a manifest from a newer pairtime stops install and uninstall", "{\"v\": 99, \"targets\": []}\n", "format 99"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := testEnv(t, "linux", nil)
			in := newInstaller(t)
			writeFile(t, in.manifestPath(), []byte(tt.manifest))
			item, err := Autostart(env)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := in.PlanInstall([]Item{item}); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("PlanInstall = %v, want an error saying %q", err, tt.want)
			}
			if _, err := in.PlanUninstall(); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("PlanUninstall = %v, want an error saying %q", err, tt.want)
			}
		})
	}
}

func TestNothingToDo(t *testing.T) {
	tests := []struct {
		name string
		plan func(in *Installer) (*Plan, error)
	}{
		{"uninstall with nothing installed plans nothing and writes nothing", func(in *Installer) (*Plan, error) { return in.PlanUninstall() }},
		{"install with no items plans nothing and writes nothing", func(in *Installer) (*Plan, error) { return in.PlanInstall(nil) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := newInstaller(t)
			p, err := tt.plan(in)
			if err != nil {
				t.Fatal(err)
			}
			if !p.Empty() || p.Diff() != "" || len(p.Problems) > 0 {
				t.Errorf("the plan isn't empty: %+v", p)
			}
			if err := p.Apply(); err != nil {
				t.Fatal(err)
			}
			checkGone(t, in.Dir, "pairtime's install directory")
		})
	}
}

func TestReinstallRecordsEntriesTheManifestForgot(t *testing.T) {
	tests := []struct {
		name string
		// before is settings.json before the first install; nil for none.
		before []byte
		item   func(env Env) (Item, error)
		path   func(env Env) string
		// want is what uninstall leaves at path; nil for nothing.
		want []byte
		// newExe is the reinstall's binary path, when it moved; "" when
		// the reinstall finds everything as it wants it.
		newExe string
	}{
		{
			name:   "hooks an install the manifest forgot left are taken out by uninstall after a reinstall",
			before: golden(t, "settings-lf", "before.json"),
			item:   ClaudeCode,
			path:   ClaudeSettings,
			want:   golden(t, "settings-lf", "before.json"),
		},
		{
			name: "an autostart entry an install the manifest forgot left is removed by uninstall after a reinstall",
			item: Autostart,
			path: func(env Env) string { return filepath.Join(env.Home, ".config", "autostart", "pairtime.desktop") },
		},
		{
			name:   "an autostart entry for another binary path that an install the manifest forgot left is removed by uninstall after a reinstall",
			item:   Autostart,
			path:   func(env Env) string { return filepath.Join(env.Home, ".config", "autostart", "pairtime.desktop") },
			newExe: "/home/teen/bin/pairtime",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := testEnv(t, "linux", nil)
			if tt.before != nil {
				writeFile(t, ClaudeSettings(env), tt.before)
			}
			item, err := tt.item(env)
			if err != nil {
				t.Fatal(err)
			}
			install(t, newInstaller(t), item)

			// pairtime's data directory is gone: a new installer has no
			// manifest. The reinstall has nothing to write, unless the
			// binary moved, but records what it found.
			if tt.newExe != "" {
				env.Exe = tt.newExe
				if item, err = tt.item(env); err != nil {
					t.Fatal(err)
				}
			}
			in := newInstaller(t)
			p, err := in.PlanInstall([]Item{item})
			if err != nil {
				t.Fatal(err)
			}
			if p.Empty() != (tt.newExe == "") {
				t.Errorf("the reinstall plans changes: %v, want %v:\n%s", !p.Empty(), tt.newExe != "", p.Diff())
			}
			if err := p.Apply(); err != nil {
				t.Fatal(err)
			}
			uninstall(t, in)
			if tt.want == nil {
				checkGone(t, tt.path(env), "the entry")
			} else if got := readOrNil(t, tt.path(env)); !bytes.Equal(got, tt.want) {
				t.Errorf("after uninstall the file holds\n%s\nwant\n%s", got, tt.want)
			}
		})
	}
}

// autostartEntry reads env's autostart entry from in, as a file or a value
// in in's registry.
func autostartEntry(t *testing.T, env Env, in *Installer) Content {
	t.Helper()
	item, err := Autostart(env)
	if err != nil {
		t.Fatal(err)
	}
	c, err := in.read(item.Edits[0].Target())
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestUninstallWithoutTheManifest(t *testing.T) {
	before := golden(t, "settings-lf", "before.json")
	tests := []struct {
		name string
		goos string
	}{
		{"after the data directory is gone, uninstall still takes out the hooks and the Run value", "windows"},
		{"after the data directory is gone, uninstall still takes out the hooks and the LaunchAgent", "darwin"},
		{"after the data directory is gone, uninstall still takes out the hooks and the XDG autostart entry", "linux"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := testEnv(t, tt.goos, nil)
			writeFile(t, ClaudeSettings(env), before)
			auto, err := Autostart(env)
			if err != nil {
				t.Fatal(err)
			}
			claude, err := ClaudeCode(env)
			if err != nil {
				t.Fatal(err)
			}
			items := []Item{auto, claude}
			in := newInstaller(t)
			install(t, in, items...)
			if !autostartEntry(t, env, in).Exists {
				t.Fatal("install wrote no autostart entry")
			}
			// "Delete everything" wipes the data directory, manifest and
			// backups included (DESIGN 8.8).
			if err := os.RemoveAll(filepath.Dir(in.Dir)); err != nil {
				t.Fatal(err)
			}

			p := uninstall(t, in, items...)
			if len(p.Changes) != 2 {
				t.Errorf("the uninstall plan has %d changes, want the hooks and the autostart entry:\n%s", len(p.Changes), p.Diff())
			}
			for _, c := range p.Changes {
				if len(c.Notes) == 0 {
					t.Errorf("the change to %s doesn't say pairtime had no record of it", c.Target)
				}
			}
			if got := readOrNil(t, ClaudeSettings(env)); !bytes.Equal(got, before) {
				t.Errorf("after uninstall settings.json holds\n%s\nwant before.json\n%s", got, before)
			}
			if got := autostartEntry(t, env, in); got.Exists {
				t.Errorf("after uninstall the autostart entry is still there:\n%s", got.Data)
			}
			checkGone(t, in.Dir, "pairtime's install directory")
			again, err := in.PlanUninstall(items...)
			if err != nil {
				t.Fatal(err)
			}
			if !again.Empty() || len(again.Problems) > 0 {
				t.Errorf("a second uninstall plans changes or has problems %q:\n%s", again.Problems, again.Diff())
			}
		})
	}
}

func TestUninstallWithoutTheManifestLeavesWhatIsntPairtimes(t *testing.T) {
	otherHook := "{\n  \"hooks\": {\n    \"Stop\": [\n      {\n        \"hooks\": [\n          {\n            \"type\": \"command\",\n            \"command\": \"say done\"\n          }\n        ]\n      }\n    ]\n  }\n}\n"
	tests := []struct {
		name  string
		goos  string
		entry string // what sits at pairtime's autostart name
		typ   uint32 // its registry type, on Windows
		// settings is settings.json, with no pairtime hook in it.
		settings string
	}{
		{name: "a Run value of the same name that starts another program stays, and so do other tools' hooks", goos: "windows",
			entry: `"C:\Program Files\Other\other.exe" agent`, typ: RegSZ, settings: otherHook},
		{name: "a LaunchAgent file of the same name that starts another program stays, and so does an empty hooks object", goos: "darwin",
			entry:    "<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<plist version=\"1.0\">\n<dict>\n\t<key>ProgramArguments</key>\n\t<array>\n\t\t<string>/usr/local/bin/other</string>\n\t\t<string>agent</string>\n\t</array>\n</dict>\n</plist>\n",
			settings: "{\"hooks\": {}}\n"},
		{name: "a desktop entry of the same name that only hides a system-wide one stays, and so does an empty hook list", goos: "linux",
			entry: "[Desktop Entry]\nHidden=true\n", settings: "{\"model\": \"opus\", \"hooks\": {\"Stop\": []}}\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := testEnv(t, tt.goos, nil)
			writeFile(t, ClaudeSettings(env), []byte(tt.settings))
			in := newInstaller(t)
			auto, err := Autostart(env)
			if err != nil {
				t.Fatal(err)
			}
			claude, err := ClaudeCode(env)
			if err != nil {
				t.Fatal(err)
			}
			if target := auto.Edits[0].Target(); target.isRegistry() {
				in.Registry.Set(target.Key, target.Name, tt.entry, tt.typ)
			} else {
				writeFile(t, target.Path, []byte(tt.entry))
			}
			p := uninstall(t, in, auto, claude)
			if !p.Empty() {
				t.Errorf("uninstall planned changes to what isn't pairtime's:\n%s", p.Diff())
			}
			if got := autostartEntry(t, env, in); !got.Exists || string(got.Data) != tt.entry {
				t.Errorf("after uninstall the entry holds %q, want %q", got.Data, tt.entry)
			}
			if got := readOrNil(t, ClaudeSettings(env)); string(got) != tt.settings {
				t.Errorf("after uninstall settings.json holds\n%s\nwant\n%s", got, tt.settings)
			}
		})
	}
}

func TestAutostartClaimsAnEarlierPairtimesEntry(t *testing.T) {
	plist := func(args ...string) string {
		var b strings.Builder
		b.WriteString("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<!DOCTYPE plist PUBLIC \"-//Apple//DTD PLIST 1.0//EN\" \"http://www.apple.com/DTDs/PropertyList-1.0.dtd\">\n<plist version=\"1.0\">\n<dict>\n\t<key>Label</key>\n\t<string>dev.pairtime.agent</string>\n\t<key>ProgramArguments</key>\n\t<array>\n")
		for _, a := range args {
			b.WriteString("\t\t<string>" + a + "</string>\n")
		}
		b.WriteString("\t</array>\n</dict>\n</plist>\n")
		return b.String()
	}
	tests := []struct {
		name  string
		claim func(Content) bool
		entry Content
		want  bool
	}{
		{"a Run value for pairtime.exe at another path is pairtime's", isAgentRunValue, Content{Exists: true, Type: RegSZ, Data: []byte(`"C:\old\pairtime.exe" agent`)}, true},
		{"a Run value with environment variables in an expandable path is pairtime's", isAgentRunValue, Content{Exists: true, Type: RegExpandSZ, Data: []byte(`"%LOCALAPPDATA%\old\pairtime.exe" agent --detach`)}, true},
		{"an unquoted Run command for PAIRTIME.EXE is pairtime's", isAgentRunValue, Content{Exists: true, Type: RegSZ, Data: []byte(`C:\bin\PAIRTIME.EXE agent`)}, true},
		{"a Run value for another program is not pairtime's", isAgentRunValue, Content{Exists: true, Type: RegSZ, Data: []byte(`"C:\Program Files\Other\other.exe" agent`)}, false},
		{"a Run value that runs pairtime without the agent command is not pairtime's", isAgentRunValue, Content{Exists: true, Type: RegSZ, Data: []byte(`"C:\bin\pairtime.exe" dashboard`)}, false},
		{"a Run value with an unclosed quote is not pairtime's", isAgentRunValue, Content{Exists: true, Type: RegSZ, Data: []byte(`"C:\bin\pairtime.exe agent`)}, false},
		{"a LaunchAgent for pairtime at another path is pairtime's", isAgentLaunchAgent, Content{Exists: true, Data: []byte(plist("/opt/pairtime/bin/pairtime", "agent"))}, true},
		{"a LaunchAgent for another program is not pairtime's", isAgentLaunchAgent, Content{Exists: true, Data: []byte(plist("/usr/local/bin/other", "agent"))}, false},
		{"a property list that isn't XML is not pairtime's", isAgentLaunchAgent, Content{Exists: true, Data: []byte("bplist00\x01")}, false},
		{"a desktop entry for pairtime at another path is pairtime's", isAgentDesktopEntry, Content{Exists: true, Data: []byte("[Desktop Entry]\nType=Application\nExec=/opt/pairtime/bin/pairtime agent\n")}, true},
		{"a desktop entry with a quoted path and escapes is pairtime's", isAgentDesktopEntry, Content{Exists: true, Data: []byte("[Desktop Entry]\nExec = \"/home/te en/a\\\\\\\\b/pairtime\" agent\n")}, true},
		{"a desktop entry the teen turned off is still pairtime's", isAgentDesktopEntry, Content{Exists: true, Data: []byte("[Desktop Entry]\nExec=/home/teen/.local/bin/pairtime agent\nHidden=true\n")}, true},
		{"a desktop entry whose only Exec is in an action group is not pairtime's", isAgentDesktopEntry, Content{Exists: true, Data: []byte("[Desktop Entry]\nName=x\n[Desktop Action run]\nExec=/opt/pairtime/bin/pairtime agent\n")}, false},
		{"a desktop entry that only hides a system-wide one is not pairtime's", isAgentDesktopEntry, Content{Exists: true, Data: []byte("[Desktop Entry]\nHidden=true\n")}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.claim(tt.entry); got != tt.want {
				t.Errorf("claim(%q) = %v, want %v", tt.entry.Data, got, tt.want)
			}
		})
	}
}

func TestExecArgsUndoesExecArg(t *testing.T) {
	tests := []struct {
		name string
		exe  string
	}{
		{"a plain path comes back as it is", "/home/teen/.local/bin/pairtime"},
		{"a path with a space comes back from its quotes", "/home/te en/pairtime"},
		{"a backslash comes back from its four", `/home/a\b/pairtime`},
		{"a dollar sign, a backtick and a double quote come back", "/home/$x/`y\"z/pairtime"},
		{"a percent sign comes back from its two", "/home/100%/pairtime"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			arg, err := execArg(tt.exe)
			if err != nil {
				t.Fatal(err)
			}
			if got := execArgs(arg + " agent"); !slices.Equal(got, []string{tt.exe, "agent"}) {
				t.Errorf("execArgs(%q) = %q, want [%q agent]", arg+" agent", got, tt.exe)
			}
		})
	}
}
