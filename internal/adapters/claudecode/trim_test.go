// SPDX-License-Identifier: AGPL-3.0-or-later

package claudecode

import (
	"fmt"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/adapters"
)

// TestTrimLog checks the lines the lookback reader's child process sends
// the agent (WP-AD3): each captured transcript line, trimmed, gives the
// same records and the same problems as the line itself, read by an
// adapter of its own in the same order, so the agent's share of lookback
// is the decoding of what it records, and nothing is lost by it. Beside
// the capture's transcripts it reads one with the shapes the capture
// lacks: a synchronous Agent result, an edit's malformed structuredPatch
// and an attachment whose fields have other types.
func TestTrimLog(t *testing.T) {
	files := readTranscripts(t)
	files["p/s.jsonl"] = logLines(
		userText("q1", "run it", map[string]any{"turnOrigin": "human", "promptSource": "typed"}),
		toolUse("a1", "Agent", map[string]any{"prompt": "look"}, "2026-11-20T17:00:01.000Z"),
		toolDone("a1", map[string]any{"status": "completed", "content": []any{map[string]any{"type": "text", "text": "found"}}}, "2026-11-20T17:00:05.000Z"),
		toolUse("e1", "Edit", map[string]any{"file_path": "/home/capture/p/a.go"}, "2026-11-20T17:00:06.000Z"),
		toolDone("e1", map[string]any{"filePath": "/home/capture/p/a.go", "structuredPatch": "none"}, "2026-11-20T17:00:07.000Z"),
		entryJSON("attachment", map[string]any{"attachment": map[string]any{"type": "goal_status", "met": "yes", "sentinel": 1}}),
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
// forgets when full (older): the hundreds of histories lookback reads
// after a session being written now go first, oldest entries first, so the
// live session's next line finds its memory and isn't read again from its
// start.
func TestTranscriptMemoryKeepsLiveFiles(t *testing.T) {
	a := newTest()
	prompt := func(file, ts string) adapters.RawRecord {
		l := logLines(userText("q1", "hello there", map[string]any{"turnOrigin": "human", "promptSource": "typed",
			"entrypoint": nil, "timestamp": ts}))[0]
		l.file = file
		return logRaw(l, false, false)
	}
	if _, err := a.Parse(prompt("p/live.jsonl", "2026-11-21T10:00:00.000Z")); err != nil {
		t.Fatal(err)
	}
	for i := range maxLogFiles {
		ts := time.Date(2026, 11, 1, 0, 0, i, 0, time.UTC).Format(time.RFC3339Nano)
		if _, err := a.Parse(prompt(fmt.Sprintf("p/old-%03d.jsonl", i), ts)); err != nil {
			t.Fatal(err)
		}
	}
	remembered := func(file string) bool {
		_, ok := a.LogResume(file)
		return ok
	}
	cases := []struct {
		name string
		ok   bool
	}{
		{"the session being written now is remembered", remembered("p/live.jsonl")},
		{"the history with the oldest entries went first", !remembered("p/old-000.jsonl")},
		{"a history with newer entries is remembered", remembered(fmt.Sprintf("p/old-%03d.jsonl", maxLogFiles-1))},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if !tc.ok {
				t.Error("the reader forgot another file than the one with the oldest entries")
			}
		})
	}
}
