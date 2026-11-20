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
//   - hosted_sessions come from the sessions the process scan found.
//     docs/PLAN.md names who wires the rest: the hook path (WP-AG3) adds
//     the harness processes its hooks claim, with their session keys and
//     the projects focus_project needs, and calls Refresh when a session
//     starts or ends.
//
// src is the focus source: focus.ThisOS(), or a fake in tests. startLedger
// and startPresence must have run.
func (a *agent) newFocus(src focus.OS) {
	a.focus = focus.New(focus.Options{
		OS:        src,
		Sessions:  func() map[platform.ID]string { return a.procs.Sessions() },
		AppSecret: a.appSecret,
		Live:      func() bool { return a.presence.Status().Live },
		Presence:  a.presence.FocusAt,
		Kick:      func() { a.procs.Kick() },
		Record:    func(r focus.Record) { a.ledgerIn.Focus(r.At, r.Data) },
		Problem:   func(msg string) { a.setProblem("focus", msg) },
		Log:       a.log.With("component", "focus"),
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
