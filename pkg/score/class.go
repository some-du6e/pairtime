// SPDX-License-Identifier: AGPL-3.0-or-later

package score

import (
	"github.com/some-du6e/pairtime/pkg/event"
	"github.com/some-du6e/pairtime/pkg/params"
)

// PromptClass returns SM 2.2's class of prompt p, the one the scorer gives
// a person's prompt: answer when p answers a question or asked is set, then
// low_effort when the text test marked it, error_paste for an error
// signature with fewer than ERROR_PASTE_TEXT_MAX typed characters around
// it, substantive from SUBSTANTIVE_CHARS typed characters, and low_effort
// for the rest. asked says whether the session's last agent output before
// p asked a question or its last turn stopped on one; Questions keeps it
// for an agent that classes each prompt as it records it.
//
// The ledger writer calls it when it writes a prompt, of whatever origin
// the scorer later gives it, and records the class as the body's f.pc
// (DESIGN 3.6, "prompt_class as computed"; ledger-v1 10.7). p's
// low_effort and error_sig are what the agent derived from the prompt's
// text: LowEffort's text test, and ErrorSig's keyed signature.
func PromptClass(t *params.Table, p *event.PromptSubmit, asked bool) event.PromptClass {
	return promptClass(p, asked, t.Int(params.SUBSTANTIVE_CHARS), t.Int(params.ERROR_PASTE_TEXT_MAX))
}

// questionSessions bounds how many sessions a Questions remembers. When it
// would hold more, it forgets the one it heard from least recently.
const questionSessions = 1024

// Questions follows, record by record, what PromptClass's asked needs:
// whether each session's last agent.output asked a question and whether its
// last agent.stop stopped on one (SM 2.2, the answer class). It applies the
// scorer's side-door rule to the records it has seen: once a session has a
// record from a hook or a log reader, outputs and stops that came through
// a side door (track, plugin, bridge) don't count for it, those it saw
// before included (DESIGN 5.2). The scorer applies that rule knowing every
// record of the session, so a side-door output in a session whose first
// hook record is still to come counts here and not there.
//
// The zero value is ready to use. It isn't safe for concurrent use.
type Questions struct {
	sessions map[string]*questionState
	clock    uint64
}

// questionState is what a Questions remembers of one session. The "all"
// fields count every record, the "kept" fields only those from a path that
// isn't a side door.
type questionState struct {
	native             bool
	outputAll, stopAll bool
	outputKept         bool
	stopKept           bool
	seen               uint64
}

// Observe takes the next record the agent recorded. Records that belong to
// no harness session change nothing.
func (q *Questions) Observe(e event.Event) {
	if e.Kind() == "" {
		return
	}
	key := harnessRecord(e)
	if key == "" {
		return
	}
	s := q.state(key)
	if nativeVia(e.Via) {
		s.native = true
	}
	kept := !sideDoor(e.Via)
	switch p := e.Data.(type) {
	case *event.AgentOutput:
		s.outputAll = p.AsksQuestion
		if kept {
			s.outputKept = p.AsksQuestion
		}
	case *event.AgentStop:
		s.stopAll = p.StopReason == event.StopQuestion
		if kept {
			s.stopKept = p.StopReason == event.StopQuestion
		}
	}
}

// Asked reports whether session's last agent output asked a question, or
// its last turn stopped on one, among the records observed so far: the
// asked of PromptClass for the session's next prompt.
func (q *Questions) Asked(session string) bool {
	s := q.sessions[session]
	if s == nil {
		return false
	}
	if s.native {
		return s.outputKept || s.stopKept
	}
	return s.outputAll || s.stopAll
}

// state returns session key's state, making it, and marks it as heard from
// now.
func (q *Questions) state(key string) *questionState {
	if q.sessions == nil {
		q.sessions = make(map[string]*questionState)
	}
	q.clock++
	s := q.sessions[key]
	if s == nil {
		if len(q.sessions) >= questionSessions {
			q.forgetOldest()
		}
		s = &questionState{}
		q.sessions[key] = s
	}
	s.seen = q.clock
	return s
}

func (q *Questions) forgetOldest() {
	oldest, at := "", ^uint64(0)
	for k, s := range q.sessions {
		if s.seen < at {
			oldest, at = k, s.seen
		}
	}
	delete(q.sessions, oldest)
}
