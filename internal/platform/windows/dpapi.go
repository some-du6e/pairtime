// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build windows

package windows

import (
	"errors"
	"os"
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

// ErrEmpty means Protect or Unprotect got no bytes. DPAPI would accept an
// empty secret, but an empty key is always a bug.
var ErrEmpty = errors.New("dpapi: no data")

// Protect encrypts data with CryptProtectData under the current user's
// logon credentials (DESIGN 3.6; CP 5.2). Only this user on this Windows
// installation can decrypt the result: another account can't, and neither
// can the same account after Windows is reinstalled, because the user's
// DPAPI master key goes with the installation. entropy is mixed in, and
// Unprotect needs the same bytes. Protect never shows a prompt.
//
// The result is an opaque blob that is safe to write to a file the user
// owns. Protect doesn't keep or change data.
func Protect(data, entropy []byte) ([]byte, error) {
	if len(data) == 0 {
		return nil, ErrEmpty
	}
	in := windows.DataBlob{Size: uint32(len(data)), Data: &data[0]}
	var out windows.DataBlob
	err := windows.CryptProtectData(&in, nil, blob(entropy), 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out)
	runtime.KeepAlive(data)
	runtime.KeepAlive(entropy)
	if err != nil {
		return nil, os.NewSyscallError("CryptProtectData", err)
	}
	defer free(&out, false)
	return clone(&out), nil
}

// Unprotect decrypts a blob made by Protect with the same entropy, for the
// same user on the same installation. DPAPI checks the blob's integrity, so
// a changed byte, other entropy or another account is an error, never wrong
// plaintext. The copy of the plaintext that Windows allocated is zeroed
// before it is freed; the caller should zero the returned slice when done.
func Unprotect(data, entropy []byte) ([]byte, error) {
	if len(data) == 0 {
		return nil, ErrEmpty
	}
	in := windows.DataBlob{Size: uint32(len(data)), Data: &data[0]}
	var out windows.DataBlob
	err := windows.CryptUnprotectData(&in, nil, blob(entropy), 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out)
	runtime.KeepAlive(data)
	runtime.KeepAlive(entropy)
	if err != nil {
		return nil, os.NewSyscallError("CryptUnprotectData", err)
	}
	defer free(&out, true)
	return clone(&out), nil
}

// blob points a DATA_BLOB at b, or returns nil for no bytes, which DPAPI
// reads as no optional entropy.
func blob(b []byte) *windows.DataBlob {
	if len(b) == 0 {
		return nil
	}
	return &windows.DataBlob{Size: uint32(len(b)), Data: &b[0]}
}

// clone copies a blob Windows allocated into Go memory.
func clone(b *windows.DataBlob) []byte {
	if b.Data == nil || b.Size == 0 {
		return []byte{}
	}
	out := make([]byte, b.Size)
	copy(out, unsafe.Slice(b.Data, b.Size))
	return out
}

// free releases a blob Windows allocated with LocalAlloc, zeroing it first
// when it holds plaintext.
func free(b *windows.DataBlob, wipe bool) {
	if b.Data == nil {
		return
	}
	if wipe {
		clear(unsafe.Slice(b.Data, b.Size))
	}
	windows.LocalFree(windows.Handle(unsafe.Pointer(b.Data)))
	b.Data, b.Size = nil, 0
}
