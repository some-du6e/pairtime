// SPDX-License-Identifier: AGPL-3.0-or-later

package score

import (
	"slices"
	"testing"

	"github.com/some-du6e/pairtime/pkg/event"
)

// sideDoorPrompt is a prompt typed from 30.5 s and submitted at 60 s in a
// session whose records all arrive via v.
func sideDoorPrompt(keys bool, v event.Via) (*tl, event.Event) {
	b := newTL(keys)
	b.focus(at(0), event.AppTerminal, "p_1", "s_1")
	b.harness(at(0), v, "s_1", "p_1", &event.SessionStart{StartKind: event.StartStartup, Entrypoint: event.EntrypointCLI,
		ParentClass: event.ParentTerminalApp, HasTTY: true})
	b.press(every(atMS(30_500), atMS(59_500), 1000)...)
	e := b.prompt(at(60), v, "s_1", "p_1", "t_p", 30)
	b.samples(at(0), at(90))
	return b, e
}

func TestLevels(t *testing.T) {
	cases := []struct {
		name   string
		keys   bool
		dev    Device
		via    event.Via
		level  event.Evidence
		labels []event.Label
		lead   int64 // seconds
	}{
		{"a hook prompt with key presses in its KEYS_PRE_S is level A", true, Device{}, event.ViaHook,
			event.EvidenceA, nil, 140},
		{"key presses from the typing module add its label and keep level A", true, Device{TypingModule: true}, event.ViaHook,
			event.EvidenceA, []event.Label{event.LabelTypingModule}, 140},
		{"where the OS reports no key presses, a hook prompt is level B", false, Device{}, event.ViaHook,
			event.EvidenceB, nil, 140},
		{"on a VM the typing module can't make an anchor level A (DESIGN 6.2 D17)", true, Device{TypingModule: true, VM: true},
			event.ViaHook, event.EvidenceB, []event.Label{event.LabelTypingModule, event.LabelVM}, 140},
		{"a plugin prompt with key presses in the host window is level A, labeled via_plugin", true, Device{}, event.ViaPlugin,
			event.EvidenceA, []event.Label{event.LabelViaPlugin}, 140},
		{"a plugin prompt without key presses is level C, labeled via_plugin (DESIGN 6.2 D2)", false, Device{}, event.ViaPlugin,
			event.EvidenceC, []event.Label{event.LabelViaPlugin}, 140},
		{"a bridge prompt has the typing lead only and is level C, labeled via_bridge (DESIGN 6.2 D4)", false, Device{},
			event.ViaBridge, event.EvidenceC, []event.Label{event.LabelViaBridge}, 20},
		{"an unverified bridge prompt is level C and labeled via_bridge too", false, Device{}, event.ViaBridgeUnverified,
			event.EvidenceC, []event.Label{event.LabelViaBridge}, 20},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b, e := sideDoorPrompt(c.keys, c.via)
			a, ok := anchorByID(b.run(t, c.dev), e.EventID)
			if !ok {
				t.Fatal("no anchor")
			}
			if a.Level != c.level || !slices.Equal(a.Labels, c.labels) || a.LeadMS != c.lead*1000 {
				t.Errorf("level %s labels %q lead %d ms, want %s, %q and %d s", a.Level, a.Labels, a.LeadMS, c.level, c.labels, c.lead)
			}
			if err := a.Event().Validate(); err != nil {
				t.Errorf("the anchor isn't a valid event.Anchor: %v", err)
			}
		})
	}
}

func TestSideDoorCrossChecks(t *testing.T) {
	t.Run("a plugin prompt a log reader holds within TRACK_LOG_MATCH_S is a cross-check: the log record's anchor counts", func(t *testing.T) {
		b := newTL(false)
		terminalSession(b)
		b.press(every(atMS(30_500), atMS(54_500), 1000)...)
		logged := b.prompt(at(55), event.ViaLog, "s_1", "p_1", "t_log", 30)
		b.prompt(at(60), event.ViaPlugin, "s_1", "p_1", "t_plugin", 30)
		b.samples(at(0), at(90))
		got := b.run(t, Device{})
		if len(got.Anchors) != 1 || got.Anchors[0].ID != logged.EventID {
			t.Errorf("anchors %+v, want only the log record's", got.Anchors)
		}
		if _, ok := turnByKey(got, "t_plugin"); ok || len(got.Turns) != 1 {
			t.Errorf("turns %+v, want only the logged one", got.Turns)
		}
	})
	t.Run("a bridge prompt in a session a hook sees is a cross-check, not a turn (DESIGN 5.7)", func(t *testing.T) {
		b := newTL(false)
		terminalSession(b)
		b.press(every(atMS(30_500), atMS(59_500), 1000)...)
		b.prompt(at(60), event.ViaBridge, "s_1", "p_1", "t_bridge", 30)
		b.harness(at(70), event.ViaBridge, "s_1", "p_1", &event.AgentOutput{TurnKey: "t_bridge", Words: 40})
		b.samples(at(0), at(90))
		got := b.run(t, Device{})
		if len(got.Anchors) != 0 || len(got.Turns) != 0 {
			t.Errorf("anchors %+v turns %+v, want none", got.Anchors, got.Turns)
		}
	})
}

// TestPermissionReports checks that the reports of one permission answer
// from two code paths are one act (SM 1.2, DESIGN 6.4).
func TestPermissionReports(t *testing.T) {
	type report struct {
		via      event.Via
		req, ans int64 // milliseconds after base
	}
	cases := []struct {
		name         string
		reports      []report
		anchors      int
		corroborated bool
	}{
		{"the hook's and Claude's OTel span's reports of one answer, a few milliseconds apart, are one PA anchor, corroborated",
			[]report{{event.ViaHook, 43_120, 58_480}, {event.ViaOTel, 43_118, 58_476}}, 1, true},
		{"two answers whose times differ by PA_MIN_LATENCY_MS are two acts",
			[]report{{event.ViaHook, 43_000, 50_000}, {event.ViaHook, 43_300, 50_300}}, 2, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := newTL(false)
			terminalSession(b)
			b.press(atMS(55_500))
			var first event.Event
			for i, r := range c.reports {
				e := b.harness(atMS(r.ans), r.via, "s_1", "p_1", &event.AgentPermission{RequestedAt: atMS(r.req),
					AnsweredAt: atMS(r.ans), Decision: event.DecisionAccept, DecidedBy: event.DecidedByUser})
				if i == 0 {
					first = e
				}
			}
			b.samples(at(0), at(90))
			d := b.run(t, Device{})
			var pas []Anchor
			for _, a := range d.Anchors {
				if a.Kind == event.AnchorPermissionAnswer {
					pas = append(pas, a)
				}
			}
			if len(pas) != c.anchors {
				t.Fatalf("%d PA anchors %+v, want %d", len(pas), pas, c.anchors)
			}
			if c.corroborated && (pas[0].ID != first.EventID || !pas[0].Corroborated) {
				t.Errorf("anchor %+v, want the hook's, corroborated", pas[0])
			}
		})
	}
}

// actCase is one human act other than a prompt, at 58 s, with input at
// 55.5 s unless input says otherwise.
type actCase struct {
	name  string
	keys  bool
	app   event.AppClass
	input func(b *tl)
	act   func(b *tl) event.Event
	// want describes the anchor; kind "" means none.
	want Anchor
	flag string
}

func TestActs(t *testing.T) {
	edit := func(v event.Via, act event.EditorAct, src event.EditSource) func(b *tl) event.Event {
		return func(b *tl) event.Event { return b.editor(at(58), v, "p_1", act, src) }
	}
	pointer := func(b *tl) { b.mouse(atMS(55_500)) }
	permission := func(req, ans int64, by event.DecidedBy) func(b *tl) event.Event {
		return func(b *tl) event.Event {
			return b.harness(ans, event.ViaHook, "s_1", "p_1", &event.AgentPermission{RequestedAt: req, AnsweredAt: ans,
				Decision: event.DecisionAccept, DecidedBy: by})
		}
	}
	shell := func(class event.CmdClass, interactive bool) func(b *tl) event.Event {
		return func(b *tl) event.Event {
			return b.shell(at(460), "p_1", &event.ShellCommand{CmdClass: class, TStart: at(58), TEnd: at(458), Interactive: interactive})
		}
	}
	cases := []actCase{
		{name: "a typed edit, with input in its slot and the editor in focus on the project, is E with EDIT_LEAD_S",
			app: event.AppEditor, act: edit(event.ViaEditor, event.ActEdit, event.EditSourceHuman),
			want: Anchor{Kind: event.AnchorTypedEdit, Reason: event.ReasonEdit, LeadMS: 120_000, Level: event.EvidenceB}},
		{name: "a save of unknown source is a typed edit too",
			app: event.AppEditor, act: edit(event.ViaEditor, event.ActSave, event.EditSourceUnknown),
			want: Anchor{Kind: event.AnchorTypedEdit, Reason: event.ReasonEdit, LeadMS: 120_000, Level: event.EvidenceB}},
		{name: "an edit the agent made opens nothing",
			app: event.AppEditor, act: edit(event.ViaEditor, event.ActEdit, event.EditSourceAgent)},
		{name: "a pasted edit opens nothing",
			app: event.AppEditor, act: edit(event.ViaEditor, event.ActEdit, event.EditSourcePaste)},
		{name: "an edit while a terminal has focus opens nothing",
			app: event.AppTerminal, act: edit(event.ViaEditor, event.ActEdit, event.EditSourceHuman)},
		{name: "where the OS reports key presses, an edit needs one in its slot",
			keys: true, app: event.AppEditor, input: pointer, act: edit(event.ViaEditor, event.ActEdit, event.EditSourceHuman)},
		{name: "editor navigation needs any input in its slot, and with pointer input alone it is level B",
			keys: true, app: event.AppEditor, input: pointer, act: edit(event.ViaEditor, event.ActNavigate, event.EditSourceUnknown),
			want: Anchor{Kind: event.AnchorEditorNavigation, Reason: event.ReasonNavigate, LeadMS: 120_000, Level: event.EvidenceB}},
		{name: "an editor heartbeat that failed the peer-process check is level C, with no side-door label",
			app: event.AppEditor, act: edit(event.ViaEditorUnverified, event.ActEdit, event.EditSourceHuman),
			want: Anchor{Kind: event.AnchorTypedEdit, Reason: event.ReasonEdit, LeadMS: 120_000, Level: event.EvidenceC}},
		{name: "a permission answer by the person while present is PA: the wait as lead, PA_TRAIL_S after",
			app: event.AppTerminal, act: permission(at(43), at(58), event.DecidedByUser),
			want: Anchor{Kind: event.AnchorPermissionAnswer, Reason: event.ReasonPermission, LeadMS: 15_000, TrailMS: 10_000,
				Level: event.EvidenceB}},
		{name: "a wait over PA_MAX_S credits PA_MAX_S of it",
			app: event.AppTerminal, act: permission(at(-200), at(58), event.DecidedByUser),
			want: Anchor{Kind: event.AnchorPermissionAnswer, Reason: event.ReasonPermission, LeadMS: 120_000, TrailMS: 10_000,
				Level: event.EvidenceB}},
		{name: "an answer faster than PA_MIN_LATENCY_MS opens nothing and is flagged",
			app: event.AppTerminal, act: permission(atMS(57_750), at(58), event.DecidedByUser), flag: FlagPermissionTooFast},
		{name: "a permission the config decided opens nothing",
			app: event.AppTerminal, act: permission(at(43), at(58), event.DecidedByConfig)},
		{name: "an interrupt while present is INT with INT_LEAD_S and INT_TRAIL_S",
			app: event.AppTerminal, act: func(b *tl) event.Event {
				return b.harness(at(58), event.ViaHook, "s_1", "p_1", &event.AgentInterrupt{TurnKey: "t_0"})
			},
			want: Anchor{Kind: event.AnchorInterrupt, Reason: event.ReasonInterrupt, LeadMS: 30_000, TrailMS: 10_000,
				Level: event.EvidenceB}},
		{name: "a message queued with input in its slot and the host window in focus is Q, as a prompt of its length",
			app: event.AppTerminal, act: func(b *tl) event.Event {
				return b.harness(at(58), event.ViaHook, "s_1", "p_1", &event.AgentQueue{TurnKey: "t_q", LenChars: 40})
			},
			// 40 / 1.5 = 26.67 s, rounded to 27.
			want: Anchor{Kind: event.AnchorQueuedMessage, Reason: event.ReasonQueued, LeadMS: 147_000, Level: event.EvidenceB,
				Origin: event.OriginHumanTyped}},
		{name: "a message queued after the agent's last output asked a question takes an answer's lead, as a prompt would (SM 2.2)",
			app: event.AppTerminal, act: func(b *tl) event.Event {
				b.harness(at(40), event.ViaLog, "s_1", "p_1", &event.AgentOutput{TurnKey: "t_0", Words: 30, IsFinal: true,
					AsksQuestion: true})
				return b.harness(at(58), event.ViaHook, "s_1", "p_1", &event.AgentQueue{TurnKey: "t_q", LenChars: 40})
			},
			// ANSWER_LEAD_S and the 27 s of typing.
			want: Anchor{Kind: event.AnchorQueuedMessage, Reason: event.ReasonQueued, LeadMS: 60_000 + 27_000,
				Level: event.EvidenceB, Origin: event.OriginHumanTyped}},
		{name: "a queued message longer than its lead's input slots could type is human_pasted, its typing part cut by rule 7",
			app: event.AppTerminal, act: func(b *tl) event.Event {
				return b.harness(at(58), event.ViaHook, "s_1", "p_1", &event.AgentQueue{TurnKey: "t_q", LenChars: 450})
			},
			// One input slot: 15 s at 15 characters a second is 225, and
			// 225 / 1.5 = 150 s of typing.
			want: Anchor{Kind: event.AnchorQueuedMessage, Reason: event.ReasonQueued, LeadMS: 120_000 + 150_000,
				Level: event.EvidenceB, Origin: event.OriginHumanPasted}},
		{name: "an interactive command typed in a shell credits CMD_LEAD_S, its run up to CMD_WATCH_MAX_S and CMD_TRAIL_S after it",
			app: event.AppTerminal, act: shell(event.CmdRun, true),
			want: Anchor{Kind: event.AnchorTypedCommand, Reason: event.ReasonCommand, LeadMS: 60_000, WatchMS: 300_000,
				TrailMS: 60_000, Level: event.EvidenceB}},
		{name: "a typed test command is TEST",
			app: event.AppTerminal, act: shell(event.CmdTest, true),
			want: Anchor{Kind: event.AnchorTypedCommand, Reason: event.ReasonTest, LeadMS: 60_000, WatchMS: 300_000,
				TrailMS: 60_000, Level: event.EvidenceB}},
		{name: "a typed build command is TEST, as SM 5.2 defines it",
			app: event.AppTerminal, act: shell(event.CmdBuild, true),
			want: Anchor{Kind: event.AnchorTypedCommand, Reason: event.ReasonTest, LeadMS: 60_000, WatchMS: 300_000,
				TrailMS: 60_000, Level: event.EvidenceB}},
		{name: "a command that wasn't typed in an interactive shell opens nothing",
			app: event.AppTerminal, act: shell(event.CmdRun, false)},
		{name: "a human commit with input in its slot is G with COMMIT_LEAD_S",
			app: event.AppTerminal, act: func(b *tl) event.Event { return b.git(at(58), "p_1", event.ActorHuman) },
			want: Anchor{Kind: event.AnchorHumanCommit, Reason: event.ReasonCommit, LeadMS: 60_000, Level: event.EvidenceB}},
		{name: "an agent's commit opens nothing",
			app: event.AppTerminal, act: func(b *tl) event.Event { return b.git(at(58), "p_1", event.ActorAgent) }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := newTL(c.keys)
			b.focus(at(0), c.app, "p_1", "s_1")
			b.start(at(0), "s_1", "p_1", event.EntrypointCLI, event.ParentTerminalApp, true)
			if c.input != nil {
				c.input(b)
			} else {
				b.press(atMS(55_500))
			}
			e := c.act(b)
			b.samples(at(0), at(480))
			got := b.run(t, Device{})
			a, ok := anchorByID(got, e.EventID)
			switch {
			case c.want.Kind == "" && ok:
				t.Errorf("an anchor %+v, want none", a)
			case c.want.Kind != "" && !ok:
				t.Fatalf("no anchor, want %s", c.want.Kind)
			case ok:
				if a.Kind != c.want.Kind || a.Reason != c.want.Reason || a.LeadMS != c.want.LeadMS ||
					a.TrailMS != c.want.TrailMS || a.WatchMS != c.want.WatchMS || a.Level != c.want.Level ||
					(c.want.Origin != "" && a.Origin != c.want.Origin) {
					t.Errorf("anchor %+v, want kind %s reason %s lead %d trail %d watch %d level %s", a, c.want.Kind,
						c.want.Reason, c.want.LeadMS, c.want.TrailMS, c.want.WatchMS, c.want.Level)
				}
				if err := a.Event().Validate(); err != nil {
					t.Errorf("the anchor isn't a valid event.Anchor: %v", err)
				}
			}
			flagged := slices.ContainsFunc(got.Flags, func(f Flag) bool { return f.EventID == e.EventID && f.Name == c.flag })
			if c.flag != "" && !flagged {
				t.Errorf("flags %+v, want %s", got.Flags, c.flag)
			}
		})
	}
}

// TestAppSessionActs checks that a session an agent app's server started
// opens no queued message, permission answer or interrupt anchor (SM 2.2,
// What isn't a message; SM 2.3). Those acts come through the app's own
// interface, from any device and any person the app lets in, and the app's
// window here hosts every session its server drives, so its focus would
// pass them as the person's. The session's last session.start before the
// act decides, as it does for rule 4.
func TestAppSessionActs(t *testing.T) {
	queued := func(v event.Via) func(b *tl) event.Event {
		return func(b *tl) event.Event {
			return b.harness(at(58), v, "s_1", "p_1", &event.AgentQueue{TurnKey: "t_q", LenChars: 40})
		}
	}
	answered := func(req int64) func(b *tl) event.Event {
		return func(b *tl) event.Event {
			return b.harness(at(58), event.ViaHook, "s_1", "p_1", &event.AgentPermission{RequestedAt: req, AnsweredAt: at(58),
				Decision: event.DecisionAccept, DecidedBy: event.DecidedByUser})
		}
	}
	permission := answered(at(43))
	interrupt := func(b *tl) event.Event {
		return b.harness(at(58), event.ViaHook, "s_1", "p_1", &event.AgentInterrupt{TurnKey: "t_0"})
	}
	// resumed is T3 Code's window in front from base, hosting s_1, which
	// its server started (appWindow), and then, at 30 s, the person
	// resuming s_1 in a terminal, which takes the front.
	resumed := func(b *tl) {
		appWindow(b)
		b.focus(at(30), event.AppTerminal, "p_1", "s_1")
		b.harness(at(30), event.ViaHook, "s_1", "p_1", &event.SessionStart{StartKind: event.StartResume,
			Entrypoint: event.EntrypointCLI, ParentClass: event.ParentTerminalApp, HasTTY: true})
	}
	cases := []struct {
		name    string
		session func(b *tl)
		act     func(b *tl) event.Event
		// want is the act's anchor kind, or "" for none, and flag a flag
		// the act raises.
		want event.AnchorKind
		flag string
	}{
		{name: "a message queued in an agent app's session opens no Q anchor, though input in its slot and the app's window in front, hosting the session, back it",
			session: appWindow, act: queued(event.ViaHook)},
		{name: "a transcript's copy of a message queued in an agent app's session opens no Q anchor either",
			session: appWindow, act: queued(event.ViaLog)},
		{name: "a permission answered in an agent app's session opens no PA anchor, though the person was present when the answer came",
			session: appWindow, act: permission},
		{name: "a permission answered faster than PA_MIN_LATENCY_MS in an agent app's session opens nothing and is still flagged, since a script answered it",
			session: appWindow, act: answered(atMS(57_750)), flag: FlagPermissionTooFast},
		{name: "an interrupt in an agent app's session opens no INT anchor, though the person was present",
			session: appWindow, act: interrupt},
		{name: "the same queued message in a session a person started from a terminal opens Q",
			session: terminalSession, act: queued(event.ViaHook), want: event.AnchorQueuedMessage},
		{name: "the same permission answer in a session a person started from a terminal opens PA",
			session: terminalSession, act: permission, want: event.AnchorPermissionAnswer},
		{name: "the same interrupt in a session a person started from a terminal opens INT",
			session: terminalSession, act: interrupt, want: event.AnchorInterrupt},
		{name: "a message queued after the person resumed an agent app's session from a terminal opens Q: the session's last start before the act decides, as it does for rule 4",
			session: resumed, act: queued(event.ViaHook), want: event.AnchorQueuedMessage},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := newTL(false)
			c.session(b)
			b.press(atMS(55_500))
			e := c.act(b)
			b.samples(at(0), at(480))
			got := b.run(t, Device{})
			a, ok := anchorByID(got, e.EventID)
			switch {
			case c.want == "" && ok:
				t.Errorf("a %s anchor, want none", a.Kind)
			case c.want != "" && (!ok || a.Kind != c.want):
				t.Errorf("anchor %+v (found %v), want %s", a, ok, c.want)
			}
			flagged := slices.ContainsFunc(got.Flags, func(f Flag) bool { return f.EventID == e.EventID && f.Name == c.flag })
			if c.flag != "" && !flagged {
				t.Errorf("flags %+v, want %s", got.Flags, c.flag)
			}
			checkDerived(t, got)
		})
	}
}

// TestTypedEditFocus checks SC1's typed-edit focus test in
// D-editor-heartbeat-first's order: the editor in front is on the project of
// the latest heartbeat with input in the same or previous slot, up to the
// edit, a verified one ahead of a side door's (D-rule2-sidedoor), and on the
// focus record's only when there is none. An edit is such a heartbeat
// itself, so another project's heartbeat decides only when it is verified
// and the edit isn't.
func TestTypedEditFocus(t *testing.T) {
	// heartbeat is a navigation in p_1 at s seconds, with input just before
	// it, arriving via v.
	heartbeat := func(s int64, v event.Via) func(b *tl) {
		return func(b *tl) {
			b.mouse(at(s) - 500)
			b.editor(at(s), v, "p_1", event.ActNavigate, event.EditSourceUnknown)
		}
	}
	cases := []struct {
		name string
		// record is the project the editor's focus record names, before the
		// heartbeat and the edit, in p_2, at 58 s that arrives via via.
		record string
		before func(b *tl)
		via    event.Via
		// level is the edit's anchor's, or "" for none.
		level event.Evidence
	}{
		{"D-editor-heartbeat-first: an edit is typed in its project's window though the focus record names another, since its own heartbeat is the latest",
			"p_1", nil, event.ViaEditor, event.EvidenceB},
		{"D-editor-heartbeat-first: an edit just after a verified heartbeat of another project in its slot is still its own window's",
			"", heartbeat(56, event.ViaEditor), event.ViaEditor, event.EvidenceB},
		{"D-rule2-sidedoor: an unverified edit just after a verified heartbeat of another project in its slot opens nothing, since the verified one says which window is in front",
			"", heartbeat(56, event.ViaEditor), event.ViaEditorUnverified, ""},
		{"D-rule2-sidedoor: an unverified edit whose slot and the one before hold no verified heartbeat is its own window's, at level C",
			"", heartbeat(29, event.ViaEditor), event.ViaEditorUnverified, event.EvidenceC},
		{"D-rule2-sidedoor: another project's plugin heartbeat just before an unverified edit doesn't outrank the edit's own",
			"", heartbeat(56, event.ViaPlugin), event.ViaEditorUnverified, event.EvidenceC},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := newTL(false)
			b.focus(at(0), event.AppEditor, c.record)
			b.press(atMS(55_500))
			if c.before != nil {
				c.before(b)
			}
			e := b.editor(at(58), c.via, "p_2", event.ActEdit, event.EditSourceHuman)
			b.samples(at(0), at(90))
			a, ok := anchorByID(b.run(t, Device{}), e.EventID)
			switch {
			case c.level == "" && ok:
				t.Errorf("an anchor %+v, want none", a)
			case c.level != "" && !ok:
				t.Fatalf("no anchor, want a typed edit at level %s", c.level)
			case ok && (a.Kind != event.AnchorTypedEdit || a.Level != c.level):
				t.Errorf("anchor %s at level %s, want a typed edit at level %s", a.Kind, a.Level, c.level)
			}
		})
	}
}
