// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build darwin

package notifier

import (
	"context"
	"errors"
	"fmt"

	"github.com/some-du6e/pairtime/internal/platform/darwin"
)

// Platform returns this OS's backend: osascript's display notification, one
// short process per notification and never a polling loop (TE WP4.6;
// CP 3.3). macOS shows the notification as Script Editor's, so the title
// line says pairtime and Title goes in the subtitle. A click on it opens
// Script Editor, and display notification can't attach a URL or an action,
// so on macOS a click can never reach the recap, whatever WP-UI1 serves:
// the coach's text there mustn't promise one. A Mac with no osascript is
// ErrUnavailable. This backend can't tell a locked screen; macOS hides
// notification previews on its lock screen unless the user changed that.
func Platform() Backend {
	return darwinBackend{}
}

type darwinBackend struct {
	o darwin.Osascript
}

// Show returns as soon as osascript has posted the notification, so it
// never calls shown.
func (b darwinBackend) Show(ctx context.Context, m Message, _ func()) error {
	err := b.o.DisplayNotification(ctx, "pairtime", m.Title, m.Body)
	if errors.Is(err, darwin.ErrNoOsascript) {
		return fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	return err
}
