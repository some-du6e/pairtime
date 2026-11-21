// SPDX-License-Identifier: AGPL-3.0-or-later

package main

// Tests for `pairtime doctor`. Each runs doctor against a fake agent on a
// PAIRTIME_HOME of its own, which answers ipc.AskStatus with a status the
// test makes up, as the agent answers it with GET /v1/status's reply.

import (
	"encoding/json"
	"errors"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/agent/doctor"
	"github.com/some-du6e/pairtime/internal/agent/home"
	"github.com/some-du6e/pairtime/internal/agent/ipc"
	"github.com/some-du6e/pairtime/internal/agent/keys"
	"github.com/some-du6e/pairtime/internal/jsonschema"
	"github.com/some-du6e/pairtime/internal/version"
)

// swap sets *p to v for the rest of the test.
func swap[T any](t *testing.T, p *T, v T) {
	t.Helper()
	old := *p
	*p = v
	t.Cleanup(func() { *p = old })
}

// newDoctorHome is newHome on a computer where doctor finds no harness, so
// the tests of the agent's lines never read the settings or run the hooks
// of the machine they run on.
func newDoctorHome(t *testing.T) fakeHome {
	t.Helper()
	swap(t, &doctorHooks, func() []doctor.Hooks { return nil })
	swap(t, &doctorRoundTrips, func(string, string, int) ([]time.Duration, error) {
		t.Error("doctor timed a hook with none installed")
		return nil, errors.New("no hook")
	})
	swap(t, &doctorStartup, func(string, int) ([]time.Duration, error) { return []time.Duration{2 * time.Millisecond}, nil })
	return newHome(t)
}

// runningStatus is a running agent's status for the tests to change.
func runningStatus() doctor.Status {
	return doctor.Status{
		Version: doctor.Version{Version: version.Get().Version, GoVersion: "go1.26.0", Platform: "linux/amd64"},
		PID:     4242, Started: time.Date(2026, 11, 24, 9, 0, 0, 0, time.UTC), UptimeS: 3*3600 + 12*60,
		Mode: "local", Wakeups: 190,
		IPC: doctor.IPC{Via: "socket", Stored: 412},
		Signals: doctor.Signals{
			Idle:  doctor.Signal{Via: "MIT-SCREEN-SAVER"},
			Focus: doctor.Signal{Via: "_NET_ACTIVE_WINDOW, with each window's process from X-Resource"},
		},
		Adapters: doctor.Adapters{Harnesses: map[string]doctor.HarnessCounts{
			"claude_code": {Calls: 412, Records: 3201},
		}},
	}
}

// statusAgent starts a fake agent that answers the status question with
// st, and no other question.
func (h fakeHome) statusAgent(st doctor.Status) func() []string {
	h.t.Helper()
	b, err := json.Marshal(st)
	if err != nil {
		h.t.Fatal(err)
	}
	return h.agent(func(q string) (json.RawMessage, error) {
		if q != ipc.AskStatus {
			return nil, ipc.ErrUnknownQuestion
		}
		return b, nil
	})
}

// doctorChecks runs doctor --json and returns its checks by name.
func doctorChecks(t *testing.T) (map[string]doctor.Check, int) {
	t.Helper()
	code, stdout, stderr := runCmd("doctor", "--json")
	if stderr != "" {
		t.Errorf("doctor --json wrote %q on stderr", stderr)
	}
	var out doctorJSON
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		t.Fatalf("doctor --json printed what isn't its JSON: %v\n%s", err, stdout)
	}
	m := map[string]doctor.Check{}
	for _, c := range out.Checks {
		if _, dup := m[c.Name]; dup {
			t.Errorf("doctor has two %q checks", c.Name)
		}
		m[c.Name] = c
	}
	return m, code
}

func TestDoctorSignals(t *testing.T) {
	tests := []struct {
		name         string
		idle, focus  doctor.Signal
		wantIdle     doctor.Result
		wantFocus    doctor.Result
		wantText     []string
		wantNotInOut []string
	}{
		{
			name:      "on GNOME on Wayland, idle time from Mutter is idle detection working and focus is unknown on this desktop, never presence unknown (D-design-5.9)",
			idle:      doctor.Signal{Via: "org.gnome.Mutter.IdleMonitor"},
			focus:     doctor.Signal{Unknown: "Wayland gives other programs no focus signal"},
			wantIdle:  doctor.OK,
			wantFocus: doctor.Warn,
			wantText: []string{"idle detection working, from org.gnome.Mutter.IdleMonitor",
				"focus unknown on this desktop: Wayland gives other programs no focus signal"},
			wantNotInOut: []string{"presence unknown on this desktop", "level C"},
		},
		{
			name:         "on X11 both signals work, each with its source",
			idle:         doctor.Signal{Via: "MIT-SCREEN-SAVER"},
			focus:        doctor.Signal{Via: "_NET_ACTIVE_WINDOW, with each window's process from X-Resource"},
			wantIdle:     doctor.OK,
			wantFocus:    doctor.OK,
			wantText:     []string{"idle detection working, from MIT-SCREEN-SAVER", "focus detection working, from _NET_ACTIVE_WINDOW"},
			wantNotInOut: []string{"unknown on this desktop"},
		},
		{
			name:         "with no idle source, presence is unknown on this desktop, with the agent's reason",
			idle:         doctor.Signal{Unknown: "no X server and no Mutter on the session bus"},
			focus:        doctor.Signal{Unknown: "no X server"},
			wantIdle:     doctor.Warn,
			wantFocus:    doctor.Warn,
			wantText:     []string{"presence unknown on this desktop: no X server and no Mutter on the session bus", "focus unknown on this desktop: no X server"},
			wantNotInOut: []string{"idle detection working", "focus detection working"},
		},
		{
			name:         "a signal the agent gives no source or reason for is unknown, never working",
			wantIdle:     doctor.Warn,
			wantFocus:    doctor.Warn,
			wantText:     []string{"presence unknown on this desktop: the agent doesn't say why", "focus unknown on this desktop: the agent doesn't say why"},
			wantNotInOut: []string{"working"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newDoctorHome(t)
			st := runningStatus()
			st.Signals.Idle, st.Signals.Focus = tt.idle, tt.focus
			h.statusAgent(st)
			checks, _ := doctorChecks(t)
			if c := checks["idle"]; c.Result != tt.wantIdle {
				t.Errorf("idle is %s %q, want %s", c.Result, c.Text, tt.wantIdle)
			}
			if c := checks["focus"]; c.Result != tt.wantFocus {
				t.Errorf("focus is %s %q, want %s", c.Result, c.Text, tt.wantFocus)
			}
			_, text, _ := runCmd("doctor")
			for _, want := range tt.wantText {
				if !strings.Contains(text, want) {
					t.Errorf("doctor doesn't say %q:\n%s", want, text)
				}
			}
			for _, not := range tt.wantNotInOut {
				if strings.Contains(text, not) {
					t.Errorf("doctor says %q:\n%s", not, text)
				}
			}
		})
	}
}

func TestDoctorReadsTheAgentNotTheTerminal(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
	}{
		{"a terminal with an X display still prints the agent's unknown focus", map[string]string{"DISPLAY": ":0", "XDG_SESSION_TYPE": "x11"}},
		{"a terminal with no desktop at all still prints the agent's working idle detection", map[string]string{"DISPLAY": "", "WAYLAND_DISPLAY": "", "XDG_SESSION_TYPE": "tty", "DBUS_SESSION_BUS_ADDRESS": ""}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			h := newDoctorHome(t)
			st := runningStatus()
			st.Signals.Idle = doctor.Signal{Via: "org.gnome.Mutter.IdleMonitor"}
			st.Signals.Focus = doctor.Signal{Unknown: "Wayland gives other programs no focus signal"}
			asked := h.statusAgent(st)
			checks, _ := doctorChecks(t)
			if checks["idle"].Result != doctor.OK || checks["focus"].Result != doctor.Warn || !strings.Contains(checks["focus"].Text, "focus unknown on this desktop") {
				t.Errorf("idle %+v, focus %+v; want them as the agent's status has them", checks["idle"], checks["focus"])
			}
			if got := asked(); !slices.Contains(got, ipc.AskStatus) {
				t.Errorf("doctor asked the agent %q, never %q", got, ipc.AskStatus)
			}
		})
	}
}

func TestDoctorAgentLine(t *testing.T) {
	tests := []struct {
		name      string
		agent     func(h fakeHome)
		wantCode  int
		want      doctor.Result
		wantText  string
		wantSkips []string
	}{
		{
			name:     "a running agent is ok, with its version, how long it has run and what its mode uploads",
			agent:    func(h fakeHome) { h.statusAgent(runningStatus()) },
			want:     doctor.OK,
			wantText: "running pairtime " + version.Get().Version + " for 3h 12m, in local mode, which uploads nothing",
		},
		{
			name: "an agent of another version is a warning that says how to move it to this one",
			agent: func(h fakeHome) {
				st := runningStatus()
				st.Version.Version = "v0.0.9"
				h.statusAgent(st)
			},
			want:     doctor.Warn,
			wantText: "run pairtime install with this one to move the agent to it",
		},
		{
			name:      "no agent fails, says how to start one, and skips the lines only the agent can answer",
			agent:     func(fakeHome) {},
			wantCode:  1,
			want:      doctor.Fail,
			wantText:  "isn't running, so pairtime tracks nothing. Logging in starts it, or run: pairtime agent --detach",
			wantSkips: []string{"idle", "focus"},
		},
		{
			name:      "an agent from before doctor is a warning, and the lines it can't answer are skipped",
			agent:     func(h fakeHome) { h.agent(nil) },
			want:      doctor.Warn,
			wantText:  "runs an older pairtime that can't answer doctor",
			wantSkips: []string{"idle", "focus"},
		},
		{
			name: "an agent that fails to answer is a failure with what it said",
			agent: func(h fakeHome) {
				h.agent(func(string) (json.RawMessage, error) { return nil, errors.New("the ledger is shut") })
			},
			wantCode:  1,
			want:      doctor.Fail,
			wantText:  "couldn't be asked",
			wantSkips: []string{"idle", "focus"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newDoctorHome(t)
			tt.agent(h)
			checks, code := doctorChecks(t)
			c := checks["agent"]
			if c.Result != tt.want || !strings.Contains(c.Text, tt.wantText) || code != tt.wantCode {
				t.Errorf("agent line %s %q, exit %d; want %s with %q, exit %d", c.Result, c.Text, code, tt.want, tt.wantText, tt.wantCode)
			}
			for _, name := range tt.wantSkips {
				if checks[name].Result != doctor.Skip {
					t.Errorf("%s is %+v, want it skipped", name, checks[name])
				}
			}
		})
	}
}

func TestDoctorSlowAgent(t *testing.T) {
	old := doctorWait
	doctorWait = 200 * time.Millisecond
	t.Cleanup(func() { doctorWait = old })
	h := newDoctorHome(t)
	release := make(chan struct{})
	h.agent(func(string) (json.RawMessage, error) {
		<-release
		return nil, ipc.ErrUnknownQuestion
	})
	// Registered after the agent's, so it runs first and the agent can
	// stop.
	t.Cleanup(func() { close(release) })
	checks, code := doctorChecks(t)
	if c := checks["agent"]; c.Result != doctor.Fail || !strings.Contains(c.Text, "didn't answer within 0.2 s") || code != 1 {
		t.Errorf("a stuck agent gives %+v and exit %d, want a failure that says it didn't answer within 0.2 s, and exit 1", c, code)
	}
}

func TestDoctorAdaptersAndProblems(t *testing.T) {
	tests := []struct {
		name    string
		edit    func(*doctor.Status)
		check   string
		want    doctor.Result
		wantHas []string
	}{
		{
			name:    "an adapter with no parse failures is ok, with its counts",
			edit:    func(*doctor.Status) {},
			check:   "adapter.claude_code",
			want:    doctor.OK,
			wantHas: []string{"412 calls made 3201 records", "no parse failures"},
		},
		{
			name: "parse failures are a warning, with the count, the last failure and where to report it",
			edit: func(s *doctor.Status) {
				s.Adapters.Harnesses["claude_code"] = doctor.HarnessCounts{Calls: 10, Records: 30, ParseFailures: 2, LastFailure: "UserPromptSubmit: unknown field prompt_v2"}
			},
			check:   "adapter.claude_code",
			want:    doctor.Warn,
			wantHas: []string{"2 calls didn't fully parse", "UserPromptSubmit: unknown field prompt_v2", "github.com/some-du6e/pairtime/issues"},
		},
		{
			name:    "an adapter this pairtime has but no call reached yet is found, with none",
			edit:    func(s *doctor.Status) { s.Adapters.Harnesses = map[string]doctor.HarnessCounts{} },
			check:   "adapter.claude_code",
			want:    doctor.OK,
			wantHas: []string{"found; no hook call has come in since the agent started"},
		},
		{
			name:    "calls of harnesses with no adapter are a warning that they count for nothing",
			edit:    func(s *doctor.Status) { s.Adapters.Other = doctor.HarnessCounts{Calls: 3} },
			check:   "adapter.other",
			want:    doctor.Warn,
			wantHas: []string{"3 hook calls came from harnesses pairtime has no adapter for"},
		},
		{
			name: "each problem the agent reports is a line of its own, in the agent's words",
			edit: func(s *doctor.Status) {
				s.Problems = map[string]string{"uploader": "uploads stopped at batch 3: sign in again with pairtime login"}
			},
			check:   "problem.uploader",
			want:    doctor.Warn,
			wantHas: []string{"uploads stopped at batch 3: sign in again with pairtime login"},
		},
		{
			name: "a problem's control characters and line breaks never reach the terminal",
			edit: func(s *doctor.Status) {
				s.Problems = map[string]string{"config": "line one\nline two \x1b]0;title\x07 done"}
			},
			check:   "problem.config",
			want:    doctor.Warn,
			wantHas: []string{"line one", "line two", "done"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newDoctorHome(t)
			st := runningStatus()
			tt.edit(&st)
			h.statusAgent(st)
			checks, _ := doctorChecks(t)
			c, ok := checks[tt.check]
			if !ok {
				t.Fatalf("doctor has no %s check: %+v", tt.check, checks)
			}
			if c.Result != tt.want {
				t.Errorf("%s is %s %q, want %s", tt.check, c.Result, c.Text, tt.want)
			}
			for _, want := range tt.wantHas {
				if !strings.Contains(c.Text, want) {
					t.Errorf("%s says %q, want it to say %q", tt.check, c.Text, want)
				}
			}
			_, text, _ := runCmd("doctor")
			if bad := terminalUnsafe(text); len(bad) > 0 {
				t.Errorf("doctor printed control characters %q:\n%s", bad, text)
			}
		})
	}
}

func loadDoctorSchema(t *testing.T) *jsonschema.Schema {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "docs", "schema", "cli-doctor.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	s, err := jsonschema.Parse(b)
	if err != nil {
		t.Fatalf("cli-doctor.schema.json: %v", err)
	}
	return s
}

func TestDoctorJSONFitsItsSchema(t *testing.T) {
	schema := loadDoctorSchema(t)
	tests := []struct {
		name  string
		agent func(h fakeHome)
	}{
		{"with a running agent", func(h fakeHome) { h.statusAgent(runningStatus()) }},
		{"with no agent", func(fakeHome) {}},
		{"with an agent from before doctor", func(h fakeHome) { h.agent(nil) }},
		{"with problems, parse failures, unknown signals and the macOS loop off the main thread", func(h fakeHome) {
			st := runningStatus()
			st.Signals.Idle = doctor.Signal{Unknown: "no idle source"}
			st.Signals.Focus = doctor.Signal{Unknown: "no focus source"}
			st.Signals.Loop = &doctor.Loop{MainThread: false, Dropped: 3}
			st.Adapters.Harnesses["claude_code"] = doctor.HarnessCounts{Calls: 3, ParseFailures: 1, WriteFailures: 1, LastFailure: "Stop: no session_id"}
			st.Adapters.Other = doctor.HarnessCounts{Calls: 2}
			st.Problems = map[string]string{"ledger": "the ledger can't open", "uploader": "uploads stopped"}
			h.statusAgent(st)
		}},
	}
	for _, tt := range tests {
		t.Run("doctor --json fits cli-doctor.schema.json "+tt.name, func(t *testing.T) {
			h := newDoctorHome(t)
			tt.agent(h)
			_, stdout, _ := runCmd("doctor", "--json")
			errs, err := schema.ValidateJSON([]byte(stdout))
			if err != nil {
				t.Fatalf("doctor --json printed what isn't JSON: %v\n%s", err, stdout)
			}
			for _, e := range errs {
				t.Errorf("doctor --json breaks cli-doctor.schema.json: %s", e)
			}
		})
	}
}

func TestDoctorSchemaRejects(t *testing.T) {
	schema := loadDoctorSchema(t)
	good := `{"v":1,"checks":[{"name":"agent","result":"ok","text":"running"}],"summary":{"ok":1,"warn":0,"fail":0,"skip":0}}`
	if errs, err := schema.ValidateJSON([]byte(good)); err != nil || len(errs) > 0 {
		t.Fatalf("the schema refuses a good doctor --json: %v %v", err, errs)
	}
	tests := []struct {
		name, doc string
	}{
		{"a result that isn't ok, warn, fail or skip", `{"v":1,"checks":[{"name":"agent","result":"FAIL","text":"x"}],"summary":{"ok":0,"warn":0,"fail":1,"skip":0}}`},
		{"a check with no text", `{"v":1,"checks":[{"name":"agent","result":"ok"}],"summary":{"ok":1,"warn":0,"fail":0,"skip":0}}`},
		{"a field the schema doesn't name", `{"v":1,"checks":[],"summary":{"ok":0,"warn":0,"fail":0,"skip":0},"extra":1}`},
		{"a version other than 1", `{"v":2,"checks":[],"summary":{"ok":0,"warn":0,"fail":0,"skip":0}}`},
		{"a check name with a space", `{"v":1,"checks":[{"name":"the agent","result":"ok","text":"x"}],"summary":{"ok":1,"warn":0,"fail":0,"skip":0}}`},
	}
	for _, tt := range tests {
		t.Run("the schema refuses "+tt.name, func(t *testing.T) {
			errs, err := schema.ValidateJSON([]byte(tt.doc))
			if err == nil && len(errs) == 0 {
				t.Errorf("the schema took %s", tt.doc)
			}
		})
	}
}

// TestDoctorSchemaMatchesGo fails when cli-doctor.schema.json and the Go
// types doctor --json prints drift apart: a property on one side only, or
// a required list that isn't the fields Go always writes.
func TestDoctorSchemaMatchesGo(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "docs", "schema", "cli-doctor.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err := json.Unmarshal(b, &root); err != nil {
		t.Fatal(err)
	}
	defs := root["$defs"].(map[string]any)
	objects := []struct {
		def string
		typ reflect.Type
	}{
		{"", reflect.TypeFor[doctorJSON]()},
		{"check", reflect.TypeFor[doctor.Check]()},
		{"summary", reflect.TypeFor[doctor.Summary]()},
	}
	for _, o := range objects {
		t.Run("the schema's "+cmpName(o.def)+" has the fields of "+o.typ.Name(), func(t *testing.T) {
			s := root
			if o.def != "" {
				s = defs[o.def].(map[string]any)
			}
			var props, required []string
			for p := range s["properties"].(map[string]any) {
				props = append(props, p)
			}
			for _, r := range s["required"].([]any) {
				required = append(required, r.(string))
			}
			var fields, always []string
			for i := range o.typ.NumField() {
				name, opts, _ := strings.Cut(o.typ.Field(i).Tag.Get("json"), ",")
				fields = append(fields, name)
				if opts != "omitempty" {
					always = append(always, name)
				}
			}
			for _, l := range [][]string{props, required, fields, always} {
				slices.Sort(l)
			}
			if !slices.Equal(props, fields) {
				t.Errorf("properties %q, Go fields %q", props, fields)
			}
			if !slices.Equal(required, always) {
				t.Errorf("required %q, Go always writes %q", required, always)
			}
		})
	}
	t.Run("the schema's results are the ones doctor gives, in order", func(t *testing.T) {
		var got []string
		for _, r := range defs["check"].(map[string]any)["properties"].(map[string]any)["result"].(map[string]any)["enum"].([]any) {
			got = append(got, r.(string))
		}
		want := []string{string(doctor.OK), string(doctor.Warn), string(doctor.Fail), string(doctor.Skip)}
		if !slices.Equal(got, want) {
			t.Errorf("the schema's results are %q, want %q", got, want)
		}
	})
}

func TestDoctorUsage(t *testing.T) {
	newHome(t)
	tests := []struct {
		name       string
		args       []string
		wantCode   int
		wantStderr string
	}{
		{"an argument is a usage error", []string{"doctor", "now"}, 2, "takes no arguments"},
		{"--json and --report together are a usage error", []string{"doctor", "--json", "--report"}, 2, "don't go together"},
		{"an unknown flag is a usage error", []string{"doctor", "--fix"}, 2, "Usage: pairtime doctor"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, _, stderr := runCmd(tt.args...)
			if code != tt.wantCode || !strings.Contains(stderr, tt.wantStderr) {
				t.Errorf("%q = %d, stderr %q; want %d with %q", tt.args, code, stderr, tt.wantCode, tt.wantStderr)
			}
		})
	}
}

// statusSequence starts a fake agent that answers the n-th status question
// with sts[n], and the last of them after that.
func (h fakeHome) statusSequence(sts ...doctor.Status) func() []string {
	h.t.Helper()
	var mu sync.Mutex
	n := 0
	return h.agent(func(q string) (json.RawMessage, error) {
		if q != ipc.AskStatus {
			return nil, ipc.ErrUnknownQuestion
		}
		mu.Lock()
		st := sts[min(n, len(sts)-1)]
		n++
		mu.Unlock()
		return json.Marshal(st)
	})
}

// reportMachine is a computer with Claude Code's hooks, which run exe,
// whose warm round trips take 10 ms and more and whose start alone takes
// 5 ms and more, and a span at rest of 20 ms.
func reportMachine(t *testing.T, exe string) {
	t.Helper()
	swap(t, &doctorReportSpan, 20*time.Millisecond)
	swap(t, &doctorHooks, func() []doctor.Hooks {
		return []doctor.Hooks{{Harness: "claude_code", Title: "Claude Code", Found: true, Settings: filepath.Join(filepath.Dir(exe), "settings.json"),
			Events: []string{"SessionStart"}, Exes: []string{exe}}}
	})
	times := func(from int) func(string, int) ([]time.Duration, error) {
		return func(_ string, n int) ([]time.Duration, error) {
			ds := make([]time.Duration, n)
			for i := range ds {
				ds[i] = time.Duration(from+i) * time.Millisecond
			}
			return ds, nil
		}
	}
	swap(t, &doctorRoundTrips, func(exe, _ string, n int) ([]time.Duration, error) { return times(10)(exe, n) })
	swap(t, &doctorStartup, times(5))
}

// TestDoctorReportNamesNothing plants a username, a hostname and a home
// path in the environment, in the data directory, in the agent's status
// and in the hook's path, and finds none of them in the report (DESIGN
// 5.9; TE WP7.9).
func TestDoctorReportNamesNothing(t *testing.T) {
	const user, host = "plantedteen", "plantedbox"
	data := filepath.Join(shortHome(t), user)
	if err := os.MkdirAll(data, 0o700); err != nil {
		t.Fatal(err)
	}
	homeDir := filepath.Join(t.TempDir(), "home-"+user)
	for k, v := range map[string]string{"USER": user, "USERNAME": user, "LOGNAME": user, "HOSTNAME": host, "COMPUTERNAME": host,
		"HOME": homeDir, "USERPROFILE": homeDir, home.EnvHome: data} {
		t.Setenv(k, v)
	}
	h := fakeHome{t: t, dir: data}
	h.writeFile("config.toml", []byte("# "+homeDir+" on "+host+"\n"))
	h.writeFile("state.json", []byte(`{"home":"`+filepath.ToSlash(homeDir)+`","host":"`+host+`"}`))
	st := runningStatus()
	// Even a version that names the teen, which a build from a checkout
	// can have, doesn't reach the report.
	st.Version.Version = "v0.1.0-" + user
	st.Signals.Focus = doctor.Signal{Unknown: "no X display on " + host}
	st.Problems = map[string]string{"config": "couldn't read " + filepath.Join(homeDir, ".config") + ": permission denied", "uploader": host + " " + user}
	st.Footprint = doctor.Footprint{RSSBytes: 18_200_000, PeakRSSBytes: 21_400_000, CPUS: 3.2}
	h.statusAgent(st)
	reportMachine(t, filepath.Join(homeDir, ".local", "bin", "pairtime"))

	code, out, errOut := runCmd("doctor", "--report")
	if code != 0 {
		t.Fatalf("doctor --report = %d, stderr %q", code, errOut)
	}
	// The 20 round trips take 10 to 29 ms and the starts 5 to 24 ms, so
	// their medians are 19.5 and 14.5 ms, which round up.
	for _, want := range []string{"pairtime footprint report", "agent memory:    18.2 MB now, 21.4 MB at most", "hook round trip: claude_code: 20 ms median",
		"start-up alone:  15 ms median"} {
		if !strings.Contains(out, want) {
			t.Errorf("the report doesn't have %q:\n%s", want, out)
		}
	}
	planted := []string{user, host, homeDir, filepath.ToSlash(homeDir), strings.ReplaceAll(homeDir, "/", `\`), data, filepath.ToSlash(data)}
	if real, err := os.Hostname(); err == nil && len(real) >= 6 {
		planted = append(planted, real)
	}
	for _, p := range planted {
		if strings.Contains(strings.ToLower(out), strings.ToLower(p)) {
			t.Errorf("the report has %q:\n%s", p, out)
		}
	}
}

func TestDoctorReportSpan(t *testing.T) {
	first := runningStatus()
	first.UptimeS, first.Wakeups, first.IPC.Stored = 3600, 120, 5
	first.Footprint = doctor.Footprint{RSSBytes: 18_000_000, PeakRSSBytes: 20_000_000, CPUS: 1.8}
	then := func(edit func(*doctor.Status)) doctor.Status {
		st := first
		st.Adapters.Harnesses = maps.Clone(first.Adapters.Harnesses)
		st.Wakeups += 2
		st.Footprint.CPUS += 0.000001
		edit(&st)
		return st
	}
	tests := []struct {
		name  string
		agent func(h fakeHome)
		want  []string
		not   []string
	}{
		{
			name:  "a quiet span gives the agent's CPU and wakeups over it, at rest",
			agent: func(h fakeHome) { h.statusSequence(first, then(func(*doctor.Status) {})) },
			want:  []string{"agent:           " + version.Get().Version + ", running for 1h 0m", "0.050% since it started;", " at rest (pairtime aims", "over the same span"},
			not:   []string{"not at rest"},
		},
		{
			name: "a span in which the agent stored a hook call says it wasn't at rest",
			agent: func(h fakeHome) {
				h.statusSequence(first, then(func(s *doctor.Status) { s.IPC.Stored++ }))
			},
			want: []string{"while it took hook calls, so not at rest"},
		},
		{
			name: "a span in which an adapter took a call says it wasn't at rest",
			agent: func(h fakeHome) {
				h.statusSequence(first, then(func(s *doctor.Status) {
					c := s.Adapters.Harnesses["claude_code"]
					c.Calls++
					s.Adapters.Harnesses["claude_code"] = c
				}))
			},
			want: []string{"so not at rest"},
		},
		{
			name: "an agent that restarted during the span gives only its numbers since its start",
			agent: func(h fakeHome) {
				h.statusSequence(first, then(func(s *doctor.Status) { s.PID++; s.Started = s.Started.Add(time.Hour) }))
			},
			want: []string{"since it started (pairtime aims"},
			not:  []string{"over the same span"},
		},
		{
			name:  "with no agent, the report says it isn't running and still has the computer and the hook",
			agent: func(fakeHome) {},
			want:  []string{"agent:           not running, so its memory, CPU and wakeups aren't here", "os:", "hook round trip: claude_code:"},
			not:   []string{"agent memory"},
		},
		{
			name:  "an agent from before doctor is one that can't say",
			agent: func(h fakeHome) { h.agent(nil) },
			want:  []string{"agent:           an older pairtime that can't say"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newDoctorHome(t)
			reportMachine(t, filepath.Join(h.dir, "bin", "pairtime"))
			tt.agent(h)
			code, out, errOut := runCmd("doctor", "--report")
			if code != 0 {
				t.Fatalf("doctor --report = %d, stderr %q", code, errOut)
			}
			for _, w := range tt.want {
				if !strings.Contains(out, w) {
					t.Errorf("the report doesn't have %q:\n%s", w, out)
				}
			}
			for _, w := range tt.not {
				if strings.Contains(out, w) {
					t.Errorf("the report has %q:\n%s", w, out)
				}
			}
		})
	}
}

// copyAsPairtime copies the test binary to pairtime, or pairtime.exe, in a
// folder of its own: what install's hooks run, by name, so that
// install.ReadClaudeHooks takes them for pairtime's.
func copyAsPairtime(t *testing.T) string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	name := "pairtime"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	dst := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(dst, b, 0o755); err != nil {
		t.Fatal(err)
	}
	return dst
}

// TestDoctorHookRoundTrip has doctor time real test calls of the hook that
// Claude Code's settings run, a copy of this binary, which runs `pairtime
// hook` (runEnv), and finds the calls went to doctor's own listener: none
// reached the agent, and none was spooled for it.
func TestDoctorHookRoundTrip(t *testing.T) {
	h := newHome(t)
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	t.Setenv("USERPROFILE", homeDir)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	exe := copyAsPairtime(t)
	hooks := map[string]any{}
	for _, ev := range []string{"SessionStart", "UserPromptSubmit", "Stop", "StopFailure", "Notification", "PermissionRequest",
		"PostToolUse", "SubagentStart", "SubagentStop", "SessionEnd"} {
		group := map[string]any{"hooks": []any{map[string]any{"type": "command", "command": exe,
			"args": []string{"hook", "--harness", "claude_code", "--event", ev}}}}
		if ev == "PostToolUse" {
			group["matcher"] = "Edit|Write|Bash"
		}
		hooks[ev] = []any{group}
	}
	b, err := json.Marshal(map[string]any{"hooks": hooks})
	if err != nil {
		t.Fatal(err)
	}
	settings := filepath.Join(homeDir, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settings), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settings, b, 0o600); err != nil {
		t.Fatal(err)
	}
	// The hook processes run pairtime from the test binary.
	t.Setenv(runEnv, ownArgs)
	h.statusAgent(runningStatus())

	checks, _ := doctorChecks(t)
	tests := []struct {
		name  string
		check func(t *testing.T)
	}{
		{"the hook line is ok, with the time of the test calls", func(t *testing.T) {
			if c := checks["hook.claude_code"]; c.Result != doctor.OK || !strings.Contains(c.Text, "a test call took") || !strings.Contains(c.Text, "the median of 3") {
				t.Errorf("hook.claude_code is %+v", c)
			}
		}},
		{"Claude Code's hooks bring the folder-trust hint", func(t *testing.T) {
			if c := checks["trust.claude_code"]; c.Result != doctor.OK || !strings.Contains(c.Text, "trust dialog") {
				t.Errorf("trust.claude_code is %+v", c)
			}
		}},
		{"no test call was spooled for the agent, so none can reach its ledger", func(t *testing.T) {
			if _, err := os.Stat(filepath.Join(h.dir, "inbox")); !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("the agent's inbox is there (%v): a test call was spooled for it", err)
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, tt.check)
	}
}

func TestDoctorKeystoreLine(t *testing.T) {
	tests := []struct {
		name     string
		setup    func(t *testing.T, dir string)
		want     doctor.Result
		wantText string
	}{
		{"with no device key yet the line warns that nothing is recorded", func(*testing.T, string) {}, doctor.Warn, "no device key yet"},
		{"a device key in the key file warns with the keystore's own fallback warning", func(t *testing.T, dir string) {
			if _, err := keys.Open(keys.Options{Dir: dir, Stores: []keys.Store{}}); err != nil {
				t.Fatal(err)
			}
		}, doctor.Warn, "pairtime keeps its device key in a file that only your account has permission to read"},
		{"a keystore.json that doesn't read fails", func(t *testing.T, dir string) {
			if err := os.MkdirAll(filepath.Join(dir, "keys"), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "keys", "keystore.json"), []byte("{"), 0o600); err != nil {
				t.Fatal(err)
			}
		}, doctor.Fail, "keystore.json, which says where the device key is, doesn't read"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newDoctorHome(t)
			tt.setup(t, h.dir)
			h.statusAgent(runningStatus())
			checks, _ := doctorChecks(t)
			if c := checks["keystore"]; c.Result != tt.want || !strings.Contains(c.Text, tt.wantText) {
				t.Errorf("keystore is %+v, want %s with %q", c, tt.want, tt.wantText)
			}
		})
	}
}
