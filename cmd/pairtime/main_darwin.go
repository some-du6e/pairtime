// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build darwin

package main

import "runtime"

// macOS delivers screen lock and app switch notifications only on the
// process's main thread (internal/platform/darwin; TestFindingRunLoop). Go
// runs init functions on the main goroutine while it is still on the main
// thread, and locking it here keeps it there, so that `pairtime agent` can
// lend the main thread to the agent's darwin.Loop (agent_darwin.go). The
// other commands don't mind where their main goroutine runs.
func init() { runtime.LockOSThread() }
