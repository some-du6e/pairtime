// SPDX-License-Identifier: AGPL-3.0-or-later

package adapters

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/some-du6e/pairtime/internal/agent/hookpath"
	"github.com/some-du6e/pairtime/internal/agent/inbox"
	"github.com/some-du6e/pairtime/internal/platform"
	"github.com/some-du6e/pairtime/pkg/event"
)

// fake is an adapter for the package's own tests: it makes a session.end
// for every call and reads only its Hosts and Fixtures.
type fake struct {
	harness event.Harness
	hosts   []ProcessMatch
	fixture fs.FS
}

func (f *fake) Name() event.Harness            { return f.harness }
func (f *fake) Detect(Env) Detection           { return Detection{} }
func (f *fake) Plan(Env) ([]ConfigEdit, error) { return nil, nil }
func (f *fake) Sources() []Source              { return nil }
func (f *fake) Hosts() []ProcessMatch          { return f.hosts }
func (f *fake) Fixtures() fs.FS                { return f.fixture }
func (f *fake) Parse(raw RawRecord) ([]Record, error) {
	e := event.Event{Envelope: event.Envelope{T: raw.Arrival.T, Via: raw.Arrival.Via, Source: event.SourceHook,
		Harness: f.harness, SessionKey: "s_1"}, Data: &event.SessionEnd{Reason: "other"}}
	return []Record{{Event: e}}, nil
}

func claudeHosts() []ProcessMatch { return []ProcessMatch{{Names: []string{"claude"}}} }

func TestRegistry(t *testing.T) {
	mkFake := func(Options) Adapter { return &fake{harness: event.HarnessCodex} }
	cases := []struct {
		name  string
		do    func(r *Registry)
		panic bool
	}{
		{name: "a harness registers once", do: func(r *Registry) { r.Register(event.HarnessCodex, mkFake) }},
		{name: "a second registration of one harness panics", panic: true,
			do: func(r *Registry) { r.Register(event.HarnessCodex, mkFake); r.Register(event.HarnessCodex, mkFake) }},
		{name: "a harness pkg/event doesn't know panics", panic: true,
			do: func(r *Registry) { r.Register("vim_copilot", mkFake) }},
		{name: "none isn't a harness to register", panic: true,
			do: func(r *Registry) { r.Register(event.HarnessNone, mkFake) }},
		{name: "a nil factory panics", panic: true, do: func(r *Registry) { r.Register(event.HarnessCodex, nil) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if r := recover(); (r != nil) != tc.panic {
					t.Errorf("panic %v, want one: %v", r, tc.panic)
				}
			}()
			tc.do(&Registry{})
		})
	}
	t.Run("New makes the registered adapter, and Harnesses lists it", func(t *testing.T) {
		r := &Registry{}
		r.Register(event.HarnessCodex, mkFake)
		if a, ok := r.New(event.HarnessCodex, Options{}); !ok || a.Name() != event.HarnessCodex {
			t.Errorf("New = %v, %v", a, ok)
		}
		if _, ok := r.New(event.HarnessGemini, Options{}); ok {
			t.Error("New made an adapter that isn't registered")
		}
		if hs := r.Harnesses(); len(hs) != 1 || hs[0] != event.HarnessCodex {
			t.Errorf("Harnesses = %v", hs)
		}
	})
}

// delivery is a hook call of h's from under parents, by way.
func delivery(h event.Harness, way Delivery, parents ...platform.Proc) inbox.Delivery {
	c := Call{Dir: "000001", Event: "Stop", Stdin: []byte(`{"session_id":"x","hook_event_name":"Stop"}`),
		Arrived: time.UnixMilli(1795193284378).UTC(), Parents: parents}
	return DeliveryOf(h, c, way, c.Arrived.Add(-time.Second))
}

func TestHookRecord(t *testing.T) {
	a := &fake{harness: event.HarnessClaudeCode, hosts: claudeHosts()}
	claude := platform.Proc{PID: 10, Name: "claude", TTY: platform.TTYAttached}
	tmux := platform.Proc{PID: 9, Name: "tmux", Alias: "tmux: server", TTY: platform.TTYNone}
	cases := []struct {
		name    string
		way     Delivery
		parents []platform.Proc
		via     event.Via
		walk    bool
		harness event.Harness
	}{
		{name: "over the socket from under claude, the call is via hook and carries the walk",
			way: Walked, parents: []platform.Proc{claude, {PID: 8, Name: "bash"}, tmux}, via: event.ViaHook, walk: true,
			harness: event.HarnessClaudeCode},
		{name: "through the spool, with the same chain recorded, the call is via plugin and carries no walk, only the harness name",
			way: Spooled, parents: []platform.Proc{claude, {PID: 8, Name: "bash"}, tmux}, via: event.ViaPlugin,
			harness: event.HarnessClaudeCode},
		{name: "over the socket from under another harness, the call is via plugin and names that harness",
			way: Walked, parents: []platform.Proc{{PID: 11, Name: "cursor-agent"}, tmux}, via: event.ViaPlugin,
			harness: event.HarnessCursor},
		{name: "over the socket from under a script, the call is via plugin and names no harness",
			way: Walked, parents: []platform.Proc{{PID: 12, Name: "python3"}}, via: event.ViaPlugin},
		{name: "over the socket from under Cursor's desktop app, which runs these hooks too, the call is via plugin and names Cursor",
			way: Walked, parents: []platform.Proc{{PID: 13, Name: "sh"}, {PID: 11, Name: "cursor"}, {PID: 1, Name: "systemd"}},
			via: event.ViaPlugin, harness: event.HarnessCursor},
		{name: "through the spool from under Cursor's desktop app, the recorded chain names Cursor",
			way: Spooled, parents: []platform.Proc{{PID: 13, Name: "sh"}, {PID: 11, Name: "cursor"}, {PID: 1, Name: "systemd"}},
			via: event.ViaPlugin, harness: event.HarnessCursor},
		{name: "over the socket from under an editor that hosts no harness's hooks, the call names none",
			way: Walked, parents: []platform.Proc{{PID: 13, Name: "sh"}, {PID: 11, Name: "zed"}, {PID: 1, Name: "systemd"}},
			via: event.ViaPlugin},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := HookRecord(a, "linux", delivery(event.HarnessClaudeCode, tc.way, tc.parents...))
			if err != nil {
				t.Fatal(err)
			}
			if raw.Arrival.Via != tc.via || (raw.Arrival.Walk != nil) != tc.walk || raw.Arrival.Harness != tc.harness {
				t.Errorf("via %s, walk %v, harness %q", raw.Arrival.Via, raw.Arrival.Walk, raw.Arrival.Harness)
			}
			if tc.walk && (raw.Arrival.Walk.ParentClass != event.ParentMultiplexer || !raw.Arrival.Walk.HasTTY) {
				t.Errorf("walk %+v", *raw.Arrival.Walk)
			}
			if raw.Event != "Stop" || !json.Valid(raw.Payload) || raw.Incomplete {
				t.Errorf("raw %+v", raw)
			}
		})
	}
	t.Run("a call another adapter's --harness names isn't this one's", func(t *testing.T) {
		if _, err := HookRecord(a, "linux", delivery(event.HarnessCursor, Walked, claude)); err == nil {
			t.Error("no error")
		}
	})
}

func TestHookRecordTimes(t *testing.T) {
	claude := platform.Proc{PID: 10, Name: "claude", TTY: platform.TTYAttached}
	a := &fake{harness: event.HarnessClaudeCode, hosts: claudeHosts()}
	const sent = 1795193284378
	cases := []struct {
		name string
		edit func(d *inbox.Delivery)
		want int64
	}{
		{name: "a call keeps the sender's own stamp when it is no later than the agent took it in",
			edit: func(d *inbox.Delivery) { d.Received.Wall = d.Sent.Add(time.Minute) }, want: sent},
		{name: "a call stamped after the agent took it in gets the agent's stamp",
			edit: func(d *inbox.Delivery) { d.Received.Wall = d.Sent.Add(-time.Hour) }, want: sent - 3_600_000},
		{name: "a spooled call stamped after its drain gets the drain's stamp",
			edit: func(d *inbox.Delivery) {
				d.Via, d.Walk, d.PeerPID = inbox.ViaInbox, nil, 0
				d.Received.Wall = d.Sent.Add(-time.Hour)
			}, want: sent - 3_600_000},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := delivery(event.HarnessClaudeCode, Walked, claude)
			tc.edit(&d)
			raw, err := HookRecord(a, "linux", d)
			if err != nil {
				t.Fatal(err)
			}
			if raw.Arrival.T != tc.want || raw.Arrival.BootID == "" {
				t.Errorf("t %d boot %q, want t %d", raw.Arrival.T, raw.Arrival.BootID, tc.want)
			}
		})
	}
}

func TestKeys(t *testing.T) {
	k := FixtureKeys()
	const sid = "617e16a3-ddb4-44ca-af68-72cf58ed1a34"
	// The vectors were computed apart from this code, with Python's hmac,
	// from ledger-v1.md section 3's formulas.
	cases := []struct {
		name, got, want string
	}{
		{"a session key is the hook path's: s and 32 hex digits of the HMAC over its domain and id",
			k.Session(sid), "s99375ed85934f6e841690aed815dead1"},
		{"a turn key is t and the HMAC over its domain, the session's id and the turn's",
			k.Turn(sid, "42d241bb-6c23-458c-917f-eec07efb607a"), "t17d6a1183b68c111c8277953cbae0109"},
		{"an agent key is a and the HMAC over its domain, the session's id and the subagent's",
			k.Agent(sid, "a627b317dcd2e5387"), "a18edd018233f21d701ec5411b03a6e4a"},
		{"a cron key is c and the HMAC over its domain, the session's id and the scheduled prompt's prefix",
			k.Cron(sid, "Reply with the word tick and nothing else."), "c81715e31a20c0bb3f23196f4f3b0e3c5"},
		{"the memory key is m and the HMAC over its domain alone", k.Memory(), "m3272157ed3aa96e2a5649916f4566b51"},
		{"an app key is r and the HMAC over its domain, the app, the kind of id and the id",
			k.App("t3_code", "thread", "9f2c6b1e-4a7d-4f0e-8c3b-5d1a2e7f6c40"), "r6292e3995a0cf0b6b005b77be255a608"},
		{"an app key of an id in two parts takes each part as part() frames it",
			k.App("t3_code", "record", "2b7e1f4c-0d9a-4e63-a58f-c1d2e3f40516", "2026-11-23T04:02:49.338Z"), "r67ac9ec76e54f3135ad8b76763cf9568"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.got != tc.want {
				t.Errorf("got %s, want %s", tc.got, tc.want)
			}
		})
	}
	t.Run("the session key is hookpath.SessionKey, which os.focus lists in hosted_sessions", func(t *testing.T) {
		if got, want := k.Session(sid), hookpath.SessionKey([]byte("pairtime adapters test keys v1.."), sid); got != want {
			t.Errorf("Session = %s, hookpath.SessionKey = %s", got, want)
		}
	})
	t.Run("one id gives different keys in different domains, and one pair of ids in different orders", func(t *testing.T) {
		seen := map[string]bool{}
		for _, key := range []string{k.Session("x"), k.Turn("x", ""), k.Agent("x", ""), k.Cron("x", ""),
			k.Turn("", "x"), k.Turn("x", "y"), k.Turn("y", "x"), k.Turn("xy", ""), k.Memory(), k.Session("memory"),
			k.App("x", ""), k.App("x", "y"), k.App("x", "thread", "y"), k.App("x", "message", "y"), k.App("x", "thread", "y", "")} {
			if seen[key[1:]] {
				t.Errorf("%s repeats", key)
			}
			seen[key[1:]] = true
		}
	})
	t.Run("NewKeys refuses a K_session that isn't 32 bytes, the nil one the agent has before its keystore opens included", func(t *testing.T) {
		for _, secret := range [][]byte{nil, {}, make([]byte, 16), make([]byte, 33)} {
			func() {
				defer func() {
					if recover() == nil {
						t.Errorf("NewKeys took a %d-byte secret", len(secret))
					}
				}()
				NewKeys(secret)
			}()
		}
		if k := NewKeys(make([]byte, hookpath.SessionSecretLen)); k == nil {
			t.Error("NewKeys refused a 32-byte secret")
		}
	})
	t.Run("Keys never prints its secret", func(t *testing.T) {
		for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%x"} {
			if s := fmt.Sprintf(verb, k); strings.Contains(s, "pairtime adapters") || strings.Contains(s, hex.EncodeToString([]byte("pairtime"))) {
				t.Errorf("%s shows the secret: %s", verb, s)
			}
		}
	})
}

func TestParseParents(t *testing.T) {
	cases := []struct {
		name string
		line string
		want platform.Proc
	}{
		{"a process with a terminal", "3467805 pts/11 claude", platform.Proc{PID: 3467805, Name: "claude", TTY: platform.TTYAttached}},
		{"a process title is named by the part before its colon", "3466313 ? tmux: server",
			platform.Proc{PID: 3466313, Name: "tmux", Alias: "tmux: server", TTY: platform.TTYNone}},
		{"a shell script is sh, named by its file", "3471082 ? allow-once.sh",
			platform.Proc{PID: 3471082, Name: "sh", Alias: "allow-once.sh", TTY: platform.TTYNone}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parseParents([]byte(tc.line + "\n"))
			if len(got) != 1 || got[0] != tc.want {
				t.Errorf("parseParents = %+v, want %+v", got, tc.want)
			}
		})
	}
	t.Run("each process's parent is the next line's", func(t *testing.T) {
		got := parseParents([]byte("3 pts/1 claude\n2 pts/1 bash\n1 ? systemd\n"))
		if len(got) != 3 || got[0].PPID != 2 || got[1].PPID != 1 || got[2].PPID != 0 {
			t.Errorf("parseParents = %+v", got)
		}
	})
}

func TestNormalize(t *testing.T) {
	ev := func(session, turn string) Replayed {
		return Replayed{From: "000001", Record: Record{Event: event.Event{
			Envelope: event.Envelope{EventID: "x", Seq: 9, DeviceID: "d", T: 1795193284378, Mono: 5, BootID: "b",
				Source: event.SourceHook, Via: event.ViaHook, Harness: event.HarnessClaudeCode, SessionKey: session},
			Data: &event.AgentStop{TurnKey: turn, StopReason: event.StopEndTurn,
				PendingCrons: []event.PendingCron{{PrefixHash: "c_" + turn, Schedule: "* * * * *"}}}}}}
	}
	lines, err := Normalize([]Replayed{ev("s_a", "t_1"), ev("s_b", "t_1"), ev("s_a", "t_2")})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		`{"data":{"pending_crons":[{"prefix_hash":"cron-1","schedule":"* * * * *"}],"stop_hook_active":false,"stop_reason":"end_turn","turn_key":"turn-1"},"harness":"claude_code","kind":"agent.stop","session_key":"session-1","source":"hook","t":1795193284378,"via":"hook"}`,
		`{"data":{"pending_crons":[{"prefix_hash":"cron-1","schedule":"* * * * *"}],"stop_hook_active":false,"stop_reason":"end_turn","turn_key":"turn-1"},"harness":"claude_code","kind":"agent.stop","session_key":"session-2","source":"hook","t":1795193284378,"via":"hook"}`,
		`{"data":{"pending_crons":[{"prefix_hash":"cron-2","schedule":"* * * * *"}],"stop_hook_active":false,"stop_reason":"end_turn","turn_key":"turn-2"},"harness":"claude_code","kind":"agent.stop","session_key":"session-1","source":"hook","t":1795193284378,"via":"hook"}`,
	}
	t.Run("keys become symbols numbered by first appearance in their family, and the agent's and the replay's own fields go", func(t *testing.T) {
		for i, l := range lines {
			var m any
			_ = json.Unmarshal(l.Event, &m)
			got, _ := json.Marshal(m)
			if string(got) != want[i] {
				t.Errorf("record %d:\n got  %s\n want %s", i+1, got, want[i])
			}
		}
	})
	t.Run("Diff finds a changed record and a missing one", func(t *testing.T) {
		if d := Diff(lines, lines, 10); len(d) != 0 {
			t.Errorf("Diff of a replay with itself: %v", d)
		}
		changed := append([]Line(nil), lines[:2]...)
		changed[0].From = "000002"
		if d := Diff(changed, lines, 10); len(d) != 2 {
			t.Errorf("Diff = %v", d)
		}
	})
	t.Run("Diff says a record's text, command and edits differ by length, never what they hold, and DiffContent shows them", func(t *testing.T) {
		secret := "my prompt about my friend"
		got := append([]Line(nil), lines[:1]...)
		got[0].Text, got[0].Command = &secret, "cat /home/teen/diary.txt"
		got[0].Edits = []LineEdit{{Path: "/home/teen/diary.txt", NewStart: 1, NewLines: 1}}
		d := Diff(got, lines[:1], 10)
		if len(d) != 1 {
			t.Fatalf("Diff = %v", d)
		}
		for _, want := range []string{"text differs: 25 chars vs none", "command differs: 24 vs 0 chars", "edits differ: 1 vs 0 hunks"} {
			if !strings.Contains(d[0], want) {
				t.Errorf("Diff = %s, want %q in it", d[0], want)
			}
		}
		if strings.Contains(d[0], "friend") || strings.Contains(d[0], "diary") {
			t.Errorf("Diff shows content: %s", d[0])
		}
		if c := DiffContent(got, lines[:1], 10); len(c) != 1 || !strings.Contains(c[0], "friend") || !strings.Contains(c[0], "diary") {
			t.Errorf("DiffContent = %v", c)
		}
	})
}

func TestConformProvenance(t *testing.T) {
	expected := `{"from":"000001","event":{"data":{"reason":"other"},"harness":"claude_code","kind":"session.end","session_key":"session-1","source":"hook","t":1795193284378,"via":"hook"}}` + "\n"
	captures := func(synthetic string) fstest.MapFS {
		return fstest.MapFS{
			"1.0.0/linux/PROVENANCE.md":            {Data: []byte("---\nharness: claude_code\nsynthetic: " + synthetic + "\n---\n")},
			"1.0.0/linux/cli/hooks/000001/argv":    {Data: []byte("/run\nStop\n")},
			"1.0.0/linux/cli/hooks/000001/stdin":   {Data: []byte(`{"session_id":"x","hook_event_name":"Stop"}`)},
			"1.0.0/linux/cli/hooks/000001/time":    {Data: []byte("unix_ns=1795193284378000000\n")},
			"1.0.0/linux/cli/hooks/000001/parents": {Data: []byte("10 pts/1 claude\n9 pts/1 bash\n8 ? tmux: server\n")},
		}
	}
	mk := func() Adapter {
		return &fake{harness: event.HarnessClaudeCode, hosts: claudeHosts(),
			fixture: fstest.MapFS{"1.0.0/linux/cli/expected.jsonl": {Data: []byte(expected)}}}
	}
	t.Run("a real capture replays to its expected records", func(t *testing.T) {
		results, err := Conform(mk, captures("false"))
		if err != nil {
			t.Fatal(err)
		}
		if len(results) != 1 || !results[0].OK() || results[0].Synthetic {
			t.Errorf("results %+v", results)
		}
	})
	t.Run("an adapter whose only fixtures are synthetic fails the conformance runner", func(t *testing.T) {
		if _, err := Conform(mk, captures("true")); err == nil {
			t.Error("Conform passed an adapter with synthetic fixtures alone")
		}
	})
	t.Run("a captured run with no expected records fails, so no capture passes unchecked", func(t *testing.T) {
		c := captures("false")
		for name, f := range captures("false") {
			if rest, ok := strings.CutPrefix(name, "1.0.0/linux/cli/"); ok {
				c["1.0.0/linux/vscode/"+rest] = f
			}
		}
		results, err := Conform(mk, c)
		if err != nil {
			t.Fatal(err)
		}
		if len(results) != 2 || results[0].Run != "1.0.0/linux/cli" || !results[0].OK() ||
			results[1].Run != "1.0.0/linux/vscode" || !errors.Is(results[1].Err, ErrNoExpected) || results[1].OK() {
			t.Errorf("results %+v", results)
		}
	})
	t.Run("a captured run whose pending.md says why its expected records wait is reported pending, not replayed or failed", func(t *testing.T) {
		c := captures("false")
		for name, f := range captures("false") {
			if rest, ok := strings.CutPrefix(name, "1.0.0/linux/cli/"); ok {
				c["1.0.0/linux/vscode/"+rest] = f
			}
		}
		c["1.0.0/linux/vscode/"+PendingFile] = &fstest.MapFile{Data: []byte("The oracle can't class VS Code's parents yet.\n")}
		c["1.0.0/linux/cli/"+PendingFile] = &fstest.MapFile{Data: []byte("Written before its expected records.\n")}
		results, err := Conform(mk, c)
		if err != nil {
			t.Fatal(err)
		}
		if len(results) != 2 || !results[0].OK() || results[0].Pending ||
			!results[1].Pending || results[1].Err != nil || results[1].Records != 0 || results[1].OK() {
			t.Errorf("results %+v", results)
		}
	})
	t.Run("a capture whose provenance doesn't say counts as synthetic", func(t *testing.T) {
		c := captures("false")
		c["1.0.0/linux/PROVENANCE.md"] = &fstest.MapFile{Data: []byte("# no front matter\n")}
		if _, err := Conform(mk, c); err == nil {
			t.Error("Conform passed a capture with no provenance front matter")
		}
	})
}
