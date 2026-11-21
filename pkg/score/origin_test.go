// SPDX-License-Identifier: AGPL-3.0-or-later

package score

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/some-du6e/pairtime/pkg/event"
	"github.com/some-du6e/pairtime/pkg/params"
)

// promptCase is a prompt submitted at 60 s in session s_1, after typing from
// 30.5 s to 59.5 s, unless edit changes that.
type promptCase struct {
	name string
	keys bool
	// edit changes the prompt or adds records around it.
	edit func(b *tl, p *event.PromptSubmit)
	// typing replaces the default key presses.
	typing []int64
	origin event.Origin
	class  event.PromptClass
	// kind and lead describe the anchor; kind "" means none.
	kind event.AnchorKind
	lead int64 // seconds
}

func (c promptCase) build() (*tl, event.Event) {
	b := newTL(c.keys)
	terminalSession(b)
	typing := c.typing
	if typing == nil {
		typing = every(atMS(30_500), atMS(59_500), 1000)
	}
	b.press(typing...)
	e := b.prompt(at(60), event.ViaHook, "s_1", "p_1", "t_p", 30)
	if c.edit != nil {
		c.edit(b, e.Data.(*event.PromptSubmit))
	}
	b.samples(at(0), at(120))
	return b, e
}

func TestOrigin(t *testing.T) {
	// restartAt replaces terminalSession's session.start with one at t,
	// with the presence sample the agent takes at every hook event (DESIGN
	// 3.3).
	restartAt := func(t int64, entry event.Entrypoint, parent event.ParentClass, tty bool) func(b *tl, p *event.PromptSubmit) {
		return func(b *tl, p *event.PromptSubmit) {
			b.evs[1].T = t
			b.evs[1].Data = &event.SessionStart{StartKind: event.StartStartup, Entrypoint: entry, ParentClass: parent, HasTTY: tty}
			b.sample(t)
		}
	}
	restart := func(entry event.Entrypoint, parent event.ParentClass, tty bool) func(b *tl, p *event.PromptSubmit) {
		return restartAt(at(0), entry, parent, tty)
	}
	cases := []promptCase{
		{name: "rule 6: input in the KEYS_PRE_S before submit, focus on the window hosting the session: human_typed",
			origin: event.OriginHumanTyped, class: event.ClassSubstantive, kind: event.AnchorSubstantivePrompt, lead: 120 + 20},
		{name: "rule 1: a prompt matching a pending cron is scheduled",
			edit:   func(b *tl, p *event.PromptSubmit) { p.MatchesPendingCron = true },
			origin: event.OriginScheduled},
		{name: "rule 1: a /loop iteration is scheduled",
			edit:   func(b *tl, p *event.PromptSubmit) { p.OriginDeclared = event.DeclaredLoop },
			origin: event.OriginScheduled},
		{name: "rule 2: a /goal continuation the harness declares is self_continue",
			edit:   func(b *tl, p *event.PromptSubmit) { p.OriginDeclared = event.DeclaredGoal },
			origin: event.OriginSelfContinue},
		{name: "rule 2 reads the prompt: a later stop of its turn with stop_hook_active doesn't change a typed prompt's origin",
			edit: func(b *tl, p *event.PromptSubmit) {
				b.harness(at(90), event.ViaHook, "s_1", "p_1", &event.AgentStop{TurnKey: "t_p", StopReason: event.StopEndTurn, StopHookActive: true})
			},
			origin: event.OriginHumanTyped, class: event.ClassSubstantive, kind: event.AnchorSubstantivePrompt, lead: 120 + 20},
		{name: "rule 3: a subagent's prompt is subagent",
			edit:   func(b *tl, p *event.PromptSubmit) { p.OriginDeclared = event.DeclaredSubagent },
			origin: event.OriginSubagent},
		{name: "rule 3: a message from another session is subagent, one agent prompting another",
			edit:   func(b *tl, p *event.PromptSubmit) { p.OriginDeclared = event.DeclaredCrossSession },
			origin: event.OriginSubagent},
		{name: "rule 4: an sdk entrypoint is headless", edit: restart(event.EntrypointSDK, event.ParentScriptRuntime, true),
			origin: event.OriginHeadless},
		{name: "rule 4: a scheduler parent is headless", edit: restart(event.EntrypointCLI, event.ParentScheduler, false),
			origin: event.OriginHeadless},
		{name: "rule 4: neither a TTY nor an IDE or desktop host is headless", edit: restart(event.EntrypointCLI, event.ParentUnknown, false),
			origin: event.OriginHeadless},
		{name: "rule 4: claude -p typed by hand counts where key presses come in the KEYS_PRE_S before session.start",
			keys: true, edit: restartAt(at(55), event.EntrypointSDK, event.ParentTerminalApp, true),
			origin: event.OriginHumanTyped, class: event.ClassSubstantive, kind: event.AnchorSubstantivePrompt, lead: 140},
		{name: "rule 4: where the OS reports no key presses, claude -p typed by hand stays headless",
			edit:   restartAt(at(55), event.EntrypointSDK, event.ParentTerminalApp, true),
			origin: event.OriginHeadless},
		{name: "rule 5: a session on a vendor VM is cloud", edit: restart(event.EntrypointCloud, event.ParentUnknown, true),
			origin: event.OriginCloud},
		{name: "rule 8: no input in the KEYS_PRE_S before submit is no_input",
			typing: every(atMS(30_500), atMS(49_500), 1000), origin: event.OriginNoInput},
		{name: "rule 8: focus on a window that doesn't host the session is no_input",
			edit:   func(b *tl, p *event.PromptSubmit) { b.focus(at(40), event.AppTerminal, "p_1") },
			origin: event.OriginNoInput},
		{name: "rule 6 reads key presses where the OS reports them: pointer input alone before submit is no_input",
			keys: true, typing: every(atMS(30_500), atMS(45_500), 1000),
			edit:   func(b *tl, p *event.PromptSubmit) { b.mouse(every(atMS(50_200), atMS(59_200), 1000)...) },
			origin: event.OriginNoInput},
		{name: "rule 7: pasted characters make the prompt human_pasted",
			edit:   func(b *tl, p *event.PromptSubmit) { p.LenChars, p.PastedChars = 130, 100 },
			origin: event.OriginHumanPasted, class: event.ClassSubstantive, kind: event.AnchorSubstantivePrompt, lead: 140},
		{name: "rule 7: more typed characters than TYPING_MAX_CPS allows in the lead's input slots are human_pasted, cut to that",
			typing: every(atMS(50_500), atMS(59_500), 1000),
			edit:   func(b *tl, p *event.PromptSubmit) { p.LenChars, p.TypedLenEst = 300, 300 },
			// One input slot: 15 s at 15 characters a second is 225, and
			// 225 / 1.5 = 150 s of typing.
			origin: event.OriginHumanPasted, class: event.ClassSubstantive, kind: event.AnchorSubstantivePrompt, lead: 120 + 150},
		{name: "class answer: the agent's last output asked a question; ANSWER_LEAD_S plus typing",
			edit: func(b *tl, p *event.PromptSubmit) {
				b.harness(at(20), event.ViaLog, "s_1", "p_1", &event.AgentOutput{TurnKey: "t_0", Words: 30, IsFinal: true, AsksQuestion: true})
			},
			origin: event.OriginHumanTyped, class: event.ClassAnswer, kind: event.AnchorAnswer, lead: 60 + 20},
		{name: "class answer: a turn that stopped on a question makes the next prompt an answer",
			edit: func(b *tl, p *event.PromptSubmit) {
				b.harness(at(20), event.ViaHook, "s_1", "p_1", &event.AgentStop{TurnKey: "t_0", StopReason: event.StopQuestion})
			},
			origin: event.OriginHumanTyped, class: event.ClassAnswer, kind: event.AnchorAnswer, lead: 80},
		{name: "class answer: a go-ahead that answers a question is an answer",
			edit: func(b *tl, p *event.PromptSubmit) {
				p.LenChars, p.TypedLenEst, p.LowEffort = 2, 2, true
				b.harness(at(20), event.ViaLog, "s_1", "p_1", &event.AgentOutput{TurnKey: "t_0", Words: 30, IsFinal: true, AsksQuestion: true})
			},
			// 2 / 1.5 = 1.33 s of typing, rounded to 1.
			origin: event.OriginHumanTyped, class: event.ClassAnswer, kind: event.AnchorAnswer, lead: 61},
		{name: "class low_effort: the text test the agent applied makes a go-ahead, with LOW_LEAD_S and no typing part",
			edit:   func(b *tl, p *event.PromptSubmit) { p.LowEffort = true },
			origin: event.OriginHumanTyped, class: event.ClassLowEffort, kind: event.AnchorGoAhead, lead: 20},
		{name: "class low_effort: under SUBSTANTIVE_CHARS typed characters",
			edit:   func(b *tl, p *event.PromptSubmit) { p.LenChars, p.TypedLenEst = 19, 19 },
			origin: event.OriginHumanTyped, class: event.ClassLowEffort, kind: event.AnchorGoAhead, lead: 20},
		{name: "class substantive: exactly SUBSTANTIVE_CHARS typed characters",
			edit: func(b *tl, p *event.PromptSubmit) { p.LenChars, p.TypedLenEst = 20, 20 },
			// 20 / 1.5 = 13.33 s, rounded to 13.
			origin: event.OriginHumanTyped, class: event.ClassSubstantive, kind: event.AnchorSubstantivePrompt, lead: 133},
		{name: "class error_paste: an error signature and under ERROR_PASTE_TEXT_MAX typed characters around it",
			edit: func(b *tl, p *event.PromptSubmit) {
				p.LenChars, p.PastedChars, p.TypedLenEst, p.ErrorSig = 330, 300, 30, "e_assert"
			},
			origin: event.OriginHumanPasted, class: event.ClassErrorPaste, kind: event.AnchorSubstantivePrompt, lead: 140},
		{name: "class substantive: an error signature with ERROR_PASTE_TEXT_MAX typed characters is a hypothesis",
			edit: func(b *tl, p *event.PromptSubmit) {
				p.LenChars, p.PastedChars, p.TypedLenEst, p.ErrorSig = 340, 300, 40, "e_assert"
			},
			// 40 / 1.5 = 26.67 s, rounded to 27.
			origin: event.OriginHumanPasted, class: event.ClassSubstantive, kind: event.AnchorSubstantivePrompt, lead: 147},
		{name: "rule 4: a plugin's session.start after a headless hook one doesn't decide a hooked session's origin (DESIGN 5.2)",
			edit: func(b *tl, p *event.PromptSubmit) {
				restart(event.EntrypointSDK, event.ParentScriptRuntime, true)(b, p)
				b.harness(at(1), event.ViaPlugin, "s_1", "p_1", &event.SessionStart{StartKind: event.StartResume,
					Entrypoint: event.EntrypointCLI, ParentClass: event.ParentTerminalApp, HasTTY: true})
			},
			origin: event.OriginHeadless},
		{name: "rule 6: a plugin's session.start saying ide doesn't make the editor host a hooked terminal session",
			edit: func(b *tl, p *event.PromptSubmit) {
				b.harness(at(1), event.ViaPlugin, "s_1", "p_1", &event.SessionStart{StartKind: event.StartResume,
					Entrypoint: event.EntrypointIDE, ParentClass: event.ParentIDE})
				b.focus(at(40), event.AppEditor, "p_1")
			},
			origin: event.OriginNoInput},
		{name: "rule 6: a bridge record in a hooked terminal session doesn't make the editor host it (DESIGN 5.7)",
			edit: func(b *tl, p *event.PromptSubmit) {
				b.harness(at(30), event.ViaBridge, "s_1", "p_1", &event.AgentTool{ToolClass: event.ToolEdit, LinesAdded: 3,
					TStart: at(29), TEnd: at(30)})
				b.focus(at(40), event.AppEditor, "p_1")
			},
			origin: event.OriginNoInput},
		{name: "class low_effort: a plugin's stop on a question doesn't make a hooked session's go-ahead an answer",
			edit: func(b *tl, p *event.PromptSubmit) {
				p.LenChars, p.TypedLenEst, p.LowEffort = 2, 2, true
				b.harness(at(20), event.ViaPlugin, "s_1", "p_1", &event.AgentStop{TurnKey: "t_0", StopReason: event.StopQuestion})
			},
			origin: event.OriginHumanTyped, class: event.ClassLowEffort, kind: event.AnchorGoAhead, lead: 20},
		{name: "class low_effort: a plugin's output asking a question doesn't make a hooked session's go-ahead an answer",
			edit: func(b *tl, p *event.PromptSubmit) {
				p.LenChars, p.TypedLenEst, p.LowEffort = 2, 2, true
				b.harness(at(20), event.ViaPlugin, "s_1", "p_1", &event.AgentOutput{TurnKey: "t_0", Words: 30, IsFinal: true,
					AsksQuestion: true})
			},
			origin: event.OriginHumanTyped, class: event.ClassLowEffort, kind: event.AnchorGoAhead, lead: 20},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b, e := c.build()
			got := b.run(t, Device{TypingModule: c.keys})
			tu, ok := turnByKey(got, "t_p")
			if !ok {
				t.Fatal("no turn for the prompt")
			}
			if tu.Origin != c.origin || tu.PromptClass != c.class {
				t.Errorf("origin %q class %q, want %q and %q", tu.Origin, tu.PromptClass, c.origin, c.class)
			}
			a, ok := anchorByID(got, e.EventID)
			switch {
			case c.kind == "" && ok:
				t.Errorf("an anchor %s, want none", a.Kind)
			case c.kind != "" && !ok:
				t.Errorf("no anchor, want %s", c.kind)
			case ok && (a.Kind != c.kind || a.LeadMS != c.lead*1000):
				t.Errorf("anchor %s with lead %d ms, want %s with %d s", a.Kind, a.LeadMS, c.kind, c.lead)
			}
		})
	}
}

// rehearsalSubmit is when the hook stamped the prompt in the CI rehearsal
// that found the race INPUT_RELEASE_GRACE_MS is for, in milliseconds after
// base.
const rehearsalSubmit = 58_497

// rehearsal builds the rehearsal's prompt, a minute from base, with the
// times the rehearsal recorded (04:13:00 UTC is base): the regular samples
// came 7 ms after each slot boundary, so the last one before the prompt,
// at 45.007 s, saw the last input at 44.421 s; xdotool typed 53 characters
// from 53.7 s to 58.38 s and pressed Return at 58.49 s; the hook stamped
// the prompt at 58.497 s; and the hook's sample, at 58.501 s, saw Return's
// release at 58.5 s. release moves the release, a millisecond count after
// base, and the hook's sample to just after it when it is later. A release
// is input, but not a key press.
func rehearsal(b *tl, release int64) event.Event {
	terminalSession(b)
	b.mouse(atMS(44_421))
	b.press(every(atMS(53_700), atMS(58_380), 90)...)
	b.press(atMS(58_490))
	b.mouse(atMS(release))
	e := b.prompt(atMS(rehearsalSubmit), event.ViaHook, "s_1", "p_1", "t_p", 53)
	b.sample(max(atMS(58_501), atMS(release+1)))
	for s := int64(7); s <= 120_007; s += event.SlotMS {
		b.sample(atMS(s))
	}
	return e
}

// tableWithGrace returns the test table with INPUT_RELEASE_GRACE_MS set to
// ms, under a version no real table has.
func tableWithGrace(t *testing.T, ms int64) *params.Table {
	t.Helper()
	var doc struct {
		Version string                     `json:"version"`
		Params  map[string]json.RawMessage `json:"params"`
	}
	if err := json.Unmarshal(testTable(t).Canonical(), &doc); err != nil {
		t.Fatal(err)
	}
	doc.Version = "9.9.9"
	doc.Params[params.INPUT_RELEASE_GRACE_MS.String()] = json.RawMessage(strconv.FormatInt(ms, 10))
	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	tab, err := params.Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	return tab
}

// TestReleaseGrace checks SM 2.2 rules 6 and 7's INPUT_RELEASE_GRACE_MS
// (D-input-release-grace). The hook stamps a prompt's t when it starts,
// after the press of the key that sends it, and the agent samples a few
// milliseconds later. A sample only says when the last input before it
// came, so a release in between dates the input after t and hides the
// typing before it. Input up to 250 ms after the submit counts for the two
// rules' input test.
func TestReleaseGrace(t *testing.T) {
	cases := []struct {
		name string
		// keys says the OS reports key presses apart from other input, as
		// macOS's key-down idle time does.
		keys bool
		// release is when Return was released, in milliseconds after base.
		release int64
		edit    func(b *tl, p *event.PromptSubmit)
		// noGrace scores with INPUT_RELEASE_GRACE_MS = 0, the input test
		// before table 0.2.0.
		noGrace bool
		origin  event.Origin
		// level and lead describe the prompt's anchor; level "" means
		// none.
		level event.Evidence
		lead  int64 // seconds
	}{
		{name: "rule 6: the rehearsal's prompt, whose hook sample saw Return released 3 ms after submit, is human_typed",
			release: 58_500, origin: event.OriginHumanTyped, level: event.EvidenceB, lead: 120 + 35},
		{name: "rule 8: without the grace, the rehearsal's prompt is no_input, as the rehearsal found",
			release: 58_500, noGrace: true, origin: event.OriginNoInput},
		{name: "rule 6: input exactly INPUT_RELEASE_GRACE_MS after submit counts",
			release: rehearsalSubmit + 250, origin: event.OriginHumanTyped, level: event.EvidenceB, lead: 120 + 35},
		{name: "rule 8: input 251 ms after submit doesn't count, and the typing it hides is lost",
			release: rehearsalSubmit + 251, origin: event.OriginNoInput},
		{name: "rule 7: a pasted prompt's input test takes the grace too",
			release: 58_500, edit: func(b *tl, p *event.PromptSubmit) { p.LenChars, p.PastedChars = 153, 100 },
			origin: event.OriginHumanPasted, level: event.EvidenceB, lead: 120 + 35},
		{name: "rule 6 reads key presses where the OS reports them, and a release isn't one: Return's press before submit shows, at level A",
			keys: true, release: 58_500, origin: event.OriginHumanTyped, level: event.EvidenceA, lead: 120 + 35},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := newTL(c.keys)
			e := rehearsal(b, c.release)
			if c.edit != nil {
				c.edit(b, e.Data.(*event.PromptSubmit))
			}
			tab := testTable(t)
			if c.noGrace {
				tab = tableWithGrace(t, 0)
			}
			_, out := Step(NewState(tab, Device{}), b.records())
			var got Derived
			got.Append(out.Final)
			got.Append(out.Pending)
			if tu, ok := turnByKey(got, "t_p"); !ok || tu.Origin != c.origin {
				t.Errorf("origin %q, want %q", tu.Origin, c.origin)
			}
			a, ok := anchorByID(got, e.EventID)
			switch {
			case c.level == "" && ok:
				t.Errorf("an anchor %s, want none", a.Kind)
			case c.level != "" && !ok:
				t.Error("no anchor")
			case ok && (a.Level != c.level || a.LeadMS != c.lead*1000):
				t.Errorf("anchor at level %s with lead %d ms, want %s and %d s", a.Level, a.LeadMS, c.level, c.lead)
			}
		})
	}
}

// TestReleaseGraceRules6And7Only checks that no other test of input or
// focus takes INPUT_RELEASE_GRACE_MS (D-input-release-grace): input 100 ms
// after the act, which the grace would cover, never counts for them.
func TestReleaseGraceRules6And7Only(t *testing.T) {
	// command is a command typed in a shell in the terminal, started at
	// 58.497 s with a key press 100 ms from its start, before it when
	// early is set and after it otherwise, and the shell's sample right
	// after that press.
	command := func(early bool) func(b *tl) {
		return func(b *tl) {
			start := atMS(rehearsalSubmit)
			press := start + 100
			if early {
				press = start - 100
			}
			b.focus(at(0), event.AppTerminal, "p_1")
			b.mouse(atMS(44_421))
			b.press(press)
			b.sample(press + 1)
			b.shell(at(70), "p_1", &event.ShellCommand{CmdClass: event.CmdRun, TStart: start, TEnd: at(69), Interactive: true})
			b.samples(at(0), at(120))
		}
	}
	// claudeP is a claude -p session from a terminal, started at 55 s
	// with a key press at 55 s plus offset ms and the agent's sample right
	// after it, whose prompt comes at 60 s.
	claudeP := func(offset int64) func(b *tl) {
		return func(b *tl) {
			b.focus(at(0), event.AppTerminal, "p_1", "s_1")
			b.start(at(55), "s_1", "p_1", event.EntrypointSDK, event.ParentTerminalApp, true)
			b.press(at(55) + offset)
			b.sample(at(55) + offset + 1)
			b.prompt(at(60), event.ViaHook, "s_1", "p_1", "t_p", 30)
			b.samples(at(0), at(120))
		}
	}
	hasCommand := func(d Derived) bool {
		return slices.ContainsFunc(d.Anchors, func(a Anchor) bool { return a.Kind == event.AnchorTypedCommand })
	}
	origin := func(d Derived) event.Origin {
		tu, _ := turnByKey(d, "t_p")
		return tu.Origin
	}
	cases := []struct {
		name  string
		keys  bool
		build func(b *tl)
		ok    func(d Derived) bool
	}{
		{name: "focus at submit: the window hosting the session coming into focus 100 ms after submit leaves the prompt no_input",
			build: func(b *tl) {
				e := rehearsal(b, 58_500)
				b.evs[0].Data = &event.OSFocus{AppClass: event.AppBrowser, AppHash: "a_browser"}
				b.focus(e.T+100, event.AppTerminal, "p_1", "s_1")
			},
			ok: func(d Derived) bool { return origin(d) == event.OriginNoInput }},
		{name: "a typed command's evidence test: a key press 100 ms after the command starts opens no anchor",
			build: command(false), ok: func(d Derived) bool { return !hasCommand(d) }},
		{name: "a typed command's evidence test, the control: a key press 100 ms before the command starts opens one",
			build: command(true), ok: hasCommand},
		{name: "rule 4: a key press 100 ms after a claude -p session starts leaves its prompt headless",
			keys: true, build: claudeP(100), ok: func(d Derived) bool { return origin(d) == event.OriginHeadless }},
		{name: "rule 4, the control: a key press 100 ms before the session starts makes its prompt human_typed",
			keys: true, build: claudeP(-100), ok: func(d Derived) bool { return origin(d) == event.OriginHumanTyped }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := newTL(c.keys)
			c.build(b)
			if d := b.run(t, Device{}); !c.ok(d) {
				t.Errorf("anchors %+v, turns %+v", d.Anchors, d.Turns)
			}
		})
	}
}

func TestIDESession(t *testing.T) {
	b := newTL(false)
	b.focus(at(0), event.AppEditor, "p_1")
	b.start(at(0), "s_1", "p_1", event.EntrypointIDE, event.ParentIDE, false)
	b.press(every(atMS(30_500), atMS(59_500), 1000)...)
	e := b.prompt(at(60), event.ViaHook, "s_1", "p_1", "t_p", 30)
	b.samples(at(0), at(90))
	got := b.run(t, Device{})
	t.Run("rule 4: an IDE extension has no TTY and isn't headless", func(t *testing.T) {
		if tu, _ := turnByKey(got, "t_p"); tu.Origin == event.OriginHeadless {
			t.Error("the IDE session's prompt is headless")
		}
	})
	t.Run("rule 6: for an IDE harness, focus on the editor window in its project counts", func(t *testing.T) {
		if _, ok := anchorByID(got, e.EventID); !ok {
			t.Error("no anchor for the prompt typed in the IDE")
		}
	})
}

func TestHookAndTranscript(t *testing.T) {
	b := newTL(false)
	terminalSession(b)
	b.press(every(atMS(30_500), atMS(59_500), 1000)...)
	hook := b.prompt(at(60), event.ViaHook, "s_1", "p_1", "t_p", 30)
	b.prompt(atMS(60_400), event.ViaLog, "s_1", "p_1", "t_p", 30)
	b.samples(at(0), at(90))
	got := b.run(t, Device{})
	t.Run("a prompt a hook and a transcript line both saw is one turn", func(t *testing.T) {
		if len(got.Turns) != 1 {
			t.Errorf("%d turns, want 1", len(got.Turns))
		}
	})
	t.Run("the hook's record is the anchor, corroborated by the transcript (DESIGN 6.4)", func(t *testing.T) {
		if len(got.Anchors) != 1 {
			t.Fatalf("%d anchors, want 1", len(got.Anchors))
		}
		if a := got.Anchors[0]; a.ID != hook.EventID || !a.Corroborated || a.Via != event.ViaHook {
			t.Errorf("anchor %s via %s corroborated %v, want the hook's, corroborated", a.ID, a.Via, a.Corroborated)
		}
	})
}

func TestContinuationTurn(t *testing.T) {
	b := newTL(false)
	terminalSession(b)
	b.press(every(atMS(30_500), atMS(59_500), 1000)...)
	b.prompt(at(60), event.ViaHook, "s_1", "p_1", "t_1", 30)
	b.harness(at(100), event.ViaHook, "s_1", "p_1", &event.AgentStop{TurnKey: "t_1", StopReason: event.StopEndTurn})
	b.harness(at(130), event.ViaLog, "s_1", "p_1", &event.AgentOutput{TurnKey: "t_2", Words: 50})
	b.harness(at(160), event.ViaHook, "s_1", "p_1", &event.AgentStop{TurnKey: "t_2", StopReason: event.StopEndTurn, StopHookActive: true})
	b.samples(at(0), at(180))
	got := b.run(t, Device{})
	tu, ok := turnByKey(got, "t_2")
	t.Run("a turn key no prompt carries is a turn a stop hook continued, from the previous stop to its own", func(t *testing.T) {
		if !ok || tu.TSubmit != at(100) || tu.TStop != at(160) || tu.Origin != event.OriginSelfContinue {
			t.Errorf("turn %+v, want self_continue from %d to %d", tu, at(100), at(160))
		}
	})
	t.Run("a continued turn's output counts toward its words", func(t *testing.T) {
		if tu.WordsOut != 50 {
			t.Errorf("words_out %d, want 50", tu.WordsOut)
		}
	})
}

// backticked returns the `quoted` items of s, in order.
func backticked(s string) []string {
	var out []string
	for _, m := range regexp.MustCompile("`([^`]+)`").FindAllStringSubmatch(s, -1) {
		out = append(out, m[1])
	}
	return out
}

// TestGoAheadList checks the go-ahead list against SM 2.2's own text, the
// spec the list is copied from.
func TestGoAheadList(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "docs", "design", "struggle-metric.md"))
	if err != nil {
		t.Fatal(err)
	}
	_, rest, ok := strings.Cut(string(b), "on the go-ahead list (")
	list, picks, ok2 := strings.Cut(rest, "and a pick such as")
	picks, _, ok3 := strings.Cut(picks, ")")
	if !ok || !ok2 || !ok3 {
		t.Fatal("SM 2.2's go-ahead list isn't where this test looks for it")
	}
	t.Run("the go-ahead list is SM 2.2's, word for word and in order", func(t *testing.T) {
		if want := backticked(list); !slices.Equal(goAheads, want) {
			t.Errorf("goAheads = %q, SM has %q", goAheads, want)
		}
	})
	t.Run("every go-ahead SM lists is low effort, whatever its case and punctuation", func(t *testing.T) {
		for _, g := range backticked(list) {
			for _, text := range []string{g, strings.ToUpper(g) + "!!", " " + g + "."} {
				if !LowEffort(text) {
					t.Errorf("LowEffort(%q) = false", text)
				}
			}
		}
	})
	t.Run("every pick SM gives as an example is low effort", func(t *testing.T) {
		got := backticked(picks)
		if len(got) == 0 {
			t.Fatal("no picks found")
		}
		for _, p := range got {
			if !LowEffort(p) {
				t.Errorf("LowEffort(%q) = false", p)
			}
		}
	})
}

func TestLowEffort(t *testing.T) {
	cases := []struct {
		name string
		text string
		want bool
	}{
		{"a go-ahead with capitals and punctuation normalizes onto the list", "Go ahead!", true},
		{"spaces are stripped, so a spaced-out go-ahead matches", "keep   going", true},
		{"under four characters after normalizing is low effort", "k.", true},
		{"a pick by number is low effort", "option 12", true},
		{"a bare number is a pick", "2", true},
		{"the word option alone is not a pick", "option", false},
		{"a go-ahead inside a longer request is not low effort", "yes, but use a min-heap", false},
		{"SM 2.2's short substantive example is not low effort", "use a min-heap for the queue", false},
		{"SM 6.4's morning prompt is not low effort", "nice. explain what you changed in the service worker and why", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := LowEffort(c.text); got != c.want {
				t.Errorf("LowEffort(%q) = %v, want %v", c.text, got, c.want)
			}
		})
	}
}
