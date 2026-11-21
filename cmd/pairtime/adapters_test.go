// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// claudeCapture is WP-AD1's Claude Code 2.1.327 capture on Linux.
var claudeCapture = filepath.Join("..", "..", "testdata", "fixtures", "claude_code", "2.1.327", "linux")

// osFolder makes an OS folder of the Claude Code capture in a temporary
// directory: the real one's PROVENANCE.md, the hook calls of its cli run
// when cli is set, and the files of the runs given, by path in the folder.
func osFolder(t *testing.T, cli bool, files map[string]string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "linux")
	if cli {
		if err := os.CopyFS(filepath.Join(dir, "cli", "hooks"), os.DirFS(filepath.Join(claudeCapture, "cli", "hooks"))); err != nil {
			t.Fatal(err)
		}
	}
	files["PROVENANCE.md"] = ""
	prov, err := os.ReadFile(filepath.Join(claudeCapture, "PROVENANCE.md"))
	if err != nil {
		t.Fatal(err)
	}
	for name, data := range files {
		if name == "PROVENANCE.md" {
			data = string(prov)
		}
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestAdapters(t *testing.T) {
	osDir, err := filepath.Abs(claudeCapture)
	if err != nil {
		t.Fatal(err)
	}
	// A run waiting for its expected records, with and without the
	// pending.md that says why, and a run whose calls can't be read.
	pending := osFolder(t, true, map[string]string{"vscode/hooks/000001/argv": "/run\nStop\n",
		"vscode/pending.md": "gen_expected.py can't class VS Code's parent chains yet.\n"})
	unexplained := osFolder(t, true, map[string]string{"vscode/hooks/000001/argv": "/run\nStop\n"})
	broken := osFolder(t, false, map[string]string{"broken/hooks/000001/argv": "/run\nUserPromptSubmit\n"})
	private := filepath.Join(t.TempDir(), "expected.jsonl")
	privateLine := `{"from":"000001","event":{"data":{"turn_key":"turn-1"},"harness":"claude_code","kind":"prompt.submit","session_key":"session-1","source":"hook","t":1,"via":"hook"},"text":"a prompt only the person has seen"}` + "\n"
	if err := os.WriteFile(private, []byte(privateLine), 0o600); err != nil {
		t.Fatal(err)
	}
	differs := filepath.Join(t.TempDir(), "expected.jsonl")
	line := `{"from":"000001","event":{"data":{"reason":"other"},"harness":"claude_code","kind":"session.end","session_key":"session-1","source":"hook","t":1,"via":"hook"}}` + "\n"
	if err := os.WriteFile(differs, []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		// dir is the folder the command runs in, "" for the package's.
		dir        string
		args       []string
		code       int
		wantStdout string
		wantStderr string
		// inStdout and notInStdout must and mustn't be in what it prints.
		inStdout, notInStdout []string
	}{
		{name: "list names Claude Code and the hook events its adapter reads", args: []string{"list"},
			wantStdout: "claude_code\thooks: SessionStart, UserPromptSubmit, Stop, StopFailure, Notification, PermissionRequest, PostToolUse, PostToolUseFailure, SubagentStart, SubagentStop, SessionEnd\n"},
		{name: "the real Claude Code capture replays to the adapter's expected records, run by run",
			args: []string{"test", claudeCapture}, wantStdout: "ok   claude_code/2.1.327/linux/cli: 89 records\n"},
		{name: "a run's own folder replays alone",
			args: []string{"test", filepath.Join(claudeCapture, "cli")}, wantStdout: "ok   claude_code/2.1.327/linux/cli: 89 records\n"},
		{name: "a run's folder given as . from inside it replays", dir: filepath.Join(osDir, "cli"),
			args: []string{"test", "."}, wantStdout: "ok   claude_code/2.1.327/linux/cli: 89 records\n"},
		{name: "the OS folder given as . from inside it replays its runs", dir: osDir,
			args: []string{"test", "."}, wantStdout: "ok   claude_code/2.1.327/linux/cli: 89 records\n"},
		{name: "a run given by its name from inside its OS folder replays", dir: osDir,
			args: []string{"test", "cli"}, wantStdout: "ok   claude_code/2.1.327/linux/cli: 89 records\n"},
		{name: "a run that doesn't replay to the expected file fails, saying where",
			args: []string{"test", "--expected", differs, filepath.Join(claudeCapture, "cli")}, code: 1,
			wantStdout: "FAIL claude_code/2.1.327/linux/cli\n  record 1:\n"},
		{name: "a record that differs is shown without its prompt, reply, command line or paths, which say only that they differ",
			args: []string{"test", "--expected", differs, filepath.Join(claudeCapture, "cli")}, code: 1,
			inStdout: []string{"  text differs: ", " chars vs none"}, notInStdout: []string{`"text":`, `"command":`, `"edits":`}},
		{name: "--show-content shows the records that differ whole",
			args: []string{"test", "--show-content", "--expected", differs, filepath.Join(claudeCapture, "cli")}, code: 1,
			inStdout: []string{`"text":`, `"command":`}, notInStdout: []string{"text differs: "}},
		{name: "a run with no expected records that holds a pending.md is reported pending, and the other runs still replay",
			args: []string{"test", pending}, wantStdout: "ok   claude_code/2.1.327/linux/cli: 89 records\n",
			inStdout: []string{"pending claude_code/2.1.327/linux/vscode: no expected records yet, and its pending.md says why"}},
		{name: "a run with no expected records and no pending.md fails",
			args: []string{"test", unexplained}, code: 1, wantStderr: "FAIL vscode: no expected records"},
		{name: "--show-content shows nothing of the expected records when the run itself can't be read",
			args: []string{"test", "--show-content", "--expected", private, filepath.Join(broken, "broken")}, code: 1,
			wantStdout: "FAIL claude_code/2.1.327/linux/broken\n", notInStdout: []string{"only the person", `"text":`}},
		{name: "a folder with no run fails", args: []string{"test", t.TempDir()}, code: 1, wantStderr: "holds no run with hooks/"},
		{name: "test with no folder is a usage error", args: []string{"test"}, code: 2, wantStderr: "Usage:"},
		{name: "an unknown subcommand is a usage error", args: []string{"frob"}, code: 2, wantStderr: `unknown command "frob"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.dir != "" {
				t.Chdir(tc.dir)
			}
			var stdout, stderr bytes.Buffer
			code := run(append([]string{"adapters"}, tc.args...), &stdout, &stderr)
			if code != tc.code {
				t.Errorf("exit code %d, want %d\nstdout: %s\nstderr: %s", code, tc.code, stdout.String(), stderr.String())
			}
			if tc.wantStdout != "" && !strings.HasPrefix(stdout.String(), tc.wantStdout) {
				t.Errorf("stdout %q, want it to start %q", stdout.String(), tc.wantStdout)
			}
			if tc.wantStderr != "" && !strings.Contains(stderr.String(), tc.wantStderr) {
				t.Errorf("stderr %q, want %q in it", stderr.String(), tc.wantStderr)
			}
			for _, s := range tc.inStdout {
				if !strings.Contains(stdout.String(), s) {
					t.Errorf("stdout has no %q", s)
				}
			}
			for _, s := range tc.notInStdout {
				if strings.Contains(stdout.String(), s) {
					t.Errorf("stdout has %q", s)
				}
			}
		})
	}
}
