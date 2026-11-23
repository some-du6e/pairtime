// SPDX-License-Identifier: AGPL-3.0-or-later

package install

import (
	"strings"
	"testing"
)

func TestMaskSettings(t *testing.T) {
	pairtime := `{"hooks": [{"type": "command", "command": "/home/teen/.local/bin/pairtime", "args": ["hook", "--harness", "claude_code", "--event", "Stop"]}]}`
	tests := []struct {
		name string
		src  string
		want string
	}{
		{"a string value is masked and its member name stays",
			"{\n  \"env\": {\n    \"TOKEN\": \"sk-secret\"\n  }\n}\n",
			"{\n  \"env\": {\n    \"TOKEN\": \"***\"\n  }\n}\n"},
		{"numbers, true, false, null and empty strings stay",
			`{"n": 3, "t": true, "f": false, "z": null, "e": ""}`,
			`{"n": 3, "t": true, "f": false, "z": null, "e": ""}`},
		{"pairtime's handlers and the matcher of a group of its own stay, and another tool's command is masked",
			`{"hooks": {"Stop": [{"matcher": "", "hooks": [{"type": "command", "command": "curl https://ntfy.sh/topic"}]}, ` + pairtime + `]}}`,
			`{"hooks": {"Stop": [{"matcher": "", "hooks": [{"type": "***", "command": "***"}]}, ` + pairtime + `]}}`},
		{"in a pairtime handler, a string the teen added, an argument pairtime doesn't write and a matcher it doesn't give the event are masked, and pairtime's own strings stay",
			`{"hooks": {"PostToolUse": [{"matcher": "token-1", "hooks": [{"type": "command", "command": "/home/teen/.local/bin/pairtime", "args": ["hook", "--harness", "claude_code", "--event", "PostToolUse", "token-2"], "statusMessage": "token-3"}]}, ` +
				`{"matcher": "Edit|Write|Bash", "hooks": [{"type": "command", "command": "/home/teen/.local/bin/pairtime", "args": ["hook", "--harness", "claude_code", "--event", "PostToolUse"]}]}]}}`,
			`{"hooks": {"PostToolUse": [{"matcher": "***", "hooks": [{"type": "command", "command": "/home/teen/.local/bin/pairtime", "args": ["hook", "--harness", "claude_code", "--event", "PostToolUse", "***"], "statusMessage": "***"}]}, ` +
				`{"matcher": "Edit|Write|Bash", "hooks": [{"type": "command", "command": "/home/teen/.local/bin/pairtime", "args": ["hook", "--harness", "claude_code", "--event", "PostToolUse"]}]}]}}`},
		{"a line comment keeps its slashes and the space after them",
			"{\n  // token sk-secret\n  \"a\": 1\n}",
			"{\n  // ***\n  \"a\": 1\n}"},
		{"a block comment keeps its lines and its ends",
			"{ /* one\n   two */ \"a\": 1 }",
			"{ /* ***\n   *** */ \"a\": 1 }"},
		{"CRLF line endings stay, after a comment too",
			"{\r\n  // secret\r\n  \"a\": \"b\"\r\n}\r\n",
			"{\r\n  // ***\r\n  \"a\": \"***\"\r\n}\r\n"},
		{"a file of comments only is masked line by line",
			"// secret\n",
			"// ***\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := string(maskSettings([]byte(tt.src))); got != tt.want {
				t.Errorf("maskSettings gives\n%q\nwant\n%q", got, tt.want)
			}
		})
	}
}

func TestEscapeControls(t *testing.T) {
	tests := []struct {
		name string
		s    string
		want string
	}{
		{"an escape sequence that clears the screen is written out", "a\x1b[2Jb", "a<U+001B>[2Jb"},
		{"a bell is written out", "\a", "<U+0007>"},
		{"a carriage return that would overwrite the line is written out", "shown\rhidden", "shown<U+000D>hidden"},
		{"a CRLF line ending stays", "a\r\nb\n", "a\r\nb\n"},
		{"a tab stays", "a\tb", "a\tb"},
		{"DEL is written out", "a\x7f", "a<U+007F>"},
		{"a C1 control is written out", "a\xc2\x9b2J", "a<U+009B>2J"},
		{"a right-to-left override is written out", "a\xe2\x80\xaeb", "a<U+202E>b"},
		{"a byte that isn't UTF-8 is written out", "a\xffb", "a<FF>b"},
		{"UTF-8 text stays", "日本語 é", "日本語 é"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := escapeControls(tt.s); got != tt.want {
				t.Errorf("escapeControls(%q) = %q, want %q", tt.s, got, tt.want)
			}
		})
	}
}

func TestPlanDiffHidesWhatIsntPairtimes(t *testing.T) {
	tests := []struct {
		name     string
		settings string
		want     string // in the install diff
	}{
		{"a token and a base URL in the env block, right above where hooks go, aren't shown",
			"{\n  \"env\": {\n    \"ANTHROPIC_AUTH_TOKEN\": \"sk-or-v1-SECRET\",\n    \"ANTHROPIC_BASE_URL\": \"https://SECRET.example/v1\"\n  }\n}\n",
			// Only the masked context lines: where the diff puts the
			// closing brace of env depends on how it aligns the hunk.
			"   \"env\": {\n     \"ANTHROPIC_AUTH_TOKEN\": \"***\",\n     \"ANTHROPIC_BASE_URL\": \"***\"\n"},
		{"a webhook URL in another tool's Stop hook, right above pairtime's, isn't shown",
			"{\n  \"hooks\": {\n    \"Stop\": [\n      {\n        \"hooks\": [\n          {\n            \"type\": \"command\",\n            \"command\": \"curl -d done https://ntfy.sh/SECRET\"\n          }\n        ]\n      }\n    ]\n  }\n}\n",
			"             \"command\": \"***\"\n"},
		{"a one-line file's token isn't shown on either side",
			"{\"env\": {\"TOKEN\": \"SECRET\"}}\n",
			"-{\"env\": {\"TOKEN\": \"***\"}}\n"},
		{"a comment's text and the control characters in it aren't shown",
			"{\n  // SECRET \x1b[2J\x07\n  \"model\": \"opus\"\n}\n",
			"   // ***\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := testEnv(t, "linux", nil)
			items := linuxItems(t, env, []byte(tt.settings))
			in := newInstaller(t)
			p, err := in.PlanInstall(items)
			if err != nil {
				t.Fatal(err)
			}
			d := p.Diff()
			if strings.Contains(d, "SECRET") || strings.ContainsAny(d, "\x1b\x07") {
				t.Errorf("the install diff shows what isn't pairtime's:\n%s", d)
			}
			if !strings.Contains(d, tt.want) {
				t.Errorf("the install diff\n%s\ndoesn't hold\n%s", d, tt.want)
			}
			if !strings.Contains(d, `"command": "`+env.Exe+`"`) {
				t.Errorf("the install diff doesn't show pairtime's own hooks:\n%s", d)
			}
			if err := p.Apply(); err != nil {
				t.Fatal(err)
			}
			u, err := in.PlanUninstall()
			if err != nil {
				t.Fatal(err)
			}
			defer u.Close()
			if d := u.Diff(); strings.Contains(d, "SECRET") || !strings.Contains(d, "--harness") {
				t.Errorf("the uninstall diff shows what isn't pairtime's, or not pairtime's hooks:\n%s", d)
			}
		})
	}
}

func TestFileDiff(t *testing.T) {
	tests := []struct {
		name          string
		before, after []byte
		want          string
	}{
		{"a file that isn't there yet is /dev/null on the left", nil, []byte("a\n"),
			"--- /dev/null\n+++ /home/teen/f\n@@ -0,0 +1 @@\n+a\n"},
		{"a change in a file names it on both sides", []byte("a\nb\n"), []byte("a\nc\n"),
			"--- /home/teen/f\n+++ /home/teen/f\n@@ -1,2 +1,2 @@\n a\n-b\n+c\n"},
		{"a file that doesn't change has no diff", []byte("a\n"), []byte("a\n"), ""},
		{"what a terminal acts on is written out, and CRLF endings stay", []byte("a\r\n"), []byte("a\x1b[2J\r\n"),
			"--- /home/teen/f\n+++ /home/teen/f\n@@ -1 +1 @@\n-a\r\n+a<U+001B>[2J\r\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := FileDiff("/home/teen/f", tt.before, tt.after); got != tt.want {
				t.Errorf("FileDiff = %q, want %q", got, tt.want)
			}
		})
	}
}
