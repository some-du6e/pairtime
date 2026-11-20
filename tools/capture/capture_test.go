// SPDX-License-Identifier: AGPL-3.0-or-later

package capture

import (
	"bufio"
	"bytes"
	"compress/gzip"
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
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = dir
	cmd.Stdin = bytes.NewReader(stdin)
	cmd.Env = append(os.Environ(), env...)
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
)

// A payload with the shapes that break naive copies: non-ASCII text, CRLF,
// and no newline at the end.
var payload = []byte("{\"session_id\":\"3f9c\",\"hook_event_name\":\"UserPromptSubmit\",\"prompt\":\"caf\u00e9 \u2603\"}\r\n{\"tail\":true}")

// hostile is a hook payload whose strings hold command substitutions, as an
// agent's reply quoting a command can. A shell that reads it as its script
// runs both and creates the two files.
var hostile = []byte("{\"session_id\":\"x\",\"last_assistant_message\":\"I ran \x60touch backtick-ran\x60 and $(touch dollar-ran)\"}")

func TestDumpers(t *testing.T) {
	unixOnly(t)
	for _, d := range dumpers {
		t.Run(d.name, func(t *testing.T) {
			base := d.argv(t)
			// call runs one hook call in a fresh run directory and returns the
			// run directory and what the dumper printed.
			call := func(t *testing.T, stdin []byte, args ...string) (string, result) {
				t.Helper()
				runDir := filepath.Join(t.TempDir(), "run")
				argv := append(append([]string{}, base...), append([]string{runDir, "UserPromptSubmit"}, args...)...)
				return runDir, run(t, t.TempDir(), stdin, []string{secretName + "=" + secretValue}, argv...)
			}
			first := func(runDir string) string { return filepath.Join(runDir, "hooks", "000001") }

			cases := []struct {
				name  string
				check func(t *testing.T)
			}{
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
				{"concurrent calls each get their own directory", func(t *testing.T) {
					runDir := filepath.Join(t.TempDir(), "run")
					var wg sync.WaitGroup
					for i := 0; i < 8; i++ {
						wg.Add(1)
						go func(i int) {
							defer wg.Done()
							cmd := exec.Command(base[0], append(append([]string{}, base[1:]...), runDir, "PostToolUse")...)
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
	history := filepath.Join(home, ".claude", "history.jsonl")
	captured := jsonText(t, map[string]any{"display": "a captured prompt", "project": project}) + "\n"
	elsewhere := jsonText(t, map[string]any{"display": "a prompt from another project", "project": filepath.Join(home, "school")}) + "\n"
	bySession := jsonText(t, map[string]any{"display": "a prompt by session id", "sessionId": "sess-1"}) + "\n"
	write(t, history, captured+elsewhere+bySession)

	runDir := filepath.Join(t.TempDir(), "run")
	writeCapture(t, runDir, "claude_code", true, map[string]string{"CLAUDE_CONFIG_DIR": config})
	write(t, filepath.Join(runDir, "hooks", "000001", "stdin"),
		jsonText(t, map[string]any{"session_id": "sess-1", "transcript_path": transcript, "cwd": project}))
	write(t, filepath.Join(runDir, "hooks", "000002", "stdin"),
		jsonText(t, map[string]any{"session_id": "sess-1", "transcript_path": filepath.Join(projects, "gone.jsonl"), "cwd": project}))
	write(t, filepath.Join(runDir, "hooks", "000003", "stdin"), "not json")

	r := run(t, ".", nil, []string{"HOME=" + home}, python(t, "collect.py", runDir, "--history", history)...)
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
		{"history keeps only lines from the captured sessions and folders", func(t *testing.T) {
			if h := got["home/.claude/history.jsonl"]; h != captured+bySession {
				t.Fatalf("history = %q, want %q", h, captured+bySession)
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
{"keys":"ghp_AbCdEfGhIjKlMnOpQrStUvWxYz0123456789 AKIAABCDEFGHIJKLMNOP eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N_XgL0n3I9PlFUP0THsR8U","trailer":"Co-Authored-By: Claude <noreply@anthropic.com>"}
`,
	"otlp/000001.json": `{"resourceLogs":[{"resource":{"attributes":[{"key":"user.account_uuid","value":{"stringValue":"9b1e7f9e-1111-4222-8333-444455556666"}},{"key":"organization.id","value":{"stringValue":"77aa88bb-1111-4222-8333-444455556666"}},{"key":"user.email","value":{"stringValue":"rowan.quinn@westfield.edu"}},{"key":"session.id","value":{"stringValue":"` + plantedSession + `"}}]}}]}`,
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
			for _, v := range []string{"rowan", "westfield", "sk-ant-api03", "ghp_", "akiaabcd", "eyjhbgci", "9b1e7f9e", "77aa88bb"} {
				if strings.Contains(everything, v) {
					t.Errorf("%q survived the scrub", v)
				}
			}
		}},
		{"a home path is replaced in every spelling", func(t *testing.T) {
			for _, want := range []string{`/home/capture/pairtime-capture/project`, `C:\\Users\\capture\\pairtime-capture`,
				`C%3A/Users/capture/pairtime-capture`, `C:\\\\Users\\\\capture\\\\x`, `-home-capture-pairtime-capture-project`} {
				if !strings.Contains(all.String(), want) {
					t.Errorf("no %s in the output", want)
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
		{"ids that aren't secrets and public tool addresses are kept", func(t *testing.T) {
			for _, want := range []string{plantedSession, plantedTool, "noreply@anthropic.com"} {
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
		{"folder names are scrubbed like file contents", func(t *testing.T) {
			want := "transcripts/home/.claude/projects/-home-capture-pairtime-capture-project/" + plantedSession + ".jsonl"
			if _, ok := out[want]; !ok {
				t.Fatalf("no %s; got %v", want, keys(out))
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
		{"a known value inside a longer word is reported and fails the run", func(t *testing.T) {
			dir := t.TempDir()
			write(t, filepath.Join(dir, "run", "notes.txt"), "rowanqsLaptop\n")
			r := run(t, ".", nil, quiet, python(t, "scrub.py", "--no-detect", "--user", "rowanq", filepath.Join(dir, "run"), filepath.Join(dir, "out"))...)
			if r.code != 1 || !strings.Contains(r.stdout, "STILL PRESENT") || !strings.Contains(r.stdout, "notes.txt:1") {
				t.Fatalf("exit %d:\n%s", r.code, r.stdout)
			}
		}},
		{"an output folder inside the run is refused", func(t *testing.T) {
			r := run(t, ".", nil, quiet, python(t, "scrub.py", "--no-detect", runDir, filepath.Join(runDir, "out"))...)
			if r.code != 2 {
				t.Fatalf("exit %d, want 2:\n%s", r.code, r.stderr)
			}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, c.check)
	}

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
		})
	}
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
	project := filepath.Join(root, "project")
	runDir := filepath.Join(root, "runs", "cli")
	settings := filepath.Join(project, ".claude", "settings.local.json")
	hooks := readSettings(t, settings)

	cases := []struct {
		name  string
		check func(t *testing.T)
	}{
		{"every DESIGN 5.1 hook runs the dumper itself in exec form, with the run and its label as args", func(t *testing.T) {
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
				want := []string{runDir, name}
				if h.Type != "command" || h.Command != kit(t, "dump-hook.sh") || strings.Join(h.Args, "\n") != strings.Join(want, "\n") {
					t.Errorf("%s: %+v, want %s %q", name, h, kit(t, "dump-hook.sh"), want)
				}
				if entries[0].Matcher != matcher {
					t.Errorf("%s matcher = %q, want %q", name, entries[0].Matcher, matcher)
				}
			}
			if h := hooks["SessionEnd"][0].Hooks[0]; h.Timeout < 5 {
				t.Errorf("SessionEnd timeout = %d, want room beyond the 1.5 s budget", h.Timeout)
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
		{"a new run keeps the old settings file as a backup", func(t *testing.T) {
			r := run(t, ".", nil, env, python(t, "prepare.py", "claude_code", root, "vscode")...)
			if r.code != 0 {
				t.Fatalf("exit %d: %s", r.code, r.stderr)
			}
			matches, _ := filepath.Glob(settings + ".bak-*")
			if len(matches) != 1 {
				t.Fatalf("backups: %v", matches)
			}
			if h := readSettings(t, settings)["Stop"][0].Hooks[0]; h.Args[0] != filepath.Join(root, "runs", "vscode") {
				t.Fatalf("Stop hook writes to %s", h.Args[0])
			}
		}},
		{"--shell-form writes one command string that works in a shell", func(t *testing.T) {
			r := run(t, ".", nil, env, python(t, "prepare.py", "claude_code", root, "shell", "--shell-form")...)
			if r.code != 0 {
				t.Fatalf("exit %d: %s", r.code, r.stderr)
			}
			h := readSettings(t, settings)["Stop"][0].Hooks[0]
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
		{"a dumper that can't run as a command is refused", func(t *testing.T) {
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
				if err := c.spoil(filepath.Join(copied, "dump-hook.sh")); err != nil {
					t.Fatal(err)
				}
				r := run(t, ".", nil, env, need(t, "python3"), "-I", "-B", filepath.Join(copied, "prepare.py"), "claude_code",
					filepath.Join(t.TempDir(), "capture"), "cli")
				if r.code != 2 || !strings.Contains(r.stderr, c.want) {
					t.Errorf("%s: exit %d, want 2 and %q: %s", c.name, r.code, c.want, r.stderr)
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
			if _, err := os.Stat(settings); err == nil {
				t.Errorf("%s is still there for a Codex run", settings)
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
			if _, err := os.Stat(filepath.Join(full, "project")); err == nil {
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
