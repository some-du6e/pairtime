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
// line says pairtime and Title goes in the subtitle. A Mac with no
// osascript is ErrUnavailable. This backend can't tell a locked screen;
// macOS hides notification previews on its lock screen unless the user
// changed that.
//
// A click on the notification opens Script Editor, the app it belongs to.
// display notification takes no URL and no action, so on this backend a
// click can never open the recap, whatever URL the dashboard (WP-UI1)
// offers, and the coach's text mustn't invite one on macOS (WP-CO1).
func Platform() Backend {
	return darwinBackend{}
}

type darwinBackend struct {
	o darwin.Osascript
}

func (b darwinBackend) Show(ctx context.Context, m Message) error {
	err := b.o.DisplayNotification(ctx, "pairtime", m.Title, m.Body)
	if errors.Is(err, darwin.ErrNoOsascript) {
		return fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	return err
}
