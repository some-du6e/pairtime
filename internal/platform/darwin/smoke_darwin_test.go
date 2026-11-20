// SPDX-License-Identifier: AGPL-3.0-or-later

package darwin

import (
	"context"
	"fmt"
	"math"
	"os"
	"slices"
	"strings"
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

// hidLagS is how far, in seconds, IOKit's HIDIdleTime may stray from
// CoreGraphics' any-input idle time. On an idle runner the two agree to a
// few milliseconds, but HIDIdleTime can miss a posted event. In platform.yml
// run 44656311435, the second key TestFindingKeyDownIdle posted, 1.5 s
// after its first, reset CoreGraphics' time and left HIDIdleTime counting
// from the first key (own_process_run.reader.after in
// macos-key-down-idle.json). That test posts its two keys about quietS
// apart, so twice quietS allows for the miss and still fails a time that is
// finite but wrong by more.
const hidLagS = 2 * quietS

func TestPresenceOnMac(t *testing.T) {
	tests := []struct {
		name  string
		check func(t *testing.T)
	}{
		{
			name: "Presence returns any-input and key-down idle times that pass pkg/event's rules",
			check: func(t *testing.T) {
				p, err := Presence()
				if err != nil {
					t.Fatal(err)
				}
				if err := p.Validate(); err != nil {
					t.Fatalf("sample %+v breaks pkg/event's rules: %v", p, err)
				}
				// CoreGraphics answers on macos-latest, so a sample without
				// idle_keys_s fell back to HIDIdleTime or got an invalid
				// key-down time.
				if p.IdleKeysS == nil {
					t.Errorf("sample %+v has a null idle_keys_s; CoreGraphics should give both times", p)
				}
			},
		},
		{
			name: "CoreGraphics gives a finite idle time that isn't negative for both event types",
			check: func(t *testing.T) {
				for _, typ := range []uint32{cgAnyInput, cgKeyDown} {
					v, ok := (macPresence{}).cgIdle(typ)
					switch {
					case !ok:
						t.Errorf("cgIdle(%#x): CoreGraphics didn't load: %v", typ, cg.err)
					case !validIdle(v):
						t.Errorf("cgIdle(%#x) = %v; want a finite time >= 0", typ, v)
					}
				}
			},
		},
		{
			name: "CoreGraphics' any-input idle time agrees with IOKit's HIDIdleTime, allowing for HIDIdleTime's lag",
			check: func(t *testing.T) {
				cgS, ok := (macPresence{}).cgIdle(cgAnyInput)
				if !ok || !validIdle(cgS) {
					t.Fatalf("cgIdle(any input) = %v, %v; want a valid idle time", cgS, ok)
				}
				ns, ok := (macPresence{}).hidIdle()
				if !ok {
					t.Fatal("hidIdle() gave no time")
				}
				if hidS := float64(ns) / 1e9; math.Abs(cgS-hidS) > hidLagS {
					t.Errorf("CoreGraphics says %.3f s since any input and HIDIdleTime %.3f s; want them within %v s", cgS, hidS, hidLagS)
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
	var others []App
	for {
		select {
		case app := <-r.focus:
			if app.PID == pid {
				return app
			}
			others = append(others, app)
		case <-deadline:
			t.Fatalf("no Focus for pid %d within %v; Focus came for %+v", pid, wait, others)
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
	selRunningApps, selCount             uintptr
	selObjectAtIndex                     uintptr
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
		testObjC.selRunningApps = objc.registerName("runningApplications")
		testObjC.selCount = objc.registerName("count")
		testObjC.selObjectAtIndex = objc.registerName("objectAtIndex:")
	})
	return testObjC.err
}

// activationInfo returns the userInfo NSWorkspace attaches to an
// activation of the NSRunningApplication ra, autoreleased.
func activationInfo(ra uintptr) uintptr {
	return testObjC.send2(testObjC.clsNSDictionary, testObjC.selDictWithObjForKey, ra, objc.applicationKey)
}

// activationTarget picks the app a synthetic activation names, inside a
// pool: this process's NSRunningApplication, or else the first running app,
// whose pid appFrom reads as valid and which isn't the app in front, since
// the Loop has reported that one already and drops a repeat. It fails the
// test, listing what it saw, when appFrom reads no app.
func activationTarget(t *testing.T) (uintptr, App) {
	t.Helper()
	ws := objc.send(objc.clsNSWorkspace, objc.selSharedWorkspace)
	front, _ := appFrom(objc.send(ws, objc.selFrontApp))
	candidates := []uintptr{objc.send(objc.clsNSRunningApplication, testObjC.selCurrentApp)}
	apps := objc.send(ws, testObjC.selRunningApps)
	for i, n := uintptr(0), objc.send(apps, testObjC.selCount); i < n && i < 50; i++ {
		candidates = append(candidates, objc.send1(apps, testObjC.selObjectAtIndex, i))
	}
	var seen []string
	for _, ra := range candidates {
		app, ok := appFrom(ra)
		seen = append(seen, fmt.Sprintf("%#x (%s) kind of NSRunningApplication %v: %+v, %v",
			ra, testObjC.className(ra), objc.sendBool1(ra, objc.selIsKindOfClass, objc.clsNSRunningApplication), app, ok))
		if ok && app.PID > 0 && app.PID != front.PID {
			if ra != candidates[0] {
				t.Logf("this process's NSRunningApplication won't do (%s), so the activation names pid %d", seen[0], app.PID)
			}
			return ra, app
		}
	}
	t.Fatalf("appFrom read no usable app; front is %+v; this process is pid %d; saw:\n%s", front, os.Getpid(), strings.Join(seen, "\n"))
	return 0, App{}
}

// deliverActivation hands the observer's method the
// NSWorkspaceDidActivateApplicationNotification NSWorkspace would send if
// the app activationTarget picks came to the front, the way NSWorkspace's
// notification center calls it, and returns that app. No app's focus
// changes.
func deliverActivation(t *testing.T, b *macBackend) App {
	t.Helper()
	if err := loadTestObjC(); err != nil {
		t.Fatal(err)
	}
	if b.observer == 0 {
		t.Fatalf("the Loop has no NSWorkspace observer: %v", b.l.Status().Focus)
	}
	var want App
	withPool(func() {
		ws := objc.send(objc.clsNSWorkspace, objc.selSharedWorkspace)
		ra, app := activationTarget(t)
		want = app
		note := testObjC.send3(testObjC.clsNSNotification, testObjC.selNoteWithName, objc.didActivateName, ws, activationInfo(ra))
		before := b.activations.Load()
		objc.send1(b.observer, objc.selActivated, note)
		if ran := b.activations.Load() - before; ran != 1 {
			t.Errorf("the observer's method ran %d times for one notification, want 1", ran)
		}
	})
	return want
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
		ra, _ := activationTarget(t)
		testObjC.send3(nc, testObjC.selPostNotification, objc.didActivateName, ws, activationInfo(ra))
	})
}

// postDistributed posts a distributed notification, as loginwindow posts
// com.apple.screenIsLocked, under a name only this test observes.
func postDistributed(name string) {
	s := cfString(name)
	defer cf.release(s)
	cf.postNotification(cf.distributedCenter(), s, 0, 0, true)
}

// testLockNames are lock notification names private to this process and
// to one use, so posting them locks nothing, no other process hears them,
// and one test's notification can't reach another test's Loop late.
func testLockNames(use string) [2]string {
	p := fmt.Sprintf("dev.pairtime.test.%d.%s.", os.Getpid(), use)
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
				want := deliverActivation(t, b)
				if got := rec.waitFocus(t, want.PID); got != want {
					t.Errorf("Focus(%+v), want Focus(%+v)", got, want)
				}
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
				names := testLockNames("loop")
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
				want := deliverActivation(t, b)
				rec.waitFocus(t, want.PID)
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var rec *macRecorder
			l, b, rec := startMacLoop(t, tt.mainThread, func(b *macBackend) {
				b.lockNames = testLockNames("loop")
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
