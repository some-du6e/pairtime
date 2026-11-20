// SPDX-License-Identifier: AGPL-3.0-or-later

package notifier

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
	block   chan struct{} // when set, Show waits for it to close, or for ctx
	entered chan struct{} // when set, Show sends on it as it starts
}

func (f *fakeBackend) Show(ctx context.Context, m Message) error {
	f.mu.Lock()
	f.calls = append(f.calls, m)
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
			if err := st.Put(memoryKey, remembered{LastShown: at(tt.lastShown), Sessions: []string{"seen"}}); err != nil {
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
		{name: "every outcome has a name", o: Stopped, want: "the agent stopped"},
		{name: "an outcome out of range says so", o: Outcome(99), want: "Outcome(99)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.o.String(); got != tt.want {
				t.Errorf("String() = %q, want %q", got, tt.want)
			}
		})
	}
	for o := Queued; o <= Stopped; o++ {
		if outcomeNames[o] == "" {
			t.Errorf("Outcome %d has no name", int(o))
		}
	}
}
