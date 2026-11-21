// SPDX-License-Identifier: AGPL-3.0-or-later

package uploader

import (
	"math"
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

// The uploader's own pace, under ingest's rate of a burst of 30 requests
// and one more every 10 s for each device (ingest-v1 7 item 17), with room
// to spare: a burst of paceBurst, then one request every paceEvery, five a
// minute. However busy a session, whose prompts and stops each bring an
// upload forward, the server's bucket never runs dry from this device's
// own requests, so none is refused as rate_limited and no 429 of its own
// making becomes an upload-failure note. State reads count, as they do on
// the server.
const (
	paceBurst = 12
	paceEvery = 12 * time.Second
)

// pacer is a bucket of requests that refills one every every, up to
// burst, on the wall clock the server's limiter counts by. Only the
// uploader's loop uses it.
type pacer struct {
	burst  float64
	every  time.Duration
	tokens float64
	at     time.Time
}

func newPacer(burst int, every time.Duration) *pacer {
	return &pacer{burst: float64(burst), every: every, tokens: float64(burst)}
}

// refill adds the requests earned since it last ran. A clock that went
// back adds none.
func (p *pacer) refill(now time.Time) {
	if !p.at.IsZero() && now.After(p.at) {
		p.tokens = min(p.burst, p.tokens+float64(now.Sub(p.at))/float64(p.every))
	}
	if p.at.IsZero() || now.After(p.at) {
		p.at = now
	}
}

// wait returns how long after now the next request may go.
func (p *pacer) wait(now time.Time) time.Duration {
	p.refill(now)
	if p.tokens >= 1 {
		return 0
	}
	return time.Duration(math.Ceil((1 - p.tokens) * float64(p.every)))
}

// take spends a request at now. A request that goes whatever the pace, as
// on lock and suspend, or the second of a cycle, can take it below one, and
// the requests after it wait the longer.
func (p *pacer) take(now time.Time) {
	p.refill(now)
	p.tokens--
}

// drain spends every request left, after the server refused one as too
// fast: the requests after it go at the pace alone, which the server's
// bucket refills faster than.
func (p *pacer) drain(now time.Time) {
	p.refill(now)
	p.tokens = min(p.tokens, 0)
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
