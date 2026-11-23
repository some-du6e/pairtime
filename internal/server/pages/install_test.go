// SPDX-License-Identifier: AGPL-3.0-or-later

package pages

import (
	"bytes"
	"cmp"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/some-du6e/pairtime/internal/release"
)

// changedLines returns the lines of b that differ from a's, or nil when the
// two have different numbers of lines.
func changedLines(a, b []byte) []string {
	x, y := strings.Split(string(a), "\n"), strings.Split(string(b), "\n")
	if len(x) != len(y) {
		return nil
	}
	var out []string
	for i := range y {
		if x[i] != y[i] {
			out = append(out, y[i])
		}
	}
	return out
}

// TestInstallScripts checks GET /install.sh and /install.ps1 (docs/PLAN.md,
// WP-RL2): each answers with scriptsRelease's own install script, byte for
// byte the one that release's signed manifest lists, as plain text in
// UTF-8.
func TestInstallScripts(t *testing.T) {
	site := newSite(t, Options{})
	mux := http.NewServeMux()
	site.Register(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()
	do := func(t *testing.T, method, path string) (*http.Response, []byte) {
		t.Helper()
		req, err := http.NewRequest(method, srv.URL+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		res, err := srv.Client().Do(req)
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
	// The release's manifest, opened as a client opens one, with the keys
	// this build trusts, and the keys the release's scripts trust.
	data, err := fs.ReadFile(scriptFiles, "install/manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	trusted, err := release.TrustedKeys()
	if err != nil {
		t.Fatal(err)
	}
	m, _, err := release.Open(data, trusted)
	if err != nil {
		t.Fatalf("install/manifest.json: %v", err)
	}
	text, err := fs.ReadFile(scriptFiles, "install/release-keys.txt")
	if err != nil {
		t.Fatal(err)
	}
	keys, err := release.ParsePublicKeys(string(text))
	if err != nil || len(keys) == 0 {
		t.Fatalf("install/release-keys.txt has %d keys: %v", len(keys), err)
	}
	for _, s := range release.InstallScripts() {
		path := "/" + s.Name
		get, body := do(t, http.MethodGet, path)
		head, headBody := do(t, http.MethodHead, path)
		src, err := fs.ReadFile(scriptFiles, "install/"+s.Name)
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(body)
		changed := changedLines(src, body)
		installs := func(line string) bool { return strings.Contains(line, "'"+scriptsRelease+"'") }
		// install.sh lists the keys in one quoted string, split by spaces,
		// and install.ps1 quotes each one, so a key is found by its text.
		trusts := func(line string) bool {
			for _, k := range keys {
				if !strings.Contains(line, k.String()) {
					return false
				}
			}
			return true
		}
		tests := []struct {
			name string
			ok   bool
		}{
			{"GET " + path + " answers 200", get.StatusCode == http.StatusOK},
			{"GET " + path + " is plain text in UTF-8", get.Header.Get("Content-Type") == "text/plain; charset=utf-8"},
			{"no browser sniffs another type into " + path, get.Header.Get("X-Content-Type-Options") == "nosniff"},
			{"nothing stores " + path + ", so a server that serves a newer release hands nobody the older one", get.Header.Get("Cache-Control") == "no-store"},
			{path + "'s SHA-256 is the one " + scriptsRelease + "'s signed manifest lists for it", hex.EncodeToString(sum[:]) == m.InstallScripts[s.Name]},
			{path + " is the release's copy with two lines filled in and every other byte as it was", len(changed) == 2},
			{path + " installs " + scriptsRelease, len(changed) == 2 && (installs(changed[0]) || installs(changed[1]))},
			{path + " trusts every key the release trusts", len(changed) == 2 && (trusts(changed[0]) || trusts(changed[1]))},
			{"HEAD " + path + " answers 200 with the script's length and no body",
				head.StatusCode == http.StatusOK && head.Header.Get("Content-Length") == strconv.Itoa(len(body)) && len(headBody) == 0},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				if !tt.ok {
					t.Errorf("GET %s: %s, %q, %d bytes with SHA-256 %x, changed lines %q; HEAD: %s, %q",
						path, get.Status, get.Header, len(body), sum, changed, head.Status, head.Header)
				}
			})
		}
	}
	t.Run("POST /install.sh isn't allowed", func(t *testing.T) {
		if res, _ := do(t, http.MethodPost, "/install.sh"); res.StatusCode != http.StatusMethodNotAllowed {
			t.Errorf("POST /install.sh: %s", res.Status)
		}
	})
}

// TestStampScripts checks what stampScripts takes before a server serves a
// copy of a release's install scripts: only the release's own files, whose
// stamped scripts are the bytes its signed manifest lists.
func TestStampScripts(t *testing.T) {
	const version = "v0.1.0-alpha.9"
	newKey := func(t *testing.T) release.SecretKey {
		t.Helper()
		k, err := release.GenerateSecretKey(rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		return k
	}
	signer, other := newKey(t), newKey(t)
	// The key the release's scripts trust, which here is the one that
	// signed it.
	keyText := signer.Public().String()
	srcs := map[string]string{
		"install.sh":  "#!/bin/sh\npinned_version=''\npinned_keys=''\necho install\n",
		"install.ps1": "$PinnedVersion = ''\n$PinnedKeys = @()\nWrite-Output install\n",
	}
	// folder is a copy of a release's files, as install/ holds them: its
	// scripts before stamping, its release-keys.txt and its manifest, which
	// signer signs. With listed, the manifest lists the stamped scripts'
	// SHA-256s, as tools/release scripts -payload writes them.
	folder := func(t *testing.T, listed bool) fstest.MapFS {
		t.Helper()
		fsys := fstest.MapFS{"install/release-keys.txt": {Data: []byte("# The release's keys.\n" + keyText + "\n")}}
		sums := map[string]string{}
		for _, s := range release.InstallScripts() {
			fsys["install/"+s.Name] = &fstest.MapFile{Data: []byte(srcs[s.Name])}
			stamped, err := s.Stamp([]byte(srcs[s.Name]), version, []string{keyText})
			if err != nil {
				t.Fatal(err)
			}
			sum := sha256.Sum256(stamped)
			sums[s.Name] = hex.EncodeToString(sum[:])
		}
		m := &release.Manifest{
			Version:        version,
			Commit:         strings.Repeat("a", 40),
			Date:           "2026-11-23T00:00:00Z",
			MinimumVersion: "v0.0.0",
			Artifacts: []release.Artifact{{
				OS: "linux", Arch: "amd64",
				URL:    "https://github.com/some-du6e/pairtime/releases/download/" + version + "/pairtime_0.1.0-alpha.9_linux_amd64.tar.gz",
				SHA256: strings.Repeat("b", 64), Size: 1, BinarySHA256: strings.Repeat("c", 64),
			}},
		}
		if listed {
			m.InstallScripts = sums
		}
		payload, err := release.EncodeManifest(m)
		if err != nil {
			t.Fatal(err)
		}
		env, err := release.Sign(payload, signer)
		if err != nil {
			t.Fatal(err)
		}
		fsys["install/manifest.json"] = &fstest.MapFile{Data: env}
		return fsys
	}
	tests := []struct {
		name     string
		unlisted bool                // the manifest lists no install scripts
		edit     func(fstest.MapFS)  // a change to the folder once it is made
		version  string              // the release the server serves; "" for the folder's
		trusted  []release.PublicKey // the keys the server trusts; nil for the signer's
		wantErr  string              // "" means the scripts are served
	}{
		{name: "a release's own files, signed by a key the server trusts, are served stamped for that release"},
		{
			name: "a script changed after the release is refused, since its stamped SHA-256 isn't the one the manifest lists",
			edit: func(f fstest.MapFS) {
				f["install/install.sh"].Data = append(f["install/install.sh"].Data, "echo more\n"...)
			},
			wantErr: "install/install.sh stamped for " + version + " has SHA-256",
		},
		{
			name: "a key added to the copy's release keys is refused, since the stamped script would trust a key the release's doesn't",
			edit: func(f fstest.MapFS) {
				f["install/release-keys.txt"].Data = append(f["install/release-keys.txt"].Data, other.Public().String()+"\n"...)
			},
			wantErr: "has SHA-256",
		},
		{
			name:    "a manifest that no key the server trusts signed is refused",
			trusted: []release.PublicKey{other.Public()},
			wantErr: "no signature verifies under a trusted key",
		},
		{
			name: "a manifest changed by a byte is refused",
			edit: func(f fstest.MapFS) {
				f["install/manifest.json"].Data = bytes.Replace(f["install/manifest.json"].Data, []byte(`"payload":"e`), []byte(`"payload":"f`), 1)
			},
			wantErr: "install/manifest.json:",
		},
		{
			name:    "the manifest of another release is refused",
			version: "v0.1.0-alpha.8",
			wantErr: "is the manifest of " + version + ", not of v0.1.0-alpha.8",
		},
		{name: "a release whose manifest lists no install scripts is refused", unlisted: true, wantErr: "lists no SHA-256 for install.sh"},
		{name: "a copy without one of the scripts is refused", edit: func(f fstest.MapFS) { delete(f, "install/install.ps1") }, wantErr: "install.ps1"},
		{name: "a copy without the release's keys is refused", edit: func(f fstest.MapFS) { delete(f, "install/release-keys.txt") }, wantErr: "release-keys.txt"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fsys := folder(t, !tt.unlisted)
			if tt.edit != nil {
				tt.edit(fsys)
			}
			v := cmp.Or(tt.version, version)
			trusted := tt.trusted
			if trusted == nil {
				trusted = []release.PublicKey{signer.Public()}
			}
			got, err := stampScripts(fsys, v, trusted)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("stampScripts error = %v, want one containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			for _, s := range release.InstallScripts() {
				want, err := s.Stamp([]byte(srcs[s.Name]), version, []string{keyText})
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(got[s.Name], want) {
					t.Errorf("%s is\n%s\nwant\n%s", s.Name, got[s.Name], want)
				}
			}
		})
	}
}
