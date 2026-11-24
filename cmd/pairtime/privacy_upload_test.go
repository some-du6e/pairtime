// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"strings"
	"testing"

	"github.com/some-du6e/pairtime/internal/agent/uploader"
)

// TestDescribeUpload checks what privacy show says an upload holds, in
// plain words, beside the body it prints: a line for each kind of item it
// carries, and what never goes.
func TestDescribeUpload(t *testing.T) {
	tests := []struct {
		name    string
		p       uploader.Preview
		want    []string // what it says, each in order
		notWant []string
	}{
		{
			name: "an upload with commit records says what a record holds, and that no commit message goes",
			p:    uploader.Preview{JSON: []byte(`{}`), Batches: 1, Commits: 2},
			want: []string{
				"  - 1 batch root:",
				"  - 2 commit records: for each commit, its id and its parents' ids, which project,\n" +
					"    as a key only this computer can match to a folder, when this computer\n" +
					"    first saw it, the dates the commit itself carries, how many files and\n" +
					"    lines it changed, the commits it replaced through an amend or a rebase,\n" +
					"    whether you or your AI agent made it, and the AI tool it credits, if any;\n",
				"and no prompt, answer, code, commit message, file name or path, window title,\ncommand line or keystroke.\n",
			},
		},
		{
			name:    "an upload of one commit record counts it in the singular",
			p:       uploader.Preview{JSON: []byte(`{}`), Commits: 1},
			want:    []string{"  - 1 commit record: for each commit,"},
			notWant: []string{"batch root", "minute"},
		},
		{
			name:    "an upload without commit records says nothing of them",
			p:       uploader.Preview{JSON: []byte(`{}`), Batches: 2, Buckets: 3},
			want:    []string{"  - 2 batch roots:", "  - 3 minutes:", "and no prompt, answer, code, file name or path, window title, command line\nor keystroke.\n"},
			notWant: []string{"commit"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := describeUpload(tt.p, "https://pairtime.example.org/")
			at := 0
			for _, w := range tt.want {
				i := strings.Index(out[at:], w)
				if i < 0 {
					t.Fatalf("privacy show said\n%s\nwant, after what came before, %q", out, w)
				}
				at += i + len(w)
			}
			for _, w := range tt.notWant {
				if strings.Contains(out, w) {
					t.Errorf("privacy show said %q", w)
				}
			}
		})
	}
}
