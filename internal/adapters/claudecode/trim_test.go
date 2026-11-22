// SPDX-License-Identifier: AGPL-3.0-or-later

package claudecode

import (
	"reflect"
	"strconv"
	"testing"

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
