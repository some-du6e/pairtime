// SPDX-License-Identifier: AGPL-3.0-or-later

package redact

import (
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
	text := "```\ntoken = \"" + ghp + "\"\n```"
	r := mustNew(t, Known{})
	start := strings.Index(text, ghp)
	found := false
	for _, s := range r.Find(text) {
		if s.Type == TypeSecret && s.Rule == "github-pat" && s.Start == start && s.End == start+len(ghp) {
			found = true
		}
	}
	if !found {
		t.Errorf("Find(%q) has no github-pat span at %d", text, start)
	}
	restored := Render(text, r.Find(text)) // the block added back, its secret still out
	if strings.Contains(restored, ghp) || !strings.Contains(restored, "token = \"[SECRET]\"") {
		t.Errorf("restored block = %q", restored)
	}
}

func TestNewLoadsRulesOnce(t *testing.T) {
	a, b := mustNew(t, Known{}), mustNew(t, testTeen)
	if a.rules != b.rules {
		t.Error("two Redactors loaded the rules twice")
	}
}
