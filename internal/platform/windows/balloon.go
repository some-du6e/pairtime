// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build windows

package windows

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"time"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

// A notification on Windows is a balloon from a notification-area icon
// (Shell_NotifyIconW with NIF_INFO), which Windows 10 and 11 show as a
// toast (TE WP4.6). pairtime has no tray icon of its own yet, so each
// balloon gets a transient one: a hidden window and an icon, made on one
// locked OS thread, kept while the balloon is up, then removed. Nothing is
// left between notifications.

var (
	// ErrNoNotificationArea means there is no notification area to show a
	// balloon from: Explorer's taskbar isn't running, as in a session with
	// no desktop, or the shell refused the icon.
	ErrNoNotificationArea = errors.New("balloon: no notification area to show a balloon from")
	// ErrUserAway means Windows says nobody is at the screen: it is locked,
	// a screen saver runs, or another user's session is in front
	// (QUNS_NOT_PRESENT).
	ErrUserAway = errors.New("balloon: the screen is locked or another session is in front")
	// ErrUserBusy means Windows says this isn't a time for notifications: a
	// full-screen app or game, presentation settings, or the quiet hour after
	// a user's first sign-in.
	ErrUserBusy = errors.New("balloon: Windows isn't showing notifications now")
)

// balloonLifetime is the longest a balloon's icon stays up. Windows tells
// the window when the balloon times out or is clicked, which ends it
// sooner; a toast shows for 5 s unless the user chose longer.
const balloonLifetime = 30 * time.Second

// balloonCleanupWait is how long ShowBalloon waits, once ctx is done, for
// the balloon's thread to take its icon down. The thread ends its wait
// within milliseconds of ctx, so this only matters for a thread that is
// stuck, which ShowBalloon then leaves behind rather than block its caller.
const balloonCleanupWait = time.Second

// ShowBalloon shows title and text in a balloon and returns when the
// balloon is gone, after at most balloonLifetime, or when ctx is done; the
// icon and its window are removed either way. Once ctx is done it returns
// within balloonCleanupWait, even if the balloon's thread is stuck. It
// first asks Windows whether this is a time for a notification
// (SHQueryUserNotificationState), and returns ErrUserAway or ErrUserBusy
// without showing anything when it isn't.
// The balloon plays no sound, respects the quiet hour after a first
// sign-in, and is dropped rather than queued when Windows can't show it at
// once (NIF_REALTIME). Focus Assist, or Do Not Disturb on Windows 11, holds
// it back like any other notification.
func ShowBalloon(ctx context.Context, title, text string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	state, err := QueryUserNotificationState()
	if err != nil {
		return err
	}
	switch state {
	case qunsAcceptsNotifications:
	case qunsNotPresent:
		return ErrUserAway
	default:
		return fmt.Errorf("%w (%s)", ErrUserBusy, state)
	}
	if text == "" {
		text = title
	}
	return onBalloonThread(ctx, balloonCleanupWait, func(ctx context.Context) error {
		return showBalloonOnThisThread(ctx, title, text, balloonLifetime)
	})
}

// onBalloonThread runs show on a new goroutine locked to its OS thread and
// returns show's error. When ctx is done first, it waits up to cleanup for
// show to see ctx and take its icon down, so a stop leaves no ghost icon,
// and then returns ctx's error rather than wait on a thread that is stuck.
// done has room for show's answer, so a thread that finishes later still
// ends.
func onBalloonThread(ctx context.Context, cleanup time.Duration, show func(context.Context) error) error {
	done := make(chan error, 1)
	go func() {
		// The window, its icon and its messages belong to one OS thread.
		// This goroutine never unlocks it, so the thread ends with the
		// goroutine and carries nothing of the window's to other work,
		// such as a WM_QUIT left in its queue.
		runtime.LockOSThread()
		done <- show(ctx)
	}()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
	}
	select {
	case err := <-done:
		return err
	case <-time.After(cleanup):
		return ctx.Err()
	}
}

// showBalloonOnThisThread makes the window and icon, shows the balloon,
// waits, and removes both. The caller has locked its OS thread.
func showBalloonOnThisThread(ctx context.Context, title, text string, lifetime time.Duration) error {
	b, err := newBalloon()
	if err != nil {
		return err
	}
	defer b.close()
	if err := b.addIcon(); err != nil {
		return err
	}
	if err := b.show(title, text); err != nil {
		return err
	}
	return b.wait(ctx, lifetime)
}

// UserNotificationState is SHQueryUserNotificationState's answer: whether
// Windows thinks this is a time to show the user a notification.
type UserNotificationState int32

// The QUERY_USER_NOTIFICATION_STATE values.
const (
	qunsNotPresent           UserNotificationState = 1
	qunsBusy                 UserNotificationState = 2
	qunsRunningD3DFullScreen UserNotificationState = 3
	qunsPresentationMode     UserNotificationState = 4
	qunsAcceptsNotifications UserNotificationState = 5
	qunsQuietTime            UserNotificationState = 6
	qunsApp                  UserNotificationState = 7
)

func (s UserNotificationState) String() string {
	switch s {
	case qunsNotPresent:
		return "QUNS_NOT_PRESENT"
	case qunsBusy:
		return "QUNS_BUSY"
	case qunsRunningD3DFullScreen:
		return "QUNS_RUNNING_D3D_FULL_SCREEN"
	case qunsPresentationMode:
		return "QUNS_PRESENTATION_MODE"
	case qunsAcceptsNotifications:
		return "QUNS_ACCEPTS_NOTIFICATIONS"
	case qunsQuietTime:
		return "QUNS_QUIET_TIME"
	case qunsApp:
		return "QUNS_APP"
	}
	return fmt.Sprintf("QUERY_USER_NOTIFICATION_STATE(%d)", int32(s))
}

// QueryUserNotificationState asks Windows whether this is a time to show the
// user a notification. It is one call, made when there is a notification to
// show, never a poll.
func QueryUserNotificationState() (UserNotificationState, error) {
	if err := loadBalloonAPI(); err != nil {
		return 0, err
	}
	var s int32
	r, _, _ := balloonAPI.queryUserNotificationState.Call(uintptr(unsafe.Pointer(&s)))
	if hr := int32(r); hr < 0 {
		return 0, fmt.Errorf("SHQueryUserNotificationState: HRESULT 0x%08X", uint32(hr))
	}
	return UserNotificationState(s), nil
}

// balloonAPI holds the user32 and shell32 calls the balloon makes. They are
// looked up once, and a missing one makes every balloon fail with
// ErrNoNotificationArea instead of crashing the agent.
var balloonAPI struct {
	once sync.Once
	err  error

	createWindowEx, defWindowProc, destroyWindow, dispatchMessage, getMessage,
	loadIcon, postMessage, postQuitMessage, registerClassEx, setTimer,
	translateMessage, shellNotifyIcon, queryUserNotificationState *windows.LazyProc
}

func loadBalloonAPI() error {
	a := &balloonAPI
	a.once.Do(func() {
		user32 := windows.NewLazySystemDLL("user32.dll")
		shell32 := windows.NewLazySystemDLL("shell32.dll")
		a.createWindowEx = user32.NewProc("CreateWindowExW")
		a.defWindowProc = user32.NewProc("DefWindowProcW")
		a.destroyWindow = user32.NewProc("DestroyWindow")
		a.dispatchMessage = user32.NewProc("DispatchMessageW")
		a.getMessage = user32.NewProc("GetMessageW")
		a.loadIcon = user32.NewProc("LoadIconW")
		a.postMessage = user32.NewProc("PostMessageW")
		a.postQuitMessage = user32.NewProc("PostQuitMessage")
		a.registerClassEx = user32.NewProc("RegisterClassExW")
		a.setTimer = user32.NewProc("SetTimer")
		a.translateMessage = user32.NewProc("TranslateMessage")
		a.shellNotifyIcon = shell32.NewProc("Shell_NotifyIconW")
		a.queryUserNotificationState = shell32.NewProc("SHQueryUserNotificationState")
		for _, p := range []*windows.LazyProc{
			a.createWindowEx, a.defWindowProc, a.destroyWindow, a.dispatchMessage, a.getMessage,
			a.loadIcon, a.postMessage, a.postQuitMessage, a.registerClassEx, a.setTimer,
			a.translateMessage, a.shellNotifyIcon, a.queryUserNotificationState,
		} {
			if err := p.Find(); err != nil {
				a.err = fmt.Errorf("%w: %w", ErrNoNotificationArea, err)
				return
			}
		}
	})
	return a.err
}

// The constants the balloon uses, from shellapi.h and winuser.h.
const (
	nimAdd        = 0x0
	nimModify     = 0x1
	nimDelete     = 0x2
	nimSetVersion = 0x4

	nifMessage  = 0x01
	nifIcon     = 0x02
	nifTip      = 0x04
	nifInfo     = 0x10
	nifRealtime = 0x40
	nifShowTip  = 0x80

	niifInfo             = 0x01
	niifNoSound          = 0x10
	niifRespectQuietTime = 0x80

	notifyIconVersion4 = 4

	// The icon events, in the low word of lParam under NOTIFYICON_VERSION_4.
	ninBalloonHide      = 0x0400 + 3 // WM_USER + 3
	ninBalloonTimeout   = 0x0400 + 4
	ninBalloonUserClick = 0x0400 + 5

	idiInformation = 32516

	balloonWMDestroy  = 0x0002     // WM_DESTROY
	balloonWMClose    = 0x0010     // WM_CLOSE
	balloonWMTimer    = 0x0113     // WM_TIMER
	balloonWMCallback = 0x8000 + 1 // WM_APP + 1: the shell's messages about the icon
	balloonWMStop     = 0x8000 + 2 // WM_APP + 2: the caller gave up
	balloonIconID     = 1
	balloonTimerID    = 1
)

// notifyIconData is NOTIFYICONDATAW, all of it: the shell reads cbSize to
// tell which version of the structure it was given.
type notifyIconData struct {
	Size            uint32
	Wnd             windows.HWND
	ID              uint32
	Flags           uint32
	CallbackMessage uint32
	Icon            windows.Handle
	Tip             [128]uint16
	State           uint32
	StateMask       uint32
	Info            [256]uint16
	Version         uint32 // a union with uTimeout, which Windows ignores since Vista
	InfoTitle       [64]uint16
	InfoFlags       uint32
	GUIDItem        windows.GUID
	BalloonIcon     windows.Handle
}

// balloonWndClass is WNDCLASSEXW.
type balloonWndClass struct {
	Size       uint32
	Style      uint32
	WndProc    uintptr
	ClsExtra   int32
	WndExtra   int32
	Instance   windows.Handle
	Icon       windows.Handle
	Cursor     windows.Handle
	Background windows.Handle
	MenuName   *uint16
	ClassName  *uint16
	IconSm     windows.Handle
}

// balloonMsg is MSG, with room for lPrivate.
type balloonMsg struct {
	Hwnd    windows.HWND
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      struct{ X, Y int32 }
	Private uint32
}

// balloonPtrWords is 1 on 64-bit Windows and 0 on 32-bit.
const balloonPtrWords = unsafe.Sizeof(uintptr(0)) / 8

// The Windows SDK's sizes: NOTIFYICONDATAW is 976 bytes on 64-bit Windows
// and 956 on 32-bit, WNDCLASSEXW 80 and 48. An index out of range here
// stops the build, for each of the three Windows targets, if a field is
// wrong.
var (
	_ = [1]struct{}{}[unsafe.Sizeof(notifyIconData{})-(956+20*balloonPtrWords)]
	_ = [1]struct{}{}[unsafe.Sizeof(balloonWndClass{})-(48+32*balloonPtrWords)]
)

// balloonClassName is the window class of the hidden windows.
const balloonClassName = "pairtime.notify"

var balloonClass struct {
	once sync.Once
	err  error
	inst windows.Handle
	name *uint16 // kept for every CreateWindowExW
}

// registerBalloonClass registers the hidden windows' class, once per
// process, with one callback for the window procedure: Go can make only so
// many callbacks, and never frees them.
func registerBalloonClass() error {
	c := &balloonClass
	c.once.Do(func() {
		if c.err = loadBalloonAPI(); c.err != nil {
			return
		}
		if err := windows.GetModuleHandleEx(0, nil, &c.inst); err != nil {
			c.err = fmt.Errorf("GetModuleHandleExW: %w", err)
			return
		}
		name, err := windows.UTF16PtrFromString(balloonClassName)
		if err != nil {
			c.err = err
			return
		}
		c.name = name
		wc := balloonWndClass{
			WndProc:   windows.NewCallback(balloonWndProc),
			Instance:  c.inst,
			ClassName: c.name,
		}
		wc.Size = uint32(unsafe.Sizeof(wc))
		if r, _, err := balloonAPI.registerClassEx.Call(uintptr(unsafe.Pointer(&wc))); r == 0 {
			c.err = fmt.Errorf("RegisterClassExW: %w", err)
		}
	})
	return c.err
}

// balloonWndProc is the window procedure of every hidden window. It ends
// the message loop when the balloon is gone, when the lifetime timer fires,
// when the caller gives up, and when something asks the window to close,
// as taskkill without /F does to every top-level window of a process.
//
// WM_CLOSE never reaches DefWindowProc, whose DestroyWindow would take the
// window, and its timer, from under the message loop; the loop ends instead
// and close removes the icon, then the window. A window destroyed some other
// way ends the loop too, at WM_DESTROY, so nothing can leave it waiting.
func balloonWndProc(hwnd windows.HWND, msg uint32, wparam, lparam uintptr) uintptr {
	switch msg {
	case balloonWMCallback:
		switch lparam & 0xffff {
		case ninBalloonHide, ninBalloonTimeout, ninBalloonUserClick:
			balloonAPI.postQuitMessage.Call(0)
		}
		return 0
	case balloonWMTimer, balloonWMStop, balloonWMClose, balloonWMDestroy:
		balloonAPI.postQuitMessage.Call(0)
		return 0
	}
	r, _, _ := balloonAPI.defWindowProc.Call(uintptr(hwnd), uintptr(msg), wparam, lparam)
	return r
}

// balloon is a hidden window and its notification-area icon. Every method
// runs on the OS thread that called newBalloon.
type balloon struct {
	hwnd windows.HWND
	icon bool // the shell has the icon
}

// newBalloon makes the hidden window. It is never shown.
func newBalloon() (*balloon, error) {
	if err := registerBalloonClass(); err != nil {
		return nil, err
	}
	title, err := windows.UTF16PtrFromString("pairtime")
	if err != nil {
		return nil, err
	}
	r, _, err := balloonAPI.createWindowEx.Call(
		0,
		uintptr(unsafe.Pointer(balloonClass.name)),
		uintptr(unsafe.Pointer(title)),
		0,          // no style: never visible
		0, 0, 0, 0, // position and size
		0, // no parent
		0, // no menu
		uintptr(balloonClass.inst),
		0)
	if r == 0 {
		return nil, fmt.Errorf("CreateWindowExW: %w", err)
	}
	return &balloon{hwnd: windows.HWND(r)}, nil
}

func (b *balloon) data() notifyIconData {
	d := notifyIconData{Wnd: b.hwnd, ID: balloonIconID}
	d.Size = uint32(unsafe.Sizeof(d))
	return d
}

// addIcon adds the window's icon to the notification area.
func (b *balloon) addIcon() error {
	icon, _, _ := balloonAPI.loadIcon.Call(0, idiInformation)
	d := b.data()
	d.Flags = nifMessage | nifIcon | nifTip | nifShowTip
	d.CallbackMessage = balloonWMCallback
	d.Icon = windows.Handle(icon)
	putBalloonText(d.Tip[:], "pairtime")
	if !shellNotifyIcon(nimAdd, &d) {
		return fmt.Errorf("%w: Shell_NotifyIconW(NIM_ADD) failed", ErrNoNotificationArea)
	}
	b.icon = true
	// Without version 4 the shell sends the same balloon events, only
	// unpacked, so a refusal here changes nothing the window reads.
	d.Version = notifyIconVersion4
	shellNotifyIcon(nimSetVersion, &d)
	return nil
}

// iconExists asks the shell whether it has the icon, by setting its
// tooltip to the same text: NIM_MODIFY fails for an icon the shell doesn't
// have.
func (b *balloon) iconExists() bool {
	d := b.data()
	d.Flags = nifTip | nifShowTip
	putBalloonText(d.Tip[:], "pairtime")
	return shellNotifyIcon(nimModify, &d)
}

// show puts the balloon up from the icon.
func (b *balloon) show(title, text string) error {
	d := b.data()
	d.Flags = nifInfo | nifRealtime
	d.InfoFlags = niifInfo | niifNoSound | niifRespectQuietTime
	putBalloonText(d.InfoTitle[:], title)
	putBalloonText(d.Info[:], text)
	if !shellNotifyIcon(nimModify, &d) {
		return errors.New("balloon: Shell_NotifyIconW(NIM_MODIFY) refused the balloon")
	}
	return nil
}

// wait runs the window's messages until the balloon is gone, lifetime has
// passed, or ctx is done.
func (b *balloon) wait(ctx context.Context, lifetime time.Duration) error {
	if r, _, err := balloonAPI.setTimer.Call(uintptr(b.hwnd), balloonTimerID, uintptr(lifetime.Milliseconds()), 0); r == 0 {
		return fmt.Errorf("SetTimer: %w", err)
	}
	stop := make(chan struct{})
	var watcher sync.WaitGroup
	watcher.Add(1)
	go func() {
		defer watcher.Done()
		select {
		case <-ctx.Done():
			balloonAPI.postMessage.Call(uintptr(b.hwnd), balloonWMStop, 0, 0)
		case <-stop:
		}
	}()
	err := pumpBalloonMessages()
	close(stop)
	// The window must outlive the watcher, which may be posting to it.
	watcher.Wait()
	return err
}

// removeIcon takes the icon out of the notification area.
func (b *balloon) removeIcon() {
	if b.icon {
		d := b.data()
		shellNotifyIcon(nimDelete, &d)
		b.icon = false
	}
}

// close removes the icon and destroys the window. A balloon still up goes
// with its icon.
func (b *balloon) close() {
	b.removeIcon()
	if b.hwnd != 0 {
		balloonAPI.destroyWindow.Call(uintptr(b.hwnd))
		b.hwnd = 0
	}
}

// pumpBalloonMessages runs this thread's message loop until the window
// procedure posts WM_QUIT.
func pumpBalloonMessages() error {
	var m balloonMsg
	for {
		r, _, err := balloonAPI.getMessage.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		switch int32(r) {
		case 0:
			return nil
		case -1:
			return fmt.Errorf("GetMessageW: %w", err)
		}
		balloonAPI.translateMessage.Call(uintptr(unsafe.Pointer(&m)))
		balloonAPI.dispatchMessage.Call(uintptr(unsafe.Pointer(&m)))
	}
}

func shellNotifyIcon(msg uint32, d *notifyIconData) bool {
	r, _, _ := balloonAPI.shellNotifyIcon.Call(uintptr(msg), uintptr(unsafe.Pointer(d)))
	return r != 0
}

// putBalloonText copies s into dst as UTF-16 ending in NUL, cut to fit
// without splitting a surrogate pair.
func putBalloonText(dst []uint16, s string) {
	u := utf16.Encode([]rune(s))
	if limit := len(dst) - 1; len(u) > limit {
		u = u[:limit]
		if last := u[len(u)-1]; last >= 0xd800 && last < 0xdc00 {
			u = u[:len(u)-1]
		}
	}
	n := copy(dst, u)
	dst[n] = 0
}
