// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
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
