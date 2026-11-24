// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"hash/crc32"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/adapters"
	"github.com/some-du6e/pairtime/internal/adapters/git"
	"github.com/some-du6e/pairtime/internal/agent/home"
	"github.com/some-du6e/pairtime/internal/agent/hookpath"
	"github.com/some-du6e/pairtime/internal/agent/inbox"
	"github.com/some-du6e/pairtime/internal/agent/keys"
	"github.com/some-du6e/pairtime/internal/agent/ledger"
	"github.com/some-du6e/pairtime/internal/agent/presence"
	"github.com/some-du6e/pairtime/internal/agent/sched"
	"github.com/some-du6e/pairtime/internal/agent/state"
	"github.com/some-du6e/pairtime/internal/agent/tailers"
	"github.com/some-du6e/pairtime/internal/agent/uploader"
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
	// The prompt asks for a seal of its own: its batch is sealed while the
	// SessionStart still waits, and local-only for it.
	promptSealed := func() (sealed, online bool) {
		ledger.ReadBatches(r.dirs.Ledger(), 1, func(b ledger.Batch) error {
			for _, rec := range b.Records {
				if body, err := pkgledger.ParseBody(rec.Body); err == nil {
					if ev, err := body.Event(); err == nil && ev.Kind() == event.KindPromptSubmit {
						sealed, online = true, b.Online
					}
				}
			}
			return nil
		})
		return sealed, online
	}
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		if sealed, online := promptSealed(); sealed {
			if online {
				t.Fatal("the prompt sent while the SessionStart waited was sealed online before the SessionStart reached the ledger")
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the prompt sent while the SessionStart waited was never sealed")
		}
	}
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
	// lastPrompt is the batch of the other session's last prompt, which
	// it sent once the parked call was parsed.
	parkedIn, firstOnline, lastPrompt := -1, -1, -1
	if err := ledger.ReadBatches(r.dirs.Ledger(), 1, func(b ledger.Batch) error {
		for _, rec := range b.Records {
			body, err := pkgledger.ParseBody(rec.Body)
			if err != nil {
				return err
			}
			ev, err := body.Event()
			if err != nil {
				continue
			}
			switch {
			case ev.SessionKey == k.Session(waiting) && ev.Kind() == event.KindSessionStart:
				parkedIn = len(batches)
			case ev.SessionKey == k.Session(captureSession) && ev.Kind() == event.KindPromptSubmit:
				lastPrompt = len(batches)
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
		{"the other session's last prompt, sent once the call was parsed, is in an online batch from the mark on",
			firstOnline >= 0 && lastPrompt >= firstOnline && batches[lastPrompt].Online},
	}
	for _, ch := range checks {
		t.Run(ch.name, func(t *testing.T) {
			if !ch.ok {
				t.Errorf("no: batches %v, the SessionStart in batch %d, the first online %d, the other session's last prompt in %d", modes, parkedIn, firstOnline, lastPrompt)
			}
		})
	}
}

// TestAdaptersWaiting checks the adapters' part of the ledger writer's
// Waiting (agent.waiting): a hook call made before the moment asked about
// counts while it is still to be parsed, wherever it waits, and stops
// counting once its records are written.
func TestAdaptersWaiting(t *testing.T) {
	const moment, boot = int64(1_795_100_000_000), "b_os"
	// before is the writer's test of an event against the moment: by its
	// t, or by its mono in boot, where the moment is mono 50,000, each
	// holding its own millisecond (ledger moment.before).
	before := func(e event.Envelope) bool {
		return e.T <= moment || e.BootID == boot && e.Mono > 0 && e.Mono <= 50_000
	}
	call := func(ms int64) inbox.Delivery { return inbox.Delivery{Received: clock.Stamp{Wall: time.UnixMilli(ms)}} }
	// resent is a call the hook stamped at sent with no monotonic reading,
	// so its records carry the agent's stamp, from received
	// (hookpath.Times).
	resent := func(sent, received int64) inbox.Delivery {
		return inbox.Delivery{Message: inbox.Message{Sent: time.UnixMilli(sent)}, Received: clock.Stamp{Wall: time.UnixMilli(received)}}
	}
	at := func(ms int64) callStamp { return callStamp{t: ms} }
	// leave writes what an earlier run left in held.jsonl, with suffix.
	leave := func(h *adapterHost, suffix, body string) {
		if err := os.WriteFile(h.a.dirs.Held()+suffix, []byte(body), 0o600); err != nil {
			panic(err)
		}
	}
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
		{"a call made before the moment counts while it waits for the worker", func(h *adapterHost) { h.queue = []inbox.Delivery{call(moment - 1)} }, true},
		{"a call made at the moment counts, since one of the moment's own millisecond can't be told from one made before it", func(h *adapterHost) { h.queue = []inbox.Delivery{call(moment)} }, true},
		{"a call made after the moment doesn't count", func(h *adapterHost) { h.queue = []inbox.Delivery{call(moment + 1)} }, false},
		{"a call made before the moment counts while the step that took it works", func(h *adapterHost) { h.stepping = []callStamp{at(moment + 1), at(moment - 1)} }, true},
		{"a parked call made before the moment counts", func(h *adapterHost) { h.parkedAt[at(moment-1)] = 1 }, true},
		{"a parked call made after it doesn't", func(h *adapterHost) { h.parkedAt[at(moment+1)] = 2 }, false},
		{"a parked call made before the moment by its boot's monotonic clock counts, though its t is after",
			func(h *adapterHost) { h.parkedAt[callStamp{t: moment + 1, mono: 49_000, boot: boot}] = 1 }, true},
		{"a held call made before the moment counts while calls are held", func(h *adapterHost) { h.held = 1; h.heldFrom.add(at(moment - 1)) }, true},
		{"a held call made before the moment by its boot's monotonic clock counts, though its t is after",
			func(h *adapterHost) { h.held = 1; h.heldFrom.add(callStamp{t: moment + 1, mono: 49_000, boot: boot}) }, true},
		{"held calls count while before holds a stretch, though the earliest was made after the moment: the earliest alone can't tell whether one was made in a stretch",
			func(h *adapterHost) { h.held = 3; h.heldFrom.add(at(moment + 1)) }, true},
		{"held calls none of which the agent could read don't count", func(h *adapterHost) { h.held = 2 }, false},
		{"held calls a .replay file this run can't delete keeps don't count, as they wait for the next start",
			func(h *adapterHost) {
				h.held = 1
				h.heldFrom.add(at(moment - 1))
				h.replayStuck = true
				h.replayStopped()
			}, false},
		{"nor do those a writer that stopped after a failure leaves", func(h *adapterHost) { h.held = 1; h.heldFrom.add(at(moment - 1)); h.down = true; h.replayStopped() }, false},
		{"a call whose records carry the agent's stamp counts by the hook's own when that is before the moment",
			func(h *adapterHost) { h.queue = []inbox.Delivery{resent(moment-1, moment+5)} }, true},
		{"a parked call counts by the hook's own stamp too", func(h *adapterHost) { h.parkedAt[stampOf(resent(moment-1, moment+5))] = 1 }, true},
		{"a call in the step counts by the hook's own stamp too", func(h *adapterHost) { h.stepping = []callStamp{stampOf(resent(moment-1, moment+5))} }, true},
		{"a held call counts by the hook's own stamp too", func(h *adapterHost) { h.held = 1; h.heldFrom.add(stampOf(resent(moment-1, moment+5))) }, true},
		{"a call whose own stamp and its records' are both after the moment doesn't count",
			func(h *adapterHost) { h.queue = []inbox.Delivery{resent(moment+1, moment+5)} }, false},
		{"with nothing an earlier run held, nothing waits to be counted", func(h *adapterHost) { h.expectCount() }, false},
		{"calls a kill mid-replay left in held.jsonl's .replay file may be on their way until they are counted",
			func(h *adapterHost) { leave(h, ".replay", "{}\n"); h.expectCount() }, true},
		{"so may calls a kill left in the pending log", func(h *adapterHost) { leave(h, pendingSuffix, "{}\n"); h.expectCount() }, true},
		{"an empty held.jsonl holds no call", func(h *adapterHost) { leave(h, "", ""); h.expectCount() }, false},
		{"a socket call with no monotonic reading of the hook's, stamped and taken after the moment, doesn't count, though the agent took it within spoolSlack of the moment",
			func(h *adapterHost) {
				h.queue = []inbox.Delivery{{Message: inbox.Message{Sent: time.UnixMilli(moment + 1)}, Via: inbox.ViaSocket,
					Received: clock.Stamp{Wall: time.UnixMilli(moment + 5), Mono: 50_001, BootID: boot}}}
			}, false},
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

	flows := []struct {
		name  string
		check func(t *testing.T)
	}{
		{"from the ledger's start until the worker has counted the calls an earlier run held, any of them may be on its way, and once it has, a held call it can't read counts for nothing", func(t *testing.T) {
			h := newHost()
			leftHeld(t, h)
			h.expectCount()
			uncounted := h.waiting(before)
			h.countHeld()
			if !uncounted || h.waiting(before) {
				t.Errorf("before the count: %v, after it: %v; want true, false", uncounted, h.waiting(before))
			}
		}},
		{"a moment that holds nothing, as after an upgrade from a version whose seal frames keep none, holds no call the worker hasn't counted yet", func(t *testing.T) {
			h := newHost()
			leftHeld(t, h)
			h.expectCount()
			// The newest batch counts as local-only, and its row keeps no
			// moment and no stretch, as an older version's, so the next seal
			// asks Waiting by the writer's own stretches, which hold nothing.
			first := optionsWriter(t, h.a.dirs, func(o *ledger.Options) { o.Online = func() bool { return false } })
			if _, err := first.Write(presenceAt(time.Now())); err != nil {
				t.Fatal(err)
			}
			if err := first.Close(); err != nil {
				t.Fatal(err)
			}
			dropRowStretches(t, h.a.dirs.Ledger())
			_, rows, err := ledger.ReadRows(h.a.dirs.Ledger(), 0)
			if err != nil {
				t.Fatal(err)
			}
			if len(rows) != 1 || rows[0].Online || !rows[0].LocalUntil.IsZero() {
				t.Fatalf("the first writer sealed %+v, want one local-only batch with no moment", rows)
			}
			w := optionsWriter(t, h.a.dirs, func(o *ledger.Options) {
				o.Online = func() bool { return true }
				o.Waiting = h.waiting
			})
			if _, err := w.Write(presenceAt(time.Now())); err != nil {
				t.Fatal(err)
			}
			if b, ok, err := w.Seal(); !ok || err != nil || !b.Online {
				t.Errorf("the batch after the local-only one was sealed %v, online %v (%v); want online, since a moment that holds nothing holds no call", ok, b.Online, err)
			}
		}},
		{"a stretch the newest seal frame records as going on, which the next Open ends, holds the calls an earlier run held until the worker has counted them, as their times aren't known yet, though it began after the chain's first", func(t *testing.T) {
			h := newHost()
			leftHeld(t, h)
			h.expectCount()
			// The install's stretch, the switch back's batch, one sealed online
			// after it, which passes that stretch, and a second stretch, going
			// on as the writer closes. It began at the online batch's read,
			// after the beginning of time, so only the rule for an envelope
			// with no times holds the calls in it (ledger waitFor.holds).
			var local atomic.Bool
			first := optionsWriter(t, h.a.dirs, func(o *ledger.Options) { o.Online = func() bool { return !local.Load() } })
			for _, l := range []bool{true, false, false, true} {
				local.Store(l)
				// Made a few ms after the seal before, so no stretch end of
				// that seal's millisecond holds it.
				time.Sleep(5 * time.Millisecond)
				if _, err := first.Write(presenceAt(time.Now())); err != nil {
					t.Fatal(err)
				}
				if _, _, err := first.Seal(); err != nil {
					t.Fatal(err)
				}
			}
			if err := first.Close(); err != nil {
				t.Fatal(err)
			}
			_, rows, err := ledger.ReadRows(h.a.dirs.Ledger(), 0)
			if err != nil || len(rows) != 4 || rows[0].Online || !rows[2].Online || rows[3].Online {
				t.Fatalf("the first writer sealed %+v (%v); want local-only, either, online, local-only", rows, err)
			}
			w := optionsWriter(t, h.a.dirs, func(o *ledger.Options) {
				o.Online = func() bool { return true }
				o.Waiting = h.waiting
			})
			if _, err := w.Write(presenceAt(time.Now().Add(time.Second))); err != nil {
				t.Fatal(err)
			}
			b, ok, err := w.Seal()
			h.countHeld()
			if _, err := w.Write(presenceAt(time.Now().Add(2 * time.Second))); err != nil {
				t.Fatal(err)
			}
			b2, ok2, err2 := w.Seal()
			if !ok || err != nil || b.Online || !ok2 || err2 != nil || !b2.Online {
				t.Errorf("the batches after the local-only one were sealed online %v, then %v (%v, %v); want local-only while the held calls are uncounted, then online", b.Online, b2.Online, err, err2)
			}
		}},
		{"held calls count while a stretch no batch sealed online has passed holds anything, as one made in it can be held behind one made before it began, which is all the earliest stamp tells of", func(t *testing.T) {
			h := newHost()
			// The writer's Waiting asks about a stretch from moment-10,000 to the
			// moment, and holds an envelope with no times while it has one
			// (ledger waitFor.holds).
			inStretch := func(e event.Envelope) bool {
				if e.T == 0 && e.Mono == 0 {
					return true
				}
				return e.T >= moment-10_000 && e.T <= moment
			}
			h.held = 2
			h.heldFrom.add(at(moment - 20_000)) // before the stretch began
			h.heldFrom.add(at(moment - 5000))   // in it
			if !h.waiting(inStretch) {
				t.Error("waiting = false while a call made in the stretch is held behind one made before it; want true")
			}
		}},
		{"a stuck replay's held calls, made in a stretch, wait for the next start, so after a switch back the seal due the mark goes online", func(t *testing.T) {
			h := newHost()
			var local atomic.Bool
			local.Store(true)
			w := optionsWriter(t, h.a.dirs, func(o *ledger.Options) {
				o.Online = func() bool { return !local.Load() }
				o.Waiting = h.waiting
			})
			if _, err := w.Write(presenceAt(time.Now())); err != nil {
				t.Fatal(err)
			}
			// The stretch's first record seals at once on the writer's loop,
			// which may run before this seal, so the newest row says how the
			// batch went.
			if r := sealNow(t, w, h.a.dirs.Ledger()); r.Online {
				t.Fatal("the stretch's batch was sealed online; want local-only")
			}
			// A call made in the stretch is held behind a .replay file this run
			// couldn't delete.
			h.held = 1
			h.heldFrom.add(callStamp{t: time.Now().UnixMilli()})
			h.replayStuck = true
			h.replayStopped()
			time.Sleep(5 * time.Millisecond)
			local.Store(false)
			w.NoteMode(true)
			time.Sleep(5 * time.Millisecond)
			if _, err := w.Write(presenceAt(time.Now())); err != nil {
				t.Fatal(err)
			}
			// The note asked for a seal of its own, which the writer's loop may
			// run before this one, so the newest row says how the batch went.
			if r := sealNow(t, w, h.a.dirs.Ledger()); !r.Online || !r.Back {
				t.Errorf("the first batch after the switch back was sealed online %v, marked %v; want online and marked", r.Online, r.Back)
			}
		}},
		{"a parked call counts until it is parsed, by the time its records carry", func(t *testing.T) {
			h := newHost()
			c := hostCall{d: call(moment - 1)}
			h.park(c)
			parked := h.waiting(before)
			h.unparked(c)
			if !parked || h.waiting(before) {
				t.Errorf("parked: %v, then parsed: %v; want true, then false", parked, h.waiting(before))
			}
		}},
		{"a call held while the keys or the writer weren't there counts, in this run and the next, until no call is held", func(t *testing.T) {
			capture := readCapture(t)
			h := newHost()
			c := capture[0]
			c.Arrived = time.UnixMilli(moment - 1)
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
		}},
		{"a call made before the moment counts while the step that took it parses it, and not once its records are written", func(t *testing.T) {
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
			c.Arrived = time.UnixMilli(moment - 1)
			h.queue = []inbox.Delivery{adapters.DeliveryOf(event.HarnessClaudeCode, c, adapters.Walked, c.Arrived)}
			// The step asks about the call's folder before it parses the call.
			var during bool
			h.askedForTest = func(string) { during = h.waiting(before) }
			h.step(false)
			if !during || h.waiting(before) || w.Status().Next < 2 {
				t.Errorf("while the step parsed it: %v, once its records were written: %v, records written %d; want true, false, and some",
					during, h.waiting(before), w.Status().Next-1)
			}
		}},
		{"a call's records reach the writer with the hook's own stamp, which seals them local-only when only it is before the moment", func(t *testing.T) {
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
			// A stretch that ends now: the moment.
			w.NoteMode(false)
			w.NoteMode(true)
			h.keys = &adapterKeys{feat: lk.Features(), project: bytes.Repeat([]byte{8}, 32)}
			h.writer, h.ready = w, true
			c := capture[0]
			c.Arrived = time.Now().Add(-time.Minute)
			d := adapters.DeliveryOf(event.HarnessClaudeCode, c, adapters.Walked, c.Arrived)
			// The hook gave no monotonic reading, so its records carry the
			// agent's stamp, from after the moment.
			d.Mono, d.BootID = 0, ""
			d.Received = clock.Now()
			h.queue = []inbox.Delivery{d}
			h.step(false)
			if w.Status().Next < 2 {
				t.Fatal("the call's records weren't written")
			}
			if r := sealNow(t, w, h.a.dirs.Ledger()); r.Online {
				t.Error("the batch of the call's records was sealed online, want local-only")
			}
		}},
	}
	for _, c := range flows {
		t.Run(c.name, c.check)
	}
}

// TestModesSeen checks what the agent sees of config.toml's mode for the
// ledger writer (modes): the config watch's first look at a change notes a
// switch back online at once, and only that; a stretch the agent saw end
// while a locked keyring kept the ledger shut, or that ended before a
// pairtime login the agent didn't see, or that the last run saw going on,
// makes the records made before its end that reach the writer local-only,
// and the first batch after them is marked as the first after the switch
// back (ledger.Batch.Back); and what it sees goes to state.json as it
// changes.
func TestModesSeen(t *testing.T) {
	// opened waits for the agent's ledger to open, as on a goroutine of its
	// own.
	opened := func(t *testing.T, a *agent) *ledger.Writer {
		t.Helper()
		for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
			if w := a.writer.Load(); w != nil {
				return w
			}
		}
		t.Fatal("the ledger didn't open")
		return nil
	}
	// sealOne writes an os.presence event made at at to the agent's
	// writer, and seals it.
	sealOne := func(t *testing.T, a *agent, w *ledger.Writer, at time.Time) ledger.Row {
		t.Helper()
		if _, err := w.Write(presenceAt(at)); err != nil {
			t.Fatal(err)
		}
		return sealNow(t, w, a.dirs.Ledger())
	}
	seenLocal := func(a *agent) (bool, time.Time) {
		a.modes.mu.Lock()
		defer a.modes.mu.Unlock()
		return a.modes.local, a.modes.ended
	}
	// kept is what state.json says modes saw, as the next start reads it.
	kept := func(t *testing.T, dirs home.Dirs) modesState {
		t.Helper()
		st, _, err := state.Open(dirs.StateFile())
		if err != nil {
			t.Fatal(err)
		}
		var ms modesState
		if _, err := st.Get(modesKey, &ms); err != nil {
			t.Fatal(err)
		}
		return ms
	}
	cases := []struct {
		name  string
		check func(t *testing.T)
	}{
		{"a switch back online the config watch hears of is noted at its first look, before the reload", func(t *testing.T) {
			a, dirs := modesAgent(t, "local")
			t.Cleanup(a.startLedger())
			w := opened(t, a)
			// The scheduler doesn't run, so the watch's reload never comes.
			t.Cleanup(a.watchConfig())
			if err := home.WriteFile(dirs.ConfigFile(), []byte("mode = \"online\"\n")); err != nil {
				t.Fatal(err)
			}
			for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(10 * time.Millisecond) {
				if local, _ := seenLocal(a); !local {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("the config watch's first look didn't note the switch back online")
				}
			}
			// A millisecond after the note: one of the note's own millisecond
			// counts as made before it.
			if b := sealOne(t, a, w, time.Now().Add(time.Millisecond)); !b.Online || !b.Back {
				t.Errorf("the batch after the switch back was sealed online %v, marked %v; want both", b.Online, b.Back)
			}
		}},
		{"a first look at config.toml that still says local-only notes nothing", func(t *testing.T) {
			a, dirs := modesAgent(t, "local")
			a.startModes()
			t.Cleanup(a.watchConfig())
			if err := home.WriteFile(dirs.ConfigFile(), []byte("mode = \"local\"\n# an edit of another line\n")); err != nil {
				t.Fatal(err)
			}
			time.Sleep(500 * time.Millisecond)
			if local, ended := seenLocal(a); !local || !ended.IsZero() {
				t.Errorf("after the look, local-only %v, a stretch ended %v; want still local-only, and none ended", local, ended)
			}
		}},
		{"presence's records queued while a locked keyring kept the ledger shut through a switch back online are sealed local-only, and the batch after them is marked", func(t *testing.T) {
			a, dirs := modesAgent(t, "local")
			store := &lockedStore{items: map[string][]byte{}}
			a.keyStores = []keys.Store{store}
			// One run unlocked first, so keystore.json names the store and the
			// next run's keystore stays shut while it is locked.
			first, err := a.openLedger()
			if err != nil {
				t.Fatal(err)
			}
			if err := first.Close(); err != nil {
				t.Fatal(err)
			}
			store.setLocked(true)
			a.ledgerRetry = 20 * time.Millisecond
			t.Cleanup(a.startLedger())
			for deadline := time.Now().Add(10 * time.Second); store.refusals() == 0; time.Sleep(10 * time.Millisecond) {
				if time.Now().After(deadline) {
					t.Fatal("the agent never asked the locked keyring")
				}
			}
			// A second before the switch, so the two readings of this run's
			// clock, cut to the millisecond, can't be the same.
			made := time.Now().Add(-time.Second)
			a.ledgerIn.Record(presence.Record{At: made, Data: &event.OSPresence{IdleAnyS: 1}})
			// pairtime login switches config.toml online, and the watch notes it.
			setMode(t, dirs, "online")
			a.noteMode()
			store.setLocked(false)
			w := opened(t, a)
			for deadline := time.Now().Add(10 * time.Second); w.Status().Next < 2; time.Sleep(10 * time.Millisecond) {
				if time.Now().After(deadline) {
					t.Fatal("presence's queued record never reached the writer")
				}
			}
			waited := sealNow(t, w, dirs.Ledger())
			after := sealOne(t, a, w, time.Now().Add(time.Millisecond))
			if waited.Online || waited.Back || !after.Online || !after.Back {
				t.Errorf("the queued record's batch sealed online %v, marked %v, and the next online %v, marked %v; want local-only, then online and marked",
					waited.Online, waited.Back, after.Online, after.Back)
			}
		}},
		{"a pairtime login the agent didn't see ends the stretch where the agent finds it, by both clocks: an event made in the link's second, or before the agent's start, is sealed local-only, and the batch after them is marked", func(t *testing.T) {
			a, dirs := modesAgent(t, "online")
			linked := time.Now().Add(-time.Minute).UTC().Truncate(time.Second)
			writeAccount(t, dirs, linked)
			// An event of this run made before the agent found the link, by
			// this run's clock, though after the link by the wall clock: a
			// second before, so the two readings, cut to the millisecond,
			// can't be the same.
			made := time.Now().Add(-time.Second)
			t.Cleanup(a.startLedger())
			w := opened(t, a)
			inSecond := sealOne(t, a, w, linked.Add(500*time.Millisecond))
			if _, err := w.Write(runPresenceAt(made)); err != nil {
				t.Fatal(err)
			}
			beforeStart := sealNow(t, w, dirs.Ledger())
			if _, err := w.Write(runPresenceAt(time.Now().Add(time.Second))); err != nil {
				t.Fatal(err)
			}
			after := sealNow(t, w, dirs.Ledger())
			if inSecond.Online || beforeStart.Online || !after.Online || !after.Back {
				t.Errorf("the event in the link's second sealed online %v, the one made before the agent's start %v, and the next online %v, marked %v; want local-only, local-only, then online and marked",
					inSecond.Online, beforeStart.Online, after.Online, after.Back)
			}
		}},
		{"a link state.json keeps as seen is no link the agent didn't see at the next start", func(t *testing.T) {
			a, dirs := modesAgent(t, "online")
			linked := time.Now().Add(-time.Hour).UTC().Truncate(time.Second)
			writeAccount(t, dirs, linked)
			ended := time.Now().Add(-time.Minute).Round(0)
			keepState(t, dirs, map[string]any{modesKey: modesState{Ended: ended, Linked: linked.UnixMilli()}})
			if a.state, _, _ = state.Open(dirs.StateFile()); a.state == nil {
				t.Fatal("state.json didn't open")
			}
			a.startModes()
			if got := a.seen(); !got.Ended.Equal(ended) || !got.Earlier || !got.Until.IsZero() {
				t.Errorf("the start saw %+v, want the stretch end state.json kept, as an earlier run's, with no link's end", got)
			}
		}},
		{"a state.json from before modes kept its link takes the link the uploader had settled, its stretch ended at the link's second by the wall clock alone, as an earlier run's", func(t *testing.T) {
			a, dirs := modesAgent(t, "online")
			linked := time.Now().Add(-time.Hour).UTC().Truncate(time.Second)
			writeAccount(t, dirs, linked)
			keepState(t, dirs, map[string]any{uploader.StateKey: map[string]int64{"linked_at": linked.UnixMilli()}})
			if a.state, _, _ = state.Open(dirs.StateFile()); a.state == nil {
				t.Fatal("state.json didn't open")
			}
			a.startModes()
			got, ms := a.seen(), kept(t, dirs)
			if !got.Ended.Equal(linked.Add(time.Second)) || !got.Earlier || got.OS.HasMono() || !got.Until.IsZero() || ms.Linked != linked.UnixMilli() {
				t.Errorf("the start saw %+v, and state.json keeps %+v; want the stretch ended at the end of the link's second, as an earlier run's, and the uploader's link kept", got, ms)
			}
		}},
		{"a link the uploader had settled, though modes kept another, ends the stretch at the link's second by the wall clock alone", func(t *testing.T) {
			a, dirs := modesAgent(t, "online")
			linked := time.Now().Add(-time.Hour).UTC().Truncate(time.Second)
			writeAccount(t, dirs, linked)
			keepState(t, dirs, map[string]any{
				modesKey: modesState{Ended: linked.Add(-time.Hour), OSMono: 12_345, OSBoot: "b_kept",
					Linked: linked.Add(-2 * time.Hour).UnixMilli()},
				uploader.StateKey: map[string]int64{"linked_at": linked.UnixMilli()},
			})
			if a.state, _, _ = state.Open(dirs.StateFile()); a.state == nil {
				t.Fatal("state.json didn't open")
			}
			a.startModes()
			if got := a.seen(); !got.Ended.Equal(linked.Add(time.Second)) || !got.Earlier || got.OS.HasMono() {
				t.Errorf("the start saw %+v, want the stretch ended at the end of the link's second, as an earlier run's", got)
			}
		}},
		{"a state.json from before modes kept its link, whose uploader had settled another link, takes account.json's as a link the agent didn't see as it starts", func(t *testing.T) {
			a, dirs := modesAgent(t, "online")
			linked := time.Now().Add(-time.Hour).UTC().Truncate(time.Second)
			writeAccount(t, dirs, linked)
			keepState(t, dirs, map[string]any{uploader.StateKey: map[string]int64{"linked_at": linked.Add(-time.Hour).UnixMilli()}})
			if a.state, _, _ = state.Open(dirs.StateFile()); a.state == nil {
				t.Fatal("state.json didn't open")
			}
			a.startModes()
			// The start keeps it, before anything asks what modes saw.
			if ms := kept(t, dirs); ms.Linked != linked.UnixMilli() || ms.Ended.IsZero() || !ms.Until.Equal(linked.Add(time.Second)) {
				t.Errorf("state.json keeps %+v after the start, want the link, with the stretch's end", ms)
			}
			if got := a.seen(); got.Ended.IsZero() || got.Earlier || !got.Until.Equal(linked.Add(time.Second)) || !got.OS.HasMono() {
				t.Errorf("the start saw %+v, want the stretch ended now, by both clocks, and by the wall clock to the end of the link's second", got)
			}
		}},
		{"a state.json set aside, which kept neither modes nor the uploader's link, is rebuilt from the ledger: a batch sealed online after the link's second has the link count as seen, and the newest seal frame's local_until ends the stretch, as an earlier run's", func(t *testing.T) {
			a, dirs := modesAgent(t, "online")
			linked := time.Now().Add(-time.Hour).UTC().Truncate(time.Second)
			writeAccount(t, dirs, linked)
			// The last run saw the stretch end a minute after the link, and
			// sealed a batch online after it.
			ended := linked.Add(time.Minute)
			w := optionsWriter(t, dirs, func(o *ledger.Options) { o.Online = func() bool { return true } })
			w.NoteSeen(ledger.Seen{Ended: ended})
			if _, err := w.Write(presenceAt(time.Now())); err != nil {
				t.Fatal(err)
			}
			if row := sealNow(t, w, dirs.Ledger()); !row.Online || !row.LocalUntil.Equal(ended) {
				t.Fatalf("the last run's batch sealed online %v, local-only until %v; want online, until %v", row.Online, row.LocalUntil, ended)
			}
			if err := w.Close(); err != nil {
				t.Fatal(err)
			}
			if a.state, _, _ = state.Open(dirs.StateFile()); a.state == nil {
				t.Fatal("state.json didn't open")
			}
			a.startModes()
			got, ms := a.seen(), kept(t, dirs)
			if got.Local || !got.Ended.Equal(ended) || !got.Earlier || got.OS.HasMono() || !got.Until.IsZero() ||
				ms.Linked != linked.UnixMilli() || a.linkedSeen.Load() != linked.UnixMilli() {
				t.Errorf("the start saw %+v, and state.json keeps %+v; want the stretch ended at the newest seal frame's local_until, as an earlier run's, and the link seen", got, ms)
			}
		}},
		{"a state.json set aside, with no batch sealed online after the link's second, leaves the link one the agent didn't see, which ends the stretch where the agent finds it", func(t *testing.T) {
			a, dirs := modesAgent(t, "online")
			linked := time.Now().Add(-time.Hour).UTC().Truncate(time.Second)
			writeAccount(t, dirs, linked)
			w := optionsWriter(t, dirs, func(o *ledger.Options) { o.Online = func() bool { return false } })
			// The seal frames record a moment, so only the missing online
			// batch keeps the link unseen.
			w.NoteSeen(ledger.Seen{Ended: linked.Add(time.Minute)})
			if _, err := w.Write(presenceAt(time.Now())); err != nil {
				t.Fatal(err)
			}
			if err := w.Close(); err != nil {
				t.Fatal(err)
			}
			if a.state, _, _ = state.Open(dirs.StateFile()); a.state == nil {
				t.Fatal("state.json didn't open")
			}
			a.startModes()
			if got := a.seen(); got.Earlier || !got.OS.HasMono() || !got.Until.Equal(linked.Add(time.Second)) {
				t.Errorf("the start saw %+v, want the stretch ended now, by both clocks, and by the wall clock to the end of the link's second", got)
			}
		}},
		{"a state.json set aside, whose ledger's newest seal frame records no moment, as an older version's doesn't, leaves the link one the agent didn't see", func(t *testing.T) {
			a, dirs := modesAgent(t, "online")
			linked := time.Now().Add(-time.Hour).UTC().Truncate(time.Second)
			writeAccount(t, dirs, linked)
			w := optionsWriter(t, dirs, func(o *ledger.Options) { o.Online = func() bool { return true } })
			if _, err := w.Write(presenceAt(time.Now())); err != nil {
				t.Fatal(err)
			}
			if row := sealNow(t, w, dirs.Ledger()); !row.Online || !row.LocalUntil.IsZero() {
				t.Fatalf("the batch sealed online %v, local-only until %v; want online, with no moment", row.Online, row.LocalUntil)
			}
			if err := w.Close(); err != nil {
				t.Fatal(err)
			}
			if a.state, _, _ = state.Open(dirs.StateFile()); a.state == nil {
				t.Fatal("state.json didn't open")
			}
			a.startModes()
			if got := a.seen(); got.Earlier || !got.OS.HasMono() || !got.Until.Equal(linked.Add(time.Second)) {
				t.Errorf("the start saw %+v, want the stretch ended now, by both clocks, and by the wall clock to the end of the link's second", got)
			}
		}},
		{"a state.json set aside, whose ledger's newest batch counts as local-only, rebuilds the link as seen and the stretch as going on as the last run stopped, so it ends at the start, by both clocks", func(t *testing.T) {
			a, dirs := modesAgent(t, "online")
			linked := time.Now().Add(-time.Hour).UTC().Truncate(time.Second)
			writeAccount(t, dirs, linked)
			var on atomic.Bool
			on.Store(true)
			w := optionsWriter(t, dirs, func(o *ledger.Options) { o.Online = on.Load })
			w.NoteSeen(ledger.Seen{Ended: linked.Add(time.Minute)})
			if _, err := w.Write(presenceAt(time.Now())); err != nil {
				t.Fatal(err)
			}
			if row := sealNow(t, w, dirs.Ledger()); !row.Online {
				t.Fatal("the last run's first batch was sealed local-only")
			}
			on.Store(false)
			if _, err := w.Write(presenceAt(time.Now())); err != nil {
				t.Fatal(err)
			}
			if row := sealNow(t, w, dirs.Ledger()); row.Online {
				t.Fatal("the last run's newest batch was sealed online")
			}
			if err := w.Close(); err != nil {
				t.Fatal(err)
			}
			if a.state, _, _ = state.Open(dirs.StateFile()); a.state == nil {
				t.Fatal("state.json didn't open")
			}
			start := time.Now()
			a.startModes()
			if got := a.seen(); got.Earlier || !got.OS.HasMono() || !got.Until.IsZero() || got.Ended.Before(start) || a.linkedSeen.Load() != linked.UnixMilli() {
				t.Errorf("the start saw %+v, want the stretch ended at the start, by both clocks, and the link seen", got)
			}
		}},
		{"a pairtime login made after the start, which the config watch didn't hear, is taken in as the ledger opens: the stretch ends there by both clocks, and state.json keeps the link", func(t *testing.T) {
			a, dirs := modesAgent(t, "online")
			first := time.Now().Add(-time.Hour).UTC().Truncate(time.Second)
			writeAccount(t, dirs, first)
			keepState(t, dirs, map[string]any{modesKey: modesState{Ended: first.Add(time.Minute), Linked: first.UnixMilli()}})
			if a.state, _, _ = state.Open(dirs.StateFile()); a.state == nil {
				t.Fatal("state.json didn't open")
			}
			a.startModes()
			relinked := time.Now().UTC().Truncate(time.Second)
			writeAccount(t, dirs, relinked)
			got, ms := a.seen(), kept(t, dirs)
			if got.Earlier || !got.OS.HasMono() || !got.Until.Equal(relinked.Add(time.Second)) || ms.Linked != relinked.UnixMilli() {
				t.Errorf("Open saw %+v, and state.json keeps %+v; want the stretch ended now by both clocks, and the new link kept", got, ms)
			}
		}},
		{"a seal while account.json names a link modes didn't account for is local-only, and the agent takes the link in off the seal's path, so the batch after it is marked", func(t *testing.T) {
			a, dirs := modesAgent(t, "online")
			first := time.Now().Add(-time.Hour).UTC().Truncate(time.Second)
			writeAccount(t, dirs, first)
			keepState(t, dirs, map[string]any{modesKey: modesState{Ended: first.Add(time.Minute), Linked: first.UnixMilli()}})
			if a.state, _, _ = state.Open(dirs.StateFile()); a.state == nil {
				t.Fatal("state.json didn't open")
			}
			t.Cleanup(a.startLedger())
			w := opened(t, a)
			if _, err := w.Write(runPresenceAt(time.Now())); err != nil {
				t.Fatal(err)
			}
			// A logout and a login the config watch didn't hear.
			relinked := time.Now().UTC().Truncate(time.Second)
			writeAccount(t, dirs, relinked)
			unseen := sealNow(t, w, dirs.Ledger())
			for deadline := time.Now().Add(10 * time.Second); a.linkedSeen.Load() != relinked.UnixMilli() || a.seeing.Load(); time.Sleep(5 * time.Millisecond) {
				if time.Now().After(deadline) {
					t.Fatal("the agent never took the link in")
				}
			}
			if ms := kept(t, dirs); ms.Linked != relinked.UnixMilli() || ms.Ended.IsZero() || !ms.Until.Equal(relinked.Add(time.Second)) {
				t.Errorf("state.json keeps %+v after the link was taken in, want the link, with the stretch's end", ms)
			}
			if _, err := w.Write(runPresenceAt(time.Now().Add(time.Second))); err != nil {
				t.Fatal(err)
			}
			after := sealNow(t, w, dirs.Ledger())
			if unseen.Online || !after.Online || !after.Back {
				t.Errorf("the seal that found the link sealed online %v, and the next online %v, marked %v; want local-only, then online and marked", unseen.Online, after.Online, after.Back)
			}
		}},
		{"a seal that reads config.toml online while modes still says local-only, as with a config watch that hears nothing, has modes see it, so state.json keeps the stretch's end and the next start ends none", func(t *testing.T) {
			a, dirs := modesAgent(t, "local")
			if a.state, _, _ = state.Open(dirs.StateFile()); a.state == nil {
				t.Fatal("state.json didn't open")
			}
			t.Cleanup(a.startLedger())
			w := opened(t, a)
			// No config watch runs here to hear the switch.
			setMode(t, dirs, "online")
			if _, err := w.Write(runPresenceAt(time.Now())); err != nil {
				t.Fatal(err)
			}
			sealNow(t, w, dirs.Ledger())
			for deadline := time.Now().Add(10 * time.Second); a.modesLocal.Load() || a.seeing.Load(); time.Sleep(5 * time.Millisecond) {
				if time.Now().After(deadline) {
					t.Fatal("modes never saw config.toml online")
				}
			}
			if ms := kept(t, dirs); ms.Local || ms.Ended.IsZero() {
				t.Errorf("state.json keeps %+v, want the stretch ended, so the next start doesn't end it there", ms)
			}
		}},
		{"a link the uploader had settled, after a stretch the last run saw going on, keeps the end the start gave that stretch, by both clocks", func(t *testing.T) {
			a, dirs := modesAgent(t, "online")
			linked := time.Now().Add(-time.Hour).UTC().Truncate(time.Second)
			writeAccount(t, dirs, linked)
			keepState(t, dirs, map[string]any{
				modesKey:          modesState{Local: true, Linked: linked.Add(-2 * time.Hour).UnixMilli()},
				uploader.StateKey: map[string]int64{"linked_at": linked.UnixMilli()},
			})
			if a.state, _, _ = state.Open(dirs.StateFile()); a.state == nil {
				t.Fatal("state.json didn't open")
			}
			a.startModes()
			if got := a.seen(); got.Earlier || !got.OS.HasMono() || !got.Until.IsZero() || got.Ended.Before(linked.Add(time.Minute)) {
				t.Errorf("the start saw %+v, want the stretch ended at the start, by both clocks, with no wall-clock bound", got)
			}
		}},
		{"a switch back the config watch's reload notes, with a link made with it, lets the writer's Online see the link at once", func(t *testing.T) {
			a, dirs := modesAgent(t, "local")
			if a.state, _, _ = state.Open(dirs.StateFile()); a.state == nil {
				t.Fatal("state.json didn't open")
			}
			t.Cleanup(a.startLedger())
			opened(t, a)
			writeAccount(t, dirs, time.Now().UTC().Truncate(time.Second))
			setMode(t, dirs, "online")
			a.noteMode()
			if !a.writerOnline() {
				t.Error("the writer's Online said local-only after the reload noted the switch back with its link")
			}
		}},
		{"a switch back the config watch's first look notes, with a link made with it, lets the writer's Online see the link at once", func(t *testing.T) {
			a, dirs := modesAgent(t, "local")
			if a.state, _, _ = state.Open(dirs.StateFile()); a.state == nil {
				t.Fatal("state.json didn't open")
			}
			t.Cleanup(a.startLedger())
			opened(t, a)
			writeAccount(t, dirs, time.Now().UTC().Truncate(time.Second))
			setMode(t, dirs, "online")
			a.noteBack()
			if !a.writerOnline() {
				t.Error("the writer's Online said local-only after the first look noted the switch back with its link")
			}
		}},
		{"a seal's online read that modes hadn't seen, where config.toml says local-only again by the time modes looks, has the writer begin the stretch again", func(t *testing.T) {
			a, dirs := modesAgent(t, "local")
			if a.state, _, _ = state.Open(dirs.StateFile()); a.state == nil {
				t.Fatal("state.json didn't open")
			}
			t.Cleanup(a.startLedger())
			w := opened(t, a)
			// No config watch runs here to hear the switches.
			setMode(t, dirs, "online")
			looked := func() {
				t.Helper()
				for deadline := time.Now().Add(10 * time.Second); a.seeing.Load(); time.Sleep(5 * time.Millisecond) {
					if time.Now().After(deadline) {
						t.Fatal("modes never looked")
					}
				}
			}
			func() {
				// modes looks only once the file says local-only again: the
				// seal's seeSoon waits for its lock.
				a.modes.mu.Lock()
				defer a.modes.mu.Unlock()
				// The stretch's first record asks for a seal of its own, which
				// reads config.toml online, and no other seal is due.
				if _, err := w.Write(runPresenceAt(time.Now())); err != nil {
					t.Fatal(err)
				}
				for deadline := time.Now().Add(10 * time.Second); w.Status().Batches < 1; time.Sleep(5 * time.Millisecond) {
					if time.Now().After(deadline) {
						t.Fatal("the stretch's first record was never sealed")
					}
				}
				setMode(t, dirs, "local")
			}()
			looked()
			func() {
				// No seeSoon reaches the writer while the next seal reads.
				a.modes.mu.Lock()
				defer a.modes.mu.Unlock()
				if _, err := w.Write(runPresenceAt(time.Now())); err != nil {
					t.Fatal(err)
				}
				setMode(t, dirs, "online")
				if row := sealNow(t, w, dirs.Ledger()); row.Online {
					t.Error("a record written after config.toml went back to local-only was sealed online")
				}
			}()
			// The seal's seeSoon keeps state.json, which the test's cleanup
			// deletes.
			looked()
		}},
		{"a link the uploader had settled whose second isn't after the end state.json kept, as after a step back of the wall clock, ends the stretch where the agent finds it, by both clocks", func(t *testing.T) {
			a, dirs := modesAgent(t, "online")
			linked := time.Now().Add(-time.Hour).UTC().Truncate(time.Second)
			writeAccount(t, dirs, linked)
			keepState(t, dirs, map[string]any{
				modesKey:          modesState{Ended: linked.Add(30 * time.Minute), Linked: linked.Add(-2 * time.Hour).UnixMilli()},
				uploader.StateKey: map[string]int64{"linked_at": linked.UnixMilli()},
			})
			if a.state, _, _ = state.Open(dirs.StateFile()); a.state == nil {
				t.Fatal("state.json didn't open")
			}
			a.startModes()
			if got := a.seen(); got.Earlier || !got.OS.HasMono() || !got.Until.Equal(linked.Add(time.Second)) {
				t.Errorf("the start saw %+v, want the stretch ended now, by both clocks, and by the wall clock to the end of the link's second", got)
			}
		}},
		{"a login that lands between Open's Seen and the writer's publish is taken in as it publishes, and the writer's Online sees it at once", func(t *testing.T) {
			a, dirs := modesAgent(t, "online")
			first := time.Now().Add(-time.Hour).UTC().Truncate(time.Second)
			writeAccount(t, dirs, first)
			keepState(t, dirs, map[string]any{modesKey: modesState{Ended: first.Add(time.Minute), Linked: first.UnixMilli()}})
			if a.state, _, _ = state.Open(dirs.StateFile()); a.state == nil {
				t.Fatal("state.json didn't open")
			}
			a.startModes()
			a.seen()
			relinked := time.Now().UTC().Truncate(time.Second)
			writeAccount(t, dirs, relinked)
			a.publishWriter(testWriter(t, dirs))
			if !a.writerOnline() {
				t.Error("the writer's Online said local-only after the publish took the login in")
			}
		}},
		{"an account.json that can't be read may name a link modes didn't account for, so the writer's Online says local-only", func(t *testing.T) {
			a, dirs := modesAgent(t, "online")
			if err := home.WriteFile(filepath.Join(dirs.Data, "account.json"), []byte("{not json")); err != nil {
				t.Fatal(err)
			}
			if a.writerOnline() {
				t.Error("the writer's Online said online with an account.json that can't be read")
			}
		}},
		{"the writer's Online sees the link modes accounted for from the Seen Open takes, so Open's seal of what a crash left reads config.toml as it is", func(t *testing.T) {
			a, dirs := modesAgent(t, "online")
			linked := time.Now().Add(-time.Hour).UTC().Truncate(time.Second)
			writeAccount(t, dirs, linked)
			keepState(t, dirs, map[string]any{modesKey: modesState{Ended: linked.Add(time.Minute), Linked: linked.UnixMilli()}})
			if a.state, _, _ = state.Open(dirs.StateFile()); a.state == nil {
				t.Fatal("state.json didn't open")
			}
			a.startModes()
			a.seen()
			if !a.writerOnline() {
				t.Error("the writer's Online said local-only after the Seen Open takes, for a link modes accounted for")
			}
		}},
		{"a link modes takes in leaves the writer's Online local-only until the writer has the stretch's end, and then it sees the link", func(t *testing.T) {
			a, dirs := modesAgent(t, "online")
			first := time.Now().Add(-time.Hour).UTC().Truncate(time.Second)
			writeAccount(t, dirs, first)
			keepState(t, dirs, map[string]any{modesKey: modesState{Ended: first.Add(time.Minute), Linked: first.UnixMilli()}})
			if a.state, _, _ = state.Open(dirs.StateFile()); a.state == nil {
				t.Fatal("state.json didn't open")
			}
			t.Cleanup(a.startLedger())
			opened(t, a)
			relinked := time.Now().UTC().Truncate(time.Second)
			writeAccount(t, dirs, relinked)
			// As seeSoon's goroutine takes the link in, under modes' lock,
			// before it gives the writer the stretch's end.
			a.modes.mu.Lock()
			took := a.unseenLinkLocked()
			early := a.writerOnline()
			a.modes.mu.Unlock()
			for deadline := time.Now().Add(10 * time.Second); a.linkedSeen.Load() != relinked.UnixMilli() || a.seeing.Load(); time.Sleep(5 * time.Millisecond) {
				if time.Now().After(deadline) {
					t.Fatal("the writer's Online never saw the link")
				}
			}
			if !took || early || !a.writerOnline() {
				t.Errorf("the link was taken in %v, and the writer's Online said online %v before the writer had the stretch's end, and %v after; want true, false, true", took, early, a.writerOnline())
			}
		}},
		{"held calls an earlier run left count as on their way from the ledger's start until the adapters have counted them", func(t *testing.T) {
			a, _ := modesAgent(t, "online")
			leftHeld(t, a.adaptersHost())
			t.Cleanup(a.startLedger())
			anything := func(event.Envelope) bool { return true }
			uncounted := a.waiting(anything)
			a.adaptersHost().countHeld()
			if !uncounted || a.waiting(anything) {
				t.Errorf("before the count, waiting %v; after it, %v; want true, false", uncounted, a.waiting(anything))
			}
		}},
		{"the end of a stretch asks for a drain of the inbox's spool once a call made just before it can have reached the spool, and within seconds, and a note that repeats online doesn't", func(t *testing.T) {
			a, _ := modesAgent(t, "online")
			ctx, cancel := context.WithCancel(context.Background())
			t.Cleanup(cancel)
			go a.sched.Run(ctx)
			kick := make(chan struct{}, 1)
			a.spoolKick.Store(&kick)
			see := func(online bool) {
				a.modes.mu.Lock()
				a.seeLocked(online)
				a.modes.mu.Unlock()
			}
			kickedWithin := func(d time.Duration) bool {
				select {
				case <-kick:
					return true
				case <-time.After(d):
					return false
				}
			}
			see(false)
			see(true)
			// Not before a call made just before the switch can have reached
			// the spool, which takes up to spoolSlack; the scheduler runs a
			// job at most 50 ms early.
			early := kickedWithin(spoolSlack + 100*time.Millisecond)
			ended := kickedWithin(2 * time.Second)
			see(true)
			repeated := kickedWithin(drainSoonAfter + 2*time.Second)
			if early || !ended || repeated {
				t.Errorf("a drain asked for within spoolSlack of the stretch's end: %v, soon after: %v, at a note that repeats online: %v; want false, true, false",
					early, ended, repeated)
			}
		}},
		{"a stretch the last run saw going on, which state.json kept, lasts until the next start, so an event made before it is sealed local-only", func(t *testing.T) {
			a, dirs := modesAgent(t, "local")
			st, _, err := state.Open(dirs.StateFile())
			if err != nil {
				t.Fatal(err)
			}
			a.state = st
			a.startModes() // and the run ends before its ledger opens
			setMode(t, dirs, "online")
			next, _ := modesAgent(t, "online")
			next.dirs = dirs
			if next.state, _, err = state.Open(dirs.StateFile()); err != nil {
				t.Fatal(err)
			}
			made := time.Now()
			t.Cleanup(next.startLedger())
			w := opened(t, next)
			before := sealOne(t, next, w, made.Add(-time.Second))
			after := sealOne(t, next, w, time.Now().Add(time.Second))
			if before.Online || !after.Online || !after.Back {
				t.Errorf("the event made before the start sealed online %v, and the next online %v, marked %v; want local-only, then online and marked",
					before.Online, after.Online, after.Back)
			}
		}},
		{"the config watch's notes go to state.json as they come, at its reload and at its first look, so a run killed after either leaves them to the next start", func(t *testing.T) {
			a, dirs := modesAgent(t, "online")
			st, _, err := state.Open(dirs.StateFile())
			if err != nil {
				t.Fatal(err)
			}
			a.state = st
			a.startModes()
			setMode(t, dirs, "local")
			a.noteMode()
			reloaded := kept(t, dirs)
			// pairtime login writes account.json, then config.toml.
			linked := time.Now().UTC().Truncate(time.Second)
			writeAccount(t, dirs, linked)
			setMode(t, dirs, "online")
			a.noteBack()
			looked := kept(t, dirs)
			if !reloaded.Local || looked.Local || looked.Ended.IsZero() || looked.OSMono <= 0 || looked.OSBoot == "" || looked.Linked != linked.UnixMilli() {
				t.Errorf("state.json said %+v after the reload of the switch to local-only, and %+v after the first look at the switch back; want local-only, then online with the stretch's end by both clocks and the link seen",
					reloaded, looked)
			}
			// The next start reads it back, as an earlier run's.
			next, _ := modesAgent(t, "online")
			next.dirs = dirs
			if next.state, _, err = state.Open(dirs.StateFile()); err != nil {
				t.Fatal(err)
			}
			next.startModes()
			want := ledger.Seen{Ended: looked.Ended, OS: clock.Stamp{Mono: looked.OSMono, BootID: looked.OSBoot}, Earlier: true}
			if got := next.seen(); got.Local || !got.Ended.Equal(want.Ended) || got.OS != want.OS || !got.Earlier {
				t.Errorf("the next start saw %+v, want %+v", got, want)
			}
			// A logout and a login the reload notes, with no first look.
			setMode(t, dirs, "local")
			a.noteMode()
			relinked := linked.Add(time.Minute)
			writeAccount(t, dirs, relinked)
			setMode(t, dirs, "online")
			a.noteMode()
			if ms := kept(t, dirs); ms.Linked != relinked.UnixMilli() {
				t.Errorf("state.json keeps %+v after the reload of a switch back with a new link, want that link seen", ms)
			}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, c.check)
	}
}

// TestSpoolHolds checks the inbox spool's part of the ledger writer's
// Waiting (agent.spoolHolds): a hook call there that no drain has handed
// on holds the first batch after a switch back when the times its records
// will carry (hookpath.Times), or the hook's own stamp, are before the
// moment asked about, and only while a drain runs in this run.
func TestSpoolHolds(t *testing.T) {
	now := clock.Now()
	// The calls are of a boot of their own, whose monotonic clock reads
	// monoNow now. The moment asked about is a minute ago by both clocks,
	// and a call is held to it as the writer holds one: by its mono in the
	// moment's boot, and by its t otherwise (ledger moment.before).
	const boot, monoNow = "b_spool", int64(1_060_000)
	moment, bound := now.Wall.Add(-time.Minute).UnixMilli(), monoNow-60_000
	before := func(e event.Envelope) bool {
		if e.BootID == boot && e.Mono > 0 {
			return e.Mono <= bound
		}
		return e.T <= moment
	}
	type call struct {
		// ago is how long before now the hook stamped it, by both clocks
		// unless ahead puts its wall time that far after now, as a step
		// back of the wall clock since leaves it; noMono leaves out the
		// monotonic reading.
		ago, ahead time.Duration
		noMono     bool
	}
	early, late := call{ago: 2 * time.Minute}, call{ago: time.Second}
	cases := []struct {
		name         string
		spool, taken []call
		// noDrain has no drain run in this run, as when the journal
		// couldn't open. Without it a drain is to come, as before
		// startIPC's first.
		noDrain bool
		want    bool
	}{
		{"a call in the spool made before the moment holds the mark", []call{early}, nil, false, true},
		{"a call in the spool made after the moment holds nothing", []call{late}, nil, false, false},
		{"a call made before the moment behind one made after it holds the mark", []call{late, early}, nil, false, true},
		{"a call whose wall time is ahead of the drain's, so that its records will carry the drain's stamp, holds the mark by the hook's own monotonic reading",
			[]call{{ago: 2 * time.Minute, ahead: time.Hour}}, nil, false, true},
		{"a call without the hook's monotonic reading, whose records its drain will time, holds the mark by the hook's own wall time",
			[]call{{ago: 2 * time.Minute, noMono: true}}, nil, false, true},
		{"a call without the hook's monotonic reading made after the moment holds nothing", []call{{ago: time.Second, noMono: true}}, nil, false, false},
		{"a call made before the moment in a file a drain took and hasn't finished holds the mark", nil, []call{early}, false, true},
		{"calls past as many as a file's scan reads may hold one made before the moment, so they hold the mark, for spoolBusyHold at most, as a file the scan can't read to its end does",
			append(slices.Repeat([]call{late}, spoolScanned), late), nil, false, true},
		{"as many calls made after the moment as a file's scan reads hold nothing",
			slices.Repeat([]call{late}, spoolScanned), nil, false, false},
		{"with no drain running in this run, a call made before the moment waits for the next start, and holds nothing", []call{early}, nil, true, false},
		{"an empty inbox holds nothing", nil, nil, false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a, dirs := modesAgent(t, "online")
			a.spoolNoDrain.Store(c.noDrain)
			message := func(cl call) inbox.Message {
				sent := now.Wall.Add(-cl.ago)
				if cl.ahead != 0 {
					sent = now.Wall.Add(cl.ahead)
				}
				m := hookMessage(t, "UserPromptSubmit", `{"session_id":"s","hook_event_name":"UserPromptSubmit"}`, inbox.Proc{PID: 4242, Name: "claude"}, sent)
				if !cl.noMono {
					m.Mono, m.BootID = monoNow-cl.ago.Milliseconds(), boot
				}
				return m
			}
			for _, sc := range c.spool {
				if err := inbox.Append(dirs.Inbox(), message(sc)); err != nil {
					t.Fatal(err)
				}
			}
			if len(c.taken) > 0 {
				var lines []byte
				for _, tc := range c.taken {
					b, err := message(tc).Encode()
					if err != nil {
						t.Fatal(err)
					}
					lines = append(append(lines, b...), '\n')
				}
				if err := os.MkdirAll(dirs.Inbox(), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dirs.Inbox(), "spool-00000000000000000001-0badc0de.drain"), lines, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if got := a.waiting(before); got != c.want {
				t.Errorf("Waiting = %v, want %v", got, c.want)
			}
		})
	}
}

// TestAgentWaiting checks the agent's other parts of the ledger writer's
// Waiting (agent.waiting): presence's queue, the git watcher's held
// changes, and the drain that lets the spool hold the mark.
func TestAgentWaiting(t *testing.T) {
	cases := []struct {
		name  string
		check func(t *testing.T)
	}{
		{"a record of presence's queued before the moment holds the mark", func(t *testing.T) {
			a, _ := modesAgent(t, "online")
			a.ledgerIn = ledger.NewPresence(nil, nil)
			t.Cleanup(a.ledgerIn.Close)
			made := time.Now()
			a.ledgerIn.Record(presence.Record{At: made, Data: &event.OSPresence{IdleAnyS: 1}})
			cut := made.Add(time.Second).UnixMilli()
			if !a.waiting(func(e event.Envelope) bool { return e.T <= cut }) {
				t.Error("a record of presence's queued before the moment held nothing")
			}
		}},
		{"a git change the watcher holds for its actor holds the mark, asked about by this run's clock from when its scan began", func(t *testing.T) {
			if _, err := exec.LookPath("git"); err != nil {
				t.Skip("git isn't installed")
			}
			a, dirs := modesAgent(t, "online")
			repo := filepath.Join(dirs.Data, "repo")
			if err := os.MkdirAll(repo, 0o700); err != nil {
				t.Fatal(err)
			}
			runGit(t, repo, "init", "-q")
			if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("a\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			runGit(t, repo, "add", "a.txt")
			runGit(t, repo, "commit", "-q", "-m", "one")
			w := testWriter(t, dirs)
			g, err := newGitHost(a, bytes.Repeat([]byte{8}, 32), w)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(g.close)
			a.adaptersHost().git.Store(g)
			loc, err := g.w.Track(context.Background(), repo)
			if err != nil {
				t.Fatal(err)
			}
			// The first scan only notes where the reflog ends: it must have
			// set its cursor before the commit, which it would take as its
			// start otherwise.
			for deadline := time.Now().Add(10 * time.Second); !g.w.State().Worktrees[loc.GitDir].Cursor.Set; time.Sleep(5 * time.Millisecond) {
				if time.Now().After(deadline) {
					t.Fatal("the worktree's first scan never set its cursor")
				}
			}
			// The moment is a minute on, by this run's clock: a change of the
			// repo's is before it only when asked about by that clock.
			mono, boot := ledger.RunClock(time.Now().Add(time.Minute))
			before := func(e event.Envelope) bool { return e.BootID == boot && e.Mono > 0 && e.Mono <= mono }
			if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("b\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			runGit(t, repo, "commit", "-q", "-am", "two")
			g.w.Poll()
			for deadline := time.Now().Add(10 * time.Second); !a.waiting(before); time.Sleep(5 * time.Millisecond) {
				if time.Now().After(deadline) {
					t.Fatal("the git change never held the mark")
				}
			}
			// Well inside the watcher's hold for the change's actor
			// (git.DefaultHold), with its scan done: a moment taken now is
			// after the scan began, by this run's clock, and a reading taken
			// as Waiting asks is not before it.
			time.Sleep(300 * time.Millisecond)
			mono, boot = ledger.RunClock(time.Now())
			if !a.waiting(before) {
				t.Error("the held change wasn't asked about by this run's clock from when its scan began")
			}
		}},
		{"the drain startIPC runs takes drainSoon's kick while it runs, and none after its stop", func(t *testing.T) {
			a, _ := modesAgent(t, "online")
			a.hooked = newHookTies()
			stop := a.startIPC(Options{DrainEvery: time.Hour})
			running := a.spoolKick.Load() != nil
			stop()
			if !running || a.spoolKick.Load() != nil || a.spoolNoDrain.Load() {
				t.Errorf("a drain to kick while startIPC ran: %v, after its stop: %v, and no drain at all: %v; want true, false, false",
					running, a.spoolKick.Load() != nil, a.spoolNoDrain.Load())
			}
		}},
		{"a journal that can't open leaves no drain to wait for in the run, so the spool holds nothing", func(t *testing.T) {
			a, dirs := modesAgent(t, "online")
			a.hooked = newHookTies()
			// A file where the journal's folder goes.
			if err := os.WriteFile(a.journalDir(), nil, 0o600); err != nil {
				t.Fatal(err)
			}
			stop := a.startIPC(Options{DrainEvery: time.Hour})
			t.Cleanup(stop)
			m := hookMessage(t, "UserPromptSubmit", `{"session_id":"s","hook_event_name":"UserPromptSubmit"}`, inbox.Proc{PID: 4242, Name: "claude"}, time.Now().Add(-time.Hour))
			if err := inbox.Append(dirs.Inbox(), m); err != nil {
				t.Fatal(err)
			}
			if !a.spoolNoDrain.Load() || a.spoolHolds(func(event.Envelope) bool { return true }) {
				t.Errorf("no drain in the run: %v, the spool holding the mark: %v; want true, false", a.spoolNoDrain.Load(), a.spoolHolds(func(event.Envelope) bool { return true }))
			}
		}},
		{"a stretch end modes sees, at a switch back or a link it didn't see, starts a busy spool file's count again", func(t *testing.T) {
			ends := []struct {
				name string
				end  func(t *testing.T, a *agent, dirs home.Dirs)
			}{
				{"a switch back modes sees starts the count again", func(t *testing.T, a *agent, dirs home.Dirs) {
					a.seeLocked(false)
					a.seeLocked(true)
				}},
				{"a link modes didn't account for starts it again", func(t *testing.T, a *agent, dirs home.Dirs) {
					writeAccount(t, dirs, time.Now().UTC().Truncate(time.Second))
					a.unseenLinkLocked()
				}},
			}
			for _, e := range ends {
				t.Run(e.name, func(t *testing.T) {
					a, dirs := modesAgent(t, "online")
					past := time.Now().Add(-time.Hour)
					a.spoolBusySince.Store(&past)
					a.modes.mu.Lock()
					e.end(t, a, dirs)
					a.modes.mu.Unlock()
					if a.spoolBusySince.Load() != nil {
						t.Error("a busy file's count still ran from an earlier stretch's end")
					}
				})
			}
		}},
		{"a spool file that stays busy holds the mark for spoolBusyHold from the first ask that finds it so and no longer, and a batch sealed online or a read that works starts the count again", func(t *testing.T) {
			a, _ := modesAgent(t, "online")
			// What each Scan gives: busy, busy past the bound twice, busy at a
			// later switch back, read, busy.
			script := []error{errBusy, errBusy, errBusy, errBusy, errBusy, nil, errBusy}
			saved := scanSpool
			t.Cleanup(func() { scanSpool = saved })
			scanSpool = func(string, int, func(inbox.Message) bool) (bool, error) {
				err := script[0]
				script = script[1:]
				return false, err
			}
			anything := func(event.Envelope) bool { return true }
			var got []bool
			ask := func() { got = append(got, a.spoolHolds(anything)) }
			ask()
			since := a.spoolBusySince.Load()
			if since == nil {
				t.Fatal("the first ask that found the file busy didn't record when")
			}
			// Past a minute's drain and drainSoon's, the drains after it may
			// still take the file.
			drained := since.Add(-drainEvery - drainSoonAfter - time.Second)
			a.spoolBusySince.Store(&drained)
			ask()
			// The file has stayed busy past the bound since.
			past := since.Add(-spoolBusyHold - time.Second)
			a.spoolBusySince.Store(&past)
			ask()
			ask()
			// The mark went with a batch sealed online.
			a.sealed(ledger.Batch{Online: true})
			ask()
			ask()
			ask()
			if want := []bool{true, true, false, false, true, false, true}; !slices.Equal(got, want) {
				t.Errorf("the spool held the mark %v, want %v", got, want)
			}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, c.check)
	}
}

// TestGitSinkClock checks the mono of a git.change record (gitHost.sink):
// a change its scan found carries this run's clock from when the scan
// began, so a step of the wall clock while the watcher held it, which
// moves its t, doesn't move its mono.
func TestGitSinkClock(t *testing.T) {
	cases := []struct {
		name string
		// by is how much later than the scan's start its event's t is.
		by time.Duration
	}{
		{"a change whose t a step of the wall clock put before its scan carries the scan's reading of this run's clock", -30 * time.Second},
		{"a change whose t a step put after its scan carries the scan's reading too", 30 * time.Second},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a, dirs := modesAgent(t, "online")
			w := testWriter(t, dirs)
			g := &gitHost{a: a, writer: w, commits: &commitStore{dir: dirs.Commits()}}
			// An hour on, with Go's monotonic reading, so this run's clock reads
			// far from now, which the sink's own reading is.
			found := time.Now().Add(time.Hour)
			ev := event.Event{Envelope: event.Envelope{T: found.Add(c.by).UnixMilli(), Source: event.SourceGit,
				Harness: event.HarnessNone, SessionKey: event.NoSession},
				Data: &event.GitChange{Op: event.GitCommit, SHA: "1a2b3c4d5e6f7a8b9c0d1e2f3a4b5c6d7e8f9a0b", FilesChanged: 1, Insertions: 1, Actor: event.ActorHuman}}
			g.sink(git.Change{Event: ev, Found: found})
			wantMono, wantBoot := ledger.RunClock(found)
			var got []event.Envelope
			if err := ledger.ReadBatches(dirs.Ledger(), 1, func(b ledger.Batch) error {
				for _, rec := range b.Records {
					body, err := pkgledger.ParseBody(rec.Body)
					if err != nil {
						return err
					}
					if e, err := body.Event(); err == nil && e.Kind() == event.KindGitChange {
						got = append(got, e.Envelope)
					}
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if len(got) != 1 || got[0].Mono != wantMono || got[0].BootID != wantBoot {
				t.Errorf("git.change records %+v, want one with mono %d of boot %s", got, wantMono, wantBoot)
			}
		})
	}
}

// errBusy is a spool file's busy open, as Scan gives it.
var errBusy = errors.New("the file is busy")

// modesAgent is an agent in a new data directory whose config.toml says
// mode = mode, with a scheduler that doesn't run.
func modesAgent(t *testing.T, mode string) (*agent, home.Dirs) {
	t.Helper()
	dir := t.TempDir()
	dirs := home.Dirs{Data: dir, Config: dir, Runtime: dir}
	setMode(t, dirs, mode)
	return &agent{dirs: dirs, log: slog.New(slog.DiscardHandler), problems: map[string]string{}, table: newestTable(),
		keyStores: []keys.Store{}, sched: sched.New()}, dirs
}

// testWriter is a ledger writer on dirs' ledger with test keys, closed
// when the test ends.
func testWriter(t *testing.T, dirs home.Dirs) *ledger.Writer { return optionsWriter(t, dirs, nil) }

// optionsWriter is testWriter with the options edit sets, when it isn't
// nil.
func optionsWriter(t *testing.T, dirs home.Dirs, edit func(*ledger.Options)) *ledger.Writer {
	t.Helper()
	lk, err := pkgledger.DeriveKeys(bytes.Repeat([]byte{9}, 32))
	if err != nil {
		t.Fatal(err)
	}
	opts := ledger.Options{Dir: dirs.Ledger(), Keys: lk, KeyID: "0123456789abcdef"}
	if edit != nil {
		edit(&opts)
	}
	w, err := ledger.Open(opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { w.Close() })
	return w
}

// setMode writes config.toml with mode = mode, as pairtime login and
// logout do, by a rename.
func setMode(t *testing.T, dirs home.Dirs, mode string) {
	t.Helper()
	if err := home.WriteFile(dirs.ConfigFile(), []byte("mode = \""+mode+"\"\n")); err != nil {
		t.Fatal(err)
	}
}

// presenceAt is an os.presence event made at at, of another run's clock,
// so the writer holds it to the moment by its t.
func presenceAt(at time.Time) ledger.Input {
	return ledger.Input{Event: event.Event{
		Envelope: event.Envelope{EventID: event.NewEventID(at), T: at.UnixMilli(), Mono: 1000, BootID: "b_test",
			Source: event.SourceOS, Via: event.ViaOS, Harness: event.HarnessNone, SessionKey: event.NoSession},
		Data: &event.OSPresence{IdleAnyS: 1},
	}}
}

// dropRowStretches rewrites the rows of the ledger's batches.seg in dir
// without the local-only stretches they record, as an older pairtime
// version's rows, which recorded none. The segments keep their seal frames
// as they are: Open rebuilds no row here, so it reads none of theirs. A
// frame is u8(kind) || u32(len) || payload || u32(CRC-32C), big-endian,
// and a row's kind is 'b'.
func dropRowStretches(t *testing.T, dir string) {
	t.Helper()
	name := filepath.Join(dir, "batches.seg")
	data, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	table := crc32.MakeTable(crc32.Castagnoli)
	var out []byte
	for len(data) > 0 {
		if len(data) < 9 {
			t.Fatalf("batches.seg ends in %d bytes that aren't a frame", len(data))
		}
		kind, n := data[0], int(binary.BigEndian.Uint32(data[1:5]))
		if len(data) < 9+n {
			t.Fatalf("batches.seg has a frame of %d bytes with %d left", n, len(data)-9)
		}
		payload := data[5 : 5+n]
		data = data[9+n:]
		if kind == 'b' {
			var row map[string]json.RawMessage
			if err := json.Unmarshal(payload, &row); err != nil {
				t.Fatal(err)
			}
			delete(row, "stretches")
			if payload, err = json.Marshal(row); err != nil {
				t.Fatal(err)
			}
		}
		frame := binary.BigEndian.AppendUint32([]byte{kind}, uint32(len(payload)))
		frame = append(frame, payload...)
		out = append(out, binary.BigEndian.AppendUint32(frame, crc32.Checksum(frame, table))...)
	}
	if err := os.WriteFile(name, out, 0o600); err != nil {
		t.Fatal(err)
	}
}

// leftHeld leaves a line in h's held.jsonl, as an earlier run holds a
// call, which no key reads.
func leftHeld(t *testing.T, h *adapterHost) {
	t.Helper()
	if err := os.WriteFile(h.a.dirs.Held(), []byte("{\"a held call\":1}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

// runPresenceAt is an os.presence event made at at, of this run's clock,
// as presence's records are.
func runPresenceAt(at time.Time) ledger.Input {
	in := presenceAt(at)
	in.Event.Mono, in.Event.BootID = ledger.RunClock(at)
	return in
}

// writeAccount writes account.json as pairtime login does, linked at
// linked.
func writeAccount(t *testing.T, dirs home.Dirs, linked time.Time) {
	t.Helper()
	b := []byte(`{"v":1,"server":"https://pairtime.example","device_id":"d_testdevice00000000000000a","linked_at":"` + linked.Format(time.RFC3339) + `"}`)
	if err := home.WriteFile(filepath.Join(dirs.Data, "account.json"), b); err != nil {
		t.Fatal(err)
	}
}

// keepState puts keys in state.json, as an earlier run left it.
func keepState(t *testing.T, dirs home.Dirs, keys map[string]any) {
	t.Helper()
	st, _, err := state.Open(dirs.StateFile())
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range keys {
		if err := st.Put(k, v); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}
}

// sealNow seals what w, the writer of the ledger in dir, has pending, and
// returns the newest batch's row, which a seal the writer still owed may
// have sealed first.
func sealNow(t *testing.T, w *ledger.Writer, dir string) ledger.Row {
	t.Helper()
	if _, _, err := w.Seal(); err != nil {
		t.Fatal(err)
	}
	_, rows, err := ledger.ReadRows(dir, w.Status().Batches)
	if err != nil || len(rows) != 1 {
		t.Fatalf("the newest row doesn't read (%v): %+v", err, rows)
	}
	return rows[0]
}

// TestLogRecordsByTheRunClock checks how takeLine dates a transcript line
// for the ledger, which holds a log record to the moment config.toml was
// last known to say local-only (#174): by this run's clock, which a step of
// the wall clock doesn't move, for a line read within LATE_READ_S of its
// time, and by its t for one read later, as for a line from before the run.
// The run's clock leaves out a suspend, so a line made after a switch back
// online and read after one is sealed online, and one made before the
// switch is sealed local-only however late it is read. A restart over an
// old stretch's end seals a line written while no agent ran online.
func TestLogRecordsByTheRunClock(t *testing.T) {
	// shift is how far ahead the writer's clock runs, so this run's clock
	// reads more than a suspend at its moment, as an agent's that has run
	// for hours does, and later moves it on by both clocks (stretch).
	// later is atomic because the ledger writer's goroutine reads it,
	// through ahead.
	const shift = 2 * time.Hour
	var later atomic.Int64
	ahead := func() time.Time { return time.Now().Add(shift + time.Duration(later.Load())) } // with its monotonic reading
	online := func(o *ledger.Options) {
		o.Online = func() bool { return true }
		o.Now = ahead
	}
	// host is an adapter host with the Claude Code adapter, writing to w as
	// the worker's does.
	host := func(dirs home.Dirs, w *ledger.Writer) *adapterHost {
		a := &agent{dirs: dirs, log: slog.New(slog.DiscardHandler), problems: map[string]string{}, hooked: newHookTies()}
		secret := bytes.Repeat([]byte{7}, hookpath.SessionSecretLen)
		a.kSession.Store(&secret)
		h := newAdapterHost(a)
		h.keys = &adapterKeys{}
		h.attach(w)
		return h
	}
	// take has h take a transcript prompt made at made, read at wall by the
	// wall clock and at run by this run's (logRead).
	take := func(t *testing.T, h *adapterHost, made, wall, run time.Time) {
		t.Helper()
		saved := logRead
		logRead = func() (time.Time, time.Time) { return wall, run }
		defer func() { logRead = saved }()
		b, err := json.Marshal(map[string]any{"type": "user", "sessionId": "66666666-0000-4000-8000-000000000006",
			"promptId": "ffffffff-0000-4000-8000-000000000061", "timestamp": made.UTC().Format(time.RFC3339Nano), "cwd": "/home/teen/p",
			"version": "2.1.327", "entrypoint": "cli", "isSidechain": false, "promptSource": "typed", "turnOrigin": "human",
			"message": map[string]any{"role": "user", "content": "What does this do?"}})
		if err != nil {
			t.Fatal(err)
		}
		file := "/home/teen/.claude/projects/-p/s.jsonl"
		line := tailers.Line{Harness: event.HarnessClaudeCode, Bytes: b, Log: adapters.LogLine{File: file, Path: file, End: int64(len(b) + 1)}}
		if !h.takeLine(tailers.New(tailers.Options{NoNotify: true}), line) {
			t.Fatal("takeLine didn't take the line")
		}
	}
	// stretch notes a local-only stretch that ends now, by w's clock, a
	// minute after the chain began, so the lines it is about are dated
	// after that: the writer holds a log record dated before the chain began
	// that carries no reading of a run's clock, as a line read after a
	// suspend, to when it took it in (ledger.Writer.lookback).
	stretch := func(w *ledger.Writer) time.Time {
		later.Store(int64(time.Minute))
		w.NoteMode(false)
		w.NoteMode(true)
		return ahead()
	}
	cases := []struct {
		name string
		// run takes the line, and returns the mono its prompt.submit should
		// have, -1 for any.
		run func(t *testing.T, dirs home.Dirs) (*ledger.Writer, int64)
		// online is how the line's batch is sealed.
		online bool
	}{
		{"a line read promptly keeps the read's reading of this run's clock",
			func(t *testing.T, dirs home.Dirs) (*ledger.Writer, int64) {
				w := optionsWriter(t, dirs, online)
				read := ahead()
				take(t, host(dirs, w), read, read, read)
				mono, _ := ledger.RunClock(read)
				return w, mono
			}, true},
		{"a line read 11 s after it was made has no reading of this run's clock",
			func(t *testing.T, dirs home.Dirs) (*ledger.Writer, int64) {
				w := optionsWriter(t, dirs, online)
				read := ahead()
				take(t, host(dirs, w), read.Add(-11*time.Second), read, read)
				return w, 0
			}, true},
		{"a line made 3 s before a stretch's end and read 2 s after it is sealed local-only, with the read's reading less 5 s",
			func(t *testing.T, dirs home.Dirs) (*ledger.Writer, int64) {
				w := optionsWriter(t, dirs, online)
				end := stretch(w)
				read := end.Add(2 * time.Second) // by both clocks
				take(t, host(dirs, w), end.Add(-3*time.Second), read, read)
				mono, _ := ledger.RunClock(read)
				return w, mono - 5000
			}, false},
		{"a line read exactly LATE_READ_S after it was made keeps the read's reading less those 10 s",
			func(t *testing.T, dirs home.Dirs) (*ledger.Writer, int64) {
				w := optionsWriter(t, dirs, online)
				read := ahead()
				take(t, host(dirs, w), read.Add(-10*time.Second), read, read)
				mono, _ := ledger.RunClock(read)
				return w, mono - 10_000
			}, true},
		{"a line read a millisecond later than that has none",
			func(t *testing.T, dirs home.Dirs) (*ledger.Writer, int64) {
				w := optionsWriter(t, dirs, online)
				read := ahead()
				take(t, host(dirs, w), read.Add(-10_001*time.Millisecond), read, read)
				return w, 0
			}, true},
		{"a line made in a local-only stretch and read after a suspend is sealed local-only",
			func(t *testing.T, dirs home.Dirs) (*ledger.Writer, int64) {
				w := optionsWriter(t, dirs, online)
				end := stretch(w)
				read := ahead()
				take(t, host(dirs, w), end.Add(-5*time.Second), read.Add(30*time.Minute), read)
				return w, -1
			}, false},
		{"a line made after a switch back online and read after a suspend is sealed online",
			func(t *testing.T, dirs home.Dirs) (*ledger.Writer, int64) {
				w := optionsWriter(t, dirs, online)
				end := stretch(w)
				made := end.Add(time.Second)
				// Read two seconds later by this run's clock, which leaves out
				// the 30 minutes the computer slept since.
				take(t, host(dirs, w), made, made.Add(30*time.Minute), ahead().Add(2*time.Second))
				return w, 0
			}, true},
		{"a line written while no agent ran, after the stretch the newest seal frame ends, is sealed online",
			func(t *testing.T, dirs home.Dirs) (*ledger.Writer, int64) {
				// An earlier run ended a stretch two hours ago, which the seal
				// frames since record.
				end := time.Now().Add(-2 * time.Hour).Round(0)
				lk, err := pkgledger.DeriveKeys(bytes.Repeat([]byte{9}, 32))
				if err != nil {
					t.Fatal(err)
				}
				before, err := ledger.Open(ledger.Options{Dir: dirs.Ledger(), Keys: lk, KeyID: "0123456789abcdef",
					Online: func() bool { return true }, Now: func() time.Time { return end }})
				if err != nil {
					t.Fatal(err)
				}
				before.NoteMode(false)
				before.NoteMode(true)
				if _, err := before.Write(presenceAt(end)); err != nil {
					t.Fatal(err)
				}
				if _, _, err := before.Seal(); err != nil {
					t.Fatal(err)
				}
				if err := before.Close(); err != nil {
					t.Fatal(err)
				}
				// This run's writer, and a line written an hour ago, which this
				// run reads now.
				w := optionsWriter(t, dirs, func(o *ledger.Options) { o.Online = func() bool { return true } })
				read := time.Now()
				take(t, host(dirs, w), read.Add(-time.Hour), read, read)
				return w, 0
			}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			later.Store(0)
			dir := t.TempDir()
			dirs := home.Dirs{Data: dir, Config: dir, Runtime: dir}
			w, mono := tc.run(t, dirs)
			if r := sealNow(t, w, dirs.Ledger()); r.Online != tc.online {
				t.Errorf("the line's batch was sealed online %v, want %v", r.Online, tc.online)
			}
			var got []int64
			for _, rec := range readLedger(t, dirs, event.SourceLog) {
				if _, ok := rec.ev.Data.(*event.PromptSubmit); ok {
					got = append(got, rec.ev.Mono)
				}
			}
			if len(got) != 1 || mono >= 0 && got[0] != mono {
				t.Errorf("the prompt.submit monos are %v, want one of %d", got, mono)
			}
		})
	}
}
