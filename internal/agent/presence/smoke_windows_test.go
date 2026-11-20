// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build windows

package presence

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/agent/sched"
	"github.com/some-du6e/pairtime/pkg/event"
)

// TestSmokeWindows is WP-OS1's minute on windows-latest: the sampler on the
// agent's scheduler and the real Windows sources (GetLastInputInfo, the
// session's lock state, the suspend and lock watches), in real time. No
// test may move focus or input on a hosted runner (JE must-fix 11), so the
// project context is a fake: a prompt hook that starts a turn.
func TestSmokeWindows(t *testing.T) {
	if testing.Short() {
		t.Skip("the smoke test takes a minute")
	}
	tests := []struct {
		name          string
		before, after time.Duration
	}{
		{
			name:   "on Windows nothing is sampled until a fake context goes live, and then samples land on slot boundaries",
			before: 20 * time.Second,
			after:  40 * time.Second,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sch := sched.New()
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan struct{})
			go func() {
				sch.Run(ctx)
				close(done)
			}()
			defer func() {
				cancel()
				<-done
			}()

			var mu sync.Mutex
			var recs []Record
			var problems []string
			s := New(Options{
				Sched: sch,
				OS:    ThisOS(),
				Record: func(r Record) {
					mu.Lock()
					defer mu.Unlock()
					recs = append(recs, r)
				},
				Problem: func(msg string) {
					mu.Lock()
					defer mu.Unlock()
					problems = append(problems, msg)
				},
			})
			s.Start()
			defer s.Stop()

			time.Sleep(tt.before)
			mu.Lock()
			early := len(recs)
			mu.Unlock()
			if early != 0 {
				t.Fatalf("%d records before any project context, want none", early)
			}
			live := time.Now()
			s.Hook("s_smoke", event.KindPromptSubmit)
			time.Sleep(tt.after)
			s.Stop()

			mu.Lock()
			defer mu.Unlock()
			if n := len(problems); n > 0 && problems[n-1] != "" {
				t.Errorf("the sampler reports problems: %q", problems[n-1])
			}
			// One sample at once, then one on each 15 s boundary from a
			// second after it: two or three in 40 s.
			if n := len(recs); n < 3 || n > 4 {
				t.Fatalf("%d records in %v of a live context, want 3 or 4", n, tt.after)
			}
			for i, r := range recs {
				p, ok := r.Data.(*event.OSPresence)
				if !ok {
					t.Errorf("record %d is a %T, want *event.OSPresence", i, r.Data)
					continue
				}
				if err := p.Validate(); err != nil {
					t.Errorf("record %d: %v", i, err)
				}
				if p.IdleKeysS != nil {
					t.Errorf("record %d has idle_keys_s %v; Windows has none without a keyboard listener", i, *p.IdleKeysS)
				}
				if i == 0 {
					if d := r.At.Sub(live); d < 0 || d > time.Second {
						t.Errorf("the first sample came %v after the context went live, want at once", d)
					}
					continue
				}
				// A timer may fire a moment late, or up to the scheduler's
				// 50 ms early.
				if off := time.Duration(r.At.UnixNano() % int64(SampleInterval)); off > 500*time.Millisecond && off < SampleInterval-100*time.Millisecond {
					t.Errorf("sample %d at %v is %v past a slot boundary", i, r.At.UTC(), off)
				}
			}
		})
	}
}
