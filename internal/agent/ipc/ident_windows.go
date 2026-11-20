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
	id, _, err := pidInfo(pid)
	return id, err
}

// pidInfo returns the user the process pid runs as and when it started,
// in 100 ns units since 1601 (a FILETIME). With the start time, a pid
// names one process even after Windows gives it to another.
func pidInfo(pid uint32) (identity, uint64, error) {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return identity{}, 0, os.NewSyscallError("OpenProcess", err)
	}
	defer windows.CloseHandle(h)
	var token windows.Token
	if err := windows.OpenProcessToken(h, windows.TOKEN_QUERY, &token); err != nil {
		return identity{}, 0, os.NewSyscallError("OpenProcessToken", err)
	}
	defer token.Close()
	user, err := token.GetTokenUser()
	if err != nil {
		return identity{}, 0, err
	}
	started, err := processStart(h)
	if err != nil {
		return identity{}, 0, err
	}
	return identity{sid: user.User.Sid.String()}, started, nil
}

// processStart returns when the process h started, in 100 ns units since
// 1601.
func processStart(h windows.Handle) (uint64, error) {
	var created, exited, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(h, &created, &exited, &kernel, &user); err != nil {
		return 0, os.NewSyscallError("GetProcessTimes", err)
	}
	return uint64(created.HighDateTime)<<32 | uint64(created.LowDateTime), nil
}
