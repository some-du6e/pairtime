// SPDX-License-Identifier: AGPL-3.0-or-later

package install

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func TestRemoveLockFile(t *testing.T) {
	tests := []struct {
		name string
		// setup prepares the installer and returns what the case holds,
		// which the test closes after RemoveLockFile.
		setup func(t *testing.T, in *Installer) *Plan
		// wantErr is the error RemoveLockFile must return, nil for none.
		wantErr error
		// stays says whether the lock file must still be there after.
		stays bool
	}{
		{
			name:  "with no lock file there is nothing to delete, and none is made",
			setup: func(*testing.T, *Installer) *Plan { return nil },
		},
		{
			name: "the file of a lock nobody holds is deleted",
			setup: func(t *testing.T, in *Installer) *Plan {
				p, err := in.PlanInstall(nil)
				if err != nil {
					t.Fatal(err)
				}
				p.Close()
				return nil
			},
		},
		{
			name: "the file of a lock a plan holds stays, so a second install can't lock a new one beside it",
			setup: func(t *testing.T, in *Installer) *Plan {
				p, err := in.PlanInstall(nil)
				if err != nil {
					t.Fatal(err)
				}
				return p
			},
			wantErr: ErrBusy,
			stays:   true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := newInstaller(t)
			held := tt.setup(t, in)
			err := in.RemoveLockFile()
			if held != nil {
				held.Close()
			}
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("RemoveLockFile = %v, want %v", err, tt.wantErr)
			}
			_, serr := os.Lstat(in.lockPath())
			if there := serr == nil; there != tt.stays {
				t.Errorf("after RemoveLockFile the lock file is there: %v, want %v (%v)", there, tt.stays, serr)
			}
			if serr != nil && !errors.Is(serr, fs.ErrNotExist) {
				t.Fatal(serr)
			}
			if !tt.stays {
				// The lock can be taken again at once, from a new file.
				p, err := in.PlanInstall(nil)
				if err != nil {
					t.Fatalf("planning after RemoveLockFile: %v", err)
				}
				p.Close()
				if _, err := os.Stat(filepath.Join(filepath.Dir(in.Dir), "install.lock")); err != nil {
					t.Errorf("planning made no new lock file: %v", err)
				}
			}
		})
	}
}
