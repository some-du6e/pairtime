// SPDX-License-Identifier: AGPL-3.0-or-later

package score

import (
	"bytes"
	"cmp"
	"fmt"
	"reflect"
	"slices"
	"testing"

	"github.com/some-du6e/pairtime/pkg/event"
)

// TestPromptClass builds TestOrigin's class cases and checks that
// PromptClass, given Questions' asked from the records before the prompt,
// gives the class the scorer gives the same prompt in Step.
func TestPromptClass(t *testing.T) {
	cases := []promptCase{
		{name: "class substantive: a prompt of SUBSTANTIVE_CHARS or more typed characters",
			class: event.ClassSubstantive},
		{name: "class answer: the agent's last output asked a question",
			edit: func(b *tl, p *event.PromptSubmit) {
				b.harness(at(20), event.ViaLog, "s_1", "p_1", &event.AgentOutput{TurnKey: "t_0", Words: 30, IsFinal: true, AsksQuestion: true})
			},
			class: event.ClassAnswer},
		{name: "class answer: a turn that stopped on a question makes the next prompt an answer",
			edit: func(b *tl, p *event.PromptSubmit) {
				b.harness(at(20), event.ViaHook, "s_1", "p_1", &event.AgentStop{TurnKey: "t_0", StopReason: event.StopQuestion})
			},
			class: event.ClassAnswer},
		{name: "class answer: a go-ahead that answers a question is an answer",
			edit: func(b *tl, p *event.PromptSubmit) {
				p.LenChars, p.TypedLenEst, p.LowEffort = 2, 2, true
				b.harness(at(20), event.ViaLog, "s_1", "p_1", &event.AgentOutput{TurnKey: "t_0", Words: 30, IsFinal: true, AsksQuestion: true})
			},
			class: event.ClassAnswer},
		{name: "class answer: a prompt the harness marks as answering a question dialog",
			edit:  func(b *tl, p *event.PromptSubmit) { p.AnswersQuestion = true },
			class: event.ClassAnswer},
		{name: "class substantive: only the last output counts, and a later one that asks nothing ends the answer",
			edit: func(b *tl, p *event.PromptSubmit) {
				b.harness(at(10), event.ViaLog, "s_1", "p_1", &event.AgentOutput{TurnKey: "t_0", Words: 30, AsksQuestion: true})
				b.harness(at(20), event.ViaLog, "s_1", "p_1", &event.AgentOutput{TurnKey: "t_0", Words: 30, IsFinal: true})
			},
			class: event.ClassSubstantive},
		{name: "class low_effort: the text test the agent applied makes a go-ahead",
			edit:  func(b *tl, p *event.PromptSubmit) { p.LowEffort = true },
			class: event.ClassLowEffort},
		{name: "class low_effort: under SUBSTANTIVE_CHARS typed characters",
			edit:  func(b *tl, p *event.PromptSubmit) { p.LenChars, p.TypedLenEst = 19, 19 },
			class: event.ClassLowEffort},
		{name: "class substantive: exactly SUBSTANTIVE_CHARS typed characters",
			edit:  func(b *tl, p *event.PromptSubmit) { p.LenChars, p.TypedLenEst = 20, 20 },
			class: event.ClassSubstantive},
		{name: "class error_paste: an error signature and under ERROR_PASTE_TEXT_MAX typed characters around it",
			edit: func(b *tl, p *event.PromptSubmit) {
				p.LenChars, p.PastedChars, p.TypedLenEst, p.ErrorSig = 330, 300, 30, "e_assert"
			},
			class: event.ClassErrorPaste},
		{name: "class substantive: an error signature with ERROR_PASTE_TEXT_MAX typed characters is a hypothesis",
			edit: func(b *tl, p *event.PromptSubmit) {
				p.LenChars, p.PastedChars, p.TypedLenEst, p.ErrorSig = 340, 300, 40, "e_assert"
			},
			class: event.ClassSubstantive},
		{name: "class low_effort: a plugin's stop on a question doesn't make a hooked session's go-ahead an answer",
			edit: func(b *tl, p *event.PromptSubmit) {
				p.LenChars, p.TypedLenEst, p.LowEffort = 2, 2, true
				b.harness(at(20), event.ViaPlugin, "s_1", "p_1", &event.AgentStop{TurnKey: "t_0", StopReason: event.StopQuestion})
			},
			class: event.ClassLowEffort},
		{name: "class low_effort: a plugin's output asking a question doesn't make a hooked session's go-ahead an answer",
			edit: func(b *tl, p *event.PromptSubmit) {
				p.LenChars, p.TypedLenEst, p.LowEffort = 2, 2, true
				b.harness(at(20), event.ViaPlugin, "s_1", "p_1", &event.AgentOutput{TurnKey: "t_0", Words: 30, IsFinal: true,
					AsksQuestion: true})
			},
			class: event.ClassLowEffort},
		{name: "class answer: a question in another session doesn't count, and one in this session does",
			edit: func(b *tl, p *event.PromptSubmit) {
				b.harness(at(10), event.ViaLog, "s_1", "p_1", &event.AgentOutput{TurnKey: "t_0", Words: 30, IsFinal: true, AsksQuestion: true})
				b.harness(at(20), event.ViaLog, "s_2", "p_1", &event.AgentOutput{TurnKey: "t_9", Words: 30, IsFinal: true})
			},
			class: event.ClassAnswer},
		{name: "class low_effort: a plugin's question before a hook prompt that is its session's first hook record leaves a go-ahead low_effort, in a session with no hook session.start",
			edit: func(b *tl, p *event.PromptSubmit) {
				b.evs[1].Via = event.ViaPlugin // terminalSession's session.start
				p.LenChars, p.TypedLenEst, p.LowEffort = 2, 2, true
				b.harness(at(20), event.ViaPlugin, "s_1", "p_1", &event.AgentOutput{TurnKey: "t_0", Words: 30, IsFinal: true,
					AsksQuestion: true})
			},
			class: event.ClassLowEffort},
		{name: "class answer: a plugin's question makes a plugin prompt an answer when no hook or log record came before it, though one comes after",
			edit: func(b *tl, p *event.PromptSubmit) {
				b.evs[1].Via = event.ViaPlugin // terminalSession's session.start
				b.evs[2].Via = event.ViaPlugin // the prompt
				p.LenChars, p.TypedLenEst, p.LowEffort = 2, 2, true
				b.harness(at(20), event.ViaPlugin, "s_1", "p_1", &event.AgentOutput{TurnKey: "t_0", Words: 30, IsFinal: true,
					AsksQuestion: true})
				b.harness(at(90), event.ViaHook, "s_1", "p_1", &event.SessionStart{StartKind: event.StartResume,
					Entrypoint: event.EntrypointCLI, ParentClass: event.ParentTerminalApp, HasTTY: true})
			},
			class: event.ClassAnswer},
		{name: "class substantive: an asking output numbered after the prompt but dated before it doesn't make the prompt an answer",
			edit: func(b *tl, p *event.PromptSubmit) {
				o := b.harness(at(50), event.ViaLog, "s_1", "p_1", &event.AgentOutput{TurnKey: "t_0", Words: 30, IsFinal: true,
					AsksQuestion: true})
				b.writtenAt(o, at(61))
			},
			class: event.ClassSubstantive},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b, e := c.build()
			tu, ok := turnByKey(b.run(t, Device{}), "t_p")
			if !ok {
				t.Fatal("no turn for the prompt")
			}
			if tu.PromptClass != c.class {
				t.Fatalf("the scorer gives class %q, and the case expects %q", tu.PromptClass, c.class)
			}
			// The agent observes the records in the order it writes them,
			// by seq, and classes the prompt by its own record's via.
			recs := b.records()
			slices.SortFunc(recs, func(x, y event.Event) int { return cmp.Compare(x.Seq, y.Seq) })
			var q Questions
			var prompt *event.Event
			for i, r := range recs {
				if r.EventID == e.EventID {
					prompt = &recs[i]
					break
				}
				q.Observe(r)
			}
			if prompt == nil {
				t.Fatal("the prompt isn't among the records")
			}
			if got := PromptClass(testTable(t), prompt.Data.(*event.PromptSubmit), q.Asked(prompt.SessionKey, prompt.Via)); got != tu.PromptClass {
				t.Errorf("PromptClass gives %q, and the scorer %q", got, tu.PromptClass)
			}
		})
	}
	t.Run("class answer: a plugin prompt after a plugin's question is an answer whether its session's first hook record comes in the prompt's Step or a later one", func(t *testing.T) {
		// A plugin prompt after a plugin's question, in a session whose
		// first hook record comes 950 s later, in a later Step.
		b := newTL(false)
		b.focus(at(0), event.AppTerminal, "p_1", "s_1")
		b.press(every(atMS(30_500), atMS(59_500), 1000)...)
		b.harness(at(20), event.ViaPlugin, "s_1", "p_1", &event.AgentOutput{TurnKey: "t_0", Words: 30, IsFinal: true,
			AsksQuestion: true})
		e := b.harness(at(60), event.ViaPlugin, "s_1", "p_1", &event.PromptSubmit{TurnKey: "t_p", LenChars: 2, TypedLenEst: 2,
			LowEffort: true, OriginDeclared: event.DeclaredNone})
		b.samples(at(0), at(1000))
		b.start(at(1010), "s_1", "p_1", event.EntrypointCLI, event.ParentTerminalApp, true)
		b.samples(at(1015), at(1100))
		recs := b.records()
		i := slices.IndexFunc(recs, func(r event.Event) bool { return r.T >= at(1010) })
		_, outs := stepBatches(t, [][]event.Event{recs[:i], recs[i:]})
		one := scoreAll(t, Device{}, recs)
		want, ok := anchorByID(one, e.EventID)
		if !ok || want.PromptClass != event.ClassAnswer {
			t.Fatalf("one Step's anchor for the prompt is %+v (found %v), want an answer's", want, ok)
		}
		got, ok := anchorByID(outs[0].Final, e.EventID)
		if !ok {
			t.Fatalf("the prompt's anchor isn't final after the first Step, whose FinalBefore is %d s", (outs[0].FinalBefore-base)/1000)
		}
		if g, w := marshal(t, got), marshal(t, want); !bytes.Equal(g, w) {
			t.Errorf("the first Step's final anchor differs from one Step's %s", firstDiff(g, w))
		}
	})
}

// TestQuestions checks Questions' record-by-record rule on sessions the
// scorer gives no human prompt, such as one only a plugin reports.
func TestQuestions(t *testing.T) {
	rec := func(v event.Via, sess string, p event.Payload) event.Event {
		h := event.HarnessClaudeCode
		if sess == event.NoSession {
			h = event.HarnessNone
		}
		return event.Event{Envelope: event.Envelope{Via: v, Harness: h, SessionKey: sess}, Data: p}
	}
	asks := &event.AgentOutput{TurnKey: "t_1", AsksQuestion: true}
	tells := &event.AgentOutput{TurnKey: "t_1"}
	stopsOnQuestion := &event.AgentStop{TurnKey: "t_1", StopReason: event.StopQuestion}
	stopsAtEnd := &event.AgentStop{TurnKey: "t_1", StopReason: event.StopEndTurn}
	cases := []struct {
		name string
		recs []event.Event
		// via is the path the prompt that reads asked comes by.
		via  event.Via
		want bool
	}{
		{"a session with no records has asked nothing", nil, event.ViaHook, false},
		{"an output that asks a question counts for a plugin prompt in a session only a plugin reports",
			[]event.Event{rec(event.ViaPlugin, "s_1", asks)}, event.ViaPlugin, true},
		{"a stop on a question counts for a bridge prompt in a session only the bridge reports",
			[]event.Event{rec(event.ViaBridge, "s_1", stopsOnQuestion)}, event.ViaBridge, true},
		{"a hook record drops the side-door outputs seen before it",
			[]event.Event{rec(event.ViaPlugin, "s_1", asks), rec(event.ViaHook, "s_1", stopsAtEnd)}, event.ViaPlugin, false},
		{"after a hook record, a side-door stop on a question doesn't count",
			[]event.Event{rec(event.ViaLog, "s_1", tells), rec(event.ViaTrack, "s_1", stopsOnQuestion)}, event.ViaPlugin, false},
		{"an output that asked stays asked across a stop that didn't",
			[]event.Event{rec(event.ViaLog, "s_1", asks), rec(event.ViaHook, "s_1", stopsAtEnd)}, event.ViaHook, true},
		{"a later output that asks nothing ends it",
			[]event.Event{rec(event.ViaLog, "s_1", asks), rec(event.ViaLog, "s_1", tells)}, event.ViaHook, false},
		{"another session's question doesn't count",
			[]event.Event{rec(event.ViaLog, "s_2", asks)}, event.ViaHook, false},
		{"records of no session change nothing",
			[]event.Event{rec(event.ViaLog, "s_1", asks), rec(event.ViaOS, event.NoSession, &event.OSPower{Op: event.PowerLock})},
			event.ViaHook, true},
		{"a record with no payload changes nothing",
			[]event.Event{rec(event.ViaLog, "s_1", asks), rec(event.ViaLog, "s_1", (*event.AgentOutput)(nil))}, event.ViaHook, true},
		{"a hook prompt reads no side door's question in a session with no hook or log record yet",
			[]event.Event{rec(event.ViaPlugin, "s_1", asks)}, event.ViaHook, false},
		{"after a log record, a plugin prompt reads no side door's question",
			[]event.Event{rec(event.ViaPlugin, "s_1", asks), rec(event.ViaLog, "s_1", &event.AgentTool{ToolClass: event.ToolBash})},
			event.ViaPlugin, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var q Questions
			for _, r := range c.recs {
				q.Observe(r)
			}
			if got := q.Asked("s_1", c.via); got != c.want {
				t.Errorf("Asked = %v, want %v", got, c.want)
			}
		})
	}

	t.Run("past questionSessions sessions, the one heard from least recently is forgotten", func(t *testing.T) {
		var q Questions
		q.Observe(rec(event.ViaLog, "s_old", asks))
		q.Observe(rec(event.ViaLog, "s_kept", asks))
		for i := range questionSessions - 2 {
			q.Observe(rec(event.ViaLog, fmt.Sprintf("s_%d", i), tells))
		}
		q.Observe(rec(event.ViaLog, "s_kept", asks))
		q.Observe(rec(event.ViaLog, "s_new", asks))
		if len(q.sessions) != questionSessions {
			t.Fatalf("remembers %d sessions, want %d", len(q.sessions), questionSessions)
		}
		hook := event.ViaHook
		if q.Asked("s_old", hook) || !q.Asked("s_kept", hook) || !q.Asked("s_new", hook) {
			t.Errorf("asked: s_old %v, s_kept %v, s_new %v; want false, true, true",
				q.Asked("s_old", hook), q.Asked("s_kept", hook), q.Asked("s_new", hook))
		}
	})
}

// TestTrackClass checks that TrackClass gives a `pairtime track` call the
// class of the anchor the scorer opens for it.
func TestTrackClass(t *testing.T) {
	cases := []struct {
		name    string
		textLen int
		label   string
		// lowEffort and errorSig are what the ledger writer derives from the
		// label (DESIGN 5.4).
		lowEffort bool
		errorSig  string
		// asks puts a side door's output asking a question in the session
		// before the call.
		asks bool
		want event.PromptClass
	}{
		{name: "a call of 30 characters is substantive", textLen: 30, want: event.ClassSubstantive},
		{name: "a call of exactly SUBSTANTIVE_CHARS characters is substantive", textLen: 20, want: event.ClassSubstantive},
		{name: "a call under SUBSTANTIVE_CHARS characters is a go-ahead", textLen: 19, want: event.ClassLowEffort},
		{name: "a call of two characters is a go-ahead", textLen: 2, label: "ok", lowEffort: true, want: event.ClassLowEffort},
		{name: "a go-ahead padded to 30 characters is a go-ahead", textLen: 30, label: "keep going!!!!!!!!!!!!!!!!!!!!", lowEffort: true,
			want: event.ClassLowEffort},
		{name: "an error line of 25 characters is an error paste", textLen: 25, label: "TypeError: x is undefined", errorSig: "e_type",
			want: event.ClassErrorPaste},
		{name: "still broken above an error line, 38 characters, is an error paste", textLen: 38, label: "still broken\nTypeError: x is undefined",
			errorSig: "e_type", want: event.ClassErrorPaste},
		{name: "an error paste of ERROR_PASTE_TEXT_MAX characters or more is substantive", textLen: 40,
			label: "TypeError: x is undefined\nfails in prod!", errorSig: "e_type", want: event.ClassSubstantive},
		{name: "an error line under SUBSTANTIVE_CHARS is an error paste, not a go-ahead", textLen: 10, label: "ValueError", errorSig: "e_value",
			want: event.ClassErrorPaste},
		{name: "a side door's output that asks a question doesn't make a call an answer", textLen: 30, asks: true, want: event.ClassSubstantive},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := trackScene(false)
			if c.asks {
				b.harness(at(50), event.ViaPlugin, "s_t", "p_1", &event.AgentOutput{TurnKey: "t_0", Words: 30, IsFinal: true,
					AsksQuestion: true})
			}
			tr := b.trackAs(at(60), "s_t", c.textLen, event.LogNoReader, c.label, c.lowEffort, c.errorSig)
			b.samples(at(0), at(120))
			a, ok := anchorByID(b.run(t, Device{}), tr.EventID)
			if !ok {
				t.Fatal("the call opened no anchor")
			}
			if a.PromptClass != c.want {
				t.Fatalf("the scorer's anchor has class %q, and the case expects %q", a.PromptClass, c.want)
			}
			if got := TrackClass(testTable(t), tr.Data.(*event.CLITrack)); got != a.PromptClass {
				t.Errorf("TrackClass gives %q, and the scorer %q", got, a.PromptClass)
			}
		})
	}
}

// TestRelayClass checks that RelayClass, given Questions' asked from the
// records before the relay.message, as the ledger writer has it, gives the
// class the scorer gives the relayed prompt the record witnesses, on a
// server with t3-away's timing (sentAt60).
func TestRelayClass(t *testing.T) {
	asks := func(b *tl) {
		b.harness(at(30), event.ViaHook, "s_1", "p_1", &event.AgentOutput{TurnKey: "t_0", Words: 30, IsFinal: true,
			AsksQuestion: true})
	}
	cases := []struct {
		name      string
		lenChars  int
		lowEffort bool
		before    func(b *tl)
		want      event.PromptClass
	}{
		{"a message of 90 typed characters is substantive", 90, false, nil, event.ClassSubstantive},
		{"a message of exactly SUBSTANTIVE_CHARS characters is substantive", 20, false, nil, event.ClassSubstantive},
		{"a message under SUBSTANTIVE_CHARS characters is a go-ahead", 19, false, nil, event.ClassLowEffort},
		{"a message the reader's go-ahead test marked is a go-ahead, whatever its length", 30, true, nil, event.ClassLowEffort},
		{"a message after an output of its session that asked a question is an answer, a go-ahead included", 3, true, asks, event.ClassAnswer},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := newTL(false)
			appServer(b)
			if c.before != nil {
				c.before(b)
			}
			m := appMessage(at(60), c.lenChars)
			m.LowEffort = c.lowEffort
			_, r := sentAt60(b, m, c.lenChars)
			tu, ok := turnByKey(b.run(t, Device{}), "t_1")
			if !ok || tu.Origin != event.OriginRelayed {
				t.Fatalf("turn %+v (found: %v), want a relayed one", tu, ok)
			}
			if tu.PromptClass != c.want {
				t.Fatalf("the scorer gives class %q, and the case expects %q", tu.PromptClass, c.want)
			}
			var q Questions
			var rec *event.RelayMessage
			for _, e := range b.records() {
				if e.EventID == r.EventID {
					rec = e.Data.(*event.RelayMessage)
					break
				}
				q.Observe(e)
			}
			if rec == nil {
				t.Fatal("the relay.message isn't among the records")
			}
			if got := RelayClass(testTable(t), rec, q.Asked(r.SessionKey, r.Via)); got != tu.PromptClass {
				t.Errorf("RelayClass gives %q, and the scorer %q", got, tu.PromptClass)
			}
		})
	}
}

// TestQuestionsState checks that a Questions restored from another's State
// classes the prompts after it as that one does, and that Restore refuses a
// state no Questions could have.
func TestQuestionsState(t *testing.T) {
	rec := func(v event.Via, sess string, p event.Payload) event.Event {
		return event.Event{Envelope: event.Envelope{Via: v, Harness: event.HarnessClaudeCode, SessionKey: sess}, Data: p}
	}
	asks := &event.AgentOutput{TurnKey: "t_1", AsksQuestion: true}
	tells := &event.AgentOutput{TurnKey: "t_1"}
	before := []event.Event{
		rec(event.ViaPlugin, "s_plugin", asks),
		rec(event.ViaLog, "s_log", asks),
		rec(event.ViaPlugin, "s_mixed", asks),
		rec(event.ViaHook, "s_mixed", &event.AgentStop{TurnKey: "t_1", StopReason: event.StopEndTurn}),
	}
	// After the restore: one more record each in two sessions, and
	// enough new sessions to make both forget their oldest.
	after := []event.Event{rec(event.ViaTrack, "s_mixed", &event.AgentStop{TurnKey: "t_1", StopReason: event.StopQuestion}),
		rec(event.ViaLog, "s_log", tells)}
	for i := range questionSessions - 2 {
		after = append(after, rec(event.ViaLog, fmt.Sprintf("s_%d", i), asks))
	}

	var live, restored Questions
	for _, r := range before {
		live.Observe(r)
	}
	if err := restored.Restore(live.State()); err != nil {
		t.Fatal(err)
	}
	t.Run("a restored Questions remembers what the one it came from does", func(t *testing.T) {
		if !reflect.DeepEqual(restored.State(), live.State()) {
			t.Errorf("restored %+v, from %+v", restored.State(), live.State())
		}
	})
	for _, r := range after {
		live.Observe(r)
		restored.Observe(r)
	}
	t.Run("after the same records, both ask the same and forget the same sessions", func(t *testing.T) {
		for _, s := range []string{"s_plugin", "s_log", "s_mixed", "s_0"} {
			for _, v := range []event.Via{event.ViaHook, event.ViaPlugin} {
				if live.Asked(s, v) != restored.Asked(s, v) {
					t.Errorf("session %s, a prompt by %s: asked %v live and %v restored", s, v, live.Asked(s, v), restored.Asked(s, v))
				}
			}
		}
		if !reflect.DeepEqual(restored.State(), live.State()) {
			t.Errorf("the restored state went another way than the live one")
		}
	})

	good := live.State()
	cases := []struct {
		name string
		edit func(st *QuestionsState)
	}{
		{"a session with no key is refused", func(st *QuestionsState) { st.Sessions[0].Key = "" }},
		{"a session listed twice is refused", func(st *QuestionsState) { st.Sessions[1].Key = st.Sessions[0].Key }},
		{"a session heard from after the clock is refused", func(st *QuestionsState) { st.Sessions[0].Seen = st.Clock + 1 }},
		{"two sessions heard from at once are refused", func(st *QuestionsState) { st.Sessions[1].Seen = st.Sessions[0].Seen }},
		{"more sessions than a Questions remembers are refused", func(st *QuestionsState) {
			st.Clock++
			st.Sessions = append(st.Sessions, AskedSession{Key: "s_extra", Seen: st.Clock})
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := good
			st.Sessions = slices.Clone(good.Sessions)
			c.edit(&st)
			var q Questions
			if err := q.Restore(st); err == nil {
				t.Error("Restore took it")
			}
		})
	}
}
