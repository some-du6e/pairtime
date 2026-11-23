// SPDX-License-Identifier: AGPL-3.0-or-later

package config

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/some-du6e/pairtime/pkg/score"
)

// atScoring makes the scoring version this build runs v until the test
// ends, as a build of pkg/score at version v would have it.
func atScoring(t *testing.T, v string) {
	t.Helper()
	old := scoringVersion
	scoringVersion = func() string { return v }
	t.Cleanup(func() { scoringVersion = old })
}

// TestScoringFreeze checks that the scoring freeze ends shadow mode without
// a hand edit (DESIGN 6.7 step 4; docs/PLAN.md, WP-IN2): Parse, Load and
// LoadParsed read display.shadow as the file says while the scoring
// version is below 1.0.0, and as off from 1.0.0 on.
func TestScoringFreeze(t *testing.T) {
	const on, off = "[display]\nshadow = true\n", "[display]\nshadow = false\n"
	tests := []struct {
		name        string
		version     string
		file        string
		calibrating bool // what Calibrating reports
		shadow      bool // display.shadow as Parse reads it
	}{
		{"at 0.4.0, before the freeze, shadow = true keeps your time calibrating", "0.4.0", on, true, true},
		{"at 0.99.9, still before the freeze, shadow = true keeps your time calibrating", "0.99.9", on, true, true},
		{"at 1.0.0, the freeze, shadow = true is read as off", "1.0.0", on, false, false},
		{"at 1.2.3, after the freeze, shadow = true is read as off", "1.2.3", on, false, false},
		{"at 10.0.0 a MAJOR of two digits is after the freeze too", "10.0.0", on, false, false},
		{"a MAJOR written with a leading zero counts by its value", "01.0.0", on, false, false},
		{"shadow = false stays off before the freeze", "0.4.0", off, true, false},
		{"shadow = false stays off after the freeze", "1.0.0", off, false, false},
		{"a scoring version that isn't MAJOR.MINOR.PATCH freezes nothing, so shadow stays as the file says", "1.0", on, true, true},
		{"a scoring version with a suffix isn't one, so shadow stays as the file says", "1.0.0-rc.1", on, true, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			atScoring(t, tt.version)
			if got := Calibrating(); got != tt.calibrating {
				t.Errorf("Calibrating() = %v at %s, want %v", got, tt.version, tt.calibrating)
			}
			cfg, problems := Parse([]byte(tt.file))
			if cfg.Display.Shadow != tt.shadow || len(problems) != 0 {
				t.Errorf("Parse(%q) at %s has shadow %v and problems %q, want %v and none", tt.file, tt.version, cfg.Display.Shadow, problems, tt.shadow)
			}
			name := filepath.Join(t.TempDir(), "config.toml")
			if err := os.WriteFile(name, []byte(tt.file), 0o600); err != nil {
				t.Fatal(err)
			}
			if cfg, _, err := Load(name); err != nil || cfg.Display.Shadow != tt.shadow {
				t.Errorf("Load at %s has shadow %v, %v, want %v", tt.version, cfg.Display.Shadow, err, tt.shadow)
			}
			if cfg, _, parsed, err := LoadParsed(name); err != nil || !parsed || cfg.Display.Shadow != tt.shadow {
				t.Errorf("LoadParsed at %s has shadow %v, parsed %v, %v, want %v", tt.version, cfg.Display.Shadow, parsed, err, tt.shadow)
			}
		})
	}

	// Two spellings of the setting: the decoder puts one of them in the
	// file's struct, by map order, so Parse must not read either.
	twice := []struct {
		name string
		file string
	}{
		{"display.shadow in two spellings under [display] is read as off every time, and reported", "[display]\nshadow = true\nShadow = true\n"},
		{"display.shadow in a [display] and a [Display] table is read as off every time, and reported", "[display]\nshadow = true\n[Display]\nshadow = true\n"},
	}
	for _, tt := range twice {
		t.Run(tt.name, func(t *testing.T) {
			atScoring(t, "0.4.0")
			for range 50 {
				cfg, problems := Parse([]byte(tt.file))
				if cfg.Display.Shadow || len(problems) != 1 || !strings.Contains(problems[0], "display.shadow is set more than once") {
					t.Fatalf("Parse(%q) has shadow %v and problems %q, want it off and the setting reported", tt.file, cfg.Display.Shadow, problems)
				}
			}
		})
	}

	t.Run("pkg/score's VERSION is MAJOR.MINOR.PATCH, so the freeze is read from it", func(t *testing.T) {
		parts := strings.Split(score.Version(), ".")
		if len(parts) != 3 || slices.ContainsFunc(parts, func(p string) bool { return p == "" || strings.Trim(p, "0123456789") != "" }) {
			t.Errorf("pkg/score's version %q isn't MAJOR.MINOR.PATCH, so the freeze would never end shadow", score.Version())
		}
	})

	// The freeze reads the setting as off, and an edit still sees what the
	// file says, so it can still change the line.
	edits := []struct {
		name string
		file string
		on   bool
		want string
	}{
		{"after the freeze, SetShadow still turns the line off", on, false, off},
		{"after the freeze, SetShadow still turns the line on, though Parse reads it as off", off, true, on},
	}
	for _, tt := range edits {
		t.Run(tt.name, func(t *testing.T) {
			atScoring(t, "1.0.0")
			name := filepath.Join(t.TempDir(), "config.toml")
			if err := os.WriteFile(name, []byte(tt.file), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := SetShadow(name, tt.on); err != nil {
				t.Fatal(err)
			}
			if got, err := os.ReadFile(name); err != nil || string(got) != tt.want {
				t.Errorf("config.toml is %q, %v, want %q", got, err, tt.want)
			}
		})
	}
}

func TestSetShadow(t *testing.T) {
	atScoring(t, "0.4.0")
	onDefault := strings.Replace(defaultFile, "\nshadow = false\n", "\nshadow = true\n", 1)
	if onDefault == defaultFile {
		t.Fatal("the default config.toml has no shadow = false line")
	}
	tests := []struct {
		name    string
		file    string
		missing bool // there is no config.toml, and file is ignored
		on      bool
		want    string // config.toml afterwards; with missing and !on, there is none
		wantErr string
		notErr  string // what the error mustn't say
		same    bool   // config.toml must be left alone, not written again
	}{
		{name: "on in the default file changes only the shadow line", file: defaultFile, on: true, want: onDefault},
		{name: "off after on changes the line back", file: onDefault, on: false, want: defaultFile},
		{name: "on with no config.toml writes the default file with shadow on", missing: true, on: true, want: onDefault},
		{name: "off with no config.toml writes none, since shadow is off by default", missing: true, on: false},
		{name: "on in a file that says so already leaves it alone", file: "[display]\nshadow = true\n", on: true, want: "[display]\nshadow = true\n", same: true},
		{
			name: "an existing [display] table keeps its comments, a comment after the value and the tables around it",
			file: "# mine\nmode = \"local\"\n\n[display]\n# my calibration week\nshadow = false # set by me\n\n[notifications]\nenabled = false\n",
			on:   true,
			want: "# mine\nmode = \"local\"\n\n[display]\n# my calibration week\nshadow = true # set by me\n\n[notifications]\nenabled = false\n",
		},
		{
			name: "CRLF line endings and comments stay in an existing [display] table",
			file: "# a\r\n[display]\r\n# mine\r\nshadow = false\r\n[http]\r\nport = 8123\r\n",
			on:   true,
			want: "# a\r\n[display]\r\n# mine\r\nshadow = true\r\n[http]\r\nport = 8123\r\n",
		},
		{
			name: "a file without [display] gets the table at its end, with CRLF endings in a CRLF file",
			file: "mode = \"local\"\r\n[notifications]\r\nenabled = true\r\n",
			on:   true,
			want: "mode = \"local\"\r\n[notifications]\r\nenabled = true\r\n\r\n[display]\r\nshadow = true\r\n",
		},
		{name: "an empty file gets the table alone", file: "", on: true, want: "[display]\nshadow = true\n"},
		{
			name: "a [display] table without the setting gets it under its header",
			file: "mode = \"local\"\n\n[display]\n# mine\n",
			on:   true,
			want: "mode = \"local\"\n\n[display]\nshadow = true\n# mine\n",
		},
		{
			name: "a [display] header on the file's last line, with no line break after it, gets the setting under it",
			file: "mode = \"local\"\n[display]",
			on:   true,
			want: "mode = \"local\"\n[display]\nshadow = true\n",
		},
		{name: "an indented setting keeps its indentation", file: "[display]\n  shadow = false\n", on: true, want: "[display]\n  shadow = true\n"},
		{name: "a file that starts with a byte order mark keeps it", file: "\ufeff[display]\nshadow = false\n", on: true, want: "\ufeff[display]\nshadow = true\n"},
		{
			name: "a display.shadow dotted key at the top of the file is changed where it is",
			file: "display.shadow = false # mine\n[http]\nport = 0\n",
			on:   true,
			want: "display.shadow = true # mine\n[http]\nport = 0\n",
		},
		{
			name: "a [display] made of dotted keys at the top of the file gets the setting beside them, and no header",
			file: "mode = \"local\"\ndisplay.theme = \"dark\"\n[http]\nport = 0\n",
			on:   true,
			want: "mode = \"local\"\ndisplay.theme = \"dark\"\ndisplay.shadow = true\n[http]\nport = 0\n",
		},
		{
			name: "a shadow setting under another table isn't touched, and [display] gets its own",
			file: "[notifications]\nshadow = true\n",
			on:   true,
			want: "[notifications]\nshadow = true\n\n[display]\nshadow = true\n",
		},
		{name: "an inline table that says shadow = true already is left alone", file: "display = { shadow = true }\n", on: true, want: "display = { shadow = true }\n", same: true},
		{name: "off in a file without the setting, which is off already, adds nothing", file: "mode = \"local\"\n", on: false, want: "mode = \"local\"\n", same: true},
		{
			name:    "a setting in an inline table isn't changed, and the error says to change it where it is, not to add lines",
			file:    "display = { shadow = false }\n",
			on:      true,
			want:    "display = { shadow = false }\n",
			wantErr: "change shadow to true where",
			notErr:  "add the lines",
		},
		{
			name:    "two spellings of the setting in an inline table aren't changed, and the error says to write it once, in lower case",
			file:    "display = { shadow = true, Shadow = true }\n",
			on:      true,
			want:    "display = { shadow = true, Shadow = true }\n",
			wantErr: "the setting once, as shadow, in lower case",
		},
		{
			name:    "a setting spelled Shadow in an inline table isn't changed, and the error says to write it in lower case, not to add lines",
			file:    "display = { Shadow = true }\n",
			on:      true,
			want:    "display = { Shadow = true }\n",
			wantErr: "in lower case",
			notErr:  "add the lines",
		},
		{
			name:    "a table spelled [Display], which Parse doesn't read, isn't changed, and the error says to write it in lower case",
			file:    "[Display]\nshadow = false\n",
			on:      true,
			want:    "[Display]\nshadow = false\n",
			wantErr: "in lower case",
		},
		{
			name:    "a setting spelled Shadow, which Parse doesn't read, isn't changed, and the error says to write it in lower case",
			file:    "[display]\nShadow = false\n",
			on:      true,
			want:    "[display]\nShadow = false\n",
			wantErr: "in lower case",
		},
		{
			name:    "a file with the setting in two spellings, one of them in lower case, isn't changed, and the error says to write it in lower case",
			file:    "[display]\nshadow = false\nShadow = true\n",
			on:      true,
			want:    "[display]\nshadow = false\nShadow = true\n",
			wantErr: "in lower case",
		},
		{
			name:    "a file that doesn't parse isn't changed, and the error says what to write by hand",
			file:    "[display]\nshadow = false\n[unclosed\n",
			on:      true,
			want:    "[display]\nshadow = false\n[unclosed\n",
			wantErr: "set shadow = true under [display]",
		},
		{
			name:    "a value that isn't true or false makes the file not parse, so it isn't changed",
			file:    "[display]\nshadow = \"yes\"\n",
			on:      true,
			want:    "[display]\nshadow = \"yes\"\n",
			wantErr: "doesn't parse",
		},
		{
			name:    "a file with a multi-line string isn't changed",
			file:    "[display]\nshadow = false\n[notifications]\nquiet_hours = \"\"\"\n22:00-07:00\"\"\"\n",
			on:      true,
			want:    "[display]\nshadow = false\n[notifications]\nquiet_hours = \"\"\"\n22:00-07:00\"\"\"\n",
			wantErr: "multi-line string",
		},
		{
			name: "a file with a multi-line string that says shadow = true already is left alone, since nothing in it is rewritten",
			file: "[display]\nshadow = true\n[notifications]\nquiet_hours = \"\"\"22:00-07:00\"\"\"\n",
			on:   true,
			want: "[display]\nshadow = true\n[notifications]\nquiet_hours = \"\"\"22:00-07:00\"\"\"\n",
			same: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			name := filepath.Join(t.TempDir(), "config.toml")
			var before os.FileInfo
			if !tt.missing {
				if err := os.WriteFile(name, []byte(tt.file), 0o600); err != nil {
					t.Fatal(err)
				}
				var err error
				if before, err = os.Stat(name); err != nil {
					t.Fatal(err)
				}
			}
			err := SetShadow(name, tt.on)
			switch {
			case tt.wantErr == "" && err != nil:
				t.Fatalf("error %v", err)
			case tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)):
				t.Fatalf("error %v, want one containing %q", err, tt.wantErr)
			case tt.notErr != "" && strings.Contains(err.Error(), tt.notErr):
				t.Errorf("error %v says %q", err, tt.notErr)
			}
			got, err := os.ReadFile(name)
			if tt.missing && !tt.on {
				if err == nil {
					t.Errorf("SetShadow wrote %q, want no file", got)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tt.want {
				t.Errorf("config.toml is\n%q\nwant\n%q", got, tt.want)
			}
			if tt.wantErr == "" {
				// Parse reads the setting the edit made, and finds no new
				// problem: the edit changed nothing else.
				cfg, problems := Parse(got)
				_, was := Parse([]byte(tt.file))
				if cfg.Display.Shadow != tt.on || len(problems) > len(was) {
					t.Errorf("Parse after the edit has shadow %v and problems %q, before %q; want shadow %v", cfg.Display.Shadow, problems, was, tt.on)
				}
			}
			if tt.same {
				if after, err := os.Stat(name); err != nil || !os.SameFile(before, after) {
					t.Errorf("config.toml was written again: %v", err)
				}
			}
		})
	}

	t.Run("a file with both spellings of the setting is refused every time, and never changed", func(t *testing.T) {
		// The spellings say different things, so a decode that picked one
		// of them by map order would let some of the tries through.
		const file = "[display]\nshadow = false\n[Display]\nshadow = true\n"
		name := filepath.Join(t.TempDir(), "config.toml")
		if err := os.WriteFile(name, []byte(file), 0o600); err != nil {
			t.Fatal(err)
		}
		for range 30 {
			if err := SetShadow(name, true); err == nil || !strings.Contains(err.Error(), "in lower case") {
				t.Fatalf("SetShadow = %v, want the error that says to write it in lower case", err)
			}
		}
		if got, err := os.ReadFile(name); err != nil || string(got) != file {
			t.Errorf("config.toml is %q, %v, want it as it was", got, err)
		}
	})

	t.Run("running it twice changes nothing the second time, and doesn't write the file again", func(t *testing.T) {
		name := filepath.Join(t.TempDir(), "config.toml")
		if err := WriteDefault(name); err != nil {
			t.Fatal(err)
		}
		if err := SetShadow(name, true); err != nil {
			t.Fatal(err)
		}
		first, err := os.Stat(name)
		if err != nil {
			t.Fatal(err)
		}
		if err := SetShadow(name, true); err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		second, err := os.Stat(name)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != onDefault || !os.SameFile(first, second) {
			t.Errorf("the second run left %q, written again: %v; want %q, not written again", got, !os.SameFile(first, second), onDefault)
		}
	})
}

// TestPlanShadow checks that PlanShadow says what SetShadow then writes,
// and writes nothing itself, not even the lock file every edit takes.
func TestPlanShadow(t *testing.T) {
	atScoring(t, "0.4.0")
	tests := []struct {
		name    string
		file    string
		missing bool
		// link is set when config.toml is a symlink to a file that isn't
		// there.
		link    bool
		on      bool
		wantErr string
	}{
		{name: "on in the default file plans the shadow line's change", file: defaultFile, on: true},
		{name: "a config.toml that is a link to nothing is refused, with the edit's error, and nothing is planned", link: true, on: true, wantErr: "a link pairtime can't follow"},
		{name: "on with no config.toml plans the default file with shadow on, from no file", missing: true, on: true},
		{name: "off with no config.toml plans no file at all", missing: true, on: false},
		{name: "on in a file that says so already plans no change", file: "[display]\nshadow = true\n", on: true},
		{name: "a file SetShadow refuses is refused with SetShadow's error", file: "[display\n", on: true, wantErr: "set shadow = true under [display]"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			name := filepath.Join(dir, "config.toml")
			switch {
			case tt.link:
				if err := os.Symlink(filepath.Join(dir, "gone.toml"), name); err != nil {
					t.Skipf("this system can't make a symlink here: %v", err)
				}
			case !tt.missing:
				if err := os.WriteFile(name, []byte(tt.file), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			now, then, err := PlanShadow(name, tt.on)
			if entries, _ := os.ReadDir(dir); len(entries) != map[bool]int{true: 0, false: 1}[tt.missing] {
				t.Errorf("PlanShadow left %d files in the folder, so it wrote something", len(entries))
			}
			setErr := SetShadow(name, tt.on)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) || setErr == nil || err.Error() != setErr.Error() {
					t.Errorf("PlanShadow's error %v, SetShadow's %v; want the same one, containing %q", err, setErr, tt.wantErr)
				}
				return
			}
			if err != nil || setErr != nil {
				t.Fatalf("PlanShadow: %v; SetShadow: %v", err, setErr)
			}
			if tt.missing != (now == nil) || (!tt.missing && string(now) != tt.file) {
				t.Errorf("PlanShadow's file as it is: %q, want %q (none: %v)", now, tt.file, tt.missing)
			}
			wrote, rerr := os.ReadFile(name)
			switch {
			case then == nil && rerr == nil:
				t.Errorf("PlanShadow planned no file, and SetShadow wrote %q", wrote)
			case then != nil && (rerr != nil || string(wrote) != string(then)):
				t.Errorf("PlanShadow planned %q, and SetShadow wrote %q, %v", then, wrote, rerr)
			}
		})
	}
}

// TestUnparsedError checks the error an edit returns for a config.toml
// that doesn't parse. Its Error says what the decoder said, as the
// commands print it, and Line and Hand say where the file breaks and what
// to write by hand, for a screen that shows neither the decoder's words
// nor the teen's.
func TestUnparsedError(t *testing.T) {
	tests := []struct {
		name string
		file string
		line bool // Line is set
	}{
		{"a value that isn't TOML gives the line the file breaks at", "[display]\nshadow = mysecretword\n", true},
		{"a value of the wrong type gives the line of the value, which the decoder names", "[display]\nshadow = \"yes\"\n", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			name := filepath.Join(t.TempDir(), "config.toml")
			if err := os.WriteFile(name, []byte(tt.file), 0o600); err != nil {
				t.Fatal(err)
			}
			for _, edit := range []struct {
				what string
				err  error
			}{
				{"PlanShadow", func() error { _, _, err := PlanShadow(name, true); return err }()},
				{"SetShadow", SetShadow(name, true)},
			} {
				var u *UnparsedError
				switch {
				case !errors.As(edit.err, &u):
					t.Errorf("%s: %v, want an UnparsedError", edit.what, edit.err)
				case (u.Line > 0) != tt.line:
					t.Errorf("%s: Line %d, want one set: %v", edit.what, u.Line, tt.line)
				case !strings.Contains(u.Hand, "set shadow = true under [display] in "+name) || strings.Contains(u.Hand, "mysecretword"):
					t.Errorf("%s: Hand %q, want what to write by hand and none of the file's words", edit.what, u.Hand)
				case !strings.HasPrefix(u.Error(), "config: config.toml doesn't parse, so pairtime didn't change it (toml: "):
					t.Errorf("%s: Error %q, want the decoder's words, as before", edit.what, u.Error())
				}
			}
		})
	}
}
