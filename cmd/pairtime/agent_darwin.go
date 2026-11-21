// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build darwin

package main

import (
	"context"

	"github.com/some-du6e/pairtime/internal/agent"
)

// runAgentMain runs the agent on another goroutine and the macOS
// notification loop on this one, the main goroutine, which main_darwin.go's
// init keeps on the main thread, until the agent stops (WP-OS9). The loop
// gives presence its suspends, resumes, locks and unlocks and focus its app
// activations. If no notification source registers, the loop returns at
// once and the agent runs on without it; GET /v1/status says why.
func runAgentMain(ctx context.Context, opts agent.Options) error {
	mac := agent.NewMacLoop()
	opts.Mac = mac
	loopCtx, stopLoop := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		defer stopLoop()
		done <- agent.Run(ctx, opts)
	}()
	if err := mac.Run(loopCtx); err != nil && opts.Log != nil {
		opts.Log.Warn("the macOS notification loop isn't running, so power events, locks and app switches go unseen", "err", err)
	}
	return <-done
}
