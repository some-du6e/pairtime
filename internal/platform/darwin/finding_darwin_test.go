// SPDX-License-Identifier: AGPL-3.0-or-later

package darwin

import (
	"fmt"
	"math"
	"os"
	"runtime"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// TestFindingKeyDownIdle answers WP-OS3's open question: does
// CGEventSourceSecondsSinceLastEventType with kCGEventKeyDown need the
// Input Monitoring permission (SM 1.2, DESIGN 5.7)? It records what the
// calls return on macos-latest and, to see whether key-down idle notices a
// key at all, posts one key-down and key-up of F20, a key with no default
// action, and reads again. It writes macos-key-down-idle.json to
// $PAIRTIME_FINDINGS_DIR, which platform.yml uploads as an artifact, and
// fails only when it can't write the file. It never asks for a permission.
//
// Posting events is a test-only tool here. The client never posts, taps or
// listens to input events (DESIGN 7.2).
func TestFindingKeyDownIdle(t *testing.T) {
	if os.Getenv(findingsEnv) == "" {
		t.Skipf("%s isn't set; platform.yml sets it on macos-latest", findingsEnv)
	}
	if err := loadCG(); err != nil {
		t.Fatal(err)
	}
	if err := loadIOKit(); err != nil {
		t.Fatal(err)
	}
	p, err := bindProbe()
	if err != nil {
		t.Fatal(err)
	}

	f := keyFinding{
		Question: "Does CGEventSourceSecondsSinceLastEventType with kCGEventKeyDown need the Input Monitoring permission? (SM 1.2, DESIGN 5.7, WP-OS3)",
		MacOS:    sysctl("kern.osproductversion"),
		Build:    sysctl("kern.osversion"),
		Model:    sysctl("hw.model"),
		Arch:     runtime.GOARCH,
		Go:       runtime.Version(),
		Run:      os.Getenv("GITHUB_RUN_ID") + "/" + os.Getenv("GITHUB_RUN_ATTEMPT"),
		UptimeS:  uptime(),
	}
	if granted, known := listenAccess(); known {
		f.InputMonitoring = &granted
	}
	f.IOHIDListenAccess = p.hidAccess()
	if p.preflightPost != nil {
		granted := p.preflightPost()
		f.PostEventAccess = &granted
	}
	_, f.SessionDictionary = (macPresence{}).session()
	if s, err := Presence(); err != nil {
		f.PresenceError = err.Error()
	} else {
		f.Presence = &s
	}

	f.Before = p.read()
	f.Probe = p.post()
	f.Answer, f.Detail = f.conclude()
	if !writeFinding(t, "macos-key-down-idle.json", f) {
		t.Fatalf("%s is set but nothing was written", findingsEnv)
	}
	t.Logf("%s: %s", f.Answer, f.Detail)
}

// keyFinding is the artifact. Numbers that came back NaN or infinite are
// null, since JSON has no such values.
type keyFinding struct {
	Question string `json:"question"`
	Answer   string `json:"answer"`
	Detail   string `json:"detail"`

	MacOS   string   `json:"macos_version"`
	Build   string   `json:"macos_build"`
	Model   string   `json:"hw_model"`
	Arch    string   `json:"arch"`
	Go      string   `json:"go"`
	Run     string   `json:"github_run"`
	UptimeS *float64 `json:"uptime_s"`

	// InputMonitoring is CGPreflightListenEventAccess: whether this process
	// has Input Monitoring. IOHIDListenAccess is IOHIDCheckAccess for
	// kIOHIDRequestTypeListenEvent. PostEventAccess is
	// CGPreflightPostEventAccess, which the probe's key event needs.
	InputMonitoring   *bool  `json:"input_monitoring_granted"`
	IOHIDListenAccess string `json:"iohid_listen_access"`
	PostEventAccess   *bool  `json:"post_event_granted"`
	SessionDictionary bool   `json:"session_dictionary"`

	Presence      any    `json:"presence_sample,omitempty"`
	PresenceError string `json:"presence_error,omitempty"`

	Before idleReading `json:"before"`
	Probe  keyProbe    `json:"probe"`
}

// idleReading is every idle value the question touches, read at once.
type idleReading struct {
	// For kCGEventSourceStateHIDSystemState, which Presence reads, and
	// kCGEventSourceStateCombinedSessionState.
	AnyHIDS       *float64 `json:"idle_any_s_hid_state"`
	KeysHIDS      *float64 `json:"idle_keys_s_hid_state"`
	AnyCombinedS  *float64 `json:"idle_any_s_combined_state"`
	KeysCombinedS *float64 `json:"idle_keys_s_combined_state"`
	// HIDIdleS is IOKit's HIDIdleTime, the any-input fallback.
	HIDIdleS *float64 `json:"hid_idle_time_s"`
	// KeyDowns is CGEventSourceCounterForEventType for key-down events.
	KeyDowns uint32 `json:"key_down_count_hid_state"`
}

type keyProbe struct {
	Key       string      `json:"key"`
	Attempted bool        `json:"attempted"`
	Error     string      `json:"error,omitempty"`
	After     idleReading `json:"after"`
	// AnyReset says the posted key reached the HID system state;
	// KeysReset says key-down idle saw it too.
	AnyReset  bool `json:"any_input_idle_reset"`
	KeysReset bool `json:"key_down_idle_reset"`
	CountRose bool `json:"key_down_count_rose"`
}

// conclude turns the readings into the one-line answer Karim reads first.
func (f keyFinding) conclude() (answer, detail string) {
	keys := "null"
	if v := f.Probe.After.KeysHIDS; v != nil {
		keys = fmt.Sprintf("%.3f s", *v)
	}
	switch {
	case f.InputMonitoring != nil && *f.InputMonitoring:
		return "inconclusive", "the test process has Input Monitoring on this runner, so this run can't show what the call returns without it"
	case !f.Probe.Attempted:
		return "inconclusive", "no key event could be made, so there was no fresh key press to look for: " + f.Probe.Error
	case !f.Probe.AnyReset:
		return "inconclusive", "the posted key-down didn't reach the HID system state (any-input idle didn't drop), most likely because the runner refuses posted events without Accessibility"
	case f.Probe.KeysReset:
		return "no permission needed", "without Input Monitoring, key-down idle dropped to " + keys + " after a posted key-down, as any-input idle did"
	}
	return "permission needed, or posted keys don't count", "without Input Monitoring, any-input idle dropped after a posted key-down but key-down idle stayed at " + keys +
		": the call needs the permission, or it counts only hardware keys. One hardware key press on a Mac without the permission tells the two apart"
}

// probe holds the calls only this test makes.
type probe struct {
	sourceCreate     func(state int32) uintptr
	keyboardEvent    func(source uintptr, key uint16, down bool) uintptr
	postEvent        func(tap uint32, ev uintptr)
	counter          func(state int32, eventType uint32) uint32
	preflightPost    func() bool
	hidCheckAccess   func(requestType uint32) uint32
	secondsSinceLast func(state int32, eventType uint32) float64
}

const (
	cgStateCombinedSession int32  = 0    // kCGEventSourceStateCombinedSessionState
	cgHIDEventTap          uint32 = 0    // kCGHIDEventTap
	kVKF20                 uint16 = 0x5A // kVK_F20 in HIToolbox's Events.h
)

func bindProbe() (*probe, error) {
	p := &probe{}
	l := dlopen(pathCoreGraphics)
	l.fn(&p.sourceCreate, "CGEventSourceCreate")
	l.fn(&p.keyboardEvent, "CGEventCreateKeyboardEvent")
	l.fn(&p.postEvent, "CGEventPost")
	l.fn(&p.counter, "CGEventSourceCounterForEventType")
	l.fn(&p.secondsSinceLast, "CGEventSourceSecondsSinceLastEventType")
	if l.err != nil {
		return nil, l.err
	}
	// Both arrived in macOS 10.15; a missing one leaves its field nil.
	(&lib{h: l.h}).fn(&p.preflightPost, "CGPreflightPostEventAccess")
	(&lib{h: dlopen(pathIOKit).h}).fn(&p.hidCheckAccess, "IOHIDCheckAccess")
	return p, nil
}

// hidAccess names IOHIDCheckAccess's answer for kIOHIDRequestTypeListenEvent.
func (p *probe) hidAccess() string {
	if p.hidCheckAccess == nil {
		return "unavailable"
	}
	const listenEvent = 1 // kIOHIDRequestTypeListenEvent
	switch a := p.hidCheckAccess(listenEvent); a {
	case 0:
		return "granted"
	case 1:
		return "denied"
	case 2:
		return "unknown"
	default:
		return fmt.Sprintf("IOHIDAccessType %d", a)
	}
}

func (p *probe) read() idleReading {
	r := idleReading{
		KeysHIDS:      finite(p.secondsSinceLast(cgStateHIDSystem, cgKeyDown)),
		AnyHIDS:       finite(p.secondsSinceLast(cgStateHIDSystem, cgAnyInput)),
		KeysCombinedS: finite(p.secondsSinceLast(cgStateCombinedSession, cgKeyDown)),
		AnyCombinedS:  finite(p.secondsSinceLast(cgStateCombinedSession, cgAnyInput)),
		KeyDowns:      p.counter(cgStateHIDSystem, cgKeyDown),
	}
	if ns, ok := (macPresence{}).hidIdle(); ok {
		r.HIDIdleS = finite(float64(ns) / 1e9)
	}
	return r
}

// post sends one F20 key-down and key-up into the HID system state and
// waits up to two seconds for any-input idle to notice.
func (p *probe) post() keyProbe {
	kp := keyProbe{Key: "F20 (kVK_F20, 0x5A), down then up, from a kCGEventSourceStateHIDSystemState source to kCGHIDEventTap"}
	start := p.read()
	src := p.sourceCreate(cgStateHIDSystem)
	if src == 0 {
		kp.Error = "CGEventSourceCreate returned NULL"
		kp.After = p.read()
		return kp
	}
	defer cf.release(src)
	for _, down := range []bool{true, false} {
		ev := p.keyboardEvent(src, kVKF20, down)
		if ev == 0 {
			kp.Error = "CGEventCreateKeyboardEvent returned NULL"
			kp.After = p.read()
			return kp
		}
		p.postEvent(cgHIDEventTap, ev)
		cf.release(ev)
	}
	kp.Attempted = true
	deadline := time.Now().Add(2 * time.Second)
	for {
		kp.After = p.read()
		if below(kp.After.AnyHIDS, 1) || time.Now().After(deadline) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	kp.AnyReset = below(kp.After.AnyHIDS, 1)
	kp.KeysReset = below(kp.After.KeysHIDS, 1)
	kp.CountRose = kp.After.KeyDowns > start.KeyDowns
	return kp
}

func finite(v float64) *float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return nil
	}
	return &v
}

func below(v *float64, limit float64) bool { return v != nil && *v < limit }

func sysctl(name string) string {
	s, err := unix.Sysctl(name)
	if err != nil {
		return "unknown: " + err.Error()
	}
	return s
}

// uptime is the seconds since boot. A key-down idle time close to it means
// no key since boot.
func uptime() *float64 {
	tv, err := unix.SysctlTimeval("kern.boottime")
	if err != nil {
		return nil
	}
	boot := time.Unix(tv.Sec, int64(tv.Usec)*1000)
	return finite(time.Since(boot).Seconds())
}
