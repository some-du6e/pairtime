// SPDX-License-Identifier: AGPL-3.0-or-later

package ipc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/some-du6e/pairtime/internal/agent/inbox"
	"github.com/some-du6e/pairtime/internal/platform"
	"github.com/some-du6e/pairtime/internal/platform/clock"
)

const (
	// maxConns bounds the connections the agent serves at once. A hook
	// hangs up within milliseconds, so this is far more than a teen's
	// machine makes; it keeps a runaway process from taking every thread.
	maxConns = 64
	// idleTimeout is how long the agent waits for the next frame.
	idleTimeout = 2 * time.Minute
	// replyTimeout is how long the agent waits to write a reply.
	replyTimeout = 10 * time.Second
	// selfTestTimeout bounds the self-test at startup.
	selfTestTimeout = 3 * time.Second
	// maxAcceptFailures is how many accepts in a row may fail, about 10 s
	// of trying, before Serve gives up.
	maxAcceptFailures = 20
)

// acceptRetry is how much longer Serve waits after each failed accept in a
// row before it tries again, up to a second. Tests shorten it.
var acceptRetry = 50 * time.Millisecond

// errNoPipe means a named pipe was asked for off Windows.
var errNoPipe = errors.New("ipc: there is no named pipe off Windows")

// reply is the agent's answer to one frame: the ack of a message, or the
// answer to a question (ask.go).
type reply struct {
	ID    string `json:"id,omitempty"`
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
	// Unknown is set on the reply to a question the agent doesn't answer.
	Unknown bool `json:"unknown,omitempty"`
	// Answer is the answer to a question, one JSON value.
	Answer json.RawMessage `json:"answer,omitempty"`
}

// Options configure Listen.
type Options struct {
	// ForcePipe skips the socket and listens on the named pipe. It works
	// on Windows only, where tests use it to run the fallback on machines
	// whose AF_UNIX works.
	ForcePipe bool
	// Log gets connection problems. Nil discards them.
	Log *slog.Logger
	// Walk, when set, reads the parent chain of each connection's peer
	// from the pid the OS reports for it, nearest first. Serve calls it as
	// soon as it has accepted a connection and checked who runs its peer,
	// before it reads a message, while a sender that waits for its ack is
	// still alive; every Delivery from the connection carries what it
	// returned as Walk. A sender that doesn't wait may be gone by then,
	// and Windows may have given its pid to another process, so the walk
	// counts only when its first process has the pid and start time the
	// sender recorded for itself (DESIGN 3.4; internal/agent/hookpath).
	// Only a walk that counts vouches for where a message came from. Walk
	// isn't called when the OS gives no pid, nor for the self-test.
	Walk func(pid int) []platform.Proc
	// Answer, when set, answers the questions the CLI asks (Ask), such as
	// AskLastSession, with one JSON value of at most about 1 MiB once
	// encoded. It returns ErrUnknownQuestion for a question it has no
	// answer for, and ErrStarting while it can't answer yet. It runs on
	// the connection's goroutine, so it should
	// answer from what the agent already holds, and never before Listen
	// returns: a question that comes during the socket's self-test is
	// turned away. Without it the agent answers no question.
	Answer func(question string) (json.RawMessage, error)
}

// Server is the agent's end of the socket or the pipe.
type Server struct {
	ep       Endpoint
	via      inbox.Via
	ln       net.Listener
	want     identity // the user every peer must run as
	fallback string   // why the server is on the pipe, when it is
	log      *slog.Logger
	walk     func(pid int) []platform.Proc
	answerer func(question string) (json.RawMessage, error)
	sem      chan struct{}
	refused  atomic.Uint64

	// stopped is set once the listener is closed, by Close or by Serve
	// giving up (stopListening).
	stopped  atomic.Bool
	stopOnce sync.Once
	stopErr  error

	mu     sync.Mutex
	conns  map[net.Conn]struct{}
	closed bool
	wg     sync.WaitGroup
}

// Listen starts the agent's end of ep. It makes the socket's directory
// reachable by this user only, removes a socket that a dead agent left
// behind, listens, and runs a self-test: a message sent to itself through
// the same checks that every sender and connection go through. On
// Windows, when any of that fails, or opts.ForcePipe is set, it listens on
// the named pipe instead (CP 6.3; DESIGN 3.3), and writes the note without
// which senders don't try the pipe (Endpoint.PipeNote). Elsewhere the
// failure is the error.
func Listen(ep Endpoint, opts Options) (*Server, error) {
	me, err := self()
	if err != nil {
		return nil, fmt.Errorf("ipc: can't tell which user this process runs as: %w", err)
	}
	log := opts.Log
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	s := &Server{
		ep:       ep,
		want:     me,
		log:      log,
		walk:     opts.Walk,
		answerer: opts.Answer,
		sem:      make(chan struct{}, maxConns),
		conns:    make(map[net.Conn]struct{}),
	}
	var sockErr error
	switch {
	case opts.ForcePipe && ep.Pipe == "":
		return nil, errNoPipe
	case opts.ForcePipe:
		sockErr = errors.New("the socket was skipped on purpose")
		// A socket file left from an earlier run would only slow
		// senders down on their way to the pipe.
		removeStale(ep.Socket)
	default:
		if sockErr = s.listenSocket(); sockErr == nil {
			// A note left by an agent that was on the pipe would send
			// senders whose socket dial fails to a pipe that is gone, or
			// that someone else has made since.
			removePipeNote(ep)
			return s, nil
		}
		// An agent that already answers on the socket gets every message
		// first, so a pipe of this one's would carry nothing.
		if ep.Pipe == "" || errors.Is(sockErr, ErrInUse) {
			return nil, sockErr
		}
	}
	ln, err := listenPipe(ep.Pipe)
	if err != nil {
		return nil, errors.Join(sockErr, err)
	}
	if err := writePipeNote(ep); err != nil {
		ln.Close()
		return nil, errors.Join(sockErr, fmt.Errorf("ipc: noting that the agent listens on the pipe: %w", err))
	}
	s.ln, s.via, s.fallback = ln, inbox.ViaPipe, sockErr.Error()
	return s, nil
}

// removePipeNote deletes the note that says an agent listens on ep's pipe,
// so senders stop trying the pipe.
func removePipeNote(ep Endpoint) {
	if ep.PipeNote != "" {
		os.Remove(ep.PipeNote)
	}
}

func (s *Server) listenSocket() error {
	if err := secureDir(filepath.Dir(s.ep.Socket), s.want); err != nil {
		return err
	}
	ln, err := listenUnix(s.ep.Socket)
	if err != nil {
		return err
	}
	s.ln, s.via = ln, inbox.ViaSocket
	if err := s.selfTest(ln); err != nil {
		ln.Close()
		s.ln, s.via = nil, ""
		return fmt.Errorf("ipc: the socket failed its self-test: %w", err)
	}
	return nil
}

// selfTest sends a probe message to the socket from this process and serves
// it, through the sender's check of who runs the agent, the agent's check
// of who connected, the framing and the ack. Windows 10 builds differ in
// how much of AF_UNIX works, SIO_AF_UNIX_GETPEERPID in particular (CP
// 6.3), and this is where a build that falls short shows. A sender that
// connects during the test is turned away, and spools its message, and so
// is a question: Options.Answer doesn't run until Listen has returned.
func (s *Server) selfTest(ln *net.UnixListener) error {
	probe := inbox.New("ipc.selftest", nil)
	deadline := time.Now().Add(selfTestTimeout)
	ctx, cancel := context.WithDeadline(context.Background(), deadline)
	defer cancel()
	if err := ln.SetDeadline(deadline); err != nil {
		return err
	}
	defer ln.SetDeadline(time.Time{})
	sent := make(chan error, 1)
	go func() {
		err := s.probe(ctx, probe)
		if err != nil {
			ln.SetDeadline(time.Now()) // wakes the Accept below
		}
		sent <- err
	}()
	got := false
	sink := sinkFunc(func(d inbox.Delivery) error {
		if d.ID != probe.ID {
			return errors.New("the agent is still starting")
		}
		got = true
		return nil
	})
	for !got {
		c, err := ln.Accept()
		if err != nil {
			return errors.Join(err, <-sent)
		}
		// handle runs on this goroutine, so got needs no lock.
		s.handle(c, sink, deadline, nil, false)
	}
	return <-sent
}

// probe is the sender's half of the self-test.
func (s *Server) probe(ctx context.Context, m inbox.Message) error {
	c, err := dialSocket(ctx, s.ep.Socket, s.want)
	if err != nil {
		return err
	}
	conn := &Conn{c: c, via: inbox.ViaSocket}
	defer conn.Close()
	return conn.Send(ctx, m)
}

// Via says whether the server is on the socket or on the pipe. It is ""
// once the server has stopped listening, after Close or once Serve has
// given up, because senders can't reach it then and spool instead.
func (s *Server) Via() inbox.Via {
	if s.stopped.Load() {
		return ""
	}
	return s.via
}

// Fallback says why the server is on the pipe, or is "" when it isn't.
func (s *Server) Fallback() string { return s.fallback }

// Refused counts the connections turned away because their peer isn't
// this user.
func (s *Server) Refused() uint64 { return s.refused.Load() }

// Serve takes connections until Close, delivers every message they carry
// to sink, and acks each one sink stored. It returns nil after Close.
//
// When accepting fails maxAcceptFailures times in a row, Serve stops
// listening and returns the error; the connections it took carry on. With
// the socket file or the pipe's note gone, senders find no agent at once
// and spool. A listener left open would have the OS queue their
// connections for an Accept that never comes, and each sender would wait
// out its timeout first.
func (s *Server) Serve(sink inbox.Sink) error {
	failures := 0
	for {
		c, err := s.ln.Accept()
		if err != nil {
			if s.isClosed() {
				return nil
			}
			failures++
			if failures >= maxAcceptFailures {
				return errors.Join(fmt.Errorf("ipc: accepting connections keeps failing, so the agent stopped listening: %w", err), s.stopListening())
			}
			s.log.Warn("ipc: accepting a connection failed", "err", err)
			// Out of file descriptors and the like: wait, as net/http
			// does, and try again.
			time.Sleep(min(time.Duration(failures)*acceptRetry, time.Second))
			continue
		}
		failures = 0
		select {
		case s.sem <- struct{}{}:
		default:
			s.log.Warn("ipc: too many connections at once, so one was dropped")
			c.Close()
			continue
		}
		if !s.track(c) {
			<-s.sem
			c.Close()
			return nil
		}
		go func() {
			defer s.wg.Done()
			defer func() { <-s.sem }()
			defer s.untrack(c)
			s.handle(c, sink, time.Time{}, s.walk, true)
		}()
	}
}

// track adds c to the open connections, unless the server is closed.
func (s *Server) track(c net.Conn) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false
	}
	s.conns[c] = struct{}{}
	s.wg.Add(1)
	return true
}

func (s *Server) untrack(c net.Conn) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.conns, c)
}

func (s *Server) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

// Close stops listening, drops open connections, and returns once every
// handler has finished, so no Deliver runs after it. A message whose ack
// it cuts off is spooled by its sender, and arrives through the drain.
// On the pipe it deletes its note first, so senders stop trying the pipe.
func (s *Server) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	for c := range s.conns {
		c.Close()
	}
	s.mu.Unlock()
	err := s.stopListening()
	s.wg.Wait()
	return err
}

// stopListening closes the listener, once, which also deletes the socket
// file. On the pipe it deletes the note first, so senders stop trying the
// pipe.
func (s *Server) stopListening() error {
	s.stopOnce.Do(func() {
		s.stopped.Store(true)
		if s.via == inbox.ViaPipe {
			removePipeNote(s.ep)
		}
		s.stopErr = s.ln.Close()
	})
	return s.stopErr
}

// handle serves one connection until the sender hangs up, sends a bad
// frame or goes quiet for idleTimeout. until, when not zero, is a deadline
// for all of it. walk, when set, reads the peer's parent chain first
// (Options.Walk). asks says whether a question gets its answer, which it
// doesn't during the self-test.
func (s *Server) handle(c net.Conn, sink inbox.Sink, until time.Time, walk func(int) []platform.Proc, asks bool) {
	defer c.Close()
	id, pid, err := peerOf(c)
	if err == nil && id != s.want {
		err = fmt.Errorf("it runs as %s", id)
	}
	if err != nil {
		s.refused.Add(1)
		s.log.Warn("ipc: refused a connection that isn't from this user", "peer_pid", pid, "err", err)
		return
	}
	var chain []platform.Proc
	if walk != nil && pid > 0 {
		chain = walk(pid)
	}
	for {
		if err := c.SetReadDeadline(earliest(time.Now().Add(idleTimeout), until)); err != nil {
			return
		}
		body, err := ReadFrame(c, inbox.MaxMessage)
		if err != nil {
			if !errors.Is(err, io.EOF) {
				s.log.Debug("ipc: a connection ended badly", "err", err)
			}
			return
		}
		rep, keep := s.take(body, sink, pid, chain, asks)
		b, err := json.Marshal(rep)
		if err != nil {
			return
		}
		if err := c.SetWriteDeadline(earliest(time.Now().Add(replyTimeout), until)); err != nil {
			return
		}
		if err := WriteFrame(c, b, maxAnswer); err != nil || !keep {
			return
		}
	}
}

// take answers the question in one frame (ask.go), or delivers the message
// in it, and returns the reply and whether the connection may carry on.
// One that sent something other than a question or a message may not.
// With asks false a question is turned away, unanswered, and the
// connection ends.
func (s *Server) take(body []byte, sink inbox.Sink, pid int, chain []platform.Proc, asks bool) (reply, bool) {
	if q, ok := asQuestion(body); ok {
		if !asks {
			return reply{ID: q.ID, Error: startingAnswer}, false
		}
		return s.answer(q)
	}
	m, err := inbox.Decode(body)
	if err != nil {
		msg := err.Error()
		if len(msg) > 200 {
			msg = msg[:200]
		}
		return reply{Error: msg}, false
	}
	d := inbox.Delivery{Message: m, Via: s.via, PeerPID: pid, Walk: chain, Received: clock.Now()}
	if err := sink.Deliver(d); err != nil {
		s.log.Warn("ipc: couldn't store a message", "err", err)
		return reply{ID: m.ID, Error: "the agent couldn't store the message"}, true
	}
	return reply{ID: m.ID, OK: true}, true
}

// peerOf returns who is at the other end of c, and the process id when the
// OS says.
func peerOf(c net.Conn) (identity, int, error) {
	switch c := c.(type) {
	case *net.UnixConn:
		return unixPeer(c)
	case interface{ peer() (identity, int, error) }:
		return c.peer()
	}
	return identity{}, 0, fmt.Errorf("ipc: can't ask the OS about a %T", c)
}

// earliest returns t, or until when that is sooner and not zero.
func earliest(t, until time.Time) time.Time {
	if !until.IsZero() && until.Before(t) {
		return until
	}
	return t
}

// sinkFunc makes a function a Sink.
type sinkFunc func(inbox.Delivery) error

func (f sinkFunc) Deliver(d inbox.Delivery) error { return f(d) }
