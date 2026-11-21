// SPDX-License-Identifier: AGPL-3.0-or-later

package device

import (
	"encoding/base64"
	"net/http"
	"time"
)

// meReply is the body of GET /api/v1/devices/me.
type meReply struct {
	DeviceID    string    `json:"device_id"`
	OS          string    `json:"os"`
	ClientBuild string    `json:"client_build"`
	PublicKey   string    `json:"public_key"`
	LinkedAt    time.Time `json:"linked_at"`
	LastSeenAt  time.Time `json:"last_seen_at"`
}

// me describes the device of the request's token: GET /api/v1/devices/me.
func (s *Service) me(w http.ResponseWriter, r *http.Request) {
	d, ok := s.authenticate(w, r, false)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, meReply{
		DeviceID:    d.ID,
		OS:          d.OS,
		ClientBuild: d.ClientBuild,
		PublicKey:   base64.StdEncoding.EncodeToString(d.PublicKey),
		LinkedAt:    d.LinkedAt.UTC(),
		LastSeenAt:  d.LastSeenAt.UTC(),
	})
}

// removeMe removes the device of the request's token, which then stops
// working: DELETE /api/v1/devices/me, sent by pairtime logout. A device
// whose account Hack Club Auth has since marked ineligible can still
// remove itself, so its row doesn't outlive the notice's promise
// (D-device-account-binding).
func (s *Service) removeMe(w http.ResponseWriter, r *http.Request) {
	d, ok := s.authenticate(w, r, true)
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
