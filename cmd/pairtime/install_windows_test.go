// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"

	"github.com/some-du6e/pairtime/internal/install"
)

// runValue returns pairtime's Run value under HKEY_CURRENT_USER, which is
// its login item on Windows, and whether it is there.
func runValue() (string, bool) {
	k, err := registry.OpenKey(registry.CURRENT_USER, install.RunKey, registry.QUERY_VALUE)
	if err != nil {
		return "", false
	}
	defer k.Close()
	v, _, err := k.GetStringValue(install.RunValue)
	return v, err == nil
}

// deleteRunValue deletes pairtime's Run value.
func deleteRunValue() {
	k, err := registry.OpenKey(registry.CURRENT_USER, install.RunKey, registry.SET_VALUE)
	if err != nil {
		return
	}
	defer k.Close()
	k.DeleteValue(install.RunValue)
}

// processAlive reports whether the process pid is running.
func processAlive(pid int) bool {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(h)
	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err != nil {
		return false
	}
	return code == stillActive
}

// sidSystem is the SID of the system account, LocalSystem.
const sidSystem = "S-1-5-18"

// checkPrivateDir checks that only this user and the system may open dir:
// its DACL is protected, so it takes none of its parent's entries, and it
// has entries for no one else. The agent's socket folder, which is the
// data directory on Windows, gets the system's entry too (internal/agent/
// ipc, dirSDDL).
func checkPrivateDir(t *testing.T, dir string) {
	t.Helper()
	protected, sids := daclOf(t, dir)
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	if !protected {
		t.Errorf("%s's DACL isn't protected, so it takes its parent's entries: %q", dir, sids)
	}
	for _, sid := range sids {
		if sid != user.User.Sid.String() && sid != sidSystem {
			t.Errorf("%s's DACL has an entry for %s, want only this user's and the system's", dir, sid)
		}
	}
}

// daclOf returns whether the DACL of dir is protected, and the SIDs of its
// entries, in order.
func daclOf(t *testing.T, dir string) (bool, []string) {
	t.Helper()
	sd, err := windows.GetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatalf("reading the DACL of %s: %v", dir, err)
	}
	control, _, err := sd.Control()
	if err != nil {
		t.Fatal(err)
	}
	acl, _, err := sd.DACL()
	if err != nil || acl == nil {
		t.Fatalf("%s has no DACL: %v", dir, err)
	}
	var sids []string
	for i := range uint32(acl.AceCount) {
		var a *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(acl, i, &a); err != nil {
			t.Fatal(err)
		}
		sids = append(sids, (*windows.SID)(unsafe.Pointer(&a.SidStart)).String())
	}
	return control&windows.SE_DACL_PROTECTED != 0, sids
}
