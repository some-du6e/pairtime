// SPDX-License-Identifier: AGPL-3.0-or-later

package ipc

import (
	"errors"
	"path/filepath"

	"github.com/some-du6e/pairtime/internal/agent/home"
)

// SocketName is the agent's socket in the runtime directory (CP 6.3, 7).
const SocketName = "agent.sock"

// Endpoint says where one agent listens, and where its senders spool.
type Endpoint struct {
	// Socket is the AF_UNIX socket: agent.sock in the runtime directory.
	Socket string
	// Pipe is the named pipe the agent falls back to on Windows, and ""
	// elsewhere.
	Pipe string
	// Inbox is the directory of the spool that Send falls back to.
	Inbox string
}

// EndpointFor returns the endpoint of the agent that uses d. Senders and
// the agent compute it the same way, so they meet without a file that says
// where the agent is.
func EndpointFor(d home.Dirs) (Endpoint, error) {
	if d.Runtime == "" || d.Data == "" {
		return Endpoint{}, errors.New("ipc: the data and runtime directories must be set")
	}
	pipe, err := pipeName(d.Runtime)
	if err != nil {
		return Endpoint{}, err
	}
	return Endpoint{Socket: filepath.Join(d.Runtime, SocketName), Pipe: pipe, Inbox: d.Inbox()}, nil
}
