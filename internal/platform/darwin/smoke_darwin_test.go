// SPDX-License-Identifier: AGPL-3.0-or-later

package darwin

import (
	"context"
	"fmt"
	"os"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/ebitengine/purego"

	"github.com/some-du6e/pairtime/pkg/event"
)

// These tests run on macos-latest in platform.yml. They read the OS's own
// values and drive the real callbacks with synthetic notifications; none
// moves focus, sleeps the runner or locks its screen (JE must-fix 11).

// wait is how long a test waits for an event it caused. Hosted macOS
// runners are slow, not that slow.
const wait = 10 * time.Second

func TestPresenceOnMac(t *testing.T) {
	tests := []struct {
		name  string
		check func(t *testing.T)
	}{
		{
			name: "Presence returns an any-input idle time that passes pkg/event's rules",
			check: func(t *testing.T) {
				p, err := Presence()
				if err != nil {
					t.Fatal(err)
				}
				if err := p.Validate(); err != nil {
					t.Fatalf("sample %+v breaks pkg/event's rules: %v", p, err)
				}
			},
		},
		{
			name: "CoreGraphics loads and answers for both event types",
			check: func(t *testing.T) {
				for _, typ := range []uint32{cgAnyInput, cgKeyDown} {
					if _, ok := (macPresence{}).cgIdle(typ); !ok {
						t.Errorf("cgIdle(%#x): CoreGraphics didn't load: %v", typ, cg.err)
					}
				}
			},
		},
		{
			name: "IOKit's HIDIdleTime, the fallback, gives a time that isn't negative",
			check: func(t *testing.T) {
				ns, ok := (macPresence{}).hidIdle()
				if !ok || ns < 0 {
					t.Errorf("hidIdle() = %d, %v; want a time >= 0", ns, ok)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, tt.check)
	}
}

// macRecorder is a Handler that keeps every call in order.
type macRecorder struct {
	mu    sync.Mutex
	log   []string
	power chan event.PowerOp
	focus chan App
}

func newMacRecorder() *macRecorder {
	return &macRecorder{power: make(chan event.PowerOp, 64), focus: make(chan App, 64)}
}

func (r *macRecorder) note(s string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.log = append(r.log, s)
}

func (r *macRecorder) lines() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.log)
}

func (r *macRecorder) Power(at time.Time, op event.PowerOp) {
	r.note("power " + string(op))
	r.power <- op
}

func (r *macRecorder) Focus(at time.Time, app App) {
	r.focus <- app
}

// waitPower waits for op, skipping none: a power event the test didn't
// cause would be a failure.
func (r *macRecorder) waitPower(t *testing.T, op event.PowerOp) {
	t.Helper()
	select {
	case got := <-r.power:
		if got != op {
			t.Fatalf("Power(%s), want Power(%s)", got, op)
		}
	case <-time.After(wait):
		t.Fatalf("no Power(%s) within %v", op, wait)
	}
}

// waitFocus waits for an activation of pid, skipping the frontmost app the
// Loop reports at the start and anything else the runner activates.
func (r *macRecorder) waitFocus(t *testing.T, pid int) App {
	t.Helper()
	deadline := time.After(wait)
	for {
		select {
		case app := <-r.focus:
			if app.PID == pid {
				return app
			}
		case <-deadline:
			t.Fatalf("no Focus for pid %d within %v", pid, wait)
		}
	}
}

// startMacLoop runs a Loop with the real backend, on the main thread or on
// a goroutine of its own, until the test ends. tweak sees the backend
// before Run opens it.
func startMacLoop(t *testing.T, mainThread bool, tweak func(b *macBackend)) (*Loop, *macBackend, *macRecorder) {
	t.Helper()
	rec := newMacRecorder()
	l := NewLoop(rec)
	var mb *macBackend
	l.newBackend = func() (backend, error) {
		b, err := newBackend()
		if err != nil {
			return nil, err
		}
		mb = b.(*macBackend)
		if tweak != nil {
			tweak(mb)
		}
		return mb, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	errc := make(chan error, 1)
	run := func() { errc <- l.Run(ctx) }
	if mainThread {
		go onMain(run)
	} else {
		go run()
	}
	<-l.Ready()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-errc:
			if err != nil {
				t.Errorf("Run: %v", err)
			}
		case <-time.After(wait):
			t.Errorf("Run didn't return within %v of its context ending", wait)
		}
	})
	if mb == nil {
		t.Fatalf("Run returned before opening the backend: %v", l.Status())
	}
	return l, mb, rec
}

// testObjC has the messages only the tests send: making a synthetic
// activation notification, handing it over, and naming a class.
var testObjC struct {
	once      sync.Once
	err       error
	send2     func(obj, sel, a, b uintptr) uintptr
	send3     func(obj, sel, a, b, c uintptr) uintptr
	className func(obj uintptr) string // object_getClassName

	clsNSDictionary, clsNSNotification   uintptr
	selCurrentApp, selDictWithObjForKey  uintptr
	selPostNotification, selNoteWithName uintptr
}

func loadTestObjC() error {
	testObjC.once.Do(func() {
		if testObjC.err = loadObjC(); testObjC.err != nil {
			return
		}
		l := dlopen(pathLibObjC)
		msgSend := l.sym("objc_msgSend")
		l.fn(&testObjC.className, "object_getClassName")
		if testObjC.err = l.err; testObjC.err != nil {
			return
		}
		purego.RegisterFunc(&testObjC.send2, msgSend)
		purego.RegisterFunc(&testObjC.send3, msgSend)
		testObjC.clsNSDictionary = objc.getClass("NSDictionary")
		testObjC.clsNSNotification = objc.getClass("NSNotification")
		testObjC.selCurrentApp = objc.registerName("currentApplication")
		testObjC.selDictWithObjForKey = objc.registerName("dictionaryWithObject:forKey:")
		testObjC.selPostNotification = objc.registerName("postNotificationName:object:userInfo:")
		testObjC.selNoteWithName = objc.registerName("notificationWithName:object:userInfo:")
	})
	return testObjC.err
}

// activationInfo returns the userInfo NSWorkspace would attach to an
// activation of this process, autoreleased.
func activationInfo() uintptr {
	app := objc.send(objc.clsNSRunningApplication, testObjC.selCurrentApp)
	return testObjC.send2(testObjC.clsNSDictionary, testObjC.selDictWithObjForKey, app, objc.applicationKey)
}

// deliverActivation hands the observer's method the
// NSWorkspaceDidActivateApplicationNotification NSWorkspace would send if
// this process came to the front, the way NSWorkspace's notification center
// calls it. No app's focus changes.
func deliverActivation(t *testing.T, b *macBackend) {
	t.Helper()
	if err := loadTestObjC(); err != nil {
		t.Fatal(err)
	}
	if b.observer == 0 {
		t.Fatalf("the Loop has no NSWorkspace observer: %v", b.l.Status().Focus)
	}
	withPool(func() {
		ws := objc.send(objc.clsNSWorkspace, objc.selSharedWorkspace)
		note := testObjC.send3(testObjC.clsNSNotification, testObjC.selNoteWithName, objc.didActivateName, ws, activationInfo())
		objc.send1(b.observer, objc.selActivated, note)
	})
}

// postActivation posts that same notification to NSWorkspace's
// notification center, for TestFindingRunLoop.
func postActivation(t *testing.T) {
	t.Helper()
	if err := loadTestObjC(); err != nil {
		t.Fatal(err)
	}
	withPool(func() {
		ws := objc.send(objc.clsNSWorkspace, objc.selSharedWorkspace)
		nc := objc.send(ws, objc.selNotificationCenter)
		testObjC.send3(nc, testObjC.selPostNotification, objc.didActivateName, ws, activationInfo())
	})
}

// postDistributed posts a distributed notification, as loginwindow posts
// com.apple.screenIsLocked, under a name only this test observes.
func postDistributed(name string) {
	s := cfString(name)
	defer cf.release(s)
	cf.postNotification(cf.distributedCenter(), s, 0, 0, true)
}

// testLockNames are lock notification names private to this process, so
// posting them locks nothing and no other process hears them.
func testLockNames() [2]string {
	p := fmt.Sprintf("dev.pairtime.test.%d.", os.Getpid())
	return [2]string{p + "screenIsLocked", p + "screenIsUnlocked"}
}

func TestLoopOnMac(t *testing.T) {
	tests := []struct {
		name       string
		mainThread bool
		run        func(t *testing.T, l *Loop, b *macBackend, rec *macRecorder)
	}{
		{
			name:       "on the main thread every source registers",
			mainThread: true,
			run: func(t *testing.T, l *Loop, b *macBackend, rec *macRecorder) {
				st := l.Status()
				if !st.MainThread || st.Power != nil || st.Lock != nil || st.Focus != nil {
					t.Errorf("Status() = %+v, want the main thread and no errors", st)
				}
			},
		},
		{
			name:       "the observer's method turns a synthetic activation of this process into its App",
			mainThread: true,
			run: func(t *testing.T, l *Loop, b *macBackend, rec *macRecorder) {
				deliverActivation(t, b)
				app := rec.waitFocus(t, os.Getpid())
				t.Logf("activation: pid %d, bundle id %q, executable %q", app.PID, app.BundleID, app.Executable)
			},
		},
		{
			name:       "the IOKit callback, called through the C ABI, reports a sleep before answering it and then a wake",
			mainThread: true,
			run: func(t *testing.T, l *Loop, b *macBackend, rec *macRecorder) {
				var callPower func(refcon uintptr, service uint32, msg uint32, arg uintptr)
				purego.RegisterFunc(&callPower, callbacks.power)
				callPower(0, 0, msgSystemWillSleep, 42)
				rec.waitPower(t, event.PowerSuspend)
				callPower(0, 0, msgCanSystemSleep, 43)
				callPower(0, 0, msgSystemHasPoweredOn, 0)
				rec.waitPower(t, event.PowerResume)
				want := []string{"power suspend", "ack 42", "ack 43", "power resume"}
				if got := rec.lines(); !slices.Equal(got, want) {
					t.Errorf("calls and answers = %q, want %q", got, want)
				}
			},
		},
		{
			name:       "on the main thread distributed lock and unlock notifications reach the handler",
			mainThread: true,
			run: func(t *testing.T, l *Loop, b *macBackend, rec *macRecorder) {
				// The runner's real lock state seeded the filter; start from
				// unlocked so both posts are news.
				l.seedLock(false)
				names := testLockNames()
				postDistributed(names[0])
				rec.waitPower(t, event.PowerLock)
				postDistributed(names[1])
				rec.waitPower(t, event.PowerUnlock)
			},
		},
		{
			name:       "off the main thread IOKit still registers, Status says where the Loop runs, and the observer still turns an activation into an App",
			mainThread: false,
			run: func(t *testing.T, l *Loop, b *macBackend, rec *macRecorder) {
				st := l.Status()
				if st.MainThread || st.Power != nil {
					t.Errorf("Status() = %+v, want off the main thread with IOKit registered", st)
				}
				deliverActivation(t, b)
				rec.waitFocus(t, os.Getpid())
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var rec *macRecorder
			l, b, rec := startMacLoop(t, tt.mainThread, func(b *macBackend) {
				b.lockNames = testLockNames()
				b.testAck = func(id uintptr) {
					if testPowerIDs[id] {
						rec.note(fmt.Sprintf("ack %d", id))
						return
					}
					// A real idle-sleep query during the test still gets its
					// answer from IOKit.
					iokit.allowPowerChange(b.rootPort, id)
				}
			})
			tt.run(t, l, b, rec)
		})
	}
}

// testPowerIDs are the notification ids the tests' synthetic IOKit
// messages carry.
var testPowerIDs = map[uintptr]bool{42: true, 43: true}
