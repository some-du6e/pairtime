// SPDX-License-Identifier: AGPL-3.0-or-later

package dashboard

import (
	"cmp"
	"context"
	"slices"
	"time"
)

// Demo is the fixed sample /demo shows: thirty made-up days of a teen
// building three projects with Claude Code. Most of it is level A. Some
// is level B, most of that from a project built partly on a laptop where
// pairtime sees input but can't tell whether it was typing. One day's
// uploads reached the server late with no record of why, and the oldest
// days were read back from Claude Code's transcripts at install (both
// level C). The last few minutes of today are still pending.
//
// The numbers never change. The last day is always today in UTC, so the
// page reads the way a dashboard does on the day it is opened.
type Demo struct{}

// DemoAgent is the agent the sample's teen builds with.
const DemoAgent = "Claude Code"

// The sample's projects. Real project keys are opaque HMACs (DESIGN 4.3);
// these say what they are.
const (
	demoChip8     = "p_sample_chip8"
	demoRobotics  = "p_sample_scouting"
	demoPortfolio = "p_sample_portfolio"
)

// View returns the sample as of now, whatever the account.
func (Demo) View(_ context.Context, _ string, now time.Time) (View, error) {
	today := now.UTC().Format(DateLayout)
	return Build(Meta{
		Sample: true,
		AsOf:   now,
		TZ:     "UTC",
		Today:  today,
		Agent:  DemoAgent,
		Names: map[string]string{
			demoChip8:     "chip8-emulator",
			demoRobotics:  "robotics-scouting",
			demoPortfolio: "portfolio-site",
		},
		// Each sample project's made-up key is its id too.
		Keys: map[string][]string{
			demoChip8:     {demoChip8},
			demoRobotics:  {demoRobotics},
			demoPortfolio: {demoPortfolio},
		},
	}, demoRows(now.UTC()))
}

// demoProject is how the sample's teen works in one project: weights for
// the activity kinds in Kinds order, and for levels A, B and C on an
// ordinary day.
type demoProject struct {
	key    string
	kinds  []int
	levels []int
}

var demoProjects = [3]demoProject{
	// An emulator: lots of tests and debugging, at a desktop whose
	// keyboard backs nearly everything.
	{key: demoChip8, kinds: []int{28, 22, 14, 10, 12, 8, 4, 2}, levels: []int{94, 6, 0}},
	// The robotics team's scouting app, partly on a laptop where pairtime
	// sees input but not which kind.
	{key: demoRobotics, kinds: []int{32, 20, 12, 12, 6, 6, 8, 4}, levels: []int{45, 55, 0}},
	// A personal site, with more editing by hand.
	{key: demoPortfolio, kinds: []int{26, 18, 8, 14, 2, 2, 26, 4}, levels: []int{80, 20, 0}},
}

// demoDay is one day of the sample. Minutes are per project, in
// demoProjects order.
type demoDay struct {
	// ago counts days before today.
	ago int
	// you and agent are the teen's credited minutes and the minutes
	// Claude Code worked on its own (agent_only, SM 2.6); youPending and
	// agentPending are how many of them are still pending.
	you, agent               [3]int
	youPending, agentPending [3]int
	// levelC marks a project's time that day as level C: uploads that
	// reached the server late, or lookback.
	levelC [3]bool
}

// demoDays has the days with any time; the others are days off.
var demoDays = []demoDay{
	{ago: 0, you: [3]int{72, 41, 0}, agent: [3]int{160, 52, 0}, youPending: [3]int{9, 0, 0}, agentPending: [3]int{12, 0, 0}},
	{ago: 1, you: [3]int{62, 0, 48}, agent: [3]int{150, 0, 30}},
	// The scouting time reached the server late, without the
	// upload-failure notes that would have made it offline, so it is
	// backfilled: level C (DESIGN 6.4, 7.4).
	{ago: 3, you: [3]int{50, 45, 0}, agent: [3]int{95, 60, 0}, levelC: [3]bool{false, true, false}},
	{ago: 4, you: [3]int{0, 85, 0}, agent: [3]int{0, 120, 0}},
	{ago: 5, you: [3]int{95, 60, 0}, agent: [3]int{210, 75, 0}},
	{ago: 6, you: [3]int{140, 0, 35}, agent: [3]int{290, 0, 20}},
	{ago: 7, you: [3]int{80, 0, 0}, agent: [3]int{170, 0, 0}},
	{ago: 8, you: [3]int{0, 70, 0}, agent: [3]int{0, 90, 0}},
	{ago: 10, you: [3]int{120, 0, 30}, agent: [3]int{260, 0, 15}},
	{ago: 11, you: [3]int{0, 55, 0}, agent: [3]int{0, 70, 0}},
	{ago: 12, you: [3]int{45, 0, 0}, agent: [3]int{100, 0, 0}},
	{ago: 14, you: [3]int{0, 0, 90}, agent: [3]int{0, 0, 60}},
	{ago: 15, you: [3]int{65, 40, 0}, agent: [3]int{140, 50, 0}},
	{ago: 17, you: [3]int{0, 100, 0}, agent: [3]int{0, 150, 0}},
	{ago: 18, you: [3]int{70, 0, 0}, agent: [3]int{130, 0, 0}},
	{ago: 19, you: [3]int{0, 0, 60}, agent: [3]int{0, 0, 40}},
	{ago: 21, you: [3]int{110, 0, 0}, agent: [3]int{240, 0, 0}},
	{ago: 22, you: [3]int{0, 50, 0}, agent: [3]int{0, 65, 0}},
	{ago: 24, you: [3]int{40, 0, 0}, agent: [3]int{80, 0, 0}},
	// Before the install: lookback read these from Claude Code's
	// transcripts, with no presence data behind them.
	{ago: 25, you: [3]int{75, 0, 0}, agent: [3]int{160, 0, 0}, levelC: [3]bool{true, true, true}},
	{ago: 27, you: [3]int{0, 60, 0}, agent: [3]int{0, 80, 0}, levelC: [3]bool{true, true, true}},
	{ago: 28, you: [3]int{90, 0, 0}, agent: [3]int{180, 0, 0}, levelC: [3]bool{true, true, true}},
	{ago: 29, you: [3]int{0, 0, 40}, agent: [3]int{0, 0, 25}, levelC: [3]bool{true, true, true}},
}

// levelCOnly is the level weights of a level-C day.
var levelCOnly = []int{0, 0, 100}

// demoRows lays the sample out as rollup rows, the last day on today's
// date. Each project's minutes split into kinds and then levels in whole
// seconds, so every total the page shows adds up exactly.
func demoRows(today time.Time) []Row {
	var rows []Row
	for _, d := range demoDays {
		day := today.AddDate(0, 0, -d.ago).Format(DateLayout)
		for i, p := range demoProjects {
			levels := p.levels
			if d.levelC[i] {
				levels = levelCOnly
			}
			for _, pending := range []bool{false, true} {
				you, agent := d.you[i]-d.youPending[i], d.agent[i]-d.agentPending[i]
				if pending {
					you, agent = d.youPending[i], d.agentPending[i]
				}
				if agent > 0 {
					rows = append(rows, Row{Day: day, Project: p.key, Agent: true, Pending: pending, Seconds: float64(agent * 60)})
				}
				for k, ks := range split(you*60, p.kinds) {
					for l, ls := range split(ks, levels) {
						if ls > 0 {
							rows = append(rows, Row{Day: day, Project: p.key, Kind: Kinds[k], Level: Levels[l], Pending: pending, Seconds: float64(ls)})
						}
					}
				}
			}
		}
	}
	return rows
}

// split divides total into parts in proportion to weights, in whole units
// that add up to total exactly: each part gets its floor, and the units
// left over go to the largest remainders, earlier parts first on a tie. A
// part with no weight gets nothing.
func split(total int, weights []int) []int {
	out := make([]int, len(weights))
	sum := 0
	for _, w := range weights {
		sum += w
	}
	if sum == 0 || total == 0 {
		return out
	}
	type remainder struct{ part, r int }
	rems := make([]remainder, len(weights))
	given := 0
	for i, w := range weights {
		out[i] = total * w / sum
		rems[i] = remainder{part: i, r: total * w % sum}
		given += out[i]
	}
	slices.SortStableFunc(rems, func(a, b remainder) int { return cmp.Compare(b.r, a.r) })
	for k := 0; given < total; k++ {
		out[rems[k].part]++
		given++
	}
	return out
}
