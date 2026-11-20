// SPDX-License-Identifier: AGPL-3.0-or-later

package pages

import (
	"context"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/server/dashboard"
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
			name:    "the page has every section: today, the last 7 days, projects, evidence levels and activity kinds",
			signIn:  signInOn(""),
			want:    []string{`<h2 id="today">Today <span class="when">Friday 20 November</span></h2>`, `<h2 id="week">Last 7 days</h2>`, `<h2 id="projects">Projects</h2>`, `<h2 id="levels">Evidence levels</h2>`, `<h2 id="kinds">What you spent it on</h2>`},
			notWant: []string{"Nothing counted"},
		},
		{
			name:    "today leads with your time, and says the agent's time and what is still pending",
			signIn:  signInOn(""),
			section: "today",
			want:    []string{`<p class="hero"><strong>1h 53m</strong> of your time</p>`, "Claude Code ran for 3h 32m. 9m of your time is still pending."},
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
			want:    []string{"Two clocks, not a score.", "there&#39;s no right ratio between them."},
			notWant: []string{"%", "goal", "target", `class="bar`, `class="seg`},
		},
		{
			name:    "the last 7 days are columns stacked by project on an axis to 4h, oldest first, ending today",
			signIn:  signInOn(""),
			section: "week",
			want: []string{
				"<strong>12h 13m</strong> of your time. Claude Code ran for 21h 2m.",
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
			name:    "today's pending minutes sit lighter on top of their project's part, and the legend says what lighter means",
			signIn:  signInOn(""),
			section: "week",
			want: []string{
				`<span class="seg c1 f26" title="chip8-emulator, 1h 3m"></span><span class="seg c1 pend f4" title="chip8-emulator, 9m pending"></span><span class="seg c2 f17" title="robotics-scouting, 41m"></span><small class="cap">1h 53m</small>`,
				`<li><span class="key c1 pend"></span>Lighter means pending</li>`,
				"Pending minutes are from the last 15 minutes.",
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
				`<tr><td><span class="key c1"></span>chip8-emulator</td><td><strong>6h 59m</strong> <span class="agent">Claude Code 15h 5m</span></td><td><strong>18h 34m</strong> <span class="agent">Claude Code 39h 25m</span></td></tr>`,
				`<tr><td><span class="key c2"></span>robotics-scouting</td><td><strong>3h 51m</strong> <span class="agent">Claude Code 5h 7m</span></td><td><strong>10h 6m</strong> <span class="agent">Claude Code 13h 32m</span></td></tr>`,
				`<tr><td><span class="key c3"></span>portfolio-site</td><td><strong>1h 23m</strong> <span class="agent">Claude Code 50m</span></td><td><strong>5h 3m</strong> <span class="agent">Claude Code 3h 10m</span></td></tr>`,
				`<tr><th>All projects</th><td><strong>12h 13m</strong> <span class="agent">Claude Code 21h 2m</span></td><td><strong>33h 43m</strong> <span class="agent">Claude Code 56h 7m</span></td></tr>`,
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
			name:    "without sign-in the banner says sign-in isn't open yet, and how a visitor would get a dashboard",
			signIn:  SignIn{},
			want:    []string{"Sign-in isn&#39;t open on this server yet. Once it is, you sign in with Hack Club, run <code>pairtime login</code> on your computer, and your own dashboard shows your time the way this one does."},
			notWant: []string{"Sign in with Hack Club</a>", "Sign-ups are closed"},
		},
		{
			name:    "with sign-in but sign-ups closed the banner says testers join by invitation, and offers no sign-in",
			signIn:  signInOn(""),
			want:    []string{"Sign-ups are closed for now, and testers join by invitation."},
			notWant: []string{"Sign in with Hack Club</a>", "isn&#39;t open"},
		},
		{
			name:    "with sign-ups open the banner offers Hack Club sign-in",
			opts:    Options{SignupsOpen: true},
			signIn:  signInOn(""),
			want:    []string{`<a class="button" href="/auth/hca/login">Sign in with Hack Club</a> and run <code>pairtime login</code>`},
			notWant: []string{"Sign-ups are closed", "isn&#39;t open"},
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
		{name: "the page changes every day, so a cache must ask again", header: "Cache-Control", want: "no-cache"},
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
	v, err := dashboard.Demo{}.View(context.Background(), 0, demoClock)
	if err != nil {
		t.Fatal(err)
	}
	_, page := getDemo(t, Options{}, signInOn(""))
	today := v.Days[len(v.Days)-1]
	tests := []struct {
		name, section, want string
	}{
		{name: "the hero is today's time", section: "today", want: "<strong>" + duration(today.You.Total()) + "</strong> of your time"},
		{name: "the agent's time today is the view's", section: "today", want: "Claude Code ran for " + duration(today.Agent.Total()) + "."},
		{name: "the week's time is the view's", section: "week", want: "<strong>" + duration(v.Week.You.Total()) + "</strong> of your time. Claude Code ran for " + duration(v.Week.Agent.Total()) + "."},
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
	for _, p := range []struct{ name, page string }{
		{name: "the sample dashboard", page: demo},
		{name: "the home page", page: home},
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
		{name: "a level with no time gets 0", values: []float64{5, 0, 0}, want: []int{100, 0, 0}},
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
