// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/server/config"
	"github.com/some-du6e/pairtime/internal/server/store"
	"github.com/some-du6e/pairtime/internal/server/store/storetest"
	"github.com/some-du6e/pairtime/internal/server/writer"
)

func TestNewPrivacyHub(t *testing.T) {
	key := config.EmailKey + "=" + strings.Repeat("ab", 32)
	tests := []struct {
		name    string
		aud     string
		environ []string
		wantOn  bool
		wantErr string
	}{
		{name: "without privacy_hub.aud the endpoints are off", environ: []string{key}},
		{name: "with privacy_hub.aud and the email key the endpoints are on", aud: "pairtime", environ: []string{key}, wantOn: true},
		{name: "privacy_hub.aud without the email key stops serve, since the eraser finds accounts by their email's index", aud: "pairtime", wantErr: config.EmailKey},
		{name: "an email key that isn't 32 bytes stops serve", aud: "pairtime", environ: []string{config.EmailKey + "=abcd"}, wantErr: config.EmailKey},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			secrets, err := config.LoadSecrets("", tt.environ)
			if err != nil {
				t.Fatal(err)
			}
			db := storetest.Open(t, store.SQLite)
			w := writer.Start(db.Write, writer.Options{Window: time.Millisecond})
			t.Cleanup(w.Close)
			cfg := &config.Config{PrivacyHub: config.PrivacyHub{Aud: tt.aud}, Secrets: secrets}
			svc, err := newPrivacyHub(cfg, db, w, slog.New(slog.NewTextHandler(io.Discard, nil)))
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("newPrivacyHub error = %v, want one naming %s", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if (svc != nil) != tt.wantOn {
				t.Errorf("newPrivacyHub gave a service: %v, want %v", svc != nil, tt.wantOn)
			}
		})
	}
}
