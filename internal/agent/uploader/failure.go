// SPDX-License-Identifier: AGPL-3.0-or-later

package uploader

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"net/http"
	"strings"
)

// Reason is why an upload didn't reach the server, as an upload-failure
// note (rtype 0x41) names it: one of the five codes of DESIGN 3.6 and
// ledger-v1 4.7. The notes are the client's own claim that it was offline,
// which DESIGN 7.4's offline label rests on.
type Reason string

const (
	// ReasonDNS is a server name that didn't resolve.
	ReasonDNS Reason = "dns"
	// ReasonConnect is a connection that couldn't be made, or broke or
	// timed out before a whole response came back.
	ReasonConnect Reason = "connect"
	// ReasonTLS is a TLS handshake that failed, a certificate that didn't
	// verify included.
	ReasonTLS Reason = "tls"
	// Reason5xx is a response with a 5xx status.
	Reason5xx Reason = "http_5xx"
	// Reason429 is a 429 response: the server asked the device to slow
	// down.
	Reason429 Reason = "http_429"
)

// Reasons lists every Reason, in DESIGN 3.6's order.
func Reasons() []Reason {
	return []Reason{ReasonDNS, ReasonConnect, ReasonTLS, Reason5xx, Reason429}
}

// reasonOf returns the note an upload whose request failed with err, or
// whose response had status, gets, and false for an outcome that isn't a
// failure to reach the server: a success, any other status the server
// answered with, which is a refusal and not an outage, and a request that
// stopped because the agent is stopping.
func reasonOf(err error, status int) (Reason, bool) {
	if err == nil {
		switch {
		case status == http.StatusTooManyRequests:
			return Reason429, true
		case status >= 500 && status <= 599:
			return Reason5xx, true
		}
		return "", false
	}
	if errors.Is(err, context.Canceled) {
		return "", false
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return ReasonDNS, true
	}
	if isTLS(err) {
		return ReasonTLS, true
	}
	// Everything else is the connection: refused, reset, unreachable, or
	// timed out at any point before a whole response arrived.
	return ReasonConnect, true
}

// isTLS reports whether err comes from the TLS handshake. net/http's
// handshake timeout has an unexported type, so its text is checked too.
func isTLS(err error) bool {
	var (
		header   tls.RecordHeaderError
		alert    tls.AlertError
		verify   *tls.CertificateVerificationError
		unknown  x509.UnknownAuthorityError
		hostname x509.HostnameError
		invalid  x509.CertificateInvalidError
	)
	switch {
	case errors.As(err, &header), errors.As(err, &alert), errors.As(err, &verify),
		errors.As(err, &unknown), errors.As(err, &hostname), errors.As(err, &invalid):
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "TLS handshake") || strings.Contains(msg, "tls: ")
}
