// SPDX-License-Identifier: AGPL-3.0-or-later

package privacy_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/server/privacy"
)

// jwk is key as a JWK of the hub's key set.
func jwk(t *testing.T, kid string, key *ecdsa.PublicKey) map[string]string {
	t.Helper()
	p, err := key.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	return map[string]string{"kty": "EC", "crv": "P-256", "kid": kid, "alg": "ES256", "use": "sig",
		"x": base64.RawURLEncoding.EncodeToString(p[1:33]), "y": base64.RawURLEncoding.EncodeToString(p[33:])}
}

// keySet serves a JWKS that a test changes, and counts its fetches.
type keySet struct {
	mu      sync.Mutex
	keys    []map[string]string
	status  int
	fetches int
}

func (k *keySet) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.fetches++
	if k.status != 0 && k.status != http.StatusOK {
		w.WriteHeader(k.status)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"keys": k.keys})
}

func (k *keySet) set(status int, keys ...map[string]string) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.status, k.keys = status, keys
}

func (k *keySet) count() int {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.fetches
}

func newKey(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func TestJWKS(t *testing.T) {
	a, b := newKey(t), newKey(t)
	type step struct {
		// after moves the clock on before the step.
		after time.Duration
		// serve, when set, changes what the key set answers first.
		serve func(k *keySet)
		kid   string
		// want is the key the step must get, or nil for an error of
		// wantStatus and wantCode.
		want        *ecdsa.PrivateKey
		wantStatus  int
		wantCode    string
		wantFetches int
		// wantCause is a part of the error's cause, which pairtimed logs.
		wantCause string
	}
	tests := []struct {
		name  string
		steps []step
	}{
		{name: "a kid the set publishes gives its key, and the set is fetched once for 300 s", steps: []step{
			{serve: func(k *keySet) { k.set(200, jwk(t, "a", &a.PublicKey)) }, kid: "a", want: a, wantFetches: 1},
			{after: 299 * time.Second, kid: "a", want: a, wantFetches: 1},
		}},
		{name: "after 300 s the set is fetched again", steps: []step{
			{serve: func(k *keySet) { k.set(200, jwk(t, "a", &a.PublicKey)) }, kid: "a", want: a, wantFetches: 1},
			{after: 300 * time.Second, kid: "a", want: a, wantFetches: 2},
		}},
		{name: "a kid the fresh set lacks fetches the set again once 30 s have passed since the last fetch, and one still missing is a 401 unknown_key", steps: []step{
			{serve: func(k *keySet) { k.set(200, jwk(t, "a", &a.PublicKey)) }, kid: "a", want: a, wantFetches: 1},
			{after: 31 * time.Second, kid: "b", wantStatus: 401, wantCode: "unknown_key", wantFetches: 2},
		}},
		{name: "another unknown kid within 30 s of a fetch is a 503 without a fetch, and the key the hub added is found once 30 s have passed", steps: []step{
			{serve: func(k *keySet) { k.set(200, jwk(t, "a", &a.PublicKey)) }, kid: "a", want: a, wantFetches: 1},
			{after: 10 * time.Second, serve: func(k *keySet) { k.set(200, jwk(t, "a", &a.PublicKey), jwk(t, "b", &b.PublicKey)) }, kid: "b", wantStatus: 503, wantCode: "unknown_key", wantFetches: 1},
			{after: 20 * time.Second, kid: "b", want: b, wantFetches: 2},
		}},
		{name: "a set that can't be fetched is a 503 jwks_unavailable, keys older than 300 s aren't used, and a failed fetch isn't tried again for 30 s", steps: []step{
			{serve: func(k *keySet) { k.set(200, jwk(t, "a", &a.PublicKey)) }, kid: "a", want: a, wantFetches: 1},
			{after: 301 * time.Second, serve: func(k *keySet) { k.set(500) }, kid: "a", wantStatus: 503, wantCode: "jwks_unavailable", wantFetches: 2, wantCause: "answered 500"},
			{after: 10 * time.Second, kid: "made-up", wantStatus: 503, wantCode: "jwks_unavailable", wantFetches: 2},
			{after: 20 * time.Second, serve: func(k *keySet) { k.set(200, jwk(t, "a", &a.PublicKey)) }, kid: "a", want: a, wantFetches: 3},
		}},
		{name: "keys of another curve or kind, a point off the curve and a kid two keys share are left out", steps: []step{
			{serve: func(k *keySet) {
				off := jwk(t, "off", &a.PublicKey)
				off["y"] = off["x"]
				curve := jwk(t, "curve", &a.PublicKey)
				curve["crv"] = "P-384"
				kind := jwk(t, "kind", &a.PublicKey)
				kind["kty"] = "RSA"
				enc := jwk(t, "enc", &a.PublicKey)
				enc["use"] = "enc"
				k.set(200, off, curve, kind, enc, jwk(t, "twice", &a.PublicKey), jwk(t, "twice", &b.PublicKey), jwk(t, "b", &b.PublicKey))
			}, kid: "b", want: b, wantFetches: 1},
			{after: 31 * time.Second, kid: "off", wantStatus: 401, wantCode: "unknown_key", wantFetches: 2},
			{after: 31 * time.Second, kid: "curve", wantStatus: 401, wantCode: "unknown_key", wantFetches: 3},
			{after: 31 * time.Second, kid: "kind", wantStatus: 401, wantCode: "unknown_key", wantFetches: 4},
			{after: 31 * time.Second, kid: "enc", wantStatus: 401, wantCode: "unknown_key", wantFetches: 5},
			{after: 31 * time.Second, kid: "twice", wantStatus: 401, wantCode: "unknown_key", wantFetches: 6},
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			set := &keySet{}
			srv := httptest.NewServer(set)
			t.Cleanup(srv.Close)
			now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
			j := &privacy.JWKS{URL: srv.URL, Transport: srv.Client().Transport, Now: func() time.Time { return now }}
			for i, s := range tt.steps {
				now = now.Add(s.after)
				if s.serve != nil {
					s.serve(set)
				}
				got, err := j.Key(context.Background(), s.kid)
				switch {
				case s.want != nil:
					if err != nil || !got.Equal(&s.want.PublicKey) {
						t.Fatalf("step %d: Key(%q) = %v, %v; want the set's key", i, s.kid, got, err)
					}
				default:
					var e *privacy.Error
					if !errors.As(err, &e) || e.Status != s.wantStatus || e.Code != s.wantCode {
						t.Fatalf("step %d: Key(%q) error = %v, want %d %s", i, s.kid, err, s.wantStatus, s.wantCode)
					}
					if s.wantCause != "" && (e.Err == nil || !strings.Contains(e.Err.Error(), s.wantCause)) {
						t.Fatalf("step %d: Key(%q)'s cause is %v, want one saying %q", i, s.kid, e.Err, s.wantCause)
					}
				}
				if n := set.count(); n != s.wantFetches {
					t.Fatalf("step %d: the set was fetched %d times, want %d", i, n, s.wantFetches)
				}
			}
		})
	}
}

// TestJWKSRedirects checks the client JWKS builds around its transport:
// it follows up to three redirects to https, and no fourth and none away
// from https, which config's https-only check on jwks_url relies on.
func TestJWKSRedirects(t *testing.T) {
	a := newKey(t)
	set := &keySet{}
	set.set(200, jwk(t, "a", &a.PublicKey))
	plain := httptest.NewServer(set)
	t.Cleanup(plain.Close)
	mux := http.NewServeMux()
	mux.Handle("/keys", set)
	mux.HandleFunc("/hop/{n}", func(w http.ResponseWriter, r *http.Request) {
		n, err := strconv.Atoi(r.PathValue("n"))
		if err != nil || n < 1 {
			http.NotFound(w, r)
			return
		}
		next := "/keys"
		if n > 1 {
			next = "/hop/" + strconv.Itoa(n-1)
		}
		http.Redirect(w, r, next, http.StatusFound)
	})
	mux.HandleFunc("/to-http", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, plain.URL+"/keys", http.StatusFound)
	})
	srv := httptest.NewTLSServer(mux)
	t.Cleanup(srv.Close)
	tests := []struct {
		name      string
		path      string
		wantKey   bool
		wantCause string
	}{
		{name: "three redirects to https are followed", path: "/hop/3", wantKey: true},
		{name: "a fourth redirect is refused", path: "/hop/4", wantCause: "too many"},
		{name: "a redirect away from https is refused", path: "/to-http", wantCause: "away from https"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			j := &privacy.JWKS{URL: srv.URL + tt.path, Transport: srv.Client().Transport}
			got, err := j.Key(context.Background(), "a")
			if tt.wantKey {
				if err != nil || !got.Equal(&a.PublicKey) {
					t.Fatalf("Key = %v, %v; want the set's key", got, err)
				}
				return
			}
			var e *privacy.Error
			if !errors.As(err, &e) || e.Code != "jwks_unavailable" || e.Err == nil || !strings.Contains(e.Err.Error(), tt.wantCause) {
				t.Fatalf("Key error = %v, want a 503 jwks_unavailable whose cause says %q", err, tt.wantCause)
			}
		})
	}
}
