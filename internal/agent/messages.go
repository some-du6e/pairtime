// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"fmt"
	"path/filepath"
	"runtime"
	"time"

	"github.com/some-du6e/pairtime/internal/agent/hookpath"
	"github.com/some-du6e/pairtime/internal/agent/inbox"
	"github.com/some-du6e/pairtime/internal/agent/ipc"
	"github.com/some-du6e/pairtime/internal/platform/clock"
	"github.com/some-du6e/pairtime/pkg/event"
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
	// Before the first drain, so that a call that waited too long goes
	// unread, and whether or not a drain runs at all.
	a.expireInbox()
	j, err := inbox.OpenJournal(a.journalDir(), time.Now())
	if err != nil {
		a.setProblem("ipc", fmt.Sprintf("hooks spool their messages, because the agent can't open its journal: %v", err))
		a.spoolNoDrain.Store(true)
		a.markDrained() // no drain will come
		return func() {}
	}
	a.journal = j
	// Hook calls go through the hook sink, whichever way they come.
	sink := newHookSink(j, runtime.GOOS, a.hooked, a.hookSteps())
	walk := a.walkPeer
	if opts.Walk != nil {
		walk = opts.Walk
	}
	// The CLI's questions are answered by a.answer (dashboard.go): the
	// dashboard's sign-in link, and the last work session `pairtime recap`
	// asks for, which answerQuestion gives from the bucket store that
	// startScorer opened before this.
	l := &ipcListener{a: a, sink: sink, opts: ipc.Options{ForcePipe: opts.ForcePipe, Log: a.log, Walk: walk,
		Answer: a.answer}}
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
				a.drain(drainer)
				if first {
					a.markDrained()
				}
				first = false
			}
		}
	}()
	kick <- struct{}{}
	a.spoolKick.Store(&kick)
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
		a.spoolKick.Store(nil)
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

// spoolSlack is how long after its own stamp a hook call can still reach
// the inbox's spool: pairtime hook reads stdin for up to StdinWait, and
// spools the call when the agent's ack doesn't come within HookTimeout,
// and a second covers the spool's write. drainSoon drains a little later
// than that after a switch back, for the scheduler's lag.
const (
	spoolSlack     = hookpath.StdinWait + ipc.HookTimeout + time.Second
	drainSoonAfter = spoolSlack + 250*time.Millisecond
)

// spoolBusyHold bounds how long a spool file a scan can't read to its end,
// as one that stays busy, holds the mark (spoolHolds): as long as the
// drains that may still take it, MaxFileFailures a minute apart, and
// drainSoon's. By then a drain has taken the file or set it aside, or
// another program keeps it from every drain that long, and its calls wait
// for a drain that can take it.
const spoolBusyHold = inbox.MaxFileFailures*drainEvery + drainSoonAfter

// scanSpool is inbox.Scan, which a test replaces.
var scanSpool = inbox.Scan

// spoolScanned bounds how many of a spool file's calls spoolHolds reads.
// Senders append calls in the order they make them, each within
// spoolSlack of its stamp, so one made before a moment comes after one
// made after it only in the seconds around it, and the calls after those
// in a file were all made later.
const spoolScanned = 1024

// drainSoon has the inbox drained once spoolSlack has passed, rather than
// at the next minute's drain, as the switch back online after a local-only
// stretch asks (modes): a hook call made just before the switch that went
// to the spool, its ack late, holds the mark of the first batch after the
// switch while it waits there (spoolHolds), and this has it reach the
// ledger writer within seconds, its batch sealed local-only by the call's
// own time in the stretch's run. Without a drain running, as before
// startIPC, it does nothing: the first drain comes as startIPC starts.
func (a *agent) drainSoon() {
	k := a.spoolKick.Load()
	if k == nil || a.sched == nil {
		return
	}
	kick := *k
	a.sched.After("inbox.drain.soon", drainSoonAfter, func(time.Time) {
		select {
		case kick <- struct{}{}:
		default: // a drain is already due
		}
	})
}

// spoolHolds reports whether the inbox's spool holds a hook call, which no
// drain has handed to the adapters yet, that before says may have been
// made before config.toml was last seen local-only, for the ledger
// writer's Waiting (agent.waiting). It judges each call as Write will
// judge its records once a drain hands it over: by the times they will
// carry (hookpath.Times) and by the hook's own stamp (stampOf). So a call
// made after the switch, as one spooled while the agent restarted, holds
// nothing. drainSoon has a call made before the switch handed over within
// seconds. It reads the spool from the agent's start, before startIPC's
// first drain, since that drain runs in this run. A spool file Scan
// couldn't read to its end, as one it couldn't open for a moment while a
// drain renames it on Windows, or one that holds more calls than a scan
// reads (spoolScanned), may hold one, so it holds the mark, for
// spoolBusyHold at most, while the other files are read on: one another
// program keeps busy longer is passed over. The spool is used only while
// hooks can't reach the socket, so it is empty almost always. With no
// drain running in this run, as when the journal couldn't open, its calls
// wait for the next start, and hold nothing.
func (a *agent) spoolHolds(before func(event.Envelope) bool) bool {
	if a.spoolNoDrain.Load() {
		return false
	}
	now := clock.Now()
	found, err := scanSpool(a.dirs.Inbox(), spoolScanned, func(m inbox.Message) bool {
		return stampOf(inbox.Delivery{Message: m, Via: inbox.ViaInbox, Received: now}).waits(before)
	})
	if err == nil {
		a.spoolBusySince.Store(nil)
		return found
	}
	// Only the writer's seals ask, one at a time, and a batch sealed online
	// ends the asks in a row (agent.sealed).
	since := a.spoolBusySince.Load()
	if since == nil {
		t := time.Now()
		a.spoolBusySince.Store(&t)
		since = &t
	}
	if d := time.Since(*since); d < spoolBusyHold {
		return true
	} else if d < spoolBusyHold+time.Minute {
		a.log.Warn("a file of the inbox's spool can't be read to its end, so the first batch after config.toml went back online no longer waits for it", "err", err)
	}
	return false
}

// expireInbox deletes, unread, the spool files of the inbox whose first
// call has waited inbox.SpoolKeep (D-inbox-age-cap): at the agent's start,
// before its first drain, and with each retention look, so a call waits
// unread no longer than held.jsonl keeps one, whether or not a drain runs,
// as while the journal or the ledger can't open.
func (a *agent) expireInbox() {
	n, err := inbox.Expire(a.dirs.Inbox(), time.Now())
	if n > 0 {
		a.log.Warn("deleted spool files of the inbox, unread, as they waited longer than hook calls are kept", "files", n, "kept", inbox.SpoolKeep)
	}
	if err != nil {
		a.log.Warn("the inbox's spool files that waited too long", "err", err)
		a.setProblem("inbox", fmt.Sprintf("pairtime couldn't delete hook calls that waited in its inbox longer than %d days, and tries again within the hour: %v",
			int(inbox.SpoolKeep/(24*time.Hour)), err))
		return
	}
	a.setProblem("inbox", "")
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
	// Spooled counts the hook calls of the last week that only the spool
	// delivered, for doctor (DESIGN 5.2).
	Spooled spooledReply `json:"spooled"`
}

// spooledWindow is how far back doctor's spool line looks: the last week,
// which the journal keeps (inbox.KeepDays).
const spooledWindow = inbox.KeepDays * 24 * time.Hour

// spooledReply counts the hook calls made since Since that reached the
// agent only through the spool, as when it wasn't running or didn't take
// them in time. Their events get via = plugin, so they earn side-door
// credit (DESIGN 5.2; D-spool-via), which doctor's spool line measures.
type spooledReply struct {
	Since time.Time `json:"since"`
	Calls uint64    `json:"calls"`
	// Prompts is how many of the calls were prompts, and Unknown how many
	// the journal can't tell, whose lines an older version wrote.
	Prompts uint64 `json:"prompts"`
	Unknown uint64 `json:"unknown,omitempty"`
}

func (a *agent) ipcStatus() ipcReply {
	var r ipcReply
	r.Spooled.Since = time.Now().Add(-spooledWindow).UTC().Truncate(time.Second)
	if a.journal != nil {
		r.Stored, r.Duplicates = a.journal.Stats()
		r.Spooled.Calls, r.Spooled.Prompts, r.Spooled.Unknown = a.journal.Spooled(r.Spooled.Since)
	}
	if srv := a.ipcSrv.Load(); srv != nil {
		// Via is "" once the server stopped listening, as it does when
		// Serve gives up.
		r.Via, r.Refused = srv.Via(), srv.Refused()
	}
	return r
}
