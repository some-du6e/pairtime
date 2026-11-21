// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"bytes"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/adapters"
	"github.com/some-du6e/pairtime/internal/agent/home"
	"github.com/some-du6e/pairtime/internal/agent/hookpath"
	"github.com/some-du6e/pairtime/internal/agent/inbox"
	"github.com/some-du6e/pairtime/internal/agent/keys"
	"github.com/some-du6e/pairtime/internal/agent/ledger"
	"github.com/some-du6e/pairtime/internal/platform/clock"
	"github.com/some-du6e/pairtime/pkg/event"
	pkgledger "github.com/some-du6e/pairtime/pkg/ledger"
)

// TestCallParkedAcrossLogin is a session that starts while config.toml
// says local, as every install does, in a folder whose first resolve is
// slow, so its SessionStart waits, parked, until after pairtime login
// switched the file online, in a real agent. The call was made in
// local-only mode, so its batch is sealed local-only; so is every batch
// sealed while it waited, as the batch of another session's prompt after
// the login, since a batch the server could take before it would let its
// late batch stop the batch roots until the next sign-in (ingest-v1 5.3);
// and the first batch after it is the first online one, marked as the
// first after the switch back, where the server's chain can start
// (ledger.Batch.Back).
func TestCallParkedAcrossLogin(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git isn't installed")
	}
	capture := readCapture(t)
	r := newHookReplay(t)
	slow, quick := filepath.Join(r.home, "slow"), r.folder()
	for _, dir := range []string{slow, quick} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		runGit(t, dir, "init", "-q")
		if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte(filepath.Base(dir)+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		runGit(t, dir, "add", "README.md")
		runGit(t, dir, "commit", "-q", "-m", "first in "+filepath.Base(dir))
	}
	hold := holdGit(t, slow)

	const waiting = "aaaaaaaa-1111-4222-8333-444444444444" // the session that starts in the slow folder
	base := time.Now().UTC()
	send := func(c adapters.Call) {
		c.Arrived = time.Now().UTC()
		r.send(t, event.HarnessClaudeCode, c, base)
	}
	// The agent writes the default config.toml, which says mode = "local".
	a := startAgent(t, r.options([]keys.Store{}))
	send(moved(t, capture[0], "f00031", slow, waiting, nil))
	waitStatus(t, a, func(s statusReply) bool { return s.Adapters.Waiting == 1 })
	// pairtime login switches config.toml online, writing the whole file
	// and renaming it into place (config.SetOnline, home.WriteFile).
	if err := home.WriteFile(r.dirs.ConfigFile(), []byte("mode = \"online\"\n")); err != nil {
		t.Fatal(err)
	}
	// Past the config watch's debounce, so the switch is noted either way.
	time.Sleep(time.Second)
	// Another session's prompt after the login, while the SessionStart
	// still waits.
	send(moved(t, capture[0], "f00032", quick, captureSession, nil))
	send(moved(t, capture[1], "f00033", quick, captureSession, nil))
	waitStatus(t, a, func(s statusReply) bool {
		return s.Adapters.Harnesses[event.HarnessClaudeCode].Calls == 2 && s.Adapters.Waiting == 1
	})
	// The slow folder's first resolve ends, and the SessionStart goes, with
	// its session's first prompt after it.
	os.Remove(hold)
	send(moved(t, capture[1], "f00034", slow, waiting, nil))
	waitParsed(t, a, 4)
	// The other session's next prompt.
	send(moved(t, capture[4], "f00035", quick, captureSession, nil))
	waitParsed(t, a, 5)
	a.cancel()
	if err := a.waitExit(t); err != nil {
		t.Fatal(err)
	}

	_, secret := deviceKeys(t, r.dirs, []keys.Store{})
	k := adapters.NewKeys(secret)
	var batches []ledger.Batch
	parkedIn, firstOnline := -1, -1
	if err := ledger.ReadBatches(r.dirs.Ledger(), 1, func(b ledger.Batch) error {
		for _, rec := range b.Records {
			body, err := pkgledger.ParseBody(rec.Body)
			if err != nil {
				return err
			}
			if ev, err := body.Event(); err == nil && ev.SessionKey == k.Session(waiting) && ev.Kind() == event.KindSessionStart {
				parkedIn = len(batches)
			}
		}
		if b.Online && firstOnline < 0 {
			firstOnline = len(batches)
		}
		batches = append(batches, b)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	modes := make([]string, len(batches))
	for i, b := range batches {
		switch {
		case b.Back:
			modes[i] = "marked"
		case b.Online:
			modes[i] = "online"
		default:
			modes[i] = "local"
		}
	}
	checks := []struct {
		name string
		ok   bool
	}{
		{"the SessionStart made before the login is in a batch sealed local-only", parkedIn >= 0 && !batches[parkedIn].Online},
		{"no batch before it, the prompt's sealed while it waited among them, was sealed online", firstOnline > parkedIn},
		{"the first batch sealed online comes after it and is marked as the first after the switch back",
			firstOnline >= 0 && batches[firstOnline].Back},
		{"a batch after the mark holds the other session's last prompt, and is online", slices.Contains(modes[max(firstOnline, 0):], "marked") && batches[len(batches)-1].Online},
	}
	for _, ch := range checks {
		t.Run(ch.name, func(t *testing.T) {
			if !ch.ok {
				t.Errorf("no: batches %v, the SessionStart in batch %d, the first online %d", modes, parkedIn, firstOnline)
			}
		})
	}
}

// TestAdaptersWaiting checks the adapters' part of the ledger writer's
// Waiting (agent.waiting): a hook call made before the moment asked about
// counts while it is still to be parsed, wherever it waits, and stops
// counting once its records are written.
func TestAdaptersWaiting(t *testing.T) {
	const before = int64(1_795_100_000_000)
	call := func(ms int64) inbox.Delivery { return inbox.Delivery{Received: clock.Stamp{Wall: time.UnixMilli(ms)}} }
	newHost := func() *adapterHost {
		dir := t.TempDir()
		return newAdapterHost(&agent{dirs: home.Dirs{Data: dir, Config: dir, Runtime: dir}, log: slog.New(slog.DiscardHandler),
			problems: map[string]string{}, hooked: newHookTies()})
	}
	states := []struct {
		name string
		set  func(h *adapterHost)
		want bool
	}{
		{"a call made before the moment counts while it waits for the worker", func(h *adapterHost) { h.queue = []inbox.Delivery{call(before - 1)} }, true},
		{"a call made at the moment or after it doesn't count", func(h *adapterHost) { h.queue = []inbox.Delivery{call(before)} }, false},
		{"a call made before the moment counts while the step that took it works", func(h *adapterHost) { h.stepFrom = before - 1 }, true},
		{"a parked call made before the moment counts", func(h *adapterHost) { h.parkedAt[before-1] = 1 }, true},
		{"a parked call made after it doesn't", func(h *adapterHost) { h.parkedAt[before+1] = 2 }, false},
		{"a held call made before the moment counts while calls are held", func(h *adapterHost) { h.held, h.heldFrom = 1, before-1 }, true},
		{"held calls that were all made after it don't count", func(h *adapterHost) { h.held, h.heldFrom = 3, before+1 }, false},
		{"held calls none of which the agent could read don't count", func(h *adapterHost) { h.held = 2 }, false},
		{"nothing to parse counts for nothing", func(*adapterHost) {}, false},
	}
	for _, c := range states {
		t.Run(c.name, func(t *testing.T) {
			h := newHost()
			c.set(h)
			if got := h.waiting(before); got != c.want {
				t.Errorf("waiting = %v, want %v", got, c.want)
			}
		})
	}

	t.Run("a parked call counts until it is parsed, by the time its records carry", func(t *testing.T) {
		h := newHost()
		c := hostCall{d: call(before - 1)}
		h.park(c)
		parked := h.waiting(before)
		h.unparked(c)
		if !parked || h.waiting(before) {
			t.Errorf("parked: %v, then parsed: %v; want true, then false", parked, h.waiting(before))
		}
	})
	t.Run("a call held while the keys or the writer weren't there counts, in this run and the next, until no call is held", func(t *testing.T) {
		capture := readCapture(t)
		h := newHost()
		c := capture[0]
		c.Arrived = time.UnixMilli(before - 1)
		if left := h.hold([]inbox.Delivery{adapters.DeliveryOf(event.HarnessClaudeCode, c, adapters.Walked, c.Arrived)}); len(left) != 0 {
			t.Fatalf("%d calls couldn't be held", len(left))
		}
		held := h.waiting(before)
		// The next start counts what held.jsonl holds, reading each call.
		next := newAdapterHost(h.a)
		next.countHeld()
		counted := next.waiting(before)
		next.count(func() { next.lessHeldLocked(1) })
		if !held || !counted || next.waiting(before) {
			t.Errorf("held: %v, counted at the next start: %v, after its replay: %v; want true, true, false", held, counted, next.waiting(before))
		}
	})
	t.Run("a call made before the moment counts while the step that took it parses it, and not once its records are written", func(t *testing.T) {
		capture := readCapture(t)
		h := newHost()
		secret := bytes.Repeat([]byte{7}, hookpath.SessionSecretLen)
		h.a.kSession.Store(&secret)
		lk, err := pkgledger.DeriveKeys(bytes.Repeat([]byte{9}, 32))
		if err != nil {
			t.Fatal(err)
		}
		w, err := ledger.Open(ledger.Options{Dir: h.a.dirs.Ledger(), Keys: lk, KeyID: "0123456789abcdef"})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { w.Close() })
		h.keys = &adapterKeys{feat: lk.Features(), project: bytes.Repeat([]byte{8}, 32)}
		h.writer, h.ready = w, true
		c := capture[0]
		c.Arrived = time.UnixMilli(before - 1)
		h.queue = []inbox.Delivery{adapters.DeliveryOf(event.HarnessClaudeCode, c, adapters.Walked, c.Arrived)}
		// The step asks about the call's folder before it parses the call.
		var during bool
		h.askedForTest = func(string) { during = h.waiting(before) }
		h.step(false)
		if !during || h.waiting(before) || w.Status().Next < 2 {
			t.Errorf("while the step parsed it: %v, once its records were written: %v, records written %d; want true, false, and some",
				during, h.waiting(before), w.Status().Next-1)
		}
	})
}
