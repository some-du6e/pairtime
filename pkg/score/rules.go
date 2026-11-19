// SPDX-License-Identifier: AGPL-3.0-or-later

package score

import "github.com/some-du6e/pairtime/pkg/params"

// rules holds the parameters the scorer reads, converted to milliseconds
// where they are durations. Step reads them from the table once, so no rule
// looks a parameter up in a loop, and every read is a params.NAME selector
// the static check in pkg/params can see (TE WP0.6).
type rules struct {
	// SM 8.1, presence and origin.
	sleepGap       int64
	presenceGap    int64
	presenceTail   int64
	returnGapMax   int64
	keysPre        int64
	typingMaxCPS   int64
	substantive    int64 // characters
	errorPasteMax  int64 // characters
	paMinLatencyMS int64

	// SM 8.2, anchor windows.
	thinkLead   int64
	compose     params.Rat // characters per second
	composeMax  int64
	lowLead     int64
	answerLead  int64
	paMax       int64
	paTrail     int64
	intLead     int64
	intTrail    int64
	editLead    int64
	cmdLead     int64
	cmdTrail    int64
	cmdWatchMax int64
	commitLead  int64

	// SM 8.3, reading.
	readWPM     int64
	readHorizon int64

	// DESIGN 6.3.
	pendingWindow int64
	trackInputPre int64
	trackMinGap   int64
	trackLogMatch int64
	trackDedupe   int64

	// settle is how long after a time the scorer may still change what it
	// derived for that time: the pending window for late records (DESIGN
	// 6.2 D1), plus the furthest any SC1 rule looks ahead, which is the
	// return rule's: a silent run of up to RETURN_GAP_MAX_S whose deciding
	// anchor may come READ_HORIZON_S after it (SM 2.1).
	settle int64
}

func newRules(t *params.Table) rules {
	ms := func(p params.IntParam) int64 { return t.Int(p) * 1000 }
	r := rules{
		sleepGap:       ms(params.SLEEP_GAP_S),
		presenceGap:    ms(params.PRESENCE_GAP_S),
		presenceTail:   ms(params.PRESENCE_TAIL_S),
		returnGapMax:   ms(params.RETURN_GAP_MAX_S),
		keysPre:        ms(params.KEYS_PRE_S),
		typingMaxCPS:   t.Int(params.TYPING_MAX_CPS),
		substantive:    t.Int(params.SUBSTANTIVE_CHARS),
		errorPasteMax:  t.Int(params.ERROR_PASTE_TEXT_MAX),
		paMinLatencyMS: t.Int(params.PA_MIN_LATENCY_MS),

		thinkLead:   ms(params.THINK_LEAD_S),
		compose:     t.Rat(params.COMPOSE_CPS),
		composeMax:  ms(params.COMPOSE_MAX_S),
		lowLead:     ms(params.LOW_LEAD_S),
		answerLead:  ms(params.ANSWER_LEAD_S),
		paMax:       ms(params.PA_MAX_S),
		paTrail:     ms(params.PA_TRAIL_S),
		intLead:     ms(params.INT_LEAD_S),
		intTrail:    ms(params.INT_TRAIL_S),
		editLead:    ms(params.EDIT_LEAD_S),
		cmdLead:     ms(params.CMD_LEAD_S),
		cmdTrail:    ms(params.CMD_TRAIL_S),
		cmdWatchMax: ms(params.CMD_WATCH_MAX_S),
		commitLead:  ms(params.COMMIT_LEAD_S),

		readWPM:     t.Int(params.READ_WPM),
		readHorizon: ms(params.READ_HORIZON_S),

		pendingWindow: ms(params.PENDING_WINDOW_S),
		trackInputPre: ms(params.TRACK_INPUT_PRE_S),
		trackMinGap:   ms(params.TRACK_MIN_GAP_S),
		trackLogMatch: ms(params.TRACK_LOG_MATCH_S),
		trackDedupe:   ms(params.TRACK_DEDUPE_S),
	}
	r.settle = r.pendingWindow + r.returnGapMax + r.readHorizon
	return r
}

// roundDiv returns a / b rounded to the nearest integer, halves away from
// zero, for b > 0. SM states its leads in whole seconds and rounds a
// fractional typing time to the nearest one: 61 characters at 1.5 a second
// are 40.67 s, which SM 6.4 counts as 41, and 35 characters are 23.33 s,
// which SM 6.5 counts as 23 (docs/errata/2026-11-19-sm-6.4-prompt-lengths.md
// asks Karim to confirm the rounding).
func roundDiv(a, b int64) int64 {
	if a < 0 {
		return -roundDiv(-a, b)
	}
	return (2*a + b) / (2 * b)
}

// typingMS is the typing part of a lead for n typed characters (SM 2.3):
// n / COMPOSE_CPS, rounded to a whole second and capped at COMPOSE_MAX_S.
func (r rules) typingMS(n int64) int64 {
	if n <= 0 {
		return 0
	}
	ms := roundDiv(n*r.compose.Den(), r.compose.Num()) * 1000
	return min(ms, r.composeMax)
}

// readingMS is how long prose takes to read at READ_WPM, rounded to a
// whole second: the reading part of RETURN_CREDIT (SM 2.1).
func (r rules) readingMS(words int64) int64 {
	if words <= 0 {
		return 0
	}
	return roundDiv(60*words, r.readWPM) * 1000
}
