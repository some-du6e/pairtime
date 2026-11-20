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

// allBytes, as both halves of the length, makes the file lock cover every
// byte a file can have, from offset 0, as Go's own cmd/go locks its files.
// No one reads agent.lock, so it doesn't matter that readers are kept out
// of it too.
const allBytes = ^uint32(0)

// acquire takes an exclusive LockFileEx lock on agent.lock in dir, as the
// flock does elsewhere, and then creates the named mutex of DESIGN 3.2.
//
// The file lock is the one that keeps a second agent out. The mutex lives
// in this session's Local\ namespace and is named after the path as it is
// spelled, so on its own it let the same user run a second agent on the
// directory from session 0, where Claude Code over OpenSSH and a scheduled
// task set to run whether the user is logged on or not both run, or
// through another spelling of the path, such as an 8.3 name or a junction
// (#46). A lock on the file holds across sessions and spellings.
func acquire(dir string) (func() error, error) {
	name := filepath.Join(dir, lockFile)
	f, err := os.OpenFile(name, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	// The lock belongs to the open file, so a second Acquire in this
	// process conflicts as well. Go opens files without handle
	// inheritance, so a child process never holds the lock. The file is
	// open for synchronous I/O, so LockFileEx answers at once.
	err = windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0, allBytes, allBytes, new(windows.Overlapped))
	if err != nil {
		f.Close()
		if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
			return nil, ErrLocked
		}
		return nil, &os.PathError{Op: "LockFileEx", Path: name, Err: err}
	}
	closeMutex, err := createMutex(dir)
	if err != nil {
		unlockFile(f)
		f.Close()
		return nil, err
	}
	// The closure keeps f reachable, so the garbage collector can't close
	// it, and drop the lock, early.
	return func() error {
		return errors.Join(closeMutex(), unlockFile(f), f.Close())
	}, nil
}

// unlockFile drops f's lock before f is closed. Windows drops a closed
// file's locks as well, but LockFileEx's documentation says how soon
// depends on the system's resources and recommends unlocking explicitly,
// so the next agent finds the lock free at once.
func unlockFile(f *os.File) error {
	if err := windows.UnlockFileEx(windows.Handle(f.Fd()), 0, allBytes, allBytes, new(windows.Overlapped)); err != nil {
		return &os.PathError{Op: "UnlockFileEx", Path: f.Name(), Err: err}
	}
	return nil
}

// createMutex creates dir's named mutex and returns the function that
// closes it. The mutex is held by existing: the first process creates it,
// a later one gets ERROR_ALREADY_EXISTS, and the kernel deletes it when
// the last handle closes, which includes the holder dying. Nobody waits on
// it or owns it, so the goroutine and OS thread that created it don't
// matter.
func createMutex(dir string) (func() error, error) {
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

// mutexName is the mutex's name for the data directory dir. Mutex names
// can't contain backslashes, so it uses a hash of the path, lowercased
// because Windows paths ignore case. The Local\ prefix keeps the name in
// this logon session's namespace, where processes of other sessions can't
// create it first.
func mutexName(dir string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(filepath.Clean(dir))))
	return `Local\pairtime-agent-` + hex.EncodeToString(sum[:16])
}
