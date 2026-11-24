// SPDX-License-Identifier: AGPL-3.0-or-later

// Package hookpath is the fast path that every harness hook runs, `pairtime
// hook` (DESIGN 3.2; CP 6.2; docs/PLAN.md, WP-AG3), and the agent's reading
// of where a hook call came from.
//
// The sender's half is Send. It stamps the call with the wall clock, the
// OS's monotonic clock and the boot id (clock.Now) as the harness makes it,
// records its own pid and start time as the first entry of the call's
// parent chain, reads the event the harness writes to stdin, up to
// MaxStdin, and hands all of it to the agent as one inbox.Message over the
// socket. When the agent hasn't acked it within ipc.HookTimeout, Send
// records the rest of the hook's parent chain (ReadChain) in the message,
// appends it to the inbox's spool instead, and says whether the caller
// should start the agent: it should when nothing answered at all.
// `pairtime track` and `pairtime emit` will take the same path (WP-AD18,
// WP-AD19).
//
// A harness waits for its hooks and reads what they leave behind: Claude
// Code blocks the action on exit status 2, parses stdout as JSON, and adds
// the stdout of some events to the model's context (CP 6.2). So nothing
// here prints, and nothing fails the call: every problem ends in the spool,
// or, when even the spool fails, in a lost call the harness never hears
// about. `pairtime hook` exits 0 whatever Send returns.
//
// The agent's half is Of, which says what a call's events are worth, and
// Times, which says when it was made (DESIGN 3.4, 5.2):
//
//   - Only the agent's own walk of the parent chain makes a call's events
//     via = hook (DESIGN 3.4). The walk starts from the pid the OS reports
//     for the socket or pipe connection, and runs as soon as the agent
//     accepts it, while the sender waits for its ack (ipc.Options.Walk). It
//     counts when the process the hook runs under is a harness, or an IDE
//     that hosts the harness the call names (Hosts), and only when the
//     walk's first process has the pid and start time the sender recorded
//     for itself, so a pid Windows has handed to another process walks
//     nothing.
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
//     times, since the drain can come long after the call (Times), but no
//     call is timed later than when the agent took it in. A call the agent
//     also got over the socket is stored once: both copies carry one
//     message id.
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
	"github.com/some-du6e/pairtime/internal/platform/clock"
)

// Kind is the inbox message kind of a hook call.
const Kind = inbox.KindHook

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
// call goes with only its own entry rather than wait for a reader that is
// stuck.
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
	// Prepare, when set, runs once before a call goes to the spool, and a
	// call it fails is lost instead. `pairtime hook` makes the data
	// directories there, for this user only (home.Dirs.Create), so that the
	// first call ever, spooled before any agent ran, doesn't leave a
	// prompt in a directory others may read; and it refuses when another
	// user owns them, as under sudo. It never runs on the way to an agent
	// that acks.
	Prepare func() error
	// Now, when set, replaces the clocks; Self, the reader of the hook's own
	// process, which is the first entry of every call's chain; and Chain,
	// the reader of the hook's whole parent chain, which a spooled call
	// carries. Tests set them.
	Now   func() clock.Stamp
	Self  func() (platform.Proc, error)
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
		now = clock.Now
	}
	stamp := now() // first, as the harness calls the hook
	// Every call names its sender, so the agent can check that the process
	// it walks from the connection is this one and not a later process
	// that has the same pid (Of).
	self := ownEntry(opts.Self)
	wait := opts.StdinWait
	if wait <= 0 {
		wait = StdinWait
	}
	data, whole, cut := readStdin(c.Stdin, MaxStdin, wait)

	m := inbox.Message{ID: inbox.NewID(), Kind: Kind, Sent: stamp.Wall, Mono: stamp.Mono, BootID: stamp.BootID, Chain: []inbox.Proc{self}}
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
	if opts.Prepare != nil {
		if perr := opts.Prepare(); perr != nil {
			res.Err = errors.Join(err, perr)
			return res
		}
	}
	// Only a spooled call carries the rest of the hook's parent chain.
	// Over the socket the agent reads the chain itself from the connection
	// and believes no other (Of); through the spool, the recorded chain is
	// all that can name the harness, which is what telling apart the copies
	// of one call needs (DESIGN 5.1, 5.2). So the process table is read
	// only here, off the path that a running agent's calls take.
	m.Chain = ownChain(self, opts.Chain)
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

// ownEntry reads the hook's own process with read, selfOS when it is nil,
// for the first entry of the call's chain: its pid, name and start, read
// the way the agent's table read gives them, so the agent can tell that
// the process it walks is this one (Of). It reads this process alone,
// which takes microseconds, so every call has it, even one whose whole
// chain misses chainWait. A read that fails or panics leaves the pid
// alone, and then the agent's walk of the call counts for nothing.
func ownEntry(read func() (platform.Proc, error)) inbox.Proc {
	if read == nil {
		read = selfOS
	}
	p, err := readSelf(read)
	if err != nil || p.PID <= 0 {
		return inbox.Proc{PID: os.Getpid()}
	}
	return procOf(p)
}

// readSelf is read, with a panic turned into an error.
func readSelf(read func() (platform.Proc, error)) (p platform.Proc, err error) {
	defer func() {
		if r := recover(); r != nil {
			p, err = platform.Proc{}, fmt.Errorf("hookpath: reading this process panicked: %v", r)
		}
	}()
	return read()
}

// ownChain returns a spooled call's chain: self, the hook's own entry, then
// its ancestors as read gives them, ReadChain when it is nil, at most
// inbox.MaxChain processes in all. It waits at most chainWait for read,
// and a read that fails, panics or takes longer leaves self alone, as does
// a chain that starts with another process. The agent believes none of
// the chain (Of), so it carries only what names each process.
//
// read runs on a goroutine of its own, so that a stuck reader can't hold
// the call. A panic there would kill the process with status 2, which
// Claude Code takes as blocking the prompt (CP 6.2), and runHook's recover
// covers only the main goroutine; so the goroutine recovers by itself.
func ownChain(self inbox.Proc, read func() ([]platform.Proc, error)) []inbox.Proc {
	if read == nil {
		read = func() ([]platform.Proc, error) { return ReadChain(os.Getpid()) }
	}
	chainc := make(chan []platform.Proc, 1)
	go func() {
		defer func() {
			if recover() != nil {
				chainc <- nil // nothing else was sent, and it holds one
			}
		}()
		chain, err := read()
		if err != nil {
			chain = nil
		}
		chainc <- chain
	}()
	var chain []platform.Proc
	select {
	case chain = <-chainc:
	case <-time.After(chainWait):
	}
	out := []inbox.Proc{self}
	if len(chain) == 0 || chain[0].PID != self.PID {
		return out
	}
	for _, p := range chain[1:min(len(chain), inbox.MaxChain)] {
		if p.PID <= 0 {
			break
		}
		out = append(out, procOf(p))
	}
	return out
}

// procOf is p as a chain entry carries it.
func procOf(p platform.Proc) inbox.Proc {
	return inbox.Proc{PID: p.PID, Name: p.Name, Start: p.Start.UTC(), Ticks: p.Ticks}
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
