// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"errors"
	"fmt"
	"io/fs"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/windows"
)

// canRemoveRunning is false: Windows doesn't let anyone delete a program
// while it runs.
const canRemoveRunning = false

// removesHeldFiles is false: Windows doesn't delete a file that a process
// has open, the holder of its lock included, so a lock file goes only once
// its lock is dropped, and stays if another process has opened it by then.
const removesHeldFiles = false

// removeBinary deletes the binary at path. The agent that ran it has
// stopped, but Windows may keep the file in use for a moment after the
// process ends, so it tries again for a few seconds.
func removeBinary(path string) error {
	var err error
	for i := 0; i < 50; i++ {
		if err = os.Remove(path); err == nil || os.IsNotExist(err) {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return err
}

// psQuotes are the characters PowerShell ends a single-quoted string at:
// the apostrophe and the typographic single quotes. Inside the string each
// is written twice.
var psQuotes = []rune{'\'', 0x2018, 0x2019, 0x201a, 0x201b}

// psQuote writes s as a PowerShell string that holds s and nothing else.
func psQuote(s string) string {
	var b strings.Builder
	b.WriteByte('\'')
	for _, r := range s {
		b.WriteRune(r)
		if slices.Contains(psQuotes, r) {
			b.WriteRune(r)
		}
	}
	b.WriteByte('\'')
	return b.String()
}

// removeAfterExit has Windows PowerShell, which every Windows 10 and 11
// has, delete files once this process has exited: the binary this process
// runs from, and any copy that something else still ran. Then it deletes
// each of dirs that is an empty folder by then, the innermost first, and
// leaves any of them that is a link or a junction, which Directory.Delete
// would delete whatever it points to. Last it deletes each of links that
// is still a link or a junction, by itself: a data directory's, which
// uninstall leaves until the files behind it have gone. It starts
// PowerShell with no window and doesn't wait for it.
func removeAfterExit(files, dirs, links []string) error {
	root := os.Getenv("SystemRoot")
	if !filepath.IsAbs(root) {
		return fmt.Errorf("SystemRoot isn't an absolute path, so PowerShell can't be found")
	}
	ps := filepath.Join(root, "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
	var b strings.Builder
	fmt.Fprintf(&b, "$ErrorActionPreference = 'SilentlyContinue'; Wait-Process -Id %d -Timeout 120; ", os.Getpid())
	for _, f := range files {
		fmt.Fprintf(&b, "for ($i = 0; $i -lt 150; $i++) { try { [IO.File]::Delete(%[1]s) } catch {}; if (-not [IO.File]::Exists(%[1]s)) { break }; Start-Sleep -Milliseconds 200 }; ", psQuote(f))
	}
	for i := len(dirs) - 1; i >= 0; i-- {
		// Directory.Delete without recursive deletes only an empty folder,
		// but a reparse point, a link or a junction, whatever it holds.
		fmt.Fprintf(&b, "try { if (-not ([IO.File]::GetAttributes(%[1]s) -band [IO.FileAttributes]::ReparsePoint)) { [IO.Directory]::Delete(%[1]s) } } catch {}; ", psQuote(dirs[i]))
	}
	for _, l := range links {
		fmt.Fprintf(&b, "try { if ([IO.File]::GetAttributes(%[1]s) -band [IO.FileAttributes]::ReparsePoint) { [IO.Directory]::Delete(%[1]s) } } catch {}; ", psQuote(l))
	}
	cmd := exec.Command(ps, "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", b.String())
	cmd.SysProcAttr = detachedAttr()
	cmd.Dir = root
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

// commitBinary puts tmp, the new binary, at dst. Windows doesn't let anyone
// replace a program's file while it runs, and a hook Claude Code started,
// or an agent that a hook started again, may run dst. It does let one
// rename the file, so dst moves aside first, as CP 4.6 says and install.ps1
// does, and goes at once if nothing runs it. The copy is named for this
// process, pairtime.old.<pid>.exe, so that two installs at once never
// delete each other's: an install whose new copy can't go in puts its old
// one back. If something runs the copy, the next install, uninstall or
// agent start deletes it once this process has ended (removeOldBinaries,
// strayBinaries). A copy that a process with this id left there and that
// something still runs makes this one take a random id.
func commitBinary(tmp, dst string) error {
	dir := filepath.Dir(dst)
	old := filepath.Join(dir, "pairtime.old."+strconv.Itoa(os.Getpid())+".exe")
	// One that a process with this id left before, which no install needs.
	if err := os.Remove(old); err != nil && !errors.Is(err, fs.ErrNotExist) {
		// Something still runs it, so this copy takes a random id that no
		// copy there has, which the cleanups still read as an id.
		for {
			old = filepath.Join(dir, "pairtime.old."+strconv.FormatUint(uint64(rand.Uint32()), 10)+".exe")
			if _, err := os.Lstat(old); err != nil {
				break
			}
		}
	}
	if err := renameRetry(dst, old); errors.Is(err, fs.ErrNotExist) {
		// Nothing is there, or another install moved it aside meanwhile
		// and puts its own copy in.
		return renameRetry(tmp, dst)
	} else if err != nil {
		return err
	}
	if err := renameRetry(tmp, dst); err != nil {
		// The old one goes back, so that the hooks and the login item
		// still find a pairtime there.
		return errors.Join(err, putBack(old, dst))
	}
	os.Remove(old)
	return nil
}

// putBack moves old, the copy commitBinary moved aside, back to dst. If
// another install has put its new copy at dst meanwhile, that copy stays,
// and old goes instead.
func putBack(old, dst string) error {
	from, err := windows.UTF16PtrFromString(old)
	if err != nil {
		return err
	}
	to, err := windows.UTF16PtrFromString(dst)
	if err != nil {
		return err
	}
	// Without MOVEFILE_REPLACE_EXISTING, which os.Rename passes, a file at
	// dst stays.
	switch err := windows.MoveFileEx(from, to, 0); {
	case errors.Is(err, fs.ErrExist):
		os.Remove(old)
	case err != nil:
		return &os.LinkError{Op: "rename", Old: old, New: dst, Err: err}
	}
	return nil
}

// renameRetry renames from to to, trying again for a few seconds while
// another process, such as a virus scanner, has one of them open, which
// makes the rename fail with access denied or a sharing violation.
func renameRetry(from, to string) error {
	var err error
	for i := 0; i < 50; i++ {
		err = os.Rename(from, to)
		if err == nil || !(errors.Is(err, windows.ERROR_ACCESS_DENIED) || errors.Is(err, windows.ERROR_SHARING_VIOLATION)) {
			return err
		}
		time.Sleep(100 * time.Millisecond)
	}
	return err
}

// oldBinaries returns the copies of the binary in dir that an install moved
// aside (commitBinary) and that are pairtime's.
func oldBinaries(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		p := filepath.Join(dir, e.Name())
		if isOldName(e.Name()) && e.Type().IsRegular() && isPairtimeBinary(p) {
			out = append(out, p)
		}
	}
	return out
}

// isOldName reports whether name is one that commitBinary, or install.ps1,
// gives a copy it moves aside, in any case: pairtime.old.<pid>.exe, named
// for the process that moved it, or pairtime.old.exe, the name earlier
// versions tried first.
func isOldName(name string) bool {
	_, ok := oldPID(name)
	return ok || strings.ToLower(name) == "pairtime.old.exe"
}

// oldPID returns the id of the process that name, pairtime.old.<pid>.exe
// in any case, is named for.
func oldPID(name string) (uint32, bool) {
	pid, ok := strings.CutPrefix(strings.ToLower(name), "pairtime.old.")
	if !ok {
		return 0, false
	}
	pid, ok = strings.CutSuffix(pid, ".exe")
	if !ok || pid == "" {
		return 0, false
	}
	n, err := strconv.ParseUint(pid, 10, 32)
	if err != nil {
		return 0, false
	}
	return uint32(n), true
}

// stillActive is the exit code GetExitCodeProcess gives a process that
// hasn't ended (STILL_ACTIVE).
const stillActive = 259

// placing reports whether an install that still runs may need p, a copy
// oldBinaries found, to put back (commitBinary): whether p is named for a
// process that hasn't ended. A process that Windows won't let this user
// open counts as one that hasn't. pairtime.old.exe names no process, so
// the cleanups delete it as they always have.
func placing(p string) bool {
	pid, ok := oldPID(filepath.Base(p))
	if !ok {
		return false
	}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
		return false // no process has that id
	} else if err != nil {
		return true
	}
	defer windows.CloseHandle(h)
	var code uint32
	return windows.GetExitCodeProcess(h, &code) != nil || code == stillActive
}

// removeOldBinaries deletes each copy oldBinaries finds in dir that nothing
// runs any more and that no install still running may put back (placing).
func removeOldBinaries(dir string) {
	for _, p := range oldBinaries(dir) {
		if !placing(p) {
			os.Remove(p)
		}
	}
}
