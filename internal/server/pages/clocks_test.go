// SPDX-License-Identifier: AGPL-3.0-or-later

package pages

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/server/dashboard"
	"github.com/some-du6e/pairtime/internal/server/rollup"
)

// noonClock is when the tests look at dashboard.Unattended: twelve hours
// into its day.
var noonClock = time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)

// whySentence is the sentence that says why none of the time is yours,
// for the owner's agent.
const whySentence = "pairtime counts your time from your typing and mouse on the computer where your agent runs, so when it runs on a server you reach over SSH or from a remote app, its time shows here and yours doesn&#39;t."

// getOwner serves GET /dashboard for an owner signed in as in whose view
// is v, and returns the page.
func getOwner(t *testing.T, in SignIn, v dashboard.View) string {
	t.Helper()
	mux, _ := ownerSite(t, in, "u1", fakeSource{v: v}, true, nil)
	resp, page := serve(mux, httptest.NewRequest(http.MethodGet, "/dashboard", nil))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /dashboard = %d, want 200:\n%s", resp.StatusCode, page)
	}
	return page
}

// unattendedView is dashboard.Unattended at noon.
func unattendedView(t *testing.T) dashboard.View {
	t.Helper()
	v, err := dashboard.Unattended{}.View(context.Background(), "", noonClock)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// timeRow is one row of a dayView: seconds of your time, or of the
// agent's when agent is set, in a project on the day ago days before
// today.
type timeRow struct {
	project string
	ago     int
	agent   bool
	s       float64
}

// dayView is an owner's view read at asOf in zone tz, with the agent
// named agent ("" for AgentName), from rows.
func dayView(t *testing.T, asOf time.Time, tz, agent string, rows ...timeRow) dashboard.View {
	t.Helper()
	loc, err := rollup.LoadZone(tz)
	if err != nil {
		t.Fatal(err)
	}
	if agent == "" {
		agent = dashboard.AgentName
	}
	local := asOf.In(loc)
	today := time.Date(local.Year(), local.Month(), local.Day(), 12, 0, 0, 0, time.UTC)
	var rs []dashboard.Row
	for _, r := range rows {
		row := dashboard.Row{Day: today.AddDate(0, 0, -r.ago).Format(dashboard.DateLayout), Project: r.project, Seconds: r.s, Agent: r.agent}
		if !r.agent {
			row.Kind, row.Level = "prompting", "A"
		}
		rs = append(rs, row)
	}
	v, err := dashboard.Build(dashboard.Meta{AsOf: asOf, TZ: tz, Today: today.Format(dashboard.DateLayout), Agent: agent}, rs)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// agentOnly is n projects, p1 to pn, each with less of the agent's time
// today than the one before and none of yours.
func agentOnly(n int) []timeRow {
	var rows []timeRow
	for i := 1; i <= n; i++ {
		rows = append(rows, timeRow{project: "p" + strconv.Itoa(i), agent: true, s: float64(20-i) * 600})
	}
	return rows
}

// shownPart and foldedPart split today's section at the row the quieter
// projects fold into.
func shownPart(today string) string {
	s, _, _ := strings.Cut(today, `<details class="more">`)
	return s
}

func foldedPart(today string) string {
	_, s, _ := strings.Cut(today, `<details class="more">`)
	return s
}

func TestTodayClocks(t *testing.T) {
	at := time.Date(2026, 11, 20, 18, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		v    func(*testing.T) dashboard.View
		// part picks the part of today's section to look in.
		part    func(string) string
		want    []string
		notWant []string
		// order lists strings that must appear in this order.
		order []string
	}{
		{
			name: "today's clocks go the most time first, yours and the agent's together, whatever the month's order",
			v: func(t *testing.T) dashboard.View {
				return dayView(t, at, "UTC", "",
					timeRow{project: "p1", ago: 9, s: 5 * 3600}, timeRow{project: "p1", s: 600},
					timeRow{project: "p2", agent: true, s: 7200},
					timeRow{project: "p3", s: 1800}, timeRow{project: "p3", agent: true, s: 1800})
			},
			order: []string{"</span>p2</p>", "</span>p3</p>", "</span>p1</p>"},
		},
		{
			name:    "a project with none of your time today shows the agent's clock alone, never a big 0m",
			v:       unattendedView,
			want:    []string{"<li>\n" + `<p class="clock-name" title="g889f4cadffe2f9a5444229db62ed33c6"><span class="key c1"></span>Project 889f4c</p>` + "\n" + `<p class="face"><span>Your agent</span> <strong>15h 5m</strong></p>` + "\n</li>"},
			notWant: []string{"<span>You</span>", "<strong>0m</strong></p>\n</li>"},
		},
		{
			name: "a project with none of the agent's time today shows your clock alone",
			v: func(t *testing.T) dashboard.View {
				return dayView(t, at, "UTC", "", timeRow{project: "p1", s: 1800})
			},
			want:    []string{`<p class="face"><span>You</span> <strong>30m</strong></p>` + "\n</li>"},
			notWant: []string{"<span>Your agent</span>"},
		},
		{
			name: "a clock's pending minutes show on the clock they belong to",
			v:    unattendedView,
			want: []string{`<p class="face"><span>Your agent</span> <strong>8h 32m</strong> <small>3m pending</small></p>`},
		},
		{
			name: "the page shows six clocks at once",
			v:    unattendedView,
			part: shownPart,
			want: []string{"Project 889f4c", "Project ae6119", "Project 497b03", "Project 343abe", "Project ff89bc", "Project 228e8f"},
			notWant: []string{
				"Project 3da650", "Project 9b3bca", "Project 44f882",
			},
		},
		{
			name: "past six projects the rest fold into one row, which says how many there are and their time, and opens to their clocks",
			v:    unattendedView,
			part: foldedPart,
			want: []string{
				"\n<summary>3 more projects: your agent 16m</summary>\n<ul class=\"clocks\">",
				"Project 3da650", "Project 9b3bca", "Project 44f882",
				`<p class="face"><span>Your agent</span> <strong>&lt;1m</strong></p>`,
			},
			notWant: []string{"Project 889f4c"},
		},
		{
			name: "one project past six folds as 1 more project, with your time and the agent's in it",
			v: func(t *testing.T) dashboard.View {
				rows := agentOnly(6)
				return dayView(t, at, "UTC", "", append(rows, timeRow{project: "p7", s: 300}, timeRow{project: "p7", agent: true, s: 600})...)
			},
			part: foldedPart,
			want: []string{"<summary>1 more project: you 5m, your agent 10m</summary>"},
		},
		{
			name:    "six projects or fewer fold nothing",
			v:       func(t *testing.T) dashboard.View { return dayView(t, at, "UTC", "", agentOnly(6)...) },
			notWant: []string{"<details", "more project"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			page := sectionOf(getOwner(t, signInOn("Robin"), tt.v(t)), "today")
			if page == "" {
				t.Fatal("the page has no section today")
			}
			if tt.part != nil {
				page = tt.part(page)
			}
			for _, w := range tt.want {
				if !strings.Contains(page, w) {
					t.Errorf("today doesn't contain %q:\n%s", w, page)
				}
			}
			for _, w := range tt.notWant {
				if strings.Contains(page, w) {
					t.Errorf("today contains %q", w)
				}
			}
			last := -1
			for _, w := range tt.order {
				i := strings.Index(page, w)
				if i < 0 || i < last {
					t.Errorf("%q isn't after the one before it in %v:\n%s", w, tt.order, page)
				}
				last = i
			}
		})
	}
}

func TestZeroSentence(t *testing.T) {
	at := time.Date(2026, 11, 20, 18, 0, 0, 0, time.UTC)
	why := `<p class="why">` + whySentence + `</p>`
	tests := []struct {
		name               string
		v                  func(*testing.T) dashboard.View
		today, week        []string
		notToday, notWeek  []string
		wantCountOnThePage int
	}{
		{
			name:               "with none of today your time and the agent's clock running, one sentence under the big number says why, and the week doesn't say it again",
			v:                  unattendedView,
			today:              []string{`<p class="hero"><strong>0m</strong> of your time</p>` + "\n" + why + "\n" + `<p class="lede">`},
			week:               []string{"<strong>0m</strong> of your time."},
			notWeek:            []string{`class="why"`},
			wantCountOnThePage: 1,
		},
		{
			name: "with nothing on either clock today there's nothing to explain today, and the week says why its time isn't yours",
			v: func(t *testing.T) dashboard.View {
				return dayView(t, at, "UTC", "", timeRow{project: "p1", ago: 1, agent: true, s: 3600})
			},
			notToday:           []string{`class="why"`},
			week:               []string{"<strong>0m</strong> of your time. Your agent worked on its own for 1h.</p>\n" + why},
			wantCountOnThePage: 1,
		},
		{
			name: "with some of today your time there's no sentence",
			v: func(t *testing.T) dashboard.View {
				return dayView(t, at, "UTC", "", timeRow{project: "p1", s: 60}, timeRow{project: "p1", agent: true, s: 3600})
			},
			notToday: []string{`class="why"`},
			notWeek:  []string{`class="why"`},
		},
		{
			name: "with some of the week your time the week says nothing more, though today is the agent's alone",
			v: func(t *testing.T) dashboard.View {
				return dayView(t, at, "UTC", "", timeRow{project: "p1", ago: 3, s: 600}, timeRow{project: "p1", agent: true, s: 3600})
			},
			today:              []string{why},
			notWeek:            []string{`class="why"`},
			wantCountOnThePage: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			page := getOwner(t, signInOn("Robin"), tt.v(t))
			for _, c := range []struct {
				id          string
				want, avoid []string
			}{{"today", tt.today, tt.notToday}, {"week", tt.week, tt.notWeek}} {
				s := sectionOf(page, c.id)
				for _, w := range c.want {
					if !strings.Contains(s, w) {
						t.Errorf("section %s doesn't contain %q:\n%s", c.id, w, s)
					}
				}
				for _, w := range c.avoid {
					if strings.Contains(s, w) {
						t.Errorf("section %s contains %q", c.id, w)
					}
				}
			}
			if n := strings.Count(page, whySentence); n != tt.wantCountOnThePage {
				t.Errorf("the page says why %d times, want %d", n, tt.wantCountOnThePage)
			}
		})
	}
}

func TestNoneOfYourTime(t *testing.T) {
	tests := []struct {
		name    string
		v       func(*testing.T) dashboard.View
		section string
		want    []string
		notWant []string
	}{
		{
			name:    "a week with none of your time leaves out the chart and its table, which would hold only zeros",
			v:       unattendedView,
			section: "week",
			notWant: []string{`<div class="chart"`, "The last 7 days as a table"},
		},
		{
			name:    "a week with some of your time keeps them",
			v:       ownerView,
			section: "week",
			want:    []string{`<div class="chart" aria-hidden="true">`, "<summary>The last 7 days as a table</summary>"},
		},
		{
			name:    "the projects table says a window with none of your time quietly, not as a bold 0m",
			v:       unattendedView,
			section: "projects",
			want: []string{
				`<span class="key c1"></span>Project 889f4c</td><td><span class="none">0m</span> <span class="agent">Your agent 52h 5m</span></td><td><span class="none">0m</span> <span class="agent">Your agent 65h 5m</span></td></tr>`,
				`<tr><th>All projects</th><td><span class="none">0m</span> <span class="agent">`,
			},
			notWant: []string{"<strong>0m</strong>"},
		},
		{
			name:    "with none of your time in the week, the evidence levels say there's nothing to back up instead of listing each level at 0%",
			v:       unattendedView,
			section: "levels",
			want:    []string{`<p class="note">There&#39;s none of your time in the last 7 days to back up yet.</p>`, `<a href="/docs/evidence">What A, B and C mean</a>`},
			notWant: []string{`<ul class="levels">`, "0%"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := sectionOf(getOwner(t, signInOn("Robin"), tt.v(t)), tt.section)
			for _, w := range tt.want {
				if !strings.Contains(s, w) {
					t.Errorf("section %s doesn't contain %q:\n%s", tt.section, w, s)
				}
			}
			for _, w := range tt.notWant {
				if strings.Contains(s, w) {
					t.Errorf("section %s contains %q", tt.section, w)
				}
			}
		})
	}
}

func TestAgentLine(t *testing.T) {
	twoAt := func(t *testing.T, asOf time.Time, tz string, each float64) dashboard.View {
		return dayView(t, asOf, tz, "", timeRow{project: "p1", agent: true, s: each}, timeRow{project: "p2", agent: true, s: each})
	}
	tests := []struct {
		name        string
		v           func(*testing.T) dashboard.View
		today, week string
	}{
		{
			name:  "agent time within the hours the day has had reads as one agent's",
			v:     ownerView,
			today: "Your agent worked on its own for 3h 32m.",
			week:  "Your agent worked on its own for 21h 2m.",
		},
		{
			name:  "past the hours the day has had, it reads as every agent's time added up",
			v:     unattendedView,
			today: "Your agents worked on their own for 31h 5m, adding up every agent's time.",
		},
		{
			name: "it goes by the hours so far today, not a whole day: 10h by 06:00 took agents working at once",
			v: func(t *testing.T) dashboard.View {
				return twoAt(t, time.Date(2026, 11, 20, 6, 0, 0, 0, time.UTC), "UTC", 5*3600)
			},
			today: "Your agents worked on their own for 10h, adding up every agent's time.",
		},
		{
			name: "the same 10h by 23:00 reads as one agent's",
			v: func(t *testing.T) dashboard.View {
				return twoAt(t, time.Date(2026, 11, 20, 23, 0, 0, 0, time.UTC), "UTC", 5*3600)
			},
			today: "Your agent worked on its own for 10h.",
		},
		{
			name: "a device clock a few minutes fast doesn't make one agent read as several: 8m by 00:03 is within ingest's 5 minutes of skew",
			v: func(t *testing.T) dashboard.View {
				return dayView(t, time.Date(2026, 11, 20, 0, 3, 0, 0, time.UTC), "UTC", "", timeRow{project: "p1", agent: true, s: 8 * 60})
			},
			today: "Your agent worked on its own for 8m.",
		},
		{
			name: "the hours so far count from midnight in the account's zone: 11h by 10:00 in New York, though 15:00 in UTC",
			v: func(t *testing.T) dashboard.View {
				return twoAt(t, time.Date(2026, 11, 20, 15, 0, 0, 0, time.UTC), "America/New_York", 5.5*3600)
			},
			today: "Your agents worked on their own for 11h, adding up every agent's time.",
		},
		{
			name: "the week's line is a sum once one of its days had more agent time than hours, though today's isn't",
			v: func(t *testing.T) dashboard.View {
				return dayView(t, time.Date(2026, 11, 20, 18, 0, 0, 0, time.UTC), "UTC", "",
					timeRow{project: "p1", ago: 1, agent: true, s: 15 * 3600}, timeRow{project: "p2", ago: 1, agent: true, s: 15 * 3600},
					timeRow{project: "p1", agent: true, s: 3600})
			},
			today: "Your agent worked on its own for 1h.",
			week:  "Your agents worked on their own for 31h, adding up every agent's time.",
		},
		{
			name: "a day whose midnight the clocks skip is 23 hours long, so 23h 30m of agent time on it passes it: Santiago's 2027-09-05",
			v: func(t *testing.T) dashboard.View {
				return dayView(t, time.Date(2027, 9, 6, 12, 0, 0, 0, time.UTC), "America/Santiago", "",
					timeRow{project: "p1", ago: 1, agent: true, s: 11.75 * 3600}, timeRow{project: "p2", ago: 1, agent: true, s: 11.75 * 3600})
			},
			week: "Your agents worked on their own for 23h 30m, adding up every agent's time.",
		},
		{
			name: "a named agent reads as several of it",
			v: func(t *testing.T) dashboard.View {
				return dayView(t, time.Date(2026, 11, 20, 6, 0, 0, 0, time.UTC), "UTC", "Claude Code",
					timeRow{project: "p1", agent: true, s: 4 * 3600}, timeRow{project: "p2", agent: true, s: 4 * 3600})
			},
			today: "Claude Code agents worked on their own for 8h, adding up every agent's time.",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			page := getOwner(t, signInOn("Robin"), tt.v(t))
			if s := sectionOf(page, "today"); tt.today != "" && !strings.Contains(s, `<p class="lede">`+tt.today) {
				t.Errorf("today doesn't say %q:\n%s", tt.today, s)
			}
			if s := sectionOf(page, "week"); tt.week != "" && !strings.Contains(s, "of your time. "+tt.week+"</p>") {
				t.Errorf("the week doesn't say %q:\n%s", tt.week, s)
			}
		})
	}
	t.Run("the sample's numbers don't follow the clock, so its today counts as a whole day: 3h 32m by 01:00 reads as one agent's", func(t *testing.T) {
		mux := http.NewServeMux()
		newSite(t, Options{}).RegisterDemo(mux, SignIn{}, func() time.Time { return time.Date(2026, 11, 20, 1, 0, 0, 0, time.UTC) })
		_, page := serve(mux, httptest.NewRequest(http.MethodGet, "/demo", nil))
		if want := "Claude Code worked on its own for 3h 32m."; !strings.Contains(sectionOf(page, "today"), want) {
			t.Errorf("the sample's today doesn't say %q", want)
		}
	})
}

// keyClass finds the color of each clock.
var keyClass = regexp.MustCompile(`<p class="clock-name"[^>]*><span class="key ([a-z0-9]+)">`)

func TestClockColors(t *testing.T) {
	at := time.Date(2026, 11, 20, 18, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		n    int
		want []string
	}{
		{name: "one project gets the first color", n: 1, want: []string{"c1"}},
		{name: "six projects, all shown at once, get six colors of their own", n: 6, want: []string{"c1", "c2", "c3", "c4", "c5", "c6"}},
		{name: "eight projects get the palette's eight colors, the folded ones too", n: 8, want: []string{"c1", "c2", "c3", "c4", "c5", "c6", "c7", "c8"}},
		{name: "a ninth project shares Other's gray, never a ninth hue", n: 9, want: []string{"c1", "c2", "c3", "c4", "c5", "c6", "c7", "c8", "co"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			today := sectionOf(getOwner(t, signInOn("Robin"), dayView(t, at, "UTC", "", agentOnly(tt.n)...)), "today")
			var got []string
			for _, m := range keyClass.FindAllStringSubmatch(today, -1) {
				got = append(got, m[1])
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("the clocks' colors are %v, want %v", got, tt.want)
			}
		})
	}
}

func TestShownClockColors(t *testing.T) {
	// The owner typed in a1 to a3 two days ago, and today agents worked in
	// six other projects, all of whose clocks show at once.
	rows := []timeRow{{project: "a1", ago: 2, s: 600}, {project: "a2", ago: 2, s: 600}, {project: "a3", ago: 2, s: 600}}
	page := getOwner(t, signInOn("Robin"), dayView(t, time.Date(2026, 11, 20, 18, 0, 0, 0, time.UTC), "UTC", "", append(rows, agentOnly(6)...)...))
	var clocks []string
	for _, m := range keyClass.FindAllStringSubmatch(sectionOf(page, "today"), -1) {
		clocks = append(clocks, m[1])
	}
	tests := []struct {
		name string
		ok   bool
	}{
		{name: "every clock shown at once has a color of its own, though the week's own projects came first", ok: slices.Equal(clocks, []string{"c3", "c4", "c5", "c6", "c7", "c8"})},
		{name: "the week's projects keep the first colors, in the chart's order", ok: strings.Contains(sectionOf(page, "week"), `<li><span class="key c1"></span>a1</li>`+"\n"+`<li><span class="key c2"></span>a2</li>`)},
		{name: "the week's project that no color is left for folds into the chart's Other", ok: strings.Contains(sectionOf(page, "week"), `<li><span class="key co"></span>Other</li>`)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !tt.ok {
				t.Errorf("today's clocks are %v; the page:\n%s", clocks, page)
			}
		})
	}
}

func TestShownWeekClockColors(t *testing.T) {
	// The owner typed in b1 to b6 earlier this week and in b7 today, and
	// agents worked today in b7 and five projects of their own: b7 is a
	// shown clock that the week's order reaches last.
	var rows []timeRow
	for i := 1; i <= 6; i++ {
		rows = append(rows, timeRow{project: "b" + strconv.Itoa(i), ago: 3, s: float64(1200 - i)})
	}
	rows = append(rows, timeRow{project: "b7", s: 60}, timeRow{project: "b7", agent: true, s: 20000})
	page := getOwner(t, signInOn("Robin"), dayView(t, time.Date(2026, 11, 20, 18, 0, 0, 0, time.UTC), "UTC", "", append(rows, agentOnly(5)...)...))
	var clocks []string
	for _, m := range keyClass.FindAllStringSubmatch(sectionOf(page, "today"), -1) {
		clocks = append(clocks, m[1])
	}
	t.Run("a shown clock the week's order reaches last still leaves a color for every other shown clock", func(t *testing.T) {
		seen := make(map[string]bool)
		for _, c := range clocks {
			if c == "co" || seen[c] {
				t.Errorf("today's clocks are %v, want six colors of their own", clocks)
				break
			}
			seen[c] = true
		}
		if len(clocks) != 6 {
			t.Errorf("today has %d clocks, want 6", len(clocks))
		}
	})
}

func TestShortCodeTitles(t *testing.T) {
	// coded is the sample's numbers with its projects called by short
	// codes, as a real dashboard calls them.
	coded := func(t *testing.T) dashboard.View {
		v := ownerView(t)
		keys := []string{"g7c41e9a2d05b8f63e1a94c7d2b6f0e85", "gd2a8f61c94e07b35a8c1f6e29d4b7a03", "g5be03d7f18a6c92e4b07d5a1c8f3e962"}
		codes := dashboard.ShortCodes(keys)
		for i := range v.Projects {
			v.Projects[i].Keys, v.Projects[i].Code, v.Projects[i].Name = []string{keys[i]}, codes[keys[i]], dashboard.CodeName(codes[keys[i]])
		}
		return v
	}
	tests := []struct {
		name    string
		v       func(*testing.T) dashboard.View
		section string
		want    []string
		notWant []string
	}{
		{
			name:    "a clock called by its project's short code has the full key in a title",
			v:       coded,
			section: "today",
			want:    []string{`<p class="clock-name" title="g7c41e9a2d05b8f63e1a94c7d2b6f0e85"><span class="key c1"></span>Project 7c41e9</p>`},
		},
		{
			name:    "the week's legend and table head do too",
			v:       coded,
			section: "week",
			want: []string{
				`<li title="g7c41e9a2d05b8f63e1a94c7d2b6f0e85"><span class="key c1"></span>Project 7c41e9</li>`,
				`<th title="gd2a8f61c94e07b35a8c1f6e29d4b7a03">Project d2a8f6</th>`,
			},
		},
		{
			name:    "and so does the projects table",
			v:       coded,
			section: "projects",
			want:    []string{`<tr><td title="g5be03d7f18a6c92e4b07d5a1c8f3e962"><span class="key c3"></span>Project 5be03d</td>`},
		},
		{
			name:    "a project with a name has no title",
			v:       ownerView,
			section: "projects",
			want:    []string{`<tr><td><span class="key c1"></span>chip8-emulator</td>`},
			notWant: []string{"title="},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := sectionOf(getOwner(t, signInOn("Robin"), tt.v(t)), tt.section)
			for _, w := range tt.want {
				if !strings.Contains(s, w) {
					t.Errorf("section %s doesn't contain %q:\n%s", tt.section, w, s)
				}
			}
			for _, w := range tt.notWant {
				if strings.Contains(s, w) {
					t.Errorf("section %s contains %q", tt.section, w)
				}
			}
		})
	}
}

func TestInstallLines(t *testing.T) {
	readme, err := os.ReadFile("../../../README.md")
	if err != nil {
		t.Fatal(err)
	}
	page := getOwner(t, signInOn("Robin"), emptyView(t))
	tests := []struct {
		name, in, want string
	}{
		{name: "the empty dashboard gives the macOS and Linux install command", in: page, want: "<p>On macOS or Linux:</p>\n" + `<pre class="install"><code>curl -fsSL https://github.com/some-du6e/pairtime/releases/download/` + installRelease + `/install.sh | sh</code></pre>`},
		{name: "the empty dashboard gives the Windows install command", in: page, want: "<p>On Windows, in PowerShell:</p>\n" + `<pre class="install"><code>irm https://github.com/some-du6e/pairtime/releases/download/` + installRelease + `/install.ps1 | iex</code></pre>`},
		{name: "README.md gives the same macOS and Linux command", in: string(readme), want: "```sh\n" + installLines[0].Command + "\n```"},
		{name: "README.md gives the same Windows command", in: string(readme), want: "```powershell\n" + installLines[1].Command + "\n```"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !strings.Contains(tt.in, tt.want) {
				t.Errorf("it doesn't contain %q", tt.want)
			}
		})
	}
}
