// SPDX-License-Identifier: AGPL-3.0-or-later

package ipc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/some-du6e/pairtime/internal/agent/inbox"
)

// HookTimeout is how long a hook waits for the agent's ack before it
// spools its message instead (DESIGN 3.2).
const HookTimeout = 100 * time.Millisecond

// ErrRefused means the agent answered, but didn't store the message.
var ErrRefused = errors.New("ipc: the agent didn't take the message")

// ErrNotAgent means the process at the other end runs as another user, or
// the OS wouldn't say who it runs as. Such a process could have put its own
// socket or pipe where the agent's should be, to read what hooks send, so
// nothing is sent to it.
var ErrNotAgent = errors.New("ipc: the other end isn't this user's agent")

// Send hands m to the agent at ep, and appends it to the spool in ep.Inbox
// when no agent acks it within timeout. It returns how the message went:
// inbox.ViaSocket or inbox.ViaPipe once the agent has acked it, or
// inbox.ViaInbox once it is in the spool. It fails only when both ways
// fail, and never takes much longer than timeout and one file append.
//
// A message may end up both acked and spooled, when the ack comes too late;
// the agent keeps one copy.
func Send(ep Endpoint, m inbox.Message, timeout time.Duration) (inbox.Via, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	c, err := Dial(ctx, ep)
	if err == nil {
		err = c.Send(ctx, m)
		c.Close()
		if err == nil {
			return c.via, nil
		}
	}
	if aerr := inbox.Append(ep.Inbox, m); aerr != nil {
		return "", errors.Join(err, aerr)
	}
	return inbox.ViaInbox, nil
}

// Conn is a connection to the agent, from Dial. It may carry many messages
// in turn, but not at once.
type Conn struct {
	c   net.Conn
	via inbox.Via
}

// Dial connects to the agent at ep: to its socket, or on Windows to its
// named pipe when the socket isn't there or doesn't answer and the agent's
// note says it listens on the pipe (Endpoint.PipeNote). Before it returns
// it checks that the agent runs as this user, and fails with ErrNotAgent
// when it doesn't.
func Dial(ctx context.Context, ep Endpoint) (*Conn, error) {
	me, err := self()
	if err != nil {
		return nil, err
	}
	return dial(ctx, ep, me)
}

// dial is Dial for an agent that must run as want.
func dial(ctx context.Context, ep Endpoint, want identity) (*Conn, error) {
	c, err := dialSocket(ctx, ep.Socket, want)
	if err == nil {
		return &Conn{c: c, via: inbox.ViaSocket}, nil
	}
	if ep.Pipe == "" {
		return nil, err
	}
	pc, perr := dialPipe(ctx, ep, want)
	if perr != nil {
		return nil, errors.Join(err, perr)
	}
	return &Conn{c: pc, via: inbox.ViaPipe}, nil
}

// Via says whether the connection is the socket or the pipe.
func (c *Conn) Via() inbox.Via { return c.via }

// Send hands m to the agent and waits for its ack until ctx is done. It
// returns nil only once the agent has stored m. After an error the
// connection may be out of step, so the caller closes it.
func (c *Conn) Send(ctx context.Context, m inbox.Message) error {
	b, err := m.Encode()
	if err != nil {
		return err
	}
	rb, err := c.roundTrip(ctx, b, inbox.MaxMessage, maxReply)
	if err != nil {
		return err
	}
	var rep reply
	if err := json.Unmarshal(rb, &rep); err != nil {
		return fmt.Errorf("ipc: the agent's reply isn't JSON: %w", err)
	}
	switch {
	case !rep.OK:
		return fmt.Errorf("%w: %s", ErrRefused, rep.Error)
	case rep.ID != m.ID:
		return errors.New("ipc: the agent acked another message")
	}
	return nil
}

// roundTrip writes body as one frame of at most out bytes and reads the
// agent's reply frame of at most in bytes, until ctx is done.
func (c *Conn) roundTrip(ctx context.Context, body []byte, out, in int) ([]byte, error) {
	deadline, _ := ctx.Deadline() // the zero time means none
	if err := c.c.SetDeadline(deadline); err != nil {
		return nil, err
	}
	// A cancel before the deadline cuts the wait short too.
	stop := context.AfterFunc(ctx, func() { c.c.SetDeadline(time.Now()) })
	defer stop()
	if err := WriteFrame(c.c, body, out); err != nil {
		return nil, err
	}
	return ReadFrame(c.c, in)
}

// Close closes the connection.
func (c *Conn) Close() error { return c.c.Close() }
