// SPDX-License-Identifier: AGPL-3.0-or-later

package params

// The three parameter types are structs with one unexported field, so only
// this package can make a name; "Reading parameters" in the package
// documentation says why. Their fields have different names, which gives
// the types different underlying types, so no conversion turns a name of
// one type into a name of another.

// IntParam names a parameter whose value is an integer. [Table.Int] reads
// it. The unit is in the name: _S seconds, _MS milliseconds, _D days, _CPS
// characters per second, _WPM words and _LPM lines per minute; the rest are
// counts.
type IntParam struct{ intName string }

// RatParam names a parameter whose value is an exact rational, which the
// table writes as a decimal such as 1.5. [Table.Rat] reads it.
type RatParam struct{ ratName string }

// BoolParam names a flag. [Table.Bool] reads it.
type BoolParam struct{ flagName string }

// SM 8.1, presence and origin.
var (
	// SAMPLE_S is the interval between OS idle-time samples.
	SAMPLE_S = IntParam{"SAMPLE_S"}
	// SLEEP_GAP_S is the longest gap between samples before the machine
	// counts as asleep or the agent as frozen, a hard break.
	SLEEP_GAP_S = IntParam{"SLEEP_GAP_S"}
	// PRESENCE_GAP_S is the longest no-input run that counts as present
	// when there is input on both sides of it (fit).
	PRESENCE_GAP_S = IntParam{"PRESENCE_GAP_S"}
	// PRESENCE_TAIL_S is how long presence lasts after the last input
	// before a longer gap.
	PRESENCE_TAIL_S = IntParam{"PRESENCE_TAIL_S"}
	// RETURN_GAP_MAX_S is the longest silence the return rule can bridge.
	RETURN_GAP_MAX_S = IntParam{"RETURN_GAP_MAX_S"}
	// KEYS_PRE_S is how soon before a prompt is submitted input must come
	// for the prompt to count as typed by a person.
	KEYS_PRE_S = IntParam{"KEYS_PRE_S"}
	// TYPING_MAX_CPS is the fastest plausible typing; typed length beyond
	// it counts as pasted.
	TYPING_MAX_CPS = IntParam{"TYPING_MAX_CPS"}
	// SUBSTANTIVE_CHARS is the typed characters a prompt needs to be
	// substantive.
	SUBSTANTIVE_CHARS = IntParam{"SUBSTANTIVE_CHARS"}
	// ERROR_PASTE_TEXT_MAX is the typed characters outside a pasted error
	// below which the prompt is an error paste, not a hypothesis.
	ERROR_PASTE_TEXT_MAX = IntParam{"ERROR_PASTE_TEXT_MAX"}
	// CRON_PREFIX_CHARS is the length of the prompt prefix matched against
	// Stop.session_crons[].prompt.
	CRON_PREFIX_CHARS = IntParam{"CRON_PREFIX_CHARS"}
	// PA_MIN_LATENCY_MS is the fastest permission answer a person gives;
	// faster ones come from a script.
	PA_MIN_LATENCY_MS = IntParam{"PA_MIN_LATENCY_MS"}
)

// SM 8.2, anchor windows.
var (
	// THINK_LEAD_S is the think allowance before a substantive act (fit).
	THINK_LEAD_S = IntParam{"THINK_LEAD_S"}
	// COMPOSE_CPS is the typing speed the typing part of a lead allows
	// for (fit).
	COMPOSE_CPS = RatParam{"COMPOSE_CPS"}
	// COMPOSE_MAX_S caps the typing part of a lead.
	COMPOSE_MAX_S = IntParam{"COMPOSE_MAX_S"}
	// LOW_LEAD_S is the lead of a go-ahead.
	LOW_LEAD_S = IntParam{"LOW_LEAD_S"}
	// ANSWER_LEAD_S is the think part of an answer to the agent's
	// question.
	ANSWER_LEAD_S = IntParam{"ANSWER_LEAD_S"}
	// PA_MAX_S is the longest credited wait between a permission request
	// and its answer.
	PA_MAX_S = IntParam{"PA_MAX_S"}
	// PA_TRAIL_S is the trail after a permission answer.
	PA_TRAIL_S = IntParam{"PA_TRAIL_S"}
	// INT_LEAD_S is the lead of an interrupt.
	INT_LEAD_S = IntParam{"INT_LEAD_S"}
	// INT_TRAIL_S is the trail of an interrupt.
	INT_TRAIL_S = IntParam{"INT_TRAIL_S"}
	// EDIT_LEAD_S is the lead of a typed edit or editor navigation.
	EDIT_LEAD_S = IntParam{"EDIT_LEAD_S"}
	// CMD_LEAD_S is the lead of a typed command.
	CMD_LEAD_S = IntParam{"CMD_LEAD_S"}
	// CMD_TRAIL_S is the trail of a typed command, for reading its result.
	CMD_TRAIL_S = IntParam{"CMD_TRAIL_S"}
	// CMD_WATCH_MAX_S is the longest credited run of a command the person
	// started.
	CMD_WATCH_MAX_S = IntParam{"CMD_WATCH_MAX_S"}
	// COMMIT_LEAD_S is the lead of a human commit.
	COMMIT_LEAD_S = IntParam{"COMMIT_LEAD_S"}
)

// SM 8.3, reading bucket and lookups.
var (
	// R_TURN_BASE_S is what each human-owned turn puts in the reading
	// bucket when it is submitted, which is all a turn that shows only a
	// spinner gets.
	R_TURN_BASE_S = IntParam{"R_TURN_BASE_S"}
	// READ_WPM is the prose reading speed (fit).
	READ_WPM = IntParam{"READ_WPM"}
	// DIFF_LPM is the diff and code reading speed (fit).
	DIFF_LPM = IntParam{"DIFF_LPM"}
	// TOOL_LINES_MAX is the lines of tool output a harness shows per call
	// before collapsing the rest.
	TOOL_LINES_MAX = IntParam{"TOOL_LINES_MAX"}
	// BUCKET_MAX_S is the most reading one turn can bank (fit).
	BUCKET_MAX_S = IntParam{"BUCKET_MAX_S"}
	// BUCKET_TTL_S is how long output stays in the reading bucket.
	BUCKET_TTL_S = IntParam{"BUCKET_TTL_S"}
	// READ_HORIZON_S is how far before or after a human act in the
	// project reading can count.
	READ_HORIZON_S = IntParam{"READ_HORIZON_S"}
	// LOOKUP_BRACKET_S is how close project work must be on each side of a
	// browser run for it to be a lookup.
	LOOKUP_BRACKET_S = IntParam{"LOOKUP_BRACKET_S"}
	// LOOKUP_MAX_S is the longest credited browser or chat-app run (fit).
	LOOKUP_MAX_S = IntParam{"LOOKUP_MAX_S"}
)

// SM 8.4, attribution.
var (
	// ATTRIB_STICKY_S is how long a multi-session terminal keeps the
	// project of its last anchor when no upcoming lead applies.
	ATTRIB_STICKY_S = IntParam{"ATTRIB_STICKY_S"}
	// PROC_SCAN_S is the interval of the harness process scan.
	PROC_SCAN_S = IntParam{"PROC_SCAN_S"}
)

// SM 8.5, dark flow and progress.
var (
	// MINHASH_PERMS is the number of MinHash values per prompt. The event
	// model fixes the same number as event.MinHashSize, and the ledger
	// commits the sketches, so a new value is a change to both.
	MINHASH_PERMS = IntParam{"MINHASH_PERMS"}
	// SHINGLE_WORDS is the length of a word shingle, in words (SM 8.5
	// SHINGLE).
	SHINGLE_WORDS = IntParam{"SHINGLE_WORDS"}
	// SHINGLE_CHARS is the length of a character shingle, in characters
	// (SM 8.5 SHINGLE).
	SHINGLE_CHARS = IntParam{"SHINGLE_CHARS"}
	// SHINGLE_MIN_WORDS is the fewest words a prompt needs for word
	// shingles; shorter prompts use character shingles (SM 8.5 SHINGLE).
	SHINGLE_MIN_WORDS = IntParam{"SHINGLE_MIN_WORDS"}
	// SIM_JACCARD is the estimated Jaccard similarity at which two prompts
	// are near-duplicates (fit).
	SIM_JACCARD = RatParam{"SIM_JACCARD"}
	// REROLL_WINDOW_S is the window in which re-rolls are counted.
	REROLL_WINDOW_S = IntParam{"REROLL_WINDOW_S"}
	// REROLL_FREE_ATTEMPTS is the attempts in a re-roll chain that are
	// credited.
	REROLL_FREE_ATTEMPTS = IntParam{"REROLL_FREE_ATTEMPTS"}
	// REWRITE_OVERLAP is the share of a turn's changed lines the previous
	// turn also changed that makes it a rewrite repeat (fit).
	REWRITE_OVERLAP = RatParam{"REWRITE_OVERLAP"}
	// REWRITE_FREE_REPEATS is the rewrite repeats that are credited.
	REWRITE_FREE_REPEATS = IntParam{"REWRITE_FREE_REPEATS"}
	// SAME_ERROR_LIMIT is the occurrences of one error signature after
	// which error pastes and go-aheads stop counting.
	SAME_ERROR_LIMIT = IntParam{"SAME_ERROR_LIMIT"}
	// NOREAD_MIN_WORDS is the shortest answer, in words, the unread test
	// applies to.
	NOREAD_MIN_WORDS = IntParam{"NOREAD_MIN_WORDS"}
	// NOREAD_FRAC is the share of the reading time at NOREAD_WPM under
	// which a reply counts as unread.
	NOREAD_FRAC = RatParam{"NOREAD_FRAC"}
	// NOREAD_WPM is the reading speed the unread test assumes.
	NOREAD_WPM = IntParam{"NOREAD_WPM"}
	// AUTOPILOT_RUN is the consecutive unread go-aheads before discounting
	// starts.
	AUTOPILOT_RUN = IntParam{"AUTOPILOT_RUN"}
	// NO_PROGRESS_LIMIT_S is the credited time without a progress event
	// before the half rate starts (fit).
	NO_PROGRESS_LIMIT_S = IntParam{"NO_PROGRESS_LIMIT_S"}
	// NO_PROGRESS_WEIGHT is the weight of credit past NO_PROGRESS_LIMIT_S.
	NO_PROGRESS_WEIGHT = RatParam{"NO_PROGRESS_WEIGHT"}
	// PROGRESS_RESTORE_S is how soon a progress event must come to restore
	// half-weighted seconds.
	PROGRESS_RESTORE_S = IntParam{"PROGRESS_RESTORE_S"}
	// ERROR_RESOLVED_RUNS is the runs without an error's signature after
	// which the error counts as resolved.
	ERROR_RESOLVED_RUNS = IntParam{"ERROR_RESOLVED_RUNS"}
)

// SM 8.6, rollups, submissions, lookback and witnessing.
var (
	// SESSION_GAP_S is the gap between credited minutes that starts a new
	// work session.
	SESSION_GAP_S = IntParam{"SESSION_GAP_S"}
	// DAY_CAP_S caps credited time per day.
	DAY_CAP_S = IntParam{"DAY_CAP_S"}
	// DAY_FLAG_S is the credited time per day above which a review flag is
	// raised.
	DAY_FLAG_S = IntParam{"DAY_FLAG_S"}
	// UNCOMMITTED_CAP_S caps credited time after the last commit in a
	// submission window.
	UNCOMMITTED_CAP_S = IntParam{"UNCOMMITTED_CAP_S"}
	// UNKNOWN_BUCKET_MAX_S caps the reading per turn when presence is
	// unknown (lookback).
	UNKNOWN_BUCKET_MAX_S = IntParam{"UNKNOWN_BUCKET_MAX_S"}
	// UNKNOWN_PA_MAX_S caps the permission wait in lookback.
	UNKNOWN_PA_MAX_S = IntParam{"UNKNOWN_PA_MAX_S"}
	// WITNESS_LAG_S is how soon the server must see a minute's batch for
	// the minute to be witnessed rather than backfilled.
	WITNESS_LAG_S = IntParam{"WITNESS_LAG_S"}
)

// DESIGN 6.3, parameters the design adds.
var (
	// PENDING_WINDOW_S is how long after its minute a bucket stays
	// pending (DESIGN 6.2 D1).
	PENDING_WINDOW_S = IntParam{"PENDING_WINDOW_S"}
	// BATCH_S is the interval between ledger batches. It replaces SM's
	// CHAIN_HEAD_INTERVAL_S (DESIGN 6.2 D8).
	BATCH_S = IntParam{"BATCH_S"}
	// CHECKPOINT_S is the interval between scorer checkpoints (DESIGN
	// 6.5).
	CHECKPOINT_S = IntParam{"CHECKPOINT_S"}
	// CONTEXT_IDLE_S is how long without input or a running turn before
	// presence sampling slows (DESIGN 6.2 D14).
	CONTEXT_IDLE_S = IntParam{"CONTEXT_IDLE_S"}
	// SLOW_SAMPLE_S is the sample interval after CONTEXT_IDLE_S (DESIGN
	// 6.2 D14).
	SLOW_SAMPLE_S = IntParam{"SLOW_SAMPLE_S"}
	// TRACK_INPUT_PRE_S is how long before a fallback track call the
	// host window must have seen OS input for the call to open an anchor
	// (DESIGN 5.4, condition 2).
	TRACK_INPUT_PRE_S = IntParam{"TRACK_INPUT_PRE_S"}
	// TRACK_MIN_GAP_S is how long after a track anchor no other track call
	// from the same session can open one (DESIGN 5.4, condition 3).
	TRACK_MIN_GAP_S = IntParam{"TRACK_MIN_GAP_S"}
	// TRACK_LOG_MATCH_S is the window around a track call from a session
	// seen through hooks or a log reader: with no hook or log prompt in
	// it, the call raises track_without_prompt (DESIGN 5.4).
	TRACK_LOG_MATCH_S = IntParam{"TRACK_LOG_MATCH_S"}
	// TRACK_DEDUPE_S is the window in which a track call matches a hook or
	// log prompt from the same session, becoming track_matched and opening
	// nothing (DESIGN 5.4).
	TRACK_DEDUPE_S = IntParam{"TRACK_DEDUPE_S"}
	// OFFLINE_CAP_S caps offline time per submission (DESIGN 6.2 D6).
	OFFLINE_CAP_S = IntParam{"OFFLINE_CAP_S"}
	// OFFLINE_PUSH_S is how soon after reconnecting a commit must reach the
	// forge for offline time to count (DESIGN 6.2 D6).
	OFFLINE_PUSH_S = IntParam{"OFFLINE_PUSH_S"}
	// BUILD_DAY_S is the credited time at level A or B a build day needs.
	// It renames SM's STREAK_DAY_S (DESIGN 6.2 D10).
	BUILD_DAY_S = IntParam{"BUILD_DAY_S"}
	// BUILD_DAY_LAG_S is how soon the server must receive a build day's
	// credit (DESIGN 6.2 D10).
	BUILD_DAY_LAG_S = IntParam{"BUILD_DAY_LAG_S"}
	// WEEKLY_GOAL_DEFAULT is the default weekly goal, in build days
	// (DESIGN 6.2 D10).
	WEEKLY_GOAL_DEFAULT = IntParam{"WEEKLY_GOAL_DEFAULT"}
	// FREEZE_EARN_DAYS is the build days that earn one freeze (DESIGN 6.2
	// D10).
	FREEZE_EARN_DAYS = IntParam{"FREEZE_EARN_DAYS"}
	// FREEZE_MAX is the most freezes held at once (DESIGN 6.2 D10).
	FREEZE_MAX = IntParam{"FREEZE_MAX"}
	// BOARD_WINDOW_D is the window club boards rank build days over
	// (DESIGN 6.2 D10).
	BOARD_WINDOW_D = IntParam{"BOARD_WINDOW_D"}
	// CONSISTENCY_WINDOW_D is the window consistency counts build days in
	// (DESIGN 6.2 D10).
	CONSISTENCY_WINDOW_D = IntParam{"CONSISTENCY_WINDOW_D"}
	// LONG_FIGHT_MIN_S is the credited time an error must stay alive
	// before its resolution is a long_fight moment (DESIGN 3.8).
	LONG_FIGHT_MIN_S = IntParam{"LONG_FIGHT_MIN_S"}
	// REVEAL_POST_S is how far past a submitted window re-scoring runs
	// (DESIGN 6.5).
	REVEAL_POST_S = IntParam{"REVEAL_POST_S"}
	// CHALLENGE_K_MIN is the fewest turns the challenge draws, when the
	// pool has that many (DESIGN 6.3 CHALLENGE_K, 7.5).
	CHALLENGE_K_MIN = IntParam{"CHALLENGE_K_MIN"}
	// CHALLENGE_K_FRAC is the share of challengeable turns the challenge
	// draws, between CHALLENGE_K_MIN and CHALLENGE_K_MAX (DESIGN 6.3
	// CHALLENGE_K, 7.5).
	CHALLENGE_K_FRAC = RatParam{"CHALLENGE_K_FRAC"}
	// CHALLENGE_K_MAX caps the turns the challenge draws (DESIGN 6.3
	// CHALLENGE_K, 7.5).
	CHALLENGE_K_MAX = IntParam{"CHALLENGE_K_MAX"}
	// PREAPPROVE_ROW_MAX_S is the largest captured_s of a row a program
	// preset may pre-approve (DESIGN 6.4).
	PREAPPROVE_ROW_MAX_S = IntParam{"PREAPPROVE_ROW_MAX_S"}
	// COMPOSE_NEEDS_KEYS, when true, credits the typing part of a lead
	// only where input_keys exists, on every machine (DESIGN 6.7).
	COMPOSE_NEEDS_KEYS = BoolParam{"COMPOSE_NEEDS_KEYS"}
	// AUDIT_BASE_P is the floor of a submission's audit probability
	// before risk is added (DESIGN 7.9).
	AUDIT_BASE_P = RatParam{"AUDIT_BASE_P"}
)

// all lists every parameter, in the order of SM 8 and DESIGN 6.3. A table
// must give each of them a value of its type and may hold nothing else.
// TestDeclaredParams checks that every variable above is here exactly once.
//
// While every table has the same parameters, one list serves them all. The
// first MAJOR version that adds or drops a parameter makes it a list per
// MAJOR version.
var all = []param{
	SAMPLE_S, SLEEP_GAP_S, PRESENCE_GAP_S, PRESENCE_TAIL_S, RETURN_GAP_MAX_S, KEYS_PRE_S,
	TYPING_MAX_CPS, SUBSTANTIVE_CHARS, ERROR_PASTE_TEXT_MAX, CRON_PREFIX_CHARS, PA_MIN_LATENCY_MS,

	THINK_LEAD_S, COMPOSE_CPS, COMPOSE_MAX_S, LOW_LEAD_S, ANSWER_LEAD_S, PA_MAX_S, PA_TRAIL_S,
	INT_LEAD_S, INT_TRAIL_S, EDIT_LEAD_S, CMD_LEAD_S, CMD_TRAIL_S, CMD_WATCH_MAX_S, COMMIT_LEAD_S,

	R_TURN_BASE_S, READ_WPM, DIFF_LPM, TOOL_LINES_MAX, BUCKET_MAX_S, BUCKET_TTL_S, READ_HORIZON_S,
	LOOKUP_BRACKET_S, LOOKUP_MAX_S,

	ATTRIB_STICKY_S, PROC_SCAN_S,

	MINHASH_PERMS, SHINGLE_WORDS, SHINGLE_CHARS, SHINGLE_MIN_WORDS, SIM_JACCARD, REROLL_WINDOW_S,
	REROLL_FREE_ATTEMPTS, REWRITE_OVERLAP, REWRITE_FREE_REPEATS, SAME_ERROR_LIMIT, NOREAD_MIN_WORDS,
	NOREAD_FRAC, NOREAD_WPM, AUTOPILOT_RUN, NO_PROGRESS_LIMIT_S, NO_PROGRESS_WEIGHT,
	PROGRESS_RESTORE_S, ERROR_RESOLVED_RUNS,

	SESSION_GAP_S, DAY_CAP_S, DAY_FLAG_S, UNCOMMITTED_CAP_S, UNKNOWN_BUCKET_MAX_S, UNKNOWN_PA_MAX_S,
	WITNESS_LAG_S,

	PENDING_WINDOW_S, BATCH_S, CHECKPOINT_S, CONTEXT_IDLE_S, SLOW_SAMPLE_S, TRACK_INPUT_PRE_S,
	TRACK_MIN_GAP_S, TRACK_LOG_MATCH_S, TRACK_DEDUPE_S, OFFLINE_CAP_S, OFFLINE_PUSH_S, BUILD_DAY_S,
	BUILD_DAY_LAG_S, WEEKLY_GOAL_DEFAULT, FREEZE_EARN_DAYS, FREEZE_MAX, BOARD_WINDOW_D,
	CONSISTENCY_WINDOW_D, LONG_FIGHT_MIN_S, REVEAL_POST_S, CHALLENGE_K_MIN, CHALLENGE_K_FRAC,
	CHALLENGE_K_MAX, PREAPPROVE_ROW_MAX_S, COMPOSE_NEEDS_KEYS, AUDIT_BASE_P,
}

// kind is the type of a parameter's value.
type kind uint8

const (
	kindInt kind = iota + 1
	kindRat
	kindBool
)

func (k kind) String() string {
	switch k {
	case kindInt:
		return "integer"
	case kindRat:
		return "rational"
	case kindBool:
		return "flag"
	}
	return "unknown"
}

// param is a parameter name of any of the three types. String returns the
// name.
type param interface {
	String() string
	kind() kind
}

// String returns the parameter's name, such as "THINK_LEAD_S".
func (p IntParam) String() string { return p.intName }

// String returns the parameter's name, such as "COMPOSE_CPS".
func (p RatParam) String() string { return p.ratName }

// String returns the flag's name, such as "COMPOSE_NEEDS_KEYS".
func (p BoolParam) String() string { return p.flagName }

func (IntParam) kind() kind  { return kindInt }
func (RatParam) kind() kind  { return kindRat }
func (BoolParam) kind() kind { return kindBool }
