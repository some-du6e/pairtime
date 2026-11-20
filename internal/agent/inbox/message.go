// SPDX-License-Identifier: AGPL-3.0-or-later

// Package inbox is where messages for the agent wait, and where the agent
// records the ones it took in (DESIGN 3.5). A message is one hook call,
// track call or plugin event, handed over by a process that a harness, the
// teen's coding agent or a plugin started.
//
// A message reaches the agent one of two ways. When the agent is up, the
// sender hands it over the socket (package ipc), and the agent acks it once
// it is stored. When the agent is down, slow or unreachable, the sender
// appends it to the spool, inbox/spool.jsonl, in one O_APPEND write
// (Append), and the agent drains the spool when it starts and every minute
// after that (Drain).
//
// Either way can deliver a message twice. A sender that gave up waiting for
// an ack spools a message the agent had in fact stored, and an agent killed
// between draining a spool file and deleting it drains that file again when
// it restarts. So every message carries a random id, and a Sink stores each
// id once: the Journal does, and so must the ledger writer that replaces it
// (WP-LG5, WP-AG4).
package inbox

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// MaxMessage is the largest message, as JSON, that the socket and the spool
// carry. A hook reads at most 1 MiB of stdin (DESIGN 3.2), which leaves
// room for the envelope and for escaping.
const MaxMessage = 4 << 20

// ErrTooBig means a message is over MaxMessage.
var ErrTooBig = errors.New("inbox: message over 4 MiB")

// Message is what a sender hands the agent.
type Message struct {
	// ID is 32 lowercase hex digits from crypto/rand (NewID). The agent
	// stores each id once.
	ID string `json:"id"`
	// Kind says what sent the message and how to read Body, such as "hook"
	// (WP-AG3). It is 1 to 64 lowercase letters, digits and the
	// characters . : _ -
	Kind string `json:"kind"`
	// Sent is the sender's wall-clock time when it made the message.
	Sent time.Time `json:"sent"`
	// Body is the payload, a JSON value whose shape depends on Kind. It
	// may hold content, such as a prompt, so it stays on the device.
	Body json.RawMessage `json:"body,omitempty"`
}

// New returns a message of the given kind with a new id, sent now.
func New(kind string, body json.RawMessage) Message {
	// UTC drops the monotonic reading, which JSON can't carry anyway.
	return Message{ID: NewID(), Kind: kind, Sent: time.Now().UTC(), Body: body}
}

// NewID returns a new message id: 128 random bits as 32 lowercase hex
// digits.
func NewID() string {
	var b [16]byte
	rand.Read(b[:]) // never fails: crypto/rand panics instead
	return hex.EncodeToString(b[:])
}

// Check reports whether the agent takes m.
func (m Message) Check() error {
	switch {
	case !validID(m.ID):
		return errors.New("inbox: a message id must be 32 lowercase hex digits")
	case !validKind(m.Kind):
		return errors.New("inbox: a message kind must be 1 to 64 of a-z 0-9 . : _ -")
	case m.Sent.IsZero():
		return errors.New("inbox: a message needs the time it was sent")
	case len(m.Body) > 0 && !json.Valid(m.Body):
		return errors.New("inbox: a message body must be JSON")
	}
	return nil
}

// Encode checks m and returns it as compact JSON, which never contains a
// newline.
func (m Message) Encode() ([]byte, error) {
	if err := m.Check(); err != nil {
		return nil, err
	}
	b, err := json.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("inbox: %w", err)
	}
	if len(b) > MaxMessage {
		return nil, ErrTooBig
	}
	return b, nil
}

// Decode parses one message from JSON and checks it. Fields it doesn't know
// are ignored, so an older agent takes a newer sender's messages.
func Decode(b []byte) (Message, error) {
	if len(b) > MaxMessage {
		return Message{}, ErrTooBig
	}
	var m Message
	if err := json.Unmarshal(b, &m); err != nil {
		return Message{}, fmt.Errorf("inbox: %w", err)
	}
	return m, m.Check()
}

func validID(id string) bool {
	if len(id) != 32 {
		return false
	}
	for i := range len(id) {
		c := id[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

func validKind(k string) bool {
	if len(k) == 0 || len(k) > 64 {
		return false
	}
	for i := range len(k) {
		c := k[i]
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9':
		case c == '.', c == ':', c == '_', c == '-':
		default:
			return false
		}
	}
	return true
}

// Via is how a message reached the agent. The agent sets it from the
// channel the message came in on; what the sender claims plays no part
// (DESIGN 3.4).
type Via string

const (
	// ViaSocket is the AF_UNIX socket.
	ViaSocket Via = "socket"
	// ViaPipe is the named pipe that Windows falls back to.
	ViaPipe Via = "pipe"
	// ViaInbox is the spool, drained from disk.
	ViaInbox Via = "inbox"
)

// Delivery is a message as the agent took it in.
type Delivery struct {
	Message
	Via Via
	// PeerPID is the sending process as the OS reported it for the socket
	// or the pipe, so later work packages can walk its parent chain
	// themselves instead of believing the sender. It is 0 for the spool,
	// and where the OS doesn't say.
	PeerPID int
	// Received is the agent's wall-clock time when it took the message.
	Received time.Time
}

// Sink is where the agent puts each message it takes in. The socket acks a
// message, and a drain moves past it, only after Deliver returns nil for
// it, so Deliver must have stored the message durably by then: enough that
// it survives the agent being killed, if not a power cut. Deliver must
// also store each id once, and return nil without storing anything for an
// id it has stored before, because both ways in deliver some messages
// twice.
//
// For a message it will never store, such as one whose body it can't
// read, Deliver returns ErrRejected, wrapped or not, and a drain counts the
// message and goes on to the next. Any other error says that the sink
// can't store messages for now, and a drain stops there until the next
// one (Drainer.Drain). A sink that returned such an error for one message
// for good would hold the drain back for good.
type Sink interface {
	Deliver(d Delivery) error
}

// ErrRejected is what a Sink returns, wrapped or not, for a message it will
// never store. Over the socket the agent refuses such a message like any
// other it can't store, so the sender spools it, and the drain then counts
// it and skips it.
var ErrRejected = errors.New("inbox: the agent won't store this message")
