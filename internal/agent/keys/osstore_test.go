// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build !(linux && desktop)

package keys

import (
	"runtime"
	"testing"
)

// osStoreForTest says where a first run with this OS's stores should put
// K_dev on its CI runner: DPAPI on windows-latest and the login Keychain on
// macos-latest (platform.yml). On Linux without the desktop tag (ci.yml)
// it depends on whether the runner has a session bus with a Secret
// Service, so "" lets either the Secret Service or the key file pass.
// keys_desktop_test.go has the desktop build's version, which starts
// fakesecret.
func osStoreForTest(*testing.T) Kind {
	switch runtime.GOOS {
	case "windows":
		return KindDPAPI
	case "darwin":
		return KindKeychain
	}
	return ""
}
