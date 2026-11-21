// SPDX-License-Identifier: AGPL-3.0-or-later

package pages

import (
	"bytes"
	"html/template"
	"net/http"
)

// Page is a page another package renders through layout.html, such as
// package device's /device and /account/devices (WP-SV3), so every page
// of the server has the same header, stylesheet, footer and CSP.
type Page struct {
	// Title heads the browser tab, before "pairtime".
	Title string
	// Body is the page's content, which the caller rendered with
	// html/template, so it is already escaped.
	Body template.HTML
	// Account is the header's sign-in area, or nil for none.
	Account *Header
	// Form says whether the body has a form. The page's CSP then lets a
	// form post to this server, and nowhere else.
	Form bool
}

// Header is the sign-in area of a Page's header, as the home page shows
// it.
type Header struct {
	SignedIn bool
	// Handle names the signed-in person: the display name of their
	// sign-in, or their account's handle without one.
	Handle string
	// Login and Logout are the sign-in button's link and the sign-out
	// form's action.
	Login, Logout string
}

// WritePage writes p through layout.html with status. Like the home page,
// such a page differs by session, so nothing may store it, and its
// referrer policy is same-origin: under no-referrer, browsers send
// "Origin: null" with a form's POST, which the forms' own Origin checks
// refuse. A signed-in header has the sign-out form, so its CSP lets a form
// post to this server too.
func (s *Site) WritePage(w http.ResponseWriter, status int, p Page) {
	d := layoutData{Title: p.Title, Body: p.Body}
	if h := p.Account; h != nil {
		d.Account = &account{SignedIn: h.SignedIn, Handle: h.Handle, Login: h.Login, Logout: h.Logout}
	}
	var page bytes.Buffer
	if err := s.layout.wrap(&page, d); err != nil {
		http.Error(w, "This page didn't render.", http.StatusInternalServerError)
		return
	}
	csp := s.csp
	if p.Form || (d.Account != nil && d.Account.SignedIn) {
		csp = s.cspForm
	}
	writePage(w, status, page.Bytes(), csp, "no-store", "same-origin")
}
