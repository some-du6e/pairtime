// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build linux

package linux

import (
	"context"
	"errors"
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
	// open connects to the display; openX11 outside tests.
	open func(timeout time.Duration) (*xconn, error)

	mu    sync.Mutex
	c     *xconn
	major byte // MIT-SCREEN-SAVER's major opcode on c
}

// connect opens the connection and checks for the extension. s.mu is held.
func (s *x11Idle) connect(timeout time.Duration) error {
	c, err := s.open(timeout)
	if err != nil {
		return err
	}
	if err := c.conn.SetDeadline(time.Now().Add(timeout)); err != nil {
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
	timeout, err := callTimeout(ctx)
	if err != nil {
		return 0, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.c == nil {
		if err := s.connect(timeout); err != nil {
			return 0, err
		}
	}
	ms, err := s.query(timeout)
	if err != nil {
		s.c.close()
		s.c = nil
		return 0, err
	}
	return time.Duration(ms) * time.Millisecond, nil
}

func (s *x11Idle) query(timeout time.Duration) (uint32, error) {
	if err := s.c.conn.SetDeadline(time.Now().Add(timeout)); err != nil {
		return 0, err
	}
	return s.c.idleSince(s.major)
}

func (s *x11Idle) name() string { return screenSaverExtension }

func (s *x11Idle) close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.c != nil {
		s.c.close()
		s.c = nil
	}
}

// sampleTimeout bounds one idle query. The display server or Mutter
// answers in well under a millisecond; one that takes a second is hung,
// and the sample is better missing than late.
const sampleTimeout = time.Second

// callTimeout is sampleTimeout, or less if ctx ends sooner.
func callTimeout(ctx context.Context) (time.Duration, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	d := sampleTimeout
	if deadline, ok := ctx.Deadline(); ok {
		if left := time.Until(deadline); left < d {
			d = left
		}
	}
	if d <= 0 {
		return 0, context.DeadlineExceeded
	}
	return d, nil
}
