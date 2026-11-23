// SPDX-License-Identifier: AGPL-3.0-or-later

package wakacompat

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// captures is wakatime-cli v2.28.4's requests, captured in CI.
const captures = "../../testdata/fixtures/wakatime/2.28.4/linux"

func ptr[T any](v T) *T { return &v }

func TestParse(t *testing.T) {
	editorUA := "wakatime/v2.28.4 (linux-7.0.0-1020-azure-unknown) go1.27.3 vscode/1.106.3 vscode-wakatime/25.5.1"
	cases := []struct {
		name    string
		in      string
		want    Heartbeat
		refused string
	}{
		{
			name: "an editor heartbeat as wakatime-cli sends it reads every field",
			in:   `{"branch":"master","cursorpos":4,"entity":"/home/capture/pairtime-capture/projects/dual-send/test_wordcount.py","type":"file","human_line_changes":1,"is_write":true,"language":"Python","lineno":12,"lines":15,"dependencies":["unittest","wordcount"],"project":"dual-send","project_root_count":6,"time":1795442819.873664,"user_agent":"` + editorUA + `"}`,
			want: Heartbeat{
				Entity: "/home/capture/pairtime-capture/projects/dual-send/test_wordcount.py", Type: "file", Time: 1795442819.873664,
				Project: ptr("dual-send"), Branch: ptr("master"), Language: ptr("Python"), Dependencies: []string{"unittest", "wordcount"},
				Lines: ptr[int64](15), LineNo: ptr[int64](12), CursorPos: ptr[int64](4), ProjectRootCount: ptr[int64](6),
				IsWrite: ptr(true), UserAgent: editorUA, HumanLineChanges: ptr[int64](1),
			},
		},
		{
			name: "a transcript-derived heartbeat keeps its AI fields",
			in:   `{"ai_line_changes":5,"ai_cached_input_tokens":38151,"ai_session":"617e16a3-ddb4-44ca-af68-72cf58ed1a34","ai_input_tokens":923,"ai_output_tokens":626,"ai_prompt_length":53,"branch":"master","category":"ai coding","entity":"/home/capture/pairtime-capture/projects/cli/wordcount.py","type":"file","is_write":true,"language":"Python","lines":7,"project":"cli","project_root_count":6,"time":1795193390.401,"user_agent":"wakatime/v2.28.4 (linux-7.0.0-1020-azure-unknown) go1.27.3 opus/5-5 claude-code/2.1.327","ai_subscription_plan":"max"}`,
			want: Heartbeat{
				Entity: "/home/capture/pairtime-capture/projects/cli/wordcount.py", Type: "file", Category: "ai coding", Time: 1795193390.401,
				Project: ptr("cli"), Branch: ptr("master"), Language: ptr("Python"), Lines: ptr[int64](7), ProjectRootCount: ptr[int64](6),
				IsWrite: ptr(true), UserAgent: "wakatime/v2.28.4 (linux-7.0.0-1020-azure-unknown) go1.27.3 opus/5-5 claude-code/2.1.327",
				AISession: "617e16a3-ddb4-44ca-af68-72cf58ed1a34", AISubscriptionPlan: "max", AIPromptLength: 53, AILineChanges: ptr[int64](5),
				AIInputTokens: 923, AIOutputTokens: 626, AICachedInputTokens: 38151,
			},
		},
		{
			name: "an empty project and branch are kept apart from none, as wakatime-cli sends them for an app",
			in:   `{"branch":"","category":"running tests","entity":"terminal","type":"app","project":"","time":1795442793.569615}`,
			want: Heartbeat{Entity: "terminal", Type: "app", Category: "running tests", Time: 1795442793.569615, Project: ptr(""), Branch: ptr("")},
		},
		{
			name: "a number written as a string reads as the number",
			in:   `{"entity":"a.go","time":"1795442779.5","lineno":"12"}`,
			want: Heartbeat{Entity: "a.go", Time: 1795442779.5, LineNo: ptr[int64](12)},
		},
		{
			name: "an integer field takes a whole float and leaves out a fraction",
			in:   `{"entity":"a.go","time":1795442779,"lineno":12.0,"cursorpos":3.5}`,
			want: Heartbeat{Entity: "a.go", Time: 1795442779, LineNo: ptr[int64](12)},
		},
		{
			name: "a boolean written as a string reads as the boolean",
			in:   `{"entity":"a.go","time":1795442779,"is_write":"true"}`,
			want: Heartbeat{Entity: "a.go", Time: 1795442779, IsWrite: ptr(true)},
		},
		{
			name: "dependencies as one comma-separated string become the list",
			in:   `{"entity":"a.go","time":1795442779,"dependencies":"fmt, os,,strings"}`,
			want: Heartbeat{Entity: "a.go", Time: 1795442779, Dependencies: []string{"fmt", "os", "strings"}},
		},
		{
			name: "plugin stands in for a missing user_agent, as Hackatime takes it",
			in:   `{"entity":"a.go","time":1795442779,"plugin":"vim/9.1 vim-wakatime/11.0"}`,
			want: Heartbeat{Entity: "a.go", Time: 1795442779, UserAgent: "vim/9.1 vim-wakatime/11.0"},
		},
		{
			name: "an unknown field is ignored and a field of the wrong type is left out",
			in:   `{"entity":"a.go","time":1795442779,"machine":"x","lines":"many","is_write":1,"project":7,"dependencies":["os",3]}`,
			want: Heartbeat{Entity: "a.go", Time: 1795442779, Dependencies: []string{"os"}},
		},
		{
			name: "a time in milliseconds is read as seconds",
			in:   `{"entity":"a.go","time":1795442779500}`,
			want: Heartbeat{Entity: "a.go", Time: 1795442779.5},
		},
		{name: "an array isn't a heartbeat", in: `[{"entity":"a.go","time":1795442779}]`, refused: InvalidHeartbeat},
		{name: "null isn't a heartbeat", in: `null`, refused: InvalidHeartbeat},
		{name: "a string isn't a heartbeat", in: `"a.go"`, refused: InvalidHeartbeat},
		{name: "a heartbeat with no entity is refused", in: `{"time":1795442779}`, refused: InvalidHeartbeat},
		{name: "a heartbeat whose entity isn't a string is refused", in: `{"entity":12,"time":1795442779}`, refused: InvalidHeartbeat},
		{name: "a heartbeat with no time is refused", in: `{"entity":"a.go"}`, refused: InvalidHeartbeatTime},
		{name: "a time that isn't a number is refused", in: `{"entity":"a.go","time":"yesterday"}`, refused: InvalidHeartbeatTime},
		{name: "NaN written as a string is no time", in: `{"entity":"a.go","time":"NaN"}`, refused: InvalidHeartbeatTime},
		{name: "a time outside Hackatime's window is refused", in: `{"entity":"a.go","time":42}`, refused: InvalidHeartbeatTime},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := Parse([]byte(c.in))
			if c.refused != "" {
				var r *Refusal
				if !errors.As(err, &r) || r.Type != c.refused {
					t.Fatalf("Parse = %+v, %v; want a %s refusal", got, err, c.refused)
				}
				if strings.Contains(r.Message, "a.go") {
					t.Errorf("the refusal %q repeats the entity", r.Message)
				}
				return
			}
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("Parse =\n%+v\nwant\n%+v", got, c.want)
			}
		})
	}
}

func TestSeconds(t *testing.T) {
	cases := []struct {
		name string
		in   float64
		want float64
		ok   bool
	}{
		{"epoch seconds in the window are kept", 1795442779.25, 1795442779.25, true},
		{"the window's first second is in it", 1e9, 1e9, true},
		{"the window's end is out of it", 2e9, 0, false},
		{"milliseconds are scaled to seconds", 1795442779250, 1795442779.25, true},
		{"microseconds are scaled to seconds", 1795442779250000, 1795442779.25, true},
		{"nanoseconds are scaled to seconds", 1795442779250000000, 1795442779.25, true},
		{"seconds from 1601 are moved to 1970", 1795442779 + windowsEpochOffset, 1795442779, true},
		{"a time before 2001 is refused", 999999999, 0, false},
		{"0 is refused", 0, 0, false},
		{"a negative time is refused", -1795442779, 0, false},
		{"NaN is refused", math.NaN(), 0, false},
		{"infinity is refused", math.Inf(1), 0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := Seconds(c.in)
			if ok != c.ok || (ok && math.Abs(got-c.want) > 1e-6) {
				t.Errorf("Seconds(%v) = %v, %v; want %v, %v", c.in, got, ok, c.want, c.ok)
			}
		})
	}
}

// capturedHeartbeats returns the heartbeats of every request a run's
// stand-in pairtime took, in order, as raw JSON.
func capturedHeartbeats(t *testing.T, run string) [][]json.RawMessage {
	t.Helper()
	metas, err := filepath.Glob(filepath.Join(captures, run, "requests", "*.meta"))
	if err != nil || len(metas) == 0 {
		t.Fatalf("no captured requests for %s: %v", run, err)
	}
	var out [][]json.RawMessage
	for _, m := range metas {
		meta, err := os.ReadFile(m)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Contains(meta, []byte("\nserver=pairtime\n")) || !bytes.Contains(meta, []byte("\nmethod=POST\n")) {
			continue
		}
		body, err := os.ReadFile(strings.TrimSuffix(m, ".meta") + ".json")
		if err != nil {
			t.Fatal(err)
		}
		var items []json.RawMessage
		if err := json.Unmarshal(body, &items); err != nil {
			t.Fatalf("%s: %v", m, err)
		}
		out = append(out, items)
	}
	return out
}

// TestCapturedHeartbeats reads every heartbeat wakatime-cli sent and
// writes it back out: a field Parse doesn't read, or reads wrong, comes
// back missing or changed.
func TestCapturedHeartbeats(t *testing.T) {
	for _, run := range []string{"dual-send", "replay", "ai-sync", "local-only"} {
		t.Run("every heartbeat of the "+run+" run reads and writes back unchanged", func(t *testing.T) {
			n := 0
			for _, req := range capturedHeartbeats(t, run) {
				for _, raw := range req {
					n++
					h, err := Parse(raw)
					if err != nil {
						t.Fatalf("Parse(%s): %v", raw, err)
					}
					back, err := json.Marshal(h)
					if err != nil {
						t.Fatal(err)
					}
					var want, got map[string]any
					if err := json.Unmarshal(raw, &want); err != nil {
						t.Fatal(err)
					}
					if err := json.Unmarshal(back, &got); err != nil {
						t.Fatal(err)
					}
					if !reflect.DeepEqual(got, want) {
						t.Errorf("wrote back\n%s\nfor\n%s", back, raw)
					}
				}
			}
			if n == 0 {
				t.Fatal("the run sent pairtime no heartbeats")
			}
		})
	}
}

func TestKey(t *testing.T) {
	replay := capturedHeartbeats(t, "replay")
	if len(replay) != 3 {
		t.Fatalf("the replay run sent pairtime %d requests, want 3", len(replay))
	}
	key := func(t *testing.T, raw string) Key {
		t.Helper()
		h, err := Parse([]byte(raw))
		if err != nil {
			t.Fatal(err)
		}
		return h.Key()
	}
	base := `{"entity":"a.go","type":"file","time":1795442779.25,"lineno":3,"is_write":false,"ai_line_changes":0}`
	cases := []struct {
		name string
		a, b string
		same bool
	}{
		{"the heartbeat wakatime-cli replayed after a failed send has its first send's key", string(replay[0][0]), string(replay[2][0]), true},
		{"the next heartbeat has another key", string(replay[0][0]), string(replay[1][0]), false},
		{"key order doesn't change the key", base, `{"ai_line_changes":0,"is_write":false,"lineno":3,"time":1795442779.25,"type":"file","entity":"a.go"}`, true},
		{"a number's spelling doesn't change the key", base, `{"entity":"a.go","type":"file","time":"1.79544277925e9","lineno":3.0,"is_write":"false","ai_line_changes":0}`, true},
		{"an unknown field doesn't change the key", base, `{"entity":"a.go","type":"file","time":1795442779.25,"lineno":3,"is_write":false,"ai_line_changes":0,"machine":"x"}`, true},
		{"a time in milliseconds keys as the same time in seconds", base, `{"entity":"a.go","type":"file","time":1795442779250,"lineno":3,"is_write":false,"ai_line_changes":0}`, true},
		{"another time is another heartbeat", base, strings.Replace(base, "779.25", "779.26", 1), false},
		{"another entity is another heartbeat", base, strings.Replace(base, "a.go", "b.go", 1), false},
		{"another line is another heartbeat", base, strings.Replace(base, `"lineno":3`, `"lineno":4`, 1), false},
		{"a write is another heartbeat", base, strings.Replace(base, `"is_write":false`, `"is_write":true`, 1), false},
		{"line changes left out are kept apart from 0", base, strings.Replace(base, `,"ai_line_changes":0`, "", 1), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a, b := key(t, c.a), key(t, c.b)
			if (a == b) != c.same {
				t.Errorf("keys %s and %s: same = %v, want %v", a, b, a == b, c.same)
			}
		})
	}
}
