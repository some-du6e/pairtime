// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"log/slog"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/agent/hookpath"
	"github.com/some-du6e/pairtime/internal/agent/procs"
	"github.com/some-du6e/pairtime/internal/platform"
	"github.com/some-du6e/pairtime/pkg/event"
)

// TestTieLive checks the gates tieLive puts before a tie (WP-AD3): only a
// fresh line of a session no hook reports, by the adapter's memory or this
// run's hook ties, reaches procs.Scanner.Tie, and a session that wasn't
// tied reads the process table again only after tieRetry.
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
		// run calls tieLive as the case says and returns whether it may tie.
		run   func(h *adapterHost)
		tied  bool
		reads int
	}{
		{"a fresh line of a session no hook reports ties it", one,
			func(h *adapterHost) { h.tieLive(event.HarnessClaudeCode, sid, now.UnixMilli(), false) }, true, 1},
		{"a line written longer than tieFresh ago ties nothing and reads no table", one,
			func(h *adapterHost) {
				h.tieLive(event.HarnessClaudeCode, sid, now.Add(-tieFresh-time.Second).UnixMilli(), false)
			}, false, 0},
		{"a session the adapter remembers a hook reported ties nothing", one,
			func(h *adapterHost) { h.tieLive(event.HarnessClaudeCode, sid, now.UnixMilli(), true) }, false, 0},
		{"a session a hook tied in this run ties nothing", one,
			func(h *adapterHost) {
				h.a.hooked.tie(platform.ID{PID: 999}, h.sessionKey(sid))
				h.tieLive(event.HarnessClaudeCode, sid, now.UnixMilli(), false)
			}, false, 0},
		{"a session that couldn't be tied isn't tried again within tieRetry", two,
			func(h *adapterHost) {
				h.tieLive(event.HarnessClaudeCode, sid, now.UnixMilli(), false)
				h.tieLive(event.HarnessClaudeCode, sid, now.UnixMilli(), false)
			}, false, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reads := 0
			a := &agent{log: slog.New(slog.DiscardHandler), problems: map[string]string{}, hooked: newHookTies()}
			secret := make([]byte, hookpath.SessionSecretLen)
			a.kSession.Store(&secret)
			a.procs = procs.New(procs.Options{OS: platform.Linux, Self: me, Boot: "test-boot",
				Read: func() ([]platform.Proc, error) { reads++; return slices.Clone(tc.table), nil }})
			h := newAdapterHost(a)
			tc.run(h)
			_, tied := a.procs.TiedTo(h.sessionKey(sid))
			if tied != tc.tied || reads != tc.reads {
				t.Errorf("tied %v after %d table reads; want tied %v after %d", tied, reads, tc.tied, tc.reads)
			}
		})
	}
}
