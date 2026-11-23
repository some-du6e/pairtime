// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build windows

package windows

import (
	"os"
	"runtime"
	"testing"
)

// childEnvs are the variables that make this test binary a child process,
// and what each child runs: one whose only GUI threads are the windows a
// test makes, each on a thread that ends (balloonsAlone, lockWatchesAlone),
// or one in background mode, which only the process can leave
// (backgroundChild).
var childEnvs = []struct {
	name string
	run  func(n string) int
}{
	{balloonsAloneEnv, balloonsAlone},
	{lockWatchesAloneEnv, lockWatchesAlone},
	{backgroundEnv, backgroundChild},
}

func init() {
	// Package initialization runs on the main thread, and locking it here
	// keeps it for the main goroutine, which makes no window. Otherwise a
	// window's goroutine could run there, and when a goroutine locked to
	// the main thread ends, Go parks the thread for good instead of ending
	// it (runtime.mexit), which would leave a GUI thread behind.
	for _, c := range childEnvs {
		if os.Getenv(c.name) != "" {
			runtime.LockOSThread()
			return
		}
	}
}

func TestMain(m *testing.M) {
	for _, c := range childEnvs {
		if n := os.Getenv(c.name); n != "" {
			os.Exit(c.run(n))
		}
	}
	os.Exit(m.Run())
}
