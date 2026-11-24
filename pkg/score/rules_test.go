// SPDX-License-Identifier: AGPL-3.0-or-later

package score

import (
	"testing"

	"github.com/some-du6e/pairtime/pkg/params"
)

// lateMarginMS is the part of PENDING_WINDOW_S that E-return-lookahead
// leaves past READ_HORIZON_S for records that arrive late, the ones DESIGN
// 3.7 names (bridge heartbeats, transcript lines, an inbox drained after an
// agent restart) and a sample slowed to one a minute (D14).
const lateMarginMS = 300_000

// scorerTables returns the embedded parameter tables the scorer can score
// with: those that hold every parameter it reads, as NewState requires.
// Table 0.1.0 predates INPUT_RELEASE_GRACE_MS and table 0.2.0 predates
// RELAY_MATCH_S and RELAY_SEEN_LAG_S, so neither is one.
func scorerTables(t *testing.T) []*params.Table {
	t.Helper()
	var out []*params.Table
	for _, tab := range params.Tables() {
		if holdsRules(tab) {
			out = append(out, tab)
		}
	}
	if len(out) == 0 {
		t.Fatal("the scorer can score with none of the embedded tables")
	}
	return out
}

// holdsRules reports whether newRules can read every parameter it needs
// from tab. Table.Int and the others panic on a parameter tab lacks.
func holdsRules(tab *params.Table) (ok bool) {
	defer func() {
		if recover() != nil {
			ok = false
		}
	}()
	newRules(tab)
	return true
}

// TestLookAheads checks, for every parameter table pkg/params embeds that
// the scorer can score with, that no rule looks further ahead than
// READ_HORIZON_S and that PENDING_WINDOW_S keeps every look-ahead at least
// lateMarginMS inside it, so a record up to that late changes only what is
// still pending and the Finals of a Step per minute are what one Step
// gives (DESIGN 6.2 D1;
// docs/errata/2026-11-19-return-rule-lookahead.md, decided 2026-11-20 as
// E-return-lookahead). It pins the code half of that rule change, whose
// DESIGN 3.7 and D1 text lands with P10. A MINOR table that broke one of
// these would change minutes the agent has already called final.
func TestLookAheads(t *testing.T) {
	for _, tab := range scorerTables(t) {
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
			{"reach is READ_HORIZON_S, the bound E-return-lookahead sets for every rule", r.reach, ms(params.READ_HORIZON_S), true},
			{"settle is PENDING_WINDOW_S, after which a minute is final (DESIGN 6.2 D1)", r.settle, ms(params.PENDING_WINDOW_S), true},
			{"a substantive prompt's lead, THINK_LEAD_S + COMPOSE_MAX_S, is within READ_HORIZON_S", r.thinkLead + r.composeMax, r.reach, false},
			{"an answer's lead, ANSWER_LEAD_S + COMPOSE_MAX_S, is within READ_HORIZON_S", r.answerLead + r.composeMax, r.reach, false},
			{"a go-ahead's lead, LOW_LEAD_S, is within READ_HORIZON_S", r.lowLead, r.reach, false},
			{"a permission's credited wait, PA_MAX_S, is within READ_HORIZON_S", r.paMax, r.reach, false},
			{"an interrupt's lead, INT_LEAD_S, is within READ_HORIZON_S", r.intLead, r.reach, false},
			{"a typed edit's or navigation's lead, EDIT_LEAD_S, is within READ_HORIZON_S", r.editLead, r.reach, false},
			{"a typed command's lead and watched run, CMD_LEAD_S + CMD_WATCH_MAX_S, is within READ_HORIZON_S", r.cmdLead + r.cmdWatchMax, r.reach, false},
			{"a human commit's lead, COMMIT_LEAD_S, is within READ_HORIZON_S", r.commitLead, r.reach, false},
			{"INPUT_RELEASE_GRACE_MS, how far past a prompt's submit SM 2.2 rules 6 and 7 read input, is within READ_HORIZON_S", r.releaseGrace, r.reach, false},
			{"the presence bridge, PRESENCE_GAP_S, which waits for the input after a silence, is within READ_HORIZON_S", r.presenceGap, r.reach, false},
			{"TRACK_DEDUPE_S, how far after a track call a hook or log record still decides it, is within READ_HORIZON_S (E-track-windows)", r.trackDedupe, r.reach, false},
			{"TRACK_LOG_MATCH_S, how far after a side-door prompt a hook or log prompt still makes it a cross-check, is within READ_HORIZON_S (DESIGN 5.2)", r.trackLogMatch, r.reach, false},
			{"PENDING_WINDOW_S leaves at least 300 s past READ_HORIZON_S for late records (E-return-lookahead)", r.reach + lateMarginMS, r.settle, false},
			{"a record that ends a sample's reach takes effect once the newest record is that allowance, PENDING_WINDOW_S - READ_HORIZON_S, past it, so a sample up to that late keeps the reach (SM 2.1, WP-HL1)", r.late, r.settle - r.reach, true},
			{"a relayed lead, COMPOSE_MAX_S before its stamp, is still pending when its pairing comes RELAY_MATCH_S after the stamp (SM 8.1)", r.relayMatch + r.composeMax, r.settle, false},
			{"a prompt's longest lead, THINK_LEAD_S + COMPOSE_MAX_S, ending at an app's stamp, is within READ_HORIZON_S of it, so reach from the stamp never cuts it (SM 2.3)", r.thinkLead + r.composeMax, r.reach, false},
			{"RELAY_SEEN_LAG_S is within RELAY_MATCH_S, so a pairing, the later of its two records, comes at most RELAY_MATCH_S after the stamp (SM 2.2)", r.relaySeenLag, r.relayMatch, false},
			{"the deadline for the pairing that vouches for a stop's relayed reading is PENDING_WINDOW_S - 60 s after the stop (SM 2.4)", r.relayVouch, ms(params.PENDING_WINDOW_S) - 60_000, true},
			{"that deadline comes while the stop's own minute is still pending, so a vouching pairing never lands in a final minute (SM 2.4)", r.relayVouch, r.settle - 1, false},
			{"a relayed reading, at most READ_HORIZON_S before the stamp of the anchor that vouches for it, is within reach of the stamp (SM 2.3)", r.readHorizon, r.reach, false},
			{"a sample slowed to one a minute shows input at most twice SLOW_SAMPLE_S late, inside those 300 s (D14, as pkg/score reads it)", r.slowGap, lateMarginMS, false},
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
