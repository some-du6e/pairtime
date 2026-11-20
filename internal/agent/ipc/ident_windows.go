// SPDX-License-Identifier: AGPL-3.0-or-later

package ipc

import (
	"os"
	"sync"

	"golang.org/x/sys/windows"
)

// identity is who a process runs as: its user's SID, in string form.
type identity struct {
	sid string
}

func (id identity) String() string { return id.sid }

// self is the user this process runs as.
var self = sync.OnceValues(func() (identity, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return identity{}, err
	}
	return identity{sid: user.User.Sid.String()}, nil
})

// pidIdentity returns the user the process pid runs as. A process of
// another user usually can't be opened at all, which is an error here and
// a refusal for the caller.
func pidIdentity(pid uint32) (identity, error) {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return identity{}, os.NewSyscallError("OpenProcess", err)
	}
	defer windows.CloseHandle(h)
	var token windows.Token
	if err := windows.OpenProcessToken(h, windows.TOKEN_QUERY, &token); err != nil {
		return identity{}, os.NewSyscallError("OpenProcessToken", err)
	}
	defer token.Close()
	user, err := token.GetTokenUser()
	if err != nil {
		return identity{}, err
	}
	return identity{sid: user.User.Sid.String()}, nil
}
