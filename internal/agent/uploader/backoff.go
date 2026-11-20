// SPDX-License-Identifier: AGPL-3.0-or-later

package uploader

import (
	"math/rand/v2"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The backoff between failed uploads: full jitter, a wait drawn evenly
// from 0 to min(backoffCap, backoffBase × 2^(failures - 1)), so devices
// that lost the server together don't come back together; and never
// shorter than a Retry-After the server sent, up to retryAfterMax.
const (
	backoffBase   = 5 * time.Second
	backoffCap    = 10 * time.Minute
	retryAfterMax = time.Hour
)

// backoff counts failures in a row and draws the wait before the next
// try. Its methods are safe for concurrent use.
type backoff struct {
	// draw returns a random duration in [0, n). Tests replace it.
	draw func(n int64) int64

	mu       sync.Mutex
	failures int
}

func newBackoff(draw func(n int64) int64) *backoff {
	if draw == nil {
		draw = rand.Int64N
	}
	return &backoff{draw: draw}
}

// fail records a failure and returns how long to wait before the next
// try: full jitter over the doubled ceiling, and at least retryAfter,
// which is 0 when the server sent none.
func (b *backoff) fail(retryAfter time.Duration) time.Duration {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.failures++
	ceiling := backoffCap
	if shift := b.failures - 1; shift < 30 {
		ceiling = min(backoffCap, backoffBase<<shift)
	}
	wait := time.Duration(b.draw(int64(ceiling) + 1))
	return max(wait, min(retryAfter, retryAfterMax))
}

// ok records a success, which starts the doubling again.
func (b *backoff) ok() {
	b.mu.Lock()
	b.failures = 0
	b.mu.Unlock()
}

// failing returns the number of failures in a row.
func (b *backoff) failing() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.failures
}

// retryAfter reads a response's Retry-After (RFC 9110 10.2.3): a number of
// seconds, or an HTTP date, which now measures. It returns 0 when there is
// none or it doesn't parse, and never less than 0.
func retryAfter(h http.Header, now time.Time) time.Duration {
	v := strings.TrimSpace(h.Get("Retry-After"))
	if v == "" {
		return 0
	}
	if s, err := strconv.ParseUint(v, 10, 32); err == nil {
		return time.Duration(s) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		return max(t.Sub(now), 0)
	}
	return 0
}
