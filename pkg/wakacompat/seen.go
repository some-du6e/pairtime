// SPDX-License-Identifier: AGPL-3.0-or-later

package wakacompat

import (
	"slices"
	"time"
)

// Seen is the set of heartbeats an endpoint took, by Key. It remembers a
// heartbeat while the heartbeat's own time is within its window of now,
// and can say of a heartbeat whether it is new, one it took, or too old
// for it to tell. It holds at most max keys, and past that forgets the
// older half. It is not safe for concurrent use.
//
// A replay can come long after the first send. While Hackatime can't be
// reached, wakatime-cli sends each batch to every destination in turn and
// queues the whole batch again when Hackatime's send fails, so a local
// endpoint gets the batch again at each try, and once more when Hackatime
// answers, as long after as the outage lasted
// (docs/errata/2026-11-23-wakatime-cli-2.28.4.md).
type Seen struct {
	window time.Duration
	max    int
	keys   map[Key]float64
	pruned time.Time
}

// Status is what Seen says of a heartbeat.
type Status int

const (
	// New is a heartbeat Seen hasn't been told of.
	New Status = iota
	// Taken is one Seen was told of with Add.
	Taken
	// Stale is one whose time is further back than the window, which
	// Seen can't tell from one it took and forgot.
	Stale
)

// NewSeen returns an empty Seen that remembers heartbeats whose times are
// within window of now, at most limit of them. A limit below 2 means 2.
func NewSeen(window time.Duration, limit int) *Seen {
	return &Seen{window: window, max: max(limit, 2), keys: make(map[Key]float64)}
}

// Check says what the heartbeat with key k and time t is, at now.
func (s *Seen) Check(k Key, t float64, now time.Time) Status {
	if t < s.cutoff(now) {
		return Stale
	}
	if _, ok := s.keys[k]; ok {
		return Taken
	}
	return New
}

// Add remembers k, the key of a heartbeat with time t. Once in each
// thirtieth of the window, and whenever Seen holds max keys, it first
// forgets the keys whose times have left the window, and when that isn't
// enough, the older half of the rest.
func (s *Seen) Add(k Key, t float64, now time.Time) {
	if now.Sub(s.pruned) >= s.window/30 || len(s.keys) >= s.max {
		s.prune(now)
	}
	s.keys[k] = t
}

// Len is how many keys Seen holds.
func (s *Seen) Len() int { return len(s.keys) }

func (s *Seen) cutoff(now time.Time) float64 {
	return float64(now.Add(-s.window).UnixNano()) / 1e9
}

func (s *Seen) prune(now time.Time) {
	s.pruned = now
	cut := s.cutoff(now)
	for k, t := range s.keys {
		if t < cut {
			delete(s.keys, k)
		}
	}
	if len(s.keys) < s.max {
		return
	}
	times := make([]float64, 0, len(s.keys))
	for _, t := range s.keys {
		times = append(times, t)
	}
	slices.Sort(times)
	mid := times[len(times)/2]
	for k, t := range s.keys {
		if t < mid {
			delete(s.keys, k)
		}
	}
	if len(s.keys) >= s.max {
		// Half of them or more share one time, which only a flood of
		// heartbeats can do. Forget them all rather than grow.
		clear(s.keys)
	}
}
