// SPDX-License-Identifier: AGPL-3.0-or-later

package darwin

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ebitengine/purego"
)

// The distributed notifications loginwindow posts when the screen locks
// and unlocks.
const (
	screenLockedName   = "com.apple.screenIsLocked"
	screenUnlockedName = "com.apple.screenIsUnlocked"
)

// observerToken identifies pairtime's distributed-notification observer.
// CoreFoundation only compares it, never dereferences it.
const observerToken uintptr = 0x7061697274696d65 // "pairtime"

// runSeconds is how long one CFRunLoopRunInMode may run. The run loop
// returns after each source it handles, so this is never reached in
// practice; it is long so that the run loop sets no timer of its own.
const runSeconds = 1e10

// current is the backend of the running Loop, which the C callbacks look
// up. ErrRunning keeps it to one.
var current atomic.Pointer[macBackend]

// callbacks are the C function pointers into Go. purego can make a fixed
// number per process and never frees one, so each is made once.
var callbacks struct {
	once                                   sync.Once
	power, distributed, activated, perform uintptr
}

func makeCallbacks() {
	callbacks.once.Do(func() {
		callbacks.power = purego.NewCallback(onPower)
		callbacks.distributed = purego.NewCallback(onDistributed)
		callbacks.activated = purego.NewCallback(onActivated)
		callbacks.perform = purego.NewCallback(onPerform)
	})
}

// macBackend is the Loop's side in macOS: one CFRunLoop on the Loop's
// thread with IOKit's power source, a source that wakes it for stop, the
// distributed-notification observer for the screen lock, and an
// NSWorkspace observer.
type macBackend struct {
	l  *Loop
	rl uintptr // the thread's CFRunLoop, set by open

	// wake is a version 0 source that does nothing when it fires. It keeps
	// the run loop from returning kCFRunLoopRunFinished when no other
	// source registered, and stop signals it, because CFRunLoopStop does
	// nothing while the thread is between two runs.
	wake  uintptr
	mu    sync.Mutex
	done  bool
	stopc chan struct{}

	// IOKit sleep and wake (QA1340).
	rootPort    uint32  // io_connect_t from IORegisterForSystemPower
	notifyPort  uintptr // IONotificationPortRef
	notifier    uint32  // io_object_t
	powerSource uintptr

	// Screen lock and unlock. lockNames are the notification names, which
	// tests replace with their own.
	lockNames                [2]string
	center                   uintptr
	lockedName, unlockedName uintptr

	// NSWorkspace activations.
	wsCenter, observer uintptr

	// testAck, when set, gets IOKit answers in place of IOAllowPowerChange,
	// so a test can feed the callback a message macOS never sent.
	testAck func(id uintptr)
	// testDistributed, when set, hears on which thread each distributed
	// notification arrived.
	testDistributed func(onMain bool)
}

func newBackend() (backend, error) {
	if err := loadCF(); err != nil {
		return nil, err
	}
	return &macBackend{
		lockNames: [2]string{screenLockedName, screenUnlockedName},
		stopc:     make(chan struct{}),
	}, nil
}

func (b *macBackend) open(l *Loop) Status {
	makeCallbacks()
	b.l = l
	current.Store(b)
	st := Status{MainThread: cf.pthreadMainNP() == 1}

	b.mu.Lock()
	b.rl = cf.runLoopGetCurrent()
	ctx := [10]uintptr{9: callbacks.perform} // CFRunLoopSourceContext, version 0
	b.wake = cf.runLoopSourceCreate(0, 0, &ctx)
	if b.wake != 0 {
		cf.runLoopAddSource(b.rl, b.wake, cf.defaultMode)
	}
	b.mu.Unlock()

	st.Power = b.openPower()
	st.Lock = b.openLock()
	st.Focus = b.openFocus()
	return st
}

func (b *macBackend) openPower() error {
	if err := loadIOKit(); err != nil {
		return err
	}
	var port uintptr
	var notifier uint32
	root := iokit.registerForSystemPower(0, &port, callbacks.power, &notifier)
	if root == 0 {
		return errors.New("IORegisterForSystemPower failed")
	}
	src := iokit.portGetRunLoopSource(port)
	if src == 0 {
		iokit.deregisterForSystemPower(&notifier)
		iokit.serviceClose(root)
		iokit.portDestroy(port)
		return errors.New("IONotificationPortGetRunLoopSource returned no source")
	}
	cf.runLoopAddSource(b.rl, src, cf.commonMode)
	b.rootPort, b.notifyPort, b.notifier, b.powerSource = root, port, notifier, src
	return nil
}

func (b *macBackend) openLock() error {
	b.center = cf.distributedCenter()
	if b.center == 0 {
		return errors.New("CFNotificationCenterGetDistributedCenter returned no center")
	}
	b.lockedName = cfString(b.lockNames[0])
	b.unlockedName = cfString(b.lockNames[1])
	if b.lockedName == 0 || b.unlockedName == 0 {
		return errors.New("CFStringCreateWithCString failed")
	}
	for _, name := range []uintptr{b.lockedName, b.unlockedName} {
		cf.addObserver(b.center, observerToken, callbacks.distributed, name, 0, cfDeliverImmediately)
	}
	// The first notification that repeats the state at the start isn't
	// news, so the Loop learns the state now.
	if locked, ok := (macPresence{}).session(); ok {
		b.l.seedLock(locked)
	}
	return nil
}

func (b *macBackend) openFocus() error {
	if err := loadObjC(); err != nil {
		return err
	}
	cls, err := observerClass()
	if err != nil {
		return err
	}
	withPool(func() {
		ws := objc.send(objc.clsNSWorkspace, objc.selSharedWorkspace)
		nc := objc.send(ws, objc.selNotificationCenter)
		if nc == 0 {
			err = errors.New("NSWorkspace has no notification center")
			return
		}
		obs := objc.send(objc.send(cls, objc.selAlloc), objc.selInit)
		if obs == 0 {
			err = fmt.Errorf("couldn't make a %s", observerClassName)
			return
		}
		// The shared workspace lives as long as the process, so its
		// notification center needs no retain.
		objc.send4(nc, objc.selAddObserver, obs, objc.selActivated, objc.didActivateName, 0)
		b.wsCenter, b.observer = nc, obs
		if app, ok := appFrom(objc.send(ws, objc.selFrontApp)); ok {
			b.l.activated(time.Now().Round(0), app)
		}
	})
	return err
}

// observerClass registers the Objective-C class whose method gets
// NSWorkspace's activation notifications, once per process.
var observerOnce struct {
	sync.Once
	cls uintptr
	err error
}

func observerClass() (uintptr, error) {
	observerOnce.Do(func() {
		cls := objc.allocateClassPair(objc.clsNSObject, observerClassName, 0)
		if cls == 0 {
			observerOnce.err = fmt.Errorf("objc_allocateClassPair(%s) failed", observerClassName)
			return
		}
		// v@:@ is a method returning void that takes self, _cmd and one
		// object, the NSNotification.
		if !objc.addMethod(cls, objc.selActivated, callbacks.activated, "v@:@") {
			observerOnce.err = fmt.Errorf("class_addMethod(%s) failed", activatedSelector)
			return
		}
		objc.registerClassPair(cls)
		observerOnce.cls = cls
	})
	return observerOnce.cls, observerOnce.err
}

func (b *macBackend) run() {
	for !b.stopping() {
		var r int32
		if objc.err == nil && objc.poolPush != nil {
			// Each source's work runs in its own pool, so the objects AppKit
			// autoreleases while posting a notification are freed after it.
			withPool(func() { r = cf.runLoopRunInMode(cf.defaultMode, runSeconds, true) })
		} else {
			r = cf.runLoopRunInMode(cf.defaultMode, runSeconds, true)
		}
		if r == cfRunLoopRunFinished {
			// Nothing to service, not even the wake source: wait for stop
			// rather than spin.
			<-b.stopc
			return
		}
	}
}

func (b *macBackend) stopping() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.done
}

func (b *macBackend) stop() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.done {
		b.done = true
		close(b.stopc)
	}
	if b.rl == 0 {
		return
	}
	if b.wake != 0 {
		cf.runLoopSourceSignal(b.wake)
	}
	cf.runLoopWakeUp(b.rl)
	cf.runLoopStop(b.rl)
}

// close unregisters in the reverse order of open, as QA1340 does for IOKit.
func (b *macBackend) close() {
	if b.observer != 0 {
		withPool(func() {
			objc.send1(b.wsCenter, objc.selRemoveObserver, b.observer)
			objc.send(b.observer, objc.selRelease)
		})
		b.observer, b.wsCenter = 0, 0
	}
	if b.center != 0 {
		cf.removeEveryObserver(b.center, observerToken)
		b.center = 0
	}
	for _, s := range []*uintptr{&b.lockedName, &b.unlockedName} {
		if *s != 0 {
			cf.release(*s)
			*s = 0
		}
	}
	if b.powerSource != 0 {
		cf.runLoopRemoveSource(b.rl, b.powerSource, cf.commonMode)
		iokit.deregisterForSystemPower(&b.notifier)
		iokit.serviceClose(b.rootPort)
		iokit.portDestroy(b.notifyPort)
		b.powerSource, b.rootPort, b.notifyPort, b.notifier = 0, 0, 0, 0
	}
	b.mu.Lock()
	if b.wake != 0 {
		cf.runLoopRemoveSource(b.rl, b.wake, cf.defaultMode)
		cf.runLoopSourceInvalid(b.wake)
		cf.release(b.wake)
		b.wake = 0
	}
	b.mu.Unlock()
	current.CompareAndSwap(b, nil)
}

func (b *macBackend) allowPowerChange(id uintptr) {
	switch {
	case b.testAck != nil:
		b.testAck(id)
	case b.rootPort != 0:
		iokit.allowPowerChange(b.rootPort, id)
	}
}

// onPower is IOKit's IOServiceInterestCallback for system power messages.
// For kIOMessageSystemWillSleep and kIOMessageCanSystemSleep, arg is the
// notification id that IOAllowPowerChange takes.
func onPower(refcon uintptr, service uint32, msg uint32, arg uintptr) {
	if b := current.Load(); b != nil {
		b.l.powerMessage(time.Now().Round(0), msg, arg)
	}
}

// onDistributed is the CFNotificationCallback for the screen lock
// notifications.
func onDistributed(center, observer, name, object, userInfo uintptr) {
	b := current.Load()
	if b == nil {
		return
	}
	at := time.Now().Round(0)
	if b.testDistributed != nil {
		b.testDistributed(cf.pthreadMainNP() == 1)
	}
	switch {
	case cf.equal(name, b.lockedName):
		b.l.lockChanged(at, true)
	case cf.equal(name, b.unlockedName):
		b.l.lockChanged(at, false)
	}
}

// onActivated is the observer's pairtimeAppActivated: method, which
// NSWorkspace calls with an NSWorkspaceDidActivateApplicationNotification.
func onActivated(self, cmd, note uintptr) {
	b := current.Load()
	if b == nil {
		return
	}
	at := time.Now().Round(0)
	withPool(func() {
		info := objc.send(note, objc.selUserInfo)
		if app, ok := appFrom(objc.send1(info, objc.selObjectForKey, objc.applicationKey)); ok {
			b.l.activated(at, app)
		}
	})
}

// onPerform is the wake source's perform callback. Waking the run loop is
// all the source is for.
func onPerform(info uintptr) {}
