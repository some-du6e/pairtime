// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/some-du6e/pairtime/internal/server/config"
)

// syncBuffer is a bytes.Buffer that serve's goroutines can log to while a
// test reads it.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// testEnv returns an env that writes to fresh buffers and sees only the
// given environment.
func testEnv(environ ...string) (*env, *syncBuffer, *syncBuffer) {
	var stdout, stderr syncBuffer
	return &env{stdout: &stdout, stderr: &stderr, environ: environ}, &stdout, &stderr
}

func TestRun(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantCode   int
		wantStdout string // substring; "" means stdout must be empty
		wantStderr string // substring; "" means stderr must be empty
	}{
		{name: "no arguments print the usage and fail", args: nil, wantCode: 2, wantStderr: "Usage:"},
		{name: "version prints the build", args: []string{"version"}, wantCode: 0, wantStdout: "pairtimed "},
		{name: "--version is version", args: []string{"--version"}, wantCode: 0, wantStdout: "pairtimed "},
		{name: "version prints the config_version a pairtime.toml that uses this release's keys sets", args: []string{"version"}, wantCode: 0, wantStdout: fmt.Sprintf("\npairtime.toml config_version %d\n", config.ConfigVersion)},
		{name: "version takes no arguments", args: []string{"version", "extra"}, wantCode: 2, wantStderr: "takes no arguments"},
		{name: "help lists the commands", args: []string{"help"}, wantCode: 0, wantStdout: "Commands:"},
		{name: "an unknown command fails with the usage", args: []string{"frobnicate"}, wantCode: 2, wantStderr: `unknown command "frobnicate"`},
		{name: "a command's -h prints its flags and succeeds", args: []string{"migrate", "-h"}, wantCode: 0, wantStderr: "-status"},
		{name: "an unknown flag is a usage error", args: []string{"serve", "-nope"}, wantCode: 2, wantStderr: "flag provided but not defined"},
		{name: "a stray argument is a usage error", args: []string{"migrate", "extra"}, wantCode: 2, wantStderr: "unexpected arguments"},
		{name: "serve without its config file fails", args: []string{"serve", "-config", "/nonexistent/pairtime.toml"}, wantCode: 1, wantStderr: "pairtime.toml"},
		{name: "healthcheck exits 1, never 2, on a usage error, as Docker expects", args: []string{"healthcheck", "-nope"}, wantCode: 1, wantStderr: "flag provided but not defined"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e, stdout, stderr := testEnv()
			code := run(context.Background(), tt.args, e)
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

func TestCommandTable(t *testing.T) {
	tests := []struct {
		name    string
		command string
	}{
		{name: "serve registers itself", command: "serve"},
		{name: "migrate registers itself", command: "migrate"},
		{name: "healthcheck registers itself", command: "healthcheck"},
		{name: "version registers itself", command: "version"},
		{name: "init registers itself", command: "init"},
		{name: "keys registers itself", command: "keys"},
		{name: "snapshot registers itself", command: "snapshot"},
		{name: "doctor registers itself", command: "doctor"},
	}
	help := usage()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, ok := commands[tt.command]
			if !ok {
				t.Fatalf("%s isn't in the command table", tt.command)
			}
			if c.summary == "" {
				t.Errorf("%s has no summary for the help text", tt.command)
			}
			if !strings.Contains(help, "  "+tt.command+" ") || !strings.Contains(help, c.summary) {
				t.Errorf("the help text doesn't list %s with its summary:\n%s", tt.command, help)
			}
		})
	}
	t.Run("registering a name twice panics", func(t *testing.T) {
		defer func() {
			if recover() == nil {
				t.Error("register accepted a second version command")
			}
		}()
		register(&command{name: "version"})
	})
}

func TestGetenv(t *testing.T) {
	tests := []struct {
		name    string
		environ []string
		want    string
	}{
		{name: "an unset variable is empty", environ: []string{"OTHER=1"}, want: ""},
		{name: "a set variable is found", environ: []string{"PAIRTIME_CONFIG=/a.toml"}, want: "/a.toml"},
		{name: "the last of two settings wins, as with exec", environ: []string{"PAIRTIME_CONFIG=/a.toml", "PAIRTIME_CONFIG=/b.toml"}, want: "/b.toml"},
		{name: "a value may contain '='", environ: []string{"PAIRTIME_CONFIG=/a=b.toml"}, want: "/a=b.toml"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := &env{environ: tt.environ}
			if got := e.getenv("PAIRTIME_CONFIG"); got != tt.want {
				t.Errorf("getenv = %q, want %q", got, tt.want)
			}
		})
	}
}
