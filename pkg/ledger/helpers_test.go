// SPDX-License-Identifier: AGPL-3.0-or-later

package ledger

import (
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"math/rand/v2"
	"testing"

	"github.com/some-du6e/pairtime/pkg/event"
	"github.com/some-du6e/pairtime/pkg/ledger/features"
)

// testT is 2026-11-19T00:41:40Z in Unix ms.
const testT = 1795046500000

func ptr[T any](v T) *T { return &v }

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatalf("bad hex %q: %v", s, err)
	}
	return b
}

func mustHash(t *testing.T, s string) Hash {
	t.Helper()
	h, err := HashOf(mustHex(t, s))
	if err != nil {
		t.Fatal(err)
	}
	return h
}

// exampleKDev is the worked example's K_dev, 0x00 to 0x1f (spec 12.1).
func exampleKDev() []byte {
	k := make([]byte, KeySize)
	for i := range k {
		k[i] = byte(i)
	}
	return k
}

func testKeys(t *testing.T) *Keys {
	t.Helper()
	k, err := DeriveKeys(exampleKDev())
	if err != nil {
		t.Fatal(err)
	}
	return k
}

// testServerKey is a fixed Ed25519 key that signs the tests' receipts and
// day heads. Nothing real uses it.
func testServerKey() ed25519.PrivateKey {
	seed := make([]byte, ed25519.SeedSize)
	copy(seed, "pairtime pkg/ledger test server")
	return ed25519.NewKeyFromSeed(seed)
}

func testServerPub() ed25519.PublicKey { return testServerKey().Public().(ed25519.PublicKey) }

// otherServerKey is an Ed25519 key made from seed text, for a server other
// than the test server.
func otherServerKey(seed string) ed25519.PrivateKey {
	b := make([]byte, ed25519.SeedSize)
	copy(b, seed)
	return ed25519.NewKeyFromSeed(b)
}

// serverKeyAlways is an Options.ServerKey with one key for all time.
func serverKeyAlways(pub ed25519.PublicKey) func(uint64) ed25519.PublicKey {
	return func(uint64) ed25519.PublicKey { return pub }
}

func testMinHash() []uint32 {
	h := make([]uint32, event.MinHashSize)
	for i := range h {
		h[i] = uint32(i) * 2654435761
	}
	h[event.MinHashSize-1] = math.MaxUint32
	return h
}

// sampleEvent returns a valid event of kind k with every payload field
// set, local fields included, as pkg/event's own tests build them.
func sampleEvent(k event.Kind) (event.Event, error) {
	env := event.Envelope{
		EventID:        "0193f6a1-2b3c-7d4e-8f00-112233445566",
		DeviceID:       "d_7f3a9c",
		Seq:            18301,
		T:              testT,
		Mono:           81163000,
		BootID:         "b_51c2",
		Source:         event.SourceHook,
		Via:            event.ViaHook,
		Harness:        event.HarnessClaudeCode,
		HarnessVersion: "2.1.281",
		SessionKey:     "s_4be1f0",
		ProjectKey:     "p_9f2c",
	}
	noHarness := func(src event.Source, via event.Via) {
		env.Source, env.Via, env.Harness, env.HarnessVersion, env.SessionKey = src, via, event.HarnessNone, "", event.NoSession
	}
	var p event.Payload
	switch k {
	case event.KindSessionStart:
		p = &event.SessionStart{StartKind: event.StartResume, Entrypoint: event.EntrypointCLI, AutomationFlags: []string{"background_agent"},
			ParentClass: event.ParentTerminalApp, HasTTY: true, PermissionMode: "acceptEdits"}
	case event.KindSessionEnd:
		p = &event.SessionEnd{Reason: "prompt_input_exit"}
	case event.KindPromptSubmit:
		p = &event.PromptSubmit{TurnKey: "t_01", LenChars: 140, PastedChars: 40, TypedLenEst: 100, MinHash: testMinHash(),
			TemplateHash: "th_5e", ErrorSig: "es_77", SlashCommand: "/review", OriginDeclared: event.DeclaredLoop,
			MatchesPendingCron: true, AnswersQuestion: true, LowEffort: true, SealedText: []byte("sealed\x00\xffbox")}
	case event.KindAgentOutput:
		p = &event.AgentOutput{TurnKey: "t_01", Words: 160, CodeLines: 12, IsFinal: true, AsksQuestion: true, OffersOptions: true}
	case event.KindAgentTool:
		p = &event.AgentTool{ToolClass: event.ToolBash, Files: []string{"f_a1", "f_b2"}, LinesAdded: 8, LinesRemoved: 3, CmdHash: "c_9d",
			ExitCode: ptr[int64](3221225477), ErrorSig: "es_12", VisibleOutputLines: 40, TStart: testT, TEnd: testT + 12000}
	case event.KindAgentPermission:
		p = &event.AgentPermission{RequestedAt: testT - 9000, AnsweredAt: testT, Decision: event.DecisionReject, DecidedBy: event.DecidedByUser}
	case event.KindAgentInterrupt:
		p = &event.AgentInterrupt{TurnKey: "t_01"}
	case event.KindAgentQueue:
		p = &event.AgentQueue{TurnKey: "t_01", LenChars: 52}
	case event.KindAgentStop:
		p = &event.AgentStop{TurnKey: "t_01", StopReason: event.StopQuestion, StopHookActive: true,
			PendingCrons: []event.PendingCron{{PrefixHash: "ph_3c", Schedule: "*/20 * * * *"}}}
	case event.KindAgentSubagent:
		p = &event.AgentSubagent{Phase: event.SubagentStart, AgentKey: "a_77"}
	case event.KindEditorActivity:
		noHarness(event.SourceEditor, event.ViaEditor)
		p = &event.EditorActivity{Editor: "vscode", Act: event.ActEdit, FileHash: "f_a1", LinesChanged: 4, EditSource: event.EditSourceHuman}
	case event.KindShellCommand:
		noHarness(event.SourceShell, event.ViaShell)
		p = &event.ShellCommand{CmdClass: event.CmdTest, CmdHash: "c_9d", ExitCode: ptr[int64](0), TStart: testT, TEnd: testT + 30000,
			Interactive: true, ErrorSig: "es_12"}
	case event.KindGitChange:
		noHarness(event.SourceGit, event.ViaGit)
		p = &event.GitChange{Op: event.GitCommit, SHA: "1a2b3c4d5e6f7a8b9c0d1e2f3a4b5c6d7e8f9a0b",
			Parents: []string{"0f1e2d3c4b5a69788796a5b4c3d2e1f0a9b8c7d6"}, FilesChanged: 3, Insertions: 40, Deletions: 12,
			Actor: event.ActorHuman, AgentTrailer: "claude"}
	case event.KindOSPresence:
		noHarness(event.SourceOS, event.ViaOS)
		env.ProjectKey = ""
		p = &event.OSPresence{IdleAnyS: 3.5, IdleKeysS: ptr(12.25), Locked: true}
	case event.KindOSFocus:
		noHarness(event.SourceOS, event.ViaOS)
		env.ProjectKey = ""
		p = &event.OSFocus{AppClass: event.AppTerminal, AppHash: "app_5d", HostedSessions: []string{"s_4be1f0"}, FocusProject: "p_9f2c"}
	case event.KindOSPower:
		noHarness(event.SourceOS, event.ViaOS)
		env.ProjectKey = ""
		p = &event.OSPower{Op: event.PowerSuspend}
	case event.KindOTelActiveTime:
		env.Source, env.Via = event.SourceOTel, event.ViaOTel
		p = &event.OTelActiveTime{Type: event.ActiveTimeUser, Seconds: 42.5}
	case event.KindCLITrack:
		env.Source, env.Via = event.SourceCLI, event.ViaTrack
		p = &event.CLITrack{Label: "fix the login redirect loop", TextLen: 27,
			EnvHints: []event.EnvHint{"CLAUDECODE", "CLAUDE_CODE_SESSION_ID"}, LogMatch: event.LogMatched}
	case event.KindRelayMessage:
		env.Source, env.Via = event.SourceLog, event.ViaApp
		p = &event.RelayMessage{App: event.RelayAppT3Code, Client: event.RelayClientWeb, SentAt: testT - 3000, ThreadKey: "r_6454f1",
			Declared: event.RelayDeclaredNone, LenChars: 90, Prompt: "0193f6a1-4d5e-7f60-9a11-223344556677"}
	default:
		return event.Event{}, fmt.Errorf("no sample event for kind %q; add one", k)
	}
	return event.Event{Envelope: env, Data: p}, nil
}

// sampleContent returns the content EventRecord takes for sampleEvent's
// event of kind k (spec 10): texts that agree with the sample's len_chars,
// words and code_lines, edit hunks and commands. It is zero for the kinds
// that take none.
func sampleContent(k event.Kind) Content {
	switch k {
	case event.KindPromptSubmit:
		return Content{Text: []byte("the navbar still overlaps the hero on mobile\r\n```css\n.nav { z-index: 10 }\n```\nTypeError: x is undefined"),
			Class: features.ErrorPaste}
	case event.KindAgentOutput:
		return Content{Text: []byte("Here's the fix:\n```js\nconst x = 1;\n```\nRun the tests again.")}
	case event.KindAgentTool:
		return Content{Edits: []features.Edit{{Path: "src/nav.js", OldStart: 10, OldLines: 3, NewStart: 10, NewLines: 5}},
			Command: "npm  test"}
	case event.KindShellCommand:
		return Content{Command: "pytest -k test_login"}
	case event.KindCLITrack:
		return Content{Text: []byte("fix the login redirect loop"), Class: features.Substantive}
	case event.KindRelayMessage:
		// The class of the app's own text; the record commits none of it.
		return Content{Class: features.Substantive}
	}
	return Content{}
}

// testEventID returns a UUIDv7 for time t with bits from rng, so the
// tests' files come out the same every run.
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

func pick[T any](rng *rand.Rand, xs []T) T { return xs[rng.IntN(len(xs))] }

func randHex(rng *rand.Rand, n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(rng.Uint32())
	}
	return hex.EncodeToString(b)
}

const keyChars = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789_-"

func randKey(rng *rand.Rand, n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = keyChars[rng.IntN(len(keyChars))]
	}
	return string(b)
}

// allRTypes lists every code in spec 4.2's table.
func allRTypes() []RType {
	var rs []RType
	for r := 0; r < 256; r++ {
		if RType(r).Known() {
			rs = append(rs, RType(r))
		}
	}
	return rs
}

// recordSpec says what goes into one test record.
type recordSpec struct {
	rtype      RType
	deviceID   string
	seq        uint64
	t          int64
	projectKey string // for a checkpoint, and for an event unless it is an os.* event
	sessionKey string // for an event that belongs to a harness session
}

// testRecord writes the record spec describes, with a sample event for an
// event record. It returns the record's project_key and session_key too.
func testRecord(rng *rand.Rand, k *Keys, spec recordSpec) (rec Record, projectKey, sessionKey string, err error) {
	switch spec.rtype {
	case RTypeCheckpoint:
		state := fmt.Sprintf(`{ "since_progress_ms": %d, "sessions": [] }`, rng.IntN(1_000_000))
		minute := uint64(spec.t) / CheckpointMS * CheckpointMS
		rec, err = CheckpointRecord(k, spec.seq, spec.projectKey, minute, json.RawMessage(state))
		return rec, spec.projectKey, event.NoSession, err
	case RTypeUploadFailure:
		reason := pick(rng, []string{"dns", "connect", "tls", "http_5xx", "http_429"})
		rec, err = UploadFailureRecord(k, spec.seq, uint64(spec.t), reason)
		return rec, "", event.NoSession, err
	}
	kind, ok := spec.rtype.EventKind()
	if !ok {
		return Record{}, "", "", fmt.Errorf("no record for %s", spec.rtype)
	}
	ev, err := sampleEvent(kind)
	if err != nil {
		return Record{}, "", "", err
	}
	ev.EventID = testEventID(rng, spec.t)
	ev.DeviceID = spec.deviceID
	ev.Seq = spec.seq
	ev.T = spec.t
	ev.Mono = rng.Int64N(1 << 40)
	if ev.SessionKey != event.NoSession {
		ev.SessionKey = spec.sessionKey
	}
	if ev.ProjectKey != "" {
		ev.ProjectKey = spec.projectKey
	}
	rec, err = EventRecord(k, ev, sampleContent(kind))
	return rec, ev.ProjectKey, ev.SessionKey, err
}

func entriesOf(recs []Record) []Entry {
	es := make([]Entry, len(recs))
	for i, r := range recs {
		es[i] = r.Entry()
	}
	return es
}
