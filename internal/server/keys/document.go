// SPDX-License-Identifier: AGPL-3.0-or-later

package keys

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// DocumentPath is where pairtimed serves the keys document.
const DocumentPath = "/.well-known/pairtime-keys.json"

// Format is the keys document's format member (docs/spec/keys-v1.md 2).
const Format = "pairtime-keys-v1"

// keyJSON is one key in the document.
type keyJSON struct {
	KeyID     string `json:"key_id"`
	PublicKey string `json:"public_key"`
	ValidFrom int64  `json:"valid_from"`
	// ValidTo is null while the key is current.
	ValidTo *int64 `json:"valid_to"`
}

// documentJSON is the keys document, in its members' order.
type documentJSON struct {
	Format     string    `json:"format"`
	ServerKeys []keyJSON `json:"server_keys"`
	// EvidenceKey is WP-SB2's slot for the current evidence key (DESIGN
	// 7.6), null until it fills it.
	EvidenceKey *keyJSON `json:"evidence_key"`
}

// Document returns the keys document for h (docs/spec/keys-v1.md 2):
// every server key, oldest first, with its id, its public key in hex and
// when it is valid, and the evidence key's slot, null for now.
func (h *History) Document() ([]byte, error) {
	doc := documentJSON{Format: Format, ServerKeys: []keyJSON{}}
	for _, k := range h.Server {
		j := keyJSON{KeyID: k.ID, PublicKey: hex.EncodeToString(k.Public), ValidFrom: k.ValidFrom}
		if k.ValidTo != Current {
			to := k.ValidTo
			j.ValidTo = &to
		}
		doc.ServerKeys = append(doc.ServerKeys, j)
	}
	b, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("keys: writing the keys document: %w", err)
	}
	return append(b, '\n'), nil
}

// Handler serves doc as GET DocumentPath does (docs/spec/keys-v1.md 1):
// JSON that a cache must check with the server before each reuse
// (Cache-Control: no-cache), so a client that fetches it before an upload
// gets the current key (DESIGN 7.6), and an ETag, so a check that finds it
// unchanged gets 304 Not Modified without the body.
func Handler(doc []byte) http.Handler {
	sum := sha256.Sum256(doc)
	etag := `"` + hex.EncodeToString(sum[:16]) + `"`
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Type", "application/json")
		h.Set("Cache-Control", "no-cache")
		h.Set("ETag", etag)
		http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(doc))
	})
}
