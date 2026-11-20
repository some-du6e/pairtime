// SPDX-License-Identifier: AGPL-3.0-or-later

package uploader

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"syscall"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/pkg/ledger"
)

func TestReasons(t *testing.T) {
	urlErr := func(err error) error {
		return &url.Error{Op: "Post", URL: "https://pairtime.example/api/v1/ingest/batches", Err: err}
	}
	tests := []struct {
		name   string
		err    error
		status int
		want   Reason
		ok     bool
	}{
		{"a name that doesn't resolve is dns", urlErr(&net.OpError{Op: "dial", Err: &net.DNSError{Err: "no such host", Name: "pairtime.example", IsNotFound: true}}), 0, ReasonDNS, true},
		{"a refused connection is connect", urlErr(&net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED}), 0, ReasonConnect, true},
		{"a connection cut mid-answer is connect", urlErr(fmt.Errorf("read: %w", syscall.ECONNRESET)), 0, ReasonConnect, true},
		{"a timeout is connect", urlErr(context.DeadlineExceeded), 0, ReasonConnect, true},
		{"a certificate from an unknown authority is tls", urlErr(&tls.CertificateVerificationError{Err: x509.UnknownAuthorityError{}}), 0, ReasonTLS, true},
		{"a certificate for another name is tls", urlErr(x509.HostnameError{Host: "pairtime.example"}), 0, ReasonTLS, true},
		{"a bad TLS record is tls", urlErr(tls.RecordHeaderError{Msg: "first record does not look like a TLS handshake"}), 0, ReasonTLS, true},
		{"net/http's handshake timeout is tls", urlErr(errors.New("net/http: TLS handshake timeout")), 0, ReasonTLS, true},
		{"a 429 is http_429", nil, http.StatusTooManyRequests, Reason429, true},
		{"a 503 is http_5xx", nil, http.StatusServiceUnavailable, Reason5xx, true},
		{"a 500 is http_5xx", nil, http.StatusInternalServerError, Reason5xx, true},
		{"a 200 is no failure", nil, http.StatusOK, "", false},
		{"a 409 is a refusal, not an outage", nil, http.StatusConflict, "", false},
		{"a 401 is a refusal, not an outage", nil, http.StatusUnauthorized, "", false},
		{"the agent stopping is no failure", urlErr(context.Canceled), 0, "", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := reasonOf(tc.err, tc.status)
			if got != tc.want || ok != tc.ok {
				t.Errorf("reasonOf = %q, %v; want %q, %v", got, ok, tc.want, tc.ok)
			}
		})
	}
	keys, err := ledger.DeriveKeys(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range Reasons() {
		t.Run("the ledger takes "+string(r)+" as a failure note's reason", func(t *testing.T) {
			if _, err := ledger.UploadFailureRecord(keys, 1, uint64(time.Now().UnixMilli()), string(r)); err != nil {
				t.Error(err)
			}
		})
	}
}

func TestBackoff(t *testing.T) {
	top := func(n int64) int64 { return n - 1 }
	zero := func(int64) int64 { return 0 }
	tests := []struct {
		name  string
		draw  func(int64) int64
		fails int
		after time.Duration
		want  time.Duration
	}{
		{"the first failure waits at most the base", top, 1, 0, backoffBase},
		{"each failure in a row doubles the ceiling", top, 3, 0, 4 * backoffBase},
		{"the ceiling stops at the cap", top, 40, 0, backoffCap},
		{"full jitter can draw no wait at all", zero, 5, 0, 0},
		{"a Retry-After longer than the draw is kept", zero, 1, 30 * time.Second, 30 * time.Second},
		{"a Retry-After past an hour waits an hour", zero, 1, 5 * time.Hour, retryAfterMax},
		{"a Retry-After shorter than the draw doesn't shorten it", top, 2, time.Second, 2 * backoffBase},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			b := newBackoff(tc.draw)
			var got time.Duration
			for range tc.fails {
				got = b.fail(tc.after)
			}
			if got != tc.want {
				t.Errorf("wait %v, want %v", got, tc.want)
			}
			b.ok()
			if b.failing() != 0 {
				t.Error("a success didn't start the count again")
			}
		})
	}
	now := time.Date(2026, 11, 20, 20, 0, 0, 0, time.UTC)
	headers := []struct {
		name  string
		value string
		want  time.Duration
	}{
		{"Retry-After in seconds", "120", 2 * time.Minute},
		{"Retry-After as an HTTP date", now.Add(90 * time.Second).Format(http.TimeFormat), 90 * time.Second},
		{"a Retry-After date in the past is no wait", now.Add(-time.Minute).Format(http.TimeFormat), 0},
		{"no Retry-After is no wait", "", 0},
		{"a Retry-After that doesn't parse is no wait", "soon", 0},
	}
	for _, tc := range headers {
		t.Run(tc.name, func(t *testing.T) {
			h := http.Header{}
			if tc.value != "" {
				h.Set("Retry-After", tc.value)
			}
			if got := retryAfter(h, now); got != tc.want {
				t.Errorf("retryAfter = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestClock(t *testing.T) {
	local := time.Date(2026, 11, 20, 20, 0, 0, 0, time.UTC)
	tests := []struct {
		name     string
		serverMS int64
		rtt      time.Duration
		want     time.Duration
	}{
		{"a server 10 minutes ahead moves the clock forward", local.Add(10*time.Minute + 50*time.Millisecond).UnixMilli(), 100 * time.Millisecond, 10 * time.Minute},
		{"a server behind moves it back", local.Add(-time.Hour + 50*time.Millisecond).UnixMilli(), 100 * time.Millisecond, -time.Hour},
		{"the offset is from the middle of the round trip", local.Add(time.Second).UnixMilli(), 2 * time.Second, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			now := local
			c := &clock{now: func() time.Time { return now }}
			if _, known := c.Offset(); known {
				t.Fatal("a new clock knows an offset")
			}
			c.observe(tc.serverMS, local, local.Add(tc.rtt))
			if off, _ := c.Offset(); off != tc.want {
				t.Errorf("offset %v, want %v", off, tc.want)
			}
			if got := c.serverNow(); !got.Equal(local.Add(tc.want)) {
				t.Errorf("serverNow %v", got)
			}
		})
	}
	h := http.Header{}
	h.Set("X-Pairtime-Time", "1795089660250")
	if ms, ok := serverTime(h); !ok || ms != 1795089660250 {
		t.Errorf("serverTime = %d, %v", ms, ok)
	}
}
