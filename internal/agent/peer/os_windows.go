// SPDX-License-Identifier: AGPL-3.0-or-later

package peer

import (
	"net/netip"

	"github.com/some-du6e/pairtime/internal/platform"
	"github.com/some-du6e/pairtime/internal/platform/windows"
)

func ownerOS(client, server netip.AddrPort) (int, []platform.Proc, error) {
	return windows.LoopbackOwner(client, server)
}

func selfOS() string { return windows.SelfOwner() }
