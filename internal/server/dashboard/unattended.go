// SPDX-License-Identifier: AGPL-3.0-or-later

package dashboard

import (
	"context"
	"time"
)

// Unattended is a second fixed sample, which the pages' tests and their
// preview (tools/pagespreview) draw and pairtimed never serves: thirty
// made-up days of an account whose only computer is a server where agents
// work in nine projects, several at a time, and nobody types. pairtime
// counts the owner's time from input on the computer the agent runs on
// (SM 2.1, 3.4), so none of it is the owner's, and the agents' time today
// adds up to more than a day. Two projects have a few minutes still
// pending. It reads as the owner's own view does: the agent is AgentName,
// each project is called by its short code, and each computer's projects
// count apart.
//
// Like Demo, the numbers never change, and the last day is today in UTC.
// One project's agent clock is wall time (SM 2.6), so its busiest project's
// 15h 5m today needs the day to have run that long: read it from late in
// the day, as the tests and the pages preview do at 18:00.
type Unattended struct{}

// unattendedKeys are the sample's project keys, made up in the git
// adapter's form. Each one is its project's id too.
var unattendedKeys = [9]string{
	"g889f4cadffe2f9a5444229db62ed33c6",
	"gae61190e4b266e089df3032ab58aaafb",
	"g497b03548bc468b92395951962faee9c",
	"g343abeef930f8b969e1f52fa8eff02c5",
	"gff89bcfc77642de8909b939d26f751d6",
	"g228e8fc3f0da54b90639fba8c33fa4be",
	"g3da650294cbfaad9550b9d744e2be170",
	"g9b3bca103ff790ac539811b3c64be892",
	"g44f8821e9ebcf916b20a3f46d0ce58cc",
}

// unattendedDay is one day of the sample: the seconds the agents worked
// on their own in each project, in unattendedKeys order, and how many of
// them are still pending.
type unattendedDay struct {
	ago            int
	agent, pending [9]int
}

// unattendedDays has the days with any time; the server sat idle on the
// others. Today's projects run from 15h 5m down to 40 s, 31h 5m in all.
var unattendedDays = []unattendedDay{
	{ago: 0, agent: [9]int{54300, 30720, 14820, 5760, 3660, 1680, 720, 240, 40}, pending: [9]int{0, 180, 0, 0, 120}},
	{ago: 1, agent: [9]int{36000, 21000, 9000, 2400, 0, 1200}},
	{ago: 2, agent: [9]int{42000, 0, 13200, 5400, 1800, 0, 600}},
	{ago: 4, agent: [9]int{30000, 18000, 0, 0, 3000, 900, 0, 300}},
	{ago: 5, agent: [9]int{25200, 0, 7200}},
	{ago: 6, agent: [9]int{0, 14400, 0, 3600}},
	{ago: 9, agent: [9]int{28800, 7200, 0, 0, 0, 0, 0, 0, 900}},
	{ago: 13, agent: [9]int{0, 25200, 3600}},
	{ago: 20, agent: [9]int{18000}},
}

// View returns the sample as of now, whatever the account.
func (Unattended) View(_ context.Context, _ string, now time.Time) (View, error) {
	keys := make([]string, len(unattendedKeys))
	copy(keys, unattendedKeys[:])
	short := ShortCodes(keys)
	m := Meta{
		AsOf: now, TZ: "UTC", Today: now.UTC().Format(DateLayout), Agent: AgentName,
		Names: map[string]string{}, Codes: map[string]string{}, Keys: map[string][]string{},
		SplitByDevice: true,
	}
	for _, k := range keys {
		m.Names[k], m.Codes[k], m.Keys[k] = CodeName(short[k]), short[k], []string{k}
	}
	var rows []Row
	for _, d := range unattendedDays {
		day := now.UTC().AddDate(0, 0, -d.ago).Format(DateLayout)
		for i, k := range keys {
			if final := d.agent[i] - d.pending[i]; final > 0 {
				rows = append(rows, Row{Day: day, Project: k, Agent: true, Seconds: float64(final)})
			}
			if d.pending[i] > 0 {
				rows = append(rows, Row{Day: day, Project: k, Agent: true, Pending: true, Seconds: float64(d.pending[i])})
			}
		}
	}
	return Build(m, rows)
}
