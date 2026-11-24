// SPDX-License-Identifier: AGPL-3.0-or-later

package pages

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/server/dashboard"
	"github.com/some-du6e/pairtime/internal/server/rollup"
)

// demoClock is when the tests look at the sample.
var demoClock = time.Date(2026, 11, 20, 18, 30, 0, 0, time.UTC)

// getDemo serves GET /demo from a Site built from o with sign-in as in,
// at demoClock, and returns the response and its body.
func getDemo(t *testing.T, o Options, in SignIn) (*http.Response, string) {
	t.Helper()
	mux := http.NewServeMux()
	newSite(t, o).RegisterDemo(mux, in, func() time.Time { return demoClock })
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/demo", nil))
	resp := rec.Result()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp, string(body)
}

// sectionOf returns the part of page from the section labelled by id to its
// end, or "" when there is none.
func sectionOf(page, id string) string {
	_, rest, ok := strings.Cut(page, `<section aria-labelledby="`+id+`">`)
	if !ok {
		return ""
	}
	s, _, _ := strings.Cut(rest, "</section>")
	return s
}

// kindSplitNote is the dashboard's note under its split by kind, for the
// agent named agent: a bucket's 15-second slot carries one kind and one
// level, and the app splits every second (D-kind-split-by-slot).
func kindSplitNote(agent string) string {
	return `<p class="note">This page gives each 15 seconds one kind of work and one evidence level, so these splits can differ from the pairtime app on your computer by a few seconds, but your totals and the time ` +
		agent + ` worked on its own match it exactly.</p>`
}

func TestDemoPage(t *testing.T) {
	tests := []struct {
		name    string
		opts    Options
		signIn  SignIn
		section string // the section to look in, or "" for the whole page
		want    []string
		notWant []string
	}{
		{
			name:   "the page says on itself that its data is a made-up sample",
			signIn: signInOn(""),
			want: []string{
				"<title>Sample dashboard · pairtime</title>",
				"<h1>Sample dashboard</h1>",
				`<strong class="tag">Sample data</strong> These are thirty made-up days of a teen building three projects with Claude Code. No real person&#39;s time is on this page.`,
			},
		},
		{
			name:   "the banner says a real dashboard shows projects by a short code, since project names never reach the server (DESIGN 4.3)",
			signIn: signInOn(""),
			want:   []string{"The project names are made up too, and your own dashboard shows each project by a short code instead, since your project names stay on your computer."},
		},
		{
			name:    "the page has every section: today, the last 7 days, projects, evidence levels and activity kinds",
			signIn:  signInOn(""),
			want:    []string{`<h2 id="today">Today <span class="when">Friday 20 November</span></h2>`, `<h2 id="week">Last 7 days</h2>`, `<h2 id="projects">Projects</h2>`, `<h2 id="levels">Evidence levels</h2>`, `<h2 id="kinds">What you spent it on</h2>`},
			notWant: []string{"Nothing counted"},
		},
		{
			name:    "today leads with your time, and says the agent's time and what is still pending",
			signIn:  signInOn(""),
			section: "today",
			want:    []string{`<p class="hero"><strong>1h 53m</strong> of your time</p>`, "Claude Code worked on its own for 3h 32m. 9m of your time is still pending."},
		},
		{
			name:    "today's chess clock shows each project's time and Claude Code's side by side, DESIGN 3.8's You 1h 12m, Claude 2h 40m included",
			signIn:  signInOn(""),
			section: "today",
			want: []string{
				`<p class="clock-name"><span class="key c1"></span>chip8-emulator</p>`,
				`<p class="face"><span>You</span> <strong>1h 12m</strong> <small>9m pending</small></p>`,
				`<p class="face"><span>Claude Code</span> <strong>2h 40m</strong> <small>12m pending</small></p>`,
				`<p class="clock-name"><span class="key c2"></span>robotics-scouting</p>`,
				`<p class="face"><span>You</span> <strong>41m</strong></p>`,
				`<p class="face"><span>Claude Code</span> <strong>52m</strong></p>`,
			},
			notWant: []string{"portfolio-site"},
		},
		{
			name:    "the chess clock is information, never a target ratio: no share, no ratio and no goal between the two clocks",
			signIn:  signInOn(""),
			section: "today",
			want:    []string{"Two clocks, not a score. Claude Code&#39;s clock counts the time it worked on a project with none of your time counted there, so the two never run at once, and there&#39;s no right ratio between them."},
			notWant: []string{"%", "goal", "target", `class="bar`, `class="seg`},
		},
		{
			name:    "the last 7 days are columns stacked by project on an axis to 4h, oldest first, ending today",
			signIn:  signInOn(""),
			section: "week",
			want: []string{
				"<strong>12h 13m</strong> of your time. Claude Code worked on its own for 21h 2m.",
				`<div class="yaxis"><span class="at25">1h</span><span class="at50">2h</span><span class="at75">3h</span><span class="at100">4h</span></div>`,
				`<li class="col"><span class="seg c1 f58" title="chip8-emulator, 2h 20m"></span><span class="seg c3 f15" title="portfolio-site, 35m"></span><small class="cap">2h 55m</small></li>`,
				`<li><span>Sat</span><span class="dom">14</span></li>`,
				`<li class="today"><span>Today</span></li>`,
			},
		},
		{
			name:    "a day off is an empty column with no total",
			signIn:  signInOn(""),
			section: "week",
			want:    []string{`<li class="col"></li>`, `<li><span>Wed</span><span class="dom">18</span></li>`},
		},
		{
			name:    "today's pending minutes sit faded on top of their project's part, and the legend says what faded means",
			signIn:  signInOn(""),
			section: "week",
			want: []string{
				`<span class="seg c1 f26" title="chip8-emulator, 1h 3m"></span><span class="seg c1 pend f4" title="chip8-emulator, 9m pending"></span><span class="seg c2 f17" title="robotics-scouting, 41m"></span><small class="cap">1h 53m</small>`,
				`<li><span class="key c1 pend"></span>Faded means pending</li>`,
				"Pending minutes can still change, so they stay faded until they settle, within about 15 minutes of when they end.",
			},
		},
		{
			name:    "the week chart has a legend of its projects, and its numbers as a table",
			signIn:  signInOn(""),
			section: "week",
			want: []string{
				`<li><span class="key c1"></span>chip8-emulator</li>`,
				`<li><span class="key c2"></span>robotics-scouting</li>`,
				`<li><span class="key c3"></span>portfolio-site</li>`,
				"<summary>The last 7 days as a table</summary>",
				"<thead><tr><th>Day</th><th>chip8-emulator</th><th>robotics-scouting</th><th>portfolio-site</th><th>Total</th></tr></thead>",
				"<tr><td>Sat 14 Nov</td><td>2h 20m</td><td>0m</td><td>35m</td><td>2h 55m</td></tr>",
				"<tr><td>Today</td><td>1h 12m, 9m pending</td><td>41m</td><td>0m</td><td>1h 53m, 9m pending</td></tr>",
			},
		},
		{
			name:    "each project's time and Claude Code's over 7 and 30 days, and the totals",
			signIn:  signInOn(""),
			section: "projects",
			want: []string{
				`<tr role="row"><td role="cell"><span class="key c1"></span>chip8-emulator</td><td role="cell" data-label="Last 7 days"><strong>6h 59m</strong> <span class="agent">Claude Code 15h 5m</span></td><td role="cell" data-label="Last 30 days"><strong>18h 34m</strong> <span class="agent">Claude Code 39h 25m</span></td></tr>`,
				`<tr role="row"><td role="cell"><span class="key c2"></span>robotics-scouting</td><td role="cell" data-label="Last 7 days"><strong>3h 51m</strong> <span class="agent">Claude Code 5h 7m</span></td><td role="cell" data-label="Last 30 days"><strong>10h 6m</strong> <span class="agent">Claude Code 13h 32m</span></td></tr>`,
				`<tr role="row"><td role="cell"><span class="key c3"></span>portfolio-site</td><td role="cell" data-label="Last 7 days"><strong>1h 23m</strong> <span class="agent">Claude Code 50m</span></td><td role="cell" data-label="Last 30 days"><strong>5h 3m</strong> <span class="agent">Claude Code 3h 10m</span></td></tr>`,
				`<tr role="row"><th role="rowheader">All projects</th><td role="cell" data-label="Last 7 days"><strong>12h 13m</strong> <span class="agent">Claude Code 21h 2m</span></td><td role="cell" data-label="Last 30 days"><strong>33h 43m</strong> <span class="agent">Claude Code 56h 7m</span></td></tr>`,
			},
		},
		{
			name:    "the split by evidence level prints each share that fits, explains A, B and C, and links /docs/evidence",
			signIn:  signInOn(""),
			section: "levels",
			want: []string{
				`<a href="/docs/evidence">What A, B and C mean</a>`,
				`<div class="segbar" role="img" aria-label="Your time by evidence level: level A 74%, level B 20%, level C 6%"><span class="lva f74" title="Level A, 9h 3m, 74%">74%</span><span class="lvb f20" title="Level B, 2h 24m, 20%">20%</span><span class="lvc f6" title="Level C, 45m, 6%"></span></div>`,
				`<li><span class="key lva"></span><span><strong>Level A.</strong> Your keyboard backs it up.</span><span class="lt">9h 3m, 74%</span></li>`,
				`<li><span class="key lvb"></span><span><strong>Level B.</strong> Your computer saw you there but can&#39;t tell whether you were typing.</span><span class="lt">2h 24m, 20%</span></li>`,
				`<li><span class="key lvc"></span><span><strong>Level C.</strong> pairtime can check it less closely`,
			},
		},
		{
			name:    "the split by activity kind ranks the kinds, longest first, with the agent named",
			signIn:  signInOn(""),
			section: "kinds",
			want: []string{
				`<li><span class="kname">Writing prompts</span><span class="track" aria-hidden="true"><span class="bar f99" title="Writing prompts, 3h 30m"></span><span class="bar pend f1" title="Writing prompts, 2m pending"></span></span><span class="kt">3h 32m</span></li>`,
				`<span class="kname">Reading Claude Code&#39;s output</span>`,
				`<span class="kname">Following Claude Code as it works</span>`,
				`<span class="kname">Looking things up</span>`,
			},
		},
		{
			name:    "the split by kind says it is counted per 15-second slot, so it can be a few seconds off the app's while totals and the agent's time match (D-kind-split-by-slot)",
			signIn:  signInOn(""),
			section: "kinds",
			want:    []string{kindSplitNote("Claude Code")},
		},
		{
			name:    "without sign-in and with sign-ups closed, as on the live server, the banner says both, as the home page does, and promises no dashboard of your own",
			signIn:  SignIn{},
			want:    []string{"Sign-ups are closed for now, and testers join by invitation, using pairtime on their own computers until sign-ups open. Sign-in isn&#39;t set up on this server yet."},
			notWant: []string{"Sign in with Hack Club</a>", "your own dashboard shows your time"},
		},
		{
			name:    "with sign-in but sign-ups closed the banner says only the people who run the server get accounts, and offers no sign-in",
			signIn:  signInOn(""),
			want:    []string{"Sign-ups are closed for now, and testers join by invitation. Until sign-ups open, this server makes accounts only for the people who run it, and testers use pairtime on their own computers."},
			notWant: []string{"Sign in with Hack Club</a>", "isn&#39;t set up", "your own dashboard shows your time"},
		},
		{
			name:    "with sign-ups open but no sign-in set up the banner says so, and offers no sign-in",
			opts:    Options{SignupsOpen: true},
			signIn:  SignIn{},
			want:    []string{"Sign-ups on this server are open, but sign-in isn&#39;t set up on it yet."},
			notWant: []string{"Sign in with Hack Club</a>", "your own dashboard shows your time"},
		},
		{
			name:    "with sign-ups open the banner offers Hack Club sign-in",
			opts:    Options{SignupsOpen: true},
			signIn:  signInOn(""),
			want:    []string{`<a class="button" href="/auth/hca/login?return_to=%2Fdashboard">Sign in with Hack Club</a> and run <code>pairtime login</code>`},
			notWant: []string{"Sign-ups are closed", "isn&#39;t set up"},
		},
		{
			name:    "a signed-in visitor gets a link to their own dashboard instead of a sign-in button",
			opts:    Options{SignupsOpen: true},
			signIn:  signInOn("U0FAKEVERIFIED"),
			want:    []string{`<a class="button" href="/dashboard">See your own dashboard</a>`},
			notWant: []string{"Sign in with Hack Club</a>", "Sign-ups are closed"},
		},
		{
			name:    "the page has no script, no form, no style attribute and no SVG",
			signIn:  signInOn(""),
			notWant: []string{"<script", "<form", " style=", "<svg"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, page := getDemo(t, tt.opts, tt.signIn)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("GET /demo = %d, want 200", resp.StatusCode)
			}
			if tt.section != "" {
				if page = sectionOf(page, tt.section); page == "" {
					t.Fatalf("the page has no section %q", tt.section)
				}
			}
			for _, w := range tt.want {
				if !strings.Contains(page, w) {
					t.Errorf("the page doesn't contain %q", w)
				}
			}
			for _, w := range tt.notWant {
				if strings.Contains(page, w) {
					t.Errorf("the page contains %q", w)
				}
			}
		})
	}
}

func TestDemoHeaders(t *testing.T) {
	s := newSite(t, Options{})
	resp, _ := getDemo(t, Options{}, signInOn("U0FAKEVERIFIED"))
	tests := []struct {
		name, header, want string
	}{
		{name: "the page is HTML", header: "Content-Type", want: "text/html; charset=utf-8"},
		{name: "browsers may not read it as another type", header: "X-Content-Type-Options", want: "nosniff"},
		{name: "the page differs by session and changes every day, so nothing may store it", header: "Cache-Control", want: "no-store"},
		{name: "links from the page carry no referrer", header: "Referrer-Policy", want: "no-referrer"},
		{name: "the CSP is the one the privacy notice and evidence page send, allowing no form, even for a signed-in visitor", header: "Content-Security-Policy", want: s.csp},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resp.Header.Get(tt.header); got != tt.want {
				t.Errorf("%s = %q, want %q", tt.header, got, tt.want)
			}
		})
	}
}

func TestDemoRoute(t *testing.T) {
	mux := http.NewServeMux()
	s := newSite(t, Options{})
	s.Register(mux)
	s.RegisterHome(mux, SignIn{})
	s.RegisterDemo(mux, SignIn{}, time.Now)
	srv := httptest.NewServer(mux)
	defer srv.Close()
	tests := []struct {
		name     string
		method   string
		path     string
		wantCode int
	}{
		{name: "/demo serves the sample dashboard", path: "/demo", wantCode: 200},
		{name: "HEAD /demo answers too", method: http.MethodHead, path: "/demo", wantCode: 200},
		{name: "POST /demo is refused", method: http.MethodPost, path: "/demo", wantCode: 405},
		{name: "a path below /demo is a 404", path: "/demo/2026-11-20", wantCode: 404},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			method := tt.method
			if method == "" {
				method = http.MethodGet
			}
			req, err := http.NewRequest(method, srv.URL+tt.path, nil)
			if err != nil {
				t.Fatal(err)
			}
			resp, err := srv.Client().Do(req)
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if resp.StatusCode != tt.wantCode {
				t.Errorf("%s %s = %d, want %d", method, tt.path, resp.StatusCode, tt.wantCode)
			}
		})
	}
}

// TestDemoNumbers checks the page's figures against the sample's View, so
// the page shows the numbers dashboard.Build summed and no others.
func TestDemoNumbers(t *testing.T) {
	v, err := dashboard.Demo{}.View(context.Background(), "", demoClock)
	if err != nil {
		t.Fatal(err)
	}
	_, page := getDemo(t, Options{}, signInOn(""))
	today := v.Days[len(v.Days)-1]
	tests := []struct {
		name, section, want string
	}{
		{name: "the hero is today's time", section: "today", want: "<strong>" + duration(today.You.Total()) + "</strong> of your time"},
		{name: "the agent's time today is the view's", section: "today", want: "Claude Code worked on its own for " + duration(today.Agent.Total()) + "."},
		{name: "the week's time is the view's", section: "week", want: "<strong>" + duration(v.Week.You.Total()) + "</strong> of your time. Claude Code worked on its own for " + duration(v.Week.Agent.Total()) + "."},
		{name: "the month's total is the view's", section: "projects", want: "<strong>" + duration(v.Month.You.Total()) + "</strong> <span class=\"agent\">Claude Code " + duration(v.Month.Agent.Total()) + "</span></td></tr>\n</tfoot>"},
		{name: "level A's time is the view's", section: "levels", want: "Your keyboard backs it up.</span><span class=\"lt\">" + duration(v.Week.Levels[0].Total()) + ","},
		{name: "the longest kind's time is the view's", section: "kinds", want: "<span class=\"kt\">" + duration(v.Week.Kinds[0].Total()) + "</span>"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if s := sectionOf(page, tt.section); !strings.Contains(s, tt.want) {
				t.Errorf("section %s doesn't contain %q", tt.section, tt.want)
			}
		})
	}
}

// classAttr finds each class attribute on a page.
var classAttr = regexp.MustCompile(`class="([^"]*)"`)

// TestPageClasses checks that every class the pages use has a rule in
// style.css, so a bar never draws at no size and a color never goes
// missing because a name is misspelled on one side.
func TestPageClasses(t *testing.T) {
	css, err := fs.ReadFile(files, "style.css")
	if err != nil {
		t.Fatal(err)
	}
	_, demo := getDemo(t, Options{SignupsOpen: true}, signInOn(""))
	_, home := getHome(t, Options{}, signInOn("U0FAKEVERIFIED"))
	// The owner's page with a refused time zone shows every part it has.
	mux, _ := ownerSite(t, signInOn("U0FAKEVERIFIED"), "u1", fakeSource{v: ownerView(t)}, true, rollup.ErrBadZone)
	r := httptest.NewRequest(http.MethodPost, "/dashboard/zone", strings.NewReader("zone=Mars"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	_, owner := serve(mux, r)
	for _, p := range []struct{ name, page string }{
		{name: "the sample dashboard", page: demo},
		{name: "the home page", page: home},
		{name: "the owner's dashboard", page: owner},
	} {
		seen := make(map[string]bool)
		for _, m := range classAttr.FindAllStringSubmatch(p.page, -1) {
			for _, c := range strings.Fields(m[1]) {
				seen[c] = true
			}
		}
		for c := range seen {
			t.Run(p.name+"'s class "+c+" has a rule in style.css", func(t *testing.T) {
				if !regexp.MustCompile(`\.` + regexp.QuoteMeta(c) + `[^a-zA-Z0-9_-]`).Match(css) {
					t.Errorf("style.css has no rule for .%s", c)
				}
			})
		}
	}
}

// TestSizeClasses checks that style.css sizes a bar for every whole
// percent, f0 to f100, each to its own number.
func TestSizeClasses(t *testing.T) {
	css, err := fs.ReadFile(files, "style.css")
	if err != nil {
		t.Fatal(err)
	}
	found := regexp.MustCompile(`\.f([0-9]+)\{--f:([0-9]+)%\}`).FindAllStringSubmatch(string(css), -1)
	var got []int
	for _, m := range found {
		if m[1] != m[2] {
			t.Errorf(".f%s sets --f to %s%%", m[1], m[2])
		}
		n, _ := strconv.Atoi(m[1])
		got = append(got, n)
	}
	want := make([]int, 101)
	for i := range want {
		want[i] = i
	}
	t.Run("f0 to f100 each set --f to their own percent, once and in order", func(t *testing.T) {
		if !reflect.DeepEqual(got, want) {
			t.Errorf("size classes %v, want 0 to 100", got)
		}
	})
}

func TestDuration(t *testing.T) {
	tests := []struct {
		name string
		s    float64
		want string
	}{
		{name: "nothing is 0m", s: 0, want: "0m"},
		{name: "a few seconds show as under a minute, not as nothing", s: 12.5, want: "<1m"},
		{name: "minutes round down, so the page never shows more than there was", s: 59*60 + 59, want: "59m"},
		{name: "whole hours drop the minutes", s: 2 * 3600, want: "2h"},
		{name: "hours and minutes", s: 72*60 + 30, want: "1h 12m"},
		{name: "past a day it still counts hours", s: 26*3600 + 5*60, want: "26h 5m"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := duration(tt.s); got != tt.want {
				t.Errorf("duration(%v) = %q, want %q", tt.s, got, tt.want)
			}
		})
	}
}

func TestAxisTop(t *testing.T) {
	tests := []struct {
		name       string
		most, want float64
	}{
		{name: "an empty week gets the smallest axis", most: 0, want: 20 * 60},
		{name: "a column exactly at a top fits it", most: 3600, want: 3600},
		{name: "a column just over a top gets the next", most: 3601, want: 2 * 3600},
		{name: "a 2h 55m day gets a 4h axis", most: 175 * 60, want: 4 * 3600},
		{name: "past a day the axis goes up in 4-hour steps", most: 25 * 3600, want: 28 * 3600},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := axisTop(tt.most); got != tt.want {
				t.Errorf("axisTop(%v) = %v, want %v", tt.most, got, tt.want)
			}
		})
	}
}

func TestStack(t *testing.T) {
	tests := []struct {
		name  string
		sizes []float64
		top   float64
		want  []int
	}{
		{name: "parts take their share of the axis", sizes: []float64{50, 25}, top: 100, want: []int{50, 25}},
		{name: "rounding happens where each part ends, so the column is as tall as its total", sizes: []float64{33.4, 33.4, 33.2}, top: 100, want: []int{33, 34, 33}},
		{name: "a tiny part still shows", sizes: []float64{80, 0.1}, top: 100, want: []int{80, 1}},
		{name: "a column at the top with a tiny part never passes 100", sizes: []float64{99.9, 0.1}, top: 100, want: []int{99, 1}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := stack(tt.sizes, tt.top); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("stack(%v, %v) = %v, want %v", tt.sizes, tt.top, got, tt.want)
			}
		})
	}
}

func TestPercents(t *testing.T) {
	tests := []struct {
		name   string
		values []float64
		want   []int
	}{
		{name: "shares add up to exactly 100", values: []float64{1, 1, 1}, want: []int{34, 33, 33}},
		{name: "the points left over go to the largest remainders", values: []float64{66.6, 20.3, 13.1}, want: []int{67, 20, 13}},
		{name: "the point left over goes to the largest remainder, not the first value", values: []float64{20.1, 13.8, 66.1}, want: []int{20, 14, 66}},
		{name: "a level with no time gets 0", values: []float64{5, 0, 0}, want: []int{100, 0, 0}},
		{name: "a level with a sliver of time gets 1%, taken from the largest, so none reads 0% and none 100% beside it", values: []float64{99.72, 0, 0.28}, want: []int{99, 0, 1}},
		{name: "two slivers each get 1%", values: []float64{99.5, 0.25, 0.25}, want: []int{98, 1, 1}},
		{name: "with no time at all every share is 0", values: []float64{0, 0, 0}, want: []int{0, 0, 0}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := percents(tt.values); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("percents(%v) = %v, want %v", tt.values, got, tt.want)
			}
		})
	}
}

func TestFill(t *testing.T) {
	tests := []struct {
		name   string
		v, top float64
		want   string
	}{
		{name: "the longest bar fills its track", v: 10, top: 10, want: "f100"},
		{name: "a bar takes its share, rounded", v: 7.26, top: 10, want: "f73"},
		{name: "a bar with any time never draws at no size", v: 0.01, top: 10, want: "f1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := fill(tt.v, tt.top); got != tt.want {
				t.Errorf("fill(%v, %v) = %q, want %q", tt.v, tt.top, got, tt.want)
			}
		})
	}
}

// fakeSource is a dashboard.Source with one answer.
type fakeSource struct {
	v   dashboard.View
	err error
}

func (f fakeSource) View(context.Context, string, time.Time) (dashboard.View, error) {
	return f.v, f.err
}

// ownerView is the sample's numbers as an owner's own view, which names
// the agent as real dashboards do.
func ownerView(t *testing.T) dashboard.View {
	t.Helper()
	v, err := dashboard.Demo{}.View(context.Background(), "", demoClock)
	if err != nil {
		t.Fatal(err)
	}
	v.Sample, v.Agent, v.TZ = false, dashboard.AgentName, "Europe/London"
	return v
}

// unreadable is the account ownerSite takes for a session that can't be
// read.
const unreadable = "!"

// splitView is ownerView as dashboard.Rollups makes it until project
// registration (WP-SV10): each computer's projects count apart.
func splitView(t *testing.T) dashboard.View {
	t.Helper()
	v := ownerView(t)
	v.SplitByDevice = true
	return v
}

// emptyView is a new account's view: 30 days with nothing in them.
func emptyView(t *testing.T) dashboard.View {
	t.Helper()
	v, err := dashboard.Build(dashboard.Meta{Today: "2026-11-20", TZ: "UTC", Agent: dashboard.AgentName}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// zoneCall records what SetZone was asked.
type zoneCall struct{ account, zone string }

// ownerSite serves the owner's dashboard with sign-in as in, the request
// signed in to account ("" for none), src as the views, and setZone's
// answer to the form. account "!" stands for a session that can't be
// read.
func ownerSite(t *testing.T, in SignIn, account string, src dashboard.Source, sameOrigin bool, setZone error) (*http.ServeMux, *[]zoneCall) {
	t.Helper()
	var calls []zoneCall
	mux := http.NewServeMux()
	newSite(t, Options{}).RegisterDashboard(mux, in, Dashboard{
		Source: src,
		Account: func(*http.Request) (string, error) {
			switch account {
			case "":
				return "", dashboard.ErrNotSignedIn
			case unreadable:
				return "", errors.New("database is locked")
			}
			return account, nil
		},
		SetZone:    func(_ context.Context, a, z string) error { calls = append(calls, zoneCall{a, z}); return setZone },
		SameOrigin: func(*http.Request) bool { return sameOrigin },
		Now:        func() time.Time { return demoClock },
		Logger:     slog.New(slog.DiscardHandler),
	})
	return mux, &calls
}

func serve(mux *http.ServeMux, r *http.Request) (*http.Response, string) {
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, r)
	resp := rec.Result()
	body, _ := io.ReadAll(resp.Body)
	return resp, string(body)
}

func TestOwnerDashboard(t *testing.T) {
	tests := []struct {
		name         string
		signIn       SignIn
		account      string
		src          dashboard.Source
		wantCode     int
		wantLocation string
		want         []string
		notWant      []string
		wantHeaders  map[string]string
	}{
		{
			name:     "with sign-in off and sign-ups closed the page says there's no dashboard of your own, promises none, and links the sample",
			signIn:   SignIn{},
			wantCode: 200,
			want: []string{
				"Sign-in isn&#39;t set up on this server yet, so there&#39;s no dashboard of your own to show. Sign-ups are closed for now too, and testers join by invitation, using pairtime on their own computers until sign-ups open.</p>",
				`<a class="button" href="/demo">See a sample dashboard</a>`,
			},
			notWant:     []string{"<form", "Sample data"},
			wantHeaders: map[string]string{"Cache-Control": "no-store"},
		},
		{
			name:         "a visitor who isn't signed in goes to Hack Club sign-in, which brings them back to the dashboard, with return_to encoded as the device pages encode it",
			signIn:       signInOn(""),
			wantCode:     303,
			wantLocation: "/auth/hca/login?return_to=%2Fdashboard",
		},
		{
			name:     "a signed-in owner sees their own time, named as theirs and the agent's, with no sample label",
			signIn:   signInOn("U0FAKEVERIFIED"),
			account:  "u1",
			src:      fakeSource{v: ownerView(t)},
			wantCode: 200,
			want: []string{
				"<title>Your dashboard · pairtime</title>",
				"<h1>Your dashboard</h1>",
				"Your agent worked on its own for 3h 32m.",
				`<p class="face"><span>Your agent</span> <strong>2h 40m</strong>`,
				"Two clocks, not a score. Your agent&#39;s clock counts the time it worked on a project with none of your time counted there",
				`<span class="kname">Following your agent as it works</span>`,
				`<span class="agent">Your agent 15h 5m</span>`,
			},
			notWant: []string{"Sample data", "made-up", "Nothing counted yet."},
		},
		{
			name:     "the owner's split by kind says it is counted per 15-second slot, so it can be a few seconds off the app's while totals and the agent's time match (D-kind-split-by-slot)",
			signIn:   signInOn("U0FAKEVERIFIED"),
			account:  "u1",
			src:      fakeSource{v: ownerView(t)},
			wantCode: 200,
			want:     []string{kindSplitNote("your agent")},
		},
		{
			name:     "the owner's header names them by their sign-in's display name, beside a link to the dashboard and the sign-out form",
			signIn:   signInOn("Robin"),
			account:  "u1",
			src:      fakeSource{v: ownerView(t)},
			wantCode: 200,
			want: []string{
				`<a href="/dashboard">Your dashboard</a>`,
				"<p>Signed in as <strong>Robin</strong></p>",
				`<form method="post" action="/auth/logout"><button type="submit">Sign out</button></form>`,
			},
		},
		{
			name:     "an owner whose sign-in brought no display name gets no id in the header in its place, only the link to the dashboard and the sign-out button",
			signIn:   signedInNoName(),
			account:  "u1",
			src:      fakeSource{v: ownerView(t)},
			wantCode: 200,
			want: []string{
				`<a href="/dashboard">Your dashboard</a>` + "\n" + `<form method="post" action="/auth/logout"><button type="submit">Sign out</button></form>`,
			},
			notWant: []string{"Signed in"},
		},
		{
			name:     "the owner can set the time zone the days are counted in",
			signIn:   signInOn("U0FAKEVERIFIED"),
			account:  "u1",
			src:      fakeSource{v: ownerView(t)},
			wantCode: 200,
			want: []string{
				"pairtime counts your days in <strong>Europe/London</strong> time.",
				`<form class="zone" method="post" action="/dashboard/zone">`,
				`<input id="zone-name" name="zone" value="Europe/London" required maxlength="64" autocomplete="off" spellcheck="false">`,
				"days already counted stay as they are.",
			},
			wantHeaders: map[string]string{"Cache-Control": "no-store", "Referrer-Policy": "same-origin"},
		},
		{
			name:     "an owner with no time yet is told how to link the app, instead of empty charts",
			signIn:   signInOn("U0FAKEVERIFIED"),
			account:  "u1",
			src:      fakeSource{v: emptyView(t)},
			wantCode: 200,
			want: []string{
				"<strong>Nothing counted yet.</strong> Install pairtime on the computer where you run your agent, then run <code>pairtime login</code> on it to link it to this account.",
				`<pre class="install"><code>curl -fsSL `,
				`<pre class="install"><code>irm `,
				`<a href="/demo">See a sample dashboard</a>`,
				`<form class="zone"`,
			},
			notWant: []string{`<ol class="bars">`, `<section aria-labelledby="today">`, "Sample data"},
		},
		{
			name:     "a view that doesn't load gets an error, not a dashboard with wrong numbers",
			signIn:   signInOn("U0FAKEVERIFIED"),
			account:  "u1",
			src:      fakeSource{err: context.DeadlineExceeded},
			wantCode: 500,
			want:     []string{"Your dashboard didn't load."},
		},
		{
			name:     "a session that can't be read gets an error, not a trip to sign-in, which wouldn't help",
			signIn:   signInOn("U0FAKEVERIFIED"),
			account:  unreadable,
			src:      fakeSource{v: ownerView(t)},
			wantCode: 500,
			want:     []string{"Your dashboard didn't load."},
		},
		{
			name:     "while each computer's projects count apart, the projects table says a repo on two computers shows as two projects",
			signIn:   signInOn("U0FAKEVERIFIED"),
			account:  "u1",
			src:      fakeSource{v: splitView(t)},
			wantCode: 200,
			want:     []string{`<p class="note">For now, a repo you work on from two computers shows as two projects here, one for each computer.</p>`},
		},
		{
			name:     "a view that doesn't split projects by computer says nothing about it",
			signIn:   signInOn("U0FAKEVERIFIED"),
			account:  "u1",
			src:      fakeSource{v: ownerView(t)},
			wantCode: 200,
			notWant:  []string{"two computers"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mux, _ := ownerSite(t, tt.signIn, tt.account, tt.src, true, nil)
			resp, page := serve(mux, httptest.NewRequest(http.MethodGet, "/dashboard", nil))
			if resp.StatusCode != tt.wantCode {
				t.Fatalf("GET /dashboard = %d, want %d", resp.StatusCode, tt.wantCode)
			}
			if got := resp.Header.Get("Location"); got != tt.wantLocation {
				t.Errorf("Location = %q, want %q", got, tt.wantLocation)
			}
			for _, w := range tt.want {
				if !strings.Contains(page, w) {
					t.Errorf("the page doesn't contain %q", w)
				}
			}
			for _, w := range tt.notWant {
				if strings.Contains(page, w) {
					t.Errorf("the page contains %q", w)
				}
			}
			for h, want := range tt.wantHeaders {
				if got := resp.Header.Get(h); got != want {
					t.Errorf("%s = %q, want %q", h, got, want)
				}
			}
		})
	}
}

func TestOwnerDashboardCSP(t *testing.T) {
	s := newSite(t, Options{})
	tests := []struct {
		name    string
		signIn  SignIn
		account string
		want    string
	}{
		{name: "the signed-in owner's page lets its forms post to this server only", signIn: signInOn("U0FAKEVERIFIED"), account: "u1", want: s.cspForm},
		{name: "with sign-in off the page has no form, and its CSP allows none", signIn: SignIn{}, want: s.csp},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mux, _ := ownerSite(t, tt.signIn, tt.account, fakeSource{v: ownerView(t)}, true, nil)
			resp, page := serve(mux, httptest.NewRequest(http.MethodGet, "/dashboard", nil))
			if got := resp.Header.Get("Content-Security-Policy"); got != tt.want {
				t.Errorf("CSP %q, want %q", got, tt.want)
			}
			if m := fetches.FindString(page); m != "" {
				t.Errorf("the page has %q, which makes a browser fetch something", m)
			}
			for _, m := range formAction.FindAllStringSubmatch(page, -1) {
				if !strings.HasPrefix(m[1], "/") || strings.HasPrefix(m[1], "//") {
					t.Errorf("a form posts to %q, which isn't a path on this server", m[1])
				}
			}
		})
	}
}

func TestZoneForm(t *testing.T) {
	tokyo, err := rollup.LoadZone("Asia/Tokyo")
	if err != nil {
		t.Fatal(err)
	}
	post := func(zone string) *http.Request {
		r := httptest.NewRequest(http.MethodPost, "/dashboard/zone", strings.NewReader("zone="+zone))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		return r
	}
	tests := []struct {
		name         string
		signIn       SignIn
		account      string
		sameOrigin   bool
		setZone      error
		body         string
		wantCode     int
		wantLocation string
		wantCalls    []zoneCall
		want         string
	}{
		{
			name:   "a zone sent from the owner's dashboard is saved for their account, trimmed, and the browser goes back to the dashboard",
			signIn: signInOn("U0FAKEVERIFIED"), account: "u1", sameOrigin: true, body: "+America%2FNew_York+",
			wantCode: 303, wantLocation: "/dashboard", wantCalls: []zoneCall{{"u1", "America/New_York"}},
		},
		{
			name:   "a zone pairtime doesn't know is refused beside the form, and the dashboard still shows",
			signIn: signInOn("U0FAKEVERIFIED"), account: "u1", sameOrigin: true, body: "Mars", setZone: fmt.Errorf("rollup: %q: %w", "Mars", rollup.ErrBadZone),
			wantCode: 422, wantCalls: []zoneCall{{"u1", "Mars"}}, want: `<p class="error" role="alert">That isn&#39;t a time zone pairtime knows.`,
		},
		{
			name:   "a second change inside 30 days is refused beside the form, with the dates of the last change and of the next one allowed as the refusal gives them in the account's zone",
			signIn: signInOn("U0FAKEVERIFIED"), account: "u1", sameOrigin: true, body: "Europe%2FParis",
			setZone: &rollup.ZoneTooSoonError{
				Changed: time.Date(2026, 11, 21, 8, 30, 0, 0, tokyo),
				Next:    time.Date(2026, 12, 21, 8, 30, 0, 0, tokyo),
			},
			wantCode: 422, wantCalls: []zoneCall{{"u1", "Europe/Paris"}},
			want: `<p class="error" role="alert">You changed your time zone on 21 November 2026. You can change it again from 21 December 2026.</p>`,
		},
		{
			name:   "a zone that doesn't save for another reason is an error, not a refusal",
			signIn: signInOn("U0FAKEVERIFIED"), account: "u1", sameOrigin: true, body: "Europe%2FParis", setZone: context.DeadlineExceeded,
			wantCode: 500, wantCalls: []zoneCall{{"u1", "Europe/Paris"}},
		},
		{
			name:   "a post that doesn't come from this server's pages is refused, and nothing is saved",
			signIn: signInOn("U0FAKEVERIFIED"), account: "u1", sameOrigin: false, body: "UTC",
			wantCode: 403,
		},
		{
			name:   "a post that isn't signed in goes to sign-in, and nothing is saved",
			signIn: signInOn(""), sameOrigin: true, body: "UTC",
			wantCode: 303, wantLocation: "/auth/hca/login?return_to=%2Fdashboard",
		},
		{
			name:   "a post whose session can't be read is an error, not a trip to sign-in, and nothing is saved",
			signIn: signInOn("U0FAKEVERIFIED"), account: unreadable, sameOrigin: true, body: "UTC",
			wantCode: 500,
		},
		{
			name:   "with sign-in off the form saves nothing and goes back to the dashboard",
			signIn: SignIn{}, sameOrigin: true, body: "UTC",
			wantCode: 303, wantLocation: "/dashboard",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mux, calls := ownerSite(t, tt.signIn, tt.account, fakeSource{v: ownerView(t)}, tt.sameOrigin, tt.setZone)
			resp, page := serve(mux, post(tt.body))
			if resp.StatusCode != tt.wantCode {
				t.Fatalf("POST /dashboard/zone = %d, want %d", resp.StatusCode, tt.wantCode)
			}
			if got := resp.Header.Get("Location"); got != tt.wantLocation {
				t.Errorf("Location = %q, want %q", got, tt.wantLocation)
			}
			if !reflect.DeepEqual(*calls, tt.wantCalls) {
				t.Errorf("SetZone calls %v, want %v", *calls, tt.wantCalls)
			}
			if tt.want != "" && !strings.Contains(page, tt.want) {
				t.Errorf("the page doesn't contain %q", tt.want)
			}
		})
	}
}

func TestCapitalize(t *testing.T) {
	tests := []struct{ name, in, want string }{
		{name: "a lower-case first letter goes up", in: "your agent", want: "Your agent"},
		{name: "a name that starts in upper case stays", in: "Claude Code", want: "Claude Code"},
		{name: "nothing stays nothing", in: "", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := capitalize(tt.in); got != tt.want {
				t.Errorf("capitalize(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// manyView is an owner's view of ten projects, p1 the busiest over the
// month and p10 the quietest, with today's time in the projects week
// names, and today's agent-only time, and none of the owner's, in those
// agentWeek names.
func manyView(t *testing.T, agentWeek []string, week ...string) dashboard.View {
	t.Helper()
	var rows []dashboard.Row
	for i := 1; i <= 10; i++ {
		key := "p" + strconv.Itoa(i)
		// Earlier in the month, more for the lower numbers.
		rows = append(rows, dashboard.Row{Day: "2026-11-01", Project: key, Kind: "prompting", Level: "A", Seconds: float64((11 - i) * 3600)})
	}
	for _, key := range week {
		rows = append(rows, dashboard.Row{Day: "2026-11-20", Project: key, Kind: "prompting", Level: "A", Seconds: 1800})
	}
	for _, key := range agentWeek {
		rows = append(rows, dashboard.Row{Day: "2026-11-20", Project: key, Agent: true, Seconds: 2400})
	}
	v, err := dashboard.Build(dashboard.Meta{Today: "2026-11-20", TZ: "UTC", Agent: dashboard.AgentName}, rows)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestManyProjects(t *testing.T) {
	tests := []struct {
		name      string
		week      []string
		agentWeek []string
		section   string
		want      []string
		notWant   []string
	}{
		{
			name:    "past eight projects in a week, the chart draws the rest together as Other, in its own gray, and never a ninth hue",
			week:    []string{"p1", "p2", "p3", "p4", "p5", "p6", "p7", "p8", "p9", "p10"},
			section: "week",
			want: []string{
				`<li><span class="key c8"></span>p8</li>`,
				`<li><span class="key co"></span>Other</li>`,
				`title="Other, 1h"`,
			},
			notWant: []string{`<li><span class="key co"></span>p9</li>`, `<span class="key c9">`},
		},
		{
			name:    "the table still lists each project past the eighth by name",
			week:    []string{"p1", "p2", "p3", "p4", "p5", "p6", "p7", "p8", "p9", "p10"},
			section: "week",
			want:    []string{"<th>p9</th><th>p10</th><th>Total</th>"},
		},
		{
			name:    "a project that is the week's only one gets a color of its own, though the month ranks it sixth",
			week:    []string{"p6"},
			section: "week",
			want:    []string{`<li><span class="key c1"></span>p6</li>`, `title="p6, 30m"`},
			notWant: []string{"Other"},
		},
		{
			name:      "projects with only the agent's time this week don't take the first colors, so the week's one project with the owner's time gets c1 though eight agent-only projects rank above it",
			week:      []string{"p9"},
			agentWeek: []string{"p1", "p2", "p3", "p4", "p5", "p6", "p7", "p8"},
			section:   "week",
			want:      []string{`<li><span class="key c1"></span>p9</li>`, `title="p9, 30m"`},
			notWant:   []string{"Other"},
		},
		{
			name:    "a project keeps its color in every section: the week's only project is c1 in the projects table too",
			week:    []string{"p6"},
			section: "projects",
			want:    []string{`<tr role="row"><td role="cell"><span class="key c1"></span>p6</td>`, `<tr role="row"><td role="cell"><span class="key c2"></span>p1</td>`},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mux, _ := ownerSite(t, signInOn("U0FAKEVERIFIED"), "u1", fakeSource{v: manyView(t, tt.agentWeek, tt.week...)}, true, nil)
			_, page := serve(mux, httptest.NewRequest(http.MethodGet, "/dashboard", nil))
			if page = sectionOf(page, tt.section); page == "" {
				t.Fatalf("the page has no section %q", tt.section)
			}
			for _, w := range tt.want {
				if !strings.Contains(page, w) {
					t.Errorf("the section doesn't contain %q", w)
				}
			}
			for _, w := range tt.notWant {
				if strings.Contains(page, w) {
					t.Errorf("the section contains %q", w)
				}
			}
		})
	}
}
