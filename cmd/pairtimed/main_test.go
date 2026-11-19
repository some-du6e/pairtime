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
		{name: "version", args: []string{"version"}, wantCode: 0, wantStdout: "pairtimed "},
		{name: "version with an argument", args: []string{"version", "extra"}, wantCode: 2, wantStderr: "takes no arguments"},
		{name: "help", args: []string{"help"}, wantCode: 0, wantStdout: "Commands:"},
		{name: "unknown command", args: []string{"serve"}, wantCode: 2, wantStderr: `unknown command "serve"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := run(tt.args, &stdout, &stderr)
			if code != tt.wantCode {
				t.Errorf("exit code = %d, want %d", code, tt.wantCode)
			}
			for _, s := range []struct{ stream, got, want string }{
				{"stdout", stdout.String(), tt.wantStdout},
				{"stderr", stderr.String(), tt.wantStderr},
			} {
				if s.want == "" && s.got != "" {
					t.Errorf("%s = %q, want it empty", s.stream, s.got)
				}
				if s.want != "" && !strings.Contains(s.got, s.want) {
					t.Errorf("%s = %q, want it to contain %q", s.stream, s.got, s.want)
				}
			}
		})
	}
}
