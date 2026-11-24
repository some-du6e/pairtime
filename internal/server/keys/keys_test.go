// SPDX-License-Identifier: AGPL-3.0-or-later

package keys_test

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/server/backup"
	"github.com/some-du6e/pairtime/internal/server/keys"
	"github.com/some-du6e/pairtime/internal/server/migrations"
	"github.com/some-du6e/pairtime/internal/server/store"
	"github.com/some-du6e/pairtime/internal/server/store/storetest"
	"github.com/some-du6e/pairtime/internal/server/writer"
)

// testKey returns the public half of the Ed25519 key whose seed is 32
// bytes of b.
func testKey(b byte) ed25519.PublicKey {
	return ed25519.NewKeyFromSeed(bytes.Repeat([]byte{b}, ed25519.SeedSize)).Public().(ed25519.PublicKey)
}

// The keys the tests record.
var (
	keyA = testKey(1)
	keyB = testKey(2)
	keyC = testKey(3)
)

// at returns the Unix ms of an RFC 3339 time.
func at(s string) int64 {
	v, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		panic(err)
	}
	return v.UnixMilli()
}

// fixture is a migrated database and its writer.
type fixture struct {
	db *store.DB
	w  *writer.Writer
}

func newFixture(t *testing.T, engine store.Engine) *fixture {
	t.Helper()
	ctx := context.Background()
	db := storetest.Open(t, engine)
	ms, err := migrations.Embedded()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := migrations.Apply(ctx, db, ms, time.Now); err != nil {
		t.Fatal(err)
	}
	w := writer.Start(db.Write, writer.Options{Window: time.Millisecond})
	t.Cleanup(w.Close)
	return &fixture{db: db, w: w}
}

func (f *fixture) exec(t *testing.T, q string, args ...any) {
	t.Helper()
	if err := f.w.Do(context.Background(), 1, func(ctx context.Context, tx writer.Tx) error {
		_, err := tx.ExecContext(ctx, q, args...)
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

// receipt stores batch b of device as a witness row whose receipt key pub
// signed at received.
func (f *fixture) receipt(t *testing.T, device string, b int, pub ed25519.PublicKey, received int64) {
	t.Helper()
	f.exec(t, `INSERT INTO witness_rows (device_id, b, account_id, n, root, head, prev, received_at_ms, receipt_sig, receipt_key_id) VALUES ($1, $2, 'u1', 1, $3, $4, $5, $6, $7, $8)`,
		device, b, strings.Repeat("ab", 32), strings.Repeat("cd", 32), strings.Repeat("ef", 32), received, strings.Repeat("01", 64), backup.KeyID(pub))
}

// recorded stands in for serve's key check: server_keys records pub, at
// t, as rotating to it would.
func (f *fixture) recorded(t *testing.T, pub ed25519.PublicKey, t0 int64) {
	t.Helper()
	f.exec(t, `INSERT INTO server_keys (id, server_key_id, pepper_fingerprint, recorded_at_ms) VALUES (1, $1, $2, $3)
ON CONFLICT (id) DO UPDATE SET server_key_id = excluded.server_key_id, recorded_at_ms = excluded.recorded_at_ms`, backup.KeyID(pub), "0123456789abcdef", t0)
}

func (f *fixture) record(pub ed25519.PublicKey, now int64) error {
	return f.w.Do(context.Background(), 2, func(ctx context.Context, tx writer.Tx) error {
		return keys.RecordServer(ctx, tx, pub, now)
	})
}

func key(pub ed25519.PublicKey, from, to int64) keys.ServerKey {
	return keys.ServerKey{ID: backup.KeyID(pub), Public: pub, ValidFrom: from, ValidTo: to}
}

// dayHead stores a day head the server signed with pub for the UTC day
// that holds t.
func (f *fixture) dayHead(t *testing.T, pub ed25519.PublicKey, t0 int64) {
	t.Helper()
	h := strings.Repeat("ab", 32)
	f.exec(t, `INSERT INTO day_heads (day, n, root, prev, head, signature, key_id, written_at_ms) VALUES ($1, 0, $2, $3, $4, $5, $6, $7)`,
		t0/86_400_000, h, h, h, strings.Repeat("01", 64), backup.KeyID(pub), t0)
}

func TestRecordServer(t *testing.T) {
	type start struct {
		// before stores what the server held when this start came.
		before func(t *testing.T, f *fixture)
		pub    ed25519.PublicKey
		now    int64
		// wantErr is part of the start's error, and wantIs an error it
		// wraps; neither means it records the key.
		wantErr string
		wantIs  error
	}
	firstA := start{before: func(t *testing.T, f *fixture) { f.recorded(t, keyA, at("2026-11-21T11:40:00Z")) }, pub: keyA, now: at("2026-11-24T03:00:00Z")}
	rotateB := start{before: func(t *testing.T, f *fixture) { f.recorded(t, keyB, at("2026-12-01T10:00:00Z")) }, pub: keyB, now: at("2026-12-01T10:00:05Z")}
	tests := []struct {
		name   string
		starts []start
		want   []keys.ServerKey
	}{
		{name: "a first key with no receipts is valid from the start of the UTC day server_keys first recorded it",
			starts: []start{firstA},
			want:   []keys.ServerKey{key(keyA, at("2026-11-21T00:00:00Z"), keys.Current)}},
		{name: "a first key that signed receipts before server_keys recorded it is valid from the start of its first receipt's UTC day",
			starts: []start{{before: func(t *testing.T, f *fixture) {
				f.receipt(t, "d1", 1, keyA, at("2026-11-20T23:59:59.999Z"))
				f.receipt(t, "d1", 2, keyA, at("2026-11-21T05:42:00.123Z"))
				f.recorded(t, keyA, at("2026-11-21T11:40:00Z"))
			}, pub: keyA, now: at("2026-11-24T03:00:00Z")}},
			want: []keys.ServerKey{key(keyA, at("2026-11-20T00:00:00Z"), keys.Current)}},
		{name: "receipts another key signed don't move a first key's validity",
			starts: []start{{before: func(t *testing.T, f *fixture) {
				f.receipt(t, "d1", 1, keyC, at("2026-11-19T10:00:00Z"))
				f.recorded(t, keyA, at("2026-11-21T11:40:00Z"))
			}, pub: keyA, now: at("2026-11-24T03:00:00Z")}},
			want: []keys.ServerKey{key(keyA, at("2026-11-21T00:00:00Z"), keys.Current)}},
		{name: "a first key that hasn't signed and isn't in server_keys is valid from the start of the UTC day it is recorded",
			starts: []start{{pub: keyA, now: at("2026-11-24T03:00:00Z")}},
			want:   []keys.ServerKey{key(keyA, at("2026-11-24T00:00:00Z"), keys.Current)}},
		{name: "recording the current key again changes nothing, even with an earlier receipt in witness_rows",
			starts: []start{firstA, {before: func(t *testing.T, f *fixture) {
				f.receipt(t, "d1", 1, keyA, at("2026-11-19T08:00:00Z"))
			}, pub: keyA, now: at("2026-11-25T03:00:00Z")}},
			want: []keys.ServerKey{key(keyA, at("2026-11-21T00:00:00Z"), keys.Current)}},
		{name: "a new key is valid from the start that records it, not from the start of its day or from server_keys, and the current key is retired the millisecond before",
			starts: []start{firstA, rotateB},
			want: []keys.ServerKey{
				key(keyA, at("2026-11-21T00:00:00Z"), at("2026-12-01T10:00:04.999Z")),
				key(keyB, at("2026-12-01T10:00:05Z"), keys.Current)}},
		{name: "a new key is valid from its first receipt when that came before the start that records it",
			starts: []start{firstA, {before: func(t *testing.T, f *fixture) {
				f.receipt(t, "d1", 1, keyA, at("2026-12-01T09:59:00Z"))
				f.receipt(t, "d1", 2, keyB, at("2026-12-01T10:00:00.250Z"))
			}, pub: keyB, now: at("2026-12-01T10:05:00Z")}},
			want: []keys.ServerKey{
				key(keyA, at("2026-11-21T00:00:00Z"), at("2026-12-01T10:00:00.249Z")),
				key(keyB, at("2026-12-01T10:00:00.250Z"), keys.Current)}},
		{name: "a new key is taken after the current key's last receipt and the last day it signed",
			starts: []start{firstA, {before: func(t *testing.T, f *fixture) {
				f.receipt(t, "d1", 1, keyA, at("2026-12-01T09:59:00Z"))
				f.dayHead(t, keyA, at("2026-11-30T12:00:00Z"))
			}, pub: keyB, now: at("2026-12-01T10:00:05Z")}},
			want: []keys.ServerKey{
				key(keyA, at("2026-11-21T00:00:00Z"), at("2026-12-01T10:00:04.999Z")),
				key(keyB, at("2026-12-01T10:00:05Z"), keys.Current)}},
		{name: "a third key retires only the current key and leaves the retired one as it was",
			starts: []start{firstA, rotateB, {before: func(t *testing.T, f *fixture) { f.recorded(t, keyC, at("2027-01-05T00:00:00Z")) }, pub: keyC, now: at("2027-01-05T00:00:01Z")}},
			want: []keys.ServerKey{
				key(keyA, at("2026-11-21T00:00:00Z"), at("2026-12-01T10:00:04.999Z")),
				key(keyB, at("2026-12-01T10:00:05Z"), at("2027-01-05T00:00:00.999Z")),
				key(keyC, at("2027-01-05T00:00:01Z"), keys.Current)}},
		{name: "a retired key is refused, and the history stays as it was",
			starts: []start{firstA, rotateB, {before: func(t *testing.T, f *fixture) { f.recorded(t, keyA, at("2027-01-05T00:00:00Z")) }, pub: keyA, now: at("2027-01-05T00:00:01Z"),
				wantErr: "stopped being valid at 2026-12-01T10:00:04.999Z", wantIs: keys.ErrRetired}},
			want: []keys.ServerKey{
				key(keyA, at("2026-11-21T00:00:00Z"), at("2026-12-01T10:00:04.999Z")),
				key(keyB, at("2026-12-01T10:00:05Z"), keys.Current)}},
		{name: "a new key whose first receipt came before the current key's last one is refused, since two keys would be valid at once",
			starts: []start{firstA, {before: func(t *testing.T, f *fixture) {
				f.receipt(t, "d1", 1, keyB, at("2026-11-21T12:00:00Z"))
				f.receipt(t, "d2", 1, keyA, at("2026-11-22T09:00:00Z"))
			}, pub: keyB, now: at("2026-12-01T10:00:05Z"), wantErr: "two keys would be valid at once"}},
			want: []keys.ServerKey{key(keyA, at("2026-11-21T00:00:00Z"), keys.Current)}},
		{name: "a new key is refused while the clock reads before the current key's last receipt",
			starts: []start{firstA, {before: func(t *testing.T, f *fixture) {
				f.receipt(t, "d1", 1, keyA, at("2026-12-02T00:00:00Z"))
			}, pub: keyB, now: at("2026-12-01T10:00:05Z"), wantErr: "Check the server's clock"}},
			want: []keys.ServerKey{key(keyA, at("2026-11-21T00:00:00Z"), keys.Current)}},
		{name: "a new key is refused before the end of a day whose head the current key signed",
			starts: []start{firstA, {before: func(t *testing.T, f *fixture) {
				f.dayHead(t, keyA, at("2026-11-30T12:00:00Z"))
			}, pub: keyB, now: at("2026-11-30T20:00:00Z"), wantErr: "signed until 2026-11-30T23:59:59.999Z"}},
			want: []keys.ServerKey{key(keyA, at("2026-11-21T00:00:00Z"), keys.Current)}},
		{name: "a key that isn't 32 bytes is refused",
			starts: []start{{pub: keyA[:31], now: at("2026-11-24T03:00:00Z"), wantErr: "a server key is 32 bytes"}}},
	}
	for _, engine := range storetest.Engines() {
		for _, tt := range tests {
			t.Run(string(engine)+"/"+tt.name, func(t *testing.T) {
				f := newFixture(t, engine)
				for i, s := range tt.starts {
					if s.before != nil {
						s.before(t, f)
					}
					err := f.record(s.pub, s.now)
					switch {
					case s.wantErr == "" && err != nil:
						t.Fatalf("start %d: %v", i+1, err)
					case s.wantErr != "" && (err == nil || !strings.Contains(err.Error(), s.wantErr)):
						t.Fatalf("start %d: error %v, want one saying %q", i+1, err, s.wantErr)
					case s.wantIs != nil && !errors.Is(err, s.wantIs):
						t.Fatalf("start %d: error %v doesn't wrap %v", i+1, err, s.wantIs)
					}
				}
				h, err := keys.Load(context.Background(), f.db.Read)
				if err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(h.Server, tt.want) {
					t.Errorf("the history holds\n%+v\nwant\n%+v", h.Server, tt.want)
				}
			})
		}
	}
}

func TestLoad(t *testing.T) {
	type row struct {
		pub      ed25519.PublicKey
		id       string
		from, to int64
		// open leaves valid_to NULL.
		open bool
	}
	tests := []struct {
		name    string
		rows    []row
		want    []keys.ServerKey
		wantErr string
	}{
		{name: "an empty history loads with no keys"},
		{name: "a history that holds together loads oldest first, a NULL valid_to as Current",
			rows: []row{{pub: keyB, from: 200, open: true}, {pub: keyA, from: 100, to: 199}},
			want: []keys.ServerKey{key(keyA, 100, 199), key(keyB, 200, keys.Current)}},
		{name: "two keys valid at the same millisecond are refused",
			rows:    []row{{pub: keyA, from: 100, to: 200}, {pub: keyB, from: 200, open: true}},
			wantErr: "both valid"},
		{name: "a validity that ends before it starts is refused",
			rows:    []row{{pub: keyA, from: 300, to: 200}},
			wantErr: "before it became valid"},
		{name: "a stored public key that isn't the key its id names is refused",
			rows:    []row{{pub: keyB, id: backup.KeyID(keyA), from: 100, open: true}},
			wantErr: "isn't that key"},
	}
	for _, engine := range storetest.Engines() {
		for _, tt := range tests {
			t.Run(string(engine)+"/"+tt.name, func(t *testing.T) {
				f := newFixture(t, engine)
				for _, r := range tt.rows {
					id := r.id
					if id == "" {
						id = backup.KeyID(r.pub)
					}
					var to any = r.to
					if r.open {
						to = nil
					}
					f.exec(t, `INSERT INTO key_history (kind, key_id, public_key, valid_from_ms, valid_to_ms, recorded_at_ms) VALUES ($1, $2, $3, $4, $5, $6)`,
						keys.KindServer, id, hex.EncodeToString(r.pub), r.from, to, int64(1))
				}
				h, err := keys.Load(context.Background(), f.db.Read)
				if tt.wantErr != "" {
					if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
						t.Fatalf("Load error %v, want one saying %q", err, tt.wantErr)
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(h.Server, tt.want) {
					t.Errorf("Load = %+v, want %+v", h.Server, tt.want)
				}
			})
		}
	}
}

func TestAt(t *testing.T) {
	h := &keys.History{Server: []keys.ServerKey{key(keyA, 100, 199), key(keyB, 200, keys.Current)}}
	tests := []struct {
		name string
		t    int64
		want ed25519.PublicKey
	}{
		{name: "no key is valid before the first key's valid_from", t: 99},
		{name: "a key is valid at its first millisecond", t: 100, want: keyA},
		{name: "a retired key is valid at its last millisecond", t: 199, want: keyA},
		{name: "the next key takes over the millisecond after", t: 200, want: keyB},
		{name: "the current key is valid with no end", t: 1 << 62, want: keyB},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			k, ok := h.At(tt.t)
			if ok != (tt.want != nil) || (ok && !k.Public.Equal(tt.want)) {
				t.Errorf("At(%d) = %s, %v; want key %s", tt.t, k.ID, ok, backup.KeyID(tt.want))
			}
		})
	}
}

func TestDocument(t *testing.T) {
	tests := []struct {
		name string
		h    keys.History
		// evidence are the private halves the ring holds.
		evidence []*ecdh.PrivateKey
		want     string
	}{
		{name: "with no keys the document lists none, and the evidence key's slot is null",
			want: "{\n  \"format\": \"pairtime-keys-v1\",\n  \"server_keys\": [],\n  \"evidence_key\": null\n}\n"},
		{name: "a retired key keeps its place, oldest first, with the last millisecond it was valid, and the current key's valid_to is null",
			h: keys.History{Server: []keys.ServerKey{key(keyA, 1795046400000, 1795600799999), key(keyB, 1795600800000, keys.Current)}},
			want: fmt.Sprintf(`{
  "format": "pairtime-keys-v1",
  "server_keys": [
    {
      "key_id": %q,
      "public_key": %q,
      "valid_from": 1795046400000,
      "valid_to": 1795600799999
    },
    {
      "key_id": %q,
      "public_key": %q,
      "valid_from": 1795600800000,
      "valid_to": null
    }
  ],
  "evidence_key": null
}
`, backup.KeyID(keyA), hex.EncodeToString(keyA), backup.KeyID(keyB), hex.EncodeToString(keyB))},
		{name: "the current evidence key fills its slot with the same four members, its public key the uncompressed point, and a retired one isn't listed",
			h: keys.History{
				Server:   []keys.ServerKey{key(keyA, 1795046400000, keys.Current)},
				Evidence: []keys.EvidenceKey{evidence(evA, 1795046400000, 1795600799999), evidence(evB, 1795600800000, keys.Current)},
			},
			evidence: []*ecdh.PrivateKey{evA, evB},
			want: fmt.Sprintf(`{
  "format": "pairtime-keys-v1",
  "server_keys": [
    {
      "key_id": %q,
      "public_key": %q,
      "valid_from": 1795046400000,
      "valid_to": null
    }
  ],
  "evidence_key": {
    "key_id": %q,
    "public_key": %q,
    "valid_from": 1795600800000,
    "valid_to": null
  }
}
`, backup.KeyID(keyA), hex.EncodeToString(keyA), backup.KeyID(evB.PublicKey().Bytes()), hex.EncodeToString(evB.PublicKey().Bytes()))},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ring, err := keys.NewRing(&tt.h, tt.evidence...)
			if err != nil {
				t.Fatal(err)
			}
			got, err := keys.Document(&tt.h, ring)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tt.want {
				t.Errorf("Document =\n%s\nwant\n%s", got, tt.want)
			}
		})
	}
}

func TestHandler(t *testing.T) {
	doc := []byte("{\"format\": \"pairtime-keys-v1\"}\n")
	sum := sha256.Sum256(doc)
	etag := `"` + hex.EncodeToString(sum[:16]) + `"`
	h := keys.Handler(doc)
	tests := []struct {
		name     string
		method   string
		header   map[string]string
		wantCode int
		wantBody string
	}{
		{name: "a GET gets the document", method: http.MethodGet, wantCode: http.StatusOK, wantBody: string(doc)},
		{name: "a GET whose If-None-Match names the document's ETag gets 304 and no body", method: http.MethodGet,
			header: map[string]string{"If-None-Match": etag}, wantCode: http.StatusNotModified},
		{name: "a GET whose If-None-Match names another ETag gets the document", method: http.MethodGet,
			header: map[string]string{"If-None-Match": `"0123"`}, wantCode: http.StatusOK, wantBody: string(doc)},
		{name: "a HEAD gets the headers and no body", method: http.MethodHead, wantCode: http.StatusOK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, keys.DocumentPath, nil)
			for k, v := range tt.header {
				req.Header.Set(k, v)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != tt.wantCode || rec.Body.String() != tt.wantBody {
				t.Errorf("%s = %d %q, want %d %q", tt.method, rec.Code, rec.Body.String(), tt.wantCode, tt.wantBody)
			}
			want := map[string]string{"Cache-Control": "no-cache", "ETag": etag}
			if tt.wantCode == http.StatusOK {
				want["Content-Type"] = "application/json"
			}
			for k, v := range want {
				if got := rec.Header().Get(k); got != v {
					t.Errorf("%s is %q, want %q", k, got, v)
				}
			}
		})
	}
}
