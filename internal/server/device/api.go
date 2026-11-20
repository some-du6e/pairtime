// SPDX-License-Identifier: AGPL-3.0-or-later

package device

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"
)

// KeyLabel starts the message a device signs to register its key
// (docs/spec/device-auth-v1.md section 6).
const KeyLabel = "pairtime-device-key-v1\n"

// KeyMessage is the message whose signature proves the app holds the
// private half of publicKey, the base64 string it sends, for device id.
func KeyMessage(id, publicKey string) []byte {
	return []byte(KeyLabel + id + "\n" + publicKey)
}

// keyRequest is the body of POST /api/v1/devices/me/key.
type keyRequest struct {
	PublicKey string `json:"public_key"`
	Signature string `json:"signature"`
}

// keyReply is the body of its answer.
type keyReply struct {
	DeviceID  string `json:"device_id"`
	PublicKey string `json:"public_key"`
}

// meReply is the body of GET /api/v1/devices/me.
type meReply struct {
	DeviceID    string    `json:"device_id"`
	OS          string    `json:"os"`
	ClientBuild string    `json:"client_build"`
	PublicKey   *string   `json:"public_key"`
	CreatedAt   time.Time `json:"created_at"`
	LastSeenAt  time.Time `json:"last_seen_at"`
}

// registerKey registers the device's Ed25519 key, once: POST
// /api/v1/devices/me/key.
func (s *Service) registerKey(w http.ResponseWriter, r *http.Request) {
	d, ok := s.authenticate(w, r)
	if !ok {
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxFormBytes))
	if err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "The body is too long.")
		return
	}
	var req keyRequest
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil || dec.More() {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "The body must be JSON with public_key and signature.")
		return
	}
	pub, err := base64.StdEncoding.Strict().DecodeString(req.PublicKey)
	if err != nil || len(pub) != ed25519.PublicKeySize {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "public_key must be a 32-byte Ed25519 public key in base64.")
		return
	}
	sig, err := base64.StdEncoding.Strict().DecodeString(req.Signature)
	if err != nil || len(sig) != ed25519.SignatureSize || !ed25519.Verify(pub, KeyMessage(d.ID, req.PublicKey), sig) {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "signature must be the key's Ed25519 signature of the registration message, in base64.")
		return
	}
	created, err := s.store.RegisterKey(r.Context(), d, pub)
	switch {
	case errors.Is(err, ErrKeyRegistered):
		writeOAuthError(w, http.StatusConflict, "key_already_registered", "This device already has a different key. Run pairtime logout, then pairtime login, to link it with a new one.")
		return
	case errors.Is(err, ErrRevoked):
		w.Header().Set("WWW-Authenticate", challengeBadToken)
		writeOAuthError(w, http.StatusUnauthorized, "invalid_token", "This device token isn't valid, or its device was removed. Run pairtime login again.")
		return
	case err != nil:
		s.log.Error("registering a device key", "device", d.ID, "err", err)
		writeOAuthError(w, http.StatusInternalServerError, "server_error", "Something went wrong on this pairtime server. Try again in a minute.")
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
		s.log.Info("registered a device key", "device", d.ID)
	}
	writeJSON(w, status, keyReply{DeviceID: d.ID, PublicKey: base64.StdEncoding.EncodeToString(pub)})
}

// me describes the device of the request's token: GET /api/v1/devices/me.
func (s *Service) me(w http.ResponseWriter, r *http.Request) {
	d, ok := s.authenticate(w, r)
	if !ok {
		return
	}
	reply := meReply{
		DeviceID:    d.ID,
		OS:          d.OS,
		ClientBuild: d.ClientBuild,
		CreatedAt:   d.CreatedAt.UTC(),
		LastSeenAt:  d.LastSeenAt.UTC(),
	}
	if d.PublicKey != nil {
		k := base64.StdEncoding.EncodeToString(d.PublicKey)
		reply.PublicKey = &k
	}
	writeJSON(w, http.StatusOK, reply)
}

// removeMe removes the device of the request's token, which then stops
// working: DELETE /api/v1/devices/me, sent by pairtime logout.
func (s *Service) removeMe(w http.ResponseWriter, r *http.Request) {
	d, ok := s.authenticate(w, r)
	if !ok {
		return
	}
	if err := s.store.RemoveSelf(r.Context(), d); err != nil {
		s.log.Error("removing a device", "device", d.ID, "err", err)
		writeOAuthError(w, http.StatusInternalServerError, "server_error", "Something went wrong on this pairtime server. Try again in a minute.")
		return
	}
	s.log.Info("removed a device at its own request", "device", d.ID)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}
