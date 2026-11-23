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
	"strings"
	"syscall"
	"testing"
	"time"

	agentledger "github.com/some-du6e/pairtime/internal/agent/ledger"
	"github.com/some-du6e/pairtime/pkg/event"
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

func TestPace(t *testing.T) {
	t0 := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	spent := func(n int, at time.Time) *pacer {
		p := newPacer(paceBurst, paceEvery)
		for range n {
			p.take(at)
		}
		return p
	}
	drained := newPacer(paceBurst, paceEvery)
	drained.drain(t0)
	tests := []struct {
		name      string
		got, want time.Duration
	}{
		{"a burst goes at once", spent(paceBurst-1, t0).wait(t0), 0},
		{"past the burst, the next request waits a pace", spent(paceBurst, t0).wait(t0), paceEvery},
		{"each pace earns one request back", spent(paceBurst, t0).wait(t0.Add(paceEvery)), 0},
		{"a request past the pace, as on lock and suspend, makes the next one wait the longer", spent(paceBurst+1, t0).wait(t0), 2 * paceEvery},
		{"after a 429 the requests left are spent", drained.wait(t0), paceEvery},
		{"a clock that went back earns nothing", spent(paceBurst, t0).wait(t0.Add(-time.Hour)), paceEvery},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.got != tc.want {
				t.Errorf("wait %v, want %v", tc.got, tc.want)
			}
		})
	}
	t.Run("an upload asked for every second for an hour never runs ingest's bucket dry", func(t *testing.T) {
		p := newPacer(paceBurst, paceEvery)
		server := newPacer(30, 10*time.Second) // ingest-v1 7 item 17
		sent := 0
		for s := range 3600 {
			now := t0.Add(time.Duration(s) * time.Second)
			if p.wait(now) > 0 {
				continue
			}
			if server.wait(now) > 0 {
				t.Fatalf("the server would refuse request %d, at %v", sent+1, now.Sub(t0))
			}
			p.take(now)
			server.take(now)
			sent++
		}
		if limit := paceBurst + 3600/int(paceEvery/time.Second); sent > limit {
			t.Errorf("%d requests in an hour, over %d", sent, limit)
		}
	})
}

func TestFlushBound(t *testing.T) {
	t0 := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	limits := []struct {
		name      string
		got, want time.Duration
	}{
		{"with no hook waiting, a flush waits up to its end", flushLimit(t0, t0.Add(flushWait), 0, false), flushWait},
		{"a hook leaves it no more than hookBound after the hook", flushLimit(t0, t0.Add(flushWait), t0.Add(-2*time.Second).UnixMilli(), false), hookBound - 2*time.Second},
		{"a hook longer ago than hookBound ends it at once", flushLimit(t0, t0.Add(flushWait), t0.Add(-hookBound-time.Second).UnixMilli(), false), 0},
		{"an urgent upload ends it at once", flushLimit(t0, t0.Add(flushWait), 0, true), 0},
	}
	for _, tc := range limits {
		t.Run(tc.name, func(t *testing.T) {
			if tc.got != tc.want {
				t.Errorf("wait %v, want %v", tc.got, tc.want)
			}
		})
	}
	// A scorer that never catches up, as during a long rebuild.
	slow := func(t *testing.T) *Uploader {
		t.Helper()
		block := make(chan struct{})
		t.Cleanup(func() { close(block) })
		return bareUploader(t, func(o *Options) { o.Flush = func() { <-block } })
	}
	waits := []struct {
		name string
		meet func(u *Uploader) // what happens while the flush waits
	}{
		{"a hook that comes while a flush waits for a slow scorer ends the wait hookBound after the hook", func(u *Uploader) {
			u.hookAt.Store(time.Now().Add(300*time.Millisecond - hookBound).UnixMilli())
			u.tellFlush()
		}},
		{"an urgent upload ends a flush that waits", func(u *Uploader) { u.Urgent() }},
	}
	for _, tc := range waits {
		t.Run(tc.name, func(t *testing.T) {
			u := slow(t)
			start := time.Now()
			time.AfterFunc(100*time.Millisecond, func() { tc.meet(u) })
			if !u.flush() {
				t.Fatal("the flush says the uploader is stopping")
			}
			if took := time.Since(start); took > 3*time.Second {
				t.Errorf("the flush waited %v, where flushWait would be %v", took, flushWait)
			}
		})
	}
	t.Run("a batch with a stop starts the bound, and the plan that takes its row ends it", func(t *testing.T) {
		u := bareUploader(t, nil)
		u.st, u.rows, u.loaded = saved{Chain: u.opts.Ledger.DeviceID(), From: 1}, &rowIndex{device: u.opts.Ledger.DeviceID()}, true
		rtype, _ := ledger.RTypeOf(event.KindAgentStop)
		hdr, err := ledger.NewHeader(1, rtype, "p_test", "s_test")
		if err != nil {
			t.Fatal(err)
		}
		u.Sealed(agentledger.Batch{Row: ledger.BatchRow{B: 1, FirstSeq: 1, N: 1}, SealedAt: time.Now(), Records: []ledger.Record{{Header: hdr, Body: []byte(`{"e":{}}`)}}})
		set := u.hookAt.Load() != 0
		u.takeSealed()
		if !set || u.hookAt.Load() != 0 {
			t.Errorf("the bound started %v, and ends at %d", set, u.hookAt.Load())
		}
	})
	t.Run("a hook's upload goes within DESIGN 3.3's 10 s, with a second for the plan and the round trip", func(t *testing.T) {
		if hookBound+time.Second > 10*time.Second || SoonDelay >= hookBound {
			t.Errorf("hookBound %v, SoonDelay %v", hookBound, SoonDelay)
		}
	})
}

// TestClockProblem checks what doctor hears of a clock off the server's:
// the server dates a minute by this computer's clock, so a slow one makes
// live work late, and one more than 5 minutes fast has it refused at
// submission (WP-SV6).
func TestClockProblem(t *testing.T) {
	tests := []struct {
		name string
		off  time.Duration // the server's clock less this computer's
		has  string        // what doctor hears, "" for nothing
	}{
		{"a clock 20 minutes slow makes live work late, doctor says", 20 * time.Minute, "is 20 minutes behind the server's"},
		{"a clock 10 minutes fast has work refused, doctor says", -10 * time.Minute, "is 10 minutes ahead of the server's"},
		{"a clock 5 and a half minutes fast is more than 5 minutes ahead, past the limit, not at it", -(5*time.Minute + 30*time.Second), "is more than 5 minutes ahead of the server's"},
		{"a clock 20 and a half minutes slow is more than 20 minutes behind", 20*time.Minute + 30*time.Second, "is more than 20 minutes behind the server's"},
		{"a clock 2 minutes slow is nothing to say", 2 * time.Minute, ""},
		{"nor is one 2 minutes fast", -2 * time.Minute, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := clockProblem(tc.off, 900_000)
			if tc.has == "" && got != "" || !strings.Contains(got, tc.has) {
				t.Errorf("doctor hears %q, want %q in it", got, tc.has)
			}
		})
	}
}
