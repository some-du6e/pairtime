// SPDX-License-Identifier: AGPL-3.0-or-later

package focus

import (
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
)

// AppSecretLabel is the HKDF label of K_app, the secret app_hash is keyed
// with. Like the git adapter's project secret, it is derived from the
// device key K_dev under a "pairtime/v1/" label of its own, which no other
// key uses (docs/spec/ledger-v1.md section 3).
const AppSecretLabel = "pairtime/v1/app"

// AppSecretLen is the length of K_app, in bytes.
const AppSecretLen = 32

// AppSecret derives K_app from K_dev the way ledger-v1.md section 3
// derives the ledger's keys: HKDF-SHA256 with RFC 5869's default salt of
// 32 zero bytes, info AppSecretLabel, and 32 bytes of output.
func AppSecret(kDev []byte) ([]byte, error) {
	if len(kDev) != 32 {
		return nil, errors.New("K_dev must be 32 bytes")
	}
	return hkdf.Key(sha256.New, kDev, nil, AppSecretLabel, AppSecretLen)
}

// appDomain is hashed first, so an app_hash can't equal another keyed
// value made under the same secret.
const appDomain = "pairtime/v1/app/exe\x00"

// appHash is os.focus's app_hash for an app on goos whose executable is
// called name, as appName spells it: "a" and the first 16 bytes of
// HMAC-SHA256(K_app, appDomain || goos || 0x00 || name), in hex. It is a
// local field (SM 1.2), and keyed besides, so that nobody who has the
// ledger but not the device's key can test a guess of which app it was
// (AGENTS.md). Two windows of one app have the same app_hash.
func appHash(secret []byte, goos, name string) string {
	m := hmac.New(sha256.New, secret)
	m.Write([]byte(appDomain))
	m.Write([]byte(goos))
	m.Write([]byte{0})
	m.Write([]byte(name))
	return "a" + hex.EncodeToString(m.Sum(nil)[:16])
}
