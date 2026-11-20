// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build linux

package linux

import (
	"context"
	"errors"
	"net"
	"sync"
	"time"
)

// errNoScreenSaver means the X server lacks the MIT-SCREEN-SAVER extension,
// whose QueryInfo is the X server's own idle counter.
var errNoScreenSaver = errors.New("the X server has no MIT-SCREEN-SAVER extension")

// x11Idle reads idle time from the X server's MIT-SCREEN-SAVER extension
// (XScreenSaverQueryInfo's request, CP 3.3). It keeps one connection open
// between samples, and reconnects on the next sample after any failure.
type x11Idle struct {
	// open connects to the display by deadline; openX11 outside tests.
	open func(deadline time.Time) (*xconn, error)

	mu     sync.Mutex
	c      *xconn
	major  byte // MIT-SCREEN-SAVER's major opcode on c
	closed bool // after close, samples fail rather than reconnect
}

// connect opens the connection and checks for the extension, all by
// deadline. s.mu is held.
func (s *x11Idle) connect(deadline time.Time) error {
	c, err := s.open(deadline)
	if err != nil {
		return err
	}
	if err := c.conn.SetDeadline(deadline); err != nil {
		c.close()
		return err
	}
	major, ok, err := c.queryExtension(screenSaverExtension)
	if err == nil && !ok {
		err = errNoScreenSaver
	}
	if err == nil {
		err = c.screenSaverVersion(major)
	}
	if err != nil {
		c.close()
		return err
	}
	s.c, s.major = c, major
	return nil
}

func (s *x11Idle) idle(ctx context.Context) (time.Duration, error) {
	// One deadline covers the whole sample: the dials, the connection
	// setup and the extension check when it has to reconnect, and the
	// query.
	deadline, err := sampleDeadline(ctx, sampleTimeout)
	if err != nil {
		return 0, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return 0, net.ErrClosed
	}
	if s.c == nil {
		if err := s.connect(deadline); err != nil {
			return 0, err
		}
	}
	ms, err := s.query(deadline)
	if err != nil {
		s.c.close()
		s.c = nil
		return 0, err
	}
	return time.Duration(ms) * time.Millisecond, nil
}

func (s *x11Idle) query(deadline time.Time) (uint32, error) {
	if err := s.c.conn.SetDeadline(deadline); err != nil {
		return 0, err
	}
	return s.c.idleSince(s.major)
}

func (s *x11Idle) name() string { return screenSaverExtension }

func (s *x11Idle) close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	if s.c != nil {
		s.c.close()
		s.c = nil
	}
}

// sampleTimeout bounds one idle sample, reconnecting included. The display
// server or Mutter answers in well under a millisecond; one that takes a
// second is hung, and the sample is better missing than late.
const sampleTimeout = time.Second

// sampleDeadline is when a sample has to be done: limit from now, or ctx's
// deadline if that is sooner.
func sampleDeadline(ctx context.Context, limit time.Duration) (time.Time, error) {
	if err := ctx.Err(); err != nil {
		return time.Time{}, err
	}
	deadline := time.Now().Add(limit)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	if time.Until(deadline) <= 0 {
		return time.Time{}, context.DeadlineExceeded
	}
	return deadline, nil
}
