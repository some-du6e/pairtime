// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build !windows

package tailers

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/adapters"
	"github.com/some-du6e/pairtime/pkg/event"
)

// TestFIFOHoldsNothingUp checks that a FIFO where a log file was looked
// for, as a hook call's transcript_path can name one, blocks neither the
// open nor the tailer: os.Open of a FIFO waits for a writer.
func TestFIFOHoldsNothingUp(t *testing.T) {
	cases := []struct {
		name string
		run  func(t *testing.T, r *rig, path string)
	}{
		{"openLog refuses it at once", func(t *testing.T, _ *rig, path string) {
			if fh, err := openLog(path); err == nil {
				fh.Close()
				t.Error("openLog opened a FIFO")
			}
		}},
		{"a hint that names it reads nothing, and the tailer goes on", func(t *testing.T, r *rig, path string) {
			r.step(0, false)
			r.tl.Hint(event.HarnessClaudeCode, path, adapters.LogRoot{})
			r.step(time.Second, true)
			if len(r.got) != 0 {
				t.Errorf("handed over %v", r.texts(0))
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t)
			if err := os.MkdirAll(filepath.Join(r.dir, "p"), 0o700); err != nil {
				t.Fatal(err)
			}
			path := r.path("s.jsonl")
			if err := syscall.Mkfifo(path, 0o600); err != nil {
				t.Skip(err)
			}
			done := make(chan struct{})
			go func() {
				defer close(done)
				tc.run(t, r, path)
			}()
			select {
			case <-done:
			case <-time.After(10 * time.Second):
				t.Fatal("blocked on the FIFO")
			}
		})
	}
}
