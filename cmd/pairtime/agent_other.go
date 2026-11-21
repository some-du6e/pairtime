// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build !darwin

package main

import (
	"context"

	"github.com/some-du6e/pairtime/internal/agent"
)

// runAgentMain runs the agent on the calling goroutine. Only macOS needs
// the main thread for something else (agent_darwin.go).
func runAgentMain(ctx context.Context, opts agent.Options) error {
	return agent.Run(ctx, opts)
}
