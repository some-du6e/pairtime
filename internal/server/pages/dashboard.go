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

// colorSlots is how many projects get a color of their own: --c1 to --c5
// in style.css, the first five slots of a categorical palette that passes
// the color-vision checks for neighbors in a stack, in light and dark
// mode. The projects after them share --co and are drawn together as
// Other.
const colorSlots = 5

// otherName is what the week chart calls the projects past colorSlots.
const otherName = "Other"

// minPrinted is the smallest share, in percent, that the evidence bar
// prints inside its block.
const minPrinted = 14

// demoDescription is the sample's meta description, which link previews
// show.
const demoDescription = "A sample pairtime dashboard: a made-up teen's time building with Claude Code, and the time Claude Code ran."

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
	// Zone is the IANA time zone the account's days are counted in, and
	// ZoneError says why the last change to it was refused.
	Zone, ZoneError string
	// ZonePath is where the time zone form posts.
	ZonePath string
	// Agent names the agent in a sentence, and AgentStart at its start or
	// alone, as in "Your agent ran for 2h".
	Agent, AgentStart string
	Today             todayPart
	Week              weekPart
	Projects          []projectRow
	// AllProjects totals the projects.
	AllProjects projectRow
	Levels      levelsPart
	Kinds       []kindRow
}

type todayPart struct {
	// Date is today, such as "Friday 20 November".
	Date       string
	You, Agent string
	// YouPending is how much of You is pending, or "" for none.
	YouPending string
	Clocks     []clockRow
}

// clockRow is one project's chess clock.
type clockRow struct {
	Color, Name string
	You, Agent  string
	// YouPending and AgentPending are "" when nothing is pending.
	YouPending, AgentPending string
}

type weekPart struct {
	You, Agent string
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

type legendItem struct{ Color, Name string }

// weekTable is the week chart's numbers as a table.
type weekTable struct {
	Names []string
	Rows  []weekRow
}

type weekRow struct {
	Day   string
	Cells []string
	Total string
}

// projectRow is a row of the projects table.
type projectRow struct {
	Color, Name           string
	Week, Month           string
	WeekAgent, MonthAgent string
}

type levelsPart struct {
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
		Owner: !v.Sample, Empty: !v.Sample && v.Month.You.Total() == 0 && v.Month.Agent.Total() == 0,
		Zone: v.TZ, ZonePath: ZonePath,
	}
	if in.LoginPath != "" {
		// Sign-in brings the visitor to their own dashboard.
		p.Login = in.LoginPath + "?return_to=" + DashboardPath
	}
	// Colors go to the projects with time this week first, in the view's
	// order, then to the rest, so the week chart never folds one of the
	// week's projects into Other while a quieter one keeps a color. Each
	// project keeps its color across the page.
	inWeek := make(map[string]bool, len(v.Week.Projects))
	for _, pt := range v.Week.Projects {
		inWeek[pt.Project] = true
	}
	var order []string
	for _, week := range []bool{true, false} {
		for _, pr := range v.Projects {
			if inWeek[pr.Key] == week {
				order = append(order, pr.Key)
			}
		}
	}
	color := make(map[string]string, len(v.Projects))
	names := make(map[string]string, len(v.Projects))
	for i, key := range order {
		color[key] = "c" + strconv.Itoa(i+1)
		if i >= colorSlots {
			color[key] = "co"
		}
	}
	for _, pr := range v.Projects {
		names[pr.Key] = pr.Name
	}

	today := v.Days[len(v.Days)-1]
	date, err := time.Parse(dashboard.DateLayout, today.Date)
	if err != nil {
		return dashPage{}, fmt.Errorf("pages: %w", err)
	}
	p.Today = todayPart{Date: date.Format("Monday 2 January"), You: duration(today.You.Total()), Agent: duration(today.Agent.Total()), YouPending: pendingOf(today.You)}
	for _, pt := range today.Projects {
		p.Today.Clocks = append(p.Today.Clocks, clockRow{
			Color: color[pt.Project], Name: names[pt.Project],
			You: duration(pt.You.Total()), Agent: duration(pt.Agent.Total()),
			YouPending: pendingOf(pt.You), AgentPending: pendingOf(pt.Agent),
		})
	}

	if p.Week, err = newWeekPart(v, v.Days[len(v.Days)-dashboard.WeekDays:], color, names); err != nil {
		return dashPage{}, err
	}

	week := make(map[string]dashboard.ProjectTime, len(v.Week.Projects))
	for _, pt := range v.Week.Projects {
		week[pt.Project] = pt
	}
	for _, pt := range v.Month.Projects {
		w := week[pt.Project]
		p.Projects = append(p.Projects, projectRow{
			Color: color[pt.Project], Name: names[pt.Project],
			Week: duration(w.You.Total()), Month: duration(pt.You.Total()),
			WeekAgent: duration(w.Agent.Total()), MonthAgent: duration(pt.Agent.Total()),
		})
	}
	p.AllProjects = projectRow{
		Name: "All projects",
		Week: duration(v.Week.You.Total()), Month: duration(v.Month.You.Total()),
		WeekAgent: duration(v.Week.Agent.Total()), MonthAgent: duration(v.Month.Agent.Total()),
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

// newWeekPart lays out the week chart over days, the last 7.
func newWeekPart(v dashboard.View, days []dashboard.Day, color, names map[string]string) (weekPart, error) {
	w := weekPart{You: duration(v.Week.You.Total()), Agent: duration(v.Week.Agent.Total())}
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
		w.Table.Names = append(w.Table.Names, names[pt.Project])
		if color[pt.Project] == "co" {
			other = true
		} else {
			w.Legend = append(w.Legend, legendItem{Color: color[pt.Project], Name: names[pt.Project]})
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

		// One stack part per color, projects past colorSlots together.
		type part struct {
			color, name string
			you         dashboard.Time
		}
		var parts []part
		byProject := make(map[string]dashboard.Time, len(d.Projects))
		for _, pt := range d.Projects {
			byProject[pt.Project] = pt.You
			if pt.You.Total() == 0 {
				continue
			}
			name := names[pt.Project]
			if color[pt.Project] == "co" {
				name = otherName
			}
			if n := len(parts); n > 0 && parts[n-1].color == color[pt.Project] {
				parts[n-1].you = parts[n-1].you.Add(pt.You)
				continue
			}
			parts = append(parts, part{color: color[pt.Project], name: name, you: pt.You})
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
	var l levelsPart
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
)

// Dashboard wires the signed-in owner's dashboard to its data.
type Dashboard struct {
	// Source is where views come from: dashboard.Rollups in pairtimed.
	Source dashboard.Source
	// Account returns the users id of the account a request is signed in
	// to, and false when it isn't signed in.
	Account func(r *http.Request) (string, bool)
	// SetZone sets an account's time zone. It wraps rollup.ErrBadZone for
	// a name that isn't one.
	SetZone func(ctx context.Context, account, zone string) error
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
// dashboard, and POST /dashboard/zone, which sets its time zone. With
// sign-in off the page says so and links /demo. A visitor who isn't signed
// in goes to Hack Club sign-in, which brings them back.
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
		id, ok := d.Account(r)
		if !ok {
			http.Redirect(w, r, in.LoginPath+"?return_to="+DashboardPath, http.StatusSeeOther)
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
			d.Logger.Error("setting a time zone", "user", id, "err", err)
			http.Error(w, "Your time zone didn't save. Try again in a minute.", http.StatusInternalServerError)
			return
		}
		http.Redirect(w, r, DashboardPath, http.StatusSeeOther)
	})
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
	id, ok := d.Account(r)
	if !ok {
		http.Redirect(w, r, in.LoginPath+"?return_to="+DashboardPath, http.StatusSeeOther)
		return
	}
	v, err := d.Source.View(r.Context(), id, d.Now())
	var page []byte
	if err == nil {
		acct := &account{SignedIn: true, Login: in.LoginPath, Logout: in.LogoutPath}
		if in.Visitor != nil {
			acct.Handle, _ = in.Visitor(r)
		}
		page, err = s.ownerPage(v, in, acct, zoneError)
	}
	if err != nil {
		d.Logger.Error("showing a dashboard", "user", id, "err", err)
		http.Error(w, "Your dashboard didn't load. Try again in a minute.", http.StatusInternalServerError)
		return
	}
	writePage(w, code, page, s.cspForm, "no-store", "same-origin")
}

// ownerPage renders v as a signed-in account's own dashboard, with acct in
// the header.
func (s *Site) ownerPage(v dashboard.View, in SignIn, acct *account, zoneError string) ([]byte, error) {
	d, err := newDashPage(v, in, s.signupsOpen)
	if err != nil {
		return nil, err
	}
	d.ZoneError = zoneError
	var body bytes.Buffer
	if err := s.dash.Execute(&body, d); err != nil {
		return nil, fmt.Errorf("pages: %w", err)
	}
	var out bytes.Buffer
	if err := s.layout.wrap(&out, layoutData{Title: "Your dashboard", Class: "dash", Body: template.HTML(body.String()), Account: acct}); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}
