// SPDX-License-Identifier: AGPL-3.0-or-later

package redact

import (
	"testing"
)

func TestKnownValues(t *testing.T) {
	teen := Known{
		Accounts:  []string{"maya"},
		Homes:     []string{"/home/maya/"},
		Hosts:     []string{"Mayas-Laptop"},
		GitNames:  []string{"Maya Kowalski"},
		GitEmails: []string{"maya.k@example.com"},
		GitHub:    []string{"mkowal"},
		HCA:       []string{"maya_k"},
		Private:   []string{"secret-robot"},
		Deny:      []string{"Jonah", "Lincoln High"},
	}
	windows := Known{Homes: []string{`C:\Users\Maya Chen`}}
	spaced := Known{Accounts: []string{"Maya Chen"}, Homes: []string{"/home/maya_k"}, Private: []string{"my_app"}}
	accented := Known{GitNames: []string{"José Núñez"}}
	generic := Known{
		Accounts: []string{"Owner", "al"},
		Hosts:    []string{"localhost"},
		Homes:    []string{"/", `C:\`},
		Private:  []string{"app"},
		Deny:     []string{"app", "Al"},
	}
	tests := []struct {
		name  string
		known Known
		text  string
		want  string
	}{
		{"the home directory goes and the rest of the path stays", teen,
			"open /home/maya/proj/main.go", "open [HOME]/proj/main.go"},
		{"the account name goes where other text joins it with punctuation", teen,
			"user maya_dev and x-maya-proj", "user [ACCOUNT]_dev and x-[ACCOUNT]-proj"},
		{"the home goes as Claude Code names a project folder after it", teen,
			"~/.claude/projects/-home-maya-proj/1.jsonl", "~/.claude/projects/[HOME]-proj/1.jsonl"},
		{"a value inside a longer word stays", teen,
			"the mayan calendar", "the mayan calendar"},
		{"matching ignores case", teen,
			"MAYA and Maya", "[ACCOUNT] and [ACCOUNT]"},
		{"the hostname goes and its domain stays", teen,
			"ssh mayas-laptop.local", "ssh [HOST].local"},
		{"git name and email win over the account and email inside them", teen,
			"Signed-off-by: Maya Kowalski <maya.k@example.com>", "Signed-off-by: [GIT_NAME] <[GIT_EMAIL]>"},
		{"GitHub and HCA usernames go", teen,
			"github.com/mkowal/robot, @maya_k on Slack", "github.com/[GITHUB]/robot, @[HCA] on Slack"},
		{"a private repo name goes", teen,
			"push secret-robot to origin", "push [PRIVATE] to origin"},
		{"the deny list catches names regex can't", teen,
			"Jonah from lincoln high said hi", "[DENY] from [DENY] said hi"},
		{"a Windows home goes with backslashes", windows,
			`cd C:\Users\Maya Chen\proj`, `cd [HOME]\proj`},
		{"a Windows home goes inside a JSON string", windows,
			`{"cwd":"C:\\Users\\Maya Chen\\proj"}`, `{"cwd":"[HOME]\\proj"}`},
		{"a Windows home goes with forward slashes", windows,
			"C:/Users/Maya Chen/proj", "[HOME]/proj"},
		{"a Windows home goes as Git Bash writes it", windows,
			"/c/Users/Maya Chen/proj", "[HOME]/proj"},
		{"a Windows home goes as WSL writes it", windows,
			"/mnt/c/Users/Maya Chen/proj", "[HOME]/proj"},
		{"a Windows home goes in a file URL", windows,
			"file:///C:/Users/Maya%20Chen/proj", "file:///[HOME]/proj"},
		{"a Windows home goes in a VS Code file URI, with its drive in lower case and its colon escaped", windows,
			"file:///c%3A/Users/Maya%20Chen/robot", "file://[HOME]/robot"},
		{"a Windows home goes as Claude Code names a project folder after it", windows,
			`C:\Users\Maya Chen\.claude\projects\C--Users-Maya-Chen-robot\1.jsonl`, `[HOME]\.claude\projects\[HOME]-robot\1.jsonl`},
		{"a Windows home goes in JSON that was put in a JSON string", windows,
			`{"nested":"{\"cwd\":\"C:\\\\Users\\\\Maya Chen\\\\robot\"}"}`, `{"nested":"{\"cwd\":\"[HOME]\\\\robot\"}"}`},
		{"a Windows home goes URL-escaped in a query string", windows,
			"?folder=C%3A%5CUsers%5CMaya+Chen", "?folder=[HOME]"},
		{"an account with a space goes as a project folder name spells it", spaced,
			"projects/-Users-Maya-Chen-robot/", "projects/-Users-[ACCOUNT]-robot/"},
		{"an account with a space goes URL-escaped", spaced,
			"/Users/Maya%20Chen/robot", "/Users/[ACCOUNT]/robot"},
		{"a home with an underscore goes as a project folder name spells it", spaced,
			"projects/-home-maya-k-robot/", "projects/[HOME]-robot/"},
		{"a private folder goes as a project folder name spells it", spaced,
			"projects/-Users-sam-my-app/", "projects/-Users-sam-[PRIVATE]/"},
		{"a name goes as Python's json module escapes it", accented,
			`{"author": "Jos\u00e9 N\u00fa\u00f1ez"}`, `{"author": "[GIT_NAME]"}`},
		{"a name goes as written", accented,
			"by José Núñez", "by [GIT_NAME]"},
		{"generic names and short values are skipped, except on the deny list", generic,
			"the app owner al on localhost at /", "the [DENY] owner [DENY] on localhost at /"},
		{"the account goes at the start of an embedded line of ps output", teen,
			`{"stdout":"USER  PID\nmaya  4242"}`, `{"stdout":"USER  PID\n[ACCOUNT]  4242"}`},
		{"a name goes after a sign-off's escaped line break", teen,
			`"thanks,\nMaya"`, `"thanks,\n[ACCOUNT]"`},
		{"the git name goes after an escaped tab, as in git shortlog", teen,
			`"    42\tMaya Kowalski"`, `"    42\t[GIT_NAME]"`},
		{"the deny list catches a name after an escaped return", teen,
			`"hi\r\nJonah"`, `"hi\r\n[DENY]"`},
		{"the account goes between unicode escapes, as Go writes angle brackets", teen,
			"\x5cu003cmaya\x5cu003e", "\x5cu003c[ACCOUNT]\x5cu003e"},
		{"a value after a doubled backslash and an n goes too, as in JSON inside JSON", teen,
			`"a\\nmaya"`, `"a\\n[ACCOUNT]"`},
		{"a letter after a backslash that no JSON escape uses still joins the word", teen,
			`C:\xmaya`, `C:\xmaya`},
		{"an escaped name goes after an escaped line break", accented,
			"{\"by\": \"x\x5cnJos\x5cu00e9 N\x5cu00fa\x5cu00f1ez\"}", `{"by": "x\n[GIT_NAME]"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := mustNew(t, tt.known)
			if got := Render(tt.text, r.Find(tt.text)); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRespell(t *testing.T) {
	const home, capture = `C:\Users\Maya Chen`, `C:\Users\capture`
	tests := []struct {
		name        string
		t           Type
		value       string
		matched     string
		replacement string
		want        string
		ok          bool
	}{
		{"a home written as is takes the replacement as is", TypeHome, home, home, capture, capture, true},
		{"a JSON-escaped home takes a JSON-escaped replacement", TypeHome, home, `C:\\Users\\Maya Chen`, capture, `C:\\Users\\capture`, true},
		{"a home escaped twice takes a replacement escaped twice", TypeHome, home, `C:\\\\Users\\\\Maya Chen`, capture, `C:\\\\Users\\\\capture`, true},
		{"a project folder's dashes carry over", TypeHome, home, "C--Users-Maya-Chen", capture, "C--Users-capture", true},
		{"VS Code's file URI path carries over, lower-case drive and all", TypeHome, home, "/c%3A/Users/Maya%20Chen", capture, "/c%3A/Users/capture", true},
		{"a Git Bash path carries over", TypeHome, home, "/c/Users/Maya Chen", capture, "/c/Users/capture", true},
		{"a home given with forward slashes still matches backslashes", TypeHome, "C:/Users/Maya Chen", home, "C:/Users/capture", capture, true},
		{"case doesn't matter, as it doesn't to Find", TypeHome, home, `c:\users\MAYA CHEN`, capture, capture, true},
		{"an account in a URL takes its escapes", TypeAccount, "Maya Chen", "Maya%20Chen", "capture", "capture", true},
		{"a name in Python's JSON takes ASCII escapes", TypeGitName, "José Núñez", "Jos\x5cu00e9 N\x5cu00fa\x5cu00f1ez", "Capture User", "Capture User", true},
		{"text that's more than one spelling isn't respelled", TypeHome, "/home/maya", "/home/maya-robot", "/home/capture", "", false},
		{"a value Find skips isn't respelled", TypeAccount, "user", "user", "capture", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := Respell(tt.t, tt.value, tt.matched, tt.replacement)
			if got != tt.want || ok != tt.ok {
				t.Errorf("Respell(%s, %q, %q, %q) = %q, %v; want %q, %v", tt.t, tt.value, tt.matched, tt.replacement, got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestJSONEscape(t *testing.T) {
	tests := []struct {
		name  string
		in    string
		ascii bool
		want  string
	}{
		{"backslashes and quotes are escaped", `C:\a "b"`, false, `C:\\a \"b\"`},
		{"control characters become \\u escapes", "a\tb", false, `a\u0009b`},
		{"non-ASCII stays unless asked", "é", false, "é"},
		{"non-ASCII becomes \\u escapes when asked", "é", true, `\u00e9`},
		{"characters past the BMP become surrogate pairs", "😀", true, `\ud83d\ude00`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := jsonEscape(tt.in, tt.ascii); got != tt.want {
				t.Errorf("jsonEscape(%q, %v) = %q, want %q", tt.in, tt.ascii, got, tt.want)
			}
		})
	}
}
