// SPDX-License-Identifier: AGPL-3.0-or-later

package git

import (
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
)

// ProjectSecretLabel is the HKDF label of the project secret. ledger-v1.md
// section 3 lists it beside the ledger's own labels, with the domain
// strings below: every key derived from K_dev has a "pairtime/v1/" label
// of its own, and no label is used for two keys.
const ProjectSecretLabel = "pairtime/v1/project"

// SecretLen is the length of the project secret, in bytes.
const SecretLen = 32

// ProjectSecret derives the project secret from the device key K_dev the
// way ledger-v1.md section 3 derives K_leaf and the rest: HKDF-SHA256 with
// RFC 5869's default salt of 32 zero bytes, info ProjectSecretLabel, and 32
// bytes of output. Project keys are HMACs under it, so they are keyed per
// device (AGENTS.md), and rotating or deleting K_dev changes every key.
func ProjectSecret(kDev []byte) ([]byte, error) {
	if len(kDev) != 32 {
		return nil, errors.New("K_dev must be 32 bytes")
	}
	return hkdf.Key(sha256.New, kDev, nil, ProjectSecretLabel, SecretLen)
}

// Each kind of project key hashes a different domain string first, so a
// root commit and a folder can never produce the same key.
const (
	domainRoots  = "pairtime/v1/project/roots\x00"
	domainFolder = "pairtime/v1/project/folder\x00"
)

// Key prefixes say which kind of identity a key came from, so `pairtime
// ledger show` and doctor can tell them apart. They leak nothing a
// project registration doesn't already say.
const (
	prefixRoots  = "g"
	prefixFolder = "d"
)

// rootsKey is the project key of a repo with the given root commits. The
// roots are sorted and deduplicated first, so the order git lists them in
// doesn't matter.
func rootsKey(secret []byte, roots []string) string {
	sorted := slices.Clone(roots)
	slices.Sort(sorted)
	sorted = slices.Compact(sorted)
	m := hmac.New(sha256.New, secret)
	m.Write([]byte(domainRoots))
	for _, r := range sorted {
		m.Write([]byte(r))
		m.Write([]byte{'\n'})
	}
	return prefixRoots + hex.EncodeToString(m.Sum(nil)[:16])
}

// folderKey is the project key of a folder identified by its path: a
// folder without git, a repo with no commit yet, or a shallow clone. It is
// the "salted path hash" of DESIGN 5.8, with the device's project secret
// as the salt.
func folderKey(secret []byte, dir string) string {
	m := hmac.New(sha256.New, secret)
	m.Write([]byte(domainFolder))
	m.Write([]byte(foldPath(dir)))
	return prefixFolder + hex.EncodeToString(m.Sum(nil)[:16])
}

// caseInsensitive is whether paths on this OS name the same folder in any
// case. It is true of Windows and of macOS's default file systems; a
// case-sensitive APFS volume is rare enough that treating its "Game" and
// "game" folders as one project is the smaller error.
var caseInsensitive = runtime.GOOS == "windows" || runtime.GOOS == "darwin"

// foldPath is the form of an absolute path that keys and comparisons use:
// cleaned, with forward slashes, and in lower case where the OS ignores
// case.
func foldPath(dir string) string {
	p := filepath.ToSlash(filepath.Clean(dir))
	if caseInsensitive {
		p = strings.ToLower(p)
	}
	return p
}
