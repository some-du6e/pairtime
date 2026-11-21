// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"errors"

	"golang.org/x/sys/windows"
)

// checkUser refuses to run with the elevated half of a split token, as in a
// terminal started with "Run as administrator". The agent install starts
// would keep administrator rights for the rest of the session, while
// pairtime never needs them (DESIGN 3.2), and the hook starts no agent from
// such a token either (mayStartAgent). A token that isn't split is the only
// one the user has, and the login item starts the agent with it too.
func checkUser(string) error {
	if elevationType() == tokenElevationFull {
		return errors.New("this runs as administrator, which pairtime never needs, and the agent install starts would keep administrator rights until you log out; run it again from a terminal you didn't start as administrator")
	}
	return nil
}

// protectDir gives dir the DACL that home gives the directories it makes
// (home.Dirs.Create): protected, with one entry that gives this user full
// access and that everything made inside inherits. A folder an install
// script made has its parent's entries instead, which may let other users
// in. Setting the DACL hands its entry down, once, to what dir holds
// already.
func protectDir(dir string) error {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return err
	}
	sd, err := windows.SecurityDescriptorFromString("D:P(A;OICI;FA;;;" + user.User.Sid.String() + ")")
	if err != nil {
		return err
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil)
}
