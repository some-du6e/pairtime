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
// Windows it inherits its directory's ACL.
func WriteFile(name string, data []byte) error {
	return writeFile(name, data, false)
}

// WritePrivate is WriteFile for a file that holds a secret, such as the
// bearer token in port.json. On Windows the file gets a protected DACL that
// lets only the current user open it, whatever its directory allows.
func WritePrivate(name string, data []byte) error {
	return writeFile(name, data, true)
}

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
