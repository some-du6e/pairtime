// SPDX-License-Identifier: AGPL-3.0-or-later

package uploader

import (
	"bytes"
	"compress/gzip"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strconv"
)

// The paths of ingest-v1's three endpoints.
const (
	pathBatches = "/api/v1/ingest/batches"
	pathState   = "/api/v1/ingest/state"
	pathTime    = "/api/v1/time"
)

// signContext starts the signed message M (DESIGN 4.5; ingest-v1 2.2).
const signContext = "pairtime-ingest-v1\n"

// Signer signs with the device's Ed25519 key, the one it registered with
// its token (WP-SV3): RFC 8032's PureEdDSA, what crypto/ed25519.Sign
// computes.
type Signer func(message []byte) ([]byte, error)

// KeySigner returns the Signer of an Ed25519 private key.
func KeySigner(key ed25519.PrivateKey) Signer {
	return func(m []byte) ([]byte, error) {
		if len(key) != ed25519.PrivateKeySize {
			return nil, fmt.Errorf("uploader: an Ed25519 private key is %d bytes, got %d", ed25519.PrivateKeySize, len(key))
		}
		return ed25519.Sign(key, m), nil
	}
}

// message returns M = "pairtime-ingest-v1\n" || device_id || "\n" || req
// || "\n" || time || "\n" || hex(SHA-256(raw body)), with req and time in
// decimal ASCII and the hex lowercase (ingest-v1 2.2).
func message(deviceID string, req uint64, unixMS int64, bodyHash string) []byte {
	m := make([]byte, 0, len(signContext)+len(deviceID)+1+20+1+20+1+len(bodyHash))
	m = append(m, signContext...)
	m = append(m, deviceID...)
	m = append(m, '\n')
	m = strconv.AppendUint(m, req, 10)
	m = append(m, '\n')
	m = strconv.AppendInt(m, unixMS, 10)
	m = append(m, '\n')
	return append(m, bodyHash...)
}

// bodyHash returns hex(SHA-256(raw)), the X-Pairtime-Body-SHA256 value
// and the end of M.
func bodyHash(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// compress returns the raw body: the JSON in one gzip member with nothing
// after it (ingest-v1 2.1). Its header has no name and no time, so the
// same JSON always gives the same bytes.
func compress(js []byte) ([]byte, error) {
	var buf bytes.Buffer
	zw, err := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	if err != nil {
		return nil, err
	}
	if _, err := zw.Write(js); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// signed is one signing of a request: the counter, the time and the
// signature that go in its headers.
type signed struct {
	req       uint64
	unixMS    int64
	signature string // "ed25519=" and the base64
}

// sign signs the raw body whose hash is bodyHash as request req at
// unixMS.
func sign(s Signer, deviceID string, req uint64, unixMS int64, hash string) (signed, error) {
	if s == nil {
		return signed{}, errors.New("uploader: no device key to sign with")
	}
	sig, err := s(message(deviceID, req, unixMS, hash))
	if err != nil {
		return signed{}, fmt.Errorf("uploader: signing the request: %w", err)
	}
	if len(sig) != ed25519.SignatureSize {
		return signed{}, fmt.Errorf("uploader: an Ed25519 signature is %d bytes, got %d", ed25519.SignatureSize, len(sig))
	}
	return signed{req: req, unixMS: unixMS, signature: "ed25519=" + base64.StdEncoding.EncodeToString(sig)}, nil
}

// setHeaders sets the headers of a POST of the raw body whose hash is
// hash, signed as sg, with Idempotency-Key key (ingest-v1 2.1).
func setHeaders(h http.Header, c Credentials, key, hash string, sg signed, userAgent string) {
	h.Set("Authorization", "Bearer "+c.Token)
	h.Set("Idempotency-Key", key)
	h.Set("Content-Type", "application/json")
	h.Set("Content-Encoding", "gzip")
	h.Set("X-Pairtime-Device", c.DeviceID)
	h.Set("X-Pairtime-Req", strconv.FormatUint(sg.req, 10))
	h.Set("X-Pairtime-Time", strconv.FormatInt(sg.unixMS, 10))
	h.Set("X-Pairtime-Body-SHA256", hash)
	h.Set("X-Pairtime-Signature", sg.signature)
	h.Set("User-Agent", userAgent)
}
