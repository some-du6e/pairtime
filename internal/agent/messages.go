// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"fmt"
	"path/filepath"
	"time"

	"github.com/some-du6e/pairtime/internal/agent/inbox"
	"github.com/some-du6e/pairtime/internal/agent/ipc"
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
// it. It returns the step that stops all of it.
func (a *agent) startIPC(opts Options) func() {
	j, err := inbox.OpenJournal(a.journalDir(), time.Now())
	if err != nil {
		a.setProblem("ipc", fmt.Sprintf("hooks spool their messages, because the agent can't open its journal: %v", err))
		return func() {}
	}
	a.journal = j

	ep, err := ipc.EndpointFor(a.dirs)
	if err == nil {
		a.ipcSrv, err = ipc.Listen(ep, ipc.Options{ForcePipe: opts.ForcePipe, Log: a.log})
	}
	switch {
	case err != nil:
		a.ipcSrv = nil
		a.setProblem("ipc", fmt.Sprintf("hooks spool their messages, because the agent can't listen for them: %v", err))
	case a.ipcSrv.Fallback() != "":
		a.setProblem("ipc", "the agent listens on the named pipe, because "+a.ipcSrv.Fallback())
	}
	served := make(chan struct{})
	if srv := a.ipcSrv; srv != nil {
		go func() {
			defer close(served)
			if err := srv.Serve(j); err != nil {
				a.setProblem("ipc", fmt.Sprintf("hooks spool their messages, because the agent stopped taking them: %v", err))
			}
		}()
	} else {
		close(served)
	}

	// Drains run on a goroutine of their own, because a long spool takes a
	// while and scheduler jobs must return at once. That goroutine is the
	// only one that uses the Drainer.
	drainer := inbox.NewDrainer(a.dirs.Inbox(), j, func() time.Time { return time.Now().UTC() })
	kick := make(chan struct{}, 1)
	stop := make(chan struct{})
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		for {
			select {
			case <-stop:
				return
			case <-kick:
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
		if a.ipcSrv != nil {
			a.ipcSrv.Close()
		}
		<-served
		if err := j.Close(); err != nil {
			a.log.Warn("closing the journal", "err", err)
		}
	}
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
	if a.ipcSrv != nil {
		r.Via, r.Refused = a.ipcSrv.Via(), a.ipcSrv.Refused()
	}
	return r
}
