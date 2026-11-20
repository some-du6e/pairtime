// SPDX-License-Identifier: AGPL-3.0-or-later

package ledger

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/some-du6e/pairtime/pkg/event"
	"github.com/some-du6e/pairtime/pkg/ledger"
	"github.com/some-du6e/pairtime/pkg/ledger/features"
)

// The device keys the tests write with. testKDev is the K_dev of
// ledger-v1 12.1, 0x00 to 0x1f. They are test keys, used nowhere else.
var (
	testKDev  = seqBytes(0x00)
	otherKDev = seqBytes(0x40)
)

// The keys' ids, as the keystore would give them.
const (
	testKeyID  = "00112233445566778899aabbccddeeff"
	otherKeyID = "ffeeddccbbaa99887766554433221100"
)

func seqBytes(from byte) []byte {
	b := make([]byte, ledger.KeySize)
	for i := range b {
		b[i] = from + byte(i)
	}
	return b
}

func keysOf(t testing.TB, kDev []byte) *ledger.Keys {
	t.Helper()
	k, err := ledger.DeriveKeys(kDev)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

// testT is 2026-11-19T12:00:00Z, the day of ledger-v1 12.4.
const testT = int64(1_795_089_600_000)

// clock is a settable test clock.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func newClock(ms int64) *clock { return &clock{t: time.UnixMilli(ms).UTC()} }

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) set(ms int64) {
	c.mu.Lock()
	c.t = time.UnixMilli(ms).UTC()
	c.mu.Unlock()
}

// open opens a writer on dir with the test keys and the clock, and closes
// it when the test ends, unless the test closed it.
func open(t testing.TB, dir string, c *clock, edit ...func(*Options)) *Writer {
	t.Helper()
	w, err := openErr(dir, c, edit...)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { w.Close() })
	return w
}

func openErr(dir string, c *clock, edit ...func(*Options)) (*Writer, error) {
	k, err := ledger.DeriveKeys(testKDev)
	if err != nil {
		return nil, err
	}
	opts := Options{Dir: dir, Keys: k, KeyID: testKeyID, Now: c.now}
	for _, e := range edit {
		e(&opts)
	}
	return Open(opts)
}

// abandon leaves w as a crash would: its goroutine and files stop, and
// nothing unsealed is sealed.
func abandon(w *Writer) {
	w.stopOnce.Do(func() {
		if w.job != nil {
			w.job.Stop()
		}
		close(w.stop)
	})
	<-w.done
	w.sealMu.Lock()
	defer w.sealMu.Unlock()
	w.mu.Lock()
	w.closed = true
	w.mu.Unlock()
	w.closeFiles()
}

// readAll reads every sealed batch in dir.
func readAll(t testing.TB, dir string) []Batch {
	t.Helper()
	var out []Batch
	if err := ReadBatches(dir, 1, func(b Batch) error {
		out = append(out, b)
		return nil
	}); err != nil {
		t.Fatalf("ReadBatches: %v", err)
	}
	return out
}

// verify checks the chain in dir as pkg/ledger verifies one (spec 9): no
// gap in b or s from 1, every root and head, and every body, its nonce
// under keys and its agreement with its header. It returns the batches.
func verify(t testing.TB, dir string, keys *ledger.Keys) []Batch {
	t.Helper()
	info, err := ReadChain(dir)
	if err != nil {
		t.Fatalf("ReadChain: %v", err)
	}
	batches := readAll(t, dir)
	chain := ledger.Chain{DeviceID: info.DeviceID}
	for _, b := range batches {
		chain.Batches = append(chain.Batches, ledger.BatchOf(b.Row, b.Records))
	}
	if err := chain.Verify(ledger.Options{Keys: keys}); err != nil {
		t.Fatalf("pkg/ledger refuses the chain the writer wrote: %v", err)
	}
	return batches
}

// records lists the records of batches, in order.
func records(batches []Batch) []ledger.Record {
	var out []ledger.Record
	for _, b := range batches {
		out = append(out, b.Records...)
	}
	return out
}

// bodyOf parses a record's body.
func bodyOf(t testing.TB, r ledger.Record) ledger.Body {
	t.Helper()
	b, err := ledger.ParseBody(r.Body)
	if err != nil {
		t.Fatalf("record %d: %v", r.Header.Seq(), err)
	}
	return b
}

// eventOf decodes the event a record holds.
func eventOf(t testing.TB, r ledger.Record) event.Event {
	t.Helper()
	ev, err := bodyOf(t, r).Event()
	if err != nil {
		t.Fatalf("record %d: %v", r.Header.Seq(), err)
	}
	return ev
}

// featuresOf reads a record's f.
func featuresOf(t testing.TB, r ledger.Record) (features.F, bool) {
	t.Helper()
	f, ok, err := bodyOf(t, r).Features(r.Header.RType())
	if err != nil {
		t.Fatalf("record %d: %v", r.Header.Seq(), err)
	}
	return f, ok
}

// testEventID returns a UUIDv7 for time t with bits from rng.
func testEventID(rng *rand.Rand, t int64) string {
	var b [16]byte
	for i := 0; i < 6; i++ {
		b[i] = byte(uint64(t) >> (40 - 8*i))
	}
	for i := 6; i < 16; i++ {
		b[i] = byte(rng.Uint32())
	}
	b[6] = b[6]&0x0f | 0x70
	b[8] = b[8]&0x3f | 0x80
	h := hex.EncodeToString(b[:])
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}

// env returns the envelope of a harness event of session s in project p,
// which arrived through a hook. The writer sets device_id and seq.
func env(rng *rand.Rand, t int64, s, p string) event.Envelope {
	return event.Envelope{EventID: testEventID(rng, t), T: t, Mono: rng.Int64N(1 << 40), BootID: "b_test",
		Source: event.SourceHook, Via: event.ViaHook, Harness: event.HarnessClaudeCode, HarnessVersion: "2.1.327",
		SessionKey: s, ProjectKey: p}
}

// osEnv is the envelope of an OS event, which has no project and no
// session.
func osEnv(rng *rand.Rand, t int64) event.Envelope {
	e := env(rng, t, event.NoSession, "")
	e.Source, e.Via, e.Harness, e.HarnessVersion = event.SourceOS, event.ViaOS, event.HarnessNone, ""
	return e
}

// prompts are texts a person might send, from a go-ahead to a paste.
var prompts = []string{
	"ok",
	"yes",
	"fix the login redirect loop on mobile",
	"still broken, see src/nav.js:12\r\nTypeError: x is undefined",
	"the navbar still overlaps the hero on mobile\r\n```css\n.nav { z-index: 10 }\n```\nTypeError: x is undefined",
	"Почему тест падает? 🙃 我们再试一次",
	"Traceback (most recent call last):\n  File \"app.py\", line 3, in <module>\nKeyError: 'user'",
	"",
}

// messages are final agent messages.
var messages = []string{
	"Done. The redirect now checks the session first.",
	"Here's the fix:\n```js\nconst x = 1;\n```\nRun the tests again.",
	"Which one do you want, the modal or the drawer?",
}

func words(s string) int { return len(strings.Fields(s)) }

func ptr[T any](v T) *T { return &v }

// kinds are the kinds randomInput picks from, with the records that hold
// no event: a checkpoint and an upload-failure note.
var kinds = []string{
	"session.start", "prompt.submit", "prompt.bridge", "agent.output", "agent.final", "agent.final.plugin",
	"agent.tool", "agent.stop", "shell.command", "os.presence", "os.power", "os.focus", "git.change",
	"cli.track", "checkpoint", "note",
}

// write writes one record of kind, built from rng, at time t, and returns
// its sequence number and the input it wrote, which is zero for a
// checkpoint or a note.
func write(w *Writer, rng *rand.Rand, kind string, t int64) (uint64, Input, error) {
	sess := fmt.Sprintf("s_%d", rng.IntN(3))
	proj := []string{"", "p_web", "p_api"}[rng.IntN(3)]
	var in Input
	switch kind {
	case "checkpoint":
		state := json.RawMessage(fmt.Sprintf(`{"since_progress_ms":%d,"sessions":[]}`, rng.IntN(1_000_000)))
		s, err := w.Checkpoint("p_web", uint64(t)/ledger.CheckpointMS*ledger.CheckpointMS, state)
		return s, in, err
	case "note":
		s, err := w.UploadFailure(uint64(t), []string{"dns", "connect", "tls", "http_5xx", "http_429"}[rng.IntN(5)])
		return s, in, err
	}
	in = sampleInput(rng, kind, t, sess, proj)
	s, err := w.Write(in)
	return s, in, err
}

// sampleInput builds a valid input of kind.
func sampleInput(rng *rand.Rand, kind string, t int64, sess, proj string) Input {
	e := env(rng, t, sess, proj)
	switch kind {
	case "session.start":
		return Input{Event: event.Event{Envelope: e, Data: &event.SessionStart{StartKind: event.StartStartup,
			Entrypoint: event.EntrypointCLI, ParentClass: event.ParentTerminalApp, HasTTY: true}}}
	case "prompt.submit":
		text := prompts[rng.IntN(len(prompts))]
		n := utf8.RuneCountInString(text)
		pasted := rng.IntN(n + 1)
		return Input{Event: event.Event{Envelope: e, Data: &event.PromptSubmit{TurnKey: "t_1", LenChars: n,
			PastedChars: pasted, TypedLenEst: n - pasted, OriginDeclared: event.DeclaredNone}}, Text: []byte(text)}
	case "prompt.bridge":
		e.Source, e.Via = event.SourceLog, event.ViaBridge
		n := rng.IntN(400)
		return Input{Event: event.Event{Envelope: e, Data: &event.PromptSubmit{TurnKey: "t_1", LenChars: n,
			TypedLenEst: n, OriginDeclared: event.DeclaredNone}}}
	case "agent.output":
		return Input{Event: event.Event{Envelope: e, Data: &event.AgentOutput{TurnKey: "t_1", Words: rng.IntN(300)}}}
	case "agent.final":
		text := messages[rng.IntN(len(messages))]
		return Input{Event: event.Event{Envelope: e, Data: &event.AgentOutput{TurnKey: "t_1", Words: words(text),
			IsFinal: true, AsksQuestion: strings.HasSuffix(text, "?")}}, Text: []byte(text)}
	case "agent.final.plugin":
		e.Via = event.ViaPlugin
		return Input{Event: event.Event{Envelope: e, Data: &event.AgentOutput{TurnKey: "t_1", Words: 40, IsFinal: true}}}
	case "agent.tool":
		edits := []features.Edit{{Path: "src/nav.js", OldStart: 10, OldLines: 3, NewStart: 10, NewLines: 5}}
		if rng.IntN(2) == 0 {
			edits = append(edits, features.Edit{Path: "/etc/hosts", OldStart: 0, OldLines: 0, NewStart: 1, NewLines: 2})
		}
		return Input{Event: event.Event{Envelope: e, Data: &event.AgentTool{ToolClass: event.ToolEdit, LinesAdded: 7,
			LinesRemoved: 3, TStart: t - 2000, TEnd: t}}, Edits: edits, Command: "npm  test"}
	case "agent.stop":
		reason := []event.StopReason{event.StopEndTurn, event.StopQuestion}[rng.IntN(2)]
		return Input{Event: event.Event{Envelope: e, Data: &event.AgentStop{TurnKey: "t_1", StopReason: reason}}}
	case "shell.command":
		e.Source, e.Via, e.Harness, e.HarnessVersion, e.SessionKey = event.SourceShell, event.ViaShell, event.HarnessNone, "", event.NoSession
		return Input{Event: event.Event{Envelope: e, Data: &event.ShellCommand{CmdClass: event.CmdTest, ExitCode: ptr[int64](1),
			TStart: t - 5000, TEnd: t}}, Command: "pytest -k test_login"}
	case "os.presence":
		return Input{Event: event.Event{Envelope: osEnv(rng, t), Data: &event.OSPresence{IdleAnyS: float64(rng.IntN(600)) / 4}}}
	case "os.power":
		op := []event.PowerOp{event.PowerSuspend, event.PowerResume, event.PowerLock, event.PowerUnlock}[rng.IntN(4)]
		return Input{Event: event.Event{Envelope: osEnv(rng, t), Data: &event.OSPower{Op: op}}}
	case "os.focus":
		return Input{Event: event.Event{Envelope: osEnv(rng, t), Data: &event.OSFocus{AppClass: event.AppTerminal,
			HostedSessions: []string{sess}, FocusProject: "p_web"}}}
	case "git.change":
		e.Source, e.Via, e.Harness, e.HarnessVersion, e.SessionKey = event.SourceGit, event.ViaGit, event.HarnessNone, "", event.NoSession
		return Input{Event: event.Event{Envelope: e, Data: &event.GitChange{Op: event.GitCommit,
			SHA: "1a2b3c4d5e6f7a8b9c0d1e2f3a4b5c6d7e8f9a0b", FilesChanged: 3, Insertions: 40, Deletions: 12,
			Actor: event.ActorHuman}}}
	case "cli.track":
		e.Source, e.Via = event.SourceCLI, event.ViaTrack
		label := []string{"fix the login redirect loop", "ok", "add tests for the parser please"}[rng.IntN(3)]
		return Input{Event: event.Event{Envelope: e, Data: &event.CLITrack{Label: label, TextLen: utf8.RuneCountInString(label),
			LogMatch: event.LogNoReader}}, Text: []byte(label)}
	}
	panic("no sample input of kind " + kind)
}

// writeRandom writes n random records from rng, a second apart from from, and
// seals after about one in every sealEvery.
func writeRandom(t testing.TB, w *Writer, rng *rand.Rand, n, sealEvery int, from int64) {
	t.Helper()
	for i := range n {
		kind := kinds[rng.IntN(len(kinds))]
		if _, _, err := write(w, rng, kind, from+int64(i)*1000); err != nil {
			t.Fatalf("writing a %s: %v", kind, err)
		}
		if rng.IntN(sealEvery) == 0 {
			if _, _, err := w.Seal(); err != nil {
				t.Fatalf("Seal: %v", err)
			}
		}
	}
}

// fileSize returns the size of name.
func fileSize(t testing.TB, name string) int64 {
	t.Helper()
	fi, err := os.Stat(name)
	if err != nil {
		t.Fatal(err)
	}
	return fi.Size()
}
