// SPDX-License-Identifier: AGPL-3.0-or-later

package wakatime

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/adapters"
	"github.com/some-du6e/pairtime/internal/agent/peer"
	"github.com/some-du6e/pairtime/pkg/event"
	"github.com/some-du6e/pairtime/pkg/wakacompat"
)

// expected holds the records each captured run makes, as normalize
// writes them.
const expected = "testdata/2.28.4/linux"

// The peer check's results the tests hand the bridge.
var (
	passed     = peer.Result{Reason: peer.Passed, PID: 4242, Editor: 1}
	notCLI     = peer.Result{Reason: peer.NotWakaTime, PID: 4343, Editor: -1}
	noOwner    = peer.Result{Reason: peer.NoOwner, Editor: -1}
	testClock  = Clock{T: capturedNow.UnixMilli(), Mono: 9_000_000, BootID: "b_test"}
	scriptUA   = "python-requests/2.32.3"
	pluginUA   = "wakatime/v2.28.4 (linux-7.0.0-1020-azure-unknown) go1.27.3 vscode/1.106.3 vscode-wakatime/25.5.1"
	claudeUA   = "wakatime/v2.28.4 (linux-7.0.0-1020-azure-unknown) go1.27.3 opus/5-5 claude-code/2.1.327"
	promptUA   = "wakatime/v2.28.4 (linux-7.0.0-1020-azure-unknown) go1.27.3 claude-code/2.1.327"
	projectDir = "/home/capture/pairtime-capture/projects/cli"
)

// tagged is a record with the captured heartbeat it came from.
type tagged struct {
	from string
	rec  adapters.Record
}

// runCapture sends a run's captured requests through an endpoint whose
// Accept is a bridge, as the agent will wire them, with the peer check
// giving verdict, and returns the records with where each came from.
func runCapture(t *testing.T, run string, verdict peer.Result) ([]tagged, *Bridge) {
	t.Helper()
	keys := loadKeys(t)
	br := NewBridge(BridgeOptions{Options: adapters.FixtureOptions()})
	var out []tagged
	var from map[string]string
	e := newEndpoint(t, nil, Options{
		Key:  keys.Pairtime,
		Peer: func(*http.Request) peer.Result { return verdict },
		Accept: func(b Batch) error {
			for _, r := range br.Records(b, testClock) {
				k, _, _ := strings.Cut(r.LogKey, ".")
				out = append(out, tagged{from: from[k], rec: r})
			}
			return nil
		},
	})
	for _, c := range loadRun(t, run) {
		if c.meta["method"] != http.MethodPost {
			continue
		}
		// Each heartbeat's Key, to tell which one a record came from.
		var items []json.RawMessage
		if err := json.Unmarshal(c.body, &items); err != nil {
			t.Fatal(err)
		}
		from = map[string]string{}
		for i, raw := range items {
			h, err := wakacompat.Parse(raw)
			if err != nil {
				t.Fatal(err)
			}
			from[h.Key().String()] = fmt.Sprintf("%s#%d", strings.TrimSuffix(c.name, ".meta")+".json", i)
		}
		if w := serve(e, c.httpRequest(keys.Pairtime)); w.Code != http.StatusCreated {
			t.Fatalf("%s: status %d: %s", c.name, w.Code, w.Body)
		}
	}
	return out, br
}

// keyFamilies names the symbol each keyed field gets in normalize, as the
// conformance runner names them (internal/adapters, Normalize), with
// editor.activity's file_hash in the files' family, so an agent's edit and
// a person's edit of one file get one symbol.
var keyFamilies = map[string]string{
	"session_key": "session", "project_key": "project", "turn_key": "turn",
	"files": "file", "file_hash": "file",
}

// normalize writes records as the expected files hold them: where each
// came from, and its event with the fields the agent assigns dropped and
// each keyed value replaced by a symbol, numbered by family in order of
// first appearance, so the files hold no key.
func normalize(t *testing.T, recs []tagged) []any {
	t.Helper()
	syms := map[string]map[string]string{}
	of := func(fam, v string) string {
		m := syms[fam]
		if m == nil {
			m = map[string]string{}
			syms[fam] = m
		}
		if s, ok := m[v]; ok {
			return s
		}
		m[v] = fam + "-" + strconv.Itoa(len(m)+1)
		return m[v]
	}
	var walk func(v any)
	walk = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			keys := make([]string, 0, len(x))
			for k := range x {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				fam, keyed := keyFamilies[k]
				switch val := x[k].(type) {
				case string:
					if keyed && val != "" && val != event.NoSession {
						x[k] = of(fam, val)
					}
				case []any:
					for i, el := range val {
						if s, ok := el.(string); ok && keyed {
							val[i] = of(fam, s)
						} else {
							walk(el)
						}
					}
				default:
					walk(val)
				}
			}
		case []any:
			for _, el := range x {
				walk(el)
			}
		}
	}
	out := make([]any, 0, len(recs))
	for _, r := range recs {
		b, err := json.Marshal(r.rec.Event)
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]any
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatal(err)
		}
		for _, k := range []string{"event_id", "seq", "device_id", "mono", "boot_id"} {
			delete(m, k)
		}
		walk(m)
		out = append(out, map[string]any{"from": r.from, "event": m})
	}
	return out
}

func readExpected(t *testing.T, run string) []any {
	t.Helper()
	f, err := os.Open(filepath.Join(expected, run+".jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var out []any
	sc := bufio.NewScanner(f)
	sc.Buffer(nil, 1<<20)
	for sc.Scan() {
		if len(bytes.TrimSpace(sc.Bytes())) == 0 {
			continue
		}
		var v any
		if err := json.Unmarshal(sc.Bytes(), &v); err != nil {
			t.Fatalf("%s.jsonl: %v", run, err)
		}
		out = append(out, v)
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestCapturedRuns(t *testing.T) {
	for _, run := range []string{"dual-send", "replay", "ai-sync", "local-only"} {
		t.Run("the "+run+" run's heartbeats make the expected records", func(t *testing.T) {
			recs, _ := runCapture(t, run, passed)
			got, want := normalize(t, recs), readExpected(t, run)
			for i := 0; i < max(len(got), len(want)); i++ {
				var g, w any
				if i < len(got) {
					g = got[i]
				}
				if i < len(want) {
					w = want[i]
				}
				if !reflect.DeepEqual(g, w) {
					gb, _ := json.Marshal(g)
					wb, _ := json.Marshal(w)
					t.Errorf("record %d:\n got %s\nwant %s", i, gb, wb)
				}
			}
		})
	}
}

func TestCapturedRecordsValidate(t *testing.T) {
	for _, run := range []string{"dual-send", "replay", "ai-sync", "local-only"} {
		t.Run("every record of the "+run+" run passes pkg/event's checks and names no path", func(t *testing.T) {
			recs, _ := runCapture(t, run, passed)
			if len(recs) == 0 {
				t.Fatal("no records")
			}
			for i, r := range recs {
				ev := r.rec.Event
				ev.EventID = event.NewEventID(time.UnixMilli(ev.T))
				ev.DeviceID = "d_test"
				ev.Seq = uint64(i + 1)
				if err := ev.Validate(); err != nil {
					t.Errorf("record %d (%s): %v", i, r.from, err)
				}
				b, err := json.Marshal(ev)
				if err != nil {
					t.Fatal(err)
				}
				for _, leak := range []string{"/home/capture", "wordcount", "hook-allowed", "notes.txt", "pairtime-capture"} {
					if bytes.Contains(b, []byte(leak)) {
						t.Errorf("record %d (%s) holds %q: %s", i, r.from, leak, b)
					}
				}
				if r.rec.Text != nil || r.rec.Command != "" || r.rec.Edits != nil {
					t.Errorf("record %d (%s) carries content", i, r.from)
				}
			}
		})
	}
}

// hb is a heartbeat for the table tests.
func hb(t *testing.T, s string) wakacompat.Heartbeat {
	t.Helper()
	h, err := wakacompat.Parse([]byte(s))
	if err != nil {
		t.Fatalf("Parse(%s): %v", s, err)
	}
	return h
}

// at is a time in the capture's day, sec seconds after 14:00 UTC, as a
// heartbeat's time and as Unix milliseconds.
func at(sec float64) (float64, int64) {
	base := float64(time.Date(2026, 11, 23, 14, 0, 0, 0, time.UTC).Unix())
	return base + sec, int64((base + sec) * 1000)
}

func editorHB(t *testing.T, file string, sec float64, extra string) wakacompat.Heartbeat {
	ts, _ := at(sec)
	return hb(t, fmt.Sprintf(`{"entity":%q,"type":"file","time":%v,"user_agent":%q,"project":"cli"%s}`, projectDir+"/"+file, ts, pluginUA, extra))
}

func agentHB(t *testing.T, file string, sec float64, extra string) wakacompat.Heartbeat {
	ts, _ := at(sec)
	return hb(t, fmt.Sprintf(`{"entity":%q,"type":"file","time":%v,"user_agent":%q,"project":"cli","category":"ai coding","ai_session":"617e16a3-ddb4-44ca-af68-72cf58ed1a34"%s}`, projectDir+"/"+file, ts, claudeUA, extra))
}

func kinds(recs []adapters.Record) string {
	var ks []string
	for _, r := range recs {
		s := string(r.Event.Kind()) + " " + string(r.Event.Via)
		if a, ok := r.Event.Data.(*event.EditorActivity); ok {
			s += " " + string(a.Act) + " " + string(a.EditSource)
		}
		ks = append(ks, s)
	}
	return strings.Join(ks, ", ")
}

func TestBridgeVia(t *testing.T) {
	cases := []struct {
		name    string
		verdict peer.Result
		h       func(t *testing.T) wakacompat.Heartbeat
		want    string
	}{
		{"an editor heartbeat that passed the peer check is via editor", passed,
			func(t *testing.T) wakacompat.Heartbeat {
				return editorHB(t, "wordcount.py", 0, `,"human_line_changes":1`)
			},
			"editor.activity editor edit unknown"},
		{"a post from a test script comes out editor_unverified", notCLI,
			func(t *testing.T) wakacompat.Heartbeat {
				ts, _ := at(0)
				return hb(t, fmt.Sprintf(`{"entity":%q,"type":"file","time":%v,"user_agent":%q,"human_line_changes":1}`, projectDir+"/wordcount.py", ts, scriptUA))
			},
			"editor.activity editor_unverified edit unknown"},
		{"a transcript heartbeat that passed the peer check gives bridge records", passed,
			func(t *testing.T) wakacompat.Heartbeat {
				return agentHB(t, "wordcount.py", 0, `,"ai_prompt_length":90,"ai_line_changes":5`)
			},
			"agent.tool bridge, prompt.submit bridge"},
		{"a transcript-shaped post from a test script gives bridge_unverified records", notCLI,
			func(t *testing.T) wakacompat.Heartbeat {
				ts, _ := at(0)
				return hb(t, fmt.Sprintf(`{"entity":%q,"type":"file","time":%v,"user_agent":%q,"ai_session":"s1","ai_prompt_length":90,"ai_line_changes":5}`, projectDir+"/wordcount.py", ts, scriptUA))
			},
			"agent.tool bridge_unverified, prompt.submit bridge_unverified"},
		{"a connection whose client the check couldn't find fails it", noOwner,
			func(t *testing.T) wakacompat.Heartbeat { return editorHB(t, "wordcount.py", 0, "") },
			"editor.activity editor_unverified navigate unknown"},
		{"with no peer check at all a heartbeat is unverified", peer.Result{},
			func(t *testing.T) wakacompat.Heartbeat {
				return agentHB(t, "wordcount.py", 0, `,"ai_prompt_length":12`)
			},
			"prompt.submit bridge_unverified"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			br := NewBridge(BridgeOptions{Options: adapters.FixtureOptions()})
			recs := br.Records(Batch{Heartbeats: []wakacompat.Heartbeat{c.h(t)}, Peer: c.verdict}, testClock)
			if got := kinds(recs); got != c.want {
				t.Errorf("records %q, want %q", got, c.want)
			}
		})
	}
}

func TestBridgeMapping(t *testing.T) {
	cases := []struct {
		name string
		hbs  func(t *testing.T) []wakacompat.Heartbeat
		want string
		// noFile says no editor.activity may name a file.
		noFile bool
	}{
		{"is_write makes a save, human_line_changes an edit, anything else a navigate",
			func(t *testing.T) []wakacompat.Heartbeat {
				return []wakacompat.Heartbeat{
					editorHB(t, "wordcount.py", 0, `,"is_write":true`),
					editorHB(t, "wordcount.py", 10, `,"human_line_changes":2`),
					editorHB(t, "wordcount.py", 20, `,"category":"debugging","lineno":4`),
				}
			},
			"editor.activity editor save unknown, editor.activity editor edit unknown, editor.activity editor navigate unknown", false},
		{"a category of ai coding doesn't make an editor heartbeat the agent's, since wakatime-cli relabels typing near agent activity",
			func(t *testing.T) []wakacompat.Heartbeat {
				return []wakacompat.Heartbeat{editorHB(t, "wordcount.py", 0, `,"category":"ai coding","human_line_changes":2`)}
			},
			"editor.activity editor edit unknown", false},
		{"a transcript heartbeat with only token counts makes no record",
			func(t *testing.T) []wakacompat.Heartbeat {
				return []wakacompat.Heartbeat{agentHB(t, "wordcount.py", 0, `,"ai_input_tokens":9772,"ai_output_tokens":143,"is_write":false`)}
			},
			"", false},
		{"a transcript heartbeat that changed no lines makes no agent.tool",
			func(t *testing.T) []wakacompat.Heartbeat {
				return []wakacompat.Heartbeat{agentHB(t, "wordcount.py", 0, `,"ai_prompt_length":43,"ai_line_changes":0,"is_write":true`)}
			},
			"prompt.submit bridge", false},
		{"a prompt's own heartbeat with no model token is transcript-derived all the same",
			func(t *testing.T) []wakacompat.Heartbeat {
				ts, _ := at(0)
				return []wakacompat.Heartbeat{hb(t, fmt.Sprintf(`{"entity":"Claude 2197af56","type":"app","time":%v,"user_agent":%q,"ai_session":"2197af56","ai_prompt_length":11,"project":"cli"}`, ts, promptUA))}
			},
			"prompt.submit bridge", false},
		{"a non-file heartbeat with human_line_changes is an edit with no file",
			func(t *testing.T) []wakacompat.Heartbeat {
				ts, _ := at(0)
				return []wakacompat.Heartbeat{hb(t, fmt.Sprintf(`{"entity":"terminal","type":"app","time":%v,"user_agent":%q,"category":"running tests","human_line_changes":3}`, ts, pluginUA))}
			},
			"editor.activity editor edit unknown", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			br := NewBridge(BridgeOptions{Options: adapters.FixtureOptions()})
			recs := br.Records(Batch{Heartbeats: c.hbs(t), Peer: passed}, testClock)
			if got := kinds(recs); got != c.want {
				t.Errorf("records %q, want %q", got, c.want)
			}
			for _, r := range recs {
				if a, ok := r.Event.Data.(*event.EditorActivity); ok && c.noFile && a.FileHash != "" {
					t.Errorf("file_hash %q, want none", a.FileHash)
				}
			}
		})
	}
}

func TestAgentEdits(t *testing.T) {
	const window = AgentEditWindow / 1000
	cases := []struct {
		name string
		// batches are sent in turn; the last batch's last record is checked.
		batches func(t *testing.T) [][]wakacompat.Heartbeat
		note    bool // NoteAgentEdit the agent's edit instead of sending it
		want    event.EditSource
	}{
		{"an editor edit 120 s after the agent's edit of the same file is the agent's",
			func(t *testing.T) [][]wakacompat.Heartbeat {
				return [][]wakacompat.Heartbeat{{agentHB(t, "a.py", 0, `,"ai_line_changes":5`), editorHB(t, "a.py", window, `,"human_line_changes":5`)}}
			}, false, event.EditSourceAgent},
		{"an editor edit 121 s after it isn't",
			func(t *testing.T) [][]wakacompat.Heartbeat {
				return [][]wakacompat.Heartbeat{{agentHB(t, "a.py", 0, `,"ai_line_changes":5`), editorHB(t, "a.py", window+1, `,"human_line_changes":5`)}}
			}, false, event.EditSourceUnknown},
		{"an editor edit just before the agent's edit, in the same batch, is the agent's too",
			func(t *testing.T) [][]wakacompat.Heartbeat {
				return [][]wakacompat.Heartbeat{{editorHB(t, "a.py", -30, `,"human_line_changes":5`), agentHB(t, "a.py", 0, `,"ai_line_changes":5`)}}
			}, false, event.EditSourceAgent},
		{"an agent edit from an earlier batch counts",
			func(t *testing.T) [][]wakacompat.Heartbeat {
				return [][]wakacompat.Heartbeat{{agentHB(t, "a.py", 0, `,"ai_line_changes":5`)}, {editorHB(t, "a.py", 30, `,"is_write":true`)}}
			}, false, event.EditSourceAgent},
		{"an agent edit of another file doesn't count",
			func(t *testing.T) [][]wakacompat.Heartbeat {
				return [][]wakacompat.Heartbeat{{agentHB(t, "b.py", 0, `,"ai_line_changes":5`), editorHB(t, "a.py", 30, `,"human_line_changes":5`)}}
			}, false, event.EditSourceUnknown},
		{"a navigation near the agent's edit is no edit, so it stays unknown",
			func(t *testing.T) [][]wakacompat.Heartbeat {
				return [][]wakacompat.Heartbeat{{agentHB(t, "a.py", 0, `,"ai_line_changes":5`), editorHB(t, "a.py", 30, "")}}
			}, false, event.EditSourceUnknown},
		{"an agent edit NoteAgentEdit was told of counts",
			func(t *testing.T) [][]wakacompat.Heartbeat {
				return [][]wakacompat.Heartbeat{{editorHB(t, "a.py", 30, `,"human_line_changes":5`)}}
			}, true, event.EditSourceAgent},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			opts := adapters.FixtureOptions()
			br := NewBridge(BridgeOptions{Options: opts})
			if c.note {
				proj, _ := opts.Project(projectDir)
				key, err := opts.Features.Path("a.py")
				if err != nil {
					t.Fatal(err)
				}
				_, t0 := at(0)
				br.NoteAgentEdit(proj.Key, key.String(), t0)
			}
			var last []adapters.Record
			for _, b := range c.batches(t) {
				last = br.Records(Batch{Heartbeats: b, Peer: passed}, testClock)
			}
			var got *event.EditorActivity
			for _, r := range last {
				if a, ok := r.Event.Data.(*event.EditorActivity); ok {
					got = a
				}
			}
			if got == nil {
				t.Fatalf("no editor.activity in %s", kinds(last))
			}
			if got.EditSource != c.want {
				t.Errorf("edit_source %s, want %s", got.EditSource, c.want)
			}
		})
	}
}

func TestBridgeKeys(t *testing.T) {
	opts := adapters.FixtureOptions()
	proj, _ := opts.Project(projectDir)
	wordcount, err := opts.Features.Path("wordcount.py")
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name  string
		hb    func(t *testing.T) wakacompat.Heartbeat
		check func(t *testing.T, recs []adapters.Record)
	}{
		{"a transcript heartbeat's session is the key the hook path gives its session id",
			func(t *testing.T) wakacompat.Heartbeat {
				return agentHB(t, "wordcount.py", 0, `,"ai_prompt_length":12`)
			},
			func(t *testing.T, recs []adapters.Record) {
				if want := opts.Keys.Session("617e16a3-ddb4-44ca-af68-72cf58ed1a34"); recs[0].Event.SessionKey != want {
					t.Errorf("session_key %s, want %s", recs[0].Event.SessionKey, want)
				}
				if recs[0].Event.Harness != event.HarnessClaudeCode || recs[0].Event.HarnessVersion != "2.1.327" {
					t.Errorf("harness %s %s, want claude_code 2.1.327", recs[0].Event.Harness, recs[0].Event.HarnessVersion)
				}
			}},
		{"a file is keyed relative to its project, as the agent's own edits are",
			func(t *testing.T) wakacompat.Heartbeat { return agentHB(t, "wordcount.py", 0, `,"ai_line_changes":3`) },
			func(t *testing.T, recs []adapters.Record) {
				tool := recs[0].Event.Data.(*event.AgentTool)
				if len(tool.Files) != 1 || tool.Files[0] != wordcount.String() || recs[0].Event.ProjectKey != proj.Key {
					t.Errorf("files %v in %s, want [%s] in %s", tool.Files, recs[0].Event.ProjectKey, wordcount, proj.Key)
				}
				if tool.LinesAdded != 3 || tool.LinesRemoved != 0 || tool.TStart != recs[0].Event.T || tool.TEnd != recs[0].Event.T {
					t.Errorf("tool %+v", tool)
				}
			}},
		{"a record's mono is the arrival's reading taken back to its time",
			func(t *testing.T) wakacompat.Heartbeat {
				return editorHB(t, "wordcount.py", float64(capturedNow.Unix()-time.Date(2026, 11, 23, 14, 0, 0, 0, time.UTC).Unix())-4, "")
			},
			func(t *testing.T, recs []adapters.Record) {
				if want := testClock.Mono - 4000; recs[0].Event.Mono != want || recs[0].Event.BootID != testClock.BootID {
					t.Errorf("mono %d boot %s, want %d %s", recs[0].Event.Mono, recs[0].Event.BootID, want, testClock.BootID)
				}
			}},
		{"a record that reached the agent long after its time carries no mono",
			func(t *testing.T) wakacompat.Heartbeat { return editorHB(t, "wordcount.py", 0, "") },
			func(t *testing.T, recs []adapters.Record) {
				if recs[0].Event.Mono != 0 {
					t.Errorf("mono %d, want 0", recs[0].Event.Mono)
				}
			}},
		{"a heartbeat with an edit and a prompt makes the edit first, and each record's LogKey names the heartbeat and the role",
			func(t *testing.T) wakacompat.Heartbeat {
				return agentHB(t, "wordcount.py", 0, `,"ai_prompt_length":12,"ai_line_changes":3`)
			},
			func(t *testing.T, recs []adapters.Record) {
				if len(recs) != 2 || !strings.HasSuffix(recs[0].LogKey, ".tool") || !strings.HasSuffix(recs[1].LogKey, ".prompt") ||
					strings.TrimSuffix(recs[0].LogKey, ".tool") != strings.TrimSuffix(recs[1].LogKey, ".prompt") {
					t.Errorf("log keys %q, %q", recs[0].LogKey, recs[len(recs)-1].LogKey)
				}
			}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			br := NewBridge(BridgeOptions{Options: opts})
			recs := br.Records(Batch{Heartbeats: []wakacompat.Heartbeat{c.hb(t)}, Peer: passed}, testClock)
			if len(recs) == 0 {
				t.Fatal("no records")
			}
			c.check(t, recs)
		})
	}
}

func TestBridgeProjects(t *testing.T) {
	ignored := adapters.FixtureOptions()
	inner := ignored.Project
	// The agent's resolver answers a folder out of pairtime with Ignored
	// and false (gitHost.project; adapters.Project.Ignored).
	ignored.Project = func(dir string) (adapters.Project, bool) {
		if strings.HasSuffix(dir, "/private") {
			return adapters.Project{Ignored: true}, false
		}
		return inner(dir)
	}
	cases := []struct {
		name  string
		opts  BridgeOptions
		hbs   func(t *testing.T) []wakacompat.Heartbeat
		check func(t *testing.T, br *Bridge, recs []adapters.Record)
	}{
		{"an app heartbeat takes its project from its session's files in the same batch, whatever their order",
			BridgeOptions{Options: adapters.FixtureOptions()},
			func(t *testing.T) []wakacompat.Heartbeat {
				ts, _ := at(0)
				return []wakacompat.Heartbeat{
					hb(t, fmt.Sprintf(`{"entity":"Claude x","type":"app","time":%v,"user_agent":%q,"ai_session":"617e16a3-ddb4-44ca-af68-72cf58ed1a34","ai_prompt_length":9}`, ts, promptUA)),
					agentHB(t, "wordcount.py", 1, `,"ai_line_changes":2`),
				}
			},
			func(t *testing.T, br *Bridge, recs []adapters.Record) {
				if recs[0].Event.ProjectKey == "" || recs[0].Event.ProjectKey != recs[1].Event.ProjectKey {
					t.Errorf("project keys %q and %q", recs[0].Event.ProjectKey, recs[1].Event.ProjectKey)
				}
			}},
		{"a heartbeat with no file takes the project its WakaTime name was seen with",
			BridgeOptions{Options: adapters.FixtureOptions()},
			func(t *testing.T) []wakacompat.Heartbeat {
				ts, _ := at(5)
				return []wakacompat.Heartbeat{
					editorHB(t, "wordcount.py", 0, ""),
					hb(t, fmt.Sprintf(`{"entity":"Claude y","type":"app","time":%v,"user_agent":%q,"ai_session":"other-session","ai_prompt_length":9,"project":"cli"}`, ts, promptUA)),
				}
			},
			func(t *testing.T, br *Bridge, recs []adapters.Record) {
				if recs[1].Event.ProjectKey == "" || recs[1].Event.ProjectKey != recs[0].Event.ProjectKey {
					t.Errorf("project keys %q and %q", recs[0].Event.ProjectKey, recs[1].Event.ProjectKey)
				}
				if names := br.ProjectNames(); len(names) != 1 || names["cli"] != recs[0].Event.ProjectKey {
					t.Errorf("ProjectNames %v, want cli for %s", names, recs[0].Event.ProjectKey)
				}
			}},
		{"a heartbeat in a folder out of pairtime makes no record",
			BridgeOptions{Options: ignored},
			func(t *testing.T) []wakacompat.Heartbeat {
				ts, _ := at(0)
				return []wakacompat.Heartbeat{
					hb(t, fmt.Sprintf(`{"entity":"/home/capture/private/diary.md","type":"file","time":%v,"user_agent":%q,"project":"private","human_line_changes":4}`, ts, pluginUA)),
					editorHB(t, "wordcount.py", 1, ""),
				}
			},
			func(t *testing.T, br *Bridge, recs []adapters.Record) {
				if len(recs) != 1 {
					t.Errorf("records %s, want the one outside the private folder", kinds(recs))
				}
				if _, ok := br.ProjectNames()["private"]; ok {
					t.Error("the private folder's name went into ProjectNames")
				}
			}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			br := NewBridge(c.opts)
			c.check(t, br, br.Records(Batch{Heartbeats: c.hbs(t), Peer: passed}, testClock))
		})
	}
}

// privateDir is a folder out of pairtime in TestBridgeExclusions.
const privateDir = "/home/capture/private"

func TestBridgeExclusions(t *testing.T) {
	const session = "617e16a3-ddb4-44ca-af68-72cf58ed1a34"
	opts := adapters.FixtureOptions()
	inner := opts.Project
	opts.Project = func(dir string) (adapters.Project, bool) {
		if dir == privateDir {
			return adapters.Project{Ignored: true}, false
		}
		return inner(dir)
	}
	tracked, _ := inner(projectDir)
	// fileIn is the session's agent edit of a file in dir, named project.
	fileIn := func(t *testing.T, dir, project string, sec float64) wakacompat.Heartbeat {
		ts, _ := at(sec)
		return hb(t, fmt.Sprintf(`{"entity":%q,"type":"file","time":%v,"user_agent":%q,"project":%q,"ai_session":%q,"ai_line_changes":2}`, dir+"/notes.md", ts, claudeUA, project, session))
	}
	// prompt is an app heartbeat of sess, named project when it isn't "".
	prompt := func(t *testing.T, sess, project string, sec float64) wakacompat.Heartbeat {
		ts, _ := at(sec)
		name := ""
		if project != "" {
			name = fmt.Sprintf(`,"project":%q`, project)
		}
		return hb(t, fmt.Sprintf(`{"entity":"Claude x","type":"app","time":%v,"user_agent":%q,"ai_session":%q,"ai_prompt_length":30%s}`, ts, promptUA, sess, name))
	}
	cases := []struct {
		name string
		// ignored is the agent's session test, by session key.
		ignored func(key string) bool
		batches func(t *testing.T) [][]wakacompat.Heartbeat
		// want is the kinds of the last batch's records, and project the
		// project they must be in, or "" for none.
		want    string
		project string
	}{
		{"an app prompt after its session's file heartbeat in a folder out of pairtime makes no record", nil,
			func(t *testing.T) [][]wakacompat.Heartbeat {
				return [][]wakacompat.Heartbeat{{fileIn(t, privateDir, "private", 0)}, {prompt(t, session, "", 10)}}
			}, "", ""},
		{"an app prompt in the same batch as its session's excluded file heartbeat makes no record", nil,
			func(t *testing.T) [][]wakacompat.Heartbeat {
				return [][]wakacompat.Heartbeat{{prompt(t, session, "", 0), fileIn(t, privateDir, "private", 1)}}
			}, "", ""},
		{"a session whose latest file heartbeat moved into a folder out of pairtime loses its project", nil,
			func(t *testing.T) [][]wakacompat.Heartbeat {
				return [][]wakacompat.Heartbeat{{fileIn(t, projectDir, "cli", 0)}, {fileIn(t, privateDir, "private", 10)}, {prompt(t, session, "", 20)}}
			}, "", ""},
		{"a session whose latest file heartbeat is in a tracked folder again takes that project", nil,
			func(t *testing.T) [][]wakacompat.Heartbeat {
				return [][]wakacompat.Heartbeat{{fileIn(t, privateDir, "private", 0)}, {fileIn(t, projectDir, "cli", 10)}, {prompt(t, session, "", 20)}}
			}, "prompt.submit bridge", tracked.Key},
		{"a name seen in a folder out of pairtime makes a prompt of another session that carries it no record", nil,
			func(t *testing.T) [][]wakacompat.Heartbeat {
				return [][]wakacompat.Heartbeat{{fileIn(t, privateDir, "shared", 0)}, {prompt(t, "other-session", "shared", 10)}}
			}, "", ""},
		{"a name seen in a folder out of pairtime and then in a tracked one maps to the tracked project", nil,
			func(t *testing.T) [][]wakacompat.Heartbeat {
				return [][]wakacompat.Heartbeat{{fileIn(t, privateDir, "shared", 0)}, {fileIn(t, projectDir, "shared", 5)}, {prompt(t, "other-session", "shared", 10)}}
			}, "prompt.submit bridge", tracked.Key},
		{"the session test drops an app prompt that has no file heartbeat, by the session's key",
			func(key string) bool { return key == opts.Keys.Session("kept-out") },
			func(t *testing.T) [][]wakacompat.Heartbeat {
				return [][]wakacompat.Heartbeat{{prompt(t, "kept-out", "cli", 0)}}
			}, "", ""},
		{"the session test drops the session's file heartbeats too",
			func(key string) bool { return key == opts.Keys.Session(session) },
			func(t *testing.T) [][]wakacompat.Heartbeat {
				return [][]wakacompat.Heartbeat{{fileIn(t, projectDir, "cli", 0), editorHB(t, "a.py", 1, "")}}
			}, "editor.activity editor navigate unknown", tracked.Key},
		{"the session test keeps the other sessions' prompts",
			func(key string) bool { return key == opts.Keys.Session("kept-out") },
			func(t *testing.T) [][]wakacompat.Heartbeat {
				return [][]wakacompat.Heartbeat{{editorHB(t, "a.py", 0, "")}, {prompt(t, "another", "cli", 10)}}
			}, "prompt.submit bridge", tracked.Key},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			br := NewBridge(BridgeOptions{Options: opts, SessionIgnored: c.ignored})
			var last []adapters.Record
			for _, b := range c.batches(t) {
				last = br.Records(Batch{Heartbeats: b, Peer: passed}, testClock)
			}
			if got := kinds(last); got != c.want {
				t.Fatalf("records %q, want %q", got, c.want)
			}
			for _, r := range last {
				if r.Event.ProjectKey != c.project {
					t.Errorf("%s in project %q, want %q", r.Event.Kind(), r.Event.ProjectKey, c.project)
				}
			}
			if _, ok := br.ProjectNames()["private"]; ok {
				t.Error("a name seen only in a folder out of pairtime is in ProjectNames")
			}
		})
	}
}

func TestBridgeFolders(t *testing.T) {
	file := func(t *testing.T, entity string) wakacompat.Heartbeat {
		ts, _ := at(0)
		return hb(t, fmt.Sprintf(`{"entity":%q,"type":"file","time":%v,"user_agent":%q}`, entity, ts, pluginUA))
	}
	app := func(t *testing.T) wakacompat.Heartbeat {
		ts, _ := at(0)
		return hb(t, fmt.Sprintf(`{"entity":"Claude x","type":"app","time":%v,"user_agent":%q,"ai_session":"s","ai_prompt_length":9}`, ts, promptUA))
	}
	cases := []struct {
		name string
		hbs  func(t *testing.T) []wakacompat.Heartbeat
		want []string
	}{
		{"each file heartbeat's folder is asked once, in the batch's order",
			func(t *testing.T) []wakacompat.Heartbeat {
				return []wakacompat.Heartbeat{file(t, "/p/b/x.py"), file(t, "/p/a/y.py"), file(t, "/p/b/z.py")}
			},
			[]string{"/p/b", "/p/a"}},
		{"a subfolder is asked as itself, for the git adapter to map to its repo",
			func(t *testing.T) []wakacompat.Heartbeat {
				return []wakacompat.Heartbeat{file(t, "/p/repo/src/deep/x.go")}
			},
			[]string{"/p/repo/src/deep"}},
		{"a heartbeat with no file asks about no folder",
			func(t *testing.T) []wakacompat.Heartbeat { return []wakacompat.Heartbeat{app(t)} },
			nil},
		{"a relative or remote path asks about no folder",
			func(t *testing.T) []wakacompat.Heartbeat {
				return []wakacompat.Heartbeat{file(t, "x.py"), file(t, "ssh://host/p/x.py")}
			},
			nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			br := NewBridge(BridgeOptions{Options: adapters.FixtureOptions()})
			if got := br.Folders(Batch{Heartbeats: c.hbs(t)}); !reflect.DeepEqual(got, c.want) {
				t.Errorf("Folders = %q, want %q", got, c.want)
			}
		})
	}
}

func TestHarnessOf(t *testing.T) {
	cases := []struct {
		name, ua    string
		want        event.Harness
		wantVersion string
	}{
		{"Claude Code's token names claude_code and its version", claudeUA, event.HarnessClaudeCode, "2.1.327"},
		{"a prompt's user agent with no model token names it too", promptUA, event.HarnessClaudeCode, "2.1.327"},
		{"the agent's token is the last one, after an editor's in an IDE session",
			"wakatime/v2.28.4 (darwin-25.1.0-arm64) go1.27.3 vscode/1.106.3 vscode-wakatime/25.5.1 opus/5-5 claude-code/2.1.327", event.HarnessClaudeCode, "2.1.327"},
		{"Codex's token names codex", "wakatime/v2.28.4 (linux-6.8.0-x86_64) go1.27.3 gpt/5-codex codex-cli/0.63.0", event.HarnessCodex, "0.63.0"},
		{"a model token named claude isn't Claude Code", "wakatime/v2.28.4 (linux) go1.27.3 claude/3 amp/0.1", event.HarnessOther, ""},
		{"an agent pkg/event has no harness for is other", "wakatime/v2.28.4 (linux) go1.27.3 sonnet/4-5 goose/1.2.0", event.HarnessOther, ""},
		{"a version IsVersion refuses is left out", "wakatime/v2.28.4 (linux) go1.27.3 claude-code/unknown", event.HarnessClaudeCode, ""},
		{"an empty user agent is other", "", event.HarnessOther, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h, v := harnessOf(c.ua)
			if h != c.want || v != c.wantVersion {
				t.Errorf("harnessOf(%q) = %s %q, want %s %q", c.ua, h, v, c.want, c.wantVersion)
			}
		})
	}
}

func TestEditorOf(t *testing.T) {
	cases := []struct{ name, ua, want string }{
		{"VS Code's plugin names vscode", pluginUA, "vscode"},
		{"a name is lower-cased", "wakatime/v2.28.4 (windows-10.0.26100-amd64) go1.27.3 PyCharm/2025.2 PyCharm-wakatime/15.1.0", "pycharm"},
		{"characters a key can't hold are left out", "wakatime/v2.28.4 (linux) go1.27.3 Sublime.Text/4 sublime-wakatime/11", "sublimetext"},
		{"a user agent with no product is unknown", "wakatime/v2.28.4 (linux) go1.27.3", "unknown"},
		{"a script's user agent names the script", scriptUA, "python-requests"},
		{"an empty user agent is unknown", "", "unknown"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := editorOf(c.ua); got != c.want {
				t.Errorf("editorOf(%q) = %q, want %q", c.ua, got, c.want)
			}
		})
	}
}

func TestLedgerPath(t *testing.T) {
	cases := []struct {
		name, root, file, want string
		ok                     bool
	}{
		{"a file inside the root is relative to it", "/home/capture/p", "/home/capture/p/src/a.go", "src/a.go", true},
		{"a file outside the root stays absolute", "/home/capture/p", "/home/capture/q/a.go", "/home/capture/q/a.go", true},
		{"with no root a file stays absolute", "", "/home/capture/p/a.go", "/home/capture/p/a.go", true},
		{"dots in a path are resolved", "/home/capture/p", "/home/capture/p/x/../a.go", "a.go", true},
		{"a Windows path gets slashes and an upper-case drive, and compares without case", `C:\Users\capture\P`, `c:\users\capture\p\src\a.go`, "src/a.go", true},
		{"a relative path can't be keyed", "", "a.go", "", false},
		{"a remote path can't be keyed", "", "ssh://host/a.go", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := ledgerPath(c.root, c.file)
			if got != c.want || ok != c.ok {
				t.Errorf("ledgerPath(%q, %q) = %q, %v; want %q, %v", c.root, c.file, got, ok, c.want, c.ok)
			}
		})
	}
}
