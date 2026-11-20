// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"slices"

	"github.com/some-du6e/pairtime/internal/agent/focus"
	"github.com/some-du6e/pairtime/internal/agent/presence"
)

// osSources are the OS's presence, focus and power sources, which openOS
// opens once per agent (WP-OS9). On macOS and Linux presence and focus read
// one connection to the OS between them: the MacLoop that cmd/pairtime
// runs on the main thread, or one linux.Desktop. On Windows each has its
// own (WP-OS1, WP-OS2).
type osSources struct {
	presence presence.OS
	focus    focus.OS
	// signals says where each signal comes from now, for GET /v1/status
	// (DESIGN 5.9).
	signals func() signalsReply
	// close, when set, closes the connection once presence and focus have
	// stopped.
	close func()
}

// signalsReply is GET /v1/status's signals: where idle time, focus, power
// and the lock state come from on this desktop, or why each is unknown.
// doctor's idle and focus lines read it from the running agent, so they
// say what the agent reads, not what the terminal doctor runs in can reach
// (DESIGN 5.9, D-design-5.9).
type signalsReply struct {
	Idle  signalReply `json:"idle"`
	Focus signalReply `json:"focus"`
	// Power covers suspend and resume.
	Power signalReply `json:"power"`
	Lock  signalReply `json:"lock"`
	// Session is the kind of Linux desktop session: x11, wayland or none.
	Session string `json:"session,omitempty"`
	// Loop is the state of the macOS notification loop, once it runs.
	Loop *loopReply `json:"loop,omitempty"`
}

// signalReply is one signal's source, or why there is none: exactly one of
// its fields is set.
type signalReply struct {
	Via     string `json:"via,omitempty"`
	Unknown string `json:"unknown,omitempty"`
}

// loopReply is what darwin.Loop's Status says beyond its sources.
type loopReply struct {
	// MainThread is set when the Loop runs on the process's main thread,
	// where macOS delivers screen lock and app switch notifications. When
	// it isn't, doctor warns that locks and app switches may not arrive.
	MainThread bool `json:"main_thread"`
	// Dropped counts the events lost because the agent fell behind.
	Dropped uint64 `json:"dropped"`
}

func via(name string) signalReply       { return signalReply{Via: name} }
func unknown(reason string) signalReply { return signalReply{Unknown: reason} }

// signalsNow says where each signal comes from. An agent made without
// openOS, as in tests of one component, reports each as unknown.
func (a *agent) signalsNow() signalsReply {
	if a.signals == nil {
		const none = "the agent hasn't opened the OS's sources"
		return signalsReply{Idle: unknown(none), Focus: unknown(none), Power: unknown(none), Lock: unknown(none)}
	}
	return a.signals()
}

// Why power and lock are unknown while the OS names a source for them but
// the sampler runs no watch on it.
const (
	noSuspendWatch = "the suspend and resume watch isn't running; problems.presence says why"
	noLockWatch    = "the lock and unlock watch isn't running; problems.presence says why"
)

// watched makes power and lock in r unknown while the sampler runs no
// watch for them, so the reply never names a source the agent doesn't
// listen to. The OS layer says where suspends and locks come from on this
// desktop; only the sampler knows whether the agent hears them, and seals
// a batch on each (DESIGN 3.3, 3.6). Every OS's signals pass through it.
func (a *agent) watched(r signalsReply) signalsReply {
	var watching []string
	if a.presence != nil {
		watching = a.presence.Status().Watching
	}
	if r.Power.Via != "" && !slices.Contains(watching, "suspend") {
		r.Power = unknown(noSuspendWatch)
	}
	if r.Lock.Via != "" && !slices.Contains(watching, "lock") {
		r.Lock = unknown(noLockWatch)
	}
	return r
}

// componentSignals reads the sources from the components themselves, where
// the OS layer keeps no status of its own: the focus component's source,
// and the sampler's power watches. Windows uses it, and so do the OSes
// pairtime doesn't ship for.
func (a *agent) componentSignals(idle, power, lock string) signalsReply {
	r := signalsReply{
		Idle:  unknown("pairtime reads no idle time on this OS"),
		Power: unknown("pairtime sees no suspends on this OS"),
		Lock:  unknown("pairtime sees no locks on this OS"),
	}
	if idle != "" {
		r.Idle = via(idle)
	}
	if power != "" {
		r.Power = via(power)
	}
	if lock != "" {
		r.Lock = via(lock)
	}
	r = a.watched(r)
	r.Focus = unknown("the focus component hasn't started")
	if a.focus != nil {
		st := a.focus.Status()
		switch {
		case st.NoSource != "":
			r.Focus = unknown(st.NoSource)
		case st.Via != "":
			r.Focus = via(st.Via)
		}
	}
	return r
}
