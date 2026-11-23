// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/some-du6e/pairtime/internal/agent/focus"
	"github.com/some-du6e/pairtime/internal/agent/presence"
	"github.com/some-du6e/pairtime/internal/platform/darwin"
	"github.com/some-du6e/pairtime/pkg/event"
)

// MacLoop is the agent's macOS notification loop: a darwin.Loop whose
// handler passes each suspend, resume, lock and unlock to presence and each
// app activation to focus (WP-OS9). macOS delivers screen lock and app
// switch notifications only on the process's main thread (the darwin
// package doc; TestFindingRunLoop), so the Loop can't run inside Run.
// cmd/pairtime makes a MacLoop, whose init keeps the main goroutine on the
// main thread, passes it in Options.Mac, runs the agent on another
// goroutine and calls the MacLoop's Run on the main one until the agent
// stops.
//
// It builds on every OS, so ci.yml tests the agent's use of it with fakes;
// its Run works on macOS only.
type MacLoop struct {
	loop  notifyLoop
	power presence.MacPower
	focus focus.MacFocus

	mu      sync.Mutex
	stopped bool  // Run has returned
	err     error // what it returned
}

// notifyLoop is what a MacLoop uses of its darwin.Loop. The Loop's Status
// comes from macOS, so ci.yml's tests give a MacLoop a fake one.
type notifyLoop interface {
	Run(ctx context.Context) error
	Ready() <-chan struct{}
	Status() darwin.Status
}

// NewMacLoop returns a MacLoop that delivers nothing until Run.
func NewMacLoop() *MacLoop {
	m := &MacLoop{}
	m.loop = darwin.NewLoop(macHandler{m})
	return m
}

// Run registers for the notifications on the calling goroutine's thread and
// passes them on until ctx is done, as darwin.Loop.Run does, and returns
// what that returns. Call it on the main goroutine, with the main thread
// locked to it. It returns at once with an error when no notification
// source registers, or anywhere but macOS; the agent goes on without them,
// and GET /v1/status says why.
//
// MacFocus.Watch starts whether or not activations will come, so Run tells
// focus when they won't: once the Loop's Status says the activation
// observer didn't register, or when the Loop's Run fails. Focus is unknown
// from then on, and the focus component records it (SM 2.5).
func (m *MacLoop) Run(ctx context.Context) error {
	observed := make(chan struct{})
	go func() {
		defer close(observed)
		// Ready closes once the Loop has registered its sources, or once
		// its Run has returned without getting that far.
		<-m.loop.Ready()
		if err := m.loop.Status().Focus; err != nil {
			m.focus.Unknown(time.Now(), err.Error())
		}
	}()
	err := m.loop.Run(ctx)
	<-observed
	if err != nil {
		m.focus.Unknown(time.Now(), loopFailed(err))
	}
	m.mu.Lock()
	m.stopped, m.err = true, err
	m.mu.Unlock()
	return err
}

// loopFailed says why the notification loop's Run returned err, for
// GET /v1/status and the focus component.
func loopFailed(err error) string {
	if errors.Is(err, errors.ErrUnsupported) {
		return "the macOS notification loop runs on macOS only"
	}
	return "the macOS notification loop didn't start: " + err.Error()
}

// macHandler is the Loop's darwin.Handler.
type macHandler struct{ m *MacLoop }

func (h macHandler) Power(at time.Time, op event.PowerOp) { h.m.power.Power(at, op) }
func (h macHandler) Focus(at time.Time, app darwin.App)   { h.m.focus.Focus(at, app) }

// Where each signal comes from on macOS, as GET /v1/status names it.
const (
	macIdleVia = "CGEventSourceSecondsSinceLastEventType in the HID system state, for any input and for key-down, which also counts key-downs other processes post; IOKit's HIDIdleTime when it can't answer"
	// darwin.Loop's sources.
	macPowerVia = "IOKit IORegisterForSystemPower"
	macLockVia  = "the com.apple.screenIsLocked and com.apple.screenIsUnlocked distributed notifications"
)

// noMacLoop is why power, lock and focus are unknown for an agent run
// without a MacLoop, as in package agent's tests.
const noMacLoop = "the agent runs without the macOS notification loop, which only `pairtime agent` starts on the main thread"

// signals says where each signal comes from (DESIGN 5.9): idle time from
// darwin.Presence, which needs no loop, and power, lock and focus from the
// Loop's Status, with the thread it runs on and the events it dropped. A
// nil MacLoop has no Loop.
func (m *MacLoop) signals() signalsReply {
	r := signalsReply{Idle: via(macIdleVia)}
	unknownAll := func(reason string) signalsReply {
		r.Power, r.Lock, r.Focus = unknown(reason), unknown(reason), unknown(reason)
		return r
	}
	if m == nil {
		return unknownAll(noMacLoop)
	}
	select {
	case <-m.loop.Ready():
	default:
		return unknownAll("the macOS notification loop hasn't started yet")
	}
	m.mu.Lock()
	stopped, err := m.stopped, m.err
	m.mu.Unlock()
	if stopped {
		if err != nil {
			return unknownAll(loopFailed(err))
		}
		return unknownAll("the macOS notification loop has stopped")
	}
	st := m.loop.Status()
	r.Power = sourceOf(macPowerVia, st.Power)
	r.Lock = sourceOf(macLockVia, st.Lock)
	r.Focus = sourceOf(focus.MacVia, st.Focus)
	r.Loop = &loopReply{MainThread: st.MainThread, Dropped: st.Dropped}
	return r
}

// sourceOf is the signal from source name, or unknown with err when that
// source didn't register.
func sourceOf(name string, err error) signalReply {
	if err != nil {
		return unknown(err.Error())
	}
	return via(name)
}
