// SPDX-License-Identifier: AGPL-3.0-or-later

// Package ipc is how hooks and the CLI hand messages to the running agent
// (DESIGN 3.2, 3.3; CP 6.2, 6.3): a per-user AF_UNIX socket on all three
// OSes, and on Windows a named pipe for the builds where AF_UNIX falls
// short.
//
// A sender writes each message as a frame, a 4-byte big-endian length and
// then the message as JSON, and waits for the agent's reply frame. The
// agent replies only once its Sink has stored the message. When no ack
// comes in time, because the agent is down, busy or killed mid-message,
// Send appends the message to the spool instead (package inbox), and the
// agent drains the spool later. A message can therefore arrive twice, and
// the Sink keeps one copy of each id.
//
// No other user may hand the agent messages or read the ones hooks send:
//
//   - The socket lives in a directory only this user can open (0700 on
//     macOS and Linux, a protected DACL on Windows), whose owner must be
//     this user, so nobody else can connect to it or put a socket of their
//     own in its place.
//   - The agent asks the OS who each connection comes from (SO_PEERCRED,
//     LOCAL_PEERCRED, SIO_AF_UNIX_GETPEERPID, GetNamedPipeClientProcessId)
//     and drops any that isn't this user.
//   - A sender asks the OS who runs the agent, the same way, and sends
//     nothing to a process of another user.
//   - The named pipe has a DACL that admits this user only, is created with
//     FILE_FLAG_FIRST_PIPE_INSTANCE so a pipe someone else made first is
//     noticed, and refuses remote clients.
//
// A hook must never slow its harness down, so every wait is bounded:
// Send gives up on the agent after its timeout, DESIGN 3.2's 100 ms for a
// hook, and spools the message.
package ipc
