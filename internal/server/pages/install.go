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

// scriptsRelease is the release whose install scripts pairtimed serves at
// /install.sh and /install.ps1, so DESIGN's `irm https://<host>/install.ps1
// | iex` and `curl -fsSL https://<host>/install.sh | sh` install it
// (docs/PLAN.md, WP-RL2). Those commands take no flags, and the
// repository's install/ copies name no release and trust no key, so the
// server serves the release's own copies: install/ here holds that
// release's install.sh, install.ps1 and release-keys.txt as its tag has
// them and its signed manifest.json, and New stamps the scripts the way
// tools/release scripts stamped them for it (release.InstallScript.Stamp).
// The copy is pinned to a release, not taken from install/ at the commit
// pairtimed is built from, which can be newer than any release: a script
// whose bytes no release lists would install a release it wasn't made for.
// install/README.md says how to move it to a newer release.
const scriptsRelease = "v0.1.0-alpha.6"

// scriptFiles is the copy of scriptsRelease's install scripts.
//
//go:embed install/install.sh install/install.ps1 install/release-keys.txt install/manifest.json
var scriptFiles embed.FS

// servedScripts returns scriptsRelease's install scripts as this server
// serves them, stamped and checked by stampScripts against the release
// keys this server's code trusts.
func servedScripts() (map[string][]byte, error) {
	trusted, err := release.TrustedKeys()
	if err != nil {
		return nil, fmt.Errorf("pages: the release keys this server trusts: %w", err)
	}
	return stampScripts(scriptFiles, scriptsRelease, trusted)
}

// stampScripts stamps the install scripts in fsys's install/ folder for
// the release version, with the release keys its release-keys.txt lists,
// and returns them by file name. It refuses them unless each is byte for
// byte the script the release carries: its SHA-256 must be the one
// install_scripts lists in the release's manifest.json there, which one
// of trusted, the release keys this server's code trusts, must have
// signed. So a copy changed by a byte, or a manifest of another release or
// of no release, stops pairtimed at start rather than reaching anyone who
// runs the script.
func stampScripts(fsys fs.FS, version string, trusted []release.PublicKey) (map[string][]byte, error) {
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
	text, err := fs.ReadFile(fsys, "install/release-keys.txt")
	if err != nil {
		return nil, fmt.Errorf("pages: %w", err)
	}
	keys, err := release.ParsePublicKeys(string(text))
	if err != nil {
		return nil, fmt.Errorf("pages: install/release-keys.txt: %w", err)
	}
	texts := make([]string, len(keys))
	for i, k := range keys {
		texts[i] = k.String()
	}
	out := map[string][]byte{}
	for _, s := range release.InstallScripts() {
		src, err := fs.ReadFile(fsys, "install/"+s.Name)
		if err != nil {
			return nil, fmt.Errorf("pages: %w", err)
		}
		stamped, err := s.Stamp(src, version, texts)
		if err != nil {
			return nil, fmt.Errorf("pages: install/%s: %w", s.Name, err)
		}
		want, ok := m.InstallScripts[s.Name]
		if !ok {
			return nil, fmt.Errorf("pages: the manifest of %s lists no SHA-256 for %s", version, s.Name)
		}
		sum := sha256.Sum256(stamped)
		if got := hex.EncodeToString(sum[:]); got != want {
			return nil, fmt.Errorf("pages: install/%s stamped for %s has SHA-256 %s, but the release's signed manifest lists %s, so it isn't the script the release carries; copy the release's files again as install/README.md says", s.Name, version, got, want)
		}
		out[s.Name] = stamped
	}
	return out, nil
}

// registerScripts serves the install scripts at /install.sh and
// /install.ps1.
func (s *Site) registerScripts(mux *http.ServeMux) {
	for _, sc := range release.InstallScripts() {
		body, ok := s.scripts[sc.Name]
		if !ok {
			continue
		}
		mux.HandleFunc("GET /"+sc.Name, func(w http.ResponseWriter, _ *http.Request) {
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
