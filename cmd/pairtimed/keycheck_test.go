// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"crypto/x509"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/server/backup"
)

// serveOnce runs pairtimed serve on configPath until it listens, then
// stops it, and returns its exit code and standard error. A serve that
// refuses to start returns without listening.
func serveOnce(t *testing.T, configPath string) (int, string) {
	t.Helper()
	e, _, stderr := testEnv()
	up := make(chan struct{}, 1)
	e.listening = func(string, string) { up <- struct{}{} }
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan int, 1)
	go func() { done <- run(ctx, []string{"serve", "-config", configPath}, e) }()
	select {
	case <-up:
		cancel()
	case code := <-done:
		return code, stderr.String()
	case <-time.After(60 * time.Second):
		t.Fatalf("serve neither listened nor stopped within a minute; stderr:\n%s", stderr)
	}
	select {
	case code := <-done:
		return code, stderr.String()
	case <-time.After(60 * time.Second):
		t.Fatalf("serve didn't stop within a minute; stderr:\n%s", stderr)
	}
	return 0, ""
}

// TestServeChecksKeys checks that serve ties the database to the keys in
// data_dir (#133): the first start records them, and a later start with
// other keys, as after init on a rebuilt host, refuses to serve.
func TestServeChecksKeys(t *testing.T) {
	makeKeys := func(t *testing.T, dataDir string) *backup.Keys {
		t.Helper()
		keys, err := backup.Generate(backup.KeysDir(dataDir), rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		return keys
	}
	tests := []struct {
		name string
		// before runs between the first start and the second.
		before   func(t *testing.T, dataDir string)
		wantCode int
		wantErr  string
	}{
		{name: "the same keys serve again"},
		{name: "other keys are refused, naming the backup to restore them from",
			before: func(t *testing.T, dataDir string) {
				if err := os.RemoveAll(backup.KeysDir(dataDir)); err != nil {
					t.Fatal(err)
				}
				makeKeys(t, dataDir)
			},
			wantCode: 1, wantErr: "aren't the ones this database was written with"},
		{name: "another evidence key beside the same server key and pepper is refused, since the stored blobs are sealed to the recorded one",
			before: func(t *testing.T, dataDir string) {
				k, err := ecdh.P256().GenerateKey(rand.Reader)
				if err != nil {
					t.Fatal(err)
				}
				der, err := x509.MarshalPKCS8PrivateKey(k)
				if err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(backup.KeysDir(dataDir), backup.EvidenceKeyFile)
				if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0o600); err != nil {
					t.Fatal(err)
				}
			},
			wantCode: 1, wantErr: "evidence key in data_dir isn't the current one"},
		{name: "the keys gone from data_dir serve on with a warning, since there is nothing to compare",
			before: func(t *testing.T, dataDir string) {
				if err := os.RemoveAll(backup.KeysDir(dataDir)); err != nil {
					t.Fatal(err)
				}
			},
			wantErr: "no keys to check against the database"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			configPath, dbPath := writeConfig(t)
			dataDir := filepath.Dir(dbPath)
			keys := makeKeys(t, dataDir)
			if code, stderr := serveOnce(t, configPath); code != 0 {
				t.Fatalf("the first start exited %d; stderr:\n%s", code, stderr)
			}
			db, err := sql.Open("sqlite", dbPath)
			if err != nil {
				t.Fatal(err)
			}
			var id, pepper string
			err = db.QueryRow("SELECT server_key_id, pepper_fingerprint FROM server_keys WHERE id = 1").Scan(&id, &pepper)
			db.Close()
			if err != nil || id != keys.Public().ServerKeyID() || pepper != keys.PepperFingerprint() {
				t.Fatalf("server_keys holds %q, %q (%v); want %s and %s", id, pepper, err, keys.Public().ServerKeyID(), keys.PepperFingerprint())
			}
			if len(pepper) != 16 {
				t.Errorf("the pepper's fingerprint %q isn't 16 hex digits", pepper)
			}
			if tt.before != nil {
				tt.before(t, dataDir)
			}
			code, stderr := serveOnce(t, configPath)
			if code != tt.wantCode {
				t.Fatalf("the second start exited %d, want %d; stderr:\n%s", code, tt.wantCode, stderr)
			}
			if tt.wantErr != "" && !strings.Contains(stderr, tt.wantErr) {
				t.Errorf("stderr doesn't say %q:\n%s", tt.wantErr, stderr)
			}
		})
	}
}

// fetchKeysDocument starts serve on configPath, fetches
// /.well-known/pairtime-keys.json, stops serve and returns the document.
func fetchKeysDocument(t *testing.T, configPath string) []byte {
	t.Helper()
	e, _, stderr := testEnv()
	up := make(chan string, 1)
	e.listening = func(public, _ string) { up <- public }
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan int, 1)
	go func() { done <- run(ctx, []string{"serve", "-config", configPath}, e) }()
	var public string
	select {
	case public = <-up:
	case code := <-done:
		t.Fatalf("serve exited %d before listening; stderr:\n%s", code, stderr)
	case <-time.After(60 * time.Second):
		t.Fatalf("serve didn't start within a minute; stderr:\n%s", stderr)
	}
	client := &http.Client{Timeout: 10 * time.Second}
	var body []byte
	resp, err := client.Get("http://" + public + "/.well-known/pairtime-keys.json")
	if err == nil {
		body, err = io.ReadAll(resp.Body)
		resp.Body.Close()
		if err == nil && resp.StatusCode != http.StatusOK {
			err = fmt.Errorf("it answered %d", resp.StatusCode)
		}
	}
	cancel()
	select {
	case code := <-done:
		if code != 0 {
			t.Errorf("serve exited %d once stopped; stderr:\n%s", code, stderr)
		}
	case <-time.After(60 * time.Second):
		t.Fatalf("serve didn't stop within a minute; stderr:\n%s", stderr)
	}
	if err != nil {
		t.Fatalf("fetching the keys document: %v", err)
	}
	return body
}

// TestServeKeysDocument checks /.well-known/pairtime-keys.json as serve
// publishes it (WP-SV17, WP-SB2; docs/spec/keys-v1.md): the server key in
// data_dir, current, from the start of the UTC day serve first recorded
// it, and the evidence key in data_dir, current from that start's
// millisecond; the same document byte for byte on a later start; and an
// empty list of keys and a null evidence key, still answering, without
// them.
func TestServeKeysDocument(t *testing.T) {
	tests := []struct {
		name string
		keys bool
		// starts is how many times serve starts. Each start after the
		// first must serve the first one's document.
		starts int
	}{
		{name: "the document lists the server key in data_dir as current, valid from the start of the UTC day serve first recorded it, and the evidence key from that start", keys: true, starts: 1},
		{name: "a second start serves the same document, byte for byte, since both keys are in the history already", keys: true, starts: 2},
		{name: "without keys the document lists no server key and no evidence key, and serve still answers with it", starts: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			configPath, dbPath := writeConfig(t)
			var pub backup.Public
			if tt.keys {
				keys, err := backup.Generate(backup.KeysDir(filepath.Dir(dbPath)), rand.Reader)
				if err != nil {
					t.Fatal(err)
				}
				pub = keys.Public()
			}
			first := fetchKeysDocument(t, configPath)
			for i := 1; i < tt.starts; i++ {
				if again := fetchKeysDocument(t, configPath); !bytes.Equal(again, first) {
					t.Errorf("start %d served\n%s\nand the first\n%s", i+1, again, first)
				}
			}
			var doc struct {
				Format     string `json:"format"`
				ServerKeys []struct {
					KeyID     string `json:"key_id"`
					PublicKey string `json:"public_key"`
					ValidFrom int64  `json:"valid_from"`
					ValidTo   *int64 `json:"valid_to"`
				} `json:"server_keys"`
				EvidenceKey json.RawMessage `json:"evidence_key"`
			}
			if err := json.Unmarshal(first, &doc); err != nil {
				t.Fatalf("the document doesn't parse: %v\n%s", err, first)
			}
			if doc.Format != "pairtime-keys-v1" || doc.ServerKeys == nil {
				t.Errorf("the document's format is %q and its server keys %v; want pairtime-keys-v1 and a list", doc.Format, doc.ServerKeys)
			}
			if !tt.keys {
				if len(doc.ServerKeys) != 0 || string(doc.EvidenceKey) != "null" {
					t.Errorf("the document lists %d server keys and evidence key %s, and data_dir has no keys", len(doc.ServerKeys), doc.EvidenceKey)
				}
				return
			}
			db, err := sql.Open("sqlite", dbPath)
			if err != nil {
				t.Fatal(err)
			}
			var recorded, evidenceFrom int64
			err = db.QueryRow("SELECT recorded_at_ms FROM server_keys WHERE id = 1").Scan(&recorded)
			if err == nil {
				err = db.QueryRow("SELECT valid_from_ms FROM key_history WHERE kind = 'evidence' AND valid_to_ms IS NULL").Scan(&evidenceFrom)
			}
			db.Close()
			if err != nil {
				t.Fatal(err)
			}
			var ev struct {
				KeyID     string `json:"key_id"`
				PublicKey string `json:"public_key"`
				ValidFrom int64  `json:"valid_from"`
				ValidTo   *int64 `json:"valid_to"`
			}
			if err := json.Unmarshal(doc.EvidenceKey, &ev); err != nil {
				t.Fatalf("the evidence key %s doesn't parse: %v", doc.EvidenceKey, err)
			}
			if ev.KeyID != pub.EvidenceKeyID() || ev.PublicKey != hex.EncodeToString(pub.Evidence.Bytes()) || ev.ValidFrom != evidenceFrom || ev.ValidTo != nil {
				t.Errorf("the document lists evidence key %s, %s, valid from %d to %v; want %s, %s, from %d to null",
					ev.KeyID, ev.PublicKey, ev.ValidFrom, ev.ValidTo, pub.EvidenceKeyID(), hex.EncodeToString(pub.Evidence.Bytes()), evidenceFrom)
			}
			if len(doc.ServerKeys) != 1 {
				t.Fatalf("the document lists %d server keys, want 1:\n%s", len(doc.ServerKeys), first)
			}
			k := doc.ServerKeys[0]
			want := recorded - recorded%86_400_000
			if k.KeyID != pub.ServerKeyID() || k.PublicKey != hex.EncodeToString(pub.Server) || k.ValidFrom != want || k.ValidTo != nil {
				t.Errorf("the document lists key %s, %s, valid from %d to %v; want %s, %s, from %d to null",
					k.KeyID, k.PublicKey, k.ValidFrom, k.ValidTo, pub.ServerKeyID(), hex.EncodeToString(pub.Server), want)
			}
		})
	}
}
