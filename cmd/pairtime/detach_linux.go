// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// unitWait is how long startInUnit waits for systemd-run to put the agent
// in its unit. A scope starts in some tens of milliseconds; a user manager
// that hangs mustn't hold up a hook for longer than this.
var unitWait = 3 * time.Second

// inUnit reports whether the process pid runs in the systemd unit named
// unit: whether a line of /proc/<pid>/cgroup (cgroups(7)) has a path that
// ends in the unit's name, as the unified hierarchy's line does, and the
// name=systemd line of a legacy or hybrid one.
var inUnit = func(pid int, unit string) bool {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/cgroup")
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(b), "\n") {
		// hierarchy-ID:controller-list:cgroup-path
		if f := strings.SplitN(line, ":", 3); len(f) == 3 && strings.HasSuffix(f[2], "/"+unit) {
			return true
		}
	}
	return false
}

// userManager returns the path of systemd-run, and whether this process's
// user has a systemd user manager to start the agent's unit: XDG_RUNTIME_DIR
// is an absolute path, the manager's private socket in it,
// systemd/private, is a socket that euid owns, and systemd-run is on PATH.
// It starts no process, so it costs a hook next to nothing.
//
// A session bus says nothing, since one runs without systemd too. The
// owner keeps a root shell that holds another user's XDG_RUNTIME_DIR, as
// `sudo -E` leaves it, from starting root's agent under that user's
// manager.
func userManager(lookupEnv func(string) (string, bool), lstat func(string) (fs.FileInfo, error), lookPath func(string) (string, error), euid int) (string, bool) {
	dir, _ := lookupEnv("XDG_RUNTIME_DIR")
	if !filepath.IsAbs(dir) {
		return "", false
	}
	fi, err := lstat(filepath.Join(dir, "systemd", "private"))
	if err != nil || fi.Mode().Type() != fs.ModeSocket {
		return "", false
	}
	if st, ok := fi.Sys().(*syscall.Stat_t); !ok || int64(st.Uid) != int64(euid) {
		return "", false
	}
	run, err := lookPath("systemd-run")
	if err != nil {
		return "", false
	}
	return run, true
}

// newUnitName returns a name for the agent's scope that no other start
// uses: pairtime-agent-, 16 random hex digits and .scope. A fixed name
// would make a start fail while the unit of an agent that is stopping is
// still loaded, or while another hook starts one at the same moment, and
// such a start would fall back to the starter's own unit. The data
// directory's lock still lets one agent at a time run there, and the
// unit's name is how startInUnit sees the agent arrive in it.
func newUnitName() string {
	var b [8]byte
	rand.Read(b[:])
	return "pairtime-agent-" + hex.EncodeToString(b[:]) + ".scope"
}

// unitArgs are systemd-run's arguments that run `exe agent` in a new scope
// named unit under the user's manager. For a scope, systemd-run asks the
// manager to move systemd-run's own process into the new unit, waits until
// the unit has started, and then executes the agent in that process. So
// the agent keeps the environment, working directory and session of the
// process startInUnit started, its process ID and its exit status, and
// runs in the manager's app.slice, out of the starter's unit. --collect
// unloads the unit even when the agent fails, so a failed unit neither
// stays in `systemctl --user --failed` nor keeps its name, and
// --description keeps the binary's path out of unit lists and the journal.
func unitArgs(unit, exe string) []string {
	return []string{"--user", "--scope", "--quiet", "--collect", "--unit=" + unit, "--description=pairtime agent", "--", exe, "agent"}
}

// startInUnit starts `exe agent` in dir in a scope of its own under the
// user's systemd manager (unitArgs), in a session of its own like any
// agent (detachedAttr), and returns once the process is in the unit. It
// returns errNoUserManager when there is no manager (userManager), and
// another error when the binary's path holds a $, or systemd-run fails to
// start, exits before its process reaches the unit or takes longer than
// unitWait, which it is then killed for. launchAgent then starts the agent
// in the starter's unit.
//
// An exit with status 3 before the process is seen in the unit counts as a
// start: only the agent exits so, when another agent holds the data
// directory, and systemd-run runs the agent only once it is in its unit.
// systemd-run's own stderr can't be read for why it failed, because the
// agent it turns into would keep writing its log to that pipe, and die of
// SIGPIPE once this process is gone; its exit status is all there is.
func startInUnit(exe, dir string) (launched, error) {
	run, ok := userManager(os.LookupEnv, os.Lstat, exec.LookPath, os.Geteuid())
	if !ok {
		return launched{}, errNoUserManager
	}
	if strings.Contains(exe, "$") {
		// Scopes are to expand ${NAME} in their command line by default in
		// a later systemd (systemd-run(1), --expand-environment=), which
		// systemd-run before 254 doesn't take.
		return launched{}, fmt.Errorf("the binary's path %q holds a $, which systemd-run may expand", exe)
	}
	unit := newUnitName()
	cmd := exec.Command(run, unitArgs(unit, exe)...)
	cmd.Dir = dir
	cmd.SysProcAttr = detachedAttr()
	if err := cmd.Start(); err != nil {
		return launched{}, fmt.Errorf("starting systemd-run: %w", err)
	}
	pid := cmd.Process.Pid
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	timeout := time.NewTimer(unitWait)
	defer timeout.Stop()
	poll := time.NewTicker(2 * time.Millisecond)
	defer poll.Stop()
	for {
		if inUnit(pid, unit) {
			return launched{PID: pid, Unit: unit, exited: exited}, nil
		}
		select {
		case err := <-exited:
			var ee *exec.ExitError
			if errors.As(err, &ee) && ee.ExitCode() == exitAlreadyRunning {
				exited <- err // for the caller, as cmd.Wait gave it
				return launched{PID: pid, Unit: unit, exited: exited}, nil
			}
			if err == nil {
				err = errors.New("exit status 0")
			}
			return launched{}, fmt.Errorf("systemd-run exited before the agent was in its unit: %w", err)
		case <-timeout.C:
			cmd.Process.Kill()
			<-exited
			return launched{}, fmt.Errorf("systemd-run didn't start the agent's unit within %v", unitWait)
		case <-poll.C:
		}
	}
}
