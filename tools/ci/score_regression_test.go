// SPDX-License-Identifier: AGPL-3.0-or-later

package ci

import (
	"bytes"
	"errors"
	"os/exec"
	"strings"
	"testing"
)

// TestScoreRegression runs score_regression.sh over fake diffs: a PR that
// rewrites a timeline's golden.jsonl changes the scorer's output, so it
// must change pkg/score/VERSION too (DESIGN 6.6; docs/PLAN.md, WP-SC4).
func TestScoreRegression(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skipf("score_regression.sh needs bash: %v", err)
	}
	const golden = "testdata/timelines/6.1/golden.jsonl"
	tests := []struct {
		name     string
		diff     []string
		wantCode int
		wantOut  string
	}{
		{name: "a modified golden without a version change fails", diff: []string{"M\t" + golden, "M\tpkg/score/credit.go"},
			wantCode: 1, wantOut: "::error file=" + golden + "::golden modified without a change to pkg/score/VERSION"},
		{name: "a modified golden with pkg/score/VERSION changed passes", diff: []string{"M\t" + golden, "M\tpkg/score/VERSION", "M\tpkg/score/credit.go"}},
		{name: "a golden of a nested timeline counts too", diff: []string{"M\ttestdata/timelines/sidedoor/a-track-jiggler/golden.jsonl"},
			wantCode: 1, wantOut: "sidedoor/a-track-jiggler/golden.jsonl"},
		{name: "a new golden for a new timeline passes, since it had no output before", diff: []string{"A\ttestdata/timelines/6.7/golden.jsonl", "A\ttestdata/timelines/6.7/events.jsonl"}},
		{name: "a deleted golden goes with its timeline and passes", diff: []string{"D\t" + golden, "D\ttestdata/timelines/6.1/events.jsonl"}},
		{name: "a golden renamed with every byte kept passes", diff: []string{"R100\t" + golden + "\ttestdata/timelines/6.1-old/golden.jsonl"}},
		{name: "a golden renamed with changes fails", diff: []string{"R087\t" + golden + "\ttestdata/timelines/6.1-old/golden.jsonl"},
			wantCode: 1, wantOut: "renamed with changes"},
		{name: "a golden that changed type fails", diff: []string{"T\t" + golden}, wantCode: 1},
		{name: "an unknown status on a golden counts as a change", diff: []string{"U\t" + golden}, wantCode: 1},
		{name: "a version change alone passes", diff: []string{"M\tpkg/score/VERSION"}},
		{name: "scorer code without a golden change passes, since go test holds the output to the goldens",
			diff: []string{"M\tpkg/score/credit.go"}},
		{name: "a look-alike file isn't a golden", diff: []string{"M\ttestdata/timelines/6.1/golden.json", "M\tpkg/score/golden_test.go"}},
		{name: "a path git quoted is still classified", diff: []string{"M\t\"testdata/timelines/6.1/golden.jsonl\""}, wantCode: 1},
		{name: "every changed golden is reported", diff: []string{"M\t" + golden, "M\ttestdata/timelines/6.2/golden.jsonl"},
			wantCode: 1, wantOut: "::error file=testdata/timelines/6.2/golden.jsonl::"},
		{name: "a malformed line is bad input", diff: []string{"M"}, wantCode: 2},
		{name: "an empty diff passes", diff: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stdin := strings.Join(tt.diff, "\n")
			if stdin != "" {
				stdin += "\n"
			}
			cmd := exec.Command("bash", "score_regression.sh")
			cmd.Stdin = strings.NewReader(stdin)
			var stdout, stderr bytes.Buffer
			cmd.Stdout = &stdout
			cmd.Stderr = &stderr
			code := 0
			if err := cmd.Run(); err != nil {
				var exit *exec.ExitError
				if !errors.As(err, &exit) {
					t.Fatal(err)
				}
				code = exit.ExitCode()
			}
			if code != tt.wantCode {
				t.Errorf("exit code = %d, want %d\nstdout:\n%s\nstderr:\n%s", code, tt.wantCode, stdout.String(), stderr.String())
			}
			if !strings.Contains(stdout.String(), tt.wantOut) {
				t.Errorf("stdout lacks %q:\n%s", tt.wantOut, stdout.String())
			}
		})
	}
}
