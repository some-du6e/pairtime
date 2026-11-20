// SPDX-License-Identifier: AGPL-3.0-or-later

// Package score is pairtime's scorer: the deterministic state machine that
// turns events into credited human seconds (docs/DESIGN.md 3.7, 6). The
// struggle-metric spec, docs/design/struggle-metric.md (SM), is its
// rulebook, with the deviations DESIGN 6.2 lists.
//
// [Step] takes a [State] and new records and returns the next state and
// what it derived. It does no I/O and reads no clock, and it computes in
// integers: times in Unix milliseconds, lengths in milliseconds, rational
// parameters as exact fractions from pkg/params. The only floats it reads
// are PR1's idle-time fields, converted to milliseconds through their
// decimal text, and the only ones it writes are PR1's when a derived record
// is converted with an Event method. So the same records give the same
// output, byte for byte, on every platform.
//
// # What WP-SC1 builds
//
// This is the scorer's first part. Step derives:
//
//   - slots: 15-second presence slots with input_any and input_keys
//     rebuilt from idle samples, SM 2.1's present values and its hard
//     breaks (lock, suspend, a gap between samples over SLEEP_GAP_S);
//   - turns, with SM 2.2's origin rules 1 to 8 and prompt classes;
//   - anchors, SM 2.3's human acts with their think and typing parts,
//     trails, evidence levels and labels (DESIGN 6.4), including the side
//     doors of DESIGN 5.2 and 6.2 D2;
//   - the silent runs SM 2.1's return rule credits, with RETURN_CREDIT;
//   - what became of each `pairtime track` call (DESIGN 5.4, 6.2 D3);
//   - review flags.
//
// # What WP-SC2 builds
//
// On top of those, Step applies SM 2.7's rule to every second and sums the
// result into 15-second credit slots (Credit). For each second it takes:
//
//   - the anchor windows that cover it, realized where the person is
//     present and the slot is focused on the anchor's project, with a
//     lead's typing part only where key presses allow it (SM 2.3), and
//     the lead of the next anchor at or after the second winning (SM 3.3
//     rule 3);
//   - else the session's reading bucket (SM 2.4): filled by what the agent
//     showed, reset to R_TURN_BASE_S by each prompt a person sends, capped
//     at BUCKET_MAX_S, emptied after BUCKET_TTL_S, drained oldest first and
//     only within READ_HORIZON_S of a human act, as SUP while the agent
//     runs and READ after; plugin and bridge output fills a turn only up to
//     R_TURN_BASE_S when no hook or transcript record backs it (DESIGN
//     5.2), and what each turn filled is in Fill;
//   - else a bracketed browser or chat-app run (SM 2.5, LOOKUP);
//   - the return rule's reading and think parts in returned slots (SM 2.1,
//     with the ~r suffix);
//   - and for a second nothing credits, one uncredited reason (SM 5.2).
//
// Each credited second takes the level of the anchor behind it (DESIGN
// 6.4), and each slot counts its agent_running_s and agent-only seconds
// (SM 2.6, 5.1). A slot's project is the one its focus resolves to; one
// timeline across projects is WP-SC3's (SM 3), minute buckets WP-SC4's,
// checkpoints WP-SC5's, and dark flow and the no-progress weight WP-SC6's,
// so every second is at weight 1 for now.
//
// # Final and pending
//
// What Step derives for a time stays pending until now is PENDING_WINDOW_S
// later, then becomes final and is output once (DESIGN 3.7, 6.2 D1). No
// record changes what was derived for a time more than READ_HORIZON_S
// before its own t: a lead reaches back at most THINK_LEAD_S +
// COMPOSE_MAX_S, reading needs a human act within READ_HORIZON_S, and the
// return rule an act on record within READ_HORIZON_S of the silence's start
// (SM 2.1). So a record that arrives up to PENDING_WINDOW_S - READ_HORIZON_S
// late changes only what is still pending. An anchor is decided at the
// latest t of its records; where that is later than its windows allow, as
// a long typed command's record is at its end, the windows it would open
// before then credit nothing, in every Step alike. A browser run with credit
// just before it may be a lookup, whose credit waits for the run's end, so
// nothing from such a run on is final until its end bracket is. SM 2.5 sets
// no bound on that (docs/errata/2026-11-19-return-rule-lookahead.md,
// docs/errata/2026-11-20-lookup-lookahead.md). Until WP-SC5 adds
// checkpoints, a State holds every record it was given.
//
// # Readings of the spec
//
// Where SM and DESIGN leave a choice open, the scorer takes these, each
// stated again where it is implemented:
//
//   - Leads are whole seconds: a typing time is rounded to the nearest
//     second, as SM 6.4 (161 s) and 6.5 (143 s) round theirs.
//   - A silent run's length is its slots' length, from the end of the last
//     input slot to the start of the next, because samples show when the
//     last input before a silence came but not when the first one after it
//     did. A tail is timed from the last input, to the millisecond.
//   - A gap between samples longer than SLEEP_GAP_S is a hard break, except
//     one that follows CONTEXT_IDLE_S without input and lasts at most twice
//     SLOW_SAMPLE_S: the agent samples once a minute then, which DESIGN 6.2
//     D14 says changes no credit.
//   - "Input in its slot" is any input, and "input_keys where available"
//     means key presses where the OS reports them, exactly as each row of
//     SM 2.3 says.
//   - A typed build command is TEST, as SM 5.2 defines it ("a typed test
//     or build command") and as SM 2.3's activity column has it ("testing
//     for tests and builds"); SM 2.3's reason column says "CMD, or TEST
//     when cmd_class = test", which would give a build the reason CMD and
//     the activity debugging under SM 5.2's map.
//   - Level B is any anchor from a hook, log, shell, git or editor whose
//     evidence shows input but no key press, whether or not the OS reports
//     key presses (DESIGN 6.4 says "keyboard-specific input unavailable").
//   - A message from another session (origin_declared cross_session) is
//     origin subagent: SM 2.2 has no rule for it, and like a subagent's it
//     is one agent prompting another (TP DI 26).
//   - The editor in focus on a session's project hosts a session only the
//     WakaTime bridge reports, as it does an IDE session, since the bridge
//     reads it from the editor's wakatime-cli (DESIGN 5.7).
//   - unread_prose_words is SM 2.1's definition: the prose that arrived
//     after the person's last input in a slot whose focus held the
//     session. SM 6.1 cycle 6, where the answer comes 10 s before that
//     input, says 173 s, which the definition doesn't give
//     (docs/errata/2026-11-20-sm-6.1-return-unread-words.md).
//   - SM 2.2 rule 2 reads a prompt's declared origin. A turn a stop hook
//     continued has no prompt.submit and is self_continue as a
//     continuation; a later stop with stop_hook_active doesn't change a
//     typed prompt's origin, since it can come long after the prompt's
//     minutes are final.
//
// WP-SC2 adds these:
//
//   - A second belongs to a window when the second's start does, so
//     window edges are applied to whole seconds (SM 1.3).
//   - Presence after a silence longer than PRESENCE_GAP_S starts at the
//     second of the first input a sample taken inside the slot reports,
//     and at the slot's start when only the sample at its end reports one,
//     which is what SM 6.1's drink and 6.4's 23:37 row need together
//     (docs/errata/2026-11-19-presence-start-after-silence.md).
//   - A second is credited from the bucket when the bucket holds anything,
//     as SM 2.7's b > 0 says, and drains up to a second from it. A turn's
//     base counts toward the R_TURN_BASE_S that side-door output may fill
//     (testdata/timelines/sidedoor/README.md).
//   - A turn runs to its agent.stop, and no later than the session's next
//     prompt or session.end. Before its stop comes, a turn that hooks or a
//     log reader see runs up to now, since they will report the stop. Any
//     other turn runs to its last agent record, and one with no agent
//     record after its prompt doesn't run, as a track turn never does
//     (docs/errata/2026-11-19-turns-without-a-stop.md). A silence of a
//     turn's agent records longer than BUCKET_TTL_S ends a hooked turn's
//     run BUCKET_TTL_S after the last one, and one longer than
//     READ_HORIZON_S ends any other turn's at the last one, so no record
//     reaches further back than the scorer's look-ahead.
//     A continuation whose first record comes later than that after the
//     previous stop starts at the record. The rules for hooked turns and
//     for silences are docs/errata/2026-11-20-hooked-turn-runs.md.
//   - A queued message resets the bucket when the agent picks it up, which
//     no record says, so at the session's first agent record after the
//     enqueue, the next point an agent takes up queued input, or at the
//     enqueue when none follows within READ_HORIZON_S.
//   - A lookup second needs presence, and a run of browser or chat-app
//     slots ends at a hard break. Its brackets are credited seconds of the
//     same project, other than lookups, ending at most LOOKUP_BRACKET_S
//     before the run or starting at most LOOKUP_BRACKET_S after it.
//   - An uncredited second takes the first reason that applies in the order
//     docs/errata/2026-11-19-sm-6.6-uncredited-codes.md proposes, with
//     X.no_input on the seconds a no-input prompt's lead would have
//     covered, as it says, X.lookup_cap for a lookup's seconds past
//     LOOKUP_MAX_S, and X.bucket_cap for a present, focused second within
//     READ_HORIZON_S of an act whose bucket is empty only because
//     BUCKET_MAX_S evicted output that hasn't expired. The erratum's rule 4
//     names no code for the run of a no-input turn, which is X.agent_only,
//     SM 5.2's code for an agent turn that ran with nothing credited.
//     docs/errata/2026-11-20-uncredited-code-placement.md asks about these
//     placements. Seconds before the presence sampler ran are X.absent
//     until lookback (WP-SC7).
package score
