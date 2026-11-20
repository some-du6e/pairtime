// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build !windows

package ipc

import (
	"context"
	"net"
)

// Named pipes are the Windows fallback (CP 6.3); there is none elsewhere.

func pipeName(string) (string, error) { return "", nil }

func listenPipe(string) (net.Listener, error) { return nil, errNoPipe }

func dialPipe(context.Context, string, identity) (net.Conn, error) { return nil, errNoPipe }
