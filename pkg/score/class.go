// SPDX-License-Identifier: AGPL-3.0-or-later

package score

import (
	"cmp"
	"errors"
	"fmt"
	"slices"

	"github.com/some-du6e/pairtime/pkg/event"
	"github.com/some-du6e/pairtime/pkg/params"
)

// PromptClass returns SM 2.2's class of prompt p, the one the scorer gives
// a person's prompt: answer when p answers a question or asked is set, then
// low_effort when the text test marked it, error_paste for an error
// signature with fewer than ERROR_PASTE_TEXT_MAX typed characters around
// it, substantive from SUBSTANTIVE_CHARS typed characters, and low_effort
// for the rest. asked is Questions.Asked for p's session and via, from the
// records before p in seq order.
//
// The ledger writer calls it when it writes a prompt, of whatever origin
// the scorer later gives it, and records the class as the body's f.pc
// (DESIGN 3.6, "prompt_class as computed"; ledger-v1 10.7). p's
// low_effort and error_sig are what the agent derived from the prompt's
// text: LowEffort's text test, and ErrorSig's keyed signature.
func PromptClass(t *params.Table, p *event.PromptSubmit, asked bool) event.PromptClass {
	return promptClass(p, asked, t.Int(params.SUBSTANTIVE_CHARS), t.Int(params.ERROR_PASTE_TEXT_MAX))
}

// TrackClass returns the class of a `pairtime track` call, the one the
// scorer gives the anchor the call opens (DESIGN 5.4): low_effort when the
// go-ahead test marked the label, error_paste for an error signature with
// fewer than ERROR_PASTE_TEXT_MAX characters of text_len, substantive from
// SUBSTANTIVE_CHARS characters of it, low_effort under that, and never
// answer. p's low_effort and error_sig are what the ledger writer derived
// from the label after pkg/redact, and error_sig is read back from the
// record's f.es when the record is scored from the ledger. The writer
// records the class as a track record's f.pc (ledger-v1 10.7).
func TrackClass(t *params.Table, p *event.CLITrack) event.PromptClass {
	return trackClass(p, t.Int(params.SUBSTANTIVE_CHARS), t.Int(params.ERROR_PASTE_TEXT_MAX))
}

// trackClass is TrackClass with SUBSTANTIVE_CHARS and ERROR_PASTE_TEXT_MAX
// given.
func trackClass(p *event.CLITrack, substantive, errorPasteMax int64) event.PromptClass {
	n := count(p.TextLen)
	switch {
	case p.LowEffort:
		return event.ClassLowEffort
	case p.ErrorSig != "" && n < errorPasteMax:
		return event.ClassErrorPaste
	case n >= substantive:
		return event.ClassSubstantive
	}
	return event.ClassLowEffort
}

// questionSessions bounds how many sessions a Questions remembers. When it
// would hold more, it forgets the one it heard from least recently.
const questionSessions = 1024

// Questions follows, record by record, what PromptClass's asked needs:
// whether each session's last agent.output asked a question and whether its
// last agent.stop stopped on one (SM 2.2, the answer class). It applies
// ledger-v1 10.7's rule as the records arrive: once a session has a record
// from a hook or a log reader, outputs and stops that came through a side
// door (track, plugin, bridge) don't count for it, those it saw before
// included, and they don't count for a hook or log prompt either (DESIGN
// 5.2). pkg/score's class follows the same rule (session.askedBefore).
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
// asked of PromptClass for the session's next prompt, whose record came by
// via. Only the outputs and stops that didn't come through a side door
// count when the session has a record from a hook or a log reader, or
// when via is one of those, so a hook or log prompt never reads a side
// door's question; all of them count otherwise.
func (q *Questions) Asked(session string, via event.Via) bool {
	s := q.sessions[session]
	if s == nil {
		return false
	}
	if s.native || nativeVia(via) {
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

// QuestionsState is what a Questions remembers, so a writer that keeps
// classing prompts across restarts can keep it too: State takes it, and a
// Questions given it by Restore classes the prompts after it as the one it
// came from would. The ledger writer keeps one in its seal frames.
type QuestionsState struct {
	// Clock counts the records the Questions heard a session from. It
	// orders the sessions, and the one heard from least recently is the
	// first forgotten.
	Clock    uint64         `json:"clock"`
	Sessions []AskedSession `json:"sessions,omitempty"`
}

// AskedSession is one session's part of a QuestionsState. The "all"
// fields count every record of the session, the "kept" ones only those
// that didn't come through a side door, and Native says a hook or a log
// reader has reported the session.
type AskedSession struct {
	Key        string `json:"key"`
	Native     bool   `json:"native,omitempty"`
	OutputAll  bool   `json:"output_all,omitempty"`
	StopAll    bool   `json:"stop_all,omitempty"`
	OutputKept bool   `json:"output_kept,omitempty"`
	StopKept   bool   `json:"stop_kept,omitempty"`
	// Seen is Clock when the Questions last heard from the session.
	Seen uint64 `json:"seen"`
}

// State returns what q remembers, its sessions in the order it last heard
// from them.
func (q *Questions) State() QuestionsState {
	st := QuestionsState{Clock: q.clock}
	for key, s := range q.sessions {
		st.Sessions = append(st.Sessions, AskedSession{Key: key, Native: s.native, OutputAll: s.outputAll,
			StopAll: s.stopAll, OutputKept: s.outputKept, StopKept: s.stopKept, Seen: s.seen})
	}
	slices.SortFunc(st.Sessions, func(a, b AskedSession) int { return cmp.Compare(a.Seen, b.Seen) })
	return st
}

// Restore makes q remember st and nothing else. It refuses a state no
// Questions could have: more than questionSessions sessions, a session with
// no key or listed twice, or one heard from at no point of the clock, after
// it, or at the same point as another.
func (q *Questions) Restore(st QuestionsState) error {
	if len(st.Sessions) > questionSessions {
		return fmt.Errorf("score: a Questions remembers at most %d sessions", questionSessions)
	}
	sessions := make(map[string]*questionState, len(st.Sessions))
	seen := make(map[uint64]bool, len(st.Sessions))
	for _, a := range st.Sessions {
		if a.Key == "" || sessions[a.Key] != nil || a.Seen == 0 || a.Seen > st.Clock || seen[a.Seen] {
			return errors.New("score: a session in the state isn't one a Questions could remember")
		}
		seen[a.Seen] = true
		sessions[a.Key] = &questionState{native: a.Native, outputAll: a.OutputAll, stopAll: a.StopAll,
			outputKept: a.OutputKept, stopKept: a.StopKept, seen: a.Seen}
	}
	q.sessions, q.clock = sessions, st.Clock
	return nil
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
