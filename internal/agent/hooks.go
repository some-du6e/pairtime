// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"runtime"

	"github.com/some-du6e/pairtime/internal/agent/hookpath"
	"github.com/some-du6e/pairtime/internal/agent/inbox"
	"github.com/some-du6e/pairtime/internal/platform"
)

// hookSink is the Sink in front of the journal, which the socket, the pipe
// and the drain all deliver to (WP-AG3). For every hook call it works out
// where the call came from (hookpath.Of), for the journal to record, and
// for a call that came over the socket or the pipe it also:
//
//   - claims the harness process that the agent's own walk found under the
//     hook, so the process scan doesn't report that process's session as
//     one that fires no hooks (DESIGN 3.3);
//   - hands the call to presence with its session key and kind, so a
//     prompt gets the sample SM 2.2 rule 6 reads, and turns set D14's
//     cadence (presence.Sampler.Hook).
//
// It does both before the call is stored and acked, while the hook is
// still running, and whether or not storing works: the walk showed that
// the harness fires hooks either way.
//
// A spooled call does neither. Its line proves nothing about which process
// wrote it, so it claims nothing (D-spool-via), and the drain that reads it
// can come long after the call, when a sample says nothing about the
// prompt and a turn started then would be one that already ended.
type hookSink struct {
	a    *agent
	next inbox.Sink
	// key keys harness session ids for presence. It is random for each run
	// of the agent, which is as long as presence keeps a session.
	key []byte
}

func newHookSink(a *agent, next inbox.Sink) *hookSink {
	key := make([]byte, 32)
	rand.Read(key) // never fails: crypto/rand panics instead
	return &hookSink{a: a, next: next, key: key}
}

// Deliver stores d through the next sink, with what the agent made of a
// hook call. It is safe to call from many goroutines.
func (s *hookSink) Deliver(d inbox.Delivery) error {
	if d.Kind != hookpath.Kind {
		return s.next.Deliver(d)
	}
	p := hookpath.Of(runtime.GOOS, d)
	d.EventVia, d.Harness = p.Via, p.Harness
	if d.Via != inbox.ViaInbox {
		if p.Host != nil {
			s.claim(*p.Host)
		}
		if b, err := hookpath.DecodeBody(d.Message); err == nil && s.a.presence != nil {
			s.a.presence.Hook(s.sessionKey(b), hookpath.PresenceKind(b))
		}
	}
	return s.next.Deliver(d)
}

// claim tells the process scan that host fires hooks.
func (s *hookSink) claim(host platform.Proc) {
	if s.a.procs != nil {
		s.a.procs.ClaimLive(host)
	}
}

// sessionKey returns the opaque key presence keeps the call's session
// under: the harness's session id, keyed with s.key, or "" when the call
// names none. Presence keeps it in memory only, but it is a key all the
// same, never the harness's own id (AGENTS.md).
func (s *hookSink) sessionKey(b hookpath.Body) string {
	id := hookpath.SessionID(b)
	if id == "" {
		return ""
	}
	mac := hmac.New(sha256.New, s.key)
	mac.Write([]byte(b.Harness))
	mac.Write([]byte{0})
	mac.Write([]byte(id))
	return "s_" + hex.EncodeToString(mac.Sum(nil)[:8])
}

// walkPeer reads the parent chain of a connection's peer, for the socket
// and the pipe to hand on with each of its messages (ipc.Options.Walk). A
// table it can't read gives no walk, so the calls on that connection count
// as plugin calls.
func (a *agent) walkPeer(pid int) []platform.Proc {
	chain, err := hookpath.ReadChain(pid)
	if err != nil {
		a.log.Warn("couldn't read the parent chain of a hook", "pid", pid, "err", err)
		return nil
	}
	return chain
}
