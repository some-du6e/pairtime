// SPDX-License-Identifier: AGPL-3.0-or-later

package home

import (
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

// checkOwnerOnly checks that a file from WritePrivate has a protected DACL
// with a single entry: full access for the current user. A file from
// WriteFile inherits its directory's ACL, so there is nothing to check.
func checkOwnerOnly(t *testing.T, name string, private bool) {
	t.Helper()
	if !private {
		return
	}
	got, err := windows.GetNamedSecurityInfo(name, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	// SDDL writes well-known accounts as aliases: the CI runner's built-in
	// Administrator comes out as LA, not as its SID. So the wanted DACL goes
	// through the same conversion before the two are compared.
	want, err := windows.SecurityDescriptorFromString("D:P(A;;FA;;;" + user.User.Sid.String() + ")")
	if err != nil {
		t.Fatal(err)
	}
	if got.String() != want.String() {
		t.Errorf("%s has the DACL %s, want %s: protected, with one entry that gives the current user full access", name, got, want)
	}
}

// fileAllAccess is FILE_ALL_ACCESS, FA in SDDL.
const fileAllAccess = windows.STANDARD_RIGHTS_REQUIRED | windows.SYNCHRONIZE | 0x1ff

// ace is the part of an access-allowed ACE that the checks compare.
type ace struct {
	Type  uint8
	Flags uint8
	Mask  windows.ACCESS_MASK
	SID   string
}

// checkPrivateDir checks that a directory Create made has a protected
// DACL, so it takes nothing from its parent, with one entry: full access
// for the current user, inherited by the files (OI) and directories (CI)
// made inside it.
func checkPrivateDir(t *testing.T, dir string) {
	t.Helper()
	protected, aces := readDACL(t, dir)
	if !protected {
		t.Errorf("%s's DACL isn't protected, so it takes entries from its parent's", dir)
	}
	checkUserOnly(t, dir, aces, windows.OBJECT_INHERIT_ACE|windows.CONTAINER_INHERIT_ACE)
}

// checkPrivateFile checks that a file's DACL has one entry, full access for
// the current user, whether the file inherited it or has its own.
func checkPrivateFile(t *testing.T, name string) {
	t.Helper()
	_, aces := readDACL(t, name)
	checkUserOnly(t, name, aces, 0)
}

// checkUserOnly checks that aces is one entry that allows the current user
// full access, with the inheritance flags flags. Whether the entry was
// itself inherited doesn't matter.
func checkUserOnly(t *testing.T, name string, aces []ace, flags uint8) {
	t.Helper()
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	want := ace{Type: windows.ACCESS_ALLOWED_ACE_TYPE, Flags: flags, Mask: fileAllAccess, SID: user.User.Sid.String()}
	if len(aces) != 1 {
		t.Errorf("%s's DACL has the entries %+v, want only %+v", name, aces, want)
		return
	}
	got := aces[0]
	got.Flags &^= windows.INHERITED_ACE
	if got != want {
		t.Errorf("%s's DACL has the entry %+v (inherited flag left out), want %+v", name, got, want)
	}
}

// readDACL returns whether the DACL of name is protected, and its entries.
func readDACL(t *testing.T, name string) (bool, []ace) {
	t.Helper()
	sd, err := windows.GetNamedSecurityInfo(extendedPath(name), windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatalf("reading the DACL of %s: %v", name, err)
	}
	control, _, err := sd.Control()
	if err != nil {
		t.Fatal(err)
	}
	acl, _, err := sd.DACL()
	if err != nil {
		t.Fatalf("%s's security descriptor has no DACL: %v", name, err)
	}
	if acl == nil {
		t.Fatalf("%s has a null DACL, which lets everyone in", name)
	}
	var aces []ace
	for i := range uint32(acl.AceCount) {
		var a *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(acl, i, &a); err != nil {
			t.Fatal(err)
		}
		// Every ACE type the checks accept starts its SID at SidStart.
		sid := (*windows.SID)(unsafe.Pointer(&a.SidStart))
		aces = append(aces, ace{Type: a.Header.AceType, Flags: a.Header.AceFlags, Mask: a.Mask, SID: sid.String()})
	}
	return control&windows.SE_DACL_PROTECTED != 0, aces
}

// permissions describes who may open path, as SDDL, to compare before and
// after.
func permissions(t *testing.T, path string) string {
	t.Helper()
	sd, err := windows.GetNamedSecurityInfo(extendedPath(path), windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatalf("reading the DACL of %s: %v", path, err)
	}
	return sd.String()
}
