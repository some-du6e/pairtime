// SPDX-License-Identifier: AGPL-3.0-or-later

package pages

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"math"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/some-du6e/pairtime/internal/server/dashboard"
	"github.com/some-du6e/pairtime/internal/server/rollup"
	"github.com/some-du6e/pairtime/pkg/projectname"
)

// The dashboard page draws a dashboard.View, a server-rendered first cut
// of WP-SV8's owner dashboard: today's chess clock per project, the last 7
// days as columns stacked by project, each project's time over 7 and 30
// days, and the last 7 days split by evidence level and by activity kind.
// /demo draws it from dashboard.Demo's fixed sample and says on the page
// that the data is made up.
//
// The chess clock is information, never a target ratio (DESIGN 3.8): the
// page shows the owner's time and the agent's side by side as two
// figures, with no bar, ratio or goal between them.
//
// The CSP allows no style attribute and the page loads nothing, so bars
// are HTML elements sized by class: style.css has f0 to f100, each setting
// --f to that percentage, and each chart reads --f as a height or a width.
// Every number a chart draws is on the page as text too, in a label, a
// legend or a table, and every bar segment names its project and time in
// a title, so nothing rests on color or hover alone.

// colorSlots is how many projects get a color of their own: --c1 to --c8
// in style.css, the eight slots of a categorical palette whose neighbors
// in slot order pass the color-vision checks, in light and dark mode. Two
// slots further apart can look alike to a color-blind reader, as no eight
// hues can all be told apart, so a color is never all a project has:
// every swatch has its name beside it, and every bar its title and a
// table. The projects after them share --co and are drawn together as
// Other, never in a ninth hue.
const colorSlots = 8

// shownClocks is how many of today's projects get a chess clock on the
// page at once, the most time first. The rest fold into one row that
// opens to show theirs.
const shownClocks = 6

// otherName is what the week chart calls the projects past colorSlots.
const otherName = "Other"

// minPrinted is the smallest share, in percent, that the evidence bar
// prints inside its block.
const minPrinted = 14

// demoDescription is the sample's meta description, which link previews
// show.
const demoDescription = "A sample pairtime dashboard: a made-up teen's time building with Claude Code, and the time Claude Code ran."

// installRelease is the release whose install lines the empty dashboard
// shows. Its notes give the same two commands, and so does README.md,
// which TestInstallLines holds them to. It is the release the server
// serves at /install.sh (ScriptsRelease), which TestDashboardFollowsThePin
// holds it to while the pin is on, so the PR that moves the pin after a
// release moves all three.
const installRelease = "v0.1.0-alpha.6"

// installLines are the commands that install pairtime, as the release's
// notes and README.md give them. Each runs the release's own install
// script, which checks the release's signature first and asks before it
// changes anything.
var installLines = []installLine{
	{Where: "On macOS or Linux:", Command: "curl -fsSL " + SourceURL + "/releases/download/" + installRelease + "/install.sh | sh"},
	{Where: "On Windows, in PowerShell:", Command: "irm " + SourceURL + "/releases/download/" + installRelease + "/install.ps1 | iex"},
}

// levelText says what each evidence level means, in the home page's words
// (home.md) and DESIGN 6.4's sense.
var levelText = map[string]string{
	"A": "Your keyboard backs it up.",
	"B": "Your computer saw you there but can't tell whether you were typing.",
	"C": "pairtime can check it less closely, like time it reads back later from your tools' history.",
}

// kindName names an activity kind (SM 5.1, 5.2) for the page. agent is
// the agent's name.
func kindName(kind, agent string) string {
	switch kind {
	case "prompting":
		return "Writing prompts"
	case "reading_output":
		return "Reading " + agent + "'s output"
	case "supervising":
		return "Following " + agent + " as it works"
	case "reviewing":
		return "Reviewing and deciding"
	case "testing":
		return "Testing"
	case "debugging":
		return "Running commands"
	case "hand_edit":
		return "Editing by hand"
	case "lookup":
		return "Looking things up"
	}
	// A kind from a newer scoring version.
	return strings.ReplaceAll(kind, "_", " ")
}

// dashPage is what dashboard.html reads. Every time is already written out
// as hours and minutes.
type dashPage struct {
	Sample bool
	// SignIn and SignupsOpen say what the sample's banner tells a visitor
	// about getting a dashboard of their own, and Login is where sign-in
	// starts.
	SignIn, SignupsOpen bool
	Login               string
	// SignedIn says the visitor is signed in, so the sample's banner links
	// their own dashboard instead of offering sign-in.
	SignedIn bool
	// Owner is set on a signed-in account's own dashboard, and Empty when
	// it has no time in the last 30 days yet, which shows how to link the
	// app instead of empty charts.
	Owner, Empty bool
	// ETag is the owner's view's dashboard.ETag, on #live, which the
	// page's script polls the snapshot with (WP-SV8); "" on the sample.
	ETag string
	// SplitByDevice says a repo used on two computers shows as two
	// projects (dashboard.View.SplitByDevice).
	SplitByDevice bool
	// Zone is the IANA time zone the account's days are counted in, and
	// ZoneError says why the last change to it was refused.
	Zone, ZoneError string
	// ZonePath is where the time zone form posts, and HidePath where a
	// project's "Hide this name" button does, "" on a page that offers
	// none.
	ZonePath, HidePath string
	// Agent names the agent in a sentence, and AgentStart at its start or
	// alone, as in "Your agent worked on its own for 2h".
	Agent, AgentStart string
	// Install says how to install pairtime, for the empty state.
	Install  []installLine
	Today    todayPart
	Week     weekPart
	Projects []projectRow
	// AllProjects totals the projects.
	AllProjects projectRow
	Levels      levelsPart
	Kinds       []kindRow
}

type todayPart struct {
	// Date is today, such as "Friday 20 November".
	Date string
	You  string
	// Why says why none of the day is your time when the agent's clock
	// ran, or is "" (zeroWhy).
	Why string
	// AgentLine says how long the agent worked on its own (agentLine).
	AgentLine string
	// YouPending is how much of You is pending, or "" for none.
	YouPending string
	// Clocks are the projects' chess clocks shown at once, and More the
	// row the rest fold into, nil when there are none.
	Clocks []clockRow
	More   *moreClocks
}

// clockRow is one project's chess clock: a face for each clock that ran
// in the project today, so a project with none of your time shows the
// agent's alone, never a big 0m.
type clockRow struct {
	Color, Name string
	// Title is the project's full key when the page calls it by its short
	// code, or "".
	Title string
	Faces []faceRow
}

// faceRow is one clock of a project: You, or the agent.
type faceRow struct {
	Label, Time string
	// Pending is "" when nothing is pending.
	Pending string
}

// moreClocks is the row today's quieter projects fold into: Summary says
// how many and how much time they hold, and opening it shows their
// clocks.
type moreClocks struct {
	Summary string
	Clocks  []clockRow
}

// installLine is one way to install pairtime: where it works, and the
// command.
type installLine struct{ Where, Command string }

type weekPart struct {
	You string
	// AgentLine and Why say what todayPart's do, for the last 7 days.
	AgentLine, Why string
	// Chart says the week has some of your time for the chart to draw.
	// Without any, the page leaves out the chart and its table, which
	// would hold nothing but zeros.
	Chart      bool
	Ticks      []tick
	Columns    []column
	Legend     []legendItem
	HasPending bool
	Table      weekTable
}

// tick is a label on the week chart's axis: At is its class, at25 to
// at100, and Label its time.
type tick struct{ At, Label string }

// column is one day of the week chart.
type column struct {
	// Dow and Dom are the weekday and day of the month under the column,
	// such as "Mon" and "16"; today's are "Today" and "".
	Dow, Dom string
	Today    bool
	// Total is the day's time over the column, "" for none.
	Total string
	// Segments run from the bottom of the column up.
	Segments []segment
}

// segment is a part of a column or bar: Class sets its color and size,
// and Title names what it counts.
type segment struct{ Class, Title string }

// legendItem names a project of the week chart. Title is the project's
// full key when the page calls it by its short code, or "".
type legendItem struct{ Color, Name, Title string }

// weekTable is the week chart's numbers as a table.
type weekTable struct {
	Names []nameCell
	Rows  []weekRow
}

// nameCell heads a project's column: Title is its full key when the page
// calls it by its short code, or "".
type nameCell struct{ Name, Title string }

type weekRow struct {
	Day   string
	Cells []string
	Total string
}

// projectRow is a row of the projects table: its color, "" for a project
// past colorSlots, which the table shows with no key rather than Other's
// gray, its name, and Title, the project's full key when the page calls it
// by its short code, its short code's name when the page calls it by the
// owner's label, or "". Hide says the row offers its owner a button to
// hide the project's name, which posts ID.
type projectRow struct {
	ID                 string
	Hide               bool
	Color, Name, Title string
	Week, Month        windowCell
}

// windowCell is one window's time in the projects table. A window with
// none of your time shows the agent's alone, as a clock does, and one with
// no time at all a quiet 0m.
type windowCell struct {
	// Label heads the cell's column, which a narrow screen prints in it.
	Label string
	// You and Agent are the window's times; Agent comes with the agent's
	// name, as "Your agent 52h 5m".
	You, Agent         string
	YouNone, AgentNone bool
}

// newCell lays out a window's cell from your time and the agent's.
func newCell(label, agentStart string, you, agentS float64) windowCell {
	return windowCell{
		Label: label, You: duration(you), Agent: agentStart + " " + duration(agentS),
		YouNone: you == 0, AgentNone: agentS == 0,
	}
}

type levelsPart struct {
	// None says the window has none of your time, so there is nothing to
	// split and the page says so instead of listing every level at 0%.
	None bool
	// Blocks are the segmented bar's parts, the levels with time.
	Blocks []levelBlock
	// Label is the bar's accessible name.
	Label string
	Items []levelItem
}

type levelBlock struct {
	Class, Title string
	// Share is printed in the block when Printed is set, that is when the
	// block is wide enough to hold it.
	Share   string
	Printed bool
}

type levelItem struct {
	Level, Swatch, Text, Time, Share string
}

type kindRow struct {
	Name, Time string
	Bars       []segment
}

// newDashPage lays v out for the page.
func newDashPage(v dashboard.View, in SignIn, signupsOpen bool) (dashPage, error) {
	return newDashPageFor(v, in, signupsOpen, false)
}

// newDashPageFor lays v out for a visitor who is signed in or not.
func newDashPageFor(v dashboard.View, in SignIn, signupsOpen, signedIn bool) (dashPage, error) {
	if len(v.Days) < dashboard.WeekDays {
		return dashPage{}, fmt.Errorf("pages: the view has %d days, fewer than a week", len(v.Days))
	}
	agent := v.Agent
	if agent == "" {
		agent = dashboard.AgentName
	}
	p := dashPage{
		Sample: v.Sample, SignIn: in.Enabled, SignupsOpen: signupsOpen, SignedIn: signedIn, Agent: agent, AgentStart: capitalize(agent),
		Owner: !v.Sample, Empty: !v.Sample && v.Month.You.Total() == 0 && v.Month.Agent.Total() == 0, SplitByDevice: v.SplitByDevice,
		Zone: v.TZ, ZonePath: ZonePath, Install: installLines,
	}
	if in.LoginPath != "" {
		// Sign-in brings the visitor to their own dashboard.
		p.Login = LoginURL(in.LoginPath, DashboardPath)
	}
	today := v.Days[len(v.Days)-1]
	date, err := time.Parse(dashboard.DateLayout, today.Date)
	if err != nil {
		return dashPage{}, fmt.Errorf("pages: %w", err)
	}

	// Today's clocks go the most time first, yours and the agent's
	// together, and a tie keeps the view's order.
	clocks := slices.Clone(today.Projects)
	slices.SortStableFunc(clocks, func(a, b dashboard.ProjectTime) int {
		return cmp.Compare(b.You.Total()+b.Agent.Total(), a.You.Total()+a.Agent.Total())
	})

	// Colors go to the projects with the owner's time this week first, in
	// the view's order, the order the week chart stacks them in, so its
	// neighbors are mostly neighbors in the palette too. But the clocks
	// the page shows at once always get colors of their own: the week's
	// projects that aren't among them stop short of the slots those still
	// need, and fold into the chart's Other past that. Today's other
	// projects come next, the most time first, then the rest. Each project
	// keeps its color across the page.
	inWeek := make(map[string]bool, len(v.Week.Projects))
	for _, pt := range v.Week.Projects {
		if pt.You.Total() > 0 {
			inWeek[pt.Project] = true
		}
	}
	shown := make(map[string]bool, shownClocks)
	for _, pt := range clocks[:min(len(clocks), shownClocks)] {
		shown[pt.Project] = true
	}
	shownLeft := len(shown) // shown clocks without a color yet
	color := make(map[string]string, len(v.Projects))
	give := func(key string) {
		if _, ok := color[key]; ok {
			return
		}
		color[key] = "co"
		if n := len(color); n <= colorSlots {
			color[key] = "c" + strconv.Itoa(n)
		}
		if shown[key] {
			shownLeft--
		}
	}
	for _, pr := range v.Projects {
		// A week's project that isn't shown at once takes a slot only
		// while every shown clock still without one has one left.
		if inWeek[pr.ID] && (shown[pr.ID] || len(color) < colorSlots-shownLeft) {
			give(pr.ID)
		}
	}
	for _, pt := range clocks {
		give(pt.Project)
	}
	for _, pr := range v.Projects {
		give(pr.ID)
	}
	// A project the page calls by the name its owner turned on has its
	// short code's name in a title, and one the page calls by its short
	// code has its full key there, so the owner can match either with what
	// pairtime projects says on their computer.
	names := make(map[string]string, len(v.Projects))
	titles := make(map[string]string, len(v.Projects))
	named := make(map[string]bool, len(v.Projects))
	for _, pr := range v.Projects {
		names[pr.ID], named[pr.ID] = pr.Name, pr.Named
		switch {
		case pr.Code == "":
		case pr.Named:
			titles[pr.ID] = projectname.CodeName(pr.Code)
		case len(pr.Keys) > 0:
			titles[pr.ID] = pr.Keys[0]
		default:
			titles[pr.ID] = pr.ID
		}
	}

	ran := newDayClock(v)
	week := v.Days[len(v.Days)-dashboard.WeekDays:]
	p.Today = todayPart{
		Date: date.Format("Monday 2 January"), You: duration(today.You.Total()), YouPending: pendingOf(today.You),
		AgentLine: agentLine(agent, today.Agent.Total(), ran.atOnce(today, true)),
	}
	if today.You.Total() == 0 && today.Agent.Total() > 0 {
		p.Today.Why = zeroWhy(agent)
	}
	var moreYou, moreAgent float64
	for i, pt := range clocks {
		row := clockRow{Color: color[pt.Project], Name: names[pt.Project], Title: titles[pt.Project]}
		if row.Color == "co" {
			// Past the palette: no key, rather than Other's gray.
			row.Color = ""
		}
		if pt.You.Total() > 0 {
			row.Faces = append(row.Faces, faceRow{Label: "You", Time: duration(pt.You.Total()), Pending: pendingOf(pt.You)})
		}
		if pt.Agent.Total() > 0 {
			row.Faces = append(row.Faces, faceRow{Label: capitalize(agent), Time: duration(pt.Agent.Total()), Pending: pendingOf(pt.Agent)})
		}
		if i < shownClocks {
			p.Today.Clocks = append(p.Today.Clocks, row)
			continue
		}
		if p.Today.More == nil {
			p.Today.More = &moreClocks{}
		}
		p.Today.More.Clocks = append(p.Today.More.Clocks, row)
		moreYou += pt.You.Total()
		moreAgent += pt.Agent.Total()
	}
	if m := p.Today.More; m != nil {
		m.Summary = moreSummary(len(m.Clocks), moreYou, moreAgent, agent)
	}

	if p.Week, err = newWeekPart(v, week, color, names, titles); err != nil {
		return dashPage{}, err
	}
	weekAtOnce := false
	for i, d := range week {
		weekAtOnce = weekAtOnce || ran.atOnce(d, i == len(week)-1)
	}
	p.Week.AgentLine = agentLine(agent, v.Week.Agent.Total(), weekAtOnce)
	// Today's sentence says it already when today has the agent's time,
	// so the page says it once.
	if v.Week.You.Total() == 0 && v.Week.Agent.Total() > 0 && p.Today.Why == "" {
		p.Week.Why = zeroWhy(agent)
	}

	byWeek := make(map[string]dashboard.ProjectTime, len(v.Week.Projects))
	for _, pt := range v.Week.Projects {
		byWeek[pt.Project] = pt
	}
	for _, pt := range v.Month.Projects {
		w := byWeek[pt.Project]
		c := color[pt.Project]
		if c == "co" {
			c = ""
		}
		p.Projects = append(p.Projects, projectRow{
			ID: pt.Project, Hide: named[pt.Project] && !v.Sample,
			Color: c, Name: names[pt.Project], Title: titles[pt.Project],
			Week:  newCell("Last 7 days", p.AgentStart, w.You.Total(), w.Agent.Total()),
			Month: newCell("Last 30 days", p.AgentStart, pt.You.Total(), pt.Agent.Total()),
		})
	}
	p.AllProjects = projectRow{
		Name:  "All projects",
		Week:  newCell("Last 7 days", p.AgentStart, v.Week.You.Total(), v.Week.Agent.Total()),
		Month: newCell("Last 30 days", p.AgentStart, v.Month.You.Total(), v.Month.Agent.Total()),
	}

	p.Levels = newLevelsPart(v.Week)
	if len(v.Week.Kinds) > 0 {
		top := v.Week.Kinds[0].Total()
		for _, k := range v.Week.Kinds {
			name := kindName(k.Kind, agent)
			row := kindRow{Name: name, Time: duration(k.Total())}
			if k.FinalS > 0 {
				row.Bars = append(row.Bars, segment{Class: fill(k.FinalS, top), Title: name + ", " + duration(k.FinalS)})
			}
			if k.PendingS > 0 {
				row.Bars = append(row.Bars, segment{Class: "pend " + fill(k.PendingS, top), Title: name + ", " + duration(k.PendingS) + " pending"})
			}
			p.Kinds = append(p.Kinds, row)
		}
	}
	return p, nil
}

// agentLine says how long the agent worked on its own in a window, s
// seconds. That time adds up each project's agent clock, which is wall
// time: agents running at once in one project count once there (SM 2.6),
// but agents in several projects at once add up, and can pass the hours
// the window had. atOnce says that happened on one of its days: then the
// line says the time is added up across projects, so it doesn't read as
// one agent working more hours than a day has. It says no more: a day can
// also pass its hours when the account's time zone changed that day, and
// the minutes of both zones' versions of the date count toward it. Either
// way it is information, beside your time, never a target (DESIGN 3.8).
func agentLine(agent string, s float64, atOnce bool) string {
	if atOnce {
		return capitalize(several(agent)) + " worked on their own for " + duration(s) + ", added up across projects."
	}
	return capitalize(agent) + " worked on its own for " + duration(s) + "."
}

// several names more than one of the agent: "your agents", or "Claude
// Code agents".
func several(agent string) string {
	if strings.HasSuffix(agent, "agent") {
		return agent + "s"
	}
	return agent + " agents"
}

// zeroWhy says why none of a window is your time though the agent worked
// on its own in it: pairtime counts your time from input on the computer
// the agent runs on (SM 2.1), so time of an agent that runs elsewhere is
// only the agent's (SM 3.4's SSH gap). It says what the rule implies and
// nothing about which computer the owner used.
func zeroWhy(agent string) string {
	return "pairtime counts your time from your typing and mouse on the computer where " + agent +
		" runs, so when it runs on a server you reach over SSH or from a remote app, its time shows here and yours doesn't."
}

// moreSummary is the row n of today's projects fold into: how many, and
// their time on each clock that ran in them.
func moreSummary(n int, you, agentS float64, agent string) string {
	s := strconv.Itoa(n) + " more projects: "
	if n == 1 {
		s = "1 more project: "
	}
	var clocks []string
	if you > 0 {
		clocks = append(clocks, "you "+duration(you))
	}
	if agentS > 0 {
		clocks = append(clocks, agent+" "+duration(agentS))
	}
	return s + strings.Join(clocks, ", ")
}

// dayClock tells how long a view's days had run when it was read, in its
// time zone.
type dayClock struct {
	loc  *time.Location
	asOf time.Time
	// whole counts today as a whole day: the samples' numbers don't
	// follow the clock, and a view that doesn't say when it was read
	// can't say how much of today had passed.
	whole bool
}

func newDayClock(v dashboard.View) dayClock {
	c := dayClock{loc: time.UTC, asOf: v.AsOf, whole: v.Sample || v.AsOf.IsZero()}
	if loc, err := rollup.LoadZone(v.TZ); err == nil {
		c.loc = loc
	} else {
		c.whole = true
	}
	return c
}

// atOnce reports whether the agent's time on day d, today when isToday,
// passes the time the day had run: all of a past day, and today up to
// when the view was read, and the few minutes a device's clock may run
// ahead. Mostly that takes agents working at the same time; the day of a
// time zone change can pass its hours too (agentLine).
func (c dayClock) atOnce(d dashboard.Day, isToday bool) bool {
	date, err := time.Parse(dashboard.DateLayout, d.Date)
	if err != nil {
		return false
	}
	start, end := firstInstant(date, c.loc), firstInstant(date.AddDate(0, 0, 1), c.loc)
	// Ingest takes a minute up to ingest.MaxSkewMS ahead of the server's
	// clock, so today may hold that much more than has passed.
	if until := c.asOf.Add(skew); isToday && !c.whole && c.asOf.After(start) && until.Before(end) {
		end = until
	}
	return d.Agent.Total() > end.Sub(start).Seconds()
}

// skew is how far ahead of the server's clock a device's minute may be:
// ingest-v1's 300 s (ingest.MaxSkewMS).
const skew = 300 * time.Second

// firstInstant returns the first instant, to the second, whose date in loc
// is day's or a later one, where day is a date at midnight UTC: local
// midnight, or on a day whose midnight the clocks skip, the first second
// after the jump, which time.Date doesn't promise to give (its doc says
// so). No offset from UTC reaches a day, so the instant is within two days
// of day, and a binary search over those seconds finds it, as
// internal/agent/store's DayBounds does on the device.
func firstInstant(day time.Time, loc *time.Location) time.Time {
	want := day.Format(dashboard.DateLayout)
	lo, hi := day.Unix()-2*86400, day.Unix()+2*86400
	for hi-lo > 1 {
		mid := lo + (hi-lo)/2
		if time.Unix(mid, 0).In(loc).Format(dashboard.DateLayout) >= want {
			hi = mid
		} else {
			lo = mid
		}
	}
	return time.Unix(hi, 0)
}

// newWeekPart lays out the week chart over days, the last 7.
func newWeekPart(v dashboard.View, days []dashboard.Day, color, names, titles map[string]string) (weekPart, error) {
	w := weekPart{You: duration(v.Week.You.Total()), Chart: v.Week.You.Total() > 0}
	most := 0.0
	for _, d := range days {
		most = math.Max(most, d.You.Total())
		w.HasPending = w.HasPending || d.You.PendingS > 0
	}
	top := axisTop(most)
	for i := 1; i <= 4; i++ {
		w.Ticks = append(w.Ticks, tick{At: "at" + strconv.Itoa(25*i), Label: duration(top * float64(i) / 4)})
	}

	// The legend and the table name the projects with time this week. The
	// table lists each one; the chart and legend fold those past
	// colorSlots into Other.
	other := false
	for _, pt := range v.Week.Projects {
		if pt.You.Total() == 0 {
			continue
		}
		w.Table.Names = append(w.Table.Names, nameCell{Name: names[pt.Project], Title: titles[pt.Project]})
		if color[pt.Project] == "co" {
			other = true
		} else {
			w.Legend = append(w.Legend, legendItem{Color: color[pt.Project], Name: names[pt.Project], Title: titles[pt.Project]})
		}
	}
	if other {
		w.Legend = append(w.Legend, legendItem{Color: "co", Name: otherName})
	}

	for i, d := range days {
		date, err := time.Parse(dashboard.DateLayout, d.Date)
		if err != nil {
			return weekPart{}, fmt.Errorf("pages: %w", err)
		}
		c := column{Dow: date.Format("Mon"), Dom: date.Format("2")}
		row := weekRow{Day: date.Format("Mon 2 Jan"), Total: withPending(d.You)}
		if i == len(days)-1 {
			c = column{Dow: "Today", Today: true}
			row.Day = "Today"
		}
		if d.You.Total() > 0 {
			c.Total = duration(d.You.Total())
		}

		// One stack part per colored project, in the view's order, and the
		// projects past colorSlots together as one Other part on top: a
		// shown clock can take a color ahead of a week's project the view
		// ranks above it, so Other's projects needn't sit together in the
		// stack.
		type part struct {
			color, name string
			you         dashboard.Time
		}
		var parts []part
		folded := part{color: "co", name: otherName}
		byProject := make(map[string]dashboard.Time, len(d.Projects))
		for _, pt := range d.Projects {
			byProject[pt.Project] = pt.You
			switch {
			case pt.You.Total() == 0:
			case color[pt.Project] == "co":
				folded.you = folded.you.Add(pt.You)
			default:
				parts = append(parts, part{color: color[pt.Project], name: names[pt.Project], you: pt.You})
			}
		}
		if folded.you.Total() > 0 {
			parts = append(parts, folded)
		}
		var sizes []float64
		var segs []segment
		for _, pt := range parts {
			if pt.you.FinalS > 0 {
				sizes = append(sizes, pt.you.FinalS)
				segs = append(segs, segment{Class: pt.color, Title: pt.name + ", " + duration(pt.you.FinalS)})
			}
			if pt.you.PendingS > 0 {
				sizes = append(sizes, pt.you.PendingS)
				segs = append(segs, segment{Class: pt.color + " pend", Title: pt.name + ", " + duration(pt.you.PendingS) + " pending"})
			}
		}
		for j, h := range stack(sizes, top) {
			segs[j].Class += " f" + strconv.Itoa(h)
		}
		c.Segments = segs
		w.Columns = append(w.Columns, c)

		for _, pt := range v.Week.Projects {
			if pt.You.Total() > 0 {
				row.Cells = append(row.Cells, withPending(byProject[pt.Project]))
			}
		}
		w.Table.Rows = append(w.Table.Rows, row)
	}
	return w, nil
}

// newLevelsPart lays out the split by evidence level of the window t.
func newLevelsPart(t dashboard.Totals) levelsPart {
	l := levelsPart{None: t.You.Total() == 0}
	values := make([]float64, len(t.Levels))
	for i, lt := range t.Levels {
		values[i] = lt.Total()
	}
	shares := percents(values)
	var label []string
	for i, lt := range t.Levels {
		share := strconv.Itoa(shares[i]) + "%"
		class := "lv" + strings.ToLower(lt.Level)
		item := levelItem{Level: lt.Level, Swatch: class, Text: levelText[lt.Level], Time: duration(lt.Total()), Share: share}
		if lt.Total() > 0 {
			l.Blocks = append(l.Blocks, levelBlock{
				Class: class + " f" + strconv.Itoa(max(shares[i], 1)),
				Title: "Level " + lt.Level + ", " + duration(lt.Total()) + ", " + share,
				Share: share,
				// A share prints inside its block from 14% up. On a
				// 320px phone that is 45px, room for "99%" in 0.8rem
				// bold type and the block's padding; a smaller block
				// leaves it to the list below.
				Printed: shares[i] >= minPrinted,
			})
			label = append(label, "level "+lt.Level+" "+share)
		}
		l.Items = append(l.Items, item)
	}
	if len(label) > 0 {
		l.Label = "Your time by evidence level: " + strings.Join(label, ", ")
	}
	return l
}

// duration writes seconds as hours and minutes, such as "1h 12m", "45m"
// or "2h". It rounds down to the minute, so the page never shows more
// time than there was; more than nothing but under a minute is "<1m".
func duration(s float64) string {
	m := int64(s / 60)
	switch {
	case m == 0 && s > 0:
		return "<1m"
	case m < 60:
		return strconv.FormatInt(m, 10) + "m"
	case m%60 == 0:
		return strconv.FormatInt(m/60, 10) + "h"
	}
	return strconv.FormatInt(m/60, 10) + "h " + strconv.FormatInt(m%60, 10) + "m"
}

// pendingOf writes t's pending time, or "" when none is pending.
func pendingOf(t dashboard.Time) string {
	if t.PendingS > 0 {
		return duration(t.PendingS)
	}
	return ""
}

// withPending writes t's whole time, and how much of it is pending.
func withPending(t dashboard.Time) string {
	if t.PendingS > 0 {
		return duration(t.Total()) + ", " + duration(t.PendingS) + " pending"
	}
	return duration(t.Total())
}

// axisTops are the tops the week chart's axis can have, in seconds. Each
// splits into four steps a person reads at a glance.
var axisTops = []float64{20 * 60, 40 * 60, 3600, 2 * 3600, 4 * 3600, 6 * 3600, 8 * 3600, 12 * 3600, 16 * 3600, 24 * 3600}

// axisTop returns the axis top for a chart whose tallest column is most
// seconds.
func axisTop(most float64) float64 {
	for _, t := range axisTops {
		if most <= t {
			return t
		}
	}
	return math.Ceil(most/(4*3600)) * 4 * 3600
}

// fill returns the class that sizes a bar of v seconds in a chart whose
// longest bar is top: f1 to f100, and never f0 for a bar with time in it.
func fill(v, top float64) string {
	p := int(math.Round(v / top * 100))
	return "f" + strconv.Itoa(min(max(p, 1), 100))
}

// stack returns the heights, in percent of top, of parts stacked in a
// column. It rounds where each part ends rather than each part alone, so
// the column is as tall as its total, and gives every part at least 1, so
// none with time in it disappears.
func stack(sizes []float64, top float64) []int {
	out := make([]int, len(sizes))
	sum, at := 0.0, 0
	for i, s := range sizes {
		sum += s
		end := int(math.Round(sum / top * 100))
		out[i] = max(end-at, 1)
		at += out[i]
	}
	// Rounding up a tiny part can push the column past the top; take it
	// back from the tallest part.
	if over := at - 100; over > 0 && len(out) > 0 {
		i := 0
		for j := range out {
			if out[j] > out[i] {
				i = j
			}
		}
		out[i] = max(out[i]-over, 1)
	}
	return out
}

// percents returns values as whole percentages of their sum that add up
// to exactly 100: each gets its floor, and the points left over go to the
// largest remainders, earlier values first on a tie. A value above zero
// then gets at least 1, taken from the largest, so a level with time never
// reads 0% and none reads 100% beside another. With nothing to share,
// every percentage is 0.
func percents(values []float64) []int {
	out := make([]int, len(values))
	sum := 0.0
	for _, v := range values {
		sum += v
	}
	if sum <= 0 {
		return out
	}
	type remainder struct {
		i int
		r float64
	}
	rems := make([]remainder, len(values))
	given := 0
	for i, v := range values {
		exact := v / sum * 100
		out[i] = int(exact)
		rems[i] = remainder{i: i, r: exact - float64(out[i])}
		given += out[i]
	}
	slices.SortStableFunc(rems, func(a, b remainder) int { return cmp.Compare(b.r, a.r) })
	for k := 0; given < 100 && k < len(rems); k++ {
		out[rems[k].i]++
		given++
	}
	for i, v := range values {
		if v > 0 && out[i] == 0 {
			largest := 0
			for j := range out {
				if out[j] > out[largest] {
					largest = j
				}
			}
			out[largest]--
			out[i] = 1
		}
	}
	return out
}

// dashboardPage renders v as the dashboard page, with what a visitor does
// to get a dashboard of their own from in and whether they are signed in.
func (s *Site) dashboardPage(v dashboard.View, in SignIn, signedIn bool) ([]byte, error) {
	d, err := newDashPageFor(v, in, s.signupsOpen, signedIn)
	if err != nil {
		return nil, err
	}
	var body bytes.Buffer
	if err := s.dash.Execute(&body, d); err != nil {
		return nil, fmt.Errorf("pages: %w", err)
	}
	ld := layoutData{Title: "Your dashboard", Class: "dash", Body: template.HTML(body.String())}
	if v.Sample {
		ld.Title, ld.Description = "Sample dashboard", demoDescription
	}
	var out bytes.Buffer
	if err := s.layout.wrap(&out, ld); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// checkDashboard renders the sample once for each sign-in setting, so a
// mistake in dashboard.html stops pairtimed at start instead of reaching a
// reader.
func (s *Site) checkDashboard() error {
	v, err := dashboard.Demo{}.View(context.Background(), "", time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC))
	if err != nil {
		return err
	}
	for _, in := range []SignIn{{}, {Enabled: true, LoginPath: "/sign-in"}} {
		for _, signedIn := range []bool{false, in.Enabled} {
			page, err := s.dashboardPage(v, in, signedIn)
			if err != nil {
				return err
			}
			if len(page) == 0 {
				return errors.New("pages: the dashboard page rendered empty")
			}
		}
	}
	var off bytes.Buffer
	return s.dash.ExecuteTemplate(&off, "off", struct{ SignupsOpen bool }{s.signupsOpen})
}

// RegisterDemo adds GET /demo, the dashboard page drawn from the fixed
// sample as of now(). Its banner says the data is made up, and what a
// visitor does to get a dashboard of their own, which depends on in: a
// signed-in visitor gets a link to theirs. So the page differs by session,
// and by day, and nothing may store it.
func (s *Site) RegisterDemo(mux *http.ServeMux, in SignIn, now func() time.Time) {
	mux.HandleFunc("GET /demo", func(w http.ResponseWriter, r *http.Request) {
		signedIn := false
		if in.Enabled && in.Visitor != nil {
			_, signedIn = in.Visitor(r)
		}
		v, err := dashboard.Demo{}.View(r.Context(), "", now())
		var page []byte
		if err == nil {
			page, err = s.dashboardPage(v, in, signedIn)
		}
		if err != nil {
			// New rendered the sample already, so this doesn't happen.
			http.Error(w, "This page didn't render.", http.StatusInternalServerError)
			return
		}
		writePage(w, http.StatusOK, page, s.csp, "no-store", "no-referrer")
	})
}

// capitalize returns s with its first letter in upper case.
func capitalize(s string) string {
	if s == "" || s[0] < 'a' || s[0] > 'z' {
		return s
	}
	return string(s[0]-'a'+'A') + s[1:]
}

// The owner's dashboard's paths.
const (
	DashboardPath = "/dashboard"
	ZonePath      = "/dashboard/zone"
	HidePath      = "/dashboard/projects/hide"
)

// Dashboard wires the signed-in owner's dashboard to its data.
type Dashboard struct {
	// Source is where views come from: dashboard.Rollups in pairtimed.
	Source dashboard.Source
	// Account returns the users id of the account a request is signed in
	// to: dashboard.ErrNotSignedIn when it isn't signed in, and another
	// error when its session can't be read, which the page answers with a
	// 500 rather than sending a signed-in owner to sign in again.
	Account func(r *http.Request) (string, error)
	// SetZone sets an account's time zone. It wraps rollup.ErrBadZone for
	// a name that isn't one, and returns a *rollup.ZoneTooSoonError, which
	// wraps rollup.ErrZoneTooSoon, for a change less than 30 days after the
	// last (rollup.SetZone).
	SetZone func(ctx context.Context, account, zone string) error
	// HideName deletes the name the account's owner turned on for
	// project, an account-level project id, from every device key of it
	// (D-owner-names; projectnames.Hide). Nil offers no hide button.
	HideName func(ctx context.Context, account, project string) error
	// SameOrigin reports whether a POST comes from this server's own
	// pages, as auth.Service.SameOrigin does.
	SameOrigin func(r *http.Request) bool
	// Now is the clock.
	Now func() time.Time
	// Logger hears of reads and writes that fail.
	Logger *slog.Logger
}

// zoneFormMax bounds the time zone form's body.
const zoneFormMax = 4 << 10

// RegisterDashboard adds GET /dashboard, the signed-in account's own
// dashboard, POST /dashboard/zone, which sets its time zone, and POST
// /dashboard/projects/hide, which hides the name of one of its projects
// the owner turned on (D-owner-names): the website can hide a name, never
// set or change one. With
// sign-in off the page says so and links /demo. A visitor who isn't signed
// in goes to Hack Club sign-in, which brings them back. A session that
// can't be read is a 500, since signing in again wouldn't help.
//
// The page is the owner's own, so nothing may store it, and its form posts
// to this server only. Its referrer policy is same-origin, like the home
// page's, so the form's POST carries this server's Origin.
func (s *Site) RegisterDashboard(mux *http.ServeMux, in SignIn, d Dashboard) {
	mux.HandleFunc("GET "+DashboardPath, func(w http.ResponseWriter, r *http.Request) {
		s.serveDashboard(w, r, in, d, "", http.StatusOK)
	})
	mux.HandleFunc("POST "+ZonePath, func(w http.ResponseWriter, r *http.Request) {
		if !in.Enabled || d.Account == nil {
			http.Redirect(w, r, DashboardPath, http.StatusSeeOther)
			return
		}
		if d.SameOrigin == nil || !d.SameOrigin(r) {
			http.Error(w, "Changing your time zone has to start from your dashboard.", http.StatusForbidden)
			return
		}
		id, err := d.Account(r)
		switch {
		case errors.Is(err, dashboard.ErrNotSignedIn):
			http.Redirect(w, r, LoginURL(in.LoginPath, DashboardPath), http.StatusSeeOther)
			return
		case err != nil:
			d.Logger.Error("reading a session for the dashboard", "err", err)
			http.Error(w, "Your time zone didn't save. Try again in a minute.", http.StatusInternalServerError)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, zoneFormMax)
		if err := r.ParseForm(); err != nil {
			http.Error(w, "That form didn't arrive whole.", http.StatusBadRequest)
			return
		}
		zone := strings.TrimSpace(r.PostForm.Get("zone"))
		if err := d.SetZone(r.Context(), id, zone); err != nil {
			if errors.Is(err, rollup.ErrBadZone) {
				s.serveDashboard(w, r, in, d, "That isn't a time zone pairtime knows. Use a name like America/New_York or Europe/London.", http.StatusUnprocessableEntity)
				return
			}
			var soon *rollup.ZoneTooSoonError
			if errors.As(err, &soon) {
				s.serveDashboard(w, r, in, d, zoneTooSoon(soon), http.StatusUnprocessableEntity)
				return
			}
			d.Logger.Error("setting a time zone", "user", id, "err", err)
			http.Error(w, "Your time zone didn't save. Try again in a minute.", http.StatusInternalServerError)
			return
		}
		http.Redirect(w, r, DashboardPath, http.StatusSeeOther)
	})
	mux.HandleFunc("POST "+HidePath, func(w http.ResponseWriter, r *http.Request) {
		if !in.Enabled || d.Account == nil || d.HideName == nil {
			http.Redirect(w, r, DashboardPath, http.StatusSeeOther)
			return
		}
		if d.SameOrigin == nil || !d.SameOrigin(r) {
			http.Error(w, "Hiding a project's name has to start from your dashboard.", http.StatusForbidden)
			return
		}
		id, err := d.Account(r)
		switch {
		case errors.Is(err, dashboard.ErrNotSignedIn):
			http.Redirect(w, r, LoginURL(in.LoginPath, DashboardPath), http.StatusSeeOther)
			return
		case err != nil:
			d.Logger.Error("reading a session for the dashboard", "err", err)
			http.Error(w, "The name didn't hide. Try again in a minute.", http.StatusInternalServerError)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, zoneFormMax)
		if err := r.ParseForm(); err != nil {
			http.Error(w, "That form didn't arrive whole.", http.StatusBadRequest)
			return
		}
		project := r.PostForm.Get("project")
		if project == "" || len(project) > 128 {
			http.Error(w, "That form names no project.", http.StatusBadRequest)
			return
		}
		if err := d.HideName(r.Context(), id, project); err != nil {
			d.Logger.Error("hiding a project's name", "user", id, "err", err)
			http.Error(w, "The name didn't hide. Try again in a minute.", http.StatusInternalServerError)
			return
		}
		http.Redirect(w, r, DashboardPath, http.StatusSeeOther)
	})
}

// zoneTooSoon says why a time zone change was refused, with the dates in
// the account's zone, the one the refusal's times are in.
func zoneTooSoon(e *rollup.ZoneTooSoonError) string {
	return "You changed your time zone on " + e.Changed.Format("2 January 2006") +
		". You can change it again from " + e.Next.Format("2 January 2006") + "."
}

// serveDashboard writes the dashboard page for r, with zoneError, if any,
// beside the time zone form.
func (s *Site) serveDashboard(w http.ResponseWriter, r *http.Request, in SignIn, d Dashboard, zoneError string, code int) {
	if !in.Enabled || d.Account == nil {
		var body bytes.Buffer
		var page bytes.Buffer
		off := struct{ SignupsOpen bool }{s.signupsOpen}
		if err := s.dash.ExecuteTemplate(&body, "off", off); err != nil || s.layout.wrap(&page, layoutData{Title: "Your dashboard", Class: "dash", Body: template.HTML(body.String())}) != nil {
			http.Error(w, "This page didn't render.", http.StatusInternalServerError)
			return
		}
		writePage(w, http.StatusOK, page.Bytes(), s.csp, "no-store", "no-referrer")
		return
	}
	id, err := d.Account(r)
	switch {
	case errors.Is(err, dashboard.ErrNotSignedIn):
		http.Redirect(w, r, LoginURL(in.LoginPath, DashboardPath), http.StatusSeeOther)
		return
	case err != nil:
		d.Logger.Error("reading a session for the dashboard", "err", err)
		http.Error(w, "Your dashboard didn't load. Try again in a minute.", http.StatusInternalServerError)
		return
	}
	v, err := d.Source.View(r.Context(), id, d.Now())
	var page []byte
	if err == nil {
		acct := &account{SignedIn: true, Login: in.LoginPath, Logout: in.LogoutPath}
		if in.Visitor != nil {
			acct.Name, _ = in.Visitor(r)
		}
		page, err = s.ownerPage(v, in, acct, zoneError, d.HideName != nil)
	}
	if err != nil {
		d.Logger.Error("showing a dashboard", "user", id, "err", err)
		http.Error(w, "Your dashboard didn't load. Try again in a minute.", http.StatusInternalServerError)
		return
	}
	writePage(w, code, page, s.cspLive, "no-store", "same-origin")
}

// ownerPage renders v as a signed-in account's own dashboard, with acct in
// the header, and a "Hide this name" button beside each name its owner
// turned on when canHide says the server can hide one.
func (s *Site) ownerPage(v dashboard.View, in SignIn, acct *account, zoneError string, canHide bool) ([]byte, error) {
	d, err := newDashPage(v, in, s.signupsOpen)
	if err != nil {
		return nil, err
	}
	d.ZoneError = zoneError
	if canHide {
		d.HidePath = HidePath
	}
	// The snapshot's ETag, which the page's script sends when it polls
	// (WP-SV8): the View the page draws is the one the API answers with.
	d.ETag = dashboard.ETag(v)
	var body bytes.Buffer
	if err := s.dash.Execute(&body, d); err != nil {
		return nil, fmt.Errorf("pages: %w", err)
	}
	// The script goes in as its bytes, outside the template, so the page
	// carries exactly what cspLive's hash names.
	body.WriteString("<script>")
	body.Write(s.liveJS)
	body.WriteString("</script>\n")
	var out bytes.Buffer
	if err := s.layout.wrap(&out, layoutData{Title: "Your dashboard", Class: "dash", Body: template.HTML(body.String()), Account: acct}); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}
