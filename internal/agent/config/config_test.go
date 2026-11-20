// SPDX-License-Identifier: AGPL-3.0-or-later

package config

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParse(t *testing.T) {
	off := Config{Mode: Local, Notifications: Notifications{Off: true}}
	tests := []struct {
		name         string
		file         string
		want         Config
		wantProblems []string // a substring of each problem expected, in order
	}{
		{name: "the default file parses to the defaults with no problems", file: defaultFile, want: Default()},
		{name: "an empty file gives the defaults", file: "", want: Default()},
		{name: "the defaults are local mode and the default port", file: "", want: Config{Mode: Local, HTTP: HTTP{Port: 0}}},
		{name: "mode local is kept", file: `mode = "local"`, want: Default()},
		{
			name:         "a mode this release lacks falls back to local and is reported",
			file:         `mode = "online"`,
			want:         Default(),
			wantProblems: []string{`no mode "online"`},
		},
		{
			name:         "a mode is checked under any case of its name",
			file:         `Mode = "online"`,
			want:         Default(),
			wantProblems: []string{`no mode "online"`},
		},
		{
			name:         "a file that doesn't parse is ignored as a whole, with notifications off",
			file:         "[http]\nport = 8123\n[unclosed\n",
			want:         ignored(),
			wantProblems: []string{"was ignored, so every setting has its default and notifications are off"},
		},
		{
			name:         "a value of the wrong type makes the whole file ignored, with notifications off",
			file:         "mode = \"local\"\n[http]\nport = \"eighty\"\n",
			want:         ignored(),
			wantProblems: []string{"was ignored, so every setting has its default and notifications are off"},
		},
		{
			name:         "an unknown setting is reported and ignored",
			file:         "colour = \"blue\"\n",
			want:         Default(),
			wantProblems: []string{`unknown setting "colour" was ignored`},
		},
		{name: "a port from 1024 up is used", file: "[http]\nport = 8123\n", want: Config{Mode: Local, HTTP: HTTP{Port: 8123}}},
		{name: "a port is used under any case of its table and name", file: "[HTTP]\nPort = 8123\n", want: Config{Mode: Local, HTTP: HTTP{Port: 8123}}},
		{
			name:         "a port below 1024 keeps the default and is reported",
			file:         "[http]\nport = 80\n",
			want:         Default(),
			wantProblems: []string{"http.port 80"},
		},
		{
			name:         "a port above 65535 keeps the default and is reported",
			file:         "[http]\nport = 70000\n",
			want:         Default(),
			wantProblems: []string{"http.port 70000"},
		},
		{name: "notifications are on with no quiet hours by default", file: "", want: Config{Mode: Local, Notifications: Notifications{}}},
		{name: "enabled = false turns notifications off", file: "[notifications]\nenabled = false\n", want: off},
		{name: "enabled = true keeps notifications on", file: "[notifications]\nenabled = true\n", want: Default()},
		{name: "enabled = false turns notifications off under a [Notifications] table", file: "[Notifications]\nenabled = false\n", want: off},
		{name: "Enabled = false turns notifications off, whatever the case of the name", file: "[notifications]\nEnabled = false\n", want: off},
		{name: "a dotted notifications.enabled = false turns notifications off", file: "notifications.enabled = false\n", want: off},
		{
			name: "quiet hours that cross midnight are kept",
			file: "[notifications]\nquiet_hours = \"22:00-07:00\"\n",
			want: Config{Mode: Local, Notifications: Notifications{Quiet: quiet("22:00-07:00")}},
		},
		{
			name: "quiet hours are kept under any case of their table and name",
			file: "[NOTIFICATIONS]\nQuiet_Hours = \"22:00-07:00\"\n",
			want: Config{Mode: Local, Notifications: Notifications{Quiet: quiet("22:00-07:00")}},
		},
		{
			name: "quiet hours within one day are kept, with notifications still off when asked",
			file: "[notifications]\nenabled = false\nquiet_hours = \"08:30-15:00\"\n",
			want: Config{Mode: Local, Notifications: Notifications{Off: true, Quiet: quiet("08:30-15:00")}},
		},
		{name: "empty quiet hours mean none", file: "[notifications]\nquiet_hours = \"\"\n", want: Default()},
		{
			name:         "quiet hours that don't parse turn notifications off and are reported",
			file:         "[notifications]\nquiet_hours = \"10pm-7am\"\n",
			want:         off,
			wantProblems: []string{`notifications.quiet_hours "10pm-7am"`},
		},
		{
			name:         "quiet hours that start and end together turn notifications off and are reported",
			file:         "[notifications]\nquiet_hours = \"07:00-07:00\"\n",
			want:         off,
			wantProblems: []string{"starts and ends at the same time"},
		},
		{
			name:         "an enabled switch that isn't true or false makes the whole file ignored, with notifications off",
			file:         "[notifications]\nenabled = \"no\"\n",
			want:         ignored(),
			wantProblems: []string{"notifications are off"},
		},
		{
			name:         "a misspelled off switch turns notifications off and is reported",
			file:         "[notifications]\nenable = false\n",
			want:         off,
			wantProblems: []string{`unknown setting "notifications.enable" was ignored, so notifications are off until it's fixed`},
		},
		{
			name:         "an unknown setting under [notifications] turns them off even beside enabled = true",
			file:         "[notifications]\nenabled = true\nsound = true\n",
			want:         off,
			wantProblems: []string{`unknown setting "notifications.sound" was ignored, so notifications are off until it's fixed`},
		},
		{
			name:         "a misspelled quiet_hours turns notifications off, since pairtime can't tell when to be quiet",
			file:         "[notifications]\nquiet-hours = \"22:00-07:00\"\n",
			want:         off,
			wantProblems: []string{`unknown setting "notifications.quiet-hours" was ignored, so notifications are off`},
		},
		{
			name: "a misspelled [notifications] table turns notifications off, and the table and its setting are reported",
			file: "[notification]\nenabled = false\n",
			want: off,
			wantProblems: []string{
				`unknown setting "notification" was ignored, so notifications are off`,
				`unknown setting "notification.enabled" was ignored, so notifications are off`,
			},
		},
		{
			name:         "quiet hours written above every table turn notifications off",
			file:         "quiet_hours = \"22:00-07:00\"\n",
			want:         off,
			wantProblems: []string{`unknown setting "quiet_hours" was ignored, so notifications are off`},
		},
		{
			name:         "an off switch written twice in two cases turns notifications off, since which one the decoder takes is chance",
			file:         "[notifications]\nenabled = true\nEnabled = false\n",
			want:         off,
			wantProblems: []string{`notifications.enabled is written more than once, as "notifications.enabled" and "notifications.Enabled", so notifications are off until it's fixed`},
		},
		{
			name: "quiet hours written in two tables of different case turn notifications off",
			file: "[notifications]\nquiet_hours = \"22:00-07:00\"\n[Notifications]\nquiet_hours = \"23:00-06:00\"\n",
			want: off,
			wantProblems: []string{
				`notifications.quiet_hours is written more than once, as "notifications.quiet_hours" and "Notifications.quiet_hours"`,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, problems := Parse([]byte(tt.file))
			if got != tt.want {
				t.Errorf("Parse() = %+v, want %+v", got, tt.want)
			}
			if len(problems) != len(tt.wantProblems) {
				t.Fatalf("problems = %q, want %d like %q", problems, len(tt.wantProblems), tt.wantProblems)
			}
			for i, want := range tt.wantProblems {
				if !strings.Contains(problems[i], want) {
					t.Errorf("problem %d = %q, want it to contain %q", i, problems[i], want)
				}
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
			if cfg != tt.want || len(problems) != 0 {
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
