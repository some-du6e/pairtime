// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// canRemoveRunning is false: Windows doesn't let anyone delete a program
// while it runs.
const canRemoveRunning = false

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
// has, delete file, the binary this process runs from, once this process
// has exited, and then each of dirs that is empty by then, the innermost
// first. It starts PowerShell with no window and doesn't wait for it.
func removeAfterExit(file string, dirs []string) error {
	root := os.Getenv("SystemRoot")
	if !filepath.IsAbs(root) {
		return fmt.Errorf("SystemRoot isn't an absolute path, so PowerShell can't be found")
	}
	ps := filepath.Join(root, "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
	var b strings.Builder
	fmt.Fprintf(&b, "$ErrorActionPreference = 'SilentlyContinue'; Wait-Process -Id %d -Timeout 120; ", os.Getpid())
	fmt.Fprintf(&b, "for ($i = 0; $i -lt 150; $i++) { try { [IO.File]::Delete(%[1]s) } catch {}; if (-not [IO.File]::Exists(%[1]s)) { break }; Start-Sleep -Milliseconds 200 }; ", psQuote(file))
	for i := len(dirs) - 1; i >= 0; i-- {
		// Directory.Delete without recursive deletes only an empty folder.
		fmt.Fprintf(&b, "try { [IO.Directory]::Delete(%s) } catch {}; ", psQuote(dirs[i]))
	}
	cmd := exec.Command(ps, "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", b.String())
	cmd.SysProcAttr = detachedAttr()
	cmd.Dir = root
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}
