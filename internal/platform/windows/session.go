// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build windows

package windows

import (
	"encoding/binary"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/some-du6e/pairtime/pkg/event"
)

// From winuser.h and wtsapi32.h.
const (
	wmDestroy          = 0x0002 // WM_DESTROY
	wmClose            = 0x0010 // WM_CLOSE
	wmWTSSessionChange = 0x02B1 // WM_WTSSESSION_CHANGE

	notifyForThisSession = 0 // NOTIFY_FOR_THIS_SESSION

	// hwndMessage is HWND_MESSAGE, (HWND)-3, the parent that makes a
	// window message-only: never shown, and sent nothing but what is
	// addressed to it.
	hwndMessage = ^uintptr(2)

	wtsCurrentServerHandle = 0          // WTS_CURRENT_SERVER_HANDLE
	wtsCurrentSession      = 0xFFFFFFFF // WTS_CURRENT_SESSION
	wtsSessionInfoEx       = 25         // WTSSessionInfoEx in WTS_INFO_CLASS
	wtsSessionStateLock    = 0x0        // WTS_SESSIONSTATE_LOCK
	wtsSessionStateUnlock  = 0x1        // WTS_SESSIONSTATE_UNLOCK
)

var (
	procWTSRegisterSessionNotification   = modWtsapi32.NewProc("WTSRegisterSessionNotification")
	procWTSUnRegisterSessionNotification = modWtsapi32.NewProc("WTSUnRegisterSessionNotification")
	procWTSQuerySessionInformationW      = modWtsapi32.NewProc("WTSQuerySessionInformationW")

	procRegisterClassExW = modUser32.NewProc("RegisterClassExW")
	procCreateWindowExW  = modUser32.NewProc("CreateWindowExW")
	procDefWindowProcW   = modUser32.NewProc("DefWindowProcW")
	procDestroyWindow    = modUser32.NewProc("DestroyWindow")
	procGetMessageW      = modUser32.NewProc("GetMessageW")
	procDispatchMessageW = modUser32.NewProc("DispatchMessageW")
	procPostMessageW     = modUser32.NewProc("PostMessageW")
	procPostQuitMessage  = modUser32.NewProc("PostQuitMessage")
)

// wndClassEx is WNDCLASSEXW.
type wndClassEx struct {
	cbSize        uint32
	style         uint32
	lpfnWndProc   uintptr
	cbClsExtra    int32
	cbWndExtra    int32
	hInstance     windows.Handle
	hIcon         windows.Handle
	hCursor       windows.Handle
	hbrBackground windows.Handle
	lpszMenuName  *uint16
	lpszClassName *uint16
	hIconSm       windows.Handle
}

// winMsg is MSG.
type winMsg struct {
	hwnd     uintptr
	message  uint32
	wParam   uintptr
	lParam   uintptr
	time     uint32
	pt       struct{ x, y int32 }
	lPrivate uint32
}

// The window class every lock watch uses, registered once per process with
// the one window procedure, for the same reason the suspend watch shares a
// callback.
var (
	lockClassOnce sync.Once
	lockClassErr  error
	lockClassName *uint16
	lockInstance  windows.Handle

	lockMu      sync.Mutex
	lockWatches = make(map[uintptr]*lockWatch) // by window handle
)

// lockWatch is one message-only window registered for the session's
// notifications, with the locked OS thread that owns it and runs its
// message loop.
type lockWatch struct {
	fn   func(event.PowerOp)
	hwnd uintptr
	done chan struct{} // closed when the message loop has exited
}

// WatchLock calls fn with event.PowerLock and event.PowerUnlock as the
// session is locked and unlocked, until stop is called. Windows sends
// those only to a window, so WatchLock makes a message-only one, owned by
// a thread that sleeps in GetMessage between notifications and so costs
// no wakeups, and registers it with WTSRegisterSessionNotification.
func WatchLock(fn func(event.PowerOp)) (stop func(), err error) {
	w, err := startLockWatch(fn)
	if err != nil {
		return nil, err
	}
	return w.stop, nil
}

func startLockWatch(fn func(event.PowerOp)) (*lockWatch, error) {
	if err := registerLockClass(); err != nil {
		return nil, err
	}
	w := &lockWatch{fn: fn, done: make(chan struct{})}
	ready := make(chan error, 1)
	go w.run(ready)
	if err := <-ready; err != nil {
		<-w.done
		return nil, err
	}
	return w, nil
}

func registerLockClass() error {
	lockClassOnce.Do(func() {
		for _, p := range []*windows.LazyProc{
			procWTSRegisterSessionNotification, procWTSUnRegisterSessionNotification,
			procRegisterClassExW, procCreateWindowExW, procDefWindowProcW, procDestroyWindow,
			procGetMessageW, procDispatchMessageW, procPostMessageW, procPostQuitMessage,
		} {
			if lockClassErr = p.Find(); lockClassErr != nil {
				return
			}
		}
		if lockClassName, lockClassErr = windows.UTF16PtrFromString("pairtime.SessionWatch"); lockClassErr != nil {
			return
		}
		if lockClassErr = windows.GetModuleHandleEx(0, nil, &lockInstance); lockClassErr != nil {
			lockClassErr = fmt.Errorf("GetModuleHandleEx: %w", lockClassErr)
			return
		}
		wc := wndClassEx{
			lpfnWndProc:   windows.NewCallback(lockWndProc),
			hInstance:     lockInstance,
			lpszClassName: lockClassName,
		}
		wc.cbSize = uint32(unsafe.Sizeof(wc))
		r, _, err := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))
		if r == 0 && !errors.Is(err, windows.ERROR_CLASS_ALREADY_EXISTS) {
			lockClassErr = fmt.Errorf("RegisterClassExW: %w", err)
		}
	})
	return lockClassErr
}

// run owns the window from creation to destruction. A window belongs to
// the thread that made it, so run keeps its OS thread, and never unlocks
// it: Go ends the thread with the goroutine instead of reusing one that
// had a message queue.
func (w *lockWatch) run(ready chan<- error) {
	runtime.LockOSThread()
	defer close(w.done)

	hwnd, _, err := procCreateWindowExW.Call(0, uintptr(unsafe.Pointer(lockClassName)), 0, 0,
		0, 0, 0, 0, hwndMessage, 0, uintptr(lockInstance), 0)
	if hwnd == 0 {
		ready <- fmt.Errorf("CreateWindowExW: %w", err)
		return
	}
	w.hwnd = hwnd
	lockMu.Lock()
	lockWatches[hwnd] = w
	lockMu.Unlock()
	defer func() {
		lockMu.Lock()
		delete(lockWatches, hwnd)
		lockMu.Unlock()
	}()

	if r, _, err := procWTSRegisterSessionNotification.Call(hwnd, notifyForThisSession); r == 0 {
		procDestroyWindow.Call(hwnd)
		ready <- fmt.Errorf("WTSRegisterSessionNotification: %w", err)
		return
	}
	ready <- nil

	var m winMsg
	for {
		// 0 is WM_QUIT, which WM_DESTROY posts; -1 is an error, after
		// which the window can't be served and goes too.
		r, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(r) == 0 {
			return
		}
		if int32(r) == -1 {
			procDestroyWindow.Call(hwnd)
			return
		}
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
	}
}

// stop closes the window and waits for its thread to finish.
func (w *lockWatch) stop() {
	procPostMessageW.Call(w.hwnd, wmClose, 0, 0)
	<-w.done
}

// lockWndProc is the window procedure. Its message is a UINT, declared 32
// bits wide for the reason onSuspendResume's type is.
func lockWndProc(hwnd uintptr, msg uint32, wParam, lParam uintptr) uintptr {
	switch msg {
	case wmWTSSessionChange:
		if op, ok := lockOp(wParam); ok {
			lockMu.Lock()
			w := lockWatches[hwnd]
			lockMu.Unlock()
			if w != nil {
				w.fn(op)
			}
		}
		return 0
	case wmClose:
		procDestroyWindow.Call(hwnd)
		return 0
	case wmDestroy:
		procWTSUnRegisterSessionNotification.Call(hwnd)
		procPostQuitMessage.Call(0)
		return 0
	}
	r, _, _ := procDefWindowProcW.Call(hwnd, uintptr(msg), wParam, lParam)
	return r
}

// lockOp maps a WM_WTSSESSION_CHANGE code to an os.power op. Logons,
// remote connects and the rest aren't presence events.
func lockOp(code uintptr) (event.PowerOp, bool) {
	switch code {
	case windows.WTS_SESSION_LOCK:
		return event.PowerLock, true
	case windows.WTS_SESSION_UNLOCK:
		return event.PowerUnlock, true
	}
	return "", false
}

// SessionLocked reports whether the calling process's session is locked
// now, from WTSQuerySessionInformationW's WTSSessionInfoEx. The agent asks
// once at start and follows WatchLock after that.
func SessionLocked() (bool, error) {
	if err := procWTSQuerySessionInformationW.Find(); err != nil {
		return false, err
	}
	var buf *byte
	var n uint32
	r, _, err := procWTSQuerySessionInformationW.Call(wtsCurrentServerHandle, wtsCurrentSession, wtsSessionInfoEx,
		uintptr(unsafe.Pointer(&buf)), uintptr(unsafe.Pointer(&n)))
	if r == 0 {
		return false, fmt.Errorf("WTSQuerySessionInformationW(WTSSessionInfoEx): %w", err)
	}
	defer windows.WTSFreeMemory(uintptr(unsafe.Pointer(buf)))
	return sessionLocked(unsafe.Slice(buf, n))
}

// sessionLocked reads the lock state from a WTSINFOEXW. That is a DWORD
// Level and then a union whose one member, WTSINFOEX_LEVEL1_W, holds
// LARGE_INTEGERs, so the union is 8-byte aligned on 32- and 64-bit Windows
// alike: SessionId at offset 8, SessionState at 12, SessionFlags at 16.
// (Windows 7 swapped the two flag values, but Go no longer runs there.)
func sessionLocked(info []byte) (bool, error) {
	if len(info) < 20 {
		return false, fmt.Errorf("WTSSessionInfoEx returned %d bytes, too few for WTSINFOEXW", len(info))
	}
	if level := binary.LittleEndian.Uint32(info[0:4]); level != 1 {
		return false, fmt.Errorf("WTSSessionInfoEx returned level %d, not 1", level)
	}
	switch flags := binary.LittleEndian.Uint32(info[16:20]); flags {
	case wtsSessionStateLock:
		return true, nil
	case wtsSessionStateUnlock:
		return false, nil
	default:
		return false, fmt.Errorf("the session's lock state is unknown (WTSSessionInfoEx flags %#x)", flags)
	}
}
