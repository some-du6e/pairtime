// SPDX-License-Identifier: AGPL-3.0-or-later

package ipc

import (
	"errors"
	"path/filepath"

	"github.com/some-du6e/pairtime/internal/agent/home"
)

// SocketName is the agent's socket in the runtime directory (CP 6.3, 7).
const SocketName = "agent.sock"

// pipeNoteName is the file in the runtime directory in which an agent on
// the named pipe says so (Endpoint.PipeNote).
const pipeNoteName = "pipe.json"

// Endpoint says where one agent listens, and where its senders spool.
type Endpoint struct {
	// Socket is the AF_UNIX socket: agent.sock in the runtime directory.
	Socket string
	// Pipe is the named pipe the agent falls back to on Windows, and ""
	// elsewhere.
	Pipe string
	// PipeNote is the file in which an agent that listens on Pipe says so,
	// and which process it is: pipe.json in the runtime directory, written
	// so that only this user can open it. The agent makes no pipe while
	// its socket works, and its name is easy to guess, so anyone could make
	// a pipe of that name and wait for hooks to come to it. A sender
	// therefore tries the pipe only while the note is there, and hands a
	// message only to the process it names. "" where there is no pipe.
	PipeNote string
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
	ep := Endpoint{Socket: filepath.Join(d.Runtime, SocketName), Pipe: pipe, Inbox: d.Inbox()}
	if pipe != "" {
		ep.PipeNote = filepath.Join(d.Runtime, pipeNoteName)
	}
	return ep, nil
}
