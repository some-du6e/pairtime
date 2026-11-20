// SPDX-License-Identifier: AGPL-3.0-or-later

package darwin

import (
	"errors"
	"fmt"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
)

// Each framework is loaded the first time something needs it, so a
// process that imports this package and never samples presence or runs a
// Loop, such as the hook fast path, pays nothing for it. Since macOS 11 the
// files live in the dyld shared cache, not on disk, and dlopen finds them
// there.
const (
	pathLibSystem      = "/usr/lib/libSystem.B.dylib"
	pathLibObjC        = "/usr/lib/libobjc.A.dylib"
	pathCoreFoundation = "/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation"
	pathCoreGraphics   = "/System/Library/Frameworks/CoreGraphics.framework/CoreGraphics"
	pathIOKit          = "/System/Library/Frameworks/IOKit.framework/IOKit"
	pathAppKit         = "/System/Library/Frameworks/AppKit.framework/AppKit"
)

// Constants from the frameworks' headers.
const (
	cfStringEncodingUTF8 uint32 = 0x08000100 // kCFStringEncodingUTF8
	cfNumberSInt64Type          = 4          // kCFNumberSInt64Type
	// cfDeliverImmediately is CFNotificationSuspensionBehaviorDeliverImmediately.
	cfDeliverImmediately = 4
	// cfRunLoopRunFinished is kCFRunLoopRunFinished: the mode has no
	// sources or timers left.
	cfRunLoopRunFinished int32 = 1
	// cgStateHIDSystem is kCGEventSourceStateHIDSystemState.
	cgStateHIDSystem int32 = 1
)

// lib is one dlopened library. Its methods record the first failure and do
// nothing after it, so a group of bindings is checked once at the end.
type lib struct {
	h   uintptr
	err error
}

func dlopen(path string) *lib {
	h, err := purego.Dlopen(path, purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		return &lib{err: fmt.Errorf("loading %s: %w", path, err)}
	}
	return &lib{h: h}
}

// fn binds the C function name to the Go function that fptr points to.
func (l *lib) fn(fptr any, name string) {
	if sym := l.sym(name); sym != 0 {
		purego.RegisterFunc(fptr, sym)
	}
}

// sym returns the address of name, or 0 after a failure.
func (l *lib) sym(name string) uintptr {
	if l.err != nil {
		return 0
	}
	sym, err := purego.Dlsym(l.h, name)
	if err == nil && sym == 0 {
		err = errors.New("address is 0")
	}
	if err != nil {
		l.err = fmt.Errorf("%s: %w", name, err)
		return 0
	}
	return sym
}

// global returns the pointer-sized value of the C global variable name,
// such as a CFStringRef constant.
func (l *lib) global(name string) uintptr {
	return deref(l.sym(name))
}

// deref returns the pointer-sized value stored at addr, or 0 when addr is
// 0. It reads through a **uintptr made from addr's own address instead of
// converting addr to unsafe.Pointer, which go vet's unsafeptr check rejects;
// purego reads C memory the same way.
func deref(addr uintptr) uintptr {
	if addr == 0 {
		return 0
	}
	return **(**uintptr)(unsafe.Pointer(&addr))
}

// cf holds CoreFoundation and the libSystem call every other group needs.
var cf struct {
	once sync.Once
	err  error

	pthreadMainNP func() int32

	release          func(obj uintptr)
	stringCreate     func(alloc uintptr, s string, encoding uint32) uintptr
	equal            func(a, b uintptr) bool
	getTypeID        func(obj uintptr) uintptr
	booleanGetTypeID func() uintptr
	booleanGetValue  func(b uintptr) bool
	numberGetTypeID  func() uintptr
	numberGetValue   func(n uintptr, typ int, out *int64) bool
	dictGetValue     func(dict, key uintptr) uintptr

	runLoopGetCurrent       func() uintptr
	runLoopRunInMode        func(mode uintptr, seconds float64, returnAfterSourceHandled bool) int32
	runLoopStop             func(rl uintptr)
	runLoopWakeUp           func(rl uintptr)
	runLoopAddSource        func(rl, source, mode uintptr)
	runLoopRemoveSource     func(rl, source, mode uintptr)
	runLoopSourceCreate     func(alloc uintptr, order int, ctx *[10]uintptr) uintptr
	runLoopSourceSignal     func(source uintptr)
	runLoopSourceInvalid    func(source uintptr)
	distributedCenter       func() uintptr
	addObserver             func(center, observer, callback, name, object uintptr, behavior int)
	removeEveryObserver     func(center, observer uintptr)
	postNotification        func(center, name, object, userInfo uintptr, deliverImmediately bool)
	defaultMode, commonMode uintptr // kCFRunLoopDefaultMode, kCFRunLoopCommonModes
}

func loadCF() error {
	cf.once.Do(func() {
		sys := dlopen(pathLibSystem)
		sys.fn(&cf.pthreadMainNP, "pthread_main_np")
		if sys.err != nil {
			cf.err = sys.err
			return
		}
		l := dlopen(pathCoreFoundation)
		l.fn(&cf.release, "CFRelease")
		l.fn(&cf.stringCreate, "CFStringCreateWithCString")
		l.fn(&cf.equal, "CFEqual")
		l.fn(&cf.getTypeID, "CFGetTypeID")
		l.fn(&cf.booleanGetTypeID, "CFBooleanGetTypeID")
		l.fn(&cf.booleanGetValue, "CFBooleanGetValue")
		l.fn(&cf.numberGetTypeID, "CFNumberGetTypeID")
		l.fn(&cf.numberGetValue, "CFNumberGetValue")
		l.fn(&cf.dictGetValue, "CFDictionaryGetValue")
		l.fn(&cf.runLoopGetCurrent, "CFRunLoopGetCurrent")
		l.fn(&cf.runLoopRunInMode, "CFRunLoopRunInMode")
		l.fn(&cf.runLoopStop, "CFRunLoopStop")
		l.fn(&cf.runLoopWakeUp, "CFRunLoopWakeUp")
		l.fn(&cf.runLoopAddSource, "CFRunLoopAddSource")
		l.fn(&cf.runLoopRemoveSource, "CFRunLoopRemoveSource")
		l.fn(&cf.runLoopSourceCreate, "CFRunLoopSourceCreate")
		l.fn(&cf.runLoopSourceSignal, "CFRunLoopSourceSignal")
		l.fn(&cf.runLoopSourceInvalid, "CFRunLoopSourceInvalidate")
		l.fn(&cf.distributedCenter, "CFNotificationCenterGetDistributedCenter")
		l.fn(&cf.addObserver, "CFNotificationCenterAddObserver")
		l.fn(&cf.removeEveryObserver, "CFNotificationCenterRemoveEveryObserver")
		l.fn(&cf.postNotification, "CFNotificationCenterPostNotification")
		cf.defaultMode = l.global("kCFRunLoopDefaultMode")
		cf.commonMode = l.global("kCFRunLoopCommonModes")
		cf.err = l.err
	})
	return cf.err
}

// cfString returns a new CFString holding s, which the caller releases.
func cfString(s string) uintptr {
	return cf.stringCreate(0, s, cfStringEncodingUTF8)
}

// cfInt64 returns the value of a CFNumber.
func cfInt64(v uintptr) (int64, bool) {
	if v == 0 || cf.getTypeID(v) != cf.numberGetTypeID() {
		return 0, false
	}
	var n int64
	if !cf.numberGetValue(v, cfNumberSInt64Type, &n) {
		return 0, false
	}
	return n, true
}

// cfTrue reports whether v is a CFBoolean true or a non-zero CFNumber.
func cfTrue(v uintptr) bool {
	if v == 0 {
		return false
	}
	if cf.getTypeID(v) == cf.booleanGetTypeID() {
		return cf.booleanGetValue(v)
	}
	n, ok := cfInt64(v)
	return ok && n != 0
}

// cg holds the CoreGraphics calls for idle time and the session.
var cg struct {
	once sync.Once
	err  error

	secondsSinceLastEventType func(state int32, eventType uint32) float64
	sessionCopyCurrent        func() uintptr
	// preflightListen is CGPreflightListenEventAccess (macOS 10.15), which
	// reports the Input Monitoring permission without asking for it. It is
	// nil if the symbol is missing.
	preflightListen func() bool

	keyScreenIsLocked, keyOnConsole uintptr // CFStrings, kept for the process's life
}

func loadCG() error {
	cg.once.Do(func() {
		if err := loadCF(); err != nil {
			cg.err = err
			return
		}
		l := dlopen(pathCoreGraphics)
		l.fn(&cg.secondsSinceLastEventType, "CGEventSourceSecondsSinceLastEventType")
		l.fn(&cg.sessionCopyCurrent, "CGSessionCopyCurrentDictionary")
		if l.err != nil {
			cg.err = l.err
			return
		}
		optional := &lib{h: l.h}
		optional.fn(&cg.preflightListen, "CGPreflightListenEventAccess")
		// The lock key is undocumented and long used; kCGSessionOnConsoleKey
		// is a CFSTR macro in CGSession.h with this value.
		cg.keyScreenIsLocked = cfString("CGSSessionScreenIsLocked")
		cg.keyOnConsole = cfString("kCGSSessionOnConsoleKey")
	})
	return cg.err
}

// iokit holds the IOKit calls for sleep and wake and for HIDIdleTime.
var iokit struct {
	once sync.Once
	err  error

	registerForSystemPower   func(refcon uintptr, port *uintptr, callback uintptr, notifier *uint32) uint32
	deregisterForSystemPower func(notifier *uint32) int32
	allowPowerChange         func(kernelPort uint32, notificationID uintptr) int32
	serviceClose             func(connect uint32) int32
	portGetRunLoopSource     func(port uintptr) uintptr
	portDestroy              func(port uintptr)
	serviceMatching          func(name string) uintptr
	getMatchingService       func(mainPort uint32, matching uintptr) uint32
	createCFProperty         func(entry uint32, key, alloc uintptr, options uint32) uintptr
	objectRelease            func(obj uint32) int32

	keyHIDIdleTime uintptr // a CFString, kept for the process's life
}

func loadIOKit() error {
	iokit.once.Do(func() {
		if err := loadCF(); err != nil {
			iokit.err = err
			return
		}
		l := dlopen(pathIOKit)
		l.fn(&iokit.registerForSystemPower, "IORegisterForSystemPower")
		l.fn(&iokit.deregisterForSystemPower, "IODeregisterForSystemPower")
		l.fn(&iokit.allowPowerChange, "IOAllowPowerChange")
		l.fn(&iokit.serviceClose, "IOServiceClose")
		l.fn(&iokit.portGetRunLoopSource, "IONotificationPortGetRunLoopSource")
		l.fn(&iokit.portDestroy, "IONotificationPortDestroy")
		l.fn(&iokit.serviceMatching, "IOServiceMatching")
		l.fn(&iokit.getMatchingService, "IOServiceGetMatchingService")
		l.fn(&iokit.createCFProperty, "IORegistryEntryCreateCFProperty")
		l.fn(&iokit.objectRelease, "IOObjectRelease")
		iokit.err = l.err
		if iokit.err == nil {
			iokit.keyHIDIdleTime = cfString("HIDIdleTime")
		}
	})
	return iokit.err
}

// objc holds the Objective-C runtime and the AppKit names that NSWorkspace
// activations need.
var objc struct {
	once sync.Once
	err  error

	getClass          func(name string) uintptr
	registerName      func(name string) uintptr
	allocateClassPair func(super uintptr, name string, extra uintptr) uintptr
	registerClassPair func(cls uintptr)
	addMethod         func(cls, sel, imp uintptr, types string) bool
	poolPush          func() uintptr
	poolPop           func(pool uintptr)

	// objc_msgSend, bound once per signature it is used with.
	send       func(obj, sel uintptr) uintptr
	send1      func(obj, sel, a uintptr) uintptr
	send4      func(obj, sel, a, b, c, d uintptr) uintptr
	sendInt32  func(obj, sel uintptr) int32
	sendBool1  func(obj, sel, a uintptr) bool
	sendString func(obj, sel uintptr) string

	// AppKit's NSString constants.
	didActivateName, applicationKey uintptr

	clsNSObject, clsNSWorkspace, clsNSRunningApplication uintptr

	selAlloc, selInit, selRelease                          uintptr
	selSharedWorkspace, selNotificationCenter, selFrontApp uintptr
	selAddObserver, selRemoveObserver, selActivated        uintptr
	selUserInfo, selObjectForKey, selIsKindOfClass         uintptr
	selProcessIdentifier, selBundleIdentifier              uintptr
	selExecutableURL, selPath, selUTF8String               uintptr
}

// observerClassName is the Objective-C class whose instance observes
// NSWorkspace for a Loop, and activatedSelector its one method.
const (
	observerClassName = "PairtimeWorkspaceObserver"
	activatedSelector = "pairtimeAppActivated:"
)

func loadObjC() error {
	objc.once.Do(func() { objc.err = bindObjC() })
	return objc.err
}

func bindObjC() error {
	if err := loadCF(); err != nil {
		return err
	}
	l := dlopen(pathLibObjC)
	l.fn(&objc.getClass, "objc_getClass")
	l.fn(&objc.registerName, "sel_registerName")
	l.fn(&objc.allocateClassPair, "objc_allocateClassPair")
	l.fn(&objc.registerClassPair, "objc_registerClassPair")
	l.fn(&objc.addMethod, "class_addMethod")
	l.fn(&objc.poolPush, "objc_autoreleasePoolPush")
	l.fn(&objc.poolPop, "objc_autoreleasePoolPop")
	msgSend := l.sym("objc_msgSend")
	if l.err != nil {
		return l.err
	}
	purego.RegisterFunc(&objc.send, msgSend)
	purego.RegisterFunc(&objc.send1, msgSend)
	purego.RegisterFunc(&objc.send4, msgSend)
	purego.RegisterFunc(&objc.sendInt32, msgSend)
	purego.RegisterFunc(&objc.sendBool1, msgSend)
	purego.RegisterFunc(&objc.sendString, msgSend)

	// Loading AppKit registers NSWorkspace and NSRunningApplication with
	// the runtime. It doesn't make the agent an app: no Dock icon, no menu.
	appKit := dlopen(pathAppKit)
	objc.didActivateName = appKit.global("NSWorkspaceDidActivateApplicationNotification")
	objc.applicationKey = appKit.global("NSWorkspaceApplicationKey")
	if appKit.err != nil {
		return appKit.err
	}
	if objc.didActivateName == 0 || objc.applicationKey == 0 {
		return errors.New("AppKit's NSWorkspace notification constants are nil")
	}
	for _, c := range []struct {
		dst  *uintptr
		name string
	}{
		{&objc.clsNSObject, "NSObject"},
		{&objc.clsNSWorkspace, "NSWorkspace"},
		{&objc.clsNSRunningApplication, "NSRunningApplication"},
	} {
		if *c.dst = objc.getClass(c.name); *c.dst == 0 {
			return fmt.Errorf("the Objective-C runtime has no class %s", c.name)
		}
	}
	for _, s := range []struct {
		dst  *uintptr
		name string
	}{
		{&objc.selAlloc, "alloc"},
		{&objc.selInit, "init"},
		{&objc.selRelease, "release"},
		{&objc.selSharedWorkspace, "sharedWorkspace"},
		{&objc.selNotificationCenter, "notificationCenter"},
		{&objc.selFrontApp, "frontmostApplication"},
		{&objc.selAddObserver, "addObserver:selector:name:object:"},
		{&objc.selRemoveObserver, "removeObserver:"},
		{&objc.selActivated, activatedSelector},
		{&objc.selUserInfo, "userInfo"},
		{&objc.selObjectForKey, "objectForKey:"},
		{&objc.selIsKindOfClass, "isKindOfClass:"},
		{&objc.selProcessIdentifier, "processIdentifier"},
		{&objc.selBundleIdentifier, "bundleIdentifier"},
		{&objc.selExecutableURL, "executableURL"},
		{&objc.selPath, "path"},
		{&objc.selUTF8String, "UTF8String"},
	} {
		*s.dst = objc.registerName(s.name)
	}
	return nil
}

// withPool runs f inside an autorelease pool, so the objects AppKit hands
// back autoreleased are freed when f returns. Threads that don't run an
// AppKit event loop have no pool of their own.
func withPool(f func()) {
	pool := objc.poolPush()
	defer objc.poolPop(pool)
	f()
}

// nsString copies an NSString into Go, or returns "" for nil.
func nsString(s uintptr) string {
	if s == 0 {
		return ""
	}
	return objc.sendString(s, objc.selUTF8String)
}

// appFrom reads an NSRunningApplication. Messages to nil return nil in
// Objective-C, so a missing executable URL gives "".
func appFrom(ra uintptr) (App, bool) {
	if ra == 0 || !objc.sendBool1(ra, objc.selIsKindOfClass, objc.clsNSRunningApplication) {
		return App{}, false
	}
	return App{
		PID:        int(objc.sendInt32(ra, objc.selProcessIdentifier)),
		BundleID:   nsString(objc.send(ra, objc.selBundleIdentifier)),
		Executable: nsString(objc.send(objc.send(ra, objc.selExecutableURL), objc.selPath)),
	}, true
}
