// SPDX-License-Identifier: AGPL-3.0-or-later

package uploader

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/some-du6e/pairtime/pkg/ledger"
)

// responseMax bounds what the uploader reads of a response. A full answer
// to 2,000 batch roots is about 400 KiB.
const responseMax = 4 << 20

// requestTimeout bounds one request, from dialing to the last byte of its
// answer.
const requestTimeout = 60 * time.Second

// answer is a response as the uploader reads it.
type answer struct {
	status  int
	header  http.Header
	body    []byte
	refusal Refusal // a refusal's error code and detail, when the body is one
}

// do sends req and reads its answer, setting the clock's offset from the
// server time the answer carries. The error is a failure to get a whole
// answer.
func (u *Uploader) do(ctx context.Context, req *http.Request) (answer, error) {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	sent := u.now()
	resp, err := u.client.Do(req.WithContext(ctx))
	if err != nil {
		return answer{}, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, responseMax))
	got := u.now()
	if ms, ok := serverTime(resp.Header); ok {
		u.clock.observe(ms, sent, got)
	}
	if err != nil {
		return answer{}, err
	}
	a := answer{status: resp.StatusCode, header: resp.Header, body: body}
	if a.status != http.StatusOK {
		json.Unmarshal(body, &a.refusal) // a body that isn't one leaves it empty
	}
	return a, nil
}

// newRequest makes a request to the server's path, with the headers every
// one carries.
func (u *Uploader) newRequest(method string, c Credentials, path string, body []byte) (*http.Request, error) {
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, strings.TrimSuffix(c.Server, "/")+path, rd)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", u.userAgent)
	return req, nil
}

// getState reads GET /api/v1/ingest/state: the device's counter, the head
// of its chain on the server and the receipt key (ingest-v1 3).
func (u *Uploader) getState(ctx context.Context, c Credentials) (State, answer, error) {
	req, err := u.newRequest(http.MethodGet, c, pathState, nil)
	if err != nil {
		return State{}, answer{}, fmt.Errorf("%w: %v", errLocal, err)
	}
	// The device signs it, as it signs an upload (ingest-v1 3).
	t := u.clock.serverNow().UnixMilli()
	sig, err := signState(c.Sign, c.DeviceID, t)
	if err != nil {
		return State{}, answer{}, fmt.Errorf("%w: %v", errLocal, err)
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("X-Pairtime-Device", c.DeviceID)
	req.Header.Set("X-Pairtime-Time", strconv.FormatInt(t, 10))
	req.Header.Set("X-Pairtime-Signature", sig)
	a, err := u.do(ctx, req)
	if err != nil || a.status != http.StatusOK {
		return State{}, a, err
	}
	var st State
	if err := json.Unmarshal(a.body, &st); err != nil {
		return State{}, a, fmt.Errorf("uploader: the state answer doesn't read: %w", err)
	}
	return st, a, nil
}

// errNoKey is a receipt the uploader can't check, because it has no
// server key or the receipt names another.
var errNoKey = errors.New("uploader: the receipt names a server key the uploader doesn't have")

// checkReceipt checks a receipt for batch b with head under the server key
// (ingest-v1 2.8; ledger-v1 7) and returns it as the ledger stores it.
func checkReceipt(key *ServerKey, deviceID string, b uint64, head ledger.Hash, wr *WireReceipt) (ledger.Receipt, error) {
	if wr == nil {
		return ledger.Receipt{}, errors.New("uploader: an accepted batch root came without its receipt")
	}
	if key == nil || wr.KeyID != key.KeyID {
		return ledger.Receipt{}, errNoKey
	}
	pub, err := publicKey(key)
	if err != nil {
		return ledger.Receipt{}, err
	}
	sig, err := hex.DecodeString(wr.Sig)
	if err != nil || len(sig) != ledger.SignatureSize {
		return ledger.Receipt{}, errors.New("uploader: a receipt's signature isn't 128 hex digits")
	}
	r := ledger.Receipt{ReceivedAt: wr.ReceivedAt}
	copy(r.Signature[:], sig)
	if err := ledger.VerifyReceipt(pub, deviceID, b, head, r); err != nil {
		return ledger.Receipt{}, err
	}
	return r, nil
}

// publicKey decodes the server key and checks that its id is the first 16
// hex digits of its SHA-256 (ingest-v1 2.8).
func publicKey(k *ServerKey) (ed25519.PublicKey, error) {
	pub, err := hex.DecodeString(k.PublicKey)
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return nil, errors.New("uploader: the server key isn't 64 hex digits")
	}
	sum := sha256.Sum256(pub)
	if hex.EncodeToString(sum[:8]) != k.KeyID {
		return nil, errors.New("uploader: the server key's id doesn't match the key")
	}
	return pub, nil
}
