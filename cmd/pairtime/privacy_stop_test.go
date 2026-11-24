// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"strings"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/agent/uploader"
)

// TestStopLines checks what privacy show says while the batch roots stop at
// a batch sealed in local-only mode: the batch, when it was sealed and why,
// as doctor's line gives them (uploader's stoppedProblem), when its seal
// frame says why, and the general text otherwise.
func TestStopLines(t *testing.T) {
	sealed := time.Date(2026, 11, 23, 4, 22, 0, 0, time.UTC)
	cause := "it holds an agent.output of 2026-11-18 09:00 UTC that reached pairtime after it had sealed a batch online since"
	tests := []struct {
		name    string
		p       uploader.Preview
		want    string
		notWant string
	}{
		{"a stop whose seal frame says why names the batch, when it was sealed and why, then how to upload again",
			uploader.Preview{Stopped: 3, Cause: cause, StoppedAt: sealed},
			"Uploads stop at batch 3, sealed in local-only mode at 2026-11-23 04:22 UTC because " + cause + ".\n" + uploadsStopped, ""},
		{"one an older version sealed says so", uploader.Preview{Stopped: 3, NoMode: true}, uploadsStoppedNoMode, "batch 3"},
		{"one whose seal frame doesn't say why gives the general text", uploader.Preview{Stopped: 3}, uploadsStopped, "batch 3"},
		{"no stop says nothing", uploader.Preview{}, "", "stop"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stopLines(tt.p)
			if got != tt.want {
				t.Errorf("stopLines = %q, want %q", got, tt.want)
			}
			if tt.notWant != "" && strings.Contains(got, tt.notWant) {
				t.Errorf("stopLines = %q, which mustn't say %q", got, tt.notWant)
			}
		})
	}
}
