// SPDX-License-Identifier: AGPL-3.0-or-later

package presence

import (
	"sync"

	"github.com/some-du6e/pairtime/pkg/event"
)

// feed hands power events from a source that runs on its own, such as the
// darwin.Loop on the main thread or logind's signals on Linux, to the
// sampler's power watches. The source starts before the sampler and may
// outlive it, so the watches register and unregister with the feed rather
// than start the source. The zero value is ready to use.
type feed struct {
	mu            sync.Mutex
	suspend, lock func(event.PowerOp)
}

// pass gives op to the watch that wants it: suspend and resume to
// WatchSuspend's, lock and unlock to WatchLock's. It returns once that call
// has. The sampler seals the open batch inside Power(suspend), so for a
// suspend the batch is sealed when pass returns (DESIGN 3.3). With no watch
// for op, pass drops it: outside a running sampler there is nothing to
// record or seal.
func (f *feed) pass(op event.PowerOp) {
	f.mu.Lock()
	fn := f.lock
	if op == event.PowerSuspend || op == event.PowerResume {
		fn = f.suspend
	}
	f.mu.Unlock()
	if fn != nil {
		fn(op)
	}
}

// watch returns a WatchSuspend or WatchLock that registers its callback in
// *slot until the returned stop is called.
func (f *feed) watch(slot *func(event.PowerOp)) func(func(event.PowerOp)) (func(), error) {
	return func(fn func(event.PowerOp)) (func(), error) {
		f.mu.Lock()
		*slot = fn
		f.mu.Unlock()
		return func() {
			f.mu.Lock()
			*slot = nil
			f.mu.Unlock()
		}, nil
	}
}

// watches sets o's power watches to the feed's.
func (f *feed) watches(o *OS) {
	o.WatchSuspend, o.WatchLock = f.watch(&f.suspend), f.watch(&f.lock)
}
