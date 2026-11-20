// SPDX-License-Identifier: AGPL-3.0-or-later

// Package hookpath is the fast path that every harness hook runs, `pairtime
// hook` (DESIGN 3.2; CP 6.2; docs/PLAN.md, WP-AG3), and the agent's reading
// of where a hook call came from.
//
// The sender's half is Send. It stamps the call with the wall clock, the
// OS's monotonic clock and the boot id (Now) as the harness makes it, reads
// the event the harness writes to stdin, up to MaxStdin, and hands both to
// the agent as one inbox.Message over the socket. When the agent hasn't
// acked it within ipc.HookTimeout, Send records the hook's parent chain
// (ReadChain) in the message, appends it to the inbox's spool instead, and
// says whether the caller should start the agent: it should when nothing
// answered at all. `pairtime track` and `pairtime emit` will take the same
// path (WP-AD18, WP-AD19).
//
// A harness waits for its hooks and reads what they leave behind: Claude
// Code blocks the action on exit status 2, parses stdout as JSON, and adds
// the stdout of some events to the model's context (CP 6.2). So nothing
// here prints, and nothing fails the call: every problem ends in the spool,
// or, when even the spool fails, in a lost call the harness never hears
// about. `pairtime hook` exits 0 whatever Send returns.
//
// The agent's half is Of, which says what a call's events are worth
// (D-spool-via, the rule docs/DESIGN.md takes in a separate PR):
//
//   - Only the agent's own walk of the parent chain makes a call's events
//     via = hook (DESIGN 3.4). The walk starts from the pid the OS reports
//     for the socket or pipe connection, and runs as soon as the agent
//     accepts it, while the sender waits for its ack (ipc.Options.Walk). It
//     counts when the process the hook runs under is a harness.
//   - Every call that reaches the agent through the spool, inbox/spool.jsonl,
//     gets via = plugin, whatever chain its line records. Any process of
//     this user can append any line, so the chain a sender records in its
//     own message never makes its events hook, never fills the session
//     fields that SM 1.2 takes from the walk (parent_class, has_tty), and
//     never tells the process scan that a harness process fired hooks. It
//     may only name the harness, which is all that telling apart the
//     copies of one call needs when two harnesses read the same hooks
//     (DESIGN 5.1: Cursor and Devin run Claude Code's).
//   - A spooled call keeps the hook's own sent, mono and boot_id as its
//     times, since the drain can come long after the call (Times). A call
//     the agent also got over the socket is stored once: both copies carry
//     one message id.
package hookpath

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"time"

	"github.com/some-du6e/pairtime/internal/agent/inbox"
	"github.com/some-du6e/pairtime/internal/agent/ipc"
	"github.com/some-du6e/pairtime/internal/platform"
)

// Kind is the inbox message kind of a hook call.
const Kind = "hook"

// MaxStdin is how much of stdin a hook reads (DESIGN 3.2). What comes after
// it is left unread, and the call is marked Cut.
const MaxStdin = 1 << 20

// StdinWait bounds how long a hook waits for the harness to finish writing
// stdin. Harnesses write their event and close stdin at once, and a hook
// stops reading as soon as what came is one whole JSON object, so the wait
// only runs out on a harness that leaves stdin open after something that
// isn't JSON, or on a person who runs the command in a terminal.
const StdinWait = time.Second

// chainWait bounds how long Send waits for its own parent chain before it
// spools a call. Reading the process table takes about a millisecond; a
// call goes without its chain rather than wait for a reader that is stuck.
const chainWait = 50 * time.Millisecond

// maxArg bounds each of the hook's arguments in the message, which come
// from a harness's hook configuration and are short.
const maxArg = 256

// bodyRoom is what a message needs beside its body: the envelope, and the
// sender's chain at its longest.
const bodyRoom = 64 << 10

// Call is one hook call, as the harness made it.
type Call struct {
	// Harness and Event are what --harness and --event said. Without Map,
	// Event is the harness's own name for the event, such as Claude Code's
	// UserPromptSubmit (internal/install's package doc); with Map it is one
	// of the generic names of DESIGN 5.3 (Generic).
	Harness, Event string
	// Map is the --map file of the generic hook mode, or "" (DESIGN 5.3).
	Map string
	// Problem says what was wrong with the hook's arguments, or is "".
	// Such a call is still sent, so the agent can count it.
	Problem string
	// Stdin is what the harness wrote the event to. Nil reads nothing.
	Stdin io.Reader
}

// Body is the body of a hook message. It holds what the harness wrote, so
// it stays on the device, like everything the inbox carries (AGENTS.md).
type Body struct {
	Harness string `json:"harness"`
	Event   string `json:"event"`
	Map     string `json:"map,omitempty"`
	Problem string `json:"problem,omitempty"`
	// Payload is stdin when it is one whole JSON value.
	Payload json.RawMessage `json:"payload,omitempty"`
	// Text is stdin when it isn't: when it was cut at MaxStdin, or isn't
	// JSON. Bytes that aren't UTF-8 become U+FFFD.
	Text string `json:"text,omitempty"`
	// Bytes is how many bytes of stdin the hook read, at most MaxStdin.
	Bytes int `json:"bytes"`
	// Cut says that stdin went on past MaxStdin, or was still open, with
	// what came so far not one whole JSON value, when the hook stopped
	// waiting (StdinWait).
	Cut bool `json:"cut,omitempty"`
	// Dropped says that what the hook read didn't fit in a message, and
	// was left out.
	Dropped bool `json:"dropped,omitempty"`
}

// DecodeBody reads a hook message's body.
func DecodeBody(m inbox.Message) (Body, error) {
	var b Body
	if m.Kind != Kind {
		return b, fmt.Errorf("hookpath: a %q message isn't a hook call", m.Kind)
	}
	if len(m.Body) == 0 {
		return b, errors.New("hookpath: the hook call has no body")
	}
	if err := json.Unmarshal(m.Body, &b); err != nil {
		return b, fmt.Errorf("hookpath: %w", err)
	}
	return b, nil
}

// Options configure Send.
type Options struct {
	// Endpoint is the agent's (ipc.EndpointFor).
	Endpoint ipc.Endpoint
	// Timeout bounds the wait for the agent's ack. Zero means
	// ipc.HookTimeout.
	Timeout time.Duration
	// StdinWait bounds the wait for stdin. Zero means StdinWait.
	StdinWait time.Duration
	// Now, when set, replaces the clocks, and Chain the reader of the
	// hook's own parent chain. Tests set them.
	Now   func() Stamp
	Chain func() ([]platform.Proc, error)
}

// Result says how one call went.
type Result struct {
	// ID is the message's id.
	ID string
	// Via is how the message went: inbox.ViaSocket or inbox.ViaPipe once
	// the agent acked it, inbox.ViaInbox once it is in the spool, and ""
	// when it was lost.
	Via inbox.Via
	// StartAgent says that nothing answered on the agent's socket or pipe,
	// so the caller should start the agent, which drains the spool when it
	// comes up (DESIGN 3.2). An agent that answered but didn't ack in time
	// is running, and a second one would only exit.
	StartAgent bool
	// Cut is Body.Cut.
	Cut bool
	// Err is what went wrong on the way, even when the spool took the
	// message in the end.
	Err error
}

// Send hands the call c to the agent, or to the spool when the agent
// doesn't take it in time. It returns within about StdinWait, the ack's
// timeout, chainWait and one file append, at their longest, and within a
// few milliseconds when the harness closes stdin and the agent is up.
func Send(c Call, opts Options) Result {
	now := opts.Now
	if now == nil {
		now = Now
	}
	stamp := now() // first, as the harness calls the hook
	wait := opts.StdinWait
	if wait <= 0 {
		wait = StdinWait
	}
	data, whole, cut := readStdin(c.Stdin, MaxStdin, wait)

	m := inbox.Message{ID: inbox.NewID(), Kind: Kind, Sent: stamp.Wall, Mono: stamp.Mono, BootID: stamp.BootID}
	m.Body = body(c, data, whole, cut)
	res := Result{ID: m.ID, Cut: cut}

	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = ipc.HookTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	conn, err := ipc.Dial(ctx, opts.Endpoint)
	if err == nil {
		err = conn.Send(ctx, m)
		conn.Close()
		if err == nil {
			res.Via = conn.Via()
			return res
		}
	} else {
		res.StartAgent = true
	}
	// Only a spooled call carries the hook's own parent chain. Over the
	// socket the agent reads the chain itself from the connection and
	// believes no other (Of); through the spool, the recorded chain is all
	// that can name the harness, which is what telling apart the copies of
	// one call needs (D-spool-via). So the process table is read only here,
	// off the path that a running agent's calls take.
	m.Chain = ownChain(opts.Chain)
	if aerr := inbox.Append(opts.Endpoint.Inbox, m); aerr != nil {
		res.Err = errors.Join(err, aerr)
		return res
	}
	res.Via, res.Err = inbox.ViaInbox, err
	return res
}

// body returns the message body of c, whose stdin was data: whole when data
// is one whole JSON value, cut when there was more.
func body(c Call, data []byte, whole, cut bool) json.RawMessage {
	b := Body{
		Harness: clip(c.Harness),
		Event:   clip(c.Event),
		Map:     clip(c.Map),
		Problem: clip(c.Problem),
		Bytes:   len(data),
		Cut:     cut,
	}
	switch {
	case len(data) == 0:
	case whole && !cut:
		b.Payload = data
	default:
		b.Text = string(data)
	}
	out, err := json.Marshal(b)
	if err != nil || len(out) > inbox.MaxMessage-bodyRoom {
		// Text whose every byte needs escaping can grow six times over.
		b.Payload, b.Text, b.Dropped = nil, "", true
		out, _ = json.Marshal(b) // can't fail: plain strings and numbers
	}
	return out
}

// clip cuts s to maxArg bytes.
func clip(s string) string {
	if len(s) > maxArg {
		return s[:maxArg]
	}
	return s
}

// ownChain reads the hook's own parent chain with read, ReadChain when it is
// nil, for a message, waiting at most chainWait for it.
func ownChain(read func() ([]platform.Proc, error)) []inbox.Proc {
	if read == nil {
		read = func() ([]platform.Proc, error) { return ReadChain(os.Getpid()) }
	}
	chainc := make(chan []platform.Proc, 1)
	go func() {
		chain, err := read()
		if err != nil {
			chain = nil
		}
		chainc <- chain
	}()
	select {
	case chain := <-chainc:
		return senderChain(chain)
	case <-time.After(chainWait):
		return nil
	}
}

// senderChain turns the hook's own parent chain into the message's, at
// most inbox.MaxChain processes long. The agent believes none of it
// (Of), so the message carries only what names each process.
func senderChain(chain []platform.Proc) []inbox.Proc {
	if len(chain) == 0 {
		return nil
	}
	n := min(len(chain), inbox.MaxChain)
	out := make([]inbox.Proc, 0, n)
	for _, p := range chain[:n] {
		if p.PID <= 0 {
			break
		}
		out = append(out, inbox.Proc{PID: p.PID, Name: p.Name, Start: p.Start.UTC()})
	}
	return out
}

// ReadChain returns the process pid and its ancestors, nearest first, as
// this OS's process table has them (WP-OS6). It is empty when pid isn't in
// the table. The hook reads its own chain with it, and the agent the chain
// of each connection's peer.
func ReadChain(pid int) ([]platform.Proc, error) {
	procs, err := readOS()
	if err != nil {
		return nil, err
	}
	return platform.NewTable(runtime.GOOS, procs).Chain(pid), nil
}

// trimSpace is bytes.TrimSpace for JSON's four whitespace bytes.
func trimSpace(b []byte) []byte {
	return bytes.Trim(b, " \t\r\n")
}
