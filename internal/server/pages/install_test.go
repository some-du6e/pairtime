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
	"regexp"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/some-du6e/pairtime/internal/release"
)

// TestInstallScripts checks GET /install.sh and /install.ps1 (docs/PLAN.md,
// WP-RL2): each answers with the script ScriptsRelease published, byte for
// byte the one its signed manifest lists, as plain text in UTF-8.
func TestInstallScripts(t *testing.T) {
	if ScriptsRelease == "" {
		t.Skip("the pin is off, so the server serves no install script (TestInstallScriptsOff)")
	}
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
	// this build trusts.
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
	for _, name := range scriptNames {
		path := "/" + name
		get, body := do(t, http.MethodGet, path)
		head, headBody := do(t, http.MethodHead, path)
		copied, err := fs.ReadFile(scriptFiles, "install/"+name)
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(body)
		tests := []struct {
			name string
			ok   bool
		}{
			{"GET " + path + " answers 200", get.StatusCode == http.StatusOK},
			{"GET " + path + " is plain text in UTF-8", get.Header.Get("Content-Type") == "text/plain; charset=utf-8"},
			{"no browser sniffs another type into " + path, get.Header.Get("X-Content-Type-Options") == "nosniff"},
			{"nothing stores " + path + ", so a server that serves a newer release hands nobody the older one", get.Header.Get("Cache-Control") == "no-store"},
			{path + " is the release's copy in install/, byte for byte", bytes.Equal(body, copied)},
			{path + "'s SHA-256 is the one " + ScriptsRelease + "'s signed manifest lists for it", hex.EncodeToString(sum[:]) == m.InstallScripts[name]},
			{path + " installs " + ScriptsRelease, bytes.Contains(body, []byte("'"+ScriptsRelease+"'"))},
			{"HEAD " + path + " answers 200 with the script's length and no body",
				head.StatusCode == http.StatusOK && head.Header.Get("Content-Length") == strconv.Itoa(len(body)) && len(headBody) == 0},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				if !tt.ok {
					t.Errorf("GET %s: %s, %q, %d bytes with SHA-256 %x; HEAD: %s, %q", path, get.Status, get.Header, len(body), sum, head.Status, head.Header)
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

// releaseKeyText is a release public key as a stamped script writes it:
// PublicKey.String's form, the prefix and 32 bytes in padded base64.
var releaseKeyText = regexp.MustCompile(`pairtime-release-ed25519-public:[A-Za-z0-9+/]{43}=`)

// TestServedScriptsTrustOnlyThisBuildsKeys checks that every key a served
// script trusts is in internal/release/release-keys.txt, the keys this
// build trusts, unless the pin is off. So the PR that drops a key fails
// here until the pin moves to a release whose scripts no longer trust it,
// or turns off (docs/spec/update-manifest.md, Replacing a key, step 4).
func TestServedScriptsTrustOnlyThisBuildsKeys(t *testing.T) {
	if ScriptsRelease == "" {
		t.Skip("the pin is off, so the server serves no install script")
	}
	scripts, err := servedScripts(ScriptsRelease)
	if err != nil {
		t.Fatal(err)
	}
	trusted, err := release.TrustedKeys()
	if err != nil {
		t.Fatal(err)
	}
	ours := map[string]bool{}
	for _, k := range trusted {
		ours[k.String()] = true
	}
	for _, name := range scriptNames {
		keys := releaseKeyText.FindAllString(string(scripts[name]), -1)
		tests := []struct {
			name string
			ok   bool
		}{
			{name + " trusts a key, since a script that trusts none refuses to install", len(keys) > 0},
		}
		for _, text := range keys {
			k, err := release.ParsePublicKey(text)
			id := text
			if err == nil {
				id = k.ID()
			}
			tests = append(tests, struct {
				name string
				ok   bool
			}{name + " trusts key " + id + ", which this build trusts too", err == nil && ours[text]})
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				if !tt.ok {
					t.Errorf("%s trusts %q, and this build trusts %d keys", name, keys, len(trusted))
				}
			})
		}
	}
}

// TestCheckScripts checks what checkScripts takes before a server serves a
// copy of a release's install scripts: only the scripts the release
// published, whose SHA-256s its signed manifest lists.
func TestCheckScripts(t *testing.T) {
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
	scripts := map[string]string{
		"install.sh":  "#!/bin/sh\npinned_version='" + version + "'\necho install\n",
		"install.ps1": "$PinnedVersion = '" + version + "'\nWrite-Output install\n",
	}
	// folder is a copy of a release's files, as install/ holds them: the
	// scripts it published and its manifest, which signer signs. With
	// listed, the manifest lists the scripts' SHA-256s, as tools/release
	// scripts -payload writes them.
	folder := func(t *testing.T, listed bool) fstest.MapFS {
		t.Helper()
		fsys := fstest.MapFS{}
		sums := map[string]string{}
		for name, body := range scripts {
			fsys["install/"+name] = &fstest.MapFile{Data: []byte(body)}
			sum := sha256.Sum256([]byte(body))
			sums[name] = hex.EncodeToString(sum[:])
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
		{name: "a release's own scripts, which a manifest a trusted key signed lists, are served byte for byte"},
		{
			name: "a script changed after the release is refused, since its SHA-256 isn't the one the manifest lists",
			edit: func(f fstest.MapFS) {
				f["install/install.sh"].Data = append(f["install/install.sh"].Data, "echo more\n"...)
			},
			wantErr: "install/install.sh has SHA-256",
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
		{name: "a copy without the release's manifest is refused", edit: func(f fstest.MapFS) { delete(f, "install/manifest.json") }, wantErr: "manifest.json"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fsys := folder(t, !tt.unlisted)
			if tt.edit != nil {
				tt.edit(fsys)
			}
			trusted := tt.trusted
			if trusted == nil {
				trusted = []release.PublicKey{signer.Public()}
			}
			got, err := checkScripts(fsys, cmp.Or(tt.version, version), trusted)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("checkScripts error = %v, want one containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			for _, name := range scriptNames {
				if string(got[name]) != scripts[name] {
					t.Errorf("%s is %q, want %q", name, got[name], scripts[name])
				}
			}
		})
	}
}

// TestInstallScriptsOff checks the pin's off state: no release, so no
// script, and both paths answer 404 while pairtimed runs without them.
func TestInstallScriptsOff(t *testing.T) {
	scripts, err := servedScripts("")
	mux := http.NewServeMux()
	(&Site{scripts: scripts}).registerScripts(mux)
	tests := []struct {
		name string
		ok   bool
	}{
		{"with the pin off there is no script to serve, and nothing to refuse", scripts == nil && err == nil},
	}
	for _, name := range scriptNames {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/"+name, nil))
		tests = append(tests, struct {
			name string
			ok   bool
		}{"with the pin off GET /" + name + " answers 404", rec.Code == http.StatusNotFound})
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !tt.ok {
				t.Errorf("servedScripts(\"\") = %d scripts, %v", len(scripts), err)
			}
		})
	}
}

// TestDashboardFollowsThePin checks that the empty dashboard's install
// lines name the release the server serves at /install.sh, unless the pin
// is off, so a PR that moves the pin moves them, and README.md's with them
// (TestInstallLines).
func TestDashboardFollowsThePin(t *testing.T) {
	tests := []struct {
		name string
		ok   bool
	}{
		{"the dashboard's install lines name the release the pin names", ScriptsRelease == "" || installRelease == ScriptsRelease},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !tt.ok {
				t.Errorf("installRelease is %s and ScriptsRelease %s", installRelease, ScriptsRelease)
			}
		})
	}
}
