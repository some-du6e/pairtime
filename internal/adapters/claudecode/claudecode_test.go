// SPDX-License-Identifier: AGPL-3.0-or-later

package claudecode

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/adapters"
	"github.com/some-du6e/pairtime/internal/platform"
	"github.com/some-du6e/pairtime/pkg/event"
)

// captures is testdata/fixtures/claude_code, WP-AD1's captures.
const captures = "../../../testdata/fixtures/claude_code"

// cliRun is the 2.1.327 Linux CLI capture's run, under captures.
const cliRun = "2.1.327/linux/cli"

func newTest() *Adapter { return New(adapters.FixtureOptions()) }

func mk() adapters.Adapter { return newTest() }

func readCalls(t *testing.T) []adapters.Call {
	t.Helper()
	sub, err := fs.Sub(os.DirFS(captures), cliRun)
	if err != nil {
		t.Fatal(err)
	}
	calls, err := adapters.ReadCalls(sub)
	if err != nil {
		t.Fatal(err)
	}
	return calls
}

func readExpected(t *testing.T) []adapters.Line {
	t.Helper()
	b, err := fs.ReadFile(newTest().Fixtures(), cliRun+"/expected.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	lines, err := adapters.ReadLines(b)
	if err != nil {
		t.Fatal(err)
	}
	return lines
}

func replay(t *testing.T, a adapters.Adapter, calls []adapters.Call, opts adapters.ReplayOptions) []adapters.Line {
	t.Helper()
	if opts.GOOS == "" {
		opts.GOOS = "linux"
	}
	recs, fails := adapters.Replay(a, calls, opts)
	for dir, err := range fails {
		t.Errorf("hooks/%s: %v", dir, err)
	}
	for _, r := range recs {
		e := r.Record.Event
		e.EventID, e.Seq, e.DeviceID = event.NewEventID(time.UnixMilli(e.T)), 1, "d_test"
		if err := e.Validate(); err != nil {
			t.Errorf("hooks/%s: %s doesn't validate: %v", r.From, e.Kind(), err)
		}
	}
	lines, err := adapters.Normalize(recs)
	if err != nil {
		t.Fatal(err)
	}
	return lines
}

func checkDiff(t *testing.T, got, want []adapters.Line) {
	t.Helper()
	for _, d := range adapters.Diff(got, want, 20) {
		t.Error(d)
	}
}

// eventOf decodes a Line's event.
func eventOf(t *testing.T, l adapters.Line) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(l.Event, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func setEvent(t *testing.T, l *adapters.Line, m map[string]any) {
	t.Helper()
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	l.Event = b
}

func TestConformance(t *testing.T) {
	results, err := adapters.Conform(mk, os.DirFS(captures))
	if err != nil {
		t.Fatal(err)
	}
	if len(results) == 0 {
		t.Fatal("no capture was replayed")
	}
	for _, r := range results {
		t.Run("every captured payload of "+r.Run+" maps to its expected records", func(t *testing.T) {
			if r.Pending {
				t.Skipf("%s has no expected records yet, and its %s says why", r.Run, adapters.PendingFile)
			}
			if r.Err != nil {
				t.Fatal(r.Err)
			}
			for dir, err := range r.Failures {
				t.Errorf("hooks/%s: %v", dir, err)
			}
			for _, d := range r.Diffs {
				t.Error(d)
			}
		})
	}
}

// TestDeliveries is D-spool-via's CI checks for WP-AD2 (docs/PLAN.md).
func TestDeliveries(t *testing.T) {
	calls := readCalls(t)
	want := readExpected(t)

	t.Run("walked calls validate and match the expected records", func(t *testing.T) {
		checkDiff(t, replay(t, newTest(), calls, adapters.ReplayOptions{Way: adapters.Walked}), want)
	})

	t.Run("the captured payloads through the spool map to the same events with via plugin, and their session.start has parent_class unknown and has_tty false", func(t *testing.T) {
		spooled := slices.Clone(want)
		for i := range spooled {
			m := eventOf(t, spooled[i])
			m["via"] = string(event.ViaPlugin)
			if m["kind"] == string(event.KindSessionStart) {
				d := m["data"].(map[string]any)
				d["parent_class"], d["has_tty"], d["entrypoint"] = "unknown", false, "unknown"
			}
			setEvent(t, &spooled[i], m)
		}
		checkDiff(t, replay(t, newTest(), calls, adapters.ReplayOptions{Way: adapters.Spooled}), spooled)
	})

	start, prompt := callNamed(t, calls, "000001", EventSessionStart), callNamed(t, calls, "000002", EventUserPromptSubmit)
	stub := stubHarness(prompt)

	t.Run("a spooled SessionStart, then a UserPromptSubmit over the socket from under a stub harness, add a session.start with via hook and the stub's parent class before the prompt", func(t *testing.T) {
		a := newTest()
		recs := parse(t, a, start, adapters.Spooled, nil)
		recs = append(recs, parse(t, a, prompt, adapters.Walked, stub)...)
		kinds := kindsOf(recs)
		if !slices.Equal(kinds, []string{"session.start/plugin", "session.start/hook", "prompt.submit/hook"}) {
			t.Fatalf("records %v", kinds)
		}
		st := recs[1].Event.Data.(*event.SessionStart)
		want := event.SessionStart{StartKind: event.StartStartup, Entrypoint: event.EntrypointUnknown,
			ParentClass: event.ParentTerminalApp, HasTTY: true}
		if !sameStart(st, &want) {
			t.Errorf("the walk's session.start is %+v, want %+v", *st, want)
		}
		if recs[1].Event.T != recs[2].Event.T {
			t.Errorf("the walk's session.start is at %d, the prompt at %d", recs[1].Event.T, recs[2].Event.T)
		}
		spooled := recs[0].Event.Data.(*event.SessionStart)
		if spooled.ParentClass != event.ParentUnknown || spooled.HasTTY || spooled.Entrypoint != event.EntrypointUnknown {
			t.Errorf("the spooled session.start is %+v", *spooled)
		}
	})

	t.Run("a SessionStart with a counted walk adds no other session.start", func(t *testing.T) {
		a := newTest()
		recs := parse(t, a, start, adapters.Walked, stub)
		recs = append(recs, parse(t, a, prompt, adapters.Walked, stub)...)
		if kinds := kindsOf(recs); !slices.Equal(kinds, []string{"session.start/hook", "prompt.submit/hook"}) {
			t.Fatalf("records %v", kinds)
		}
	})

	t.Run("a call over the socket whose walk reaches no claude process is via plugin and fills no session field", func(t *testing.T) {
		a := newTest()
		script := []platform.Proc{{PID: 4242, Name: "python3", TTY: platform.TTYNone}, {PID: 4241, Name: "bash"}}
		recs := parse(t, a, start, adapters.Walked, script)
		recs = append(recs, parse(t, a, prompt, adapters.Walked, script)...)
		if kinds := kindsOf(recs); !slices.Equal(kinds, []string{"session.start/plugin", "prompt.submit/plugin"}) {
			t.Fatalf("records %v", kinds)
		}
		if st := recs[0].Event.Data.(*event.SessionStart); st.ParentClass != event.ParentUnknown || st.HasTTY {
			t.Errorf("session.start %+v", *st)
		}
	})

	t.Run("every session a hook reported, through the spool too, is one a log reader must write no session.start for", func(t *testing.T) {
		for _, way := range []adapters.Delivery{adapters.Walked, adapters.Spooled} {
			a := newTest()
			adapters.Replay(a, calls, adapters.ReplayOptions{GOOS: "linux", Way: way})
			for _, id := range []string{"617e16a3-ddb4-44ca-af68-72cf58ed1a34", "2197af56-2e28-498b-bf5d-15bbdd529e13",
				"b0423624-aebf-47ff-88b0-927e8df77410", "e5edd056-19ab-4262-8f2f-25ec0c28c9f9"} {
				if !a.HookReported(id) {
					t.Errorf("session %s isn't reported", id[:8])
				}
			}
			if a.HookReported("00000000-0000-4000-8000-000000000000") {
				t.Error("a session no hook named is reported")
			}
		}
	})

	// The Windows and macOS chains of Cursor's desktop app are hookpath's
	// TestOf's: it runs hooks through PowerShell on Windows and through sh
	// from its plugin helper on macOS (HS 4.1).
	none := platform.TTYNone
	others := []struct {
		name    string
		goos    string
		way     adapters.Delivery
		parents []platform.Proc
		harness event.Harness
	}{
		{name: "a call cursor-agent ran is Cursor's to record", goos: "linux", way: adapters.Walked,
			parents: []platform.Proc{{PID: 5151, Name: "cursor-agent", TTY: platform.TTYAttached}, {PID: 5150, Name: "zsh"}},
			harness: event.HarnessCursor},
		{name: "a call Cursor's desktop app ran on Linux is Cursor's to record", goos: "linux", way: adapters.Walked,
			parents: []platform.Proc{{PID: 5152, Name: "sh", TTY: none}, {PID: 5150, Name: "cursor", TTY: none}, {PID: 1, Name: "systemd", TTY: none}},
			harness: event.HarnessCursor},
		{name: "a call Cursor's desktop app ran through PowerShell on Windows is Cursor's to record", goos: "windows", way: adapters.Walked,
			parents: []platform.Proc{{PID: 800, Name: "powershell", TTY: none}, {PID: 700, Name: "Cursor", TTY: none}, {PID: 600, Name: "explorer", TTY: none}},
			harness: event.HarnessCursor},
		{name: "a call Cursor's plugin helper ran through sh on macOS is Cursor's to record", goos: "darwin", way: adapters.Walked,
			parents: []platform.Proc{{PID: 820, Name: "sh", TTY: none}, {PID: 810, Name: "Cursor Helper (Plugin)", TTY: none},
				{PID: 700, Name: "Cursor", TTY: none}, {PID: 1, Name: "launchd", TTY: none}},
			harness: event.HarnessCursor},
		{name: "a spooled call whose recorded chain names Cursor's desktop app is Cursor's to record", goos: "windows", way: adapters.Spooled,
			parents: []platform.Proc{{PID: 800, Name: "powershell", TTY: none}, {PID: 700, Name: "Cursor", TTY: none}, {PID: 600, Name: "explorer", TTY: none}},
			harness: event.HarnessCursor},
		{name: "a call Devin Desktop ran on Linux is Devin's to record", goos: "linux", way: adapters.Walked,
			parents: []platform.Proc{{PID: 5152, Name: "sh", TTY: none}, {PID: 5150, Name: "devin-desktop", TTY: none}, {PID: 1, Name: "systemd", TTY: none}},
			harness: event.HarnessDevin},
		{name: "a call the Devin CLI ran is Devin's to record", goos: "linux", way: adapters.Walked,
			parents: []platform.Proc{{PID: 5151, Name: "devin", TTY: platform.TTYAttached}, {PID: 5150, Name: "bash"}},
			harness: event.HarnessDevin},
	}
	for _, tc := range others {
		t.Run(tc.name, func(t *testing.T) {
			d := adapters.DeliveryOf(event.HarnessClaudeCode, withParents(prompt, tc.parents), tc.way, prompt.Arrived)
			raw, err := adapters.HookRecord(newTest(), tc.goos, d)
			if err != nil {
				t.Fatal(err)
			}
			if raw.Arrival.Via != event.ViaPlugin || raw.Arrival.Harness != tc.harness || raw.Arrival.Walk != nil {
				t.Errorf("via %s, harness %q, walk %v; want plugin and %s with no walk", raw.Arrival.Via, raw.Arrival.Harness,
					raw.Arrival.Walk, tc.harness)
			}
			if recs, err := newTest().Parse(raw); !errors.Is(err, adapters.ErrOtherHarness) || len(recs) != 0 {
				t.Errorf("Parse = %d records, %v", len(recs), err)
			}
		})
	}

	t.Run("a call VS Code's Claude Code extension ran is Claude Code's, through the claude it starts", func(t *testing.T) {
		parents := []platform.Proc{{PID: 7001, Name: "claude", TTY: none, Start: prompt.Arrived.Add(-time.Minute)},
			{PID: 7000, Name: "code", TTY: none}, {PID: 1, Name: "systemd", TTY: none}}
		recs := parse(t, newTest(), prompt, adapters.Walked, parents)
		if kinds := kindsOf(recs); !slices.Equal(kinds, []string{"session.start/hook", "prompt.submit/hook"}) {
			t.Fatalf("records %v", kinds)
		}
	})
}

// callNamed returns the call in dir, which must be of event e.
func callNamed(t *testing.T, calls []adapters.Call, dir, e string) adapters.Call {
	t.Helper()
	for _, c := range calls {
		if c.Dir == dir {
			if c.Event != e {
				t.Fatalf("hooks/%s is %s, not %s", dir, c.Event, e)
			}
			return c
		}
	}
	t.Fatalf("no hooks/%s", dir)
	return adapters.Call{}
}

// stubHarness is a claude process under a shell in a terminal, the process
// table a test supplies in place of the capture's.
func stubHarness(c adapters.Call) []platform.Proc {
	return []platform.Proc{
		{PID: 7001, Name: "claude", TTY: platform.TTYAttached, Start: c.Arrived.Add(-time.Minute)},
		{PID: 7000, Name: "zsh", TTY: platform.TTYAttached},
		{PID: 6999, Name: "kitty", TTY: platform.TTYNone},
		{PID: 1, Name: "systemd", TTY: platform.TTYNone},
	}
}

func withParents(c adapters.Call, parents []platform.Proc) adapters.Call {
	c.Parents = parents
	return c
}

// parse hands call c to a by way, from under parents when they are given.
func parse(t *testing.T, a *Adapter, c adapters.Call, way adapters.Delivery, parents []platform.Proc) []adapters.Record {
	t.Helper()
	if parents != nil {
		c = withParents(c, parents)
	}
	d := adapters.DeliveryOf(event.HarnessClaudeCode, c, way, c.Arrived)
	raw, err := adapters.HookRecord(a, "linux", d)
	if err != nil {
		t.Fatal(err)
	}
	recs, err := a.Parse(raw)
	if err != nil {
		t.Fatalf("hooks/%s: %v", c.Dir, err)
	}
	return recs
}

func kindsOf(recs []adapters.Record) []string {
	var out []string
	for _, r := range recs {
		out = append(out, string(r.Event.Kind())+"/"+string(r.Event.Via))
	}
	return out
}

func sameStart(a, b *event.SessionStart) bool {
	return a.StartKind == b.StartKind && a.Entrypoint == b.Entrypoint && a.ParentClass == b.ParentClass &&
		a.HasTTY == b.HasTTY && a.PermissionMode == b.PermissionMode && slices.Equal(a.AutomationFlags, b.AutomationFlags)
}

// report is a claude_code.tool_decision from the capture's OTLP bodies,
// with the time the sink got the body.
type report struct {
	ToolDecision
	arrived time.Time
}

// readReports reads every tool_decision the capture's sink saved.
func readReports(t *testing.T) []report {
	t.Helper()
	dir := captures + "/" + cliRun + "/otlp"
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []report
	for _, e := range entries {
		name, ok := strings.CutSuffix(e.Name(), ".json")
		if !ok {
			continue
		}
		meta, err := os.ReadFile(dir + "/" + name + ".meta")
		if err != nil {
			t.Fatal(err)
		}
		var arrived time.Time
		for _, line := range strings.Split(string(meta), "\n") {
			if v, ok := strings.CutPrefix(line, "unix_ns="); ok {
				ns, _ := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
				arrived = time.Unix(0, ns)
			}
		}
		body, err := os.ReadFile(dir + "/" + e.Name())
		if err != nil {
			t.Fatal(err)
		}
		var otlp struct {
			ResourceLogs []struct {
				ScopeLogs []struct {
					LogRecords []struct {
						Attributes []struct {
							Key   string
							Value map[string]any
						}
					}
				}
			}
		}
		if err := json.Unmarshal(body, &otlp); err != nil {
			t.Fatalf("otlp/%s: %v", e.Name(), err)
		}
		for _, rl := range otlp.ResourceLogs {
			for _, sl := range rl.ScopeLogs {
				for _, lr := range sl.LogRecords {
					attrs := map[string]string{}
					for _, a := range lr.Attributes {
						if s, ok := a.Value["stringValue"].(string); ok {
							attrs[a.Key] = s
						}
					}
					if attrs["event.name"] != "tool_decision" {
						continue
					}
					ts, err := time.Parse(time.RFC3339Nano, attrs["event.timestamp"])
					if err != nil {
						t.Fatalf("otlp/%s: %v", e.Name(), err)
					}
					out = append(out, report{ToolDecision: ToolDecision{
						SessionID: attrs["session.id"], AgentID: attrs["agent_id"], ToolName: attrs["tool_name"],
						ToolUseID: attrs["tool_use_id"], Decision: attrs["decision"], Source: attrs["source"],
						Timestamp: ts.UnixMilli(), Arrival: arrived.UnixMilli(), PeerChecked: true,
					}, arrived: arrived})
				}
			}
		}
	}
	return out
}

// TestPermissions is E-6.1-permission's: hooks alone write no
// agent.permission, and Claude Code's own tool_decision, paired with the
// held request, writes the record SM 1.2 defines. Hooks and OTLP exports
// are handed over in the order they arrived, as the agent takes them in.
func TestPermissions(t *testing.T) {
	calls := readCalls(t)
	reports := readReports(t)
	if len(reports) == 0 {
		t.Fatal("the capture has no tool_decision")
	}

	t.Run("hooks alone give no agent.permission, at steps 4, 6 and 22 or anywhere else", func(t *testing.T) {
		for _, way := range []adapters.Delivery{adapters.Walked, adapters.Spooled} {
			recs, _ := adapters.Replay(newTest(), calls, adapters.ReplayOptions{GOOS: "linux", Way: way})
			for _, r := range recs {
				if r.Record.Event.Kind() == event.KindAgentPermission {
					t.Errorf("hooks/%s gave an agent.permission", r.From)
				}
			}
		}
	})

	// Interleave the calls and the reports by arrival.
	a := newTest()
	byTool := map[string]event.Event{}
	reportOf := map[string]ToolDecision{}
	var recorded []event.Event
	ri := 0
	decideUntil := func(until time.Time) {
		for ri < len(reports) && (until.IsZero() || reports[ri].arrived.Before(until)) {
			if rec, ok := a.Decide(reports[ri].ToolDecision); ok {
				recorded = append(recorded, rec.Event)
				byTool[reports[ri].ToolUseID] = rec.Event
				reportOf[reports[ri].ToolUseID] = reports[ri].ToolDecision
			}
			ri++
		}
	}
	slices.SortStableFunc(reports, func(x, y report) int { return x.arrived.Compare(y.arrived) })
	for _, c := range calls {
		decideUntil(c.Arrived)
		parse(t, a, c, adapters.Walked, nil)
	}
	decideUntil(time.Time{})

	permission := func(t *testing.T, toolUseID string) (event.Event, *event.AgentPermission) {
		t.Helper()
		e, ok := byTool[toolUseID]
		if !ok {
			t.Fatalf("no agent.permission for %s", toolUseID)
		}
		return e, e.Data.(*event.AgentPermission)
	}
	ms := func(dir string) int64 { return calls[mustIndex(t, calls, dir)].Arrived.UnixMilli() }

	// In each of the capture's answers, Claude Code stamped its report
	// before the call's PostToolUse arrived, and the report itself arrived
	// seconds later, with its OTLP batch. TestAnsweredAt has the other
	// orders.
	cases := []struct {
		name              string
		toolUseID         string
		request, toolCall string // the hook calls
		decision          event.Decision
		by                event.DecidedBy
	}{
		{name: "step 4: the edit the person accepted is accept by user, answered when Claude Code reports it, 84 ms before its PostToolUse",
			toolUseID: "toolu_bdrk_01T3dpcw63DcUeQ2GbQ5Ago3", request: "000008", toolCall: "000010",
			decision: event.DecisionAccept, by: event.DecidedByUser},
		{name: "step 4: the command the person accepted once is accept by user, answered at the report's timestamp",
			toolUseID: "toolu_bdrk_01X7hAW4BHxHc4odkrqUd3ag", request: "000011", toolCall: "000013",
			decision: event.DecisionAccept, by: event.DecidedByUser},
		{name: "step 6: the rm the person turned down is reject by user, answered at the report's timestamp, with no PostToolUse",
			toolUseID: "toolu_bdrk_01XFz5ytj5TGdiVUC3pDRFcP", request: "000021",
			decision: event.DecisionReject, by: event.DecidedByUser},
		{name: "step 22: the write allow-once.sh allowed is decided_by hook, answered at the report's timestamp",
			toolUseID: "toolu_bdrk_0173LvGuQ9HSwUyKyDJseP52", request: "000024", toolCall: "000025",
			decision: event.DecisionAccept, by: event.DecidedByHook},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e, p := permission(t, tc.toolUseID)
			if p.Decision != tc.decision || p.DecidedBy != tc.by {
				t.Errorf("decision %s by %s, want %s by %s", p.Decision, p.DecidedBy, tc.decision, tc.by)
			}
			if p.RequestedAt != ms(tc.request) {
				t.Errorf("requested_at %d, want hooks/%s's arrival %d", p.RequestedAt, tc.request, ms(tc.request))
			}
			r := reportOf[tc.toolUseID]
			if p.AnsweredAt != r.Timestamp {
				t.Errorf("answered_at %d, want the report's event.timestamp %d", p.AnsweredAt, r.Timestamp)
			}
			if r.Timestamp >= r.Arrival || tc.toolCall != "" && r.Timestamp >= ms(tc.toolCall) {
				t.Errorf("the report's timestamp %d isn't the earliest of the three times, so the case says nothing: it arrived at %d", r.Timestamp, r.Arrival)
			}
			if e.T != p.AnsweredAt || e.Source != event.SourceHook || e.Via != event.ViaHook {
				t.Errorf("envelope t %d source %s via %s, want t = answered_at %d and the request hook's source and via",
					e.T, e.Source, e.Via, p.AnsweredAt)
			}
			e.EventID, e.Seq, e.DeviceID = event.NewEventID(time.UnixMilli(e.T)), 1, "d_test"
			if err := e.Validate(); err != nil {
				t.Error(err)
			}
		})
	}

	t.Run("a tool Claude Code allowed by its own config, with no request, writes no record", func(t *testing.T) {
		// The grep of step 2 ran with source config and no PermissionRequest.
		if _, ok := byTool["toolu_bdrk_012dGaUfGmyEktGiq981wjqh"]; ok {
			t.Error("a config decision with no request wrote a record")
		}
	})

	t.Run("each permission request of the capture got exactly one record", func(t *testing.T) {
		requests := 0
		for _, c := range calls {
			if c.Event == EventPermissionRequest {
				requests++
			}
		}
		if len(recorded) != requests {
			t.Errorf("%d records for %d requests", len(recorded), requests)
		}
	})

	t.Run("a report over a connection the peer check didn't pass, or with a source that names nobody, writes nothing", func(t *testing.T) {
		b := newTest()
		req := callNamed(t, calls, "000008", EventPermissionRequest)
		parse(t, b, req, adapters.Walked, nil)
		base := ToolDecision{SessionID: "617e16a3-ddb4-44ca-af68-72cf58ed1a34", ToolName: "Edit", Decision: "accept",
			Source: "user_temporary", Timestamp: req.Arrived.UnixMilli() + 5000, Arrival: req.Arrived.UnixMilli() + 6000}
		unchecked := base
		if _, ok := b.Decide(unchecked); ok {
			t.Error("an unchecked report wrote a record")
		}
		odd := base
		odd.PeerChecked, odd.Source = true, "policy"
		if _, ok := b.Decide(odd); ok {
			t.Error("a report with an unknown source wrote a record")
		}
		ok := base
		ok.PeerChecked = true
		if _, got := b.Decide(ok); !got {
			t.Error("the request was used up by reports that didn't count")
		}
	})
}

// TestAnsweredAt is E-6.1-permission's answered_at: the earliest of the
// report's event.timestamp, its arrival and the arrival of the call's
// PostToolUse by tool_use_id, and never before requested_at.
func TestAnsweredAt(t *testing.T) {
	at := func(ms int64) *adapters.Arrival {
		return &adapters.Arrival{Via: event.ViaHook, T: t0 + ms, Mono: 5000 + ms, BootID: "boot",
			Walk:    &platform.Lineage{ParentClass: event.ParentTerminalApp, Entrypoint: event.EntrypointUnknown, HasTTY: true},
			Harness: event.HarnessClaudeCode}
	}
	const sid = "11111111-2222-4333-8444-555555555555" // hookRaw's
	cases := []struct {
		name string
		// post is when the call's PostToolUse arrived, after the request,
		// or 0 for none; stamped and arrival are the report's.
		post, stamped, arrival int64
		want                   int64
	}{
		{name: "the report's timestamp, when Claude Code stamped it first", post: 400, stamped: 300, arrival: 5300, want: 300},
		{name: "the PostToolUse's arrival, when it came before the report's timestamp", post: 200, stamped: 300, arrival: 5300, want: 200},
		{name: "the report's arrival, when it came before the report's own timestamp", stamped: 5300, arrival: 900, want: 900},
		{name: "never before requested_at, even when the report seems to arrive first", stamped: 300, arrival: -50, want: 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := newTest()
			if _, err := a.Parse(hookRaw(EventPermissionRequest, map[string]any{"prompt_id": "p1", "tool_name": "Bash",
				"tool_input": map[string]any{"command": "go test ./..."}}, at(0))); err != nil {
				t.Fatal(err)
			}
			if tc.post > 0 {
				if _, err := a.Parse(hookRaw(EventPostToolUse, map[string]any{"prompt_id": "p1", "tool_name": "Bash",
					"tool_input": map[string]any{"command": "go test ./..."}, "tool_response": map[string]any{"stdout": "ok\n"},
					"tool_use_id": "toolu_a", "duration_ms": 10}, at(tc.post))); err != nil {
					t.Fatal(err)
				}
			}
			rec, ok := a.Decide(ToolDecision{SessionID: sid, ToolName: "Bash", ToolUseID: "toolu_a", Decision: "accept",
				Source: "user_temporary", Timestamp: t0 + tc.stamped, Arrival: t0 + tc.arrival, PeerChecked: true})
			if !ok {
				t.Fatal("no record")
			}
			p := rec.Event.Data.(*event.AgentPermission)
			if p.RequestedAt != t0 || p.AnsweredAt != t0+tc.want || rec.Event.T != p.AnsweredAt || rec.Event.Mono != 5000+tc.want {
				t.Errorf("requested_at %d, answered_at %d, t %d, mono %d; want answered_at %d", p.RequestedAt-t0, p.AnsweredAt-t0,
					rec.Event.T-t0, rec.Event.Mono, tc.want)
			}
		})
	}
}

func mustIndex(t *testing.T, calls []adapters.Call, dir string) int {
	t.Helper()
	for i, c := range calls {
		if c.Dir == dir {
			return i
		}
	}
	t.Fatalf("no hooks/%s", dir)
	return -1
}

// TestExpectedFromInput checks the expected file against the raw captured
// calls, field by field: what an expected record copies from its call is
// in that call's payload, and its keys stand for the payload's own ids,
// one symbol for one id (docs/PLAN.md, WP-AD2's CI checks).
func TestExpectedFromInput(t *testing.T) {
	calls := readCalls(t)
	byDir := map[string]map[string]any{}
	arrived := map[string]int64{}
	for _, c := range calls {
		var m map[string]any
		if err := json.Unmarshal(c.Stdin, &m); err != nil {
			t.Fatalf("hooks/%s: %v", c.Dir, err)
		}
		byDir[c.Dir], arrived[c.Dir] = m, c.Arrived.UnixMilli()
	}
	ids := map[string]map[string]string{}  // symbol -> the id it stands for
	syms := map[string]map[string]string{} // id -> the symbol that stands for it
	same := func(t *testing.T, family, sym, id string) {
		t.Helper()
		if ids[family] == nil {
			ids[family], syms[family] = map[string]string{}, map[string]string{}
		}
		if prev, ok := ids[family][sym]; ok && prev != id {
			t.Errorf("%s stands for %q and %q", sym, prev, id)
		}
		if prev, ok := syms[family][id]; ok && prev != sym {
			t.Errorf("%q is %s and %s", id, prev, sym)
		}
		ids[family][sym], syms[family][id] = id, sym
	}
	for i, l := range readExpected(t) {
		raw, ok := byDir[l.From]
		if !ok {
			t.Fatalf("record %d: no hooks/%s", i+1, l.From)
		}
		ev := eventOf(t, l)
		data, _ := ev["data"].(map[string]any)
		str := func(m map[string]any, k string) string { s, _ := m[k].(string); return s }
		t.Run(l.From+" "+str(ev, "kind")+" copies what its call says", func(t *testing.T) {
			if got := int64(ev["t"].(float64)); got != arrived[l.From] {
				t.Errorf("t %d, the call arrived at %d", got, arrived[l.From])
			}
			same(t, "session", str(ev, "session_key"), str(raw, "session_id"))
			same(t, "project", str(ev, "project_key"), str(raw, "cwd"))
			switch str(ev, "kind") {
			case "session.start":
				if raw["hook_event_name"] == EventSessionStart && str(data, "start_kind") != str(raw, "source") {
					t.Errorf("start_kind %q, source %q", str(data, "start_kind"), str(raw, "source"))
				}
			case "session.end":
				if str(data, "reason") != str(raw, "reason") {
					t.Errorf("reason %q, the payload's %q", str(data, "reason"), str(raw, "reason"))
				}
			case "prompt.submit", "agent.queue":
				p := str(raw, "prompt")
				if int(data["len_chars"].(float64)) != len([]rune(p)) {
					t.Errorf("len_chars %v, the prompt has %d", data["len_chars"], len([]rune(p)))
				}
				if sc := str(data, "slash_command"); sc != "" && !strings.HasPrefix(p, sc) {
					t.Errorf("slash_command %q isn't how the prompt starts", sc)
				}
				if str(ev, "kind") == "prompt.submit" && (l.Text == nil || *l.Text != p) {
					t.Error("text isn't the prompt")
				}
				same(t, "turn", str(data, "turn_key"), str(raw, "prompt_id"))
			case "agent.output":
				if l.Text == nil || *l.Text != str(raw, "last_assistant_message") {
					t.Error("text isn't last_assistant_message")
				}
				same(t, "turn", str(data, "turn_key"), str(raw, "prompt_id"))
			case "agent.stop":
				if data["stop_hook_active"] != raw["stop_hook_active"] {
					t.Errorf("stop_hook_active %v, the payload's %v", data["stop_hook_active"], raw["stop_hook_active"])
				}
				same(t, "turn", str(data, "turn_key"), str(raw, "prompt_id"))
				crons, _ := raw["session_crons"].([]any)
				pending, _ := data["pending_crons"].([]any)
				if len(pending) != len(crons) {
					t.Fatalf("%d pending_crons for %d session_crons", len(pending), len(crons))
				}
				for j, pc := range pending {
					if str(pc.(map[string]any), "schedule") != str(crons[j].(map[string]any), "schedule") {
						t.Errorf("pending_crons[%d]'s schedule isn't the payload's", j)
					}
				}
			case "agent.tool":
				in, _ := raw["tool_input"].(map[string]any)
				if l.Command != str(in, "command") {
					t.Error("command isn't tool_input.command")
				}
				dur, _ := raw["duration_ms"].(float64)
				if int64(data["t_end"].(float64))-int64(data["t_start"].(float64)) != int64(dur) {
					t.Errorf("t_end - t_start isn't duration_ms %v", dur)
				}
				if files, _ := data["files"].([]any); len(files) == 1 {
					same(t, "file", files[0].(string), strings.TrimPrefix(str(in, "file_path"), str(raw, "cwd")+"/"))
				}
				for _, e := range l.Edits {
					if !strings.HasSuffix(str(in, "file_path"), "/"+e.Path) {
						t.Errorf("an edit's path %q isn't the file's", e.Path)
					}
				}
			case "agent.subagent":
				same(t, "agent", str(data, "agent_key"), str(raw, "agent_id"))
			}
		})
	}
}

// TestGenExpected reruns the conformance oracle's second implementation,
// testdata/gen_expected.py, over the capture and checks that it writes the
// committed expected file byte for byte. Without it the file could drift
// from the oracle, or be written from the Go adapter's own output, and every
// other test would stay green. It needs python3; without it the test is
// skipped, except in CI, where it fails.
func TestGenExpected(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		if os.Getenv("CI") != "" {
			t.Fatal("python3 is needed to rerun gen_expected.py")
		}
		t.Skip("python3 not found")
	}
	out := filepath.Join(t.TempDir(), "expected.jsonl")
	cmd := exec.Command(python, "-I", "-B", filepath.Join("testdata", "gen_expected.py"), filepath.Join(captures, cliRun), out)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("gen_expected.py: %v\n%s", err, b)
	}
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	want, err := fs.ReadFile(newTest().Fixtures(), cliRun+"/expected.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(got, want) {
		return
	}
	g, w := bytes.Split(got, []byte("\n")), bytes.Split(want, []byte("\n"))
	for i := 0; i < max(len(g), len(w)); i++ {
		if i >= len(g) || i >= len(w) || !bytes.Equal(g[i], w[i]) {
			t.Fatalf("line %d differs: gen_expected.py writes %d lines, fixtures/%s/expected.jsonl has %d", i+1, len(g), cliRun, len(w))
		}
	}
}
