// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRun(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantCode   int
		wantStdout string // substring; "" means stdout must be empty
		wantStderr string // substring; "" means stderr must be empty
	}{
		{name: "no arguments", args: nil, wantCode: 2, wantStderr: "Usage:"},
		{name: "version", args: []string{"version"}, wantCode: 0, wantStdout: "pairtime "},
		{name: "version names the release keys compiled in", args: []string{"version"}, wantCode: 0, wantStdout: "\nrelease keys: "},
		{name: "--version", args: []string{"--version"}, wantCode: 0, wantStdout: "pairtime "},
		{name: "version with an argument", args: []string{"version", "extra"}, wantCode: 2, wantStderr: "takes no arguments"},
		{name: "help", args: []string{"help"}, wantCode: 0, wantStdout: "Commands:"},
		{name: "-h", args: []string{"-h"}, wantCode: 0, wantStdout: "Commands:"},
		{name: "--help", args: []string{"--help"}, wantCode: 0, wantStdout: "Commands:"},
		{name: "unknown command", args: []string{"frobnicate"}, wantCode: 2, wantStderr: `unknown command "frobnicate"`},
		{name: "help lists the registered commands", args: []string{"help"}, wantCode: 0, wantStdout: "  agent  "},
		{name: "agent -h documents the exit status", args: []string{"agent", "-h"}, wantCode: 0, wantStderr: "3  another agent is already running"},
		{name: "agent with an unknown flag is a usage error", args: []string{"agent", "--frobnicate"}, wantCode: 2, wantStderr: "flag provided but not defined"},
		{name: "agent with an argument is a usage error", args: []string{"agent", "now"}, wantCode: 2, wantStderr: "takes no arguments"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := run(tt.args, &stdout, &stderr)
			if code != tt.wantCode {
				t.Errorf("exit code = %d, want %d", code, tt.wantCode)
			}
			check(t, "stdout", stdout.String(), tt.wantStdout)
			check(t, "stderr", stderr.String(), tt.wantStderr)
		})
	}
}

// pipeWith returns the read end of a pipe that holds data, as a harness
// passes a hook its event.
func pipeWith(data string) func(t *testing.T) *os.File {
	return func(t *testing.T) *os.File {
		r, w, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { r.Close() })
		if _, err := w.WriteString(data); err != nil {
			t.Fatal(err)
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
		return r
	}
}

// fileWith returns a file that holds data, open at its start.
func fileWith(data string) func(t *testing.T) *os.File {
	return func(t *testing.T) *os.File {
		path := filepath.Join(t.TempDir(), "stdin")
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
		f, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { f.Close() })
		return f
	}
}

// devNull returns the null device, a character device, which stands in
// for a person's terminal: go test has none to give.
func devNull(t *testing.T) *os.File {
	f, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	return f
}

func TestStart(t *testing.T) {
	event := `{"hook_event_name": "beforeSubmitPrompt", "cursor_version": "2026.09.26", "prompt": "fix the build"}`
	tests := []struct {
		name       string
		args       []string
		stdin      func(t *testing.T) *os.File
		wantCode   int
		wantStdout string // substring; "" means stdout must be empty
		wantStderr string // substring; "" means stderr must be empty
	}{
		{name: "a harness that runs a hook's command without its args, with the event on a pipe, gets exit 0 and no output",
			stdin: pipeWith(event), wantCode: 0},
		{name: "no arguments and a file on stdin is a program's run too, and gets exit 0 and no output",
			stdin: fileWith(event), wantCode: 0},
		{name: "no arguments and a character device on stdin, as a person's terminal is, gets the usage",
			stdin: devNull, wantCode: 2, wantStderr: "Usage:"},
		{name: "a command with a pipe on stdin runs as it would from a terminal",
			args: []string{"version"}, stdin: pipeWith(""), wantCode: 0, wantStdout: "pairtime "},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := start(tt.args, tt.stdin(t), &stdout, &stderr); code != tt.wantCode {
				t.Errorf("exit code = %d, want %d", code, tt.wantCode)
			}
			check(t, "stdout", stdout.String(), tt.wantStdout)
			check(t, "stderr", stderr.String(), tt.wantStderr)
		})
	}
}

func check(t *testing.T, stream, got, want string) {
	t.Helper()
	if want == "" {
		if got != "" {
			t.Errorf("%s = %q, want it empty", stream, got)
		}
		return
	}
	if !strings.Contains(got, want) {
		t.Errorf("%s = %q, want it to contain %q", stream, got, want)
	}
}
