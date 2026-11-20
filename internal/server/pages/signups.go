// SPDX-License-Identifier: AGPL-3.0-or-later

package pages

// Signups is the gate on account creation (DESIGN 8.6, 10.2). While
// signups_open is false, only the HCA subjects on the operators list may
// get an account, so an operator can smoke-test a deployed server; Karim
// decides who is on that list.
//
// pairtimed hands MayCreate to HCA sign-in as accounts.Options.MayCreate.
// Sign-in asks it only about an identity that has no account, inside the
// transaction that finds none, so an existing account always signs in. A
// refused identity gets sign-in's own error page, code signups_closed, and
// nothing about it is written.
type Signups struct {
	open      bool
	operators map[string]bool
}

// MayCreate reports whether the HCA identity sub, which has no account yet,
// may get one.
func (s *Signups) MayCreate(sub string) bool {
	return s.open || s.operators[sub]
}
