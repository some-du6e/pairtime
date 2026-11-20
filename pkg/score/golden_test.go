// SPDX-License-Identifier: AGPL-3.0-or-later

package score

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/some-du6e/pairtime/internal/timelines"
)

// The regression guard (DESIGN 6.6, SM 7.6; docs/PLAN.md, WP-SC4). Every
// timeline under testdata/timelines has a golden.jsonl: the buckets the
// agent writes for it when it steps the scorer once a minute over the
// timeline's events, each minute's last revision, in minute order, one
// compact JSON object a line, local fields included. The goldens are
// regression guards, not oracles: they hold what this code outputs, so any
// change to that output fails TestGolden until the PR rewrites them, and
// ci.yml's score-regression guard fails a PR that rewrites them without a
// higher pkg/score/VERSION, unless a golden's own timeline inputs change in
// the same PR (tools/ci/score_regression.sh). To rewrite them:
//
//	go test ./pkg/score -run '^TestGolden$' -update
//
// which ci.yml's score-regression step also does when they differ, for the
// run's score-goldens artifact. gh run download never overwrites a file, so
// delete the old goldens before downloading it (AGENTS.md).

// goldenFile is the name of a timeline's golden.
const goldenFile = "golden.jsonl"

var updateGoldens = flag.Bool("update", false, "rewrite each timeline's golden.jsonl from the scorer's output")

// goldenCases returns every timeline the regression guard covers: SM 6's
// worked examples and the others at the top of testdata/timelines, the
// side-door cases and the lookback cases.
func goldenCases(t *testing.T) []*timelines.Fixture {
	t.Helper()
	out := append(loadFixtures(t), sideDoorCases(t)...)
	root, err := timelines.Root()
	if err != nil {
		t.Fatal(err)
	}
	names, err := timelines.List(filepath.Join(root, "lookback"))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		t.Fatal(err)
	}
	for _, n := range names {
		f, err := timelines.Load(root, "lookback/"+n)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, f)
	}
	return out
}

// goldenOutput returns a timeline's bucket output as its golden.jsonl
// holds it.
func goldenOutput(t *testing.T, f *timelines.Fixture) []byte {
	t.Helper()
	last := lastRevisions(stepRevisions(t, fixtureDevice(f), f.Expected.Device, f.Expected.Witness, arrivals(f.Events, nil)))
	var buf bytes.Buffer
	for _, m := range slices.Sorted(maps.Keys(last)) {
		b, err := json.Marshal(last[m])
		if err != nil {
			t.Fatal(err)
		}
		buf.Write(b)
		buf.WriteByte('\n')
	}
	return buf.Bytes()
}

// lineDiff describes the first line at which two outputs differ.
func lineDiff(got, want []byte) string {
	g, w := bytes.Split(got, []byte("\n")), bytes.Split(want, []byte("\n"))
	for i := 0; i < len(g) || i < len(w); i++ {
		var a, b []byte
		if i < len(g) {
			a = g[i]
		}
		if i < len(w) {
			b = w[i]
		}
		if !bytes.Equal(a, b) {
			return fmt.Sprintf("line %d:\n  got  %s\n  want %s", i+1, a, b)
		}
	}
	return "no line differs"
}

func TestGolden(t *testing.T) {
	for _, f := range goldenCases(t) {
		t.Run(f.Name+": the scorer's bucket output is the timeline's golden.jsonl (DESIGN 6.6)", func(t *testing.T) {
			got := goldenOutput(t, f)
			path := filepath.Join(f.Dir, goldenFile)
			if *updateGoldens {
				if err := os.WriteFile(path, got, 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(path)
			if errors.Is(err, fs.ErrNotExist) {
				t.Fatalf("%s doesn't exist; write it with go test ./pkg/score -run '^TestGolden$' -update", path)
			}
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, want) {
				t.Errorf("the output differs from %s, which is a regression guard: if the change is intended, rewrite the goldens with -update and raise pkg/score/VERSION in the same PR (DESIGN 6.6). First difference, %s",
					path, lineDiff(got, want))
			}
		})
	}
}
