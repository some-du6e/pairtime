// SPDX-License-Identifier: AGPL-3.0-or-later

package dashboard

import (
	"context"
	"maps"
	"slices"
	"time"

	"github.com/some-du6e/pairtime/internal/server/rollup"
)

// AgentName is what a real dashboard calls the agent. Tier-1 buckets
// don't say which harness ran in a minute (DESIGN 4.3), so until the
// dashboard reads the harness names the devices' labels list, it names
// the agent this way rather than guess. Its time is the bucket's
// agent_only, per project (SM 2.6, D-bucket-agent-only).
const AgentName = "your agent"

// Rollups is the Source of a signed-in account's dashboard: its
// rollups_daily rows, counted in its time zone (package rollup).
type Rollups struct {
	// DB is where to read, store.DB.Read.
	DB rollup.Querier
}

// View returns the account's last MonthDays local days as of now. Its
// projects are the account-level ones the rollups count
// (D-account-project-id), each called by the short code of its first
// device key (ShortCodes). Until project registration (WP-SV10) links the
// keys of one repo, each device's key is a project of its own, and the
// view says so.
func (r Rollups) View(ctx context.Context, account string, now time.Time) (View, error) {
	zone, name, err := rollup.Zone(ctx, r.DB, account)
	if err != nil {
		return View{}, err
	}
	// Today is the local date now, from the instant itself. The window
	// steps back by whole dates at noon UTC, which no time zone's daylight
	// saving time can move.
	y, m, d := now.In(zone).Date()
	today := time.Date(y, m, d, 12, 0, 0, 0, time.UTC)
	from := today.AddDate(0, 0, -(MonthDays - 1)).Format(DateLayout)
	days, err := rollup.Read(ctx, r.DB, account, from, today.Format(DateLayout))
	if err != nil {
		return View{}, err
	}
	keys, err := rollup.Projects(ctx, r.DB, account)
	if err != nil {
		return View{}, err
	}
	// The codes are unique among all the account's projects, not only the
	// month's, so a project keeps its code while others come and go.
	// rollup_projects keeps a project's keys as long as the account, so a
	// code comes from the id only for a project nothing maps.
	codeFrom := make(map[string]string, len(keys)) // by project id
	for id, k := range keys {
		if len(k) > 0 {
			codeFrom[id] = k[0]
		}
	}
	for _, day := range days {
		if _, ok := codeFrom[day.ProjectID]; !ok && day.ProjectID != "" {
			codeFrom[day.ProjectID] = day.ProjectID
		}
	}
	short := ShortCodes(slices.Collect(maps.Values(codeFrom)))
	names := make(map[string]string, len(codeFrom))
	codes := make(map[string]string, len(codeFrom))
	for id, key := range codeFrom {
		codes[id] = short[key]
		names[id] = CodeName(short[key])
	}
	var rows []Row
	for _, day := range days {
		id := day.ProjectID
		if day.Kind == "" {
			rows = append(rows,
				Row{Day: day.Day, Project: id, Agent: true, Seconds: seconds(day.AgentOnlyFinalMS)},
				Row{Day: day.Day, Project: id, Agent: true, Pending: true, Seconds: seconds(day.AgentOnlyPendingMS)})
			continue
		}
		for i, level := range rollup.Levels {
			rows = append(rows,
				Row{Day: day.Day, Project: id, Kind: day.Kind, Level: level, Seconds: seconds(day.FinalMS[i])},
				Row{Day: day.Day, Project: id, Kind: day.Kind, Level: level, Pending: true, Seconds: seconds(day.PendingMS[i])})
		}
	}
	return Build(Meta{
		AsOf: now, TZ: name, Today: today.Format(DateLayout), Agent: AgentName,
		Names: names, Codes: codes, Keys: keys,
		// TODO(WP-SV10): false once registration links the keys of one
		// repo across devices.
		SplitByDevice: true,
	}, rows)
}

// seconds turns milliseconds into seconds. Credit moves in steps of half
// a second, which float64 holds exactly.
func seconds(ms int64) float64 { return float64(ms) / 1000 }

// CodeLen is how many characters a project's short code has, unless
// another of the account's projects shares them.
const CodeLen = 6

// ShortCodes gives each of keys, the device keys an account's projects
// are called by, the short code the owner's dashboard calls its project
// by, since the server never learns a project's name unless the owner
// publishes it. A code is the first CodeLen characters of the key after
// its kind prefix: the git adapter's keys start with a letter that says
// what kind of identity the key came from (g for a repo's root commits, d
// for a folder), and 32 hex digits, so "g7c41e9…" has the code "7c41e9".
// A key of any other form gives its own first characters. Two keys whose
// codes would be the same both get longer ones, a character at a time,
// until they differ, and no other key's code changes; keys that differ
// only in their kind prefix keep it. So every key's code is unique among
// keys. Keys are opaque HMACs under a device secret (DESIGN 4.3), so a
// code says nothing about its project either.
func ShortCodes(keys []string) map[string]string {
	codes := make(map[string]string, len(keys))
	var assign func(group []string, n int)
	assign = func(group []string, n int) {
		byCode := make(map[string][]string, len(group))
		for _, k := range group {
			body := codeBody(k)
			c := body[:min(n, len(body))]
			byCode[c] = append(byCode[c], k)
		}
		for c, ks := range byCode {
			whole := true
			for _, k := range ks {
				whole = whole && len(codeBody(k)) <= n
			}
			switch {
			case len(ks) == 1:
				codes[ks[0]] = c
			case whole:
				// The same body under two kind prefixes: only the
				// prefix tells them apart.
				for _, k := range ks {
					codes[k] = k
				}
			default:
				assign(ks, n+1)
			}
		}
	}
	assign(slices.Compact(slices.Sorted(slices.Values(keys))), CodeLen)
	return codes
}

// codeBody is what a key's short code is cut from: one of the git
// adapter's keys without its kind prefix, or any other key whole.
func codeBody(key string) string {
	if len(key) != 33 || key[0] < 'a' || key[0] > 'z' {
		return key
	}
	for i := 1; i < len(key); i++ {
		if c := key[i]; (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return key
		}
	}
	return key[1:]
}

// CodeName is what the owner's dashboard calls a project with the short
// code code: "Project 7c41e9".
func CodeName(code string) string { return "Project " + code }
