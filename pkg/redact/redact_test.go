// SPDX-License-Identifier: AGPL-3.0-or-later

package redact

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

// A synthetic teen and a synthetic prompt; nothing here is real.
var testTeen = Known{
	Accounts:  []string{"maya"},
	Homes:     []string{"/Users/maya"},
	GitEmails: []string{"maya.k@example.com"},
	Deny:      []string{"Jonah"},
}

func TestFindPipeline(t *testing.T) {
	stripe := fakeKey("sk_"+"live_", "Qw81Zx4LmN", 24, "")
	tests := []struct {
		name  string
		text  string
		code  bool // also collapse code blocks, as for an agent message
		want  string
		types []Type // the merged span types, in order
	}{
		{"a prompt loses its known values, its secret and its card number",
			"Jonah says /Users/maya/bot fails with " + stripe + "; my card 4111 1111 1111 1111 works",
			false,
			"[DENY] says [HOME]/bot fails with [SECRET]; my card [CARD] works",
			[]Type{TypeDeny, TypeHome, TypeSecret, TypeCard}},
		{"an agent message also loses its code blocks, secret inside or not",
			"Set it like this:\n```sh\nexport STRIPE_KEY=" + stripe + "\n```\nThen mail maya.k@example.com.",
			true,
			"Set it like this:\n[CODE sh: 1 line]\nThen mail [GIT_EMAIL].",
			[]Type{TypeCode, TypeGitEmail}},
		{"text with nothing to remove comes back whole",
			"Why does my loop run forever when the list is empty?",
			false,
			"Why does my loop run forever when the list is empty?",
			nil},
	}
	r := mustNew(t, testTeen)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spans := r.Find(tt.text)
			if tt.code {
				spans = append(spans, CodeBlocks(tt.text)...)
			}
			got := Render(tt.text, spans)
			if got != tt.want {
				t.Errorf("Render = %q, want %q", got, tt.want)
			}
			var types []Type
			for _, s := range Merge(spans) {
				types = append(types, s.Type)
			}
			if strings.Join(typeNames(types), ",") != strings.Join(typeNames(tt.types), ",") {
				t.Errorf("merged types = %v, want %v", types, tt.types)
			}
			// Rendering is stable: a second pass over the output finds
			// nothing new, so scrubbing a scrubbed file changes nothing.
			if again := Render(got, r.Find(got)); again != got {
				t.Errorf("a second pass changed %q to %q", got, again)
			}
		})
	}
}

// TestFindInJSON covers personal data in raw JSON captures, where the byte
// before a value at the start of an embedded line is the n of \n. Each
// result must still be JSON.
func TestFindInJSON(t *testing.T) {
	tests := []struct {
		name string
		text string
		want string
	}{
		{"a card number at the start of an embedded line goes", `{"text":"card:\n4111 1111 1111 1111"}`, `{"text":"card:\n[CARD]"}`},
		{"an SSN at the start of an embedded line goes", `{"text":"ssn:\n219-09-9999"}`, `{"text":"ssn:\n[US_SSN]"}`},
		{"an email address after an escaped line break goes and the escape stays", `{"text":"hi\nbob@westfield.edu"}`, `{"text":"hi\n[EMAIL]"}`},
		{"an email address with an escaped letter goes whole", "{\"from\":\"jos\x5cu00e9@westfield.edu\"}", `{"from":"[EMAIL]"}`},
	}
	r := mustNew(t, Known{})
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Render(tt.text, r.Find(tt.text))
			if got != tt.want {
				t.Errorf("Render = %q, want %q", got, tt.want)
			}
			if !json.Valid([]byte(got)) {
				t.Errorf("Render = %q, which isn't JSON any more", got)
			}
		})
	}
}

func typeNames(ts []Type) []string {
	out := make([]string, len(ts))
	for i, t := range ts {
		out[i] = string(t)
	}
	return out
}

// TestFindKeepsInnerSecrets checks what lets the teen restore a code block:
// Find reports the secret inside it on its own, with its rule.
func TestFindKeepsInnerSecrets(t *testing.T) {
	ghp := fakeKey("gh"+"p_", "Zx81Qw4LmN", 36, "")
	tests := []struct {
		name     string
		text     string
		restored string // the text with Find's spans removed and no code collapse
	}{
		{"a secret inside a code block is its own span", "```\ntoken = \"" + ghp + "\"\n```", "```\ntoken = \"[SECRET]\"\n```"},
		{"a secret inside an indented block in a list is its own span", "1. Run:\n   ```sh\n   gh auth login --with-token " + ghp + "\n   ```",
			"1. Run:\n   ```sh\n   gh auth login --with-token [SECRET]\n   ```"},
	}
	r := mustNew(t, Known{})
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			start := strings.Index(tt.text, ghp)
			want := Span{Start: start, End: start + len(ghp), Type: TypeSecret, Rule: "github-pat"}
			spans := r.Find(tt.text)
			if !slices.Contains(spans, want) {
				t.Errorf("Find = %v, want it to contain %v", spans, want)
			}
			if got := Render(tt.text, spans); got != tt.restored {
				t.Errorf("with the block restored, Render = %q, want %q", got, tt.restored)
			}
		})
	}
}
