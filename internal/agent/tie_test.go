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
	"github.com/some-du6e/pairtime/internal/agent/hookpath"
	"github.com/some-du6e/pairtime/internal/agent/procs"
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
// sid, from under a claude in a terminal.
func (r *tieRig) hookCall(t *testing.T, sid string, at time.Time) {
	t.Helper()
	b, _ := json.Marshal(map[string]any{"session_id": sid, "hook_event_name": "UserPromptSubmit", "prompt_id": "p1",
		"prompt": "What does this do?", "cwd": "/home/capture/p", "transcript_path": "/home/capture/p/" + sid + ".jsonl"})
	raw := adapters.RawRecord{Source: adapters.SourceHook, Event: "UserPromptSubmit", Payload: b,
		Arrival: adapters.Arrival{Via: event.ViaHook, T: at.UnixMilli(), Mono: 5000, BootID: "test-boot",
			Walk:    &platform.Lineage{ParentClass: event.ParentTerminalApp, Entrypoint: event.EntrypointUnknown, HasTTY: true},
			Harness: event.HarnessClaudeCode}}
	if _, err := r.ad.Parse(raw); err != nil {
		t.Fatal(err)
	}
}

// TestTieLive checks the gates tieLive puts before a tie (WP-AD3): only a
// fresh line of a session no hook reports, by the log reader's memory or
// this run's hook ties, reaches procs.Scanner.Tie, and a session that
// wasn't tied reads the process table again only after tieRetry.
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
	cases := []struct {
		name  string
		table []platform.Proc
		// run calls tieLive as the case says.
		run   func(t *testing.T, r *tieRig)
		tied  bool
		reads int
	}{
		{"a fresh line of a session no hook reports ties it", one,
			func(t *testing.T, r *tieRig) { r.h.tieLive(r.lr, event.HarnessClaudeCode, sid, now.UnixMilli()) }, true, 1},
		{"a line written longer than tieFresh ago ties nothing and reads no table", one,
			func(t *testing.T, r *tieRig) {
				r.h.tieLive(r.lr, event.HarnessClaudeCode, sid, now.Add(-tieFresh-time.Second).UnixMilli())
			}, false, 0},
		{"a session the log reader remembers a hook reported ties nothing", one,
			func(t *testing.T, r *tieRig) {
				r.hookCall(t, sid, now.Add(-time.Minute))
				r.h.tieLive(r.lr, event.HarnessClaudeCode, sid, now.UnixMilli())
			}, false, 0},
		{"a session the log reader remembers a hook reported before a restart ties nothing", one,
			func(t *testing.T, r *tieRig) {
				r.hookCall(t, sid, now.Add(-time.Minute))
				mem, err := r.ad.(adapters.Stateful).SaveState()
				if err != nil {
					t.Fatal(err)
				}
				ad, _ := adapters.Default.New(event.HarnessClaudeCode, adapters.Options{Keys: adapters.NewKeys(make([]byte, hookpath.SessionSecretLen))})
				if err := ad.(adapters.Stateful).LoadState(mem); err != nil {
					t.Fatal(err)
				}
				r.h.tieLive(ad.(adapters.LogReader), event.HarnessClaudeCode, sid, now.UnixMilli())
			}, false, 0},
		{"a session a hook tied in this run ties nothing", one,
			func(t *testing.T, r *tieRig) {
				r.a.hooked.tie(platform.ID{PID: 999}, r.h.sessionKey(sid))
				r.h.tieLive(r.lr, event.HarnessClaudeCode, sid, now.UnixMilli())
			}, false, 0},
		{"a session that couldn't be tied isn't tried again within tieRetry", two,
			func(t *testing.T, r *tieRig) {
				r.h.tieLive(r.lr, event.HarnessClaudeCode, sid, now.UnixMilli())
				r.h.tieLive(r.lr, event.HarnessClaudeCode, sid, now.UnixMilli())
			}, false, 1},
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
		if _, ok := r.a.procs.Tie(event.HarnessClaudeCode, r.h.sessionKey(sid), now); !ok {
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
