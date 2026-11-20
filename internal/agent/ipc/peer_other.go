// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build unix && !linux && !darwin

package ipc

import (
	"errors"
	"net"
)

// unixPeer can't tell who is at the other end of a socket on this OS, so
// the agent and its senders refuse every connection. pairtime ships for
// Windows, macOS and Linux only (CP 7).
func unixPeer(*net.UnixConn) (identity, int, error) {
	return identity{}, 0, errors.New("ipc: pairtime can't read socket peer credentials on this OS")
}
