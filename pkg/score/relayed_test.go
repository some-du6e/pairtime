// SPDX-License-Identifier: AGPL-3.0-or-later

package score

import (
	"bytes"
	"math"
	"slices"
	"testing"

	"github.com/some-du6e/pairtime/pkg/event"
)

// The rule tests for relayed prompts, SM 2.2 rule 9 as D-relayed decides it
// (docs/errata/2026-11-22-relayed-prompts.md). testdata/timelines/relayed
// holds the oracle's cases. These pin each rule on its own, the ones no
// case exercises included: a link that fails a check, two links naming one
// record, the answer class, a lead an unpaired message caps, and a
// witnessed prompt outside an agent app's session.

// appServer starts session s_1 of p_1 at base as an agent app's own server
// starts the harness for the app's clients, as T3 Code's `t3 serve` starts
// Claude Code: entrypoint unknown, parent_class agent_app and no TTY (SM
// 1.2, DESIGN 5.1).
func appServer(b *tl) {
	b.start(at(0), "s_1", "p_1", event.EntrypointUnknown, event.ParentAgentApp, false)
}

// appWindow is appServer on a desktop that runs the app: its window, an
// agent_app, is in front from base, hosting s_1 on p_1, as T3 Code's
// desktop app hosts the sessions its own server drives (SM 3.2 rule 3).
func appWindow(b *tl) {
	b.focus(at(0), event.AppAgentApp, "p_1", "s_1")
	appServer(b)
}

// appMessage is a message a person sent from the app's web client in
// thread r_1, stamped sent, n typed characters long (SM 1.2).
func appMessage(sent int64, n int) *event.RelayMessage {
	return &event.RelayMessage{App: event.RelayAppT3Code, Client: event.RelayClientWeb, SentAt: sent, ThreadKey: "r_1",
		Declared: event.RelayDeclaredNone, LenChars: n}
}

// relayMsg adds the app reader's relay.message for m, written at t and
// bound to session sess, or to none.
func (b *tl) relayMsg(t int64, sess string, m *event.RelayMessage) event.Event {
	h := event.HarnessClaudeCode
	if sess == event.NoSession {
		h = event.HarnessNone
	}
	return b.emit(t, event.Envelope{Source: event.SourceLog, Via: event.ViaApp, Harness: h, SessionKey: sess,
		ProjectKey: "p_1"}, m)
}

// sentAt60 is t3-away's timing for message m, stamped at 60 s: its run's
// prompt, t_1 with n characters typed by the hook's estimate, reaches the
// agent at 61 s, and the reader's record, written at 63 s, names the
// prompt (testdata/timelines/relayed/t3-away).
func sentAt60(b *tl, m *event.RelayMessage, n int) (prompt, relay event.Event) {
	prompt = b.prompt(at(61), event.ViaHook, "s_1", "p_1", "t_1", n)
	m.Prompt = prompt.EventID
	return prompt, b.relayMsg(at(63), "s_1", m)
}

func relayByID(d Derived, id string) (RelayRecord, bool) {
	i := slices.IndexFunc(d.Relays, func(r RelayRecord) bool { return r.ID == id })
	if i < 0 {
		return RelayRecord{}, false
	}
	return d.Relays[i], true
}

// TestRelayPairing checks how a relay.message pairs with a prompt.submit
// (SM 2.2 rule 9): by the link the agent writes on whichever of the two
// came second, checked for its session and times, one to one, the first
// link in record order claiming both records. A record that pairs with
// nothing opens nothing and raises no flag, and its prompt goes by rules 1
// to 8, here rule 4, since the session is an agent app's.
func TestRelayPairing(t *testing.T) {
	promptAt := func(b *tl, s int64, v event.Via) event.Event {
		return b.prompt(at(s), v, "s_1", "p_1", "t_1", 90)
	}
	// naming adds, at seen seconds, the record of a 90-character message
	// stamped at 60 s, bound to session sess, that names prompt p, or none
	// for the zero Event.
	naming := func(b *tl, seen int64, sess string, p event.Event) event.Event {
		m := appMessage(at(60), 90)
		m.Prompt = p.EventID
		return b.relayMsg(at(seen), sess, m)
	}
	linkTo := func(p event.Event, id string) { p.Data.(*event.PromptSubmit).Relay = id }
	cases := []struct {
		name string
		// build adds the prompt t_1 and the relay records, and returns the
		// event id of the record the prompt pairs with, "" for none, and
		// those of the records that pair with nothing.
		build  func(b *tl) (string, []string)
		origin event.Origin
	}{
		{"a record naming the prompt, which came first, pairs with it and witnesses it, so it is relayed (SM 2.2 rule 9)",
			func(b *tl) (string, []string) {
				return naming(b, 63, "s_1", promptAt(b, 61, event.ViaHook)).EventID, nil
			}, event.OriginRelayed},
		{"a prompt naming the record, which came first, pairs with it",
			func(b *tl) (string, []string) {
				r := naming(b, 63, "s_1", event.Event{})
				linkTo(promptAt(b, 70, event.ViaHook), r.EventID)
				return r.EventID, nil
			}, event.OriginRelayed},
		{"a record and a prompt that name each other are one pairing",
			func(b *tl) (string, []string) {
				p := promptAt(b, 61, event.ViaHook)
				r := naming(b, 63, "s_1", p)
				linkTo(p, r.EventID)
				return r.EventID, nil
			}, event.OriginRelayed},
		{"a prompt RELAY_MATCH_S after the stamp pairs",
			func(b *tl) (string, []string) {
				r := naming(b, 63, "s_1", event.Event{})
				linkTo(promptAt(b, 660, event.ViaHook), r.EventID)
				return r.EventID, nil
			}, event.OriginRelayed},
		{"a prompt more than RELAY_MATCH_S after the stamp pairs with nothing: a message queued that long earns nothing (SM 2.3)",
			func(b *tl) (string, []string) {
				r := naming(b, 63, "s_1", event.Event{})
				linkTo(promptAt(b, 661, event.ViaHook), r.EventID)
				return "", []string{r.EventID}
			}, event.OriginHeadless},
		{"a prompt before the stamp pairs with nothing",
			func(b *tl) (string, []string) {
				return "", []string{naming(b, 63, "s_1", promptAt(b, 59, event.ViaHook)).EventID}
			}, event.OriginHeadless},
		{"a record RELAY_SEEN_LAG_S after the stamp pairs",
			func(b *tl) (string, []string) {
				return naming(b, 120, "s_1", promptAt(b, 61, event.ViaHook)).EventID, nil
			}, event.OriginRelayed},
		{"a record more than RELAY_SEEN_LAG_S after the stamp pairs with nothing: the reader saw the row too late, as it sees an imported one",
			func(b *tl) (string, []string) {
				return "", []string{naming(b, 121, "s_1", promptAt(b, 61, event.ViaHook)).EventID}
			}, event.OriginHeadless},
		{"a record bound to no session pairs with the prompt it names",
			func(b *tl) (string, []string) {
				return naming(b, 63, event.NoSession, promptAt(b, 61, event.ViaHook)).EventID, nil
			}, event.OriginRelayed},
		{"a record bound to another session pairs with nothing",
			func(b *tl) (string, []string) {
				return "", []string{naming(b, 63, "s_2", promptAt(b, 61, event.ViaHook)).EventID}
			}, event.OriginHeadless},
		{"a log reader's prompt pairs as a hook's does",
			func(b *tl) (string, []string) {
				return naming(b, 63, "s_1", promptAt(b, 61, event.ViaLog)).EventID, nil
			}, event.OriginRelayed},
		{"a plugin's prompt pairs with nothing: a relay record witnesses only a hook or log prompt (DESIGN 5.2)",
			func(b *tl) (string, []string) {
				return "", []string{naming(b, 63, "s_1", promptAt(b, 61, event.ViaPlugin)).EventID}
			}, event.OriginHeadless},
		{"a plugin's prompt that names the record claims nothing, since its sender wrote that link, so the hook's prompt after it still pairs (DESIGN 5.2)",
			func(b *tl) (string, []string) {
				r := naming(b, 63, "s_1", event.Event{})
				linkTo(b.prompt(at(65), event.ViaPlugin, "s_1", "p_1", "t_p", 90), r.EventID)
				linkTo(promptAt(b, 70, event.ViaHook), r.EventID)
				return r.EventID, nil
			}, event.OriginRelayed},
		{"the first link wins: a second record naming the same prompt pairs with nothing",
			func(b *tl) (string, []string) {
				p := promptAt(b, 61, event.ViaHook)
				first := naming(b, 63, "s_1", p)
				return first.EventID, []string{naming(b, 64, "s_1", p).EventID}
			}, event.OriginRelayed},
		{"the first link claims both records though it fails the checks, so a later link naming the prompt is void",
			func(b *tl) (string, []string) {
				p := promptAt(b, 61, event.ViaHook)
				first := naming(b, 63, "s_2", p)
				return "", []string{first.EventID, naming(b, 64, "s_1", p).EventID}
			}, event.OriginHeadless},
		{"a second prompt naming a record an earlier prompt's link claimed pairs with nothing, and goes by rule 4",
			func(b *tl) (string, []string) {
				r := naming(b, 63, "s_1", event.Event{})
				linkTo(b.prompt(at(70), event.ViaHook, "s_1", "p_1", "t_2", 90), r.EventID)
				linkTo(promptAt(b, 80, event.ViaHook), r.EventID)
				return "", nil
			}, event.OriginHeadless},
		{"a link naming a record of another kind claims nothing, so a later link pairs",
			func(b *tl) (string, []string) {
				p := promptAt(b, 61, event.ViaHook)
				linkTo(p, b.evs[0].EventID) // the session.start
				return naming(b, 63, "s_1", p).EventID, nil
			}, event.OriginRelayed},
		{"a link naming a record the state doesn't hold claims nothing",
			func(b *tl) (string, []string) {
				p := promptAt(b, 61, event.ViaHook)
				linkTo(p, "019a0000-0000-7000-8000-0000000fffff")
				return naming(b, 63, "s_1", p).EventID, nil
			}, event.OriginRelayed},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := newTL(false)
			appServer(b)
			want, void := c.build(b)
			d := b.run(t, Device{})
			tu, ok := turnByKey(d, "t_1")
			if !ok {
				t.Fatal("no turn for the prompt")
			}
			if tu.Origin != c.origin || tu.Relay != want {
				t.Errorf("origin %q paired with %q, want %q and %q", tu.Origin, tu.Relay, c.origin, want)
			}
			if want != "" {
				if rr, ok := relayByID(d, want); !ok || rr.Prompt != tu.Prompt || rr.Origin != c.origin {
					t.Errorf("relay record %+v, want one paired with %s, origin %q", rr, tu.Prompt, c.origin)
				}
			}
			for _, id := range void {
				if rr, ok := relayByID(d, id); !ok || rr.Prompt != "" || rr.Origin != "" {
					t.Errorf("relay record %+v (found %v), want one that paired with nothing", rr, ok)
				}
				if a, ok := anchorByID(d, id); ok {
					t.Errorf("record %s that paired with nothing opened a %s anchor", id, a.Kind)
				}
			}
			if len(d.Flags) != 0 {
				t.Errorf("flags %+v, want none: a record that pairs with nothing raises no flag", d.Flags)
			}
			checkDerived(t, d)
		})
	}
}

// TestRelayedOrigin checks SM 2.2's rules for a prompt an agent app's
// message paired with: the app's own declarations (rules 1 and 3), rule 4
// with agent_app and guest, the witness that skips rule 4, rules 6 and 7
// read at the app's stamp, which a phone message skips, and rule 9, with
// the length and class of the app's own text.
func TestRelayedOrigin(t *testing.T) {
	// onServer is message m relayed to a server that samples nothing, its
	// prompt n characters long by the hook's estimate (t3-away).
	onServer := func(m *event.RelayMessage, n int) func(b *tl) (event.Event, event.Event) {
		return func(b *tl) (event.Event, event.Event) {
			appServer(b)
			return sentAt60(b, m, n)
		}
	}
	// typedHere is message m typed into the app's window here, the keys
	// pressed from 30.5 s to 59.5 s, with t3-here's timing.
	typedHere := func(m *event.RelayMessage) func(b *tl) (event.Event, event.Event) {
		return func(b *tl) (event.Event, event.Event) {
			appWindow(b)
			b.press(every(atMS(30_500), atMS(59_500), 1000)...)
			p, r := sentAt60(b, m, 90)
			b.samples(at(0), at(180))
			return p, r
		}
	}
	// edit returns build with f applied to the prompt it adds.
	edit := func(build func(b *tl) (event.Event, event.Event), f func(p *event.PromptSubmit)) func(b *tl) (event.Event, event.Event) {
		return func(b *tl) (event.Event, event.Event) {
			p, r := build(b)
			f(p.Data.(*event.PromptSubmit))
			return p, r
		}
	}
	with := func(f func(m *event.RelayMessage)) *event.RelayMessage {
		m := appMessage(at(60), 90)
		f(m)
		return m
	}
	asks := func(b *tl) {
		b.harness(at(30), event.ViaHook, "s_1", "p_1", &event.AgentOutput{TurnKey: "t_0", Words: 30, IsFinal: true,
			AsksQuestion: true})
	}
	cases := []struct {
		name  string
		keys  bool
		build func(b *tl) (prompt, relay event.Event)
		// origin, class and typed are the turn's.
		origin event.Origin
		class  event.PromptClass
		typed  int64
		// anchor is the record the anchor stands on, "prompt" or "relay",
		// or "" for none, and atStamp says whether its lead ends at the
		// relay record's stamp rather than at its t.
		anchor  string
		atStamp bool
		kind    event.AnchorKind
		lead    int64 // seconds
	}{
		{name: "rule 9: a message typed on another computer witnesses its prompt on a server, which is relayed with the app's own length and class",
			build:  onServer(appMessage(at(60), 90), 90),
			origin: event.OriginRelayed, class: event.ClassSubstantive, typed: 90,
			anchor: "relay", atStamp: true, kind: event.AnchorSubstantivePrompt, lead: 60},
		{name: "rule 4 lists agent_app: a prompt of a session an app's server started that no message witnesses is headless",
			build: func(b *tl) (event.Event, event.Event) {
				appServer(b)
				return b.prompt(at(61), event.ViaHook, "s_1", "p_1", "t_1", 90), event.Event{}
			},
			origin: event.OriginHeadless},
		{name: "rule 4 takes such a prompt whatever window hosts it: typed into the app's window here with no record, it is headless (t3-here-agent)",
			keys: true,
			build: func(b *tl) (event.Event, event.Event) {
				appWindow(b)
				b.press(every(atMS(30_500), atMS(59_500), 1000)...)
				p := b.prompt(at(61), event.ViaHook, "s_1", "p_1", "t_1", 90)
				b.samples(at(0), at(180))
				return p, event.Event{}
			},
			origin: event.OriginHeadless},
		{name: "rules 6 and 7 at the stamp: typed into the app's window here, the witnessed prompt is human_typed, with its class's full lead ending at the stamp (t3-here)",
			keys: true, build: typedHere(appMessage(at(60), 90)),
			origin: event.OriginHumanTyped, class: event.ClassSubstantive, typed: 90,
			anchor: "prompt", atStamp: true, kind: event.AnchorSubstantivePrompt, lead: 120 + 60},
		{name: "rules 6 and 7 read the stamp, not the prompt's t: typing that starts after the stamp leaves a queued prompt relayed",
			keys: true,
			build: func(b *tl) (event.Event, event.Event) {
				appWindow(b)
				b.press(every(atMS(31_500), atMS(59_500), 1000)...)
				m := appMessage(at(30), 90)
				p := b.prompt(at(61), event.ViaHook, "s_1", "p_1", "t_1", 90)
				m.Prompt = p.EventID
				r := b.relayMsg(at(63), "s_1", m)
				b.samples(at(0), at(180))
				return p, r
			},
			origin: event.OriginRelayed, class: event.ClassSubstantive, typed: 90,
			anchor: "relay", atStamp: true, kind: event.AnchorSubstantivePrompt, lead: 60},
		{name: "rule 6 reads the focus at the stamp: a window that doesn't host the session then leaves the prompt relayed, though the app's window is in front at the prompt",
			keys: true,
			build: func(b *tl) (event.Event, event.Event) {
				b.focus(at(0), event.AppTerminal, "p_1")
				appServer(b)
				b.press(every(atMS(30_500), atMS(59_500), 1000)...)
				b.focus(atMS(60_500), event.AppAgentApp, "p_1", "s_1")
				p, r := sentAt60(b, appMessage(at(60), 90), 90)
				b.samples(at(0), at(180))
				return p, r
			},
			origin: event.OriginRelayed, class: event.ClassSubstantive, typed: 90,
			anchor: "relay", atStamp: true, kind: event.AnchorSubstantivePrompt, lead: 60},
		{name: "a message from the app's phone app skips rules 6 and 7: with typing and the app's window in front here, the prompt is relayed (t3-phone)",
			keys: true, build: typedHere(with(func(m *event.RelayMessage) { m.Client = event.RelayClientMobile })),
			origin: event.OriginRelayed, class: event.ClassSubstantive, typed: 90,
			anchor: "relay", atStamp: true, kind: event.AnchorSubstantivePrompt, lead: 60},
		{name: "a record for a terminal session witnesses nothing: rules 6 and 7 read the prompt at its own t, and its lead ends there",
			keys: true,
			build: func(b *tl) (event.Event, event.Event) {
				terminalSession(b)
				b.press(every(atMS(30_500), atMS(59_500), 1000)...)
				m := appMessage(at(20), 90)
				p := b.prompt(at(60), event.ViaHook, "s_1", "p_1", "t_1", 90)
				m.Prompt = p.EventID
				r := b.relayMsg(at(62), "s_1", m)
				b.samples(at(0), at(180))
				return p, r
			},
			origin: event.OriginHumanTyped, class: event.ClassSubstantive, typed: 90,
			anchor: "prompt", kind: event.AnchorSubstantivePrompt, lead: 120 + 60},
		{name: "a record for a terminal session witnesses nothing: with no input there its prompt is no_input, not relayed",
			build: func(b *tl) (event.Event, event.Event) {
				terminalSession(b)
				p := b.prompt(at(60), event.ViaHook, "s_1", "p_1", "t_1", 90)
				m := appMessage(at(20), 90)
				m.Prompt = p.EventID
				r := b.relayMsg(at(62), "s_1", m)
				b.samples(at(0), at(180))
				return p, r
			},
			origin: event.OriginNoInput},
		{name: "rule 4 takes a guest's prompt though a message witnesses it: another person's words don't credit the owner (t3-unattended)",
			build:  edit(onServer(appMessage(at(60), 90), 90), func(p *event.PromptSubmit) { p.Guest = true }),
			origin: event.OriginHeadless},
		{name: "rule 4 takes a guest's prompt in any session: typed in a terminal here, it is headless",
			keys: true,
			build: func(b *tl) (event.Event, event.Event) {
				terminalSession(b)
				b.press(every(atMS(30_500), atMS(59_500), 1000)...)
				p := b.prompt(at(60), event.ViaHook, "s_1", "p_1", "t_1", 90)
				p.Data.(*event.PromptSubmit).Guest = true
				b.samples(at(0), at(180))
				return p, event.Event{}
			},
			origin: event.OriginHeadless},
		{name: "rule 1: a message the app's scheduler or a webhook sent, declared cron, makes its prompt scheduled",
			build:  onServer(with(func(m *event.RelayMessage) { m.Declared = event.RelayDeclaredCron }), 90),
			origin: event.OriginScheduled},
		{name: "rule 3: a message from another thread, declared cross_session, makes its prompt a subagent's",
			build:  onServer(with(func(m *event.RelayMessage) { m.Declared = event.RelayDeclaredCrossSession }), 90),
			origin: event.OriginSubagent},
		{name: "rule 1 comes before rules 6 and 7: the app's cron makes a prompt scheduled though input here would pass them",
			keys: true, build: typedHere(with(func(m *event.RelayMessage) { m.Declared = event.RelayDeclaredCron })),
			origin: event.OriginScheduled},
		{name: "the typed length is the app's own, never more than the hook's typed_len_est",
			build:  onServer(appMessage(at(60), 150), 90),
			origin: event.OriginRelayed, class: event.ClassSubstantive, typed: 90,
			anchor: "relay", atStamp: true, kind: event.AnchorSubstantivePrompt, lead: 60},
		{name: "the typed length is the app's own, without what the app adds to the prompt the hook sees",
			build:  onServer(appMessage(at(60), 45), 90),
			origin: event.OriginRelayed, class: event.ClassSubstantive, typed: 45,
			anchor: "relay", atStamp: true, kind: event.AnchorSubstantivePrompt, lead: 30},
		{name: "the class is the app's text's: under SUBSTANTIVE_CHARS of it is low_effort, though the hook saw more",
			build:  onServer(appMessage(at(60), 15), 90),
			origin: event.OriginRelayed, class: event.ClassLowEffort, typed: 15,
			anchor: "relay", atStamp: true, kind: event.AnchorGoAhead, lead: 10},
		{name: "the class is the app's text's: the record's go-ahead test makes the prompt low_effort, whatever the hook's says",
			build:  onServer(with(func(m *event.RelayMessage) { m.LenChars, m.LowEffort = 3, true }), 90),
			origin: event.OriginRelayed, class: event.ClassLowEffort, typed: 3,
			anchor: "relay", atStamp: true, kind: event.AnchorGoAhead, lead: 2},
		{name: "the class: a relayed prompt after the agent's question is an answer",
			build: func(b *tl) (event.Event, event.Event) {
				appServer(b)
				asks(b)
				return sentAt60(b, appMessage(at(60), 90), 90)
			},
			origin: event.OriginRelayed, class: event.ClassAnswer, typed: 90,
			anchor: "relay", atStamp: true, kind: event.AnchorAnswer, lead: 60},
		{name: "the class: a relayed go-ahead after the agent's question is an answer, a decision",
			build: func(b *tl) (event.Event, event.Event) {
				appServer(b)
				asks(b)
				return sentAt60(b, with(func(m *event.RelayMessage) { m.LenChars, m.LowEffort = 3, true }), 90)
			},
			origin: event.OriginRelayed, class: event.ClassAnswer, typed: 3,
			anchor: "relay", atStamp: true, kind: event.AnchorAnswer, lead: 2},
		{name: "the class: a prompt the harness marks as answering a question is an answer",
			build:  edit(onServer(appMessage(at(60), 90), 90), func(p *event.PromptSubmit) { p.AnswersQuestion = true }),
			origin: event.OriginRelayed, class: event.ClassAnswer, typed: 90,
			anchor: "relay", atStamp: true, kind: event.AnchorAnswer, lead: 60},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := newTL(c.keys)
			p, r := c.build(b)
			d := b.run(t, Device{TypingModule: c.keys})
			tu, ok := turnByKey(d, "t_1")
			if !ok {
				t.Fatal("no turn for the prompt")
			}
			if tu.Origin != c.origin || tu.PromptClass != c.class || (c.class != "" && tu.TypedChars != c.typed) {
				t.Errorf("origin %q class %q typed %d, want %q, %q and %d", tu.Origin, tu.PromptClass, tu.TypedChars,
					c.origin, c.class, c.typed)
			}
			if err := tu.Event().Validate(); err != nil {
				t.Errorf("the turn isn't a valid record: %v", err)
			}
			byPrompt, okP := anchorByID(d, p.EventID)
			byRelay, okR := anchorByID(d, r.EventID)
			var a Anchor
			switch {
			case c.anchor == "" && (okP || okR):
				t.Fatalf("anchors %+v and %+v, want none", byPrompt, byRelay)
			case c.anchor == "":
				return
			case c.anchor == "prompt" && (!okP || okR), c.anchor == "relay" && (!okR || okP):
				t.Fatalf("anchor on the prompt %v and on the record %v, want one on the %s", okP, okR, c.anchor)
			case c.anchor == "prompt":
				a = byPrompt
			default:
				a = byRelay
			}
			if a.Kind != c.kind || a.LeadMS != c.lead*1000 {
				t.Errorf("anchor %s with lead %d ms, want %s with %d s", a.Kind, a.LeadMS, c.kind, c.lead)
			}
			end := p.T
			if c.atStamp {
				end = r.Data.(*event.RelayMessage).SentAt
			}
			if a.leadEnd() != end {
				t.Errorf("the lead ends at %d ms after base, want %d", a.leadEnd()-base, end-base)
			}
		})
	}
}

// TestRelayedAnchor checks a relayed prompt's anchor (SM 2.3's relayed
// row): the relay.message itself, at the app's stamp, with typing time
// only, cut to the time since the thread's previous message of declared
// none, level C with via_relay whatever backs it.
func TestRelayedAnchor(t *testing.T) {
	r := newRules(testTable(t))
	// relayed adds t3-away's first message, with its record changed by f,
	// after the records more adds.
	relayed := func(n int, f func(m *event.RelayMessage), more func(b *tl)) func(b *tl) (event.Event, event.Event) {
		return func(b *tl) (event.Event, event.Event) {
			appServer(b)
			if more != nil {
				more(b)
			}
			m := appMessage(at(60), n)
			if f != nil {
				f(m)
			}
			return sentAt60(b, m, n)
		}
	}
	// earlier adds the record of a message stamped sMS milliseconds after
	// base in thread th and declared d, seen a second later, which pairs
	// with nothing.
	earlier := func(sMS int64, th string, d event.RelayDeclared) func(b *tl) {
		return func(b *tl) {
			m := appMessage(atMS(sMS), 90)
			m.ThreadKey, m.Declared = th, d
			b.relayMsg(atMS(sMS+1000), "s_1", m)
		}
	}
	cases := []struct {
		name   string
		dev    Device
		keys   bool
		build  func(b *tl) (prompt, relay event.Event)
		kind   event.AnchorKind
		lead   int64 // ms
		labels []event.Label
	}{
		{name: "the anchor is the relay record, at the app's stamp: 90 characters are 90 / 1.5 = 60 s of typing, level C with via_relay",
			build: relayed(90, nil, nil), kind: event.AnchorSubstantivePrompt, lead: 60_000,
			labels: []event.Label{event.LabelViaRelay}},
		{name: "a relayed go-ahead earns its typing, 2 s for `yes`, not LOW_LEAD_S",
			build: relayed(3, func(m *event.RelayMessage) { m.LowEffort = true }, nil), kind: event.AnchorGoAhead,
			lead: 2_000, labels: []event.Label{event.LabelViaRelay}},
		{name: "a relayed answer has no ANSWER_LEAD_S to decide, only its typing",
			build: func(b *tl) (event.Event, event.Event) {
				p, rr := relayed(90, nil, nil)(b)
				p.Data.(*event.PromptSubmit).AnswersQuestion = true
				return p, rr
			},
			kind: event.AnchorAnswer, lead: 60_000, labels: []event.Label{event.LabelViaRelay}},
		{name: "COMPOSE_MAX_S caps a relayed lead",
			build: relayed(600, nil, nil), kind: event.AnchorSubstantivePrompt, lead: 300_000,
			labels: []event.Label{event.LabelViaRelay}},
		{name: "a relayed lead rounds to the nearest second, as any typing part: 1 character is 0.67 s, so 1 s",
			build: relayed(1, nil, nil), kind: event.AnchorGoAhead, lead: 1_000,
			labels: []event.Label{event.LabelViaRelay}},
		{name: "the thread's previous message caps the lead at the time since its stamp, though that message paired with nothing",
			build: relayed(90, nil, earlier(55_000, "r_1", event.RelayDeclaredNone)), kind: event.AnchorSubstantivePrompt,
			lead: 5_000, labels: []event.Label{event.LabelViaRelay}},
		{name: "the cap is the time since the previous stamp, to the millisecond",
			build: relayed(90, nil, earlier(57_500, "r_1", event.RelayDeclaredNone)), kind: event.AnchorSubstantivePrompt,
			lead: 2_500, labels: []event.Label{event.LabelViaRelay}},
		{name: "only the thread's own messages cap the lead: one in another thread caps nothing",
			build: relayed(90, nil, earlier(55_000, "r_2", event.RelayDeclaredNone)), kind: event.AnchorSubstantivePrompt,
			lead: 60_000, labels: []event.Label{event.LabelViaRelay}},
		{name: "a previous message the app declared cron caps nothing",
			build: relayed(90, nil, earlier(55_000, "r_1", event.RelayDeclaredCron)), kind: event.AnchorSubstantivePrompt,
			lead: 60_000, labels: []event.Label{event.LabelViaRelay}},
		{name: "a message stamped after this one caps nothing, though its record came first",
			build: relayed(90, nil, func(b *tl) {
				m := appMessage(at(65), 90)
				b.relayMsg(at(62), "s_1", m)
			}),
			kind: event.AnchorSubstantivePrompt, lead: 60_000, labels: []event.Label{event.LabelViaRelay}},
		{name: "on a VM the anchor carries vm too, before via_relay (DESIGN 6.2 D17)",
			dev: Device{VM: true}, build: relayed(90, nil, nil), kind: event.AnchorSubstantivePrompt, lead: 60_000,
			labels: []event.Label{event.LabelVM, event.LabelViaRelay}},
		{name: "a relayed anchor is level C though the OS here saw typing: a phone message on a desktop with key presses",
			dev: Device{TypingModule: true}, keys: true,
			build: func(b *tl) (event.Event, event.Event) {
				appWindow(b)
				b.press(every(atMS(30_500), atMS(59_500), 1000)...)
				m := appMessage(at(60), 90)
				m.Client = event.RelayClientMobile
				p, rr := sentAt60(b, m, 90)
				b.samples(at(0), at(180))
				return p, rr
			},
			kind: event.AnchorSubstantivePrompt, lead: 60_000, labels: []event.Label{event.LabelViaRelay}},
		{name: "a queued message's anchor stands at its stamp and is decided at its pairing, 590 s later",
			build: func(b *tl) (event.Event, event.Event) {
				appServer(b)
				rr := b.relayMsg(at(63), "s_1", appMessage(at(60), 90))
				p := b.prompt(at(650), event.ViaHook, "s_1", "p_1", "t_1", 90)
				p.Data.(*event.PromptSubmit).Relay = rr.EventID
				return p, rr
			},
			kind: event.AnchorSubstantivePrompt, lead: 60_000, labels: []event.Label{event.LabelViaRelay}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := newTL(c.keys)
			p, rr := c.build(b)
			d := b.run(t, c.dev)
			a, ok := anchorByID(d, rr.EventID)
			if !ok {
				t.Fatalf("no anchor on the relay record; anchors %+v", d.Anchors)
			}
			sent := rr.Data.(*event.RelayMessage).SentAt
			kind, reason := promptKind(a.PromptClass)
			checks := []struct {
				what string
				ok   bool
			}{
				{"it stands at the stamp, where its lead ends", a.T == sent && a.SentAt == sent && a.leadEnd() == sent},
				{"its origin is relayed", a.Origin == event.OriginRelayed},
				{"its kind and reason are its class's", a.Kind == c.kind && kind == c.kind && a.Reason == reason},
				{"its lead is typing time only", a.LeadMS == c.lead && a.TypingMS == c.lead && !a.TypingNeedsKeys},
				{"it has no trail and no run", a.TrailMS == 0 && a.WatchMS == 0},
				{"it arrived via app, and is level C, never corroborated", a.Via == event.ViaApp && a.Level == event.EvidenceC &&
					!a.Corroborated},
				{"it carries its labels", slices.Equal(a.Labels, c.labels)},
				{"it names its prompt, whose own record opens no anchor", a.Prompt == p.EventID && a.Relay == ""},
				{"it is decided once both records are in", a.decided == max(rr.T, p.T)},
				{"it reaches back from the stamp, not from its pairing", a.from(r) == sent-r.reach},
				{"it is a valid record", a.Event().Validate() == nil},
			}
			if _, opened := anchorByID(d, p.EventID); opened {
				t.Error("the prompt opened an anchor of its own")
			}
			for _, ch := range checks {
				if !ch.ok {
					t.Errorf("%s: %+v", ch.what, a)
				}
			}
		})
	}
}

// TestTypedAtStamp checks the anchor of a witnessed prompt that rules 6 and
// 7 make typed at the app's stamp (SM 2.2, testdata/timelines/relayed/t3-here):
// the prompt's own, at its t and at its usual level, with its lead and its
// evidence window ending at the stamp, so the second between the stamp and
// the prompt is in no window.
func TestTypedAtStamp(t *testing.T) {
	r := newRules(testTable(t))
	b := newTL(true)
	appWindow(b)
	b.mouse(every(at(1), at(296), 5000)...)
	b.press(every(atMS(240_500), atMS(299_500), 1000)...)
	m := appMessage(at(300), 90)
	p := b.prompt(at(301), event.ViaHook, "s_1", "p_1", "t_1", 90)
	m.Prompt = p.EventID
	rr := b.relayMsg(at(303), "s_1", m)
	b.samples(at(0), at(420))
	d := b.run(t, Device{TypingModule: true})
	a, ok := anchorByID(d, p.EventID)
	if !ok {
		t.Fatalf("no anchor on the prompt; anchors %+v", d.Anchors)
	}
	for _, c := range []struct {
		name string
		ok   bool
	}{
		{"the anchor is the prompt's, human_typed, at its own t", a.Origin == event.OriginHumanTyped && a.T == at(301)},
		{"its lead, 120 + 90 / 1.5 = 180 s, ends at the stamp", a.LeadMS == 180_000 && a.SentAt == at(300) &&
			a.leadEnd() == at(300)},
		{"it names the record that witnessed it", a.Relay == rr.EventID && a.Prompt == ""},
		{"it is level A, with key presses in the KEYS_PRE_S before the stamp", a.Level == event.EvidenceA},
		{"it is decided once the relay record is in, and reaches back from the stamp",
			a.decided == at(303) && a.from(r) == at(300)-r.reach},
		{"the record opens no anchor of its own", func() bool { _, ok := anchorByID(d, rr.EventID); return !ok }()},
		{"the lead credits the 180 s before the stamp", sumIn(d, at(120), at(300))[rP] == 180},
		{"the slot the stamp starts holds none of the lead", sumIn(d, at(300), at(315))[rP] == 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			if !c.ok {
				t.Errorf("anchor %+v", a)
			}
		})
	}
	checkDerived(t, d)
}

// TestRelayedIsNoOtherAnchor checks that a relayed anchor is no human anchor
// for anything but its own lead and its own session's reading (SM 2.3): as
// the first anchor after a silence it doesn't make the run returned, and it
// qualifies none of the reading the person here could have done. Each
// relayed case has a twin, the same message typed into the app's window
// here, which rules 6 and 7 make an anchor that does.
func TestRelayedIsNoOtherAnchor(t *testing.T) {
	// silent is SM 2.1's return example in the app's window: the agent
	// answers 160 words at 102 s, just after the person's last touch at
	// 100 s, and after a silence they are back at 451.5 s, typing until
	// 469.5 s, with a 30-character message stamped 470 s whose prompt
	// reaches the agent at 471 s.
	silent := func(client event.RelayClient) *tl {
		b := newTL(false)
		appWindow(b)
		b.mouse(every(at(1), at(96), 5000)...)
		b.mouse(at(100))
		b.prompt(at(30), event.ViaHook, "s_1", "p_1", "t_0", 30)
		b.harness(at(102), event.ViaLog, "s_1", "p_1", &event.AgentOutput{TurnKey: "t_0", Words: 160, IsFinal: true})
		b.agentStops(at(102), "s_1", "t_0")
		b.press(every(atMS(451_500), atMS(469_500), 1000)...)
		m := appMessage(at(470), 30)
		m.Client = client
		p := b.prompt(at(471), event.ViaHook, "s_1", "p_1", "t_1", 30)
		m.Prompt = p.EventID
		b.relayMsg(at(473), "s_1", m)
		b.samples(at(0), at(531))
		return b
	}
	// watched is the person in the app's window here while the agent
	// answers a 30-character message stamped 60 s: 120 words, 40 s of
	// reading, at 90 s and a stop at 120 s. No message follows the stop, so
	// no relayed reading applies either.
	watched := func(client event.RelayClient) *tl {
		b := newTL(false)
		appWindow(b)
		b.mouse(every(at(1), at(296), 5000)...)
		m := appMessage(at(60), 30)
		m.Client = client
		sentAt60(b, m, 30)
		b.agentSays(at(90), "s_1", "t_1", 120)
		b.agentStops(at(120), "s_1", "t_1")
		b.samples(at(0), at(300))
		return b
	}
	returned := func(d Derived) int64 { return int64(len(d.Returns)) }
	reading := func(d Derived) int64 {
		s := sumIn(d, at(0), at(300))
		return s[rSUP] + s[rREAD]
	}
	cases := []struct {
		name string
		b    *tl
		got  func(d Derived) int64
		want int64
	}{
		{"a message typed here after a silence makes the run returned, as a typed prompt does (SM 2.1)",
			silent(event.RelayClientWeb), returned, 1},
		{"a relayed message after a silence doesn't, though it is substantive: as the first anchor after the run it keeps it from qualifying, as a go-ahead does",
			silent(event.RelayClientMobile), returned, 0},
		{"a message typed here qualifies the reading of its answer while the person watches: the 20 s base and the 40 s of prose (SM 2.4)",
			watched(event.RelayClientWeb), reading, 60},
		{"a relayed message qualifies none of it: it is no human anchor for its project's reading or its level",
			watched(event.RelayClientMobile), reading, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := c.b.run(t, Device{})
			if got := c.got(d); got != c.want {
				t.Errorf("got %d, want %d", got, c.want)
			}
		})
	}
}

// TestRelayHold checks that a message waiting for its pairing keeps the
// minutes a lead it could open can touch pending until the pairing or
// RELAY_MATCH_S after its stamp (SM 2.3), so that a Step a minute gives
// what one Step gives even when the prompt comes nearly RELAY_MATCH_S
// late, and that the pairing then credits the whole lead, since it reaches
// back from the stamp.
func TestRelayHold(t *testing.T) {
	// queued is a 450-character message typed into the app's window here
	// and stamped at 600 s, whose record the reader writes at 603 s. T3
	// queues it behind a turn and hands it to the agent at prompt seconds,
	// or never for 0. Typed here, its lead is a substantive prompt's, 120 +
	// 450 / 1.5 = 420 s, from 180 s to the stamp.
	queued := func(prompt int64) *tl {
		b := newTL(false)
		appWindow(b)
		b.mouse(every(at(1), at(1896), 5000)...)
		r := b.relayMsg(at(603), "s_1", appMessage(at(600), 450))
		if prompt > 0 {
			p := b.prompt(at(prompt), event.ViaHook, "s_1", "p_1", "t_1", 450)
			p.Data.(*event.PromptSubmit).Relay = r.EventID
		}
		b.samples(at(0), at(1900))
		return b
	}
	t.Run("a Step a minute gives what one Step gives when the prompt pairs 590 s after the stamp, and the lead's 420 s are credited", func(t *testing.T) {
		recs := queued(1190).records()
		one := scoreAll(t, Device{}, recs)
		if got, want := marshal(t, scoreByMinute(t, Device{}, recs)), marshal(t, one); !bytes.Equal(got, want) {
			t.Errorf("a Step a minute differs from one Step %s", firstDiff(got, want))
		}
		if n := sumIn(one, at(180), at(600))[rP]; n != 420 {
			t.Errorf("the lead credits %d s, want 420", n)
		}
	})
	s := NewState(testTable(t), Device{})
	recs := queued(0).records()
	next := 0
	for _, c := range []struct {
		name string
		upTo int64 // the Step takes the records up to here, in seconds after base
		want int64 // FinalBefore, in seconds after base
	}{
		{"waiting for its pairing, the message holds FinalBefore at the start of the longest lead it could open, 420 s before its stamp", 1140, 180},
		{"the hold lasts until RELAY_MATCH_S after the stamp", 1200, 180},
		{"once RELAY_MATCH_S after the stamp has passed with no pairing, FinalBefore is PENDING_WINDOW_S behind now again", 1215, 315},
	} {
		t.Run(c.name, func(t *testing.T) {
			var batch []event.Event
			for ; next < len(recs) && recs[next].T <= at(c.upTo); next++ {
				batch = append(batch, recs[next])
			}
			var out Output
			s, out = Step(s, batch)
			if out.FinalBefore != at(c.want) {
				t.Errorf("FinalBefore at %d s, want %d s", (out.FinalBefore-base)/1000, c.want)
			}
		})
	}
}

// TestWholeMinutes checks the slots of a device that never samples, such as
// a server with no desktop (SM 2.1): whole minutes, from the one holding
// its first record to the one holding now, so the seconds before its first
// record and after its last in their minutes take SM 5.2's codes
// (testdata/timelines/relayed). Records a log reader wrote, as lookback's
// transcripts all are, keep the range they had, which is WP-SC7's, and a
// device whose agent samples keeps the range its samples decide. A Step a
// minute gives what one Step gives on each.
func TestWholeMinutes(t *testing.T) {
	cases := []struct {
		name string
		via  event.Via
		// own adds the agent's samples, from the first record on.
		own bool
		// from and to are the first and last slots' starts, in seconds after
		// base.
		from, to int64
	}{
		{"a server's hook records give whole minutes, from the first record's minute to now's", event.ViaHook, false, 0, 105},
		{"records a log reader wrote alone keep the first record's slot and now's", event.ViaLog, false, 15, 90},
		{"a device that samples keeps the range its samples decide", event.ViaHook, true, 15, 75},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := newTL(false)
			b.harness(at(28), c.via, "s_1", "p_1", &event.SessionStart{StartKind: event.StartStartup,
				Entrypoint: event.EntrypointUnknown, ParentClass: event.ParentAgentApp})
			b.harness(at(90), c.via, "s_1", "p_1", &event.PromptSubmit{TurnKey: "t_1", LenChars: 30, TypedLenEst: 30,
				OriginDeclared: event.DeclaredNone})
			if c.own {
				b.samples(at(28), at(90))
			}
			recs := b.records()
			d := scoreAll(t, Device{}, recs)
			n := len(d.Slots)
			if n == 0 {
				t.Fatal("no slots")
			}
			if first, last := d.Slots[0].Start, d.Slots[n-1].Start; first != at(c.from) || last != at(c.to) ||
				n != int((c.to-c.from)/15)+1 || len(d.Credits) != n {
				t.Fatalf("%d slots and %d credit slots, from %d s to %d s; want them from %d s to %d s", n, len(d.Credits),
					(first-base)/1000, (last-base)/1000, c.from, c.to)
			}
			for _, s := range d.Slots {
				// With no sample at all, every slot is SM 2.1's unknown.
				if !c.own && s.Present != event.PresentUnknown {
					t.Errorf("slot at %d s is %q, want unknown", (s.Start-base)/1000, s.Present)
				}
			}
			checkDerived(t, d)
			if got, want := marshal(t, scoreByMinute(t, Device{}, recs)), marshal(t, d); !bytes.Equal(got, want) {
				t.Errorf("a Step a minute differs from one Step %s", firstDiff(got, want))
			}
		})
	}
}

// appStart starts session sess of project proj s seconds after base as
// appServer starts s_1: as an agent app's own server starts the harness.
func appStart(b *tl, s int64, sess, proj string) {
	b.start(at(s), sess, proj, event.EntrypointUnknown, event.ParentAgentApp, false)
}

// relayedMsg adds a message a person sent from the app's web client in
// thread th, n characters long and stamped sent seconds after base, whose
// run's prompt, with turn key turn, reaches session sess of project proj a
// second later; the reader's record comes two seconds after that and names
// the prompt, as in t3-away. It returns the record.
func relayedMsg(b *tl, sent int64, sess, proj, th, turn string, n int) event.Event {
	p := b.prompt(at(sent+1), event.ViaHook, sess, proj, turn, n)
	m := appMessage(at(sent), n)
	m.ThreadKey, m.Prompt = th, p.EventID
	return b.emit(at(sent+3), event.Envelope{Source: event.SourceLog, Via: event.ViaApp, Harness: event.HarnessClaudeCode,
		SessionKey: sess, ProjectKey: proj}, m)
}

// queuedMsg adds such a message whose record the reader writes three
// seconds after its stamp, before its run's prompt, which comes at prompt
// seconds and names the record, as T3 Code hands a queued message over
// once the turn before it ends (t3-late). It returns the record.
func queuedMsg(b *tl, sent, prompt int64, sess, proj, th, turn string, n int) event.Event {
	m := appMessage(at(sent), n)
	m.ThreadKey = th
	r := b.emit(at(sent+3), event.Envelope{Source: event.SourceLog, Via: event.ViaApp, Harness: event.HarnessClaudeCode,
		SessionKey: sess, ProjectKey: proj}, m)
	b.prompt(at(prompt), event.ViaHook, sess, proj, turn, n).Data.(*event.PromptSubmit).Relay = r.EventID
	return r
}

// readAfter is t3-away's first turn on a server that samples nothing: a
// 90-character message stamped at 60 s, so a 60 s lead from base, its
// prompt at 61 s, words of output at 90 s and the turn's stop at 120 s. The
// prompt resets the bucket to its 20 s base, and 60 words add 20 s.
func readAfter(b *tl, words int) {
	appServer(b)
	relayedMsg(b, 60, "s_1", "p_1", "r_1", "t_1", 90)
	says(b, at(90), "s_1", "p_1", "t_1", words)
	stops(b, at(120), "s_1", "p_1", "t_1")
}

// reasonIn is how many seconds of the slots in [from, to), in seconds after
// base, are credited or left uncredited under reason.
type reasonIn struct {
	from, to int64
	reason   event.Reason
	n        int64
}

// checkSteps checks that a Step a minute gives what one Step gives, the
// Finals of the first followed by its last Pending (DESIGN 3.7).
func checkSteps(t *testing.T, recs []event.Event, one Derived) {
	t.Helper()
	if got, want := marshal(t, scoreByMinute(t, Device{}, recs)), marshal(t, one); !bytes.Equal(got, want) {
		t.Errorf("a Step a minute differs from one Step %s", firstDiff(got, want))
	}
}

// checkRelayedCredit checks that every second credited in d is level C
// with via_relay: everything in the case rests on relayed anchors.
func checkRelayedCredit(t *testing.T, d Derived) {
	t.Helper()
	levels, labeled := levelsIn(d, math.MinInt64, math.MaxInt64, event.LabelViaRelay)
	var n int64
	for l, s := range levels {
		n += s
		if l != event.EvidenceC {
			t.Errorf("%d s credited at level %s, want every relayed second at level C", s, l)
		}
	}
	if labeled != n {
		t.Errorf("%d of %d credited seconds carry via_relay, want all", labeled, n)
	}
}

// TestRelayedReading checks SM 2.4's relayed reading on a server that
// samples nothing: a relayed prompt resets its session's bucket at pickup,
// the bucket holds at most UNKNOWN_BUCKET_MAX_S from that reset, and it
// drains, as READ and never SUP, from one of the session's stops until the
// thread's next relayed message is sent or the session's next turn starts,
// when that message is sent within READ_HORIZON_S of the stop and pairs
// within PENDING_WINDOW_S - 60 s of it. What only the 120 s cap kept from
// being read is X.bucket_cap, which comes before the codes of running turns
// (SM 5.2).
func TestRelayedReading(t *testing.T) {
	cases := []struct {
		name string
		// build adds the case's records and returns the relay record that
		// vouches for the reading, or the zero Event for none.
		build func(b *tl) event.Event
		exact []secondsWant
		want  []reasonIn
	}{
		{name: "a relayed session reads after its stop, as READ, until the thread's next relayed message is sent, which vouches for it",
			build: func(b *tl) event.Event {
				readAfter(b, 60)
				return relayedMsg(b, 300, "s_1", "p_1", "r_1", "t_2", 30)
			},
			exact: []secondsWant{
				{0, 60, map[event.Reason]int64{rP: 60}},
				{60, 120, map[event.Reason]int64{xNoAnch: 1, xAgent: 59}},
				{120, 165, map[event.Reason]int64{rREAD: 40, xNoAnch: 5}},
				{165, 270, map[event.Reason]int64{xNoAnch: 105}},
				{270, 300, map[event.Reason]int64{xNoAnch: 10, rP: 20}},
			}},
		{name: "no relayed session reads while its agent runs: relayed reading has no SUP",
			build: func(b *tl) event.Event {
				readAfter(b, 60)
				return relayedMsg(b, 300, "s_1", "p_1", "r_1", "t_2", 30)
			},
			want: []reasonIn{{0, 360, rSUP, 0}}},
		{name: "a message sent READ_HORIZON_S after the stop vouches for its reading",
			build: func(b *tl) event.Event {
				readAfter(b, 60)
				return relayedMsg(b, 720, "s_1", "p_1", "r_1", "t_2", 30)
			},
			want: []reasonIn{{120, 165, rREAD, 40}}},
		{name: "a message sent later than READ_HORIZON_S after the stop vouches for none of it, and keeps its own lead",
			build: func(b *tl) event.Event {
				readAfter(b, 60)
				relayedMsg(b, 721, "s_1", "p_1", "r_1", "t_2", 30)
				return event.Event{}
			},
			want: []reasonIn{{120, 720, rREAD, 0}, {690, 735, rP, 20}}},
		{name: "a queued message that pairs PENDING_WINDOW_S - 60 s after the stop, when its prompt comes, vouches for the reading",
			build: func(b *tl) event.Event {
				readAfter(b, 60)
				return queuedMsg(b, 400, 960, "s_1", "p_1", "r_1", "t_2", 30)
			},
			want: []reasonIn{{120, 165, rREAD, 40}, {375, 405, rP, 20}}},
		{name: "one that pairs a second later vouches for none of it, while the stop's minute may be final, but keeps its own lead (t3-late)",
			build: func(b *tl) event.Event {
				readAfter(b, 60)
				queuedMsg(b, 400, 961, "s_1", "p_1", "r_1", "t_2", 30)
				return event.Event{}
			},
			want: []reasonIn{{120, 400, rREAD, 0}, {375, 405, rP, 20}}},
		{name: "a message sent before the stop, as one T3 queued behind the turn, vouches for none of the reading after it",
			build: func(b *tl) event.Event {
				readAfter(b, 60)
				queuedMsg(b, 100, 200, "s_1", "p_1", "r_1", "t_2", 30)
				return event.Event{}
			},
			want: []reasonIn{{120, 200, rREAD, 0}, {75, 105, rP, 20}}},
		{name: "the session's next turn ends the reading, an automated one's too, though the vouching message comes later",
			build: func(b *tl) event.Event {
				readAfter(b, 60)
				b.prompt(at(140), event.ViaHook, "s_1", "p_1", "t_a", 30)
				return relayedMsg(b, 300, "s_1", "p_1", "r_1", "t_2", 30)
			},
			exact: []secondsWant{{120, 165, map[event.Reason]int64{rREAD: 20, xAuto: 25}}}},
		{name: "the bucket holds UNKNOWN_BUCKET_MAX_S from the relayed prompt's reset, and the reading the cap alone kept back is X.bucket_cap",
			build: func(b *tl) event.Event {
				// 600 words are 200 s, and with the 20 s base 220 s, of
				// which the cap keeps 120.
				readAfter(b, 600)
				return relayedMsg(b, 600, "s_1", "p_1", "r_1", "t_2", 30)
			},
			exact: []secondsWant{
				{120, 240, map[event.Reason]int64{rREAD: 120}},
				{240, 345, map[event.Reason]int64{xBucketCap: 100, xNoAnch: 5}},
				{345, 570, map[event.Reason]int64{xNoAnch: 225}},
				{570, 600, map[event.Reason]int64{xNoAnch: 10, rP: 20}},
			}},
		{name: "X.bucket_cap comes before X.automated while another session's automated turn runs (t3-unattended)",
			build: func(b *tl) event.Event {
				readAfter(b, 600)
				appStart(b, 0, "s_2", "p_2")
				b.prompt(at(250), event.ViaHook, "s_2", "p_2", "t_b", 30)
				stops(b, at(280), "s_2", "p_2", "t_b")
				return relayedMsg(b, 600, "s_1", "p_1", "r_1", "t_2", 30)
			},
			exact: []secondsWant{{240, 345, map[event.Reason]int64{xBucketCap: 100, xNoAnch: 5}}}},
		{name: "a bucket the cap didn't cut leaves no X.bucket_cap once it is read (t3-phone)",
			build: func(b *tl) event.Event {
				readAfter(b, 60)
				return relayedMsg(b, 600, "s_1", "p_1", "r_1", "t_2", 30)
			},
			want: []reasonIn{{120, 600, rREAD, 40}, {120, 600, xBucketCap, 0}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := newTL(false)
			vouch := c.build(b)
			recs := b.records()
			d := scoreAll(t, Device{}, recs)
			for _, w := range c.exact {
				if got := sumIn(d, at(w.from), at(w.to)); !sameCounts(got, w.want) {
					t.Errorf("seconds %d to %d: %v, want %v", w.from, w.to, got, w.want)
				}
			}
			for _, w := range c.want {
				if got := sumIn(d, at(w.from), at(w.to))[w.reason]; got != w.n {
					t.Errorf("seconds %d to %d hold %d s of %s, want %d", w.from, w.to, got, w.reason, w.n)
				}
			}
			for _, cr := range d.Credits {
				for _, e := range cr.Credited {
					if e.Reason == rREAD && (vouch.EventID == "" || !slices.Equal(e.AnchorIDs, []string{vouch.EventID})) {
						t.Errorf("READ at %d s names %v, want the relay record that vouches for it, %q", (cr.Slot-base)/1000,
							e.AnchorIDs, vouch.EventID)
					}
				}
			}
			checkRelayedCredit(t, d)
			checkDerived(t, d)
			checkSteps(t, recs, d)
		})
	}
}

// TestRelayedRanking checks SM 2.7's rule for a second two relayed sessions
// want, on a server that samples nothing: the session whose next relayed
// anchor's lead holds it takes it; when both sessions' next leads hold it,
// the one whose anchor is sent soonest after it; when neither does, the one
// whose latest relayed anchor is the most recent, a session with none yet
// counting as the oldest; and ties go to the lower project_key, then the
// lower session_key. No relayed case has two such sessions.
func TestRelayedRanking(t *testing.T) {
	// readSoon is a relayed session's turn: a message in thread th stamped
	// at sent seconds, n characters long, 30 words of output, 10 s on top
	// of the 20 s base, at 95 s, and a stop at 105 s; then the message
	// stamped at next seconds, a 3-character go-ahead whose 2 s lead lies
	// well after the reading, that vouches for it. It returns that
	// message's record.
	readSoon := func(b *tl, sess, proj, th string, sent int64, n int, next int64) event.Event {
		relayedMsg(b, sent, sess, proj, th, "t_"+sess+"a", n)
		says(b, at(95), sess, proj, "t_"+sess+"a", 30)
		stops(b, at(105), sess, proj, "t_"+sess+"a")
		return relayedMsg(b, next, sess, proj, th, "t_"+sess+"b", 3)
	}
	cases := []struct {
		name string
		// build adds the records and returns, by slot start in seconds
		// after base, the one anchor every second credited in the slot
		// names, where the case checks it.
		build func(b *tl) map[int64]string
		want  []projWant
	}{
		{name: "a session whose next relayed anchor's lead holds a second takes it from another session's reading",
			build: func(b *tl) map[int64]string {
				appServer(b)
				relayedMsg(b, 60, "s_1", "p_1", "r_1", "t_1", 90)
				says(b, at(90), "s_1", "p_1", "t_1", 120)
				stops(b, at(120), "s_1", "p_1", "t_1")
				relayedMsg(b, 400, "s_1", "p_1", "r_1", "t_3", 3)
				appStart(b, 0, "s_2", "p_2")
				relayedMsg(b, 150, "s_2", "p_2", "r_2", "t_2", 45)
				return nil
			},
			want: []projWant{
				{120, 150, projSecs{"p_2": {rP: 30}}},
				{150, 210, projSecs{"p_1": {rREAD: 60}}},
			}},
		{name: "when both sessions' next leads hold a second, the one whose anchor is sent soonest after it takes it",
			build: func(b *tl) map[int64]string {
				appServer(b)
				relayedMsg(b, 300, "s_1", "p_1", "r_1", "t_1", 90)
				appStart(b, 0, "s_2", "p_2")
				relayedMsg(b, 270, "s_2", "p_2", "r_2", "t_2", 90)
				return nil
			},
			want: []projWant{
				{210, 270, projSecs{"p_2": {rP: 60}}},
				{270, 300, projSecs{"p_1": {rP: 30}}},
			}},
		{name: "when neither lead holds it, the session whose latest relayed anchor is the most recent reads first",
			build: func(b *tl) map[int64]string {
				appServer(b)
				readSoon(b, "s_1", "p_1", "r_1", 60, 90, 400)
				appStart(b, 0, "s_2", "p_2")
				readSoon(b, "s_2", "p_2", "r_2", 80, 30, 450)
				return nil
			},
			want: []projWant{
				{105, 135, projSecs{"p_2": {rREAD: 30}}},
				{135, 165, projSecs{"p_1": {rREAD: 30}}},
			}},
		{name: "a session with no relayed anchor yet counts as the oldest",
			build: func(b *tl) map[int64]string {
				appServer(b)
				readSoon(b, "s_1", "p_1", "r_1", 60, 90, 400)
				// s_2's first turn is automated, 10 s of output with no base,
				// and its first relayed message comes after the reading.
				appStart(b, 0, "s_2", "p_2")
				b.prompt(at(80), event.ViaHook, "s_2", "p_2", "t_2", 30)
				says(b, at(95), "s_2", "p_2", "t_2", 30)
				stops(b, at(105), "s_2", "p_2", "t_2")
				relayedMsg(b, 450, "s_2", "p_2", "r_2", "t_4", 3)
				return nil
			},
			want: []projWant{
				{105, 135, projSecs{"p_1": {rREAD: 30}}},
				{135, 150, projSecs{"p_2": {rREAD: 10}, "": {xNoAnch: 5}}},
			}},
		{name: "ties go to the lower project_key, for two leads sent at once and for two readings whose latest anchors were",
			build: func(b *tl) map[int64]string {
				appServer(b)
				readSoon(b, "s_1", "p_1", "r_1", 60, 90, 400)
				appStart(b, 0, "s_2", "p_2")
				readSoon(b, "s_2", "p_2", "r_2", 60, 90, 450)
				return nil
			},
			want: []projWant{
				{0, 60, projSecs{"p_1": {rP: 60}}},
				{105, 135, projSecs{"p_1": {rREAD: 30}}},
				{135, 165, projSecs{"p_2": {rREAD: 30}}},
			}},
		{name: "then the lower session_key, in one project",
			build: func(b *tl) map[int64]string {
				appServer(b)
				v1 := readSoon(b, "s_1", "p_1", "r_1", 60, 90, 400)
				appStart(b, 0, "s_2", "p_1")
				v2 := readSoon(b, "s_2", "p_1", "r_2", 60, 90, 450)
				lead := ""
				for _, e := range b.evs {
					if m, ok := e.Data.(*event.RelayMessage); ok && e.SessionKey == "s_1" && m.SentAt == at(60) {
						lead = e.EventID
					}
				}
				return map[int64]string{0: lead, 45: lead, 105: v1.EventID, 120: v1.EventID, 135: v2.EventID,
					150: v2.EventID}
			},
			want: []projWant{
				{0, 60, projSecs{"p_1": {rP: 60}}},
				{105, 165, projSecs{"p_1": {rREAD: 60}}},
			}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := newTL(false)
			anchors := c.build(b)
			recs := b.records()
			d := scoreAll(t, Device{}, recs)
			for _, w := range c.want {
				if got := projSums(d, at(w.from), at(w.to)); !sameProjSecs(got, w.want) {
					t.Errorf("seconds %d to %d: %v, want %v", w.from, w.to, got, w.want)
				}
			}
			for s, id := range anchors {
				cr, ok := creditAt(d, at(s))
				if !ok || len(cr.Credited) == 0 {
					t.Errorf("slot at %d s credits nothing", s)
					continue
				}
				for _, e := range cr.Credited {
					if !slices.Equal(e.AnchorIDs, []string{id}) {
						t.Errorf("slot at %d s credits %s under %v, want under %s", s, e.Reason, e.AnchorIDs, id)
					}
				}
			}
			checkRelayedCredit(t, d)
			checkDerived(t, d)
			checkSteps(t, recs, d)
		})
	}
}

// TestLiveUnknownCodes checks SM 5.2's codes for an uncredited second of a
// live slot whose presence is unknown, as on a server with no desktop:
// X.bucket_cap first (TestRelayedReading), then X.agent_only while a turn a
// person started runs, a relayed one included, or a no_input one, then
// X.automated while an automated turn runs, then X.no_anchor, with the
// turns of every project counting, since such a slot has no project of its
// own. Records a log reader wrote alone, as lookback's are, keep X.absent
// until WP-SC7.
func TestLiveUnknownCodes(t *testing.T) {
	headlessAt := func(b *tl, sess, proj, turn string, from, to int64) {
		b.prompt(at(from), event.ViaHook, sess, proj, turn, 30)
		stops(b, at(to), sess, proj, turn)
	}
	cases := []struct {
		name  string
		build func(b *tl)
		want  []secondsWant
	}{
		{"while a relayed turn runs, X.agent_only: a relayed prompt is a person's for its own turn (SM 2.2)",
			func(b *tl) {
				appServer(b)
				relayedMsg(b, 60, "s_1", "p_1", "r_1", "t_1", 90)
				stops(b, at(120), "s_1", "p_1", "t_1")
			},
			[]secondsWant{{75, 120, map[event.Reason]int64{xAgent: 45}}}},
		{"while a no_input turn runs, X.agent_only too, where a sampled slot would say X.no_input",
			func(b *tl) {
				b.start(at(0), "s_3", "p_3", event.EntrypointCLI, event.ParentTerminalApp, true)
				headlessAt(b, "s_3", "p_3", "t_1", 60, 120)
			},
			[]secondsWant{{75, 120, map[event.Reason]int64{xAgent: 45}}}},
		{"while only an automated turn runs, X.automated",
			func(b *tl) {
				appServer(b)
				headlessAt(b, "s_1", "p_1", "t_1", 60, 120)
			},
			[]secondsWant{{75, 120, map[event.Reason]int64{xAuto: 45}}}},
		{"a person's turn in another project comes before an automated one, since such a slot has no project",
			func(b *tl) {
				appServer(b)
				headlessAt(b, "s_1", "p_1", "t_1", 60, 120)
				appStart(b, 0, "s_2", "p_2")
				relayedMsg(b, 74, "s_2", "p_2", "r_2", "t_2", 3)
				stops(b, at(105), "s_2", "p_2", "t_2")
			},
			[]secondsWant{
				{75, 105, map[event.Reason]int64{xAgent: 30}},
				{105, 120, map[event.Reason]int64{xAuto: 15}},
			}},
		{"with no turn running, X.no_anchor",
			func(b *tl) {
				appServer(b)
				headlessAt(b, "s_1", "p_1", "t_1", 60, 120)
			},
			[]secondsWant{{0, 60, map[event.Reason]int64{xNoAnch: 60}}}},
		{"records a log reader wrote alone, as lookback's transcripts are, keep X.absent",
			func(b *tl) {
				b.harness(at(0), event.ViaLog, "s_1", "p_1", &event.SessionStart{StartKind: event.StartStartup,
					Entrypoint: event.EntrypointUnknown, ParentClass: event.ParentAgentApp})
				b.harness(at(60), event.ViaLog, "s_1", "p_1", &event.PromptSubmit{TurnKey: "t_1", LenChars: 30,
					TypedLenEst: 30, OriginDeclared: event.DeclaredNone})
				b.harness(at(120), event.ViaLog, "s_1", "p_1", &event.AgentStop{TurnKey: "t_1",
					StopReason: event.StopEndTurn})
			},
			[]secondsWant{{75, 120, map[event.Reason]int64{xAbsent: 45}}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := newTL(false)
			c.build(b)
			d := b.run(t, Device{})
			for _, w := range c.want {
				if got := sumIn(d, at(w.from), at(w.to)); !sameCounts(got, w.want) {
					t.Errorf("seconds %d to %d: %v, want %v", w.from, w.to, got, w.want)
				}
			}
			checkDerived(t, d)
		})
	}
}

// TestRelayedLinks checks links that conflict: the first link in record
// order pairs, whichever record carries it, and a later link naming either
// of its records pairs with nothing, so only the first pairing's message
// opens a lead and only its prompt is relayed (SM 2.2 rule 9).
func TestRelayedLinks(t *testing.T) {
	cases := []struct {
		name string
		// build returns the relay record that pairs, the turn key of its
		// prompt, and the turn key of the prompt that pairs with nothing.
		build      func(b *tl) (relay event.Event, paired, unpaired string)
		want       []reasonIn
		pairedLead int64 // the paired record's lead, in seconds
	}{
		{name: "a record names a prompt, and a later prompt names that record: the record's link came first, and the later prompt goes by rule 4",
			build: func(b *tl) (event.Event, string, string) {
				appServer(b)
				p1 := b.prompt(at(61), event.ViaHook, "s_1", "p_1", "t_1", 90)
				m := appMessage(at(60), 90)
				m.Prompt = p1.EventID
				r := b.relayMsg(at(63), "s_1", m)
				b.prompt(at(70), event.ViaHook, "s_1", "p_1", "t_2", 90).Data.(*event.PromptSubmit).Relay = r.EventID
				return r, "t_1", "t_2"
			},
			want: []reasonIn{{0, 60, rP, 60}}, pairedLead: 60},
		{name: "a prompt names one record, and a later record names that prompt: the prompt's link came first, so the later record opens nothing",
			build: func(b *tl) (event.Event, string, string) {
				appServer(b)
				first := b.relayMsg(at(108), "s_1", appMessage(at(105), 90))
				p1 := b.prompt(at(121), event.ViaHook, "s_1", "p_1", "t_1", 90)
				p1.Data.(*event.PromptSubmit).Relay = first.EventID
				m := appMessage(at(120), 90)
				m.Prompt = p1.EventID
				b.relayMsg(at(123), "s_1", m)
				return first, "t_1", ""
			},
			want: []reasonIn{{45, 105, rP, 60}, {105, 120, rP, 0}}, pairedLead: 60},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := newTL(false)
			r, paired, unpaired := c.build(b)
			d := b.run(t, Device{})
			tu, ok := turnByKey(d, paired)
			if !ok || tu.Origin != event.OriginRelayed || tu.Relay != r.EventID {
				t.Errorf("turn %s is %q and paired with %q, want relayed and paired with %s", paired, tu.Origin, tu.Relay,
					r.EventID)
			}
			if unpaired != "" {
				if tu, ok := turnByKey(d, unpaired); !ok || tu.Origin != event.OriginHeadless || tu.Relay != "" {
					t.Errorf("turn %s is %q and paired with %q, want headless and paired with nothing", unpaired, tu.Origin,
						tu.Relay)
				}
			}
			var opened []string
			for _, a := range d.Anchors {
				opened = append(opened, a.ID)
				if a.ID == r.EventID && a.LeadMS != c.pairedLead*1000 {
					t.Errorf("the paired record's lead is %d ms, want %d s", a.LeadMS, c.pairedLead)
				}
			}
			if !slices.Equal(opened, []string{r.EventID}) {
				t.Errorf("anchors %v, want only the paired record's, %s", opened, r.EventID)
			}
			for _, rr := range d.Relays {
				if rr.ID != r.EventID && (rr.Prompt != "" || rr.Origin != "") {
					t.Errorf("relay record %+v pairs, want only %s to", rr, r.EventID)
				}
			}
			for _, w := range c.want {
				if got := sumIn(d, at(w.from), at(w.to))[w.reason]; got != w.n {
					t.Errorf("seconds %d to %d hold %d s of %s, want %d", w.from, w.to, got, w.reason, w.n)
				}
			}
			checkRelayedCredit(t, d)
			checkDerived(t, d)
		})
	}
}

// TestRelayedLatePairing checks a late pairing that comes after a stop's
// minute can be final. A message sent 580 s after the stop pairs only 1,170
// s after it, past PENDING_WINDOW_S - 60 s, and a message sent 10 s after it
// pairs at once. The late one keeps its lead, and it is no next relayed
// anchor for the stop at all, so the other vouches for the reading: the
// pairing changes nothing about a reading that may be final by then, and a
// Step a minute gives what one Step gives (SM 2.4).
func TestRelayedLatePairing(t *testing.T) {
	b := newTL(false)
	readAfter(b, 60)
	queuedMsg(b, 700, 1290, "s_1", "p_1", "r_1", "t_3", 90)
	vouch := relayedMsg(b, 710, "s_1", "p_1", "r_1", "t_2", 30)
	says(b, at(800), "s_1", "p_1", "t_2", 30)
	stops(b, at(1100), "s_1", "p_1", "t_2")
	recs := b.records()
	d := scoreAll(t, Device{}, recs)
	for _, c := range []struct {
		name string
		w    reasonIn
	}{
		{"the message that paired in time vouches for the stop's reading", reasonIn{120, 165, rREAD, 40}},
		{"the late message keeps its own 60 s lead to its stamp, and the next message the 10 s since that stamp",
			reasonIn{630, 720, rP, 70}},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := sumIn(d, at(c.w.from), at(c.w.to))[c.w.reason]; got != c.w.n {
				t.Errorf("seconds %d to %d hold %d s of %s, want %d", c.w.from, c.w.to, got, c.w.reason, c.w.n)
			}
		})
	}
	t.Run("the reading names the message that vouches for it", func(t *testing.T) {
		cr, ok := creditAt(d, at(120))
		if !ok || len(cr.Credited) != 1 || !slices.Equal(cr.Credited[0].AnchorIDs, []string{vouch.EventID}) {
			t.Errorf("slot at 120 s credits %+v, want READ under %s", cr.Credited, vouch.EventID)
		}
	})
	t.Run("a Step a minute gives what one Step gives", func(t *testing.T) { checkSteps(t, recs, d) })
	checkDerived(t, d)
}

// TestRelayedPresence checks where a relayed lead is realized on a desktop
// with the app's window in front: in a slot of any presence, a locked one
// included, but not in one the machine slept through, since a sleeping
// machine runs no agent (SM 2.1, 2.3). A message from the app's phone app
// skips rules 6 and 7, so it is relayed whatever this device saw.
func TestRelayedPresence(t *testing.T) {
	phone := func(b *tl) {
		m := appMessage(at(60), 90)
		m.Client = event.RelayClientMobile
		sentAt60(b, m, 90)
	}
	cases := []struct {
		name  string
		build func(b *tl)
		want  []secondsWant
	}{
		{"a relayed lead is realized in locked slots",
			func(b *tl) {
				appWindow(b)
				phone(b)
				b.samples(at(0), at(0))
				b.power(at(5), event.PowerLock)
				b.samples(at(15), at(135))
				b.power(at(140), event.PowerUnlock)
				b.samples(at(150), at(180))
			},
			[]secondsWant{{0, 60, map[event.Reason]int64{rP: 60}}}},
		{"but not in slots the machine slept through, whose seconds stay X.absent",
			func(b *tl) {
				appWindow(b)
				phone(b)
				b.samples(at(0), at(15))
				b.power(at(20), event.PowerSuspend)
				b.power(at(40), event.PowerResume)
				b.samples(at(45), at(180))
			},
			[]secondsWant{
				{0, 15, map[event.Reason]int64{rP: 15}},
				{15, 45, map[event.Reason]int64{xAbsent: 30}},
				{45, 60, map[event.Reason]int64{rP: 15}},
			}},
		{"a phone message's lead is realized in minutes sampled once a minute with no input, as the agent samples them while an agent app's server runs (D-relayed-context)",
			func(b *tl) {
				appWindow(b)
				phone(b)
				for _, s := range []int64{0, 60, 120, 180} {
					b.sample(at(s))
				}
			},
			[]secondsWant{{0, 60, map[event.Reason]int64{rP: 60}}}},
		{"but in minutes nothing sampled, as when the desktop's context had ended, it reads as slept through and stays X.absent",
			func(b *tl) {
				appWindow(b)
				phone(b)
				b.sample(at(-600))
				b.sample(at(61))
				b.samples(at(75), at(180))
			},
			[]secondsWant{{0, 60, map[event.Reason]int64{xAbsent: 60}}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := newTL(false)
			c.build(b)
			d := b.run(t, Device{})
			for _, w := range c.want {
				if got := sumIn(d, at(w.from), at(w.to)); !sameCounts(got, w.want) {
					t.Errorf("seconds %d to %d: %v, want %v", w.from, w.to, got, w.want)
				}
			}
			checkRelayedCredit(t, d)
			checkDerived(t, d)
		})
	}
}

// TestRelayedTotals checks a submission's totals over relayed credit:
// every second is level C and counts in via_relay_s, and the relay records,
// which aren't agent events, count toward neither logged_s nor ai_logged_s,
// though the harness's own records of a relayed turn do (SM 5.4).
func TestRelayedTotals(t *testing.T) {
	b := newTL(false)
	readAfter(b, 60)
	relayedMsg(b, 300, "s_1", "p_1", "r_1", "t_2", 30)
	recs := b.records()
	d := scoreAll(t, Device{}, recs)
	tot := Submission{From: at(0), To: at(360), Projects: []string{"p_1"}}.Totals(d, recs)
	// The leads give 60 + 20 s and the reading 40. The prompts at 61 and 301
	// s and the stop at 120 s give logged_s 59 + 120, and the relay records
	// at 63 and 303 s would add 2 s.
	for _, c := range []struct {
		name      string
		got, want float64
	}{
		{"credited_s is the leads and the reading", tot.CreditedS, 120},
		{"via_relay_s counts every relayed second", tot.ViaRelayS, 120},
		{"level_c_s holds them all", tot.LevelCS, 120},
		{"logged_s counts the harness's records of the relayed turns and not the relay records", tot.LoggedS, 179},
		{"ai_logged_s too", tot.AILoggedS, 179},
	} {
		t.Run(c.name, func(t *testing.T) {
			if c.got != c.want {
				t.Errorf("%v, want %v", c.got, c.want)
			}
		})
	}
	if err := tot.Validate(); err != nil {
		t.Errorf("the totals aren't valid: %v", err)
	}
}
