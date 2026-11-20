// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"errors"
	"fmt"
	"io/fs"
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
// rename the file, so dst moves aside to pairtime.old.exe first, as CP 4.6
// says and install.ps1 does, and goes at once if nothing runs it. If
// something does, the next install, uninstall or agent start deletes it
// (removeOldBinaries).
func commitBinary(tmp, dst string) error {
	if _, err := os.Lstat(dst); errors.Is(err, fs.ErrNotExist) {
		return renameRetry(tmp, dst)
	} else if err != nil {
		return err
	}
	dir := filepath.Dir(dst)
	old := filepath.Join(dir, "pairtime.old.exe")
	if err := os.Remove(old); err != nil && !errors.Is(err, fs.ErrNotExist) {
		// An older copy that something still runs.
		old = filepath.Join(dir, "pairtime.old."+strconv.Itoa(os.Getpid())+".exe")
	}
	if err := renameRetry(dst, old); err != nil {
		return err
	}
	if err := renameRetry(tmp, dst); err != nil {
		// The old one goes back, so that the hooks and the login item
		// still find a pairtime there.
		return errors.Join(err, os.Rename(old, dst))
	}
	os.Remove(old)
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
// gives a copy it moves aside: pairtime.old.exe, or pairtime.old.<pid>.exe
// when an older one is still in use, in any case.
func isOldName(name string) bool {
	name = strings.ToLower(name)
	if name == "pairtime.old.exe" {
		return true
	}
	pid, ok := strings.CutPrefix(name, "pairtime.old.")
	if !ok {
		return false
	}
	pid, ok = strings.CutSuffix(pid, ".exe")
	if !ok || pid == "" {
		return false
	}
	_, err := strconv.ParseUint(pid, 10, 32)
	return err == nil
}

// removeOldBinaries deletes each copy oldBinaries finds in dir that nothing
// runs any more.
func removeOldBinaries(dir string) {
	for _, p := range oldBinaries(dir) {
		os.Remove(p)
	}
}
