// SPDX-License-Identifier: AGPL-3.0-or-later

package git

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// DefaultTimeout caps one git call. Every call the adapter makes reads a
// little from the object store, so a call that takes this long is stuck,
// or the repo is enormous and the adapter should give up on it.
const DefaultTimeout = 20 * time.Second

// defaultMaxOutput caps what one call may print. The biggest output the
// adapter asks for is a page of reflog entries or the root commits of a
// repo with many histories, both far below this.
const defaultMaxOutput = 4 << 20

// maxStderr is how much of a failed call's stderr goes into its error.
const maxStderr = 2 << 10

// ErrNotRepo means the folder isn't inside a git repository.
var ErrNotRepo = errors.New("not a git repository")

// ErrUnsafeRepo means git refused the repository because another user owns
// it, as on some USB drives and network shares. The adapter doesn't
// override safe.directory, which exists to stop exactly that kind of repo
// from running its config's commands; the folder is treated as one
// without git.
var ErrUnsafeRepo = errors.New("git refused the repository because another user owns it (safe.directory)")

// ErrNoGit means no git executable was found.
var ErrNoGit = errors.New("git isn't installed or isn't on PATH")

// ErrOutputTooLong means a call printed more than the adapter reads.
var ErrOutputTooLong = errors.New("git printed more than the adapter reads")

// MinVersion is the oldest git the adapter supports. It is the release
// that added GIT_OPTIONAL_LOCKS, rev-parse --is-shallow-repository and
// trailer formatting with "only" and "unfold" (2017).
var MinVersion = Version{2, 15, 0}

// Runner runs the git CLI. Its zero value runs "git" from PATH. It is
// safe for concurrent use.
type Runner struct {
	// Path is the git executable. Empty means "git", looked up in PATH.
	Path string
	// Env is added to every call's environment after the adapter's own
	// settings. Tests use it to point HOME and git's global config at a
	// temporary directory.
	Env []string
	// Timeout caps one call whose context has no deadline. Zero means
	// DefaultTimeout.
	Timeout time.Duration

	once    sync.Once
	exe     string
	exeErr  error
	version Version
	verErr  error
}

// redirecting are environment variables that would point git at another
// repository, index or object store than the folder's own. A process that
// started the agent from inside a git hook could pass them on.
var redirecting = map[string]bool{
	"GIT_DIR":                          true,
	"GIT_WORK_TREE":                    true,
	"GIT_COMMON_DIR":                   true,
	"GIT_INDEX_FILE":                   true,
	"GIT_OBJECT_DIRECTORY":             true,
	"GIT_ALTERNATE_OBJECT_DIRECTORIES": true,
	"GIT_NAMESPACE":                    true,
	"GIT_PREFIX":                       true,
	"GIT_CONFIG":                       true,
	"GIT_CONFIG_PARAMETERS":            true,
	"GIT_CONFIG_COUNT":                 true,
	"GIT_EXTERNAL_DIFF":                true,
	"GIT_DIFF_OPTS":                    true,
	"GIT_TRACE":                        true,
	"GIT_TRACE2":                       true,
	"GIT_TRACE2_EVENT":                 true,
	"GIT_TRACE2_PERF":                  true,
	"GIT_TRACE_PACKET":                 true,
	"GIT_TRACE_PERFORMANCE":            true,
	"GIT_TRACE_SETUP":                  true,
}

// fixedEnv is what every call runs with. GIT_OPTIONAL_LOCKS=0 keeps git
// from taking the index lock to refresh stat data, so the adapter never
// makes a person's own git command fail with "index.lock exists", and never
// writes the index, which would wake its own watch. GIT_NO_LAZY_FETCH=1
// keeps git from fetching an object a partial clone lacks from its
// promisor remote, which would reach the network and write a pack into
// .git; the call fails instead (see HonorsNoLazyFetch for the versions that
// obey it). LC_ALL=C keeps error messages in English, which ErrNotRepo is
// matched on. Replace refs are ignored so the root commits are the ones a
// fresh clone has.
var fixedEnv = []string{
	"GIT_TERMINAL_PROMPT=0",
	"GIT_OPTIONAL_LOCKS=0",
	"GIT_NO_LAZY_FETCH=1",
	"GIT_NO_REPLACE_OBJECTS=1",
	"GIT_PAGER=cat",
	"LC_ALL=C",
	"LANGUAGE=C",
}

// fixedArgs come before every subcommand. log.showSignature would put GPG
// output in front of formatted log lines. core.fsmonitor=false keeps git
// from running a repo's fsmonitor hook when it reads an index: rev-parse
// --show-superproject-working-tree runs ls-files in the folder above the
// top level, which reads the index of whatever repo holds that folder, and
// the -c reaches that child through GIT_CONFIG_PARAMETERS.
var fixedArgs = []string{"-c", "log.showSignature=false", "-c", "color.ui=never", "-c", "core.quotePath=false", "-c", "core.fsmonitor=false"}

// HonorsNoLazyFetch reports whether git v obeys GIT_NO_LAZY_FETCH. Git
// 2.44 added it, and the security releases of May 2024 made the lazy-fetch
// path itself check it, back to 2.39.4, 2.40.2, 2.41.1, 2.42.2 and 2.43.4.
// A distribution that backported the fix without the version bump counts
// as not obeying it, which only costs stats in partial clones.
func HonorsNoLazyFetch(v Version) bool {
	if !v.Less(Version{2, 44, 0}) {
		return true
	}
	fixedIn := map[int]int{39: 4, 40: 2, 41: 1, 42: 2, 43: 4}
	p, ok := fixedIn[v.Minor]
	return v.Major == 2 && ok && v.Patch >= p
}

func (r *Runner) init() {
	r.once.Do(func() {
		name := r.Path
		if name == "" {
			name = "git"
		}
		r.exe, r.exeErr = exec.LookPath(name)
		if r.exeErr != nil {
			r.exeErr = fmt.Errorf("%w: %v", ErrNoGit, r.exeErr)
			return
		}
		out, err := r.run(context.Background(), "", nil, 1<<10, "version")
		if err != nil {
			r.verErr = err
			return
		}
		r.version, r.verErr = ParseVersion(string(out))
	})
}

// Version returns the git version, or an error when git can't be run or
// is older than MinVersion.
func (r *Runner) Version() (Version, error) {
	r.init()
	if r.exeErr != nil {
		return Version{}, r.exeErr
	}
	if r.verErr != nil {
		return Version{}, r.verErr
	}
	if r.version.Less(MinVersion) {
		return r.version, fmt.Errorf("git %s is older than %s, the oldest the git adapter supports", r.version, MinVersion)
	}
	return r.version, nil
}

// Run runs git with args in dir and returns what it printed on stdout.
func (r *Runner) Run(ctx context.Context, dir string, args ...string) ([]byte, error) {
	if _, err := r.Version(); err != nil {
		return nil, err
	}
	return r.run(ctx, dir, nil, defaultMaxOutput, args...)
}

// RunInput is Run with stdin.
func (r *Runner) RunInput(ctx context.Context, dir string, stdin []byte, args ...string) ([]byte, error) {
	if _, err := r.Version(); err != nil {
		return nil, err
	}
	return r.run(ctx, dir, stdin, defaultMaxOutput, args...)
}

func (r *Runner) run(ctx context.Context, dir string, stdin []byte, limit int, args ...string) ([]byte, error) {
	// A caller's own deadline wins, so a call known to be slow, like the
	// root-commit walk, can have longer.
	if _, ok := ctx.Deadline(); !ok {
		timeout := r.Timeout
		if timeout <= 0 {
			timeout = DefaultTimeout
		}
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	cmd := exec.CommandContext(ctx, r.exe, append(append([]string(nil), fixedArgs...), args...)...)
	cmd.Dir = dir
	cmd.Env = environ(os.Environ(), r.Env)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	out := &capped{max: limit}
	errOut := &capped{max: maxStderr, quiet: true}
	cmd.Stdout = out
	cmd.Stderr = errOut
	hideWindow(cmd)
	err := cmd.Run()
	if out.over {
		return nil, fmt.Errorf("git %s: %w", subcommand(args), ErrOutputTooLong)
	}
	if err != nil {
		msg := strings.TrimSpace(errOut.buf.String())
		if ctx.Err() != nil {
			return nil, fmt.Errorf("git %s: %w", subcommand(args), ctx.Err())
		}
		if strings.Contains(msg, "not a git repository") {
			return nil, ErrNotRepo
		}
		if strings.Contains(msg, "dubious ownership") {
			return nil, ErrUnsafeRepo
		}
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return out.buf.Bytes(), &ExitError{Args: subcommand(args), Code: exit.ExitCode(), Stderr: msg}
		}
		return nil, fmt.Errorf("git %s: %w", subcommand(args), err)
	}
	return out.buf.Bytes(), nil
}

// ExitError is a git call that ran and exited with a non-zero status.
type ExitError struct {
	Args   string // the subcommand
	Code   int
	Stderr string // the first 2 KiB; it can hold paths, so it stays in local logs
}

func (e *ExitError) Error() string {
	if e.Stderr == "" {
		return fmt.Sprintf("git %s exited with status %d", e.Args, e.Code)
	}
	return fmt.Sprintf("git %s exited with status %d: %s", e.Args, e.Code, e.Stderr)
}

// exitCode returns err's exit status, or -1 when git didn't run to an exit.
func exitCode(err error) int {
	var e *ExitError
	if errors.As(err, &e) {
		return e.Code
	}
	return -1
}

func subcommand(args []string) string {
	for i := 0; i < len(args); i++ {
		if args[i] == "-c" || args[i] == "-C" {
			i++
			continue
		}
		if !strings.HasPrefix(args[i], "-") {
			return args[i]
		}
	}
	return "(no subcommand)"
}

// environ returns base without the redirecting variables, then the fixed
// settings, then extra. Later entries win, as os/exec documents.
func environ(base, extra []string) []string {
	env := make([]string, 0, len(base)+len(fixedEnv)+len(extra))
	for _, kv := range base {
		name, _, _ := strings.Cut(kv, "=")
		upper := strings.ToUpper(name) // Windows names are case-insensitive
		if redirecting[upper] || strings.HasPrefix(upper, "GIT_CONFIG_KEY_") || strings.HasPrefix(upper, "GIT_CONFIG_VALUE_") {
			continue
		}
		env = append(env, kv)
	}
	env = append(env, fixedEnv...)
	return append(env, extra...)
}

// capped is a writer that keeps the first max bytes and notes whether
// there were more. A quiet one drops the rest without failing the call.
type capped struct {
	buf   bytes.Buffer
	max   int
	over  bool
	quiet bool
}

func (c *capped) Write(p []byte) (int, error) {
	n := len(p)
	if room := c.max - c.buf.Len(); len(p) > room {
		p = p[:max(room, 0)]
		if !c.quiet {
			c.over = true
		}
	}
	c.buf.Write(p)
	if c.over {
		return 0, io.ErrShortWrite
	}
	return n, nil
}

// Version is a git release number.
type Version struct{ Major, Minor, Patch int }

func (v Version) String() string { return fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch) }

// Less reports whether v is older than w.
func (v Version) Less(w Version) bool {
	if v.Major != w.Major {
		return v.Major < w.Major
	}
	if v.Minor != w.Minor {
		return v.Minor < w.Minor
	}
	return v.Patch < w.Patch
}

// ParseVersion reads `git version` output, such as "git version 2.47.1",
// "git version 2.39.5 (Apple Git-154)" or "git version 2.51.0.windows.1".
func ParseVersion(s string) (Version, error) {
	s = strings.TrimSpace(s)
	rest, ok := strings.CutPrefix(s, "git version ")
	if !ok {
		return Version{}, fmt.Errorf("unexpected `git version` output %q", s)
	}
	rest, _, _ = strings.Cut(rest, " ")
	parts := strings.Split(rest, ".")
	var nums [3]int
	for i := 0; i < len(parts) && i < 3; i++ {
		n, err := strconv.Atoi(parts[i])
		if err != nil {
			if i == 0 {
				return Version{}, fmt.Errorf("unexpected `git version` output %q", s)
			}
			break
		}
		nums[i] = n
	}
	return Version{nums[0], nums[1], nums[2]}, nil
}
