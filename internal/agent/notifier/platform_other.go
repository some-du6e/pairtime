// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build !linux && !darwin && !windows

package notifier

import "context"

// Platform returns a backend for an OS pairtime doesn't ship for, which
// can't show notifications.
func Platform() Backend {
	return noBackend{}
}

type noBackend struct{}

func (noBackend) Show(context.Context, Message, func()) error {
	return ErrUnavailable
}
