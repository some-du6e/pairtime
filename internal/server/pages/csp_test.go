// SPDX-License-Identifier: AGPL-3.0-or-later

package pages

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/some-du6e/pairtime/internal/server/pages/pagestest"
)

// TestPageCSP holds every page pages renders, and what WritePage writes
// for the pages other packages render, to the rules every server page
// keeps (D-server-ui test 1; pagestest.Check): a CSP of one shape, no
// inline script but the server's own files named by their hashes, and
// nothing that makes a browser fetch anything. WritePage carries no
// script until a page rendered outside pages needs one (D-server-ui point
// 2), so the page with a script is the owner's dashboard.
func TestPageCSP(t *testing.T) {
	scripts, err := Scripts()
	if err != nil {
		t.Fatal(err)
	}
	if len(scripts) == 0 {
		t.Fatal("pages embeds no .js file, so no case checks a page's script")
	}
	s := newSite(t, Options{})
	type page struct{ name, body, csp string }
	pages := []page{
		{name: "the privacy notice", body: string(s.privacy), csp: s.csp},
		{name: "the evidence page", body: string(s.current), csp: s.csp},
	}
	for _, e := range s.evidence {
		pages = append(pages, page{name: "the evidence definition from scoring " + e.from.String(), body: string(e.page), csp: s.csp})
	}
	for _, h := range []struct {
		name   string
		signIn SignIn
	}{
		{name: "the home page without sign-in", signIn: SignIn{}},
		{name: "the home page for a visitor who isn't signed in", signIn: signInOn("")},
		{name: "the home page for a signed-in visitor", signIn: signInOn("U0FAKEVERIFIED")},
	} {
		resp, body := getHome(t, Options{}, h.signIn)
		pages = append(pages, page{name: h.name, body: body, csp: resp.Header.Get("Content-Security-Policy")})
	}
	for _, d := range []struct {
		name   string
		opts   Options
		signIn SignIn
	}{
		{name: "the sample dashboard without sign-in", signIn: SignIn{}},
		{name: "the sample dashboard with sign-ups closed", signIn: signInOn("")},
		{name: "the sample dashboard with sign-ups open", opts: Options{SignupsOpen: true}, signIn: signInOn("")},
	} {
		resp, body := getDemo(t, d.opts, d.signIn)
		pages = append(pages, page{name: d.name, body: body, csp: resp.Header.Get("Content-Security-Policy")})
	}
	for _, o := range []struct {
		name    string
		signIn  SignIn
		account string
	}{
		{name: "the signed-in owner's dashboard, with its script", signIn: signInOn("U0FAKEVERIFIED"), account: "u1"},
		{name: "the owner's dashboard with sign-in off", signIn: SignIn{}},
	} {
		mux, _ := ownerSite(t, o.signIn, o.account, fakeSource{v: ownerView(t)}, true, nil)
		resp, body := serve(mux, httptest.NewRequest(http.MethodGet, DashboardPath, nil))
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s answered %d:\n%s", o.name, resp.StatusCode, body)
		}
		pages = append(pages, page{name: o.name, body: body, csp: resp.Header.Get("Content-Security-Policy")})
	}
	for _, w := range []struct {
		name string
		p    Page
	}{
		{name: "WritePage's page with no sign-in area", p: Page{Title: "A page", Body: "<p>Some words.</p>"}},
		{name: "WritePage's page for a visitor who isn't signed in", p: Page{Title: "A page", Body: "<p>Some words.</p>", Account: &Header{Login: "/auth/hca/login?return_to=%2Fdevice"}}},
		{name: "WritePage's page for a signed-in visitor, whose header has the sign-out form", p: Page{Title: "A page", Body: "<p>Some words.</p>", Account: &Header{SignedIn: true, Name: "Robin", Logout: "/auth/logout"}}},
		{name: "WritePage's page with a form of its own", p: Page{Title: "A page", Body: `<form method="post" action="/device"><button type="submit">Link it</button></form>`, Form: true}},
	} {
		rec := httptest.NewRecorder()
		s.WritePage(rec, http.StatusOK, w.p)
		pages = append(pages, page{name: w.name, body: rec.Body.String(), csp: rec.Header().Get("Content-Security-Policy")})
	}
	for _, p := range pages {
		t.Run(p.name+" keeps the server pages' CSP, scripts and fetch rules", func(t *testing.T) {
			pagestest.Page(t, p.body, p.csp, pagestest.Options{Scripts: scripts})
		})
	}
}
