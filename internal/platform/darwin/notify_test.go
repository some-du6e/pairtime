// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build darwin

package darwin

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf16"
)

func TestAppleScriptText(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "empty text is an empty literal", in: "", want: `""`},
		{name: "printable ASCII is one literal", in: "52 min on remind-bot.", want: `"52 min on remind-bot."`},
		{name: "a double quote and a backslash are escaped", in: `a"b\c`, want: `"a\"b\\c"`},
		{name: "a non-ASCII character is its character id", in: "café", want: `"caf" & (character id 233)`},
		{name: "text may start with a character id", in: "é!", want: `(character id 233) & "!"`},
		{name: "a newline is its character id, never a line break in the script", in: "a\nb", want: `"a" & (character id 10) & "b"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := appleScriptText(tt.in); got != tt.want {
				t.Errorf("appleScriptText(%q) = %s, want %s", tt.in, got, tt.want)
			}
		})
	}
}

func TestNotificationScript(t *testing.T) {
	tests := []struct {
		name                  string
		title, subtitle, body string
		want                  string
	}{
		{
			name:  "the script is one display notification command with title and subtitle and no sound",
			title: "pairtime", subtitle: "Your session recap is ready", body: "52 min on remind-bot.",
			want: `display notification ("52 min on remind-bot.") with title ("pairtime") subtitle ("Your session recap is ready")` + "\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NotificationScript(tt.title, tt.subtitle, tt.body); got != tt.want {
				t.Errorf("NotificationScript() = %q, want %q", got, tt.want)
			}
		})
	}
}

// The oracle for appleScriptText is AppleScript itself: osascript reads the
// expression back, and its characters' ids must be the text's code points.
// Asking for ids keeps the comparison in ASCII digits, whatever encoding
// osascript prints in.
func TestAppleScriptTextReadsBackAsTheSameText(t *testing.T) {
	pwned := filepath.Join(t.TempDir(), "pwned")
	tests := []struct {
		name string
		text string
	}{
		{name: "plain ASCII reads back as written", text: "52 min on remind-bot. See recap."},
		{name: "double quotes and backslashes read back as written", text: `say "hi" \ C:\temp\new`},
		{name: "a quote can't end the string and run AppleScript", text: `" & (do shell script "touch ` + pwned + `") & "`},
		{name: "accented Latin text reads back", text: "café déjà vu"},
		{name: "Japanese text reads back", text: "日本語のプロジェクト"},
		{name: "an emoji outside the Basic Multilingual Plane reads back", text: "done 😀"},
		{name: "a tab and a newline read back", text: "a\tb\nc"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := runOsascript(t, "return id of ("+appleScriptText(tt.text)+")\n")
			got, err := parseIDs(out)
			if err != nil {
				t.Fatalf("osascript printed %q: %v", out, err)
			}
			runes := make([]int, 0, len(tt.text))
			for _, r := range tt.text {
				runes = append(runes, int(r))
			}
			var units []int
			for _, u := range utf16.Encode([]rune(tt.text)) {
				units = append(units, int(u))
			}
			// AppleScript may count a character outside the Basic
			// Multilingual Plane as one code point or as its two UTF-16
			// units; either is the same text.
			if !slices.Equal(got, runes) && !slices.Equal(got, units) {
				t.Errorf("osascript read back ids %v, want %v", got, runes)
			}
		})
	}
	if _, err := os.Stat(pwned); err == nil {
		t.Error("text inside a string ran do shell script")
	}
}

func runOsascript(t *testing.T, script string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, osascriptPath)
	cmd.Stdin = strings.NewReader(script)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("osascript: %v: %s", err, stderr.String())
	}
	return string(out)
}

// parseIDs reads osascript's printout of an integer or a list of them.
func parseIDs(out string) ([]int, error) {
	var ids []int
	for _, f := range strings.Split(strings.TrimSpace(out), ",") {
		n, err := strconv.Atoi(strings.TrimSpace(f))
		if err != nil {
			return nil, err
		}
		ids = append(ids, n)
	}
	return ids, nil
}

func TestDisplayNotificationRunsOsascriptWithTheScriptOnStdin(t *testing.T) {
	fake := filepath.Join(t.TempDir(), "osascript")
	writeScript(t, fake, "#!/bin/sh\necho \"$#\" > \"$0.argc\"\ncat > \"$0.stdin\"\n")
	tests := []struct {
		name                  string
		title, subtitle, body string
	}{
		{name: "the script arrives on stdin and nothing is on the command line", title: "pairtime", subtitle: "Your session recap is ready", body: "52 min on secret-project."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if err := (Osascript{Path: fake}).DisplayNotification(ctx, tt.title, tt.subtitle, tt.body); err != nil {
				t.Fatal(err)
			}
			if argc := readFile(t, fake+".argc"); argc != "0\n" {
				t.Errorf("osascript got %q arguments, want none", strings.TrimSpace(argc))
			}
			if got, want := readFile(t, fake+".stdin"), NotificationScript(tt.title, tt.subtitle, tt.body); got != want {
				t.Errorf("osascript read %q on stdin, want %q", got, want)
			}
		})
	}
}

func TestDisplayNotificationErrors(t *testing.T) {
	dir := t.TempDir()
	failing := filepath.Join(dir, "failing")
	writeScript(t, failing, "#!/bin/sh\ncat > /dev/null\n"+
		`echo '0:10: syntax error: Expected end of line but found "secret-project". (-2741)' >&2`+"\nexit 1\n")
	tests := []struct {
		name            string
		path            string
		wantNoOsascript bool
		wantIn          string
		wantNotIn       string
	}{
		{name: "a missing osascript is ErrNoOsascript", path: filepath.Join(dir, "missing"), wantNoOsascript: true},
		{
			name:      "a failing osascript gives its status and error number but not its message",
			path:      failing,
			wantIn:    "status 1, AppleScript error -2741",
			wantNotIn: "secret-project",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			err := Osascript{Path: tt.path}.DisplayNotification(ctx, "pairtime", "s", "52 min on secret-project.")
			if err == nil {
				t.Fatal("DisplayNotification() = nil, want an error")
			}
			if errors.Is(err, ErrNoOsascript) != tt.wantNoOsascript {
				t.Errorf("DisplayNotification() = %v, want ErrNoOsascript: %v", err, tt.wantNoOsascript)
			}
			if !strings.Contains(err.Error(), tt.wantIn) {
				t.Errorf("DisplayNotification() = %q, want it to contain %q", err, tt.wantIn)
			}
			if tt.wantNotIn != "" && strings.Contains(err.Error(), tt.wantNotIn) {
				t.Errorf("DisplayNotification() = %q repeats osascript's message", err)
			}
		})
	}
}

func writeScript(t *testing.T, name, body string) {
	t.Helper()
	if err := os.WriteFile(name, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestAppleScriptErrorNumber(t *testing.T) {
	tests := []struct {
		name   string
		stderr string
		want   string
	}{
		{name: "the number at the end is kept and the message isn't", stderr: "0:42: execution error: Not authorized to send Apple events. (-1743)\n", want: ", AppleScript error -1743"},
		{name: "a positive number is kept", stderr: "execution error: something (2)", want: ", AppleScript error 2"},
		{name: "a message without a number gives nothing", stderr: "osascript: couldn't read the script", want: ""},
		{name: "words in the last parentheses give nothing", stderr: "found (secret-project)", want: ""},
		{name: "empty parentheses give nothing", stderr: "error ()", want: ""},
		{name: "no output gives nothing", stderr: "", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := appleScriptErrorNumber(tt.stderr); got != tt.want {
				t.Errorf("appleScriptErrorNumber(%q) = %q, want %q", tt.stderr, got, tt.want)
			}
		})
	}
}
