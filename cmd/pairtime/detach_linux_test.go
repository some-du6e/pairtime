// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/agent/home"
	"github.com/some-du6e/pairtime/internal/agent/localhttp"
)

// agentUnitName is the name newUnitName gives an agent's scope.
var agentUnitName = regexp.MustCompile(`^pairtime-agent-[0-9a-f]{16}\.scope$`)

// socketInfo is what TestUserManager's lstat says of a path.
type socketInfo struct {
	mode fs.FileMode
	uid  uint32
}

func (i socketInfo) Name() string       { return "private" }
func (i socketInfo) Size() int64        { return 0 }
func (i socketInfo) Mode() fs.FileMode  { return i.mode }
func (i socketInfo) ModTime() time.Time { return time.Time{} }
func (i socketInfo) IsDir() bool        { return i.mode.IsDir() }
func (i socketInfo) Sys() any           { return &syscall.Stat_t{Uid: i.uid} }

func TestUserManager(t *testing.T) {
	const me, other = 1000, 1001
	const run = "/run/user/1000"
	private := filepath.Join(run, "systemd", "private")
	mine := socketInfo{mode: fs.ModeSocket | 0o777, uid: me}
	onPath := map[string]string{"systemd-run": "/usr/bin/systemd-run"}
	tests := []struct {
		name  string
		euid  int
		env   map[string]string
		files map[string]socketInfo // what lstat finds
		path  map[string]string     // what lookPath finds
		want  string
		ok    bool
	}{
		{
			name: "a socket the user owns at XDG_RUNTIME_DIR/systemd/private, with systemd-run on PATH, is the user's manager",
			euid: me, env: map[string]string{"XDG_RUNTIME_DIR": run}, files: map[string]socketInfo{private: mine}, path: onPath,
			want: "/usr/bin/systemd-run", ok: true,
		},
		{
			name: "without XDG_RUNTIME_DIR there is no manager to find",
			euid: me, files: map[string]socketInfo{private: mine}, path: onPath,
		},
		{
			name: "a relative XDG_RUNTIME_DIR counts as unset, as the XDG spec says",
			euid: me, env: map[string]string{"XDG_RUNTIME_DIR": "run/user/1000"},
			files: map[string]socketInfo{filepath.Join("run/user/1000", "systemd", "private"): mine}, path: onPath,
		},
		{
			name: "without the manager's private socket there is no manager, even with a session bus",
			euid: me, env: map[string]string{"XDG_RUNTIME_DIR": run, "DBUS_SESSION_BUS_ADDRESS": "unix:path=" + run + "/bus"}, path: onPath,
		},
		{
			name: "a file there that isn't a socket isn't a manager's",
			euid: me, env: map[string]string{"XDG_RUNTIME_DIR": run}, files: map[string]socketInfo{private: {mode: 0o600, uid: me}}, path: onPath,
		},
		{
			name: "a link there isn't followed",
			euid: me, env: map[string]string{"XDG_RUNTIME_DIR": run}, files: map[string]socketInfo{private: {mode: fs.ModeSymlink | 0o777, uid: me}}, path: onPath,
		},
		{
			name: "another user's socket isn't this process's manager, as for root under sudo -E",
			euid: 0, env: map[string]string{"XDG_RUNTIME_DIR": run}, files: map[string]socketInfo{private: mine}, path: onPath,
		},
		{
			name: "a socket another user owns in this user's XDG_RUNTIME_DIR isn't this user's manager",
			euid: me, env: map[string]string{"XDG_RUNTIME_DIR": run}, files: map[string]socketInfo{private: {mode: fs.ModeSocket | 0o777, uid: other}}, path: onPath,
		},
		{
			name: "without systemd-run on PATH no unit can be started",
			euid: me, env: map[string]string{"XDG_RUNTIME_DIR": run}, files: map[string]socketInfo{private: mine},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lookupEnv := func(k string) (string, bool) {
				v, ok := tt.env[k]
				return v, ok
			}
			lstat := func(p string) (fs.FileInfo, error) {
				if fi, ok := tt.files[p]; ok {
					return fi, nil
				}
				return nil, &fs.PathError{Op: "lstat", Path: p, Err: fs.ErrNotExist}
			}
			lookPath := func(file string) (string, error) {
				if p, ok := tt.path[file]; ok {
					return p, nil
				}
				return "", &exec.Error{Name: file, Err: exec.ErrNotFound}
			}
			got, ok := userManager(lookupEnv, lstat, lookPath, tt.euid)
			if got != tt.want || ok != tt.ok {
				t.Errorf("userManager() = %q, %v; want %q, %v", got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestUnitArgs(t *testing.T) {
	unit := newUnitName()
	tests := []struct {
		name  string
		check func(t *testing.T)
	}{
		{
			name: "an agent's unit is a scope named pairtime-agent- and 16 hex digits",
			check: func(t *testing.T) {
				if !agentUnitName.MatchString(unit) {
					t.Errorf("newUnitName() = %q", unit)
				}
			},
		},
		{
			name: "every start gets a unit name of its own",
			check: func(t *testing.T) {
				if again := newUnitName(); again == unit {
					t.Errorf("newUnitName() gave %q twice", unit)
				}
			},
		},
		{
			name: "systemd-run runs the agent in that scope under the user's manager, quietly, unloaded even when it fails, described without the binary's path, at a tenth of a unit's CPU and IO weight, and not stopped by another process's OOM kill",
			check: func(t *testing.T) {
				want := []string{"--user", "--scope", "--quiet", "--collect", "--unit=" + unit, "--description=pairtime agent",
					"--property=CPUWeight=10", "--property=IOWeight=10", "--property=OOMPolicy=continue", "--", "/opt/pairtime/pairtime", "agent"}
				if got := unitArgs(unit, "/opt/pairtime/pairtime", true); !slices.Equal(got, want) {
					t.Errorf("unitArgs() = %q\nwant %q", got, want)
				}
			},
		},
		{
			name: "the start for a systemd before 253, which refuses OOMPolicy for a scope, asks for no OOM policy",
			check: func(t *testing.T) {
				if got := unitArgs(unit, "/opt/pairtime/pairtime", false); slices.Contains(got, "--property=OOMPolicy=continue") {
					t.Errorf("unitArgs() = %q, with OOMPolicy", got)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, tt.check)
	}
}

// The environment of TestLaunchAgent's stand-ins: the folder they write in,
// and what each does.
const (
	fakeDirEnv   = "PAIRTIME_TEST_FAKE_DIR"
	fakeRunEnv   = "PAIRTIME_TEST_FAKE_RUN"
	fakeAgentEnv = "PAIRTIME_TEST_FAKE_AGENT"
)

// fakeSystemdRun stands in for systemd-run in TestLaunchAgent. It writes
// its process ID and then its arguments, a line each, to systemd-run in
// $PAIRTIME_TEST_FAKE_DIR, adds them on one line to runs there, and does as
// $PAIRTIME_TEST_FAKE_RUN says: fail exits 1, as a start the manager
// refuses does, old exits 1 when asked for an OOM policy, as systemd before
// 253 does for a scope, and otherwise does what move does, and hang waits. move
// writes its process ID and the unit its --unit names to moved, which
// TestLaunchAgent's inUnit reads as the process having reached that unit,
// and then, like any other mode, it runs what follows -- in its own
// process, as systemd-run does for a scope.
const fakeSystemdRun = `#!/bin/sh
dir=$PAIRTIME_TEST_FAKE_DIR
{ echo "$$"; printf '%s\n' "$@"; } >"$dir/systemd-run"
echo "$*" >>"$dir/runs"
unit=
oom=
while [ "$#" -gt 0 ]; do
	case $1 in
	--unit=*) unit=${1#--unit=} ;;
	--property=OOMPolicy=*) oom=yes ;;
	--)
		shift
		break
		;;
	esac
	shift
done
case $PAIRTIME_TEST_FAKE_RUN in
old)
	if [ -n "$oom" ]; then
		echo "Unknown assignment: OOMPolicy=continue" >&2
		exit 1
	fi
	echo "$$ $unit" >"$dir/moved"
	;;
fail)
	echo "Failed to start transient scope unit: Unit $unit was already loaded or has a fragment file." >&2
	exit 1
	;;
hang) exec sleep 60 ;;
move) echo "$$ $unit" >"$dir/moved" ;;
esac
exec "$@"
`

// fakeAgent stands in for the agent's binary in TestLaunchAgent. It adds a
// line to agent in $PAIRTIME_TEST_FAKE_DIR with its process ID, its working
// directory and its arguments, tab-separated, and then exits with status 3,
// as an agent that finds another running does, when $PAIRTIME_TEST_FAKE_AGENT
// is exit3, and otherwise runs until it is killed.
const fakeAgent = `#!/bin/sh
printf '%s\t%s\t%s\n' "$$" "$(pwd -P)" "$*" >>"$PAIRTIME_TEST_FAKE_DIR/agent"
if [ "$PAIRTIME_TEST_FAKE_AGENT" = exit3 ]; then
	exit 3
fi
exec sleep 60
`

// TestLaunchAgent runs launchAgent against a stand-in for systemd-run on
// PATH and a real socket where the user manager's would be, and a stand-in
// for the agent.
func TestLaunchAgent(t *testing.T) {
	root := t.TempDir()
	runtimeDir := filepath.Join(root, "run")
	if err := os.MkdirAll(filepath.Join(runtimeDir, "systemd"), 0o700); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("unix", filepath.Join(runtimeDir, "systemd", "private"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	noManager := filepath.Join(root, "empty")
	bin := filepath.Join(root, "bin")
	dollar := filepath.Join(root, "a$b")
	for _, d := range []string{noManager, bin, dollar} {
		if err := os.Mkdir(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	exe := filepath.Join(root, "pairtime")
	dollarExe := filepath.Join(dollar, "pairtime")
	for path, script := range map[string]string{filepath.Join(bin, "systemd-run"): fakeSystemdRun, exe: fakeAgent, dollarExe: fakeAgent} {
		if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	oldUnits, oldWait, oldInUnit := launchUnits, unitWait, inUnit
	t.Cleanup(func() { launchUnits, unitWait, inUnit = oldUnits, oldWait, oldInUnit })
	unitWait = 300 * time.Millisecond
	inUnit = func(pid int, unit string) bool {
		b, err := os.ReadFile(filepath.Join(os.Getenv(fakeDirEnv), "moved"))
		return err == nil && string(b) == strconv.Itoa(pid)+" "+unit+"\n"
	}

	tests := []struct {
		name       string
		run        string // what the stand-in systemd-run does ($PAIRTIME_TEST_FAKE_RUN)
		agent      string // what the stand-in agent does ($PAIRTIME_TEST_FAKE_AGENT)
		exe        string
		runtimeDir string // XDG_RUNTIME_DIR
		units      bool   // launchUnits
		// wantRun is whether systemd-run ran, wantUnit whether the agent
		// runs in a unit of its own, and wantUnitErr whether launchAgent
		// says why it doesn't although a manager runs.
		wantRun, wantUnit, wantUnitErr bool
		wantExit                       int // the agent's exit status, or 0 for still running
		// runs is how many times systemd-run ran, and oom whether the last
		// asked for OOMPolicy=continue.
		runs int
		oom  bool
	}{
		{
			name: "under a user manager, the agent runs in a scope of its own, in the very process systemd-run ran in",
			run:  "move", exe: exe, runtimeDir: runtimeDir, units: true,
			wantRun: true, wantUnit: true, runs: 1, oom: true,
		},
		{
			name: "a systemd before 253, whose systemd-run refuses OOMPolicy for a scope, gets one more start without it, which runs the agent in its scope",
			run:  "old", exe: exe, runtimeDir: runtimeDir, units: true,
			wantRun: true, wantUnit: true, runs: 2,
		},
		{
			name: "when systemd-run fails, with and without the OOM policy, the agent starts where its starter runs, and launchAgent says why",
			run:  "fail", exe: exe, runtimeDir: runtimeDir, units: true,
			wantRun: true, wantUnitErr: true, runs: 2,
		},
		{
			name: "a systemd-run that hangs is killed after unitWait, and the agent starts where its starter runs",
			run:  "hang", exe: exe, runtimeDir: runtimeDir, units: true,
			wantRun: true, wantUnitErr: true, runs: 1, oom: true,
		},
		{
			name: "an agent that exits with status 3 in its unit before it is seen there counts as started, and its status reaches the caller",
			run:  "quiet", agent: "exit3", exe: exe, runtimeDir: runtimeDir, units: true,
			wantRun: true, wantUnit: true, wantExit: exitAlreadyRunning, runs: 1, oom: true,
		},
		{
			name: "a binary whose path holds a dollar sign, which a later systemd-run may expand, starts where its starter runs",
			run:  "move", exe: dollarExe, runtimeDir: runtimeDir, units: true,
			wantUnitErr: true,
		},
		{
			name: "without a user manager, the agent starts where its starter runs, and nothing is said",
			run:  "move", exe: exe, runtimeDir: noManager, units: true,
		},
		{
			name: "with units off, as in the command's tests, systemd-run isn't run",
			run:  "move", exe: exe, runtimeDir: runtimeDir,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv(fakeDirEnv, dir)
			t.Setenv(fakeRunEnv, tt.run)
			t.Setenv(fakeAgentEnv, tt.agent)
			t.Setenv("XDG_RUNTIME_DIR", tt.runtimeDir)
			launchUnits = tt.units

			began := time.Now()
			l, err := launchAgent(tt.exe, dir)
			took := time.Since(began)
			if err != nil {
				t.Fatalf("launchAgent: %v", err)
			}
			if tt.wantExit == 0 {
				t.Cleanup(func() {
					syscall.Kill(l.PID, syscall.SIGKILL)
					<-l.exited
				})
			}

			ran, err := os.ReadFile(filepath.Join(dir, "systemd-run"))
			switch {
			case err == nil && !tt.wantRun:
				t.Errorf("systemd-run ran:\n%s", ran)
			case err != nil && tt.wantRun:
				t.Fatalf("systemd-run didn't run: %v", err)
			}
			if tt.wantRun {
				lines := strings.Split(strings.TrimSuffix(string(ran), "\n"), "\n")
				runPID, _ := strconv.Atoi(lines[0])
				unit := ""
				for _, a := range lines[1:] {
					if u, ok := strings.CutPrefix(a, "--unit="); ok {
						unit = u
					}
				}
				if want := unitArgs(unit, tt.exe, tt.oom); !slices.Equal(lines[1:], want) {
					t.Errorf("systemd-run's arguments were %q\nwant %q", lines[1:], want)
				}
				runs, _ := os.ReadFile(filepath.Join(dir, "runs"))
				if n := strings.Count(string(runs), "\n"); n != tt.runs {
					t.Errorf("systemd-run ran %d times, want %d:\n%s", n, tt.runs, runs)
				}
				if tt.wantUnit && (l.Unit != unit || l.PID != runPID) {
					t.Errorf("launchAgent says the agent is process %d in unit %q; systemd-run ran as process %d for unit %q", l.PID, l.Unit, runPID, unit)
				}
				if !tt.wantUnit && l.PID == runPID {
					t.Errorf("the agent is process %d, the one systemd-run ran in, although systemd-run failed", l.PID)
				}
				if tt.run == "hang" {
					if took < unitWait {
						t.Errorf("launchAgent gave up on systemd-run after %v, before unitWait's %v", took, unitWait)
					}
					if err := syscall.Kill(runPID, 0); !errors.Is(err, syscall.ESRCH) {
						t.Errorf("the systemd-run that hung, process %d, is still there: %v", runPID, err)
					}
				}
			}
			if got := l.Unit != ""; got != tt.wantUnit {
				t.Errorf("launchAgent's unit is %q; want one: %v", l.Unit, tt.wantUnit)
			}
			if l.Unit != "" && !agentUnitName.MatchString(l.Unit) {
				t.Errorf("the agent's unit is %q", l.Unit)
			}
			if got := l.unitErr != nil; got != tt.wantUnitErr {
				t.Errorf("launchAgent's unitErr is %v; want one: %v", l.unitErr, tt.wantUnitErr)
			}

			// The agent's own line, which a start that fell back after a
			// failed systemd-run writes too.
			var started []string
			for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(10 * time.Millisecond) {
				b, _ := os.ReadFile(filepath.Join(dir, "agent"))
				if started = strings.Split(strings.TrimSuffix(string(b), "\n"), "\n"); len(b) > 0 {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("the agent didn't start within 10 s")
				}
			}
			wd, err := filepath.EvalSymlinks(dir)
			if err != nil {
				t.Fatal(err)
			}
			if want := []string{strconv.Itoa(l.PID) + "\t" + wd + "\tagent"}; !slices.Equal(started, want) {
				t.Errorf("the agents that started wrote %q; want one, the process launchAgent names, in %s, run as agent: %q", started, wd, want)
			}

			if tt.wantExit != 0 {
				select {
				case err := <-l.exited:
					var ee *exec.ExitError
					if !errors.As(err, &ee) || ee.ExitCode() != tt.wantExit {
						t.Errorf("the agent's exit reached the caller as %v; want exit status %d", err, tt.wantExit)
					}
				case <-time.After(10 * time.Second):
					t.Fatal("the agent's exit didn't reach the caller within 10 s")
				}
				return
			}
			if err := syscall.Kill(l.PID, 0); err != nil {
				t.Errorf("the agent, process %d, isn't running: %v", l.PID, err)
			}
		})
	}
}

// userManagerEnv, when set, makes TestAgentOutlivesScope run against the
// systemd user manager of the user the test runs as, and fail when there
// is none. ci.yml's step that runs it sets it, once the runner user's
// manager runs.
const userManagerEnv = "PAIRTIME_TEST_USER_MANAGER"

// canaryEnv carries a value from the session that starts an agent in
// TestAgentOutlivesScope to the agent's environment.
const canaryEnv = "PAIRTIME_TEST_CANARY"

// TestAgentOutlivesScope starts the real agent the ways a person's session
// does, from inside a systemd scope of the user's manager, as a session
// that T3 Code runs in a scope of its own or one in a terminal's scope,
// and then stops that scope, which stops every process left in it.
func TestAgentOutlivesScope(t *testing.T) {
	if os.Getenv(userManagerEnv) == "" {
		t.Skipf("runs only with %s set, against a running systemd user manager, as ci.yml does", userManagerEnv)
	}
	run, ok := userManager(os.LookupEnv, os.Lstat, exec.LookPath, os.Geteuid())
	if !ok {
		t.Fatalf("%s is set, but no systemd user manager runs for this user (XDG_RUNTIME_DIR=%q), or systemd-run isn't on PATH", userManagerEnv, os.Getenv("XDG_RUNTIME_DIR"))
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name  string
		args  []string // pairtime's arguments, run inside the session's scope
		event string   // the hook's stdin
		units bool     // unitsEnv, so the agent gets a unit as the real binary's does
	}{
		{
			name: "an agent that agent --detach starts without a unit of its own, as before, stops with the session's scope",
			args: []string{"agent", "--detach"},
		},
		{
			name: "agent --detach starts the agent in a scope of its own, which outlives the session's",
			args: []string{"agent", "--detach"}, units: true,
		},
		{
			name:  "a hook that finds the agent down starts it in a scope of its own, which outlives the session's",
			args:  []string{"hook", "--harness", "claude_code", "--event", "UserPromptSubmit"},
			event: stubPayload, units: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ptHome := shortHome(t)
			dirs := home.Dirs{Data: ptHome, Config: ptHome, Runtime: ptHome}
			id := make([]byte, 8)
			rand.Read(id)
			canary := hex.EncodeToString(id)
			session := "pairtime-test-session-" + canary + ".scope"

			tmp := t.TempDir()
			out, err := os.Create(filepath.Join(tmp, "session.out"))
			if err != nil {
				t.Fatal(err)
			}
			defer out.Close()
			said := func() string {
				b, _ := os.ReadFile(out.Name())
				return string(b)
			}
			// The session runs pairtime and then waits, so its scope stays
			// until it is stopped: a scope with no process left stops.
			args := append([]string{"--user", "--scope", "--quiet", "--collect", "--unit=" + session, "--description=pairtime test session",
				"--", "sh", "-c", `"$@"; exec sleep 300`, "sh", exe}, tt.args...)
			cmd := exec.Command(run, args...)
			cmd.Env = append(withoutEnv(os.Environ(), unitsEnv), runEnv+"="+ownArgs, home.EnvHome+"="+ptHome, canaryEnv+"="+canary)
			if tt.units {
				cmd.Env = append(cmd.Env, unitsEnv+"=1")
			}
			cmd.Stdout, cmd.Stderr = out, out
			if tt.event != "" {
				in := filepath.Join(tmp, "event.json")
				if err := os.WriteFile(in, []byte(tt.event), 0o600); err != nil {
					t.Fatal(err)
				}
				f, err := os.Open(in)
				if err != nil {
					t.Fatal(err)
				}
				defer f.Close()
				cmd.Stdin = f
			}
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			var waitErr error
			gone := make(chan struct{})
			go func() { waitErr = cmd.Wait(); close(gone) }()
			t.Cleanup(func() {
				exec.Command("systemctl", "--user", "stop", session).Run()
				cmd.Process.Kill()
				<-gone
			})

			// The agent's port.json names its process.
			var pid int
			for deadline := time.Now().Add(30 * time.Second); pid == 0; time.Sleep(50 * time.Millisecond) {
				if pf, err := localhttp.ReadPortFile(dirs.PortFile()); err == nil && pf.PID != 0 {
					pid = pf.PID
					break
				}
				select {
				case <-gone:
					t.Fatalf("the session ended (%v) before the agent wrote its port.json; it printed:\n%s", waitErr, said())
				default:
				}
				if time.Now().After(deadline) {
					t.Fatalf("the agent wrote no port.json within 30 s; the session printed:\n%s", said())
				}
			}
			stopAtCleanup(t, pid)
			if tt.args[0] == "agent" {
				// agent --detach names the process it started, which is the
				// agent itself, also when systemd-run ran in it first. The
				// line is the last it prints.
				printed := func() bool {
					s := said()
					i := strings.Index(s, "started the agent, process ")
					return i >= 0 && strings.HasSuffix(s[i:], "\n")
				}
				for deadline := time.Now().Add(10 * time.Second); !printed(); time.Sleep(20 * time.Millisecond) {
					if time.Now().After(deadline) {
						t.Fatalf("agent --detach didn't say which process it started; it printed:\n%s", said())
					}
				}
				if got := startedPid(t, said()); got != pid {
					t.Errorf("agent --detach says it started process %d, but the agent is process %d", got, pid)
				}
			}

			unit := cgroupUnit(t, pid)
			switch {
			case tt.units && !agentUnitName.MatchString(unit):
				t.Fatalf("the agent, process %d, runs in %s, not in a scope of its own; the session printed:\n%s", pid, unit, said())
			case !tt.units && unit != session:
				t.Fatalf("the agent, process %d, runs in %s, not in the session's %s", pid, unit, session)
			}
			t.Logf("the agent, process %d, runs in %s", pid, unit)
			if !slices.Contains(environOf(t, pid), canaryEnv+"="+canary) {
				t.Errorf("the agent, process %d, doesn't have the session's environment: %s isn't %s there", pid, canaryEnv, canary)
			}
			// A hook starts the agent in the data directory (startHookAgent),
			// and agent --detach where it runs itself, which is where this
			// test runs.
			wantDir := ptHome
			if tt.args[0] == "agent" {
				if wantDir, err = os.Getwd(); err != nil {
					t.Fatal(err)
				}
			}
			if got, want := workingDir(t, pid), realPath(t, wantDir); got != want {
				t.Errorf("the agent, process %d, runs in the folder %s, want %s", pid, got, want)
			}
			if tt.units {
				if got := unitProperty(t, unit, "Description"); got != "pairtime agent" {
					t.Errorf("the agent's unit is described as %q, want pairtime agent", got)
				}
			}

			if b, err := exec.Command("systemctl", "--user", "stop", session).CombinedOutput(); err != nil {
				t.Fatalf("systemctl --user stop %s: %v\n%s", session, err, b)
			}
			select {
			case <-gone:
			case <-time.After(30 * time.Second):
				t.Fatal("the session's scope was stopped, but its process still ran after 30 s")
			}

			if !tt.units {
				if !processGone(pid, 30*time.Second) {
					t.Fatalf("the agent, process %d, still ran 30 s after the session's scope stopped, so this test can't tell", pid)
				}
				t.Logf("the agent, process %d, stopped with %s", pid, session)
				return
			}
			if _, err := queryAgent(dirs); err != nil {
				t.Fatalf("the agent didn't answer once the session's scope had stopped: %v", err)
			}
			t.Logf("%s stopped, and the agent, process %d, still answers", session, pid)
			if got := cgroupUnit(t, pid); got != unit {
				t.Errorf("the agent moved from %s to %s", unit, got)
			}
			if err := stopAgent(dirs); err != nil {
				t.Fatal(err)
			}
			// The scope goes with the agent's last process, and --collect
			// unloads it.
			for deadline := time.Now().Add(15 * time.Second); ; time.Sleep(100 * time.Millisecond) {
				state := unitProperty(t, unit, "LoadState")
				if state == "not-found" {
					t.Logf("the agent stopped, and its unit %s was unloaded", unit)
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("the agent stopped, but its unit %s was still %s after 15 s", unit, state)
				}
			}
		})
	}
}

// cgroupUnit returns the systemd unit the process pid runs in: the last
// element of its cgroup's path, from the unified hierarchy's line of
// /proc/<pid>/cgroup, or the name=systemd line on a legacy one.
func cgroupUnit(t *testing.T, pid int) string {
	t.Helper()
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/cgroup")
	if err != nil {
		t.Fatalf("reading the cgroup of process %d: %v", pid, err)
	}
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.SplitN(line, ":", 3)
		if len(f) == 3 && (f[0] == "0" || f[1] == "name=systemd") {
			return filepath.Base(f[2])
		}
	}
	t.Fatalf("process %d is in no cgroup systemd manages:\n%s", pid, b)
	return ""
}

// workingDir returns the working directory of the process pid.
func workingDir(t *testing.T, pid int) string {
	t.Helper()
	dir, err := os.Readlink("/proc/" + strconv.Itoa(pid) + "/cwd")
	if err != nil {
		t.Fatalf("reading the working directory of process %d: %v", pid, err)
	}
	return dir
}

// realPath returns path with every link in it resolved, as the kernel
// gives a process's working directory.
func realPath(t *testing.T, path string) string {
	t.Helper()
	p, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// environOf returns the environment of the process pid.
func environOf(t *testing.T, pid int) []string {
	t.Helper()
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/environ")
	if err != nil {
		t.Fatalf("reading the environment of process %d: %v", pid, err)
	}
	return strings.Split(string(bytes.TrimSuffix(b, []byte{0})), "\x00")
}

// unitProperty returns a property of the unit from the user's manager.
func unitProperty(t *testing.T, unit, property string) string {
	t.Helper()
	b, err := exec.Command("systemctl", "--user", "show", "--property="+property, "--value", unit).Output()
	if err != nil {
		t.Fatalf("systemctl --user show %s: %v", unit, err)
	}
	return strings.TrimSpace(string(b))
}

// processGone reports whether the process pid ends within d: whether it is
// gone or, until its parent reaps it, a zombie.
func processGone(pid int, d time.Duration) bool {
	for deadline := time.Now().Add(d); ; time.Sleep(50 * time.Millisecond) {
		b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
		if errors.Is(err, fs.ErrNotExist) {
			return true
		}
		// pid (comm) state ...; comm may hold spaces and parentheses.
		if i := bytes.LastIndexByte(b, ')'); err == nil && i >= 0 && bytes.HasPrefix(b[i+1:], []byte(" Z")) {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
	}
}
