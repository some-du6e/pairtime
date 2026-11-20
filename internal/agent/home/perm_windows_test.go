// SPDX-License-Identifier: AGPL-3.0-or-later

package home

import (
	"testing"

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
