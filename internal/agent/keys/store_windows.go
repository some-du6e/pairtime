// SPDX-License-Identifier: AGPL-3.0-or-later

package keys

import (
	pwindows "github.com/some-du6e/pairtime/internal/platform/windows"
)

// dpapiEntropy is mixed into every DPAPI blob the keystore makes, so a
// program that finds the file can't decrypt it with a plain
// CryptUnprotectData call even when it runs as the same user. It is in this
// source, so it is no secret, and it can't change without making every
// blob unreadable.
var dpapiEntropy = []byte("pairtime keystore v1")

// platformStores is DPAPI (DESIGN 3.6). It never prompts, and its blobs
// sit in the keystore's directory. Windows Credential Manager, go-keyring's
// Windows store, isn't used: DPAPI needs no module beyond golang.org/x/sys
// and has no size limit.
func platformStores(dir string) []Store {
	return []Store{fileStore{
		kind: KindDPAPI,
		dir:  dir,
		ext:  ".dpapi",
		seal: func(b []byte) ([]byte, error) { return pwindows.Protect(b, dpapiEntropy) },
		open: func(b []byte) ([]byte, error) { return pwindows.Unprotect(b, dpapiEntropy) },
	}}
}
