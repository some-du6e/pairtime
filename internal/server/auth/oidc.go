// SPDX-License-Identifier: AGPL-3.0-or-later

package auth

import (
	"context"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"
)

// discoveryTTL is how long a discovery document is used before it is
// fetched again.
const discoveryTTL = 24 * time.Hour

// discoveryRetry is how long a failed fetch of the discovery document waits
// before the next try. Meanwhile a document fetched before stays in use,
// and without one sign-ins fail at once.
const discoveryRetry = time.Minute

// jwksTTL is how long signing keys are used before they are fetched again.
const jwksTTL = time.Hour

// jwksMinInterval is the least time between two fetches of the signing
// keys, so ID tokens naming unknown keys can't make pairtimed fetch the
// JWKS for each one.
const jwksMinInterval = time.Minute

// maxResponseBytes bounds every response read from HCA.
const maxResponseBytes = 1 << 20

// minRSABits is the smallest RSA signing key accepted.
const minRSABits = 2048

// The token endpoint authentication methods pairtimed can use (OIDC Core
// 9).
const (
	clientSecretBasic = "client_secret_basic"
	clientSecretPost  = "client_secret_post"
)

// provider is the part of a discovery document pairtimed reads (OIDC
// Discovery 3).
type provider struct {
	Issuer                            string   `json:"issuer"`
	AuthorizationEndpoint             string   `json:"authorization_endpoint"`
	TokenEndpoint                     string   `json:"token_endpoint"`
	JWKSURI                           string   `json:"jwks_uri"`
	ResponseTypesSupported            []string `json:"response_types_supported"`
	IDTokenSigningAlgValuesSupported  []string `json:"id_token_signing_alg_values_supported"`
	TokenEndpointAuthMethodsSupported []string `json:"token_endpoint_auth_methods_supported"`
}

// publicKey is one signing key from the JWKS.
type publicKey struct {
	kid string
	key *rsa.PublicKey
}

// oidcClient talks to HCA: discovery, signing keys and the token endpoint.
type oidcClient struct {
	issuer, clientID, clientSecret, redirectURI string
	http                                        *http.Client
	now                                         func() time.Time
	log                                         *slog.Logger

	// mu guards the discovery document. Nobody holds it during a fetch.
	mu        sync.Mutex
	prov      *provider
	provAt    time.Time     // when prov was fetched
	provErr   error         // why the last fetch failed, if it did
	provTried time.Time     // when that fetch ran
	provBusy  chan struct{} // closed when the fetch under way ends; nil without one

	// keysMu guards the signing keys, and is held while they are fetched.
	keysMu    sync.Mutex
	keys      []publicKey
	keysAt    time.Time // when keys were fetched
	keysTried time.Time // when a fetch was last tried
}

// provider returns HCA's discovery document, fetching it when it is
// missing or a day old. One request fetches at a time: meanwhile the others
// get the document fetched before, or wait for the fetch when there is
// none. While HCA is unreachable the document fetched before stays in use,
// and a failed fetch is tried again a minute later, not on every request.
func (c *oidcClient) provider(ctx context.Context) (*provider, error) {
	for {
		c.mu.Lock()
		now := c.now()
		switch {
		case c.prov != nil && now.Sub(c.provAt) < discoveryTTL:
			p := c.prov
			c.mu.Unlock()
			return p, nil
		case c.provBusy != nil && c.prov != nil:
			p := c.prov
			c.mu.Unlock()
			return p, nil
		case c.provBusy != nil:
			busy := c.provBusy
			c.mu.Unlock()
			select {
			case <-busy:
				continue
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		case c.provErr != nil && now.Sub(c.provTried) < discoveryRetry:
			p, err := c.prov, c.provErr
			c.mu.Unlock()
			if p != nil {
				return p, nil
			}
			return nil, err
		}
		busy := make(chan struct{})
		c.provBusy = busy
		c.mu.Unlock()

		p, err := c.discover(ctx)

		c.mu.Lock()
		c.provBusy = nil
		close(busy)
		switch {
		case err == nil:
			c.prov, c.provAt, c.provErr = p, c.now(), nil
		case ctx.Err() == nil:
			// A fetch its caller gave up on says nothing about HCA, so only
			// other failures wait out discoveryRetry.
			c.provErr, c.provTried = err, c.now()
			if c.prov != nil {
				c.log.Warn("HCA's discovery document couldn't be refreshed, so the copy fetched before stays in use; the next try is in a minute",
					"fetched_at", c.provAt.UTC().Format(time.RFC3339), "err", err)
			}
		}
		stale := c.prov
		c.mu.Unlock()
		if err != nil {
			if stale != nil {
				return stale, nil
			}
			return nil, err
		}
		return p, nil
	}
}

// discover fetches and checks the discovery document.
func (c *oidcClient) discover(ctx context.Context) (*provider, error) {
	// OIDC Discovery 4: a terminating slash is removed before the suffix.
	u := strings.TrimSuffix(c.issuer, "/") + "/.well-known/openid-configuration"
	var p provider
	if err := c.getJSON(ctx, u, &p); err != nil {
		return nil, err
	}
	if p.Issuer != c.issuer {
		return nil, fmt.Errorf("the discovery document's issuer is %q, not hca.issuer %q (OIDC Discovery 4.3)", p.Issuer, c.issuer)
	}
	for _, e := range []struct{ name, url string }{
		{"authorization_endpoint", p.AuthorizationEndpoint},
		{"token_endpoint", p.TokenEndpoint},
		{"jwks_uri", p.JWKSURI},
	} {
		if err := CheckURL(e.url); err != nil {
			return nil, fmt.Errorf("the discovery document's %s: %w", e.name, err)
		}
	}
	if !slices.Contains(p.ResponseTypesSupported, "code") {
		return nil, errors.New("the discovery document doesn't list response_type code")
	}
	if len(p.IDTokenSigningAlgValuesSupported) > 0 && !slices.Contains(p.IDTokenSigningAlgValuesSupported, "RS256") {
		return nil, errors.New("the discovery document doesn't list RS256 for ID tokens")
	}
	if _, err := authMethod(p.TokenEndpointAuthMethodsSupported); err != nil {
		return nil, err
	}
	return &p, nil
}

// authMethod picks how pairtimed authenticates at the token endpoint:
// client_secret_post when the provider lists it, as HCA's OIDC guide shows,
// and otherwise client_secret_basic, the default when the list is missing
// (OIDC Discovery 3).
func authMethod(supported []string) (string, error) {
	switch {
	case len(supported) == 0:
		return clientSecretBasic, nil
	case slices.Contains(supported, clientSecretPost):
		return clientSecretPost, nil
	case slices.Contains(supported, clientSecretBasic):
		return clientSecretBasic, nil
	}
	return "", errors.New("the token endpoint takes neither client_secret_post nor client_secret_basic")
}

// signingKeys returns the keys that may have signed a token naming kid.
// It fetches the JWKS when the cached keys are an hour old or none of them
// matches kid, which is how a rotated key arrives, but never twice within
// jwksMinInterval.
func (c *oidcClient) signingKeys(ctx context.Context, jwksURI, kid string) ([]*rsa.PublicKey, error) {
	c.keysMu.Lock()
	defer c.keysMu.Unlock()
	now := c.now()
	fresh := c.keys != nil && now.Sub(c.keysAt) < jwksTTL
	if fresh {
		if ks := matchKeys(c.keys, kid); len(ks) > 0 {
			return ks, nil
		}
	}
	var fetchErr error
	if c.keys == nil || now.Sub(c.keysTried) >= jwksMinInterval {
		c.keysTried = now
		keys, err := c.fetchKeys(ctx, jwksURI)
		if err == nil {
			c.keys, c.keysAt = keys, now
		}
		fetchErr = err
	}
	if c.keys == nil {
		return nil, fetchErr
	}
	ks := matchKeys(c.keys, kid)
	if len(ks) == 0 {
		return nil, fmt.Errorf("no key in HCA's JWKS has the ID token's kid %q", kid)
	}
	return ks, nil
}

// matchKeys returns the keys a token naming kid may be checked against:
// those with that kid and those with none, or every key when kid is empty.
func matchKeys(keys []publicKey, kid string) []*rsa.PublicKey {
	var out []*rsa.PublicKey
	for _, k := range keys {
		if kid == "" || k.kid == kid || k.kid == "" {
			out = append(out, k.key)
		}
	}
	return out
}

// jwk is one key of a JWKS (RFC 7517 4, RFC 7518 6.3.1).
type jwk struct {
	Kty string `json:"kty"`
	Use string `json:"use"`
	Alg string `json:"alg"`
	Kid string `json:"kid"`
	N   string `json:"n"`
	E   string `json:"e"`
}

// fetchKeys fetches the JWKS and keeps its RS256 signing keys.
func (c *oidcClient) fetchKeys(ctx context.Context, jwksURI string) ([]publicKey, error) {
	var set struct {
		Keys []jwk `json:"keys"`
	}
	if err := c.getJSON(ctx, jwksURI, &set); err != nil {
		return nil, err
	}
	return parseKeys(set.Keys)
}

// parseKeys keeps the keys usable for RS256 signatures: kty RSA, use sig or
// none, alg RS256 or none, and a modulus of at least 2048 bits.
func parseKeys(set []jwk) ([]publicKey, error) {
	var keys []publicKey
	var errs []error
	for _, k := range set {
		if k.Kty != "RSA" || (k.Use != "" && k.Use != "sig") || (k.Alg != "" && k.Alg != "RS256") {
			continue
		}
		pub, err := rsaKey(k.N, k.E)
		if err != nil {
			errs = append(errs, fmt.Errorf("key %q: %w", k.Kid, err))
			continue
		}
		keys = append(keys, publicKey{kid: k.Kid, key: pub})
	}
	if len(keys) == 0 {
		if len(errs) > 0 {
			return nil, fmt.Errorf("HCA's JWKS has no usable RS256 signing key: %w", errors.Join(errs...))
		}
		return nil, errors.New("HCA's JWKS has no RS256 signing key")
	}
	return keys, nil
}

// rsaKey decodes a JWK's base64url modulus and exponent.
func rsaKey(n64, e64 string) (*rsa.PublicKey, error) {
	nb, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(n64, "="))
	if err != nil {
		return nil, errors.New("n isn't base64url")
	}
	eb, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(e64, "="))
	if err != nil || len(eb) == 0 || len(eb) > 4 {
		return nil, errors.New("e isn't a base64url integer of 1 to 4 bytes")
	}
	var e uint64
	for _, b := range eb {
		e = e<<8 | uint64(b)
	}
	if e < 3 || e%2 == 0 || e > 1<<31-1 {
		return nil, errors.New("e must be an odd number from 3 to 2^31-1")
	}
	n := new(big.Int).SetBytes(nb)
	if n.BitLen() < minRSABits {
		return nil, fmt.Errorf("the modulus has %d bits, fewer than %d", n.BitLen(), minRSABits)
	}
	return &rsa.PublicKey{N: n, E: int(e)}, nil
}

// exchange redeems an authorization code at the token endpoint (OIDC Core
// 3.1.3.1) and returns the ID token. Errors name the endpoint's status and
// its error code, never the request or the response body.
func (c *oidcClient) exchange(ctx context.Context, p *provider, code string) (string, error) {
	method, err := authMethod(p.TokenEndpointAuthMethodsSupported)
	if err != nil {
		return "", err
	}
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", c.redirectURI)
	if method == clientSecretPost {
		form.Set("client_id", c.clientID)
		form.Set("client_secret", c.clientSecret)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.TokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("building the token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	if method == clientSecretBasic {
		// RFC 6749 2.3.1: both are form-encoded before Basic encoding.
		req.SetBasicAuth(url.QueryEscape(c.clientID), url.QueryEscape(c.clientSecret))
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("the token request failed: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return "", fmt.Errorf("reading the token response: %w", err)
	}
	if len(body) > maxResponseBytes {
		return "", fmt.Errorf("the token response is over %d bytes", maxResponseBytes)
	}
	var tr struct {
		IDToken string `json:"id_token"`
		Error   string `json:"error"`
	}
	jsonErr := json.Unmarshal(body, &tr)
	if resp.StatusCode != http.StatusOK {
		if jsonErr == nil && tr.Error != "" {
			return "", fmt.Errorf("the token endpoint answered %s with error %q", resp.Status, errorCode(tr.Error))
		}
		return "", fmt.Errorf("the token endpoint answered %s", resp.Status)
	}
	if jsonErr != nil {
		return "", errors.New("the token response isn't JSON")
	}
	if tr.IDToken == "" {
		return "", errors.New("the token response has no id_token, so the openid scope wasn't granted")
	}
	return tr.IDToken, nil
}

// getJSON fetches u and decodes its JSON body into v.
func (c *oidcClient) getJSON(ctx context.Context, u string, v any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return fmt.Errorf("GET %s: %w", u, err)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("GET %s: %w", u, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return fmt.Errorf("GET %s: %w", u, err)
	}
	if len(body) > maxResponseBytes {
		return fmt.Errorf("GET %s: the response is over %d bytes", u, maxResponseBytes)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: %s", u, resp.Status)
	}
	if err := json.Unmarshal(body, v); err != nil {
		return fmt.Errorf("GET %s: %w", u, err)
	}
	return nil
}

// errorCode keeps an OAuth error code fit for a log line: at most 64
// characters from [A-Za-z0-9_.-].
func errorCode(s string) string {
	var b strings.Builder
	for _, r := range s {
		if b.Len() == 64 {
			break
		}
		if r == '_' || r == '-' || r == '.' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}
