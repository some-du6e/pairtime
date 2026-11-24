// SPDX-License-Identifier: AGPL-3.0-or-later

package t3code

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/adapters"
	"github.com/some-du6e/pairtime/pkg/event"
)

// rec is a record a test expects: the message it is of, and its fields.
type rec struct {
	id        string        // the message id
	created   time.Duration // its stamp
	client    event.RelayClient
	declared  event.RelayDeclared
	lenChars  int
	lowEffort bool
	class     event.PromptClass
	session   string // the session key, "" for none
	project   string
	thread    string // the thread id, th1 when ""
}

// eventID is the event_id the reader gives the record of message id,
// stamped at created.
func eventID(id string, created time.Duration) string {
	return recordID(adapters.FixtureKeys(), id, at(created), base.Add(created).UnixMilli())
}

// checkMessages checks that got holds exactly the records want lists.
func checkMessages(t *testing.T, got []Message, seen time.Time, want []rec) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d records, want %d: %+v", len(got), len(want), got)
	}
	keys := adapters.FixtureKeys()
	for i, w := range want {
		m := got[i]
		thread := w.thread
		if thread == "" {
			thread = "th1"
		}
		harness := event.HarnessClaudeCode
		checks := []struct {
			what      string
			got, want any
		}{
			{"event_id", m.EventID, eventID(w.id, w.created)},
			{"seen at", m.SeenAt, seen},
			{"harness", m.Harness, harness},
			{"session_key", m.SessionKey, w.session},
			{"project_key", m.ProjectKey, w.project},
			{"app", m.Data.App, event.RelayAppT3Code},
			{"client", m.Data.Client, w.client},
			{"sent_at", m.Data.SentAt, base.Add(w.created).UnixMilli()},
			{"thread_key", m.Data.ThreadKey, keys.App("t3_code", "thread", thread)},
			{"declared", m.Data.Declared, w.declared},
			{"len_chars", m.Data.LenChars, w.lenChars},
			{"low_effort", m.Data.LowEffort, w.lowEffort},
			{"class", m.Class, w.class},
		}
		for _, c := range checks {
			if c.got != c.want {
				t.Errorf("record %d (%s): %s is %v, want %v", i, w.id, c.what, c.got, c.want)
			}
		}
		if err := m.Data.Validate(); err != nil {
			t.Errorf("record %d (%s) isn't a valid relay.message: %v", i, w.id, err)
		}
	}
}

// readsText reports whether a run the spy saw read a message's text.
func readsText(runs []string) bool {
	return slices.ContainsFunc(runs, func(r string) bool { return strings.Contains(r, "1, 65536)") })
}

// web is the record of a message a person sent from the web client, in
// thread th1 of /work/alpha, whose run went to session sess-1.
func web(id string, created time.Duration, lenChars int, lowEffort bool, class event.PromptClass) rec {
	return rec{id: id, created: created, client: event.RelayClientWeb, declared: event.RelayDeclaredNone,
		lenChars: lenChars, lowEffort: lowEffort, class: class, session: session("sess-1"), project: "p_alpha"}
}

// TestDecision holds the reader to DESIGN 5.1's decision and SM 2.2 rule
// 9's witness test: which rows get a record, with what in it, and which
// rule turned the others away.
func TestDecision(t *testing.T) {
	const text = "please fix the flaky parser test" // 32 characters
	substantive := event.ClassSubstantive
	cases := []struct {
		name    string
		rows    func(s *store)
		folders folders       // tracked() when nil
		read    time.Duration // when the reader reads, 5 s when 0
		want    []rec
		counts  Counts
		guests  []GuestRun
		// noText says the reader reads no text of the rows, though it
		// records them.
		noText bool
	}{
		{name: "a message a person sent from the web client, with a run of its own, is recorded as declared none",
			rows: func(s *store) { s.sent("m1", 0, text) },
			want: []rec{web("m1", 0, 32, false, substantive)}, counts: Counts{Witnessed: 1}},
		{name: "T3's phone app, creationSource mobile, makes the record's client mobile",
			rows: func(s *store) { s.sentWith(message{id: "m1", source: "mobile", text: text}) },
			want: []rec{{id: "m1", client: event.RelayClientMobile, declared: event.RelayDeclaredNone, lenChars: 32,
				class: substantive, session: session("sess-1"), project: "p_alpha"}},
			counts: Counts{Witnessed: 1}},
		{name: "scheduledTaskId, a firing of T3's scheduler or a webhook, makes the record declared cron, of length 0 and no text read",
			rows: func(s *store) {
				s.sentWith(message{id: "m1", text: text, extra: map[string]any{"scheduledTaskId": "task-1"}})
			},
			want: []rec{{id: "m1", client: event.RelayClientWeb, declared: event.RelayDeclaredCron,
				class: event.ClassLowEffort, session: session("sess-1"), project: "p_alpha"}},
			counts: Counts{Declared: 1}, noText: true},
		{name: "senderThreadId, another thread's agent, makes the record declared cross_session, of length 0 and no text read",
			rows: func(s *store) {
				s.sentWith(message{id: "m1", text: text, extra: map[string]any{"senderThreadId": "thread-parent"}})
			},
			want: []rec{{id: "m1", client: event.RelayClientWeb, declared: event.RelayDeclaredCrossSession,
				class: event.ClassLowEffort, session: session("sess-1"), project: "p_alpha"}},
			counts: Counts{Declared: 1}, noText: true},
		{name: "linkedSender, an agent in a linked environment, makes the record declared cross_session, of length 0 and no text read",
			rows: func(s *store) {
				s.sentWith(message{id: "m1", text: text, extra: map[string]any{"linkedSender": map[string]any{
					"environmentId": "env-2", "threadId": "thread-far", "title": "invented title"}}})
			},
			want: []rec{{id: "m1", client: event.RelayClientWeb, declared: event.RelayDeclaredCrossSession,
				class: event.ClassLowEffort, session: session("sess-1"), project: "p_alpha"}},
			counts: Counts{Declared: 1}, noText: true},
		{name: "idleCompaction, the /compact T3's composer sends on its own, gets no record",
			rows: func(s *store) {
				s.sentWith(message{id: "m1", text: "/compact", extra: map[string]any{"idleCompaction": "onReturn"}})
			},
			counts: Counts{Marked: 1}},
		{name: "workflowScript, a workflow's script, gets no record",
			rows: func(s *store) {
				s.sentWith(message{id: "m1", text: text, extra: map[string]any{"workflowScript": true}})
			},
			counts: Counts{Marked: 1}},
		{name: "notification, a wake message, gets no record",
			rows: func(s *store) {
				s.sentWith(message{id: "m1", text: text, extra: map[string]any{"notification": map[string]any{
					"source": "system", "outcome": "completed", "summary": "invented summary"}}})
			},
			counts: Counts{Marked: 1}},
		{name: "delegatedCompletion, delegated tasks' results, gets no record",
			rows: func(s *store) {
				s.sentWith(message{id: "m1", text: text, extra: map[string]any{"delegatedCompletion": map[string]any{
					"parentRunId": "run-0", "generation": 1, "taskIds": []string{"node-1"}}}})
			},
			counts: Counts{Marked: 1}},
		{name: "an agent's message, createdBy agent, gets no record",
			rows: func(s *store) { s.sentWith(message{id: "m1", by: "agent", source: "mcp", text: text}) }},
		{name: "a message T3 made itself, creationSource server, gets no record",
			rows: func(s *store) { s.sentWith(message{id: "m1", source: "server", text: text}) }},
		{name: "a provider's subagent message, creationSource provider, gets no record",
			rows: func(s *store) { s.sentWith(message{id: "m1", by: "agent", source: "provider", text: text}) }},
		{name: "a guest's message, marked context.guest, gets no record, and its run is watched",
			rows: func(s *store) {
				s.sentWith(message{id: "m1", text: "Message from guest Ada, sent by the owner:\n\ninvented words",
					extra: map[string]any{"context": map[string]any{"version": 1, "records": []any{},
						"guest": map[string]any{"guestMessageId": "g-1", "name": "Ada", "text": "invented words"}}}})
			},
			counts: Counts{Guests: 1}, guests: []GuestRun{{SentAt: base.UnixMilli()}}},
		{name: "a message whose text opens as T3 opens a guest's is taken for a guest's, though it lost its mark",
			rows: func(s *store) {
				s.sentWith(message{id: "m1", text: "Message from guest Ada, sent by the owner:\n\ninvented words"})
			},
			counts: Counts{Guests: 1}, guests: []GuestRun{{SentAt: base.UnixMilli()}}},
		{name: "the guest opening counts after white space and an Ultrathink: line, as the reader reads a message's text",
			rows: func(s *store) {
				s.sentWith(message{id: "m1", text: " \t\nUltrathink: \r\n\u3000Message from guest Ada, sent by the owner:\n\ninvented words"})
			},
			counts: Counts{Guests: 1}, guests: []GuestRun{{SentAt: base.UnixMilli()}}},
		{name: "an Ultrathink: with words after it on its line was typed, so those words open no guest's message",
			rows:   func(s *store) { s.sent("m1", 0, "Ultrathink: Message from guest Ada is how it starts") },
			want:   []rec{web("m1", 0, len("Ultrathink: Message from guest Ada is how it starts"), false, substantive)},
			counts: Counts{Witnessed: 1}},
		{name: "a steer, whose run names another message, gets no record",
			rows: func(s *store) {
				s.sent("m0", 0, text)
				s.message(message{id: "m1", thread: "th1", run: "r-m0", created: time.Second, text: "and the lexer too"})
			},
			want: []rec{web("m0", 0, 32, false, substantive)}, counts: Counts{Witnessed: 1, NoRun: 1}},
		{name: "a queued run T3 turns into a steer once the running turn starts (steerWhenReadyRunId) gets no record",
			rows: func(s *store) {
				s.message(message{id: "m1", thread: "th1", run: "r-m1", text: text})
				s.run(run{id: "r-m1", thread: "th1", message: "m1", steerWhenReady: true})
			},
			counts: Counts{NoRun: 1}},
		{name: "a run requested at another time than the message's stamp isn't the message's own",
			rows: func(s *store) {
				s.message(message{id: "m1", thread: "th1", run: "r-m1", text: text})
				s.run(run{id: "r-m1", thread: "th1", message: "m1", requested: time.Second, started: ptr(time.Second)})
			},
			counts: Counts{NoRun: 1}},
		{name: "a message the reader decides more than RELAY_SEEN_LAG_S after its stamp is turned away by the age test",
			rows: func(s *store) { s.sent("m1", 0, text) }, read: 60*time.Second + time.Millisecond,
			counts: Counts{Aged: 1}},
		{name: "a message the reader decides RELAY_SEEN_LAG_S after its stamp is recorded",
			rows: func(s *store) { s.sent("m1", 0, text) }, read: 60 * time.Second,
			want: []rec{web("m1", 0, 32, false, substantive)}, counts: Counts{Witnessed: 1}},
		{name: "a message stamped more than 2 s after the read that decides it is refused, and counted for doctor",
			rows:   func(s *store) { s.sent("m1", 7*time.Second+time.Millisecond, text) },
			counts: Counts{Ahead: 1}},
		{name: "a message stamped 2 s after the read that decides it is recorded",
			rows: func(s *store) { s.sent("m1", 7*time.Second, text) },
			want: []rec{web("m1", 7*time.Second, 32, false, substantive)}, counts: Counts{Witnessed: 1}},
		{name: "a message edited before the reader first read it, its updated_at later than its stamp, witnesses nothing",
			rows:   func(s *store) { s.sentWith(message{id: "m1", updated: ptr(2 * time.Second), text: text}) },
			counts: Counts{Edited: 1}},
		{name: "a thread T3 runs on another provider than Claude Code gets no record",
			rows: func(s *store) {
				s.providerThread("pt-codex", "th1", "codex", "codex-thread-1")
				s.message(message{id: "m1", thread: "th1", run: "r-m1", text: text})
				s.run(run{id: "r-m1", thread: "th1", message: "m1", providerThread: "pt-codex", started: ptr(time.Second)})
			},
			counts: Counts{OtherHarness: 1}},
		{name: "a folder pairtime doesn't track or ignores gets no record, and none of its text is read",
			rows: func(s *store) { s.sent("m1", 0, text) }, folders: folders{}},
		{name: "the thread's worktree, not its project's root, is the folder the test reads",
			rows: func(s *store) {
				s.thread("th2", "p1", "/wt/beta")
				s.sentWith(message{id: "m1", thread: "th2", text: text})
			},
			folders: folders{"/wt/beta": {State: FolderTracked, Project: "p_beta"}},
			want: []rec{{id: "m1", thread: "th2", client: event.RelayClientWeb, declared: event.RelayDeclaredNone,
				lenChars: 32, class: substantive, session: session("sess-1"), project: "p_beta"}},
			counts: Counts{Witnessed: 1}},
		{name: "a message whose run hasn't started yet is recorded with no session",
			rows: func(s *store) {
				s.message(message{id: "m1", thread: "th1", run: "r-m1", text: text})
				s.run(run{id: "r-m1", thread: "th1", message: "m1"})
				s.attempt("r-m1", 1, "", "")
			},
			want: []rec{{id: "m1", client: event.RelayClientWeb, declared: event.RelayDeclaredNone, lenChars: 32,
				class: substantive, project: "p_alpha"}},
			counts: Counts{Witnessed: 1}},
		{name: "T3's Ultrathink: line is not typing: only what follows it counts, here a go-ahead",
			rows: func(s *store) { s.sent("m1", 0, "Ultrathink:\nfix it") },
			want: []rec{web("m1", 0, 6, true, event.ClassLowEffort)}, counts: Counts{Witnessed: 1}},
		{name: "T3's placeholder for a message of attachments alone counts as nothing typed",
			rows: func(s *store) { s.sent("m1", 0, attachmentOnly) },
			want: []rec{web("m1", 0, 0, true, event.ClassLowEffort)}, counts: Counts{Witnessed: 1}},
		{name: "a context reference's token is not typing",
			rows: func(s *store) {
				s.sent("m1", 0, "look at [build.log](t3-context://v1/file/file_build-log) and the failing build log")
			},
			want: []rec{web("m1", 0, len("look at  and the failing build log"), false, substantive)}, counts: Counts{Witnessed: 1}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newStore(t)
			sp := newSpy(t, s.sqlite3)
			c := &clock{}
			c.set(0)
			f := tc.folders
			if f == nil {
				f = tracked()
			}
			r := s.reader(c, f, State{})
			r.sqlite = &Runner{Path: sp.path}
			mustPoll(t, r)
			tc.rows(s)
			s.flush()
			read := tc.read
			if read == 0 {
				read = 5 * time.Second
			}
			c.set(read)
			b := mustPoll(t, r)
			checkMessages(t, b.Messages, c.t, tc.want)
			if got := r.Status().Counts; got != tc.counts {
				t.Errorf("counts are %+v, want %+v", got, tc.counts)
			}
			if !slices.Equal(b.Guests, tc.guests) {
				t.Errorf("guests are %+v, want %+v", b.Guests, tc.guests)
			}
			if (len(tc.want) == 0 || tc.noText) && readsText(sp.runs(t)) {
				t.Error("the reader read the text of a row whose text it doesn't measure")
			}
		})
	}
}

// TestHeldFolder holds the reader to its wait for pairtime's resolver: a
// row whose folder the resolver hasn't answered for is read no further, and
// is decided once it answers, while the row is young enough to record.
func TestHeldFolder(t *testing.T) {
	const text = "please fix the flaky parser test"
	cases := []struct {
		name    string
		answer  time.Duration // when the resolver answers that it tracks the folder
		records int
		counts  Counts
	}{
		{"a row is recorded once the resolver answers within RELAY_SEEN_LAG_S", 20 * time.Second, 1, Counts{Witnessed: 1}},
		{"a row whose folder is still unanswered RELAY_SEEN_LAG_S after its stamp is turned away by the age test",
			61 * time.Second, 0, Counts{Aged: 1}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newStore(t)
			sp := newSpy(t, s.sqlite3)
			c := &clock{}
			c.set(0)
			answer := Folder{State: FolderPending}
			r, err := New(Options{
				Stores: []Store{{Path: s.path, From: FromDefault}},
				SQLite: &Runner{Path: sp.path},
				Keys:   adapters.FixtureKeys(),
				Folder: func(string) Folder { return answer },
				Now:    c.now,
				Zone:   time.UTC,
			})
			if err != nil {
				t.Fatal(err)
			}
			mustPoll(t, r)
			s.sent("m1", 0, text)
			s.flush()
			for _, when := range []time.Duration{5 * time.Second, 15 * time.Second} {
				c.set(when)
				if b := mustPoll(t, r); len(b.Messages) != 0 {
					t.Fatalf("a row of an unanswered folder was recorded at %v", when)
				}
			}
			if readsText(sp.runs(t)) {
				t.Error("the reader read the text of a row whose folder the resolver hadn't answered for")
			}
			answer = Folder{State: FolderTracked, Project: "p_alpha"}
			c.set(tc.answer)
			b := mustPoll(t, r)
			var want []rec
			if tc.records > 0 {
				want = []rec{web("m1", 0, 32, false, event.ClassSubstantive)}
			}
			checkMessages(t, b.Messages, c.t, want)
			if got := r.Status().Counts; got != tc.counts {
				t.Errorf("counts are %+v, want %+v", got, tc.counts)
			}
		})
	}
}

// env is a store, its reader and the reader's clock, after a first read of
// the empty store.
type env struct {
	t *testing.T
	s *store
	c *clock
	r *Reader
}

func newEnv(t *testing.T) *env {
	t.Helper()
	s := newStore(t)
	c := &clock{}
	c.set(0)
	e := &env{t: t, s: s, c: c, r: s.reader(c, tracked(), State{})}
	mustPoll(t, e.r)
	return e
}

// poll writes what the store queued, sets the clock and reads.
func (e *env) poll(now time.Duration) Batch {
	e.t.Helper()
	e.s.flush()
	e.c.set(now)
	return mustPoll(e.t, e.r)
}

// pair writes what the store queued, sets the clock and asks about a
// prompt of session sess, stamped at pt.
func (e *env) pair(now time.Duration, sess string, pt time.Duration, id string) (Pairing, Batch) {
	e.t.Helper()
	e.s.flush()
	e.c.set(now)
	p, b, err := e.r.Pair(context.Background(), Prompt{SessionKey: session(sess), T: base.Add(pt).UnixMilli(), EventID: id})
	if err != nil {
		e.t.Fatalf("Pair: %v", err)
	}
	return p, b
}

// promptOf returns the Prompt each record of b names, by message id.
func promptOf(b Batch) map[string]string {
	out := map[string]string{}
	for _, m := range b.Messages {
		out[m.EventID] = m.Data.Prompt
	}
	return out
}

// TestPairing holds the reader to SM 2.2 rule 9's pairing (DESIGN 5.1, 4.1
// E-13): a message pairs with the first prompt its own run delivered, in
// the session the run went to, from 2 s before the run's start to its end
// but not at or before the end of an earlier run of that session, and at
// most RELAY_MATCH_S after the stamp, one to one, and the record that
// comes second names the other.
func TestPairing(t *testing.T) {
	const text = "please fix the flaky parser test"
	cases := []struct {
		name string
		run  func(t *testing.T, e *env)
	}{
		{"a prompt that reaches pairtime before the reader read its message is named by the record, written after it",
			func(t *testing.T, e *env) {
				e.s.sent("m1", 0, text)
				p, b := e.pair(time.Second, "sess-1", 500*time.Millisecond, "prompt-1")
				if !p.Known || p.Guest || p.Relay != "" {
					t.Errorf("the pairing is %+v, want a known one that names nothing", p)
				}
				if got := promptOf(b)[eventID("m1", 0)]; got != "prompt-1" {
					t.Errorf("the record names prompt %q, want prompt-1", got)
				}
			}},
		{"a prompt whose message the reader recorded earlier names that record",
			func(t *testing.T, e *env) {
				e.s.message(message{id: "m1", thread: "th1", run: "r-m1", text: text})
				e.s.run(run{id: "r-m1", thread: "th1", message: "m1"})
				e.s.attempt("r-m1", 1, "", "")
				b := e.poll(2 * time.Second)
				if len(b.Messages) != 1 || b.Messages[0].SessionKey != "" || b.Messages[0].Data.Prompt != "" {
					t.Fatalf("the queued message's record is %+v, want one of no session and no prompt", b.Messages)
				}
				e.s.run(run{id: "r-m1", thread: "th1", message: "m1", started: ptr(30 * time.Second)})
				e.s.attempt("r-m1", 1, "sess-1", "")
				p, b := e.pair(31*time.Second, "sess-1", 30500*time.Millisecond, "prompt-1")
				if p.Relay != eventID("m1", 0) || len(b.Messages) != 0 {
					t.Errorf("the pairing is %+v with %d new records, want relay %s and none", p, len(b.Messages), eventID("m1", 0))
				}
			}},
		{"a run pairs with its session's first prompt, and a later prompt of the session pairs with nothing",
			func(t *testing.T, e *env) {
				e.s.sent("m1", 0, text)
				e.poll(time.Second)
				if p, _ := e.pair(2*time.Second, "sess-1", 500*time.Millisecond, "prompt-1"); p.Relay != eventID("m1", 0) {
					t.Errorf("the first prompt's pairing is %+v, want relay %s", p, eventID("m1", 0))
				}
				if p, _ := e.pair(6*time.Second, "sess-1", 5*time.Second, "prompt-2"); p.Relay != "" || p.Guest {
					t.Errorf("the second prompt's pairing is %+v, want none", p)
				}
				if got := e.r.Status().Paired; got != 1 {
					t.Errorf("%d records paired, want 1", got)
				}
			}},
		{"a prompt stamped before the run started pairs with nothing",
			func(t *testing.T, e *env) {
				e.s.sent("m1", 10*time.Second, text)
				e.poll(11 * time.Second)
				if p, _ := e.pair(12*time.Second, "sess-1", 5*time.Second, "prompt-0"); p.Relay != "" {
					t.Errorf("the pairing is %+v, want none", p)
				}
			}},
		{"a prompt more than RELAY_MATCH_S after the message's stamp pairs with nothing",
			func(t *testing.T, e *env) {
				e.s.message(message{id: "m1", thread: "th1", run: "r-m1", text: text})
				e.s.run(run{id: "r-m1", thread: "th1", message: "m1"})
				e.poll(time.Second)
				e.s.run(run{id: "r-m1", thread: "th1", message: "m1", started: ptr(600 * time.Second)})
				e.s.attempt("r-m1", 1, "sess-1", "")
				if p, _ := e.pair(602*time.Second, "sess-1", 600*time.Second+time.Millisecond, "prompt-1"); p.Relay != "" {
					t.Errorf("the pairing is %+v, want none", p)
				}
			}},
		{"a prompt of a session the run didn't go to pairs with nothing, so a run whose session pairtime never saw stays unpaired",
			func(t *testing.T, e *env) {
				e.s.sent("m1", 0, text)
				b := e.poll(time.Second)
				if len(b.Messages) != 1 || b.Messages[0].SessionKey != session("sess-1") {
					t.Fatalf("the record is %+v, want one in session sess-1", b.Messages)
				}
				if p, _ := e.pair(2*time.Second, "sess-other", 500*time.Millisecond, "prompt-1"); p.Relay != "" {
					t.Errorf("the pairing is %+v, want none", p)
				}
			}},
		{"a message edited after the reader first read it pairs with nothing",
			func(t *testing.T, e *env) {
				e.s.message(message{id: "m1", thread: "th1", run: "r-m1", text: text})
				e.s.run(run{id: "r-m1", thread: "th1", message: "m1"})
				e.poll(time.Second)
				e.s.message(message{id: "m1", thread: "th1", run: "r-m1", updated: ptr(20 * time.Second), text: "an agent's words"})
				e.s.run(run{id: "r-m1", thread: "th1", message: "m1", started: ptr(30 * time.Second)})
				e.s.attempt("r-m1", 1, "sess-1", "")
				if p, _ := e.pair(31*time.Second, "sess-1", 30500*time.Millisecond, "prompt-1"); p.Relay != "" {
					t.Errorf("the pairing is %+v, want none", p)
				}
				if got := e.r.Status().Edited; got != 1 {
					t.Errorf("%d rows counted as changed, want 1", got)
				}
			}},
		{"a message T3 promoted to a steer, its row moved to the running run, pairs with nothing",
			func(t *testing.T, e *env) {
				e.s.sent("m0", 0, text)
				e.s.message(message{id: "m1", thread: "th1", run: "r-m1", created: 2 * time.Second, text: "and the lexer too"})
				e.s.run(run{id: "r-m1", thread: "th1", message: "m1", requested: 2 * time.Second})
				e.poll(3 * time.Second)
				if p, _ := e.pair(4*time.Second, "sess-1", 500*time.Millisecond, "prompt-0"); p.Relay != eventID("m0", 0) {
					t.Fatalf("the running run's prompt's pairing is %+v, want relay %s", p, eventID("m0", 0))
				}
				e.s.message(message{id: "m1", thread: "th1", run: "r-m0", created: 10 * time.Second, text: "and the lexer too"})
				e.s.run(run{id: "r-m1", thread: "th1", message: "m1", requested: 2 * time.Second, status: "cancelled"})
				// Should Claude Code report the steer as a prompt, it pairs
				// with neither message.
				p, _ := e.pair(11*time.Second, "sess-1", 10500*time.Millisecond, "prompt-steer")
				if p.Relay != "" {
					t.Errorf("the pairing is %+v, want none", p)
				}
			}},
		{"a prompt after the run ended pairs with nothing",
			func(t *testing.T, e *env) {
				e.s.message(message{id: "m1", thread: "th1", run: "r-m1", text: text})
				e.s.run(run{id: "r-m1", thread: "th1", message: "m1", started: ptr(100 * time.Millisecond), completed: ptr(4 * time.Second)})
				e.s.attempt("r-m1", 1, "sess-1", "")
				e.poll(5 * time.Second)
				if p, _ := e.pair(9*time.Second, "sess-1", 8*time.Second, "prompt-late"); p.Relay != "" {
					t.Errorf("the pairing is %+v, want none", p)
				}
			}},
		{"a run's window closes at its end with no slack: a prompt a millisecond after it pairs with nothing",
			func(t *testing.T, e *env) {
				e.s.message(message{id: "m1", thread: "th1", run: "r-m1", text: text})
				e.s.run(run{id: "r-m1", thread: "th1", message: "m1", started: ptr(100 * time.Millisecond), completed: ptr(4 * time.Second)})
				e.s.attempt("r-m1", 1, "sess-1", "")
				e.poll(5 * time.Second)
				if p, _ := e.pair(6*time.Second, "sess-1", 4*time.Second+time.Millisecond, "prompt-late"); p.Relay != "" {
					t.Errorf("the pairing is %+v, want none", p)
				}
			}},
		{"a prompt stamped up to 2 s before its run's start pairs, as the hook may stamp it before T3 stamps the start",
			func(t *testing.T, e *env) {
				e.s.message(message{id: "m1", thread: "th1", run: "r-m1", text: text})
				e.s.run(run{id: "r-m1", thread: "th1", message: "m1"})
				e.poll(time.Second)
				e.s.run(run{id: "r-m1", thread: "th1", message: "m1", started: ptr(10 * time.Second)})
				e.s.attempt("r-m1", 1, "sess-1", "")
				if p, _ := e.pair(11*time.Second, "sess-1", 8*time.Second, "prompt-1"); p.Relay != eventID("m1", 0) {
					t.Errorf("the pairing is %+v, want relay %s", p, eventID("m1", 0))
				}
			}},
		{"a run whose own prompt never reached the reader doesn't take the prompt of the run T3 starts as it ends",
			func(t *testing.T, e *env) {
				// m1's prompt is never asked about: the agent never saw it.
				e.s.sent("m1", 0, text)
				e.s.message(message{id: "m2", thread: "th1", run: "r-m2", created: time.Second, text: "then run the whole suite"})
				e.s.run(run{id: "r-m2", thread: "th1", message: "m2", requested: time.Second})
				if b := e.poll(2 * time.Second); len(b.Messages) != 2 {
					t.Fatalf("got %d records, want 2", len(b.Messages))
				}
				e.s.run(run{id: "r-m1", thread: "th1", message: "m1", started: ptr(100 * time.Millisecond), completed: ptr(30 * time.Second)})
				e.s.run(run{id: "r-m2", thread: "th1", message: "m2", requested: time.Second, started: ptr(30*time.Second + 50*time.Millisecond)})
				e.s.attempt("r-m2", 1, "sess-1", "")
				if p, _ := e.pair(31*time.Second, "sess-1", 30300*time.Millisecond, "prompt-2"); p.Relay != eventID("m2", time.Second) {
					t.Errorf("the second run's prompt's pairing is %+v, want relay %s", p, eventID("m2", time.Second))
				}
			}},
		{"a run that delivered nothing doesn't take the prompt of a guest's run T3 starts within 2 s of its end",
			func(t *testing.T, e *env) {
				e.s.sent("m0", 0, text)
				guest(e.s, "g1", time.Second)
				e.poll(2 * time.Second)
				e.s.run(run{id: "r-m0", thread: "th1", message: "m0", started: ptr(100 * time.Millisecond), completed: ptr(10 * time.Second),
					status: "cancelled"})
				e.s.run(run{id: "r-g1", thread: "th1", message: "g1", requested: time.Second, started: ptr(11 * time.Second)})
				e.s.attempt("r-g1", 1, "sess-1", "")
				if p, _ := e.pair(12*time.Second, "sess-1", 11300*time.Millisecond, "prompt-g"); !p.Guest || p.Relay != "" {
					t.Errorf("the guest's prompt's pairing is %+v, want a guest's and no relay", p)
				}
			}},
		{"a run T3 cancelled after it started doesn't take the prompt of the owner's next run",
			func(t *testing.T, e *env) {
				e.s.sent("m0", 0, text)
				e.s.message(message{id: "m1", thread: "th1", run: "r-m1", created: time.Second, text: "then run the whole suite"})
				e.s.run(run{id: "r-m1", thread: "th1", message: "m1", requested: time.Second})
				e.poll(2 * time.Second)
				e.s.run(run{id: "r-m0", thread: "th1", message: "m0", started: ptr(100 * time.Millisecond), completed: ptr(10 * time.Second),
					status: "cancelled"})
				e.s.run(run{id: "r-m1", thread: "th1", message: "m1", requested: time.Second, started: ptr(10500 * time.Millisecond)})
				e.s.attempt("r-m1", 1, "sess-1", "")
				if p, _ := e.pair(12*time.Second, "sess-1", 10800*time.Millisecond, "prompt-1"); p.Relay != eventID("m1", time.Second) {
					t.Errorf("the next run's prompt's pairing is %+v, want relay %s", p, eventID("m1", time.Second))
				}
			}},
		{"a recorded queued message pairs with its prompt though Pair's first read finds T3's store busy",
			func(t *testing.T, e *env) {
				sh := newShim(t, e.s.sqlite3, busyOnce)
				e.r.sqlite = &Runner{Path: sh.path}
				e.s.message(message{id: "m1", thread: "th1", run: "r-m1", text: text})
				e.s.run(run{id: "r-m1", thread: "th1", message: "m1"})
				e.s.attempt("r-m1", 1, "", "")
				e.poll(2 * time.Second)
				e.s.run(run{id: "r-m1", thread: "th1", message: "m1", started: ptr(30 * time.Second)})
				e.s.attempt("r-m1", 1, "sess-1", "")
				sh.arm(t)
				p, b := e.pair(31*time.Second, "sess-1", 30500*time.Millisecond, "prompt-1")
				if sh.armed() {
					t.Fatal("no read found the store busy")
				}
				if !p.Known || p.Relay != eventID("m1", 0) || len(b.Messages) != 0 {
					t.Errorf("the pairing is %+v with %d new records, want a known one with relay %s", p, len(b.Messages), eventID("m1", 0))
				}
				if got := e.r.Status().Paired; got != 1 {
					t.Errorf("%d records paired, want 1", got)
				}
			}},
		{"a pairing found once both records were written goes on neither, isn't counted, and takes the run",
			func(t *testing.T, e *env) {
				e.s.message(message{id: "m1", thread: "th1", run: "r-m1", text: text})
				e.s.run(run{id: "r-m1", thread: "th1", message: "m1"})
				e.s.attempt("r-m1", 1, "", "")
				e.poll(2 * time.Second)
				e.s.run(run{id: "r-m1", thread: "th1", message: "m1", started: ptr(30 * time.Second)})
				e.s.attempt("r-m1", 1, "sess-1", "")
				e.s.flush()
				e.s.close()
				e.c.set(31 * time.Second)
				p, _, err := e.r.Pair(context.Background(), Prompt{SessionKey: session("sess-1"),
					T: base.Add(30500 * time.Millisecond).UnixMilli(), EventID: "prompt-1"})
				if !errors.Is(err, ErrNotRunning) || p.Known || p.Relay != "" {
					t.Fatalf("Pair with T3 closed gave %+v, %v; want an unknown answer and ErrNotRunning", p, err)
				}
				e.s.open()
				if b := e.poll(32 * time.Second); len(b.Messages) != 0 {
					t.Fatalf("got %d records, want none", len(b.Messages))
				}
				if got := e.r.Status().Paired; got != 0 {
					t.Errorf("%d records paired, want none", got)
				}
				if p, _ := e.pair(34*time.Second, "sess-1", 33*time.Second, "prompt-2"); p.Relay != "" {
					t.Errorf("a later prompt's pairing is %+v, want none", p)
				}
			}},
		{"a prompt the reader couldn't answer for pairs with the record of its message that a later read writes",
			func(t *testing.T, e *env) {
				e.s.sent("m1", 0, text)
				e.s.flush()
				e.s.close()
				e.c.set(time.Second)
				p, b, err := e.r.Pair(context.Background(), Prompt{SessionKey: session("sess-1"),
					T: base.Add(500 * time.Millisecond).UnixMilli(), EventID: "prompt-1"})
				if !errors.Is(err, ErrNotRunning) || p.Known || len(b.Messages) != 0 {
					t.Fatalf("Pair with T3 closed gave %+v, %d records, %v; want an unknown answer and ErrNotRunning", p, len(b.Messages), err)
				}
				e.s.open()
				b = e.poll(2 * time.Second)
				if got := promptOf(b)[eventID("m1", 0)]; got != "prompt-1" {
					t.Errorf("the record names prompt %q, want prompt-1", got)
				}
			}},
		{"two queued messages pair with the prompts their runs deliver, in order",
			func(t *testing.T, e *env) {
				e.s.sent("m1", 0, text)
				e.s.message(message{id: "m2", thread: "th1", run: "r-m2", created: 5 * time.Second, text: "then run the whole suite"})
				e.s.run(run{id: "r-m2", thread: "th1", message: "m2", requested: 5 * time.Second})
				if b := e.poll(6 * time.Second); len(b.Messages) != 2 {
					t.Fatalf("got %d records, want 2", len(b.Messages))
				}
				if p, _ := e.pair(7*time.Second, "sess-1", 500*time.Millisecond, "prompt-1"); p.Relay != eventID("m1", 0) {
					t.Errorf("the first prompt's pairing is %+v, want relay %s", p, eventID("m1", 0))
				}
				e.s.run(run{id: "r-m1", thread: "th1", message: "m1", started: ptr(100 * time.Millisecond), completed: ptr(39 * time.Second)})
				e.s.run(run{id: "r-m2", thread: "th1", message: "m2", requested: 5 * time.Second, started: ptr(40 * time.Second)})
				e.s.attempt("r-m2", 1, "sess-1", "")
				if p, _ := e.pair(41*time.Second, "sess-1", 40300*time.Millisecond, "prompt-2"); p.Relay != eventID("m2", 5*time.Second) {
					t.Errorf("the second prompt's pairing is %+v, want relay %s", p, eventID("m2", 5*time.Second))
				}
			}},
		{"a run takes no prompt stamped at or before the end of an earlier run of its session: a steer 112 ms before a turn's end, as in the capture, doesn't go to the queued message whose run T3 starts 1,475 ms after that end",
			func(t *testing.T, e *env) {
				e.s.sent("m1", 0, text)
				e.s.message(message{id: "m2", thread: "th1", run: "r-m2", created: 5 * time.Second, text: "then run the whole suite"})
				e.s.run(run{id: "r-m2", thread: "th1", message: "m2", requested: 5 * time.Second})
				if b := e.poll(6 * time.Second); len(b.Messages) != 2 {
					t.Fatalf("got %d records, want 2", len(b.Messages))
				}
				if p, _ := e.pair(7*time.Second, "sess-1", 500*time.Millisecond, "prompt-1"); p.Relay != eventID("m1", 0) {
					t.Fatalf("the first prompt's pairing is %+v, want relay %s", p, eventID("m1", 0))
				}
				// The owner's steer reaches m1's turn as a prompt of its own
				// during its Stop hook, 112 ms before the turn ends.
				if p, _ := e.pair(38900*time.Millisecond, "sess-1", 38888*time.Millisecond, "prompt-steer"); !p.Known || p.Relay != "" || p.Guest {
					t.Errorf("the steer's pairing is %+v, want a known one that pairs with nothing", p)
				}
				e.s.run(run{id: "r-m1", thread: "th1", message: "m1", started: ptr(100 * time.Millisecond), completed: ptr(39 * time.Second)})
				e.s.run(run{id: "r-m2", thread: "th1", message: "m2", requested: 5 * time.Second, started: ptr(40475 * time.Millisecond)})
				e.s.attempt("r-m2", 1, "sess-1", "")
				if p, _ := e.pair(41*time.Second, "sess-1", 40775*time.Millisecond, "prompt-2"); p.Relay != eventID("m2", 5*time.Second) {
					t.Errorf("the queued message's prompt's pairing is %+v, want relay %s, not the steer's prompt taken for it", p, eventID("m2", 5*time.Second))
				}
				if got := e.r.Status().Paired; got != 2 {
					t.Errorf("%d records paired, want 2: each message with its own run's prompt, and the steer with none", got)
				}
			}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { tc.run(t, newEnv(t)) })
	}
}

// guestText is the text T3 sends on for an invented guest's message.
const guestText = "Message from guest Ada, sent by the owner:\n\ninvented words"

// guestMark is the context T3 marks the guest's message id with.
func guestMark(id string) map[string]any {
	return map[string]any{"context": map[string]any{"version": 1, "records": []any{},
		"guest": map[string]any{"guestMessageId": "g-" + id, "name": "Ada", "text": "invented words"}}}
}

// runKey is the key the reader gives T3's run id.
func runKey(id string) string { return adapters.FixtureKeys().App("t3_code", "run", id) }

// guest writes a guest's message in thread th1 at created, sent on by the
// owner, with a run of its own T3 queued for it.
func guest(s *store, id string, created time.Duration) {
	s.message(message{id: id, thread: "th1", run: "r-" + id, created: created, text: guestText, extra: guestMark(id)})
	s.run(run{id: "r-" + id, thread: "th1", message: id, requested: created})
}

// TestGuests holds the reader to DESIGN 5.1's guest rule: it tells the agent
// which runs a guest's message started, without reading the message, so the
// prompt such a run delivers keeps no text, and which running turns the
// owner steered a guest's message into (Steer), whose prompts the agent
// marks (docs/errata/2026-11-23-t3-reader.md, decision 4).
func TestGuests(t *testing.T) {
	cases := []struct {
		name string
		run  func(t *testing.T, e *env)
	}{
		{"a prompt a guest's run delivers is a guest's",
			func(t *testing.T, e *env) {
				guest(e.s, "g1", 0)
				e.s.run(run{id: "r-g1", thread: "th1", message: "g1", started: ptr(100 * time.Millisecond)})
				e.s.attempt("r-g1", 1, "sess-1", "")
				p, b := e.pair(time.Second, "sess-1", 500*time.Millisecond, "prompt-1")
				if !p.Known || !p.Guest || p.Relay != "" || len(b.Messages) != 0 {
					t.Errorf("the pairing is %+v with %d records, want a guest's and no record", p, len(b.Messages))
				}
				if want := []GuestRun{{SentAt: base.UnixMilli()}}; !slices.Equal(b.Guests, want) {
					t.Errorf("guests are %+v, want %+v", b.Guests, want)
				}
			}},
		{"a guest's queued run marks the prompt it delivers once it starts",
			func(t *testing.T, e *env) {
				e.s.sent("m0", 0, "please fix the flaky parser test")
				guest(e.s, "g1", 2*time.Second)
				e.poll(3 * time.Second)
				if p, _ := e.pair(4*time.Second, "sess-1", 500*time.Millisecond, "prompt-0"); p.Guest || p.Relay != eventID("m0", 0) {
					t.Errorf("the owner's prompt's pairing is %+v, want relay %s and no guest", p, eventID("m0", 0))
				}
				e.s.run(run{id: "r-m0", thread: "th1", message: "m0", started: ptr(100 * time.Millisecond), completed: ptr(29 * time.Second)})
				e.s.run(run{id: "r-g1", thread: "th1", message: "g1", requested: 2 * time.Second, started: ptr(30 * time.Second)})
				e.s.attempt("r-g1", 1, "sess-1", "")
				if p, _ := e.pair(31*time.Second, "sess-1", 30500*time.Millisecond, "prompt-1"); !p.Guest {
					t.Errorf("the guest's prompt's pairing is %+v, want a guest's", p)
				}
			}},
		{"a guest's queued run takes its own prompt, not a steer stamped before the end of the owner's turn before it, which its start slack reaches",
			func(t *testing.T, e *env) {
				e.s.sent("m0", 0, "please fix the flaky parser test")
				guest(e.s, "g1", 2*time.Second)
				e.poll(3 * time.Second)
				if p, _ := e.pair(4*time.Second, "sess-1", 500*time.Millisecond, "prompt-0"); p.Relay != eventID("m0", 0) {
					t.Fatalf("the owner's prompt's pairing is %+v, want relay %s", p, eventID("m0", 0))
				}
				// A steer reaches the owner's turn 112 ms before it ends, and
				// T3 starts the guest's run 1,475 ms after the end, as in the
				// capture.
				if p, _ := e.pair(28900*time.Millisecond, "sess-1", 28888*time.Millisecond, "prompt-steer"); p.Guest || p.Relay != "" {
					t.Errorf("the steer's pairing is %+v, want none", p)
				}
				e.s.run(run{id: "r-m0", thread: "th1", message: "m0", started: ptr(100 * time.Millisecond), completed: ptr(29 * time.Second)})
				e.s.run(run{id: "r-g1", thread: "th1", message: "g1", requested: 2 * time.Second, started: ptr(30475 * time.Millisecond)})
				e.s.attempt("r-g1", 1, "sess-1", "")
				if p, _ := e.pair(31*time.Second, "sess-1", 30775*time.Millisecond, "prompt-g"); !p.Guest {
					t.Errorf("the guest's own prompt's pairing is %+v, want a guest's", p)
				}
			}},
		{"a guest's message steered into a running turn is reported as steered, and the turn as a Steer from the message's stamp while it runs, though the reader marks no prompt itself",
			func(t *testing.T, e *env) {
				e.s.sent("m0", 0, "please fix the flaky parser test")
				e.s.message(message{id: "g1", thread: "th1", run: "r-m0", created: 2 * time.Second,
					text: "Message from guest Ada, sent by the owner:\n\ninvented words"})
				p, b := e.pair(3*time.Second, "sess-1", 2500*time.Millisecond, "prompt-steer")
				if p.Guest {
					t.Errorf("the pairing is %+v, want no guest's", p)
				}
				if want := []GuestRun{{SentAt: base.Add(2 * time.Second).UnixMilli(), Steered: true}}; !slices.Equal(b.Guests, want) {
					t.Errorf("guests are %+v, want %+v", b.Guests, want)
				}
				want := []Steer{{Run: runKey("r-m0"), Sessions: []string{session("sess-1")}, From: base.Add(2 * time.Second).UnixMilli()}}
				if got := e.r.Steers(); !reflect.DeepEqual(got, want) {
					t.Errorf("steers are %+v, want %+v", got, want)
				}
			}},
		{"a Steer starts at its turn's start when the guest's message is stamped before the turn started, and ends at the turn's end once a read sees it",
			func(t *testing.T, e *env) {
				e.s.message(message{id: "m0", thread: "th1", run: "r-m0", text: "please fix the flaky parser test"})
				e.s.message(message{id: "g1", thread: "th1", run: "r-m0", created: time.Second, text: guestText, extra: guestMark("g1")})
				e.s.run(run{id: "r-m0", thread: "th1", message: "m0", started: ptr(3 * time.Second)})
				e.s.attempt("r-m0", 1, "sess-1", "")
				e.poll(4 * time.Second)
				e.s.run(run{id: "r-m0", thread: "th1", message: "m0", started: ptr(3 * time.Second), completed: ptr(9 * time.Second)})
				e.poll(10 * time.Second)
				want := []Steer{{Run: runKey("r-m0"), Sessions: []string{session("sess-1")},
					From: base.Add(3 * time.Second).UnixMilli(), To: base.Add(9 * time.Second).UnixMilli()}}
				if got := e.r.Steers(); !reflect.DeepEqual(got, want) {
					t.Errorf("steers are %+v, want %+v", got, want)
				}
			}},
		{"a guest's queued message the owner promotes into a running turn makes that turn a Steer from the promotion's stamp",
			func(t *testing.T, e *env) {
				e.s.sent("m0", 0, "please fix the flaky parser test")
				guest(e.s, "g1", 2*time.Second)
				e.poll(3 * time.Second)
				if got := e.r.Steers(); len(got) != 0 {
					t.Fatalf("steers are %+v before the promotion, want none", got)
				}
				e.s.message(message{id: "g1", thread: "th1", run: "r-m0", created: 5 * time.Second, text: guestText, extra: guestMark("g1")})
				e.s.run(run{id: "r-g1", thread: "th1", message: "g1", requested: 2 * time.Second, status: "cancelled"})
				e.poll(6 * time.Second)
				want := []Steer{{Run: runKey("r-m0"), Sessions: []string{session("sess-1")}, From: base.Add(5 * time.Second).UnixMilli()}}
				if got := e.r.Steers(); !reflect.DeepEqual(got, want) {
					t.Errorf("steers are %+v, want %+v", got, want)
				}
			}},
		{"a Steer whose turn the guest's message leaves for a run of its own, as T3's follow-up does, ends at the read that found it so when no read saw the turn end",
			func(t *testing.T, e *env) {
				e.s.sent("m0", 0, "please fix the flaky parser test")
				e.s.message(message{id: "g1", thread: "th1", run: "r-m0", created: 2 * time.Second, text: guestText, extra: guestMark("g1")})
				e.poll(3 * time.Second)
				e.s.message(message{id: "g1", thread: "th1", run: "r-g1", created: 10 * time.Second, text: guestText})
				e.s.run(run{id: "r-g1", thread: "th1", message: "g1", requested: 10 * time.Second})
				e.poll(11 * time.Second)
				want := []Steer{{Run: runKey("r-m0"), Sessions: []string{session("sess-1")},
					From: base.Add(2 * time.Second).UnixMilli(), To: base.Add(11 * time.Second).UnixMilli()}}
				if got := e.r.Steers(); !reflect.DeepEqual(got, want) {
					t.Errorf("steers are %+v, want %+v", got, want)
				}
			}},
		{"a Steer of a turn on another provider names that provider's session",
			func(t *testing.T, e *env) {
				e.s.providerThread("pt-codex", "th1", "codex", "codex-thread-1")
				e.s.message(message{id: "m0", thread: "th1", run: "r-m0", text: "please fix the flaky parser test"})
				e.s.run(run{id: "r-m0", thread: "th1", message: "m0", providerThread: "pt-codex", started: ptr(100 * time.Millisecond)})
				e.s.attempt("r-m0", 1, "codex-session-1", "pt-codex")
				e.s.message(message{id: "g1", thread: "th1", run: "r-m0", created: 2 * time.Second, text: guestText, extra: guestMark("g1")})
				e.poll(3 * time.Second)
				want := []Steer{{Run: runKey("r-m0"), Sessions: []string{session("codex-session-1")}, From: base.Add(2 * time.Second).UnixMilli()}}
				if got := e.r.Steers(); !reflect.DeepEqual(got, want) {
					t.Errorf("steers are %+v, want %+v", got, want)
				}
			}},
		{"a Steer stays across a restart, in state.json",
			func(t *testing.T, e *env) {
				e.s.sent("m0", 0, "please fix the flaky parser test")
				e.s.message(message{id: "g1", thread: "th1", run: "r-m0", created: 2 * time.Second, text: guestText, extra: guestMark("g1")})
				e.poll(3 * time.Second)
				raw, err := json.Marshal(e.r.State())
				if err != nil {
					t.Fatal(err)
				}
				var saved State
				if err := json.Unmarshal(raw, &saved); err != nil {
					t.Fatal(err)
				}
				want := []Steer{{Run: runKey("r-m0"), Sessions: []string{session("sess-1")}, From: base.Add(2 * time.Second).UnixMilli()}}
				if got := e.s.reader(e.c, tracked(), saved).Steers(); !reflect.DeepEqual(got, want) {
					t.Errorf("the restarted reader's steers are %+v, want %+v", got, want)
				}
			}},
		{"an owner's message steered into a running turn is no Steer",
			func(t *testing.T, e *env) {
				e.s.sent("m0", 0, "please fix the flaky parser test")
				e.s.message(message{id: "m1", thread: "th1", run: "r-m0", created: 2 * time.Second, text: "and the lexer test too"})
				e.poll(3 * time.Second)
				if got := e.r.Steers(); len(got) != 0 {
					t.Errorf("steers are %+v, want none", got)
				}
			}},
		{"a guest's run an earlier read saw still marks its prompt when T3's store can't be read",
			func(t *testing.T, e *env) {
				guest(e.s, "g1", 0)
				e.s.run(run{id: "r-g1", thread: "th1", message: "g1", started: ptr(100 * time.Millisecond)})
				e.s.attempt("r-g1", 1, "sess-1", "")
				e.poll(time.Second)
				e.s.close()
				e.c.set(2 * time.Second)
				p, _, err := e.r.Pair(context.Background(), Prompt{SessionKey: session("sess-1"),
					T: base.Add(500 * time.Millisecond).UnixMilli(), EventID: "prompt-1"})
				if err == nil || p.Known || !p.Guest {
					t.Errorf("the pairing is %+v (err %v), want an unknown one that is still a guest's", p, err)
				}
			}},
		{"a guest's queued message the owner promotes to a steer, which T3 sends again as a run of its own after Claude Code declines it, marks that run's prompt",
			func(t *testing.T, e *env) {
				e.s.sent("m0", 0, "please fix the flaky parser test")
				guest(e.s, "g1", 2*time.Second)
				e.poll(3 * time.Second)
				// The promotion: the row joins m0's running run, stamped anew,
				// and its own run is cancelled before it started.
				e.s.message(message{id: "g1", thread: "th1", run: "r-m0", created: 5 * time.Second, text: guestText, extra: guestMark("g1")})
				e.s.run(run{id: "r-g1", thread: "th1", message: "g1", requested: 2 * time.Second, status: "cancelled"})
				e.poll(6 * time.Second)
				// The decline: T3 sends the message again, without its guest
				// mark, as a run of its own, in the same row.
				e.s.message(message{id: "g1", thread: "th1", run: "r-g1-again", created: 20 * time.Second, text: guestText})
				e.s.run(run{id: "r-g1-again", thread: "th1", message: "g1", requested: 20 * time.Second})
				e.poll(21 * time.Second)
				e.s.run(run{id: "r-m0", thread: "th1", message: "m0", started: ptr(100 * time.Millisecond), completed: ptr(29 * time.Second)})
				e.s.run(run{id: "r-g1-again", thread: "th1", message: "g1", requested: 20 * time.Second, started: ptr(30 * time.Second)})
				e.s.attempt("r-g1-again", 1, "sess-1", "")
				if p, _ := e.pair(31*time.Second, "sess-1", 30500*time.Millisecond, "prompt-g"); !p.Guest || p.Relay != "" {
					t.Errorf("the resent guest's prompt's pairing is %+v, want a guest's", p)
				}
			}},
		{"a guest's message first read as a steer, which T3 sends again as a run of its own, marks that run's prompt",
			func(t *testing.T, e *env) {
				e.s.sent("m0", 0, "please fix the flaky parser test")
				e.s.message(message{id: "g1", thread: "th1", run: "r-m0", created: 2 * time.Second, text: guestText, extra: guestMark("g1")})
				if b := e.poll(3 * time.Second); !slices.Equal(b.Guests, []GuestRun{{SentAt: base.Add(2 * time.Second).UnixMilli(), Steered: true}}) {
					t.Fatalf("guests are %+v, want the steered one", b.Guests)
				}
				e.s.message(message{id: "g1", thread: "th1", run: "r-g1", created: 10 * time.Second, text: guestText})
				e.s.run(run{id: "r-g1", thread: "th1", message: "g1", requested: 10 * time.Second})
				e.poll(11 * time.Second)
				e.s.run(run{id: "r-m0", thread: "th1", message: "m0", started: ptr(100 * time.Millisecond), completed: ptr(19 * time.Second)})
				e.s.run(run{id: "r-g1", thread: "th1", message: "g1", requested: 10 * time.Second, started: ptr(20 * time.Second)})
				e.s.attempt("r-g1", 1, "sess-1", "")
				if p, _ := e.pair(21*time.Second, "sess-1", 20500*time.Millisecond, "prompt-g"); !p.Guest || p.Relay != "" {
					t.Errorf("the guest's prompt's pairing is %+v, want a guest's", p)
				}
			}},
		{"a guest's message read before its run is in the store marks the run's prompt once it is",
			func(t *testing.T, e *env) {
				e.s.message(message{id: "g1", thread: "th1", run: "r-g1", text: guestText, extra: guestMark("g1")})
				if b := e.poll(time.Second); !slices.Equal(b.Guests, []GuestRun{{SentAt: base.UnixMilli()}}) {
					t.Fatalf("guests are %+v, want one not steered", b.Guests)
				}
				e.s.run(run{id: "r-g1", thread: "th1", message: "g1", started: ptr(2 * time.Second)})
				e.s.attempt("r-g1", 1, "sess-1", "")
				if p, _ := e.pair(3*time.Second, "sess-1", 2500*time.Millisecond, "prompt-g"); !p.Guest {
					t.Errorf("the guest's prompt's pairing is %+v, want a guest's", p)
				}
			}},
		{"a guest's message stays watched across a restart, by its message's key in state.json",
			func(t *testing.T, e *env) {
				guest(e.s, "g1", 0)
				e.poll(time.Second)
				raw, err := json.Marshal(e.r.State())
				if err != nil {
					t.Fatal(err)
				}
				var saved State
				if err := json.Unmarshal(raw, &saved); err != nil {
					t.Fatal(err)
				}
				key := adapters.FixtureKeys().App("t3_code", "message", "g1")
				if !slices.ContainsFunc(saved.Watched, func(w WatchState) bool { return w.Guest && w.Message == key }) {
					t.Fatalf("state.json keeps %+v, with no guest's watch of g1's key", saved.Watched)
				}
				again := e.s.reader(e.c, tracked(), saved)
				e.s.run(run{id: "r-g1", thread: "th1", message: "g1", started: ptr(2 * time.Second)})
				e.s.attempt("r-g1", 1, "sess-1", "")
				e.s.flush()
				e.c.set(3 * time.Second)
				p, _, err := again.Pair(context.Background(), Prompt{SessionKey: session("sess-1"),
					T: base.Add(2500 * time.Millisecond).UnixMilli(), EventID: "prompt-g"})
				if err != nil || !p.Guest {
					t.Errorf("the restarted reader's pairing is %+v (err %v), want a guest's", p, err)
				}
			}},
		{"a guest's run in a thread T3 runs on another provider marks the prompt it delivers",
			func(t *testing.T, e *env) {
				e.s.providerThread("pt-codex", "th1", "codex", "codex-thread-1")
				e.s.message(message{id: "g1", thread: "th1", run: "r-g1", text: guestText, extra: guestMark("g1")})
				e.s.run(run{id: "r-g1", thread: "th1", message: "g1", providerThread: "pt-codex", started: ptr(100 * time.Millisecond)})
				e.s.attempt("r-g1", 1, "codex-session-1", "pt-codex")
				if p, _ := e.pair(time.Second, "codex-session-1", 500*time.Millisecond, "prompt-g"); !p.Guest {
					t.Errorf("the pairing is %+v, want a guest's", p)
				}
			}},
		{"with no sqlite3, nothing marks a prompt, which keeps its text like any prompt's",
			func(t *testing.T, e *env) {
				guest(e.s, "g1", 0)
				e.s.run(run{id: "r-g1", thread: "th1", message: "g1", started: ptr(100 * time.Millisecond)})
				e.s.attempt("r-g1", 1, "sess-1", "")
				e.s.flush()
				r := e.s.reader(e.c, tracked(), State{})
				r.sqlite = &Runner{Path: "sqlite3-that-isnt-there"}
				e.c.set(time.Second)
				p, _, err := r.Pair(context.Background(), Prompt{SessionKey: session("sess-1"),
					T: base.Add(500 * time.Millisecond).UnixMilli(), EventID: "prompt-1"})
				if !errors.Is(err, ErrNoSQLite3) || p.Known || p.Guest {
					t.Errorf("the pairing is %+v (err %v), want an unknown one and ErrNoSQLite3", p, err)
				}
				if st := r.Status(); st.Reading || st.Off != OffNoSQLite3 {
					t.Errorf("the status is %+v, want off for no sqlite3", st)
				}
			}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { tc.run(t, newEnv(t)) })
	}
}

// TestCursor holds the reader's cursor to DESIGN 3.5 and 5.1: it starts at
// the newest row, survives a restart in state.json, and resyncs by stamp
// when T3's VACUUM renumbers the rows or another store takes the place of
// the one read, recording no message twice.
func TestCursor(t *testing.T) {
	const text = "please fix the flaky parser test"
	cases := []struct {
		name string
		run  func(t *testing.T, s *store, c *clock)
	}{
		{"a first read starts at the newest row and records nothing T3 stored before it",
			func(t *testing.T, s *store, c *clock) {
				s.sent("m1", 0, text)
				s.flush()
				c.set(time.Second)
				r := s.reader(c, tracked(), State{})
				if b := mustPoll(t, r); len(b.Messages) != 0 {
					t.Errorf("the first read recorded %d messages, want none", len(b.Messages))
				}
				s.sent("m2", 2*time.Second, text)
				s.flush()
				c.set(3 * time.Second)
				checkMessages(t, mustPoll(t, r).Messages, c.t, []rec{web("m2", 2*time.Second, 32, false, event.ClassSubstantive)})
			}},
		{"a reader started from its saved state goes on from its cursor",
			func(t *testing.T, s *store, c *clock) {
				r := s.reader(c, tracked(), State{})
				mustPoll(t, r)
				s.sent("m1", 0, text)
				s.flush()
				c.set(time.Second)
				mustPoll(t, r)
				saved := r.State()
				if saved.Cursor == nil || saved.Cursor.RowID != 1 || saved.Cursor.Message != adapters.FixtureKeys().App("t3_code", "message", "m1") {
					t.Fatalf("the saved cursor is %+v, want rowid 1 and the key of m1", saved.Cursor)
				}
				s.sent("m2", 2*time.Second, text)
				s.flush()
				c.set(3 * time.Second)
				again := s.reader(c, tracked(), saved)
				checkMessages(t, mustPoll(t, again).Messages, c.t, []rec{web("m2", 2*time.Second, 32, false, event.ClassSubstantive)})
			}},
		{"rows renumbered under the cursor, as T3's VACUUM may, resync by stamp and nothing is recorded twice",
			func(t *testing.T, s *store, c *clock) {
				r := s.reader(c, tracked(), State{})
				mustPoll(t, r)
				// The assistant's row comes after the person's in rowid order
				// but is stamped before it, as provider items are.
				s.sent("m1", 5*time.Second, text)
				s.assistant("a1", 2*time.Second)
				s.flush()
				c.set(6 * time.Second)
				if b := mustPoll(t, r); len(b.Messages) != 1 {
					t.Fatalf("got %d records, want 1", len(b.Messages))
				}
				s.add("UPDATE orchestration_v2_projection_messages SET rowid = rowid + 100;")
				s.sent("m2", 10*time.Second, text)
				s.flush()
				c.set(11 * time.Second)
				checkMessages(t, mustPoll(t, r).Messages, c.t, []rec{web("m2", 10*time.Second, 32, false, event.ClassSubstantive)})
				if b := mustPoll(t, r); len(b.Messages) != 0 {
					t.Errorf("a read after the resync recorded %d messages, want none", len(b.Messages))
				}
			}},
		{"after a resync by stamp, a renumbered message still waiting for its run pairs, and a queued guest's prompt is still a guest's",
			func(t *testing.T, s *store, c *clock) {
				r := s.reader(c, tracked(), State{})
				mustPoll(t, r)
				// m1 waits for its run, and g1, a guest's, waits behind it. The
				// assistant's row, the cursor's after this read, comes after
				// them in rowid order and is stamped after them too, so the
				// resync's floor, the cursor's stamp, is past both, and only
				// their watches take the resync back to them.
				s.message(message{id: "m1", thread: "th1", run: "r-m1", created: 5 * time.Second, text: text})
				s.run(run{id: "r-m1", thread: "th1", message: "m1", requested: 5 * time.Second})
				guest(s, "g1", 6*time.Second)
				s.assistant("a1", 8*time.Second)
				s.flush()
				c.set(9 * time.Second)
				if b := mustPoll(t, r); len(b.Messages) != 1 {
					t.Fatalf("got %d records, want 1", len(b.Messages))
				}
				s.add("UPDATE orchestration_v2_projection_messages SET rowid = rowid + 100;")
				s.message(message{id: "m2", thread: "th1", run: "r-m2", created: 10 * time.Second, text: text})
				s.run(run{id: "r-m2", thread: "th1", message: "m2", requested: 10 * time.Second})
				s.flush()
				c.set(11 * time.Second)
				checkMessages(t, mustPoll(t, r).Messages, c.t, []rec{{id: "m2", created: 10 * time.Second, client: event.RelayClientWeb,
					declared: event.RelayDeclaredNone, lenChars: 32, class: event.ClassSubstantive, project: "p_alpha"}})
				if b := mustPoll(t, r); len(b.Messages) != 0 || len(b.Guests) != 0 {
					t.Errorf("a read after the resync found %d records and %d guests, want none", len(b.Messages), len(b.Guests))
				}
				s.run(run{id: "r-m1", thread: "th1", message: "m1", requested: 5 * time.Second, started: ptr(20 * time.Second)})
				s.attempt("r-m1", 1, "sess-1", "")
				s.flush()
				c.set(21 * time.Second)
				p, _, err := r.Pair(context.Background(), Prompt{SessionKey: session("sess-1"),
					T: base.Add(20500 * time.Millisecond).UnixMilli(), EventID: "prompt-1"})
				if err != nil || p.Relay != eventID("m1", 5*time.Second) {
					t.Errorf("m1's prompt's pairing is %+v (err %v), want relay %s", p, err, eventID("m1", 5*time.Second))
				}
				s.run(run{id: "r-m1", thread: "th1", message: "m1", requested: 5 * time.Second, started: ptr(20 * time.Second),
					completed: ptr(29 * time.Second)})
				s.run(run{id: "r-g1", thread: "th1", message: "g1", requested: 6 * time.Second, started: ptr(30 * time.Second)})
				s.attempt("r-g1", 1, "sess-1", "")
				s.flush()
				c.set(31 * time.Second)
				p, _, err = r.Pair(context.Background(), Prompt{SessionKey: session("sess-1"),
					T: base.Add(30500 * time.Millisecond).UnixMilli(), EventID: "prompt-g"})
				if err != nil || !p.Guest {
					t.Errorf("g1's prompt's pairing is %+v (err %v), want a guest's", p, err)
				}
				if st := r.Status(); st.Edited != 0 || st.Guests != 1 || st.Aged != 0 {
					t.Errorf("the counts are %+v, want 1 guest and no row aged or changed", st.Counts)
				}
			}},
		{"an older store copied over the live one, its newest rowid below the cursor, resyncs",
			func(t *testing.T, s *store, c *clock) {
				r := s.reader(c, tracked(), State{})
				mustPoll(t, r)
				s.sent("m1", 0, text)
				s.sent("m2", time.Second, text)
				s.sent("m3", 2*time.Second, text)
				s.flush()
				c.set(3 * time.Second)
				mustPoll(t, r)
				s.add("DELETE FROM orchestration_v2_projection_messages;")
				s.sent("m4", 20*time.Second, text)
				s.flush()
				c.set(21 * time.Second)
				checkMessages(t, mustPoll(t, r).Messages, c.t, []rec{web("m4", 20*time.Second, 32, false, event.ClassSubstantive)})
			}},
		{"a read further behind than one window takes several",
			func(t *testing.T, s *store, c *clock) {
				r := s.reader(c, tracked(), State{})
				mustPoll(t, r)
				s.add("WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i + 1 FROM n WHERE i < 1200)" +
					" INSERT INTO orchestration_v2_projection_messages (message_id, thread_id, run_id, role, streaming, created_at, updated_at, payload_json)" +
					" SELECT 'a' || i, 'th1', NULL, 'assistant', 0, " + q(at(0)) + ", " + q(at(0)) + ", '{}' FROM n;")
				s.sent("m1", time.Second, text)
				s.flush()
				c.set(2 * time.Second)
				checkMessages(t, mustPoll(t, r).Messages, c.t, []rec{web("m1", time.Second, 32, false, event.ClassSubstantive)})
				if got := r.State().Cursor.RowID; got != 1201 {
					t.Errorf("the cursor is at rowid %d, want 1201", got)
				}
			}},
		{"a closed T3, its -wal file gone, is not read at all, and nothing is made in its folder",
			func(t *testing.T, s *store, c *clock) {
				r := s.reader(c, tracked(), State{})
				mustPoll(t, r)
				s.close()
				s.sent("m1", 0, text)
				s.flush()
				c.set(time.Second)
				if _, err := r.Poll(context.Background()); !errors.Is(err, ErrNotRunning) {
					t.Errorf("Poll with T3 closed gave %v, want ErrNotRunning", err)
				}
				if got := s.files(); !slices.Equal(got, []string{"statev2.sqlite"}) {
					t.Errorf("T3's folder holds %q, want only the store", got)
				}
				if st := r.Status(); st.Reading || st.Off != OffNotRunning {
					t.Errorf("the status is %+v, want off as T3 isn't running", st)
				}
			}},
		{"a read that T3 stops in the middle of starts no more sqlite3, so nothing is made in its folder",
			func(t *testing.T, s *store, c *clock) {
				r := s.reader(c, tracked(), State{})
				mustPoll(t, r)
				s.close()
				// The next run of a read that began while T3 held its store open.
				if _, err := r.query(context.Background(), startSQL()); !errors.Is(err, ErrNotRunning) {
					t.Errorf("a run after T3 stopped gave %v, want ErrNotRunning", err)
				}
				if got := s.files(); !slices.Equal(got, []string{"statev2.sqlite"}) {
					t.Errorf("T3's folder holds %q, want only the store", got)
				}
			}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newStore(t)
			c := &clock{}
			c.set(0)
			tc.run(t, s, c)
		})
	}
}

// TestProbe holds the schema probe to DESIGN 5.1: the reader reads a store
// with the columns, the index and the payload keys it needs and migration
// 55, passes a newer head with a doctor line, and turns anything else down.
func TestProbe(t *testing.T) {
	cases := []struct {
		name  string
		edit  string // SQL that changes the store
		why   string // what the refusal says; "" when the store passes
		newer bool
	}{
		{name: "a store of the schema the reader was tested against passes"},
		{name: "a newer head passes, with a doctor line", edit: "INSERT INTO effect_sql_migrations (migration_id, name) VALUES (86, 'Later');",
			newer: true},
		{name: "a store with no migration 55 is turned down", edit: "DELETE FROM effect_sql_migrations WHERE migration_id = 55;",
			why: "no migration 55"},
		{name: "a store whose migration 55 isn't OrchestrationV2 is turned down",
			edit: "UPDATE effect_sql_migrations SET name = 'Other' WHERE migration_id = 55;", why: "no migration 55"},
		{name: "a store whose head is below 55 is turned down",
			edit: "DELETE FROM effect_sql_migrations; INSERT INTO effect_sql_migrations (migration_id, name) VALUES (54, 'ProjectionThreadsAutoSettleDisabledAt');",
			why:  "no migration 55"},
		{name: "a table without a column the reader names is turned down",
			// The table again without driver, as Ubuntu 20.04's sqlite3 3.31 can
			// make it: DROP COLUMN came in 3.35.
			edit: "CREATE TABLE pt_without_driver AS SELECT provider_thread_id, thread_id, owner_node_id, provider, provider_session_id," +
				" status, first_run_ordinal, last_run_ordinal, updated_at, payload_json, provider_instance_id" +
				" FROM orchestration_v2_projection_provider_threads;" +
				" DROP TABLE orchestration_v2_projection_provider_threads;" +
				" ALTER TABLE pt_without_driver RENAME TO orchestration_v2_projection_provider_threads;",
			why: "has no column driver"},
		{name: "a stamp index on other columns is turned down",
			edit: "DROP INDEX orchestration_v2_projection_messages_thread_created_idx;" +
				" CREATE INDEX orchestration_v2_projection_messages_thread_created_idx ON orchestration_v2_projection_messages(thread_id, updated_at);",
			why: "isn't on (thread_id, created_at, message_id)"},
		{name: "a message without a payload key the reader reads is turned down",
			edit: "INSERT INTO orchestration_v2_projection_messages (message_id, thread_id, role, streaming, created_at, updated_at, payload_json)" +
				" VALUES ('m1', 'th1', 'user', 0, " + q(at(0)) + ", " + q(at(0)) + ", '{\"id\":\"m1\",\"text\":\"\"}');",
			why: "no payload key createdBy"},
		{name: "a store that isn't T3's is turned down",
			edit: "DROP TABLE effect_sql_migrations;", why: "isn't a store of T3 Code's version 2 schema"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newStore(t)
			if tc.edit != "" {
				s.add(tc.edit)
				s.flush()
			}
			c := &clock{}
			c.set(0)
			r := s.reader(c, tracked(), State{})
			_, err := r.Poll(context.Background())
			st := r.Status()
			var se *SchemaError
			switch {
			case tc.why == "" && err != nil:
				t.Errorf("Poll: %v", err)
			case tc.why != "" && (!errors.As(err, &se) || !strings.Contains(se.Why, tc.why)):
				t.Errorf("Poll gave %v, want a schema refusal that says %q", err, tc.why)
			case tc.why != "" && (st.Reading || st.Off != OffSchema || !strings.Contains(st.Problem, tc.why)):
				t.Errorf("the status is %+v, want off for the schema", st)
			}
			if st.Newer != tc.newer {
				t.Errorf("Newer is %v, want %v", st.Newer, tc.newer)
			}
			if strings.Contains(st.Problem, s.home) {
				t.Errorf("the status names a path: %q", st.Problem)
			}
		})
	}
}

// TestNoStore holds the reader to its doctor states when there is nothing
// to read.
func TestNoStore(t *testing.T) {
	c := &clock{}
	r, err := New(Options{
		Stores: []Store{{Path: t.TempDir() + "/userdata/statev2.sqlite", From: FromDefault}},
		Keys:   adapters.FixtureKeys(),
		Folder: tracked().resolve,
		Now:    c.now,
	})
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		call func() error
	}{
		{"a Poll with no store anywhere says so", func() error { _, err := r.Poll(context.Background()); return err }},
		{"a Pair with no store anywhere says so, and knows nothing", func() error {
			p, _, err := r.Pair(context.Background(), Prompt{SessionKey: session("sess-1"), T: 1, EventID: "prompt-1"})
			if p.Known || p.Guest || p.Relay != "" {
				t.Errorf("the pairing is %+v, want an empty one", p)
			}
			return err
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.call(); !errors.Is(err, ErrNoStore) {
				t.Errorf("got %v, want ErrNoStore", err)
			}
			if st := r.Status(); st.Off != OffNoStore {
				t.Errorf("the status is %+v, want off for no store", st)
			}
		})
	}
}

// TestQuiet holds the reader to the cost DESIGN 3.3 allows it while T3
// runs: a read starts no sqlite3 while a stat of the -wal and -shm files
// says T3 has written nothing since the last read, unless a row waits for
// its folder or a prompt asks. The last read's word is the stat once its
// runs are done, while the -wal is as it was when the read began: the
// reader's own mark stamps the -shm (TestOwnReads), and T3 commits nothing
// without writing the -wal.
func TestQuiet(t *testing.T) {
	const text = "please fix the flaky parser test"
	cases := []struct {
		name    string
		folders folders // tracked() when nil
		// wrote says T3 wrote after the read before the one under test, and
		// ask that the read under test is Pair's. during is what changed in
		// the stat while the read before ran.
		wrote, ask bool
		during     func(f *walFiles)
		runs       bool
	}{
		{name: "a read when T3 wrote nothing since the last one starts no sqlite3"},
		{name: "a read after T3 wrote starts sqlite3", wrote: true, runs: true},
		{name: "a read while a row waits for its folder starts sqlite3, though T3 wrote nothing",
			folders: folders{"/work/alpha": {State: FolderPending}}, runs: true},
		{name: "a read for a prompt the agent asks about starts sqlite3, though T3 wrote nothing", ask: true, runs: true},
		{name: "a read after one whose own runs stamped the -shm alone, as setting a reader's mark does, starts no sqlite3",
			during: func(f *walFiles) { f.shmTime++ }},
		{name: "a read after one during which T3 wrote, changing the -wal, starts sqlite3",
			during: func(f *walFiles) { f.walSize, f.walTime = f.walSize+1, f.walTime+1 }, runs: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newStore(t)
			sp := newSpy(t, s.sqlite3)
			c := &clock{}
			c.set(0)
			f := tc.folders
			if f == nil {
				f = tracked()
			}
			r := s.reader(c, f, State{})
			r.sqlite = &Runner{Path: sp.path}
			files := walFiles{walSize: 1, walTime: 1, shmSize: 1, shmTime: 1}
			// next, when set, changes what the stat says from its next call
			// on: the call that settles a read, once its runs are done.
			var next func(*walFiles)
			r.stat = func(string) (walFiles, bool) {
				said := files
				if next != nil {
					next(&files)
					next = nil
				}
				return said, true
			}
			mustPoll(t, r)
			s.sent("m1", 0, text)
			s.flush()
			files.walSize, files.walTime = 2, 2
			next = tc.during
			c.set(time.Second)
			mustPoll(t, r)
			if tc.wrote {
				files.walSize, files.walTime = 3, 3
			}
			before := len(sp.runs(t))
			c.set(2 * time.Second)
			if tc.ask {
				if _, _, err := r.Pair(context.Background(), Prompt{SessionKey: session("sess-1"),
					T: base.Add(1500 * time.Millisecond).UnixMilli(), EventID: "prompt-1"}); err != nil {
					t.Fatalf("Pair: %v", err)
				}
			} else {
				mustPoll(t, r)
			}
			if runs := len(sp.runs(t)) > before; runs != tc.runs {
				t.Errorf("the read started sqlite3: %v, want %v", runs, tc.runs)
			}
			if st := r.Status(); !st.Reading {
				t.Errorf("the status is %+v, want reading", st)
			}
		})
	}
}

// TestWALFiles holds walOf to what TestQuiet's stat stands in for: a row T3
// writes changes what a stat of the store's -wal and -shm files says.
func TestWALFiles(t *testing.T) {
	cases := []struct {
		name  string
		write func(s *store)
		same  bool
	}{
		{"a row T3 writes changes what the stat says", func(s *store) { s.sent("m1", 0, "please fix the flaky parser test") }, false},
		{"with nothing written the stat says the same", func(*store) {}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newStore(t)
			before, ok := walOf(s.path)
			if !ok {
				t.Fatal("no -wal or -shm file to stat")
			}
			tc.write(s)
			s.flush()
			after, ok := walOf(s.path)
			if !ok {
				t.Fatal("no -wal or -shm file to stat")
			}
			if same := before == after; same != tc.same {
				t.Errorf("the stats %+v and %+v are the same: %v, want %v", before, after, same, tc.same)
			}
		})
	}
}

// TestOwnReads holds the reader's own sqlite3 runs to what its skip rests
// on, on Linux, with a store's real -wal and -shm files, for which TestQuiet
// stands a stat in: once a read has read what T3 wrote, each of the
// reader's kinds of run leaves both files as a stat before it found them,
// size and time alike, and the read after that one starts no sqlite3. The
// first run after T3 writes sets a reader's mark in the -shm for T3's
// newest commit, as any SQLite reader of a store in WAL mode does, and the
// kernel stamps the -shm's time at that write; it never writes the -wal,
// so the read settles on the stat its runs leave (settled). Every case
// reads once first, as the reader does the moment T3 writes.
func TestOwnReads(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the times Linux gives a store's -wal and -shm files are the ones this test reads")
	}
	const text = "please fix the flaky parser test"
	poll := func(t *testing.T, r *Reader) { mustPoll(t, r) }
	cases := []struct {
		name string
		// ready readies the reader for the read under test, once it has read
		// what T3 wrote: nil for none.
		ready func(r *Reader)
		read  func(t *testing.T, r *Reader)
		// runs says the read under test starts sqlite3.
		runs bool
	}{
		{name: "the read after the one that read what T3 wrote starts no sqlite3, as the stat that read left still holds",
			read: poll},
		{name: "a Poll's runs leave the -wal and -shm files as a stat before them found them",
			ready: func(r *Reader) { r.quiet = nil }, read: poll, runs: true},
		{name: "a Pair's runs, which read the watched rows and their runs, leave the files as they were",
			read: func(t *testing.T, r *Reader) {
				if _, _, err := r.Pair(context.Background(), Prompt{SessionKey: session("sess-1"),
					T: base.Add(500 * time.Millisecond).UnixMilli(), EventID: "prompt-1"}); err != nil {
					t.Fatalf("Pair: %v", err)
				}
			}, runs: true},
		{name: "the schema probe's runs leave the files as they were",
			ready: func(r *Reader) { r.quiet, r.probed = nil, time.Time{} }, read: poll, runs: true},
		{name: "a resync's runs, which walk T3's stamp index, leave the files as they were",
			ready: func(r *Reader) {
				c := *r.cursor
				c.Message = "the key of a message the row at the cursor doesn't hold"
				r.quiet, r.cursor = nil, &c
			}, read: poll, runs: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newStore(t)
			sp := newSpy(t, s.sqlite3)
			c := &clock{}
			c.set(0)
			r := s.reader(c, tracked(), State{})
			r.sqlite = &Runner{Path: sp.path}
			mustPoll(t, r)
			s.sent("m1", 0, text)
			s.flush()
			c.set(time.Second)
			if b := mustPoll(t, r); len(b.Messages) != 1 {
				t.Fatalf("the read of what T3 wrote got %d records, want 1", len(b.Messages))
			}
			if tc.ready != nil {
				tc.ready(r)
			}
			before, ok := walOf(s.path)
			if !ok {
				t.Fatal("no -wal or -shm file to stat")
			}
			n := len(sp.runs(t))
			c.set(2 * time.Second)
			tc.read(t, r)
			if runs := len(sp.runs(t)) > n; runs != tc.runs {
				t.Errorf("the read started sqlite3: %v, want %v", runs, tc.runs)
			}
			after, ok := walOf(s.path)
			if !ok {
				t.Fatal("no -wal or -shm file to stat")
			}
			if after != before {
				t.Errorf("the reader's runs changed the stat of the -wal and -shm files from %+v to %+v", before, after)
			}
		})
	}
}

// TestNoSQLite3 holds the reader to DESIGN 5.9's doctor line for a missing
// sqlite3: the reader looks for one at every read, whether or not T3 runs,
// and says it is missing whatever else keeps it from reading.
func TestNoSQLite3(t *testing.T) {
	cases := []struct {
		name    string
		store   bool // T3's store is there
		open    bool // T3 holds it open
		missing bool // there is no sqlite3
		// gone removes the sqlite3 an earlier read ran before the read the
		// case checks, which T3's write since keeps from its quiet skip.
		gone bool
		off  string
	}{
		{name: "with T3 running, no sqlite3 turns the reader off, and the status says so", store: true, open: true, missing: true, off: OffNoSQLite3},
		{name: "with T3 stopped, the reader is off for that, and the status still says sqlite3 is missing", store: true, missing: true, off: OffNotRunning},
		{name: "with no store, the status still says sqlite3 is missing", missing: true, off: OffNoStore},
		{name: "with sqlite3 there, the status doesn't say it is missing", store: true, off: OffNotRunning},
		{name: "a sqlite3 removed after an earlier read ran it is missing too, not a read that failed, so doctor gives the install command",
			store: true, open: true, gone: true, off: OffNoSQLite3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newStore(t)
			if !tc.open {
				s.close()
			}
			path := s.path
			if !tc.store {
				path = t.TempDir() + "/userdata/statev2.sqlite"
			}
			tool := s.sqlite3
			switch {
			case tc.missing:
				tool = "sqlite3-that-isnt-there"
			case tc.gone:
				tool = newShim(t, s.sqlite3, "").path
			}
			c := &clock{}
			c.set(0)
			r, err := New(Options{Stores: []Store{{Path: path, From: FromDefault}}, SQLite: &Runner{Path: tool},
				Keys: adapters.FixtureKeys(), Folder: tracked().resolve, Now: c.now, Zone: time.UTC})
			if err != nil {
				t.Fatal(err)
			}
			if tc.gone {
				if _, err := r.Poll(context.Background()); err != nil {
					t.Fatalf("the read before the removal failed: %v", err)
				}
				if err := os.Remove(tool); err != nil {
					t.Fatal(err)
				}
				s.sent("m1", time.Second, "please fix the flaky parser test")
				s.flush()
				c.set(2 * time.Second)
			}
			if _, err := r.Poll(context.Background()); err == nil {
				t.Error("Poll worked, want an error")
			}
			if st := r.Status(); st.Reading || st.Off != tc.off || st.NoSQLite3 != (tc.missing || tc.gone) {
				t.Errorf("the status is %+v, want off %s and NoSQLite3 %v", st, tc.off, tc.missing || tc.gone)
			}
		})
	}
}

// TestNoJSON holds the reader to DESIGN 5.9's doctor line for a sqlite3
// built without JSON functions, which no query of the reader works without:
// the reader is off for that reason alone, so doctor can say how to get a
// sqlite3 that has them.
func TestNoJSON(t *testing.T) {
	cases := []struct {
		name string
		body string // the shim's sh
	}{
		{"a sqlite3 that knows no JSON function turns the reader off for no JSON", noJSON},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newStore(t)
			sh := newShim(t, s.sqlite3, tc.body)
			c := &clock{}
			c.set(0)
			r := s.reader(c, tracked(), State{})
			r.sqlite = &Runner{Path: sh.path}
			_, err := r.Poll(context.Background())
			var se *SchemaError
			if !errors.Is(err, ErrNoJSON) || !errors.As(err, &se) {
				t.Errorf("Poll gave %v, want a SchemaError for ErrNoJSON", err)
			}
			if st := r.Status(); st.Reading || st.Off != OffNoJSON || st.Problem != ErrNoJSON.Error() {
				t.Errorf("the status is %+v, want off for no JSON", st)
			}
		})
	}
}
