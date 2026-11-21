// SPDX-License-Identifier: AGPL-3.0-or-later

package doctor

import (
	"errors"
	"unsafe"

	"golang.org/x/sys/windows"
)

// OSSignature checks the binary at path's Authenticode signature the way
// Windows does, with WinVerifyTrust's default policy, no prompt, no
// revocation check and nothing fetched over the network, so doctor sends
// nothing anywhere.
func OSSignature(path string) (Verdict, string) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return Unchecked, "its signature couldn't be checked: " + err.Error()
	}
	file := &windows.WinTrustFileInfo{Size: uint32(unsafe.Sizeof(windows.WinTrustFileInfo{})), FilePath: p}
	data := &windows.WinTrustData{
		Size:                            uint32(unsafe.Sizeof(windows.WinTrustData{})),
		UIChoice:                        windows.WTD_UI_NONE,
		RevocationChecks:                windows.WTD_REVOKE_NONE,
		UnionChoice:                     windows.WTD_CHOICE_FILE,
		StateAction:                     windows.WTD_STATEACTION_VERIFY,
		ProvFlags:                       windows.WTD_REVOCATION_CHECK_NONE | windows.WTD_CACHE_ONLY_URL_RETRIEVAL,
		FileOrCatalogOrBlobOrSgnrOrCert: unsafe.Pointer(file),
	}
	verr := windows.WinVerifyTrustEx(windows.InvalidHWND, &windows.WINTRUST_ACTION_GENERIC_VERIFY_V2, data)
	data.StateAction = windows.WTD_STATEACTION_CLOSE
	windows.WinVerifyTrustEx(windows.InvalidHWND, &windows.WINTRUST_ACTION_GENERIC_VERIFY_V2, data)
	switch {
	case verr == nil:
		return Trusted, "signed, and Windows trusts the signature"
	case errors.Is(verr, windows.Errno(windows.TRUST_E_NOSIGNATURE)), errors.Is(verr, windows.Errno(windows.TRUST_E_SUBJECT_FORM_UNKNOWN)):
		return Unsigned, "not code-signed"
	case errors.Is(verr, windows.Errno(windows.TRUST_E_BAD_DIGEST)):
		return Broken, "signed, but it doesn't match its signature"
	}
	return Untrusted, "signed, but Windows doesn't trust the signature: " + verr.Error()
}
