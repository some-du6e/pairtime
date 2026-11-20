// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build windows

package windows

import (
	"encoding/binary"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"

	"golang.org/x/sys/windows"

	"github.com/some-du6e/pairtime/pkg/event"
)

// Broadcasts that aren't suspends or resumes, from winuser.h.
const (
	pbtAPMPowerStatusChange = 0xA    // PBT_APMPOWERSTATUSCHANGE: AC or battery
	pbtPowerSettingChange   = 0x8013 // PBT_POWERSETTINGCHANGE
)

var procSendMessageW = modUser32.NewProc("SendMessageW")

// recorder collects the ops a watch delivers, from whatever thread.
type recorder struct {
	mu  sync.Mutex
	ops []event.PowerOp
}

func (r *recorder) record(op event.PowerOp) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ops = append(r.ops, op)
}

func (r *recorder) got() []event.PowerOp {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.ops)
}

// TestWatchSuspend registers with Windows for real, then plays power
// broadcasts through the callback it registered, the way Windows calls it.
// A hosted runner can't be put to sleep.
func TestWatchSuspend(t *testing.T) {
	tests := []struct {
		name  string
		types []uint32
		want  []event.PowerOp
	}{
		{
			name:  "a suspend and a wake by the user give one suspend and one resume",
			types: []uint32{pbtAPMSuspend, pbtAPMResumeAutomatic, pbtAPMResumeSuspend},
			want:  []event.PowerOp{event.PowerSuspend, event.PowerResume},
		},
		{
			name:  "an automatic wake gives a resume",
			types: []uint32{pbtAPMSuspend, pbtAPMResumeAutomatic},
			want:  []event.PowerOp{event.PowerSuspend, event.PowerResume},
		},
		{
			name:  "two sleeps give two suspends and two resumes",
			types: []uint32{pbtAPMSuspend, pbtAPMResumeAutomatic, pbtAPMSuspend, pbtAPMResumeSuspend},
			want:  []event.PowerOp{event.PowerSuspend, event.PowerResume, event.PowerSuspend, event.PowerResume},
		},
		{
			name:  "battery and power-setting broadcasts give nothing",
			types: []uint32{pbtAPMPowerStatusChange, pbtPowerSettingChange},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var r recorder
			stop, err := WatchSuspend(r.record)
			if err != nil {
				t.Fatal(err)
			}
			suspendMu.Lock()
			id := suspendLastID
			suspendMu.Unlock()
			for _, typ := range tt.types {
				syscall.SyscallN(suspendCallback, id, uintptr(typ), 0)
			}
			stop()
			// After stop the registration is gone, and a late call
			// reaches nobody.
			syscall.SyscallN(suspendCallback, id, pbtAPMSuspend, 0)
			if got := r.got(); !slices.Equal(got, tt.want) {
				t.Errorf("delivered %v, want %v", got, tt.want)
			}
		})
	}
}

// TestWatchLock makes the real message-only window and registers it with
// WTS, then sends it the session-change messages Windows would. Nothing
// locks the runner's session.
func TestWatchLock(t *testing.T) {
	tests := []struct {
		name  string
		codes []uintptr
		want  []event.PowerOp
	}{
		{
			name:  "a lock and an unlock reach fn in order",
			codes: []uintptr{windows.WTS_SESSION_LOCK, windows.WTS_SESSION_UNLOCK},
			want:  []event.PowerOp{event.PowerLock, event.PowerUnlock},
		},
		{
			name:  "logons, logoffs and remote connects reach nothing",
			codes: []uintptr{windows.WTS_SESSION_LOGON, windows.WTS_SESSION_LOGOFF, windows.WTS_REMOTE_CONNECT, windows.WTS_REMOTE_DISCONNECT},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var r recorder
			w, err := startLockWatch(r.record)
			if err != nil {
				t.Fatal(err)
			}
			for _, code := range tt.codes {
				// SendMessageW returns once the window's thread has run
				// the window procedure.
				procSendMessageW.Call(w.hwnd, wmWTSSessionChange, code, 0)
			}
			w.stop()
			// The window went with its thread; nothing reaches fn now.
			procSendMessageW.Call(w.hwnd, wmWTSSessionChange, windows.WTS_SESSION_LOCK, 0)
			if got := r.got(); !slices.Equal(got, tt.want) {
				t.Errorf("delivered %v, want %v", got, tt.want)
			}
			lockMu.Lock()
			left := len(lockWatches)
			lockMu.Unlock()
			if left != 0 {
				t.Errorf("%d lock watches still registered after stop", left)
			}
		})
	}
}

// TestSessionLocked reads the runner's session for real. Its lock state
// is the runner's business, so only the read is checked.
func TestSessionLocked(t *testing.T) {
	tests := []struct {
		name string
	}{
		{"WTSQuerySessionInformationW reads this session's lock state"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := SessionLocked(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// TestSessionLockedInfo checks where SessionLocked finds the flags in a
// WTSINFOEXW.
func TestSessionLockedInfo(t *testing.T) {
	info := func(level, flags uint32, size int) []byte {
		b := make([]byte, size)
		binary.LittleEndian.PutUint32(b[0:], level)
		binary.LittleEndian.PutUint32(b[8:], 1)  // SessionId
		binary.LittleEndian.PutUint32(b[12:], 0) // SessionState: WTSActive
		if size >= 20 {
			binary.LittleEndian.PutUint32(b[16:], flags)
		}
		return b
	}
	tests := []struct {
		name    string
		info    []byte
		want    bool
		wantErr string
	}{
		{name: "flags 0 at offset 16 mean locked", info: info(1, wtsSessionStateLock, 24), want: true},
		{name: "flags 1 mean unlocked", info: info(1, wtsSessionStateUnlock, 24), want: false},
		{name: "flags 0xFFFFFFFF mean the state is unknown", info: info(1, 0xFFFFFFFF, 24), wantErr: "unknown"},
		{name: "a level other than 1 is refused", info: info(2, wtsSessionStateUnlock, 24), wantErr: "level 2"},
		{name: "a buffer too short for the flags is refused", info: info(1, 0, 16), wantErr: "16 bytes"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := sessionLocked(tt.info)
			switch {
			case tt.wantErr == "" && err != nil:
				t.Fatalf("sessionLocked: %v", err)
			case tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)):
				t.Fatalf("sessionLocked error %v, want one containing %q", err, tt.wantErr)
			case got != tt.want:
				t.Errorf("sessionLocked = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestOps checks which notifications become os.power ops.
func TestOps(t *testing.T) {
	tests := []struct {
		name   string
		op     func() (event.PowerOp, bool)
		want   event.PowerOp
		wantOK bool
	}{
		{"PBT_APMSUSPEND is a suspend", func() (event.PowerOp, bool) { return suspendOp(pbtAPMSuspend) }, event.PowerSuspend, true},
		{"PBT_APMRESUMEAUTOMATIC is a resume", func() (event.PowerOp, bool) { return suspendOp(pbtAPMResumeAutomatic) }, event.PowerResume, true},
		{"PBT_APMRESUMESUSPEND is a resume", func() (event.PowerOp, bool) { return suspendOp(pbtAPMResumeSuspend) }, event.PowerResume, true},
		{"a battery change is no op", func() (event.PowerOp, bool) { return suspendOp(pbtAPMPowerStatusChange) }, "", false},
		{"WTS_SESSION_LOCK is a lock", func() (event.PowerOp, bool) { return lockOp(windows.WTS_SESSION_LOCK) }, event.PowerLock, true},
		{"WTS_SESSION_UNLOCK is an unlock", func() (event.PowerOp, bool) { return lockOp(windows.WTS_SESSION_UNLOCK) }, event.PowerUnlock, true},
		{"WTS_CONSOLE_CONNECT is no op", func() (event.PowerOp, bool) { return lockOp(windows.WTS_CONSOLE_CONNECT) }, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := tt.op()
			if got != tt.want || ok != tt.wantOK {
				t.Errorf("got %q, %v; want %q, %v", got, ok, tt.want, tt.wantOK)
			}
		})
	}
}
