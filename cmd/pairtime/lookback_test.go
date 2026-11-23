// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// lookbackOut reads what lookback's reader wrote: each line's trimmed
// entry, and whether it ended with its done line.
func lookbackOut(t *testing.T, out []byte) (lines []string, done bool) {
	t.Helper()
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		var l struct {
			L    json.RawMessage `json:"l"`
			Done bool            `json:"done"`
		}
		if err := json.Unmarshal(sc.Bytes(), &l); err != nil {
			t.Fatalf("lookback wrote %q: %v", sc.Text(), err)
		}
		if l.L != nil {
			lines = append(lines, string(l.L))
		}
		done = done || l.Done
	}
	return lines, done
}

// TestLookback checks `pairtime lookback`, lookback's reader (WP-AD3): it
// lowers its priority before it reads anything and reads nothing when it
// can't, and it writes each line of its job trimmed by the line's adapter,
// in this process and in one of its own, started as the agent starts it.
func TestLookback(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	line := `{"type":"user","sessionId":"s1","promptId":"q1","timestamp":"2026-11-20T17:00:00.000Z","cwd":"/p",` +
		`"version":"2.1.327","entrypoint":"cli","isSidechain":false,"promptSource":"typed","turnOrigin":"human",` +
		`"message":{"role":"user","content":"hello there"},"toolUseResult":{"file":{"content":"the whole file it read"}}}`
	if err := os.WriteFile(path, []byte(line+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	job, err := json.Marshal(map[string]any{"files": []any{map[string]any{"h": "claude_code", "p": path, "from": 0, "to": len(line) + 1}}})
	if err != nil {
		t.Fatal(err)
	}
	trimmed := func(lines []string) bool {
		return len(lines) == 1 && strings.Contains(lines[0], `"promptId":"q1"`) && !strings.Contains(lines[0], "the whole file")
	}
	cases := []struct {
		name string
		run  func(t *testing.T)
	}{
		{"it lowers its priority, then writes the job's lines trimmed by their adapter", func(t *testing.T) {
			var out, errOut bytes.Buffer
			lowered := false
			code := serveLookback(bytes.NewReader(job), &out, &errOut, func() error { lowered = true; return nil })
			lines, done := lookbackOut(t, out.Bytes())
			if code != 0 || !lowered || !done || !trimmed(lines) {
				t.Errorf("exit %d, lowered %v, done %v, lines %q; stderr %s", code, lowered, done, lines, errOut.String())
			}
		}},
		{"it reads nothing when it can't lower its priority", func(t *testing.T) {
			var out, errOut bytes.Buffer
			code := serveLookback(bytes.NewReader(job), &out, &errOut, func() error { return errors.New("not allowed") })
			if code != 1 || out.Len() != 0 || !strings.Contains(errOut.String(), "priority") {
				t.Errorf("exit %d, wrote %q, stderr %q", code, out.String(), errOut.String())
			}
		}},
		{"started as the agent starts it, it lowers its own priority and reads the job", func(t *testing.T) {
			child, err := startLookback()
			if err != nil {
				t.Fatal(err)
			}
			if _, err := child.In.Write(job); err != nil {
				t.Fatal(err)
			}
			child.In.Close()
			out, err := io.ReadAll(child.Out)
			if err != nil {
				t.Fatal(err)
			}
			werr := child.Wait()
			lines, done := lookbackOut(t, out)
			if werr != nil || !done || !trimmed(lines) {
				t.Errorf("exit %v, done %v, lines %q", werr, done, lines)
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, tc.run)
	}
}
