// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

// checkUserOnlyDir checks that dir has a protected DACL, so it takes no
// entry from its parent's, whose entries all name the current user: what
// home.Dirs.Create gives a directory it makes (#46).
func checkUserOnlyDir(t *testing.T, dir string) {
	t.Helper()
	sd, err := windows.GetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatalf("reading the DACL of %s: %v", dir, err)
	}
	control, _, err := sd.Control()
	if err != nil {
		t.Fatal(err)
	}
	if control&windows.SE_DACL_PROTECTED == 0 {
		t.Errorf("%s's DACL isn't protected, so it takes its parent's entries: %s", dir, sd)
	}
	acl, _, err := sd.DACL()
	if err != nil || acl == nil {
		t.Fatalf("%s has no DACL: %v", dir, err)
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	for i := range uint32(acl.AceCount) {
		var a *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(acl, i, &a); err != nil {
			t.Fatal(err)
		}
		if sid := (*windows.SID)(unsafe.Pointer(&a.SidStart)); !sid.Equals(user.User.Sid) {
			t.Errorf("%s's DACL has an entry for %s, want only the current user's: %s", dir, sid, sd)
		}
	}
}

func TestStartsWith(t *testing.T) {
	tests := []struct {
		name string
		t    uint32
		want bool
	}{
		{"a token that isn't split starts the agent, as with UAC off", tokenElevationDefault, true},
		{"the filtered half of a split token, what the user runs with, starts it", tokenElevationLimited, true},
		{"the elevated half, from a terminal run as administrator, doesn't", tokenElevationFull, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := startsWith(tt.t); got != tt.want {
				t.Errorf("startsWith(%d) = %v, want %v", tt.t, got, tt.want)
			}
		})
	}
	t.Run("the runner's own token reads, and lets this test binary's hooks start the agent", func(t *testing.T) {
		got := elevationType()
		t.Logf("TOKEN_ELEVATION_TYPE %d", got)
		if !startsWith(got) {
			t.Errorf("elevation type %d; TestHookProvenance's hook couldn't start its agent", got)
		}
	})
}
