// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build windows

package windows

import (
	"errors"
	"fmt"
	"runtime"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// From winuser.h.
const (
	eventSystemForeground    = 0x0003 // EVENT_SYSTEM_FOREGROUND
	eventSystemDesktopSwitch = 0x0020 // EVENT_SYSTEM_DESKTOPSWITCH

	wineventOutOfContext   = 0x0000 // WINEVENT_OUTOFCONTEXT
	wineventSkipOwnProcess = 0x0002 // WINEVENT_SKIPOWNPROCESS

	pmNoRemove = 0x0000 // PM_NOREMOVE

	// wmStopForeground is WM_APP + 3, which stop posts to the watch's
	// thread to end its message loop. The thread has no window, so the
	// message can only come from stop.
	wmStopForeground = 0x8000 + 3
)

var (
	procSetWinEventHook    = modUser32.NewProc("SetWinEventHook")
	procUnhookWinEvent     = modUser32.NewProc("UnhookWinEvent")
	procPeekMessageW       = modUser32.NewProc("PeekMessageW")
	procPostThreadMessageW = modUser32.NewProc("PostThreadMessageW")
)

// Why the foreground can be unknown. Foreground.Unknown holds one of these.
const (
	// UnknownOtherDesktop: no window of this desktop is in front, because
	// another desktop has the input: the secure desktop of a UAC prompt or
	// of Ctrl+Alt+Del, or the lock screen. Windows tells a process nothing
	// about the windows of a desktop it isn't on.
	UnknownOtherDesktop = "no window of this desktop is in front: another desktop has the input, such as the secure desktop of a UAC prompt or Ctrl+Alt+Del, or the lock screen"
	// UnknownNoWindow: Windows names no foreground window, which it does
	// for a moment while one window loses activation and the next hasn't
	// gained it.
	UnknownNoWindow = "Windows names no foreground window"
	// UnknownNoProcess: the window in front closed before its process
	// could be read.
	UnknownNoProcess = "the window in front closed before pairtime could read its process"
)

// Foreground is a change of the window in front.
type Foreground struct {
	// At is when the change reached the watch, from time.Now, with its
	// monotonic reading. Windows delivers a change within milliseconds of
	// the switch.
	At time.Time
	// Window is the foreground window's handle, or 0 when Unknown is set.
	// It names the window only while the window exists, and never leaves
	// the device.
	Window uintptr
	// PID is the process GetWindowThreadProcessId names for the window:
	// for a classic console window, the console's first client, such as
	// cmd.exe or claude.exe, rather than conhost.exe (CP 3.3). It is 0
	// when Unknown is set.
	PID int
	// Unknown says why pairtime can't tell which window is in front, one
	// of the Unknown constants; it is empty when Window and PID are set.
	Unknown string
	// Start is set on the report WatchForeground makes as it starts, of
	// the window in front then.
	Start bool
}

// WatchForeground calls fn with the window in front as WatchForeground
// starts, and again each time another window comes to the front, until
// stop is called. It uses SetWinEventHook(EVENT_SYSTEM_FOREGROUND) out of
// context, on a locked OS thread of its own that sleeps in GetMessage
// between changes, so it costs nothing while the foreground stays put
// (CP 3.3). EVENT_SYSTEM_DESKTOPSWITCH tells it when another desktop, such
// as the secure desktop of a UAC prompt, takes the input and gives it back.
//
// It reads which window is in front and the window's process, nothing
// more: never the window's title, class or contents (DESIGN 3.3). It moves
// no focus and makes no window.
//
// fn runs on the watch's thread, one call at a time, and must return
// quickly: changes queue up behind it.
func WatchForeground(fn func(Foreground)) (stop func(), err error) {
	w, err := startForeground(fn)
	if err != nil {
		return nil, err
	}
	var once sync.Once
	return func() { once.Do(w.stop) }, nil
}

// A Go callback is never freed, and Go promises only so many per process,
// so every watch shares one, which finds its watch by the hook handle
// Windows passes back.
var (
	foregroundOnce     sync.Once
	foregroundErr      error
	foregroundCallback uintptr

	foregroundMu      sync.Mutex
	foregroundWatches = make(map[uintptr]*foregroundWatch) // by hook handle
)

// foregroundWatch is one watch: its thread, its two hooks and the last
// window it reported, which only its thread touches.
type foregroundWatch struct {
	fn    func(Foreground)
	tid   uint32
	hooks []uintptr
	last  Foreground
	done  chan struct{} // closed when the thread has unhooked and is ending
}

func loadForeground() error {
	foregroundOnce.Do(func() {
		for _, p := range []*windows.LazyProc{
			procSetWinEventHook, procUnhookWinEvent, procPeekMessageW, procPostThreadMessageW,
			procGetMessageW, procDispatchMessageW,
		} {
			if foregroundErr = p.Find(); foregroundErr != nil {
				return
			}
		}
		foregroundCallback = windows.NewCallback(onWinEvent)
	})
	return foregroundErr
}

func startForeground(fn func(Foreground)) (*foregroundWatch, error) {
	if err := loadForeground(); err != nil {
		return nil, err
	}
	w := &foregroundWatch{fn: fn, done: make(chan struct{})}
	ready := make(chan error, 1)
	go w.run(ready)
	if err := <-ready; err != nil {
		<-w.done
		return nil, err
	}
	return w, nil
}

// run owns the hooks from SetWinEventHook to UnhookWinEvent. Out-of-context
// events reach the thread that set the hook, through its message queue, so
// run keeps its OS thread and never unlocks it: Go ends the thread with the
// goroutine instead of reusing one that had a message queue. The thread
// makes no window, so it needs no window class, and Windows deleting a
// process's classes when its last GUI thread ends can't touch it.
func (w *foregroundWatch) run(ready chan<- error) {
	runtime.LockOSThread()
	defer close(w.done)

	// PeekMessage makes the thread's message queue now, so that stop's
	// PostThreadMessage finds one even if it comes before the loop starts.
	var m winMsg
	procPeekMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0, pmNoRemove)
	w.tid = windows.GetCurrentThreadId()

	// Events wait in the queue until GetMessage, so none can reach
	// onWinEvent before the watch is in foregroundWatches.
	foregroundMu.Lock()
	for _, ev := range []uintptr{eventSystemForeground, eventSystemDesktopSwitch} {
		h, _, err := procSetWinEventHook.Call(ev, ev, 0, foregroundCallback, 0, 0, wineventOutOfContext|wineventSkipOwnProcess)
		if h == 0 {
			foregroundMu.Unlock()
			w.unhook()
			if err == nil || errors.Is(err, windows.ERROR_SUCCESS) {
				err = errors.New("no hook handle")
			}
			ready <- fmt.Errorf("SetWinEventHook(%#x): %w", ev, err)
			return
		}
		w.hooks = append(w.hooks, h)
		foregroundWatches[h] = w
	}
	foregroundMu.Unlock()
	defer w.unhook()
	ready <- nil

	w.report(foregroundNow(true))
	for {
		// 0 is WM_QUIT and -1 an error; neither should come, and either
		// way the loop can't go on.
		r, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(r) == 0 || int32(r) == -1 {
			return
		}
		if m.hwnd == 0 && m.message == wmStopForeground {
			return
		}
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
	}
}

// unhook removes the watch's hooks. Only its own thread calls it.
func (w *foregroundWatch) unhook() {
	foregroundMu.Lock()
	defer foregroundMu.Unlock()
	for _, h := range w.hooks {
		procUnhookWinEvent.Call(h)
		delete(foregroundWatches, h)
	}
	w.hooks = nil
}

// stop ends the watch's message loop and waits for its thread to unhook.
// A post fails only when the thread's queue is full, which takes 10,000
// messages nobody reads, so stop tries again until the thread is gone.
func (w *foregroundWatch) stop() {
	for {
		if r, _, _ := procPostThreadMessageW.Call(uintptr(w.tid), wmStopForeground, 0, 0); r != 0 {
			<-w.done
			return
		}
		select {
		case <-w.done:
			return
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// onWinEvent is WINEVENTPROC. Its DWORD and LONG arguments are declared 32
// bits wide for the reason onSuspendResume's type is. It runs on the
// watch's own thread, inside GetMessage.
func onWinEvent(hook uintptr, ev uint32, hwnd uintptr, idObject, idChild int32, thread, msTime uint32) uintptr {
	foregroundMu.Lock()
	w := foregroundWatches[hook]
	foregroundMu.Unlock()
	if w == nil {
		return 0
	}
	switch ev {
	case eventSystemForeground:
		w.report(foregroundOf(time.Now(), hwnd))
	case eventSystemDesktopSwitch:
		// The event names no window. Back on this desktop, the window in
		// front is whatever GetForegroundWindow says.
		w.report(foregroundNow(false))
	}
	return 0
}

// report passes f on unless it repeats the last report. A window that
// comes to the front again after a moment on another desktop is a change.
func (w *foregroundWatch) report(f Foreground) {
	if !f.Start && f.Window == w.last.Window && f.PID == w.last.PID && f.Unknown == w.last.Unknown {
		return
	}
	w.last = f
	w.fn(f)
}

// foregroundNow reads the window in front now.
func foregroundNow(start bool) Foreground {
	f := foregroundOf(time.Now(), uintptr(windows.GetForegroundWindow()))
	if f.Unknown == UnknownNoWindow {
		// GetForegroundWindow gives no window while another desktop has
		// the input, which is the usual reason it gives none at a desktop
		// switch or at the start.
		f.Unknown = UnknownOtherDesktop
	}
	f.Start = start
	return f
}

// foregroundOf names the process of window hwnd, which Windows reported in
// front at at.
func foregroundOf(at time.Time, hwnd uintptr) Foreground {
	return foregroundFrom(at, hwnd, func(h uintptr) uint32 {
		var pid uint32
		windows.GetWindowThreadProcessId(windows.HWND(h), &pid)
		return pid
	})
}

// foregroundFrom is foregroundOf with the process lookup passed in, so
// tests can give it windows that don't exist.
func foregroundFrom(at time.Time, hwnd uintptr, pidOf func(uintptr) uint32) Foreground {
	f := Foreground{At: at}
	if hwnd == 0 {
		f.Unknown = UnknownNoWindow
		return f
	}
	pid := pidOf(hwnd)
	if pid == 0 {
		f.Unknown = UnknownNoProcess
		return f
	}
	f.Window, f.PID = hwnd, int(pid)
	return f
}
