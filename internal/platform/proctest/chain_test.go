// SPDX-License-Identifier: AGPL-3.0-or-later

package proctest

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/platform"
	"github.com/some-du6e/pairtime/pkg/event"
)

const (
	// envOut, in a helper's environment, names the file it writes its
	// result to. A test binary started with it set is a helper.
	envOut = "PAIRTIME_PROCTEST_OUT"
	// envExe and envScript let chain.sh and an interactive shell find the
	// helper and chain.sh.
	envExe    = "PAIRTIME_PROCTEST_EXE"
	envScript = "PAIRTIME_PROCTEST_SCRIPT"
	// envRegroup, set to 1, makes the test binary move into a process group
	// of its own and then exec itself as the helper, the way GNU timeout
	// without --foreground runs a command (regroupSelf).
	envRegroup = "PAIRTIME_PROCTEST_REGROUP"
)

// result is what a helper reports.
type result struct {
	PID     int
	Chain   []platform.Proc
	Lineage platform.Lineage
	Err     string
}

func TestMain(m *testing.M) {
	if os.Getenv(envRegroup) != "" {
		err := regroupSelf()
		fmt.Fprintln(os.Stderr, "proctest regroup:", err)
		os.Exit(1)
	}
	if out := os.Getenv(envOut); out != "" {
		os.Exit(helper(out))
	}
	os.Exit(m.Run())
}

// withoutEnv returns env without the variable name.
func withoutEnv(env []string, name string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		if !strings.HasPrefix(kv, name+"=") {
			out = append(out, kv)
		}
	}
	return out
}

// helper runs at the bottom of a chain a test started. It reads the
// process table, as the hook path will, and classifies its own chain as a
// harness's.
func helper(out string) int {
	r := result{PID: os.Getpid()}
	procs, err := readOS()
	if err != nil {
		r.Err = err.Error()
	} else {
		r.Chain = platform.NewTable(goos, procs).Chain(r.PID)
		r.Lineage = platform.Classify(goos, r.Chain)
	}
	data, err := json.Marshal(r)
	if err == nil {
		err = os.WriteFile(out, data, 0o600)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "proctest helper:", err)
		return 1
	}
	return 0
}

// rig holds what the chains need. exe and script are slash paths, which
// bash takes on every OS; helper is the helper's native path.
type rig struct {
	exe, helper, script, dir, bash, python string
}

var outs atomic.Int64

func newRig(t *testing.T) *rig {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "chain.sh")
	// The helper isn't the script's last command, so bash can't exec it in
	// place of itself and drop out of the chain.
	body := "\"$" + envExe + "\"\nstatus=$?\nexit $status\n"
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	return &rig{
		exe:    filepath.ToSlash(exe),
		helper: exe,
		script: filepath.ToSlash(script),
		dir:    dir,
		bash:   need(t, "bash", bashPath),
		python: need(t, "python", pythonPath),
	}
}

// need finds a program. In CI a missing one fails the test, so a changed
// runner image can't quietly skip the oracle; elsewhere it skips.
func need(t *testing.T, what string, find func() (string, error)) string {
	t.Helper()
	path, err := find()
	if err == nil {
		return path
	}
	if os.Getenv("CI") != "" {
		t.Fatalf("no %s on this runner: %v", what, err)
	}
	t.Skipf("no %s here: %v", what, err)
	return ""
}

func (r *rig) env(out string) []string {
	return append(os.Environ(),
		envOut+"="+filepath.ToSlash(out),
		envExe+"="+r.exe,
		envScript+"="+r.script,
		"BASH_SILENCE_DEPRECATION_WARNING=1",
	)
}

func (r *rig) outFile() string {
	return filepath.Join(r.dir, fmt.Sprintf("out-%d.json", outs.Add(1)))
}

// run starts name with args at the top of a chain and returns what the
// helper at its bottom reported.
func (r *rig) run(t *testing.T, name string, args ...string) result {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	out := r.outFile()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = r.env(out)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s %q: %v\n%s", name, args, err, b)
	}
	return readResult(t, out)
}

// interact types line into an interactive bash on a terminal and returns
// what the helper the line starts reported.
func (r *rig) interact(t *testing.T, line string) result {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	out := r.outFile()
	if err := interactive(ctx, r.bash, r.env(out), line); err != nil {
		t.Fatalf("interactive bash running %q: %v", line, err)
	}
	return readResult(t, out)
}

func readResult(t *testing.T, out string) result {
	t.Helper()
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("the helper wrote no result: %v", err)
	}
	var r result
	if err := json.Unmarshal(data, &r); err != nil {
		t.Fatal(err)
	}
	if r.Err != "" {
		t.Fatalf("the helper couldn't read the process table: %s", r.Err)
	}
	for _, p := range r.Chain {
		t.Logf("  pid %d ppid %d %q alias %q pgid %d tty %s start %s", p.PID, p.PPID, p.Name, p.Alias, p.PGID, p.TTY, p.Start.Format(time.RFC3339Nano))
	}
	t.Logf("  => %s, entrypoint %s, has_tty %v, decided by #%d, script shell %v",
		r.Lineage.ParentClass, r.Lineage.Entrypoint, r.Lineage.HasTTY, r.Lineage.By, r.Lineage.ScriptShell)
	summarize(t, r)
	return r
}

// summarize adds the chain to the job summary when the test runs in
// GitHub Actions, so every CI run records the real chains each reader saw
// on its OS. The chains hold only the runner's own test processes.
func summarize(t *testing.T, r result) {
	t.Helper()
	path := os.Getenv("GITHUB_STEP_SUMMARY")
	if path == "" {
		return
	}
	var b strings.Builder
	fmt.Fprintf(&b, "\n#### %s, %s\n\n| # | pid | ppid | name | alias | pgid | tty |\n|---:|---:|---:|---|---|---:|---|\n", t.Name(), goos)
	for i, p := range r.Chain {
		fmt.Fprintf(&b, "| %d | %d | %d | %s | %s | %d | %s |\n", i, p.PID, p.PPID, p.Name, p.Alias, p.PGID, p.TTY)
	}
	fmt.Fprintf(&b, "\n%s, entrypoint %s, has_tty %v, decided by #%d, script shell %v\n",
		r.Lineage.ParentClass, r.Lineage.Entrypoint, r.Lineage.HasTTY, r.Lineage.By, r.Lineage.ScriptShell)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Logf("couldn't add to the job summary: %v", err)
		return
	}
	defer f.Close()
	if _, err := f.WriteString(b.String()); err != nil {
		t.Logf("couldn't add to the job summary: %v", err)
	}
}

// checkChain checks what every chain must show: the helper itself first,
// under its own executable name, and the test process among its
// ancestors, which proves the parent links in between.
func checkChain(t *testing.T, r result) {
	t.Helper()
	if len(r.Chain) < 2 || r.Chain[0].PID != r.PID {
		t.Fatalf("the helper's chain doesn't start at the helper, pid %d: %+v", r.PID, r.Chain)
	}
	exe, _ := os.Executable()
	if want := platform.ExeName(goos, exe); !strings.EqualFold(r.Chain[0].Name, want) {
		t.Errorf("the helper is named %q, want %q", r.Chain[0].Name, want)
	}
	if indexOf(r.Chain, os.Getpid()) < 0 {
		t.Errorf("the test process, pid %d, isn't in the helper's chain", os.Getpid())
	}
}

func indexOf(chain []platform.Proc, pid int) int {
	for i, p := range chain {
		if p.PID == pid {
			return i
		}
	}
	return -1
}

// checkTTY checks the terminal the reader reports for the helper and what
// Classify makes of it: attached, in a foreground group, when the chain
// runs on a terminal the test opened; none in CI, whose processes have no
// terminal; unknown on Windows, which doesn't say.
func checkTTY(t *testing.T, r result, onTerminal bool) {
	t.Helper()
	h := r.Chain[0]
	switch {
	case goos == platform.Windows:
		if h.TTY != platform.TTYUnknown || h.TPGID != 0 {
			t.Errorf("TTY = %s, TPGID = %d on Windows, want unknown and 0", h.TTY, h.TPGID)
		}
	case onTerminal:
		if h.TTY != platform.TTYAttached || !r.Lineage.HasTTY || h.TPGID <= 0 {
			t.Errorf("TTY = %s, TPGID = %d, has_tty %v on a terminal, want attached, a foreground group and has_tty", h.TTY, h.TPGID, r.Lineage.HasTTY)
		}
	case os.Getenv("CI") != "":
		if h.TTY != platform.TTYNone || r.Lineage.HasTTY || h.TPGID != 0 {
			t.Errorf("TTY = %s, TPGID = %d, has_tty %v in CI, which has no terminal; want none, 0 and no has_tty", h.TTY, h.TPGID, r.Lineage.HasTTY)
		}
	}
}

func decider(r result) platform.Proc {
	if r.Lineage.By < 0 {
		return platform.Proc{}
	}
	return r.Chain[r.Lineage.By]
}

func TestRealChains(t *testing.T) {
	r := newRig(t)
	self := os.Getpid()

	t.Run("python running bash running a harness is script_runtime, the shell skipped", func(t *testing.T) {
		res := r.run(t, r.python, "-c", "import subprocess, sys; sys.exit(subprocess.call(sys.argv[1:]))", r.bash, r.script)
		checkChain(t, res)
		checkTTY(t, res, false)
		if res.Lineage.ParentClass != event.ParentScriptRuntime {
			t.Errorf("parent_class = %s, want script_runtime", res.Lineage.ParentClass)
		}
		if by := decider(res); !strings.HasPrefix(strings.ToLower(by.Name), "python") {
			t.Errorf("decided by %q, want python", by.Name)
		}
		if i, j := indexOf(res.Chain, decider(res).PID), indexOf(res.Chain, self); i < 2 || j < i {
			t.Errorf("python is #%d and the test #%d in the chain; want bash between the helper and python, and python under the test", i, j)
		}
	})

	t.Run("a harness straight under a program the table doesn't know is unknown", func(t *testing.T) {
		res := r.run(t, r.helper)
		checkChain(t, res)
		checkTTY(t, res, false)
		if res.Lineage.ParentClass != event.ParentUnknown || decider(res).PID != self {
			t.Errorf("parent_class = %s decided by pid %d, want unknown decided by the test, pid %d", res.Lineage.ParentClass, decider(res).PID, self)
		}
	})

	t.Run(scriptCase.name, func(t *testing.T) {
		res := r.run(t, r.bash, r.script)
		checkChain(t, res)
		checkTTY(t, res, false)
		if res.Lineage.ParentClass != scriptCase.class || res.Lineage.ScriptShell != scriptCase.scriptShell {
			t.Errorf("parent_class = %s, script shell %v; want %s, %v", res.Lineage.ParentClass, res.Lineage.ScriptShell, scriptCase.class, scriptCase.scriptShell)
		}
	})

	t.Run("an interactive shell is skipped, so a harness typed into it isn't a script", func(t *testing.T) {
		if interactive == nil {
			t.Skipf("no interactive shell test on %s, where shells are always skipped", goos)
		}
		res := r.interact(t, `"$`+envExe+`"; exit`)
		checkChain(t, res)
		checkTTY(t, res, true)
		if h := res.Chain[0]; h.PGID != h.TPGID {
			t.Errorf("the helper is in group %d, but the terminal's foreground group is %d; a command typed into a shell is its foreground job", h.PGID, h.TPGID)
		}
		if res.Lineage.ParentClass == event.ParentScriptRuntime || res.Lineage.ScriptShell {
			t.Errorf("parent_class = %s, script shell %v; an interactive shell must not count as a script", res.Lineage.ParentClass, res.Lineage.ScriptShell)
		}
	})

	t.Run("bash running a script typed into an interactive shell is script_runtime", func(t *testing.T) {
		if interactive == nil {
			t.Skipf("no interactive shell test on %s", goos)
		}
		res := r.interact(t, `"`+r.bash+`" "$`+envScript+`"; exit`)
		checkChain(t, res)
		checkTTY(t, res, true)
		if res.Lineage.ParentClass != event.ParentScriptRuntime || !res.Lineage.ScriptShell {
			t.Errorf("parent_class = %s, script shell %v; want script_runtime from the shell running chain.sh", res.Lineage.ParentClass, res.Lineage.ScriptShell)
		}
	})

	t.Run("a script whose command leaves the script's process group is still script_runtime", func(t *testing.T) {
		if interactive == nil || regroup == nil {
			t.Skipf("no process groups on %s", goos)
		}
		script := filepath.Join(r.dir, "regroup.sh")
		body := regroup(t, r) + "\nstatus=$?\nexit $status\n"
		if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
			t.Fatal(err)
		}
		res := r.interact(t, `"`+r.bash+`" "`+filepath.ToSlash(script)+`"; exit`)
		checkChain(t, res)
		checkTTY(t, res, true)
		if h := res.Chain[0]; h.PGID == h.TPGID {
			t.Errorf("the helper is in the terminal's foreground group %d; regroup.sh's command should have moved it out", h.TPGID)
		}
		if res.Lineage.ParentClass != event.ParentScriptRuntime || !res.Lineage.ScriptShell {
			t.Errorf("parent_class = %s, script shell %v; want script_runtime from the shell running regroup.sh, which holds the terminal", res.Lineage.ParentClass, res.Lineage.ScriptShell)
		}
	})
}

func TestReadsThisProcess(t *testing.T) {
	procs, err := readOS()
	if err != nil {
		t.Fatal(err)
	}
	if len(procs) < 3 {
		t.Fatalf("the table has %d processes", len(procs))
	}
	tab := platform.NewTable(goos, procs)
	me, ok := tab.Get(os.Getpid())
	if !ok {
		t.Fatalf("this process, pid %d, isn't in the table", os.Getpid())
	}
	t.Run("the parent is the one the OS reports", func(t *testing.T) {
		if me.PPID != os.Getppid() {
			t.Errorf("PPID = %d, want %d", me.PPID, os.Getppid())
		}
	})
	t.Run("the name is the executable's", func(t *testing.T) {
		exe, _ := os.Executable()
		if want := platform.ExeName(goos, exe); !strings.EqualFold(me.Name, want) {
			t.Errorf("Name = %q, want %q", me.Name, want)
		}
	})
	t.Run("the start time is this run's", func(t *testing.T) {
		if me.Start.IsZero() || time.Since(me.Start) > time.Hour || time.Until(me.Start) > 2*time.Second {
			t.Errorf("Start = %s, now %s", me.Start, time.Now())
		}
	})
	t.Run("the process group and terminal are what this OS reports", func(t *testing.T) {
		if me.PGID != ownPGID() {
			t.Errorf("PGID = %d, want %d", me.PGID, ownPGID())
		}
		if (me.TTY == platform.TTYUnknown) != (goos == platform.Windows) {
			t.Errorf("TTY = %s on %s", me.TTY, goos)
		}
		if me.TTY != platform.TTYAttached && me.TPGID != 0 {
			t.Errorf("TPGID = %d for a process with no terminal", me.TPGID)
		}
	})
	t.Run("this process is this user's", func(t *testing.T) {
		if want := selfOwner(); me.Owner == "" || me.Owner != want {
			t.Errorf("Owner = %q, want %q", me.Owner, want)
		}
	})
	t.Run("a process the OS runs has another owner", func(t *testing.T) {
		if runsAsOS() {
			t.Skip("this test runs as the OS's own user or session")
		}
		p, ok := tab.Get(osPID)
		if !ok {
			t.Fatalf("pid %d isn't in the table", osPID)
		}
		if p.Owner == "" || p.Owner == me.Owner {
			t.Errorf("pid %d (%s) has Owner %q, this process %q; want another owner", p.PID, p.Name, p.Owner, me.Owner)
		}
	})
	t.Run("a second read gives this process the same ID", func(t *testing.T) {
		again, err := readOS()
		if err != nil {
			t.Fatal(err)
		}
		p, ok := platform.NewTable(goos, again).Get(os.Getpid())
		if !ok || p.ID() != me.ID() || me.ID().Start == 0 {
			t.Errorf("ID = %+v, then %+v (found %v); want the same, with a start", me.ID(), p.ID(), ok)
		}
		if goos == platform.Linux && me.Ticks == 0 {
			t.Error("Ticks = 0 on Linux")
		}
	})
	t.Run("a process this user can't open still has a start time", func(t *testing.T) {
		if goos != platform.Windows {
			t.Skip("only the Windows reader opens processes")
		}
		for _, p := range procs {
			if strings.EqualFold(p.Name, "services") {
				if p.Start.IsZero() {
					t.Errorf("services.exe, pid %d, has no start time", p.PID)
				}
				return
			}
		}
		t.Error("services.exe isn't in the table")
	})
	t.Run("the parent is in the table and started first", func(t *testing.T) {
		parent, ok := tab.Parent(me)
		if !ok {
			t.Fatalf("the parent, pid %d, isn't in the table or started after this process", me.PPID)
		}
		if !parent.Start.IsZero() && parent.Start.After(me.Start) {
			t.Errorf("the parent started at %s, after this process at %s", parent.Start, me.Start)
		}
	})
}
