// SPDX-License-Identifier: AGPL-3.0-or-later

package main

// What `pairtime install` and `pairtime uninstall` share: where the binary
// goes and how it gets there, the record of the folders install made, and
// how the two find, start and stop the agent.

import (
	"bytes"
	"crypto/sha256"
	"debug/buildinfo"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/some-du6e/pairtime/internal/agent/home"
	"github.com/some-du6e/pairtime/internal/agent/localhttp"
	"github.com/some-du6e/pairtime/internal/install"
)

// modulePath is pairtime's Go module, which the build info of every binary
// built from it names.
const modulePath = "github.com/some-du6e/pairtime"

// setupEnv returns this machine's install.Env, with the binary at its
// per-user path, which every hook and the login item run (CP 7), and
// pairtime's directories. PAIRTIME_HOME is refused: the hooks and the login
// item install writes can't carry it, so they would feed the agent of the
// usual data directory, not the one it names. So are root in someone
// else's home directory and an elevated token on Windows (checkUser).
func setupEnv() (install.Env, home.Dirs, error) {
	if os.Getenv(home.EnvHome) != "" {
		return install.Env{}, home.Dirs{}, fmt.Errorf("%s is set, and the hooks and login item that install writes can't carry it, so they would feed the pairtime of your usual folders; unset it and run this again", home.EnvHome)
	}
	env, err := install.DefaultEnv("")
	if err != nil {
		v := "HOME"
		if runtime.GOOS == "windows" {
			v = "USERPROFILE"
		}
		return install.Env{}, home.Dirs{}, fmt.Errorf("finding your home folder: %w; set %s to its full path, then run this again", err, v)
	}
	if err := checkUser(env.Home); err != nil {
		return install.Env{}, home.Dirs{}, err
	}
	if env.Exe, err = install.BinaryPath(env); err != nil {
		return install.Env{}, home.Dirs{}, err
	}
	dirs, err := home.Default()
	if err != nil {
		if runtime.GOOS == "windows" {
			// BinaryPath has checked LOCALAPPDATA, so it is APPDATA, and
			// home's own message would point at PAIRTIME_HOME, which
			// install refuses.
			err = errors.New(`APPDATA isn't a full path, so pairtime has nowhere to keep its settings; set it to your AppData\Roaming folder, then run this again`)
		}
		return install.Env{}, home.Dirs{}, err
	}
	return env, dirs, nil
}

// self returns the path of the running binary, with links resolved.
func self() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(exe)
}

// sameBinary reports whether the files at a and b hold the same bytes. A
// file that isn't there isn't the same as anything.
func sameBinary(a, b string) (bool, error) {
	ai, err := os.Stat(a)
	if err != nil {
		return false, err
	}
	bi, err := os.Stat(b)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if os.SameFile(ai, bi) {
		return true, nil
	}
	if ai.Size() != bi.Size() {
		return false, nil
	}
	ah, err := fileSum(a)
	if err != nil {
		return false, err
	}
	bh, err := fileSum(b)
	if err != nil {
		return false, err
	}
	return bytes.Equal(ah, bh), nil
}

func fileSum(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return nil, err
	}
	return h.Sum(nil), nil
}

// stageBinary copies src to a new temporary file beside dst, which is
// where the binary goes, and returns the copy's path. commitBinary renames
// it into place, so that a harness that runs a hook meanwhile runs the old
// binary or the new one, never part of one. In between, install runs the
// hook self-test on the copy, since a copy that fails it must never land
// where pairtime's hooks run it. The copy has dst's extension, .exe on
// Windows, so that it runs there.
func stageBinary(src, dst string) (string, error) {
	in, err := os.Open(src)
	if err != nil {
		return "", err
	}
	defer in.Close()
	f, err := os.CreateTemp(filepath.Dir(dst), ".pairtime.new-*"+filepath.Ext(dst))
	if err != nil {
		return "", err
	}
	tmp := f.Name()
	if err := writeBinary(f, in); err != nil {
		f.Close()
		os.Remove(tmp)
		return "", err
	}
	return tmp, nil
}

// writeBinary copies in to f, makes f a program, flushes it to disk and
// closes it.
func writeBinary(f *os.File, in io.Reader) error {
	if _, err := io.Copy(f, in); err != nil {
		return err
	}
	if err := f.Chmod(0o755); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	return f.Close()
}

// checkReplaceable refuses to put pairtime at path, its per-user path, when
// something that isn't pairtime is there: the teen's own script, say, or
// another tool's program of the same name, as pipx and uv put their tools
// in ~/.local/bin too. install would replace it with no copy, and
// uninstall would then delete what install put there, so the file would
// be gone for good.
func checkReplaceable(path string) error {
	if _, err := os.Lstat(path); errors.Is(err, fs.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	if isPairtimeBinary(path) {
		return nil
	}
	return fmt.Errorf("%s is there already, and it isn't pairtime, so install leaves it alone; move it somewhere else, then run pairtime install again", install.Printable(path))
}

// isPairtimeBinary reports whether the file at path is a program built
// from pairtime's module, which its Go build info says without running it.
// Uninstall deletes the binary at pairtime's per-user path only then.
func isPairtimeBinary(path string) bool {
	bi, err := buildinfo.ReadFile(path)
	return err == nil && (bi.Main.Path == modulePath || strings.HasPrefix(bi.Path, modulePath+"/"))
}

// setupRecord is install/setup.json in the data directory: what install
// made outside the files the installer's manifest records, so that
// uninstall can take it away. Uninstall works without it too, from the
// per-user path and pairtime's own directories, and then only leaves
// behind the empty folders install made above them.
type setupRecord struct {
	V int `json:"v"`
	// Binary is where install put the binary.
	Binary string `json:"binary,omitempty"`
	// Made are the folders install made that weren't there, outermost
	// first: for the binary, and above pairtime's own directories.
	Made []string `json:"made,omitempty"`
}

func setupPath(dirs home.Dirs) string { return filepath.Join(dirs.Data, "install", "setup.json") }

// readSetup reads setup.json. One that isn't there is an empty record.
func readSetup(dirs home.Dirs) (setupRecord, error) {
	data, err := os.ReadFile(setupPath(dirs))
	if errors.Is(err, fs.ErrNotExist) {
		return setupRecord{V: 1}, nil
	}
	if err != nil {
		return setupRecord{}, err
	}
	var r setupRecord
	if err := json.Unmarshal(data, &r); err != nil {
		return setupRecord{}, fmt.Errorf("%s is damaged: %w", setupPath(dirs), err)
	}
	return r, nil
}

// writeSetup writes r to setup.json, or deletes the file when r records
// nothing.
func writeSetup(dirs home.Dirs, r setupRecord) error {
	if r.Binary == "" && len(r.Made) == 0 {
		if err := os.Remove(setupPath(dirs)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		return nil
	}
	r.V = 1
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(setupPath(dirs)), 0o700); err != nil {
		return err
	}
	return home.WriteFile(setupPath(dirs), append(data, '\n'))
}

// missingDirs returns dir and the folders above it that aren't there,
// outermost first.
func missingDirs(dir string) []string {
	var out []string
	for d := filepath.Clean(dir); ; {
		if _, err := os.Lstat(d); err == nil {
			break
		}
		out = append(out, d)
		parent := filepath.Dir(d)
		if parent == d {
			break
		}
		d = parent
	}
	slices.Reverse(out)
	return out
}

// notThere returns pairtime's data and config directories, the folder of
// its binary, and the folders above them, that aren't there, outermost
// first. install and uninstall take it before anything else, since even
// planning makes the data directory, for the installer's lock file
// (install.Installer.RemoveLockFile).
func notThere(env install.Env, dirs home.Dirs) []string {
	var missing []string
	for _, d := range []string{dirs.Data, dirs.Config, filepath.Dir(env.Exe)} {
		missing = addMade(missing, missingDirs(d))
	}
	return missing
}

// tidy takes away what a command that changed nothing made anyway, given
// missing, what notThere said before it began: in a data directory that
// wasn't there, the installer's lock file, which planning makes, and the
// agent's, which looking for an agent makes, then each folder of missing
// that is empty by then. A lock file goes only while tidy holds its lock,
// so when another install, or an agent another install started, holds
// one, the file stays, and with it the data directory.
func tidy(in *install.Installer, dirs home.Dirs, missing []string) {
	if slices.Contains(missing, dirs.Data) {
		in.RemoveLockFile()
		removeAgentLock(dirs.Data, nil)
		os.Remove(in.Dir)
	}
	removeEmptyDirs(missing)
}

// removeAgentLock deletes agent.lock in the data directory dir, and only
// while it holds the lock. home's lock is on the file, so deleting the
// file of a lock an agent holds would let a second agent lock a new file
// beside it, and agentLocked, which stopAgent and uninstall rely on, would
// no longer see the first. held is the lock when the caller holds it, and
// removeAgentLock drops it; with nil it takes the lock itself, and leaves
// the file when an agent holds it.
func removeAgentLock(dir string, held *home.Lock) {
	name := filepath.Join(dir, "agent.lock")
	if held == nil {
		if _, err := os.Lstat(name); err != nil {
			return
		}
		l, err := home.Acquire(dir)
		if err != nil {
			return
		}
		held = l
	}
	if removesHeldFiles {
		os.Remove(name)
		held.Release()
		return
	}
	held.Release()
	os.Remove(name)
}

// addMade returns made with each of more it lacks, outermost first.
func addMade(made, more []string) []string {
	for _, d := range more {
		if !slices.Contains(made, d) {
			made = append(made, d)
		}
	}
	slices.SortStableFunc(made, func(a, b string) int { return len(a) - len(b) })
	return made
}

// removeEmptyDirs removes each of dirs that is an empty, real folder,
// innermost first, and returns those it couldn't: a folder something else
// put files in since, or that a link now stands in for, stays.
func removeEmptyDirs(dirs []string) []string {
	var kept []string
	for i := len(dirs) - 1; i >= 0; i-- {
		d := dirs[i]
		info, err := os.Lstat(d)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil || info.Mode().Type() != fs.ModeDir || os.Remove(d) != nil {
			kept = append(kept, d)
		}
	}
	slices.Reverse(kept)
	return kept
}

// within reports whether path is dir or inside it, comparing paths as the
// OS does: in any case on Windows.
func within(path, dir string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// samePath reports whether a and b are the same path, as within compares
// them.
func samePath(a, b string) bool {
	rel, err := filepath.Rel(a, b)
	return err == nil && rel == "."
}

// agentStatus is the part of GET /v1/status that install reads.
type agentStatus struct {
	PID     int `json:"pid"`
	Version struct {
		Version string `json:"version"`
	} `json:"version"`
}

// errAgentDown means port.json names no running agent.
var errAgentDown = errors.New("pairtime's agent isn't running")

// agentHTTP talks to the agent on 127.0.0.1, never through a proxy.
var agentHTTP = &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{Proxy: nil}}

// agentCall sends one request to the API of the agent that uses dirs, with
// the bearer token from port.json, and returns the answer's body. The
// token goes out only while an agent holds the data directory's lock, the
// test stopAgent makes first too: port.json outlives an agent that crashed
// or was killed, and every later agent keeps its token (internal/agent,
// listen), so whatever listens on a stale port since, such as another
// account's program, would get a token that opens this teen's agent.
func agentCall(dirs home.Dirs, method, path string, want int) ([]byte, error) {
	pf, err := localhttp.ReadPortFile(dirs.PortFile())
	if errors.Is(err, fs.ErrNotExist) {
		return nil, errAgentDown
	}
	if err != nil {
		return nil, err
	}
	if pf.PID == 0 || pf.Port <= 0 || !agentLocked(dirs) {
		return nil, errAgentDown
	}
	req, err := http.NewRequest(method, fmt.Sprintf("http://127.0.0.1:%d%s", pf.Port, path), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+pf.Token)
	resp, err := agentHTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != want {
		return nil, fmt.Errorf("the agent answered %s", resp.Status)
	}
	return body, nil
}

// queryAgent returns the status of the agent that uses dirs.
func queryAgent(dirs home.Dirs) (agentStatus, error) {
	body, err := agentCall(dirs, http.MethodGet, "/v1/status", http.StatusOK)
	if err != nil {
		return agentStatus{}, err
	}
	var st agentStatus
	if err := json.Unmarshal(body, &st); err != nil {
		return agentStatus{}, fmt.Errorf("the agent's status: %w", err)
	}
	return st, nil
}

// agentLocked reports whether an agent holds the lock on dirs' data
// directory. Taking the lock is the one test that holds for an agent that
// is starting, stuck or has no port.json, so it is what says an agent is
// gone.
func agentLocked(dirs home.Dirs) bool {
	if info, err := os.Stat(dirs.Data); err != nil || !info.IsDir() {
		return false
	}
	l, err := home.Acquire(dirs.Data)
	if err != nil {
		return errors.Is(err, home.ErrLocked)
	}
	l.Release()
	return false
}

// stopWait is how long install and uninstall wait for an agent to stop.
const stopWait = 15 * time.Second

// stopAgent asks the agent that uses dirs to stop, over its API, and
// waits until its lock is free.
func stopAgent(dirs home.Dirs) error {
	deadline := time.Now().Add(stopWait)
	asked := 0
	for {
		if !agentLocked(dirs) {
			return nil
		}
		if pf, err := localhttp.ReadPortFile(dirs.PortFile()); err == nil && pf.PID != 0 && pf.PID != asked {
			if _, err := agentCall(dirs, http.MethodPost, "/v1/agent/stop", http.StatusAccepted); err == nil {
				asked = pf.PID
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("pairtime's agent didn't stop within %v; stop it yourself (it's the process called pairtime) and run this again", stopWait)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// startWait is how long install waits for the agent it started to answer.
const startWait = 30 * time.Second

// startAgentFrom starts exe's agent in a process of its own, with no
// console window, as `agent --detach` does (detachedAttr), and waits until
// it answers. It runs in the data directory, so it doesn't keep the folder
// install ran in from being deleted. When another agent was already
// running, that one's status is returned.
//
// It asks only an agent that port.json says started since: the one it
// started, or, when that one found another agent holding the data
// directory, one whose port.json was written since. An older port.json is
// an agent's that has stopped, and its port may be anyone's by now. Until
// port.json names its agent, it doesn't touch the lock either (agentCall),
// since an agent that found the lock taken at that moment would exit.
func startAgentFrom(exe string, dirs home.Dirs) (agentStatus, error) {
	cmd := exec.Command(exe, "agent")
	cmd.SysProcAttr = detachedAttr()
	cmd.Dir = dirs.Data
	// Some file systems keep a file's time in whole seconds, or two.
	since := time.Now().Add(-2 * time.Second)
	if err := cmd.Start(); err != nil {
		return agentStatus{}, fmt.Errorf("starting pairtime's agent: %w", err)
	}
	pid := cmd.Process.Pid
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	other := false // the agent it started found another holding the data directory
	deadline := time.Now().Add(startWait)
	for {
		if startedSince(dirs, pid, other, since) {
			if st, err := queryAgent(dirs); err == nil {
				return st, nil
			}
		}
		select {
		case err := <-exited:
			var ee *exec.ExitError
			if errors.As(err, &ee) && ee.ExitCode() == exitAlreadyRunning {
				// Another agent holds the lock, and may still be starting.
				exited, other = nil, true
				continue
			}
			if err == nil {
				err = errors.New("it stopped at once")
			}
			return agentStatus{}, fmt.Errorf("pairtime's agent didn't start: %w; run pairtime agent to see why", err)
		case <-time.After(100 * time.Millisecond):
		}
		if time.Now().After(deadline) {
			if other {
				return agentStatus{}, fmt.Errorf("another pairtime agent holds pairtime's folder but didn't answer within %v; stop it (it's the process called pairtime), then run pairtime install again", startWait)
			}
			return agentStatus{}, fmt.Errorf("pairtime's agent didn't answer within %v of starting; run pairtime agent to see why", startWait)
		}
	}
}

// startedSince reports whether port.json names pid, the agent that
// startAgentFrom started, or, when other is set, was written at since or
// later.
func startedSince(dirs home.Dirs, pid int, other bool, since time.Time) bool {
	pf, err := localhttp.ReadPortFile(dirs.PortFile())
	switch {
	case err != nil || pf.PID == 0:
		return false
	case pf.PID == pid:
		return true
	case !other:
		return false
	}
	info, err := os.Stat(dirs.PortFile())
	return err == nil && !info.ModTime().Before(since)
}
