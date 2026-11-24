// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"bytes"
	"cmp"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/agent/home"
	"github.com/some-du6e/pairtime/internal/agent/hookpath"
	"github.com/some-du6e/pairtime/internal/agent/inbox"
	"github.com/some-du6e/pairtime/internal/agent/ipc"
	"github.com/some-du6e/pairtime/internal/agent/keys"
	"github.com/some-du6e/pairtime/internal/install"
	"github.com/some-du6e/pairtime/pkg/event"
)

// loadEnv turns TestSustainedLoad on: it takes minutes, so only load.yml
// runs it. loadSecondsEnv sets the active span's length in seconds, and
// loadReportEnv names a file for the numbers, as JSON. loadMarksEnv names
// a folder the test marks the active span's last loadCountSpan in, a file
// named count-start holding its pid as the span begins and count-end as
// it ends, so load.yml counts the system calls of that span alone, with
// nothing tracing the span the CPU is measured over.
const (
	loadEnv        = "PAIRTIME_LOAD"
	loadSecondsEnv = "PAIRTIME_LOAD_SECONDS"
	loadReportEnv  = "PAIRTIME_LOAD_REPORT"
	loadMarksEnv   = "PAIRTIME_LOAD_MARKS"
	loadCountSpan  = 30 * time.Second
)

// The synthetic projects folder of a heavy Claude Code user, shaped like
// the one #393 measured (1,758 folders and 3,428 transcripts, 117 of them
// written in the last hour): projects of sessions, and in some sessions a
// folder of subagents' transcripts. Every file is older than lookback's
// window, so nothing is left to look back on, as on a machine pairtime has
// run on for a while.
const (
	loadProjects   = 60
	loadSessions   = 30 // transcripts in a project's folder
	loadWithAgents = 14 // of its sessions, those with subagents
	loadAgents     = 2  // transcripts in a folder of subagents
	loadActive     = 100
	loadKeepLines  = 6 // lines each transcript starts with
)

// The load of the active span, as #393 measured it: 1.3 hook calls a
// second, about two transcript lines for each, a tool's result saved to a
// file of its own every 2 s, a new subagent every 5 s and a new session a
// minute. Claude Code writes such files all the time, and each is a name
// the tailer doesn't know, in a folder it watches.
const (
	loadHookEvery     = time.Second * 10 / 13
	loadLineEvery     = time.Second * 10 / 26
	loadResultEvery   = 2 * time.Second
	loadSubagentEvery = 5 * time.Second
	loadSessionEvery  = time.Minute
)

// loadFixture is a Claude Code transcript and a PostToolUse call's stdin
// from the capture, which the synthetic lines and calls copy.
const loadFixture = "../../testdata/fixtures/claude_code/2.1.327/linux/cli"

// loadSpans says how long each part of the run takes: the agent taking the
// tree up, the idle span over it, and the active span, whose work is
// fixed (loadWork) and paced at the rates above.
type loadSpans struct {
	warm, idle, active time.Duration
}

// loadReport is what TestSustainedLoad measured. Counts are what the
// agent's status says it took, which a run before a fix and one after must
// agree on, since both did the same work.
type loadReport struct {
	Folders int     `json:"folders"`
	Files   int     `json:"files"`
	IdleS   float64 `json:"idle_s"`
	// ActiveS is the active span's part the CPU and writes are measured
	// over, before the last loadCountSpan, and ActiveHooks the hook calls
	// in it.
	ActiveS     float64 `json:"active_s"`
	ActiveHooks int     `json:"active_hooks"`
	Hooks       int     `json:"hooks"`
	ViaSocket   int     `json:"hooks_via_socket"`
	Lines       int     `json:"lines_appended"`
	Created     int     `json:"files_created"`

	IdleCPUPerMin   float64 `json:"idle_cpu_s_per_min"`
	ActiveCPUPerMin float64 `json:"active_cpu_s_per_min"`
	CPUPerHook      float64 `json:"cpu_s_per_hook"`
	// Written is what the process handed write(2) and the like (wchar),
	// and DiskWritten what reached the disk (write_bytes), each scaled to
	// an hour of the active span; ReadCalls is read(2) and the like
	// (syscr) a minute of it.
	WrittenPerHour      float64 `json:"wchar_per_hour"`
	DiskWrittenPerHour  float64 `json:"write_bytes_per_hour"`
	ReadCallsPerMin     float64 `json:"syscr_per_min"`
	IdleReadCallsPerMin float64 `json:"idle_syscr_per_min"`

	Calls         uint64 `json:"claude_code_calls"`
	Records       uint64 `json:"claude_code_records"`
	ParseFailures uint64 `json:"claude_code_parse_failures"`
	Ignored       uint64 `json:"claude_code_ignored"`
}

// TestSustainedLoad puts the agent under a heavy Claude Code user's load
// (#393) over a synthetic tree, and reports what that costs: CPU a minute
// idle and active, CPU a hook call, bytes written an hour, and read calls a
// minute. load.yml runs it under -cpuprofile, on this PR's code and on its
// base's with this file copied in, and holds the two runs' counts equal.
func TestSustainedLoad(t *testing.T) {
	if os.Getenv(loadEnv) == "" {
		t.Skip(loadEnv + " isn't set: load.yml runs this")
	}
	spans := loadSpans{warm: 20 * time.Second, idle: time.Minute, active: 90 * time.Second}
	if s, err := strconv.Atoi(os.Getenv(loadSecondsEnv)); err == nil && s > 0 {
		spans.active = time.Duration(s) * time.Second
	}
	if spans.active < 2*loadCountSpan {
		t.Fatalf("%s is %s; the active span needs at least %s, the last %s of it for the count", loadSecondsEnv, spans.active, 2*loadCountSpan, loadCountSpan)
	}
	homeDir := t.TempDir()
	tree := buildLoadTree(t, homeDir)
	dirs := home.Dirs{Data: t.TempDir(), Config: t.TempDir()}
	dirs.Runtime = dirs.Data
	if err := dirs.Create(); err != nil {
		t.Fatal(err)
	}
	r := startAgent(t, Options{Dirs: dirs, Ports: []int{0}, KeyStores: []keys.Store{}, Env: &install.Env{GOOS: runtime.GOOS, Home: homeDir}})
	ep, err := ipc.EndpointFor(dirs)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(spans.warm)

	u0 := readLoadUsage(t)
	time.Sleep(spans.idle)
	u1 := readLoadUsage(t)
	var u2 loadUsage
	var hooks int
	work := tree.run(t, ep, spans.active, spans.active-loadCountSpan, func(w loadWork) {
		u2, hooks = readLoadUsage(t), w.hooks
		loadMark(t, "count-start")
	})
	loadMark(t, "count-end")

	// Everything sent is taken up before the counts are read.
	var s statusReply
	deadline := time.Now().Add(time.Minute)
	for {
		s = getStatus(t, r)
		if s.Adapters.Harnesses[event.HarnessClaudeCode].Calls >= uint64(work.viaSocket) && s.Adapters.Waiting == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the agent hadn't taken the load up a minute after it ended: %+v", s.Adapters)
		}
		time.Sleep(200 * time.Millisecond)
	}
	// The tailer reads what the last appends wrote within a poll.
	time.Sleep(10 * time.Second)
	s = getStatus(t, r)
	cc := s.Adapters.Harnesses[event.HarnessClaudeCode]

	idle, active := u1.at.Sub(u0.at), u2.at.Sub(u1.at)
	rep := loadReport{
		Folders: tree.folders, Files: tree.files,
		IdleS: idle.Seconds(), ActiveS: active.Seconds(), ActiveHooks: hooks,
		Hooks: work.hooks, ViaSocket: work.viaSocket, Lines: work.lines, Created: work.created,
		IdleCPUPerMin:       (u1.cpu - u0.cpu).Seconds() / idle.Minutes(),
		ActiveCPUPerMin:     (u2.cpu - u1.cpu).Seconds() / active.Minutes(),
		CPUPerHook:          (u2.cpu - u1.cpu).Seconds() / float64(max(hooks, 1)),
		WrittenPerHour:      float64(u2.wchar-u1.wchar) / active.Hours(),
		DiskWrittenPerHour:  float64(u2.writeBytes-u1.writeBytes) / active.Hours(),
		ReadCallsPerMin:     float64(u2.syscr-u1.syscr) / active.Minutes(),
		IdleReadCallsPerMin: float64(u1.syscr-u0.syscr) / idle.Minutes(),
		Calls:               cc.Calls, Records: cc.Records, ParseFailures: cc.ParseFailures, Ignored: cc.Ignored,
	}
	out, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("load: %s", out)
	if p := os.Getenv(loadReportEnv); p != "" {
		if err := os.WriteFile(p, append(out, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cases := []struct {
		name string
		ok   bool
	}{
		{"every hook call reached the agent over the socket", work.viaSocket == work.hooks},
		{"the agent took every call the hooks sent", cc.Calls >= uint64(work.viaSocket)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if !c.ok {
				t.Errorf("load: %s", out)
			}
		})
	}
}

// loadMark writes the mark name into PAIRTIME_LOAD_MARKS's folder, when
// it is set, holding this process's pid.
func loadMark(t *testing.T, name string) {
	t.Helper()
	dir := os.Getenv(loadMarksEnv)
	if dir == "" {
		return
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(strconv.Itoa(os.Getpid())), 0o644); err != nil {
		t.Fatal(err)
	}
}

// loadUsage is the process's CPU time and I/O counters at one moment.
type loadUsage struct {
	at                       time.Time
	cpu                      time.Duration
	wchar, writeBytes, syscr int64
}

// readLoadUsage reads getrusage(2) and /proc/self/io.
func readLoadUsage(t *testing.T) loadUsage {
	t.Helper()
	var ru syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &ru); err != nil {
		t.Fatal(err)
	}
	u := loadUsage{at: time.Now(), cpu: time.Duration(ru.Utime.Nano() + ru.Stime.Nano())}
	data, err := os.ReadFile("/proc/self/io")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		k, v, ok := strings.Cut(line, ": ")
		if !ok {
			continue
		}
		n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
		if err != nil {
			continue
		}
		switch k {
		case "wchar":
			u.wchar = n
		case "write_bytes":
			u.writeBytes = n
		case "syscr":
			u.syscr = n
		}
	}
	return u
}

// loadTree is the synthetic projects folder, and what the active span
// writes to.
type loadTree struct {
	root           string
	folders, files int
	// active are the transcripts the active span appends to, and cwd the
	// folder each one's lines name.
	active []string
	cwd    map[string]string
	// lines are the capture's transcript lines, and hook its PostToolUse
	// call's stdin, which every synthetic one copies.
	lines []map[string]any
	hook  map[string]any
	n     int
	rng   *rand.Rand
}

// buildLoadTree writes the synthetic projects folder under home's .claude,
// with every file and folder dated 40 days back.
func buildLoadTree(t *testing.T, homeDir string) *loadTree {
	t.Helper()
	tr := &loadTree{root: filepath.Join(homeDir, ".claude", "projects"), cwd: make(map[string]string), rng: rand.New(rand.NewPCG(393, 1))}
	tr.loadFixture(t)
	old := time.Now().Add(-40 * 24 * time.Hour)
	var all, folders []string
	for p := range loadProjects {
		cwd := filepath.Join(homeDir, "work", fmt.Sprintf("proj-%02d", p))
		if err := os.MkdirAll(cwd, 0o755); err != nil {
			t.Fatal(err)
		}
		pdir := filepath.Join(tr.root, "-"+strings.ReplaceAll(strings.TrimPrefix(cwd, "/"), "/", "-"))
		folders = append(folders, pdir)
		for s := range loadSessions {
			sid := fmt.Sprintf("%08x-0000-4000-8000-%012x", p, s)
			file := filepath.Join(pdir, sid+".jsonl")
			tr.write(t, file, sid, cwd, loadKeepLines, old)
			all = append(all, file)
			tr.cwd[file] = cwd
			if s >= loadWithAgents {
				continue
			}
			adir := filepath.Join(pdir, sid, "subagents")
			folders = append(folders, filepath.Dir(adir), adir)
			for a := range loadAgents {
				afile := filepath.Join(adir, fmt.Sprintf("agent-%08x.jsonl", p*1000+s*10+a))
				tr.write(t, afile, sid, cwd, loadKeepLines, old)
				all = append(all, afile)
				tr.cwd[afile] = cwd
			}
		}
	}
	// Folders last, since adding a file to one dates it now.
	for i := len(folders) - 1; i >= 0; i-- {
		if err := os.Chtimes(folders[i], old, old); err != nil {
			t.Fatal(err)
		}
	}
	tr.folders, tr.files = len(folders), len(all)
	for _, i := range tr.rng.Perm(len(all))[:loadActive] {
		tr.active = append(tr.active, all[i])
	}
	return tr
}

// loadFixture reads the capture's transcript lines and PostToolUse stdin.
func (tr *loadTree) loadFixture(t *testing.T) {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(loadFixture, "transcripts/home/*/claude-home/projects/*/*.jsonl"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no transcript in %s: %v", loadFixture, err)
	}
	data, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range bytes.Split(bytes.TrimSpace(data), []byte("\n")) {
		var m map[string]any
		if err := json.Unmarshal(line, &m); err != nil {
			t.Fatalf("%s: %v", files[0], err)
		}
		tr.lines = append(tr.lines, m)
	}
	stdin, err := os.ReadFile(filepath.Join(loadFixture, "hooks/000003/stdin"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(stdin, &tr.hook); err != nil || tr.hook["hook_event_name"] != "PostToolUse" {
		t.Fatalf("hooks/000003/stdin isn't a PostToolUse call: %v", err)
	}
}

// line is the next of the capture's lines, as session sid in folder cwd
// would write it at at.
func (tr *loadTree) line(sid, cwd string, at time.Time) []byte {
	tr.n++
	m := make(map[string]any, len(tr.lines[0]))
	for k, v := range tr.lines[tr.n%len(tr.lines)] {
		m[k] = v
	}
	m["sessionId"] = sid
	if _, ok := m["uuid"]; ok {
		m["uuid"] = fmt.Sprintf("00000000-0000-4000-9000-%012x", tr.n)
	}
	if _, ok := m["cwd"]; ok {
		m["cwd"] = cwd
	}
	if _, ok := m["timestamp"]; ok {
		m["timestamp"] = at.UTC().Format("2006-01-02T15:04:05.000Z")
	}
	b, err := json.Marshal(m)
	if err != nil {
		panic(err)
	}
	return append(b, '\n')
}

// write makes file hold n of the capture's lines, dated at.
func (tr *loadTree) write(t *testing.T, file, sid, cwd string, n int, at time.Time) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	var b []byte
	for range n {
		b = append(b, tr.line(sid, cwd, at)...)
	}
	if err := os.WriteFile(file, b, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(file, at, at); err != nil {
		t.Fatal(err)
	}
}

// loadWork counts what the active span did.
type loadWork struct {
	hooks, viaSocket, lines, created int
}

// loadSession returns the project folder and session of transcript file: a
// session's own, <project>/<session>.jsonl, or one of its subagents',
// <project>/<session>/subagents/<agent>.jsonl.
func loadSession(file string) (project, sid string) {
	dir := filepath.Dir(file)
	if filepath.Base(dir) == "subagents" {
		return filepath.Dir(filepath.Dir(dir)), filepath.Base(filepath.Dir(dir))
	}
	return dir, strings.TrimSuffix(filepath.Base(file), ".jsonl")
}

// run does the active span's fixed work, paced over span: the hook calls,
// the lines they go with, the tools' result files, the subagents and the
// sessions, in an order the seed fixes, so a run before a fix and one
// after do the same. It calls atMid, with the work so far, before the
// first step at or after mid.
func (tr *loadTree) run(t *testing.T, ep ipc.Endpoint, span, mid time.Duration, atMid func(loadWork)) loadWork {
	t.Helper()
	const (
		hookStep = iota
		lineStep
		resultStep
		subagentStep
		sessionStep
	)
	type step struct {
		at   time.Duration
		kind int
	}
	var steps []step
	for _, k := range []struct {
		kind  int
		every time.Duration
	}{{hookStep, loadHookEvery}, {lineStep, loadLineEvery}, {resultStep, loadResultEvery}, {subagentStep, loadSubagentEvery}, {sessionStep, loadSessionEvery}} {
		for at := k.every; at <= span; at += k.every {
			steps = append(steps, step{at, k.kind})
		}
	}
	slices.SortFunc(steps, func(a, b step) int { return cmp.Or(cmp.Compare(a.at, b.at), cmp.Compare(a.kind, b.kind)) })
	var w loadWork
	start := time.Now()
	for _, s := range steps {
		if d := time.Until(start.Add(s.at)); d > 0 {
			time.Sleep(d)
		}
		if atMid != nil && s.at >= mid {
			atMid(w)
			atMid = nil
		}
		file := tr.active[tr.rng.IntN(len(tr.active))]
		project, sid := loadSession(file)
		cwd := tr.cwd[file]
		switch s.kind {
		case hookStep:
			w.hooks++
			body := make(map[string]any, len(tr.hook))
			for k, v := range tr.hook {
				body[k] = v
			}
			body["session_id"], body["transcript_path"], body["cwd"] = sid, file, cwd
			stdin, err := json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
			res := hookpath.Send(hookpath.Call{Harness: "claude_code", Event: "PostToolUse", Stdin: bytes.NewReader(stdin)},
				hookpath.Options{Endpoint: ep, Timeout: 5 * time.Second})
			if res.Via == inbox.ViaSocket && res.Err == nil {
				w.viaSocket++
			}
		case lineStep:
			w.lines++
			f, err := os.OpenFile(file, os.O_WRONLY|os.O_APPEND, 0)
			if err != nil {
				t.Fatal(err)
			}
			_, err = f.Write(tr.line(sid, cwd, time.Now()))
			if cerr := f.Close(); err == nil {
				err = cerr
			}
			if err != nil {
				t.Fatal(err)
			}
		case resultStep:
			w.created++
			dir := filepath.Join(project, sid, "tool-results")
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("toolu_%08d.txt", w.created)), []byte("a tool's output\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		case subagentStep, sessionStep:
			w.created++
			next := filepath.Join(project, sid, "subagents", fmt.Sprintf("agent-new%05d.jsonl", w.created))
			if s.kind == sessionStep {
				sid = fmt.Sprintf("ffffffff-0000-4000-8000-%012x", w.created)
				next = filepath.Join(project, sid+".jsonl")
			}
			tr.write(t, next, sid, cwd, 2, time.Now())
			tr.cwd[next] = cwd
			tr.active = append(tr.active, next)
		}
	}
	return w
}
