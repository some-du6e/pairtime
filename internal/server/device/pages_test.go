// SPDX-License-Identifier: AGPL-3.0-or-later

package device_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/some-du6e/pairtime/internal/server/auth/hcafake"
	"github.com/some-du6e/pairtime/internal/server/device"
	"github.com/some-du6e/pairtime/internal/server/store/storetest"
)

// TestAccountNames checks how the device pages name the account they are
// signed in to (D-display-name). Where a page asks whether the account is
// the reader's, it gives the display name of the sign-in with the Slack ID
// beside it, since anyone can pick a name, and the Slack ID alone without
// a display name. The header gives the name alone, and without one no name
// and no id at all (docs/errata/2026-11-22-header-without-name.md).
func TestAccountNames(t *testing.T) {
	yes := true
	named := verified("ident!teen1", "U0TEEN1")
	named.FirstName = "Robin"
	noSlack := hcafake.Identity{Sub: "ident!teen3", Email: "teen3@example.org", VerificationStatus: "verified", YSWSEligible: &yes, FirstName: "Sam"}
	markup := verified("ident!teen4", "U0TEEN4")
	markup.FirstName = "<b>Robin</b>"
	const robin = "<strong><bdi>Robin</bdi> (Slack ID U0TEEN1)</strong>"

	// The pages, each read by a browser signed in as its case's identity.
	enter := func(_ *harness, b *browser) string { return b.get(device.PagePath).body }
	confirm := func(h *harness, b *browser) string {
		c := h.requestCode(testDeviceID)
		return b.get(device.PagePath + "?user_code=" + url.QueryEscape(c.UserCode)).body
	}
	taken := func(h *harness, b *browser) string {
		h.link(h.signIn(verified("ident!owner1", "U0OWNER1")), testDeviceID)
		c := h.requestCode(testDeviceID)
		return b.post(device.PagePath, url.Values{"csrf": {b.csrf(device.PagePath)}, "user_code": {c.UserCode}}).body
	}
	relink := func(h *harness, b *browser) string {
		h.link(b, testDeviceID)
		c := h.requestCode(testDeviceID)
		return b.get(device.PagePath + "?user_code=" + url.QueryEscape(c.UserCode)).body
	}
	devices := func(_ *harness, b *browser) string { return b.get(device.DevicesPath).body }
	token := func(h *harness, b *browser) string { return h.link(b, testDeviceID).Account }

	tests := []struct {
		name    string
		id      hcafake.Identity
		page    func(h *harness, b *browser) string
		want    []string
		notWant []string
	}{
		{
			name: "/device names the account by its display name with the Slack ID beside it, and the header by the name",
			id:   named, page: enter,
			want: []string{"to link that computer to your account, " + robin + ".", "<p>Signed in as <strong>Robin</strong></p>"},
		},
		{
			name: "the confirmation names the account the same way, beside the code to check",
			id:   named, page: confirm,
			want: []string{"asks to be linked to your account, " + robin + "."},
		},
		{
			name: "the relink page names the account the same way, since approving it signs the other copy of pairtime out",
			id:   named, page: relink,
			want: []string{"asks to be linked to your account, " + robin + ", as the computer"},
		},
		{
			name: "the taken page names the account the same way",
			id:   named, page: taken,
			want: []string{"So it can't be linked to your account, " + robin + ", and nothing changed."},
		},
		{
			name: "/account/devices names the account the same way",
			id:   named, page: devices,
			want: []string{"The computers linked to your account, " + robin + "."},
		},
		{
			name: "an account without a Slack ID shows its Hack Club Auth ID beside the name",
			id:   noSlack, page: enter,
			want: []string{"your account, <strong><bdi>Sam</bdi> (Hack Club Auth ID ident!teen3)</strong>."},
		},
		{
			name:    "without a display name the pages name the account by its Slack ID alone, as before, and the header shows no name and no id",
			id:      verified("ident!teen1", "U0TEEN1"),
			page:    enter,
			want:    []string{"your account, <strong>U0TEEN1</strong>.", `<a href="/dashboard">Your dashboard</a>` + "\n" + `<form method="post" action="/auth/logout">`},
			notWant: []string{"<bdi>", "Signed in", "<strong>U0TEEN1</strong></p>"},
		},
		{
			name:    "/account/devices without a display name keeps the Slack ID in its text, and the header shows none",
			id:      verified("ident!teen1", "U0TEEN1"),
			page:    devices,
			want:    []string{"The computers linked to your account, <strong>U0TEEN1</strong>."},
			notWant: []string{"Signed in"},
		},
		{
			name:    "a display name is shown as text, never as markup",
			id:      markup,
			page:    enter,
			want:    []string{"<bdi>&lt;b&gt;Robin&lt;/b&gt;</bdi> (Slack ID U0TEEN4)", "Signed in as <strong>&lt;b&gt;Robin&lt;/b&gt;</strong>"},
			notWant: []string{"<b>Robin</b>"},
		},
		{
			name:    "the token response names the account by its Slack ID, since the display name stays with the browser's session",
			id:      named,
			page:    token,
			want:    []string{"U0TEEN1"},
			notWant: []string{"Robin"},
		},
	}
	for _, engine := range storetest.Engines() {
		t.Run(string(engine), func(t *testing.T) {
			for _, tt := range tests {
				t.Run(tt.name, func(t *testing.T) {
					h := newHarness(t, harnessConfig{engine: engine})
					got := tt.page(h, h.signIn(tt.id))
					for _, w := range tt.want {
						if !strings.Contains(got, w) {
							t.Errorf("the page doesn't contain %q:\n%s", w, got)
						}
					}
					for _, w := range tt.notWant {
						if strings.Contains(got, w) {
							t.Errorf("the page contains %q:\n%s", w, got)
						}
					}
				})
			}
		})
	}
}

// TestSignedOutRedirects checks that the device pages send a browser that
// isn't signed in through sign-in and back with return_to query-encoded,
// as /dashboard does, so a page's own query comes back whole.
func TestSignedOutRedirects(t *testing.T) {
	tests := []struct {
		name, path, want string
	}{
		{name: "/device comes back to /device", path: device.PagePath, want: "/auth/hca/login?return_to=%2Fdevice"},
		{name: "/device with a code comes back to that code's confirmation", path: device.PagePath + "?user_code=wdjb+mjht", want: "/auth/hca/login?return_to=%2Fdevice%3Fuser_code%3DWDJB-MJHT"},
		{name: "/account/devices comes back to /account/devices", path: device.DevicesPath, want: "/auth/hca/login?return_to=%2Faccount%2Fdevices"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t, harnessConfig{engine: storetest.Engines()[0]})
			req, err := http.NewRequest(http.MethodGet, h.srv.URL+tt.path, nil)
			if err != nil {
				t.Fatal(err)
			}
			r := do(t, h.anonymous(), req)
			if r.status != http.StatusSeeOther || r.header.Get("Location") != tt.want {
				t.Errorf("GET %s signed out answered %d, Location %q; want 303 to %q", tt.path, r.status, r.header.Get("Location"), tt.want)
			}
		})
	}
}
