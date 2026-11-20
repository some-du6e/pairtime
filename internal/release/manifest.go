// SPDX-License-Identifier: AGPL-3.0-or-later

package release

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"path"
	"strings"
	"time"
)

// Manifest is the update manifest a release publishes, before signing.
// docs/spec/update-manifest.md defines each field.
type Manifest struct {
	// Version is the release version, such as v0.1.0-alpha.1.
	Version string `json:"version"`
	// Commit is the full git commit hash the release was built from.
	Commit string `json:"commit"`
	// Date is the commit time in RFC 3339 UTC, the binaries' -X Date.
	Date string `json:"date"`
	// MinimumVersion is the oldest client version the release still
	// supports. It is at most Version.
	MinimumVersion string `json:"minimum_version"`
	// Artifacts holds one archive per platform.
	Artifacts []Artifact `json:"artifacts"`
}

// Artifact is one platform's archive.
type Artifact struct {
	// OS and Arch are Go's GOOS and GOARCH names.
	OS   string `json:"os"`
	Arch string `json:"arch"`
	// URL is where the archive is published.
	URL string `json:"url"`
	// SHA256 is the archive's SHA-256 in lowercase hex, and Size its length
	// in bytes.
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
	// BinarySHA256 is the SHA-256 of the pairtime binary inside the archive.
	BinarySHA256 string `json:"binary_sha256"`
}

// For returns the artifact for one platform.
func (m *Manifest) For(goos, goarch string) (Artifact, bool) {
	for _, a := range m.Artifacts {
		if a.OS == goos && a.Arch == goarch {
			return a, true
		}
	}
	return Artifact{}, false
}

// Validate checks every field against the spec.
func (m *Manifest) Validate() error {
	v, err := ParseVersion(m.Version)
	if err != nil {
		return fmt.Errorf("version: %w", err)
	}
	minimum, err := ParseVersion(m.MinimumVersion)
	if err != nil {
		return fmt.Errorf("minimum_version: %w", err)
	}
	if minimum.Compare(v) > 0 {
		return fmt.Errorf("minimum_version %s is newer than version %s", m.MinimumVersion, m.Version)
	}
	if !isLowerHex(m.Commit, 40) && !isLowerHex(m.Commit, 64) {
		return fmt.Errorf("commit %q isn't a full git hash in lowercase hex", m.Commit)
	}
	if t, err := time.Parse(time.RFC3339, m.Date); err != nil || t.UTC().Format(time.RFC3339) != m.Date {
		return fmt.Errorf("date %q isn't RFC 3339 in UTC with whole seconds, like 2026-11-19T10:49:00Z", m.Date)
	}
	if len(m.Artifacts) == 0 {
		return errors.New("artifacts: none listed")
	}
	seen := map[string]bool{}
	for i, a := range m.Artifacts {
		if err := a.validate(); err != nil {
			return fmt.Errorf("artifacts[%d]: %w", i, err)
		}
		platform := a.OS + "/" + a.Arch
		if seen[platform] {
			return fmt.Errorf("artifacts[%d]: %s is listed twice", i, platform)
		}
		seen[platform] = true
	}
	return nil
}

func (a Artifact) validate() error {
	if !isGoName(a.OS) || !isGoName(a.Arch) {
		return fmt.Errorf("platform %q/%q isn't a GOOS/GOARCH pair", a.OS, a.Arch)
	}
	if err := checkURL(a.URL); err != nil {
		return err
	}
	if !isLowerHex(a.SHA256, 64) {
		return fmt.Errorf("sha256 %q isn't 64 lowercase hex digits", a.SHA256)
	}
	if a.Size <= 0 {
		return fmt.Errorf("size %d isn't positive", a.Size)
	}
	if !isLowerHex(a.BinarySHA256, 64) {
		return fmt.Errorf("binary_sha256 %q isn't 64 lowercase hex digits", a.BinarySHA256)
	}
	return nil
}

// checkURL requires an absolute https URL naming a file. Plain http is
// allowed only for a loopback host, which test fixtures serve from.
func checkURL(s string) error {
	u, err := url.Parse(s)
	if err != nil {
		return fmt.Errorf("url: %w", err)
	}
	switch {
	case u.Scheme == "https" && u.Host != "":
	case u.Scheme == "http" && isLoopback(u.Hostname()):
	default:
		return fmt.Errorf("url %q isn't https (http is allowed only for a loopback host)", s)
	}
	if u.User != nil {
		return fmt.Errorf("url %q carries credentials", s)
	}
	if base := path.Base(u.Path); base == "/" || base == "." || strings.HasSuffix(u.Path, "/") {
		return fmt.Errorf("url %q doesn't name a file", s)
	}
	return nil
}

func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	addr, err := netip.ParseAddr(host)
	return err == nil && addr.IsLoopback()
}

// isGoName reports whether s looks like a GOOS or GOARCH value. It allows
// names Go adds later, so an old client still reads a newer manifest.
func isGoName(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') {
			return false
		}
	}
	return s != ""
}

func isLowerHex(s string, n int) bool {
	if len(s) != n {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// EncodeManifest validates m and returns the payload Sign signs: indented
// JSON with a final newline, readable once decoded from the envelope.
func EncodeManifest(m *Manifest) ([]byte, error) {
	if err := m.Validate(); err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(m); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// DecodeManifest parses and validates a payload. Fields it doesn't know
// are ignored, so a client keeps reading manifests that newer releases
// extend.
func DecodeManifest(payload []byte) (*Manifest, error) {
	var m Manifest
	if err := json.Unmarshal(payload, &m); err != nil {
		return nil, fmt.Errorf("manifest payload: %w", err)
	}
	if err := m.Validate(); err != nil {
		return nil, fmt.Errorf("manifest payload: %w", err)
	}
	return &m, nil
}
