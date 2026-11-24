// SPDX-License-Identifier: AGPL-3.0-or-later

package ingest

import (
	"math"
	"sync"
	"time"
)

// maxTracked bounds the devices the limiter remembers. Past it, take first
// forgets every device that is idle with a full bucket, which is the state
// a forgotten device starts in again.
const maxTracked = 10000

// limiter is each device's upload rate (SP 8.6; ingest-v1 2.5 step 11): a
// bucket of burst requests that refills one every every, and one request
// in flight at a time. It counts in memory, which suits pairtimed's one
// process (DESIGN 3.10).
type limiter struct {
	mu      sync.Mutex
	burst   float64
	every   time.Duration
	devices map[string]*rate
}

type rate struct {
	tokens float64
	at     time.Time
	busy   bool
}

func newLimiter(burst int, every time.Duration) *limiter {
	return &limiter{burst: float64(burst), every: every, devices: make(map[string]*rate)}
}

// refill adds the tokens earned since st.at. A clock that went back adds
// none.
func (l *limiter) refill(st *rate, now time.Time) {
	if now.After(st.at) {
		st.tokens = min(l.burst, st.tokens+float64(now.Sub(st.at))/float64(l.every))
		st.at = now
	}
}

// take admits a request of device id at now and returns the func that ends
// it. It refuses one while another of the device's is in flight, or when
// the device's bucket is empty, with the seconds to wait.
func (l *limiter) take(id string, now time.Time) (func(), *refusal) {
	l.mu.Lock()
	defer l.mu.Unlock()
	st, ok := l.devices[id]
	if !ok {
		if len(l.devices) >= maxTracked {
			l.prune(now)
		}
		st = &rate{tokens: l.burst, at: now}
		l.devices[id] = st
	}
	l.refill(st, now)
	switch {
	case st.busy:
		r := refuse(CodeRateLimited, "Another upload from this device is still being answered; wait for it, then send again.")
		r.retryAfter = 1
		return nil, r
	case st.tokens < 1:
		r := refuse(CodeRateLimited, "This device is uploading faster than its rate; wait Retry-After seconds, then send the same body and key.")
		r.retryAfter = max(1, int(math.Ceil((1-st.tokens)*l.every.Seconds())))
		return nil, r
	}
	st.tokens--
	st.busy = true
	return func() {
		l.mu.Lock()
		st.busy = false
		l.mu.Unlock()
	}, nil
}

// prune forgets every idle device whose bucket is full.
func (l *limiter) prune(now time.Time) {
	for id, st := range l.devices {
		l.refill(st, now)
		if !st.busy && st.tokens >= l.burst {
			delete(l.devices, id)
		}
	}
}

// Limiter is a device's request rate as ingest counts it (SP 8.6): a
// bucket of burst requests that refills one every every, and one request
// in flight at a time. The teen layer's routes (package devlog) keep one
// of their own: they take only the device token, and ingest counts only
// signed requests against a device's upload rate, so that nobody without
// the device's key can use it up.
type Limiter struct{ l *limiter }

// NewLimiter returns a Limiter of burst requests refilled one every every,
// as Options.RateBurst and RateEvery are ingest's.
func NewLimiter(burst int, every time.Duration) *Limiter {
	return &Limiter{l: newLimiter(burst, every)}
}

// Take admits a request of device id at now and returns the func that ends
// it. While another of the device's requests is in flight, or when its
// bucket is empty, it returns nil and the seconds to wait.
func (l *Limiter) Take(id string, now time.Time) (done func(), retryAfter int) {
	done, r := l.l.take(id, now)
	if r != nil {
		return nil, r.retryAfter
	}
	return done, 0
}
