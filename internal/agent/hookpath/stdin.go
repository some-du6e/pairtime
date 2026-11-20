// SPDX-License-Identifier: AGPL-3.0-or-later

package hookpath

import (
	"encoding/json"
	"fmt"
	"io"
	"time"
)

// Stdin is read into blocks of readBlock bytes, a new one each time fewer
// than minRead bytes of the last are left. A hook's event is a few
// kilobytes, so most calls make one block and copy nothing.
const (
	readBlock = 64 << 10
	minRead   = 4 << 10
)

// part is what one read of stdin returned.
type part struct {
	b   []byte // owned by whoever receives the part
	err error  // io.EOF or a read error, once reading has stopped
}

// readStdin reads r until EOF, until more than limit bytes have come, or
// until wait has passed, whichever is first. It also stops as soon as what
// came is one whole JSON object, because nothing says that every harness
// closes stdin after its event (CP 6.2). It returns at most limit bytes;
// whole, when they are one whole JSON value; and cut, when stdin went on
// past limit, or was still open when the wait ran out and what came so far
// isn't one whole JSON value.
//
// The reads run on a goroutine of their own, which is left blocked in Read
// when stdin stays open. The process exits soon after anyway. A panic in a
// Read ends stdin like a read error: on that goroutine it would kill the
// process with status 2, which runHook's recover can't stop.
func readStdin(r io.Reader, limit int, wait time.Duration) (data []byte, whole, cut bool) {
	if r == nil {
		return nil, false, false
	}
	parts := make(chan part, 16)
	go func() {
		defer func() {
			if v := recover(); v != nil {
				parts <- part{err: fmt.Errorf("hookpath: reading stdin panicked: %v", v)}
			}
		}()
		var free []byte
		total := 0
		for total <= limit {
			if len(free) < minRead {
				free = make([]byte, readBlock)
			}
			n, err := r.Read(free)
			if n > 0 {
				// Capped, so that appending to it can't write into free.
				parts <- part{b: free[:n:n]}
				free = free[n:]
				total += n
			}
			if err != nil {
				parts <- part{err: err}
				return
			}
		}
	}()

	timer := time.NewTimer(wait)
	defer timer.Stop()
	for {
		select {
		case p := <-parts:
			if p.err != nil {
				// EOF, or a read error, which ends stdin as surely.
				return data, isJSON(data), false
			}
			if data == nil {
				data = p.b
			} else {
				data = append(data, p.b...)
			}
			if len(data) > limit {
				return data[:limit], false, true
			}
			if endsObject(data) && isJSON(data) {
				return data, true, false
			}
		case <-timer.C:
			whole := isJSON(data)
			return data, whole, !whole
		}
	}
}

// endsObject reports whether the last byte of b that isn't whitespace closes
// a JSON object. Only then can b be one whole object, so only then is all
// of it checked, which keeps a payload that comes in many reads from being
// checked over and over.
func endsObject(b []byte) bool {
	b = trimSpace(b)
	return len(b) > 0 && b[len(b)-1] == '}'
}

// isJSON reports whether data is one whole JSON value.
func isJSON(data []byte) bool {
	return len(trimSpace(data)) > 0 && json.Valid(data)
}
