// SPDX-License-Identifier: AGPL-3.0-or-later

package adapters

import (
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/some-du6e/pairtime/pkg/event"
)

// Factory makes an adapter with the agent's keys and project resolver. An
// adapter keeps memory of the sessions it parses, so the agent makes one of
// each and keeps it.
type Factory func(Options) Adapter

// Registry holds the adapters pairtime ships, by harness. Each adapter
// package registers itself in Default from an init function, and a binary
// that imports it gets it, so adding a harness adds a package and leaves
// this one alone (DESIGN 3.4).
type Registry struct {
	mu        sync.RWMutex
	factories map[event.Harness]Factory
}

// Default is the registry adapter packages register in.
var Default = &Registry{}

// Register adds the factory for harness h to Default.
func Register(h event.Harness, f Factory) { Default.Register(h, f) }

// Register adds the factory for harness h. It panics on a harness pkg/event
// doesn't know, a nil factory, or a second registration, which fails every
// test of a binary that imports both, so two adapters for one harness are
// found in CI.
func (r *Registry) Register(h event.Harness, f Factory) {
	if h == event.HarnessNone || f == nil || !knownHarness(h) {
		panic(fmt.Sprintf("adapters: can't register %q: it needs a factory and a harness pkg/event knows", h))
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.factories == nil {
		r.factories = map[event.Harness]Factory{}
	}
	if r.factories[h] != nil {
		panic(fmt.Sprintf("adapters: %q is registered twice", h))
	}
	r.factories[h] = f
}

// New makes harness h's adapter, or reports that none is registered.
func (r *Registry) New(h event.Harness, opts Options) (Adapter, bool) {
	r.mu.RLock()
	f := r.factories[h]
	r.mu.RUnlock()
	if f == nil {
		return nil, false
	}
	return f(opts), true
}

// Harnesses returns the registered harnesses, sorted.
func (r *Registry) Harnesses() []event.Harness {
	r.mu.RLock()
	defer r.mu.RUnlock()
	hs := make([]event.Harness, 0, len(r.factories))
	for h := range r.factories {
		hs = append(hs, h)
	}
	slices.Sort(hs)
	return hs
}

// knownHarness reports whether h is one of pkg/event's harness values,
// which pkg/event doesn't export: Validate names the harness field of an
// event whose harness it doesn't know, and of no other.
func knownHarness(h event.Harness) bool {
	if h == "" {
		return false
	}
	e := event.Event{Envelope: event.Envelope{Harness: h}, Data: &event.SessionEnd{}}
	for _, err := range unwrap(e.Validate()) {
		if strings.HasPrefix(err.Error(), "harness: ") {
			return false
		}
	}
	return true
}

func unwrap(err error) []error {
	if err == nil {
		return nil
	}
	if j, ok := err.(interface{ Unwrap() []error }); ok {
		return j.Unwrap()
	}
	return []error{err}
}
