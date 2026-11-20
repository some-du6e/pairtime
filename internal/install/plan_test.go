// SPDX-License-Identifier: AGPL-3.0-or-later

package install

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
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
		break_ func(t *testing.T, env Env, in *Installer)
		want   string // in the problem
	}{
		{
			name: "a settings file that stopped being JSON stays, and the autostart entry still goes",
			break_: func(t *testing.T, env Env, _ *Installer) {
				writeFile(t, ClaudeSettings(env), []byte("{ oops"))
			},
			want: "isn't valid JSON",
		},
		{
			name: "a backup that changed stops its own target's undo, and the autostart entry still goes",
			break_: func(t *testing.T, _ Env, in *Installer) {
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
			tt.break_(t, env, in)
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
