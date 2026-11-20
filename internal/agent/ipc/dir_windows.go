// SPDX-License-Identifier: AGPL-3.0-or-later

package ipc

import (
	"fmt"
	"os"
	"strings"

	"golang.org/x/sys/windows"
)

// Owners a socket directory may have besides this user. A directory made by
// an elevated process of this user belongs to Administrators, and one made
// by an installer running as the system belongs to SYSTEM; both could open
// anything of this user's anyway.
const (
	sidAdministrators = "S-1-5-32-544"
	sidSystem         = "S-1-5-18"
)

// dirSDDL is the DACL the socket's directory gets: full access for SYSTEM
// and this user, inherited by everything made in it, the socket file
// included, and none of the parent's entries.
func dirSDDL(want identity) string {
	return "D:P(A;OICI;FA;;;SY)(A;OICI;FA;;;" + want.sid + ")"
}

// secureDir makes dir, the socket's directory, when it is missing, and
// gives it dirSDDL's DACL, so no other user can open the socket. On
// AF_UNIX, whoever may write the socket file may connect to it (CP 6.3).
// %LOCALAPPDATA% is this user's only already, and CP 6.3 says to set it
// explicitly anyway. A directory owned by another user is refused, because
// its owner could put the old DACL back.
func secureDir(dir string, want identity) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	osd, err := windows.GetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION)
	if err != nil {
		return fmt.Errorf("ipc: reading who owns %s: %w", dir, err)
	}
	owner, _, err := osd.Owner()
	if err != nil {
		return fmt.Errorf("ipc: reading who owns %s: %w", dir, err)
	}
	if o := owner.String(); o != want.sid && o != sidAdministrators && o != sidSystem {
		return fmt.Errorf("ipc: %s belongs to %s, not to this user (%s), so the agent won't put its socket there", dir, o, want.sid)
	}
	wantSD, err := windows.SecurityDescriptorFromString(dirSDDL(want))
	if err != nil {
		return err
	}
	got, err := windows.GetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return fmt.Errorf("ipc: reading the DACL of %s: %w", dir, err)
	}
	if sameDACL(got, wantSD) {
		return nil
	}
	dacl, _, err := wantSD.DACL()
	if err != nil {
		return err
	}
	// Setting a DACL also passes its inheritable entries down to what is
	// in the directory already, once.
	if err := windows.SetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil); err != nil {
		return fmt.Errorf("ipc: setting the DACL of %s: %w", dir, err)
	}
	return nil
}

// sameDACL reports whether got is protected and has want's entries. The two
// are compared as SDDL, rendered the same way, because SDDL writes some
// accounts as aliases (the built-in Administrator as LA, say). The flags
// are compared apart from the entries: Windows marks a DACL it has
// propagated as auto-inherited (AI), which want doesn't say.
func sameDACL(got, want *windows.SECURITY_DESCRIPTOR) bool {
	gotFlags, gotACEs := splitDACL(got.String())
	_, wantACEs := splitDACL(want.String())
	return strings.Contains(gotFlags, "P") && gotACEs == wantACEs
}

// splitDACL splits the DACL part of an SDDL string into its flags and its
// entries.
func splitDACL(sddl string) (flags, aces string) {
	i := strings.Index(sddl, "D:")
	if i < 0 {
		return "", ""
	}
	d := sddl[i+2:]
	if j := strings.Index(d, "S:"); j >= 0 {
		d = d[:j] // a SACL part, which these descriptors don't have
	}
	if j := strings.IndexByte(d, '('); j >= 0 {
		return d[:j], d[j:]
	}
	return d, ""
}

// secureSocket does nothing on Windows: the socket file inherits the
// directory's DACL when it is made.
func secureSocket(string) error { return nil }
