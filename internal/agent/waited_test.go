// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"bytes"
	"context"
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
	"github.com/some-du6e/pairtime/internal/agent/presence"
	"github.com/some-du6e/pairtime/internal/agent/sched"
	"github.com/some-du6e/pairtime/internal/agent/state"
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
	const moment, boot = int64(1_795_100_000_000), "b_os"
	// before is the writer's test of an event against the moment: by its
	// t, or by its mono in boot, where the moment is mono 50,000.
	before := func(e event.Envelope) bool { return e.T < moment || e.BootID == boot && e.Mono > 0 && e.Mono < 50_000 }
	call := func(ms int64) inbox.Delivery { return inbox.Delivery{Received: clock.Stamp{Wall: time.UnixMilli(ms)}} }
	at := func(ms int64) callStamp { return callStamp{t: ms} }
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
		{"a call made at the moment or after it doesn't count", func(h *adapterHost) { h.queue = []inbox.Delivery{call(moment)} }, false},
		{"a call made before the moment counts while the step that took it works", func(h *adapterHost) { h.stepping = []callStamp{at(moment + 1), at(moment - 1)} }, true},
		{"a parked call made before the moment counts", func(h *adapterHost) { h.parkedAt[at(moment-1)] = 1 }, true},
		{"a parked call made after it doesn't", func(h *adapterHost) { h.parkedAt[at(moment+1)] = 2 }, false},
		{"a parked call made before the moment by its boot's monotonic clock counts, though its t is after",
			func(h *adapterHost) { h.parkedAt[callStamp{t: moment + 1, mono: 49_000, boot: boot}] = 1 }, true},
		{"a held call made before the moment counts while calls are held", func(h *adapterHost) { h.held = 1; h.heldFrom.add(at(moment - 1)) }, true},
		{"a held call made before the moment by its boot's monotonic clock counts, though its t is after",
			func(h *adapterHost) { h.held = 1; h.heldFrom.add(callStamp{t: moment + 1, mono: 49_000, boot: boot}) }, true},
		{"held calls that were all made after it don't count", func(h *adapterHost) { h.held = 3; h.heldFrom.add(at(moment + 1)) }, false},
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
		c := hostCall{d: call(moment - 1)}
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
	})
}

// TestModesSeen checks what the agent sees of config.toml's mode for the
// ledger writer (modes): the config watch's first look at a change notes a
// switch back online at once, and only that; a stretch the agent saw end
// while a locked keyring kept the ledger shut, or that ended before a
// pairtime login the agent didn't see, or that the last run saw going on,
// makes the records made before its end that reach the writer local-only,
// and the first batch after them is marked as the first after the switch
// back (ledger.Batch.Back).
func TestModesSeen(t *testing.T) {
	newAgent := func(t *testing.T, mode string) (*agent, home.Dirs) {
		t.Helper()
		dir := t.TempDir()
		dirs := home.Dirs{Data: dir, Config: dir, Runtime: dir}
		setMode(t, dirs, mode)
		return &agent{dirs: dirs, log: slog.New(slog.DiscardHandler), problems: map[string]string{}, table: newestTable(),
			keyStores: []keys.Store{}, sched: sched.New()}, dirs
	}
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

	t.Run("a switch back online the config watch hears of is noted at its first look, before the reload", func(t *testing.T) {
		a, dirs := newAgent(t, "local")
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
		if b := sealOne(t, a, w, time.Now()); !b.Online || !b.Back {
			t.Errorf("the batch after the switch back was sealed online %v, marked %v; want both", b.Online, b.Back)
		}
	})
	t.Run("a first look at config.toml that still says local-only notes nothing", func(t *testing.T) {
		a, dirs := newAgent(t, "local")
		a.startModes()
		t.Cleanup(a.watchConfig())
		if err := home.WriteFile(dirs.ConfigFile(), []byte("mode = \"local\"\n# an edit of another line\n")); err != nil {
			t.Fatal(err)
		}
		time.Sleep(500 * time.Millisecond)
		if local, ended := seenLocal(a); !local || !ended.IsZero() {
			t.Errorf("after the look, local-only %v, a stretch ended %v; want still local-only, and none ended", local, ended)
		}
	})
	t.Run("presence's records queued while a locked keyring kept the ledger shut through a switch back online are sealed local-only, and the batch after them is marked", func(t *testing.T) {
		a, dirs := newAgent(t, "local")
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
		made := time.Now()
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
		after := sealOne(t, a, w, time.Now())
		if waited.Online || waited.Back || !after.Online || !after.Back {
			t.Errorf("the queued record's batch sealed online %v, marked %v, and the next online %v, marked %v; want local-only, then online and marked",
				waited.Online, waited.Back, after.Online, after.Back)
		}
	})
	t.Run("an event made in the second a pairtime login the agent didn't see linked the computer is sealed local-only, and the batch after it is marked", func(t *testing.T) {
		a, dirs := newAgent(t, "online")
		linked := time.Now().Add(-time.Minute).UTC().Truncate(time.Second)
		if err := os.WriteFile(filepath.Join(dirs.Data, "account.json"), []byte(`{"v":1,"server":"https://pairtime.example","device_id":"d_testdevice00000000000000a","linked_at":"`+linked.Format(time.RFC3339)+`"}`), 0o600); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(a.startLedger())
		w := opened(t, a)
		before := sealOne(t, a, w, linked.Add(500*time.Millisecond))
		after := sealOne(t, a, w, linked.Add(1500*time.Millisecond))
		if before.Online || !after.Online || !after.Back {
			t.Errorf("the event in the link's second sealed online %v, and the next online %v, marked %v; want local-only, then online and marked",
				before.Online, after.Online, after.Back)
		}
	})
	t.Run("the end of a stretch asks for a drain of the inbox's spool within seconds, and a note that repeats online doesn't", func(t *testing.T) {
		a, _ := newAgent(t, "online")
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
		kicked := func() bool {
			select {
			case <-kick:
				return true
			case <-time.After(spoolSlack + 2*time.Second):
				return false
			}
		}
		see(false)
		see(true)
		ended := kicked()
		see(true)
		repeated := kicked()
		if !ended || repeated {
			t.Errorf("a drain asked for at the stretch's end: %v, at a note that repeats online: %v; want true, false", ended, repeated)
		}
	})
	t.Run("a hook call in the inbox's spool holds the first batch after a switch back until a drain hands it on, whatever its time", func(t *testing.T) {
		a, dirs := newAgent(t, "online")
		never := func(event.Envelope) bool { return false }
		if err := os.MkdirAll(dirs.Inbox(), 0o700); err != nil {
			t.Fatal(err)
		}
		spool := filepath.Join(dirs.Inbox(), inbox.SpoolName)
		taken := filepath.Join(dirs.Inbox(), "spool-1.drain")
		write := func(name, text string) {
			t.Helper()
			if err := os.WriteFile(name, []byte(text), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		write(spool, "{}\n")
		noDrain := a.waiting(never)
		kick := make(chan struct{}, 1)
		a.spoolKick.Store(&kick)
		spooled := a.waiting(never)
		write(spool, "")
		empty := a.waiting(never)
		write(taken, "{}\n")
		draining := a.waiting(never)
		if noDrain || !spooled || empty || !draining {
			t.Errorf("with no drain running: %v; a call in the spool: %v; an empty spool: %v; a file a drain took: %v; want false, true, false, true",
				noDrain, spooled, empty, draining)
		}
	})
	t.Run("a stretch the last run saw going on, which state.json kept, lasts until the next start, so an event made before it is sealed local-only", func(t *testing.T) {
		a, dirs := newAgent(t, "local")
		st, _, err := state.Open(dirs.StateFile())
		if err != nil {
			t.Fatal(err)
		}
		a.state = st
		a.startModes() // and the run ends before its ledger opens
		setMode(t, dirs, "online")
		next, _ := newAgent(t, "online")
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
	})
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
