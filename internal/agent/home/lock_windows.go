// SPDX-License-Identifier: AGPL-3.0-or-later

package home

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

// The lock is the existence of a named mutex: the first process creates
// it, a later one gets ERROR_ALREADY_EXISTS, and the kernel deletes it when
// the last handle closes, which includes the holder dying. Nobody waits on
// it or owns it, so the goroutine and OS thread that created it don't
// matter.
func acquire(dir string) (func() error, error) {
	name, err := windows.UTF16PtrFromString(mutexName(dir))
	if err != nil {
		return nil, err
	}
	h, err := windows.CreateMutex(nil, false, name)
	switch {
	case err == nil:
		return func() error { return windows.CloseHandle(h) }, nil
	case errors.Is(err, windows.ERROR_ALREADY_EXISTS), errors.Is(err, windows.ERROR_ACCESS_DENIED):
		// Access denied means a mutex of that name exists and isn't ours
		// to open, so it is held all the same.
		if h != 0 {
			windows.CloseHandle(h)
		}
		return nil, ErrLocked
	default:
		return nil, os.NewSyscallError("CreateMutex", err)
	}
}

// mutexName is the lock's name for the data directory dir. Mutex names
// can't contain backslashes, so it uses a hash of the path, lowercased
// because Windows paths ignore case. The Local\ prefix keeps the name in
// this logon session's namespace, where processes of other sessions can't
// create it first.
func mutexName(dir string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(filepath.Clean(dir))))
	return `Local\pairtime-agent-` + hex.EncodeToString(sum[:16])
}
