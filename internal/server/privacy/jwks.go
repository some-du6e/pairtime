// SPDX-License-Identifier: AGPL-3.0-or-later

package privacy

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

// DefaultJWKSURL is where the hub publishes its keys (DESIGN 8.9), which
// privacy_hub.jwks_url replaces only to point at a hub run for a test.
const DefaultJWKSURL = "https://yeet.hackclub.com/.well-known/jwks.json"

// How long fetched keys are trusted, and how soon a kid they lack may
// fetch them again (DESIGN 8.9; the hub's spec, "verifying it").
const (
	KeysFor        = 300 * time.Second
	RefetchAfter   = 30 * time.Second
	maxJWKSSize    = 256 << 10
	jwksFetchLimit = 10 * time.Second
)

// JWKS fetches the hub's public keys from URL and keeps them for KeysFor.
// A kid the keys lack fetches them again. No fetch, failed or not, comes
// sooner than RefetchAfter after the last one, so a key the hub has just
// added works within a retry, and a token naming a made-up kid, which
// anyone can send before its signature is checked, can't make the server
// fetch on every request. Keys it can't fetch fail with a 503
// jwks_unavailable, which the hub retries: it never trusts keys older
// than KeysFor.
type JWKS struct {
	URL string
	// Client fetches the keys. nil is a client that gives up after 10 s.
	Client *http.Client
	// Now is the clock; nil is time.Now.
	Now func() time.Time

	mu      sync.Mutex
	keys    map[string]*ecdsa.PublicKey
	fetched time.Time
	tried   time.Time
}

// Key returns the key kid names.
func (j *JWKS) Key(ctx context.Context, kid string) (*ecdsa.PublicKey, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	now := time.Now()
	if j.Now != nil {
		now = j.Now()
	}
	fresh := j.keys != nil && now.Sub(j.fetched) < KeysFor
	if k, ok := j.keys[kid]; ok && fresh {
		return k, nil
	}
	if !j.tried.IsZero() && now.Sub(j.tried) < RefetchAfter {
		// The hub may have added kid since, or its keys may come back:
		// the 503 makes it retry once the next fetch is allowed.
		if fresh {
			return nil, &Error{Status: http.StatusServiceUnavailable, Code: "unknown_key"}
		}
		return nil, &Error{Status: http.StatusServiceUnavailable, Code: "jwks_unavailable"}
	}
	j.tried = now
	keys, err := j.fetch(ctx)
	if err != nil {
		return nil, &Error{Status: http.StatusServiceUnavailable, Code: "jwks_unavailable"}
	}
	j.keys, j.fetched = keys, now
	if k, ok := keys[kid]; ok {
		return k, nil
	}
	return nil, badToken("unknown_key")
}

// fetch reads the key set: each P-256 signing key with a kid, a JWK of
// kty EC (RFC 7517, RFC 7518 6.2). Keys of other kinds and ones whose
// point isn't on the curve are left out, and a kid two keys share names
// neither. The fetch serves every request waiting on the keys, so the
// request that set it off can't cancel it, and it gives up after
// jwksFetchLimit.
func (j *JWKS) fetch(ctx context.Context) (map[string]*ecdsa.PublicKey, error) {
	client := j.Client
	if client == nil {
		client = &http.Client{Timeout: jwksFetchLimit, CheckRedirect: httpsRedirects}
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), jwksFetchLimit)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, j.URL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("privacy: the hub's keys answered %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxJWKSSize+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxJWKSSize {
		return nil, errors.New("privacy: the hub's key set is too large")
	}
	var set struct {
		Keys []struct {
			Kty string `json:"kty"`
			Crv string `json:"crv"`
			Kid string `json:"kid"`
			Alg string `json:"alg"`
			Use string `json:"use"`
			X   string `json:"x"`
			Y   string `json:"y"`
		} `json:"keys"`
	}
	if err := json.Unmarshal(body, &set); err != nil {
		return nil, fmt.Errorf("privacy: the hub's key set: %w", err)
	}
	keys := make(map[string]*ecdsa.PublicKey)
	shared := make(map[string]bool)
	for _, k := range set.Keys {
		if k.Kty != "EC" || k.Crv != "P-256" || k.Kid == "" || (k.Alg != "" && k.Alg != "ES256") || (k.Use != "" && k.Use != "sig") {
			continue
		}
		x, errX := b64.DecodeString(k.X)
		y, errY := b64.DecodeString(k.Y)
		if errX != nil || errY != nil || len(x) != 32 || len(y) != 32 {
			continue
		}
		pub, err := ecdsa.ParseUncompressedPublicKey(elliptic.P256(), append(append([]byte{4}, x...), y...))
		if err != nil {
			continue
		}
		if _, dup := keys[k.Kid]; dup || shared[k.Kid] {
			delete(keys, k.Kid)
			shared[k.Kid] = true
			continue
		}
		keys[k.Kid] = pub
	}
	return keys, nil
}

// httpsRedirects follows a redirect only to https and at most three
// times, so a redirect can't take the key set past config's https-only
// check on jwks_url.
func httpsRedirects(req *http.Request, via []*http.Request) error {
	if req.URL.Scheme != "https" {
		return errors.New("privacy: the hub's key set redirects away from https")
	}
	if len(via) >= 3 {
		return errors.New("privacy: the hub's key set redirects too many times")
	}
	return nil
}
