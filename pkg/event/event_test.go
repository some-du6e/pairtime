// SPDX-License-Identifier: AGPL-3.0-or-later

package event

import (
	"bytes"
	"encoding/json"
	"math"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

const (
	testEventID  = "0193f6a1-2b3c-7d4e-8f00-112233445566"
	testEventID2 = "0193f6a1-4d5e-7f60-9a11-223344556677"
	testT        = 1795046500000 // 2026-11-19T00:41:40Z
)

func ptr[T any](v T) *T { return &v }

func testMinHash() []uint32 {
	h := make([]uint32, MinHashSize)
	for i := range h {
		h[i] = uint32(i) * 2654435761
	}
	h[MinHashSize-1] = math.MaxUint32
	return h
}

// sample returns a valid event of kind k with every payload field set,
// optional ones included, so round trips cover the whole payload.
func sample(t *testing.T, k Kind) Event {
	t.Helper()
	env := Envelope{
		EventID:        testEventID,
		DeviceID:       "d_7f3a9c",
		Seq:            18301,
		T:              testT,
		Mono:           81163000,
		BootID:         "b_51c2",
		Source:         SourceHook,
		Via:            ViaHook,
		Harness:        HarnessClaudeCode,
		HarnessVersion: "2.1.281",
		SessionKey:     "s_4be1f0",
		ProjectKey:     "p_9f2c",
	}
	noHarness := func(src Source, via Via) {
		env.Source, env.Via, env.Harness, env.HarnessVersion, env.SessionKey = src, via, HarnessNone, "", NoSession
	}
	var p Payload
	switch k {
	case KindSessionStart:
		p = &SessionStart{StartKind: StartResume, Entrypoint: EntrypointCLI, AutomationFlags: []string{"background_agent"},
			ParentClass: ParentTerminalApp, HasTTY: true, PermissionMode: "acceptEdits"}
	case KindSessionEnd:
		p = &SessionEnd{Reason: "prompt_input_exit"}
	case KindPromptSubmit:
		// A guest's prompt keeps no text, so Guest is set in guestSample.
		p = &PromptSubmit{TurnKey: "t_01", LenChars: 140, PastedChars: 40, TypedLenEst: 100, MinHash: testMinHash(),
			TemplateHash: "th_5e", ErrorSig: "es_77", SlashCommand: "/review", OriginDeclared: DeclaredLoop,
			MatchesPendingCron: true, AnswersQuestion: true, LowEffort: true, Relay: testEventID2,
			SealedText: []byte("sealed\x00\xffbox")}
	case KindAgentOutput:
		p = &AgentOutput{TurnKey: "t_01", Words: 160, CodeLines: 12, IsFinal: true, AsksQuestion: true, OffersOptions: true}
	case KindAgentTool:
		p = &AgentTool{ToolClass: ToolBash, Files: []string{"f_a1", "f_b2"}, LinesAdded: 8, LinesRemoved: 3, CmdHash: "c_9d",
			ExitCode: ptr[int64](3221225477), ErrorSig: "es_12", VisibleOutputLines: 40, TStart: testT, TEnd: testT + 12000}
	case KindAgentPermission:
		p = &AgentPermission{RequestedAt: testT - 9000, AnsweredAt: testT, Decision: DecisionReject, DecidedBy: DecidedByUser}
	case KindAgentInterrupt:
		p = &AgentInterrupt{TurnKey: "t_01"}
	case KindAgentQueue:
		p = &AgentQueue{TurnKey: "t_01", LenChars: 52}
	case KindAgentStop:
		p = &AgentStop{TurnKey: "t_01", StopReason: StopQuestion, StopHookActive: true,
			PendingCrons: []PendingCron{{PrefixHash: "ph_3c", Schedule: "*/20 * * * *"}}}
	case KindAgentSubagent:
		p = &AgentSubagent{Phase: SubagentStart, AgentKey: "a_77"}
	case KindEditorActivity:
		noHarness(SourceEditor, ViaEditor)
		p = &EditorActivity{Editor: "vscode", Act: ActEdit, FileHash: "f_a1", LinesChanged: 4, EditSource: EditSourceHuman}
	case KindShellCommand:
		noHarness(SourceShell, ViaShell)
		p = &ShellCommand{CmdClass: CmdTest, CmdHash: "c_9d", ExitCode: ptr[int64](0), TStart: testT, TEnd: testT + 30000,
			Interactive: true, ErrorSig: "es_12"}
	case KindGitChange:
		noHarness(SourceGit, ViaGit)
		p = &GitChange{Op: GitCommit, SHA: "1a2b3c4d5e6f7a8b9c0d1e2f3a4b5c6d7e8f9a0b",
			Parents: []string{"0f1e2d3c4b5a69788796a5b4c3d2e1f0a9b8c7d6"}, FilesChanged: 3, Insertions: 40, Deletions: 12,
			Actor: ActorHuman, AgentTrailer: "claude"}
	case KindOSPresence:
		noHarness(SourceOS, ViaOS)
		env.ProjectKey = ""
		p = &OSPresence{IdleAnyS: 3.5, IdleKeysS: ptr(12.25), Locked: true}
	case KindOSFocus:
		noHarness(SourceOS, ViaOS)
		env.ProjectKey = ""
		p = &OSFocus{AppClass: AppTerminal, AppHash: "app_5d", HostedSessions: []string{"s_4be1f0"}, FocusProject: "p_9f2c"}
	case KindOSPower:
		noHarness(SourceOS, ViaOS)
		env.ProjectKey = ""
		p = &OSPower{Op: PowerSuspend}
	case KindOTelActiveTime:
		env.Source, env.Via = SourceOTel, ViaOTel
		p = &OTelActiveTime{Type: ActiveTimeUser, Seconds: 42.5}
	case KindCLITrack:
		env.Source, env.Via = SourceCLI, ViaTrack
		p = &CLITrack{Label: "fix the login redirect loop\non mobile", TextLen: 37, LowEffort: true, ErrorSig: "e_track",
			EnvHints: []EnvHint{EnvClaudeCode, EnvClaudeCodeSessionID}, LogMatch: LogMatched}
	case KindRelayMessage:
		env.Source, env.Via = SourceLog, ViaApp
		p = &RelayMessage{App: RelayAppT3Code, Client: RelayClientMobile, SentAt: testT - 3000, ThreadKey: "r_6454f1",
			Declared: RelayDeclaredCron, LenChars: 90, LowEffort: true, Prompt: testEventID2}
	default:
		t.Fatalf("no sample for kind %q; add one", k)
	}
	return Event{Envelope: env, Data: p}
}

// guestSample is prompt.submit's second sample: a guest's prompt, which
// keeps no text (SM 1.2), so sample's prompt, which has text, can't be one
// too. Between them the two set every field.
func guestSample(t *testing.T) Event {
	t.Helper()
	e := sample(t, KindPromptSubmit)
	p := e.Data.(*PromptSubmit)
	p.Guest, p.SealedText = true, nil
	return e
}

// samples returns every sample of kind k: sample's, and for prompt.submit
// guestSample's as well.
func samples(t *testing.T, k Kind) []Event {
	t.Helper()
	if k == KindPromptSubmit {
		return []Event{sample(t, k), guestSample(t)}
	}
	return []Event{sample(t, k)}
}

func TestKindTable(t *testing.T) {
	ks := Kinds()
	if len(ks) != 19 {
		t.Errorf("Kinds() has %d kinds, SM 1.2 lists 19", len(ks))
	}
	// Payloads whose zero value is a real event: an unexplained session end
	// and an idle sample taken mid-keystroke. A track call always says
	// whether a log reader matched it.
	zeroIsValid := map[Kind]bool{KindSessionEnd: true, KindOSPresence: true}
	seen := map[Kind]bool{}
	for _, k := range ks {
		t.Run(string(k), func(t *testing.T) {
			if seen[k] {
				t.Errorf("kind %q is registered twice", k)
			}
			seen[k] = true
			p := newPayload(k)
			if p == nil {
				t.Fatalf("newPayload(%q) = nil", k)
			}
			if p.Kind() != k {
				t.Errorf("newPayload(%q).Kind() = %q", k, p.Kind())
			}
			if err := p.Validate(); (err == nil) != zeroIsValid[k] {
				t.Errorf("a zero %s payload: Validate() = %v, want valid = %v", k, err, zeroIsValid[k])
			}
		})
	}
	if newPayload("agent.telepathy") != nil {
		t.Error("newPayload accepted an unknown kind")
	}
}

func TestEventValidateSamples(t *testing.T) {
	for _, k := range Kinds() {
		t.Run(string(k), func(t *testing.T) {
			for _, e := range samples(t, k) {
				if err := e.Validate(); err != nil {
					t.Errorf("Validate() = %v, want nil", err)
				}
				if err := e.Data.Validate(); err != nil {
					t.Errorf("payload Validate() = %v, want nil", err)
				}
			}
		})
	}
}

func TestEventValidate(t *testing.T) {
	tests := []struct {
		name   string
		kind   Kind
		mutate func(e *Event)
		want   string // a substring of the error, or "" for a valid event
	}{
		// Envelope.
		{"event_id is a UUIDv7", KindAgentStop, func(e *Event) { e.EventID = "0193f6a1-2b3c-4d4e-8f00-112233445566" }, "event_id: must be a lowercase UUIDv7"},
		{"event_id is lowercase", KindAgentStop, func(e *Event) { e.EventID = strings.ToUpper(e.EventID) }, "event_id:"},
		{"device_id is required", KindAgentStop, func(e *Event) { e.DeviceID = "" }, "device_id:"},
		{"seq starts at 1", KindAgentStop, func(e *Event) { e.Seq = 0 }, "seq: must be at least 1"},
		{"t in seconds is a unit mistake", KindAgentStop, func(e *Event) { e.T = testT / 1000 }, "t: must be Unix milliseconds"},
		{"t in microseconds is a unit mistake", KindAgentStop, func(e *Event) { e.T = testT * 1000 }, "t: must be Unix milliseconds"},
		{"mono isn't negative", KindAgentStop, func(e *Event) { e.Mono = -1 }, "mono:"},
		{"boot_id is required", KindAgentStop, func(e *Event) { e.BootID = "" }, "boot_id:"},
		{"unknown source", KindAgentStop, func(e *Event) { e.Source = "telepathy" }, "source:"},
		{"via is required", KindAgentStop, func(e *Event) { e.Via = "" }, `via: "" is not one of`},
		{"unknown via", KindAgentStop, func(e *Event) { e.Via = "wakatime" }, `via: "wakatime" is not one of`},
		{"unknown harness", KindAgentStop, func(e *Event) { e.Harness = "Claude Code" }, "harness:"},
		{"harness_version is one line", KindAgentStop, func(e *Event) { e.HarnessVersion = "2.1\n" }, "harness_version:"},
		{"harness_version is a version, never a bare word such as a host name", KindAgentStop, func(e *Event) { e.HarnessVersion = "karims-mbp.local" }, "harness_version: must be a version"},
		{"harness_version may carry a pre-release suffix", KindAgentStop, func(e *Event) { e.HarnessVersion = "v0.1.0-alpha.1" }, ""},
		{"harness_version may be absent", KindAgentStop, func(e *Event) { e.HarnessVersion = "" }, ""},
		{"session_key isn't a path", KindAgentStop, func(e *Event) { e.SessionKey = "/home/teen/.claude/projects/x.jsonl" }, "session_key:"},
		{"project_key isn't a path", KindAgentStop, func(e *Event) { e.ProjectKey = `C:\Users\teen\game` }, "project_key:"},
		{"data is required", KindAgentStop, func(e *Event) { e.Data = nil }, "data: missing"},
		{"typed nil data", KindAgentStop, func(e *Event) { e.Data = (*AgentStop)(nil) }, "data: missing"},

		// What may produce each kind.
		{"OS events have no harness", KindOSPresence, func(e *Event) { e.Harness = HarnessCursor }, `harness: must be "none"`},
		{"OS events have no session", KindOSFocus, func(e *Event) { e.SessionKey = "s_1" }, `session_key: must be "none"`},
		{"OS events come from the OS", KindOSPower, func(e *Event) { e.Source = SourceHook }, "source: os.power events come from"},
		{"git events come from git", KindGitChange, func(e *Event) { e.Source = SourceLog }, "source: git.change events come from"},
		{"git events have no session", KindGitChange, func(e *Event) { e.SessionKey = "s_1" }, `session_key: must be "none"`},
		{"otel events come from otel", KindOTelActiveTime, func(e *Event) { e.Source = SourceHook }, "source:"},
		{"track events don't come from hooks", KindCLITrack, func(e *Event) { e.Source = SourceHook }, "source:"},
		{"an app's message comes from its store, which its reader reads as a log, never from a hook", KindRelayMessage,
			func(e *Event) { e.Source = SourceHook }, `source: relay.message events come from ["log"], got "hook"`},
		{"an app's message names the harness session the app bound its run to", KindRelayMessage, func(e *Event) {}, ""},
		{"an app's message whose run the reader tied to no session has no harness and no session", KindRelayMessage, func(e *Event) {
			e.Harness, e.HarnessVersion, e.SessionKey = HarnessNone, "", NoSession
		}, ""},

		// How the agent may receive each kind (DESIGN 4.1 E-2).
		{"presence samples arrive from the OS watchers", KindOSPresence, func(e *Event) { e.Via = ViaPlugin }, `via: os.presence events arrive via ["os"], got "plugin"`},
		{"focus events arrive from the OS watchers", KindOSFocus, func(e *Event) { e.Via = ViaHook }, `via: os.focus events arrive via ["os"], got "hook"`},
		{"power events arrive from the OS watchers", KindOSPower, func(e *Event) { e.Via = ViaTrack }, `via: os.power events arrive via ["os"], got "track"`},
		{"git changes arrive from the git watcher", KindGitChange, func(e *Event) { e.Via = ViaBridge }, `via: git.change events arrive via ["git"], got "bridge"`},
		{"track calls arrive through pairtime track", KindCLITrack, func(e *Event) { e.Via = ViaPlugin }, `via: cli.track events arrive via ["track"], got "plugin"`},
		{"active time arrives through the OTLP receiver", KindOTelActiveTime, func(e *Event) { e.Via = ViaLog }, `via: otel.active_time events arrive via ["otel"], got "log"`},
		{"an app's message arrives through the app's reader, never as a plugin", KindRelayMessage, func(e *Event) { e.Via = ViaPlugin },
			`via: relay.message events arrive via ["app"], got "plugin"`},
		{"only an app's message arrives via app, never a prompt (DESIGN 4.1 E-12)", KindPromptSubmit, func(e *Event) { e.Via = ViaApp },
			`via: prompt.submit events never arrive via "app", which only ["relay.message"] events do`},
		{"an editor event, which may arrive any other way, never arrives via app", KindEditorActivity, func(e *Event) { e.Via = ViaApp },
			`via: editor.activity events never arrive via "app", which only ["relay.message"] events do`},
		{"an OS event via app breaks the OS events' via rule", KindOSFocus, func(e *Event) { e.Via = ViaApp },
			`via: os.focus events arrive via ["os"], got "app"`},
		{"a hook kind may arrive through a plugin", KindPromptSubmit, func(e *Event) { e.Via = ViaPlugin }, ""},
		{"a hook kind may arrive through the bridge", KindAgentTool, func(e *Event) { e.Via = ViaBridgeUnverified }, ""},
		{"an editor kind may arrive unverified", KindEditorActivity, func(e *Event) { e.Via = ViaEditorUnverified }, ""},
		{"harness events have a harness", KindPromptSubmit, func(e *Event) { e.Harness = HarnessNone }, "harness: prompt.submit events come from a harness"},
		{"harness events have a session", KindAgentTool, func(e *Event) { e.SessionKey = NoSession }, "session_key: agent.tool events belong to a harness session"},

		// Payloads.
		{"start_kind is required", KindSessionStart, func(e *Event) { e.Data.(*SessionStart).StartKind = "" }, "data.start_kind:"},
		{"automation flags are keys", KindSessionStart, func(e *Event) {
			e.Data.(*SessionStart).AutomationFlags = []string{"ok", "not a key"}
		}, "data.automation_flags[1]:"},
		{"session.end reason is a key", KindSessionEnd, func(e *Event) { e.Data.(*SessionEnd).Reason = "user quit" }, "data.reason:"},
		{"agent.interrupt has a turn", KindAgentInterrupt, func(e *Event) { e.Data.(*AgentInterrupt).TurnKey = "" }, "data.turn_key:"},
		{"agent.queue has a turn", KindAgentQueue, func(e *Event) { e.Data.(*AgentQueue).TurnKey = "" }, "data.turn_key:"},
		{"agent.queue len_chars isn't negative", KindAgentQueue, func(e *Event) { e.Data.(*AgentQueue).LenChars = -1 }, "data.len_chars: must not be negative"},
		{"typed_len_est adds up", KindPromptSubmit, func(e *Event) { e.Data.(*PromptSubmit).TypedLenEst = 140 }, "data.typed_len_est: must be len_chars - pasted_chars = 100"},
		{"pasted_chars fits in len_chars", KindPromptSubmit, func(e *Event) {
			p := e.Data.(*PromptSubmit)
			p.PastedChars, p.TypedLenEst = 200, -60
		}, "data.pasted_chars: is 200, more than len_chars (140)"},
		{"minhash has 64 values", KindPromptSubmit, func(e *Event) { e.Data.(*PromptSubmit).MinHash = []uint32{1, 2, 3} }, "data.minhash:"},
		{"slash command starts with a slash", KindPromptSubmit, func(e *Event) { e.Data.(*PromptSubmit).SlashCommand = "loop" }, "data.slash_command:"},
		{"slash command has no arguments", KindPromptSubmit, func(e *Event) { e.Data.(*PromptSubmit).SlashCommand = "/loop 20m check CI" }, "data.slash_command:"},
		{"origin_declared is required", KindPromptSubmit, func(e *Event) { e.Data.(*PromptSubmit).OriginDeclared = "" }, "data.origin_declared:"},
		{"a prompt's relay is the event id of its app message", KindPromptSubmit, func(e *Event) { e.Data.(*PromptSubmit).Relay = "r_6454f1" },
			`data.relay: must be a lowercase UUIDv7, got "r_6454f1"`},
		{"a prompt that came before its app message, or has none, names no relay", KindPromptSubmit, func(e *Event) {
			e.Data.(*PromptSubmit).Relay = ""
		}, ""},
		{"a guest's prompt keeps no text (SM 1.2)", KindPromptSubmit, func(e *Event) { e.Data.(*PromptSubmit).Guest = true },
			"data.text: is set, but a guest's prompt keeps no text"},
		{"a guest's prompt without its text", KindPromptSubmit, func(e *Event) {
			p := e.Data.(*PromptSubmit)
			p.Guest, p.SealedText = true, nil
		}, ""},
		{"a session an agent app's server started is agent_app", KindSessionStart, func(e *Event) {
			p := e.Data.(*SessionStart)
			p.ParentClass, p.Entrypoint, p.HasTTY = ParentAgentApp, EntrypointUnknown, false
		}, ""},
		{"word counts aren't negative", KindAgentOutput, func(e *Event) { e.Data.(*AgentOutput).Words = -1 }, "data.words:"},
		{"tool files are hashes, not paths", KindAgentTool, func(e *Event) { e.Data.(*AgentTool).Files = []string{"src/main.go"} }, "data.files[0]:"},
		{"a tool doesn't end before it starts", KindAgentTool, func(e *Event) {
			p := e.Data.(*AgentTool)
			p.TEnd = p.TStart - 1
		}, "data.t_end: is 1 ms before t_start"},
		{"a permission isn't answered before it's asked", KindAgentPermission, func(e *Event) {
			p := e.Data.(*AgentPermission)
			p.AnsweredAt = p.RequestedAt - 1000
		}, "data.answered_at:"},
		{"unknown decision", KindAgentPermission, func(e *Event) { e.Data.(*AgentPermission).Decision = "maybe" }, "data.decision:"},
		{"a pending cron has a schedule", KindAgentStop, func(e *Event) { e.Data.(*AgentStop).PendingCrons[0].Schedule = "" }, "data.pending_crons[0].schedule: missing"},
		{"a pending cron hashes its prompt", KindAgentStop, func(e *Event) {
			e.Data.(*AgentStop).PendingCrons[0].PrefixHash = "check CI every 20 minutes"
		}, "data.pending_crons[0].prefix_hash:"},
		{"unknown subagent phase", KindAgentSubagent, func(e *Event) { e.Data.(*AgentSubagent).Phase = "pause" }, "data.phase:"},
		{"unknown edit source", KindEditorActivity, func(e *Event) { e.Data.(*EditorActivity).EditSource = "copilot" }, "data.edit_source:"},
		{"a command doesn't end before it starts", KindShellCommand, func(e *Event) {
			p := e.Data.(*ShellCommand)
			p.TEnd = p.TStart - 1
		}, "data.t_end:"},
		{"short sha", KindGitChange, func(e *Event) { e.Data.(*GitChange).SHA = "1a2b3c4" }, "data.sha:"},
		{"parents are object ids", KindGitChange, func(e *Event) { e.Data.(*GitChange).Parents = []string{"HEAD~1"} }, "data.parents[0]:"},
		{"the trailer is an agent name, not the raw trailer", KindGitChange, func(e *Event) {
			e.Data.(*GitChange).AgentTrailer = "Co-Authored-By: Claude <noreply@anthropic.com>"
		}, "data.agent_trailer:"},
		{"idle isn't negative", KindOSPresence, func(e *Event) { e.Data.(*OSPresence).IdleAnyS = -1 }, "data.idle_any_s:"},
		{"idle isn't NaN", KindOSPresence, func(e *Event) { e.Data.(*OSPresence).IdleAnyS = math.NaN() }, "data.idle_any_s:"},
		{"keys are idle at least as long as any input", KindOSPresence, func(e *Event) { e.Data.(*OSPresence).IdleKeysS = ptr(1.0) }, "data.idle_keys_s:"},
		{"app_class is required", KindOSFocus, func(e *Event) { e.Data.(*OSFocus).AppClass = "" }, "data.app_class:"},
		{"hosted sessions are keys", KindOSFocus, func(e *Event) { e.Data.(*OSFocus).HostedSessions = []string{"pid 4242"} }, "data.hosted_sessions[0]:"},
		// SM 2.5: where the client can't tell which window is in front, the
		// record says unknown and nothing about a window.
		{"focus the client can't see is an unknown record with no other field", KindOSFocus, func(e *Event) {
			e.Data = &OSFocus{AppClass: AppUnknown}
		}, ""},
		{"an unknown record names no app, so it has no app_hash", KindOSFocus, func(e *Event) {
			e.Data = &OSFocus{AppClass: AppUnknown, AppHash: "app_5d"}
		}, `data.app_hash: is set, but an "unknown" record can't tell which app is in front`},
		{"an unknown record knows no window, so it hosts no session", KindOSFocus, func(e *Event) {
			e.Data = &OSFocus{AppClass: AppUnknown, HostedSessions: []string{"s_4be1f0"}}
		}, `data.hosted_sessions: is set, but an "unknown" record can't tell which window is in front, so it hosts no session`},
		{"an unknown record knows no window, so no project resolves from it", KindOSFocus, func(e *Event) {
			e.Data = &OSFocus{AppClass: AppUnknown, FocusProject: "p_9f2c"}
		}, `data.focus_project: is set, but an "unknown" record`},
		{"no window in front is none, and may still name nothing", KindOSFocus, func(e *Event) {
			e.Data = &OSFocus{AppClass: AppNone}
		}, ""},
		{"unknown power op", KindOSPower, func(e *Event) { e.Data.(*OSPower).Op = "hibernate" }, "data.op:"},
		{"otel seconds aren't negative", KindOTelActiveTime, func(e *Event) { e.Data.(*OTelActiveTime).Seconds = -5 }, "data.seconds:"},
		{"a track label keeps its line breaks", KindCLITrack, func(e *Event) { e.Data.(*CLITrack).Label = "line one\nline two" }, ""},
		{"a track label holds no control character but LF", KindCLITrack, func(e *Event) { e.Data.(*CLITrack).Label = "line one\r\nline two" },
			"data.label: must not contain control characters other than line feeds"},
		{"a track call's error_sig is an opaque key", KindCLITrack, func(e *Event) { e.Data.(*CLITrack).ErrorSig = "/home/x/err.log" }, "data.error_sig:"},
		{"a track label holds the AGENTS.md entry's 400 characters", KindCLITrack, func(e *Event) {
			e.Data.(*CLITrack).Label = strings.Repeat("é", 400)
		}, ""},
		{"a track label is capped at 400 characters", KindCLITrack, func(e *Event) {
			e.Data.(*CLITrack).Label = strings.Repeat("é", 401)
		}, "data.label: has 401 characters, more than 400"},
		{"a track call says whether a log reader matched it", KindCLITrack, func(e *Event) { e.Data.(*CLITrack).LogMatch = "" }, "data.log_match:"},
		{"unknown log match", KindCLITrack, func(e *Event) { e.Data.(*CLITrack).LogMatch = "maybe" }, "data.log_match:"},
		{"track text length isn't negative", KindCLITrack, func(e *Event) { e.Data.(*CLITrack).TextLen = -1 }, "data.text_len: must not be negative"},
		{"env hints are names, never values", KindCLITrack, func(e *Event) {
			e.Data.(*CLITrack).EnvHints = []EnvHint{EnvClaudeCode, "CLAUDE_CODE_SESSION_ID=5f0c"}
		}, "data.env_hints[1]: must be one of the harness variable names"},
		{"a value shaped like a name isn't an env hint", KindCLITrack, func(e *Event) {
			e.Data.(*CLITrack).EnvHints = []EnvHint{"production"}
		}, "data.env_hints[0]: must be one of the harness variable names"},
		{"only harness variables are env hints", KindCLITrack, func(e *Event) { e.Data.(*CLITrack).EnvHints = []EnvHint{"PATH"} }, "data.env_hints[0]:"},
		{"every listed harness variable is an env hint", KindCLITrack, func(e *Event) {
			e.Data.(*CLITrack).EnvHints = []EnvHint{EnvClaudeCodeSessionID, EnvClaudeCodeChildSession, EnvClaudeCode}
		}, ""},
		{"env hints are listed once", KindCLITrack, func(e *Event) {
			e.Data.(*CLITrack).EnvHints = []EnvHint{EnvClaudeCode, EnvClaudeCode}
		}, "data.env_hints[1]: repeats an earlier name"},
		{"a track call from no harness session", KindCLITrack, func(e *Event) {
			e.Harness, e.SessionKey, e.Data.(*CLITrack).LogMatch = HarnessOther, NoSession, LogNoReader
		}, ""},
		{"an app's message names the app its reader read", KindRelayMessage, func(e *Event) { e.Data.(*RelayMessage).App = "" }, "data.app:"},
		{"only an app pairtime has a reader for writes app messages", KindRelayMessage, func(e *Event) { e.Data.(*RelayMessage).App = "slack" },
			`data.app: "slack" is not one of`},
		{"an app's message says which of its clients typed it", KindRelayMessage, func(e *Event) { e.Data.(*RelayMessage).Client = "" }, "data.client:"},
		{"the client is the app's web client or its phone app", KindRelayMessage, func(e *Event) { e.Data.(*RelayMessage).Client = "server" },
			`data.client: "server" is not one of`},
		{"a message typed in the app's web client", KindRelayMessage, func(e *Event) { e.Data.(*RelayMessage).Client = RelayClientWeb }, ""},
		{"the app's stamp is in milliseconds", KindRelayMessage, func(e *Event) { e.Data.(*RelayMessage).SentAt = testT / 1000 },
			"data.sent_at: must be Unix milliseconds"},
		{"the thread is a key, never the app's own text or a path", KindRelayMessage, func(e *Event) {
			e.Data.(*RelayMessage).ThreadKey = "threads/8b0c"
		}, "data.thread_key:"},
		{"an app's message says what sent it", KindRelayMessage, func(e *Event) { e.Data.(*RelayMessage).Declared = "" }, "data.declared:"},
		{"what sent an app's message is a person, the app's scheduler or another thread", KindRelayMessage, func(e *Event) {
			e.Data.(*RelayMessage).Declared = RelayDeclared(DeclaredLoop)
		}, `data.declared: "loop" is not one of`},
		{"a message the app says a person sent", KindRelayMessage, func(e *Event) { e.Data.(*RelayMessage).Declared = RelayDeclaredNone }, ""},
		{"a message from another thread", KindRelayMessage, func(e *Event) { e.Data.(*RelayMessage).Declared = RelayDeclaredCrossSession }, ""},
		{"an app message's typed length isn't negative", KindRelayMessage, func(e *Event) { e.Data.(*RelayMessage).LenChars = -1 },
			"data.len_chars: must not be negative"},
		{"an app message's prompt is the paired prompt's event id", KindRelayMessage, func(e *Event) { e.Data.(*RelayMessage).Prompt = "t_01" },
			`data.prompt: must be a lowercase UUIDv7, got "t_01"`},
		{"an app message that came before its prompt, or pairs with none, names no prompt", KindRelayMessage, func(e *Event) {
			e.Data.(*RelayMessage).Prompt = ""
		}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := sample(t, tt.kind)
			tt.mutate(&e)
			err := e.Validate()
			switch {
			case tt.want == "":
				if err != nil {
					t.Errorf("Validate() = %v, want nil", err)
				}
			case err == nil:
				t.Errorf("Validate() = nil, want an error containing %q", tt.want)
			case !strings.Contains(err.Error(), tt.want):
				t.Errorf("Validate() = %q, want it to contain %q", err, tt.want)
			}
		})
	}
}

func TestEventValidateReportsEveryError(t *testing.T) {
	e := sample(t, KindPromptSubmit)
	e.Seq = 0
	e.Data.(*PromptSubmit).TypedLenEst = 1
	err := e.Validate()
	if err == nil {
		t.Fatal("Validate() = nil, want two errors")
	}
	for _, want := range []string{"seq:", "data.typed_len_est:"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("Validate() = %q, want it to contain %q", err, want)
		}
	}
}

// TestEnvHintErrorsLeaveOutTheEntry checks that Validate doesn't echo a
// rejected env hint: an entry that isn't a listed name may be a variable's
// value, and errors end up in logs.
func TestEnvHintErrorsLeaveOutTheEntry(t *testing.T) {
	for _, tt := range []struct {
		name  string
		entry EnvHint
	}{
		{"a NAME=value pair isn't repeated", "CLAUDE_CODE_SESSION_ID=5f0c9a2e"},
		{"a bare value isn't repeated", "x9TqLm2vR8pZ"},
		{"a value with a space isn't repeated", "correct horse"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			e := sample(t, KindCLITrack)
			e.Data.(*CLITrack).EnvHints = []EnvHint{tt.entry}
			err := e.Validate()
			switch {
			case err == nil:
				t.Fatalf("Validate() = nil, want an error for the env hint")
			case !strings.Contains(err.Error(), "data.env_hints[0]:"):
				t.Errorf("Validate() = %q, want an error for data.env_hints[0]", err)
			case strings.Contains(err.Error(), string(tt.entry)):
				t.Errorf("Validate() = %q, which repeats the rejected entry", err)
			}
		})
	}
}

func TestEventJSONRoundTrip(t *testing.T) {
	for _, k := range Kinds() {
		t.Run(string(k), func(t *testing.T) {
			all := samples(t, k)
			v := reflect.ValueOf(all[0].Data).Elem()
			for i := 0; i < v.NumField(); i++ {
				set := slices.ContainsFunc(all, func(e Event) bool { return !reflect.ValueOf(e.Data).Elem().Field(i).IsZero() })
				if !set {
					t.Errorf("no sample sets %s.%s; set every field in one so the round trip covers it", v.Type().Name(), v.Type().Field(i).Name)
				}
			}
			for _, want := range all {
				b, err := json.Marshal(want)
				if err != nil {
					t.Fatalf("Marshal: %v", err)
				}
				var got Event
				if err := json.Unmarshal(b, &got); err != nil {
					t.Fatalf("Unmarshal(%s): %v", b, err)
				}
				if !reflect.DeepEqual(got, want) {
					t.Errorf("the round trip changed the event\njson: %s\n got: %+v %+v\nwant: %+v %+v", b, got.Envelope, got.Data, want.Envelope, want.Data)
				}
				again, err := json.Marshal(got)
				if err != nil {
					t.Fatalf("second Marshal: %v", err)
				}
				if !bytes.Equal(again, b) {
					t.Errorf("the JSON changed on a second round trip\n first: %s\nsecond: %s", b, again)
				}
			}
		})
	}
}

// TestEventJSONGolden pins the wire format: field names, their order, and
// how hashes, null and omitted fields look.
func TestEventJSONGolden(t *testing.T) {
	e := sample(t, KindOSPresence)
	p := e.Data.(*OSPresence)
	p.IdleKeysS, p.Locked = nil, false
	got, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"event_id":"0193f6a1-2b3c-7d4e-8f00-112233445566","device_id":"d_7f3a9c","seq":18301,"t":1795046500000,` +
		`"mono":81163000,"boot_id":"b_51c2","source":"os","via":"os","harness":"none","session_key":"none",` +
		`"kind":"os.presence","data":{"idle_any_s":3.5,"idle_keys_s":null,"locked":false}}`
	if string(got) != want {
		t.Errorf("Marshal =\n%s\nwant\n%s", got, want)
	}
}

func TestEventMarshalWithoutData(t *testing.T) {
	for name, data := range map[string]Payload{"nil": nil, "typed nil": (*OSPower)(nil)} {
		t.Run(name, func(t *testing.T) {
			if b, err := json.Marshal(Event{Envelope: sample(t, KindOSPower).Envelope, Data: data}); err == nil {
				t.Errorf("Marshal with data %#v = %s, want an error", data, b)
			}
		})
	}
}

func TestEventUnmarshalErrors(t *testing.T) {
	b, err := json.Marshal(sample(t, KindAgentInterrupt))
	if err != nil {
		t.Fatal(err)
	}
	valid := string(b)
	swap := func(old, new string) string {
		t.Helper()
		if !strings.Contains(valid, old) {
			t.Fatalf("%q isn't in %s", old, valid)
		}
		return strings.Replace(valid, old, new, 1)
	}
	tests := []struct {
		name, json, want string
	}{
		{"unknown kind", swap(`"kind":"agent.interrupt"`, `"kind":"agent.telepathy"`), `unknown kind "agent.telepathy"`},
		{"no kind", swap(`"kind":"agent.interrupt",`, ``), `unknown kind ""`},
		{"no data", swap(`,"data":{"turn_key":"t_01"}`, ``), "agent.interrupt event has no data"},
		{"null data", swap(`{"turn_key":"t_01"}`, `null`), "agent.interrupt event has no data"},
		{"data of the wrong shape", swap(`{"turn_key":"t_01"}`, `["t_01"]`), "agent.interrupt data"},
		{"via as a number", swap(`"via":"hook"`, `"via":1`), "via"},
		{"seq as a string", swap(`"seq":18301`, `"seq":"18301"`), "seq"},
		{"negative seq", swap(`"seq":18301`, `"seq":-1`), "seq"},
		{"not an object", `[1,2,3]`, "cannot unmarshal"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var e Event
			err := json.Unmarshal([]byte(tt.json), &e)
			if err == nil {
				t.Fatalf("Unmarshal(%s) = nil, want an error containing %q", tt.json, tt.want)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("Unmarshal(%s) = %q, want it to contain %q", tt.json, err, tt.want)
			}
		})
	}
}

// localJSONNames returns the JSON names of kind k's local payload fields.
func localJSONNames(t *testing.T, k Kind) []string {
	t.Helper()
	var names []string
	for _, f := range jsonFields(reflect.TypeOf(newPayload(k)).Elem()) {
		if f.local {
			if !f.omitempty {
				t.Errorf("%s.%s is local but not omitempty, so a shareable event would still carry it", k, f.name)
			}
			names = append(names, f.name)
		}
	}
	return names
}

// TestLocalFields pins which fields stay on the device. Changing this list
// changes what pairtime may upload; it needs a privacy review, not just a
// test update.
func TestLocalFields(t *testing.T) {
	want := map[Kind][]string{
		KindPromptSubmit: {"minhash", "template_hash", "error_sig", "text"},
		KindAgentTool:    {"cmd_hash"},
		KindShellCommand: {"cmd_hash"},
		KindOSFocus:      {"app_hash"},
		KindCLITrack:     {"label", "error_sig"},
		KindAgentStop:    {"pending_crons"},
		// An app's message holds no text and no digest of it: the reader
		// measures and classes the app's text and drops it (DESIGN 5.1).
		// prompt.submit's guest and relay are shareable too.
		KindRelayMessage: nil,
	}
	for _, k := range Kinds() {
		t.Run(string(k), func(t *testing.T) {
			if got := localJSONNames(t, k); !slices.Equal(got, want[k]) {
				t.Errorf("%s local fields = %q, want %q", k, got, want[k])
			}
		})
	}
	// Of the derived records only the minute bucket leaves the device, as
	// tier 1, and its anchor ids stay behind (DESIGN 4.1 E-5, 4.3).
	for _, d := range []struct {
		typ  reflect.Type
		want []string
	}{
		{reflect.TypeFor[MinuteBucket](), []string{"anchors"}},
		{reflect.TypeFor[BucketSlot](), nil},
	} {
		t.Run(d.typ.Name(), func(t *testing.T) {
			var got []string
			for _, f := range jsonFields(d.typ) {
				if !f.local {
					continue
				}
				if !f.omitempty {
					t.Errorf("%s.%s is local but not omitempty, so a shareable record would still carry it", d.typ.Name(), f.name)
				}
				got = append(got, f.name)
			}
			if !slices.Equal(got, d.want) {
				t.Errorf("%s local fields = %q, want %q", d.typ.Name(), got, d.want)
			}
		})
	}
}

func TestShareable(t *testing.T) {
	for _, k := range Kinds() {
		t.Run(string(k), func(t *testing.T) {
			e := sample(t, k)
			before, err := json.Marshal(e)
			if err != nil {
				t.Fatal(err)
			}
			s := e.Shareable()
			if after, _ := json.Marshal(e); !bytes.Equal(after, before) {
				t.Errorf("Shareable modified the original\nbefore: %s\n after: %s", before, after)
			}
			if s.Kind() != k || s.Envelope != e.Envelope {
				t.Errorf("Shareable changed the envelope or kind")
			}
			orig := reflect.ValueOf(e.Data).Elem()
			got := reflect.ValueOf(s.Data).Elem()
			for i := 0; i < got.NumField(); i++ {
				f := got.Type().Field(i)
				local := f.Tag.Get("pairtime") == "local"
				if local && !got.Field(i).IsZero() {
					t.Errorf("local field %s is still set", f.Name)
				}
				if !local && !reflect.DeepEqual(got.Field(i).Interface(), orig.Field(i).Interface()) {
					t.Errorf("field %s changed, but it isn't local", f.Name)
				}
			}
			b, err := json.Marshal(s)
			if err != nil {
				t.Fatal(err)
			}
			for _, name := range localJSONNames(t, k) {
				if bytes.Contains(b, []byte(`"`+name+`":`)) {
					t.Errorf("the shareable JSON still has %q: %s", name, b)
				}
			}
			if err := s.Validate(); err != nil {
				t.Errorf("the shareable event doesn't validate: %v", err)
			}
		})
	}
	if s := (Event{}).Shareable(); s.Data != nil {
		t.Errorf("Shareable of an event with no data = %+v", s)
	}
}

func TestNewEventID(t *testing.T) {
	at := time.UnixMilli(testT + 123)
	id := NewEventID(at)
	if !isUUIDv7(id) {
		t.Fatalf("NewEventID = %q, not a lowercase UUIDv7", id)
	}
	ms, err := strconv.ParseUint(strings.ReplaceAll(id[:13], "-", ""), 16, 64)
	if err != nil || int64(ms) != at.UnixMilli() {
		t.Errorf("NewEventID(%d ms) = %q, whose timestamp is %d (%v)", at.UnixMilli(), id, ms, err)
	}
	if again := NewEventID(at); again == id {
		t.Errorf("two ids for the same millisecond are both %q", id)
	}
	if later := NewEventID(at.Add(time.Millisecond)); later <= id {
		t.Errorf("ids aren't time-ordered: %q, then %q a millisecond later", id, later)
	}
}

func TestReason(t *testing.T) {
	tests := []struct {
		r          Reason
		credited   bool
		uncredited bool
		activity   Activity
	}{
		{"P", true, false, ActivityPrompting},
		{"READ~r", true, false, ActivityReadingOutput},
		{"TEST", true, false, ActivityTesting},
		{"CMD", true, false, ActivityDebugging},
		{"LOOKUP", true, false, ActivityLookup},
		{"X.absent", false, true, ""},
		{"X.noprog", false, true, ""},
		{"X.absent~r", false, false, ""},
		{"P+restored", true, false, ActivityPrompting},
		{"READ~r+restored", true, false, ActivityReadingOutput},
		{"X.absent+restored", false, false, ""},
		{"P+restored~r", false, false, ""},
		{"P+restored+restored", false, false, ""},
		{"P~r~r", false, false, ""},
		{"p", false, false, ""},
		{"", false, false, ""},
	}
	for _, tt := range tests {
		t.Run(strconv.Quote(string(tt.r)), func(t *testing.T) {
			if got := tt.r.Credited(); got != tt.credited {
				t.Errorf("Reason(%q).Credited() = %v, want %v", tt.r, got, tt.credited)
			}
			if got := tt.r.Uncredited(); got != tt.uncredited {
				t.Errorf("Reason(%q).Uncredited() = %v, want %v", tt.r, got, tt.uncredited)
			}
			if got := tt.r.Activity(); got != tt.activity {
				t.Errorf("Reason(%q).Activity() = %q, want %q", tt.r, got, tt.activity)
			}
			if got := tt.r.Valid(); got != (tt.credited || tt.uncredited) {
				t.Errorf("Reason(%q).Valid() = %v", tt.r, got)
			}
		})
	}
}

// embedded wraps a real payload the way an adapter might, to keep extra
// data next to it. Neither Validate nor Shareable may accept it.
type embedded struct {
	*PromptSubmit
	Raw string
}

func TestForeignPayload(t *testing.T) {
	e := sample(t, KindPromptSubmit)
	ps, ok := e.Data.(*PromptSubmit)
	if !ok {
		t.Fatalf("sample prompt.submit has data %T", e.Data)
	}
	for name, data := range map[string]Payload{"value": embedded{ps, "raw prompt"}, "pointer": &embedded{ps, "raw prompt"}} {
		t.Run(name, func(t *testing.T) {
			f := e
			f.Data = data
			if err := f.Validate(); err == nil || !strings.Contains(err.Error(), "is not the payload type") {
				t.Errorf("Validate(%T) = %v, want a payload type error", data, err)
			}
			s := f.Shareable()
			if s.Data != nil {
				t.Errorf("Shareable(%T) kept data %T", data, s.Data)
			}
			if _, err := json.Marshal(s); err == nil {
				t.Errorf("Shareable(%T) produced an event that marshals", data)
			}
		})
	}
}
