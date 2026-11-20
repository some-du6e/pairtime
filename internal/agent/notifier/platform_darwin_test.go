// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build darwin

package notifier

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/platform/darwin"
)

func TestDarwinBackendErrors(t *testing.T) {
	tests := []struct {
		name string
		path string
		want error
	}{
		{name: "a Mac with no osascript can't show notifications", path: filepath.Join(t.TempDir(), "missing"), want: ErrUnavailable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			err := darwinBackend{o: darwin.Osascript{Path: tt.path}}.Show(ctx, Message{Title: Title, Body: "52 min on remind-bot."}, func() {})
			if !errors.Is(err, tt.want) {
				t.Errorf("Show() = %v, want %v", err, tt.want)
			}
		})
	}
}
