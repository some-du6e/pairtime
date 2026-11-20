// SPDX-License-Identifier: AGPL-3.0-or-later

package pages

import "net/http"

// Signups is the gate on account creation (DESIGN 8.6, 10.2). While
// signups_open is false, only the HCA subjects on the operators list may
// get an account, so an operator can smoke-test a deployed server; Karim
// decides who is on that list. An identity that already has an account
// always signs in.
type Signups struct {
	open      bool
	operators map[string]bool
	refusal   []byte
	csp       string
}

// Open reports whether anyone may create an account.
func (s *Signups) Open() bool { return s.open }

// MayCreate reports whether the HCA identity sub, which has no account yet,
// may get one.
func (s *Signups) MayCreate(sub string) bool {
	return s.open || s.operators[sub]
}

// Admit is what HCA sign-in calls once it has verified an identity and
// before it writes anything about it (WP-SV2). exists says whether sub
// already has an account. When sub may not get one, Admit answers with the
// refusal page and 403 Forbidden and returns false. The caller then
// returns at once and writes no row.
func (s *Signups) Admit(w http.ResponseWriter, sub string, exists bool) bool {
	if exists || s.MayCreate(sub) {
		return true
	}
	writePage(w, http.StatusForbidden, s.refusal, s.csp, "no-store")
	return false
}
