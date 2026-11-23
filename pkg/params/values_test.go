// SPDX-License-Identifier: AGPL-3.0-or-later

package params

import (
	"fmt"
	"math/big"
	"slices"
	"strconv"
	"testing"
)

// TestValues checks every parameter of every table against the value SM 8
// or DESIGN 6.3 gives it. The rows are copied from those two tables, not
// from the JSON files, so a wrong value in a file fails here, and so does
// a parameter without a row. A table older than the version that added a
// parameter must not have it. So far every table has SM's values; a table
// that changes one will need a row of its own for the older value.
func TestValues(t *testing.T) {
	cases := []struct {
		p param
		// want is the value as SM or DESIGN states it, without units or
		// thousands separators.
		want string
		// src is the table row that states it.
		src string
	}{
		{SAMPLE_S, "15", "SM 8.1"},
		{SLEEP_GAP_S, "30", "SM 8.1"},
		{PRESENCE_GAP_S, "120", "SM 8.1"},
		{PRESENCE_TAIL_S, "30", "SM 8.1"},
		{RETURN_GAP_MAX_S, "600", "SM 8.1"},
		{KEYS_PRE_S, "10", "SM 8.1"},
		{INPUT_RELEASE_GRACE_MS, "250", "SM 8.1"},
		{TYPING_MAX_CPS, "15", "SM 8.1"},
		{SUBSTANTIVE_CHARS, "20", "SM 8.1"},
		{ERROR_PASTE_TEXT_MAX, "40", "SM 8.1"},
		{CRON_PREFIX_CHARS, "1000", "SM 8.1"},
		{PA_MIN_LATENCY_MS, "300", "SM 8.1"},
		{RELAY_MATCH_S, "600", "SM 8.1"},
		{RELAY_SEEN_LAG_S, "60", "SM 8.1"},

		{THINK_LEAD_S, "120", "SM 8.2"},
		{COMPOSE_CPS, "1.5", "SM 8.2"},
		{COMPOSE_MAX_S, "300", "SM 8.2"},
		{LOW_LEAD_S, "20", "SM 8.2"},
		{ANSWER_LEAD_S, "60", "SM 8.2"},
		{PA_MAX_S, "120", "SM 8.2"},
		{PA_TRAIL_S, "10", "SM 8.2"},
		{INT_LEAD_S, "30", "SM 8.2"},
		{INT_TRAIL_S, "10", "SM 8.2"},
		{EDIT_LEAD_S, "120", "SM 8.2"},
		{CMD_LEAD_S, "60", "SM 8.2"},
		{CMD_TRAIL_S, "60", "SM 8.2"},
		{CMD_WATCH_MAX_S, "300", "SM 8.2"},
		{COMMIT_LEAD_S, "60", "SM 8.2"},

		{R_TURN_BASE_S, "20", "SM 8.3"},
		{READ_WPM, "180", "SM 8.3"},
		{DIFF_LPM, "8", "SM 8.3"},
		{TOOL_LINES_MAX, "4", "SM 8.3"},
		{BUCKET_MAX_S, "600", "SM 8.3"},
		{BUCKET_TTL_S, "7200", "SM 8.3"},
		{READ_HORIZON_S, "600", "SM 8.3"},
		{LOOKUP_BRACKET_S, "30", "SM 8.3"},
		{LOOKUP_MAX_S, "180", "SM 8.3"},

		{ATTRIB_STICKY_S, "120", "SM 8.4"},
		{PROC_SCAN_S, "60", "SM 8.4"},

		{MINHASH_PERMS, "64", "SM 8.5"},
		{SHINGLE_WORDS, "3", "SM 8.5 SHINGLE, word 3-shingles"},
		{SHINGLE_CHARS, "5", "SM 8.5 SHINGLE, character 5-shingles"},
		{SHINGLE_MIN_WORDS, "6", "SM 8.5 SHINGLE, character shingles under 6 words"},
		{SIM_JACCARD, "0.8", "SM 8.5"},
		{REROLL_WINDOW_S, "1800", "SM 8.5"},
		{REROLL_FREE_ATTEMPTS, "2", "SM 8.5"},
		{REWRITE_OVERLAP, "0.5", "SM 8.5"},
		{REWRITE_FREE_REPEATS, "1", "SM 8.5"},
		{SAME_ERROR_LIMIT, "3", "SM 8.5"},
		{NOREAD_MIN_WORDS, "100", "SM 8.5"},
		{NOREAD_FRAC, "0.25", "SM 8.5"},
		{NOREAD_WPM, "238", "SM 8.5"},
		{AUTOPILOT_RUN, "3", "SM 8.5"},
		{NO_PROGRESS_LIMIT_S, "7200", "SM 8.5"},
		{NO_PROGRESS_WEIGHT, "0.5", "SM 8.5"},
		{PROGRESS_RESTORE_S, "86400", "SM 8.5"},
		{ERROR_RESOLVED_RUNS, "2", "SM 8.5"},

		{SESSION_GAP_S, "1200", "SM 8.6"},
		{DAY_CAP_S, "43200", "SM 8.6"},
		{DAY_FLAG_S, "36000", "SM 8.6"},
		{UNCOMMITTED_CAP_S, "3600", "SM 8.6"},
		{UNKNOWN_BUCKET_MAX_S, "120", "SM 8.6"},
		{UNKNOWN_PA_MAX_S, "60", "SM 8.6"},
		{WITNESS_LAG_S, "900", "SM 8.6"},

		{PENDING_WINDOW_S, "900", "DESIGN 6.3"},
		{BATCH_S, "60", "DESIGN 6.3"},
		{CHECKPOINT_S, "900", "DESIGN 6.3"},
		{CONTEXT_IDLE_S, "600", "DESIGN 6.3"},
		{SLOW_SAMPLE_S, "60", "DESIGN 6.3"},
		{TRACK_INPUT_PRE_S, "30", "DESIGN 6.3"},
		{TRACK_MIN_GAP_S, "20", "DESIGN 6.3"},
		{TRACK_LOG_MATCH_S, "10", "DESIGN 6.3"},
		{TRACK_DEDUPE_S, "30", "DESIGN 6.3"},
		{OFFLINE_CAP_S, "10800", "DESIGN 6.3"},
		{OFFLINE_PUSH_S, "86400", "DESIGN 6.3"},
		{BUILD_DAY_S, "900", "DESIGN 6.3, SM 8.6 STREAK_DAY_S renamed"},
		{BUILD_DAY_LAG_S, "259200", "DESIGN 6.3"},
		{WEEKLY_GOAL_DEFAULT, "3", "DESIGN 6.3"},
		{FREEZE_EARN_DAYS, "7", "DESIGN 6.3"},
		{FREEZE_MAX, "2", "DESIGN 6.3"},
		{BOARD_WINDOW_D, "90", "DESIGN 6.3"},
		{CONSISTENCY_WINDOW_D, "30", "DESIGN 6.3"},
		{LONG_FIGHT_MIN_S, "1800", "DESIGN 6.3"},
		{REVEAL_POST_S, "900", "DESIGN 6.3"},
		{CHALLENGE_K_MIN, "10", "DESIGN 6.3 CHALLENGE_K, max(10, ...)"},
		{CHALLENGE_K_FRAC, "0.1", "DESIGN 6.3 CHALLENGE_K, 10% of challengeable turns"},
		{CHALLENGE_K_MAX, "30", "DESIGN 6.3 CHALLENGE_K, capped at 30"},
		{PREAPPROVE_ROW_MAX_S, "3600", "DESIGN 6.3"},
		{COMPOSE_NEEDS_KEYS, "false", "DESIGN 6.3"},
		{AUDIT_BASE_P, "0.08", "DESIGN 6.3"},
	}
	for _, tab := range Tables() {
		rows := make(map[string]int)
		for _, c := range cases {
			rows[c.p.String()]++
			if from := since(c.p); from != "" && compareVersions(tab.Version(), from) < 0 {
				t.Run(fmt.Sprintf("table %s has no %s, which %s added", tab.Version(), c.p, from), func(t *testing.T) {
					if got, ok := tab.Text(c.p.String()); ok {
						t.Errorf("table %s has %s = %s", tab.Version(), c.p, got)
					}
				})
				continue
			}
			t.Run(fmt.Sprintf("table %s: %s is %s per %s", tab.Version(), c.p.String(), c.want, c.src), func(t *testing.T) {
				if got, ok := tab.Text(c.p.String()); !ok {
					t.Fatalf("table %s has no %s", tab.Version(), c.p.String())
				} else if got != c.want {
					t.Errorf("%s = %s in the table, want %s (%s)", c.p.String(), got, c.want, c.src)
				}
				switch p := c.p.(type) {
				case IntParam:
					want, err := strconv.ParseInt(c.want, 10, 64)
					if err != nil {
						t.Fatalf("the row for %s isn't an integer: %v", p, err)
					}
					if got := tab.Int(p); got != want {
						t.Errorf("Int(%s) = %d, want %d", p, got, want)
					}
				case RatParam:
					want, ok := new(big.Rat).SetString(c.want)
					if !ok {
						t.Fatalf("the row for %s isn't a decimal: %q", p, c.want)
					}
					if got := tab.Rat(p); got.Num() != want.Num().Int64() || got.Den() != want.Denom().Int64() {
						t.Errorf("Rat(%s) = %s, want %s", p, got, want)
					}
				case BoolParam:
					if got := strconv.FormatBool(tab.Bool(p)); got != c.want {
						t.Errorf("Bool(%s) = %s, want %s", p, got, c.want)
					}
				}
			})
		}
		t.Run("table "+tab.Version()+": every parameter has exactly one row", func(t *testing.T) {
			for _, name := range tab.Names() {
				if rows[name] != 1 {
					t.Errorf("%s has %d rows, want 1 with the value SM or DESIGN gives it", name, rows[name])
				}
			}
			for _, p := range all {
				if rows[p.String()] == 0 {
					t.Errorf("%s has no row", p)
				}
			}
			for name := range rows {
				if !slices.ContainsFunc(all, func(p param) bool { return p.String() == name }) {
					t.Errorf("a row checks %s, which no table has", name)
				}
			}
		})
	}
}

// TestRationals pins the fraction behind every rational parameter, worked
// out by hand from its decimal, so a parser that rounds or doesn't reduce
// to lowest terms fails.
func TestRationals(t *testing.T) {
	tab, err := ByVersion("0.1.0")
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		p        RatParam
		num, den int64
	}{
		{COMPOSE_CPS, 3, 2},
		{SIM_JACCARD, 4, 5},
		{REWRITE_OVERLAP, 1, 2},
		{NOREAD_FRAC, 1, 4},
		{NO_PROGRESS_WEIGHT, 1, 2},
		{CHALLENGE_K_FRAC, 1, 10},
		{AUDIT_BASE_P, 2, 25},
	}
	covered := make(map[RatParam]bool)
	for _, c := range cases {
		covered[c.p] = true
		t.Run(fmt.Sprintf("%s is exactly %d/%d", c.p, c.num, c.den), func(t *testing.T) {
			if got := tab.Rat(c.p); got.Num() != c.num || got.Den() != c.den {
				t.Errorf("Rat(%s) = %s, want %d/%d", c.p, got, c.num, c.den)
			}
		})
	}
	t.Run("every rational parameter has a row", func(t *testing.T) {
		for _, p := range all {
			if r, ok := p.(RatParam); ok && !covered[r] {
				t.Errorf("%s has no row", r)
			}
		}
	})
}

// TestReplacedNames checks that the SM 8 names this table replaces are
// gone from every table, so nothing can read them by mistake.
func TestReplacedNames(t *testing.T) {
	cases := []struct {
		name string
		why  string
	}{
		{"CHAIN_HEAD_INTERVAL_S", "BATCH_S replaces it (DESIGN 6.2 D8)"},
		{"STREAK_DAY_S", "BUILD_DAY_S renames it (DESIGN 6.3)"},
		{"SHINGLE", "SHINGLE_WORDS, SHINGLE_CHARS and SHINGLE_MIN_WORDS hold its three numbers"},
		{"CHALLENGE_K", "CHALLENGE_K_MIN, CHALLENGE_K_FRAC and CHALLENGE_K_MAX hold its three numbers"},
	}
	for _, tab := range Tables() {
		for _, c := range cases {
			t.Run(fmt.Sprintf("table %s has no %s because %s", tab.Version(), c.name, c.why), func(t *testing.T) {
				if v, ok := tab.Text(c.name); ok {
					t.Errorf("table %s has %s = %s", tab.Version(), c.name, v)
				}
			})
		}
	}
}
