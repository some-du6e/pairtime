// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build unix

package ipc

import (
	"os"
	"strconv"
)

// identity is who a process runs as: its effective uid.
type identity struct {
	uid int
}

func (id identity) String() string { return "uid " + strconv.Itoa(id.uid) }

// self is the user this process runs as.
func self() (identity, error) {
	return identity{uid: os.Geteuid()}, nil
}
