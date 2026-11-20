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
			"user maya_dev and -home-maya-proj", "user [ACCOUNT]_dev and -home-[ACCOUNT]-proj"},
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
		{"a name goes as Python's json module escapes it", accented,
			`{"author": "Jos\u00e9 N\u00fa\u00f1ez"}`, `{"author": "[GIT_NAME]"}`},
		{"a name goes as written", accented,
			"by José Núñez", "by [GIT_NAME]"},
		{"generic names and short values are skipped, except on the deny list", generic,
			"the app owner al on localhost at /", "the [DENY] owner [DENY] on localhost at /"},
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
