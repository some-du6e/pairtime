// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build windows

package notifier

import (
	"errors"
	"testing"

	"github.com/some-du6e/pairtime/internal/platform/windows"
)

func TestMapWindowsError(t *testing.T) {
	other := errors.New("balloon: Shell_NotifyIconW(NIM_MODIFY) refused the balloon")
	tests := []struct {
		name string
		err  error
		want error
	}{
		{name: "a locked screen makes the note wait", err: windows.ErrUserAway, want: ErrAway},
		{name: "a full-screen app or a presentation drops the note", err: windows.ErrUserBusy, want: ErrBusy},
		{name: "a session with no notification area can't show notifications", err: windows.ErrNoNotificationArea, want: ErrUnavailable},
		{name: "any other failure is passed on as it is", err: other, want: other},
		{name: "success stays success", err: nil, want: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mapWindowsError(tt.err)
			if !errors.Is(got, tt.want) {
				t.Errorf("mapWindowsError(%v) = %v, want %v", tt.err, got, tt.want)
			}
			if tt.err != nil && !errors.Is(got, tt.err) {
				t.Errorf("mapWindowsError(%v) = %v, which lost the original error", tt.err, got)
			}
		})
	}
}
