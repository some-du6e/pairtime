// SPDX-License-Identifier: AGPL-3.0-or-later

package score

import (
	"math"

	"github.com/some-du6e/pairtime/pkg/params"
)

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

	// SM 8.3, the reading bucket and lookups. The bucket counts in units
	// of 1 / (READ_WPM * DIFF_LPM) of a second, in which every fill is a
	// whole number: a word of prose is 60 * DIFF_LPM units and a line of
	// code or diff 60 * READ_WPM (SM 2.4).
	turnBase      int64 // ms
	readWPM       int64
	diffLPM       int64
	toolLinesMax  int64 // lines
	bucketMax     int64 // ms
	bucketTTL     int64 // ms
	readHorizon   int64
	lookupBracket int64
	lookupMax     int64
	unitsPerS     int64

	// DESIGN 6.3.
	pendingWindow int64
	contextIdle   int64
	// slowGap is the longest gap between samples that isn't a hard break
	// once the agent records one sample a minute (DESIGN 6.2 D14): twice
	// SLOW_SAMPLE_S, as SLEEP_GAP_S is twice SAMPLE_S.
	slowGap       int64
	trackInputPre int64
	trackMinGap   int64
	trackLogMatch int64
	trackDedupe   int64
	// composeNeedsKeys realizes the typing part of every lead only in
	// slots with key presses, on every machine (DESIGN 6.3, 6.7).
	composeNeedsKeys bool

	// reach is the furthest back from its own t that a record may change
	// what the scorer derived: READ_HORIZON_S, the bound DESIGN 3.7 and 6.2
	// D1 set (docs/errata/2026-11-19-return-rule-lookahead.md, decided
	// 2026-11-20 as E-return-lookahead). Reading needs a human act within it
	// (SM 2.4), the return rule an act on record within it of the silence's
	// start (SM 2.1), and a lead, at most THINK_LEAD_S + COMPOSE_MAX_S, fits
	// inside it, as do TRACK_LOG_MATCH_S and TRACK_DEDUPE_S, the windows in
	// which a hook or log record decides a side-door prompt or a track call
	// (DESIGN 5.2, 5.4). Where a record would reach further, its effect is
	// cut at its t minus reach, in every Step alike: a typed command's lead
	// and run when its record comes at the end of a run longer than about
	// nine minutes; a permission's wait when its record comes more than
	// eight minutes after the answer; a turn's run across a longer silence
	// of its agent's records outside hooked sessions (turnBuild.runUntil);
	// and a queued message's reset when no agent record follows within it.
	reach int64
	// settle is how long after a time the scorer may still change what it
	// derived for that time: PENDING_WINDOW_S, after which DESIGN 6.2 D1
	// makes a minute final. That leaves PENDING_WINDOW_S minus reach, 300 s,
	// for records that arrive late, so whatever a record changes is still
	// pending when it arrives up to that late, and the Finals of a Step per
	// minute are what one Step gives. A browser run's lookup waits for its
	// end, which can be later still, and Step holds FinalBefore for it on
	// its own (docs/errata/2026-11-20-lookup-lookahead.md).
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

		turnBase:      ms(params.R_TURN_BASE_S),
		readWPM:       t.Int(params.READ_WPM),
		diffLPM:       t.Int(params.DIFF_LPM),
		toolLinesMax:  t.Int(params.TOOL_LINES_MAX),
		bucketMax:     ms(params.BUCKET_MAX_S),
		bucketTTL:     ms(params.BUCKET_TTL_S),
		readHorizon:   ms(params.READ_HORIZON_S),
		lookupBracket: ms(params.LOOKUP_BRACKET_S),
		lookupMax:     ms(params.LOOKUP_MAX_S),

		pendingWindow: ms(params.PENDING_WINDOW_S),
		contextIdle:   ms(params.CONTEXT_IDLE_S),
		slowGap:       ms(params.SLOW_SAMPLE_S) * t.Int(params.SLEEP_GAP_S) / t.Int(params.SAMPLE_S),
		trackInputPre: ms(params.TRACK_INPUT_PRE_S),
		trackMinGap:   ms(params.TRACK_MIN_GAP_S),
		trackLogMatch: ms(params.TRACK_LOG_MATCH_S),
		trackDedupe:   ms(params.TRACK_DEDUPE_S),

		composeNeedsKeys: t.Bool(params.COMPOSE_NEEDS_KEYS),
	}
	r.unitsPerS = r.readWPM * r.diffLPM
	r.reach = r.readHorizon
	r.settle = r.pendingWindow
	return r
}

// countMax bounds every count the scorer reads from a record: words, lines
// and characters. event.Validate only asks that a count not be negative, so
// a valid record may carry any int, and a hostile one carries 2^62. At
// countMax a count is far past every cap it meets (a turn's 600 s of
// reading, a lead's 300 s of typing, a silent run's 600 s), so clamping
// changes no result. It keeps every product the rules form, and every sum
// of clamped counts, inside int64, and it fits an int on 32-bit targets.
const countMax = math.MaxInt32

// count returns a count from a record, clamped to [0, countMax]. Every
// count the scorer reads goes through it before any arithmetic.
func count(n int) int64 { return min(max(int64(n), 0), countMax) }

// addCount returns the sum of two clamped counts, saturated at countMax.
func addCount(a, b int64) int64 { return min(a+b, countMax) }

// unitsMax bounds a sum of reading-bucket units, such as all the output of
// one turn, so that converting it to milliseconds can't overflow.
const unitsMax = math.MaxInt64 / 1000

// addUnits returns the sum of two amounts of units in [0, unitsMax],
// saturated at unitsMax.
func addUnits(a, b int64) int64 { return min(a+b, unitsMax) }

// units converts milliseconds to reading-bucket units.
func (r rules) units(ms int64) int64 { return ms * r.unitsPerS / 1000 }

// proseUnits is what words of prose fill: 60 * words / READ_WPM seconds.
// words is a clamped count.
func (r rules) proseUnits(words int64) int64 { return 60 * min(words, countMax) * r.diffLPM }

// lineUnits is what lines of code, diff or tool output fill: 60 * lines /
// DIFF_LPM seconds. lines is a clamped count, or the sum of two.
func (r rules) lineUnits(lines int64) int64 { return 60 * min(lines, 2*countMax) * r.readWPM }

// unitsMS converts reading-bucket units to milliseconds, rounded to the
// nearest. u is at most unitsMax.
func (r rules) unitsMS(u int64) int64 { return roundDiv(min(u, unitsMax)*1000, r.unitsPerS) }

// roundDiv returns a / b rounded to the nearest integer, halves away from
// zero, for any a and any b > 0. SM 1.3 rounds a lead's typing part and the
// reading part of RETURN_CREDIT to the nearest second, halves up: 61
// characters at 1.5 a second are 40.67 s, which is 41 (SM 6.4), and 35
// characters are 23.33 s, which is 23 (SM 6.5)
// (docs/errata/2026-11-19-sm-6.4-prompt-lengths.md, decided 2026-11-20 as
// E-6.4-prompt-lengths).
//
// It works from Go's truncated quotient and remainder, so it negates
// nothing it can't: |r| < b, and the quotient moves by one only when b > 1,
// where it can't be math.MinInt64 or math.MaxInt64.
func roundDiv(a, b int64) int64 {
	q, r := a/b, a%b
	if r < 0 {
		r = -r
	}
	// r >= b - r is 2r >= b without the overflow.
	if r != 0 && r >= b-r {
		if a < 0 {
			q--
		} else {
			q++
		}
	}
	return q
}

// typingMS is the typing part of a lead for n typed characters (SM 2.3):
// n / COMPOSE_CPS, rounded to the nearest second (SM 1.3) and capped at
// COMPOSE_MAX_S.
func (r rules) typingMS(n int64) int64 {
	if n <= 0 {
		return 0
	}
	ms := roundDiv(min(n, countMax)*r.compose.Den(), r.compose.Num()) * 1000
	return min(ms, r.composeMax)
}

// readingMS is how long prose takes to read at READ_WPM, rounded to the
// nearest second (SM 1.3): the reading part of RETURN_CREDIT (SM 2.1).
func (r rules) readingMS(words int64) int64 {
	if words <= 0 {
		return 0
	}
	return roundDiv(60*min(words, countMax), r.readWPM) * 1000
}
