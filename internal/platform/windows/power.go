// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build windows

package windows

import (
	"fmt"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/some-du6e/pairtime/pkg/event"
)

// From winuser.h and powrprof.h.
const (
	deviceNotifyCallback = 2 // DEVICE_NOTIFY_CALLBACK

	pbtAPMSuspend         = 0x4  // PBT_APMSUSPEND
	pbtAPMResumeSuspend   = 0x7  // PBT_APMRESUMESUSPEND
	pbtAPMResumeAutomatic = 0x12 // PBT_APMRESUMEAUTOMATIC
)

// deviceNotifySubscribeParameters is DEVICE_NOTIFY_SUBSCRIBE_PARAMETERS.
type deviceNotifySubscribeParameters struct {
	callback uintptr
	context  uintptr
}

var (
	procPowerRegisterSuspendResumeNotification   = modPowrprof.NewProc("PowerRegisterSuspendResumeNotification")
	procPowerUnregisterSuspendResumeNotification = modPowrprof.NewProc("PowerUnregisterSuspendResumeNotification")
)

// A Go callback is never freed, and a process gets about 2,000 of them,
// so every registration shares one and finds its watch by the context
// value Windows passes back.
var (
	suspendCallbackOnce sync.Once
	suspendCallback     uintptr

	suspendMu      sync.Mutex
	suspendWatches = make(map[uintptr]*suspendWatch)
	suspendLastID  uintptr
)

// suspendWatch is one PowerRegisterSuspendResumeNotification registration.
type suspendWatch struct {
	fn     func(event.PowerOp)
	params *deviceNotifySubscribeParameters // kept here while Windows may read it
	handle uintptr                          // the HPOWERNOTIFY

	mu   sync.Mutex
	last event.PowerOp
}

// WatchSuspend calls fn with event.PowerSuspend before the machine sleeps
// or hibernates and with event.PowerResume once it wakes, until stop is
// called. It uses PowerRegisterSuspendResumeNotification with a callback,
// which needs no window and, unlike WM_POWERBROADCAST, reaches a process
// with no top-level window (CP 2.2). Lid and power-button actions reach it
// as the suspend they cause; a lid set to do nothing changes nothing that
// presence measures.
//
// fn runs on a thread of Windows' own. Windows gives a suspend about two
// seconds before it sleeps, so fn should seal what it has and return.
func WatchSuspend(fn func(event.PowerOp)) (stop func(), err error) {
	for _, p := range []*windows.LazyProc{procPowerRegisterSuspendResumeNotification, procPowerUnregisterSuspendResumeNotification} {
		if err := p.Find(); err != nil {
			return nil, err
		}
	}
	suspendCallbackOnce.Do(func() { suspendCallback = windows.NewCallback(onSuspendResume) })

	w := &suspendWatch{fn: fn}
	suspendMu.Lock()
	suspendLastID++
	id := suspendLastID
	suspendWatches[id] = w
	suspendMu.Unlock()
	forget := func() {
		suspendMu.Lock()
		delete(suspendWatches, id)
		suspendMu.Unlock()
	}

	w.params = &deviceNotifySubscribeParameters{callback: suspendCallback, context: id}
	r, _, _ := procPowerRegisterSuspendResumeNotification.Call(deviceNotifyCallback,
		uintptr(unsafe.Pointer(w.params)), uintptr(unsafe.Pointer(&w.handle)))
	if r != 0 {
		forget()
		return nil, fmt.Errorf("PowerRegisterSuspendResumeNotification: %w", syscall.Errno(r))
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			procPowerUnregisterSuspendResumeNotification.Call(w.handle)
			forget()
		})
	}, nil
}

// onSuspendResume is DEVICE_NOTIFY_CALLBACK_ROUTINE. Type is a ULONG, and
// on amd64 and arm64 the upper half of its register is undefined, so it is
// declared 32 bits wide.
func onSuspendResume(context uintptr, typ uint32, setting uintptr) uintptr {
	if op, ok := suspendOp(typ); ok {
		suspendMu.Lock()
		w := suspendWatches[context]
		suspendMu.Unlock()
		if w != nil {
			w.deliver(op)
		}
	}
	return 0 // ERROR_SUCCESS
}

// suspendOp maps a power broadcast type to an os.power op. A wake by the
// user brings PBT_APMRESUMEAUTOMATIC and then PBT_APMRESUMESUSPEND, an
// automatic wake only the first, so both mean a resume and deliver drops
// the repeat.
func suspendOp(typ uint32) (event.PowerOp, bool) {
	switch typ {
	case pbtAPMSuspend:
		return event.PowerSuspend, true
	case pbtAPMResumeAutomatic, pbtAPMResumeSuspend:
		return event.PowerResume, true
	}
	return "", false
}

// deliver passes op to fn unless it repeats the op before it.
func (w *suspendWatch) deliver(op event.PowerOp) {
	w.mu.Lock()
	repeat := op == w.last
	w.last = op
	w.mu.Unlock()
	if !repeat {
		w.fn(op)
	}
}
