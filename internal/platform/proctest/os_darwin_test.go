// SPDX-License-Identifier: AGPL-3.0-or-later

package proctest

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"syscall"
	"testing"

	"github.com/some-du6e/pairtime/internal/platform"
	"github.com/some-du6e/pairtime/internal/platform/darwin"
	"github.com/some-du6e/pairtime/pkg/event"
)

const goos = platform.Darwin

func readOS() ([]platform.Proc, error) { return darwin.Processes() }

// readSelf reads this process alone, as a hook reads its own chain entry.
func readSelf() (platform.Proc, error) { return darwin.Self() }

func ownPGID() int { return syscall.Getpgrp() }

func bashPath() (string, error)   { return exec.LookPath("bash") }
func pythonPath() (string, error) { return exec.LookPath("python3") }

func selfOwner() string { return darwin.SelfOwner() }

// osPID is a process the OS runs as root: launchd.
const osPID = 1

func runsAsOS() bool { return os.Geteuid() == 0 }

// regroup returns a script line that runs the helper the way GNU timeout
// without --foreground runs a command, in a process group of its own.
// macOS has no timeout(1), so the test binary does the same itself.
var regroup = func(*testing.T, *rig) string {
	return envRegroup + `=1 "$` + envExe + `"`
}

// regroupSelf moves this process into a process group of its own and
// execs it again as the helper. It returns only on failure.
func regroupSelf() error {
	if err := syscall.Setpgid(0, 0); err != nil {
		return err
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	return syscall.Exec(exe, []string{exe}, withoutEnv(os.Environ(), envRegroup))
}

var scriptCase = struct {
	name        string
	class       event.ParentClass
	scriptShell bool
}{"bash running a script is script_runtime, because its child shares its process group", event.ParentScriptRuntime, true}

// interactive runs bash -i under script(1), which gives it a terminal and
// makes it that terminal's session leader, so it has job control as in a
// Terminal window, and types line into it. stdin stays open until bash
// exits, so script(1) never sees an end of input early.
var interactive = func(ctx context.Context, bash string, env []string, line string) error {
	script, err := exec.LookPath("script")
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, script, "-q", "/dev/null", bash, "--norc", "--noprofile", "--noediting", "-i")
	cmd.Env = env
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	if _, err := io.WriteString(stdin, line+"\n"); err != nil {
		cmd.Process.Kill()
		cmd.Wait()
		return err
	}
	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("%w; output: %q", err, out.String())
	}
	return nil
}
