// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"bytes"
	"fmt"
	"log/slog"
	"slices"
	"testing"

	"github.com/some-du6e/pairtime/internal/adapters"
	"github.com/some-du6e/pairtime/internal/agent/home"
	"github.com/some-du6e/pairtime/internal/agent/hookpath"
	"github.com/some-du6e/pairtime/internal/agent/ledger"
	"github.com/some-du6e/pairtime/pkg/event"
	pkgledger "github.com/some-du6e/pairtime/pkg/ledger"
)

// TestHookEventIDs checks the event_ids of a hook call's records
// (hookEventID): the same each time the call is parsed, so a call parsed
// again in a later run, as after a crash during a held.jsonl replay or a
// kill before its pending log was emptied, makes no second record of what
// the ledger holds, and counts each as a repeat; counted from the end of
// Parse's output, so a call read back from an earlier run's held.jsonl
// line, which never has the walk's session.start first, gives its prompt
// the id the prompt had; and never shared by two records.
func TestHookEventIDs(t *testing.T) {
	capture := readCapture(t)
	secret := bytes.Repeat([]byte{7}, hookpath.SessionSecretLen)
	lk, err := pkgledger.DeriveKeys(bytes.Repeat([]byte{9}, 32))
	if err != nil {
		t.Fatal(err)
	}
	// run parses calls, each delivered by way, in a run of the agent on the
	// ledger in dir: a host with this installation's keys and a writer of
	// its own, which it closes. It returns the host.
	run := func(t *testing.T, dir string, way adapters.Delivery, calls ...adapters.Call) *adapterHost {
		t.Helper()
		h := newAdapterHost(&agent{dirs: home.Dirs{Data: dir, Config: dir, Runtime: dir}, log: slog.New(slog.DiscardHandler),
			problems: map[string]string{}, hooked: newHookTies()})
		h.a.kSession.Store(&secret)
		w, err := ledger.Open(ledger.Options{Dir: h.a.dirs.Ledger(), Keys: lk, KeyID: "0123456789abcdef"})
		if err != nil {
			t.Fatal(err)
		}
		h.keys = &adapterKeys{feat: lk.Features(), project: bytes.Repeat([]byte{8}, 32)}
		h.writer, h.ready = w, true
		for _, c := range calls {
			h.queue = append(h.queue, adapters.DeliveryOf(event.HarnessClaudeCode, c, way, calls[0].Arrived))
		}
		h.step(false)
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
		return h
	}
	// ids is the event_ids of the hook records of the ledger in dir, in the
	// order they were written, and their kinds.
	ids := func(t *testing.T, dir string) ([]string, []event.Kind) {
		t.Helper()
		var out []string
		var kinds []event.Kind
		for _, r := range readLedger(t, home.Dirs{Data: dir, Config: dir, Runtime: dir}, event.SourceHook) {
			out = append(out, r.ev.EventID)
			kinds = append(kinds, r.ev.Kind())
		}
		return out, kinds
	}
	prompt, stop := capture[1], capture[3] // the session's first prompt, and a stop with the agent's last message
	cases := []struct {
		name  string
		check func(t *testing.T) (ok bool, got string)
	}{
		{"a call parsed again in a later run, as after a crash or a kill, makes no second record of what the ledger holds, and counts each as a repeat",
			func(t *testing.T) (bool, string) {
				dir := t.TempDir()
				run(t, dir, adapters.Walked, prompt)
				first, _ := ids(t, dir)
				// Read back from the killed run's held.jsonl line, the call
				// comes again with no walk.
				again := run(t, dir, adapters.Spooled, prompt)
				after, kinds := ids(t, dir)
				repeats := again.statsOf(event.HarnessClaudeCode, true).Repeats
				return len(first) == 2 && slices.Equal(after, first) && repeats == 1,
					fmt.Sprintf("records %v of kinds %v, repeats %d", after, kinds, repeats)
			}},
		{"a call read back with no walk gives its prompt the id it had when the walk's session.start went first",
			func(t *testing.T) (bool, string) {
				walked, plugin := t.TempDir(), t.TempDir()
				run(t, walked, adapters.Walked, prompt)
				run(t, plugin, adapters.Spooled, prompt)
				a, ak := ids(t, walked)
				b, bk := ids(t, plugin)
				ok := len(a) == 2 && ak[0] == event.KindSessionStart && ak[1] == event.KindPromptSubmit &&
					len(b) == 1 && bk[0] == event.KindPromptSubmit && b[0] == a[1] && b[0] != a[0]
				return ok, fmt.Sprintf("walked %v (%v), read back %v (%v)", a, ak, b, bk)
			}},
		{"a call's records have the same ids in every run",
			func(t *testing.T) (bool, string) {
				one, two := t.TempDir(), t.TempDir()
				run(t, one, adapters.Walked, stop)
				run(t, two, adapters.Walked, stop)
				a, _ := ids(t, one)
				b, _ := ids(t, two)
				return len(a) >= 2 && slices.Equal(a, b), fmt.Sprintf("first run %v, second %v", a, b)
			}},
		{"no two records share an id, of one call or of different calls",
			func(t *testing.T) (bool, string) {
				dir := t.TempDir()
				run(t, dir, adapters.Walked, capture[:20]...)
				got, _ := ids(t, dir)
				seen := map[string]bool{}
				for _, id := range got {
					if seen[id] {
						return false, fmt.Sprintf("two records share %s", id)
					}
					seen[id] = true
				}
				return len(got) > 10, fmt.Sprintf("%d records", len(got))
			}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if ok, got := c.check(t); !ok {
				t.Errorf("no: %s", got)
			}
		})
	}
}
