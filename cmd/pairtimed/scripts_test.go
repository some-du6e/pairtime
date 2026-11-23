// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/release"
	"github.com/some-du6e/pairtime/internal/server/pages"
)

// TestServeInstallScripts checks that pairtimed's public listener serves
// the install scripts of the release internal/server/pages pins
// (pages.ScriptsRelease), at /install.sh and /install.ps1, byte for byte
// the scripts that release's signed manifest lists, and that the ops
// listener doesn't (docs/PLAN.md, WP-RL2).
func TestServeInstallScripts(t *testing.T) {
	if pages.ScriptsRelease == "" {
		t.Skip("the pin is off, so pairtimed serves no install script")
	}
	configPath, _ := writeConfig(t)
	public, private := startServe(t, configPath)
	data, err := os.ReadFile(filepath.Join("..", "..", "internal", "server", "pages", "install", "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	trusted, err := release.TrustedKeys()
	if err != nil {
		t.Fatal(err)
	}
	m, _, err := release.Open(data, trusted)
	if err != nil || m.Version != pages.ScriptsRelease {
		t.Fatalf("the served release's manifest: %v, %+v, want %s's", err, m, pages.ScriptsRelease)
	}
	client := &http.Client{Timeout: 10 * time.Second}
	get := func(t *testing.T, url string) (*http.Response, []byte) {
		t.Helper()
		res, err := client.Get(url)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		body, err := io.ReadAll(res.Body)
		if err != nil {
			t.Fatal(err)
		}
		return res, body
	}
	for _, name := range []string{"install.sh", "install.ps1"} {
		path := "/" + name
		res, body := get(t, public+path)
		ops, _ := get(t, private+path)
		sum := sha256.Sum256(body)
		tests := []struct {
			name string
			ok   bool
		}{
			{"the public listener serves " + path, res.StatusCode == http.StatusOK},
			{path + " is the script " + m.Version + "'s signed manifest lists, byte for byte", hex.EncodeToString(sum[:]) == m.InstallScripts[name]},
			{path + " is plain text in UTF-8", res.Header.Get("Content-Type") == "text/plain; charset=utf-8"},
			{path + " loads nothing, sends no referrer and is never stored, as every answer of the public listener",
				res.Header.Get("Content-Security-Policy") == defaultHeaders["Content-Security-Policy"] &&
					res.Header.Get("Referrer-Policy") == "no-referrer" && res.Header.Get("Cache-Control") == "no-store"},
			{"the ops listener doesn't serve " + path, ops.StatusCode == http.StatusNotFound},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				if !tt.ok {
					t.Errorf("GET %s: %s, %q, %d bytes with SHA-256 %x; the ops listener: %s", path, res.Status, res.Header, len(body), sum, ops.Status)
				}
			})
		}
	}
}
