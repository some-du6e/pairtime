// SPDX-License-Identifier: AGPL-3.0-or-later

package submission

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

// TestNewR checks the freeze's r: 32 fresh bytes each time, and the
// commitment the lowercase hex of their SHA-256 (DESIGN 7.5 step 2).
func TestNewR(t *testing.T) {
	r1, c1, err := newR()
	if err != nil {
		t.Fatal(err)
	}
	r2, _, err := newR()
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		ok   bool
	}{
		{name: "r is 32 bytes", ok: len(r1) == 32},
		{name: "the commitment is the lowercase hex of r's SHA-256", ok: c1 == func() string { s := sha256.Sum256(r1); return hex.EncodeToString(s[:]) }()},
		{name: "two freezes draw two different r", ok: !bytes.Equal(r1, r2)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !tt.ok {
				t.Errorf("r %x, commitment %s", r1, c1)
			}
		})
	}
}
