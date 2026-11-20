// SPDX-License-Identifier: AGPL-3.0-or-later

package git

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The tests drive the real git binary in repos they create under
// t.TempDir(), never a repo on the machine. git's own log, reflog, diff
// --shortstat and post-rewrite hook output are the oracle (PLAN WP-AD5).

// testSecret is the project secret of the tests' resolvers.
var testSecret = bytes.Repeat([]byte{0x42}, SecretLen)

// testNow is the clock of scanners in tests that don't need the real one.
var testNow = time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)

// gitEnv is one test's git setup: its own home and global config, no
// system config, and fixed names, so nothing from the machine leaks in.
type gitEnv struct {
	t    *testing.T
	home string
	env  []string
	// clock is the date the next commit gets, in Unix seconds. Each
	// command that writes advances it by a minute, so every commit has
	// its own author date, as commits a person makes do.
	clock int64
	// realDates leaves dates to git's clock, for tests that match agent
	// commands against reflog times.
	realDates bool
}

const (
	authorName  = "Rowan Example"
	authorEmail = "rowan@example.com"
)

func newGitEnv(t *testing.T) *gitEnv {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git isn't installed")
	}
	home := t.TempDir()
	cfg := filepath.Join(home, "gitconfig")
	conf := "[init]\n\tdefaultBranch = main\n" +
		"[protocol \"file\"]\n\tallow = always\n" +
		"[core]\n\tautocrlf = false\n\tfsmonitor = false\n" +
		"[advice]\n\tdetachedHead = false\n" +
		"[gc]\n\tauto = 0\n"
	if err := os.WriteFile(cfg, []byte(conf), 0o600); err != nil {
		t.Fatal(err)
	}
	env := []string{
		"HOME=" + home,
		"USERPROFILE=" + home,
		"XDG_CONFIG_HOME=" + home,
		"GIT_CONFIG_GLOBAL=" + cfg,
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=" + authorName,
		"GIT_AUTHOR_EMAIL=" + authorEmail,
		"GIT_COMMITTER_NAME=" + authorName,
		"GIT_COMMITTER_EMAIL=" + authorEmail,
		"GIT_EDITOR=true",
		"GIT_SEQUENCE_EDITOR=true",
		"GIT_TERMINAL_PROMPT=0",
		"GIT_MERGE_AUTOEDIT=no",
	}
	return &gitEnv{t: t, home: home, env: env, clock: 1795000000}
}

func (g *gitEnv) runner() *Runner { return &Runner{Env: g.env} }

func (g *gitEnv) resolver(m ProjectMap, ex Excludes) *Resolver {
	g.t.Helper()
	r, err := NewResolver(ResolverOptions{Git: g.runner(), Secret: testSecret, Excludes: ex}, m)
	if err != nil {
		g.t.Fatal(err)
	}
	return r
}

// git runs git in dir as a person would, and returns its stdout.
func (g *gitEnv) git(dir string, args ...string) string {
	g.t.Helper()
	out, err := g.gitErr(dir, nil, args...)
	if err != nil {
		g.t.Fatalf("git %s: %v", strings.Join(args, " "), err)
	}
	return out
}

// baseEnv is the test process's environment without the variables that
// would point git at another repository than the folder's own, so a test
// run from inside a git hook can't touch the outer repo.
func baseEnv() []string {
	var env []string
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		upper := strings.ToUpper(name)
		if redirecting[upper] || strings.HasPrefix(upper, "GIT_CONFIG_KEY_") || strings.HasPrefix(upper, "GIT_CONFIG_VALUE_") {
			continue
		}
		env = append(env, kv)
	}
	return env
}

func (g *gitEnv) gitErr(dir string, extra []string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	env := append(baseEnv(), g.env...)
	if !g.realDates {
		date := fmt.Sprintf("@%d +0000", g.clock)
		env = append(env, "GIT_AUTHOR_DATE="+date, "GIT_COMMITTER_DATE="+date)
	}
	cmd.Env = append(env, extra...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	g.clock += 60
	if err != nil {
		return stdout.String(), fmt.Errorf("%v: %s", err, stderr.String())
	}
	return stdout.String(), nil
}

// testRepo is a repo a test made.
type testRepo struct {
	g   *gitEnv
	dir string
}

// initRepo makes a new repo in a folder of its own.
func (g *gitEnv) initRepo() *testRepo {
	g.t.Helper()
	dir := filepath.Join(g.t.TempDir(), "repo")
	if err := os.Mkdir(dir, 0o755); err != nil {
		g.t.Fatal(err)
	}
	g.git(dir, "init", "-q")
	return &testRepo{g: g, dir: dir}
}

func (r *testRepo) git(args ...string) string {
	r.g.t.Helper()
	return r.g.git(r.dir, args...)
}

// write writes files, relative to the repo, creating folders.
func (r *testRepo) write(files map[string]string) {
	r.g.t.Helper()
	for name, body := range files {
		p := filepath.Join(r.dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			r.g.t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			r.g.t.Fatal(err)
		}
	}
}

// commit writes files, stages everything and commits with message, and
// returns the new HEAD.
func (r *testRepo) commit(message string, files map[string]string) string {
	r.g.t.Helper()
	r.write(files)
	r.git("add", "-A")
	r.git("commit", "-q", "--allow-empty", "-m", message)
	return r.head()
}

func (r *testRepo) head() string {
	r.g.t.Helper()
	return strings.TrimSpace(r.git("rev-parse", "HEAD"))
}

func (r *testRepo) rev(spec string) string {
	r.g.t.Helper()
	return strings.TrimSpace(r.git("rev-parse", spec))
}

// lines returns n numbered lines, for files whose diffs have known sizes.
func lines(prefix string, n int) string {
	var b strings.Builder
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, "%s %d\n", prefix, i)
	}
	return b.String()
}

// oracleCommit is what git itself says about a commit.
type oracleCommit struct {
	parents                   []string
	authorTime, committerTime int64
	files, ins, del           int
}

var shortstatRE = regexp.MustCompile(`^\s*(\d+) files? changed(?:, (\d+) insertions?\(\+\))?(?:, (\d+) deletions?\(-\))?\s*$`)

// oracle reads a commit with `git log` and `git diff --shortstat` against
// its first parent, or against the empty tree for a root commit.
func (r *testRepo) oracle(sha string) oracleCommit {
	r.g.t.Helper()
	f := strings.Split(strings.TrimSpace(r.git("log", "-1", "--format=%P%x00%at%x00%ct", sha)), "\x00")
	var o oracleCommit
	o.parents = strings.Fields(f[0])
	o.authorTime, _ = strconv.ParseInt(f[1], 10, 64)
	o.committerTime, _ = strconv.ParseInt(f[2], 10, 64)
	base := ""
	if len(o.parents) > 0 {
		base = o.parents[0]
	} else {
		empty, err := r.g.gitIn(r.dir, "", "hash-object", "-t", "tree", "--stdin")
		if err != nil {
			r.g.t.Fatal(err)
		}
		base = strings.TrimSpace(empty)
	}
	stat := strings.TrimSpace(r.git("diff", "--shortstat", "-M", base, sha))
	if stat != "" {
		m := shortstatRE.FindStringSubmatch(stat)
		if m == nil {
			r.g.t.Fatalf("unexpected shortstat %q", stat)
		}
		o.files, _ = strconv.Atoi(m[1])
		o.ins, _ = strconv.Atoi(m[2])
		o.del, _ = strconv.Atoi(m[3])
	}
	return o
}

// gitIn runs git with stdin.
func (g *gitEnv) gitIn(dir, stdin string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(baseEnv(), g.env...)
	cmd.Stdin = strings.NewReader(stdin)
	out, err := cmd.Output()
	return string(out), err
}

// reflogNewest returns git's own newest HEAD reflog entry: the commit and
// the message.
func (r *testRepo) reflogNewest() (string, string) {
	r.g.t.Helper()
	out := strings.TrimSpace(r.git("log", "-g", "-1", "--format=%H%x00%gs", "HEAD"))
	sha, msg, _ := strings.Cut(out, "\x00")
	return sha, msg
}

// recordRewrites installs a post-rewrite hook in the test's own repo,
// which writes git's list of rewritten commits to a file; readRewrites
// returns it. git hands this list to the hook after an amend or a rebase,
// and nowhere else, so it is the oracle for Rewrites. Installing a hook in
// .git/hooks of a repo the test made is not core.hooksPath, which the
// adapter never sets.
func (r *testRepo) recordRewrites() {
	r.g.t.Helper()
	gitDir := strings.TrimSpace(r.git("rev-parse", "--absolute-git-dir"))
	out := filepath.ToSlash(filepath.Join(gitDir, "rewritten.txt"))
	hook := "#!/bin/sh\nwhile read old new extra; do echo \"$1 $old $new\" >> '" + out + "'; done\n"
	hooks := filepath.Join(gitDir, "hooks")
	if err := os.MkdirAll(hooks, 0o755); err != nil {
		r.g.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hooks, "post-rewrite"), []byte(hook), 0o755); err != nil {
		r.g.t.Fatal(err)
	}
}

// readRewrites returns git's rewrites for one kind of rewrite, "amend" or
// "rebase", leaving out commits that map to themselves.
func (r *testRepo) readRewrites(kind string) []Rewrite {
	r.g.t.Helper()
	gitDir := strings.TrimSpace(r.git("rev-parse", "--absolute-git-dir"))
	data, err := os.ReadFile(filepath.Join(gitDir, "rewritten.txt"))
	if err != nil {
		r.g.t.Fatalf("git ran no post-rewrite hook: %v", err)
	}
	var out []Rewrite
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		f := strings.Fields(line)
		if len(f) == 3 && f[0] == kind && f[1] != f[2] {
			out = append(out, Rewrite{OldSHA: f[1], NewSHA: f[2]})
		}
	}
	return out
}

func (r *testRepo) clearRewrites() {
	gitDir := strings.TrimSpace(r.git("rev-parse", "--absolute-git-dir"))
	os.Remove(filepath.Join(gitDir, "rewritten.txt"))
}

// sequenceEditor returns a GIT_SEQUENCE_EDITOR that replaces the rebase
// todo list with todo, so an interactive rebase runs with no one at the
// keyboard. git runs the editor through sh, on Windows the one Git for
// Windows ships, which reads forward slashes.
func (r *testRepo) sequenceEditor(todo string) string {
	r.g.t.Helper()
	p := filepath.Join(r.g.t.TempDir(), "todo.txt")
	if err := os.WriteFile(p, []byte(todo), 0o644); err != nil {
		r.g.t.Fatal(err)
	}
	return "GIT_SEQUENCE_EDITOR=cp '" + filepath.ToSlash(p) + "'"
}

// fileURL is a file:// URL for dir, which `git clone --depth` needs: a
// plain path makes a local clone, which ignores --depth.
func fileURL(dir string) string {
	p := filepath.ToSlash(dir)
	if runtime.GOOS == "windows" {
		return "file:///" + p
	}
	return "file://" + p
}

// sameDir reports whether a and b are the same folder, whatever symlinks
// or short names either path goes through.
func sameDir(t *testing.T, a, b string) bool {
	t.Helper()
	fa, err := os.Stat(a)
	if err != nil {
		return false
	}
	fb, err := os.Stat(b)
	if err != nil {
		return false
	}
	return os.SameFile(fa, fb)
}

// linkTo makes a symlink to the folder target and returns its path. On
// Windows, where a symlink can need a privilege the test lacks, a junction
// stands in when junctionOK allows it. git resolves both to the real path,
// and Go's EvalSymlinks resolves only the symlink.
func linkTo(t *testing.T, target string, junctionOK bool) string {
	t.Helper()
	link := filepath.Join(t.TempDir(), "link")
	err := os.Symlink(target, link)
	if err == nil {
		return link
	}
	if runtime.GOOS == "windows" && junctionOK {
		out, jerr := exec.Command("cmd", "/c", "mklink", "/J", link, target).CombinedOutput()
		if jerr == nil {
			return link
		}
		t.Skipf("can't make a symlink (%v) or a junction (%v: %s)", err, jerr, out)
	}
	t.Skipf("can't make a symlink: %v", err)
	return ""
}

var keyRE = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

// scanner returns a scanner for the repo at dir whose first scan is done.
func (g *gitEnv) scanner(res *Resolver, dir string) *Scanner {
	g.t.Helper()
	loc, err := res.Resolve(context.Background(), dir)
	if err != nil {
		g.t.Fatal(err)
	}
	s := NewScanner(g.runner(), loc, ScannerState{}, func() time.Time { return testNow }, res.Rekey)
	if _, err := s.Scan(context.Background()); err != nil {
		g.t.Fatal(err)
	}
	return s
}

func mustScan(t *testing.T, s *Scanner) []Change {
	t.Helper()
	changes, err := s.Scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return changes
}
