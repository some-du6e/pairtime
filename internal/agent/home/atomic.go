// SPDX-License-Identifier: AGPL-3.0-or-later

package home

import (
	"os"
	"path/filepath"
)

// WriteFile replaces the file name with data so that a reader, or the
// agent starting again after a crash, finds the old contents or the new
// ones and never a mix (DESIGN 3.5): it writes a temporary file beside
// name, flushes it to disk and renames it over name. The new file is
// readable by its owner only (mode 0600) where the OS has file modes; on
// Windows it inherits its directory's ACL, which is user-only in the
// directories Create makes.
func WriteFile(name string, data []byte) error {
	return writeFile(name, data, false)
}

// WritePrivate is WriteFile for a file that holds a secret, such as the
// bearer token in port.json. On Windows the file gets a protected DACL that
// lets only the current user open it, whatever its directory allows.
func WritePrivate(name string, data []byte) error {
	return writeFile(name, data, true)
}

// ReadFile reads the file name, as os.ReadFile does. On Windows it tries
// again for a moment while another process replaces the file, as WriteFile
// in another pairtime process does: its rename holds the file open with
// delete access, which makes an open fail with access denied or a sharing
// violation (readFile).
func ReadFile(name string) ([]byte, error) {
	return readFile(name)
}

// SyncDir flushes dir, so a file just made in it, or renamed into it, keeps
// its name through a power cut. It is best effort, and does nothing on
// Windows, which journals the names in a folder (syncDir).
func SyncDir(dir string) { syncDir(dir) }

func writeFile(name string, data []byte, private bool) (err error) {
	dir := filepath.Dir(name)
	f, err := createTemp(dir, "."+filepath.Base(name)+".tmp-", private)
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer func() {
		if err != nil {
			f.Close()
			os.Remove(tmp)
		}
	}()
	if _, err = f.Write(data); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = rename(tmp, name); err != nil {
		return err
	}
	syncDir(dir)
	return nil
}
