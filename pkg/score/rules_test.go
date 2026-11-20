// SPDX-License-Identifier: AGPL-3.0-or-later

package score

import (
	"testing"

	"github.com/some-du6e/pairtime/pkg/params"
)

// lateMarginMS is the part of PENDING_WINDOW_S that DESIGN 3.7 leaves past
// READ_HORIZON_S for records that arrive late: bridge heartbeats,
// transcript lines, an inbox drained after an agent restart, and a sample
// slowed to one a minute (D14).
const lateMarginMS = 300_000

// TestLookAheads checks, for every parameter table pkg/params embeds, that
// no rule looks further ahead than READ_HORIZON_S and that PENDING_WINDOW_S
// keeps every look-ahead at least lateMarginMS inside it, so a record up to
// that late changes only what is still pending and the Finals of a Step per
// minute are what one Step gives (DESIGN 3.7, 6.2 D1;
// docs/errata/2026-11-19-return-rule-lookahead.md, decided 2026-11-20 as
// E-return-lookahead). A MINOR table that broke one of these would change
// minutes the agent has already called final.
func TestLookAheads(t *testing.T) {
	for _, tab := range params.Tables() {
		r := newRules(tab)
		ms := func(p params.IntParam) int64 { return tab.Int(p) * 1000 }
		cases := []struct {
			name string
			got  int64
			// want is exact when exact is set, else a limit got must not
			// pass.
			want  int64
			exact bool
		}{
			{"reach is READ_HORIZON_S, the bound DESIGN 3.7 sets for every rule", r.reach, ms(params.READ_HORIZON_S), true},
			{"settle is PENDING_WINDOW_S, after which a minute is final (DESIGN 6.2 D1)", r.settle, ms(params.PENDING_WINDOW_S), true},
			{"a substantive prompt's lead, THINK_LEAD_S + COMPOSE_MAX_S, is within READ_HORIZON_S", r.thinkLead + r.composeMax, r.reach, false},
			{"an answer's lead, ANSWER_LEAD_S + COMPOSE_MAX_S, is within READ_HORIZON_S", r.answerLead + r.composeMax, r.reach, false},
			{"a go-ahead's lead, LOW_LEAD_S, is within READ_HORIZON_S", r.lowLead, r.reach, false},
			{"a permission's credited wait, PA_MAX_S, is within READ_HORIZON_S", r.paMax, r.reach, false},
			{"an interrupt's lead, INT_LEAD_S, is within READ_HORIZON_S", r.intLead, r.reach, false},
			{"a typed edit's or navigation's lead, EDIT_LEAD_S, is within READ_HORIZON_S", r.editLead, r.reach, false},
			{"a typed command's lead and watched run, CMD_LEAD_S + CMD_WATCH_MAX_S, is within READ_HORIZON_S", r.cmdLead + r.cmdWatchMax, r.reach, false},
			{"a human commit's lead, COMMIT_LEAD_S, is within READ_HORIZON_S", r.commitLead, r.reach, false},
			{"the presence bridge, PRESENCE_GAP_S, which waits for the input after a silence, is within READ_HORIZON_S", r.presenceGap, r.reach, false},
			{"TRACK_DEDUPE_S, how far after a track call a hook or log record still decides it, is within READ_HORIZON_S (DESIGN 5.4)", r.trackDedupe, r.reach, false},
			{"TRACK_LOG_MATCH_S, how far after a side-door prompt a hook or log prompt still makes it a cross-check, is within READ_HORIZON_S (DESIGN 5.2)", r.trackLogMatch, r.reach, false},
			{"PENDING_WINDOW_S leaves at least 300 s past READ_HORIZON_S for late records (DESIGN 3.7)", r.reach + lateMarginMS, r.settle, false},
			{"a sample slowed to one a minute shows input at most twice SLOW_SAMPLE_S late, inside those 300 s (DESIGN 6.2 D14)", r.slowGap, lateMarginMS, false},
		}
		for _, c := range cases {
			t.Run(tab.Version()+": "+c.name, func(t *testing.T) {
				switch {
				case c.exact && c.got != c.want:
					t.Errorf("%d ms, want %d ms", c.got, c.want)
				case !c.exact && c.got > c.want:
					t.Errorf("%d ms, more than %d ms", c.got, c.want)
				}
			})
		}
	}
}
