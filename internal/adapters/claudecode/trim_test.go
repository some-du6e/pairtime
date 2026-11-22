// SPDX-License-Identifier: AGPL-3.0-or-later

package claudecode

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/adapters"
	"github.com/some-du6e/pairtime/pkg/event"
)

// TestTrimLog checks the lines the lookback reader's child process sends
// the agent (WP-AD3): each captured transcript line, trimmed, gives the
// same records and the same problems as the line itself, read by an
// adapter of its own in the same order, so the agent's share of lookback
// is the decoding of what it records, and nothing is lost by it. Beside
// the capture's transcripts it reads one with the shapes the capture
// lacks: a synchronous Agent result, an edit's malformed structuredPatch,
// an attachment whose fields have other types, and two entries with
// neither label, a person's prompt and one marked isMeta, which only the
// first of is a prompt.
func TestTrimLog(t *testing.T) {
	files := readTranscripts(t)
	files["p/s.jsonl"] = logLines(
		userText("q1", "run it", map[string]any{"turnOrigin": "human", "promptSource": "typed"}),
		toolUse("a1", "Agent", map[string]any{"prompt": "look"}, "2026-11-20T17:00:01.000Z"),
		toolDone("a1", map[string]any{"status": "completed", "content": []any{map[string]any{"type": "text", "text": "found"}}}, "2026-11-20T17:00:05.000Z"),
		toolUse("e1", "Edit", map[string]any{"file_path": "/home/capture/p/a.go"}, "2026-11-20T17:00:06.000Z"),
		toolDone("e1", map[string]any{"filePath": "/home/capture/p/a.go", "structuredPatch": "none"}, "2026-11-20T17:00:07.000Z"),
		entryJSON("attachment", map[string]any{"attachment": map[string]any{"type": "goal_status", "met": "yes", "sentinel": 1}}),
		userText("q2", "Expanded instructions for the command.", map[string]any{"isMeta": true}),
		userText("q3", "and then the tests, please", nil),
	)
	whole, trimmed := newTest(), newTest()
	var wholeBytes, trimmedBytes int
	type diff struct {
		where      string
		got, want  any
		gerr, werr error
	}
	var diffs []diff
	for _, k := range sessionFiles(files) {
		ls := files[k]
		for i, l := range ls {
			short, err := trimmed.TrimLog(l.text)
			if err != nil {
				t.Fatalf("%s line %d: %v", k, i, err)
			}
			wholeBytes += len(l.text)
			trimmedBytes += len(short)
			want, werr := whole.Parse(logRaw(l, false, false))
			sl := l
			sl.text = short
			got, gerr := trimmed.Parse(logRaw(sl, false, false))
			if !reflect.DeepEqual(got, want) || (gerr == nil) != (werr == nil) || gerr != nil && gerr.Error() != werr.Error() {
				diffs = append(diffs, diff{where: k + " line " + strconv.Itoa(i), got: got, want: want, gerr: gerr, werr: werr})
			}
		}
		last := ls[len(ls)-1]
		want, _ := whole.Parse(logRaw(last, true, false))
		got, _ := trimmed.Parse(logRaw(last, true, false))
		if !reflect.DeepEqual(got, want) {
			diffs = append(diffs, diff{where: k + " at its end", got: got, want: want})
		}
	}
	t.Logf("the capture's %d transcript bytes trim to %d", wholeBytes, trimmedBytes)
	_, notJSON := trimmed.TrimLog([]byte("{not json"))
	_, decodeErr := trimmed.DecodeLog([]byte("{not json"))
	other, otherErr := trimmed.TrimLog([]byte(`{"type":"file-history-snapshot","snapshot":{"trackedFileBackups":{"a.go":"x"}}}`))
	cases := []struct {
		name string
		ok   bool
	}{
		{"every captured line, trimmed, parses to the records and problems of the line itself", len(diffs) == 0},
		{"the trimmed lines are shorter", trimmedBytes < wholeBytes},
		{"a line that isn't JSON gives DecodeLog's error", notJSON != nil && decodeErr != nil && notJSON.Error() == decodeErr.Error()},
		{"an entry of a type the reader doesn't read keeps its type alone",
			otherErr == nil && string(other) == `{"type":"file-history-snapshot"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if !tc.ok {
				for _, d := range diffs[:min(len(diffs), 5)] {
					t.Errorf("%s: trimmed gives %+v, %v; whole gives %+v, %v", d.where, d.got, d.gerr, d.want, d.werr)
				}
				if len(diffs) == 0 {
					t.Errorf("trimmed %d of %d bytes; not JSON: %v, %v; other type: %s, %v", trimmedBytes, wholeBytes, notJSON, decodeErr, other, otherErr)
				}
			}
		})
	}
}

var _ adapters.LogReader = (*Adapter)(nil)

// TestTranscriptMemoryKeepsLiveFiles checks which file the reader's memory
// forgets when full (older): of the hundreds of histories lookback reads
// after a session being written now, those it read before the last few go
// first, oldest entries first. So the live session's next line finds its
// memory and isn't read again from its start, and the history lookback
// reads now isn't read again from its start each time a session starts.
func TestTranscriptMemoryKeepsLiveFiles(t *testing.T) {
	a := newTest()
	parse := func(file string, at time.Time) {
		t.Helper()
		l := logLines(userText("q1", "hello there", map[string]any{"turnOrigin": "human", "promptSource": "typed",
			"entrypoint": nil, "timestamp": at.Format(time.RFC3339Nano)}))[0]
		l.file = file
		if _, err := a.Parse(logRaw(l, false, false)); err != nil {
			t.Fatal(err)
		}
	}
	live := time.Date(2026, 11, 21, 10, 0, 0, 0, time.UTC)
	parse("p/live.jsonl", live)
	// Lookback reads the histories written last first, so each has older
	// entries than the one before it, and the memory is full.
	old := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	for i := range maxLogFiles - 1 {
		parse(fmt.Sprintf("p/old-%03d.jsonl", i), old.Add(-time.Duration(i)*time.Minute))
	}
	// The history read now has older entries yet, and new sessions' lines
	// come between its batches.
	parse("p/reading.jsonl", old.Add(-time.Duration(maxLogFiles)*time.Minute))
	for i := range 3 {
		parse(fmt.Sprintf("p/new-%d.jsonl", i), live.Add(time.Duration(i+1)*time.Minute))
	}
	remembered := func(file string) bool {
		_, ok := a.LogResume(file)
		return ok
	}
	raw, err := a.SaveState()
	if err != nil {
		t.Fatal(err)
	}
	var st savedState
	if err := json.Unmarshal(raw, &st); err != nil {
		t.Fatal(err)
	}
	saved := func(file string) bool {
		for _, f := range st.Files {
			if f.Key == file {
				return true
			}
		}
		return false
	}
	cases := []struct {
		name string
		ok   bool
	}{
		{"the session being written now is remembered", remembered("p/live.jsonl")},
		{"the memory saved for a restart keeps the session being written now, and the history read now",
			len(st.Files) == maxSavedFiles && saved("p/live.jsonl") && saved("p/reading.jsonl")},
		{"the history read now is remembered, though its entries are the oldest", remembered("p/reading.jsonl")},
		{"the sessions that started since are remembered", remembered("p/new-0.jsonl") && remembered("p/new-2.jsonl")},
		{"the history with the oldest entries, of those read before the last few, went first",
			!remembered(fmt.Sprintf("p/old-%03d.jsonl", maxLogFiles-2-recentFiles))},
		{"the histories read last before the one read now are remembered", remembered(fmt.Sprintf("p/old-%03d.jsonl", maxLogFiles-2))},
		{"a history with newer entries is remembered", remembered("p/old-000.jsonl")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if !tc.ok {
				t.Error("the reader forgot another file than the one it should have")
			}
		})
	}
}

// TestTranscriptMemoryKeepsLiveSessions checks which session the reader's
// memory forgets when full: one no remembered file names, so a session
// that was running at install, with no hooks, keeps the memory that its
// session.start was written while lookback reads hundreds of others and a
// hook call of a new session comes, and its next entry writes no second
// one, after a restart too.
func TestTranscriptMemoryKeepsLiveSessions(t *testing.T) {
	entry := func(sid, id string, at time.Time) string {
		return userText(id, "hello there", map[string]any{"turnOrigin": "human", "promptSource": "typed",
			"sessionId": sid, "entrypoint": "cli", "timestamp": at.Format(time.RFC3339Nano)})
	}
	starts := func(t *testing.T, a *Adapter, l txLine) int {
		t.Helper()
		recs, err := a.Parse(logRaw(l, false, false))
		if err != nil {
			t.Fatal(err)
		}
		n := 0
		for _, r := range recs {
			if _, ok := r.Event.Data.(*event.SessionStart); ok {
				n++
			}
		}
		return n
	}
	// The live session's entries are older than the read by more than the
	// hooks' grace, so its first writes its session.start at once.
	live := time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)
	lines := logLines(entry("live", "p1", live), entry("live", "p2", live.Add(time.Minute)))
	for i := range lines {
		lines[i].file = "p/live.jsonl"
	}
	a := newTest()
	first := starts(t, a, lines[0])
	// Lookback reads more histories than the memory holds, each of a
	// session of its own.
	old := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	for i := range maxLogSessions + 10 {
		l := logLines(entry(fmt.Sprintf("s-%03d", i), "q", old.Add(-time.Duration(i)*time.Minute)))[0]
		l.file = fmt.Sprintf("p/old-%03d.jsonl", i)
		starts(t, a, l)
	}
	// Then a hook call of a new session, which comes with no file of its
	// own to forget.
	if _, err := a.Parse(hookRaw(EventUserPromptSubmit, map[string]any{"session_id": "hooked", "prompt_id": "h1",
		"prompt": "and this?", "cwd": "/home/capture/p", "transcript_path": "/home/capture/p/hooked.jsonl"}, nil)); err != nil {
		t.Fatal(err)
	}
	mem, err := a.SaveState()
	if err != nil {
		t.Fatal(err)
	}
	restarted := newTest()
	if err := restarted.LoadState(mem); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		ok   bool
	}{
		{"the live session's first entry writes its session.start", first == 1},
		{"its next entry writes no second one", starts(t, a, lines[1]) == 0},
		{"nor after a restart", starts(t, restarted, lines[1]) == 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if !tc.ok {
				t.Error("the reader forgot that the live session's start was written")
			}
		})
	}
}
