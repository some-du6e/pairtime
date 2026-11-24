// SPDX-License-Identifier: AGPL-3.0-or-later

package config

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestParse(t *testing.T) {
	// The shadow cases read the file as the calibration week does, before
	// the scoring freeze, which TestScoringFreeze checks.
	atScoring(t, "0.4.0")
	tests := []struct {
		name        string
		file        string
		want        Config
		wantProblem string // a substring of the one problem expected; "" for none
	}{
		{name: "the default file parses to the defaults with no problems", file: defaultFile, want: Default()},
		{name: "an empty file gives the defaults", file: "", want: Default()},
		{name: "the defaults are local mode and the default port", file: "", want: Config{Mode: Local, HTTP: HTTP{Port: 0}}},
		{name: "mode local is kept", file: `mode = "local"`, want: Default()},
		{name: "display.shadow = true shows credited time as calibrating", file: "[display]\nshadow = true\n", want: Config{Mode: Local, Display: Display{Shadow: true}}},
		{name: "display.shadow is off by default", file: "[display]\n", want: Default()},
		{
			name:        "a mode this release lacks falls back to local and is reported",
			file:        `mode = "cloud"`,
			want:        Default(),
			wantProblem: `no mode "cloud"`,
		},
		{name: "mode online is kept", file: `mode = "online"`, want: Config{Mode: Online}},
		{
			name: "a server url is kept in lower case without its trailing slash",
			file: "[server]\nurl = \"https://Pairtime.Example.org/\"\n",
			want: Config{Mode: Local, Server: Server{URL: "https://pairtime.example.org"}},
		},
		{name: "an empty server url means the default server", file: "[server]\nurl = \"\"\n", want: Default()},
		{
			name:        "a server url over plain http to another computer is reported and ignored",
			file:        "[server]\nurl = \"http://pairtime.example.org\"\n",
			want:        Default(),
			wantProblem: `server.url "http://pairtime.example.org" uses http`,
		},
		{
			name:        "a server url with a path is reported and ignored",
			file:        "[server]\nurl = \"https://example.org/pairtime\"\n",
			want:        Default(),
			wantProblem: `server.url "https://example.org/pairtime" isn't the address of a server`,
		},
		{
			name:        "an unknown setting in [server] is reported",
			file:        "[server]\ntoken = \"ptd_x\"\n",
			want:        Default(),
			wantProblem: `unknown setting "server.token"`,
		},
		{
			name:        "a file that doesn't parse is ignored as a whole, with notifications off",
			file:        "[http]\nport = 8123\n[unclosed\n",
			want:        ignored(),
			wantProblem: "was ignored, so every setting has its default and notifications are off",
		},
		{
			name:        "a value of the wrong type makes the whole file ignored, with notifications off",
			file:        "mode = \"local\"\n[http]\nport = \"eighty\"\n",
			want:        ignored(),
			wantProblem: "was ignored, so every setting has its default and notifications are off",
		},
		{
			name:        "an unknown setting is reported and ignored",
			file:        "colour = \"blue\"\n",
			want:        Default(),
			wantProblem: `unknown setting "colour"`,
		},
		{name: "a port from 1024 up is used", file: "[http]\nport = 8123\n", want: Config{Mode: Local, HTTP: HTTP{Port: 8123}}},
		{
			name:        "a port below 1024 keeps the default and is reported",
			file:        "[http]\nport = 80\n",
			want:        Default(),
			wantProblem: "http.port 80",
		},
		{
			name:        "a port above 65535 keeps the default and is reported",
			file:        "[http]\nport = 70000\n",
			want:        Default(),
			wantProblem: "http.port 70000",
		},
		{name: "notifications are on with no quiet hours by default", file: "", want: Config{Mode: Local, Notifications: Notifications{}}},
		{name: "enabled = false turns notifications off", file: "[notifications]\nenabled = false\n", want: Config{Mode: Local, Notifications: Notifications{Off: true}}},
		{name: "enabled = true keeps notifications on", file: "[notifications]\nenabled = true\n", want: Default()},
		{
			name: "quiet hours that cross midnight are kept",
			file: "[notifications]\nquiet_hours = \"22:00-07:00\"\n",
			want: Config{Mode: Local, Notifications: Notifications{Quiet: quiet("22:00-07:00")}},
		},
		{
			name: "quiet hours within one day are kept, with notifications still off when asked",
			file: "[notifications]\nenabled = false\nquiet_hours = \"08:30-15:00\"\n",
			want: Config{Mode: Local, Notifications: Notifications{Off: true, Quiet: quiet("08:30-15:00")}},
		},
		{name: "empty quiet hours mean none", file: "[notifications]\nquiet_hours = \"\"\n", want: Default()},
		{
			name:        "quiet hours that don't parse turn notifications off and are reported",
			file:        "[notifications]\nquiet_hours = \"10pm-7am\"\n",
			want:        Config{Mode: Local, Notifications: Notifications{Off: true}},
			wantProblem: `notifications.quiet_hours "10pm-7am"`,
		},
		{
			name:        "quiet hours that start and end together turn notifications off and are reported",
			file:        "[notifications]\nquiet_hours = \"07:00-07:00\"\n",
			want:        Config{Mode: Local, Notifications: Notifications{Off: true}},
			wantProblem: "starts and ends at the same time",
		},
		{
			name:        "an enabled switch that isn't true or false makes the whole file ignored, with notifications off",
			file:        "[notifications]\nenabled = \"no\"\n",
			want:        ignored(),
			wantProblem: "notifications are off",
		},
		{
			name:        "an unknown setting in [notifications] turns notifications off and is reported",
			file:        "[notifications]\nsound = true\n",
			want:        Config{Mode: Local, Notifications: Notifications{Off: true}},
			wantProblem: `unknown setting "notifications.sound" was ignored, so notifications are off until it's fixed`,
		},
		{name: "the off switch in a table spelled [Notifications] turns notifications off", file: "[Notifications]\nenabled = false\n", want: Config{Mode: Local, Notifications: Notifications{Off: true}}},
		{name: "the off switch spelled Enabled turns notifications off", file: "[notifications]\nEnabled = false\n", want: Config{Mode: Local, Notifications: Notifications{Off: true}}},
		{name: "the switch spelled Enabled = true keeps notifications on", file: "[Notifications]\nEnabled = true\n", want: Default()},
		{
			name: "quiet hours written in capitals are kept",
			file: "[NOTIFICATIONS]\nQUIET_HOURS = \"22:00-07:00\"\n",
			want: Config{Mode: Local, Notifications: Notifications{Quiet: quiet("22:00-07:00")}},
		},
		{
			name:        "a misspelled off switch turns notifications off and is reported",
			file:        "[notifications]\nenable = false\n",
			want:        Config{Mode: Local, Notifications: Notifications{Off: true}},
			wantProblem: `unknown setting "notifications.enable" was ignored, so notifications are off until it's fixed`,
		},
		{
			name:        "an unknown key in a table spelled [Notifications] turns notifications off and is reported",
			file:        "[Notifications]\ndisabled = true\n",
			want:        Config{Mode: Local, Notifications: Notifications{Off: true}},
			wantProblem: `unknown setting "Notifications.disabled" was ignored, so notifications are off`,
		},
		{
			name:        "a misspelled dotted key turns notifications off and is reported",
			file:        "notifications.off = true\n",
			want:        Config{Mode: Local, Notifications: Notifications{Off: true}},
			wantProblem: `unknown setting "notifications.off" was ignored, so notifications are off`,
		},
		{
			name:        "an unknown key next to enabled = true still turns notifications off",
			file:        "[notifications]\nenabled = true\nquiet = \"22:00-07:00\"\n",
			want:        Config{Mode: Local, Notifications: Notifications{Off: true}},
			wantProblem: `unknown setting "notifications.quiet" was ignored, so notifications are off`,
		},
		{
			name:        "the switch written twice in different case turns notifications off and is reported",
			file:        "[notifications]\nenabled = true\nEnabled = false\n",
			want:        Config{Mode: Local, Notifications: Notifications{Off: true}},
			wantProblem: "notifications.enabled is set more than once",
		},
		{
			name:        "the switch in two spellings of the table turns notifications off and is reported",
			file:        "[notifications]\nenabled = true\n[Notifications]\nenabled = true\n",
			want:        Config{Mode: Local, Notifications: Notifications{Off: true}},
			wantProblem: "notifications.enabled is set more than once",
		},
		{
			name:        "quiet hours written twice in different case turn notifications off and are reported",
			file:        "[notifications]\nquiet_hours = \"22:00-07:00\"\nQuiet_Hours = \"\"\n",
			want:        Config{Mode: Local, Notifications: Notifications{Off: true}},
			wantProblem: "notifications.quiet_hours is set more than once",
		},
		{name: "prompt text is kept by default", file: "[privacy]\n", want: Default()},
		{name: "keep_prompt_text = true keeps prompt text", file: "[privacy]\nkeep_prompt_text = true\n", want: Default()},
		{
			name: "keep_prompt_text = false turns the switch off, which deletes the text kept",
			file: "[privacy]\nkeep_prompt_text = false\n",
			want: Config{Mode: Local, Privacy: Privacy{TextOff: true}},
		},
		{
			name: "the switch is read whatever the case of its table",
			file: "[Privacy]\nkeep_prompt_text = false\n",
			want: Config{Mode: Local, Privacy: Privacy{TextOff: true}},
		},
		{
			name:        "an unknown setting in [privacy] keeps no new prompt text and deletes none, and is reported",
			file:        "[privacy]\nkeep_prompt_txt = false\n",
			want:        Config{Mode: Local, Privacy: Privacy{TextHeld: true}},
			wantProblem: `unknown setting "privacy.keep_prompt_txt" was ignored, so pairtime keeps no new prompt text, and deletes none`,
		},
		{
			name:        "an unknown setting next to keep_prompt_text = true still keeps no new prompt text",
			file:        "[privacy]\nkeep_prompt_text = true\nprompt_days = 7\n",
			want:        Config{Mode: Local, Privacy: Privacy{TextHeld: true}},
			wantProblem: `unknown setting "privacy.prompt_days" was ignored, so pairtime keeps no new prompt text`,
		},
		{
			name:        "the switch written twice in different case keeps no new prompt text, deletes none, and is reported",
			file:        "[privacy]\nkeep_prompt_text = false\nKeep_Prompt_Text = true\n",
			want:        Config{Mode: Local, Privacy: Privacy{TextHeld: true}},
			wantProblem: "privacy.keep_prompt_text is set more than once",
		},
		{
			name: "prompt_text_days and path_days shorten how long the vault keeps text and paths",
			file: "[privacy]\nprompt_text_days = 30\npath_days = 7\n",
			want: Config{Mode: Local, Privacy: Privacy{TextDays: 30, PathDays: 7}},
		},
		{
			name:        "prompt_text_days below 1 keeps no new prompt text, shortens nothing, and is reported",
			file:        "[privacy]\nprompt_text_days = 0\n",
			want:        Config{Mode: Local, Privacy: Privacy{TextHeld: true}},
			wantProblem: "privacy.prompt_text_days is 0, which isn't a number of days from 1 up, so pairtime keeps no new prompt text",
		},
		{
			name:        "path_days below 1 keeps paths for the longest time and text as before, and is reported",
			file:        "[privacy]\npath_days = -3\n",
			want:        Default(),
			wantProblem: "privacy.path_days is -3, which isn't a number of days from 1 up, so pairtime keeps the paths for your recaps for the longest time",
		},
		{
			name:        "prompt_text_days written twice in different case keeps no new prompt text and is reported",
			file:        "[privacy]\nprompt_text_days = 30\nPrompt_Text_Days = 7\n",
			want:        Config{Mode: Local, Privacy: Privacy{TextHeld: true}},
			wantProblem: "privacy.prompt_text_days is set more than once",
		},
		{
			name:        "the switch written at the top of the file instead of under [privacy] keeps no new prompt text, deletes none, and is reported",
			file:        "mode = \"local\"\nkeep_prompt_text = false\n",
			want:        Config{Mode: Local, Privacy: Privacy{TextHeld: true}},
			wantProblem: `unknown setting "keep_prompt_text" was ignored, so pairtime keeps no new prompt text, and deletes none`,
		},
		{
			name:        "keep_prompt_text = false added after an older file's [notifications] table keeps no new prompt text, deletes none, and turns notifications off",
			file:        "[notifications]\nenabled = true\nkeep_prompt_text = false\n",
			want:        Config{Mode: Local, Notifications: Notifications{Off: true}, Privacy: Privacy{TextHeld: true}},
			wantProblem: `unknown setting "notifications.keep_prompt_text" was ignored, so notifications are off, and pairtime keeps no new prompt text and deletes none`,
		},
		{
			name:        "keep_prompt_text = false beside a setting pairtime doesn't know holds the text, so nothing is deleted on a file it can't read",
			file:        "[privacy]\nkeep_prompt_text = false\nprompt_days = 7\n",
			want:        Config{Mode: Local, Privacy: Privacy{TextOff: true, TextHeld: true}},
			wantProblem: `unknown setting "privacy.prompt_days" was ignored, so pairtime keeps no new prompt text, and deletes none`,
		},
		{
			name:        "a switch that isn't true or false makes the whole file ignored, which keeps no new prompt text and deletes none",
			file:        "[privacy]\nkeep_prompt_text = \"no\"\n",
			want:        ignored(),
			wantProblem: "pairtime keeps no new prompt text",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, problems := Parse([]byte(tt.file))
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Parse() = %+v, want %+v", got, tt.want)
			}
			switch {
			case tt.wantProblem == "" && len(problems) != 0:
				t.Errorf("problems = %q, want none", problems)
			case tt.wantProblem != "" && (len(problems) != 1 || !strings.Contains(problems[0], tt.wantProblem)):
				t.Errorf("problems = %q, want one containing %q", problems, tt.wantProblem)
			}
		})
	}
}

func TestLoad(t *testing.T) {
	tests := []struct {
		name         string
		setup        func(name string) error
		want         Config
		wantNotExist bool
		wantErr      bool
	}{
		{name: "a missing file gives the defaults and fs.ErrNotExist", setup: func(string) error { return nil }, want: Default(), wantNotExist: true, wantErr: true},
		{name: "the file WriteDefault writes loads as the defaults", setup: WriteDefault, want: Default()},
		{
			name:    "a file that can't be read gives the defaults with notifications off, and the error",
			setup:   func(name string) error { return os.Mkdir(name, 0o700) },
			want:    ignored(),
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			name := filepath.Join(t.TempDir(), "config.toml")
			if err := tt.setup(name); err != nil {
				t.Fatal(err)
			}
			cfg, problems, err := Load(name)
			if got := errors.Is(err, fs.ErrNotExist); got != tt.wantNotExist {
				t.Fatalf("Load() error = %v, want fs.ErrNotExist: %v", err, tt.wantNotExist)
			}
			if (err != nil) != tt.wantErr {
				t.Fatalf("Load() error = %v, want an error: %v", err, tt.wantErr)
			}
			if !reflect.DeepEqual(cfg, tt.want) || len(problems) != 0 {
				t.Errorf("Load() = %+v with problems %q, want %+v and none", cfg, problems, tt.want)
			}
		})
	}
}

// quiet parses quiet hours for a test table.
func quiet(s string) QuietHours {
	q, err := ParseQuietHours(s)
	if err != nil {
		panic(err)
	}
	return q
}

func TestParseQuietHours(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    string // the String of the result; "" for none
		wantErr bool
	}{
		{name: "an empty value is no quiet hours", in: "", want: ""},
		{name: "blanks alone are no quiet hours", in: "  ", want: ""},
		{name: "a span that crosses midnight is read as written", in: "22:00-07:00", want: "22:00-07:00"},
		{name: "a span within one day is read as written", in: "08:30-15:00", want: "08:30-15:00"},
		{name: "blanks around the dash are allowed", in: "22:00 - 07:00", want: "22:00-07:00"},
		{name: "the last minute of the day is a valid time", in: "23:59-00:00", want: "23:59-00:00"},
		{name: "a 12-hour time is refused", in: "10pm-7am", wantErr: true},
		{name: "an hour past 23 is refused", in: "24:00-07:00", wantErr: true},
		{name: "a minute past 59 is refused", in: "22:60-07:00", wantErr: true},
		{name: "a one-digit hour is refused", in: "9:00-17:00", wantErr: true},
		{name: "a sign is refused", in: "+9:00-17:00", wantErr: true},
		{name: "one time alone is refused", in: "22:00", wantErr: true},
		{name: "a span that starts and ends together is refused", in: "07:00-07:00", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q, err := ParseQuietHours(tt.in)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ParseQuietHours(%q) error = %v, want an error: %v", tt.in, err, tt.wantErr)
			}
			if err != nil {
				return
			}
			if got := q.String(); got != tt.want {
				t.Errorf("ParseQuietHours(%q) = %q, want %q", tt.in, got, tt.want)
			}
			if q.IsZero() != (tt.want == "") {
				t.Errorf("ParseQuietHours(%q).IsZero() = %v", tt.in, q.IsZero())
			}
		})
	}
}

func TestQuietHoursContains(t *testing.T) {
	at := func(hhmm string) time.Time {
		tm, err := time.ParseInLocation("2006-01-02 15:04", "2026-11-19 "+hhmm, time.Local)
		if err != nil {
			panic(err)
		}
		return tm
	}
	tests := []struct {
		name  string
		quiet QuietHours
		at    time.Time
		want  bool
	}{
		{name: "no quiet hours contain no time", quiet: QuietHours{}, at: at("03:00"), want: false},
		{name: "a span includes its first minute", quiet: quiet("22:00-07:00"), at: at("22:00"), want: true},
		{name: "a span that crosses midnight includes the minutes after midnight", quiet: quiet("22:00-07:00"), at: at("00:30"), want: true},
		{name: "a span stops just before its second time", quiet: quiet("22:00-07:00"), at: at("07:00"), want: false},
		{name: "a span that crosses midnight leaves the afternoon out", quiet: quiet("22:00-07:00"), at: at("15:00"), want: false},
		{name: "a span within one day includes its middle", quiet: quiet("08:30-15:00"), at: at("12:00"), want: true},
		{name: "a span within one day leaves the night out", quiet: quiet("08:30-15:00"), at: at("23:00"), want: false},
		{name: "a span within one day leaves out the minute before it", quiet: quiet("08:30-15:00"), at: at("08:29"), want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.quiet.Contains(tt.at); got != tt.want {
				t.Errorf("QuietHours(%q).Contains(%s) = %v, want %v", tt.quiet, tt.at.Format("15:04"), got, tt.want)
			}
		})
	}
}
