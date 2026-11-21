// SPDX-License-Identifier: AGPL-3.0-or-later

package proctest

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"syscall"
	"testing"

	"golang.org/x/sys/unix"

	"github.com/some-du6e/pairtime/internal/platform"
	"github.com/some-du6e/pairtime/internal/platform/linux"
	"github.com/some-du6e/pairtime/pkg/event"
)

const goos = platform.Linux

func readOS() ([]platform.Proc, error) { return linux.Processes() }

// readSelf reads this process alone, as a hook reads its own chain entry.
func readSelf() (platform.Proc, error) { return linux.Self() }

func ownPGID() int { return syscall.Getpgrp() }

func bashPath() (string, error)   { return exec.LookPath("bash") }
func pythonPath() (string, error) { return exec.LookPath("python3") }

func selfOwner() string { return linux.SelfOwner() }

// osPID is a process the OS runs as root: init.
const osPID = 1

func runsAsOS() bool { return os.Geteuid() == 0 }

// regroup returns a script line that runs the helper through GNU timeout
// without --foreground, which runs its command in a process group of its
// own.
var regroup = func(t *testing.T, _ *rig) string {
	timeout := need(t, "timeout", func() (string, error) { return exec.LookPath("timeout") })
	return `"` + timeout + `" 60 "$` + envExe + `"`
}

// regroupSelf moves this process into a process group of its own and
// execs it again as the helper, as on macOS. Linux's test runs GNU
// timeout itself, so this is only here for TestMain.
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

// interactive runs bash -i on a new pseudo-terminal, as its session leader
// with the terminal as its controlling one, so it has job control as in a
// terminal window, and types line into it.
var interactive = func(ctx context.Context, bash string, env []string, line string) error {
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		return err
	}
	defer master.Close()
	rc, err := master.SyscallConn()
	if err != nil {
		return err
	}
	var n int
	var ioErr error
	if err := rc.Control(func(fd uintptr) {
		// unlockpt(3), then ptsname(3)'s number.
		if ioErr = unix.IoctlSetPointerInt(int(fd), unix.TIOCSPTLCK, 0); ioErr == nil {
			n, ioErr = unix.IoctlGetInt(int(fd), unix.TIOCGPTN)
		}
	}); err != nil {
		return err
	}
	if ioErr != nil {
		return ioErr
	}
	tty, err := os.OpenFile(fmt.Sprintf("/dev/pts/%d", n), os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, bash, "--norc", "--noprofile", "--noediting", "-i")
	cmd.Env = env
	cmd.Stdin, cmd.Stdout, cmd.Stderr = tty, tty, tty
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	err = cmd.Start()
	tty.Close()
	if err != nil {
		return err
	}
	// The shell echoes and prints prompts; nobody reads them.
	go io.Copy(io.Discard, master)
	if _, err := io.WriteString(master, line+"\n"); err != nil {
		cmd.Process.Kill()
		cmd.Wait()
		return err
	}
	return cmd.Wait()
}
