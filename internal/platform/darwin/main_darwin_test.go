// SPDX-License-Identifier: AGPL-3.0-or-later

package darwin

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// The test binary's main goroutine stays on the main thread, as the
// agent's will: runtime.LockOSThread in an init function keeps it there
// (see the package doc). TestMain then runs the tests on other goroutines
// and lends the main thread to onMain's callers, so a test can run a Loop
// where macOS delivers screen lock and app switch notifications.
func init() { runtime.LockOSThread() }

var mainCalls = make(chan func())

func TestMain(m *testing.M) {
	code := make(chan int, 1)
	go func() { code <- m.Run() }()
	for {
		select {
		case f := <-mainCalls:
			f()
		case c := <-code:
			os.Exit(c)
		}
	}
}

// onMain runs f on the main thread and returns once f has.
func onMain(f func()) {
	done := make(chan struct{})
	mainCalls <- func() {
		defer close(done)
		f()
	}
	<-done
}

// findingsEnv names the directory where the macOS tests write what they
// found for Karim to read. platform.yml sets it on macos-latest and uploads
// the directory as an artifact (WP-OS3).
const findingsEnv = "PAIRTIME_FINDINGS_DIR"

// writeFinding writes v as indented JSON to name in the findings
// directory, and reports false when the variable isn't set.
func writeFinding(t *testing.T, name string, v any) bool {
	t.Helper()
	dir := os.Getenv(findingsEnv)
	if dir == "" {
		return false
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatalf("encoding the finding: %v", err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), append(b, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	return true
}

func TestMainThread(t *testing.T) {
	tests := []struct {
		name string
		run  func(f func())
		want int32
	}{
		{name: "onMain runs on the process's main thread", run: onMain, want: 1},
		{name: "a test goroutine isn't on the main thread", run: func(f func()) { f() }, want: 0},
	}
	if err := loadCF(); err != nil {
		t.Fatal(err)
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got int32
			tt.run(func() { got = cf.pthreadMainNP() })
			if got != tt.want {
				t.Errorf("pthread_main_np() = %d, want %d", got, tt.want)
			}
		})
	}
}
