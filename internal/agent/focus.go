// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"github.com/some-du6e/pairtime/internal/agent/focus"
	"github.com/some-du6e/pairtime/internal/platform"
)

// newFocus makes the focus component (DESIGN 3.3, WP-OS2), which
// startFocus starts. It is made before the process scan, whose every scan
// refreshes it, and started after it, because each focus change asks the
// scan to run and reads the sessions the scan found.
//
//   - Each change goes to presence's Focus with its app class, which is
//     what makes a focused terminal, editor or agent app a project
//     context, and a record is written only while a context is live
//     (D14).
//   - hosted_sessions come from the sessions the process scan found. The
//     hook path (WP-AG3) adds the processes its hooks come from, with
//     their session keys and the projects focus_project needs, and calls
//     Refresh when a new one starts.
//   - Until the ledger writer exists (WP-LG5), records are dropped, as
//     presence's are; WP-LG5 also passes AppSecret, derived from K_dev,
//     until when app_hash stays out of the records.
//
// src is the focus source: focus.ThisOS(), or a fake in tests.
func (a *agent) newFocus(src focus.OS) {
	a.focus = focus.New(focus.Options{
		OS:       src,
		Sessions: func() map[platform.ID]string { return a.procs.Sessions() },
		Live:     func() bool { return a.presence.Status().Live },
		Presence: a.presence.Focus,
		Kick:     func() { a.procs.Kick() },
		Problem:  func(msg string) { a.setProblem("focus", msg) },
		Log:      a.log.With("component", "focus"),
	})
}

// startFocus starts the focus component and returns the step that stops
// it. newFocus and startProcs must have run.
func (a *agent) startFocus() func() {
	a.focus.Start()
	return a.focus.Stop
}

// refreshFocus has the focus component walk the process table again for
// the window in front, after a scan that may have found a session under
// it. Before newFocus, as in tests of the scan alone, it does nothing.
func (a *agent) refreshFocus() {
	if a.focus != nil {
		a.focus.Refresh()
	}
}
