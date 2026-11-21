// SPDX-License-Identifier: AGPL-3.0-or-later

package ci

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// gitCommits makes a repository in a new directory with a commit per map of
// path to content, in order, and returns the directory.
func gitCommits(t *testing.T, commits ...map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-c", "user.name=pairtime", "-c", "user.email=ci@pairtime.invalid",
			"-c", "commit.gpgsign=false"}, args...)...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	git("init", "-q")
	for i, files := range commits {
		for path, content := range files {
			full := filepath.Join(dir, filepath.FromSlash(path))
			if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		git("add", "-A")
		git("commit", "-q", "--allow-empty", "-m", "commit "+string(rune('1'+i)))
	}
	return dir
}

// TestScoreRegression runs score_regression.sh over fake diffs: a PR that
// rewrites a timeline's golden.jsonl changes the scorer's output, so it
// needs a higher pkg/score/VERSION, unless the timeline's own inputs change
// with it, which makes the PR an oracle change (DESIGN 6.6, SM 7.6; issue
// #132's ruling).
func TestScoreRegression(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skipf("score_regression.sh needs bash: %v", err)
	}
	script, err := filepath.Abs("score_regression.sh")
	if err != nil {
		t.Fatal(err)
	}
	const golden = "testdata/timelines/6.1/golden.jsonl"
	// Two buckets as a golden holds them, at version v, the second
	// crediting credited seconds.
	buckets := func(v, credited string) string {
		return `{"v":"` + v + `","params":"sha256:00","minute":"2026-11-19T14:00:00Z","credited_s":60}` + "\n" +
			`{"v":"` + v + `","params":"sha256:00","minute":"2026-11-19T14:01:00Z","credited_s":` + credited + "}\n"
	}
	tests := []struct {
		name string
		diff []string
		// from and to are pkg/score/VERSION at the base and at the head.
		// Each is 0.1.0 when left empty; unset leaves both out of the
		// environment.
		from, to string
		unset    bool
		// repo, when set, holds the base and head commits whose goldens
		// the guard reads for a PATCH-only bump.
		repo     []map[string]string
		wantCode int
		wantOut  string
	}{
		{name: "a modified golden without a version change fails", diff: []string{"M\t" + golden, "M\tpkg/score/credit.go"},
			wantCode: 1, wantOut: "::error file=" + golden + "::golden changed (M) leaves pkg/score/VERSION at 0.1.0"},
		{name: "a modified golden with a higher MINOR passes", diff: []string{"M\t" + golden, "M\tpkg/score/VERSION", "M\tpkg/score/credit.go"},
			to: "0.2.0"},
		{name: "a modified golden with a higher MAJOR passes", diff: []string{"M\t" + golden, "M\tpkg/score/VERSION"}, from: "0.9.4", to: "1.0.0"},
		{name: "an edit to VERSION that keeps its trimmed version is no bump", diff: []string{"M\t" + golden, "M\tpkg/score/VERSION"},
			from: "0.1.0\n", to: " 0.1.0", wantCode: 1, wantOut: "no higher than the base's 0.1.0"},
		{name: "a lowered version is no bump", diff: []string{"M\t" + golden, "M\tpkg/score/VERSION"}, from: "0.2.0", to: "0.1.9",
			wantCode: 1, wantOut: "lowers pkg/score/VERSION from 0.2.0 to 0.1.9"},
		{name: "a lower PATCH is no bump", diff: []string{"M\t" + golden, "M\tpkg/score/VERSION"}, from: "0.1.3", to: "0.1.2",
			wantCode: 1, wantOut: "no higher than the base's 0.1.3"},
		{name: "a version that isn't MAJOR.MINOR.PATCH is no bump", diff: []string{"M\t" + golden, "M\tpkg/score/VERSION"}, to: "0.2",
			wantCode: 1, wantOut: `isn't MAJOR.MINOR.PATCH`},
		{name: "a PATCH-only bump passes when the goldens differ in v alone, as a fix that changes no output does",
			diff: []string{"M\t" + golden, "M\tpkg/score/VERSION"}, to: "0.1.1",
			repo: []map[string]string{{golden: buckets("0.1.0", "30")}, {golden: buckets("0.1.1", "30")}}},
		{name: "a PATCH-only bump fails when a golden differs in more than v",
			diff: []string{"M\t" + golden, "M\tpkg/score/VERSION"}, to: "0.1.1",
			repo:     []map[string]string{{golden: buckets("0.1.0", "30")}, {golden: buckets("0.1.1", "31")}},
			wantCode: 1, wantOut: "::error file=" + golden + "::golden changed (M) bumps only PATCH, 0.1.0 to 0.1.1, while these goldens differ in more than v"},
		{name: "a PATCH-only bump fails when the guard has no commits to compare",
			diff: []string{"M\t" + golden, "M\tpkg/score/VERSION"}, to: "0.1.1", wantCode: 1, wantOut: "can't check that only v changed"},
		{name: "a golden that changes with its timeline's events.jsonl is an oracle change and needs no version change",
			diff: []string{"M\ttestdata/timelines/6.4/events.jsonl", "M\ttestdata/timelines/6.4/golden.jsonl"}},
		{name: "so is one that changes with its timeline's expected.json or anchors.json",
			diff: []string{"M\ttestdata/timelines/6.1/expected.json", "M\ttestdata/timelines/lookback/two-sessions/anchors.json",
				"M\t" + golden, "M\ttestdata/timelines/lookback/two-sessions/golden.jsonl"}},
		{name: "another timeline's inputs don't make a golden an oracle change",
			diff:     []string{"M\ttestdata/timelines/6.4/events.jsonl", "M\ttestdata/timelines/6.4/golden.jsonl", "M\t" + golden},
			wantCode: 1, wantOut: "::error file=" + golden + "::"},
		{name: "a timeline's README isn't one of its inputs", diff: []string{"M\ttestdata/timelines/6.1/README.md", "M\t" + golden},
			wantCode: 1, wantOut: "::error file=" + golden + "::"},
		{name: "a nested timeline's inputs are the files in its own directory, not a .json above it",
			diff:     []string{"M\ttestdata/timelines/sidedoor/cases.json", "M\ttestdata/timelines/sidedoor/a-track-jiggler/golden.jsonl"},
			wantCode: 1, wantOut: "sidedoor/a-track-jiggler/golden.jsonl"},
		{name: "a generator beside a golden isn't one of its timeline's inputs",
			diff:     []string{"M\ttestdata/timelines/6.1/gen.py", "M\t" + golden},
			wantCode: 1, wantOut: "::error file=" + golden + "::"},
		{name: "an input added beside a modified golden doesn't excuse it, since the oracle guard passes a new oracle file beside code",
			diff:     []string{"A\ttestdata/timelines/6.1/notes.json", "M\t" + golden, "M\tpkg/score/credit.go"},
			wantCode: 1, wantOut: "::error file=" + golden + "::"},
		{name: "a file renamed into a golden's timeline doesn't excuse it either",
			diff:     []string{"R100\tdocs/drafts/x.json\ttestdata/timelines/6.1/x.json", "M\t" + golden},
			wantCode: 1, wantOut: "::error file=" + golden + "::"},
		{name: "nor does an input copied into it",
			diff:     []string{"C090\ttestdata/timelines/6.5/hunks.json\ttestdata/timelines/6.1/hunks.json", "M\t" + golden},
			wantCode: 1, wantOut: "::error file=" + golden + "::"},
		{name: "an input deleted from a golden's timeline makes it an oracle change",
			diff: []string{"D\ttestdata/timelines/6.1/prompts.json", "M\t" + golden}},
		{name: "a copy of a timeline's input leaves its source as it was, so it doesn't excuse that timeline's golden",
			diff:     []string{"M\t" + golden, "C100\ttestdata/timelines/6.1/events.jsonl\ttestdata/timelines/6.7/events.jsonl"},
			wantCode: 1, wantOut: "::error file=" + golden + "::"},
		{name: "a golden renamed with changes beside its timeline's renamed inputs is an oracle change",
			diff: []string{"R087\t" + golden + "\ttestdata/timelines/6.1-old/golden.jsonl",
				"R095\ttestdata/timelines/6.1/events.jsonl\ttestdata/timelines/6.1-old/events.jsonl"}},
		{name: "a golden of a nested timeline counts too", diff: []string{"M\ttestdata/timelines/sidedoor/a-track-jiggler/golden.jsonl"},
			wantCode: 1, wantOut: "sidedoor/a-track-jiggler/golden.jsonl"},
		{name: "a new golden for a new timeline passes, since it had no output before", diff: []string{"A\ttestdata/timelines/6.7/golden.jsonl"}},
		{name: "a deleted golden goes with its timeline and passes", diff: []string{"D\t" + golden}},
		{name: "a golden renamed with every byte kept passes", diff: []string{"R100\t" + golden + "\ttestdata/timelines/6.1-old/golden.jsonl"}},
		{name: "a golden renamed with changes fails", diff: []string{"R087\t" + golden + "\ttestdata/timelines/6.1-old/golden.jsonl"},
			wantCode: 1, wantOut: "golden changed (R087)"},
		{name: "a golden that changed type fails", diff: []string{"T\t" + golden}, wantCode: 1},
		{name: "an unknown status on a golden counts as a change", diff: []string{"U\t" + golden}, wantCode: 1},
		{name: "a version change alone passes", diff: []string{"M\tpkg/score/VERSION"}, to: "0.2.0"},
		{name: "scorer code without a golden change passes, since go test holds the output to the goldens",
			diff: []string{"M\tpkg/score/credit.go"}},
		{name: "a look-alike file isn't a golden", diff: []string{"M\ttestdata/timelines/6.1/golden.json", "M\tpkg/score/golden_test.go"}},
		{name: "a path git quoted is still classified", diff: []string{"M\t\"testdata/timelines/6.1/golden.jsonl\""}, wantCode: 1},
		{name: "every changed golden is reported", diff: []string{"M\t" + golden, "M\ttestdata/timelines/6.2/golden.jsonl"},
			wantCode: 1, wantOut: "::error file=" + golden + "::golden changed (M) leaves pkg/score/VERSION at 0.1.0, no higher than the base's 0.1.0 (DESIGN 6.6)\n::error file=testdata/timelines/6.2/golden.jsonl::"},
		{name: "a changed golden without the versions in the environment is bad input", diff: []string{"M\t" + golden}, unset: true,
			wantCode: 2},
		{name: "a malformed line is bad input", diff: []string{"M"}, wantCode: 2},
		{name: "an empty diff passes", diff: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stdin := strings.Join(tt.diff, "\n")
			if stdin != "" {
				stdin += "\n"
			}
			cmd := exec.Command("bash", script)
			cmd.Stdin = strings.NewReader(stdin)
			env := []string{}
			for _, kv := range os.Environ() {
				if k, _, _ := strings.Cut(kv, "="); k != "OLD_VERSION" && k != "NEW_VERSION" && k != "SCORE_BASE" && k != "SCORE_HEAD" {
					env = append(env, kv)
				}
			}
			if !tt.unset {
				from, to := tt.from, tt.to
				if from == "" {
					from = "0.1.0"
				}
				if to == "" {
					to = "0.1.0"
				}
				env = append(env, "OLD_VERSION="+from, "NEW_VERSION="+to)
			}
			if tt.repo != nil {
				if _, err := exec.LookPath("git"); err != nil {
					t.Skipf("the PATCH check reads goldens with git: %v", err)
				}
				cmd.Dir = gitCommits(t, tt.repo...)
				env = append(env, "SCORE_BASE=HEAD~1", "SCORE_HEAD=HEAD")
			}
			cmd.Env = env
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
