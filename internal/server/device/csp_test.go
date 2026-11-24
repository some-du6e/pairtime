// SPDX-License-Identifier: AGPL-3.0-or-later

package device_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/some-du6e/pairtime/internal/server/device"
	"github.com/some-du6e/pairtime/internal/server/pages"
	"github.com/some-du6e/pairtime/internal/server/pages/pagestest"
	"github.com/some-du6e/pairtime/internal/server/store"
)

// TestPagesCSP holds every page package device renders to the rules every
// server page keeps (D-server-ui test 1; pagestest.Page): a CSP of one
// shape, no script but the server's own files named by their hashes, and
// nothing that makes a browser fetch anything.
func TestPagesCSP(t *testing.T) {
	scripts, err := pages.Scripts()
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		page func(h *harness, b *browser) response
	}{
		{name: "/device, which asks for the code", page: func(_ *harness, b *browser) response { return b.get(device.PagePath) }},
		{name: "/device with a code to confirm", page: func(h *harness, b *browser) response {
			c := h.requestCode(testDeviceID)
			return b.get(device.PagePath + "?user_code=" + url.QueryEscape(c.UserCode))
		}},
		{name: "/device's answer for a computer another account holds", page: func(h *harness, b *browser) response {
			h.link(h.signIn(verified("ident!owner1", "U0OWNER1")), testDeviceID)
			c := h.requestCode(testDeviceID)
			return b.post(device.PagePath, url.Values{"csrf": {b.csrf(device.PagePath)}, "user_code": {c.UserCode}})
		}},
		{name: "/account/devices", page: func(_ *harness, b *browser) response { return b.get(device.DevicesPath) }},
	}
	for _, tt := range tests {
		t.Run(tt.name+" keeps the server pages' CSP, scripts and fetch rules", func(t *testing.T) {
			h := newHarness(t, harnessConfig{engine: store.SQLite})
			r := tt.page(h, h.signIn(verified("ident!teen1", "U0TEEN1")))
			if !strings.HasPrefix(r.header.Get("Content-Type"), "text/html") || r.status >= http.StatusInternalServerError {
				t.Fatalf("the page answered %d with %q:\n%s", r.status, r.header.Get("Content-Type"), r.body)
			}
			pagestest.Page(t, r.body, r.header.Get("Content-Security-Policy"), pagestest.Options{Scripts: scripts})
		})
	}
}
