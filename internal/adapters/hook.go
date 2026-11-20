// SPDX-License-Identifier: AGPL-3.0-or-later

package adapters

import (
	"fmt"

	"github.com/some-du6e/pairtime/internal/agent/hookpath"
	"github.com/some-du6e/pairtime/internal/agent/inbox"
	"github.com/some-du6e/pairtime/pkg/event"
)

// HookRecord turns a hook call the agent took in, d, into the raw record
// adapter a parses: a's own, since a's harness is what the call's
// --harness names. goos is the OS whose process names d's walk holds.
//
// The via is the agent's (D-spool-via; DESIGN 3.4, 5.2). It is hook only
// when the call came over the socket or the pipe, the agent's own walk of
// its parent chain counts and the process the hook ran under is one of a's
// Hosts (hookpath.Of), and plugin for every other call: one through the
// spool whatever chain its line records, one whose walk didn't count or
// reached no host, one under another harness. Only with a hook via does
// the record carry the walk's lineage, which is where SM 1.2 takes
// parent_class, has_tty and an IDE or desktop entrypoint from.
// Arrival.Harness names the harness that ran the hook, by the walk or,
// with none, by the chain the call recorded, for DESIGN 5.1's
// deduplication alone.
//
// The call's times are the hook path's (hookpath.Times): the sender's own
// stamp, since a drain can come long after the call, but never later than
// when the agent took the call in, d.Received.
func HookRecord(a Adapter, goos string, d inbox.Delivery) (RawRecord, error) {
	b, err := hookpath.DecodeBody(d.Message)
	if err != nil {
		return RawRecord{}, err
	}
	if b.Map != "" {
		return RawRecord{}, fmt.Errorf("adapters: a generic hook call (--map) isn't %s's", a.Name())
	}
	if event.Harness(b.Harness) != a.Name() {
		return RawRecord{}, fmt.Errorf("adapters: a %q hook call isn't %s's", b.Harness, a.Name())
	}
	p := hookpath.Of(goos, d, a.Name())
	arr := Arrival{Via: event.ViaPlugin, Harness: p.Harness}
	if p.Via == event.ViaHook && p.Host != nil && MatchesAny(a.Hosts(), goos, *p.Host) {
		l := p.Lineage
		arr.Via, arr.Walk = event.ViaHook, &l
	}
	at := hookpath.Times(d)
	arr.T, arr.Mono, arr.BootID = at.Wall.UnixMilli(), at.Mono, at.BootID
	return RawRecord{
		Source:     SourceHook,
		Event:      b.Event,
		Payload:    b.Payload,
		Incomplete: b.Cut || b.Dropped || len(b.Payload) == 0 && b.Bytes > 0,
		Arrival:    arr,
	}, nil
}
