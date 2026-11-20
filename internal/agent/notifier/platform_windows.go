// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build windows

package notifier

import (
	"context"
	"errors"
	"fmt"

	"github.com/some-du6e/pairtime/internal/platform/windows"
)

// Platform returns this OS's backend: a Shell_NotifyIcon balloon from a
// transient hidden window and tray icon, which Windows 10 and 11 show as a
// toast (TE WP4.6). It asks Windows first whether this is a time for a
// notification (SHQueryUserNotificationState): a locked screen is ErrAway,
// so the note waits instead of appearing on the lock screen, and a
// full-screen app, a presentation or the quiet hour after a first sign-in
// is ErrBusy. A session with no notification area is ErrUnavailable.
func Platform() Backend {
	return windowsBackend{}
}

type windowsBackend struct{}

// Show returns once the balloon is gone, so it calls shown as the shell
// takes the balloon: the next notification's gap counts from there, not
// from the balloon's end, and a failure after it, such as SetTimer's, is
// about taking the icon down.
func (windowsBackend) Show(ctx context.Context, m Message, shown func()) error {
	return mapWindowsError(windows.ShowBalloon(ctx, m.Title, m.Body, shown))
}

func mapWindowsError(err error) error {
	switch {
	case errors.Is(err, windows.ErrUserAway):
		return fmt.Errorf("%w: %w", ErrAway, err)
	case errors.Is(err, windows.ErrUserBusy):
		return fmt.Errorf("%w: %w", ErrBusy, err)
	case errors.Is(err, windows.ErrNoNotificationArea):
		return fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	return err
}
