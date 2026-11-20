// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/some-du6e/pairtime/pkg/redact"
)

func TestFixturesScrub(t *testing.T) {
	// Built at run time, so no file in the repo looks like a leaked key.
	stripe := "sk_" + "live_" + strings.Repeat("Qw81Zx4LmN", 3)
	maya := []string{"--account", "maya", "--home", "/home/maya"}
	tests := []struct {
		name      string
		files     map[string]string
		args      []string // before the directory; --no-detect is always first
		detect    *redact.Known
		wantCode  int
		wantFiles map[string]string // nil: the files don't change
		wantOut   []string          // substrings of stdout
		notOut    []string          // must appear in neither stdout nor stderr
	}{
		{
			name:     "known values and a secret become placeholders",
			files:    map[string]string{"session.jsonl": `{"cwd":"/home/maya/robot","text":"key ` + stripe + ` ok"}` + "\n"},
			args:     maya,
			wantCode: 0,
			wantFiles: map[string]string{
				"session.jsonl": `{"cwd":"[HOME]/robot","text":"key [SECRET] ok"}` + "\n",
			},
			wantOut: []string{
				"session.jsonl:1:9: home, 10 bytes\n",
				"session.jsonl:1:39: secret stripe-access-token, 38 bytes\n",
				"removed 2 spans from 1 of 1 files",
			},
			notOut: []string{"maya", stripe},
		},
		{
			name: "a key inside a JSON string is found through the escapes around it",
			files: map[string]string{
				"write.jsonl":  `{"input":{"content":"STRIPE_KEY=\"` + stripe + `\"\n"}}` + "\n",
				"prompt.jsonl": `{"text":"my key:\n` + stripe + `"}` + "\n",
				"end.jsonl":    `{"text":"it is ` + stripe + `.\""}` + "\n" + `{"text":"` + stripe + `.\nthanks"}` + "\n",
			},
			wantCode: 0,
			wantFiles: map[string]string{
				"write.jsonl":  `{"input":{"content":"STRIPE_KEY=\"[SECRET]\"\n"}}` + "\n",
				"prompt.jsonl": `{"text":"my key:\n[SECRET]"}` + "\n",
				"end.jsonl":    `{"text":"it is [SECRET].\""}` + "\n" + `{"text":"[SECRET].\nthanks"}` + "\n",
			},
			wantOut: []string{
				"write.jsonl:1:35: secret stripe-access-token, 38 bytes\n",
				"prompt.jsonl:1:19: secret stripe-access-token, 38 bytes\n",
				"end.jsonl:1:16: secret stripe-access-token, 38 bytes\n",
				"end.jsonl:2:10: secret stripe-access-token, 38 bytes\n",
				"removed 4 spans from 3 of 3 files",
			},
			notOut: []string{stripe},
		},
		{
			name: "values and an email address right after a JSON escape are found, and the escape stays",
			files: map[string]string{
				"ps.jsonl":   `{"stdout":"USER  PID\nmaya  4242"}` + "\n",
				"mail.jsonl": `{"text":"hi\njonah@westfield.edu"}` + "\n",
				"log.jsonl":  `{"stdout":"    42\tMaya Kowalski"}` + "\n",
			},
			args:     []string{"--account", "maya", "--git-name", "Maya Kowalski"},
			wantCode: 0,
			wantFiles: map[string]string{
				"ps.jsonl":   `{"stdout":"USER  PID\n[ACCOUNT]  4242"}` + "\n",
				"mail.jsonl": `{"text":"hi\n[EMAIL]"}` + "\n",
				"log.jsonl":  `{"stdout":"    42\t[GIT_NAME]"}` + "\n",
			},
			wantOut: []string{
				"ps.jsonl:1:23: account, 4 bytes\n",
				"mail.jsonl:1:14: email, 19 bytes\n",
				"log.jsonl:1:20: git_name, 13 bytes\n",
				"removed 3 spans from 3 of 3 files",
			},
			notOut: []string{"maya", "Maya", "jonah"},
		},
		{
			name:     "-n lists the spans and changes nothing",
			files:    map[string]string{"a.txt": "home is /home/maya\n"},
			args:     append([]string{"-n"}, maya...),
			wantCode: 0,
			wantOut:  []string{"a.txt:1:9: home, 10 bytes\n", "would remove 1 spans from 1 of 1 files"},
			notOut:   []string{"maya"},
		},
		{
			name:     "values read from the machine are removed too",
			files:    map[string]string{"hook.json": `{"host":"robo-pc-77"}`},
			detect:   &redact.Known{Hosts: []string{"robo-pc-77"}},
			wantCode: 0,
			wantFiles: map[string]string{
				"hook.json": `{"host":"[HOST]"}`,
			},
			wantOut: []string{"hook.json:1:10: host, 10 bytes\n"},
			notOut:  []string{"robo-pc-77"},
		},
		{
			name:     "a binary file is reported, left alone, and fails the run",
			files:    map[string]string{"state.db": "SQLite\x00\x01maya"},
			args:     maya,
			wantCode: 1,
			wantOut:  []string{"state.db: binary, not scrubbed; review it by hand", "1 entries need a look by hand"},
		},
		{
			name:      "a name that holds a value is reported redacted and fails the run",
			files:     map[string]string{"-home-maya-robot/1.jsonl": "{}\n"},
			args:      maya,
			wantCode:  1,
			wantFiles: map[string]string{"-home-maya-robot/1.jsonl": "{}\n"},
			wantOut:   []string{"[HOME]-robot: the name holds home; rename it by hand"},
			notOut:    []string{"maya"},
		},
		{
			name: "a Claude Code project folder named after a Windows home is reported, and the home goes in every spelling",
			files: map[string]string{
				"transcripts/home/.claude/projects/C--Users-Maya-Chen-robot/1.jsonl": `{"cwd":"C:\\Users\\Maya Chen\\robot",` +
					`"transcript_path":"C:\\Users\\Maya Chen\\.claude\\projects\\C--Users-Maya-Chen-robot\\1.jsonl",` +
					`"uri":"file:///c%3A/Users/Maya%20Chen/robot"}` + "\n",
			},
			args:     []string{"--home", `C:\Users\Maya Chen`, "--account", "Maya Chen"},
			wantCode: 1,
			wantFiles: map[string]string{
				"transcripts/home/.claude/projects/C--Users-Maya-Chen-robot/1.jsonl": `{"cwd":"[HOME]\\robot",` +
					`"transcript_path":"[HOME]\\.claude\\projects\\[HOME]-robot\\1.jsonl",` +
					`"uri":"file://[HOME]/robot"}` + "\n",
			},
			wantOut: []string{
				"transcripts/home/.claude/projects/[HOME]-robot: the name holds home; rename it by hand\n",
				"transcripts/home/.claude/projects/[HOME]-robot/1.jsonl:1:9: home, 20 bytes\n",
				"transcripts/home/.claude/projects/[HOME]-robot/1.jsonl:1:57: home, 20 bytes\n",
				"transcripts/home/.claude/projects/[HOME]-robot/1.jsonl:1:98: home, 18 bytes\n",
				"transcripts/home/.claude/projects/[HOME]-robot/1.jsonl:1:147: home, 23 bytes\n",
				"removed 4 spans from 1 of 1 files",
				"1 entries need a look by hand",
			},
			notOut: []string{"Maya", "Chen"},
		},
		{
			name:     "PROVENANCE.md is left alone",
			files:    map[string]string{"PROVENANCE.md": "captured_by: maya\n", "x.txt": "hi maya\n"},
			args:     maya,
			wantCode: 0,
			wantFiles: map[string]string{
				"PROVENANCE.md": "captured_by: maya\n",
				"x.txt":         "hi [ACCOUNT]\n",
			},
			wantOut: []string{"x.txt:1:4: account, 4 bytes\n", "removed 1 spans from 1 of 1 files"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			old := detectKnown
			t.Cleanup(func() { detectKnown = old })
			args := []string{"fixtures", "scrub"}
			if tt.detect != nil {
				k := *tt.detect
				detectKnown = func() redact.Known { return k }
			} else {
				args = append(args, "--no-detect")
			}
			dir := t.TempDir()
			for name, content := range tt.files {
				p := filepath.Join(dir, filepath.FromSlash(name))
				if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			var stdout, stderr bytes.Buffer
			code := run(append(append(args, tt.args...), dir), &stdout, &stderr)
			if code != tt.wantCode {
				t.Errorf("exit code = %d, want %d\nstdout:\n%s\nstderr:\n%s", code, tt.wantCode, stdout.String(), stderr.String())
			}
			for _, w := range tt.wantOut {
				if !strings.Contains(stdout.String(), w) {
					t.Errorf("stdout = %q, want it to contain %q", stdout.String(), w)
				}
			}
			for _, n := range tt.notOut {
				if strings.Contains(stdout.String(), n) || strings.Contains(stderr.String(), n) {
					t.Errorf("output shows %q:\nstdout:\n%s\nstderr:\n%s", n, stdout.String(), stderr.String())
				}
			}
			want := tt.wantFiles
			if want == nil {
				want = tt.files
			}
			for name, content := range want {
				b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(name)))
				if err != nil {
					t.Fatal(err)
				}
				if string(b) != content {
					t.Errorf("%s = %q, want %q", name, b, content)
				}
			}
			entries, err := os.ReadDir(dir)
			if err != nil {
				t.Fatal(err)
			}
			for _, e := range entries {
				if strings.HasPrefix(e.Name(), ".pairtime-scrub-") {
					t.Errorf("left a temporary file %s", e.Name())
				}
			}
		})
	}
}

func TestFixturesUsage(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantCode   int
		wantStdout string
		wantStderr string
	}{
		{"no subcommand is a usage error", []string{"fixtures"}, 2, "", "pairtime fixtures scrub [flags] <dir>"},
		{"an unknown subcommand is a usage error", []string{"fixtures", "polish"}, 2, "", `unknown command "polish"`},
		{"help prints the usage", []string{"fixtures", "help"}, 0, "Run it with -n first", ""},
		{"scrub without a directory is a usage error", []string{"fixtures", "scrub", "--no-detect"}, 2, "", "want one directory"},
		{"scrub of a missing directory is a usage error", []string{"fixtures", "scrub", "--no-detect", "/nonexistent/pairtime-test"}, 2, "", "isn't a directory"},
		{"an unknown flag is a usage error", []string{"fixtures", "scrub", "--polish", "."}, 2, "", "flag provided but not defined"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := run(tt.args, &stdout, &stderr); code != tt.wantCode {
				t.Errorf("exit code = %d, want %d", code, tt.wantCode)
			}
			if tt.wantStdout != "" && !strings.Contains(stdout.String(), tt.wantStdout) {
				t.Errorf("stdout = %q, want it to contain %q", stdout.String(), tt.wantStdout)
			}
			if tt.wantStderr != "" && !strings.Contains(stderr.String(), tt.wantStderr) {
				t.Errorf("stderr = %q, want it to contain %q", stderr.String(), tt.wantStderr)
			}
		})
	}
}
