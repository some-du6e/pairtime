// SPDX-License-Identifier: AGPL-3.0-or-later

package ci

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The event payload is GitHub's documented example of a pull_request
// "labeled" event, from github.com/octokit/webhooks at aa80c24
// (payload-examples/api.github.com/pull_request/labeled.payload.json),
// trimmed to the fields around the labels. Each case sets
// pull_request.labels to label objects of the same shape.
const payloadFile = "testdata/pull_request.labeled.json"

// oraclePaths has one path under each of is_oracle's patterns, the oracle
// paths AGENTS.md lists.
var oraclePaths = []string{
	"docs/DESIGN.md",
	"docs/BOUNTY.md",
	"docs/design/struggle-metric.md",
	"docs/research/notes/hackatime.md",
	"docs/errata/2026-11-19-bridge-unverified.md",
	"testdata/timelines/6.1/events.jsonl",
	"testdata/ledger/vectors.json",
	"tools/ledger_ref.py",
	"testdata/fixtures/claude_code/2.0.14/linux/PROVENANCE.md",
	"testdata/upstream/hackatime/heartbeat.rb",
	"testdata/checks/cc1.json",
	"testdata/redteam/replay.jsonl",
}

// codePaths has one path under each code directory the guard watches.
var codePaths = []string{
	"cmd/pairtime/main.go",
	"internal/agent/agent.go",
	"pkg/score/score.go",
	"web/src/app.ts",
	"extension/vscode/package.json",
	"integrations/airtable-automation.js",
}

type guardCase struct {
	name     string
	diff     []string // name-status lines, tab-separated
	labels   []string // names in pull_request.labels; nil with noEvent
	noEvent  bool     // run without GITHUB_EVENT_PATH
	action   string   // overrides the payload's action
	label    string   // overrides the payload's label.name
	wantCode int
	wantOut  string // substring of stdout, if set
}

func TestOracleGuard(t *testing.T) {
	for _, tool := range []string{"bash", "jq"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("oracle_guard.sh needs %s: %v", tool, err)
		}
	}
	tests := []guardCase{
		{name: "a modified oracle with code fails", diff: []string{"M\tdocs/design/struggle-metric.md", "M\tpkg/score/score.go"}, wantCode: 1, wantOut: "::error file=docs/design/struggle-metric.md::oracle modified"},
		{name: "an added oracle with code passes", diff: []string{"A\ttestdata/timelines/6.1/expected.json", "A\tpkg/score/score.go"}},
		{name: "a new erratum beside code passes", diff: []string{"A\tdocs/errata/2026-11-21-new-question.md", "M\tpkg/score/score.go"}},
		{name: "the oracle-change label lets an oracle change with code", diff: []string{"M\tdocs/design/struggle-metric.md", "M\tpkg/score/score.go"}, labels: []string{"bug", "oracle-change"}, wantOut: "the oracle-change label allows this PR"},
		{name: "a deleted oracle with code fails", diff: []string{"D\ttestdata/fixtures/claude_code/2.0.14/linux/hooks/001.json", "M\tinternal/adapters/claudecode/hooks.go"}, wantCode: 1, wantOut: "oracle deleted"},
		{name: "a renamed oracle with code fails", diff: []string{"R100\ttestdata/ledger/v1.json\ttestdata/ledger/v2.json", "M\tpkg/ledger/ledger.go"}, wantCode: 1, wantOut: "oracle renamed"},
		{name: "an oracle moved out of the oracle paths with code fails", diff: []string{"R087\tdocs/design/struggle-metric.md\tdocs/old/struggle-metric.md", "M\tpkg/score/score.go"}, wantCode: 1},
		{name: "a file renamed into an oracle path counts as an added oracle", diff: []string{"R100\tdocs/drafts/new-fixture.json\ttestdata/checks/cc1.json", "A\tpkg/check/cc1.go"}},
		{name: "a copy of an oracle beside code passes", diff: []string{"C100\ttestdata/timelines/6.1/events.jsonl\ttestdata/timelines/6.1-tabs/events.jsonl", "M\tpkg/score/score.go"}},
		{name: "a type change of an oracle with code fails", diff: []string{"T\ttools/ledger_ref.py", "M\tpkg/ledger/ledger.go"}, wantCode: 1, wantOut: "oracle changed type"},
		{name: "an oracle change without code passes", diff: []string{"M\tdocs/DESIGN.md", "M\tdocs/PLAN.md", "M\tAGENTS.md"}},
		{name: "code without an oracle change passes", diff: []string{"M\tpkg/event/event.go", "A\tpkg/event/new_test.go"}},
		{name: "docs/PLAN.md is not an oracle", diff: []string{"M\tdocs/PLAN.md", "M\tpkg/score/score.go"}},
		{name: "workflows and CI tools are not code", diff: []string{"M\tdocs/research/client-platform.md", "M\t.github/workflows/ci.yml", "M\ttools/ci/oracle_guard.sh"}},
		{name: "a look-alike path is not an oracle", diff: []string{"M\tdocs/designs/notes.md", "M\ttestdata/fixtures-old/x.json", "M\tpkg/score/score.go"}},
		{name: "a label with a similar name doesn't count", diff: []string{"M\tdocs/BOUNTY.md", "M\tcmd/pairtime/main.go"}, labels: []string{"oracle-changes", "Oracle-Change"}, wantCode: 1},
		{
			name:     "on an unlabeled event the removed label is not on the PR",
			diff:     []string{"M\tdocs/BOUNTY.md", "M\tcmd/pairtime/main.go"},
			labels:   []string{"bug"},
			action:   "unlabeled",
			label:    "oracle-change",
			wantCode: 1,
		},
		{name: "without an event payload there are no labels", diff: []string{"M\tdocs/BOUNTY.md", "M\tcmd/pairtime/main.go"}, noEvent: true, wantCode: 1},
		{name: "a path git quoted is still classified", diff: []string{"M\tdocs/design/struggle-metric.md", "A\t\"pkg/score/caf\\303\\251.go\""}, wantCode: 1},
		{name: "every oracle is reported", diff: []string{"M\tdocs/DESIGN.md", "D\tdocs/BOUNTY.md", "M\tpkg/score/score.go"}, wantCode: 1, wantOut: "::error file=docs/BOUNTY.md::oracle deleted"},
		{name: "a malformed line is bad input", diff: []string{"M"}, wantCode: 2},
		{name: "an empty diff passes", diff: nil},
	}
	for _, p := range oraclePaths {
		tests = append(tests, guardCase{
			name:     "modifying " + p + " with code fails",
			diff:     []string{"M\t" + p, "M\tpkg/score/score.go"},
			wantCode: 1,
		})
	}
	for _, p := range codePaths {
		tests = append(tests, guardCase{
			name:     "an oracle modified with a change under " + strings.SplitN(p, "/", 2)[0] + "/ fails",
			diff:     []string{"M\tdocs/design/struggle-metric.md", "M\t" + p},
			wantCode: 1,
		})
	}

	template, err := os.ReadFile(payloadFile)
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := withoutEventPath(os.Environ())
			if !tt.noEvent {
				path := filepath.Join(t.TempDir(), "event.json")
				if err := os.WriteFile(path, payload(t, template, tt), 0o644); err != nil {
					t.Fatal(err)
				}
				env = append(env, "GITHUB_EVENT_PATH="+path)
			}
			stdin := strings.Join(tt.diff, "\n")
			if stdin != "" {
				stdin += "\n"
			}
			cmd := exec.Command("bash", "oracle_guard.sh")
			cmd.Env = env
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

// payload returns the documented payload with the case's labels, and its
// action and label when it sets them.
func payload(t *testing.T, template []byte, tt guardCase) []byte {
	t.Helper()
	var event map[string]any
	if err := json.Unmarshal(template, &event); err != nil {
		t.Fatal(err)
	}
	pr := event["pull_request"].(map[string]any)
	example := pr["labels"].([]any)[0].(map[string]any)
	labels := []any{}
	for _, name := range tt.labels {
		l := make(map[string]any, len(example))
		for k, v := range example {
			l[k] = v
		}
		l["name"] = name
		labels = append(labels, l)
	}
	pr["labels"] = labels
	if tt.action != "" {
		event["action"] = tt.action
	}
	if tt.label != "" {
		event["label"].(map[string]any)["name"] = tt.label
	}
	data, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func withoutEventPath(env []string) []string {
	var out []string
	for _, kv := range env {
		if !strings.HasPrefix(kv, "GITHUB_EVENT_PATH=") {
			out = append(out, kv)
		}
	}
	return out
}
