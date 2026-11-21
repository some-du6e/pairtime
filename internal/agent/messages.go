// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"fmt"
	"path/filepath"
	"runtime"
	"time"

	"github.com/some-du6e/pairtime/internal/agent/inbox"
	"github.com/some-du6e/pairtime/internal/agent/ipc"
	"github.com/some-du6e/pairtime/internal/platform/clock"
)

// drainEvery is how often the agent drains the inbox (DESIGN 3.5).
const drainEvery = time.Minute

// journalDir is where the agent keeps the journal of the messages it took
// in, until the ledger writer takes its place (WP-LG5, WP-AG4).
func (a *agent) journalDir() string { return filepath.Join(a.dirs.Data, "received") }

// startIPC opens the journal, starts taking messages on the socket, and
// drains the inbox now and every minute after (WP-AG2). Problems go to the
// status and don't stop the agent: a hook that can't reach the agent
// spools its message, and the drain picks it up once the agent can store
// it. While the agent has no listener, because Listen failed or Serve gave
// up, it tries to listen again at every drain, since a spooled hook call
// loses its via = hook (DESIGN 5.2). It returns the step that stops all
// of it. The process scan, presence and focus must have started, for the
// hook sink to hand calls to (hookSteps).
func (a *agent) startIPC(opts Options) func() {
	j, err := inbox.OpenJournal(a.journalDir(), time.Now())
	if err != nil {
		a.setProblem("ipc", fmt.Sprintf("hooks spool their messages, because the agent can't open its journal: %v", err))
		return func() {}
	}
	a.journal = j
	// Hook calls go through the hook sink, whichever way they come.
	sink := newHookSink(j, runtime.GOOS, a.hooked, a.hookSteps())
	walk := a.walkPeer
	if opts.Walk != nil {
		walk = opts.Walk
	}
	// The CLI's questions, such as the last work session `pairtime recap`
	// asks for, are answered from the bucket store, which startScorer
	// opened before this.
	l := &ipcListener{a: a, sink: sink, opts: ipc.Options{ForcePipe: opts.ForcePipe, Log: a.log, Walk: walk,
		Answer: a.answerQuestion}}
	l.listen()

	// Drains run on a goroutine of their own, because a long spool takes a
	// while and scheduler jobs must return at once. That goroutine is the
	// only one that uses the Drainer, and the only one that listens again.
	drainer := inbox.NewDrainer(a.dirs.Inbox(), sink, clock.Now)
	kick := make(chan struct{}, 1)
	stop := make(chan struct{})
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		first := true // startIPC has just tried to listen
		for {
			select {
			case <-stop:
				return
			case <-kick:
				if !first {
					l.ensure()
				}
				first = false
				a.drain(drainer)
			}
		}
	}()
	kick <- struct{}{}
	every := opts.DrainEvery
	if every <= 0 {
		every = drainEvery
	}
	job := a.sched.Every("inbox.drain", every, func(time.Time) {
		select {
		case kick <- struct{}{}:
		default: // a drain is already due
		}
	})

	return func() {
		job.Stop()
		close(stop)
		<-drained
		l.close()
		if err := j.Close(); err != nil {
			a.log.Warn("closing the journal", "err", err)
		}
	}
}

// ipcListener keeps the agent listening on its socket, or its pipe. Only
// startIPC, the drain goroutine and the step that stops the agent use it,
// one after another, so it needs no lock; the status reads a.ipcSrv.
type ipcListener struct {
	a      *agent
	opts   ipc.Options
	sink   inbox.Sink
	srv    *ipc.Server   // the server that listens, or last did; nil if none
	served chan struct{} // closed when srv's Serve has returned
}

// listen starts listening and serving, or records why it can't.
func (l *ipcListener) listen() {
	ep, err := ipc.EndpointFor(l.a.dirs)
	var srv *ipc.Server
	if err == nil {
		srv, err = ipc.Listen(ep, l.opts)
	}
	if err != nil {
		l.a.setProblem("ipc", fmt.Sprintf("hooks spool their messages, because the agent can't listen for them: %v", err))
		return
	}
	if fb := srv.Fallback(); fb != "" {
		l.a.setProblem("ipc", "the agent listens on the named pipe, because "+fb)
	} else {
		l.a.setProblem("ipc", "")
	}
	served := make(chan struct{})
	go func() {
		defer close(served)
		if err := srv.Serve(l.sink); err != nil {
			l.a.setProblem("ipc", fmt.Sprintf("hooks spool their messages, because the agent stopped taking them: %v", err))
		}
	}()
	l.srv, l.served = srv, served
	l.a.ipcSrv.Store(srv)
}

// ensure listens again when the agent doesn't: when the last try failed,
// or when Serve has given up since, which leaves the server's Via empty.
func (l *ipcListener) ensure() {
	if l.srv != nil && l.srv.Via() != "" {
		return
	}
	l.close()
	l.listen()
}

// close stops the server, if there is one, and waits for its Serve.
func (l *ipcListener) close() {
	if l.srv == nil {
		return
	}
	l.a.ipcSrv.Store(nil)
	l.srv.Close()
	<-l.served
	l.srv, l.served = nil, nil
}

// drain moves what the inbox holds into the journal.
func (a *agent) drain(d *inbox.Drainer) {
	res, err := d.Drain()
	if err != nil {
		a.log.Warn("draining the inbox", "err", err)
	}
	if res.Busy != nil {
		a.log.Warn("the inbox's spool waits for the next drain", "err", res.Busy)
	}
	if res.Delivered > 0 || res.Bad > 0 || res.Rejected > 0 || res.Failed > 0 || res.SetAside > 0 {
		a.log.Info("drained the inbox", "files", res.Files, "messages", res.Delivered, "bad_lines", res.Bad,
			"rejected", res.Rejected, "failed_files", res.Failed, "set_aside", res.SetAside)
	}
}

// ipcReply is the socket's part of GET /v1/status.
type ipcReply struct {
	// Via is "socket" or "pipe", or empty when hooks can't reach the agent
	// and spool their messages.
	Via inbox.Via `json:"via,omitempty"`
	// Stored counts the messages the agent stored since it started, from
	// the socket and the inbox both, and Duplicates the second copies it
	// dropped.
	Stored     uint64 `json:"stored"`
	Duplicates uint64 `json:"duplicates"`
	// Refused counts connections turned away because they didn't come from
	// this user.
	Refused uint64 `json:"refused"`
}

func (a *agent) ipcStatus() ipcReply {
	var r ipcReply
	if a.journal != nil {
		r.Stored, r.Duplicates = a.journal.Stats()
	}
	if srv := a.ipcSrv.Load(); srv != nil {
		// Via is "" once the server stopped listening, as it does when
		// Serve gives up.
		r.Via, r.Refused = srv.Via(), srv.Refused()
	}
	return r
}
