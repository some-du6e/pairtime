// SPDX-License-Identifier: AGPL-3.0-or-later

package device

import (
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/some-du6e/pairtime/internal/server/accounts"
)

// KeyLabel starts the message the device key signs when the app asks for
// a code (docs/spec/device-auth-v1.md section 2).
const KeyLabel = "pairtime-device-key-v1\n"

// KeyMessage is the message whose signature proves the app holds the
// private half of publicKey, the base64 string it sends, for device id.
func KeyMessage(id, publicKey string) []byte {
	return []byte(KeyLabel + id + "\n" + publicKey)
}

// codeReply is the body of a device authorization response (RFC 8628
// 3.2).
type codeReply struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete"`
	ExpiresIn               int    `json:"expires_in"`
	Interval                int    `json:"interval"`
	// SignupsOpen is false while sign-ups are closed, so only an account
	// that exists already can approve the code, and pairtime login says so
	// before it waits (D-device-row-chain-state).
	SignupsOpen bool `json:"signups_open"`
}

// tokenReply is the body of a successful token response (RFC 6749 5.1).
type tokenReply struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	Scope       string `json:"scope"`
	DeviceID    string `json:"device_id"`
	// PublicKey is the device key the code named, in base64: the token
	// works only with it (D-key-at-grant).
	PublicKey string `json:"public_key"`
	// Account names the account that approved the code, as the pages
	// show it, so pairtime login can say whose account it is linked to.
	Account string `json:"account"`
}

// deviceCode answers a device authorization request: POST
// /oauth/device/code (RFC 8628 3.1, 3.2).
func (s *Service) deviceCode(w http.ResponseWriter, r *http.Request) {
	f, problem := form(w, r)
	if problem != "" {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", problem)
		return
	}
	if f.Get("client_id") != ClientID {
		writeOAuthError(w, http.StatusBadRequest, "invalid_client", "This server only links the pairtime app, client_id "+ClientID+".")
		return
	}
	if !s.signIn.Enabled() {
		// Nobody could approve the code, so the app shouldn't wait for
		// one.
		writeOAuthError(w, http.StatusServiceUnavailable, "temporarily_unavailable",
			"Signing in isn't open on this pairtime server yet, so it can't link apps. pairtime keeps counting on your computer, and nothing is lost.")
		return
	}
	if sc := f.Get("scope"); sc != "" && !validScope(sc) {
		writeOAuthError(w, http.StatusBadRequest, "invalid_scope", "A device token's scopes are ingest and read:self.")
		return
	}
	req := Request{DeviceID: f.Get("device_id"), OS: f.Get("os"), ClientBuild: f.Get("client_build")}
	if problem := req.check(); problem != "" {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", problem)
		return
	}
	req.PublicKey, problem = deviceKey(req.DeviceID, f.Get("public_key"), f.Get("key_signature"))
	if problem != "" {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", problem)
		return
	}
	c, err := s.store.NewCode(r.Context(), req)
	if err != nil {
		s.log.Error("making a device code", "err", err)
		writeOAuthError(w, http.StatusInternalServerError, "server_error", "Something went wrong on this pairtime server. Try again in a minute.")
		return
	}
	verify := s.baseURL + PagePath
	writeJSON(w, http.StatusOK, codeReply{
		DeviceCode:              c.DeviceCode,
		UserCode:                c.UserCode,
		VerificationURI:         verify,
		VerificationURIComplete: verify + "?user_code=" + url.QueryEscape(c.UserCode),
		ExpiresIn:               int(CodeLifetime / time.Second),
		Interval:                int(c.Interval / time.Second),
		SignupsOpen:             s.signupsOpen(),
	})
}

// deviceKey checks the device key a code request names: publicKey must be
// a 32-byte Ed25519 public key in base64, and signature its signature of
// KeyMessage for the device id, which shows the app holds the private
// half. It returns the key, or what is wrong with it.
func deviceKey(id, publicKey, signature string) (ed25519.PublicKey, string) {
	pub, err := base64.StdEncoding.Strict().DecodeString(publicKey)
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return nil, "public_key must be the device's 32-byte Ed25519 public key, in base64."
	}
	sig, err := base64.StdEncoding.Strict().DecodeString(signature)
	if err != nil || len(sig) != ed25519.SignatureSize || !ed25519.Verify(pub, KeyMessage(id, publicKey), sig) {
		return nil, "key_signature must be the key's Ed25519 signature of the key message for this device_id, in base64."
	}
	return ed25519.PublicKey(pub), ""
}

// validScope reports whether every scope in sc is one a device token has.
func validScope(sc string) bool {
	for _, v := range strings.Fields(sc) {
		if v != "ingest" && v != "read:self" {
			return false
		}
	}
	return strings.TrimSpace(sc) != ""
}

// check returns what is wrong with r, or "".
func (r Request) check() string {
	switch {
	case !ValidDeviceID(r.DeviceID):
		return "device_id must be the app's device ID: 16 to 128 letters, digits, '_' and '-'."
	case r.OS != OSWindows && r.OS != OSMacOS && r.OS != OSLinux && r.OS != OSOther:
		return "os must be windows, macos, linux or other."
	case !validBuild(r.ClientBuild):
		return "client_build must be the app's version: 1 to 64 letters, digits, '.', '_', '+' and '-'."
	}
	return ""
}

// ValidDeviceID reports whether id can be a device id: an opaque key of
// pkg/event's alphabet, 16 to 128 characters long. The app's ids are 28,
// and the lower bound keeps a hand-made short id from claiming a name
// another app might draw.
func ValidDeviceID(id string) bool {
	if len(id) < 16 || len(id) > 128 {
		return false
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		if !('a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}

// validBuild reports whether b can be a pairtime version.
func validBuild(b string) bool {
	if b == "" || len(b) > 64 {
		return false
	}
	for i := 0; i < len(b); i++ {
		c := b[i]
		if !('a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9' || c == '.' || c == '_' || c == '+' || c == '-') {
			return false
		}
	}
	return true
}

// token answers a token request: POST /oauth/token. Only the device grant
// exists so far (RFC 8628 3.4, 3.5); the program API's grants come later
// (DESIGN 3.10).
func (s *Service) token(w http.ResponseWriter, r *http.Request) {
	f, problem := form(w, r)
	if problem != "" {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", problem)
		return
	}
	if gt := f.Get("grant_type"); gt != GrantType {
		if gt == "" {
			writeOAuthError(w, http.StatusBadRequest, "invalid_request", "grant_type is missing.")
			return
		}
		writeOAuthError(w, http.StatusBadRequest, "unsupported_grant_type", "This server's token endpoint takes the device grant, "+GrantType+".")
		return
	}
	if f.Get("client_id") != ClientID {
		writeOAuthError(w, http.StatusBadRequest, "invalid_client", "This server only links the pairtime app, client_id "+ClientID+".")
		return
	}
	code := f.Get("device_code")
	if code == "" || len(code) > 128 {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "device_code is missing.")
		return
	}
	tok, _, err := s.store.Poll(r.Context(), code)
	switch {
	case err == nil:
		s.log.Info("linked a device", "device", tok.DeviceID)
		writeJSON(w, http.StatusOK, tokenReply{AccessToken: tok.AccessToken, TokenType: "Bearer", Scope: Scope, DeviceID: tok.DeviceID,
			PublicKey: base64.StdEncoding.EncodeToString(tok.PublicKey),
			Account:   s.handle(&accounts.User{SlackID: tok.SlackID, HCASub: tok.HCASub})})
	case errors.Is(err, ErrPending):
		writeOAuthError(w, http.StatusBadRequest, "authorization_pending", "Nobody has approved this code yet.")
	case errors.Is(err, ErrSlowDown):
		writeOAuthError(w, http.StatusBadRequest, "slow_down", "Asking this often is too fast: wait 5 seconds longer between requests.")
	case errors.Is(err, ErrDenied):
		writeOAuthError(w, http.StatusBadRequest, "access_denied", "The code was turned down, so this app isn't linked.")
	case errors.Is(err, ErrTaken):
		writeOAuthError(w, http.StatusBadRequest, "device_taken",
			"The account that approved this code isn't the one this device ID belongs to on this server, so it wasn't linked. To link it to that account, sign in as a new device.")
	case errors.Is(err, ErrExpired):
		writeOAuthError(w, http.StatusBadRequest, "expired_token", "The code expired before anyone approved it. Start again.")
	case errors.Is(err, ErrInvalidGrant):
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "This device code isn't known, or was used already.")
	default:
		s.log.Error("answering a device token request", "err", err)
		writeOAuthError(w, http.StatusInternalServerError, "server_error", "Something went wrong on this pairtime server. Try again in a minute.")
	}
}
