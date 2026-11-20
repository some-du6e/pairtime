// SPDX-License-Identifier: AGPL-3.0-or-later

package notifier

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/agent/config"
	"github.com/some-du6e/pairtime/internal/agent/state"
	"github.com/some-du6e/pairtime/pkg/params"
)

// The agent hands the notifier its state.json.
var _ Memory = (*state.Store)(nil)

// fakeBackend records Show calls and answers each with the next error in
// errs, then nil.
type fakeBackend struct {
	mu      sync.Mutex
	errs    []error
	calls   []Message
	ctxs    []context.Context // each call's
	block   chan struct{}     // when set, Show waits for it to close, or for ctx
	entered chan struct{}     // when set, Show sends on it as it starts
}

func (f *fakeBackend) Show(ctx context.Context, m Message, _ func()) error {
	f.mu.Lock()
	f.calls = append(f.calls, m)
	f.ctxs = append(f.ctxs, ctx)
	var err error
	if len(f.errs) > 0 {
		err, f.errs = f.errs[0], f.errs[1:]
	}
	block, entered := f.block, f.entered
	f.mu.Unlock()
	if entered != nil {
		entered <- struct{}{}
	}
	if block != nil {
		select {
		case <-block:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return err
}

func (f *fakeBackend) shown() []Message {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Message(nil), f.calls...)
}

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) Set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = t
}

// recorder keeps what Options.Result and Options.Problem hear.
type recorder struct {
	mu       sync.Mutex
	results  []string // "session: outcome"
	problems []string
}

func (r *recorder) result(n Note, o Outcome) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.results = append(r.results, n.Session+": "+o.String())
}

func (r *recorder) problem(msg string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.problems = append(r.problems, msg)
}

func (r *recorder) got() ([]string, []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.results...), append([]string(nil), r.problems...)
}

// day is the test day; times in steps are clock times on it, in UTC.
func at(hhmmss string) time.Time {
	t, err := time.Parse("2006-01-02 15:04:05", "2026-11-19 "+hhmmss)
	if err != nil {
		panic(err)
	}
	return t
}

// mustQuiet parses quiet hours for a test table.
func mustQuiet(s string) config.QuietHours {
	q, err := config.ParseQuietHours(s)
	if err != nil {
		panic(err)
	}
	return q
}

// step is one thing that happens, at a clock time.
type step struct {
	at       string                // the clock time; "" means 20:00:00 plus after
	after    time.Duration         // with no at, how long after 20:00:00
	settings *config.Notifications // when set, the settings from now on
	notify   string                // Notify about this session ("" with present: nothing)
	ago      time.Duration         // how long before the clock time the session ended
	text     string
	want     Outcome // what Notify returns
	present  bool    // call UserPresent instead of Notify
}

// rig is a notifier with fakes around it.
type rig struct {
	n       *Notifier
	backend *fakeBackend
	clock   *fakeClock
	rec     *recorder

	mu       sync.Mutex
	settings config.Notifications
}

func newRig(settings config.Notifications, errs []error, memory Memory) *rig {
	r := &rig{
		backend:  &fakeBackend{errs: errs},
		clock:    &fakeClock{t: at("20:00:00")},
		rec:      &recorder{},
		settings: settings,
	}
	r.n = New(Options{
		Backend: r.backend,
		Settings: func() config.Notifications {
			r.mu.Lock()
			defer r.mu.Unlock()
			return r.settings
		},
		Memory:  memory,
		Now:     r.clock.Now,
		Problem: r.rec.problem,
		Result:  r.rec.result,
	})
	return r
}

// run plays steps, waiting after each until the notifier is idle.
func (r *rig) run(t *testing.T, steps []step) {
	t.Helper()
	for i, s := range steps {
		when := at("20:00:00").Add(s.after)
		if s.at != "" {
			when = at(s.at)
		}
		r.clock.Set(when)
		if s.settings != nil {
			r.mu.Lock()
			r.settings = *s.settings
			r.mu.Unlock()
		}
		if s.present {
			r.n.UserPresent()
		} else {
			note := Note{Session: s.notify, Ended: r.clock.Now().Add(-s.ago), Text: s.text}
			if got := r.n.Notify(note); got != s.want {
				t.Errorf("step %d at %s: Notify(%q) = %v, want %v", i, when.Format("15:04:05"), s.notify, got, s.want)
			}
		}
		r.n.wg.Wait()
	}
}

func TestNotifyRules(t *testing.T) {
	off := config.Notifications{Off: true}
	tests := []struct {
		name         string
		settings     config.Notifications
		errs         []error // what Show returns, call by call, then nil
		steps        []step
		wantResults  []string
		wantCalls    int      // Show calls
		wantProblems []string // substrings, in order; "" means a cleared problem
	}{
		{
			name:        "a session's first note is shown",
			steps:       []step{{at: "20:00:00", notify: "s1", text: "52 min on remind-bot. See recap.", want: Queued}},
			wantResults: []string{"s1: shown"},
			wantCalls:   1,
			// A notification that was shown clears any problem doctor had.
			wantProblems: []string{""},
		},
		{
			name: "a second note about the same session is never shown",
			steps: []step{
				{at: "20:00:00", notify: "s1", want: Queued},
				{at: "20:30:00", notify: "s1", want: Repeated},
			},
			wantResults:  []string{"s1: shown", "s1: already decided for this session"},
			wantCalls:    1,
			wantProblems: []string{""},
		},
		{
			name:     "a session dropped for quiet hours isn't shown later either",
			settings: config.Notifications{Quiet: mustQuiet("19:00-20:30")},
			steps: []step{
				{at: "20:00:00", notify: "s1", want: InQuietHours},
				{at: "20:30:00", notify: "s1", want: Repeated},
			},
			wantResults: []string{"s1: quiet hours", "s1: already decided for this session"},
		},
		{
			name:        "a note without a session is refused",
			steps:       []step{{at: "20:00:00", notify: "", want: Repeated}},
			wantResults: []string{": already decided for this session"},
		},
		{
			name:        "nothing is shown while notifications are off",
			settings:    off,
			steps:       []step{{at: "20:00:00", notify: "s1", want: TurnedOff}},
			wantResults: []string{"s1: notifications are off"},
		},
		{
			name: "turning notifications off applies to the very next note",
			steps: []step{
				{at: "20:00:00", notify: "s1", want: Queued},
				{at: "21:00:00", settings: &off, notify: "s2", want: TurnedOff},
			},
			wantResults:  []string{"s1: shown", "s2: notifications are off"},
			wantCalls:    1,
			wantProblems: []string{""},
		},
		{
			name:        "nothing is shown during quiet hours",
			settings:    config.Notifications{Quiet: mustQuiet("19:00-21:00")},
			steps:       []step{{at: "20:00:00", notify: "s1", want: InQuietHours}},
			wantResults: []string{"s1: quiet hours"},
		},
		{
			name:        "quiet hours that cross midnight cover the small hours",
			settings:    config.Notifications{Quiet: mustQuiet("22:00-07:00")},
			steps:       []step{{at: "02:00:00", notify: "s1", want: InQuietHours}},
			wantResults: []string{"s1: quiet hours"},
		},
		{
			name:         "a note at the minute quiet hours end is shown",
			settings:     config.Notifications{Quiet: mustQuiet("19:00-21:00")},
			steps:        []step{{at: "21:00:00", notify: "s1", want: Queued}},
			wantResults:  []string{"s1: shown"},
			wantCalls:    1,
			wantProblems: []string{""},
		},
		{
			name:        "a session that ended more than SESSION_GAP_S ago is stale",
			steps:       []step{{at: "20:00:00", notify: "s1", ago: Fresh() + time.Second, want: Stale}},
			wantResults: []string{"s1: the session ended too long ago"},
		},
		{
			name:         "a session that ended exactly SESSION_GAP_S ago is still shown",
			steps:        []step{{at: "20:00:00", notify: "s1", ago: Fresh(), want: Queued}},
			wantResults:  []string{"s1: shown"},
			wantCalls:    1,
			wantProblems: []string{""},
		},
		{
			name: "a note within SESSION_GAP_S of the last one shown is dropped",
			steps: []step{
				{at: "20:00:00", notify: "s1", want: Queued},
				{after: Fresh() - time.Second, notify: "s2", want: TooSoon},
			},
			wantResults:  []string{"s1: shown", "s2: too soon after the last notification"},
			wantCalls:    1,
			wantProblems: []string{""},
		},
		{
			name: "a note SESSION_GAP_S after the last one shown is shown",
			steps: []step{
				{at: "20:00:00", notify: "s1", want: Queued},
				{after: Fresh(), notify: "s2", want: Queued},
			},
			wantResults:  []string{"s1: shown", "s2: shown"},
			wantCalls:    2,
			wantProblems: []string{"", ""},
		},
		{
			name: "a note that finds the screen locked waits, and is shown when the user is back",
			errs: []error{ErrAway},
			steps: []step{
				{at: "20:00:00", notify: "s1", want: Queued},
				{at: "20:05:00", present: true},
			},
			wantResults:  []string{"s1: shown"},
			wantCalls:    2,
			wantProblems: []string{""},
		},
		{
			name: "a note that waited past SESSION_GAP_S is stale when the user is back",
			errs: []error{ErrAway},
			steps: []step{
				{at: "20:00:00", notify: "s1", want: Queued},
				{after: Fresh() + time.Second, present: true},
			},
			wantResults: []string{"s1: the session ended too long ago"},
			wantCalls:   1,
		},
		{
			name: "a waiting note isn't shown once quiet hours have begun",
			errs: []error{ErrAway},
			steps: []step{
				{at: "20:00:00", notify: "s1", want: Queued},
				{at: "20:15:00", settings: &config.Notifications{Quiet: mustQuiet("20:10-07:00")}, present: true},
			},
			wantResults: []string{"s1: quiet hours"},
			wantCalls:   1,
		},
		{
			name: "a newer note takes a waiting note's place and asks the OS afresh",
			errs: []error{ErrAway},
			steps: []step{
				{at: "20:00:00", notify: "s1", want: Queued},
				{at: "20:30:00", notify: "s2", want: Queued},
			},
			wantResults:  []string{"s1: replaced by a newer note", "s2: shown"},
			wantCalls:    2,
			wantProblems: []string{""},
		},
		{
			name: "a user who is back with nothing waiting changes nothing",
			steps: []step{
				{at: "20:00:00", present: true},
			},
		},
		{
			name: "a desktop that can't notify drops the note and tells doctor, until one is shown",
			errs: []error{fmt.Errorf("%w: no session D-Bus", ErrUnavailable)},
			steps: []step{
				{at: "20:00:00", notify: "s1", want: Queued},
				{at: "20:30:00", notify: "s2", want: Queued},
			},
			wantResults:  []string{"s1: this desktop can't show notifications", "s2: shown"},
			wantCalls:    2,
			wantProblems: []string{"can't be shown on this desktop: notifier: this desktop can't show notifications: no session D-Bus", ""},
		},
		{
			name:        "a desktop that is busy drops the note, and that is no problem",
			errs:        []error{ErrBusy},
			steps:       []step{{at: "20:00:00", notify: "s1", want: Queued}},
			wantResults: []string{"s1: the OS isn't showing notifications now"},
			wantCalls:   1,
		},
		{
			name:         "a Show that fails for another reason is reported to doctor",
			errs:         []error{errors.New("boom")},
			steps:        []step{{at: "20:00:00", notify: "s1", want: Queued}},
			wantResults:  []string{"s1: failed"},
			wantCalls:    1,
			wantProblems: []string{"the last notification failed: boom"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newRig(tt.settings, tt.errs, nil)
			defer r.n.Close()
			r.run(t, tt.steps)
			results, problems := r.rec.got()
			if strings.Join(results, "\n") != strings.Join(tt.wantResults, "\n") {
				t.Errorf("results:\n%s\nwant:\n%s", strings.Join(results, "\n"), strings.Join(tt.wantResults, "\n"))
			}
			if calls := len(r.backend.shown()); calls != tt.wantCalls {
				t.Errorf("Show was called %d times, want %d", calls, tt.wantCalls)
			}
			if len(problems) != len(tt.wantProblems) {
				t.Fatalf("problems = %q, want %d matching %q", problems, len(tt.wantProblems), tt.wantProblems)
			}
			for i, want := range tt.wantProblems {
				if (want == "") != (problems[i] == "") || !strings.Contains(problems[i], want) {
					t.Errorf("problem %d = %q, want %q", i, problems[i], want)
				}
			}
		})
	}
}

// Quiet hours are clock times in the zone of the clock's times, which
// localtime.Now reads afresh on each call. So a laptop that flew from Los
// Angeles to Tokyo keeps quiet hours by Tokyo's clock.
func TestQuietHoursFollowTheClocksZone(t *testing.T) {
	instant := at("20:00:00") // 20:00 UTC
	tests := []struct {
		name string
		zone *time.Location
		want Outcome
	}{
		{name: "05:00 in Tokyo is inside quiet hours of 22:00-07:00", zone: time.FixedZone("JST", 9*3600), want: InQuietHours},
		{name: "the same instant, 12:00 in Los Angeles, is outside them", zone: time.FixedZone("PST", -8*3600), want: Queued},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n := New(Options{
				Backend:  &fakeBackend{},
				Settings: func() config.Notifications { return config.Notifications{Quiet: mustQuiet("22:00-07:00")} },
				Now:      func() time.Time { return instant.In(tt.zone) },
			})
			defer n.Close()
			if got := n.Notify(Note{Session: "s1", Ended: instant}); got != tt.want {
				t.Errorf("Notify() at %s = %v, want %v", instant.In(tt.zone).Format("15:04 MST"), got, tt.want)
			}
			n.wg.Wait()
		})
	}
}

// Note.Ended is the moment a session was found to be over. For a session
// that ended in a gap, the coach (WP-CO1) knows it once SESSION_GAP_S has
// passed since the end of the last credited minute, at the scorer's next
// step, every 60 s (DESIGN 3.7).
func TestGapEndedSession(t *testing.T) {
	lastCredited := at("19:30:00") // the end of the session's last credited minute
	foundOver := lastCredited.Add(Fresh())
	scorerStep := time.Minute
	tests := []struct {
		name  string
		ended time.Time
		now   time.Time
		want  Outcome
	}{
		{
			name:  "a session found over SESSION_GAP_S after its last credited minute is shown at the scorer's next step",
			ended: foundOver, now: foundOver.Add(scorerStep), want: Queued,
		},
		{
			name:  "it is still shown when the coach reports it SESSION_GAP_S after it was found over",
			ended: foundOver, now: foundOver.Add(Fresh()), want: Queued,
		},
		{
			name:  "given SM 5.3's work-session end instead, the end of the last credited minute, the same note is already stale",
			ended: lastCredited, now: foundOver.Add(scorerStep), want: Stale,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newRig(config.Notifications{}, nil, nil)
			defer r.n.Close()
			r.clock.Set(tt.now)
			if got := r.n.Notify(Note{Session: "s1", Ended: tt.ended}); got != tt.want {
				t.Errorf("Notify() at %s for a session found over at %s = %v, want %v", tt.now.Format(time.TimeOnly), tt.ended.Format(time.TimeOnly), got, tt.want)
			}
			r.n.wg.Wait()
		})
	}
}

func TestEveryMessageHasTheFixedTitle(t *testing.T) {
	tests := []struct {
		name string
		text string
	}{
		{name: "a note with text", text: "52 min on remind-bot. You fixed the timezone bug yourself. See recap."},
		{name: "a note without text", text: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newRig(config.Notifications{}, nil, nil)
			defer r.n.Close()
			r.run(t, []step{{at: "20:00:00", notify: "s1", text: tt.text, want: Queued}})
			shown := r.backend.shown()
			if len(shown) != 1 {
				t.Fatalf("Show was called %d times, want 1", len(shown))
			}
			if shown[0].Title != Title {
				t.Errorf("title = %q, want %q, which names no project", shown[0].Title, Title)
			}
			if want := Compose(Note{Text: tt.text}).Body; shown[0].Body != want {
				t.Errorf("body = %q, want %q", shown[0].Body, want)
			}
		})
	}
}

func TestCompose(t *testing.T) {
	long := strings.Repeat("a", MaxText+50)
	tests := []struct {
		name string
		text string
		want string
	}{
		{name: "the coach's sentence is the body as written", text: "52 min on remind-bot. See recap.", want: "52 min on remind-bot. See recap."},
		{name: "no text gives a body that points to the dashboard", text: "", want: fallbackText},
		{name: "blank text counts as no text", text: " \n\t ", want: fallbackText},
		{name: "line breaks and runs of spaces become one space", text: "52 min\non  remind-bot.\r\n\tSee recap.", want: "52 min on remind-bot. See recap."},
		{name: "control characters are dropped", text: "remind\x00-bot\x1b[31m", want: "remind-bot[31m"},
		{name: "characters that flip text direction are dropped", text: "remind\u202e-bot\u2066", want: "remind-bot"},
		{name: "text longer than MaxText is cut and ends in an ellipsis", text: long, want: strings.Repeat("a", MaxText-1) + "…"},
		{name: "a cut doesn't leave a space before the ellipsis", text: strings.Repeat("a", MaxText-2) + " bbbbbb", want: strings.Repeat("a", MaxText-2) + "…"},
		{name: "text of exactly MaxText characters is kept whole", text: strings.Repeat("é", MaxText), want: strings.Repeat("é", MaxText)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := Compose(Note{Session: "s", Text: tt.text})
			if m.Title != Title {
				t.Errorf("Compose().Title = %q, want %q", m.Title, Title)
			}
			if m.Body != tt.want {
				t.Errorf("Compose().Body = %q, want %q", m.Body, tt.want)
			}
		})
	}
}

func TestNotifyNeverWaitsForTheOS(t *testing.T) {
	tests := []struct {
		name string
	}{
		{name: "Notify returns while Show is still running, and a note that comes meanwhile is judged when Show ends"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newRig(config.Notifications{}, nil, nil)
			defer r.n.Close()
			r.backend.block = make(chan struct{})
			r.backend.entered = make(chan struct{}, 1)

			start := time.Now()
			if got := r.n.Notify(Note{Session: "s1", Ended: r.clock.Now()}); got != Queued {
				t.Fatalf("Notify(s1) = %v, want %v", got, Queued)
			}
			if d := time.Since(start); d > time.Second {
				t.Errorf("Notify took %v with Show blocked", d)
			}
			<-r.backend.entered
			// s2 comes while s1 is on its way, so it waits for the result.
			if got := r.n.Notify(Note{Session: "s2", Ended: r.clock.Now()}); got != Waiting {
				t.Errorf("Notify(s2) = %v, want %v", got, Waiting)
			}
			close(r.backend.block)
			r.n.wg.Wait()
			results, _ := r.rec.got()
			// s1 was shown, so s2 is too soon after it.
			want := "s1: shown\ns2: too soon after the last notification"
			if got := strings.Join(results, "\n"); got != want {
				t.Errorf("results:\n%s\nwant:\n%s", got, want)
			}
		})
	}
}

// notifierGoroutines counts the goroutines with a frame in notifier.go: the
// notifiers' own workers, whatever else the test binary runs, such as
// another package's goroutines or the testing package's. The goroutine
// that calls it has frames in this test file only.
func notifierGoroutines() int {
	buf := make([]byte, 1<<16)
	for {
		n := runtime.Stack(buf, true)
		if n < len(buf) {
			buf = buf[:n]
			break
		}
		buf = make([]byte, 2*len(buf))
	}
	count := 0
	for _, g := range strings.Split(string(buf), "\n\n") {
		if strings.Contains(g, "internal/agent/notifier/notifier.go:") {
			count++
		}
	}
	return count
}

// DESIGN 3.3: never a polling loop. An idle notifier has no goroutine and
// no timer of its own, before its first note and after each one. Its only
// timers are its attempts' deadlines, and an attempt's context is done once
// the attempt is over, which stops its timer.
func TestIdleNotifierRunsNothing(t *testing.T) {
	tests := []struct {
		name  string
		errs  []error
		steps []step
	}{
		{name: "a new notifier starts no goroutine"},
		{name: "a notifier that showed a note keeps no goroutine and no timer", steps: []step{{at: "20:00:00", notify: "s1", want: Queued}}},
		{
			name:  "a notifier whose note waits for the screen keeps no goroutine and no timer",
			errs:  []error{ErrAway},
			steps: []step{{at: "20:00:00", notify: "s1", want: Queued}},
		},
		{
			name: "a notifier that asked the OS again for a user who came back keeps no goroutine and no timer",
			errs: []error{ErrAway},
			steps: []step{
				{at: "20:00:00", notify: "s1", want: Queued},
				{at: "20:05:00", present: true},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newRig(config.Notifications{}, tt.errs, nil)
			defer r.n.Close()
			r.run(t, tt.steps)
			// A goroutine that has returned may take a moment to leave the
			// stacks.
			deadline := time.Now().Add(2 * time.Second)
			for notifierGoroutines() > 0 && time.Now().Before(deadline) {
				time.Sleep(10 * time.Millisecond)
			}
			if got := notifierGoroutines(); got != 0 {
				t.Errorf("%d goroutines run notifier code once the notifier is idle, want none", got)
			}
			r.n.mu.Lock()
			pending := r.n.cancel != nil
			r.n.mu.Unlock()
			if pending {
				t.Error("the idle notifier still holds an attempt's deadline")
			}
			r.backend.mu.Lock()
			defer r.backend.mu.Unlock()
			for i, ctx := range r.backend.ctxs {
				if ctx.Err() == nil {
					t.Errorf("Show call %d's context isn't done, so its deadline's timer still runs", i)
				}
			}
		})
	}
}

func TestCloseStopsAShowInProgress(t *testing.T) {
	tests := []struct {
		name string
	}{
		{name: "Close cancels the Show on its way, and every later note is Stopped"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newRig(config.Notifications{}, nil, nil)
			r.backend.block = make(chan struct{}) // never closed: only ctx ends Show
			r.backend.entered = make(chan struct{}, 1)
			r.n.Notify(Note{Session: "s1", Ended: r.clock.Now()})
			<-r.backend.entered

			start := time.Now()
			r.n.Close()
			if d := time.Since(start); d >= closeWait {
				t.Errorf("Close took %v, so Show wasn't cancelled", d)
			}
			if got := r.n.Notify(Note{Session: "s2", Ended: r.clock.Now()}); got != Stopped {
				t.Errorf("Notify after Close = %v, want %v", got, Stopped)
			}
			results, _ := r.rec.got()
			want := "s1: the agent stopped\ns2: the agent stopped"
			if got := strings.Join(results, "\n"); got != want {
				t.Errorf("results:\n%s\nwant:\n%s", got, want)
			}
		})
	}
}

func TestMemoryAcrossRestarts(t *testing.T) {
	tests := []struct {
		name      string
		lastShown string // what state.json says, as a clock time
		steps     []step
		want      []string
	}{
		{
			name:      "a session shown before a restart isn't shown again after it",
			lastShown: "19:00:00",
			steps:     []step{{at: "20:00:00", notify: "seen", want: Repeated}},
			want:      []string{"seen: already decided for this session"},
		},
		{
			name:      "the gap after the last notification holds across a restart",
			lastShown: "19:50:00",
			steps:     []step{{at: "20:00:00", notify: "s2", want: TooSoon}},
			want:      []string{"s2: too soon after the last notification"},
		},
		{
			name:      "a last notification in the future, after the clock was set back, holds nothing back",
			lastShown: "23:00:00",
			steps:     []step{{at: "20:00:00", notify: "s2", want: Queued}},
			want:      []string{"s2: shown"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			name := filepath.Join(t.TempDir(), "state.json")
			st, _, err := state.Open(name)
			if err != nil {
				t.Fatal(err)
			}
			if err := st.Put(memoryKey, remembered{LastShown: at(tt.lastShown), Sessions: []decided{{Session: "seen"}}}); err != nil {
				t.Fatal(err)
			}
			r := newRig(config.Notifications{}, nil, st)
			r.run(t, tt.steps)
			r.n.Close()
			results, _ := r.rec.got()
			if got, want := strings.Join(results, "\n"), strings.Join(tt.want, "\n"); got != want {
				t.Errorf("results:\n%s\nwant:\n%s", got, want)
			}
		})
	}
}

func TestShownNotificationIsSavedForTheNextStart(t *testing.T) {
	tests := []struct {
		name string
	}{
		{name: "a notification that was shown is in state.json for the next agent, which won't show its session again"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			name := filepath.Join(t.TempDir(), "state.json")
			st, _, err := state.Open(name)
			if err != nil {
				t.Fatal(err)
			}
			first := newRig(config.Notifications{}, nil, st)
			first.run(t, []step{{at: "20:00:00", notify: "s1", want: Queued}})
			first.n.Close()
			if data, err := os.ReadFile(name); err != nil || !strings.Contains(string(data), `"notifier"`) {
				t.Fatalf("state.json = %s, %v, want the notifier's key in it", data, err)
			}

			reopened, _, err := state.Open(name)
			if err != nil {
				t.Fatal(err)
			}
			second := newRig(config.Notifications{}, nil, reopened)
			defer second.n.Close()
			second.run(t, []step{
				{at: "21:00:00", notify: "s1", want: Repeated},
				{at: "21:00:00", notify: "s2", want: Queued},
			})
			results, _ := second.rec.got()
			want := "s1: already decided for this session\ns2: shown"
			if got := strings.Join(results, "\n"); got != want {
				t.Errorf("results:\n%s\nwant:\n%s", got, want)
			}
		})
	}
}

// A decision is in state.json as soon as it is made, not only after a
// clean stop, so an agent that crashes, is killed or loses power doesn't
// show a session's note on its next start.
func TestDecisionsAreSavedWhenMade(t *testing.T) {
	tests := []struct {
		name      string
		settings  config.Notifications
		lastShown string // what state.json says before the first start, as a clock time; "" for nothing
		ago       time.Duration
		want      Outcome
	}{
		{name: "a session dropped while notifications are off", settings: config.Notifications{Off: true}, want: TurnedOff},
		{name: "a session dropped for quiet hours", settings: config.Notifications{Quiet: mustQuiet("19:00-21:00")}, want: InQuietHours},
		{name: "a session that ended too long ago", ago: Fresh() + time.Second, want: Stale},
		{name: "a session too soon after the last notification", lastShown: "19:50:00", want: TooSoon},
		{name: "a session whose note was shown", want: Queued},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			name := filepath.Join(t.TempDir(), "state.json")
			st, _, err := state.Open(name)
			if err != nil {
				t.Fatal(err)
			}
			if tt.lastShown != "" {
				if err := st.Put(memoryKey, remembered{LastShown: at(tt.lastShown)}); err != nil {
					t.Fatal(err)
				}
			}
			first := newRig(tt.settings, nil, st)
			defer first.n.Close()
			first.run(t, []step{{at: "20:00:00", notify: "s1", ago: tt.ago, want: tt.want}})

			// The first agent never stopped cleanly: the next one reads
			// what is on disk.
			onDisk, _, err := state.Open(name)
			if err != nil {
				t.Fatal(err)
			}
			next := newRig(config.Notifications{}, nil, onDisk)
			defer next.n.Close()
			next.run(t, []step{{at: "20:01:00", notify: "s1", want: Repeated}})
		})
	}
}

// stuckMemory is a Memory whose first Put waits for release, as a Put
// does behind a Save stuck on a slow disk, and that keeps every snapshot
// put into it, in the order the Puts finish.
type stuckMemory struct {
	entered chan struct{} // gets a value as the first Put starts
	release chan struct{} // closed to let the first Put finish

	mu      sync.Mutex
	started bool // the first Put has begun; later ones don't wait
	puts    []remembered
}

func (m *stuckMemory) Get(string, any) (bool, error) { return false, nil }

func (m *stuckMemory) Put(_ string, v any) error {
	r, ok := v.(remembered)
	if !ok {
		return fmt.Errorf("Put(%T), want a remembered", v)
	}
	m.mu.Lock()
	first := !m.started
	m.started = true
	m.mu.Unlock()
	if first {
		m.entered <- struct{}{}
		<-m.release
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.puts = append(m.puts, r)
	return nil
}

func (m *stuckMemory) Save() error { return nil }

// sessionKeys lists the sessions r holds, oldest first.
func sessionKeys(r remembered) []string {
	var keys []string
	for _, s := range r.Sessions {
		keys = append(keys, s.Session)
	}
	return keys
}

func TestMemoryNeverGoesBack(t *testing.T) {
	tests := []struct {
		name string
	}{
		{name: "Close's last Put lands after a save that was stuck in Put when the agent stopped, so the newest snapshot stays"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mem := &stuckMemory{entered: make(chan struct{}, 1), release: make(chan struct{})}
			n := New(Options{
				Backend: &fakeBackend{},
				// Off, so every note is decided at once and none is shown.
				Settings: func() config.Notifications { return config.Notifications{Off: true} },
				Memory:   mem,
				Now:      func() time.Time { return at("20:00:00") },
			})
			n.Notify(Note{Session: "s1"})
			<-mem.entered // the save of [s1] is stuck in its Put
			n.Notify(Note{Session: "s2"})
			// The stuck Put finishes only after Close has stopped waiting
			// for it and gone on to its own Put of [s1 s2].
			time.AfterFunc(closeWait+500*time.Millisecond, func() { close(mem.release) })
			n.Close()
			n.wg.Wait()

			mem.mu.Lock()
			defer mem.mu.Unlock()
			for i := 1; i < len(mem.puts); i++ {
				if len(mem.puts[i].Sessions) < len(mem.puts[i-1].Sessions) {
					t.Errorf("Put %d holds %q, after Put %d's %q", i, sessionKeys(mem.puts[i]), i-1, sessionKeys(mem.puts[i-1]))
				}
			}
			if len(mem.puts) == 0 || strings.Join(sessionKeys(mem.puts[len(mem.puts)-1]), " ") != "s1 s2" {
				t.Errorf("Puts = %+v, want the last to hold [s1 s2]", mem.puts)
			}
		})
	}
}

// lateAway is a Backend whose Show ignores ctx, as an OS call that is slow
// to give up does, and finds the screen locked once released.
type lateAway struct {
	entered chan struct{}
	release chan struct{}
}

func (b *lateAway) Show(context.Context, Message, func()) error {
	b.entered <- struct{}{}
	<-b.release
	return ErrAway
}

func TestALockedScreenFoundAfterCloseIsStopped(t *testing.T) {
	tests := []struct {
		name string
	}{
		{name: "a note whose Show finds the screen locked after Close is reported Stopped, not parked"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := &lateAway{entered: make(chan struct{}, 1), release: make(chan struct{})}
			rec := &recorder{}
			n := New(Options{Backend: b, Result: rec.result, Now: func() time.Time { return at("20:00:00") }})
			n.Notify(Note{Session: "s1", Ended: at("20:00:00")})
			<-b.entered
			closed := make(chan struct{})
			go func() {
				n.Close()
				close(closed)
			}()
			for {
				n.mu.Lock()
				c := n.closed
				n.mu.Unlock()
				if c {
					break
				}
				time.Sleep(time.Millisecond)
			}
			close(b.release)
			<-closed
			n.wg.Wait()
			results, _ := rec.got()
			if got, want := strings.Join(results, "\n"), "s1: the agent stopped"; got != want {
				t.Errorf("results:\n%s\nwant:\n%s", got, want)
			}
		})
	}
}

func TestFreshIsSessionGapFromTheNewestTable(t *testing.T) {
	tables := params.Tables()
	newest := tables[len(tables)-1]
	tests := []struct {
		name string
		want time.Duration
	}{
		{name: "Fresh is the newest table's SESSION_GAP_S, in seconds", want: time.Duration(newest.Int(params.SESSION_GAP_S)) * time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Fresh(); got != tt.want || got <= 0 {
				t.Errorf("Fresh() = %v, want %v from table %s", got, tt.want, newest.Version())
			}
		})
	}
}

func TestOutcomeString(t *testing.T) {
	tests := []struct {
		name string
		o    Outcome
		want string
	}{
		{name: "Queued reads as queued", o: Queued, want: "queued"},
		{name: "Waiting reads as waiting", o: Waiting, want: "waiting"},
		{name: "Shown reads as shown", o: Shown, want: "shown"},
		{name: "TurnedOff says notifications are off", o: TurnedOff, want: "notifications are off"},
		{name: "InQuietHours says it is quiet hours", o: InQuietHours, want: "quiet hours"},
		{name: "Repeated says the session had its decision", o: Repeated, want: "already decided for this session"},
		{name: "Stale says the session ended too long ago", o: Stale, want: "the session ended too long ago"},
		{name: "TooSoon says the last notification was too recent", o: TooSoon, want: "too soon after the last notification"},
		{name: "Replaced says a newer note took the place", o: Replaced, want: "replaced by a newer note"},
		{name: "Busy says the OS isn't showing notifications", o: Busy, want: "the OS isn't showing notifications now"},
		{name: "Unavailable says this desktop can't show them", o: Unavailable, want: "this desktop can't show notifications"},
		{name: "Failed reads as failed", o: Failed, want: "failed"},
		{name: "Stopped says the agent stopped", o: Stopped, want: "the agent stopped"},
		{name: "an outcome past the last says so", o: Stopped + 1, want: fmt.Sprintf("Outcome(%d)", int(Stopped)+1)},
		{name: "a negative outcome says so", o: Outcome(-1), want: "Outcome(-1)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.o.String(); got != tt.want {
				t.Errorf("String() = %q, want %q", got, tt.want)
			}
		})
	}
}

// slowMemory is a Memory whose Save takes delay, as a Save to a slow disk
// does, and keeps what the last finished Save wrote. With saveErr set,
// every Save fails.
type slowMemory struct {
	delay   time.Duration
	saveErr error

	mu    sync.Mutex
	put   remembered // the last Put
	saved remembered // what the last Save that succeeded wrote
}

func (m *slowMemory) Get(string, any) (bool, error) { return false, nil }

func (m *slowMemory) Put(_ string, v any) error {
	r, ok := v.(remembered)
	if !ok {
		return fmt.Errorf("Put(%T), want a remembered", v)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.put = r
	return nil
}

func (m *slowMemory) Save() error {
	m.mu.Lock()
	r := m.put
	m.mu.Unlock()
	time.Sleep(m.delay)
	if m.saveErr != nil {
		return m.saveErr
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.saved = r
	return nil
}

// onDisk lists the sessions the last successful Save wrote.
func (m *slowMemory) onDisk() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return sessionKeys(m.saved)
}

// diskCheckingBackend notes, at each Show, which sessions mem has on disk.
type diskCheckingBackend struct {
	mem *slowMemory

	mu     sync.Mutex
	atShow [][]string
}

func (b *diskCheckingBackend) Show(context.Context, Message, func()) error {
	keys := b.mem.onDisk()
	b.mu.Lock()
	defer b.mu.Unlock()
	b.atShow = append(b.atShow, keys)
	return nil
}

// A note's decision is on disk before the OS gets the note, so an agent
// that crashes after the note went up can't show it again on its next
// start (#86).
func TestADecisionIsSavedBeforeTheOSGetsTheNote(t *testing.T) {
	tests := []struct {
		name        string
		saveErr     error
		wantShows   int
		wantResult  string
		wantProblem string
	}{
		{
			name:       "the note's session is on disk when Show is called, however slow the disk is",
			wantShows:  1,
			wantResult: "s1: shown",
		},
		{
			name:        "a note whose decision can't be saved isn't shown, and doctor hears why",
			saveErr:     errors.New("disk full"),
			wantResult:  "s1: failed",
			wantProblem: "couldn't save the note's decision before showing it: disk full",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mem := &slowMemory{delay: 200 * time.Millisecond, saveErr: tt.saveErr}
			b := &diskCheckingBackend{mem: mem}
			rec := &recorder{}
			n := New(Options{Backend: b, Memory: mem, Now: func() time.Time { return at("20:00:00") }, Problem: rec.problem, Result: rec.result})
			defer n.Close()
			if got := n.Notify(Note{Session: "s1", Ended: at("20:00:00")}); got != Queued {
				t.Fatalf("Notify(s1) = %v, want %v", got, Queued)
			}
			n.wg.Wait()

			b.mu.Lock()
			atShow := append([][]string(nil), b.atShow...)
			b.mu.Unlock()
			if len(atShow) != tt.wantShows {
				t.Fatalf("Show was called %d times, want %d", len(atShow), tt.wantShows)
			}
			for i, keys := range atShow {
				if !slices.Contains(keys, "s1") {
					t.Errorf("Show call %d came while the disk held %q, without s1", i, keys)
				}
			}
			results, problems := rec.got()
			if got := strings.Join(results, "\n"); got != tt.wantResult {
				t.Errorf("results:\n%s\nwant:\n%s", got, tt.wantResult)
			}
			if tt.wantProblem != "" && (len(problems) != 1 || !strings.Contains(problems[0], tt.wantProblem)) {
				t.Errorf("problems = %q, want one containing %q", problems, tt.wantProblem)
			}
			// The session had its decision either way.
			if got := n.Notify(Note{Session: "s1", Ended: at("20:00:00")}); got != Repeated {
				t.Errorf("Notify(s1) again = %v, want %v", got, Repeated)
			}
		})
	}
}

// The settings and the clock are read again right before a note goes to
// the OS, after the callbacks that ran since the note passed the rules
// (#86). Here the agent changes them from Options.Result, which is the
// last thing to run before the next attempt.
func TestRulesAreCheckedAgainRightBeforeShow(t *testing.T) {
	off := config.Notifications{Off: true}
	quiet := config.Notifications{Quiet: mustQuiet("20:10-07:00")}
	tests := []struct {
		name string
		// behind: s2 comes while s1's Show is out, and goes when s1's
		// ends. Otherwise s1 finds the screen locked, and s2 takes its
		// place.
		behind    bool
		errs      []error
		trigger   string                // the Result line on which things change
		settings  *config.Notifications // the settings from then on
		clock     string                // the clock from then on, when set
		want      []string
		wantCalls int
	}{
		{
			name:   "a note behind one the OS refused isn't shown once notifications are turned off",
			behind: true, errs: []error{ErrBusy},
			trigger: "s1: the OS isn't showing notifications now", settings: &off,
			want:      []string{"s1: the OS isn't showing notifications now", "s2: notifications are off"},
			wantCalls: 1,
		},
		{
			name:   "a note behind one the OS refused isn't shown once quiet hours have begun",
			behind: true, errs: []error{ErrBusy},
			trigger: "s1: the OS isn't showing notifications now", settings: &quiet, clock: "20:10:00",
			want:      []string{"s1: the OS isn't showing notifications now", "s2: quiet hours"},
			wantCalls: 1,
		},
		{
			name: "a note that replaced a waiting one isn't shown once notifications are turned off",
			errs: []error{ErrAway}, trigger: "s1: replaced by a newer note", settings: &off,
			want:      []string{"s1: replaced by a newer note", "s2: notifications are off"},
			wantCalls: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clock := &fakeClock{t: at("20:00:00")}
			b := &fakeBackend{errs: tt.errs}
			var mu sync.Mutex
			settings := config.Notifications{}
			var results []string
			n := New(Options{
				Backend: b,
				Settings: func() config.Notifications {
					mu.Lock()
					defer mu.Unlock()
					return settings
				},
				Now: clock.Now,
				Result: func(note Note, o Outcome) {
					line := note.Session + ": " + o.String()
					mu.Lock()
					defer mu.Unlock()
					results = append(results, line)
					if line == tt.trigger {
						settings = *tt.settings
						if tt.clock != "" {
							clock.Set(at(tt.clock))
						}
					}
				},
			})
			defer n.Close()
			if tt.behind {
				b.block = make(chan struct{})
				b.entered = make(chan struct{}, 2)
			}
			if got := n.Notify(Note{Session: "s1", Ended: clock.Now()}); got != Queued {
				t.Fatalf("Notify(s1) = %v, want %v", got, Queued)
			}
			if tt.behind {
				<-b.entered
				if got := n.Notify(Note{Session: "s2", Ended: clock.Now()}); got != Waiting {
					t.Fatalf("Notify(s2) = %v, want %v", got, Waiting)
				}
				close(b.block)
			} else {
				n.wg.Wait()
				if got := n.Notify(Note{Session: "s2", Ended: clock.Now()}); got != Queued {
					t.Fatalf("Notify(s2) = %v, want %v", got, Queued)
				}
			}
			n.wg.Wait()
			mu.Lock()
			got := strings.Join(results, "\n")
			mu.Unlock()
			if want := strings.Join(tt.want, "\n"); got != want {
				t.Errorf("results:\n%s\nwant:\n%s", got, want)
			}
			if calls := len(b.shown()); calls != tt.wantCalls {
				t.Errorf("Show was called %d times, want %d", calls, tt.wantCalls)
			}
		})
	}
}

// An unlock that comes while the OS is being asked about a note isn't lost
// when the OS answers that the screen is locked, as Windows can when it
// read the lock state just before the unlock (#86).
func TestAnUnlockDuringShowIsKept(t *testing.T) {
	tests := []struct {
		name string
		errs []error
		// want and wantCalls are once the unlock during Show has played
		// out; wantLater and wantLaterCalls after one more unlock.
		want, wantLater           []string
		wantCalls, wantLaterCalls int
	}{
		{
			name:      "a note the OS found locked as the user came back asks the OS once more, and is shown",
			errs:      []error{ErrAway},
			want:      []string{"s1: shown"},
			wantCalls: 2,
			wantLater: []string{"s1: shown"}, wantLaterCalls: 2,
		},
		{
			name:      "a note the OS still finds locked when it asks again waits for the next unlock",
			errs:      []error{ErrAway, ErrAway},
			wantCalls: 2,
			wantLater: []string{"s1: shown"}, wantLaterCalls: 3,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newRig(config.Notifications{}, tt.errs, nil)
			defer r.n.Close()
			r.backend.block = make(chan struct{})
			r.backend.entered = make(chan struct{}, 3)
			if got := r.n.Notify(Note{Session: "s1", Ended: r.clock.Now()}); got != Queued {
				t.Fatalf("Notify(s1) = %v, want %v", got, Queued)
			}
			<-r.backend.entered
			r.n.UserPresent()
			close(r.backend.block)
			r.n.wg.Wait()
			results, _ := r.rec.got()
			if got, want := strings.Join(results, "\n"), strings.Join(tt.want, "\n"); got != want {
				t.Errorf("results:\n%s\nwant:\n%s", got, want)
			}
			if calls := len(r.backend.shown()); calls != tt.wantCalls {
				t.Errorf("Show was called %d times, want %d", calls, tt.wantCalls)
			}

			r.run(t, []step{{at: "20:05:00", present: true}})
			results, _ = r.rec.got()
			if got, want := strings.Join(results, "\n"), strings.Join(tt.wantLater, "\n"); got != want {
				t.Errorf("after the next unlock, results:\n%s\nwant:\n%s", got, want)
			}
			if calls := len(r.backend.shown()); calls != tt.wantLaterCalls {
				t.Errorf("after the next unlock, Show was called %d times, want %d", calls, tt.wantLaterCalls)
			}
		})
	}
}

// A note that came while the OS was being asked about an older one gets
// the OS's answer for itself when the older one found the screen locked,
// as it would have had it come a moment later (#86).
func TestANewerNoteGetsItsOwnOSCheck(t *testing.T) {
	tests := []struct {
		name                      string
		errs                      []error
		want, wantLater           []string
		wantCalls, wantLaterCalls int
	}{
		{
			name:      "a note that came while the older one found the screen locked is shown when the screen is unlocked by then",
			errs:      []error{ErrAway},
			want:      []string{"s1: replaced by a newer note", "s2: shown"},
			wantCalls: 2,
			wantLater: []string{"s1: replaced by a newer note", "s2: shown"}, wantLaterCalls: 2,
		},
		{
			name:      "a newer note that finds the screen locked too waits for the user",
			errs:      []error{ErrAway, ErrAway},
			want:      []string{"s1: replaced by a newer note"},
			wantCalls: 2,
			wantLater: []string{"s1: replaced by a newer note", "s2: shown"}, wantLaterCalls: 3,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newRig(config.Notifications{}, tt.errs, nil)
			defer r.n.Close()
			r.backend.block = make(chan struct{})
			r.backend.entered = make(chan struct{}, 3)
			if got := r.n.Notify(Note{Session: "s1", Ended: r.clock.Now()}); got != Queued {
				t.Fatalf("Notify(s1) = %v, want %v", got, Queued)
			}
			<-r.backend.entered
			if got := r.n.Notify(Note{Session: "s2", Ended: r.clock.Now()}); got != Waiting {
				t.Fatalf("Notify(s2) = %v, want %v", got, Waiting)
			}
			close(r.backend.block)
			r.n.wg.Wait()
			results, _ := r.rec.got()
			if got, want := strings.Join(results, "\n"), strings.Join(tt.want, "\n"); got != want {
				t.Errorf("results:\n%s\nwant:\n%s", got, want)
			}
			if calls := len(r.backend.shown()); calls != tt.wantCalls {
				t.Errorf("Show was called %d times, want %d", calls, tt.wantCalls)
			}

			r.run(t, []step{{at: "20:05:00", present: true}})
			results, _ = r.rec.got()
			if got, want := strings.Join(results, "\n"), strings.Join(tt.wantLater, "\n"); got != want {
				t.Errorf("after the next unlock, results:\n%s\nwant:\n%s", got, want)
			}
			if calls := len(r.backend.shown()); calls != tt.wantLaterCalls {
				t.Errorf("after the next unlock, Show was called %d times, want %d", calls, tt.wantLaterCalls)
			}
		})
	}
}

// burst is a note about s0 at 20:00:00, then one each minute about s1 to
// sN, too soon after it.
func burst(sessions int) []step {
	steps := []step{{at: "20:00:00", notify: "s0", want: Queued}}
	for i := 1; i <= sessions; i++ {
		steps = append(steps, step{after: time.Duration(i) * time.Minute, notify: fmt.Sprintf("s%d", i), want: TooSoon})
	}
	return steps
}

// A session is remembered for as long as a note about it could pass the
// rules again, not only while it is among the newest sessions (#86).
func TestSessionsAreKeptWhileTheirNoteCouldBeShown(t *testing.T) {
	tests := []struct {
		name      string
		steps     []step
		wantCalls int
		wantKept  int    // sessions remembered at the end
		wantGone  string // a session no longer remembered, if any
	}{
		{
			name: "a session shown before sessionsKept newer ones still has its decision when its note comes again Fresh after it ended",
			steps: append(burst(sessionsKept),
				step{after: Fresh(), ago: Fresh(), notify: "s0", want: Repeated}),
			wantCalls: 1,
			wantKept:  sessionsKept + 1,
		},
		{
			name: "sessions whose notes can't pass the rules any more are forgotten beyond the newest sessionsKept",
			steps: append(burst(sessionsKept),
				step{after: 2 * Fresh(), notify: "late", want: Queued}),
			wantCalls: 2,
			wantKept:  sessionsKept,
			wantGone:  "s0",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newRig(config.Notifications{}, nil, nil)
			defer r.n.Close()
			r.run(t, tt.steps)
			if calls := len(r.backend.shown()); calls != tt.wantCalls {
				t.Errorf("Show was called %d times, want %d", calls, tt.wantCalls)
			}
			r.n.mu.Lock()
			kept := sessionKeys(r.n.snapshot())
			r.n.mu.Unlock()
			if len(kept) != tt.wantKept {
				t.Errorf("%d sessions remembered, want %d: %q", len(kept), tt.wantKept, kept)
			}
			if tt.wantGone != "" && slices.Contains(kept, tt.wantGone) {
				t.Errorf("%s is still remembered: %q", tt.wantGone, kept)
			}
		})
	}
}

func TestSessionsKeptHaveABound(t *testing.T) {
	tests := []struct {
		name     string
		sessions int
	}{
		{name: "no more than sessionsMax sessions are remembered, however many end within Fresh", sessions: sessionsMax + 40},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newRig(config.Notifications{Off: true}, nil, nil)
			defer r.n.Close()
			for i := range tt.sessions {
				r.n.Notify(Note{Session: fmt.Sprintf("s%d", i), Ended: r.clock.Now()})
			}
			r.n.mu.Lock()
			kept := sessionKeys(r.n.snapshot())
			r.n.mu.Unlock()
			if len(kept) != sessionsMax {
				t.Fatalf("%d sessions remembered, want %d", len(kept), sessionsMax)
			}
			if want := fmt.Sprintf("s%d", tt.sessions-1); kept[len(kept)-1] != want {
				t.Errorf("the newest session remembered is %s, want %s", kept[len(kept)-1], want)
			}
		})
	}
}

// acceptingBackend has the OS take each message, then keeps it on screen
// for up by the fake clock, as the Windows balloon stays up, and returns
// err.
type acceptingBackend struct {
	clock *fakeClock
	up    time.Duration
	err   error
}

func (b *acceptingBackend) Show(_ context.Context, _ Message, shown func()) error {
	shown()
	b.clock.Set(b.clock.Now().Add(b.up))
	return b.err
}

// The gap between notifications counts from when the OS took a note, and a
// note the OS took counts as shown whatever happens after that (#86).
func TestTheOSTakingANoteIsWhatCounts(t *testing.T) {
	tests := []struct {
		name         string
		up           time.Duration // how long the message stays up
		err          error         // what Show returns afterwards
		second       time.Duration // when s2 comes, after s1 at 20:00:00
		want         []string
		wantProblems []string
	}{
		{
			name: "a note Fresh after the OS took the last one is shown, though that one's balloon stayed up a while",
			up:   5 * time.Second, second: Fresh(),
			want:         []string{"s1: shown", "s2: shown"},
			wantProblems: []string{"", ""},
		},
		{
			name: "a note less than Fresh after the OS took the last one is still too soon",
			up:   5 * time.Second, second: Fresh() - time.Second,
			want:         []string{"s1: shown", "s2: too soon after the last notification"},
			wantProblems: []string{""},
		},
		{
			name: "a note the OS took is shown even when taking its balloon down failed, so the next one is too soon",
			err:  errors.New("SetTimer: Not enough quota is available to process this command."), second: time.Minute,
			want:         []string{"s1: shown", "s2: too soon after the last notification"},
			wantProblems: []string{""},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clock := &fakeClock{t: at("20:00:00")}
			rec := &recorder{}
			n := New(Options{
				Backend: &acceptingBackend{clock: clock, up: tt.up, err: tt.err},
				Now:     clock.Now,
				Problem: rec.problem,
				Result:  rec.result,
			})
			defer n.Close()
			if got := n.Notify(Note{Session: "s1", Ended: clock.Now()}); got != Queued {
				t.Fatalf("Notify(s1) = %v, want %v", got, Queued)
			}
			n.wg.Wait()
			clock.Set(at("20:00:00").Add(tt.second))
			n.Notify(Note{Session: "s2", Ended: clock.Now()})
			n.wg.Wait()
			results, problems := rec.got()
			if got, want := strings.Join(results, "\n"), strings.Join(tt.want, "\n"); got != want {
				t.Errorf("results:\n%s\nwant:\n%s", got, want)
			}
			if !slices.Equal(problems, tt.wantProblems) {
				t.Errorf("problems = %q, want %q", problems, tt.wantProblems)
			}
		})
	}
}

// Doctor and Options.Result hear attempts in the order they ended, even
// when the next attempt ends while the last one's callbacks still run
// (#86).
func TestAttemptsAreToldInTheOrderTheyEnded(t *testing.T) {
	tests := []struct {
		name         string
		wantResults  []string
		wantProblems []string
	}{
		{
			name:         "a note shown after a failed one leaves doctor with no problem, and Result hears the two in turn",
			wantResults:  []string{"s1: failed", "s2: shown"},
			wantProblems: []string{"the last notification failed: boom", ""},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			failedTold := make(chan struct{})
			release := make(chan struct{})
			cleared := make(chan struct{}, 1)
			var mu sync.Mutex
			var results, problems []string
			n := New(Options{
				Backend: &fakeBackend{errs: []error{errors.New("boom")}},
				Now:     func() time.Time { return at("20:00:00") },
				Problem: func(msg string) {
					if msg != "" {
						// s1's problem: hold it until s2 has had its chance
						// to overtake it.
						close(failedTold)
						<-release
					}
					mu.Lock()
					problems = append(problems, msg)
					mu.Unlock()
					if msg == "" {
						cleared <- struct{}{}
					}
				},
				Result: func(note Note, o Outcome) {
					mu.Lock()
					defer mu.Unlock()
					results = append(results, note.Session+": "+o.String())
				},
			})
			defer n.Close()
			if got := n.Notify(Note{Session: "s1", Ended: at("20:00:00")}); got != Queued {
				t.Fatalf("Notify(s1) = %v, want %v", got, Queued)
			}
			// s1's attempt is over and doctor is about to hear why.
			<-failedTold
			if got := n.Notify(Note{Session: "s2", Ended: at("20:00:00")}); got != Queued {
				t.Fatalf("Notify(s2) = %v, want %v", got, Queued)
			}
			// Told out of order, s2's success would reach doctor here,
			// ahead of s1's failure.
			select {
			case <-cleared:
			case <-time.After(500 * time.Millisecond):
			}
			close(release)
			n.wg.Wait()
			mu.Lock()
			defer mu.Unlock()
			if !slices.Equal(problems, tt.wantProblems) {
				t.Errorf("problems = %q, want %q", problems, tt.wantProblems)
			}
			if !slices.Equal(results, tt.wantResults) {
				t.Errorf("results = %q, want %q", results, tt.wantResults)
			}
		})
	}
}

// stalledMemory is a Memory like state.Store, whose Save holds the lock
// that Put needs for as long as it writes. Its first Save stalls, as one
// on a stuck disk does, until release is closed.
type stalledMemory struct {
	entered chan struct{} // gets a value as the first Save stalls
	release chan struct{}

	mu      sync.Mutex // state.Store's lock
	stalled bool
	puts    []remembered
}

func (m *stalledMemory) Get(string, any) (bool, error) { return false, nil }

func (m *stalledMemory) Put(_ string, v any) error {
	r, ok := v.(remembered)
	if !ok {
		return fmt.Errorf("Put(%T), want a remembered", v)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.puts = append(m.puts, r)
	return nil
}

func (m *stalledMemory) Save() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.stalled {
		m.stalled = true
		m.entered <- struct{}{}
		<-m.release
	}
	return nil
}

func TestCloseDoesntWaitOutAStuckSave(t *testing.T) {
	tests := []struct {
		name string
	}{
		{name: "Close returns within closeWait while a Save is stuck holding Memory's lock, and its last Put lands once that Save ends"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mem := &stalledMemory{entered: make(chan struct{}, 1), release: make(chan struct{})}
			n := New(Options{
				Backend: &fakeBackend{},
				// Off, so every note is decided at once and none is shown.
				Settings: func() config.Notifications { return config.Notifications{Off: true} },
				Memory:   mem,
				Now:      func() time.Time { return at("20:00:00") },
			})
			n.Notify(Note{Session: "s1"})
			<-mem.entered // the save of [s1] is stuck, holding the lock
			n.Notify(Note{Session: "s2"})
			// The disk comes back well after closeWait.
			time.AfterFunc(closeWait+1500*time.Millisecond, func() { close(mem.release) })
			start := time.Now()
			n.Close()
			if took := time.Since(start); took > closeWait+time.Second {
				t.Errorf("Close took %v with a Save stuck, want about closeWait, %v", took, closeWait)
			}
			n.wg.Wait()
			mem.mu.Lock()
			defer mem.mu.Unlock()
			if len(mem.puts) == 0 || strings.Join(sessionKeys(mem.puts[len(mem.puts)-1]), " ") != "s1 s2" {
				t.Errorf("Puts = %+v, want the last to hold [s1 s2]", mem.puts)
			}
		})
	}
}

// Options.Result is handed over from an outbox, outside the notifier's
// lock, so it can call the notifier.
func TestResultMayCallTheNotifier(t *testing.T) {
	tests := []struct {
		name string
	}{
		{name: "a Result callback that calls Notify gets its answer, and Result hears the second note after the first"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var n *Notifier
			var mu sync.Mutex
			var results []string
			inner := make(chan Outcome, 1)
			n = New(Options{
				Backend:  &fakeBackend{},
				Settings: func() config.Notifications { return config.Notifications{Off: true} },
				Now:      func() time.Time { return at("20:00:00") },
				Result: func(note Note, o Outcome) {
					mu.Lock()
					results = append(results, note.Session+": "+o.String())
					mu.Unlock()
					if note.Session == "s1" {
						inner <- n.Notify(Note{Session: "s2"})
					}
				},
			})
			defer n.Close()
			done := make(chan struct{})
			go func() {
				n.Notify(Note{Session: "s1"})
				close(done)
			}()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("Notify from a Result callback never returned")
			}
			if got := <-inner; got != TurnedOff {
				t.Errorf("Notify(s2) from the callback = %v, want %v", got, TurnedOff)
			}
			mu.Lock()
			defer mu.Unlock()
			if want := []string{"s1: notifications are off", "s2: notifications are off"}; !slices.Equal(results, want) {
				t.Errorf("results = %q, want %q", results, want)
			}
		})
	}
}
