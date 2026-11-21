// SPDX-License-Identifier: AGPL-3.0-or-later

package account

import (
	"errors"
	"net/url"
	"strings"
)

// ErrNoBrowser means this computer has no way pairtime knows to open a
// browser, as on a server without a desktop.
var ErrNoBrowser = errors.New("account: no browser to open here")

// BrowserURL returns the page a login opens in the browser: the code's
// verification_uri_complete, or its verification_uri, when it is on the
// server being linked, and false otherwise. The server sends it, and on
// Windows the command that opens a URL would also start a program, so
// nothing else is ever opened. Where another account on the computer can
// read the command line of the browser it starts, as on Linux, it opens
// verification_uri, which asks for the code the terminal shows, so the
// code stays out of the command line (openWithCode).
func BrowserURL(code Code, server string) (string, bool) {
	return browserURL(code, server, openWithCode)
}

// browserURL is BrowserURL, which opens verification_uri_complete only
// with withCode.
func browserURL(code Code, server string, withCode bool) (string, bool) {
	want, err := url.Parse(server)
	if err != nil {
		return "", false
	}
	pages := []string{code.VerificationURI}
	if withCode {
		pages = []string{code.VerificationURIComplete, code.VerificationURI}
	}
	for _, raw := range pages {
		u, err := url.Parse(raw)
		if err != nil || raw == "" || u.User != nil || u.Fragment != "" {
			continue
		}
		if strings.EqualFold(u.Scheme, want.Scheme) && strings.EqualFold(u.Host, want.Host) && strings.HasPrefix(u.Path, "/") {
			return u.String(), true
		}
	}
	return "", false
}

// OpenBrowser opens u, which BrowserURL returned, in the default browser,
// without waiting for it.
func OpenBrowser(u string) error {
	cmd, err := browserCommand(u)
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}
