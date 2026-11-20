// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build windows

package focus

import (
	"sync"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/platform/windows"
)

// TestThisOSOnWindows runs the component on the real foreground hook and
// process table of the runner, without moving focus (JE must-fix 11): the
// hook reports the window in front as it starts, and the component either
// records it, with a class from the shared table, or says in Status why it
// can't name it.
func TestThisOSOnWindows(t *testing.T) {
	tests := []struct {
		name string
	}{
		{"on Windows the window in front at the start is recorded, or Status says why it isn't"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var mu sync.Mutex
			var recs []Record
			var problems []string
			w := New(Options{
				OS:      ThisOS(),
				Record:  func(r Record) { mu.Lock(); recs = append(recs, r); mu.Unlock() },
				Problem: func(m string) { mu.Lock(); problems = append(problems, m); mu.Unlock() },
			})
			w.Start()
			defer w.Stop()
			deadline := time.Now().Add(5 * time.Second)
			for w.Status().Changes == 0 && time.Now().Before(deadline) {
				time.Sleep(10 * time.Millisecond)
			}
			w.sync()
			st := w.Status()
			t.Logf("status: %+v", st)
			mu.Lock()
			defer mu.Unlock()
			if n := len(problems); n > 0 && problems[n-1] != "" {
				t.Errorf("problems: %q", problems[n-1])
			}
			if st.Changes == 0 {
				t.Fatal("the hook reported no window in front within 5 s")
			}
			switch {
			case st.Unknown == "" && len(recs) != 1:
				t.Errorf("a known window in front gave %d records, want 1", len(recs))
			case st.Unknown != "" && len(recs) != 0:
				t.Errorf("focus is unknown (%s), but %d records were written", st.Unknown, len(recs))
			}
			for _, r := range recs {
				if err := r.Data.Validate(); err != nil {
					t.Errorf("the record doesn't validate: %v", err)
				}
				if r.Data.AppHash != "" {
					t.Errorf("app_hash %q without a secret", r.Data.AppHash)
				}
			}
		})
	}
}

func TestChangeOf(t *testing.T) {
	at := time.Date(2026, 11, 20, 14, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		f    windows.Foreground
		want Change
	}{
		{"a window in front is its process", windows.Foreground{At: at, Window: 0x10, PID: 4242}, Change{At: at, PID: 4242}},
		{"another desktop with the input is focus pairtime can't name", windows.Foreground{At: at, Unknown: windows.UnknownOtherDesktop}, Change{At: at, Unknown: windows.UnknownOtherDesktop}},
		{"a window without a process is focus pairtime can't name", windows.Foreground{At: at, Window: 0x10}, Change{At: at, Unknown: windows.UnknownNoProcess}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := changeOf(tt.f); got != tt.want {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}
