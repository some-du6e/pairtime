// SPDX-License-Identifier: AGPL-3.0-or-later

package home

import (
	"strings"
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
	sd, err := windows.GetNamedSecurityInfo(name, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	sddl := sd.String()
	want := "(A;;FA;;;" + user.User.Sid.String() + ")"
	if !strings.HasPrefix(sddl, "D:P") || !strings.HasSuffix(sddl, want) || strings.Count(sddl, "(") != 1 {
		t.Errorf("%s has the DACL %s, want a protected one with the single entry %s", name, sddl, want)
	}
}
