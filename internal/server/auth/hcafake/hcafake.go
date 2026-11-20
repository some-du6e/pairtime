// SPDX-License-Identifier: AGPL-3.0-or-later

// Package hcafake is a fake Hack Club Auth for tests, so no test ever calls
// auth.hackclub.com (docs/PLAN.md, ground rule 7).
//
// It copies the interface HCA documents, as docs/research/server-platform.md
// section 3.1 records it from hackclub/auth at 9133160bebcf: a
// doorkeeper-openid_connect provider whose discovery document lists only
// the scopes "openid profile phone birthdate" while HCA grants the
// community scopes "openid profile email name slack_id verification_status";
// endpoints under /oauth/; client_secret_basic and client_secret_post with
// Doorkeeper's Basic parsing, which doesn't form-decode the client id or
// secret; no PKCE; RS256 ID tokens that live 120 s and carry iss, sub
// ("ident!..."), aud (the client id), exp, iat, nonce, auth_time and, by
// scope, email and email_verified, slack_id, verification_status and
// ysws_eligible, leaving out claims without a value; prompt=login as a
// step-up that moves auth_time; and "idntk." access tokens.
//
// Tests choose who signs in (SetIdentity), change or re-sign the ID tokens
// it issues (SetHook), refuse the next authorization (SetDeny), rotate the
// signing key (RotateKey), and read what pairtimed sent.
package hcafake

import (
	"crypto"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// The client New registers.
const (
	ClientID     = "pairtime-test-client"
	ClientSecret = "pairtime-test-secret+/="
)

// CommunityScopes are the scopes HCA lets a community app request (SP 3.1).
var CommunityScopes = []string{"openid", "profile", "email", "name", "slack_id", "verification_status"}

// IDTokenLifetime is how long the ID tokens live: doorkeeper-openid_connect's
// default, which HCA keeps.
const IDTokenLifetime = 120 * time.Second

// codeLifetime is Doorkeeper's default authorization code lifetime.
const codeLifetime = 10 * time.Minute

// Identity is the person who signs in.
type Identity struct {
	Sub                string
	Email              string
	SlackID            string
	VerificationStatus string
	// YSWSEligible is left out of the token when nil.
	YSWSEligible *bool
}

// Verified is an identity HCA verified as eligible for YSWS programs.
func Verified() Identity {
	yes := true
	return Identity{
		Sub:                "ident!fakeVerified1",
		Email:              "verified@example.org",
		SlackID:            "U0FAKEVERIFIED",
		VerificationStatus: "verified",
		YSWSEligible:       &yes,
	}
}

// Token is an ID token about to be issued. A hook may change its header
// and claims, then sign it.
type Token struct {
	Header map[string]any
	Claims map[string]any
	key    *rsa.PrivateKey
}

// Sign signs the token with the fake's key, as HCA would.
func (t *Token) Sign() string {
	return t.SignWith(t.key)
}

// SignWith signs the token with RS256 under key.
func (t *Token) SignWith(key *rsa.PrivateKey) string {
	in := t.signingInput()
	sum := sha256.Sum256([]byte(in))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
	if err != nil {
		panic("hcafake: signing: " + err.Error())
	}
	return in + "." + b64(sig)
}

// Unsigned returns the token with alg "none" and an empty signature
// (RFC 7519 6).
func (t *Token) Unsigned() string {
	t.Header["alg"] = "none"
	return t.signingInput() + "."
}

// SignHS256 signs the token with HMAC-SHA256 under secret, the token a
// verifier that trusted the header's alg would check against the RSA
// public key's bytes.
func (t *Token) SignHS256(secret []byte) string {
	t.Header["alg"] = "HS256"
	in := t.signingInput()
	m := hmac.New(sha256.New, secret)
	m.Write([]byte(in))
	return in + "." + b64(m.Sum(nil))
}

// PublicKeyBytes is the fake's RSA modulus, the "secret" of SignHS256's
// classic confusion attack.
func (t *Token) PublicKeyBytes() []byte {
	return t.key.PublicKey.N.Bytes()
}

func (t *Token) signingInput() string {
	h, err := json.Marshal(t.Header)
	if err != nil {
		panic("hcafake: " + err.Error())
	}
	c, err := json.Marshal(t.Claims)
	if err != nil {
		panic("hcafake: " + err.Error())
	}
	return b64(h) + "." + b64(c)
}

// Server is a running fake HCA.
type Server struct {
	// URL is the issuer, the base of every endpoint.
	URL string
	srv *httptest.Server

	mu           sync.Mutex
	key          *rsa.PrivateKey
	kid          string
	oldKeys      []jwkKey
	redirectURIs []string
	identity     Identity
	hook         func(*Token) string
	deny         bool
	now          func() time.Time
	hcaSession   *hcaSession
	grants       map[string]*grant
	authorizes   []url.Values
	tokenCalls   int
	jwksCalls    int
	discovery    func(map[string]any)
}

// hcaSession is the browser's session at HCA.
type hcaSession struct {
	created, steppedUp time.Time
}

// authTime is HCA's auth_time_from_resource_owner: the later of the
// session's start and its last step-up.
func (h *hcaSession) authTime() time.Time {
	if h.steppedUp.After(h.created) {
		return h.steppedUp
	}
	return h.created
}

type grant struct {
	redirectURI string
	scopes      []string
	nonce       string
	identity    Identity
	authTime    time.Time
	issued      time.Time
	used        bool
}

type jwkKey struct {
	kid string
	pub *rsa.PublicKey
}

// New starts a fake HCA that signs in Verified() until told otherwise, and
// stops it when the test ends.
func New(t testing.TB) *Server {
	t.Helper()
	s := &Server{
		identity: Verified(),
		now:      time.Now,
		grants:   make(map[string]*grant),
	}
	s.key = Key(0)
	s.kid = thumbprint(&s.key.PublicKey)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/openid-configuration", s.serveDiscovery)
	mux.HandleFunc("GET /oauth/discovery/keys", s.serveJWKS)
	mux.HandleFunc("GET /oauth/authorize", s.serveAuthorize)
	mux.HandleFunc("POST /oauth/token", s.serveToken)
	s.srv = httptest.NewServer(mux)
	s.URL = s.srv.URL
	t.Cleanup(s.srv.Close)
	return s
}

// keys are RSA keys generated once per test binary, since 2048-bit keys
// are slow to make.
var keys struct {
	sync.Mutex
	list []*rsa.PrivateKey
}

// Key returns the i-th test key: Key(0) is a fresh fake's signing key, and
// the others stand for keys HCA never published, or for rotation.
func Key(i int) *rsa.PrivateKey {
	keys.Lock()
	defer keys.Unlock()
	for len(keys.list) <= i {
		k, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			panic("hcafake: " + err.Error())
		}
		keys.list = append(keys.list, k)
	}
	return keys.list[i]
}

// AddRedirectURI registers a redirect URI for the client, as its developer
// would on HCA's Developer Apps page.
func (s *Server) AddRedirectURI(u string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.redirectURIs = append(s.redirectURIs, u)
}

// SetIdentity chooses who signs in at the next authorizations.
func (s *Server) SetIdentity(id Identity) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.identity = id
}

// SetHook makes every ID token from now on the hook's return value. Nil
// goes back to signing tokens unchanged.
func (s *Server) SetHook(h func(*Token) string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hook = h
}

// SetDeny makes the next authorizations end in error=access_denied, as when
// the person cancels at HCA.
func (s *Server) SetDeny(deny bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deny = deny
}

// SetNow replaces the fake's clock.
func (s *Server) SetNow(now func() time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.now = now
}

// SetDiscovery lets f edit the discovery document before it is served.
func (s *Server) SetDiscovery(f func(doc map[string]any)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.discovery = f
}

// RotateKey signs from now on with key, under a new kid, and keeps the old
// key in the JWKS beside it.
func (s *Server) RotateKey(key *rsa.PrivateKey) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.oldKeys = append(s.oldKeys, jwkKey{kid: s.kid, pub: &s.key.PublicKey})
	s.key = key
	s.kid = thumbprint(&key.PublicKey)
}

// Authorizes returns the query of every authorization request so far.
func (s *Server) Authorizes() []url.Values {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]url.Values, len(s.authorizes))
	for i, q := range s.authorizes {
		out[i] = cloneValues(q)
	}
	return out
}

// TokenCalls is how many token requests the fake has had.
func (s *Server) TokenCalls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.tokenCalls
}

// JWKSCalls is how many times the JWKS was fetched.
func (s *Server) JWKSCalls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.jwksCalls
}

func (s *Server) serveDiscovery(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	u := s.URL
	doc := map[string]any{
		"issuer":                                u,
		"authorization_endpoint":                u + "/oauth/authorize",
		"token_endpoint":                        u + "/oauth/token",
		"revocation_endpoint":                   u + "/oauth/revoke",
		"introspection_endpoint":                u + "/oauth/introspect",
		"userinfo_endpoint":                     u + "/oauth/userinfo",
		"jwks_uri":                              u + "/oauth/discovery/keys",
		"scopes_supported":                      []string{"openid", "profile", "phone", "birthdate"},
		"response_types_supported":              []string{"code"},
		"response_modes_supported":              []string{"query", "fragment", "form_post"},
		"grant_types_supported":                 []string{"authorization_code", "client_credentials", "refresh_token"},
		"token_endpoint_auth_methods_supported": []string{"client_secret_basic", "client_secret_post"},
		"subject_types_supported":               []string{"public"},
		"id_token_signing_alg_values_supported": []string{"RS256"},
		"claim_types_supported":                 []string{"normal"},
		"claims_supported": []string{"iss", "sub", "aud", "exp", "iat", "email", "email_verified",
			"phone_number", "phone_number_verified", "name", "given_name", "family_name", "nickname",
			"updated_at", "birthdate", "address", "slack_id", "verification_status", "ysws_eligible", "groups"},
	}
	if s.discovery != nil {
		s.discovery(doc)
	}
	writeJSON(w, http.StatusOK, doc)
}

func (s *Server) serveJWKS(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.jwksCalls++
	list := []any{jwk(s.kid, &s.key.PublicKey)}
	for _, k := range s.oldKeys {
		list = append(list, jwk(k.kid, k.pub))
	}
	writeJSON(w, http.StatusOK, map[string]any{"keys": list})
}

func (s *Server) serveAuthorize(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	q := r.URL.Query()
	s.authorizes = append(s.authorizes, cloneValues(q))
	// Doorkeeper shows an error page, without redirecting, for an unknown
	// client or a redirect URI the client didn't register.
	if q.Get("client_id") != ClientID {
		http.Error(w, "unknown client", http.StatusUnauthorized)
		return
	}
	redirect := q.Get("redirect_uri")
	if !slices.Contains(s.redirectURIs, redirect) {
		http.Error(w, "the redirect uri is not registered for this client", http.StatusBadRequest)
		return
	}
	back := func(params url.Values) {
		u, err := url.Parse(redirect)
		if err != nil {
			http.Error(w, "bad redirect uri", http.StatusBadRequest)
			return
		}
		rq := u.Query()
		for k, v := range params {
			rq[k] = v
		}
		if st := q.Get("state"); st != "" {
			rq.Set("state", st)
		}
		u.RawQuery = rq.Encode()
		http.Redirect(w, r, u.String(), http.StatusFound)
	}
	if q.Get("response_type") != "code" {
		back(url.Values{"error": {"unsupported_response_type"}})
		return
	}
	scopes := strings.Fields(q.Get("scope"))
	if !slices.Contains(scopes, "openid") {
		back(url.Values{"error": {"invalid_scope"}})
		return
	}
	for _, sc := range scopes {
		if !slices.Contains(CommunityScopes, sc) {
			back(url.Values{"error": {"invalid_scope"}})
			return
		}
	}
	if s.deny {
		back(url.Values{"error": {"access_denied"}})
		return
	}
	now := s.now()
	if s.hcaSession == nil {
		s.hcaSession = &hcaSession{created: now}
	}
	if slices.Contains(strings.Fields(q.Get("prompt")), "login") {
		s.hcaSession.steppedUp = now
	}
	code := random()
	s.grants[code] = &grant{
		redirectURI: redirect,
		scopes:      scopes,
		nonce:       q.Get("nonce"),
		identity:    s.identity,
		authTime:    s.hcaSession.authTime(),
		issued:      now,
	}
	back(url.Values{"code": {code}})
}

func (s *Server) serveToken(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tokenCalls++
	if err := r.ParseForm(); err != nil {
		tokenError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	// Doorkeeper's from_basic splits the decoded header on its first colon
	// and doesn't form-decode either half, and r.BasicAuth doesn't either.
	id, secret, ok := r.BasicAuth()
	if !ok {
		id, secret = r.PostForm.Get("client_id"), r.PostForm.Get("client_secret")
	}
	if id != ClientID || secret != ClientSecret {
		tokenError(w, http.StatusUnauthorized, "invalid_client")
		return
	}
	if r.PostForm.Get("grant_type") != "authorization_code" {
		tokenError(w, http.StatusBadRequest, "unsupported_grant_type")
		return
	}
	now := s.now()
	g := s.grants[r.PostForm.Get("code")]
	if g == nil || g.used || now.Sub(g.issued) > codeLifetime || g.redirectURI != r.PostForm.Get("redirect_uri") {
		tokenError(w, http.StatusBadRequest, "invalid_grant")
		return
	}
	g.used = true
	tok := &Token{
		Header: map[string]any{"typ": "JWT", "alg": "RS256", "kid": s.kid},
		Claims: s.claims(g, now),
		key:    s.key,
	}
	var idToken string
	if s.hook != nil {
		idToken = s.hook(tok)
	} else {
		idToken = tok.Sign()
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"access_token":  "idntk." + random(),
		"token_type":    "Bearer",
		"expires_in":    15552000,
		"refresh_token": random(),
		"scope":         strings.Join(g.scopes, " "),
		"created_at":    now.Unix(),
		"id_token":      idToken,
	})
}

// claims are the ID token claims HCA's doorkeeper_openid_connect.rb gives
// the granted scopes, without the ones that have no value.
func (s *Server) claims(g *grant, now time.Time) map[string]any {
	c := map[string]any{
		"iss": s.URL,
		"sub": g.identity.Sub,
		"aud": ClientID,
		"exp": now.Add(IDTokenLifetime).Unix(),
		"iat": now.Unix(),
	}
	if g.nonce != "" {
		c["nonce"] = g.nonce
	}
	if !g.authTime.IsZero() {
		c["auth_time"] = g.authTime.Unix()
	}
	if slices.Contains(g.scopes, "email") && g.identity.Email != "" {
		c["email"] = g.identity.Email
		c["email_verified"] = true
	}
	if slices.Contains(g.scopes, "slack_id") && g.identity.SlackID != "" {
		c["slack_id"] = g.identity.SlackID
	}
	if slices.Contains(g.scopes, "verification_status") {
		if g.identity.VerificationStatus != "" {
			c["verification_status"] = g.identity.VerificationStatus
		}
		if g.identity.YSWSEligible != nil {
			c["ysws_eligible"] = *g.identity.YSWSEligible
		}
	}
	return c
}

func jwk(kid string, pub *rsa.PublicKey) map[string]any {
	return map[string]any{
		"kty": "RSA",
		"kid": kid,
		"use": "sig",
		"alg": "RS256",
		"n":   b64(pub.N.Bytes()),
		"e":   b64(big.NewInt(int64(pub.E)).Bytes()),
	}
}

// thumbprint is the key's RFC 7638 thumbprint, which
// doorkeeper-openid_connect uses as its kid.
func thumbprint(pub *rsa.PublicKey) string {
	j := `{"e":"` + b64(big.NewInt(int64(pub.E)).Bytes()) + `","kty":"RSA","n":"` + b64(pub.N.Bytes()) + `"}`
	sum := sha256.Sum256([]byte(j))
	return b64(sum[:])
}

func tokenError(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, map[string]string{"error": code})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func random() string {
	b := make([]byte, 32)
	rand.Read(b)
	return b64(b)
}

func b64(b []byte) string {
	return base64.RawURLEncoding.EncodeToString(b)
}

func cloneValues(v url.Values) url.Values {
	out := make(url.Values, len(v))
	for k, vs := range v {
		out[k] = slices.Clone(vs)
	}
	return out
}
