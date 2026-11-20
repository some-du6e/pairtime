// SPDX-License-Identifier: AGPL-3.0-or-later

package proctest

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/some-du6e/pairtime/internal/platform"
	"github.com/some-du6e/pairtime/internal/platform/windows"
	"github.com/some-du6e/pairtime/pkg/event"
)

const goos = platform.Windows

func readOS() ([]platform.Proc, error) { return windows.Processes() }

// ownPGID is 0: Windows has no process groups.
func ownPGID() int { return 0 }

// bashPath prefers Git Bash, which is what SM 6.6's laptop runs and what
// Actions' bash is on windows-latest, over System32's bash.exe, which
// starts WSL.
func bashPath() (string, error) {
	for _, dir := range []string{os.Getenv("ProgramFiles"), os.Getenv("ProgramW6432")} {
		if dir == "" {
			continue
		}
		p := filepath.Join(dir, "Git", "bin", "bash.exe")
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return exec.LookPath("bash")
}

func pythonPath() (string, error) { return exec.LookPath("python") }

var scriptCase = struct {
	name        string
	class       event.ParentClass
	scriptShell bool
}{"bash running a script is skipped like any shell, because Windows has no process groups", event.ParentUnknown, false}

// interactive is nil: on Windows every shell is skipped, so the case above
// already shows that a shell alone isn't taken for a script.
var interactive func(ctx context.Context, bash string, env []string, line string) error
