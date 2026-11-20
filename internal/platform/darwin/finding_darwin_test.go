// SPDX-License-Identifier: AGPL-3.0-or-later

package darwin

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// TestFindingKeyDownIdle answers WP-OS3's open question: does
// CGEventSourceSecondsSinceLastEventType with kCGEventKeyDown need the
// Input Monitoring permission (SM 1.2, DESIGN 5.7)? Readings on an idle
// runner can't tell, since nobody has pressed a key, so the test posts one
// key-down and key-up of F20, a key with no default action, and reads
// again. It does that twice:
//
//   - in this process, which macos-latest grants Input Monitoring through
//     the runner's own processes, a grant no TCC database row holds and the
//     job can't take back;
//   - in a child that is its own responsible process, spawned with
//     responsibility_spawnattrs_setdisclaim as Chromium starts its helpers,
//     so TCC judges the child alone, as it will judge a pairtime agent that
//     its LaunchAgent starts. This process posts the key for it.
//
// It writes macos-key-down-idle.json to $PAIRTIME_FINDINGS_DIR, which
// platform.yml uploads as an artifact, and fails only when it can't write
// the file. It never asks for a permission. Posting events is a test-only
// tool: the client never posts, taps or listens to input (DESIGN 7.2).
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

	f.Before = p.quiet()
	f.Probe = p.post(f.Before)
	f.OwnProcess = p.ownProcessRun(t)
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
	// CGPreflightPostEventAccess, which posting the key needs.
	InputMonitoring   *bool  `json:"input_monitoring_granted"`
	IOHIDListenAccess string `json:"iohid_listen_access"`
	PostEventAccess   *bool  `json:"post_event_granted"`
	SessionDictionary bool   `json:"session_dictionary"`

	Presence      any    `json:"presence_sample,omitempty"`
	PresenceError string `json:"presence_error,omitempty"`

	// Before and Probe are this process's run, OwnProcess the child's.
	Before     idleReading `json:"before"`
	Probe      keyProbe    `json:"probe"`
	OwnProcess *ownRun     `json:"own_process_run"`
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

// resets says which idle values a posted key moved, comparing a reading
// from before it with one after. A value counts as moved when it was at
// least quietS before and is under a second after, so input that came just
// before the post can't pass for it.
type resets struct {
	AnyReset     bool `json:"any_input_idle_reset"`
	KeysReset    bool `json:"key_down_idle_reset"`
	CountRose    bool `json:"key_down_count_rose"`
	HIDIdleReset bool `json:"hid_idle_time_reset"`
}

// quietS is how long input has to have been idle before a post.
const quietS = 1.5

func compare(before, after idleReading) resets {
	moved := func(b, a *float64) bool { return b != nil && *b >= quietS && below(a, 1) }
	return resets{
		AnyReset:     moved(before.AnyHIDS, after.AnyHIDS),
		KeysReset:    moved(before.KeysHIDS, after.KeysHIDS),
		CountRose:    after.KeyDowns > before.KeyDowns,
		HIDIdleReset: moved(before.HIDIdleS, after.HIDIdleS),
	}
}

type keyProbe struct {
	Key       string      `json:"key"`
	Attempted bool        `json:"attempted"`
	Error     string      `json:"error,omitempty"`
	After     idleReading `json:"after"`
	resets
}

// ownRun is the run in a child that is its own responsible process.
type ownRun struct {
	How    string        `json:"how"`
	Error  string        `json:"error,omitempty"`
	Reader *readerResult `json:"reader,omitempty"`
	resets
}

// conclude turns the readings into the one-line answer Karim reads first.
// A process without Input Monitoring answers the question; one with it
// can only say what the call does with the permission.
func (f keyFinding) conclude() (answer, detail string) {
	if o := f.OwnProcess; o != nil && o.Reader != nil && isFalse(o.Reader.InputMonitoring) {
		return verdict("A child that is its own responsible process, and so has no Input Monitoring,", o.resets, o.Reader.After.KeysHIDS)
	}
	if isFalse(f.InputMonitoring) && f.Probe.Attempted {
		return verdict("The test process, which has no Input Monitoring,", f.Probe.resets, f.Probe.After.KeysHIDS)
	}
	child := "wasn't run"
	if o := f.OwnProcess; o != nil {
		switch {
		case o.Error != "":
			child = "failed: " + o.Error
		case o.Reader == nil || o.Reader.InputMonitoring == nil:
			child = "couldn't tell whether it had the permission"
		default:
			child = "had the permission as well"
		}
	}
	with := "didn't move"
	if f.Probe.KeysReset {
		with = "dropped to " + secs(f.Probe.After.KeysHIDS)
	}
	return "inconclusive", "the test process has Input Monitoring on this runner, and the child meant to run without it " + child +
		". With the permission, key-down idle " + with + " after a posted key-down"
}

// verdict answers the question from what a process without Input
// Monitoring saw after a posted key.
func verdict(who string, r resets, keysAfter *float64) (answer, detail string) {
	switch {
	case !r.AnyReset:
		return "inconclusive", who + " didn't see the posted key-down at all: any-input idle didn't drop"
	case r.KeysReset:
		return "no permission needed", who + " saw key-down idle drop to " + secs(keysAfter) + " after a posted key-down, as any-input idle did"
	}
	return "permission needed, or posted keys don't count", who + " saw any-input idle drop after a posted key-down, but key-down idle stayed at " + secs(keysAfter) +
		". The call needs the permission, or it counts only hardware keys; one hardware key press on a Mac without the permission tells the two apart"
}

func isFalse(b *bool) bool { return b != nil && !*b }

func secs(v *float64) string {
	if v == nil {
		return "null"
	}
	return fmt.Sprintf("%.3f s", *v)
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

// quiet waits up to 10 s for any-input idle to reach quietS, so a post that
// follows can be told from input before it, and returns the last reading.
func (p *probe) quiet() idleReading {
	var r idleReading
	eventually(10*time.Second, func() bool {
		r = p.read()
		return r.AnyHIDS != nil && *r.AnyHIDS >= quietS
	})
	return r
}

// postKey posts one F20 key-down and key-up from a HID-state source to the
// HID event tap.
func (p *probe) postKey() error {
	src := p.sourceCreate(cgStateHIDSystem)
	if src == 0 {
		return errors.New("CGEventSourceCreate returned NULL")
	}
	defer cf.release(src)
	for _, down := range []bool{true, false} {
		ev := p.keyboardEvent(src, kVKF20, down)
		if ev == 0 {
			return errors.New("CGEventCreateKeyboardEvent returned NULL")
		}
		p.postEvent(cgHIDEventTap, ev)
		cf.release(ev)
	}
	return nil
}

// settle reads until any-input idle is under a second, for up to 2 s.
func (p *probe) settle() idleReading {
	var r idleReading
	eventually(2*time.Second, func() bool {
		r = p.read()
		return below(r.AnyHIDS, 1)
	})
	return r
}

// post posts the key in this process and reads what changed since before.
func (p *probe) post(before idleReading) keyProbe {
	kp := keyProbe{Key: "F20 (kVK_F20, 0x5A), down then up, from a kCGEventSourceStateHIDSystemState source to kCGHIDEventTap"}
	if err := p.postKey(); err != nil {
		kp.Error = err.Error()
		kp.After = p.read()
		return kp
	}
	kp.Attempted = true
	kp.After = p.settle()
	kp.resets = compare(before, kp.After)
	return kp
}

// readerEnv, set to a directory, makes the test binary the child half of
// the run without Input Monitoring: TestKeyIdleReader takes a reading,
// waits for the parent to post the key, reads again, and leaves both in
// the directory.
const readerEnv = "PAIRTIME_KEY_READER_DIR"

// readerResult is what the child leaves in result.json.
type readerResult struct {
	InputMonitoring   *bool       `json:"input_monitoring_granted"`
	IOHIDListenAccess string      `json:"iohid_listen_access"`
	Before            idleReading `json:"before"`
	After             idleReading `json:"after"`
	Error             string      `json:"error,omitempty"`
}

// TestKeyIdleReader is the child half. It exits from inside the test, with
// status 3 and before the testing package prints PASS, because the parent
// reads result.json rather than the child's output or status.
func TestKeyIdleReader(t *testing.T) {
	dir := os.Getenv(readerEnv)
	if dir == "" {
		t.Skipf("runs only as TestFindingKeyDownIdle's child, with %s set", readerEnv)
	}
	var r readerResult
	defer func() {
		if err := writeJSONFile(filepath.Join(dir, "result.json"), r); err != nil {
			fmt.Fprintln(os.Stderr, err)
		}
		os.Exit(3)
	}()
	if err := readKeyIdle(dir, &r); err != nil {
		r.Error = err.Error()
	}
}

func readKeyIdle(dir string, r *readerResult) error {
	if err := loadCG(); err != nil {
		return err
	}
	if err := loadIOKit(); err != nil {
		return err
	}
	p, err := bindProbe()
	if err != nil {
		return err
	}
	if granted, known := listenAccess(); known {
		r.InputMonitoring = &granted
	}
	r.IOHIDListenAccess = p.hidAccess()
	r.Before = p.quiet()
	if err := writeJSONFile(filepath.Join(dir, "ready.json"), r); err != nil {
		return err
	}
	if !eventually(20*time.Second, exists(filepath.Join(dir, "posted"))) {
		return errors.New("the parent never posted the key")
	}
	r.After = p.settle()
	return nil
}

// ownProcessRun starts this test binary again as TestKeyIdleReader, as its
// own responsible process, posts the key once the child has its first
// reading, and returns what the child saw.
func (p *probe) ownProcessRun(t *testing.T) *ownRun {
	run := &ownRun{How: "this test binary started again as TestKeyIdleReader with responsibility_spawnattrs_setdisclaim, so TCC judges it as its own responsible process; the parent posted the key"}
	dir := t.TempDir()
	logName := filepath.Join(dir, "reader.log")
	fail := func(msg string) *ownRun {
		b, _ := os.ReadFile(logName)
		if len(b) > 2000 {
			b = b[len(b)-2000:]
		}
		run.Error = msg
		if len(b) > 0 {
			run.Error += "; the child printed: " + string(b)
		}
		return run
	}
	exe, err := os.Executable()
	if err != nil {
		return fail(err.Error())
	}
	pid, err := spawnDisclaimed(exe, []string{exe, "-test.run=^TestKeyIdleReader$", "-test.count=1"}, append(os.Environ(), readerEnv+"="+dir), logName)
	if err != nil {
		return fail(err.Error())
	}
	exited := make(chan struct{})
	go func() {
		var ws unix.WaitStatus
		unix.Wait4(int(pid), &ws, 0, nil)
		close(exited)
	}()
	defer func() {
		select {
		case <-exited:
		case <-time.After(5 * time.Second):
			unix.Kill(int(pid), unix.SIGKILL)
			<-exited
		}
	}()
	if !eventually(30*time.Second, exists(filepath.Join(dir, "ready.json"))) {
		return fail("the child never took its first reading")
	}
	if err := p.postKey(); err != nil {
		return fail(err.Error())
	}
	if err := os.WriteFile(filepath.Join(dir, "posted"), nil, 0o600); err != nil {
		return fail(err.Error())
	}
	result := filepath.Join(dir, "result.json")
	if !eventually(30*time.Second, exists(result)) {
		return fail("the child wrote no result")
	}
	b, err := os.ReadFile(result)
	if err != nil {
		return fail(err.Error())
	}
	var r readerResult
	if err := json.Unmarshal(b, &r); err != nil {
		return fail(err.Error())
	}
	run.Reader = &r
	if r.Error != "" {
		return fail(r.Error)
	}
	run.resets = compare(r.Before, r.After)
	return run
}

func exists(name string) func() bool {
	return func() bool {
		_, err := os.Stat(name)
		return err == nil
	}
}

// writeJSONFile writes v to name by atomic rename, so a reader polling for
// name never sees half of it.
func writeJSONFile(name string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	tmp := name + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, name)
}

// spawn binds posix_spawn and responsibility_spawnattrs_setdisclaim, the
// libSystem call Chromium uses (base/process/launch_mac.cc) so that a child
// answers to TCC for itself instead of for the process that started it.
var spawn struct {
	once sync.Once
	err  error

	attrInit       func(attr *uintptr) int32
	attrDestroy    func(attr *uintptr) int32
	setDisclaim    func(attr *uintptr, disclaim int32) int32
	actionsInit    func(fa *uintptr) int32
	actionsDestroy func(fa *uintptr) int32
	addOpen        func(fa *uintptr, fd int32, path string, oflag int32, mode uint16) int32
	addDup2        func(fa *uintptr, fd, newfd int32) int32
	posixSpawn     func(pid *int32, path string, fa *uintptr, attr *uintptr, argv, envp **byte) int32
}

func loadSpawn() error {
	spawn.once.Do(func() {
		l := dlopen(pathLibSystem)
		l.fn(&spawn.attrInit, "posix_spawnattr_init")
		l.fn(&spawn.attrDestroy, "posix_spawnattr_destroy")
		l.fn(&spawn.setDisclaim, "responsibility_spawnattrs_setdisclaim")
		l.fn(&spawn.actionsInit, "posix_spawn_file_actions_init")
		l.fn(&spawn.actionsDestroy, "posix_spawn_file_actions_destroy")
		l.fn(&spawn.addOpen, "posix_spawn_file_actions_addopen")
		l.fn(&spawn.addDup2, "posix_spawn_file_actions_adddup2")
		l.fn(&spawn.posixSpawn, "posix_spawn")
		spawn.err = l.err
	})
	return spawn.err
}

// spawnDisclaimed starts path with argv and env as its own responsible
// process, with its stdout and stderr in logName.
func spawnDisclaimed(path string, argv, env []string, logName string) (int32, error) {
	if err := loadSpawn(); err != nil {
		return 0, err
	}
	var attr, fa uintptr
	if e := spawn.attrInit(&attr); e != 0 {
		return 0, fmt.Errorf("posix_spawnattr_init: %w", unix.Errno(e))
	}
	defer spawn.attrDestroy(&attr)
	if e := spawn.setDisclaim(&attr, 1); e != 0 {
		return 0, fmt.Errorf("responsibility_spawnattrs_setdisclaim: %w", unix.Errno(e))
	}
	if e := spawn.actionsInit(&fa); e != 0 {
		return 0, fmt.Errorf("posix_spawn_file_actions_init: %w", unix.Errno(e))
	}
	defer spawn.actionsDestroy(&fa)
	if e := spawn.addOpen(&fa, 1, logName, int32(unix.O_WRONLY|unix.O_CREAT|unix.O_TRUNC), 0o600); e != 0 {
		return 0, fmt.Errorf("posix_spawn_file_actions_addopen: %w", unix.Errno(e))
	}
	if e := spawn.addDup2(&fa, 1, 2); e != 0 {
		return 0, fmt.Errorf("posix_spawn_file_actions_adddup2: %w", unix.Errno(e))
	}
	cargv, cenv := cStrings(argv), cStrings(env)
	var pid int32
	e := spawn.posixSpawn(&pid, path, &fa, &attr, &cargv[0], &cenv[0])
	runtime.KeepAlive(cargv)
	runtime.KeepAlive(cenv)
	if e != 0 {
		return 0, fmt.Errorf("posix_spawn: %w", unix.Errno(e))
	}
	return pid, nil
}

// cStrings returns NUL-terminated copies of ss as a NULL-terminated array
// of pointers, the shape of argv and envp.
func cStrings(ss []string) []*byte {
	ptrs := make([]*byte, 0, len(ss)+1)
	for _, s := range ss {
		b := append([]byte(s), 0)
		ptrs = append(ptrs, &b[0])
	}
	return append(ptrs, nil)
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
		names := testLockNames("offmain")
		l, _, rec := startMacLoop(t, false, func(b *macBackend) { b.lockNames = names })
		l.seedLock(false)
		postDistributed(names[0])
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
