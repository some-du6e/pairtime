// SPDX-License-Identifier: AGPL-3.0-or-later

package keys_test

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/some-du6e/pairtime/internal/server/backup"
	"github.com/some-du6e/pairtime/internal/server/keys"
	"github.com/some-du6e/pairtime/internal/server/store/storetest"
	"github.com/some-du6e/pairtime/internal/server/writer"
	"github.com/some-du6e/pairtime/pkg/seal"
)

// evidenceKey returns the P-256 key whose scalar is 32 bytes of b.
func evidenceKey(b byte) *ecdh.PrivateKey {
	k, err := ecdh.P256().NewPrivateKey(bytes.Repeat([]byte{b}, 32))
	if err != nil {
		panic(err)
	}
	return k
}

// The evidence keys the tests record.
var (
	evA = evidenceKey(1)
	evB = evidenceKey(2)
	evC = evidenceKey(3)
)

// evidence is k's entry in a history, valid from from to to.
func evidence(k *ecdh.PrivateKey, from, to int64) keys.EvidenceKey {
	return keys.EvidenceKey{ID: seal.KeyID(k.PublicKey()), Public: k.PublicKey(), ValidFrom: from, ValidTo: to}
}

// plainKey is an evidence key with its public key as its point in hex,
// so keys compare with reflect.DeepEqual and not through crypto/ecdh's
// insides.
type plainKey struct {
	ID, Public         string
	ValidFrom, ValidTo int64
}

func plain(ks []keys.EvidenceKey) []plainKey {
	var out []plainKey
	for _, k := range ks {
		out = append(out, plainKey{ID: k.ID, Public: hex.EncodeToString(k.Public.Bytes()), ValidFrom: k.ValidFrom, ValidTo: k.ValidTo})
	}
	return out
}

// evidenceRow stores k in the history as an evidence key valid from from
// to to, or with valid_to NULL when to is keys.Current.
func (f *fixture) evidenceRow(t *testing.T, k *ecdh.PrivateKey, from, to int64) {
	t.Helper()
	var end any = to
	if to == keys.Current {
		end = nil
	}
	f.exec(t, `INSERT INTO key_history (kind, key_id, public_key, valid_from_ms, valid_to_ms, recorded_at_ms) VALUES ($1, $2, $3, $4, $5, $6)`,
		keys.KindEvidence, seal.KeyID(k.PublicKey()), hex.EncodeToString(k.PublicKey().Bytes()), from, end, from)
}

func (f *fixture) recordEvidence(k *ecdh.PrivateKey, now int64) error {
	return f.w.Do(context.Background(), 1, func(ctx context.Context, tx writer.Tx) error {
		return keys.RecordEvidence(ctx, tx, k.PublicKey(), now)
	})
}

func TestEvidenceKeyID(t *testing.T) {
	tests := []struct {
		name string
		k    *ecdh.PrivateKey
	}{
		{name: "an evidence key's id is backup.KeyID of its point, the id pairtimed keys show prints", k: evA},
		{name: "another key's id is also backup.KeyID of its point", k: evB},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got, want := seal.KeyID(tt.k.PublicKey()), backup.KeyID(tt.k.PublicKey().Bytes()); got != want {
				t.Errorf("seal.KeyID = %s, backup.KeyID = %s", got, want)
			}
		})
	}
}

func TestRecordEvidence(t *testing.T) {
	t0, t1, t2 := at("2026-11-24T10:00:00Z"), at("2026-11-25T10:00:00Z"), at("2026-11-26T10:00:00Z")
	type row struct {
		k        *ecdh.PrivateKey
		from, to int64
	}
	tests := []struct {
		name string
		// before is the history's evidence keys before the call.
		before  []row
		pub     *ecdh.PrivateKey
		now     int64
		want    []keys.EvidenceKey
		wantIs  error
		wantErr string
	}{
		{name: "a history without an evidence key records the key as current from now", pub: evA, now: t1,
			want: []keys.EvidenceKey{evidence(evA, t1, keys.Current)}},
		{name: "the key the history holds as current changes nothing", before: []row{{evA, t0, keys.Current}}, pub: evA, now: t1,
			want: []keys.EvidenceKey{evidence(evA, t0, keys.Current)}},
		{name: "another key while one is current is refused, naming the key the stored blobs are sealed to",
			before: []row{{evA, t0, keys.Current}}, pub: evB, now: t1,
			wantIs: keys.ErrOtherEvidenceKey, wantErr: "records evidence key " + seal.KeyID(evA.PublicKey()) + " as current"},
		{name: "a retired key is refused, since pairtimed never serves with a retired evidence key again",
			before: []row{{evA, t0, t1 - 1}, {evB, t1, keys.Current}}, pub: evA, now: t2,
			wantIs: keys.ErrOtherEvidenceKey, wantErr: "stopped being valid at 2026-11-25T09:59:59.999Z"},
		{name: "a new key in a history whose keys are all retired takes over from now",
			before: []row{{evA, t0, t1}}, pub: evB, now: t2,
			want: []keys.EvidenceKey{evidence(evA, t0, t1), evidence(evB, t2, keys.Current)}},
		{name: "a new key that would be valid before a retired key's end is refused, as a clock set back would make it",
			before: []row{{evA, t0, t2}}, pub: evB, now: t1, wantErr: "two keys would be valid at once"},
	}
	for _, engine := range storetest.Engines() {
		for _, tt := range tests {
			t.Run(string(engine)+"/"+tt.name, func(t *testing.T) {
				f := newFixture(t, engine)
				var before []keys.EvidenceKey
				for _, r := range tt.before {
					f.evidenceRow(t, r.k, r.from, r.to)
					before = append(before, evidence(r.k, r.from, r.to))
				}
				err := f.recordEvidence(tt.pub, tt.now)
				want := tt.want
				if tt.wantIs != nil || tt.wantErr != "" {
					if err == nil || (tt.wantIs != nil && !errors.Is(err, tt.wantIs)) || !strings.Contains(err.Error(), tt.wantErr) {
						t.Fatalf("RecordEvidence error %v, want %v saying %q", err, tt.wantIs, tt.wantErr)
					}
					want = before
				} else if err != nil {
					t.Fatal(err)
				}
				h, err := keys.Load(context.Background(), f.db.Read)
				if err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(plain(h.Evidence), plain(want)) || len(h.Server) != 0 {
					t.Errorf("the history holds evidence keys %+v and server keys %+v, want %+v and none", h.Evidence, h.Server, want)
				}
			})
		}
	}
}

// TestKinds checks that the server keys and the evidence keys share
// key_history without touching each other: each kind has its own current
// key, and each loader reads only its own kind.
func TestKinds(t *testing.T) {
	t0, t1, t2 := at("2026-11-24T10:00:00Z"), at("2026-11-25T10:00:00Z"), at("2026-11-26T10:00:00Z")
	tests := []struct {
		name string
		run  func(t *testing.T, f *fixture)
		// wantServer and wantEvidence are the history afterwards.
		wantServer   []keys.ServerKey
		wantEvidence []keys.EvidenceKey
	}{
		{name: "a server key and an evidence key are current at once, one of each kind",
			run: func(t *testing.T, f *fixture) {
				if err := f.record(keyA, t0); err != nil {
					t.Fatal(err)
				}
				if err := f.recordEvidence(evA, t0); err != nil {
					t.Fatal(err)
				}
			},
			wantServer:   []keys.ServerKey{key(keyA, t0-t0%86_400_000, keys.Current)},
			wantEvidence: []keys.EvidenceKey{evidence(evA, t0, keys.Current)}},
		{name: "a new server key retires the server key and leaves the evidence key current",
			run: func(t *testing.T, f *fixture) {
				if err := f.recordEvidence(evA, t0); err != nil {
					t.Fatal(err)
				}
				if err := f.record(keyA, t0); err != nil {
					t.Fatal(err)
				}
				if err := f.record(keyB, t2); err != nil {
					t.Fatal(err)
				}
			},
			wantServer:   []keys.ServerKey{key(keyA, t0-t0%86_400_000, t2-1), key(keyB, t2, keys.Current)},
			wantEvidence: []keys.EvidenceKey{evidence(evA, t0, keys.Current)}},
		{name: "recording the evidence key leaves a retired server key's validity as it was",
			run: func(t *testing.T, f *fixture) {
				if err := f.record(keyA, t0); err != nil {
					t.Fatal(err)
				}
				if err := f.record(keyB, t1); err != nil {
					t.Fatal(err)
				}
				if err := f.recordEvidence(evA, t2); err != nil {
					t.Fatal(err)
				}
			},
			wantServer:   []keys.ServerKey{key(keyA, t0-t0%86_400_000, t1-1), key(keyB, t1, keys.Current)},
			wantEvidence: []keys.EvidenceKey{evidence(evA, t2, keys.Current)}},
		{name: "two current evidence keys, stored behind RecordEvidence's back, fail Load, beside a current server key",
			run: func(t *testing.T, f *fixture) {
				if err := f.record(keyA, t0); err != nil {
					t.Fatal(err)
				}
				f.evidenceRow(t, evA, t0, keys.Current)
				f.evidenceRow(t, evB, t1, keys.Current)
			}},
	}
	for _, engine := range storetest.Engines() {
		for _, tt := range tests {
			t.Run(string(engine)+"/"+tt.name, func(t *testing.T) {
				f := newFixture(t, engine)
				tt.run(t, f)
				h, err := keys.Load(context.Background(), f.db.Read)
				if tt.wantServer == nil {
					if err == nil || !strings.Contains(err.Error(), "evidence keys "+seal.KeyID(evA.PublicKey())+" and "+seal.KeyID(evB.PublicKey())+" both valid") {
						t.Fatalf("Load error %v, want one naming the two current evidence keys", err)
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(h.Server, tt.wantServer) || !reflect.DeepEqual(plain(h.Evidence), plain(tt.wantEvidence)) {
					t.Errorf("the history holds server keys %+v and evidence keys %+v, want %+v and %+v", h.Server, h.Evidence, tt.wantServer, tt.wantEvidence)
				}
			})
		}
	}
}

func TestLoadEvidence(t *testing.T) {
	tests := []struct {
		name string
		// rows are stored as key_id, public_key in hex, from and to.
		rows    [][4]any
		want    []keys.EvidenceKey
		wantErr string
	}{
		{name: "evidence keys load oldest first, a NULL valid_to as Current",
			rows: [][4]any{{seal.KeyID(evB.PublicKey()), hex.EncodeToString(evB.PublicKey().Bytes()), int64(200), nil},
				{seal.KeyID(evA.PublicKey()), hex.EncodeToString(evA.PublicKey().Bytes()), int64(100), int64(199)}},
			want: []keys.EvidenceKey{evidence(evA, 100, 199), evidence(evB, 200, keys.Current)}},
		{name: "a stored point that isn't the key its id names is refused",
			rows:    [][4]any{{seal.KeyID(evA.PublicKey()), hex.EncodeToString(evB.PublicKey().Bytes()), int64(100), nil}},
			wantErr: "isn't that key"},
		{name: "a stored public key that isn't a P-256 point is refused",
			rows:    [][4]any{{backup.KeyID(keyA), hex.EncodeToString(keyA), int64(100), nil}},
			wantErr: "isn't that key"},
		{name: "a validity that ends before it starts is refused",
			rows:    [][4]any{{seal.KeyID(evA.PublicKey()), hex.EncodeToString(evA.PublicKey().Bytes()), int64(300), int64(200)}},
			wantErr: "before it became valid"},
	}
	for _, engine := range storetest.Engines() {
		for _, tt := range tests {
			t.Run(string(engine)+"/"+tt.name, func(t *testing.T) {
				f := newFixture(t, engine)
				for _, r := range tt.rows {
					f.exec(t, `INSERT INTO key_history (kind, key_id, public_key, valid_from_ms, valid_to_ms, recorded_at_ms) VALUES ($1, $2, $3, $4, $5, 1)`,
						keys.KindEvidence, r[0], r[1], r[2], r[3])
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
				if !reflect.DeepEqual(plain(h.Evidence), plain(tt.want)) {
					t.Errorf("Load = %+v, want %+v", h.Evidence, tt.want)
				}
			})
		}
	}
}

// documentEvidence is the evidence_key member of the keys document doc,
// or nil when it is null.
func documentEvidence(t *testing.T, doc []byte) map[string]any {
	t.Helper()
	var d struct {
		EvidenceKey map[string]any `json:"evidence_key"`
	}
	if err := json.Unmarshal(doc, &d); err != nil {
		t.Fatalf("the document doesn't parse: %v\n%s", err, doc)
	}
	return d.EvidenceKey
}

func TestRing(t *testing.T) {
	retired, current, viewer, stranger := evA, evB, evC, evidenceKey(4)
	h := &keys.History{
		Server:   []keys.ServerKey{key(keyA, 1795046400000, keys.Current)},
		Evidence: []keys.EvidenceKey{evidence(retired, 1795046400000, 1795600799999), evidence(current, 1795600800000, keys.Current)},
	}
	ring, err := keys.NewRing(h, retired, current)
	if err != nil {
		t.Fatal(err)
	}
	c := seal.Context{Submission: "s1", Part: "metadata"}
	m := []byte("a revealed prompt")
	sealTo := func(t *testing.T, k *ecdh.PrivateKey) []byte {
		t.Helper()
		b, err := seal.Seal(k.PublicKey(), c, m)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	opens := func(t *testing.T, r *keys.Ring, blob []byte) {
		t.Helper()
		got, err := r.Open(blob, c)
		if err != nil || !bytes.Equal(got, m) {
			t.Errorf("Open = %q, %v; want %q", got, err, m)
		}
	}
	tests := []struct {
		name string
		run  func(t *testing.T)
	}{
		{name: "a blob sealed to the current key opens", run: func(t *testing.T) { opens(t, ring, sealTo(t, current)) }},
		{name: "a blob sealed to the retired key opens with it", run: func(t *testing.T) { opens(t, ring, sealTo(t, retired)) }},
		{name: "only the current key's id is current", run: func(t *testing.T) {
			for id, want := range map[string]bool{seal.KeyID(current.PublicKey()): true, seal.KeyID(retired.PublicKey()): false, seal.KeyID(stranger.PublicKey()): false, "": false} {
				if got := ring.IsCurrent(id); got != want {
					t.Errorf("IsCurrent(%q) = %v, want %v", id, got, want)
				}
			}
			if k, ok := ring.Current(); !ok || k.ID != seal.KeyID(current.PublicKey()) {
				t.Errorf("Current = %+v, %v; want the current key", k, ok)
			}
		}},
		{name: "a blob sealed to a key the ring doesn't hold fails with ErrUnknownEvidenceKey", run: func(t *testing.T) {
			if got, err := ring.Open(sealTo(t, stranger), c); !errors.Is(err, keys.ErrUnknownEvidenceKey) || got != nil {
				t.Errorf("Open = %q, %v; want ErrUnknownEvidenceKey", got, err)
			}
		}},
		{name: "a blob sealed to the retired key re-sealed to the current one opens with the ring, and no longer with the retired key", run: func(t *testing.T) {
			again, err := ring.Reseal(sealTo(t, retired), c, current.PublicKey())
			if err != nil {
				t.Fatal(err)
			}
			if id, err := seal.BlobKeyID(again); err != nil || !ring.IsCurrent(id) {
				t.Errorf("the re-sealed blob names key %s (%v), not the current one", id, err)
			}
			opens(t, ring, again)
			if _, err := seal.Open(retired, c, again); !errors.Is(err, seal.ErrOtherKey) {
				t.Errorf("the retired key opens the re-sealed blob: %v", err)
			}
		}},
		{name: "a blob re-sealed to a viewer's key opens with the viewer's key", run: func(t *testing.T) {
			again, err := ring.Reseal(sealTo(t, current), c, viewer.PublicKey())
			if err != nil {
				t.Fatal(err)
			}
			if got, err := seal.Open(viewer, c, again); err != nil || !bytes.Equal(got, m) {
				t.Errorf("the viewer opens %q, %v; want %q", got, err, m)
			}
		}},
		{name: "the keys document lists the current evidence key alone, served with Cache-Control: no-cache", run: func(t *testing.T) {
			doc, err := keys.Document(h, ring)
			if err != nil {
				t.Fatal(err)
			}
			want := map[string]any{"key_id": backup.KeyID(current.PublicKey().Bytes()), "public_key": hex.EncodeToString(current.PublicKey().Bytes()), "valid_from": float64(1795600800000), "valid_to": nil}
			if got := documentEvidence(t, doc); !reflect.DeepEqual(got, want) {
				t.Errorf("evidence_key = %v, want %v", got, want)
			}
			for _, s := range []string{seal.KeyID(retired.PublicKey()), hex.EncodeToString(retired.PublicKey().Bytes())} {
				if bytes.Contains(doc, []byte(s)) {
					t.Errorf("the document names the retired evidence key, %s:\n%s", s, doc)
				}
			}
			rec := httptest.NewRecorder()
			keys.Handler(doc).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, keys.DocumentPath, nil))
			if rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != "no-cache" || !bytes.Equal(rec.Body.Bytes(), doc) {
				t.Errorf("GET = %d, Cache-Control %q, body %q; want 200, no-cache and the document", rec.Code, rec.Header().Get("Cache-Control"), rec.Body.String())
			}
		}},
		{name: "a ring without the current key's private half has no current key, and the document's evidence key is null", run: func(t *testing.T) {
			r, err := keys.NewRing(h, retired)
			if err != nil {
				t.Fatal(err)
			}
			if _, ok := r.Current(); ok || r.IsCurrent(seal.KeyID(current.PublicKey())) {
				t.Error("the ring reports a current key whose private half it doesn't hold")
			}
			doc, err := keys.Document(h, r)
			if err != nil {
				t.Fatal(err)
			}
			if got := documentEvidence(t, doc); got != nil {
				t.Errorf("evidence_key = %v, want null", got)
			}
		}},
		{name: "a nil ring holds no key: the document's evidence key is null and every blob's key is unknown", run: func(t *testing.T) {
			var r *keys.Ring
			doc, err := keys.Document(h, r)
			if err != nil {
				t.Fatal(err)
			}
			if got := documentEvidence(t, doc); got != nil {
				t.Errorf("evidence_key = %v, want null", got)
			}
			if _, err := r.Open(sealTo(t, current), c); !errors.Is(err, keys.ErrUnknownEvidenceKey) {
				t.Errorf("Open error %v, want ErrUnknownEvidenceKey", err)
			}
		}},
		{name: "NewRing refuses a key the history doesn't hold, since nothing says whether it is current", run: func(t *testing.T) {
			if _, err := keys.NewRing(h, current, stranger); err == nil || !strings.Contains(err.Error(), seal.KeyID(stranger.PublicKey())) {
				t.Errorf("NewRing error %v, want one naming %s", err, seal.KeyID(stranger.PublicKey()))
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, tt.run)
	}
}
