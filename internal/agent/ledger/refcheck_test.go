// SPDX-License-Identifier: AGPL-3.0-or-later

package ledger

import (
	"bytes"
	"math/rand/v2"
	"strings"
	"testing"

	"github.com/some-du6e/pairtime/pkg/event"
	"github.com/some-du6e/pairtime/pkg/ledger"
)

// TestReferenceCheck writes records of every kind with the writer, across
// midnight, reads them back from disk and has tools/ledger_ref.py, which
// shares no code with this package or pkg/ledger, recompute every nonce,
// entry, root and head, every commitment from the text it was written
// from, and the feature map of every tool call, shell command and prompt
// without its text (docs/PLAN.md, WP-LG5's oracle). It needs python 3.
func TestReferenceCheck(t *testing.T) {
	python := pythonPath(t)
	dir := t.TempDir()
	start := testT + 10*3_600_000 // 22:00 UTC, so the records span midnight
	clk := newClock(start)
	w := open(t, dir, clk)
	inputs := map[uint64]Input{}
	long := strings.Repeat("the drawer, not the modal ✓ ", 700)
	for _, in := range []Input{
		harness(&event.AgentOutput{TurnKey: "t_1", Words: 3500, IsFinal: true}, []byte(long)),
		bridgePrompt(80),
		track("add tests for the parser please"),
		prompt("", 0),
	} {
		s, err := w.Write(in)
		if err != nil {
			t.Fatal(err)
		}
		inputs[s] = in
	}
	rng := rand.New(rand.NewPCG(20261120, 5))
	for i := range 400 {
		ms := start + int64(i)*30_000
		clk.set(ms)
		s, in, err := write(w, rng, kinds[rng.IntN(len(kinds))], ms)
		if err != nil {
			t.Fatal(err)
		}
		if in.Event.Data != nil {
			inputs[s] = in
		}
		if rng.IntN(8) == 0 {
			if _, _, err := w.Seal(); err != nil {
				t.Fatal(err)
			}
		}
	}
	device := w.DeviceID()
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	days, err := segments(dir)
	if err != nil || len(days) != 2 {
		t.Fatalf("segments %v, %v; the records should span two days", days, err)
	}
	batches := verify(t, dir, keysOf(t, testKDev))
	commits, feats := contentVecs(t, testKDev, batches, inputs)
	if len(commits) == 0 || len(feats) == 0 {
		t.Fatalf("%d commitments and %d feature maps to check", len(commits), len(feats))
	}
	doc := vecFile{Format: vectorsFormat, Note: "Written by internal/agent/ledger's TestReferenceCheck.",
		Chains:      []vecChain{chainVec(t, device, testKDev, ledger.Hash{}, batches)},
		Commitments: commits, Features: feats}

	// The same chain with one byte of one stored body changed, to show
	// the check reads the bytes the writer stored.
	tampered := doc
	tampered.Chains = []vecChain{chainVec(t, device, testKDev, ledger.Hash{}, batches)}
	rec := &tampered.Chains[0].Batches[0].Records[0]
	rec.Body = bytes.Clone(rec.Body)
	rec.Body[len(rec.Body)-2] ^= 0x01

	cases := []struct {
		name string
		doc  vecFile
		want int
	}{
		{"the chain the writer stored, and its commitments and features, pass tools/ledger_ref.py check", doc, 0},
		{"tools/ledger_ref.py check fails the chain with one byte of a stored body changed", tampered, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			code, out := refCheck(t, python, c.doc)
			if code != c.want {
				t.Fatalf("tools/ledger_ref.py check exited %d, want %d:\n%s", code, c.want, out)
			}
			lines := strings.Split(strings.TrimSpace(out), "\n")
			t.Logf("%s", lines[len(lines)-1])
		})
	}
}
