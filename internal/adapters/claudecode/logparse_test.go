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
	"github.com/some-du6e/pairtime/internal/platform"
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
// a log record's mono counts back from: a day after the capture, as for a
// lookback.
const logBase = 1_795_280_000_000

func logRaw(l txLine, idle, replay bool) adapters.RawRecord {
	return logRawAt(l, idle, replay, logBase)
}

// logRawAt is logRaw for a line the agent read at Unix ms at.
func logRawAt(l txLine, idle, replay bool, at int64) adapters.RawRecord {
	raw := adapters.RawRecord{Source: adapters.SourceLog, Payload: l.text,
		Arrival: adapters.Arrival{Via: event.ViaLog, T: at, Mono: 10_000_000, BootID: "replay"},
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

// readDelay is how long after a line is written the replay with hooks has
// the tailers read it.
const readDelay = 50 * time.Millisecond

// readWithHooks replays the capture as the agent takes it in live: every
// hook call, delivered as way says, and every transcript line, read
// readDelay after it and the lines above it were written, in the order
// the agent gets them, a hook call before a line read at the same moment.
// It returns the hooks' records and the transcripts' apart.
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
		// Claude Code stamps some entries a few ms before the line above
		// them, and the tailers hand a file's lines over in its order: a
		// line is read no earlier than the one before it.
		var at time.Time
		for i := range ls {
			if ls[i].t.After(at) {
				at = ls[i].t
			}
			steps = append(steps, step{at: at.Add(readDelay), line: &ls[i]})
		}
		// The tailers' idle markers: IdleAfter after the file's last line,
		// and then every IdleAfter while the adapter holds something back,
		// for which one startGrace later stands, by when a session.start
		// that waits for its hooks has waited long enough.
		idle := at.Add(readDelay + 10*time.Second)
		steps = append(steps, step{at: idle, line: &ls[len(ls)-1], idle: true},
			step{at: idle.Add(startGrace * time.Millisecond), line: &ls[len(ls)-1], idle: true})
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
		recs, err := a.Parse(logRawAt(*s.line, s.idle, false, s.at.UnixMilli()))
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
				// arrival less duration_ms, the transcript's from its
				// entries, and for a call the person approved, which waited
				// for them, from its result alone. Every call of the capture
				// ran under 5 s.
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
	for _, read := range []struct {
		how  string
		recs []adapters.Record
	}{{"read alone", alone}, {"read with hooks only the spool delivered", withSpooled}} {
		for sid, ep := range want {
			t.Run(read.how+", "+sid[:8]+" gets one session.start with entrypoint "+string(ep), func(t *testing.T) {
				got := ofKind[*event.SessionStart](sessionOf(read.recs, sid))
				if len(got) != 1 {
					t.Fatalf("%d session.starts, want 1", len(got))
				}
				st := got[0].Event.Data.(*event.SessionStart)
				if st.Entrypoint != ep || st.ParentClass != event.ParentUnknown || st.HasTTY || st.StartKind != event.StartStartup {
					t.Errorf("session.start %+v, want startup, %s, parent unknown, no TTY", *st, ep)
				}
			})
		}
	}
	t.Run("read with their hooks via hook, the transcripts give no session.start", func(t *testing.T) {
		if got := ofKind[*event.SessionStart](withHooks); len(got) != 0 {
			t.Errorf("%d session.starts", len(got))
		}
	})
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
		id, ok := ids["7ca5940f"]
		if !ok {
			t.Fatal("the capture has no pickup 7ca5940f")
		}
		key := adapters.FixtureKeys().Turn(mainSession, id)
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
// writes each prompt and the queued message again, which pkg/score takes
// with the hook's for one act and CC1 needs, the interrupt, the text the
// hooks don't see and the tools they don't, but no session.start, no final
// output or stop, and no Edit, Write or Bash call. With hooks only the spool
// delivered, which the scorer leaves out of a session a log reader covers
// (DESIGN 5.2), the transcript's copies of those are the ones that count,
// and it writes them.
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
		{"the queued message, as when it read alone", logs, isKind(event.KindAgentQueue), count(alone, isKind(event.KindAgentQueue))},
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
		{"a labeled prompt whose text is an interrupt's is a prompt", userText("q1", "[Request interrupted by user]", map[string]any{"turnOrigin": "human", "promptSource": "typed"}), false, event.DeclaredNone},
		{"a prompt with neither label, as an older version writes it, declares nothing and is a failure", userText("q1", "hello there", nil), true, event.DeclaredNone},
		{"a task notification with neither label declares subagent and is a failure", userText("q1", "<task-notification>a report</task-notification>", nil), true, event.DeclaredSubagent},
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

// TestTranscriptUnlabeledEntries checks which main-conversation user
// entries with neither turnOrigin nor promptSource the reader takes for a
// prompt, each with a parse failure: one a person typed. A slash command's
// expanded instructions (isMeta), a local command's output, bash mode's
// lines, a memory note and a compaction's summary are none, labeled or
// not, and nor is the pickup of a message queued while the turn before
// ran, whose agent.queue already recorded it (E-cc-queued-turn). A task
// notification is never that pickup, and ends no message's wait.
func TestTranscriptUnlabeledEntries(t *testing.T) {
	labeled := userText("q1", "fix the failing test", map[string]any{"turnOrigin": "human", "promptSource": "typed",
		"timestamp": "2026-11-20T17:00:00.000Z"})
	enqueue := entryJSON("queue-operation", map[string]any{"operation": "enqueue", "content": "and then the docs",
		"timestamp": "2026-11-20T17:00:10.000Z"})
	const report = "<task-notification>\n<task-id>b1</task-id>\n<status>completed</status>\n</task-notification>"
	notified := entryJSON("queue-operation", map[string]any{"operation": "enqueue", "content": report,
		"timestamp": "2026-11-20T17:00:11.000Z"})
	summary := "This session is being continued from a previous conversation that ran out of context."
	cases := []struct {
		name    string
		entries []string
		// prompts are the prompt.submits of the last entry, and fails how
		// many of the entries are parse failures.
		prompts, fails int
	}{
		{"an unlabeled entry with text is a prompt and a failure", []string{userText("q2", "hello there", nil)}, 1, 1},
		{"an unlabeled isMeta entry is neither", []string{userText("q2", "the command's instructions", map[string]any{"isMeta": true})}, 0, 0},
		{"an unlabeled local command's output is neither", []string{userText("q2", "<local-command-stdout>done</local-command-stdout>", nil)}, 0, 0},
		{"an unlabeled entry with no text is neither", []string{userText("q2", "  ", nil)}, 0, 0},
		{"bash mode's unlabeled command is neither", []string{userText("q2", "<bash-input>git status</bash-input>", nil)}, 0, 0},
		{"bash mode's unlabeled output is neither",
			[]string{userText("q2", "<bash-stdout>On branch main</bash-stdout><bash-stderr></bash-stderr>", nil)}, 0, 0},
		{"an unlabeled memory note is neither", []string{userText("q2", "<user-memory-input>use tabs</user-memory-input>", nil)}, 0, 0},
		{"an unlabeled compaction summary is neither",
			[]string{userText("q2", summary, map[string]any{"isCompactSummary": true, "isVisibleInTranscriptOnly": true})}, 0, 0},
		{"an unlabeled entry shown in the transcript only is neither",
			[]string{userText("q2", summary, map[string]any{"isVisibleInTranscriptOnly": true})}, 0, 0},
		{"a compaction summary with the running turn's labels is no prompt either",
			[]string{labeled, userText("q1", summary, map[string]any{"isCompactSummary": true, "turnOrigin": "human", "promptSource": "typed"})}, 0, 0},
		{"the unlabeled pickup of a message queued in the turn before is no prompt, and a failure",
			[]string{labeled, enqueue, userText("q2", "and then the docs", nil)}, 0, 1},
		{"an unlabeled entry after the pickup is a prompt again, and both are failures",
			[]string{labeled, enqueue, userText("q2", "and then the docs", nil), userText("q3", "now the release notes", nil)}, 1, 2},
		{"a labeled pickup takes the wait up too",
			[]string{labeled, enqueue, userText("q2", "and then the docs", map[string]any{"turnOrigin": "human", "promptSource": "queued"}),
				userText("q3", "now the release notes", nil)}, 1, 1},
		{"a message removed from the queue is no pickup's",
			[]string{labeled, enqueue, entryJSON("queue-operation", map[string]any{"operation": "remove", "timestamp": "2026-11-20T17:00:12.000Z"}),
				userText("q2", "and then the docs", nil)}, 1, 1},
		{"an unlabeled task notification taken up while a message waits is a prompt of its own, and a failure",
			[]string{labeled, enqueue, notified, userText("q2", report, nil)}, 1, 1},
		{"and the waiting message's pickup after it is still no prompt",
			[]string{labeled, enqueue, notified, userText("q2", report, nil), userText("q3", "and then the docs", nil)}, 0, 2},
		{"a task notification removed from the queue leaves the person's message waiting",
			[]string{labeled, enqueue, entryJSON("queue-operation", map[string]any{"operation": "remove", "content": report,
				"timestamp": "2026-11-20T17:00:12.000Z"}), userText("q2", "and then the docs", nil)}, 0, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := newTest()
			var recs []adapters.Record
			var errs []error
			for _, l := range logLines(tc.entries...) {
				var err error
				if recs, err = a.Parse(logRaw(l, false, false)); err != nil {
					errs = append(errs, err)
				}
			}
			if len(errs) != tc.fails {
				t.Errorf("parse failures %v, want %d", errs, tc.fails)
			}
			if ps := ofKind[*event.PromptSubmit](recs); len(ps) != tc.prompts {
				t.Errorf("the last entry's records %v, want %d prompt.submit", recs, tc.prompts)
			}
		})
	}
}

// TestTranscriptNativeAtItsStop checks a session whose hook calls came
// through the spool until its turn's Stop, the first that came via hook, as
// one started while the agent was down: that Stop reports the turn's final
// output and stop, so the transcript's final message, stamped a little
// before the Stop and read after it, adds neither. A turn whose Stop the
// spool delivered keeps the transcript's.
func TestTranscriptNativeAtItsStop(t *testing.T) {
	const at = 1_795_194_000_000 // 2026-11-20T17:00:00Z, the entries' time
	spooled := func(ms int64) *adapters.Arrival {
		return &adapters.Arrival{Via: event.ViaPlugin, T: ms, Mono: 5000, BootID: "boot", Harness: event.HarnessClaudeCode}
	}
	walked := func(ms int64) *adapters.Arrival {
		return &adapters.Arrival{Via: event.ViaHook, T: ms, Mono: 5000, BootID: "boot", Harness: event.HarnessClaudeCode,
			Walk: &platform.Lineage{ParentClass: event.ParentTerminalApp, Entrypoint: event.EntrypointUnknown, HasTTY: true}}
	}
	cases := []struct {
		name string
		stop *adapters.Arrival
		// fromLog is the final outputs and the stops the transcript gives.
		fromLog int
	}{
		{"a turn whose Stop came via hook gets no final output or stop from the transcript", walked(at + 2_150), 0},
		{"a turn whose Stop the spool delivered keeps the transcript's", spooled(at + 2_150), 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := newTest()
			call := func(name string, payload map[string]any, arr *adapters.Arrival) {
				t.Helper()
				payload["session_id"], payload["cwd"] = "s1", "/home/capture/p"
				if _, err := a.Parse(hookRaw(name, payload, arr)); err != nil {
					t.Fatalf("%s: %v", name, err)
				}
			}
			var logs []adapters.Record
			read := func(l txLine) {
				t.Helper()
				recs, err := a.Parse(logRaw(l, false, false))
				if err != nil {
					t.Fatal(err)
				}
				logs = append(logs, recs...)
			}
			lines := logLines(
				userText("q1", "fix the failing test", map[string]any{"turnOrigin": "human", "promptSource": "typed"}),
				assistantText("m1", "end_turn", "Done: the test passes.", "2026-11-20T17:00:02.000Z"),
				entryJSON("system", map[string]any{"subtype": "turn_duration", "timestamp": "2026-11-20T17:00:02.300Z"}))
			call(EventSessionStart, map[string]any{"source": "startup"}, spooled(at-1_000))
			call(EventUserPromptSubmit, map[string]any{"prompt_id": "q1", "prompt": "fix the failing test"}, spooled(at))
			read(lines[0])
			read(lines[1])
			call(EventStop, map[string]any{"prompt_id": "q1", "last_assistant_message": "Done: the test passes.",
				"stop_hook_active": false}, tc.stop)
			read(lines[2])
			finals := 0
			for _, r := range ofKind[*event.AgentOutput](logs) {
				if r.Event.Data.(*event.AgentOutput).IsFinal {
					finals++
				}
			}
			if stops := len(ofKind[*event.AgentStop](logs)); finals != tc.fromLog || stops != tc.fromLog {
				t.Errorf("the transcript gave %d final outputs and %d stops, want %d of each", finals, stops, tc.fromLog)
			}
		})
	}
}

// TestTranscriptHistoryOfAHookedSession checks a session whose first
// via-hook call came before lookback read its history (hookedAt): an entry
// from before that call, by more than startGrace, keeps the reader's
// session.start, which the hook's own, dated later, can't stand for in SM
// 2.2 rules 4 and 5, and an entry from since gets none.
func TestTranscriptHistoryOfAHookedSession(t *testing.T) {
	const hooked = 1_795_194_000_000 // 2026-11-20T17:00:00Z, the session's first via-hook call
	walked := &adapters.Arrival{Via: event.ViaHook, T: hooked, Mono: 5000, BootID: "boot", Harness: event.HarnessClaudeCode,
		Walk: &platform.Lineage{ParentClass: event.ParentTerminalApp, Entrypoint: event.EntrypointUnknown, HasTTY: true}}
	cases := []struct {
		name string
		// at is the entry's time, and starts the session.starts the
		// transcript gives for it.
		at     string
		starts int
	}{
		{"an entry from a day before the session's first via-hook call keeps the reader's session.start", "2026-11-19T17:00:00.000Z", 1},
		{"an entry from within startGrace before that call gets none", "2026-11-20T16:59:30.000Z", 0},
		{"an entry from after that call gets none", "2026-11-20T17:00:05.000Z", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := newTest()
			if _, err := a.Parse(hookRaw(EventSessionStart, map[string]any{"session_id": "s1", "cwd": "/home/capture/p",
				"source": "startup"}, walked)); err != nil {
				t.Fatal(err)
			}
			recs := readLog(t, a, "p/s.jsonl", logLines(userText("q1", "fix the failing test",
				map[string]any{"turnOrigin": "human", "promptSource": "typed", "timestamp": tc.at})))
			if got := len(ofKind[*event.SessionStart](recs)); got != tc.starts {
				t.Errorf("%d session.starts from the transcript, want %d", got, tc.starts)
			}
		})
	}
}

// TestTranscriptReplayOfAHookedSession checks that a hooked session's old
// lines, read again as replays, as a Rewind reads a file taken up at its
// end, give no session.start: the hook's start is the session's, and the
// next line read live would write the replay's dated at the session's first
// entry, before any stretch the ledger knows of. Nor does such a replay's
// start count as the session's token once its hooks came, so an old line
// read live after them, as lookback reads the history before a session's
// hooks, gets the start of its own. The Rewind's mark survives a restart,
// in the Rewind's middle or after it. An unhooked session's replays still
// give the reader's start, its only one.
func TestTranscriptReplayOfAHookedSession(t *testing.T) {
	const hooked = 1_795_194_000_000 // 2026-11-20T17:00:00Z, the session's first via-hook call
	walked := &adapters.Arrival{Via: event.ViaHook, T: hooked, Mono: 5000, BootID: "boot", Harness: event.HarnessClaudeCode,
		Walk: &platform.Lineage{ParentClass: event.ParentTerminalApp, Entrypoint: event.EntrypointUnknown, HasTTY: true}}
	cases := []struct {
		name string
		// hook is when the session's first via-hook call comes: "first",
		// before the replays; "after", between them and the line read live;
		// "" for never. live is that line's time. restart is when the
		// reader's memory goes to another reader: "mid", between the two
		// replays; "late", after the line read live, which then reads the
		// old lines again as replays and that line again; "" for never.
		// starts is how many session.starts the transcript gives, and at,
		// when set, the time of the one it gives.
		hook, live, restart string
		starts              int
		at                  int64
	}{
		{"a hooked session's old lines read again as replays, then a line read live, give no session.start",
			"first", "2026-11-21T17:33:00.000Z", "", 0, 0},
		{"replays from before the session's first via-hook call, which comes before the next line read live, give no session.start",
			"after", "2026-11-21T17:33:00.000Z", "", 0, 0},
		{"an old line read live after them and after that call, older than its grace, gets one session.start, dated at it",
			"after", "2026-11-19T18:00:00.000Z", "", 1, 1_795_111_200_000},
		{"a restart between a hooked session's replays leaves the rest of the Rewind's without a session.start",
			"first", "2026-11-21T17:33:00.000Z", "mid", 0, 0},
		{"and the Rewind's lines read again as a restart's replays after it ended give none either",
			"first", "2026-11-21T17:33:00.000Z", "late", 0, 0},
		{"an unhooked session's old lines read again as replays give the reader's session.start at the next line read live",
			"", "2026-11-21T17:33:00.000Z", "", 1, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lines := logLines(
				userText("q1", "fix the failing test", map[string]any{"turnOrigin": "human", "promptSource": "typed",
					"timestamp": "2026-11-19T17:00:00.000Z"}),
				assistantText("m1", "end_turn", "Done.", "2026-11-19T17:00:09.000Z"),
				userText("q2", "now the docs", map[string]any{"turnOrigin": "human", "promptSource": "typed", "timestamp": tc.live}),
			)
			a := newTest()
			hook := func() {
				if _, err := a.Parse(hookRaw(EventSessionStart, map[string]any{"session_id": "s1", "cwd": "/home/capture/p",
					"source": "resume"}, walked)); err != nil {
					t.Fatal(err)
				}
			}
			restart := func() {
				mem, err := a.SaveState()
				if err != nil {
					t.Fatal(err)
				}
				a = newTest()
				if err := a.LoadState(mem); err != nil {
					t.Fatal(err)
				}
			}
			replay := func() {
				for i, l := range lines[:2] {
					if tc.restart == "mid" && i == 1 {
						restart()
					}
					if _, err := a.Parse(logRaw(l, false, true)); err != nil {
						t.Fatal(err)
					}
				}
			}
			var recs []adapters.Record
			live := func() {
				for _, raw := range []adapters.RawRecord{logRaw(lines[2], false, false), logRaw(lines[2], true, false)} {
					got, err := a.Parse(raw)
					if err != nil {
						t.Fatal(err)
					}
					recs = append(recs, got...)
				}
			}
			if tc.hook == "first" {
				hook()
			}
			replay()
			if tc.hook == "after" {
				hook()
			}
			live()
			if tc.restart == "late" {
				restart()
				replay()
				live()
			}
			starts := ofKind[*event.SessionStart](recs)
			if len(starts) != tc.starts || tc.at != 0 && starts[0].Event.T != tc.at {
				t.Errorf("%d session.starts from the transcript, %v; want %d, at %d", len(starts), starts, tc.starts, tc.at)
			}
		})
	}
}

// TestTranscriptRestartOfAHookedSession checks that a restart's replays,
// which continue the memory the reader saved (LogResume), keep the rule by
// time for a hooked session: the start of a line from before its hooks
// came, by more than the grace, still waiting when the agent stopped,
// comes after the restart, as without one. Only a Rewind's replays, of a
// file the reader had no memory of, give none
// (TestTranscriptReplayOfAHookedSession).
func TestTranscriptRestartOfAHookedSession(t *testing.T) {
	const hooked = 1_795_194_000_000 // 2026-11-20T17:00:00Z, the session's first via-hook call
	walked := &adapters.Arrival{Via: event.ViaHook, T: hooked, Mono: 5000, BootID: "boot", Harness: event.HarnessClaudeCode,
		Walk: &platform.Lineage{ParentClass: event.ParentTerminalApp, Entrypoint: event.EntrypointUnknown, HasTTY: true}}
	// The line is from 200 s before that call, and the agent reads it 10 s
	// after it was written, so its start waits.
	const entry = hooked - 200_000
	l := logLines(userText("q1", "fix the failing test", map[string]any{"turnOrigin": "human", "promptSource": "typed",
		"timestamp": "2026-11-20T16:56:40.000Z"}))[0]
	cases := []struct {
		name string
		// restart says the agent stops while the start waits, and the
		// restarted reader reads the line again as a replay.
		restart bool
	}{
		{"a start still waiting when the agent stopped comes after the restart", true},
		{"as it comes without one", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := newTest()
			if _, err := a.Parse(hookRaw(EventSessionStart, map[string]any{"session_id": "s1", "cwd": "/home/capture/p",
				"source": "startup"}, walked)); err != nil {
				t.Fatal(err)
			}
			if _, err := a.Parse(logRawAt(l, false, false, entry+10_000)); err != nil {
				t.Fatal(err)
			}
			if tc.restart {
				mem, err := a.SaveState()
				if err != nil {
					t.Fatal(err)
				}
				a = newTest()
				if err := a.LoadState(mem); err != nil {
					t.Fatal(err)
				}
				if _, err := a.Parse(logRawAt(l, false, true, entry+20_000)); err != nil {
					t.Fatal(err)
				}
			}
			recs, err := a.Parse(logRawAt(l, true, false, entry+95_000))
			if err != nil {
				t.Fatal(err)
			}
			if got := len(ofKind[*event.SessionStart](recs)); got != 1 {
				t.Errorf("%d session.starts at the line's grace, want 1", got)
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

// toolUse writes an assistant entry that asks for tool name with input.
func toolUse(id, name string, input any, ts string) string {
	return entryJSON("assistant", map[string]any{"timestamp": ts, "message": map[string]any{"id": "m_" + id, "role": "assistant",
		"stop_reason": "tool_use", "content": []any{map[string]any{"type": "tool_use", "id": id, "name": name, "input": input}}}})
}

// toolDone writes the user entry that holds the result of tool use id,
// with result as its toolUseResult.
func toolDone(id string, result any, ts string) string {
	return entryJSON("user", map[string]any{"timestamp": ts, "promptId": "q1", "toolUseResult": result,
		"message": map[string]any{"role": "user", "content": []any{map[string]any{"type": "tool_result", "tool_use_id": id, "content": "done"}}}})
}

// TestTranscriptToolShapes checks that a field a tool gives a type of its
// own costs the reader that field, never the entry: tools put fields of
// other types under the names the reader reads (objectFields).
func TestTranscriptToolShapes(t *testing.T) {
	prompt := userText("q1", "run it", map[string]any{"turnOrigin": "human", "promptSource": "typed"})
	cases := []struct {
		name  string
		lines []string
		// class is the class of the one tool call, "" for none.
		class event.ToolClass
		fails bool
	}{
		{"a synchronous Agent call's result, whose content is a list, is a subagent call and no failure",
			[]string{toolUse("a1", "Agent", map[string]any{"prompt": "look", "subagent_type": "Explore"}, "2026-11-20T17:00:01.000Z"),
				toolDone("a1", map[string]any{"status": "completed", "agentId": "x", "content": []any{map[string]any{"type": "text", "text": "found"}},
					"totalDurationMs": 4000}, "2026-11-20T17:00:05.000Z")},
			event.ToolSubagent, false},
		{"an MCP tool's input whose command isn't a string is no failure",
			[]string{toolUse("m1", "mcp__db__query", map[string]any{"command": map[string]any{"sql": "select 1"}}, "2026-11-20T17:00:01.000Z"),
				toolDone("m1", map[string]any{"rows": 1}, "2026-11-20T17:00:02.000Z")},
			event.ToolOther, false},
		{"an edit's structuredPatch that isn't a list of hunks is a failure, and the call still counts",
			[]string{toolUse("e1", "Edit", map[string]any{"file_path": "/home/capture/p/a.go"}, "2026-11-20T17:00:01.000Z"),
				toolDone("e1", map[string]any{"filePath": "/home/capture/p/a.go", "structuredPatch": "none"}, "2026-11-20T17:00:02.000Z")},
			event.ToolEdit, true},
		{"an attachment whose met isn't a boolean is no failure",
			[]string{entryJSON("attachment", map[string]any{"attachment": map[string]any{"type": "goal_status", "met": "yes"}})},
			"", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := newTest()
			var recs []adapters.Record
			var errs []error
			for _, l := range logLines(append([]string{prompt}, tc.lines...)...) {
				r, err := a.Parse(logRaw(l, false, false))
				recs = append(recs, r...)
				if err != nil {
					errs = append(errs, err)
				}
			}
			tools := ofKind[*event.AgentTool](recs)
			switch {
			case tc.class == "" && len(tools) != 0:
				t.Errorf("tools %v, want none", tools)
			case tc.class != "" && (len(tools) != 1 || tools[0].Event.Data.(*event.AgentTool).ToolClass != tc.class):
				t.Errorf("tools %v, want one of class %s", tools, tc.class)
			}
			if (len(errs) > 0) != tc.fails {
				t.Errorf("failures %v, want some: %v", errs, tc.fails)
			}
		})
	}
}

// TestTranscriptLetsGo checks that once a file is idle the reader holds
// back nothing that no later line will resolve, so the file's memory
// moves on (LogResume) and the tailers stop sending idle markers: a slash
// command that started no turn, and a tool use whose result never came
// within maxToolWait.
func TestTranscriptLetsGo(t *testing.T) {
	prompt := userText("q1", "run it", map[string]any{"turnOrigin": "human", "promptSource": "typed"})
	exit := []string{
		userText("q9", "<command-name>/exit</command-name>\n<command-message>exit</command-message>\n<command-args></command-args>", nil),
		userText("q9", "<local-command-stdout>Catch you later!</local-command-stdout>", nil),
	}
	use := toolUse("b1", "Bash", map[string]any{"command": "sleep 100000"}, "2026-11-20T17:00:01.000Z")
	at := func(s string) int64 { ts, _ := time.Parse(time.RFC3339Nano, s); return ts.UnixMilli() }
	cases := []struct {
		name  string
		lines []string
		// at is when the agent reads the lines, in Unix ms, past the
		// session.start's grace, and idle whether the idle marker follows.
		at   int64
		idle bool
		free bool
	}{
		{"/exit's output lets its markup go at once", exit, at("2026-11-20T17:02:00.000Z"), false, true},
		{"a file that ends in a command's markup holds nothing back once idle", exit[:1], at("2026-11-20T17:02:00.000Z"), true, true},
		{"a tool use whose result hasn't come holds the file back within maxToolWait", []string{prompt, use},
			at("2026-11-20T17:00:01.000Z") + maxToolWait - 1, true, false},
		{"a tool use whose result hasn't come in maxToolWait is let go once idle", []string{prompt, use},
			at("2026-11-20T17:00:01.000Z") + maxToolWait, true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := newTest()
			ls := logLines(tc.lines...)
			for _, l := range ls {
				if _, err := a.Parse(logRawAt(l, false, false, tc.at)); err != nil {
					t.Fatal(err)
				}
			}
			last := ls[len(ls)-1]
			if tc.idle {
				if _, err := a.Parse(logRawAt(last, true, false, tc.at)); err != nil {
					t.Fatal(err)
				}
			}
			resume, _ := a.LogResume(last.file)
			if free := resume == last.end; free != tc.free {
				t.Errorf("the memory starts at %d of %d, want it let go: %v", resume, last.end, tc.free)
			}
		})
	}
}

// TestTranscriptTokenChanges checks E-cc-transcript-labels' rule for a
// session no hook reported: a session.start at the first entry that
// states a token and another wherever it changes, a change that comes
// while the first start waits for startGrace included.
func TestTranscriptTokenChanges(t *testing.T) {
	entry := func(tok, ts string) string {
		return userText("q1", "hello there", map[string]any{"turnOrigin": "human", "promptSource": "typed", "entrypoint": tok, "timestamp": ts})
	}
	at := func(s string) int64 { ts, _ := time.Parse(time.RFC3339Nano, s); return ts.UnixMilli() }
	cases := []struct {
		name  string
		lines []string
		want  []event.Entrypoint
	}{
		{"a token stated twice gives one start", []string{entry("cli", "2026-11-20T17:00:00.000Z"), entry("cli", "2026-11-20T17:00:10.000Z")},
			[]event.Entrypoint{event.EntrypointUnknown}},
		{"a token that changes within startGrace gives a start of its own", []string{entry("cli", "2026-11-20T17:00:00.000Z"), entry("sdk-cli", "2026-11-20T17:00:10.000Z")},
			[]event.Entrypoint{event.EntrypointUnknown, event.EntrypointSDK}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := newTest()
			ls := logLines(tc.lines...)
			var recs []adapters.Record
			for _, l := range ls {
				r, err := a.Parse(logRawAt(l, false, false, at("2026-11-20T17:00:11.000Z")))
				if err != nil {
					t.Fatal(err)
				}
				recs = append(recs, r...)
			}
			r, err := a.Parse(logRawAt(ls[len(ls)-1], true, false, at("2026-11-20T17:05:00.000Z")))
			if err != nil {
				t.Fatal(err)
			}
			recs = append(recs, r...)
			var got []event.Entrypoint
			for _, s := range ofKind[*event.SessionStart](recs) {
				got = append(got, s.Event.Data.(*event.SessionStart).Entrypoint)
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("session.starts %v, want %v", got, tc.want)
			}
		})
	}
}

// TestTranscriptTextBlockKeys checks that each text block of a turn's
// last message makes records of its own key, so that no two records of
// different blocks share an event_id (adapters.Record.LogKey).
func TestTranscriptTextBlockKeys(t *testing.T) {
	msg := entryJSON("assistant", map[string]any{"timestamp": "2026-11-20T17:00:05.000Z", "message": map[string]any{"id": "m1", "role": "assistant",
		"stop_reason": "end_turn", "content": []any{map[string]any{"type": "text", "text": "One."}, map[string]any{"type": "text", "text": "Two."},
			map[string]any{"type": "text", "text": "Three?"}}}})
	cases := []struct {
		name string
		ok   func([]adapters.Record) bool
	}{
		{"two outputs before the final one, each block's", func(recs []adapters.Record) bool {
			var finals, others int
			for _, r := range ofKind[*event.AgentOutput](recs) {
				if r.Event.Data.(*event.AgentOutput).IsFinal {
					finals++
				} else {
					others++
				}
			}
			return finals == 1 && others == 2
		}},
		{"no two records share a key", func(recs []adapters.Record) bool {
			seen := map[string]bool{}
			for _, r := range recs {
				if seen[r.LogKey] {
					return false
				}
				seen[r.LogKey] = true
			}
			return true
		}},
	}
	a := newTest()
	ls := logLines(userText("q1", "say three things", map[string]any{"turnOrigin": "human", "promptSource": "typed"}), msg)
	var recs []adapters.Record
	for _, l := range ls {
		r, err := a.Parse(logRaw(l, false, false))
		if err != nil {
			t.Fatal(err)
		}
		recs = append(recs, r...)
	}
	r, err := a.Parse(logRaw(ls[len(ls)-1], true, false))
	if err != nil {
		t.Fatal(err)
	}
	recs = append(recs, r...)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if !tc.ok(recs) {
				for _, r := range recs {
					t.Logf("%s %s", r.Event.Kind(), r.LogKey)
				}
				t.Error("the records don't keep each block apart")
			}
		})
	}
}

// TestNativeCutoffOutlivesHookMemory checks that the reader keeps a
// session's native cutoff once the hook calls' memory of the session is
// gone, as after 256 other sessions: it still leaves the finals and stops
// to the hooks that reported them.
func TestNativeCutoffOutlivesHookMemory(t *testing.T) {
	a := newTest()
	if _, fails := adapters.Replay(a, readCalls(t), adapters.ReplayOptions{GOOS: "linux", Way: adapters.Walked}); len(fails) > 0 {
		t.Fatalf("the hook calls failed: %v", fails)
	}
	for sid := range a.sessions {
		delete(a.sessions, sid)
	}
	files := readTranscripts(t)
	var recs []adapters.Record
	for _, k := range sessionFiles(files) {
		recs = append(recs, readLog(t, a, k, files[k])...)
	}
	count := func(keep func(adapters.Record) bool) int {
		n := 0
		for _, r := range recs {
			if keep(r) {
				n++
			}
		}
		return n
	}
	cases := []struct {
		name string
		keep func(adapters.Record) bool
	}{
		{"no stop", func(r adapters.Record) bool { return r.Event.Kind() == event.KindAgentStop }},
		{"no final output", func(r adapters.Record) bool {
			o, ok := r.Event.Data.(*event.AgentOutput)
			return ok && o.IsFinal
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if n := count(tc.keep); n != 0 {
				t.Errorf("%d such records, want none: the hooks reported them", n)
			}
		})
	}
}
