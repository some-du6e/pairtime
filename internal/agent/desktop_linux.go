// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build linux

package agent

import (
	"context"

	"github.com/some-du6e/pairtime/internal/agent/focus"
	"github.com/some-du6e/pairtime/internal/agent/presence"
	"github.com/some-du6e/pairtime/internal/platform/linux"
)

// openOS opens the Linux desktop's signals once, for presence and focus
// both (WP-OS9): idle time from the X server or GNOME's Mutter, focus from
// the window manager's _NET_ACTIVE_WINDOW, and suspend, resume, lock and
// unlock from logind (internal/platform/linux). Open doesn't fail: a signal
// the desktop doesn't give is unknown, and GET /v1/status says why.
func (a *agent) openOS(Options) osSources {
	d := linux.Open(context.Background(), linux.Config{Log: a.log.With("component", "desktop")})
	return osSources{
		presence: presence.LinuxOS(d),
		focus:    focus.LinuxOS(d),
		signals:  func() signalsReply { return linuxSignals(d.Status()) },
		close:    func() { d.Close() },
	}
}

// linuxSignals is GET /v1/status's signals from the desktop's Status,
// which names each signal's source or says why it is unknown.
//
// TODO(D-focus-unknown): while Focus is unknown nothing records that;
// WP-EV2 and its follow-up record an os.focus with app_class unknown.
func linuxSignals(st linux.Status) signalsReply {
	return signalsReply{
		Idle:    signalOf(st.Idle),
		Focus:   signalOf(st.Focus),
		Power:   signalOf(st.Power),
		Lock:    signalOf(st.Lock),
		Session: string(st.Session),
	}
}

func signalOf(s linux.Signal) signalReply {
	if s.Known() {
		return via(s.Via)
	}
	if s.Unknown == "" {
		return unknown("the desktop doesn't say")
	}
	return unknown(s.Unknown)
}
