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
// action, and reads again. It writes
// macos-key-down-idle-input-monitoring-<granted|denied|unknown>.json to
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
	// platform.yml runs this twice, the second time after revoking Input
	// Monitoring, so the file says which state it saw.
	state := "unknown"
	if f.InputMonitoring != nil {
		state = map[bool]string{true: "granted", false: "denied"}[*f.InputMonitoring]
	}
	if !writeFinding(t, "macos-key-down-idle-input-monitoring-"+state+".json", f) {
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
	// HIDIdleReset says IOKit's HIDIdleTime, the fallback, saw it.
	HIDIdleReset bool `json:"hid_idle_time_reset"`
}

// conclude turns the readings into the one-line answer Karim reads first.
func (f keyFinding) conclude() (answer, detail string) {
	keys := "null"
	if v := f.Probe.After.KeysHIDS; v != nil {
		keys = fmt.Sprintf("%.3f s", *v)
	}
	switch {
	case f.InputMonitoring != nil && *f.InputMonitoring:
		saw := "didn't see it"
		if f.Probe.KeysReset {
			saw = "dropped to " + keys
		}
		return "inconclusive", "the test process has Input Monitoring here, so this run can't show what the call returns without it. With it, key-down idle " + saw +
			" after a posted key-down. The run without the permission, if platform.yml could revoke it, is in the -denied file"
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
	kp.HIDIdleReset = below(kp.After.HIDIdleS, 1)
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

// TestFindingRunLoop records two facts about where macOS delivers what a
// Loop listens to, for whoever wires the Loop into the agent: whether a
// notification posted to NSWorkspace's notification center reaches the
// observer's method, which says whether the center can be tested by
// posting to it, and whether distributed notifications reach a Loop that
// isn't on the main thread. It writes macos-run-loop.json and fails only
// when it can't.
func TestFindingRunLoop(t *testing.T) {
	if os.Getenv(findingsEnv) == "" {
		t.Skipf("%s isn't set; platform.yml sets it on macos-latest", findingsEnv)
	}
	if err := loadTestObjC(); err != nil {
		t.Fatal(err)
	}
	f := runLoopFinding{MacOS: sysctl("kern.osproductversion")}
	t.Run("a post to NSWorkspace's notification center", func(t *testing.T) {
		_, b, _ := startMacLoop(t, true, nil)
		withPool(func() {
			ws := objc.send(objc.clsNSWorkspace, objc.selSharedWorkspace)
			f.CenterClass = testObjC.className(objc.send(ws, objc.selNotificationCenter))
		})
		before := b.activations.Load()
		postActivation(t)
		f.PostReachedObserver = eventually(2*time.Second, func() bool { return b.activations.Load() > before })
	})
	t.Run("a distributed notification to a Loop off the main thread", func(t *testing.T) {
		l, _, rec := startMacLoop(t, false, func(b *macBackend) { b.lockNames = testLockNames() })
		l.seedLock(false)
		postDistributed(testLockNames()[0])
		select {
		case <-rec.power:
			f.DistributedOffMain = true
		case <-time.After(3 * time.Second):
		}
	})
	if !writeFinding(t, "macos-run-loop.json", f) {
		t.Fatalf("%s is set but nothing was written", findingsEnv)
	}
}

type runLoopFinding struct {
	MacOS string `json:"macos_version"`
	// CenterClass is the class of [[NSWorkspace sharedWorkspace]
	// notificationCenter].
	CenterClass string `json:"workspace_notification_center_class"`
	// PostReachedObserver is whether posting an activation notification to
	// that center called the observer's method within 2 s, with the Loop on
	// the main thread.
	PostReachedObserver bool `json:"post_to_workspace_center_reached_observer"`
	// DistributedOffMain is whether a distributed notification reached a
	// Loop running off the main thread within 3 s, while the main thread ran
	// no run loop.
	DistributedOffMain bool `json:"distributed_notification_reached_loop_off_main_thread"`
}

// eventually polls cond every 20 ms until it holds or d has passed.
func eventually(d time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(d)
	for !cond() {
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(20 * time.Millisecond)
	}
	return true
}
