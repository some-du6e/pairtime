// SPDX-License-Identifier: AGPL-3.0-or-later

package home

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestLockFile(t *testing.T) {
	tests := []struct {
		name string
		// held says another holder has the lock as LockFile starts, and
		// releaseAfter, when set, that it lets go after that long.
		held         bool
		releaseAfter time.Duration
		wantErr      error
	}{
		{name: "a lock no one holds is taken at once, and its file stays after the release"},
		{name: "a lock another holder keeps is ErrBusy once the wait ends", held: true, wantErr: ErrBusy},
		{name: "a lock another holder lets go of during the wait is taken then", held: true, releaseAfter: 100 * time.Millisecond},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			name := filepath.Join(t.TempDir(), "config.lock")
			if tt.held {
				// A second LockFile in one process conflicts as one in
				// another does, since the lock belongs to the open file.
				held, err := LockFile(name, 0)
				if err != nil {
					t.Fatal(err)
				}
				var once sync.Once
				let := func() { once.Do(func() { held() }) }
				defer let()
				if tt.releaseAfter > 0 {
					time.AfterFunc(tt.releaseAfter, let)
				}
			}
			release, err := LockFile(name, 500*time.Millisecond)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("LockFile = %v, want %v", err, tt.wantErr)
			}
			if err != nil {
				return
			}
			if err := release(); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(name); err != nil {
				t.Errorf("the lock file is gone after the release: %v", err)
			}
		})
	}
}
