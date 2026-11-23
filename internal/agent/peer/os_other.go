// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build !linux && !darwin && !windows

package peer

import (
	"errors"
	"net/netip"

	"github.com/some-du6e/pairtime/internal/platform"
)

// ownerOS fails: pairtime has no connection lookup for this OS, so every
// check fails here.
func ownerOS(netip.AddrPort, netip.AddrPort) (int, []platform.Proc, error) {
	return 0, nil, errors.New("pairtime can't tell which process holds a connection on this OS")
}

func selfOS() string { return "" }
