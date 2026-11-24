// SPDX-License-Identifier: AGPL-3.0-or-later

package submission

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/server/backup"
	"github.com/some-du6e/pairtime/internal/server/ingest"
	"github.com/some-du6e/pairtime/internal/server/store/storetest"
)

// TestMessage checks M, the bytes a device's submissions request signs
// (D-sb1-submissions, Q4): ingest-v1 2.2's message with
// pairtime-submissions-v1 as its context and the method and request target
// in place of the request counter.
func TestMessage(t *testing.T) {
	empty := sha256.Sum256(nil)
	tests := []struct {
		name                                     string
		device, method, target, time, bodySHA256 string
		want                                     string
	}{
		{
			name:   "a create signs its method, path and body hash",
			device: "d_example", method: "POST", target: "/api/v1/submissions", time: "1795089660250", bodySHA256: strings.Repeat("ab", 32),
			want: "pairtime-submissions-v1\nd_example\nPOST /api/v1/submissions\n1795089660250\n" + strings.Repeat("ab", 32),
		},
		{
			name:   "a read signs its query and the SHA-256 of an empty body",
			device: "d_example", method: "GET", target: "/api/v1/submissions?updated_since=2026-11-24T12:00:00Z&limit=10", time: "1795089660250", bodySHA256: hex.EncodeToString(empty[:]),
			want: "pairtime-submissions-v1\nd_example\nGET /api/v1/submissions?updated_since=2026-11-24T12:00:00Z&limit=10\n1795089660250\ne3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := string(Message(tt.device, tt.method, tt.target, tt.time, tt.bodySHA256)); got != tt.want {
				t.Errorf("Message = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestSignedRequests checks a device's request to the submissions API
// (D-sb1-submissions, Q4): ingest's headers, checked in ingest-v1 2.5's
// order and answered with its codes, a signature over Message with no
// request counter, and the device's rate, which only a signed request
// uses up.
func TestSignedRequests(t *testing.T) {
	start, end := t0.Add(-2*day), t0.Add(-day)
	list := SubmissionsPath + "?limit=1"
	// signed is a list request from the laptop with edit applied.
	signed := func(h *harness, edit func(r *http.Request)) *http.Request {
		r := h.request("ptd_laptop", "GET", list, "")
		if edit != nil {
			edit(r)
		}
		return r
	}
	// signAs signs r again as device with key, over target and body, at.
	signAs := func(r *http.Request, device string, key ed25519.PrivateKey, method, target, body string, at time.Time) {
		ts := strconv.FormatInt(at.UnixMilli(), 10)
		sum := sha256.Sum256([]byte(body))
		bodySHA := hex.EncodeToString(sum[:])
		r.Header.Set(ingest.HeaderDevice, device)
		r.Header.Set(ingest.HeaderTime, ts)
		r.Header.Set(ingest.HeaderBodySHA256, bodySHA)
		r.Header.Set(ingest.HeaderSignature, "ed25519="+base64.StdEncoding.EncodeToString(ed25519.Sign(key, Message(device, method, target, ts, bodySHA))))
	}
	tests := []struct {
		name string
		// edit changes the laptop's signed list request, or token, when
		// set, sends it with that token instead.
		edit       func(r *http.Request)
		token      string
		wantStatus int
		wantCode   string
	}{
		{name: "a request signed as Message says is taken", wantStatus: http.StatusOK},
		{name: "an Authorization that isn't a bearer token gets 401 token_missing", edit: func(r *http.Request) { r.Header.Set("Authorization", "Basic ptd_laptop") }, wantStatus: http.StatusUnauthorized, wantCode: ingest.CodeTokenMissing},
		{name: "a token the device store doesn't know gets 401 token_invalid", token: "ptd_unknown", wantStatus: http.StatusUnauthorized, wantCode: ingest.CodeTokenInvalid},
		{name: "a removed device gets 403 device_revoked", token: "ptd_removed", wantStatus: http.StatusForbidden, wantCode: ingest.CodeDeviceRevoked},
		{name: "a device store that fails gets 503 unavailable", token: "ptd_down", wantStatus: http.StatusServiceUnavailable, wantCode: ingest.CodeUnavailable},
		{name: "no X-Pairtime-Device gets 403 device_mismatch", edit: func(r *http.Request) { r.Header.Del(ingest.HeaderDevice) }, wantStatus: http.StatusForbidden, wantCode: ingest.CodeDeviceMismatch},
		{name: "another device's X-Pairtime-Device gets 403 device_mismatch", edit: func(r *http.Request) { r.Header.Set(ingest.HeaderDevice, "d2") }, wantStatus: http.StatusForbidden, wantCode: ingest.CodeDeviceMismatch},
		{name: "a device with no key on record gets 403 device_key_missing", token: "ptd_keyless", wantStatus: http.StatusForbidden, wantCode: ingest.CodeDeviceKeyMissing},
		{name: "a time that isn't decimal gets 400 time_malformed", edit: func(r *http.Request) { r.Header.Set(ingest.HeaderTime, "+1795089660250") }, wantStatus: http.StatusBadRequest, wantCode: ingest.CodeTimeMalformed},
		{name: "no time gets 400 time_malformed", edit: func(r *http.Request) { r.Header.Del(ingest.HeaderTime) }, wantStatus: http.StatusBadRequest, wantCode: ingest.CodeTimeMalformed},
		{name: "a body hash in capitals gets 400 body_hash_malformed", edit: func(r *http.Request) {
			r.Header.Set(ingest.HeaderBodySHA256, strings.ToUpper(r.Header.Get(ingest.HeaderBodySHA256)))
		}, wantStatus: http.StatusBadRequest, wantCode: ingest.CodeBodyHashMalformed},
		{name: "a signature without its ed25519= gets 400 signature_malformed", edit: func(r *http.Request) {
			r.Header.Set(ingest.HeaderSignature, strings.TrimPrefix(r.Header.Get(ingest.HeaderSignature), "ed25519="))
		}, wantStatus: http.StatusBadRequest, wantCode: ingest.CodeSignatureMalformed},
		{name: "a time more than 300 s from the server's clock gets 401 clock_skew", edit: func(r *http.Request) {
			signAs(r, "d1", keyOf("d1"), "GET", list, "", t0.Add(-300*time.Second-time.Millisecond))
		}, wantStatus: http.StatusUnauthorized, wantCode: ingest.CodeClockSkew},
		{name: "a time 300 s from the server's clock is taken", edit: func(r *http.Request) {
			signAs(r, "d1", keyOf("d1"), "GET", list, "", t0.Add(300*time.Second))
		}, wantStatus: http.StatusOK},
		{name: "a body other than the one hashed gets 400 body_hash_mismatch", edit: func(r *http.Request) {
			signAs(r, "d1", keyOf("d1"), "GET", list, "{}", t0)
		}, wantStatus: http.StatusBadRequest, wantCode: ingest.CodeBodyHashMismatch},
		{name: "a signature by another key gets 401 signature_invalid", edit: func(r *http.Request) {
			signAs(r, "d1", keyOf("d2"), "GET", list, "", t0)
		}, wantStatus: http.StatusUnauthorized, wantCode: ingest.CodeSignatureInvalid},
		{name: "a signature over another request target gets 401 signature_invalid", edit: func(r *http.Request) {
			signAs(r, "d1", keyOf("d1"), "GET", SubmissionsPath+"?limit=2", "", t0)
		}, wantStatus: http.StatusUnauthorized, wantCode: ingest.CodeSignatureInvalid},
		{name: "a signature over another method gets 401 signature_invalid", edit: func(r *http.Request) {
			signAs(r, "d1", keyOf("d1"), "POST", list, "", t0)
		}, wantStatus: http.StatusUnauthorized, wantCode: ingest.CodeSignatureInvalid},
		{name: "an ingest signature doesn't pass for a submissions request", edit: func(r *http.Request) {
			sig := ed25519.Sign(keyOf("d1"), ingest.Message("d1", "1", r.Header.Get(ingest.HeaderTime), r.Header.Get(ingest.HeaderBodySHA256)))
			r.Header.Set(ingest.HeaderSignature, "ed25519="+base64.StdEncoding.EncodeToString(sig))
		}, wantStatus: http.StatusUnauthorized, wantCode: ingest.CodeSignatureInvalid},
	}
	for _, engine := range storetest.Engines() {
		for _, tt := range tests {
			t.Run(string(engine)+"/"+tt.name, func(t *testing.T) {
				h := newHarness(t, engine)
				r := signed(h, tt.edit)
				if tt.token != "" {
					r = h.request(tt.token, "GET", list, "")
				}
				rec := h.serve(r)
				want(t, rec, tt.wantStatus, tt.wantCode)
				if got := rec.Header().Get(ingest.HeaderTime); got != strconv.FormatInt(t0.UnixMilli(), 10) || rec.Header().Get("Cache-Control") != "no-store" {
					t.Errorf("X-Pairtime-Time %q, Cache-Control %q; want the server's clock and no-store on every answer", got, rec.Header().Get("Cache-Control"))
				}
				switch tt.wantCode {
				case ingest.CodeSignatureInvalid:
					if got := refusalBody(t, rec).DeviceKeyID; got != backup.KeyID(devices["ptd_laptop"].PublicKey) {
						t.Errorf("device_key_id %q, want the laptop's key's", got)
					}
				case ingest.CodeUnavailable:
					if got := rec.Header().Get("Retry-After"); got != "5" {
						t.Errorf("Retry-After %q, want 5", got)
					}
				}
			})
		}
		t.Run(string(engine)+"/a replay of a signed create within the 300 s gets the first answer and changes nothing", func(t *testing.T) {
			h := newHarness(t, engine)
			body := window("hard-mode", "req-1", start, end)
			first := h.request("ptd_laptop", "POST", SubmissionsPath, body)
			replay := h.request("ptd_laptop", "POST", SubmissionsPath, body)
			for k, vs := range first.Header {
				replay.Header[k] = vs
			}
			v := view(t, h.serve(first))
			before := h.rows()
			h.clock.set(t0.Add(299 * time.Second))
			rec := h.serve(replay)
			want(t, rec, http.StatusOK, "")
			if got := view(t, rec); got.ID != v.ID {
				t.Errorf("the replay answered %s, want %s", got.ID, v.ID)
			}
			if after := h.rows(); after != before {
				t.Errorf("the replay changed the rows from %s to %s", before, after)
			}
		})
		t.Run(string(engine)+"/a device past its rate gets 429 rate_limited with Retry-After, and a request that doesn't verify uses none of it", func(t *testing.T) {
			h := newHarness(t, engine, func(o *Options) { o.Rate = ingest.NewLimiter(1, 10*time.Second) })
			want(t, h.serve(signed(h, func(r *http.Request) { signAs(r, "d1", keyOf("d2"), "GET", list, "", t0) })), http.StatusUnauthorized, ingest.CodeSignatureInvalid)
			want(t, h.call("ptd_laptop", "GET", list, ""), http.StatusOK, "")
			rec := h.call("ptd_laptop", "GET", list, "")
			want(t, rec, http.StatusTooManyRequests, ingest.CodeRateLimited)
			if got := rec.Header().Get("Retry-After"); got != "10" {
				t.Errorf("Retry-After %q, want 10", got)
			}
			want(t, h.call("ptd_desktop", "GET", list, ""), http.StatusOK, "")
			want(t, h.call("sess:fresh", "GET", list, ""), http.StatusOK, "")
		})
	}
}
