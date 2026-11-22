// SPDX-License-Identifier: AGPL-3.0-or-later

package claudecode

import (
	"bytes"
	"cmp"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/adapters"
	"github.com/some-du6e/pairtime/pkg/event"
)

// projectsDir is the capture's Claude Code projects folder, under captures,
// which holds its four session transcripts and one subagent's.
const projectsDir = cliRun + "/transcripts/home/pairtime-capture/claude-home/projects"

// The capture's sessions (WP-AD1; PROVENANCE.md): 617e16a3 the interactive
// one of steps 1 to 14, b0423624 the one /clear started, resumed at step
// 17, 2197af56 step 20's nested claude -p, and e5edd056 step 18's.
const (
	mainSession   = "617e16a3-ddb4-44ca-af68-72cf58ed1a34"
	clearSession  = "b0423624-aebf-47ff-88b0-927e8df77410"
	nestedSession = "2197af56-2e28-498b-bf5d-15bbdd529e13"
	headlessRun   = "e5edd056-19ab-4262-8f2f-25ec0c28c9f9"
)

// txLine is one line of a captured transcript, as the tailers would hand
// it over.
type txLine struct {
	file        string
	offset, end int64
	text        []byte
	// t is the line's timestamp, or the previous line's for one with none,
	// which orders it among the hook calls.
	t time.Time
}

// readTranscripts reads every captured transcript, by its path under the
// projects folder: the four sessions' and the subagent's.
func readTranscripts(t *testing.T) map[string][]txLine {
	t.Helper()
	root := filepath.Join(captures, projectsDir)
	out := map[string][]txLine{}
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".jsonl") {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		if !transcriptFile(rel) {
			t.Fatalf("%s isn't a transcript by transcriptFile", rel)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		var at int64
		var last time.Time
		for len(b) > 0 {
			i := bytes.IndexByte(b, '\n')
			if i < 0 {
				t.Fatalf("%s ends without a newline", rel)
			}
			line := b[:i]
			var ts struct {
				Timestamp string `json:"timestamp"`
			}
			if json.Unmarshal(line, &ts) == nil && ts.Timestamp != "" {
				if v, err := time.Parse(time.RFC3339Nano, ts.Timestamp); err == nil {
					last = v
				}
			}
			out[rel] = append(out[rel], txLine{file: rel, offset: at, end: at + int64(i) + 1, text: line, t: last})
			at += int64(i) + 1
			b = b[i+1:]
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 5 {
		t.Fatalf("read %d transcripts, want the capture's 5", len(out))
	}
	return out
}

// sessionFiles returns the transcript files in the order a replay reads
// them: each session's own, then the subagent's.
func sessionFiles(files map[string][]txLine) []string {
	var keys []string
	for k := range files {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		si, sj := strings.Contains(keys[i], "/subagents/"), strings.Contains(keys[j], "/subagents/")
		if si != sj {
			return !si
		}
		return keys[i] < keys[j]
	})
	return keys
}

// logBase is the agent's reading of the clock when it read the lines, which
// a log record's mono counts back from.
const logBase = 1_795_200_000_000

func logRaw(l txLine, idle, replay bool) adapters.RawRecord {
	raw := adapters.RawRecord{Source: adapters.SourceLog, Payload: l.text,
		Arrival: adapters.Arrival{Via: event.ViaLog, T: logBase, Mono: 10_000_000, BootID: "replay"},
		Log:     &adapters.LogLine{File: l.file, Path: "/home/capture/" + l.file, Offset: l.offset, End: l.end, Replay: replay}}
	if idle {
		raw.Payload = nil
		raw.Log.Offset, raw.Log.Idle = l.end, true
	}
	return raw
}

// readLog hands a's Parse the lines of file, then the idle marker the
// tailers send once it stops growing, and returns the records and the
// lines it couldn't fully read.
func readLog(t *testing.T, a *Adapter, file string, lines []txLine) []adapters.Record {
	t.Helper()
	var out []adapters.Record
	for i, l := range lines {
		recs, err := a.Parse(logRaw(l, false, false))
		if err != nil {
			t.Errorf("%s line %d: %v", file, i, err)
		}
		out = append(out, recs...)
	}
	if n := len(lines); n > 0 {
		recs, err := a.Parse(logRaw(lines[n-1], true, false))
		if err != nil {
			t.Errorf("%s at its end: %v", file, err)
		}
		out = append(out, recs...)
	}
	return out
}

// readAlone reads every transcript with no hook call before it, as
// lookback does.
func readAlone(t *testing.T) []adapters.Record {
	t.Helper()
	files := readTranscripts(t)
	a := newTest()
	var out []adapters.Record
	for _, k := range sessionFiles(files) {
		out = append(out, readLog(t, a, k, files[k])...)
	}
	checkRecords(t, out)
	return out
}

// checkRecords validates every record as the agent writes it.
func checkRecords(t *testing.T, recs []adapters.Record) {
	t.Helper()
	for _, r := range recs {
		e := r.Event
		e.EventID, e.Seq, e.DeviceID = event.NewEventID(time.UnixMilli(e.T)), 1, "d_test"
		if err := e.Validate(); err != nil {
			t.Errorf("%s at %d doesn't validate: %v", e.Kind(), e.T, err)
		}
		if e.Source != event.SourceLog && e.Via == event.ViaLog {
			t.Errorf("%s at %d is via log with source %s", e.Kind(), e.T, e.Source)
		}
	}
}

// step is one thing a replay with hooks hands the adapter: a hook call or
// a transcript line.
type step struct {
	at   time.Time
	call *adapters.Call
	line *txLine
	idle bool
}

// readWithHooks replays the capture as the agent takes it in live: every
// hook call, delivered as way says, and every transcript line, in the order
// they happened, a hook call before a line of the same moment. It returns
// the hooks' records and the transcripts' apart.
func readWithHooks(t *testing.T, way adapters.Delivery) (hooks, logs []adapters.Record) {
	t.Helper()
	calls := readCalls(t)
	files := readTranscripts(t)
	var steps []step
	for i := range calls {
		steps = append(steps, step{at: calls[i].Arrived, call: &calls[i]})
	}
	for _, k := range sessionFiles(files) {
		ls := files[k]
		for i := range ls {
			steps = append(steps, step{at: ls[i].t, line: &ls[i]})
		}
		// The tailers' idle marker, IdleAfter after the file's last line.
		steps = append(steps, step{at: ls[len(ls)-1].t.Add(10 * time.Second), line: &ls[len(ls)-1], idle: true})
	}
	sort.SliceStable(steps, func(i, j int) bool {
		if !steps[i].at.Equal(steps[j].at) {
			return steps[i].at.Before(steps[j].at)
		}
		return steps[i].call != nil && steps[j].call == nil
	})
	a := newTest()
	base := calls[0].Arrived
	for _, s := range steps {
		if s.call != nil {
			raw, err := adapters.HookRecord(a, "linux", adapters.DeliveryOf(harness, *s.call, way, base))
			if err != nil {
				t.Fatalf("hooks/%s: %v", s.call.Dir, err)
			}
			recs, err := a.Parse(raw)
			if err != nil {
				t.Errorf("hooks/%s: %v", s.call.Dir, err)
			}
			hooks = append(hooks, recs...)
			continue
		}
		recs, err := a.Parse(logRaw(*s.line, s.idle, false))
		if err != nil {
			t.Errorf("%s at %d: %v", s.line.file, s.line.offset, err)
		}
		logs = append(logs, recs...)
	}
	checkRecords(t, logs)
	return hooks, logs
}

// replayHooks replays the capture's hook calls alone, walked.
func replayHooks(t *testing.T) []adapters.Record {
	t.Helper()
	recs, fails := adapters.Replay(newTest(), readCalls(t), adapters.ReplayOptions{GOOS: "linux", Way: adapters.Walked})
	for dir, err := range fails {
		t.Errorf("hooks/%s: %v", dir, err)
	}
	out := make([]adapters.Record, len(recs))
	for i, r := range recs {
		out[i] = r.Record
	}
	return out
}

// ofKind returns the records whose payload is a T, in order.
func ofKind[T event.Payload](recs []adapters.Record) []adapters.Record {
	var out []adapters.Record
	for _, r := range recs {
		if _, ok := r.Event.Data.(T); ok {
			out = append(out, r)
		}
	}
	return out
}

// sessionOf returns the records of the session whose id is sid, under the
// fixture keys every replay here uses.
func sessionOf(recs []adapters.Record, sid string) []adapters.Record {
	key := adapters.FixtureKeys().Session(sid)
	var out []adapters.Record
	for _, r := range recs {
		if r.Event.SessionKey == key {
			out = append(out, r)
		}
	}
	return out
}

func within(a, b, d int64) bool { return a-b <= d && b-a <= d }

// toolFields writes what an agent.tool says, but its span.
func toolFields(tl event.AgentTool) string {
	code := "none"
	if tl.ExitCode != nil {
		code = fmt.Sprint(*tl.ExitCode)
	}
	return fmt.Sprintf("class %s, files %v, +%d -%d, cmd %s, exit %s, error %s, visible %d", tl.ToolClass, tl.Files,
		tl.LinesAdded, tl.LinesRemoved, tl.CmdHash, code, tl.ErrorSig, tl.VisibleOutputLines)
}

// cc1Skew is CC1's time limit between a hook prompt and the transcript's
// entry for its turn (DESIGN 7.3).
const cc1Skew = 5000

// hookTools are the tool classes a PostToolUse hook gives for the Edit,
// Write and Bash calls pairtime installs it for.
var hookTools = []event.ToolClass{event.ToolEdit, event.ToolWrite, event.ToolBash, event.ToolTest, event.ToolBuild}

// TestTranscriptMatchesHooks is WP-AD3's first CI check: each captured
// transcript, read alone as lookback reads it, gives the prompt, output
// and tool events its session's hooks give, turn by turn: in the same
// order, at most 5 s apart, with the same len_chars, which is what CC1
// relies on (DESIGN 7.3). The hooks' outputs are each turn's final
// message, and their tools the Edit, Write and Bash calls that ran.
func TestTranscriptMatchesHooks(t *testing.T) {
	hooks := replayHooks(t)
	logs := readAlone(t)
	for _, sid := range []string{mainSession, clearSession, nestedSession, headlessRun} {
		h, l := sessionOf(hooks, sid), sessionOf(logs, sid)
		t.Run("the prompts of "+sid[:8]+" are the hooks', in order, within 5 s, with their lengths", func(t *testing.T) {
			hp, lp := ofKind[*event.PromptSubmit](h), ofKind[*event.PromptSubmit](l)
			if len(hp) == 0 || len(hp) != len(lp) {
				t.Fatalf("the hooks give %d prompts, the transcript %d", len(hp), len(lp))
			}
			for i := range hp {
				hd, ld := hp[i].Event.Data.(*event.PromptSubmit), lp[i].Event.Data.(*event.PromptSubmit)
				if hd.TurnKey != ld.TurnKey || hd.LenChars != ld.LenChars || !within(hp[i].Event.T, lp[i].Event.T, cc1Skew) ||
					!bytes.Equal(hp[i].Text, lp[i].Text) {
					t.Errorf("prompt %d: hook turn %s, %d chars, at %d; transcript turn %s, %d chars, at %d, text equal %v",
						i, hd.TurnKey, hd.LenChars, hp[i].Event.T, ld.TurnKey, ld.LenChars, lp[i].Event.T, bytes.Equal(hp[i].Text, lp[i].Text))
				}
				if hd.SlashCommand != ld.SlashCommand || hd.LowEffort != ld.LowEffort || ld.PastedChars != 0 || ld.TypedLenEst != ld.LenChars {
					t.Errorf("prompt %d: hook %+v, transcript %+v", i, *hd, *ld)
				}
			}
		})
		t.Run("the final outputs of "+sid[:8]+" are the hooks', turn by turn, within 5 s", func(t *testing.T) {
			ho := ofKind[*event.AgentOutput](h)
			var lo []adapters.Record
			for _, r := range ofKind[*event.AgentOutput](l) {
				if r.Event.Data.(*event.AgentOutput).IsFinal {
					lo = append(lo, r)
				}
			}
			if len(ho) == 0 || len(ho) != len(lo) {
				t.Fatalf("the hooks give %d outputs, the transcript %d final ones", len(ho), len(lo))
			}
			for i := range ho {
				hd, ld := *ho[i].Event.Data.(*event.AgentOutput), *lo[i].Event.Data.(*event.AgentOutput)
				if hd != ld || !within(ho[i].Event.T, lo[i].Event.T, cc1Skew) || !bytes.Equal(ho[i].Text, lo[i].Text) {
					t.Errorf("output %d: hook %+v at %d, transcript %+v at %d", i, hd, ho[i].Event.T, ld, lo[i].Event.T)
				}
			}
		})
		t.Run("the stops of "+sid[:8]+" are the hooks', turn by turn, within 5 s", func(t *testing.T) {
			hs, ls := ofKind[*event.AgentStop](h), ofKind[*event.AgentStop](l)
			if len(hs) != len(ls) {
				t.Fatalf("the hooks give %d stops, the transcript %d", len(hs), len(ls))
			}
			for i := range hs {
				hd, ld := hs[i].Event.Data.(*event.AgentStop), ls[i].Event.Data.(*event.AgentStop)
				if hd.TurnKey != ld.TurnKey || hd.StopReason != ld.StopReason || hd.StopHookActive != ld.StopHookActive ||
					!within(hs[i].Event.T, ls[i].Event.T, cc1Skew) {
					t.Errorf("stop %d: hook %+v at %d, transcript %+v at %d", i, *hd, hs[i].Event.T, *ld, ls[i].Event.T)
				}
			}
		})
		t.Run("the Edit, Write and Bash calls of "+sid[:8]+" are the hooks', in order, within 5 s", func(t *testing.T) {
			ht := ofKind[*event.AgentTool](h)
			var lt []adapters.Record
			for _, r := range ofKind[*event.AgentTool](l) {
				if slices.Contains(hookTools, r.Event.Data.(*event.AgentTool).ToolClass) {
					lt = append(lt, r)
				}
			}
			// The subagent's calls are in a file of their own, which the
			// replay reads after the session's.
			byTime := func(a, b adapters.Record) int { return cmp.Compare(a.Event.T, b.Event.T) }
			slices.SortStableFunc(ht, byTime)
			slices.SortStableFunc(lt, byTime)
			if sid != clearSession && sid != nestedSession && len(ht) == 0 {
				t.Fatal("the hooks give no tool call")
			}
			if len(ht) != len(lt) {
				t.Fatalf("the hooks give %d tool calls, the transcript %d", len(ht), len(lt))
			}
			for i := range ht {
				hd, ld := *ht[i].Event.Data.(*event.AgentTool), *lt[i].Event.Data.(*event.AgentTool)
				// The spans come from different clocks: the hook's from its
				// arrival less duration_ms, the transcript's from its entries.
				if !within(hd.TStart, ld.TStart, cc1Skew) || !within(hd.TEnd, ld.TEnd, cc1Skew) || !within(ht[i].Event.T, lt[i].Event.T, cc1Skew) {
					t.Errorf("tool %d: hook span %d-%d, transcript %d-%d", i, hd.TStart, hd.TEnd, ld.TStart, ld.TEnd)
				}
				if toolFields(hd) != toolFields(ld) || ht[i].Command != lt[i].Command || !slices.Equal(ht[i].Edits, lt[i].Edits) {
					t.Errorf("tool %d: hook %s %d chars, transcript %s %d chars", i, toolFields(hd), len(ht[i].Command), toolFields(ld), len(lt[i].Command))
				}
			}
		})
	}
}

// TestTranscriptInterrupt is WP-AD3's second CI check: WP-AD1's captured
// interrupt, step 6's refused rm, which ended its turn with no Stop,
// replays to one agent.interrupt with its turn's key, which no hook
// reports. Step 9's Esc left no entry of its own: Claude Code rewound the
// prompt, and step 10's entry forks from the same parent.
func TestTranscriptInterrupt(t *testing.T) {
	keys := adapters.FixtureKeys()
	want := keys.Turn(mainSession, "86cab275-ba70-4c87-b805-6ed0c1e74adc")
	hooksAlone := replayHooks(t)
	hooks, withHooks := readWithHooks(t, adapters.Walked)
	cases := []struct {
		name string
		recs []adapters.Record
		n    int
	}{
		{"read alone, the transcript gives one agent.interrupt, in step 6's turn", readAlone(t), 1},
		{"read beside its hooks, the transcript still gives that agent.interrupt", withHooks, 1},
		{"the hooks give none", hooksAlone, 0},
		{"the hooks give none beside the transcript either", hooks, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ofKind[*event.AgentInterrupt](tc.recs)
			if len(got) != tc.n {
				t.Fatalf("%d interrupts, want %d", len(got), tc.n)
			}
			for _, r := range got {
				if k := r.Event.Data.(*event.AgentInterrupt).TurnKey; k != want || r.Event.SessionKey != keys.Session(mainSession) {
					t.Errorf("the interrupt is of turn %s, want step 6's %s", k, want)
				}
			}
		})
	}
}

// TestTranscriptSessionStarts checks WP-AD3's session.start rule on the
// capture: read without their hooks, as lookback reads them, the four
// transcripts give one session.start each, sdk for step 18's and step
// 20's claude -p runs and unknown for 617e16a3 and b0423624, all with
// parent_class unknown and has_tty false, and the subagent's adds none.
// Read with their hooks they give none (E-2.8-lookback).
func TestTranscriptSessionStarts(t *testing.T) {
	_, withHooks := readWithHooks(t, adapters.Walked)
	_, withSpooled := readWithHooks(t, adapters.Spooled)
	alone := readAlone(t)
	want := map[string]event.Entrypoint{mainSession: event.EntrypointUnknown, clearSession: event.EntrypointUnknown,
		nestedSession: event.EntrypointSDK, headlessRun: event.EntrypointSDK}
	for sid, ep := range want {
		t.Run("read alone, "+sid[:8]+" gets one session.start with entrypoint "+string(ep), func(t *testing.T) {
			got := ofKind[*event.SessionStart](sessionOf(alone, sid))
			if len(got) != 1 {
				t.Fatalf("%d session.starts, want 1", len(got))
			}
			st := got[0].Event.Data.(*event.SessionStart)
			if st.Entrypoint != ep || st.ParentClass != event.ParentUnknown || st.HasTTY || st.StartKind != event.StartStartup {
				t.Errorf("session.start %+v, want startup, %s, parent unknown, no TTY", *st, ep)
			}
		})
	}
	cases := []struct {
		name string
		recs []adapters.Record
	}{
		{"read with their hooks, the transcripts give no session.start", withHooks},
		{"read with hooks only the spool delivered, they give none either", withSpooled},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ofKind[*event.SessionStart](tc.recs); len(got) != 0 {
				t.Errorf("%d session.starts", len(got))
			}
		})
	}
}

// promptOf returns the transcript's prompt.submit of turn promptID in
// session sid.
func promptOf(t *testing.T, recs []adapters.Record, sid, promptID string) (*event.PromptSubmit, adapters.Record) {
	t.Helper()
	key := adapters.FixtureKeys().Turn(sid, promptID)
	for _, r := range ofKind[*event.PromptSubmit](sessionOf(recs, sid)) {
		if p := r.Event.Data.(*event.PromptSubmit); p.TurnKey == key {
			return p, r
		}
	}
	t.Fatalf("no prompt.submit of turn %s", promptID)
	return nil, adapters.Record{}
}

// TestTranscriptPrompts checks what WP-AD3 says the capture's prompts give
// when read from their transcripts: step 3's paste gets no pasted_chars,
// since 2.1.327 marks it promptSource typed; step 12's three /loop runs
// give origin_declared cron (turnOrigin scheduled, SM 2.2 rule 1), step 10's
// task notification subagent (rule 3), and the two claude -p prompts, whose
// turnOrigin is sdk, declare nothing and count no parse failure. A slash
// command's prompt is what the person typed, as its hook has it, and the
// queued message of step 21 is no prompt.
func TestTranscriptPrompts(t *testing.T) {
	recs := readAlone(t) // fails on any parse failure
	cases := []struct {
		// prompt is the first 8 characters of the turn's promptId.
		name, session, prompt string
		origin                event.DeclaredOrigin
		text                  string
		chars                 int
		slash                 string
	}{
		{"step 3's paste counts no pasted characters", mainSession, "bdf55554", event.DeclaredNone, "", 194, ""},
		{"step 12's first /loop run is declared cron", mainSession, "c8f3baad", event.DeclaredCron, "Reply with the word tick and nothing else.", 42, ""},
		{"step 12's second /loop run is declared cron", mainSession, "1e2761e2", event.DeclaredCron, "", 42, ""},
		{"step 12's third /loop run is declared cron", mainSession, "5eb0e384", event.DeclaredCron, "", 42, ""},
		{"step 10's task notification is declared subagent", mainSession, "7d122f0f", event.DeclaredSubagent, "", 1631, ""},
		{"step 20's claude -p prompt declares nothing", nestedSession, "eaaa4435", event.DeclaredNone, "Say nested.", 11, ""},
		{"step 18's claude -p prompt declares nothing", headlessRun, "c51132dd", event.DeclaredNone, "", 68, ""},
		{"/loop is the command as typed, from its markup", mainSession, "08f3c20f", event.DeclaredNone, "/loop 1m Reply with the word tick and nothing else.", 51, "/loop"},
		{"/goal is the command as typed, from the markup before the entry that starts its turn", mainSession, "8eb66f7b", event.DeclaredNone, "/goal Make the failing test pass, then stop.", 44, "/goal"},
	}
	ids := promptIDs(t)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			id := ids[tc.prompt]
			if id == "" {
				t.Fatalf("the capture has no prompt %s", tc.prompt[:8])
			}
			p, r := promptOf(t, recs, tc.session, id)
			if p.OriginDeclared != tc.origin || p.LenChars != tc.chars || p.PastedChars != 0 || p.SlashCommand != tc.slash || p.MatchesPendingCron {
				t.Errorf("got %+v", *p)
			}
			if tc.text != "" && string(r.Text) != tc.text {
				t.Errorf("text %q, want %q", r.Text, tc.text)
			}
		})
	}
	t.Run("local commands such as /clear and /exit, which start no turn, are no prompts", func(t *testing.T) {
		got := ofKind[*event.PromptSubmit](sessionOf(recs, clearSession))
		if len(got) != 1 || string(got[0].Text) != "Say done." {
			t.Errorf("%d prompts in %s, want step 17's alone", len(got), clearSession[:8])
		}
	})
	t.Run("step 21's queued message is no prompt.submit when its turn takes it up", func(t *testing.T) {
		key := adapters.FixtureKeys().Turn(mainSession, ids["7ca5940f"])
		for _, r := range ofKind[*event.PromptSubmit](recs) {
			if r.Event.Data.(*event.PromptSubmit).TurnKey == key {
				t.Error("the pickup of the queued message became a prompt.submit")
			}
		}
	})
}

// promptIDs returns the capture's promptIds by their first 8 characters.
func promptIDs(t *testing.T) map[string]string {
	t.Helper()
	ids := map[string]string{}
	for _, ls := range readTranscripts(t) {
		for _, l := range ls {
			var e struct {
				PromptID string `json:"promptId"`
			}
			if json.Unmarshal(l.text, &e) == nil && len(e.PromptID) >= 8 {
				ids[e.PromptID[:8]] = e.PromptID
			}
		}
	}
	return ids
}

// TestTranscriptQueue checks step 21's queued message read alone: the
// enqueue, while the /goal turn ran, is that turn's agent.queue with the
// message's length, and the turn that took it up stops with its own key
// (E-cc-queued-turn). The enqueues of a task notification, a scheduled
// prompt and a claude -p prompt, which no turn runs through, are none.
func TestTranscriptQueue(t *testing.T) {
	recs := readAlone(t)
	ids := promptIDs(t)
	keys := adapters.FixtureKeys()
	got := ofKind[*event.AgentQueue](recs)
	if len(got) != 1 {
		t.Fatalf("%d agent.queue records, want step 21's alone", len(got))
	}
	q := got[0].Event.Data.(*event.AgentQueue)
	if q.TurnKey != keys.Turn(mainSession, ids["8eb66f7b"]) || q.LenChars != 43 {
		t.Errorf("agent.queue %+v, want the /goal turn's with 43 characters", *q)
	}
	pickup := keys.Turn(mainSession, ids["7ca5940f"])
	stopped := false
	for _, r := range ofKind[*event.AgentStop](recs) {
		stopped = stopped || r.Event.Data.(*event.AgentStop).TurnKey == pickup
	}
	if !stopped {
		t.Error("the turn that took the message up has no stop under its own key")
	}
}

// TestTranscriptPermissions checks that the transcripts give no
// agent.permission, steps 4 and 6 included. Each tool_result entry says
// who decided in permissionDecision (user_temporary for step 4's edit and
// command, config for step 6's ls), and step 6's refused rm carries
// toolDenialKind user-rejected, but the entry is dated when the tool
// ended, which for a command is after it ran, so it dates no answer, and
// SM 1.2 doesn't list the transcript as a source of agent.permission
// (docs/errata/2026-11-21-claude-code-transcript-permissions.md).
func TestTranscriptPermissions(t *testing.T) {
	_, withHooks := readWithHooks(t, adapters.Walked)
	cases := []struct {
		name string
		recs []adapters.Record
	}{
		{"read alone, the transcripts give no agent.permission", readAlone(t)},
		{"read with their hooks, they give none either", withHooks},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ofKind[*event.AgentPermission](tc.recs); len(got) != 0 {
				t.Errorf("%d agent.permission records", len(got))
			}
		})
	}
}

// TestTranscriptScheduledMarks records what WP-AD3 asks the test to: a
// prompt the model schedules on its own is marked. The capture's /loop had
// the model call CronCreate, whose result names the job, and each of the
// job's runs is a user entry with turnOrigin scheduled and that job's
// scheduledTaskId, which the reader declares cron. The capture has no
// ScheduleWakeup, so whether its prompts are marked stays unknown.
func TestTranscriptScheduledMarks(t *testing.T) {
	var job string
	var runs, marked, wakeups int
	uses := map[string]string{}
	for _, l := range readTranscripts(t)["-home-capture-pairtime-capture-projects-cli/"+mainSession+".jsonl"] {
		e, err := decodeEntry(l.text)
		if err != nil {
			t.Fatal(err)
		}
		var raw struct {
			ScheduledTaskID string `json:"scheduledTaskId"`
			ToolUseResult   struct {
				ID string `json:"id"`
			} `json:"toolUseResult"`
		}
		json.Unmarshal(l.text, &raw)
		if e.Message == nil {
			continue
		}
		for _, p := range e.Message.Content.Parts {
			switch {
			case p.Type == "tool_use":
				uses[p.ID] = p.Name
				if p.Name == "ScheduleWakeup" {
					wakeups++
				}
			case p.Type == "tool_result" && uses[p.ToolUseID] == "CronCreate":
				job = raw.ToolUseResult.ID
			}
		}
		if raw.ScheduledTaskID != "" && raw.ScheduledTaskID == job {
			runs++
			if e.TurnOrigin != nil && *e.TurnOrigin == "scheduled" {
				marked++
			}
		}
	}
	if job == "" || runs != 3 || marked != runs {
		t.Fatalf("CronCreate's job %q had %d runs, %d of them marked scheduled; want step 12's 3, all marked", job, runs, marked)
	}
	t.Logf("a CronCreate job the model scheduled is marked: %d of its %d runs have turnOrigin scheduled", marked, runs)
	if wakeups == 0 {
		t.Log("the capture has no ScheduleWakeup, so whether its prompts are marked isn't known")
	}
}

// TestTranscriptWithHooks checks that a transcript read beside its
// session's hooks counts nothing twice, since pkg/score sums every
// agent.output and agent.tool of a turn: with every hook via hook it
// writes each prompt again, as CC1 needs, the interrupt, the text the
// hooks don't see and the tools they don't, but no session.start, no
// agent.queue, no final output or stop, and no call a hook reported. With
// hooks only the spool delivered, which the scorer leaves out of a session
// a log reader covers (DESIGN 5.2), the transcript's copies of those are
// the ones that count, and it writes them.
func TestTranscriptWithHooks(t *testing.T) {
	alone := readAlone(t)
	hooks, logs := readWithHooks(t, adapters.Walked)
	_, spooled := readWithHooks(t, adapters.Spooled)
	count := func(recs []adapters.Record, keep func(adapters.Record) bool) int {
		n := 0
		for _, r := range recs {
			if keep(r) {
				n++
			}
		}
		return n
	}
	final := func(r adapters.Record) bool {
		o, ok := r.Event.Data.(*event.AgentOutput)
		return ok && o.IsFinal
	}
	nonFinal := func(r adapters.Record) bool {
		o, ok := r.Event.Data.(*event.AgentOutput)
		return ok && !o.IsFinal
	}
	isKind := func(k event.Kind) func(adapters.Record) bool {
		return func(r adapters.Record) bool { return r.Event.Kind() == k }
	}
	hookTool := func(r adapters.Record) bool {
		tl, ok := r.Event.Data.(*event.AgentTool)
		return ok && slices.Contains(hookTools, tl.ToolClass)
	}
	otherTool := func(r adapters.Record) bool {
		tl, ok := r.Event.Data.(*event.AgentTool)
		return ok && !slices.Contains(hookTools, tl.ToolClass)
	}
	cases := []struct {
		name string
		recs []adapters.Record
		keep func(adapters.Record) bool
		want int
	}{
		{"every hook prompt gets its transcript copy", logs, isKind(event.KindPromptSubmit), count(hooks, isKind(event.KindPromptSubmit))},
		{"no final output beside a via hook Stop", logs, final, 0},
		{"no stop beside a via hook Stop", logs, isKind(event.KindAgentStop), 0},
		{"no session.start", logs, isKind(event.KindSessionStart), 0},
		{"no agent.queue beside the hook's", logs, isKind(event.KindAgentQueue), 0},
		{"no Edit, Write or Bash call a via hook PostToolUse reported", logs, hookTool, 0},
		{"the text the Stop hook doesn't carry, as when it read alone", logs, nonFinal, count(alone, nonFinal)},
		{"the tools no hook reports, as when it read alone", logs, otherTool, count(alone, otherTool)},
		{"beside spooled hooks, every final output", spooled, final, count(alone, final)},
		{"beside spooled hooks, every stop", spooled, isKind(event.KindAgentStop), count(alone, isKind(event.KindAgentStop))},
		{"beside spooled hooks, every Edit, Write and Bash call", spooled, hookTool, count(alone, hookTool)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := count(tc.recs, tc.keep); got != tc.want {
				t.Errorf("%d records, want %d", got, tc.want)
			}
		})
	}
	if count(alone, nonFinal) == 0 || count(alone, otherTool) == 0 {
		t.Error("the capture read alone has no output that isn't final or no tool the hooks don't report, so the cases above test nothing")
	}
}

// logLines turns a synthetic transcript, one entry per string, into lines
// of file "p/s.jsonl".
func logLines(entries ...string) []txLine {
	var out []txLine
	var at int64
	for _, e := range entries {
		n := int64(len(e)) + 1
		out = append(out, txLine{file: "p/s.jsonl", offset: at, end: at + n, text: []byte(e)})
		at += n
	}
	return out
}

// entryJSON writes a transcript entry of session s with fields kv.
func entryJSON(typ string, kv map[string]any) string {
	m := map[string]any{"type": typ, "sessionId": "s1", "timestamp": "2026-11-20T17:00:00.000Z", "cwd": "/home/capture/p",
		"version": "2.1.327", "entrypoint": "cli", "isSidechain": false}
	for k, v := range kv {
		if v == nil {
			delete(m, k)
		} else {
			m[k] = v
		}
	}
	b, _ := json.Marshal(m)
	return string(b)
}

func userText(promptID, text string, kv map[string]any) string {
	m := map[string]any{"promptId": promptID, "message": map[string]any{"role": "user", "content": text}}
	for k, v := range kv {
		m[k] = v
	}
	return entryJSON("user", m)
}

func assistantText(msg, stop, text, ts string) string {
	return entryJSON("assistant", map[string]any{"timestamp": ts, "message": map[string]any{"id": msg, "role": "assistant",
		"stop_reason": stop, "content": []any{map[string]any{"type": "text", "text": text}}}})
}

// TestTranscriptLabels checks E-cc-transcript-labels' rules for values no
// capture has shown, on single entries: they declare nothing and count as
// parse failures, while the values the capture shows count none.
func TestTranscriptLabels(t *testing.T) {
	cases := []struct {
		name   string
		entry  string
		fails  bool
		origin event.DeclaredOrigin
	}{
		{"turnOrigin sdk declares nothing and is no failure", userText("q1", "hello there", map[string]any{"turnOrigin": "sdk", "promptSource": "sdk"}), false, event.DeclaredNone},
		{"turnOrigin scheduled declares cron", userText("q1", "tick", map[string]any{"turnOrigin": "scheduled", "promptSource": "system"}), false, event.DeclaredCron},
		{"turnOrigin task_notification declares subagent", userText("q1", "a report", map[string]any{"turnOrigin": "task_notification", "promptSource": "system"}), false, event.DeclaredSubagent},
		{"a turnOrigin no capture has shown declares nothing and is a failure", userText("q1", "hello there", map[string]any{"turnOrigin": "webhook"}), true, event.DeclaredNone},
		{"a promptSource no capture has shown declares nothing and is a failure", userText("q1", "hello there", map[string]any{"promptSource": "voice", "turnOrigin": "human"}), true, event.DeclaredNone},
		{"an entrypoint token HS 3.3 doesn't list is a failure", userText("q1", "hello there", map[string]any{"turnOrigin": "human", "entrypoint": "claude-watch"}), true, event.DeclaredNone},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := newTest()
			l := logLines(tc.entry)[0]
			recs, err := a.Parse(logRaw(l, false, false))
			if (err != nil) != tc.fails {
				t.Fatalf("Parse error %v, want a failure: %v", err, tc.fails)
			}
			ps := ofKind[*event.PromptSubmit](recs)
			if len(ps) != 1 || ps[0].Event.Data.(*event.PromptSubmit).OriginDeclared != tc.origin {
				t.Fatalf("records %v, want one prompt declared %s", recs, tc.origin)
			}
		})
	}
}

// TestTranscriptParseFailures checks that lines the reader can't read
// raise the parse-failure count and stop nothing: the line after each
// still parses.
func TestTranscriptParseFailures(t *testing.T) {
	cases := []struct {
		name  string
		line  string
		long  bool
		fails bool
	}{
		{"a line that isn't JSON is a failure", `{"type": "user", "message": `, false, true},
		{"a JSON line that isn't an object is a failure", `["user"]`, false, true},
		{"an entry of a type 2.1.327 doesn't write is a failure", `{"type": "teleport", "sessionId": "s1"}`, false, true},
		{"a user entry without its message is a failure", entryJSON("user", map[string]any{"promptId": "q1"}), false, true},
		{"a queue operation 2.1.327 doesn't write is a failure", entryJSON("queue-operation", map[string]any{"operation": "shuffle"}), false, true},
		{"a line too long for the tailers is a failure", "", true, true},
		{"a bookkeeping entry 2.1.327 writes is none", `{"type": "ai-title", "aiTitle": "x", "sessionId": "s1"}`, false, false},
		{"an api-request-blob is none", `{"type": "api-request-blob", "message": {"role": "system", "content": [{"type": "text", "text": "x"}]}}`, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := newTest()
			ls := logLines(tc.line, userText("q2", "the next line parses", map[string]any{"turnOrigin": "human", "promptSource": "typed"}))
			raw := logRaw(ls[0], false, false)
			raw.Log.TooLong = tc.long
			if tc.long {
				raw.Payload = nil
			}
			_, err := a.Parse(raw)
			if (err != nil) != tc.fails {
				t.Fatalf("Parse error %v, want a failure: %v", err, tc.fails)
			}
			var perr *adapters.ParseError
			if tc.fails && !errorsAs(err, &perr) {
				t.Errorf("the failure is %T, not an *adapters.ParseError the agent counts", err)
			}
			recs, err := a.Parse(logRaw(ls[1], false, false))
			if err != nil || len(ofKind[*event.PromptSubmit](recs)) != 1 {
				t.Errorf("the line after it gives %v, %v; want its prompt", recs, err)
			}
		})
	}
}

func errorsAs(err error, target **adapters.ParseError) bool {
	p, ok := err.(*adapters.ParseError)
	if ok {
		*target = p
	}
	return ok
}

// TestTranscriptGoalVerdicts checks stop_hook_active from /goal verdicts
// (SM 2.2 rule 2): a verdict that the goal isn't met continues the turn,
// and its next stop is a stop hook's continuation; the sentinel /goal
// writes when it is set, and a verdict that it is met, continue nothing.
func TestTranscriptGoalVerdicts(t *testing.T) {
	goal := func(met, sentinel bool, ts string) string {
		return entryJSON("attachment", map[string]any{"timestamp": ts, "attachment": map[string]any{"type": "goal_status", "met": met, "sentinel": sentinel}})
	}
	summary := func(ts string) string {
		return entryJSON("system", map[string]any{"subtype": "stop_hook_summary", "timestamp": ts})
	}
	cases := []struct {
		name    string
		entries []string
		want    []bool
	}{
		{"an unmet verdict makes the turn's next stop a continuation", []string{
			goal(false, true, "2026-11-20T17:00:00.000Z"),
			userText("g1", "/goal tests pass", map[string]any{"turnOrigin": "human"}),
			assistantText("m1", "end_turn", "first try", "2026-11-20T17:00:05.000Z"),
			goal(false, false, "2026-11-20T17:00:06.000Z"),
			summary("2026-11-20T17:00:06.100Z"),
			assistantText("m2", "end_turn", "second try", "2026-11-20T17:00:20.000Z"),
			goal(true, false, "2026-11-20T17:00:21.000Z"),
			summary("2026-11-20T17:00:21.100Z"),
		}, []bool{false, true}},
		{"a met verdict and the sentinel continue nothing", []string{
			goal(false, true, "2026-11-20T17:00:00.000Z"),
			userText("g1", "/goal tests pass", map[string]any{"turnOrigin": "human"}),
			assistantText("m1", "end_turn", "done", "2026-11-20T17:00:05.000Z"),
			goal(true, false, "2026-11-20T17:00:06.000Z"),
			summary("2026-11-20T17:00:06.100Z"),
		}, []bool{false}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := newTest()
			recs := readLog(t, a, "p/s.jsonl", logLines(tc.entries...))
			stops := ofKind[*event.AgentStop](recs)
			var got []bool
			for _, s := range stops {
				got = append(got, s.Event.Data.(*event.AgentStop).StopHookActive)
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("stop_hook_active %v, want %v", got, tc.want)
			}
		})
	}
}

// TestTranscriptResume checks the reader's memory across a restart: what
// SaveState keeps of a file holds no content, and a new adapter that
// loads it, given the file again from LogResume with the lines read before
// as replays, makes the records a reading without a restart makes, none of
// them twice. The restart falls on each line of the capture's main
// transcript in turn, tool uses waiting for their results included.
func TestTranscriptResume(t *testing.T) {
	files := readTranscripts(t)
	key := "-home-capture-pairtime-capture-projects-cli/" + mainSession + ".jsonl"
	lines := files[key]
	whole := readLog(t, newTest(), key, lines)
	norm := func(recs []adapters.Record) []string {
		var out []string
		for _, r := range recs {
			b, _ := json.Marshal(r.Event)
			out = append(out, string(b)+string(r.Text)+r.Command)
		}
		return out
	}
	want := norm(whole)
	secret := []string{"count_words", "grep -rn", "wordcount.py", "Reply with the word tick"}
	for cut := 1; cut < len(lines); cut += 7 {
		t.Run(fmt.Sprintf("a restart after line %d of %d loses and repeats nothing", cut, len(lines)), func(t *testing.T) {
			a := newTest()
			var got []adapters.Record
			for _, l := range lines[:cut] {
				recs, _ := a.Parse(logRaw(l, false, false))
				got = append(got, recs...)
			}
			resume, ok := a.LogResume(key)
			if !ok || resume > lines[cut-1].end {
				t.Fatalf("LogResume %d, %v; the reader took lines to %d", resume, ok, lines[cut-1].end)
			}
			mem, err := a.SaveState()
			if err != nil {
				t.Fatal(err)
			}
			for _, s := range secret {
				if strings.Contains(string(mem), s) {
					t.Fatalf("the saved memory holds %q, which is content", s)
				}
			}
			b := newTest()
			if err := b.LoadState(mem); err != nil {
				t.Fatal(err)
			}
			read := lines[cut-1].end
			for _, l := range lines {
				if l.end <= resume {
					continue
				}
				recs, err := b.Parse(logRaw(l, false, l.end <= read))
				if err != nil {
					t.Errorf("line at %d: %v", l.offset, err)
				}
				got = append(got, recs...)
			}
			recs, _ := b.Parse(logRaw(lines[len(lines)-1], true, false))
			got = append(got, recs...)
			if g := norm(got); !slices.Equal(g, want) {
				t.Errorf("%d records, want the %d of one reading", len(g), len(want))
			}
		})
	}
}

// TestQueuedPickupLearned checks the case parse.go left to WP-AD3
// (E-cc-queued-turn): a message queued in a turn that took up an earlier
// queued message, before any call of that turn, is that turn's agent.queue
// once the transcript's promptSource queued entry has named the turn.
func TestQueuedPickupLearned(t *testing.T) {
	pickupEntry := userText("p-picked", "the first queued message", map[string]any{"promptSource": "queued", "turnOrigin": "human",
		"sessionId": "s-q", "timestamp": "2026-11-20T17:07:45.084Z"})
	prompt := func(id, text string) adapters.RawRecord {
		b, _ := json.Marshal(map[string]any{"session_id": "s-q", "transcript_path": "/home/capture/p/s-q.jsonl", "cwd": "/home/capture/p",
			"hook_event_name": EventUserPromptSubmit, "prompt_id": id, "prompt": text})
		return adapters.RawRecord{Source: adapters.SourceHook, Event: EventUserPromptSubmit, Payload: b,
			Arrival: adapters.Arrival{Via: event.ViaHook, T: 1_795_194_470_000, Mono: 5000, BootID: "b_test"}}
	}
	cases := []struct {
		name       string
		readPickup bool
		want       event.Kind
	}{
		{"once the transcript named the picked-up turn, a message queued in it is agent.queue", true, event.KindAgentQueue},
		{"without the transcript it is still a prompt.submit, as before WP-AD3", false, event.KindPromptSubmit},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := newTest()
			if _, err := a.Parse(prompt("p-goal", "/goal tests pass")); err != nil {
				t.Fatal(err)
			}
			if tc.readPickup {
				if _, err := a.Parse(logRaw(logLines(pickupEntry)[0], false, false)); err != nil {
					t.Fatal(err)
				}
			}
			recs, err := a.Parse(prompt("p-picked", "and this one too"))
			if err != nil {
				t.Fatal(err)
			}
			if len(recs) != 1 || recs[0].Event.Kind() != tc.want {
				t.Errorf("records %v, want one %s", recs, tc.want)
			}
		})
	}
}
