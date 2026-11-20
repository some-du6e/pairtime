// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"encoding/json"
	"io/fs"
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
			name:     "known values and a secret become README's placeholders",
			files:    map[string]string{"session.jsonl": `{"cwd":"/home/maya/robot","text":"key ` + stripe + ` ok"}` + "\n"},
			args:     maya,
			wantCode: 0,
			wantFiles: map[string]string{
				"session.jsonl": `{"cwd":"/home/capture/robot","text":"key REDACTED_STRIPE_ACCESS_TOKEN ok"}` + "\n",
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
				"write.jsonl":  `{"input":{"content":"STRIPE_KEY=\"REDACTED_STRIPE_ACCESS_TOKEN\"\n"}}` + "\n",
				"prompt.jsonl": `{"text":"my key:\nREDACTED_STRIPE_ACCESS_TOKEN"}` + "\n",
				"end.jsonl":    `{"text":"it is REDACTED_STRIPE_ACCESS_TOKEN.\""}` + "\n" + `{"text":"REDACTED_STRIPE_ACCESS_TOKEN.\nthanks"}` + "\n",
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
				"ps.jsonl":   `{"stdout":"USER  PID\ncapture  4242"}` + "\n",
				"mail.jsonl": `{"text":"hi\ncapture@example.invalid"}` + "\n",
				"log.jsonl":  `{"stdout":"    42\tCapture User"}` + "\n",
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
			name: "telemetry account and organization ids become README's id, or 0 for a number, and each is listed",
			files: map[string]string{
				"otlp/000001.json": `{"resourceMetrics":[{"resource":{"attributes":[` +
					`{"key":"user.account_uuid","value":{"stringValue":"9b1e7f9e-1111-4222-8333-444455556666"}},` +
					`{"value":{"stringValue":"77aa88bb-1111-4222-8333-444455556666"},"key":"organization.id"},` +
					`{"key":"user.account_id","value":{"intValue":"4242"}},` +
					`{"key":"enduser.id","value":{"intValue":77}},` +
					`{"key":"session.id","value":{"stringValue":"5d6c1e2a-7b8f-4c3d-9e0f-112233445566"}}]}}]}`,
			},
			wantCode: 0,
			wantFiles: map[string]string{
				"otlp/000001.json": `{"resourceMetrics":[{"resource":{"attributes":[` +
					`{"key":"user.account_uuid","value":{"stringValue":"00000000-0000-4000-8000-000000000000"}},` +
					`{"value":{"stringValue":"00000000-0000-4000-8000-000000000000"},"key":"organization.id"},` +
					`{"key":"user.account_id","value":{"intValue":"0"}},` +
					`{"key":"enduser.id","value":{"intValue":0}},` +
					`{"key":"session.id","value":{"stringValue":"5d6c1e2a-7b8f-4c3d-9e0f-112233445566"}}]}}]}`,
			},
			wantOut: []string{
				"otlp/000001.json:1:99: telemetry_id user.account_uuid, 36 bytes\n",
				"otlp/000001.json:1:164: telemetry_id organization.id, 36 bytes\n",
				"otlp/000001.json:1:274: telemetry_id user.account_id, 4 bytes\n",
				"otlp/000001.json:1:322: telemetry_id enduser.id, 2 bytes\n",
				"removed 4 spans from 1 of 1 files",
			},
			notOut: []string{"9b1e7f9e", "77aa88bb"},
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
				"hook.json": `{"host":"capture-host"}`,
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
				"transcripts/home/.claude/projects/C--Users-Maya-Chen-robot/1.jsonl": `{"cwd":"C:\\Users\\capture\\robot",` +
					`"transcript_path":"C:\\Users\\capture\\.claude\\projects\\C--Users-capture-robot\\1.jsonl",` +
					`"uri":"file:///c%3A/Users/capture/robot"}` + "\n",
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
				"x.txt":         "hi capture\n",
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

// TestFixturesScrubPlantedCapture runs the scrub over the planted capture
// that tools/capture tests WP-AD0's scrub.py with, and checks what that
// test checks: no planted value survives, each one takes README's
// placeholder in the file's own spelling, ids that aren't secrets and
// public addresses stay, and every JSON file still parses. Here the
// Anthropic key and the GitHub token are real ones' length and shape,
// since gitleaks ignores scrub.py's short ones, and --deny stands in for
// scrub.py's --also. Unlike scrub.py, the scrub reports a folder whose
// name holds a value instead of renaming it.
func TestFixturesScrubPlantedCapture(t *testing.T) {
	const (
		session = "5d6c1e2a-7b8f-4c3d-9e0f-112233445566"
		tool    = "toolu_01ABCDEFGHIJKLMNOPQRSTUV"
		project = "transcripts/home/.claude/projects/-home-rowanq-pairtime-capture-project/"
	)
	// Built at run time, so no file in the repo looks like a leaked key.
	anthropic := "sk-ant-" + "api03-" + strings.Repeat("aB3_x9Kq-Z", 10)[:93] + "AA"
	ghp := "gh" + "p_" + strings.Repeat("Zx81Qw4LmN", 4)[:36]
	aws := "AKIA" + "ABCDEFGHIJKLMNOP"
	jwt := "eyJhbGciOiJIUzI1NiJ9" + ".eyJzdWIiOiIxMjM0NTY3ODkwIn0" + ".dozjgNryP4J3jVmNHl0w5N_XgL0n3I9PlFUP0THsR8U"
	files := map[string]string{
		"hooks/000001/stdin": `{"session_id":"` + session + `","transcript_path":"/home/rowanq/.claude/projects/-home-rowanq-pairtime-capture-project/` +
			session + `.jsonl","cwd":"/home/rowanq/pairtime-capture/project","hook_event_name":"UserPromptSubmit","prompt":"What does count_words do? Answer in one sentence."}`,
		"hooks/000001/env":   "HOME\nPATH\nROWANQ_TOKEN\n",
		"hooks/000002/stdin": `{"hook_event_name":"UserPromptSubmit","prompt":"my diary entry, which is not in the script"}`,
		project + session + ".jsonl": `{"type":"user","cwd":"C:\\Users\\RowanQ\\pairtime-capture\\project","sessionId":"` + session + `"}` + "\n" +
			`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"` + tool + `","input":{"command":"whoami"}}]}}` + "\n" +
			`{"toolUseResult":{"stdout":"rowanq\nrowanq@rowans-laptop:~$ git log\nAuthor: Rowan Quinn <rowan.quinn@westfield.edu>\n` + anthropic +
			`"},"uri":"file:///c%3A/Users/RowanQ/pairtime-capture/project"}` + "\n" +
			`{"nested":"{\"stdout\":\"C:\\\\Users\\\\RowanQ\\\\x\"}","school":"Westfield High"}` + "\n" +
			`{"keys":"` + ghp + " " + aws + " " + jwt + `","trailer":"Co-Authored-By: Claude <noreply@anthropic.com>"}` + "\n",
		"otlp/000001.json": `{"resourceLogs":[{"resource":{"attributes":[` +
			`{"key":"user.account_uuid","value":{"stringValue":"9b1e7f9e-1111-4222-8333-444455556666"}},` +
			`{"key":"organization.id","value":{"stringValue":"77aa88bb-1111-4222-8333-444455556666"}},` +
			`{"key":"user.email","value":{"stringValue":"rowan.quinn@westfield.edu"}},` +
			`{"key":"session.id","value":{"stringValue":"` + session + `"}}]}}]}`,
		"otlp/000002.bin": "\xff\xfe\x00\x81",
	}
	args := []string{"fixtures", "scrub", "--no-detect", "--home", "/home/rowanq", "--home", `C:\Users\RowanQ`, "--account", "rowanq",
		"--host", "rowans-laptop", "--git-name", "Rowan Quinn", "--git-email", "rowan.quinn@westfield.edu", "--deny", "Westfield High"}
	dir := t.TempDir()
	writeTree(t, dir, files)
	var stdout, stderr bytes.Buffer
	if code := run(append(args, dir), &stdout, &stderr); code != 1 {
		t.Fatalf("exit code = %d, want 1 for the folder name and the binary file\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	out := readTree(t, dir)
	var all strings.Builder
	for _, text := range out {
		all.WriteString(text + "\n")
	}
	everything := strings.ToLower(all.String())

	cases := []struct {
		name  string
		check func(t *testing.T)
	}{
		{"no planted value survives in any file, in any case", func(t *testing.T) {
			for _, v := range []string{"rowan", "westfield", strings.ToLower(anthropic[:24]), strings.ToLower(ghp), "akiaabcd", "eyjhbgci", "9b1e7f9e", "77aa88bb"} {
				if strings.Contains(everything, v) {
					t.Errorf("%q survived the scrub", v)
				}
			}
		}},
		{"the listing shows no planted value either", func(t *testing.T) {
			listing := strings.ToLower(stdout.String() + stderr.String())
			for _, v := range []string{"rowan", "westfield"} {
				if strings.Contains(listing, v) {
					t.Errorf("the listing shows %q:\n%s", v, stdout.String())
				}
			}
		}},
		{"a home path is replaced in every spelling", func(t *testing.T) {
			for _, want := range []string{`/home/capture/pairtime-capture/project`, `C:\\Users\\capture\\pairtime-capture`,
				`file:///c%3A/Users/capture/pairtime-capture`, `C:\\\\Users\\\\capture\\\\x`, `-home-capture-pairtime-capture-project`} {
				if !strings.Contains(all.String(), want) {
					t.Errorf("no %s in the output", want)
				}
			}
		}},
		{"names and addresses get their placeholders", func(t *testing.T) {
			for _, want := range []string{"capture@capture-host", "Capture User <capture@example.invalid>", `"school":"REDACTED"`, "capture_TOKEN",
				`"stringValue":"capture@example.invalid"`} {
				if !strings.Contains(all.String(), want) {
					t.Errorf("no %s in the output", want)
				}
			}
		}},
		{"telemetry account ids become README's id", func(t *testing.T) {
			if got := out["otlp/000001.json"]; strings.Count(got, "00000000-0000-4000-8000-000000000000") != 2 {
				t.Errorf("otlp body: %s", got)
			}
		}},
		{"keys and tokens become REDACTED and their rule", func(t *testing.T) {
			for _, want := range []string{"REDACTED_ANTHROPIC_API_KEY", "REDACTED_GITHUB_PAT", "REDACTED_AWS_ACCESS_TOKEN", "REDACTED_JWT"} {
				if !strings.Contains(all.String(), want) {
					t.Errorf("no %s in the output", want)
				}
			}
		}},
		{"ids that aren't secrets and public tool addresses are kept", func(t *testing.T) {
			for _, want := range []string{session, tool, "noreply@anthropic.com"} {
				if !strings.Contains(all.String(), want) {
					t.Errorf("%s was removed", want)
				}
			}
		}},
		{"every scrubbed JSON file is still valid JSON", func(t *testing.T) {
			for path, text := range out {
				if !strings.HasSuffix(path, "stdin") && !strings.HasSuffix(path, ".json") && !strings.HasSuffix(path, ".jsonl") {
					continue
				}
				for _, line := range strings.Split(strings.TrimSpace(text), "\n") {
					if !json.Valid([]byte(line)) {
						t.Errorf("%s: invalid JSON after the scrub: %s", path, line)
					}
				}
			}
		}},
		{"the folder named after the home and the binary file are reported", func(t *testing.T) {
			for _, want := range []string{"transcripts/home/.claude/projects/[HOME]-pairtime-capture-project: the name holds home; rename it by hand\n",
				"otlp/000002.bin: binary, not scrubbed; review it by hand\n"} {
				if !strings.Contains(stdout.String(), want) {
					t.Errorf("stdout = %q, want it to contain %q", stdout.String(), want)
				}
			}
		}},
		{"a second run changes nothing", func(t *testing.T) {
			var again bytes.Buffer
			run(append(args, dir), &again, &bytes.Buffer{})
			if !strings.Contains(again.String(), "removed 0 spans from 0 of") {
				t.Errorf("the second run listed spans:\n%s", again.String())
			}
			for path, text := range readTree(t, dir) {
				if text != out[path] {
					t.Errorf("the second run changed %s:\n%s\nto\n%s", path, out[path], text)
				}
			}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, c.check)
	}
}

// writeTree writes files, by slash-separated path, under dir.
func writeTree(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// readTree reads every file under dir, by slash-separated path.
func readTree(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		out[filepath.ToSlash(rel)] = string(b)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
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
