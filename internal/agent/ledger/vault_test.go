// SPDX-License-Identifier: AGPL-3.0-or-later

package ledger

import (
	"bytes"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/pkg/event"
	"github.com/some-du6e/pairtime/pkg/ledger"
	"github.com/some-du6e/pairtime/pkg/ledger/features"
)

// fakeVault records what the writer hands its Vault, in order, with the
// writer's flushes, which syncHook adds to the same log.
type fakeVault struct {
	mu   sync.Mutex
	next []uint64 // each Reconcile's next
	kept []keptEntry
	log  []string
}

type keptEntry struct {
	day   string
	s     uint64
	text  []byte
	paths []string
}

func (v *fakeVault) Reconcile(next uint64) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.next = append(v.next, next)
	v.log = append(v.log, fmt.Sprintf("reconcile %d", next))
	return nil
}

func (v *fakeVault) Keep(day string, s uint64, text []byte, paths []string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	e := keptEntry{day: day, s: s, paths: slices.Clone(paths)}
	if text != nil {
		e.text = append([]byte{}, text...)
	}
	v.kept = append(v.kept, e)
	v.log = append(v.log, fmt.Sprintf("keep %d", s))
}

func (v *fakeVault) Sync() {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.log = append(v.log, "sync")
}

func (v *fakeVault) flushed(name string, _ int64) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.log = append(v.log, "flush "+filepath.Base(name))
}

func (v *fakeVault) entries() []keptEntry {
	v.mu.Lock()
	defer v.mu.Unlock()
	return slices.Clone(v.kept)
}

// TestVaultContent checks what Write hands the vault for each kind of
// record: exactly the text the record's c commits, and the paths of an
// agent.tool's edits (DESIGN 3.5; ledger-v1 10.1).
func TestVaultContent(t *testing.T) {
	long := strings.Repeat("abc€ ", 4000) // 28,000 bytes, whose 16 KiB cut falls inside a euro sign
	rng := rand.New(rand.NewPCG(7, 8))
	cases := []struct {
		name string
		in   Input
		// text is what the vault gets, nil for none; paths likewise.
		text  []byte
		paths []string
		none  bool // the vault hears nothing of the record
	}{
		{name: "a prompt's text goes to the vault in full", in: prompt("fix the nav bar", 0), text: []byte("fix the nav bar")},
		{name: "a prompt with no text commits the empty text, which the vault keeps",
			in: harness(&event.PromptSubmit{TurnKey: "t_1", OriginDeclared: event.DeclaredNone}, nil), text: []byte{}},
		{name: "a final agent message goes to the vault cut to its first 16 KiB, as its record commits it",
			in:   harness(&event.AgentOutput{TurnKey: "t_1", Words: 8000, IsFinal: true}, []byte(long)),
			text: ledger.CommittedMessage([]byte(long))},
		{name: "a track label goes to the vault as its record commits it",
			in: track("add tests for the parser please"), text: []byte("add tests for the parser please")},
		{name: "a message that isn't the turn's final one gives the vault nothing",
			in: harness(&event.AgentOutput{TurnKey: "t_1", Words: 9}, nil), none: true},
		{name: "a bridge prompt, whose text the device never had, gives the vault nothing",
			in: bridgePrompt(64), none: true},
		{name: "a plugin's final message that came without its text gives the vault nothing",
			in: viaPlugin(harness(&event.AgentOutput{TurnKey: "t_1", Words: 9, IsFinal: true}, nil)), none: true},
		{name: "an agent.tool's edit paths go to the vault, and its command doesn't",
			in: Input{Event: harness(&event.AgentTool{ToolClass: event.ToolEdit, LinesAdded: 7, LinesRemoved: 3,
				TStart: testT, TEnd: testT}, nil).Event,
				Edits: []features.Edit{{Path: "src/nav.js", OldStart: 10, OldLines: 3, NewStart: 10, NewLines: 5},
					{Path: "src/app.css", OldStart: 1, OldLines: 0, NewStart: 1, NewLines: 2}},
				Command: "npm test"},
			paths: []string{"src/nav.js", "src/app.css"}},
		{name: "a shell command gives the vault nothing",
			in: sampleInput(rng, "shell.command", testT, "s_1", "p_web"), none: true},
		{name: "a presence sample gives the vault nothing",
			in: sampleInput(rng, "os.presence", testT, "", ""), none: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			v := &fakeVault{}
			w := open(t, t.TempDir(), newClock(testT), func(o *Options) { o.Vault = v })
			s, err := w.Write(c.in)
			if err != nil {
				t.Fatal(err)
			}
			got := v.entries()
			if c.none {
				if len(got) != 0 {
					t.Errorf("the vault got %+v", got)
				}
				return
			}
			if len(got) != 1 {
				t.Fatalf("the vault got %d entries, want 1", len(got))
			}
			e := got[0]
			if e.s != s || e.day != dayOf(time.UnixMilli(testT)) {
				t.Errorf("the entry is record %d of %s, want record %d of %s", e.s, e.day, s, dayOf(time.UnixMilli(testT)))
			}
			if (e.text == nil) != (c.text == nil) || !bytes.Equal(e.text, c.text) {
				t.Errorf("the vault got text of %d bytes (nil: %v), want %d (nil: %v)", len(e.text), e.text == nil, len(c.text), c.text == nil)
			}
			if !slices.Equal(e.paths, c.paths) {
				t.Errorf("the vault got paths %q, want %q", e.paths, c.paths)
			}
		})
	}
}

// TestVaultOrder checks when the writer calls its vault: Reconcile at
// Open with the number the next record takes, Keep in the order of the
// records, under the segment's day, and Sync before every flush of a
// batch.
func TestVaultOrder(t *testing.T) {
	cases := []struct {
		name string
		run  func(t *testing.T, dir string, v *fakeVault)
	}{
		{
			name: "Open reconciles the vault with the number the next record takes, before any record takes it",
			run: func(t *testing.T, dir string, v *fakeVault) {
				w := open(t, dir, newClock(testT))
				for i := range 3 {
					if _, err := w.Write(prompt(fmt.Sprintf("prompt %d", i), 0)); err != nil {
						t.Fatal(err)
					}
				}
				if err := w.Close(); err != nil {
					t.Fatal(err)
				}
				w = open(t, dir, newClock(testT+60_000), func(o *Options) { o.Vault = v })
				if _, err := w.Write(prompt("the next one", 0)); err != nil {
					t.Fatal(err)
				}
				w.Close()
				if !slices.Equal(v.next, []uint64{4}) {
					t.Errorf("Reconcile got %v, want [4]", v.next)
				}
				if v.log[0] != "reconcile 4" || !slices.Contains(v.log, "keep 4") {
					t.Errorf("the vault heard %q, want Reconcile first and record 4 kept", v.log)
				}
			},
		},
		{
			name: "after a crash cut unsealed records, Reconcile gets the number after the last record that survived",
			run: func(t *testing.T, dir string, v *fakeVault) {
				w := open(t, dir, newClock(testT))
				for i := range 3 {
					if _, err := w.Write(prompt(fmt.Sprintf("prompt %d", i), 0)); err != nil {
						t.Fatal(err)
					}
				}
				if _, _, err := w.Seal(); err != nil {
					t.Fatal(err)
				}
				seg := segPath(dir, dayOf(time.UnixMilli(testT)))
				sealed := fileSize(t, seg)
				rng := rand.New(rand.NewPCG(9, 10))
				for range 2 {
					if _, err := w.Write(sampleInput(rng, "os.presence", testT, "", "")); err != nil {
						t.Fatal(err)
					}
				}
				abandon(w)
				// A power cut takes the two records no batch holds.
				if err := os.Truncate(seg, sealed); err != nil {
					t.Fatal(err)
				}
				open(t, dir, newClock(testT+60_000), func(o *Options) { o.Vault = v })
				if !slices.Equal(v.next, []uint64{4}) {
					t.Errorf("Reconcile got %v, want [4]", v.next)
				}
			},
		},
		{
			name: "records' contents reach the vault in the order of their numbers, under their segment's day",
			run: func(t *testing.T, dir string, v *fakeVault) {
				c := newClock(testT)
				w := open(t, dir, c, func(o *Options) { o.Vault = v })
				if _, err := w.Write(prompt("one", 0)); err != nil {
					t.Fatal(err)
				}
				c.set(testT + 24*3600_000)
				if _, err := w.Write(prompt("two", 0)); err != nil {
					t.Fatal(err)
				}
				w.Close()
				got := v.entries()
				want := []string{dayOf(time.UnixMilli(testT)), dayOf(time.UnixMilli(testT + 24*3600_000))}
				if len(got) != 2 || got[0].s != 1 || got[1].s != 2 || got[0].day != want[0] || got[1].day != want[1] {
					t.Errorf("the vault got %+v, want records 1 and 2 under %q", got, want)
				}
			},
		},
		{
			name: "every seal flushes the vault before it flushes the batch",
			run: func(t *testing.T, dir string, v *fakeVault) {
				syncHook = v.flushed
				t.Cleanup(func() { syncHook = nil })
				w := open(t, dir, newClock(testT), func(o *Options) { o.Vault = v })
				for i := range 3 {
					if _, err := w.Write(prompt(fmt.Sprintf("prompt %d", i), 0)); err != nil {
						t.Fatal(err)
					}
					if _, _, err := w.Seal(); err != nil {
						t.Fatal(err)
					}
				}
				w.Close()
				v.mu.Lock()
				defer v.mu.Unlock()
				flushes := 0
				for i, line := range v.log {
					if !strings.HasPrefix(line, "flush ") || !strings.HasSuffix(line, segSuffix) || line == "flush "+batchesName {
						continue
					}
					flushes++
					if i == 0 || v.log[i-1] != "sync" {
						t.Errorf("%s isn't right after a vault flush: %q", line, v.log)
					}
				}
				if flushes < 3 {
					t.Errorf("%d flushes of a batch, want 3 or more: %q", flushes, v.log)
				}
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) { c.run(t, t.TempDir(), &fakeVault{}) })
	}
}
