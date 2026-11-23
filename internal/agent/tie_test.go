// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"encoding/json"
	"log/slog"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/adapters"
	"github.com/some-du6e/pairtime/internal/agent/home"
	"github.com/some-du6e/pairtime/internal/agent/hookpath"
	"github.com/some-du6e/pairtime/internal/agent/ledger"
	"github.com/some-du6e/pairtime/internal/agent/procs"
	"github.com/some-du6e/pairtime/internal/agent/tailers"
	"github.com/some-du6e/pairtime/internal/platform"
	"github.com/some-du6e/pairtime/pkg/event"
)

// tieRig is an agent's adapter host with a process scan that reads table,
// counting its reads, and a Claude Code adapter.
type tieRig struct {
	a     *agent
	h     *adapterHost
	ad    adapters.Adapter
	lr    adapters.LogReader
	reads int
}

func newTieRig(t *testing.T, table []platform.Proc) *tieRig {
	t.Helper()
	r := &tieRig{}
	me := platform.UIDOwner(uint32(os.Geteuid()))
	r.a = &agent{log: slog.New(slog.DiscardHandler), problems: map[string]string{}, hooked: newHookTies()}
	secret := make([]byte, hookpath.SessionSecretLen)
	r.a.kSession.Store(&secret)
	r.a.procs = procs.New(procs.Options{OS: platform.Linux, Self: me, Boot: "test-boot",
		Read: func() ([]platform.Proc, error) { r.reads++; return slices.Clone(table), nil }})
	r.h = newAdapterHost(r.a)
	ad, ok := adapters.Default.New(event.HarnessClaudeCode, adapters.Options{Keys: adapters.NewKeys(secret)})
	if !ok {
		t.Fatal("no Claude Code adapter")
	}
	r.ad, r.lr = ad, ad.(adapters.LogReader)
	return r
}

// hookCall has the rig's adapter parse a prompt's hook call of session
// sid: via hook, from under a claude in a terminal, or via plugin, from
// the spool, with no walk.
func (r *tieRig) hookCall(t *testing.T, sid string, at time.Time, via event.Via) {
	t.Helper()
	b, _ := json.Marshal(map[string]any{"session_id": sid, "hook_event_name": "UserPromptSubmit", "prompt_id": "p1",
		"prompt": "What does this do?", "cwd": "/home/capture/p", "transcript_path": "/home/capture/p/" + sid + ".jsonl"})
	raw := adapters.RawRecord{Source: adapters.SourceHook, Event: "UserPromptSubmit", Payload: b,
		Arrival: adapters.Arrival{Via: via, T: at.UnixMilli(), Mono: 5000, BootID: "test-boot", Harness: event.HarnessClaudeCode}}
	if via == event.ViaHook {
		raw.Arrival.Walk = &platform.Lineage{ParentClass: event.ParentTerminalApp, Entrypoint: event.EntrypointUnknown, HasTTY: true}
	}
	if _, err := r.ad.Parse(raw); err != nil {
		t.Fatal(err)
	}
}

// clearLine has the rig's adapter read the first user entry of session
// sid's transcript, written at at: the /clear command, which starts the
// session in the process of the one it clears.
func (r *tieRig) clearLine(t *testing.T, sid string, at time.Time) {
	t.Helper()
	b, _ := json.Marshal(map[string]any{"type": "user", "sessionId": sid, "timestamp": at.UTC().Format(time.RFC3339Nano),
		"cwd": "/home/capture/p", "version": "2.1.327", "entrypoint": "cli", "isSidechain": false,
		"message": map[string]any{"role": "user",
			"content": "<command-name>/clear</command-name>\n<command-message>clear</command-message>\n<command-args></command-args>"}})
	file := "/home/capture/.claude/projects/-p/" + sid + ".jsonl"
	r.ad.Parse(adapters.RawRecord{Source: adapters.SourceLog, Payload: b, Log: &adapters.LogLine{File: file, Path: file, End: int64(len(b) + 1)},
		Arrival: adapters.Arrival{Via: event.ViaLog, T: at.UnixMilli(), Mono: 5000, BootID: "test-boot"}})
	if !r.lr.LogCleared(sid) {
		t.Fatal("the reader doesn't take the log for one that opened with /clear")
	}
}

// TestTieLive checks the gates tieLive puts before a tie (WP-AD3): only a
// fresh line of a session no hook reports, by the log reader's memory or
// this run's hook ties, and none of whose lines since its last that counts
// is out of pairtime, reaches procs.Scanner.Tie, and none of a session
// whose log opened with /clear, which runs in the process of the session it
// cleared. A session's first try only
// reads the table, and its next line written after that read ties it, while
// a session weighed and not tied reads the process table again only after
// tieRetry. A session whose hook calls all came through the spool is one no
// hook reports, as it is for the reader's session.start (LogHooked).
func TestTieLive(t *testing.T) {
	now := time.Now()
	me := platform.UIDOwner(uint32(os.Geteuid()))
	desktop := []platform.Proc{
		{PID: 1, Name: "systemd", PGID: 1, TTY: platform.TTYNone, Start: now.Add(-time.Hour), Owner: "uid:0"},
		{PID: 100, PPID: 1, Name: "gnome-terminal-server", PGID: 100, TTY: platform.TTYNone, Start: now.Add(-40 * time.Minute), Owner: me},
	}
	claude := func(pid int) []platform.Proc {
		return []platform.Proc{
			{PID: pid - 1, PPID: 100, Name: "bash", PGID: pid - 1, TTY: platform.TTYAttached, Start: now.Add(-30 * time.Minute), Owner: me},
			{PID: pid, PPID: pid - 1, Name: "claude", PGID: pid, TTY: platform.TTYAttached, Start: now.Add(-5 * time.Minute), Owner: me},
		}
	}
	one := append(slices.Clone(desktop), claude(111)...)
	two := append(slices.Clone(one), claude(121)...)
	const sid = "617e16a3-ddb4-44ca-af68-72cf58ed1a34"
	// line has tieLive take a line of the session written at at, as the
	// worker does, and lines a first line now and a second written after
	// the read the first made.
	line := func(r *tieRig, lr adapters.LogReader, at time.Time) {
		r.h.tieLive(lr, event.HarnessClaudeCode, sid, "/home/capture/p", at.UnixMilli(), nil)
	}
	lines := func(r *tieRig) {
		line(r, r.lr, now)
		line(r, r.lr, time.Now().Add(time.Second))
	}
	cases := []struct {
		name  string
		table []platform.Proc
		// run has tieLive take lines as the case says.
		run   func(t *testing.T, r *tieRig)
		tied  bool
		reads int
	}{
		{"a session's first fresh line only reads the table, and its next, written after that read, ties it", one,
			func(t *testing.T, r *tieRig) { lines(r) }, true, 2},
		{"a first line alone ties nothing", one, func(t *testing.T, r *tieRig) { line(r, r.lr, now) }, false, 1},
		{"a line written longer than tieFresh ago ties nothing and reads no table", one,
			func(t *testing.T, r *tieRig) { line(r, r.lr, now.Add(-tieFresh-time.Second)) }, false, 0},
		{"a session the log reader remembers a hook reported ties nothing", one,
			func(t *testing.T, r *tieRig) {
				r.hookCall(t, sid, now.Add(-time.Minute), event.ViaHook)
				lines(r)
			}, false, 0},
		{"a session the log reader remembers a hook reported before a restart ties nothing", one,
			func(t *testing.T, r *tieRig) {
				r.hookCall(t, sid, now.Add(-time.Minute), event.ViaHook)
				mem, err := r.ad.(adapters.Stateful).SaveState()
				if err != nil {
					t.Fatal(err)
				}
				ad, _ := adapters.Default.New(event.HarnessClaudeCode, adapters.Options{Keys: adapters.NewKeys(make([]byte, hookpath.SessionSecretLen))})
				if err := ad.(adapters.Stateful).LoadState(mem); err != nil {
					t.Fatal(err)
				}
				line(r, ad.(adapters.LogReader), now)
				line(r, ad.(adapters.LogReader), time.Now().Add(time.Second))
			}, false, 0},
		{"a session whose hook calls all came through the spool ties as one no hook reports", one,
			func(t *testing.T, r *tieRig) {
				r.hookCall(t, sid, now.Add(-time.Minute), event.ViaPlugin)
				lines(r)
			}, true, 2},
		{"a session a hook tied in this run ties nothing", one,
			func(t *testing.T, r *tieRig) {
				r.a.hooked.tie(platform.ID{PID: 999}, r.h.sessionKey(sid))
				lines(r)
			}, false, 0},
		{"a session a line of which ran out of pairtime since its last that counts ties nothing", one,
			func(t *testing.T, r *tieRig) {
				r.a.hooked.ignore(r.h.sessionKey(sid))
				lines(r)
			}, false, 0},
		{"a session whose log opened with /clear ties nothing and reads no table", one,
			func(t *testing.T, r *tieRig) {
				r.clearLine(t, sid, now)
				lines(r)
			}, false, 0},
		{"a session weighed and not tied isn't weighed again within tieRetry", two,
			func(t *testing.T, r *tieRig) {
				lines(r)
				line(r, r.lr, time.Now().Add(2*time.Second))
			}, false, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newTieRig(t, tc.table)
			tc.run(t, r)
			_, tied := r.a.procs.TiedTo(r.h.sessionKey(sid))
			if tied != tc.tied || r.reads != tc.reads {
				t.Errorf("tied %v after %d table reads; want tied %v after %d", tied, r.reads, tc.tied, tc.reads)
			}
		})
	}
}

// TestScanStartMono checks the run clock of the scan's session.start
// (scanStart): its mono is this run's clock less how long before now the
// start is dated, and never later than now, as for a first line dated
// ahead of its read. A start dated more than LATE_READ_S before now has
// none, as a line read that late has none.
func TestScanStartMono(t *testing.T) {
	cases := []struct {
		name string
		// first is when the session's first line read live was written,
		// from now, and before how far before now the start's mono is, at
		// least, while the run is that old. none says it has no mono.
		first, before time.Duration
		none          bool
	}{
		{"a first line five seconds old gives a mono five seconds before now", -5 * time.Second, 4 * time.Second, false},
		{"a first line dated ahead of its read gives a mono no later than now", 5 * time.Second, 0, false},
		{"a first line 11 s old gives no mono", -11 * time.Second, 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newTieRig(t, nil)
			key := r.h.sessionKey("617e16a3-ddb4-44ca-af68-72cf58ed1a34")
			r.a.procs.Touch(key, time.Now().Add(tc.first), "")
			rec, ok := r.h.scanStart(procs.Session{Event: event.SessionStart{StartKind: event.StartStartup}}, event.HarnessClaudeCode, key, nil)
			after, _ := ledger.RunClock(time.Now())
			want := max(after-tc.before.Milliseconds(), 0)
			if tc.none {
				want = 0
			}
			if !ok || rec.Event.Mono > want || tc.none && rec.Event.Mono != 0 {
				t.Errorf("the scan's start has mono %d (%v); want at most %d", rec.Event.Mono, ok, want)
			}
		})
	}
}

// TestTiedStart checks that a session's later session.starts, at a change
// of its token, keep how its first went, so the session has one parent
// class (CC9; DESIGN 7.3): a session untied at its first start keeps the
// transcript's, though tied since, and one tied at its first keeps that
// walk's parent and TTY, though its process went to a hook since.
func TestTiedStart(t *testing.T) {
	now := time.Now()
	me := platform.UIDOwner(uint32(os.Geteuid()))
	claude := platform.Proc{PID: 111, PPID: 110, Name: "claude", PGID: 111, TTY: platform.TTYAttached, Start: now.Add(-5 * time.Minute), Owner: me}
	table := []platform.Proc{
		{PID: 1, Name: "systemd", PGID: 1, TTY: platform.TTYNone, Start: now.Add(-time.Hour), Owner: "uid:0"},
		{PID: 100, PPID: 1, Name: "gnome-terminal-server", PGID: 100, TTY: platform.TTYNone, Start: now.Add(-40 * time.Minute), Owner: me},
		{PID: 110, PPID: 100, Name: "bash", PGID: 110, TTY: platform.TTYAttached, Start: now.Add(-30 * time.Minute), Owner: me},
		claude,
	}
	const sid = "617e16a3-ddb4-44ca-af68-72cf58ed1a34"
	// start has tiedStart take the log reader's session.start of the
	// session, with token and kind.
	start := func(r *tieRig, token string, kind event.StartKind) adapters.Record {
		st := &event.SessionStart{StartKind: kind, Entrypoint: event.EntrypointUnknown, ParentClass: event.ParentUnknown}
		rec := adapters.Record{Event: event.Event{Envelope: event.Envelope{T: now.UnixMilli(), Source: event.SourceLog, Via: event.ViaLog,
			Harness: event.HarnessClaudeCode, SessionKey: r.h.sessionKey(sid)}, Data: st}, Token: token}
		return r.h.tiedStart(rec, st)
	}
	tie := func(t *testing.T, r *tieRig) {
		t.Helper()
		// The first try reads the table, and a line written after it ties.
		key := r.h.sessionKey(sid)
		r.a.procs.Tie(event.HarnessClaudeCode, key, now)
		if _, ok, _ := r.a.procs.Tie(event.HarnessClaudeCode, key, time.Now().Add(time.Second)); !ok {
			t.Fatal("no tie")
		}
	}
	cases := []struct {
		name string
		// run gives the session's first and second starts.
		run           func(t *testing.T, r *tieRig) (first, second adapters.Record)
		first, second event.Via
		parent        event.ParentClass // the second's
	}{
		{"a session untied at its first start keeps the transcript's at the next, though tied since",
			func(t *testing.T, r *tieRig) (adapters.Record, adapters.Record) {
				first := start(r, "cli", event.StartStartup)
				tie(t, r)
				return first, start(r, "sdk-cli", event.StartResume)
			}, event.ViaLog, event.ViaLog, event.ParentUnknown},
		{"a session tied at its first start keeps that walk at the next, though a hook claimed its process since",
			func(t *testing.T, r *tieRig) (adapters.Record, adapters.Record) {
				tie(t, r)
				first := start(r, "cli", event.StartStartup)
				r.a.procs.Claim("test-boot", claude)
				if _, ok := r.a.procs.TieOf(r.h.sessionKey(sid)); ok {
					t.Fatal("the claim left the tie")
				}
				return first, start(r, "sdk-cli", event.StartResume)
			}, event.ViaOS, event.ViaOS, event.ParentTerminalApp},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newTieRig(t, table)
			first, second := tc.run(t, r)
			s := second.Event.Data.(*event.SessionStart)
			if first.Event.Via != tc.first || second.Event.Via != tc.second || s.ParentClass != tc.parent {
				t.Errorf("the starts are via %s and %s, the second %+v; want via %s and %s, the second's parent %s",
					first.Event.Via, second.Event.Via, s, tc.first, tc.second, tc.parent)
			}
		})
	}
}

// TestLookbackLeavesTies checks that a line a history lookback read
// (tailers.Line.Lookback) neither ties its session nor makes its
// session.start the scan's, though the session is tied now: it tells of a
// time before the agent ran, and the process the session runs in now may
// not be the one that started it then.
func TestLookbackLeavesTies(t *testing.T) {
	now := time.Now()
	me := platform.UIDOwner(uint32(os.Geteuid()))
	table := []platform.Proc{
		{PID: 1, Name: "systemd", PGID: 1, TTY: platform.TTYNone, Start: now.Add(-time.Hour), Owner: "uid:0"},
		{PID: 100, PPID: 1, Name: "gnome-terminal-server", PGID: 100, TTY: platform.TTYNone, Start: now.Add(-40 * time.Minute), Owner: me},
		{PID: 110, PPID: 100, Name: "bash", PGID: 110, TTY: platform.TTYAttached, Start: now.Add(-30 * time.Minute), Owner: me},
		{PID: 111, PPID: 110, Name: "claude", PGID: 111, TTY: platform.TTYAttached, Start: now.Add(-5 * time.Minute), Owner: me},
	}
	const sid = "617e16a3-ddb4-44ca-af68-72cf58ed1a34"
	cases := []struct {
		name string
		// made is how long before the ledger began the history's prompt was
		// made, which starts the session; tied says the session is tied
		// before lookback reads it, with the two table reads that takes.
		made time.Duration
		tied bool
		// reads is how many times the process table is read in all, and
		// start says the transcript's session.start reaches the ledger.
		reads int
		start bool
	}{
		{"a line of a history made within tieFresh ties nothing and reads no table", 2 * time.Second, false, 0, false},
		{"a history's session.start stays the transcript's, though its session is tied now", 2 * time.Minute, true, 2, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			dirs := home.Dirs{Data: dir, Config: dir, Runtime: dir}
			w := optionsWriter(t, dirs, func(o *ledger.Options) { o.Online = func() bool { return true } })
			reads := 0
			a := &agent{dirs: dirs, log: slog.New(slog.DiscardHandler), problems: map[string]string{}, hooked: newHookTies()}
			secret := make([]byte, hookpath.SessionSecretLen)
			a.kSession.Store(&secret)
			a.procs = procs.New(procs.Options{OS: platform.Linux, Self: me, Boot: "test-boot",
				Read: func() ([]platform.Proc, error) { reads++; return slices.Clone(table), nil }})
			h := newAdapterHost(a)
			h.keys = &adapterKeys{}
			h.attach(w)
			key := h.sessionKey(sid)
			if tc.tied {
				a.procs.Tie(event.HarnessClaudeCode, key, time.Now())
				if _, ok, _ := a.procs.Tie(event.HarnessClaudeCode, key, time.Now().Add(time.Second)); !ok {
					t.Fatal("no tie")
				}
			}
			began := w.Created()
			b, err := json.Marshal(map[string]any{"type": "user", "sessionId": sid, "promptId": "ffffffff-0000-4000-8000-000000000061",
				"timestamp": began.Add(-tc.made).UTC().Format(time.RFC3339Nano), "cwd": "/home/teen/p", "version": "2.1.327",
				"entrypoint": "cli", "isSidechain": false, "promptSource": "typed", "turnOrigin": "human",
				"message": map[string]any{"role": "user", "content": "What does this do?"}})
			if err != nil {
				t.Fatal(err)
			}
			file := "/home/teen/.claude/projects/-p/" + sid + ".jsonl"
			line := tailers.Line{Harness: event.HarnessClaudeCode, Bytes: b, Log: adapters.LogLine{File: file, Path: file, End: int64(len(b) + 1)},
				Lookback: began.Add(-time.Hour).UnixMilli()}
			if !h.takeLine(tailers.New(tailers.Options{NoNotify: true}), line) {
				t.Fatal("takeLine didn't take the line")
			}
			if _, tied := a.procs.TiedTo(key); tied != tc.tied || reads != tc.reads {
				t.Errorf("tied %v after %d table reads; want tied %v after %d", tied, reads, tc.tied, tc.reads)
			}
			sealNow(t, w, dirs.Ledger())
			for _, rec := range readLedger(t, dirs, event.SourceOS) {
				if _, ok := rec.ev.Data.(*event.SessionStart); ok {
					t.Errorf("the history's session.start is the scan's: %+v", rec.ev.Envelope)
				}
			}
			starts := 0
			for _, rec := range readLedger(t, dirs, event.SourceLog) {
				if _, ok := rec.ev.Data.(*event.SessionStart); ok {
					starts++
				}
			}
			if tc.start && starts != 1 {
				t.Errorf("%d of the transcript's session.starts in the ledger, want 1", starts)
			}
		})
	}
}
