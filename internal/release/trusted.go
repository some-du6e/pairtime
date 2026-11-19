// SPDX-License-Identifier: AGPL-3.0-or-later

// Package release defines pairtime's update manifest: the signed list of
// release archives a client uses to check a download (CP 4.6). It holds
// the manifest's fields and rules, the DSSE envelope and Ed25519 keys that
// sign it, and the release public keys compiled into the client.
// docs/spec/update-manifest.md is the spec; tools/release writes the files
// and .github/workflows/release.yml runs it.
//
// Everything here is standard library, as DESIGN 3.12 requires of
// pairtime's cryptography.
package release

import _ "embed"

// releaseKeys is release-keys.txt, compiled into every binary that imports
// this package.
//
//go:embed release-keys.txt
var releaseKeys string

// TrustedKeys returns the release public keys compiled into this binary.
// A client accepts a manifest only with a signature from one of them. The
// list is empty until `tools/release keygen` adds the first key.
func TrustedKeys() ([]PublicKey, error) {
	return ParsePublicKeys(releaseKeys)
}
