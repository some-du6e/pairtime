// SPDX-License-Identifier: AGPL-3.0-or-later

package score

import (
	"cmp"
	"slices"
	"sort"
	"strings"

	"github.com/some-du6e/pairtime/pkg/event"
)

// Relayed prompts (SM 2.2 rule 9, D-relayed in
// docs/errata/2026-11-22-relayed-prompts.md). An agent app such as T3 Code
// runs agents on this machine for messages a person types elsewhere, and
// its reader writes a relay.message for each message the app's store says
// a person sent. A relay record is a claim: it opens nothing unless it
// pairs with the prompt.submit its message's run delivered, and then it
// witnesses that prompt, or gives it the app's own declaration.

// RelayRecord is one relay.message and what became of it (SM 2.2 rule 9):
// the prompt it paired with, if any, and the origin of the turn that prompt
// started.
type RelayRecord struct {
	ID         string              `json:"event_id"`
	T          int64               `json:"t"`
	SentAt     int64               `json:"sent_at"`
	SessionKey string              `json:"session_key"`
	ThreadKey  string              `json:"thread_key"`
	Declared   event.RelayDeclared `json:"declared"`
	// Prompt is the event_id of the prompt.submit it paired with, or ""
	// when it paired with none: no link names it, an earlier link claimed
	// it or its prompt, or its link failed SM 2.2's checks (pairRelays).
	Prompt string `json:"prompt,omitempty"`
	// Origin is the origin of the turn its prompt started, when the turn
	// went by this record: relayed when it witnessed the prompt and rule 9
	// took it, human_typed or human_pasted when rules 6 and 7 held at its
	// stamp, scheduled or subagent for the app's own declarations, headless
	// for a guest's prompt or for one rule 4 took because the record didn't
	// witness it.
	Origin event.Origin `json:"origin,omitempty"`
	// decided is when the record stops changing: when it paired, or, for
	// one that hasn't, once RELAY_MATCH_S after its stamp has passed and no
	// prompt can pair with it any more. One that wasn't decided in time for
	// its stamp (decidedInTime) can pair with no prompt, so it is decided
	// when it is written.
	decided int64
}

func relayCmp(a, b RelayRecord) int {
	if c := cmp.Compare(a.T, b.T); c != 0 {
		return c
	}
	return strings.Compare(a.ID, b.ID)
}

// relayRec is a relay.message while the timeline is derived.
type relayRec struct {
	pos int // its index in the records
	d   *event.RelayMessage
	// prompt is the index of the prompt.submit it paired with, or -1, and
	// turn the turn that went by it (finishTurn).
	prompt int
	turn   *turnBuild
}

// pairRelays pairs each relay.message with the prompt.submit its message's
// run delivered (SM 2.2 rule 9), and indexes each thread's messages for the
// cap on a relayed lead.
//
// The pairing is the app's structure, never text: the agent writes it on
// whichever of the two records arrives second, as the message's prompt or
// the prompt's relay, and the scorer reads only those links. Links count in
// record order, by t and then seq, the order the agent wrote them in: the
// first link claims both records it names, and a later one naming either is
// void, so one message pairs with one prompt and one prompt with one
// message. A link that names a record the state doesn't hold, or one of
// the wrong kind, claims nothing, and neither does the relay of a prompt
// that came by any path but a hook or a log reader: only those prompts can
// pair (pairs), so the agent writes no link on any other, and a side
// door's relay is whatever its sender wrote, which mustn't void the
// message's real pairing (DESIGN 5.2). A claimed pair pairs when it passes
// the checks (pairs).
func (d *deriver) pairRelays() {
	at := map[int]*relayRec{}
	for i, e := range d.recs {
		if p, ok := e.Data.(*event.RelayMessage); ok {
			r := &relayRec{pos: i, d: p, prompt: -1}
			d.relays = append(d.relays, r)
			at[i] = r
		}
	}
	if len(d.relays) == 0 {
		return
	}
	d.threads = map[string][]int64{}
	for _, r := range d.relays {
		if r.d.Declared == event.RelayDeclaredNone {
			d.threads[r.d.ThreadKey] = append(d.threads[r.d.ThreadKey], r.d.SentAt)
		}
	}
	for _, ts := range d.threads {
		slices.Sort(ts)
	}
	d.relayOf = map[int]*relayRec{}
	claimed := map[int]bool{}
	for i, e := range d.recs {
		var r *relayRec
		prompt := -1
		switch p := e.Data.(type) {
		case *event.RelayMessage:
			if p.Prompt == "" {
				continue
			}
			if j, ok := d.recIndex(p.Prompt); ok {
				if _, isPrompt := d.recs[j].Data.(*event.PromptSubmit); isPrompt {
					r, prompt = at[i], int(j)
				}
			}
		case *event.PromptSubmit:
			if p.Relay == "" || !nativeVia(e.Via) {
				continue
			}
			if j, ok := d.recIndex(p.Relay); ok && at[int(j)] != nil {
				r, prompt = at[int(j)], i
			}
		}
		if r == nil || claimed[r.pos] || claimed[prompt] {
			continue
		}
		claimed[r.pos], claimed[prompt] = true, true
		if d.pairs(r, prompt) {
			r.prompt = prompt
			d.relayOf[prompt] = r
		}
	}
}

// pairs reports whether relay record r and the prompt.submit at index pi
// pass SM 2.2's checks for a pairing: the prompt came by a hook or a log
// reader, the only prompts a relay record can witness (DESIGN 5.2); the
// record's session is the prompt's, or none when the reader didn't know
// it; the prompt came no earlier than the message's stamp and at most
// RELAY_MATCH_S after it; and the record was decided in time for its stamp
// (decidedInTime).
func (d *deriver) pairs(r *relayRec, pi int) bool {
	re, pe := d.recs[r.pos], d.recs[pi]
	sent := r.d.SentAt
	return nativeVia(pe.Via) && (re.SessionKey == event.NoSession || re.SessionKey == pe.SessionKey) &&
		sent <= pe.T && pe.T-sent <= d.r.relayMatch && d.r.decidedInTime(re.T, sent)
}

// relayAheadMS is how much later than the read that decided its message an
// app's stamp may be (SM 2.2, What witnesses a prompt). The app's server
// and the agent run on one machine under one clock (D-relayed), so a later
// stamp comes from a row written by hand or a clock stepped back, and the
// reader refuses the row.
const relayAheadMS = 2_000

// decidedInTime reports whether a relay record written at t, when the
// reader decided its message, came in time for the message's stamp sent
// (SM 2.2): at most RELAY_SEEN_LAG_S after it, which turns away rows the
// app imported or a script wrote with an old stamp, and at most
// relayAheadMS before it. The reader holds itself to both, and the scorer
// repeats them, so a client that skips them pairs nothing by it.
func (r rules) decidedInTime(t, sent int64) bool {
	return t-sent <= r.relaySeenLag && sent-t <= relayAheadMS
}

// turnRelay returns the relay record a turn's prompt paired with: its
// primary record's, else the first in record order that one of its other
// prompt records paired with, or nil.
func (d *deriver) turnRelay(tb *turnBuild) *relayRec {
	if tb.primary < 0 || len(d.relayOf) == 0 {
		return nil
	}
	if r := d.relayOf[tb.primary]; r != nil {
		return r
	}
	for _, i := range tb.prompts {
		if r := d.relayOf[i]; r != nil {
			return r
		}
	}
	return nil
}

// witnesses reports whether relay record r, paired with a prompt whose
// session last started with st, witnesses the prompt (SM 2.2 rule 9): the
// app says a person sent the message, declared none, and the session's
// nearest non-shell ancestor is the app's own server, parent_class
// agent_app. A record of declared cron or cross_session gives its prompt
// the app's declaration instead, and one for any other session witnesses
// nothing, so rules 4 to 8 read that prompt as they read any.
func witnesses(r *relayRec, st *event.SessionStart) bool {
	return r != nil && r.d.Declared == event.RelayDeclaredNone && st != nil && st.ParentClass == event.ParentAgentApp
}

// relayClass is the class of a relayed prompt (SM 2.2): SM 2.2's class
// rules over the app's own text, of which the record carries the go-ahead
// test and typed is the length, with the answer class read as for any
// prompt, from the prompt's answers_question and the session's last output.
// The record carries no error signature, so a relayed prompt is never an
// error paste.
func (d *deriver) relayClass(tb *turnBuild, r *relayRec, p *event.PromptSubmit, typed int64) event.PromptClass {
	app := &event.PromptSubmit{TypedLenEst: int(typed), LowEffort: r.d.LowEffort, AnswersQuestion: p.AnswersQuestion}
	e := d.recs[tb.primary]
	return promptClass(app, tb.sess.askedBefore(e.Seq, e.Via), d.r.substantive, d.r.errorPasteMax)
}

// prevMessage returns the stamp of the last relay.message of declared none
// in r's thread stamped before r, paired or not, which caps r's relayed
// lead (SM 2.3), and whether there is one.
func (d *deriver) prevMessage(r *relayRec) (int64, bool) {
	ts := d.threads[r.d.ThreadKey]
	i := sort.Search(len(ts), func(i int) bool { return ts[i] >= r.d.SentAt })
	if i == 0 {
		return 0, false
	}
	return ts[i-1], true
}

// relayRecords returns what became of each relay.message.
func (d *deriver) relayRecords() []RelayRecord {
	var out []RelayRecord
	for _, r := range d.relays {
		e := d.recs[r.pos]
		rr := RelayRecord{ID: e.EventID, T: e.T, SentAt: r.d.SentAt, SessionKey: e.SessionKey,
			ThreadKey: r.d.ThreadKey, Declared: r.d.Declared, decided: max(e.T, r.d.SentAt+d.r.relayMatch)}
		if !d.r.decidedInTime(e.T, r.d.SentAt) {
			rr.decided = e.T
		}
		if r.prompt >= 0 {
			pe := d.recs[r.prompt]
			rr.Prompt, rr.decided = pe.EventID, max(e.T, pe.T)
			if r.turn != nil {
				rr.Origin = r.turn.turn.Origin
			}
		}
		out = append(out, rr)
	}
	slices.SortFunc(out, relayCmp)
	return out
}

// relayHolds returns, for each relay.message of declared none that hasn't
// paired and still can, the earliest time a lead it could open starts:
// Step keeps FinalBefore from passing it until RELAY_MATCH_S after the
// message's stamp (SM 2.3). A relayed lead is typing time, at most
// COMPOSE_MAX_S, and a prompt rules 6 and 7 make typed at the stamp has a
// prompt's lead, think part included, so the hold covers the longest lead a
// prompt can have, ending at the stamp. A record that came more than
// RELAY_SEEN_LAG_S after its stamp, or more than relayAheadMS before it,
// can pair with nothing and holds nothing (decidedInTime). A pairing that
// comes after its lead's minutes are final changes nothing in them.
func (d *deriver) relayHolds() []int64 {
	var out []int64
	lead := max(d.r.thinkLead, d.r.answerLead, d.r.lowLead) + d.r.composeMax
	for _, r := range d.relays {
		sent := r.d.SentAt
		if r.prompt >= 0 || r.d.Declared != event.RelayDeclaredNone || !d.r.decidedInTime(d.recs[r.pos].T, sent) ||
			d.now > sent+d.r.relayMatch {
			continue
		}
		out = append(out, sent-lead)
	}
	return out
}
