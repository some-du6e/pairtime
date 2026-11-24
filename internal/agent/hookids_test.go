// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/adapters"
	"github.com/some-du6e/pairtime/internal/agent/home"
	"github.com/some-du6e/pairtime/internal/agent/hookpath"
	"github.com/some-du6e/pairtime/internal/agent/ledger"
	"github.com/some-du6e/pairtime/pkg/event"
	pkgledger "github.com/some-du6e/pairtime/pkg/ledger"
)

// TestHookEventIDs checks the event_ids of a hook call's records
// (hookEventID): the same each time the call is parsed, so a call parsed
// again after a kill, read back from the killed run's pending log against
// the memory that run saved, makes no second record of what the ledger
// holds, though its prompt comes out as the turn's agent.queue then, and
// counts it as a repeat; counted from the end of Parse's output, so a call
// read back from an earlier run's held.jsonl line, which never has the
// walk's session.start first, gives its prompt the id the prompt had; and
// never shared by two records, of one call or of two calls made in the
// same millisecond.
func TestHookEventIDs(t *testing.T) {
	capture := readCapture(t)
	secret := bytes.Repeat([]byte{7}, hookpath.SessionSecretLen)
	lk, err := pkgledger.DeriveKeys(bytes.Repeat([]byte{9}, 32))
	if err != nil {
		t.Fatal(err)
	}
	// host is an adapter host with this installation's keys, of an agent on
	// dirs, writing to w.
	host := func(dirs home.Dirs, w *ledger.Writer) *adapterHost {
		h := newAdapterHost(&agent{dirs: dirs, log: slog.New(slog.DiscardHandler), problems: map[string]string{}, hooked: newHookTies()})
		h.a.kSession.Store(&secret)
		h.keys = &adapterKeys{feat: lk.Features(), project: bytes.Repeat([]byte{8}, 32)}
		h.writer, h.ready = w, true
		return h
	}
	// run parses calls, each delivered by way, in a run of the agent on the
	// ledger in dir, with a writer of its own, which it closes. It returns
	// the host.
	run := func(t *testing.T, dir string, way adapters.Delivery, calls ...adapters.Call) *adapterHost {
		t.Helper()
		dirs := home.Dirs{Data: dir, Config: dir, Runtime: dir}
		w, err := ledger.Open(ledger.Options{Dir: dirs.Ledger(), Keys: lk, KeyID: "0123456789abcdef"})
		if err != nil {
			t.Fatal(err)
		}
		h := host(dirs, w)
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
		{"a call parsed again after a kill, read back from the killed run's pending log against the memory that run saved, makes no second record of what the ledger holds, though its prompt comes out as the turn's agent.queue, and counts a repeat",
			func(t *testing.T) (bool, string) {
				dir, next := t.TempDir(), t.TempDir()
				dirs, after := home.Dirs{Data: dir, Config: dir, Runtime: dir}, home.Dirs{Data: next, Config: next, Runtime: next}
				// The killed run's seals wait at their read of config.toml
				// once armed, so the seal its prompt asks for doesn't come
				// before the copy, as a kill leaves the ledger.
				var armed atomic.Bool
				gate := make(chan struct{})
				release := sync.OnceFunc(func() { close(gate) })
				w := optionsWriter(t, dirs, func(o *ledger.Options) {
					o.Online = func() bool {
						if armed.Load() {
							<-gate
						}
						return true
					}
				})
				t.Cleanup(release) // before the writer's Close, which waits for the seal
				killed := host(dirs, w)
				// The killed run took the session's start, which it sealed,
				// then its first prompt, just now, so the next start doesn't
				// drop the prompt as held too long (heldKeep), whenever the
				// capture was made. It wrote the prompt's record and saved the
				// memory the prompt made, and was killed before it emptied the
				// prompt's line from its pending log.
				start := adapters.DeliveryOf(event.HarnessClaudeCode, capture[0], adapters.Walked, capture[0].Arrived)
				call := adapters.DeliveryOf(event.HarnessClaudeCode, prompt, adapters.Walked, capture[0].Arrived)
				call.Received.Wall = time.Now().UTC()
				killed.queue = append(killed.queue, start)
				killed.step(false)
				sealNow(t, w, dirs.Ledger())
				armed.Store(true)
				killed.queue = append(killed.queue, call)
				killed.step(false)
				memory, err := killed.built[event.HarnessClaudeCode].(adapters.Stateful).SaveState()
				if err != nil {
					t.Fatal(err)
				}
				line, err := killed.heldLine(call)
				if err != nil {
					t.Fatal(err)
				}
				copyFiles(t, dirs.Ledger(), after.Ledger())
				release()
				if err := w.Close(); err != nil {
					t.Fatal(err)
				}
				if _, rows, err := ledger.ReadRows(after.Ledger(), 0); err != nil || len(rows) != 1 {
					t.Fatalf("the copy holds %d rows (%v); want the session's start, with the prompt's record a kill left unsealed", len(rows), err)
				}
				if err := os.WriteFile(after.Held()+pendingSuffix, line, 0o600); err != nil {
					t.Fatal(err)
				}
				first, _ := ids(t, dir)
				// The next start's Open seals what the kill left, the start
				// moves the pending line to held.jsonl (recoverPending), and
				// the replay reads the call back with no walk and a receipt of
				// its own (readHeld), and parses it against the saved memory.
				h := host(after, optionsWriter(t, after, nil))
				h.memory.Adapters = map[event.Harness]json.RawMessage{event.HarnessClaudeCode: memory}
				parsed := &parsedKinds{Adapter: h.adapter(event.HarnessClaudeCode)}
				h.built[event.HarnessClaudeCode] = parsed
				h.recoverPending()
				h.step(false)
				if err := h.writer.Close(); err != nil {
					t.Fatal(err)
				}
				got, kinds := ids(t, next)
				repeats := h.statsOf(event.HarnessClaudeCode, true).Repeats
				ok := len(first) == 2 && slices.Equal(got, first) && repeats == 1 &&
					len(parsed.kinds) == 1 && slices.Equal(parsed.kinds[0], []event.Kind{event.KindAgentQueue})
				return ok, fmt.Sprintf("the killed run's records %v, the ledger's after the next start %v of kinds %v, the replay parsed %v, repeats %d",
					first, got, kinds, parsed.kinds, repeats)
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
		{"no two records share an id, of one call or of different calls, so none is left out as a repeat",
			func(t *testing.T) (bool, string) {
				// A record whose id the ledger holds is left out as a repeat
				// (heldLogs), so a shared id shows as a repeat and a record
				// short of expected.jsonl's 19 for these calls.
				dir := t.TempDir()
				h := run(t, dir, adapters.Walked, capture[:20]...)
				got, _ := ids(t, dir)
				repeats := h.statsOf(event.HarnessClaudeCode, true).Repeats
				return len(got) == 19 && repeats == 0, fmt.Sprintf("%d records, repeats %d", len(got), repeats)
			}},
		{"two calls made in the same millisecond, as the PostToolUse hooks of tools run at once, each make their records under ids of their own",
			func(t *testing.T) (bool, string) {
				// Two PostToolUse calls of different tools, stamped alike: only
				// the call's id tells their records' ids apart.
				one, two := capture[2], capture[9]
				two.Arrived = one.Arrived
				dir := t.TempDir()
				h := run(t, dir, adapters.Walked, one, two)
				got, kinds := ids(t, dir)
				var tools []string
				for i, k := range kinds {
					if k == event.KindAgentTool {
						tools = append(tools, got[i])
					}
				}
				repeats := h.statsOf(event.HarnessClaudeCode, true).Repeats
				return len(tools) == 2 && tools[0] != tools[1] && repeats == 0,
					fmt.Sprintf("records %v of kinds %v, repeats %d", got, kinds, repeats)
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

// parsedKinds is an adapter that keeps the kinds of the records each of its
// Parse calls returned, in order.
type parsedKinds struct {
	adapters.Adapter
	kinds [][]event.Kind
}

func (p *parsedKinds) Parse(raw adapters.RawRecord) ([]adapters.Record, error) {
	recs, err := p.Adapter.Parse(raw)
	var ks []event.Kind
	for _, r := range recs {
		ks = append(ks, r.Event.Kind())
	}
	p.kinds = append(p.kinds, ks)
	return recs, err
}

// Folder is the folder the adapter places raw in (adapters.Placer).
func (p *parsedKinds) Folder(raw adapters.RawRecord) string {
	if pl, ok := p.Adapter.(adapters.Placer); ok {
		return pl.Folder(raw)
	}
	return ""
}

// TestEventIDVectors pins an event_id of each form ledger-v1.md section 3
// gives, under a K_session of 32 bytes of 7, at one t. The vectors come
// from Python's hmac and hashlib, which compute them from the spec's
// formulas, without this code.
func TestEventIDVectors(t *testing.T) {
	secret := bytes.Repeat([]byte{7}, hookpath.SessionSecretLen)
	h := newAdapterHost(&agent{log: slog.New(slog.DiscardHandler), problems: map[string]string{}, hooked: newHookTies()})
	h.a.kSession.Store(&secret)
	const at = 1795193310281 // Unix ms
	const call = "0123456789abcdef0123456789abcdef"
	cases := []struct {
		name, got, want string
	}{
		{"a hook call's last record, at place 0, takes the hook-event HMAC of the call's id and its place",
			h.hookEventID(at, call, 0), "01a1f9dc-0c49-7775-9a8d-792ecf701ea1"},
		{"the record before it, at place 1, takes another",
			h.hookEventID(at, call, 1), "01a1f9dc-0c49-7329-bc46-2493de96b6f8"},
		{"a log record takes the log-event HMAC of its file and its LogKey",
			h.logEventID(at, "/home/teen/.claude/projects/-p/s.jsonl", "1234.prompt"), "01a1f9dc-0c49-7fdf-9f49-4ef7d73b72d2"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.got != c.want {
				t.Errorf("got %s, want %s", c.got, c.want)
			}
		})
	}
}
