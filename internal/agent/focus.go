// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"github.com/some-du6e/pairtime/internal/agent/focus"
	"github.com/some-du6e/pairtime/internal/agent/keys"
	"github.com/some-du6e/pairtime/internal/platform"
)

// newFocus makes the focus component (DESIGN 3.3, WP-OS2), which
// startFocus starts. It is made before the process scan, whose every scan
// refreshes it, and started after it, because each focus change asks the
// scan to run and reads the sessions the scan found.
//
//   - Each change goes to presence's FocusAt with its app class, which is
//     what makes a focused terminal, editor or agent app a project
//     context, and a record is written only while a context is live
//     (D14). Presence dates the sample it takes for the change the same as
//     the change's record.
//   - Records go to the ledger through presence's queue (ledgerIn.Focus,
//     WP-LG5), so a change's record reaches the writer ahead of its
//     sample. Their app_hash is keyed with K_app once the keystore has
//     opened (keyFocus), and left out before; it never reaches the ledger
//     body either way, because the writer builds that from
//     Event.Shareable.
//   - hosted_sessions come from the sessions the process scan found
//     (procs.Scanner.Sessions) and the processes the agent's own walk of a
//     hook's socket or pipe connection found, each with the session_key its
//     calls name (hookTies, WP-AG3). The chain a spooled hook call recorded
//     never adds one (DESIGN 5.2). The hook sink has the component look
//     again when it ties a process to a session, and when a session starts
//     or ends.
//   - focus_project comes from the project each hooked session works in,
//     or each session the process scan tied to its process (projectOf),
//     which the adapters set from the project the git adapter resolved the
//     session's folder to (adapters.go, WP-AG7).
//
// src is the focus source: focus.ThisOS(), or a fake in tests. startLedger
// and startPresence must have run.
func (a *agent) newFocus(src focus.OS) {
	a.focus = focus.New(focus.Options{
		OS:        src,
		Sessions:  func() map[platform.ID]string { return a.hooked.merged(a.procs.Sessions()) },
		ProjectOf: a.projectOf,
		AppSecret: a.appSecret,
		Live:      func() bool { return a.presence.Status().Live },
		Presence:  a.presence.FocusAt,
		Kick:      func() { a.procs.Kick() },
		Record:    func(r focus.Record) { a.ledgerIn.Focus(r.At, r.Data) },
		Problem:   func(msg string) { a.setProblem("focus", msg) },
		Log:       a.log.With("component", "focus"),
	})
}

// projectOf is the project of session for focus (focus.Options.ProjectOf):
// its hook tie's, or, for a session the process scan tied to its process
// (procs.Scanner.TiedTo), the one its records carry while the tie lasts.
func (a *agent) projectOf(session string) string {
	if p := a.hooked.projectOf(session); p != "" {
		return p
	}
	if a.procs == nil {
		return ""
	}
	if _, tied := a.procs.TiedTo(session); !tied {
		return ""
	}
	return a.hooked.tiedProjectOf(session)
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

// keyFocus derives K_app from the device key and keeps it for the focus
// component, which keys each os.focus record's app_hash with it (WP-OS2).
// openLedger calls it once the keystore has opened. Until then, and if it
// fails, records leave app_hash out rather than carry a plain hash.
func (a *agent) keyFocus(ks *keys.Keystore) {
	var kApp []byte
	err := ks.WithDeviceKey(func(kDev []byte) error {
		var err error
		kApp, err = focus.AppSecret(kDev)
		return err
	})
	if err != nil {
		a.log.Warn("os.focus records go without app_hash, because K_app couldn't be derived", "err", err)
		return
	}
	a.kApp.Store(&kApp)
}

// appSecret returns K_app, or nil until keyFocus has derived it.
func (a *agent) appSecret() []byte {
	if k := a.kApp.Load(); k != nil {
		return *k
	}
	return nil
}
