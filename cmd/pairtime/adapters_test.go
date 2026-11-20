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

func TestAdapters(t *testing.T) {
	differs := filepath.Join(t.TempDir(), "expected.jsonl")
	line := `{"from":"000001","event":{"data":{"reason":"other"},"harness":"claude_code","kind":"session.end","session_key":"session-1","source":"hook","t":1,"via":"hook"}}` + "\n"
	if err := os.WriteFile(differs, []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name       string
		args       []string
		code       int
		wantStdout string
		wantStderr string
	}{
		{name: "list names Claude Code and the hook events its adapter reads", args: []string{"list"},
			wantStdout: "claude_code\thooks: SessionStart, UserPromptSubmit, Stop, StopFailure, Notification, PermissionRequest, PostToolUse, SubagentStart, SubagentStop, SessionEnd\n"},
		{name: "the real Claude Code capture replays to the adapter's expected records, run by run",
			args: []string{"test", claudeCapture}, wantStdout: "ok   claude_code/2.1.327/linux/cli: 89 records\n"},
		{name: "a run's own folder replays alone",
			args: []string{"test", filepath.Join(claudeCapture, "cli")}, wantStdout: "ok   claude_code/2.1.327/linux/cli: 89 records\n"},
		{name: "a run that doesn't replay to the expected file fails, saying where",
			args: []string{"test", "--expected", differs, filepath.Join(claudeCapture, "cli")}, code: 1,
			wantStdout: "FAIL claude_code/2.1.327/linux/cli\n  record 1:\n"},
		{name: "a folder with no run fails", args: []string{"test", t.TempDir()}, code: 1, wantStderr: "holds no run with hooks/"},
		{name: "test with no folder is a usage error", args: []string{"test"}, code: 2, wantStderr: "Usage:"},
		{name: "an unknown subcommand is a usage error", args: []string{"frob"}, code: 2, wantStderr: `unknown command "frob"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
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
		})
	}
}
