// SPDX-License-Identifier: AGPL-3.0-or-later

package consent

import (
	"encoding/base64"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/agent/config"
	"github.com/some-du6e/pairtime/internal/install"
)

// teenExe is the binary path the snapshots plan for: Linux's per-user path
// in a home directory called /home/teen.
const teenExe = "/home/teen/.local/bin/pairtime"

// screenFor plans what install offers in a home directory holding the
// folders found, the way runInstall does, and returns the screen and the
// home directory. It plans for Linux on every OS, so the snapshots are the
// same everywhere once the home directory's path is written as /home/teen.
// With readOnly, Claude Code's settings.json is there and read-only, so
// its planner refuses it, and the item is left out with the planner's
// reason, as runInstall leaves it. With a calibration other than "", which
// config.toml then holds, the screen also offers --calibration-volunteer's
// change, planned and masked as runInstall plans it.
func screenFor(t *testing.T, found []string, hook HookTest, readOnly bool, calibration string) (*Screen, string) {
	t.Helper()
	root := t.TempDir()
	for _, f := range found {
		if err := os.Mkdir(filepath.Join(root, f), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if readOnly {
		if err := os.WriteFile(filepath.Join(root, ".claude", "settings.json"), []byte("{}\n"), 0o400); err != nil {
			t.Fatal(err)
		}
	}
	env := install.Env{GOOS: "linux", Home: root, Getenv: func(string) string { return "" }, Exe: teenExe}
	in := &install.Installer{Dir: filepath.Join(root, "data", "install")}
	harnesses := install.Detect(env)
	diffs := map[string]string{}
	left := map[string]string{}
	plan := func(it install.Item, err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		p, err := in.PlanInstall([]install.Item{it})
		if err != nil {
			left[it.ID] = err.Error()
			return
		}
		defer p.Close()
		diffs[it.ID] = p.Diff()
	}
	plan(install.Autostart(env))
	if harnesses[0].Found() && hook.OK {
		plan(install.ClaudeCode(env))
	}
	if calibration != "" {
		name := filepath.Join(root, ".config", "pairtime", "config.toml")
		if err := os.MkdirAll(filepath.Dir(name), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(name, []byte(calibration), 0o600); err != nil {
			t.Fatal(err)
		}
		before, after, err := config.PlanShadow(name, true)
		if err != nil {
			t.Fatal(err)
		}
		diffs[ItemCalibration] = install.FileDiff(name, config.Masked(before), config.Masked(after))
	}
	return New(Input{GOOS: "linux", Binary: teenExe, Harnesses: harnesses, Hook: hook, Diffs: diffs, Left: left}), root
}

// teenPaths writes root, the test's home directory, as /home/teen, and
// every path with forward slashes, as Linux has them.
func teenPaths(s, root string) string {
	s = strings.ReplaceAll(s, root, "/home/teen")
	return strings.ReplaceAll(s, `\`, "/")
}

// TestScreenSnapshots checks the whole consent screen for three sets of
// detected harnesses, for a hook command that fails its self-test, for a
// settings file Claude Code's planner refuses, for a computer pairtime
// login linked, which config.toml lets upload, for one whose config.toml
// says online though no login linked it, for one a login linked whose
// record of the link doesn't read, and for a calibration volunteer,
// against testdata/*.golden (docs/PLAN.md, WP-IN2). A screen that differs
// is printed in base64, so a new snapshot can be read out of the CI log
// byte for byte, looked over and committed.
func TestScreenSnapshots(t *testing.T) {
	passed := HookTest{OK: true, Took: 12 * time.Millisecond}
	tests := []struct {
		name     string
		golden   string
		found    []string
		hook     HookTest
		readOnly bool
		// linked is the server the computer is linked to (Input.Linked).
		linked string
		// calibration is config.toml for --calibration-volunteer, "" when
		// install runs without it.
		calibration string
		// unlinked says config.toml says online while no login linked the
		// computer (Input.Unlinked), and unread that a login linked it but
		// its record doesn't read (Input.Unread).
		unlinked, unread bool
	}{
		{"with no AI tool found, only autostart is offered, and each tool says it wasn't found",
			"none.golden", nil, HookTest{}, false, "", "", false, false},
		{"with Claude Code found, autostart and Claude Code's hooks are offered, each with its diff",
			"claude.golden", []string{".claude"}, passed, false, "", "", false, false},
		{"with Claude Code, Cursor and Codex found, the hooks card says Cursor runs them too, and Codex has no hooks yet",
			"claude-cursor-codex.golden", []string{".claude", ".cursor", ".codex"}, passed, false, "", "", false, false},
		{"with a hook command that fails its self-test, no hooks are offered and the screen says why",
			"hook-failed.golden", []string{".claude"}, HookTest{Why: "pairtime hook failed its self-test (it exited with status 2), so pairtime leaves Claude Code's settings alone"}, false, "", "", false, false},
		{"with a Claude Code settings file its planner won't edit, the hooks are left out and Claude Code's line says why, and autostart is still offered",
			"planner-refused.golden", []string{".claude"}, passed, true, "", "", false, false},
		{"on a computer pairtime login linked, which config.toml lets upload, the screen names the server in place of local-only mode",
			"linked.golden", []string{".claude"}, passed, false, "https://pairtime.example", "", false, false},
		{"for a calibration volunteer, a card between autostart and the hooks says the time reads calibrating until scoring 1.0.0, with config.toml's diff masked",
			"calibration.golden", []string{".claude"}, passed, false, "", "mode = \"local\"\n\n[display]\n# my calibration week\nshadow = false\n", false, false},
		{"on a computer whose config.toml says online but no login linked it, the screen says pairtime uploads nothing until a login, in place of local-only mode",
			"unlinked.golden", []string{".claude"}, passed, false, "", "", true, false},
		{"on a computer whose config.toml says online and that a login linked, but whose record of the link doesn't read, the screen says pairtime uploads nothing until it does, in place of local-only mode",
			"unread.golden", []string{".claude"}, passed, false, "", "", false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, root := screenFor(t, tt.found, tt.hook, tt.readOnly, tt.calibration)
			s.in.Linked, s.in.Unlinked, s.in.Unread = tt.linked, tt.unlinked, tt.unread
			var b strings.Builder
			s.Render(&b)
			got := teenPaths(b.String(), root)
			want, err := os.ReadFile(filepath.Join("testdata", tt.golden))
			if err != nil || got != string(want) {
				t.Errorf("the screen isn't testdata/%s (%v); it is:\n%s\nin base64:\n%s", tt.golden, err, got, base64.StdEncoding.EncodeToString([]byte(got)))
			}
		})
	}
}

func TestCards(t *testing.T) {
	tests := []struct {
		name  string
		found []string
		check func(t *testing.T, s *Screen, shown string)
	}{
		{"every card says that saying no doesn't stop you from using pairtime, what saying no changes and how to turn it off", []string{".claude"},
			func(t *testing.T, s *Screen, shown string) {
				if len(s.Cards) != 2 {
					t.Fatalf("got %d cards, want 2", len(s.Cards))
				}
				for _, c := range s.Cards {
					if c.Declining == "" || c.TurnOff == "" || c.What == "" {
						t.Errorf("card %s lacks what it does, what saying no changes, or how to turn it off: %+v", c.ID, c)
					}
				}
				if n := strings.Count(shown, Declined); n != len(s.Cards) {
					t.Errorf("the screen says %q %d times, want once per card", Declined, n)
				}
			}},
		{"every card starts ticked, since the teen ran install for them, and unticks", []string{".claude"},
			func(t *testing.T, s *Screen, _ string) {
				if got := s.Ticked(); !slices.Equal(got, []string{install.ItemAutostart, install.ItemClaudeCode}) {
					t.Errorf("ticked at first: %q", got)
				}
				if !s.Untick(install.ItemClaudeCode) || s.Untick("hooks/nothing") {
					t.Error("Untick doesn't tell a card that is there from one that isn't")
				}
				if got := s.Ticked(); !slices.Equal(got, []string{install.ItemAutostart}) {
					t.Errorf("ticked after unticking the hooks: %q", got)
				}
			}},
		{"the hooks card carries the self-test's time as its cost line", []string{".claude"},
			func(t *testing.T, s *Screen, shown string) {
				if !strings.Contains(strings.Join(strings.Fields(shown), " "), "a test call just now took 12 ms") {
					t.Errorf("the screen has no cost line:\n%s", shown)
				}
			}},
		{"the diff masks a token in Claude Code's settings", []string{".claude"},
			func(t *testing.T, s *Screen, shown string) {
				if strings.Contains(shown, "sk-ant-secret") || !strings.Contains(shown, `"ANTHROPIC_AUTH_TOKEN": "***"`) {
					t.Errorf("the screen shows the token, or doesn't show it masked:\n%s", shown)
				}
			}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			for _, f := range tt.found {
				if err := os.Mkdir(filepath.Join(root, f), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			settings := filepath.Join(root, ".claude", "settings.json")
			if err := os.WriteFile(settings, []byte("{\n  \"env\": {\n    \"ANTHROPIC_AUTH_TOKEN\": \"sk-ant-secret\"\n  }\n}\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			env := install.Env{GOOS: "linux", Home: root, Getenv: func(string) string { return "" }, Exe: teenExe}
			in := &install.Installer{Dir: filepath.Join(root, "data", "install")}
			diffs := map[string]string{}
			for _, mk := range []func(install.Env) (install.Item, error){install.Autostart, install.ClaudeCode} {
				it, err := mk(env)
				if err != nil {
					t.Fatal(err)
				}
				p, err := in.PlanInstall([]install.Item{it})
				if err != nil {
					t.Fatal(err)
				}
				diffs[it.ID] = p.Diff()
				p.Close()
			}
			s := New(Input{GOOS: "linux", Binary: teenExe, Harnesses: install.Detect(env), Hook: HookTest{OK: true, Took: 12 * time.Millisecond}, Diffs: diffs})
			var b strings.Builder
			s.Render(&b)
			tt.check(t, s, b.String())
		})
	}
}

// TestCalibrationCard checks the card of pairtime install
// --calibration-volunteer and the line that says why it isn't there.
func TestCalibrationCard(t *testing.T) {
	diffs := map[string]string{install.ItemAutostart: "--- /dev/null\n+++ a\n", ItemCalibration: "--- c\n+++ c\n", install.ItemClaudeCode: "--- b\n+++ b\n"}
	tests := []struct {
		name  string
		in    Input
		check func(t *testing.T, s *Screen, shown string)
	}{
		{"the card comes between autostart and the hooks, the order install makes the changes in", Input{Diffs: diffs},
			func(t *testing.T, s *Screen, _ string) {
				if got := s.Ticked(); !slices.Equal(got, []string{install.ItemAutostart, ItemCalibration, install.ItemClaudeCode}) {
					t.Errorf("the cards are %q", got)
				}
			}},
		{"the card says your time reads calibrating until scoring 1.0.0, and how to turn it off", Input{Diffs: map[string]string{ItemCalibration: diffs[ItemCalibration]}},
			func(t *testing.T, s *Screen, shown string) {
				said := strings.Join(strings.Fields(shown), " ")
				for _, want := range []string{`show your time as "calibrating"`, "until its scoring reaches version 1.0.0", Declined, "shadow = false under [display] in config.toml"} {
					if !strings.Contains(said, want) {
						t.Errorf("the screen doesn't say %q:\n%s", want, shown)
					}
				}
			}},
		{"a card whose change is made already says nothing changes", Input{Diffs: map[string]string{ItemCalibration: ""}},
			func(t *testing.T, s *Screen, shown string) {
				if !strings.Contains(shown, "It is set up already, so nothing changes.") {
					t.Errorf("the screen doesn't say the change is made already:\n%s", shown)
				}
			}},
		{"when install leaves the change out, there is no card and the screen says why", Input{Left: map[string]string{ItemCalibration: "pairtime's scoring has reached version 1.0.0"}},
			func(t *testing.T, s *Screen, shown string) {
				said := strings.Join(strings.Fields(shown), " ")
				if len(s.Cards) != 0 || !strings.Contains(said, `Your time won't show as "calibrating": pairtime's scoring has reached version 1.0.0.`) {
					t.Errorf("the screen has %d cards and says:\n%s", len(s.Cards), shown)
				}
			}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.in.GOOS, tt.in.Binary, tt.in.Hook = "linux", teenExe, HookTest{OK: true}
			s := New(tt.in)
			var b strings.Builder
			s.Render(&b)
			tt.check(t, s, b.String())
		})
	}
}

func TestAsk(t *testing.T) {
	tests := []struct {
		name    string
		answers string
		want    bool
		ticked  []string
		says    string // what the output must hold, beside the screen
		broken  bool   // writing the output fails
	}{
		{"y installs every ticked item", "y\n", true, []string{install.ItemAutostart, install.ItemClaudeCode}, "", false},
		{"a number unticks its item, and y installs the rest", "2\ny\n", true, []string{install.ItemAutostart}, "[ ] 2. Add pairtime's hooks to Claude Code", false},
		{"a number twice ticks the item again", "2\n2\nyes\n", true, []string{install.ItemAutostart, install.ItemClaudeCode}, "", false},
		{"unticking everything and saying y installs nothing", "1\n2\nY\n", true, nil, "", false},
		{"n changes nothing", "n\n", false, []string{install.ItemAutostart, install.ItemClaudeCode}, "", false},
		{"the end of input is no", "", false, []string{install.ItemAutostart, install.ItemClaudeCode}, "", false},
		{"an empty line asks again", "\n\ny\n", true, []string{install.ItemAutostart, install.ItemClaudeCode}, "", false},
		{"an answer that is neither asks again and says what to type", "sure\n3\ny\n", true, []string{install.ItemAutostart, install.ItemClaudeCode}, "Type y, n, or a number from 1 to 2.", false},
		{"a y to a screen that couldn't be written is no", "y\n", false, []string{install.ItemAutostart, install.ItemClaudeCode}, "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := New(Input{GOOS: "linux", Binary: teenExe, Hook: HookTest{OK: true},
				Diffs: map[string]string{install.ItemAutostart: "--- /dev/null\n+++ a\n", install.ItemClaudeCode: "--- b\n+++ b\n"}})
			var out strings.Builder
			var w io.Writer = &out
			if tt.broken {
				w = brokenWriter{}
			}
			got := s.Ask(strings.NewReader(tt.answers), w)
			if got != tt.want || !slices.Equal(s.Ticked(), tt.ticked) {
				t.Errorf("Ask = %v with %q ticked, want %v with %q; output:\n%s", got, s.Ticked(), tt.want, tt.ticked, out.String())
			}
			if tt.says != "" && !strings.Contains(out.String(), tt.says) {
				t.Errorf("the output doesn't say %q:\n%s", tt.says, out.String())
			}
		})
	}
}

// brokenWriter is an output that can't be written, as a terminal that has
// gone away.
type brokenWriter struct{}

func (brokenWriter) Write([]byte) (int, error) { return 0, errors.New("the terminal is gone") }
