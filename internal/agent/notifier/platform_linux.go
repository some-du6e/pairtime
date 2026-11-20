// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build linux

package notifier

import (
	"context"
	"errors"
	"fmt"

	"github.com/some-du6e/pairtime/internal/platform/linux"
)

// Platform returns this OS's backend: org.freedesktop.Notifications.Notify
// on the session D-Bus, one short connection per notification (TE WP4.6).
// A login with no session bus, or a bus with no notification server, is
// ErrUnavailable. This backend can't tell a locked screen, so the
// desktop's own lock-screen settings decide what a locked screen shows.
func Platform() Backend {
	return linuxBackend{}
}

type linuxBackend struct {
	n linux.Notifications
}

// Show returns as soon as the server has the notification, so it never
// calls shown.
func (b linuxBackend) Show(ctx context.Context, m Message, _ func()) error {
	_, err := b.n.Notify(ctx, m.Title, m.Body)
	if errors.Is(err, linux.ErrNotifyNoBus) || errors.Is(err, linux.ErrNotifyNoServer) {
		return fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	return err
}
