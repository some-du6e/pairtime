// SPDX-License-Identifier: AGPL-3.0-or-later

package pages

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"io/fs"
	"net/http"
	"strconv"

	"github.com/some-du6e/pairtime/internal/release"
)

// ScriptsRelease is the release whose install scripts pairtimed serves at
// /install.sh and /install.ps1, so that `curl -fsSL https://<host>/install.sh
// | sh` and DESIGN's `irm https://<host>/install.ps1 | iex` install it
// (docs/PLAN.md, WP-RL2). Those commands can't pass flags, and the
// repository's install/ scripts name no release and trust no key, so the
// server serves the scripts the release published: install/ here holds
// them, byte for byte, with the release's signed manifest.json. The copy is
// pinned to a release, not taken from install/ at the commit pairtimed is
// built from, which can be newer than any release.
//
// "" turns the pin off: both paths answer 404, and pairtimed starts without
// them. The PR that drops a key from internal/release/release-keys.txt
// turns it off, unless it names a release that a remaining key signed and
// whose scripts trust no other key, and a later PR pins a release the new
// key signed (docs/spec/update-manifest.md, Replacing a key).
// install/README.md says how to move the pin after each release.
const ScriptsRelease = "v0.1.0-alpha.7"

// scriptNames are the install scripts the server serves, each at /<name>,
// as the release's manifest names them in install_scripts.
var scriptNames = [...]string{"install.sh", "install.ps1"}

// scriptFiles is the copy of ScriptsRelease's files.
//
//go:embed install
var scriptFiles embed.FS

// servedScripts returns the install scripts the server serves for the
// release version, by file name, checked by checkScripts against the
// release keys this build trusts, or none when version is "", while the pin
// is off.
func servedScripts(version string) (map[string][]byte, error) {
	if version == "" {
		return nil, nil
	}
	trusted, err := release.TrustedKeys()
	if err != nil {
		return nil, fmt.Errorf("pages: the release keys this server trusts: %w", err)
	}
	return checkScripts(scriptFiles, version, trusted)
}

// checkScripts reads the install scripts in fsys's install/ folder and
// returns them by file name, once each is byte for byte the script the
// release version published: its SHA-256 must be the one install_scripts
// lists in the release's manifest.json there, which one of trusted, the
// release keys this server's code trusts, must have signed. So a copy
// changed by a byte, or a manifest of another release or one that no
// trusted key signed, stops pairtimed at start rather than reaching anyone
// who runs the script.
func checkScripts(fsys fs.FS, version string, trusted []release.PublicKey) (map[string][]byte, error) {
	data, err := fs.ReadFile(fsys, "install/manifest.json")
	if err != nil {
		return nil, fmt.Errorf("pages: %w", err)
	}
	m, _, err := release.Open(data, trusted)
	if err != nil {
		return nil, fmt.Errorf("pages: install/manifest.json: %w", err)
	}
	if m.Version != version {
		return nil, fmt.Errorf("pages: install/manifest.json is the manifest of %s, not of %s, whose install scripts this server serves", m.Version, version)
	}
	out := map[string][]byte{}
	for _, name := range scriptNames {
		body, err := fs.ReadFile(fsys, "install/"+name)
		if err != nil {
			return nil, fmt.Errorf("pages: %w", err)
		}
		want, ok := m.InstallScripts[name]
		if !ok {
			return nil, fmt.Errorf("pages: the manifest of %s lists no SHA-256 for %s", version, name)
		}
		sum := sha256.Sum256(body)
		if got := hex.EncodeToString(sum[:]); got != want {
			return nil, fmt.Errorf("pages: install/%s has SHA-256 %s, but the signed manifest of %s lists %s, so it isn't the script the release published; download the release's files again as install/README.md says", name, got, version, want)
		}
		out[name] = body
	}
	return out, nil
}

// registerScripts serves each install script at /<name>. While the pin is
// off there is none, and both paths answer 404.
func (s *Site) registerScripts(mux *http.ServeMux) {
	for _, name := range scriptNames {
		body, ok := s.scripts[name]
		if !ok {
			continue
		}
		mux.HandleFunc("GET /"+name, func(w http.ResponseWriter, _ *http.Request) {
			writeScript(w, body)
		})
	}
}

// writeScript writes an install script as plain text in UTF-8, which the
// scripts are as ASCII, so `irm` hands PowerShell the text as it is and a
// browser shows it. Nothing stores it, so once the server serves a newer
// release, nobody is handed the older one.
func writeScript(w http.ResponseWriter, body []byte) {
	h := w.Header()
	h.Set("Content-Type", "text/plain; charset=utf-8")
	h.Set("Content-Length", strconv.Itoa(len(body)))
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	w.Write(body)
}
