// SPDX-License-Identifier: AGPL-3.0-or-later

package release

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

const (
	sum1 = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	sum2 = "fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210"
)

// testManifest returns a valid manifest with two artifacts.
func testManifest() *Manifest {
	base := "https://github.com/some-du6e/pairtime/releases/download/v0.1.0-alpha.1/"
	return &Manifest{
		Version:        "v0.1.0-alpha.1",
		Commit:         "68726d8ccf480210ce26078e4d3006120e6b49a1",
		Date:           "2026-11-19T10:49:00Z",
		MinimumVersion: "v0.0.0",
		Artifacts: []Artifact{
			{OS: "windows", Arch: "amd64", URL: base + "pairtime_0.1.0-alpha.1_windows_amd64.zip", SHA256: sum1, Size: 3000000, BinarySHA256: sum2},
			{OS: "linux", Arch: "arm64", URL: base + "pairtime_0.1.0-alpha.1_linux_arm64.tar.gz", SHA256: sum2, Size: 2900000, BinarySHA256: sum1},
		},
	}
}

func TestManifestValidate(t *testing.T) {
	tests := []struct {
		name    string
		edit    func(m *Manifest)
		wantErr string // "" means valid
	}{
		{name: "the test manifest is valid", edit: func(m *Manifest) {}},
		{name: "minimum_version may equal version", edit: func(m *Manifest) { m.MinimumVersion = m.Version }},
		{name: "a 64-digit SHA-256 git hash is a commit", edit: func(m *Manifest) { m.Commit = sum1 }},
		{name: "http is fine for 127.0.0.1", edit: func(m *Manifest) { m.Artifacts[0].URL = "http://127.0.0.1:8080/a.zip" }},
		{name: "http is fine for localhost", edit: func(m *Manifest) { m.Artifacts[0].URL = "http://localhost:8080/a.zip" }},
		{name: "http is fine for ::1", edit: func(m *Manifest) { m.Artifacts[0].URL = "http://[::1]:8080/a.zip" }},
		{name: "a platform Go adds later is accepted", edit: func(m *Manifest) { m.Artifacts[0].OS, m.Artifacts[0].Arch = "plan9", "riscv64" }},
		{name: "version must be a pairtime version", edit: func(m *Manifest) { m.Version = "0.1.0" }, wantErr: "version:"},
		{name: "minimum_version must be a pairtime version", edit: func(m *Manifest) { m.MinimumVersion = "latest" }, wantErr: "minimum_version:"},
		{name: "minimum_version can't be newer than version", edit: func(m *Manifest) { m.MinimumVersion = "v0.1.0" }, wantErr: "newer than version"},
		{name: "commit must be a full hash", edit: func(m *Manifest) { m.Commit = "68726d8" }, wantErr: "commit"},
		{name: "commit must be lowercase", edit: func(m *Manifest) { m.Commit = strings.ToUpper(m.Commit) }, wantErr: "commit"},
		{name: "date must be RFC 3339", edit: func(m *Manifest) { m.Date = "2026-11-19 10:49:00" }, wantErr: "date"},
		{name: "date must be in UTC", edit: func(m *Manifest) { m.Date = "2026-11-19T11:49:00+01:00" }, wantErr: "date"},
		{name: "date has whole seconds", edit: func(m *Manifest) { m.Date = "2026-11-19T10:49:00.5Z" }, wantErr: "date"},
		{name: "a manifest lists at least one artifact", edit: func(m *Manifest) { m.Artifacts = nil }, wantErr: "none listed"},
		{name: "a platform is listed once", edit: func(m *Manifest) { m.Artifacts[1].OS, m.Artifacts[1].Arch = "windows", "amd64" }, wantErr: "windows/amd64 is listed twice"},
		{name: "os must be set", edit: func(m *Manifest) { m.Artifacts[0].OS = "" }, wantErr: "GOOS/GOARCH"},
		{name: "arch must be lowercase", edit: func(m *Manifest) { m.Artifacts[0].Arch = "AMD64" }, wantErr: "GOOS/GOARCH"},
		{name: "os can't hold a path", edit: func(m *Manifest) { m.Artifacts[0].OS = "../windows" }, wantErr: "GOOS/GOARCH"},
		{name: "url must be https", edit: func(m *Manifest) { m.Artifacts[0].URL = "http://github.com/a.zip" }, wantErr: "isn't https"},
		{name: "url must be absolute", edit: func(m *Manifest) { m.Artifacts[0].URL = "pairtime_0.1.0_windows_amd64.zip" }, wantErr: "isn't https"},
		{name: "url must have a host", edit: func(m *Manifest) { m.Artifacts[0].URL = "https:///a.zip" }, wantErr: "isn't https"},
		{name: "url can't carry credentials", edit: func(m *Manifest) { m.Artifacts[0].URL = "https://user:pw@example.com/a.zip" }, wantErr: "credentials"},
		{name: "url must name a file", edit: func(m *Manifest) { m.Artifacts[0].URL = "https://example.com/releases/" }, wantErr: "doesn't name a file"},
		{name: "url with no path names no file", edit: func(m *Manifest) { m.Artifacts[0].URL = "https://example.com" }, wantErr: "doesn't name a file"},
		{name: "sha256 must be 64 hex digits", edit: func(m *Manifest) { m.Artifacts[0].SHA256 = sum1[:63] }, wantErr: "sha256"},
		{name: "sha256 must be lowercase", edit: func(m *Manifest) { m.Artifacts[0].SHA256 = strings.ToUpper(sum1) }, wantErr: "sha256"},
		{name: "size must be positive", edit: func(m *Manifest) { m.Artifacts[0].Size = 0 }, wantErr: "size"},
		{name: "binary_sha256 must be 64 hex digits", edit: func(m *Manifest) { m.Artifacts[1].BinarySHA256 = "" }, wantErr: "artifacts[1]: binary_sha256"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := testManifest()
			tt.edit(m)
			err := m.Validate()
			if tt.wantErr == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("err = %v, want one containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestEncodeDecodeManifest(t *testing.T) {
	payload, err := EncodeManifest(testManifest())
	if err != nil {
		t.Fatal(err)
	}
	future := bytes.Replace(payload, []byte(`"version":`), []byte(`"channel_hint": {"from": "a later release"}, "version":`), 1)
	tests := []struct {
		name    string
		payload []byte
		wantErr string
	}{
		{name: "an encoded manifest decodes to the same manifest", payload: payload},
		{name: "fields a later release adds are ignored", payload: future},
		{name: "a payload that isn't JSON is refused", payload: []byte("version: v1"), wantErr: "manifest payload"},
		{name: "a payload that breaks a rule is refused", payload: bytes.Replace(payload, []byte(`"v0.0.0"`), []byte(`"v9.0.0"`), 1), wantErr: "newer than version"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, err := DecodeManifest(tt.payload)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want one containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			got, _ := json.Marshal(m)
			want, _ := json.Marshal(testManifest())
			if !bytes.Equal(got, want) {
				t.Fatalf("decoded %s, want %s", got, want)
			}
		})
	}
	if !bytes.HasSuffix(payload, []byte("}\n")) || !bytes.Contains(payload, []byte("\n  \"version\": ")) {
		t.Errorf("EncodeManifest should write indented JSON with a final newline, wrote:\n%s", payload)
	}
	if bytes.Contains(payload, []byte(`&`)) {
		t.Errorf("EncodeManifest escaped HTML characters")
	}
	if _, err := EncodeManifest(&Manifest{}); err == nil {
		t.Errorf("EncodeManifest accepted an invalid manifest")
	}
}

func TestManifestFor(t *testing.T) {
	m := testManifest()
	tests := []struct {
		name         string
		goos, goarch string
		wantURL      string
	}{
		{name: "a listed platform is found", goos: "linux", goarch: "arm64", wantURL: m.Artifacts[1].URL},
		{name: "an unlisted arch isn't", goos: "linux", goarch: "amd64"},
		{name: "os and arch aren't swapped", goos: "amd64", goarch: "windows"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a, ok := m.For(tt.goos, tt.goarch)
			if ok != (tt.wantURL != "") || a.URL != tt.wantURL {
				t.Fatalf("For(%s, %s) = %q, %v; want %q", tt.goos, tt.goarch, a.URL, ok, tt.wantURL)
			}
		})
	}
}
