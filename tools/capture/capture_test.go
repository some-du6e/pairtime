// SPDX-License-Identifier: AGPL-3.0-or-later

package capture

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/pkg/event"
)

// need returns the path of a tool a test runs. Outside CI a missing tool
// skips the test. In CI (GitHub sets CI=true) it fails, so a runner image
// that drops a tool can't switch a check off without anyone noticing.
func need(t *testing.T, tool string) string {
	t.Helper()
	p, err := exec.LookPath(tool)
	if err != nil {
		if os.Getenv("CI") != "" {
			t.Fatalf("%s isn't installed; CI needs it to test the capture kit", tool)
		}
		t.Skipf("%s isn't installed", tool)
	}
	return p
}

func kit(t *testing.T, name string) string {
	t.Helper()
	p, err := filepath.Abs(name)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func unixOnly(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the kit's tests run on Linux in ci.yml")
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func write(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func jsonText(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// tree maps every file under root, by slash-separated relative path, to its
// contents.
func tree(t *testing.T, root string) map[string]string {
	t.Helper()
	files := map[string]string{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		b, err := os.ReadFile(p)
		files[filepath.ToSlash(rel)] = string(b)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

// result is what one run of a kit script did.
type result struct {
	stdout, stderr string
	code           int
}

func run(t *testing.T, dir string, stdin []byte, env []string, argv ...string) result {
	t.Helper()
	return runEnv(t, dir, stdin, append(os.Environ(), env...), argv...)
}

// runEnv is run with environ as the script's whole environment.
func runEnv(t *testing.T, dir string, stdin []byte, environ []string, argv ...string) result {
	t.Helper()
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = dir
	cmd.Stdin = bytes.NewReader(stdin)
	cmd.Env = environ
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	r := result{stdout: out.String(), stderr: errb.String()}
	var exit *exec.ExitError
	switch {
	case errors.As(err, &exit):
		r.code = exit.ExitCode()
	case err != nil:
		t.Fatalf("%v: %v", argv, err)
	}
	return r
}

// without returns environ without the variables named.
func without(environ []string, names ...string) []string {
	var out []string
	for _, kv := range environ {
		name, _, _ := strings.Cut(kv, "=")
		if !slices.Contains(names, name) {
			out = append(out, kv)
		}
	}
	return out
}

func python(t *testing.T, script string, args ...string) []string {
	t.Helper()
	// -I keeps the runner's PYTHON* variables and user site out of it.
	return append([]string{need(t, "python3"), "-I", "-B", kit(t, script)}, args...)
}

func TestShellcheck(t *testing.T) {
	unixOnly(t)
	cases := []struct {
		name, file string
	}{
		{"the sh dumper passes shellcheck as POSIX sh", "dump-hook.sh"},
		{"Gemini's deny-once hook passes shellcheck as POSIX sh", "deny-once.sh"},
		{"Claude Code's allow-once hook passes shellcheck as POSIX sh", "allow-once.sh"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := run(t, ".", nil, nil, need(t, "shellcheck"), "--shell=sh", "--severity=style", c.file)
			if r.code != 0 {
				t.Fatalf("shellcheck %s:\n%s%s", c.file, r.stdout, r.stderr)
			}
		})
	}
}

// A CRLF checkout of a shell script makes sh fail on the carriage returns,
// and Git for Windows checks text out with CRLF by default.
func TestLineEndings(t *testing.T) {
	unixOnly(t)
	git := need(t, "git")
	if r := run(t, ".", nil, nil, git, "rev-parse", "--is-inside-work-tree"); r.code != 0 {
		t.Skip("not a git checkout")
	}
	scripts, err := filepath.Glob("*.sh")
	if err != nil || len(scripts) == 0 {
		t.Fatalf("no shell scripts in the kit: %v", err)
	}
	for _, f := range scripts {
		t.Run(f+" is checked out with LF endings whatever core.autocrlf says", func(t *testing.T) {
			r := run(t, ".", nil, nil, git, "check-attr", "eol", "--", f)
			if got := strings.TrimSpace(r.stdout); got != f+": eol: lf" {
				t.Fatalf("git check-attr eol = %q, want %q", got, f+": eol: lf")
			}
		})
	}
}

// TestScripts checks SCRIPTS.md's step lists. Step 4's prompt refers to the
// spec pasted at step 3, and WP-AD0 wants a pasted prompt in every script.
func TestScripts(t *testing.T) {
	text := read(t, "SCRIPTS.md")
	sections := map[string]string{}
	for _, part := range regexp.MustCompile(`(?m)^## `).Split(text, -1)[1:] {
		title, body, _ := strings.Cut(part, "\n")
		sections[title] = body
	}
	lists := regexp.MustCompile(`\bsteps ((?:\d+(?: \([^)]*\))?(?:, |,? and )?)+)`)
	numbers := func(list string) map[string]bool {
		list = regexp.MustCompile(`\([^)]*\)`).ReplaceAllString(list, "")
		got := map[string]bool{}
		for _, n := range regexp.MustCompile(`\d+`).FindAllString(list, -1) {
			got[n] = true
		}
		return got
	}
	t.Run("every step list with step 4 has step 3, whose paste step 4 refers to", func(t *testing.T) {
		for _, m := range lists.FindAllStringSubmatch(text, -1) {
			if got := numbers(m[1]); got["4"] && !got["3"] {
				t.Errorf("%q has step 4 without step 3", m[0])
			}
		}
	})
	// A nested claude -p is the only run whose parents show claude under
	// claude, and a message typed while the agent works is the only source
	// of agent.queue (SM 1.2; HS 3.2, 12.1).
	claude := sections["Claude Code"]
	for _, c := range []struct{ name, row, prompt string }{
		{"the Claude Code script starts a nested session", "| 20 | Type the step 20 prompt", `claude -p "Say nested."`},
		{"the Claude Code script queues a message while /goal works", "| 21 | While step 13 is still working",
			"When you're done, say which test you fixed."},
		// A permission a hook answered is the case Claude Code's own report
		// has to tell apart from the person's (SM 1.2), and the process on
		// the far end of the sink's connections is what the OTLP receiver's
		// peer check looks up (DESIGN 5.2).
		{"the Claude Code script has the capture's hook answer one permission prompt", "| 22 | Type the step 22 prompt",
			"Create a file named hook-allowed.txt holding the single line: capture test"},
		{"the Claude Code script records which process holds the sink's connections", "| 23 | In a third terminal, run the step 23 command",
			"lsof -nP -iTCP:4318 -sTCP:ESTABLISHED -Fpc >> ROOT/runs/cli/connections"},
	} {
		t.Run(c.name, func(t *testing.T) {
			if !strings.Contains(claude, c.row) || !strings.Contains(claude, c.prompt+"\n```") {
				t.Fatalf("no table row %q with its prompt %q", c.row, c.prompt)
			}
		})
	}
	for _, h := range []string{"Claude Code", "Codex", "Cursor", "GitHub Copilot", "Gemini CLI", "Devin", "Cline"} {
		t.Run("the "+h+" script has the pasted prompt", func(t *testing.T) {
			body, ok := sections[h]
			if !ok {
				t.Fatalf("no %s section", h)
			}
			if h == "Claude Code" {
				if !strings.Contains(body, "| 3 | Copy the three-line block below and paste it as one prompt") {
					t.Fatal("the steps table has no step 3 paste")
				}
				return
			}
			for _, m := range lists.FindAllStringSubmatch(body, -1) {
				if numbers(m[1])["3"] {
					return
				}
			}
			t.Fatalf("no step list in the %s section has step 3", h)
		})
	}
}

// dumper is one of the two hook dumpers, as the command line that runs it.
type dumper struct {
	name string
	argv func(t *testing.T) []string
}

var dumpers = []dumper{
	{"sh", func(t *testing.T) []string { return []string{need(t, "sh"), kit(t, "dump-hook.sh")} }},
	{"pwsh", func(t *testing.T) []string {
		return []string{need(t, "pwsh"), "-NoProfile", "-NonInteractive", "-File", kit(t, "dump-hook.ps1")}
	}},
}

const (
	secretName  = "PAIRTIME_CAPTURE_TEST_SECRET"
	secretValue = "value-that-must-never-be-written-7f3a"

	// entrypointVar is the variable whose value the dumpers keep, and
	// entrypointSentinel a value of it one character too long to keep.
	entrypointVar      = "CLAUDE_CODE_ENTRYPOINT"
	entrypointSentinel = "pairtime-sentinel-entrypoint-6d1f"
)

// A payload with the shapes that break naive copies: non-ASCII text, CRLF,
// and no newline at the end.
var payload = []byte("{\"session_id\":\"3f9c\",\"hook_event_name\":\"UserPromptSubmit\",\"prompt\":\"caf\u00e9 \u2603\"}\r\n{\"tail\":true}")

// hostile is a hook payload whose strings hold command substitutions, as an
// agent's reply quoting a command can. A shell that reads it as its script
// runs both and creates the two files.
var hostile = []byte("{\"session_id\":\"x\",\"last_assistant_message\":\"I ran \x60touch backtick-ran\x60 and $(touch dollar-ran)\"}")

// lyingMkdir returns a PATH setting whose mkdir reports success for a
// folder that is already there. uutils mkdir before 0.11, Ubuntu 26.04's,
// can tell a process that lost a race for a folder just that (uutils issue
// 13970), so the hooks must never take mkdir's answer as a claim. With this
// mkdir a test sees that answer every time, on any machine.
func lyingMkdir(t *testing.T) string {
	t.Helper()
	real := need(t, "mkdir")
	bin := t.TempDir()
	script := "#!/bin/sh\nfor last; do :; done\nif [ -d \"$last\" ]; then\n\texit 0\nfi\nexec '" +
		strings.ReplaceAll(real, "'", `'\''`) + "' \"$@\"\n"
	if err := os.WriteFile(filepath.Join(bin, "mkdir"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return "PATH=" + bin + string(os.PathListSeparator) + os.Getenv("PATH")
}

// harnessSentinel is an argument the fake harnesses in TestDumpers are
// started with. The dumpers read a harness's arguments only to answer yes
// or no, so it must never reach a run folder (E-procs-cmdlines).
const harnessSentinel = "pairtime-sentinel-argument-6d1f"

// shQuote quotes s for a POSIX shell.
func shQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// appclassHarnesses returns the executable names that
// internal/platform/appclass.json gives harnesses, sorted. The nearest
// parent with one of them is the harness a dumper's flags file describes.
func appclassHarnesses(t *testing.T) []string {
	t.Helper()
	var table struct {
		Entries []struct {
			Names []string `json:"names"`
			Role  string   `json:"role"`
		} `json:"entries"`
	}
	if err := json.Unmarshal([]byte(read(t, filepath.Join("..", "..", "internal", "platform", "appclass.json"))), &table); err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range table.Entries {
		if e.Role == "harness" {
			names = append(names, e.Names...)
		}
	}
	sort.Strings(names)
	return names
}

// harnessParent reports whether a parents line, "pid tty name", names one
// of the harnesses in names, matched as the dumpers match it: in lower case
// and without .exe.
func harnessParent(names []string, line string) bool {
	parts := strings.SplitN(line, " ", 3)
	if len(parts) < 3 {
		return false
	}
	name := strings.TrimSuffix(strings.ToLower(parts[2]), ".exe")
	for _, n := range names {
		if n == name {
			return true
		}
	}
	return false
}

func TestDumpers(t *testing.T) {
	unixOnly(t)
	t.Run("both dumpers look for the harness names internal/platform/appclass.json gives", func(t *testing.T) {
		want := strings.Join(appclassHarnesses(t), " ")
		sh := regexp.MustCompile(`(?m)^harnesses='([^']*)'$`).FindStringSubmatch(read(t, "dump-hook.sh"))
		ps := regexp.MustCompile(`(?m)^\$harnesses = @\(([^)]*)\)$`).FindStringSubmatch(read(t, "dump-hook.ps1"))
		if sh == nil || ps == nil {
			t.Fatalf("a dumper has no list of harnesses: dump-hook.sh %t, dump-hook.ps1 %t", sh != nil, ps != nil)
		}
		for file, list := range map[string]string{"dump-hook.sh": sh[1], "dump-hook.ps1": strings.NewReplacer("'", " ", ",", " ").Replace(ps[1])} {
			names := strings.Fields(list)
			sort.Strings(names)
			if got := strings.Join(names, " "); got != want {
				t.Errorf("%s looks for %q, appclass.json names %q", file, got, want)
			}
		}
	})
	t.Run("both dumpers skip the values of the same codex options", func(t *testing.T) {
		sh := regexp.MustCompile(`(?m)^codex_values='([^']*)'$`).FindStringSubmatch(read(t, "dump-hook.sh"))
		ps := regexp.MustCompile(`(?m)^\$codexValues = @\(([^)]*)\)$`).FindStringSubmatch(read(t, "dump-hook.ps1"))
		if sh == nil || ps == nil {
			t.Fatalf("a dumper has no list of codex's options that take a value: dump-hook.sh %t, dump-hook.ps1 %t", sh != nil, ps != nil)
		}
		shOptions := strings.Fields(sh[1])
		psOptions := strings.Fields(strings.NewReplacer("'", " ", ",", " ").Replace(ps[1]))
		sort.Strings(shOptions)
		sort.Strings(psOptions)
		if strings.Join(shOptions, " ") != strings.Join(psOptions, " ") {
			t.Errorf("dump-hook.sh skips the values of %q, dump-hook.ps1 those of %q", shOptions, psOptions)
		}
	})
	for _, d := range dumpers {
		t.Run(d.name, func(t *testing.T) {
			base := d.argv(t)
			// callEnv runs one hook call in a fresh run directory, with env
			// set on top of this process's environment, and returns the run
			// directory and what the dumper printed. CLAUDE_CODE_ENTRYPOINT is
			// set only when env sets it, so a test run from a Claude Code
			// session sees what CI sees.
			callEnv := func(t *testing.T, env []string, stdin []byte, args ...string) (string, result) {
				t.Helper()
				runDir := filepath.Join(t.TempDir(), "run")
				argv := append(append([]string{}, base...), append([]string{runDir, "UserPromptSubmit"}, args...)...)
				return runDir, runEnv(t, t.TempDir(), stdin, append(without(os.Environ(), entrypointVar), env...), argv...)
			}
			// call is callEnv with a secret in a variable of its own.
			call := func(t *testing.T, stdin []byte, args ...string) (string, result) {
				t.Helper()
				return callEnv(t, []string{secretName + "=" + secretValue}, stdin, args...)
			}
			first := func(runDir string) string { return filepath.Join(runDir, "hooks", "000001") }
			// harnessCall runs one hook call under a fake harness and returns
			// the run directory. The fake is a link to sh with the harness's
			// name, which is the name ps shows. It reads the dumper's command
			// from stdin, so the arguments after its -s are the harness's own;
			// the first can't start with a dash, or sh takes it for an option
			// of its own. codex reads that -s as its --sandbox, so a codex
			// call's first argument is a sandbox mode. With through, the fake
			// runs the dumper through a shell that stays between them, as
			// allow-once.sh does, and Git Bash or PowerShell on Windows. A
			// hook command run with sh -c doesn't stay when it's one command:
			// the shell runs the dumper in its own place.
			harnessCall := func(t *testing.T, through bool, name string, args ...string) string {
				t.Helper()
				dir := t.TempDir()
				runDir := filepath.Join(dir, "run")
				in := filepath.Join(dir, "payload")
				write(t, in, string(payload))
				exe := filepath.Join(dir, "bin", name)
				if err := os.MkdirAll(filepath.Dir(exe), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(need(t, "sh"), exe); err != nil {
					t.Fatal(err)
				}
				var cmd strings.Builder
				for _, a := range append(append([]string{}, base...), runDir, "UserPromptSubmit") {
					cmd.WriteString(shQuote(a) + " ")
				}
				cmd.WriteString("<" + shQuote(in) + "\nexit $?\n")
				script := cmd.String()
				if through {
					// The exit after the dumper keeps the shell from running
					// the dumper in its own place, so it stays its parent.
					script = shQuote(need(t, "sh")) + " -c " + shQuote(script) + "\nexit $?\n"
				}
				if r := run(t, dir, []byte(script), nil, append([]string{exe, "-s"}, args...)...); r.code != 0 || r.stderr != "" {
					t.Fatalf("the fake %s: exit %d, stderr %q", name, r.code, r.stderr)
				}
				if _, err := os.Stat(filepath.Join(first(runDir), "stderr")); err == nil {
					t.Fatalf("the dumper hit an error: %s", read(t, filepath.Join(first(runDir), "stderr")))
				}
				return runDir
			}
			// underHarness runs one hook call as the child of a fake harness.
			underHarness := func(t *testing.T, name string, args ...string) string {
				t.Helper()
				return harnessCall(t, false, name, args...)
			}
			flags := func(t *testing.T, runDir string) string {
				t.Helper()
				return read(t, filepath.Join(first(runDir), "flags"))
			}

			cases := []struct {
				name  string
				check func(t *testing.T)
			}{
				{"flags names the harness the hook runs under and finds its print flag, stream-json and --bare", func(t *testing.T) {
					runDir := underHarness(t, "claude", harnessSentinel, "-p", "--output-format", "stream-json", "--bare")
					if got, want := flags(t, runDir), "name=claude\nprint=yes\nstream_json=yes\nbare=yes\n"; got != want {
						t.Fatalf("flags = %q, want %q", got, want)
					}
				}},
				{"--print, and stream-json joined to its flag by =, count too", func(t *testing.T) {
					runDir := underHarness(t, "claude", harnessSentinel, "--print", "--input-format=stream-json")
					if got, want := flags(t, runDir), "name=claude\nprint=yes\nstream_json=yes\nbare=no\n"; got != want {
						t.Fatalf("flags = %q, want %q", got, want)
					}
				}},
				{"flags says no for each flag the harness wasn't started with", func(t *testing.T) {
					runDir := underHarness(t, "claude", harnessSentinel, "--output-format", "json", "--model", "sonnet")
					if got, want := flags(t, runDir), "name=claude\nprint=no\nstream_json=no\nbare=no\n"; got != want {
						t.Fatalf("flags = %q, want %q", got, want)
					}
				}},
				// /proc keeps the arguments apart. macOS has no /proc, and its
				// ps joins them with spaces, so there the dumpers split them at
				// whitespace and an argument holding " -p " counts.
				{"an argument that only mentions a flag isn't one", func(t *testing.T) {
					if _, err := os.Stat("/proc/self/cmdline"); err != nil {
						t.Skip("no /proc here: ps joins a harness's arguments with spaces, so a -p inside one counts")
					}
					runDir := underHarness(t, "claude", harnessSentinel+" says -p and --bare and --output-format stream-json")
					if got, want := flags(t, runDir), "name=claude\nprint=no\nstream_json=no\nbare=no\n"; got != want {
						t.Fatalf("flags = %q, want %q", got, want)
					}
				}},
				{"an argument after -- isn't a flag", func(t *testing.T) {
					runDir := underHarness(t, "claude", harnessSentinel, "--", "-p", "--bare", "--output-format", "stream-json")
					if got, want := flags(t, runDir), "name=claude\nprint=no\nstream_json=no\nbare=no\n"; got != want {
						t.Fatalf("flags = %q, want %q", got, want)
					}
				}},
				// Each codex call starts with a sandbox mode, the value of the
				// fake's -s.
				{"codex's exec subcommand is its print mode", func(t *testing.T) {
					runDir := underHarness(t, "codex", "workspace-write", "exec", harnessSentinel)
					if got, want := flags(t, runDir), "name=codex\nprint=yes\nstream_json=no\nbare=no\n"; got != want {
						t.Fatalf("flags = %q, want %q", got, want)
					}
				}},
				{"codex's e, after options and the values they take, is its print mode", func(t *testing.T) {
					runDir := underHarness(t, "codex", "workspace-write", "-c", "model_reasoning_effort=high", "--profile", "work", "-i", "shot.png", "--model=o3", "e", harnessSentinel)
					if got, want := flags(t, runDir), "name=codex\nprint=yes\nstream_json=no\nbare=no\n"; got != want {
						t.Fatalf("flags = %q, want %q", got, want)
					}
				}},
				{"an option's value isn't codex's subcommand, even when it reads exec", func(t *testing.T) {
					runDir := underHarness(t, "codex", "workspace-write", "-m", "exec", harnessSentinel)
					if got, want := flags(t, runDir), "name=codex\nprint=no\nstream_json=no\nbare=no\n"; got != want {
						t.Fatalf("flags = %q, want %q", got, want)
					}
				}},
				// clap gives -i every argument up to the next option, so this
				// codex starts interactive with three images.
				{"every argument after codex's -i, up to the next option, is an image, even exec", func(t *testing.T) {
					runDir := underHarness(t, "codex", "workspace-write", "-i", "shot.png", "exec", harnessSentinel)
					if got, want := flags(t, runDir), "name=codex\nprint=no\nstream_json=no\nbare=no\n"; got != want {
						t.Fatalf("flags = %q, want %q", got, want)
					}
				}},
				{"flags names the nearest harness above a shell that runs the hook", func(t *testing.T) {
					runDir := harnessCall(t, true, "claude", harnessSentinel, "-p")
					names := appclassHarnesses(t)
					parents := strings.Split(strings.TrimSpace(read(t, filepath.Join(first(runDir), "parents"))), "\n")
					if len(parents) < 2 || harnessParent(names, parents[0]) || !harnessParent(names, parents[1]) {
						t.Fatalf("parents = %q, want a shell and then the fake claude", parents)
					}
					if got, want := flags(t, runDir), "name=claude\nprint=yes\nstream_json=no\nbare=no\n"; got != want {
						t.Fatalf("flags = %q, want %q", got, want)
					}
				}},
				{"a call writes time, argv, env, cwd, parents, stdin and flags, and leaves no temporary file", func(t *testing.T) {
					runDir, _ := call(t, payload)
					got := keys(tree(t, first(runDir)))
					if want := []string{"argv", "cwd", "env", "flags", "parents", "stdin", "time"}; strings.Join(got, " ") != strings.Join(want, " ") {
						t.Fatalf("the call folder holds %q, want %q", got, want)
					}
				}},
				{"no argument of the harness reaches the run folder", func(t *testing.T) {
					runDir := underHarness(t, "claude", harnessSentinel, "-p", "--bare", "a prompt that ends in "+harnessSentinel)
					files := tree(t, runDir)
					if _, ok := files["hooks/000001/flags"]; !ok {
						t.Fatalf("no flags file among %v", keys(files))
					}
					for path, text := range files {
						if strings.Contains(text, harnessSentinel) {
							t.Errorf("%s holds an argument of the harness: %q", path, text)
						}
					}
				}},
				{"flags is ? for each answer when no parent has a harness's name", func(t *testing.T) {
					runDir, _ := call(t, payload)
					names := appclassHarnesses(t)
					for _, line := range strings.Split(strings.TrimSpace(read(t, filepath.Join(first(runDir), "parents"))), "\n") {
						if harnessParent(names, line) {
							t.Skipf("this test runs under a harness: %q", line)
						}
					}
					if got, want := flags(t, runDir), "name=?\nprint=?\nstream_json=?\nbare=?\n"; got != want {
						t.Fatalf("flags = %q, want %q", got, want)
					}
				}},
				// A harness stops a hook that runs past its timeout. Every file
				// but flags is written before stdin is read, so a hook stopped
				// while it waits on stdin, or while flags reads a command line,
				// keeps the rest.
				{"a hook cut short while it reads stdin has written every file but flags", func(t *testing.T) {
					runDir := filepath.Join(t.TempDir(), "run")
					cmd := exec.Command(base[0], append(append([]string{}, base[1:]...), runDir, "Stop")...)
					cmd.Dir = t.TempDir()
					cmd.Env = append(without(os.Environ(), entrypointVar), entrypointVar+"=cli")
					stdin, err := cmd.StdinPipe()
					if err != nil {
						t.Fatal(err)
					}
					defer stdin.Close()
					if err := cmd.Start(); err != nil {
						t.Fatal(err)
					}
					dir := first(runDir)
					for start := time.Now(); ; time.Sleep(20 * time.Millisecond) {
						if _, err := os.Stat(filepath.Join(dir, "stdin")); err == nil {
							break
						}
						if time.Since(start) > 30*time.Second {
							_ = cmd.Process.Kill()
							_ = cmd.Wait()
							t.Fatal("no stdin file after 30 s")
						}
					}
					_ = cmd.Process.Kill()
					_ = cmd.Wait()
					for _, name := range []string{"time", "argv", "env", "entrypoint", "cwd", "parents"} {
						if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
							t.Errorf("%s is missing: %v", name, err)
						}
					}
					if _, err := os.Stat(filepath.Join(dir, "flags")); err == nil {
						t.Error("flags was written before stdin was read")
					}
				}},
				{"stdin is saved byte for byte", func(t *testing.T) {
					runDir, _ := call(t, payload)
					if got := read(t, filepath.Join(first(runDir), "stdin")); got != string(payload) {
						t.Fatalf("stdin = %q, want %q", got, payload)
					}
				}},
				{"argv holds every argument in order, run dir and label first", func(t *testing.T) {
					runDir, _ := call(t, payload, "one arg with spaces", "last")
					want := runDir + "\nUserPromptSubmit\none arg with spaces\nlast\n"
					if got := read(t, filepath.Join(first(runDir), "argv")); got != want {
						t.Fatalf("argv = %q, want %q", got, want)
					}
				}},
				{"env holds sorted variable names and never a value", func(t *testing.T) {
					runDir, _ := call(t, payload)
					names := strings.Split(strings.TrimSuffix(read(t, filepath.Join(first(runDir), "env")), "\n"), "\n")
					if !sort.StringsAreSorted(names) {
						t.Errorf("env names aren't sorted: %q", names)
					}
					found := false
					for _, n := range names {
						found = found || n == secretName
						if strings.Contains(n, "=") {
							t.Errorf("env line %q holds more than a name", n)
						}
					}
					if !found {
						t.Errorf("env doesn't list %s", secretName)
					}
					for path, text := range tree(t, runDir) {
						if strings.Contains(text, secretValue) {
							t.Errorf("%s holds the value of %s", path, secretName)
						}
					}
				}},
				// CLAUDE_CODE_ENTRYPOINT is the one variable whose value a
				// dumper writes (E-cc-transcript-labels).
				{"entrypoint holds CLAUDE_CODE_ENTRYPOINT's value, and no other variable's value reaches the run folder", func(t *testing.T) {
					runDir, _ := callEnv(t, []string{entrypointVar + "=sdk-cli", secretName + "=" + secretValue}, payload)
					if got := read(t, filepath.Join(first(runDir), "entrypoint")); got != "sdk-cli\n" {
						t.Fatalf("entrypoint = %q, want %q", got, "sdk-cli\n")
					}
					for path, text := range tree(t, runDir) {
						if strings.Contains(text, secretValue) {
							t.Errorf("%s holds the value of %s", path, secretName)
						}
					}
				}},
				{"a CLAUDE_CODE_ENTRYPOINT of 32 characters is kept, and one of 33 becomes other and reaches no file", func(t *testing.T) {
					runDir, _ := callEnv(t, []string{entrypointVar + "=" + entrypointSentinel[:32]}, payload)
					if got, want := read(t, filepath.Join(first(runDir), "entrypoint")), entrypointSentinel[:32]+"\n"; got != want {
						t.Errorf("32 characters: entrypoint = %q, want %q", got, want)
					}
					runDir, _ = callEnv(t, []string{entrypointVar + "=" + entrypointSentinel}, payload)
					if got := read(t, filepath.Join(first(runDir), "entrypoint")); got != "other\n" {
						t.Errorf("33 characters: entrypoint = %q, want %q", got, "other\n")
					}
					for path, text := range tree(t, runDir) {
						if strings.Contains(text, entrypointSentinel[:20]) {
							t.Errorf("%s holds the over-long %s: %q", path, entrypointVar, text)
						}
					}
				}},
				{"a CLAUDE_CODE_ENTRYPOINT with a capital, a space, another character or nothing in it becomes other", func(t *testing.T) {
					for _, value := range []string{"SDK-CLI", "sdk cli", "sdk.cli", ""} {
						runDir, _ := callEnv(t, []string{entrypointVar + "=" + value}, payload)
						if got := read(t, filepath.Join(first(runDir), "entrypoint")); got != "other\n" {
							t.Errorf("%s=%q: entrypoint = %q, want %q", entrypointVar, value, got, "other\n")
						}
					}
				}},
				{"no entrypoint file is written when CLAUDE_CODE_ENTRYPOINT is unset", func(t *testing.T) {
					runDir, _ := call(t, payload)
					if _, err := os.Stat(filepath.Join(first(runDir), "entrypoint")); err == nil {
						t.Fatalf("entrypoint = %q, want no file", read(t, filepath.Join(first(runDir), "entrypoint")))
					}
				}},
				{"time holds unix_ns, utc and clock, taken during the call", func(t *testing.T) {
					before := time.Now().Add(-2 * time.Second)
					runDir, _ := call(t, payload)
					after := time.Now().Add(2 * time.Second)
					fields := map[string]string{}
					for _, line := range strings.Split(strings.TrimSpace(read(t, filepath.Join(first(runDir), "time"))), "\n") {
						k, v, _ := strings.Cut(line, "=")
						fields[k] = v
					}
					ns, err := strconv.ParseInt(fields["unix_ns"], 10, 64)
					if err != nil || ns < before.UnixNano() || ns > after.UnixNano() {
						t.Errorf("unix_ns = %q, want a time between %v and %v", fields["unix_ns"], before, after)
					}
					if _, err := time.Parse(time.RFC3339, fields["utc"]); err != nil {
						t.Errorf("utc = %q: %v", fields["utc"], err)
					}
					if fields["clock"] == "" {
						t.Errorf("time has no clock line: %v", fields)
					}
				}},
				{"cwd is the directory the hook ran in", func(t *testing.T) {
					runDir := filepath.Join(t.TempDir(), "run")
					dir := t.TempDir()
					run(t, dir, payload, nil, append(append([]string{}, base...), runDir, "Stop")...)
					want, _ := filepath.EvalSymlinks(dir)
					got, _ := filepath.EvalSymlinks(strings.TrimSpace(read(t, filepath.Join(first(runDir), "cwd"))))
					if got != want {
						t.Fatalf("cwd = %q, want %q", got, want)
					}
				}},
				{"parents starts with the process that ran the hook", func(t *testing.T) {
					runDir, _ := call(t, payload)
					lines := strings.Split(strings.TrimSpace(read(t, filepath.Join(first(runDir), "parents"))), "\n")
					pid, _, _ := strings.Cut(lines[0], " ")
					if pid != strconv.Itoa(os.Getpid()) {
						t.Fatalf("parents starts with %q, want this test's pid %d", lines[0], os.Getpid())
					}
				}},
				{"each parents line is pid, terminal and name, with the terminal ps reports", func(t *testing.T) {
					runDir, _ := call(t, payload)
					lines := strings.Split(strings.TrimSpace(read(t, filepath.Join(first(runDir), "parents"))), "\n")
					for _, line := range lines {
						if len(strings.Fields(line)) < 3 {
							t.Errorf("parents line %q isn't pid, terminal and name", line)
						}
					}
					ps := run(t, ".", nil, nil, need(t, "ps"), "-o", "tty=", "-p", strconv.Itoa(os.Getpid()))
					if f := strings.Fields(lines[0]); len(f) < 3 || f[1] != strings.TrimSpace(ps.stdout) {
						t.Errorf("parents starts with %q, want this test's terminal %q", lines[0], strings.TrimSpace(ps.stdout))
					}
				}},
				{"it prints nothing, exits 0 and leaves no stderr file", func(t *testing.T) {
					runDir, r := call(t, payload)
					if r.code != 0 || r.stdout != "" || r.stderr != "" {
						t.Fatalf("exit %d, stdout %q, stderr %q; want 0 and nothing", r.code, r.stdout, r.stderr)
					}
					if _, err := os.Stat(filepath.Join(first(runDir), "stderr")); err == nil {
						t.Fatalf("stderr file written: %q", read(t, filepath.Join(first(runDir), "stderr")))
					}
				}},
				{"it exits 0 and writes nothing without arguments", func(t *testing.T) {
					dir := t.TempDir()
					r := run(t, dir, payload, nil, base...)
					if r.code != 0 || r.stdout != "" {
						t.Fatalf("exit %d, stdout %q; want 0 and nothing", r.code, r.stdout)
					}
					if files := tree(t, dir); len(files) != 0 {
						t.Fatalf("wrote %v", files)
					}
				}},
				{"a hooks folder it can't write to ends the call at once, not after a million numbers", func(t *testing.T) {
					if os.Geteuid() == 0 {
						t.Skip("root can write to a read-only folder")
					}
					runDir := filepath.Join(t.TempDir(), "run")
					hooks := filepath.Join(runDir, "hooks")
					if err := os.MkdirAll(hooks, 0o755); err != nil {
						t.Fatal(err)
					}
					if err := os.Chmod(hooks, 0o555); err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { _ = os.Chmod(hooks, 0o755) })
					ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
					defer cancel()
					cmd := exec.CommandContext(ctx, base[0], append(append([]string{}, base[1:]...), runDir, "Stop")...)
					cmd.Stdin = bytes.NewReader(payload)
					err := cmd.Run()
					if ctx.Err() != nil {
						t.Fatal("still trying numbers after 30 s")
					}
					if err != nil {
						t.Fatalf("exit: %v, want 0", err)
					}
					if entries, _ := os.ReadDir(hooks); len(entries) != 0 {
						t.Fatalf("wrote %d entries into a read-only folder", len(entries))
					}
				}},
				{"calls are numbered in arrival order", func(t *testing.T) {
					runDir := filepath.Join(t.TempDir(), "run")
					for _, in := range []string{"first", "second", "third"} {
						run(t, t.TempDir(), []byte(in), nil, append(append([]string{}, base...), runDir, "Stop")...)
					}
					for i, want := range []string{"first", "second", "third"} {
						if got := read(t, filepath.Join(runDir, "hooks", "00000"+strconv.Itoa(i+1), "stdin")); got != want {
							t.Errorf("call %d stdin = %q, want %q", i+1, got, want)
						}
					}
				}},
				// Two calls that race for one number: the other call has
				// claimed 000002, and mkdir tells this one it made that
				// folder, as uutils mkdir can tell the call that lost.
				{"a number whose time file is there is taken, even when mkdir says this call made its folder", func(t *testing.T) {
					runDir := filepath.Join(t.TempDir(), "run")
					taken := filepath.Join(runDir, "hooks", "000002")
					write(t, filepath.Join(taken, "time"), "the other call's\n")
					run(t, t.TempDir(), payload, []string{lyingMkdir(t)}, append(append([]string{}, base...), runDir, "Stop")...)
					if got := read(t, filepath.Join(taken, "time")); got != "the other call's\n" {
						t.Errorf("the other call's time file now holds %q", got)
					}
					if _, err := os.Stat(filepath.Join(taken, "stdin")); err == nil {
						t.Error("it wrote its stdin into the other call's folder")
					}
					if got := read(t, filepath.Join(runDir, "hooks", "000003", "stdin")); got != string(payload) {
						t.Errorf("hooks/000003/stdin = %q, want the payload", got)
					}
				}},
				{"concurrent calls each get their own directory", func(t *testing.T) {
					runDir := filepath.Join(t.TempDir(), "run")
					env := append(os.Environ(), lyingMkdir(t))
					var wg sync.WaitGroup
					for i := 0; i < 8; i++ {
						wg.Add(1)
						go func(i int) {
							defer wg.Done()
							cmd := exec.Command(base[0], append(append([]string{}, base[1:]...), runDir, "PostToolUse")...)
							cmd.Env = env
							cmd.Stdin = strings.NewReader("call " + strconv.Itoa(i))
							if err := cmd.Run(); err != nil {
								t.Errorf("call %d: %v", i, err)
							}
						}(i)
					}
					wg.Wait()
					var got []string
					for path, text := range tree(t, runDir) {
						if strings.HasSuffix(path, "/stdin") {
							got = append(got, text)
						}
					}
					sort.Strings(got)
					want := []string{"call 0", "call 1", "call 2", "call 3", "call 4", "call 5", "call 6", "call 7"}
					if strings.Join(got, ",") != strings.Join(want, ",") {
						t.Fatalf("stdin files = %q, want one per call: %q", got, want)
					}
				}},
			}
			for _, c := range cases {
				t.Run(c.name, c.check)
			}
		})
	}
}

// TestSplitCommandLine runs dump-hook.ps1's Split-CommandLine in pwsh on
// Linux. On Windows the flags file reads a harness's arguments through it
// from Win32_Process.CommandLine, and ci.yml runs no Windows job.
func TestSplitCommandLine(t *testing.T) {
	unixOnly(t)
	pwsh := need(t, "pwsh")
	fn := regexp.MustCompile(`(?ms)^function Split-CommandLine\b.*?^}$`).FindString(read(t, "dump-hook.ps1"))
	if fn == "" {
		t.Fatal("dump-hook.ps1 has no Split-CommandLine")
	}
	cases := []struct {
		name, line string
		want       []string
	}{
		{"a quoted argument with spaces stays one argument", `"C:\Program Files\Claude\claude.exe" -p "fix the -p flag" --bare`,
			[]string{`C:\Program Files\Claude\claude.exe`, "-p", "fix the -p flag", "--bare"}},
		{"backslashes count only before a quote", `claude.exe C:\dir\ "a\"b" "c\\" d`, []string{"claude.exe", `C:\dir\`, `a"b`, `c\`, "d"}},
		{"tabs and runs of spaces end an argument", "claude.exe\t-p   --output-format  stream-json ", []string{"claude.exe", "-p", "--output-format", "stream-json"}},
		{"an empty quoted argument is kept", `claude.exe "" -p`, []string{"claude.exe", "", "-p"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			script := fn + "\nforeach ($w in (Split-CommandLine $env:PAIRTIME_COMMAND_LINE)) { [Console]::Out.Write('[' + $w + ']') }\n"
			r := run(t, ".", nil, []string{"PAIRTIME_COMMAND_LINE=" + c.line}, pwsh, "-NoProfile", "-NonInteractive", "-Command", script)
			if want := "[" + strings.Join(c.want, "][") + "]"; r.code != 0 || r.stdout != want {
				t.Fatalf("exit %d, arguments %s, want %s\n%s", r.code, r.stdout, want, r.stderr)
			}
		})
	}
}

// TestDenyOnce checks deny-once.sh, the AfterAgent hook that gives the
// Gemini CLI capture its one deny-retry (SCRIPTS.md, step 19; HS 7.1).
func TestDenyOnce(t *testing.T) {
	unixOnly(t)
	sh := need(t, "sh")
	script := kit(t, "deny-once.sh")
	source, scripts := read(t, script), read(t, "SCRIPTS.md")
	quoted := func(name string) string {
		m := regexp.MustCompile(`(?m)^` + name + `='([^']*)'$`).FindStringSubmatch(source)
		if m == nil {
			t.Fatalf("deny-once.sh sets no %s", name)
		}
		return m[1]
	}
	prompt, reason := quoted("prompt"), quoted("reason")
	step19 := jsonText(t, map[string]any{"hook_event_name": "AfterAgent", "prompt": prompt, "prompt_response": "Apple"})
	other := jsonText(t, map[string]any{"hook_event_name": "AfterAgent", "prompt": "What does count_words do? Answer in one sentence."})
	call := func(t *testing.T, runDir, payload string, env ...string) result {
		t.Helper()
		r := run(t, t.TempDir(), []byte(payload), env, sh, script, runDir, "AfterAgent")
		if r.code != 0 || r.stderr != "" {
			t.Fatalf("exit %d, stderr %q; want 0 and nothing", r.code, r.stderr)
		}
		return r
	}
	denied := func(t *testing.T, r result) bool {
		t.Helper()
		if r.stdout == "" {
			return false
		}
		var d struct{ Decision, Reason string }
		if err := json.Unmarshal([]byte(r.stdout), &d); err != nil || d.Decision != "deny" || d.Reason != reason {
			t.Fatalf("stdout %q, want Gemini's deny decision with the reason %q", r.stdout, reason)
		}
		return true
	}

	cases := []struct {
		name  string
		check func(t *testing.T)
	}{
		{"step 19's prompt and the reason are SCRIPTS.md text, so the scrub doesn't flag them", func(t *testing.T) {
			for _, text := range []string{prompt, reason} {
				if !strings.Contains(scripts, "```text\n"+text+"\n```") {
					t.Errorf("SCRIPTS.md has no text block holding %q", text)
				}
			}
		}},
		{"the first call with step 19's prompt is turned down", func(t *testing.T) {
			if !denied(t, call(t, filepath.Join(t.TempDir(), "run"), step19)) {
				t.Fatal("not turned down")
			}
		}},
		{"a later call with step 19's prompt isn't turned down", func(t *testing.T) {
			runDir := filepath.Join(t.TempDir(), "run")
			call(t, runDir, step19)
			if denied(t, call(t, runDir, step19)) {
				t.Fatal("turned down twice")
			}
		}},
		{"step 19 is turned down once, even when mkdir says each call made the marker", func(t *testing.T) {
			runDir, path := filepath.Join(t.TempDir(), "run"), lyingMkdir(t)
			if !denied(t, call(t, runDir, step19, path)) {
				t.Fatal("not turned down")
			}
			if denied(t, call(t, runDir, step19, path)) {
				t.Fatal("turned down twice")
			}
		}},
		{"the decision it sends is kept in the run as deny-once", func(t *testing.T) {
			runDir := filepath.Join(t.TempDir(), "run")
			r := call(t, runDir, step19)
			if got := read(t, filepath.Join(runDir, "deny-once")); r.stdout == "" || got != r.stdout {
				t.Fatalf("deny-once = %q, want what it printed, %q", got, r.stdout)
			}
		}},
		{"a call with another prompt isn't turned down and leaves the denial for step 19", func(t *testing.T) {
			runDir := filepath.Join(t.TempDir(), "run")
			if denied(t, call(t, runDir, other)) {
				t.Fatal("another prompt was turned down")
			}
			if !denied(t, call(t, runDir, step19)) {
				t.Fatal("step 19 wasn't turned down after another prompt")
			}
		}},
		{"every call is recorded like any other hook, and no temporary file stays", func(t *testing.T) {
			runDir := filepath.Join(t.TempDir(), "run")
			call(t, runDir, other)
			call(t, runDir, step19)
			for i, want := range []string{other, step19} {
				dir := filepath.Join(runDir, "hooks", "00000"+strconv.Itoa(i+1))
				if got := read(t, filepath.Join(dir, "stdin")); got != want {
					t.Errorf("call %d stdin = %q, want %q", i+1, got, want)
				}
				if got := read(t, filepath.Join(dir, "argv")); got != runDir+"\nAfterAgent\n" {
					t.Errorf("call %d argv = %q", i+1, got)
				}
			}
			entries, _ := os.ReadDir(runDir)
			for _, e := range entries {
				if strings.HasPrefix(e.Name(), ".deny-once.") {
					t.Errorf("%s left in the run", e.Name())
				}
			}
		}},
		{"without arguments it exits 0 without reading the payload", func(t *testing.T) {
			dir := t.TempDir()
			r := run(t, dir, hostile, nil, sh, script)
			if r.code != 0 || r.stdout != "" || len(tree(t, dir)) != 0 {
				t.Fatalf("exit %d, stdout %q, files %v; want 0 and nothing", r.code, r.stdout, keys(tree(t, dir)))
			}
		}},
		{"it exits 0 and turns nothing down when the run folder can't be made", func(t *testing.T) {
			file := filepath.Join(t.TempDir(), "file")
			write(t, file, "")
			if denied(t, call(t, filepath.Join(file, "run"), step19)) {
				t.Fatal("turned down without a run folder to record it in")
			}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, c.check)
	}
}

// allowers are the two allow-once hooks, as the command lines that run them.
var allowers = []dumper{
	{"sh", func(t *testing.T) []string { return []string{need(t, "sh"), kit(t, "allow-once.sh")} }},
	{"pwsh", func(t *testing.T) []string {
		return []string{need(t, "pwsh"), "-NoProfile", "-NonInteractive", "-File", kit(t, "allow-once.ps1")}
	}},
}

// TestAllowOnce checks allow-once.sh and allow-once.ps1, Claude Code's
// PermissionRequest hook in a capture, which answers step 22's prompt
// itself so the capture holds one permission a hook decided (SCRIPTS.md,
// step 22; SM 1.2).
func TestAllowOnce(t *testing.T) {
	unixOnly(t)
	scripts := read(t, "SCRIPTS.md")
	constant := func(t *testing.T, file, pattern string) string {
		t.Helper()
		m := regexp.MustCompile(pattern).FindStringSubmatch(read(t, kit(t, file)))
		if m == nil {
			t.Fatalf("%s has no line matching %s", file, pattern)
		}
		return m[1]
	}
	file := constant(t, "allow-once.sh", `(?m)^file='([^']*)'$`)
	decision := constant(t, "allow-once.sh", `(?m)^decision='([^']*)'$`)
	step22 := jsonText(t, map[string]any{
		"hook_event_name": "PermissionRequest", "tool_name": "Write",
		"tool_input": map[string]any{"file_path": "/home/capture/projects/cli/" + file, "content": "capture test\n"},
	})
	other := jsonText(t, map[string]any{
		"hook_event_name": "PermissionRequest", "tool_name": "Write",
		"tool_input": map[string]any{"file_path": "/home/capture/projects/cli/notes.txt", "content": "capture test\n"},
	})

	t.Run("both scripts answer step 22's file with the same decision, Claude Code's allow for PermissionRequest", func(t *testing.T) {
		if got := constant(t, "allow-once.ps1", `(?m)^\$file = '([^']*)'$`); got != file {
			t.Errorf("allow-once.ps1 answers for %q, allow-once.sh for %q", got, file)
		}
		if got := constant(t, "allow-once.ps1", `(?m)^\$decision = '([^']*)'$`); got != decision {
			t.Errorf("allow-once.ps1 sends %q, allow-once.sh %q", got, decision)
		}
		var d struct {
			HookSpecificOutput struct {
				HookEventName string `json:"hookEventName"`
				Decision      struct {
					Behavior string `json:"behavior"`
				} `json:"decision"`
			} `json:"hookSpecificOutput"`
		}
		if err := json.Unmarshal([]byte(decision), &d); err != nil || d.HookSpecificOutput.HookEventName != "PermissionRequest" ||
			d.HookSpecificOutput.Decision.Behavior != "allow" {
			t.Fatalf("decision %q (%v), want hookSpecificOutput for PermissionRequest with behavior allow", decision, err)
		}
	})
	t.Run("step 22's prompt in SCRIPTS.md names the file, so the scrub knows it as the script's text", func(t *testing.T) {
		prompt := "Create a file named " + file + " holding the single line: capture test"
		if !strings.Contains(scripts, "```text\n"+prompt+"\n```") {
			t.Fatalf("SCRIPTS.md has no text block holding %q", prompt)
		}
	})

	for _, a := range allowers {
		t.Run(a.name, func(t *testing.T) {
			base := a.argv(t)
			call := func(t *testing.T, runDir, payload string, env ...string) (result, time.Duration) {
				t.Helper()
				start := time.Now()
				r := run(t, t.TempDir(), []byte(payload), env, append(append([]string{}, base...), runDir, "PermissionRequest")...)
				if r.code != 0 || r.stderr != "" {
					t.Fatalf("exit %d, stderr %q; want 0 and nothing", r.code, r.stderr)
				}
				return r, time.Since(start)
			}
			allowed := func(t *testing.T, r result) bool {
				t.Helper()
				if r.stdout == "" {
					return false
				}
				if r.stdout != decision+"\n" {
					t.Fatalf("stdout %q, want the decision %q", r.stdout, decision)
				}
				return true
			}
			cases := []struct {
				name  string
				check func(t *testing.T)
			}{
				{"the first call naming step 22's file is allowed, 2 s after it arrives", func(t *testing.T) {
					r, took := call(t, filepath.Join(t.TempDir(), "run"), step22)
					if !allowed(t, r) {
						t.Fatal("not allowed")
					}
					if took < 2*time.Second {
						t.Fatalf("answered after %v, want at least 2 s", took)
					}
				}},
				{"a later call naming it is left to the person", func(t *testing.T) {
					runDir := filepath.Join(t.TempDir(), "run")
					call(t, runDir, step22)
					if r, _ := call(t, runDir, step22); allowed(t, r) {
						t.Fatal("allowed twice")
					}
				}},
				{"step 22 is allowed once, even when mkdir says each call made the marker", func(t *testing.T) {
					runDir, path := filepath.Join(t.TempDir(), "run"), lyingMkdir(t)
					if r, _ := call(t, runDir, step22, path); !allowed(t, r) {
						t.Fatal("not allowed")
					}
					if r, _ := call(t, runDir, step22, path); allowed(t, r) {
						t.Fatal("allowed twice")
					}
				}},
				{"the decision it sends is kept in the run as allow-once", func(t *testing.T) {
					runDir := filepath.Join(t.TempDir(), "run")
					r, _ := call(t, runDir, step22)
					if got := read(t, filepath.Join(runDir, "allow-once")); r.stdout == "" || got != r.stdout {
						t.Fatalf("allow-once = %q, want what it printed, %q", got, r.stdout)
					}
				}},
				{"a call for another file is left to the person and keeps the answer for step 22", func(t *testing.T) {
					runDir := filepath.Join(t.TempDir(), "run")
					if r, _ := call(t, runDir, other); allowed(t, r) {
						t.Fatal("another file was allowed")
					}
					if _, err := os.Stat(filepath.Join(runDir, "allow-once")); err == nil {
						t.Fatal("allow-once was written for another file")
					}
					if r, _ := call(t, runDir, step22); !allowed(t, r) {
						t.Fatal("step 22 wasn't allowed after another file")
					}
				}},
				{"every call is recorded like any other hook, and no temporary file stays", func(t *testing.T) {
					runDir := filepath.Join(t.TempDir(), "run")
					call(t, runDir, other)
					call(t, runDir, step22)
					for i, want := range []string{other, step22} {
						dir := filepath.Join(runDir, "hooks", "00000"+strconv.Itoa(i+1))
						if got := read(t, filepath.Join(dir, "stdin")); got != want {
							t.Errorf("call %d stdin = %q, want %q", i+1, got, want)
						}
						if got := read(t, filepath.Join(dir, "argv")); got != runDir+"\nPermissionRequest\n" {
							t.Errorf("call %d argv = %q", i+1, got)
						}
					}
					entries, _ := os.ReadDir(runDir)
					for _, e := range entries {
						if strings.HasPrefix(e.Name(), ".allow-once.") {
							t.Errorf("%s left in the run", e.Name())
						}
					}
				}},
				{"calls that arrive together are each recorded, and only one is allowed", func(t *testing.T) {
					runDir := filepath.Join(t.TempDir(), "run")
					env := append(os.Environ(), lyingMkdir(t))
					var wg sync.WaitGroup
					outs := make([]string, 6)
					for i := range outs {
						wg.Add(1)
						go func(i int) {
							defer wg.Done()
							cmd := exec.Command(base[0], append(append([]string{}, base[1:]...), runDir, "PermissionRequest")...)
							cmd.Env = env
							cmd.Stdin = strings.NewReader(step22)
							out, err := cmd.Output()
							if err != nil {
								t.Errorf("call %d: %v", i, err)
							}
							outs[i] = string(out)
						}(i)
					}
					wg.Wait()
					answered := 0
					for _, out := range outs {
						if out == decision+"\n" {
							answered++
						} else if out != "" {
							t.Errorf("a call printed %q", out)
						}
					}
					if answered != 1 {
						t.Errorf("%d calls were allowed, want 1", answered)
					}
					stdins := 0
					for path, text := range tree(t, runDir) {
						if strings.HasSuffix(path, "/stdin") {
							stdins++
							if text != step22 {
								t.Errorf("%s = %q, want the payload", path, text)
							}
						}
					}
					if stdins != len(outs) {
						t.Errorf("%d calls recorded, want %d", stdins, len(outs))
					}
				}},
				{"without arguments it exits 0 without reading the payload", func(t *testing.T) {
					dir := t.TempDir()
					r := run(t, dir, hostile, nil, base...)
					if r.code != 0 || r.stdout != "" || len(tree(t, dir)) != 0 {
						t.Fatalf("exit %d, stdout %q, files %v; want 0 and nothing", r.code, r.stdout, keys(tree(t, dir)))
					}
				}},
				{"it exits 0 and allows nothing when the run folder can't be made", func(t *testing.T) {
					f := filepath.Join(t.TempDir(), "file")
					write(t, f, "")
					if r, _ := call(t, filepath.Join(f, "run"), step22); allowed(t, r) {
						t.Fatal("allowed without a run folder to record it in")
					}
				}},
			}
			for _, c := range cases {
				t.Run(c.name, c.check)
			}
		})
	}
}

// startSink runs the sink on a free port and returns its base URL.
func startSink(t *testing.T, runDir string) string {
	t.Helper()
	argv := python(t, "otlp_sink.py", runDir, "--port", "0")
	cmd := exec.Command(argv[0], argv[1:]...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	lines := make(chan string, 1)
	go func() {
		r := bufio.NewReader(stdout)
		line, _ := r.ReadString('\n')
		lines <- line
		_, _ = io.Copy(io.Discard, r)
	}()
	select {
	case line := <-lines:
		m := regexp.MustCompile(`listening on (127\.0\.0\.1:\d+)`).FindStringSubmatch(line)
		if m == nil {
			t.Fatalf("sink said %q, want its address", line)
		}
		return "http://" + m[1]
	case <-time.After(30 * time.Second):
		t.Fatal("the sink didn't start in 30 s")
	}
	return ""
}

func TestSink(t *testing.T) {
	unixOnly(t)
	runDir := t.TempDir()
	url := startSink(t, runDir)
	logs := `{"resourceLogs":[{"scopeLogs":[{"logRecords":[{"body":{"stringValue":"claude_code.user_prompt"}}]}]}]}`
	metrics := `{"resourceMetrics":[{"scopeMetrics":[{"metrics":[{"name":"claude_code.active_time.total"}]}]}]}`
	var zipped bytes.Buffer
	zw := gzip.NewWriter(&zipped)
	_, _ = zw.Write([]byte(metrics))
	_ = zw.Close()
	const headerSecret = "header-value-that-must-never-be-written"

	cases := []struct {
		name    string
		path    string
		body    io.Reader
		headers map[string]string
		file    string
		saved   string
		reply   string
		meta    []string
	}{
		{"a JSON body is saved as sent and answered with {}", "/v1/logs", strings.NewReader(logs),
			map[string]string{"Content-Type": "application/json"},
			"000001.json", logs, "{}", []string{"path=/v1/logs", "content_type=application/json", "unix_ns="}},
		{"a gzipped body is saved gunzipped", "/v1/metrics", bytes.NewReader(zipped.Bytes()),
			map[string]string{"Content-Type": "application/json", "Content-Encoding": "gzip"},
			"000002.json", metrics, "{}", []string{"content_encoding=gzip", "note=gunzipped"}},
		{"header values are never saved, only their names", "/v1/logs", strings.NewReader(logs),
			map[string]string{"Content-Type": "application/json", "Authorization": "Bearer " + headerSecret},
			"000003.json", logs, "{}", []string{"authorization"}},
		{"a protobuf body is saved as .bin and answered empty", "/v1/logs", bytes.NewReader([]byte{0x0a, 0x02, 0x08, 0x01}),
			map[string]string{"Content-Type": "application/x-protobuf"},
			"000004.bin", "\x0a\x02\x08\x01", "", []string{"content_type=application/x-protobuf"}},
		{"a chunked body is read whole", "/v1/logs", io.MultiReader(strings.NewReader(logs[:10]), strings.NewReader(logs[10:])),
			map[string]string{"Content-Type": "application/json"},
			"000005.json", logs, "{}", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodPost, url+c.path, c.body)
			if err != nil {
				t.Fatal(err)
			}
			for k, v := range c.headers {
				req.Header.Set(k, v)
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			reply, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode != http.StatusOK || string(reply) != c.reply {
				t.Errorf("reply %d %q, want 200 %q", resp.StatusCode, reply, c.reply)
			}
			if got := read(t, filepath.Join(runDir, "otlp", c.file)); got != c.saved {
				t.Errorf("%s = %q, want %q", c.file, got, c.saved)
			}
			meta := read(t, filepath.Join(runDir, "otlp", strings.TrimSuffix(c.file, filepath.Ext(c.file))+".meta"))
			for _, want := range c.meta {
				if !strings.Contains(meta, want) {
					t.Errorf("meta lacks %q:\n%s", want, meta)
				}
			}
			for path, text := range tree(t, runDir) {
				if strings.Contains(text, headerSecret) {
					t.Errorf("%s holds a header value", path)
				}
			}
		})
	}

	t.Run("it refuses to listen beyond loopback", func(t *testing.T) {
		r := run(t, ".", nil, nil, python(t, "otlp_sink.py", t.TempDir(), "--host", "0.0.0.0")...)
		if r.code != 2 || !strings.Contains(r.stderr, "loopback") {
			t.Fatalf("exit %d, stderr %q; want 2 and a loopback error", r.code, r.stderr)
		}
	})

	t.Run("lines typed into its terminal go to STEPS.txt with the time", func(t *testing.T) {
		stepsDir := t.TempDir()
		code := "import io, sys; sys.path.insert(0, sys.argv[1]); import otlp_sink; " +
			"otlp_sink.log_steps(sys.argv[2], io.StringIO('4\\n\\n5 pasted\\n'), sys.stdout)"
		r := run(t, ".", nil, nil, need(t, "python3"), "-I", "-B", "-c", code, kit(t, "."), stepsDir)
		if r.code != 0 {
			t.Fatalf("exit %d: %s", r.code, r.stderr)
		}
		lines := strings.Split(strings.TrimSpace(read(t, filepath.Join(stepsDir, "STEPS.txt"))), "\n")
		if len(lines) != 2 || !strings.HasSuffix(lines[0], "\t4") || !strings.HasSuffix(lines[1], "\t5 pasted") {
			t.Fatalf("STEPS.txt = %q, want steps 4 and 5 with times", lines)
		}
		if f := strings.Split(lines[0], "\t"); len(f) != 3 || f[0] == "" || f[1] == "" {
			t.Fatalf("step line %q, want unix_ns, utc and the text", lines[0])
		}
	})
}

// writeCapture writes the capture.json prepare.py leaves in a run: the
// harness, and the settings folder it was pointed at, if any.
func writeCapture(t *testing.T, runDir, harness string, strict bool, folders map[string]string) {
	t.Helper()
	if folders == nil {
		folders = map[string]string{}
	}
	write(t, filepath.Join(runDir, "capture.json"), jsonText(t, map[string]any{
		"harness": harness, "run": filepath.Base(runDir), "settings_folder": folders, "strict": strict, "hooks": "exec",
	}))
}

func TestCollect(t *testing.T) {
	unixOnly(t)
	home := t.TempDir()
	project := filepath.Join(home, "pairtime-capture", "project")
	// The settings folder prepare.py gave Claude Code, as CLAUDE_CONFIG_DIR.
	config := filepath.Join(home, ".claude")
	projects := filepath.Join(config, "projects", "-capture-project")
	transcript := filepath.Join(projects, "sess-1.jsonl")
	write(t, transcript, "{\"type\":\"user\",\"sessionId\":\"sess-1\"}\n")
	write(t, filepath.Join(projects, "sess-1", "subagents", "agent-a.jsonl"), "{\"isSidechain\":true}\n")
	write(t, filepath.Join(projects, "sess-2.jsonl"), "{\"type\":\"user\",\"sessionId\":\"sess-2\"}\n")
	// The run's hook calls arrived over the last minute. History entries
	// carry their own times: Claude Code's timestamp in ms, Codex's ts in
	// seconds, Gemini CLI's an ISO 8601 string.
	now := time.Now()
	during, earlier := now.Add(-30*time.Second), now.Add(-time.Hour)
	history := filepath.Join(home, ".claude", "history.jsonl")
	entry := func(fields map[string]any) string { return jsonText(t, fields) + "\n" }
	captured := entry(map[string]any{"display": "a captured prompt", "project": project, "timestamp": during.UnixMilli()})
	elsewhere := entry(map[string]any{"display": "a prompt from another project", "project": filepath.Join(home, "school"),
		"timestamp": during.UnixMilli()})
	bySession := entry(map[string]any{"display": "a prompt by session id", "sessionId": "sess-1", "project": project,
		"timestamp": during.UnixMilli()})
	attempt := entry(map[string]any{"display": "an earlier attempt's private prompt", "project": project,
		"timestamp": earlier.UnixMilli()})
	otherSession := entry(map[string]any{"display": "another session's prompt in the same folder", "sessionId": "sess-9",
		"project": project, "timestamp": during.UnixMilli()})
	undated := entry(map[string]any{"display": "a prompt with no time", "project": project})
	codex := entry(map[string]any{"session_id": "sess-1", "ts": during.Unix(), "text": "a Codex prompt"})
	write(t, history, captured+elsewhere+bySession+attempt+otherSession+undated+codex)
	logs := filepath.Join(home, ".claude", "logs.json")
	write(t, logs, jsonText(t, []map[string]any{
		{"sessionId": "sess-1", "type": "user", "message": "a Gemini prompt", "timestamp": during.UTC().Format(time.RFC3339Nano)},
		{"sessionId": "sess-9", "type": "user", "message": "another session's Gemini prompt", "timestamp": during.UTC().Format(time.RFC3339)},
	}))

	runDir := filepath.Join(t.TempDir(), "run")
	writeCapture(t, runDir, "claude_code", true, map[string]string{"CLAUDE_CONFIG_DIR": config})
	for i, at := range []time.Time{now.Add(-time.Minute), now} {
		write(t, filepath.Join(runDir, "hooks", "00000"+strconv.Itoa(i+1), "time"),
			"unix_ns="+strconv.FormatInt(at.UnixNano(), 10)+"\nutc="+at.UTC().Format(time.RFC3339)+"\nclock=gnu-date\n")
	}
	write(t, filepath.Join(runDir, "hooks", "000001", "stdin"),
		jsonText(t, map[string]any{"session_id": "sess-1", "transcript_path": transcript, "cwd": project}))
	write(t, filepath.Join(runDir, "hooks", "000002", "stdin"),
		jsonText(t, map[string]any{"session_id": "sess-1", "transcript_path": filepath.Join(projects, "gone.jsonl"), "cwd": project}))
	write(t, filepath.Join(runDir, "hooks", "000003", "stdin"), "not json")

	r := run(t, ".", nil, []string{"HOME=" + home}, python(t, "collect.py", runDir, "--history", history, "--history", logs)...)
	if r.code != 0 {
		t.Fatalf("collect.py exit %d:\n%s%s", r.code, r.stdout, r.stderr)
	}
	got := tree(t, filepath.Join(runDir, "transcripts"))

	cases := []struct {
		name  string
		check func(t *testing.T)
	}{
		{"the transcript a hook names is copied unchanged", func(t *testing.T) {
			if got["home/.claude/projects/-capture-project/sess-1.jsonl"] != read(t, transcript) {
				t.Fatalf("copied files: %v", keys(got))
			}
		}},
		{"its subagent folder comes with it", func(t *testing.T) {
			if _, ok := got["home/.claude/projects/-capture-project/sess-1/subagents/agent-a.jsonl"]; !ok {
				t.Fatalf("copied files: %v", keys(got))
			}
		}},
		{"a session no hook names stays behind", func(t *testing.T) {
			for path := range got {
				if strings.Contains(path, "sess-2") {
					t.Fatalf("copied %s", path)
				}
			}
		}},
		{"history keeps a captured session's entries, and an undated one from its folder only while the run went", func(t *testing.T) {
			// Left behind: another project's prompt, an earlier attempt's in the
			// same folder, another session's, and one that can't be placed.
			if h, want := got["home/.claude/history.jsonl"], captured+bySession+codex; h != want {
				t.Fatalf("history = %q, want %q", h, want)
			}
		}},
		{"a history file that is one JSON array is filtered entry by entry", func(t *testing.T) {
			var entries []map[string]any
			if err := json.Unmarshal([]byte(got["home/.claude/logs.json"]), &entries); err != nil {
				t.Fatalf("logs.json = %q: %v", got["home/.claude/logs.json"], err)
			}
			if len(entries) != 1 || entries[0]["message"] != "a Gemini prompt" {
				t.Fatalf("logs.json entries = %v, want only the captured session's", entries)
			}
		}},
		{"INDEX.txt lists every copied file with its source", func(t *testing.T) {
			index := got["INDEX.txt"]
			for _, want := range []string{"transcripts/home/.claude/projects/-capture-project/sess-1.jsonl\t" + transcript, "agent-a.jsonl", "history.jsonl"} {
				if !strings.Contains(index, want) {
					t.Errorf("INDEX.txt lacks %q:\n%s", want, index)
				}
			}
		}},
		{"a transcript a hook names that isn't on disk is reported", func(t *testing.T) {
			if !strings.Contains(r.stdout, "missing") || !strings.Contains(r.stdout, "gone.jsonl") {
				t.Fatalf("stdout:\n%s", r.stdout)
			}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, c.check)
	}

	// A run whose harness had a settings folder of its own takes nothing
	// from outside it: such a file ran with the person's own settings.
	outside := filepath.Join(home, "elsewhere", "projects", "-capture-project", "sess-9.jsonl")
	write(t, outside, "{\"type\":\"user\",\"sessionId\":\"sess-9\"}\n")
	elsewhereHistory := filepath.Join(home, "elsewhere", "history.jsonl")
	write(t, elsewhereHistory, captured)
	refusals := []struct {
		name, harness string
		strict        bool
		args          []string
		want          string
	}{
		{"a transcript from outside the settings folder stops the collect, and nothing is copied", "claude_code", true,
			nil, "outside " + config},
		{"a history file from outside the settings folder stops the collect", "claude_code", true,
			[]string{"--history", elsewhereHistory}, "--history " + elsewhereHistory},
		{"an --extra path from outside the settings folder stops the collect", "claude_code", true,
			[]string{"--extra", elsewhereHistory, "--yes"}, "--extra " + elsewhereHistory},
		{"a run with no capture.json isn't collected", "", false, nil, "no capture.json"},
	}
	for _, c := range refusals {
		t.Run(c.name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "run")
			if c.harness != "" {
				writeCapture(t, dir, c.harness, c.strict, map[string]string{"CLAUDE_CONFIG_DIR": config})
			}
			named := transcript
			if len(c.args) == 0 && c.harness != "" {
				named = outside
			}
			write(t, filepath.Join(dir, "hooks", "000001", "stdin"),
				jsonText(t, map[string]any{"session_id": "sess-9", "transcript_path": named, "cwd": project}))
			r := run(t, ".", nil, []string{"HOME=" + home}, python(t, "collect.py", append([]string{dir}, c.args...)...)...)
			if r.code != 2 || !strings.Contains(r.stderr, c.want) {
				t.Fatalf("exit %d, want 2 and %q:\n%s%s", r.code, c.want, r.stdout, r.stderr)
			}
			if _, err := os.Stat(filepath.Join(dir, "transcripts")); err == nil {
				t.Fatalf("copied %v", keys(tree(t, filepath.Join(dir, "transcripts"))))
			}
		})
	}

	t.Run("a harness that can't be given a settings folder may collect from anywhere", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "run")
		writeCapture(t, dir, "cursor", false, nil)
		write(t, filepath.Join(dir, "hooks", "000001", "stdin"),
			jsonText(t, map[string]any{"session_id": "sess-9", "transcript_path": outside, "cwd": project}))
		r := run(t, ".", nil, []string{"HOME=" + home}, python(t, "collect.py", dir)...)
		if r.code != 0 {
			t.Fatalf("exit %d:\n%s%s", r.code, r.stdout, r.stderr)
		}
		if _, ok := tree(t, filepath.Join(dir, "transcripts"))["home/elsewhere/projects/-capture-project/sess-9.jsonl"]; !ok {
			t.Fatalf("copied %v", keys(tree(t, filepath.Join(dir, "transcripts"))))
		}
	})
}

func keys(m map[string]string) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// The planted capture: one person's values in the spellings real files use.
const (
	plantedSession = "5d6c1e2a-7b8f-4c3d-9e0f-112233445566"
	plantedTool    = "toolu_01ABCDEFGHIJKLMNOPQRSTUV"
)

var planted = map[string]string{
	"hooks/000001/stdin": `{"session_id":"` + plantedSession + `","transcript_path":"/home/rowanq/.claude/projects/-home-rowanq-pairtime-capture-project/` + plantedSession + `.jsonl","cwd":"/home/rowanq/pairtime-capture/project","hook_event_name":"UserPromptSubmit","prompt":"What does count_words do? Answer in one sentence."}`,
	"hooks/000001/env":   "HOME\nPATH\nROWANQ_TOKEN\n",
	"hooks/000002/stdin": `{"hook_event_name":"UserPromptSubmit","prompt":"my diary entry, which is not in the script"}`,
	"transcripts/home/.claude/projects/-home-rowanq-pairtime-capture-project/" + plantedSession + ".jsonl": `{"type":"user","cwd":"C:\\Users\\RowanQ\\pairtime-capture\\project","sessionId":"` + plantedSession + `"}
{"type":"assistant","message":{"content":[{"type":"tool_use","id":"` + plantedTool + `","input":{"command":"whoami"}}]}}
{"toolUseResult":{"stdout":"rowanq\nrowanq@rowans-laptop:~$ git log\nAuthor: Rowan Quinn <rowan.quinn@westfield.edu>\nsk-ant-api03-AbCdEfGhIjKlMnOpQrStUvWxYz012345"},"uri":"file:///c%3A/Users/RowanQ/pairtime-capture/project"}
{"nested":"{\"stdout\":\"C:\\\\Users\\\\RowanQ\\\\x\"}","school":"Westfield High"}
{"keys":"ghp_AbCdEfGhIjKlMnOpQrStUvWxYz0123456789 AKIAABCDEFGHIJKLMNOP eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N_XgL0n3I9PlFUP0THsR8U","trailer":"Co-Authored-By: Claude <noreply@anthropic.com>\nCo-authored-by: aider (gpt-5) <aider@aider.chat>"}
`,
	// Account ids outside telemetry attributes: a Codex rollout's
	// session_meta (HS 6.2) and the userId in a Cline hook payload (HS 9).
	"transcripts/home/.codex/sessions/2026/11/19/rollout-2026-11-19T20-00-00-" + plantedSession + ".jsonl": `{"timestamp":"2026-11-19T20:00:00Z","type":"session_meta","payload":{"id":"` + plantedSession + `","source":"cli","creator_user_id":"user-Q9x8w7V6u5T4","creator_account_id":"acct-5566778899aa"}}
`,
	"hooks/000003/stdin": `{"clineVersion":"3.40.0","hookName":"UserPromptSubmit","taskId":"1763582400000","userId":"usr_5f4e3d2c1b0a","prompt":"What does count_words do? Answer in one sentence."}`,
	"otlp/000001.json":   `{"resourceLogs":[{"resource":{"attributes":[{"key":"user.account_uuid","value":{"stringValue":"9b1e7f9e-1111-4222-8333-444455556666"}},{"key":"organization.id","value":{"stringValue":"77aa88bb-1111-4222-8333-444455556666"}},{"key":"user.email","value":{"stringValue":"rowan.quinn@westfield.edu"}},{"key":"session.id","value":{"stringValue":"` + plantedSession + `"}}]}}]}`,
}

// scrubArgs are the values the planted capture's person would have: given
// as flags, so the runner's own home and hostname don't matter.
var scrubArgs = []string{"--no-detect", "--home", "/home/rowanq", "--home", `C:\Users\RowanQ`, "--user", "rowanq",
	"--host", "rowans-laptop", "--git-name", "Rowan Quinn", "--git-email", "rowan.quinn@westfield.edu", "--also", "Westfield High"}

func TestScrub(t *testing.T) {
	unixOnly(t)
	// An empty home folder, so no instruction file of the runner's own is
	// looked for.
	quiet := []string{"HOME=" + t.TempDir()}
	work := t.TempDir()
	runDir := filepath.Join(work, "run")
	for path, text := range planted {
		write(t, filepath.Join(runDir, filepath.FromSlash(path)), text)
	}
	if err := os.WriteFile(filepath.Join(runDir, "otlp", "000002.bin"), []byte{0xff, 0xfe, 0x00, 0x81}, 0o644); err != nil {
		t.Fatal(err)
	}
	outDir := filepath.Join(work, "out")
	r := run(t, ".", nil, quiet, python(t, "scrub.py", append(append([]string{}, scrubArgs...), runDir, outDir)...)...)
	if r.code != 0 {
		t.Fatalf("scrub.py exit %d, want 0:\n%s%s", r.code, r.stdout, r.stderr)
	}
	out := tree(t, outDir)
	all := strings.Builder{}
	for path, text := range out {
		all.WriteString(path + "\n" + text + "\n")
	}
	everything := strings.ToLower(all.String())

	cases := []struct {
		name  string
		check func(t *testing.T)
	}{
		{"no planted value survives in any file or path, in any case", func(t *testing.T) {
			for _, v := range []string{"rowan", "westfield", "sk-ant-api03", "ghp_", "akiaabcd", "eyjhbgci", "9b1e7f9e", "77aa88bb",
				"q9x8w7v6", "5566778899aa", "5f4e3d2c1b0a"} {
				if strings.Contains(everything, v) {
					t.Errorf("%q survived the scrub", v)
				}
			}
		}},
		{"names and addresses get their placeholders", func(t *testing.T) {
			for _, want := range []string{"capture@capture-host", "Capture User <capture@example.invalid>", `"school":"REDACTED"`, "capture_TOKEN"} {
				if !strings.Contains(all.String(), want) {
					t.Errorf("no %s in the output", want)
				}
			}
		}},
		{"keys and tokens become REDACTED placeholders", func(t *testing.T) {
			for _, want := range []string{"REDACTED_ANTHROPIC_KEY", "REDACTED_GITHUB_TOKEN", "REDACTED_AWS_KEY_ID", "REDACTED_JWT"} {
				if !strings.Contains(all.String(), want) {
					t.Errorf("no %s in the output", want)
				}
			}
		}},
		{"telemetry account ids become the fixed id, and the email its placeholder", func(t *testing.T) {
			got := out["otlp/000001.json"]
			if strings.Count(got, "00000000-0000-4000-8000-000000000000") != 2 || !strings.Contains(got, `"stringValue":"capture@example.invalid"`) {
				t.Fatalf("otlp body: %s", got)
			}
		}},
		{"account ids outside telemetry attributes become the fixed id", func(t *testing.T) {
			for path, want := range map[string][]string{
				"transcripts/home/.codex/sessions/2026/11/19/rollout-2026-11-19T20-00-00-" + plantedSession + ".jsonl": {
					`"creator_user_id":"00000000-0000-4000-8000-000000000000"`, `"creator_account_id":"00000000-0000-4000-8000-000000000000"`},
				"hooks/000003/stdin": {`"userId":"00000000-0000-4000-8000-000000000000"`, `"taskId":"1763582400000"`},
			} {
				for _, w := range want {
					if !strings.Contains(out[path], w) {
						t.Errorf("%s lacks %s:\n%s", path, w, out[path])
					}
				}
			}
		}},
		{"ids that aren't secrets and public tool addresses are kept", func(t *testing.T) {
			for _, want := range []string{plantedSession, plantedTool, "noreply@anthropic.com", "aider@aider.chat"} {
				if !strings.Contains(all.String(), want) {
					t.Errorf("%s was removed", want)
				}
			}
		}},
		{"every scrubbed JSON file is still valid JSON", func(t *testing.T) {
			for path, text := range out {
				if !strings.HasSuffix(path, "stdin") && !strings.HasSuffix(path, ".json") && !strings.HasSuffix(path, ".jsonl") {
					continue
				}
				for _, line := range strings.Split(strings.TrimSpace(text), "\n") {
					if !json.Valid([]byte(line)) {
						t.Errorf("%s: invalid JSON after the scrub: %s", path, line)
					}
				}
			}
		}},
		{"a file that isn't UTF-8 text is left out", func(t *testing.T) {
			if _, ok := out["otlp/000002.bin"]; ok || !strings.Contains(r.stdout, "000002.bin") {
				t.Fatalf("binary file copied or not reported:\n%s", r.stdout)
			}
		}},
		{"the report sits beside the run, holds the originals, and isn't in the output", func(t *testing.T) {
			report := read(t, runDir+"-scrub-report.txt")
			if !strings.Contains(report, "rowanq") || !strings.Contains(report, "hooks/000001/stdin:1") {
				t.Errorf("report lacks the replacements:\n%s", report)
			}
			if strings.Contains(report, "AbCdEfGhIjKlMnOpQrStUv") {
				t.Errorf("report shows a whole key")
			}
		}},
		{"a prompt not in SCRIPTS.md is flagged and a scripted one isn't", func(t *testing.T) {
			if !strings.Contains(r.stdout, "my diary entry") {
				t.Errorf("unscripted prompt not flagged:\n%s", r.stdout)
			}
			if strings.Contains(r.stdout, "What does count_words do") {
				t.Errorf("scripted prompt flagged:\n%s", r.stdout)
			}
		}},
		{"scrubbing the output again replaces nothing", func(t *testing.T) {
			again := filepath.Join(work, "again")
			r := run(t, ".", nil, quiet, python(t, "scrub.py", append(append([]string{}, scrubArgs...), "--report", filepath.Join(work, "r2.txt"), outDir, again)...)...)
			if r.code != 0 || !strings.Contains(r.stdout, "Nothing was replaced.") {
				t.Fatalf("exit %d:\n%s", r.code, r.stdout)
			}
		}},
		// README keeps any name under example.com, and the account inside
		// one still goes, as in pairtime fixtures scrub (D-email-redaction).
		{"an address under a domain reserved for examples is kept, and a known value inside it is still replaced", func(t *testing.T) {
			dir := t.TempDir()
			write(t, filepath.Join(dir, "run", "notes.txt"), "rowanq@ci.example.com\n")
			r := run(t, ".", nil, quiet, python(t, "scrub.py", "--no-detect", "--user", "rowanq", filepath.Join(dir, "run"), filepath.Join(dir, "out"))...)
			if r.code != 0 {
				t.Fatalf("exit %d:\n%s%s", r.code, r.stdout, r.stderr)
			}
			if got := read(t, filepath.Join(dir, "out", "notes.txt")); got != "capture@ci.example.com\n" {
				t.Fatalf("notes.txt = %q, want %q", got, "capture@ci.example.com\n")
			}
		}},
		{"a known value inside a longer word is reported, fails the run, and nothing is written", func(t *testing.T) {
			dir := t.TempDir()
			write(t, filepath.Join(dir, "run", "notes.txt"), "rowanqsLaptop\n")
			write(t, filepath.Join(dir, "run", "clean.txt"), "nothing private\n")
			r := run(t, ".", nil, quiet, python(t, "scrub.py", "--no-detect", "--user", "rowanq", filepath.Join(dir, "run"), filepath.Join(dir, "out"))...)
			if r.code != 1 || !strings.Contains(r.stdout, "STILL PRESENT") || !strings.Contains(r.stdout, "notes.txt:1") {
				t.Fatalf("exit %d:\n%s", r.code, r.stdout)
			}
			if _, err := os.Stat(filepath.Join(dir, "out")); err == nil {
				t.Fatalf("the scrub wrote %v although it found a leftover", keys(tree(t, filepath.Join(dir, "out"))))
			}
		}},
		{"--accept-leftovers writes the output once the person has checked them", func(t *testing.T) {
			dir := t.TempDir()
			write(t, filepath.Join(dir, "run", "notes.txt"), "rowanqsLaptop\n")
			r := run(t, ".", nil, quiet, python(t, "scrub.py", "--no-detect", "--user", "rowanq", "--accept-leftovers",
				filepath.Join(dir, "run"), filepath.Join(dir, "out"))...)
			if r.code != 0 || !strings.Contains(r.stdout, "STILL PRESENT") {
				t.Fatalf("exit %d, want 0 with the leftover still listed:\n%s", r.code, r.stdout)
			}
			if got := read(t, filepath.Join(dir, "out", "notes.txt")); got != "rowanqsLaptop\n" {
				t.Fatalf("notes.txt = %q", got)
			}
		}},
		{"an output folder inside the run is refused", func(t *testing.T) {
			r := run(t, ".", nil, quiet, python(t, "scrub.py", "--no-detect", runDir, filepath.Join(runDir, "out"))...)
			if r.code != 2 {
				t.Fatalf("exit %d, want 2:\n%s", r.code, r.stderr)
			}
		}},
		{"a report inside the pairtime checkout is refused, and nothing is written", func(t *testing.T) {
			report := filepath.Join(kit(t, "."), "inside-scrub-report.txt")
			out := filepath.Join(t.TempDir(), "out")
			r := run(t, ".", nil, quiet, python(t, "scrub.py", "--no-detect", "--report", report, runDir, out)...)
			if r.code != 2 || !strings.Contains(r.stderr, "inside the pairtime checkout") {
				t.Fatalf("exit %d, want 2:\n%s", r.code, r.stderr)
			}
			for _, p := range []string{report, out} {
				if _, err := os.Stat(p); err == nil {
					t.Errorf("%s was written", p)
				}
			}
		}},
		{"scrubbing a fixture already in the checkout needs --report outside it", func(t *testing.T) {
			repo := filepath.Join(t.TempDir(), "pairtime")
			scrub := filepath.Join(copyKit(t, repo), "scrub.py")
			fixture := filepath.Join(repo, "testdata", "fixtures", "claude_code", "2.1.328", "linux", "cli")
			write(t, filepath.Join(fixture, "notes.txt"), "nothing private\n")
			py := []string{need(t, "python3"), "-I", "-B", scrub, "--no-detect"}
			r := run(t, ".", nil, quiet, append(py, fixture, filepath.Join(t.TempDir(), "check"))...)
			if r.code != 2 || !strings.Contains(r.stderr, "--report") {
				t.Fatalf("the default report beside the fixture: exit %d, want 2 and a pointer to --report:\n%s", r.code, r.stderr)
			}
			if _, err := os.Stat(fixture + "-scrub-report.txt"); err == nil {
				t.Fatal("a report was written beside the fixture")
			}
			r = run(t, ".", nil, quiet, append(py, "--report", filepath.Join(t.TempDir(), "r.txt"), fixture, filepath.Join(t.TempDir(), "check"))...)
			if r.code != 0 {
				t.Fatalf("with --report outside: exit %d:\n%s%s", r.code, r.stdout, r.stderr)
			}
		}},
		{"git ignores scrub reports anywhere in the checkout", func(t *testing.T) {
			git := need(t, "git")
			if r := run(t, ".", nil, nil, git, "rev-parse", "--is-inside-work-tree"); r.code != 0 {
				t.Skip("not a git checkout")
			}
			r := run(t, ".", nil, nil, git, "check-ignore", "-q", filepath.Join("..", "..", "testdata", "fixtures", "claude_code",
				"2.1.328", "linux", "cli-scrub-report.txt"))
			if r.code != 0 {
				t.Fatalf("git check-ignore exit %d: a report beside a fixture isn't ignored", r.code)
			}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, c.check)
	}

	// The lines of TestFixturesScrub's cases of the same names, in
	// cmd/pairtime, so both scrubs keep the same addresses and remove the same
	// keys and passwords. scrub.py's key patterns need nothing after a key, so
	// it finds one followed by an @ or a colon where it stands. Its
	// placeholders name its own patterns.
	stripe := "sk_" + "live_" + strings.Repeat("Qw81Zx4LmN", 3) // built at run time, so no file in the repo looks like a leaked key
	anthropic := "sk-ant-" + "api03-" + strings.Repeat("aB3_x9Kq-Z", 10)[:93] + "AA"
	agree := []struct{ name, in, want string }{
		{"a key that is a kept address's local part, as a URL's user, is still replaced",
			"https://" + stripe + "@api.example.com/v1/charges\n" +
				"https://" + anthropic + "@api.example.com/v1/messages\n",
			"https://REDACTED_STRIPE_KEY@api.example.com/v1/charges\n" +
				"https://REDACTED_ANTHROPIC_KEY@api.example.com/v1/messages\n"},
		{"a key that is the user of a URL before its password, at a kept address, is still replaced",
			"https://" + stripe + ":x-oauth-basic@api.example.com/v1/charges\n" +
				"https://" + anthropic + ":x-oauth-basic@api.example.com/v1/messages\n",
			"https://REDACTED_STRIPE_KEY:REDACTED_URL_PASSWORD@api.example.com/v1/charges\n" +
				"https://REDACTED_ANTHROPIC_KEY:REDACTED_URL_PASSWORD@api.example.com/v1/messages\n"},
		{"a key that is a URL's user is replaced whatever the host",
			"https://" + stripe + ":x-oauth-basic@api.stripe.com/v1/charges\n" +
				"https://" + anthropic + ":@api.anthropic.com/v1/messages\n" +
				"postgres://" + stripe + ":pw@db:5432/app\n",
			"https://REDACTED_STRIPE_KEY:capture@example.invalid/v1/charges\n" +
				"https://REDACTED_ANTHROPIC_KEY:@api.anthropic.com/v1/messages\n" +
				"postgres://REDACTED_STRIPE_KEY:REDACTED_URL_PASSWORD@db:5432/app\n"},
		{"a URL's user and password at a kept address are replaced however the URL is written",
			`{"url":"postgres:\/\/app:Tr0ub4dor3x@db.example.com\/app"}` + "\n" +
				"//app:Tr0ub4dor3x@db.example.com/app\n" +
				"redis://:hunter2hunter2@cache.example.com:6379\n" +
				`{"stdout":"{\"url\":\"https:\\/\\/` + stripe + `:x@api.example.com\"}"}` + "\n",
			`{"url":"postgres:\/\/app:REDACTED_URL_PASSWORD@db.example.com\/app"}` + "\n" +
				"//app:REDACTED_URL_PASSWORD@db.example.com/app\n" +
				"redis://:REDACTED_URL_PASSWORD@cache.example.com:6379\n" +
				`{"stdout":"{\"url\":\"https:\\/\\/REDACTED_STRIPE_KEY:REDACTED_URL_PASSWORD@api.example.com\"}"}` + "\n"},
		{"a placeholder an earlier run wrote is left alone, even where another rule finds it",
			"postgres://app:REDACTED_STRIPE_KEY@db.example.com/app\npostgres://app:capture@example.invalid/app\n" +
				"https://REDACTED_STRIPE_KEY:capture@example.invalid/v1/charges\n",
			"postgres://app:REDACTED_STRIPE_KEY@db.example.com/app\npostgres://app:capture@example.invalid/app\n" +
				"https://REDACTED_STRIPE_KEY:capture@example.invalid/v1/charges\n"},
		{"a span that runs on past a placeholder isn't one, so all of it is replaced",
			"postgres://app:REDACTED_URL_PASSWORD@ssw0rd@db.example.com/app\n",
			"postgres://app:REDACTED_URL_PASSWORD@db.example.com/app\n"},
		{"a URL's password with ?, # or @ in it runs to its last @, and the user before it is still scanned",
			"https://" + stripe + ":pa?ss1@api.example.com/v1/charges\n" +
				`{"url":"postgres:\/\/app:Tr0ub#4dor@db.example.com\/app"}` + "\n" +
				"//app:Tr0ub?4dor@db:5432/app\n" +
				"postgres://app:p@ssw0rd@db.example.com/app\n",
			"https://REDACTED_STRIPE_KEY:REDACTED_URL_PASSWORD@api.example.com/v1/charges\n" +
				`{"url":"postgres:\/\/app:REDACTED_URL_PASSWORD@db.example.com\/app"}` + "\n" +
				"//app:REDACTED_URL_PASSWORD@db:5432/app\n" +
				"postgres://app:REDACTED_URL_PASSWORD@db.example.com/app\n"},
		{"an address that is a URL's user is replaced or kept like any other address",
			"ftp://jane@westfield.edu:s3cretpw@ftp.example.com/\nftp://noreply@anthropic.com:s3cretpw@ftp.example.com/\n",
			"ftp://capture@example.invalid:REDACTED_URL_PASSWORD@ftp.example.com/\nftp://noreply@anthropic.com:REDACTED_URL_PASSWORD@ftp.example.com/\n"},
		{"two slashes with no scheme before them aren't a URL when a bracket follows, as in namespaced XPath",
			"tree.xpath('//xs:element[@name]', namespaces=ns)\npom.findall(\".//pom:dependency[@scope]\")\n",
			"tree.xpath('//xs:element[@name]', namespaces=ns)\npom.findall(\".//pom:dependency[@scope]\")\n"},
		{"a key as a URL's user is still found when the URL runs on into a query or fragment holding an @",
			"http://" + stripe + "@localhost?next=a@b\n" +
				"http://" + stripe + "@localhost:3000?email=test@example.com\n" +
				"http://" + anthropic + "@localhost?next=a@b\n" +
				"https://" + stripe + "#frag@x\n" +
				"https://" + stripe + "?x=1@y\n",
			"http://REDACTED_STRIPE_KEY@localhost?next=a@b\n" +
				"http://REDACTED_STRIPE_KEY@localhost:REDACTED_URL_PASSWORD@example.com\n" +
				"http://REDACTED_ANTHROPIC_KEY@localhost?next=a@b\n" +
				"https://REDACTED_STRIPE_KEY#frag@x\n" +
				"https://REDACTED_STRIPE_KEY?x=1@y\n"},
		{"a URL with no scheme has its user scanned, and its password ends at the last @ before a bracket",
			"//" + stripe + ":pa[ss]@db:5432/app\n" +
				"//app:hunter2pw@localhost?ids[]=1@x\n" +
				"//user:pw@[::1]:8080/x\n",
			"//REDACTED_STRIPE_KEY:pa[ss]@db:5432/app\n" +
				"//app:REDACTED_URL_PASSWORD@localhost?ids[]=1@x\n" +
				"//user:REDACTED_URL_PASSWORD@[::1]:8080/x\n"},
		// D-email-redaction-tld.
		{"under a reserved top-level domain only a one-label name is kept, so a real address with .invalid appended is replaced",
			"Owner: jane.doe@westfield.edu.invalid\nci@runner.ci.test ci@build.example.invalid\n" +
				"user@myapp.test dev@build.localhost a@b.example capture@example.invalid\n",
			"Owner: capture@example.invalid\ncapture@example.invalid capture@example.invalid\n" +
				"user@myapp.test dev@build.localhost a@b.example capture@example.invalid\n"},
	}
	for _, c := range agree {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			write(t, filepath.Join(dir, "run", "notes.txt"), c.in)
			r := run(t, ".", nil, quiet, python(t, "scrub.py", "--no-detect", filepath.Join(dir, "run"), filepath.Join(dir, "out"))...)
			if r.code != 0 {
				t.Fatalf("exit %d:\n%s%s", r.code, r.stdout, r.stderr)
			}
			if got := read(t, filepath.Join(dir, "out", "notes.txt")); got != c.want {
				t.Fatalf("notes.txt = %q, want %q", got, c.want)
			}
		})
	}

	// Every spelling of a home folder, from a scrub that knows only the home
	// folders. Their last parts aren't account names, so nothing but the
	// home rules can replace them, and a spelling the scrub doesn't make
	// survives.
	t.Run("a home folder is replaced in every spelling the files use", func(t *testing.T) {
		spellings := []struct{ name, in, want string }{
			{"as is", `/srv/homes/qz9-casa/pairtime-capture/project`, `/home/capture/pairtime-capture/project`},
			{"as is, on Windows", `C:\Profiles\Qz9 Casa\x`, `C:\Users\capture\x`},
			{"JSON-escaped", `"C:\\Profiles\\Qz9 Casa\\pairtime-capture"`, `"C:\\Users\\capture\\pairtime-capture"`},
			{"escaped twice, in JSON inside a JSON string", `"{\"cwd\":\"C:\\\\Profiles\\\\Qz9 Casa\\\\x\"}"`,
				`\"C:\\\\Users\\\\capture\\\\x\"`},
			{"with forward slashes", `C:/Profiles/Qz9 Casa/x`, `C:/Users/capture/x`},
			{"in a file URL", `file:///c%3A/Profiles/Qz9%20Casa/x`, `file:///C%3A/Users/capture/x`},
			{"URL-encoded with its backslashes", `C%3A%5CProfiles%5CQz9%20Casa%5Cx`, `C%3A%5CUsers%5Ccapture%5Cx`},
			{"with JSON's escaped slashes", `"\/srv\/homes\/qz9-casa\/x"`, `"\/home\/capture\/x"`},
			{"as Claude Code names a project folder", `-srv-homes-qz9-casa-pairtime-capture-project`,
				`-home-capture-pairtime-capture-project`},
			{"as Claude Code names a Windows project folder", `C--Profiles-Qz9-Casa-pairtime-capture-project`,
				`C--Users-capture-pairtime-capture-project`},
		}
		var lines []string
		for _, c := range spellings {
			lines = append(lines, c.in)
		}
		dir := t.TempDir()
		folder := "transcripts/home/.claude/projects/-srv-homes-qz9-casa-pairtime-capture-project/s.jsonl"
		write(t, filepath.Join(dir, "run", "notes.txt"), strings.Join(lines, "\n")+"\n")
		write(t, filepath.Join(dir, "run", filepath.FromSlash(folder)), "{}\n")
		r := run(t, ".", nil, quiet, python(t, "scrub.py", "--no-detect", "--home", "/srv/homes/qz9-casa", "--home", `C:\Profiles\Qz9 Casa`,
			filepath.Join(dir, "run"), filepath.Join(dir, "out"))...)
		if r.code != 0 {
			t.Fatalf("exit %d:\n%s%s", r.code, r.stdout, r.stderr)
		}
		out := tree(t, filepath.Join(dir, "out"))
		got := out["notes.txt"]
		for _, c := range spellings {
			t.Run(c.name, func(t *testing.T) {
				if !strings.Contains(got, c.want) {
					t.Errorf("no %s in:\n%s", c.want, got)
				}
			})
		}
		t.Run("folder names are scrubbed like file contents", func(t *testing.T) {
			if _, ok := out["transcripts/home/.claude/projects/-home-capture-pairtime-capture-project/s.jsonl"]; !ok {
				t.Fatalf("got %v", keys(out))
			}
		})
		t.Run("no spelling of either home survives", func(t *testing.T) {
			for path, text := range out {
				if strings.Contains(strings.ToLower(path+text), "qz9") {
					t.Errorf("%s still holds qz9:\n%s", path, text)
				}
			}
		})
	})

	// Windows gives C:\Users\Karim Abdallah the short name KARIMA~1 too, and
	// often sets %TEMP% with it. Any Users\NAME~N folder is a home folder,
	// so it goes even when the scrub knows nothing about the machine.
	t.Run("a Windows short home folder name is replaced in every spelling, from any machine", func(t *testing.T) {
		shorts := []struct{ name, in, want string }{
			{"JSON-escaped, in a temp path", `{"stdout":"C:\\Users\\KARIMA~1\\AppData\\Local\\Temp\\claude-x"}`,
				`{"stdout":"C:\\Users\\capture\\AppData\\Local\\Temp\\claude-x"}`},
			{"as is", `C:\Users\JOHNSM~2\x`, `C:\Users\capture\x`},
			{"with forward slashes", `C:/Users/ROWANQ~1/x`, `C:/Users/capture/x`},
			{"URL-encoded", `C%3A%5CUsers%5CROWANQ~1%5Cx`, `C%3A%5CUsers%5Ccapture%5Cx`},
			{"escaped twice", `"C:\\\\Users\\\\KARIMA~1\\\\x"`, `"C:\\\\Users\\\\capture\\\\x"`},
			{"a short name outside Users stays", `D:\Backup\README~1.TXT`, `D:\Backup\README~1.TXT`},
		}
		var lines []string
		for _, c := range shorts {
			lines = append(lines, c.in)
		}
		dir := t.TempDir()
		write(t, filepath.Join(dir, "run", "notes.txt"), strings.Join(lines, "\n")+"\n")
		r := run(t, ".", nil, quiet, python(t, "scrub.py", "--no-detect", filepath.Join(dir, "run"), filepath.Join(dir, "out"))...)
		if r.code != 0 {
			t.Fatalf("exit %d:\n%s%s", r.code, r.stdout, r.stderr)
		}
		got := read(t, filepath.Join(dir, "out", "notes.txt"))
		for _, c := range shorts {
			t.Run(c.name, func(t *testing.T) {
				if !strings.Contains(got, c.want) {
					t.Errorf("no %s in:\n%s", c.want, got)
				}
			})
		}
	})
	t.Run("the short home folder in %TEMP% is found, and a long one gives none", func(t *testing.T) {
		code := "import json, sys; sys.path.insert(0, sys.argv[1]); import scrub; " +
			"print(json.dumps([scrub.short_homes([], {'TEMP': t}) for t in sys.argv[2:]]))"
		r := run(t, ".", nil, nil, need(t, "python3"), "-I", "-B", "-c", code, kit(t, "."),
			`C:\Users\KARIMA~1\AppData\Local\Temp`, `C:\Users\Karim Abdallah\AppData\Local\Temp`)
		var got [][]string
		if err := json.Unmarshal([]byte(r.stdout), &got); err != nil {
			t.Fatalf("%q: %v (%s)", r.stdout, err, r.stderr)
		}
		if len(got) != 2 || len(got[0]) != 1 || got[0][0] != `C:\Users\KARIMA~1` || len(got[1]) != 0 {
			t.Fatalf("short_homes = %q, want [[C:\\Users\\KARIMA~1] []]", got)
		}
	})

	// The person's own instruction files: a line of one in the capture means
	// the harness loaded it, whatever the isolation was meant to do. The
	// quote in it is JSON-escaped in the transcript, and the scripted prompt
	// both files hold is the kit's text, not theirs.
	mine := `Always answer as "Rowan the pirate", matey.`
	scripted := "What does count_words do? Answer in one sentence."
	instructions := []struct {
		name string
		// file is where the instruction file sits: below the home folder,
		// or, starting with ../, in a folder above the scratch project.
		file string
	}{
		{"a line of your own CLAUDE.md in a transcript fails the scrub", ".claude/CLAUDE.md"},
		{"a line of an AGENTS.md in a folder above the project fails the scrub", "../AGENTS.md"},
		{"a file moved aside for the capture is still checked", ".codex/AGENTS.md.pairtime-aside"},
	}
	for _, c := range instructions {
		t.Run(c.name, func(t *testing.T) {
			own, dir := t.TempDir(), t.TempDir()
			project := filepath.Join(dir, "capture", "project")
			path := filepath.Join(own, filepath.FromSlash(c.file))
			if strings.HasPrefix(c.file, "../") {
				path = filepath.Join(project, filepath.FromSlash(c.file))
			}
			write(t, path, "# Mine\n\n"+mine+"\n"+scripted+"\n")
			runDir := filepath.Join(dir, "capture", "runs", "cli")
			write(t, filepath.Join(runDir, "hooks", "000001", "cwd"), project+"\n")
			write(t, filepath.Join(runDir, "transcripts", "t.jsonl"), jsonText(t, map[string]any{
				"type": "user", "message": map[string]any{"content": "Instructions:\n" + mine + "\n" + scripted}})+"\n")
			r := run(t, ".", nil, []string{"HOME=" + own}, python(t, "scrub.py", "--no-detect", runDir, filepath.Join(dir, "out"))...)
			if r.code != 1 || !strings.Contains(r.stdout, "FROM YOUR OWN INSTRUCTION FILES") ||
				!strings.Contains(r.stdout, "holds line 3 of "+path) {
				t.Fatalf("exit %d, want 1 and line 3 of %s flagged:\n%s%s", r.code, path, r.stdout, r.stderr)
			}
			if strings.Contains(r.stdout, "line 4 of") {
				t.Errorf("the scripted prompt was flagged as the person's own text:\n%s", r.stdout)
			}
			if _, err := os.Stat(filepath.Join(dir, "out")); err == nil {
				t.Errorf("the scrub wrote its output although the capture holds the person's instructions")
			}
		})
	}
	// An account named User, a common Windows default, scrubbed with no
	// --home: the field values and keys that hold the word stay, and the
	// name is still replaced where it stands as a path or an identity.
	t.Run("a common account name such as Windows' User", func(t *testing.T) {
		common := []struct{ name, in, want string }{
			{`"type":"user" and "role":"user" stay`, `{"type":"user","message":{"role":"user"}}`,
				`{"type":"user","message":{"role":"user"}}`},
			{"a telemetry key named user.email stays", `{"key":"user.email","value":{"stringValue":"a@b.example"}}`,
				`{"key":"user.email",`},
			{"a home folder holding it is replaced in its JSON spelling", `{"cwd":"C:\\Users\\User\\pairtime-capture"}`,
				`{"cwd":"C:\\Users\\capture\\pairtime-capture"}`},
			{"name@host and HOST\\name are replaced", `{"stdout":"User@DESKTOP-AB12CD:~$ whoami\nDESKTOP-AB12CD\\User"}`,
				`capture@capture-host:~$ whoami\ncapture-host\\capture`},
			{"words that hold it stay", `{"text":"Username and users stay"}`, `Username and users stay`},
		}
		var lines []string
		for _, c := range common {
			lines = append(lines, c.in)
		}
		dir := t.TempDir()
		folder := "transcripts/home/.claude/projects/C--Users-User-pairtime-capture-project/s.jsonl"
		write(t, filepath.Join(dir, "run", filepath.FromSlash(folder)), strings.Join(lines, "\n")+"\n")
		r := run(t, ".", nil, quiet, python(t, "scrub.py", "--no-detect", "--user", "User", "--host", "DESKTOP-AB12CD",
			filepath.Join(dir, "run"), filepath.Join(dir, "out"))...)
		if r.code != 0 || !strings.Contains(r.stdout, "never as words on their own: User") {
			t.Fatalf("exit %d, want 0 and User reported as a common word:\n%s%s", r.code, r.stdout, r.stderr)
		}
		out := tree(t, filepath.Join(dir, "out"))
		got, ok := out["transcripts/home/.claude/projects/C--Users-capture-pairtime-capture-project/s.jsonl"]
		if !ok {
			t.Fatalf("the project folder name wasn't scrubbed: %v", keys(out))
		}
		for _, c := range common {
			t.Run(c.name, func(t *testing.T) {
				if !strings.Contains(got, c.want) {
					t.Errorf("no %s in:\n%s", c.want, got)
				}
			})
		}
	})

	// A name that is only common for this person goes the same way with
	// --common, and one under four characters without it.
	names := []struct {
		name  string
		args  []string
		code  int
		stays string
	}{
		{"an account name inside ordinary words fails the scrub", []string{"--user", "mark"}, 1, ""},
		{"--common replaces it only in paths and as name@host", []string{"--user", "mark", "--common", "mark"}, 0, "markdown"},
		{"a name under four characters is replaced only in paths and as name@host", []string{"--user", "al"}, 0, "always"},
	}
	for _, c := range names {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			user := c.args[1]
			write(t, filepath.Join(dir, "run", "notes.txt"), "/home/"+user+"/x "+user+"@box always markdown\n")
			r := run(t, ".", nil, quiet, python(t, "scrub.py", append(append([]string{"--no-detect"}, c.args...),
				filepath.Join(dir, "run"), filepath.Join(dir, "out"))...)...)
			if r.code != c.code {
				t.Fatalf("exit %d, want %d:\n%s%s", r.code, c.code, r.stdout, r.stderr)
			}
			if c.code != 0 {
				return
			}
			got := read(t, filepath.Join(dir, "out", "notes.txt"))
			if !strings.Contains(got, "/home/capture/x capture@box") || !strings.Contains(got, c.stays) {
				t.Fatalf("notes.txt = %q, want the path and name@box replaced and %q kept", got, c.stays)
			}
		})
	}

	t.Run("pasted blocks and a history file's prompt text are checked against SCRIPTS.md", func(t *testing.T) {
		step3 := "Here is a spec. Say in one sentence whether it is clear, and don't write any code yet.\n" +
			"count_lines(text) returns how many lines of text are not empty.\nA line holding only spaces counts as empty."
		entry := func(fields map[string]any) string { return jsonText(t, fields) + "\n" }
		dir := t.TempDir()
		write(t, filepath.Join(dir, "run", "transcripts", "home", ".claude", "history.jsonl"),
			entry(map[string]any{"display": "[Pasted text #1 +3 lines]", "pastedContents": map[string]any{
				"1": map[string]any{"id": 1, "type": "text", "content": step3}}})+
				entry(map[string]any{"display": "[Pasted text #1 +1 lines]", "pastedContents": map[string]any{
					"1": map[string]any{"id": 1, "type": "text", "content": "my private notes"}}})+
				entry(map[string]any{"session_id": "s", "ts": 1, "text": "my secret plan"}))
		r := run(t, ".", nil, quiet, python(t, "scrub.py", "--no-detect", filepath.Join(dir, "run"), filepath.Join(dir, "out"))...)
		for _, want := range []string{"my private notes", "my secret plan"} {
			if !strings.Contains(r.stdout, want) {
				t.Errorf("%q isn't flagged as unscripted:\n%s", want, r.stdout)
			}
		}
		for _, scripted := range []string{"Here is a spec", "Pasted text"} {
			if strings.Contains(r.stdout, scripted) {
				t.Errorf("%q is flagged, but it's the script's own paste or Claude Code's placeholder:\n%s", scripted, r.stdout)
			}
		}
	})

	t.Run("a capture without a line of your instruction files passes", func(t *testing.T) {
		own, dir := t.TempDir(), t.TempDir()
		write(t, filepath.Join(own, ".claude", "CLAUDE.md"), mine+"\n")
		write(t, filepath.Join(dir, "run", "transcripts", "t.jsonl"), jsonText(t, map[string]any{"type": "user", "content": scripted})+"\n")
		r := run(t, ".", nil, []string{"HOME=" + own}, python(t, "scrub.py", "--no-detect", filepath.Join(dir, "run"), filepath.Join(dir, "out"))...)
		if r.code != 0 || strings.Contains(r.stdout, "INSTRUCTION FILES") {
			t.Fatalf("exit %d, want 0:\n%s%s", r.code, r.stdout, r.stderr)
		}
	})
}

// claudeHooks is DESIGN 5.1's Claude Code hook list, with its matchers.
var claudeHooks = map[string]string{
	"SessionStart": "", "UserPromptSubmit": "", "Stop": "", "StopFailure": "", "Notification": "",
	"PermissionRequest": "", "PostToolUse": "Edit|Write|Bash", "SubagentStart": "", "SubagentStop": "", "SessionEnd": "",
}

type hookHandler struct {
	Type    string   `json:"type"`
	Command string   `json:"command"`
	Args    []string `json:"args"`
	Timeout int      `json:"timeout"`
}

type hookEntry struct {
	Matcher string        `json:"matcher"`
	Hooks   []hookHandler `json:"hooks"`
}

func readSettings(t *testing.T, path string) map[string][]hookEntry {
	t.Helper()
	var s struct {
		Hooks map[string][]hookEntry `json:"hooks"`
	}
	if err := json.Unmarshal([]byte(read(t, path)), &s); err != nil {
		t.Fatal(err)
	}
	return s.Hooks
}

// copyKit copies the kit's scripts into repo/tools/capture, keeping their
// modes, and returns that folder. prepare.py takes the folder two levels up
// as the pairtime checkout.
func copyKit(t *testing.T, repo string) string {
	t.Helper()
	dir := filepath.Join(repo, "tools", "capture")
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		ext := filepath.Ext(e.Name())
		if e.IsDir() || (ext != ".py" && ext != ".sh" && ext != ".ps1" && ext != ".md") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			t.Fatal(err)
		}
		write(t, filepath.Join(dir, e.Name()), read(t, e.Name()))
		if err := os.Chmod(filepath.Join(dir, e.Name()), info.Mode().Perm()); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestPrepare(t *testing.T) {
	unixOnly(t)
	need(t, "git")
	home := t.TempDir()
	root := filepath.Join(t.TempDir(), "capture")
	env := []string{"HOME=" + home, "PATH=" + os.Getenv("PATH")}
	r := run(t, ".", nil, env, python(t, "prepare.py", "claude_code", root, "cli")...)
	if r.code != 0 {
		t.Fatalf("prepare.py exit %d:\n%s%s", r.code, r.stdout, r.stderr)
	}
	project := filepath.Join(root, "projects", "cli")
	runDir := filepath.Join(root, "runs", "cli")
	settings := filepath.Join(project, ".claude", "settings.local.json")
	hooks := readSettings(t, settings)

	cases := []struct {
		name  string
		check func(t *testing.T)
	}{
		{"every DESIGN 5.1 hook runs a kit script itself in exec form, with the run and its label as args", func(t *testing.T) {
			if len(hooks) != len(claudeHooks) {
				t.Errorf("%d events, want %d: %v", len(hooks), len(claudeHooks), hooks)
			}
			for name, matcher := range claudeHooks {
				entries := hooks[name]
				if len(entries) != 1 || len(entries[0].Hooks) != 1 {
					t.Errorf("%s: %+v, want one entry with one handler", name, entries)
					continue
				}
				h := entries[0].Hooks[0]
				script := kit(t, "dump-hook.sh")
				if name == "PermissionRequest" {
					script = kit(t, "allow-once.sh")
				}
				want := []string{runDir, name}
				if h.Type != "command" || h.Command != script || strings.Join(h.Args, "\n") != strings.Join(want, "\n") {
					t.Errorf("%s: %+v, want %s %q", name, h, script, want)
				}
				if entries[0].Matcher != matcher {
					t.Errorf("%s matcher = %q, want %q", name, entries[0].Matcher, matcher)
				}
			}
			if h := hooks["SessionEnd"][0].Hooks[0]; h.Timeout < 5 {
				t.Errorf("SessionEnd timeout = %d, want room beyond the 1.5 s budget", h.Timeout)
			}
		}},
		{"PermissionRequest runs allow-once, with a timeout that leaves room for its 2 s wait", func(t *testing.T) {
			h := hooks["PermissionRequest"][0].Hooks[0]
			if filepath.Base(h.Command) != "allow-once.sh" || h.Timeout < 5 {
				t.Fatalf("PermissionRequest runs %s with timeout %d, want allow-once.sh and at least 5 s", h.Command, h.Timeout)
			}
		}},
		{"Claude Code starts in manual permission mode, so the script's prompts reach the person", func(t *testing.T) {
			var s struct {
				Permissions struct {
					DefaultMode string `json:"defaultMode"`
				} `json:"permissions"`
			}
			if err := json.Unmarshal([]byte(read(t, settings)), &s); err != nil {
				t.Fatal(err)
			}
			if s.Permissions.DefaultMode != "default" {
				t.Fatalf("permissions.defaultMode = %q, want default, which Claude Code shows as manual mode", s.Permissions.DefaultMode)
			}
		}},
		{"a configured hook really writes a call", func(t *testing.T) {
			h := hooks["UserPromptSubmit"][0].Hooks[0]
			run(t, project, payload, nil, append([]string{h.Command}, h.Args...)...)
			if got := read(t, filepath.Join(runDir, "hooks", "000001", "stdin")); got != string(payload) {
				t.Fatalf("stdin = %q", got)
			}
		}},
		{"a harness that drops args runs the dumper bare, which runs nothing from the payload", func(t *testing.T) {
			// The payload really is hostile: the old form, a bare /bin/sh
			// with the payload on stdin, runs the commands inside it.
			old := t.TempDir()
			run(t, old, hostile, nil, need(t, "sh"))
			if len(tree(t, old)) == 0 {
				t.Fatal("a bare sh ran nothing from the hostile payload, so this check would prove nothing")
			}
			h := hooks["Stop"][0].Hooks[0]
			before := len(tree(t, runDir))
			// Exec form without its args, and the command run through a shell.
			for _, argv := range [][]string{{h.Command}, {need(t, "sh"), "-c", h.Command}} {
				dir := t.TempDir()
				r := run(t, dir, hostile, nil, argv...)
				if r.code != 0 || r.stdout != "" || r.stderr != "" {
					t.Errorf("%q: exit %d, stdout %q, stderr %q; want 0 and nothing", argv, r.code, r.stdout, r.stderr)
				}
				if files := tree(t, dir); len(files) != 0 {
					t.Errorf("%q ran something from the payload: %v", argv, keys(files))
				}
			}
			if after := len(tree(t, runDir)); after != before {
				t.Errorf("a call without args wrote into the run: %d files, then %d", before, after)
			}
		}},
		{"on Windows a hook is one powershell.exe command line with no args to drop, run the same by Git Bash and PowerShell", func(t *testing.T) {
			// A stand-in for powershell.exe, which this Linux runner lacks.
			pwsh := need(t, "pwsh")
			bin := t.TempDir()
			write(t, filepath.Join(bin, "powershell.exe"), "#!/bin/sh\nexec "+pwsh+" \"$@\"\n")
			if err := os.Chmod(filepath.Join(bin, "powershell.exe"), 0o755); err != nil {
				t.Fatal(err)
			}
			// A space in the run folder, as in C:\Users\Rowan Quinn.
			winRun := filepath.Join(t.TempDir(), "rowan quinn", "run")
			code := "import json, sys; sys.path.insert(0, sys.argv[1]); import prepare; " +
				"print(json.dumps(prepare.hook_command(sys.argv[2], 'Stop', False, windows=True)))"
			r := run(t, ".", nil, nil, need(t, "python3"), "-I", "-B", "-c", code, kit(t, "."), winRun)
			var h map[string]any
			if err := json.Unmarshal([]byte(r.stdout), &h); err != nil {
				t.Fatalf("handler %q: %v (%s)", r.stdout, err, r.stderr)
			}
			cmd, _ := h["command"].(string)
			if _, ok := h["args"]; ok || len(h) != 1 || !strings.HasPrefix(cmd, "powershell.exe -NoProfile ") {
				t.Fatalf("handler %v, want only a command that starts with the bare powershell.exe", h)
			}
			env := []string{"PATH=" + bin + string(os.PathListSeparator) + os.Getenv("PATH")}
			shells := [][]string{{need(t, "sh"), "-c", cmd}, {pwsh, "-NoProfile", "-NonInteractive", "-Command", cmd}}
			for i, shell := range shells {
				if r := run(t, project, payload, env, shell...); r.code != 0 {
					t.Errorf("%s: exit %d: %s", shell[0], r.code, r.stderr)
				}
				call := filepath.Join(winRun, "hooks", "00000"+strconv.Itoa(i+1))
				if got := read(t, filepath.Join(call, "argv")); got != winRun+"\nStop\n" {
					t.Errorf("%s: argv = %q, want the run and Stop", shell[0], got)
				}
			}
			if got := read(t, filepath.Join(winRun, "hooks", "000001", "stdin")); got != string(payload) {
				t.Errorf("stdin through sh = %q", got)
			}
		}},
		{"the scratch project is a git repo with a made-up author", func(t *testing.T) {
			r := run(t, project, nil, env, "git", "log", "--format=%an <%ae>")
			if strings.TrimSpace(r.stdout) != "Capture User <capture@example.invalid>" {
				t.Fatalf("git log: %q %q", r.stdout, r.stderr)
			}
			if !strings.Contains(read(t, filepath.Join(project, "test_wordcount.py")), "test_double_space") {
				t.Fatal("no failing test in the scratch project")
			}
		}},
		{"a run that already holds hook calls is refused", func(t *testing.T) {
			r := run(t, ".", nil, env, python(t, "prepare.py", "claude_code", root, "cli")...)
			if r.code != 2 || !strings.Contains(r.stderr, "already holds a capture") {
				t.Fatalf("exit %d: %s", r.code, r.stderr)
			}
		}},
		{"each run gets a scratch project of its own, starting from the first commit", func(t *testing.T) {
			// What the CLI run's agent left: step 4's edit, committed.
			write(t, filepath.Join(project, "wordcount.py"), "def count_lines(text):\n    return 0\n")
			run(t, project, nil, env, "git", "commit", "-qam", "Add count_lines")
			r := run(t, ".", nil, env, python(t, "prepare.py", "claude_code", root, "vscode")...)
			if r.code != 0 {
				t.Fatalf("exit %d: %s", r.code, r.stderr)
			}
			vscode := filepath.Join(root, "projects", "vscode")
			if got := read(t, filepath.Join(vscode, "wordcount.py")); strings.Contains(got, "count_lines") {
				t.Errorf("the VS Code run starts from the CLI run's edit:\n%s", got)
			}
			if h := readSettings(t, filepath.Join(vscode, ".claude", "settings.local.json"))["Stop"][0].Hooks[0]; h.Args[0] != filepath.Join(root, "runs", "vscode") {
				t.Errorf("the VS Code run's Stop hook writes to %s", h.Args[0])
			}
			if h := readSettings(t, settings)["Stop"][0].Hooks[0]; h.Args[0] != runDir {
				t.Errorf("the CLI run's Stop hook now writes to %s", h.Args[0])
			}
		}},
		{"preparing a run again resets its project to the first commit", func(t *testing.T) {
			vscode := filepath.Join(root, "projects", "vscode")
			write(t, filepath.Join(vscode, "notes.txt"), "capture test\n")
			write(t, filepath.Join(vscode, "wordcount.py"), "edited\n")
			run(t, vscode, nil, env, "git", "commit", "-qam", "An edit during the run")
			r := run(t, ".", nil, env, python(t, "prepare.py", "claude_code", root, "vscode")...)
			if r.code != 0 || !strings.Contains(r.stdout, "Reset the scratch project") {
				t.Fatalf("exit %d, want 0 and a reset:\n%s%s", r.code, r.stdout, r.stderr)
			}
			if _, err := os.Stat(filepath.Join(vscode, "notes.txt")); err == nil {
				t.Errorf("notes.txt survived the reset")
			}
			if got := read(t, filepath.Join(vscode, "wordcount.py")); !strings.Contains(got, "def count_words") {
				t.Errorf("wordcount.py = %q, want the first commit's", got)
			}
			if r := run(t, vscode, nil, env, "git", "rev-list", "--count", "HEAD"); strings.TrimSpace(r.stdout) != "1" {
				t.Errorf("%s commits after the reset, want 1", strings.TrimSpace(r.stdout))
			}
			if _, err := os.Stat(filepath.Join(vscode, ".claude", "settings.local.json")); err != nil {
				t.Errorf("the hooks weren't written again: %v", err)
			}
		}},
		{"a project folder prepare.py didn't make is never reset", func(t *testing.T) {
			mine := filepath.Join(root, "projects", "mine")
			write(t, filepath.Join(mine, "precious.txt"), "keep me\n")
			r := run(t, ".", nil, env, python(t, "prepare.py", "claude_code", root, "mine")...)
			if r.code != 2 || !strings.Contains(r.stderr, "isn't a scratch project") {
				t.Fatalf("exit %d, want 2: %s", r.code, r.stderr)
			}
			if got := read(t, filepath.Join(mine, "precious.txt")); got != "keep me\n" {
				t.Fatalf("precious.txt = %q", got)
			}
		}},
		{"--shell-form writes one command string that works in a shell", func(t *testing.T) {
			r := run(t, ".", nil, env, python(t, "prepare.py", "claude_code", root, "shell", "--shell-form")...)
			if r.code != 0 {
				t.Fatalf("exit %d: %s", r.code, r.stderr)
			}
			h := readSettings(t, filepath.Join(root, "projects", "shell", ".claude", "settings.local.json"))["Stop"][0].Hooks[0]
			if len(h.Args) != 0 || !strings.Contains(h.Command, "dump-hook.sh") {
				t.Fatalf("handler %+v", h)
			}
			run(t, project, []byte("{}"), nil, need(t, "sh"), "-c", h.Command)
			if got := read(t, filepath.Join(root, "runs", "shell", "hooks", "000001", "argv")); !strings.HasSuffix(got, "\nStop\n") {
				t.Fatalf("argv = %q", got)
			}
		}},
		{"a kit path a shell would split is refused in exec form, and --shell-form quotes it", func(t *testing.T) {
			copied := copyKit(t, filepath.Join(t.TempDir(), "my stuff", "pairtime"))
			prep := []string{need(t, "python3"), "-I", "-B", filepath.Join(copied, "prepare.py"), "claude_code"}
			r := run(t, ".", nil, env, append(prep, filepath.Join(t.TempDir(), "capture"), "cli")...)
			if r.code != 2 || !strings.Contains(r.stderr, "--shell-form") {
				t.Fatalf("exit %d, want 2 and a pointer to --shell-form: %s", r.code, r.stderr)
			}
			r = run(t, ".", nil, env, append(prep, filepath.Join(t.TempDir(), "capture"), "cli", "--shell-form")...)
			if r.code != 0 {
				t.Fatalf("--shell-form: exit %d: %s", r.code, r.stderr)
			}
		}},
		{"a hook script that can't run as a command is refused", func(t *testing.T) {
			for _, script := range []string{"dump-hook.sh", "allow-once.sh"} {
				for _, c := range []struct {
					name, want string
					spoil      func(path string) error
				}{
					{"no executable bit", "chmod +x", func(p string) error { return os.Chmod(p, 0o644) }},
					{"Windows line endings", "line endings", func(p string) error {
						b, err := os.ReadFile(p)
						if err != nil {
							return err
						}
						return os.WriteFile(p, bytes.ReplaceAll(b, []byte("\n"), []byte("\r\n")), 0o755)
					}},
				} {
					copied := copyKit(t, filepath.Join(t.TempDir(), "pairtime"))
					if err := c.spoil(filepath.Join(copied, script)); err != nil {
						t.Fatal(err)
					}
					r := run(t, ".", nil, env, need(t, "python3"), "-I", "-B", filepath.Join(copied, "prepare.py"), "claude_code",
						filepath.Join(t.TempDir(), "capture"), "cli")
					if r.code != 2 || !strings.Contains(r.stderr, c.want) || !strings.Contains(r.stderr, script) {
						t.Errorf("%s, %s: exit %d, want 2 and %q naming the script: %s", script, c.name, r.code, c.want, r.stderr)
					}
				}
			}
		}},
		{"a capture folder inside the pairtime checkout is refused", func(t *testing.T) {
			inRepo := filepath.Join(kit(t, "."), "capture-in-repo")
			r := run(t, ".", nil, env, python(t, "prepare.py", "claude_code", inRepo, "cli")...)
			if r.code != 2 {
				t.Fatalf("exit %d, want 2: %s", r.code, r.stderr)
			}
			if _, err := os.Stat(inRepo); err == nil {
				t.Fatalf("%s was created", inRepo)
			}
		}},
		{"Claude Code gets a settings folder of its own, and every printed command uses it", func(t *testing.T) {
			config := filepath.Join(root, "claude-home")
			if info, err := os.Stat(config); err != nil || !info.IsDir() {
				t.Fatalf("no settings folder %s: %v", config, err)
			}
			for _, want := range []string{"export CLAUDE_CONFIG_DIR=" + config, "export CLAUDE_CODE_DISABLE_CLAUDE_MDS=1",
				"--history " + filepath.Join(config, "history.jsonl")} {
				if !strings.Contains(r.stdout, want) {
					t.Errorf("prepare.py didn't print %q:\n%s", want, r.stdout)
				}
			}
			if strings.Contains(r.stdout, filepath.Join(home, ".claude")) {
				t.Errorf("prepare.py still points at the person's own ~/.claude:\n%s", r.stdout)
			}
			c := readCapture(t, runDir)
			if c.Harness != "claude_code" || !c.Strict || c.Folders["CLAUDE_CONFIG_DIR"] != config || c.Hooks != "exec" {
				t.Errorf("capture.json = %+v", c)
			}
		}},
		{"ROOT is readable by its owner only, since the settings folder holds a sign-in", func(t *testing.T) {
			info, err := os.Stat(root)
			if err != nil || info.Mode().Perm()&0o077 != 0 {
				t.Fatalf("ROOT mode %v: %v", info.Mode(), err)
			}
		}},
		{"a harness that doesn't read Claude Code's hook format gets no Claude hooks", func(t *testing.T) {
			r := run(t, ".", nil, env, python(t, "prepare.py", "codex", root, "codex")...)
			if r.code != 0 {
				t.Fatalf("exit %d: %s", r.code, r.stderr)
			}
			if _, err := os.Stat(filepath.Join(root, "projects", "codex", ".claude")); err == nil {
				t.Errorf("the Codex run's project has Claude Code hooks")
			}
			codexHome := filepath.Join(root, "codex-home")
			if !strings.Contains(r.stdout, "export CODEX_HOME="+codexHome) ||
				!strings.Contains(r.stdout, "--history "+filepath.Join(codexHome, "history.jsonl")) {
				t.Errorf("no CODEX_HOME or Codex history in:\n%s", r.stdout)
			}
			if c := readCapture(t, filepath.Join(root, "runs", "codex")); c.Hooks != "none" || c.Folders["CODEX_HOME"] != codexHome {
				t.Errorf("capture.json = %+v", c)
			}
		}},
		{"a ROOT that is the home folder, or holds files the kit didn't make, is refused", func(t *testing.T) {
			full := t.TempDir()
			write(t, filepath.Join(full, "notes.txt"), "mine\n")
			for _, dir := range []string{home, full} {
				r := run(t, ".", nil, env, python(t, "prepare.py", "claude_code", dir, "cli")...)
				if r.code != 2 {
					t.Errorf("%s: exit %d, want 2: %s", dir, r.code, r.stderr)
				}
			}
			if _, err := os.Stat(filepath.Join(full, "projects")); err == nil {
				t.Errorf("a scratch project was made in a refused ROOT")
			}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, c.check)
	}

	// Cursor reads your own config files, so prepare.py refuses while one it
	// would load holds something, and passes once it's moved aside.
	preflights := []struct {
		// entry is what prepare.py names, the file itself or the folder
		// holding it; "" when it shouldn't refuse.
		name, harness, file, text, entry string
	}{
		{"Cursor is refused while your own hooks.json holds hooks", "cursor", ".cursor/hooks.json",
			`{"version":1,"hooks":{"stop":[{"command":"say done"}]}}`, ".cursor/hooks.json"},
		{"Cursor is refused while your Claude Code settings hold hooks, which it imports", "cursor", ".claude/settings.json",
			`{"model":"opus","hooks":{"Stop":[{"hooks":[{"type":"command","command":"true"}]}]}}`, ".claude/settings.json"},
		{"Cursor is refused while a skills folder of yours holds a skill", "cursor", ".claude/skills/mine/SKILL.md",
			"---\nname: mine\n---\n", ".claude/skills"},
		{"a JSON file that can't be read counts as holding something", "cursor", ".cursor/mcp.json", "{not json", ".cursor/mcp.json"},
		{"Cline is refused while your global rules hold a rule", "cline", "Documents/Cline/Rules/me.md", "Be brief.\n",
			"Documents/Cline/Rules"},
		{"settings without hooks don't stop Cursor", "cursor", ".claude/settings.json", `{"model":"opus"}`, ""},
		{"an empty hooks list doesn't stop Cursor", "cursor", ".cursor/hooks.json", `{"version":1,"hooks":{}}`, ""},
	}
	for _, c := range preflights {
		t.Run(c.name, func(t *testing.T) {
			own := t.TempDir()
			write(t, filepath.Join(own, filepath.FromSlash(c.file)), c.text)
			env := []string{"HOME=" + own, "PATH=" + os.Getenv("PATH")}
			capture := filepath.Join(t.TempDir(), "capture")
			r := run(t, ".", nil, env, python(t, "prepare.py", c.harness, capture, c.harness)...)
			if c.entry == "" {
				if r.code != 0 {
					t.Fatalf("exit %d, want 0: %s", r.code, r.stderr)
				}
				return
			}
			moved := filepath.Join(own, filepath.FromSlash(c.entry))
			if r.code != 2 || !strings.Contains(r.stderr, moved) || !strings.Contains(r.stderr, "mv "+moved+" "+moved+".pairtime-aside") {
				t.Fatalf("exit %d, want 2, naming %s with the command to move it aside:\n%s", r.code, moved, r.stderr)
			}
			if _, err := os.Stat(capture); err == nil {
				t.Fatalf("a refused prepare made %s", capture)
			}
			if err := os.Rename(moved, moved+".pairtime-aside"); err != nil {
				t.Fatal(err)
			}
			r = run(t, ".", nil, env, python(t, "prepare.py", c.harness, capture, c.harness)...)
			if r.code != 0 || !strings.Contains(r.stdout, "mv "+moved+".pairtime-aside "+moved) {
				t.Fatalf("once moved aside: exit %d, want 0 and the command to move it back:\n%s%s", r.code, r.stdout, r.stderr)
			}
		})
	}
}

// TestPrepareHarnesses checks what prepare.py sets up and prints for the
// harnesses other than Claude Code.
func TestPrepareHarnesses(t *testing.T) {
	unixOnly(t)
	need(t, "git")
	cases := []struct {
		harness, section string
		// history is the history file collect.py gets, below ROOT, or "".
		history string
		// claudeHooks says whether the harness runs Claude Code-format hooks
		// in real use, so the run's project gets them (HS 4.1, 8.1). Copilot
		// reads a repo's .claude/ too (HS 5.1), but pairtime installs only
		// Copilot's own hooks for it.
		claudeHooks bool
	}{
		{"codex", "Codex", "codex-home/history.jsonl", false},
		{"cursor", "Cursor", "", true},
		{"copilot", "GitHub Copilot", "", false},
		{"gemini", "Gemini CLI", "", false},
		{"devin", "Devin", "", true},
		{"cline", "Cline", "", false},
	}
	for _, c := range cases {
		t.Run(c.harness+"'s commands name its own fixture folder, SCRIPTS.md section and history, never Claude Code's", func(t *testing.T) {
			capture := filepath.Join(t.TempDir(), "capture")
			env := []string{"HOME=" + t.TempDir(), "PATH=" + os.Getenv("PATH")}
			r := run(t, ".", nil, env, python(t, "prepare.py", c.harness, capture, "run1")...)
			if r.code != 0 {
				t.Fatalf("exit %d:\n%s%s", r.code, r.stdout, r.stderr)
			}
			fixture := filepath.Join(filepath.Dir(filepath.Dir(kit(t, "."))), "testdata", "fixtures", c.harness) + string(os.PathSeparator)
			for _, want := range []string{fixture, `Follow the "` + c.section + `" section`} {
				if !strings.Contains(r.stdout, want) {
					t.Errorf("no %q in:\n%s", want, r.stdout)
				}
			}
			if strings.Contains(r.stdout, "claude_code") || strings.Contains(r.stdout, filepath.Join(".claude", "history.jsonl")) {
				t.Errorf("Claude Code's fixture folder or history in:\n%s", r.stdout)
			}
			if c.history != "" && !strings.Contains(r.stdout, "--history "+filepath.Join(capture, filepath.FromSlash(c.history))) {
				t.Errorf("no --history %s in:\n%s", c.history, r.stdout)
			}
			if c.history == "" && strings.Contains(r.stdout, "--history") {
				t.Errorf("a --history for a harness with no history file:\n%s", r.stdout)
			}
		})
		t.Run(c.harness+"'s project has Claude Code-format hooks only if it runs them in real use", func(t *testing.T) {
			capture := filepath.Join(t.TempDir(), "capture")
			env := []string{"HOME=" + t.TempDir(), "PATH=" + os.Getenv("PATH")}
			if r := run(t, ".", nil, env, python(t, "prepare.py", c.harness, capture, "run1")...); r.code != 0 {
				t.Fatalf("exit %d:\n%s%s", r.code, r.stdout, r.stderr)
			}
			settings := filepath.Join(capture, "projects", "run1", ".claude", "settings.local.json")
			_, err := os.Stat(settings)
			if has := err == nil; has != c.claudeHooks {
				t.Fatalf("%s exists: %v, want %v", settings, has, c.claudeHooks)
			}
			if c.claudeHooks {
				if h := readSettings(t, settings)["Stop"][0].Hooks[0]; h.Args[0] != filepath.Join(capture, "runs", "run1") {
					t.Errorf("the Stop hook writes to %s", h.Args[0])
				}
			}
		})
	}
}

// captureFile is capture.json, as collect.py and the scrub read it.
type captureFile struct {
	Harness string            `json:"harness"`
	Strict  bool              `json:"strict"`
	Folders map[string]string `json:"settings_folder"`
	Hooks   string            `json:"hooks"`
	Project string            `json:"project"`
}

func readCapture(t *testing.T, runDir string) captureFile {
	t.Helper()
	var c captureFile
	if err := json.Unmarshal([]byte(read(t, filepath.Join(runDir, "capture.json"))), &c); err != nil {
		t.Fatal(err)
	}
	return c
}

// frontMatter reads the key: value lines between the --- lines that open a
// provenance file, the way tools/ci/provenance does.
func frontMatter(t *testing.T, text string) map[string]string {
	t.Helper()
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	if strings.TrimSpace(lines[0]) != "---" {
		t.Fatalf("doesn't open with ---: %q", lines[0])
	}
	fields := map[string]string{}
	for _, line := range lines[1:] {
		if strings.TrimSpace(line) == "---" {
			return fields
		}
		if strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			t.Fatalf("front matter line %q isn't key: value", line)
		}
		fields[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}
	t.Fatal("front matter has no closing ---")
	return nil
}

func TestProvenanceTemplate(t *testing.T) {
	template := filepath.Join("..", "..", "testdata", "fixtures", "PROVENANCE.template.md")
	fields := frontMatter(t, read(t, template))
	harnesses := []event.Harness{event.HarnessClaudeCode, event.HarnessCursor, event.HarnessCopilot, event.HarnessCodex,
		event.HarnessGemini, event.HarnessDevin, event.HarnessCline, event.HarnessOpenCode}

	cases := []struct {
		name  string
		check func(t *testing.T)
	}{
		{"every field docs/PLAN.md requires is set", func(t *testing.T) {
			for _, k := range []string{"harness", "harness_version", "os", "captured_on", "captured_by", "synthetic", "scrubbed_by"} {
				if fields[k] == "" {
					t.Errorf("no %s", k)
				}
			}
		}},
		{"synthetic is true or false", func(t *testing.T) {
			if v := fields["synthetic"]; v != "true" && v != "false" {
				t.Fatalf("synthetic = %q", v)
			}
		}},
		{"captured_on is a date", func(t *testing.T) {
			if _, err := time.Parse(time.DateOnly, fields["captured_on"]); err != nil {
				t.Fatal(err)
			}
		}},
		{"harness is a pkg/event harness id", func(t *testing.T) {
			for _, h := range harnesses {
				if string(h) == fields["harness"] {
					return
				}
			}
			t.Fatalf("harness = %q, want one of %v", fields["harness"], harnesses)
		}},
		{"harness_version is a bare version, usable as a directory name", func(t *testing.T) {
			if !regexp.MustCompile(`^[0-9][0-9A-Za-z.+-]*$`).MatchString(fields["harness_version"]) {
				t.Fatalf("harness_version = %q", fields["harness_version"])
			}
		}},
		{"the provenance checker accepts the template, alone and in a fixture directory", func(t *testing.T) {
			checker := filepath.Join("..", "ci", "provenance")
			if _, err := os.Stat(checker); err != nil {
				t.Skip("tools/ci/provenance (WP-CI1) isn't in this tree yet")
			}
			fixtures := t.TempDir()
			write(t, filepath.Join(fixtures, "claude_code", "2.1.328", "windows", "PROVENANCE.md"), read(t, template))
			for _, target := range []string{template, fixtures} {
				r := run(t, filepath.Join("..", ".."), nil, nil, need(t, "go"), "run", "./tools/ci/provenance", kit(t, target))
				if r.code != 0 {
					t.Errorf("provenance %s: exit %d\n%s%s", target, r.code, r.stdout, r.stderr)
				}
			}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, c.check)
	}
}
