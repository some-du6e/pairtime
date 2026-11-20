// SPDX-License-Identifier: AGPL-3.0-or-later

package pages

import (
	"bytes"
	"html/template"
	"io"
	"net/http"
)

// The home page is the page at / exactly, and where a sign-in without
// return_to lands. It says what pairtime is, how it counts, and where the
// pilot stands, and links the privacy notice, the evidence definition and
// the code. Its header shows the visitor's sign-in: who they are signed in
// as and a sign-out button, or a button to sign in with Hack Club Auth on a
// server where sign-in is set up.
//
// home.md renders once in New for each sign-in setting, like every other
// page. Only the header depends on the request, so the page goes through
// layout.html for each one, and New renders every state of it once, so a
// mistake in the template stops pairtimed at start.

// homeDescription is the home page's meta description, which link previews
// show.
const homeDescription = "pairtime counts the time you spend building with AI, not the time your AI spends on its own."

// SignIn wires the home page to Hack Club Auth sign-in. pairtimed fills it
// from its auth.Service.
type SignIn struct {
	// Enabled says whether sign-in is set up on this server. Without it the
	// page offers no sign-in, and says it isn't set up.
	Enabled bool
	// LoginPath is where the sign-in button goes, and LogoutPath is where
	// the sign-out form posts.
	LoginPath, LogoutPath string
	// Visitor returns the handle of the account a request is signed in to,
	// and whether it is signed in at all. Nil means no request is.
	Visitor func(r *http.Request) (handle string, ok bool)
}

// homeData is what home.md reads.
type homeData struct {
	// SignIn says whether sign-in is set up on this server.
	SignIn bool
	// SignupsOpen says whether anyone may make an account, or only the
	// operators (DESIGN 8.6).
	SignupsOpen bool
	// Source is where the code is published.
	Source string
}

// account is the sign-in area that layout.html shows in the home page's
// header. A page without one shows none.
type account struct {
	SignedIn bool
	// Handle names the signed-in account.
	Handle string
	// Login and Logout are the sign-in button's link and the sign-out
	// form's action.
	Login, Logout string
}

// renderHome renders home.md for a server with sign-in and for one
// without, and every state of the page once.
func (s *Site) renderHome(o Options) error {
	var err error
	d := homeData{SignupsOpen: o.SignupsOpen, Source: SourceURL}
	if s.homeNoSignIn, err = s.layout.body("home.md", d, nil); err != nil {
		return err
	}
	d.SignIn = true
	if s.homeSignIn, err = s.layout.body("home.md", d, nil); err != nil {
		return err
	}
	states := []struct {
		body template.HTML
		acct *account
	}{
		{body: s.homeNoSignIn},
		{body: s.homeSignIn, acct: &account{Login: "/sign-in", Logout: "/sign-out"}},
		{body: s.homeSignIn, acct: &account{SignedIn: true, Handle: "U0EXAMPLE", Login: "/sign-in", Logout: "/sign-out"}},
	}
	for _, st := range states {
		if err := s.layout.wrap(io.Discard, homeLayout(st.body, st.acct)); err != nil {
			return err
		}
	}
	return nil
}

// homeLayout is what layout.html reads for the home page.
func homeLayout(body template.HTML, a *account) layoutData {
	return layoutData{Description: homeDescription, Class: "home", Body: body, Account: a}
}

// RegisterHome adds the home page to mux at / exactly, so a path that
// nothing else serves is still a 404.
//
// The page differs by session, so nothing may store it. Its referrer
// policy is same-origin rather than the other pages' no-referrer: under
// no-referrer, browsers send "Origin: null" with a form's POST, and POST
// /auth/logout refuses a sign-out whose Origin isn't this server's. Links
// to other sites still carry no referrer. Only a signed-in visitor's page
// has a form, so only its CSP lets a form post, and only to this server.
func (s *Site) RegisterHome(mux *http.ServeMux, in SignIn) {
	body := s.homeNoSignIn
	if in.Enabled {
		body = s.homeSignIn
	}
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		var a *account
		if in.Enabled {
			a = &account{Login: in.LoginPath, Logout: in.LogoutPath}
			if in.Visitor != nil {
				a.Handle, a.SignedIn = in.Visitor(r)
			}
		}
		var page bytes.Buffer
		if err := s.layout.wrap(&page, homeLayout(body, a)); err != nil {
			// New rendered every state of the page, so this doesn't happen.
			http.Error(w, "This page didn't render.", http.StatusInternalServerError)
			return
		}
		csp := s.csp
		if a != nil && a.SignedIn {
			csp = s.cspForm
		}
		writePage(w, http.StatusOK, page.Bytes(), csp, "no-store", "same-origin")
	})
}
