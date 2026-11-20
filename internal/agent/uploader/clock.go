// SPDX-License-Identifier: AGPL-3.0-or-later

package uploader

import (
	"net/http"
	"strconv"
	"sync"
	"time"
)

// clock is the device's reading of the server's clock: the local clock
// plus the offset the latest response measured (ingest-v1 5 step 2). The
// offset only corrects X-Pairtime-Time, which the server refuses more
// than 300 s from its own clock; event times keep the device's clock.
// Its methods are safe for concurrent use.
type clock struct {
	now func() time.Time

	mu     sync.Mutex
	offset time.Duration
	known  bool
}

// serverNow returns the local time corrected by the offset.
func (c *clock) serverNow() time.Time {
	c.mu.Lock()
	off := c.offset
	c.mu.Unlock()
	return c.now().Add(off)
}

// Offset returns the offset in use, and whether a response has set one.
func (c *clock) Offset() (time.Duration, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.offset, c.known
}

// observe takes the server time a response carried, in Unix ms, and the
// local times its request went out and its answer came in, and sets the
// offset to the server's time less the middle of that span.
func (c *clock) observe(serverMS int64, sent, got time.Time) {
	mid := sent.Add(got.Sub(sent) / 2)
	off := time.UnixMilli(serverMS).Sub(mid)
	c.mu.Lock()
	c.offset, c.known = off, true
	c.mu.Unlock()
}

// serverTime reads X-Pairtime-Time, which every ingest response carries
// (ingest-v1 1), and false when it is missing or isn't a time.
func serverTime(h http.Header) (int64, bool) {
	v := h.Get("X-Pairtime-Time")
	if v == "" {
		return 0, false
	}
	ms, err := strconv.ParseInt(v, 10, 64)
	return ms, err == nil && ms > 0
}
