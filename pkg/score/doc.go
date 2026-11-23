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
//   - turns, with SM 2.2's origin rules 1 to 8, rules 1 to 3 read from
//     every hook and log copy of a prompt, and prompt classes;
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
// (SM 2.6, 5.1). Minute buckets are WP-SC4's, checkpoints WP-SC5's, and dark
// flow and the no-progress weight WP-SC6's, so every second is at weight 1
// for now.
//
// # What WP-SC3 builds
//
// One timeline across projects (SM 3): every second goes to one project at
// most, so per-project credit adds up to the device's (SM 3.1). Each slot
// resolves to the project its seconds may go to (attrib.go, SM 3.2), only
// that project's windows and buckets credit them (SM 3.3 rule 1), and the
// slot's Credit says which project in ProjectKey. A terminal or an agent
// app goes by the project its focus record names, else by its hosted
// sessions. An editor goes by the latest editor heartbeat with input in the
// same or previous slot, and by the record only when there is none, since
// the client names an editor's project from the sessions it hosts and all
// of an editor's windows are one process on Windows and one app on macOS
// (D-editor-heartbeat-first). A browser, a chat app or any other app
// resolves to none, and only a lookup credits it (SM 2.5).
//
// For a window hosting sessions of several projects, SM 3.2 rule 1 is as
// E-6.3-two-tabs decides it. The slot goes to the session of the first
// anchor in the window strictly after the slot begins, if the slot starts
// inside that anchor's lead or the anchor comes within READ_HORIZON_S while
// its session's bucket isn't empty; else to the session with the most
// recent anchor less than ATTRIB_STICKY_S before the slot; else it is
// ambiguous. A second so resolved takes no better level than the resolving
// anchor and carries its side-door label (DESIGN 6.4); the anchor is among
// its AnchorIDs and in the slot's ResolvedBy. In an ambiguous slot the
// bucket doesn't drain, but anchors still count (D-ambiguous-anchors):
// every anchor window that covers a present second competes, whatever its
// project and whether or not its act has a session, in SM 3.3's order. The
// window that credits the slot's first credited second gives the slot its
// project, only that project's windows credit the rest of it, and what they
// don't credit is X.ambiguous. A terminal hosting no harness is ambiguous
// too, as SM 3.2 rule 1 has it until v1.5's shell hook reports a cwd, and
// the orchestrator extended D-ambiguous-anchors to it on 2026-11-20, so
// anchors count there as well. An agent app hosting no session keeps rule
// 3 and resolves to no project.
//
// Rule 2 prefers verified heartbeats (D-rule2-sidedoor). A side-door one,
// from a plugin, the bridge or a heartbeat that failed the peer-process
// check, decides an editor slot only when no verified one is in the same or
// previous slot, and then every second credited in the slot is level C and
// carries the side door's label, so a script posting heartbeats can't
// steer reading at level A or B into its project.
//
// Readings of SM 3.2:
//
//   - "Inside that anchor's lead" means the slot starts inside it, as the
//     6.3-tabs fixture's gen.py has it. A slot that starts before the lead
//     goes by the bucket or ATTRIB_STICKY_S.
//   - An anchor is in the window when its session is one the window hosts.
//     An act outside a harness session, such as a commit, resolves no slot
//     of such a window, though its windows compete in the window's
//     ambiguous slots.
//   - A window whose focus record names no project but whose hosted
//     sessions all have one is that project's, as one hosted session's is.
//   - Ties go to the earlier anchor for the look-ahead and to the later one
//     for ATTRIB_STICKY_S, then to the lower project key (SM 3.3 rule 6),
//     session key and event id. Two windows of an ambiguous slot tied
//     under SM 3.3 rule 3 go to the lower project key too.
//   - An agent's edit says nothing about which editor window is in front,
//     so rule 2's heartbeats are the others.
//   - A heartbeat is verified when it came by a path DESIGN 6.4 lets reach
//     level A or B: `editor`, a heartbeat that passed the peer-process
//     check, or `hook`, from Cursor's hooks. D-rule2-sidedoor names
//     `editor` verified and plugins, the bridge and `editor_unverified`
//     side doors, and no other path.
//   - SC1's typed-edit focus test takes the editor's project the same way,
//     at the edit: the latest heartbeat with input in the same or previous
//     slot, up to the edit itself, a verified one first, then the record.
//   - The return rule (SM 2.1) still needs a window that SM 3.2 resolves
//     to one project: a terminal, an editor or an agent app whose focus
//     record names it. So a long silence in a window hosting several ends
//     with its tail, and so does one in a browser, a chat app or another
//     app, whatever its record names. Its think part credits a slot only
//     when the slot resolves to the run's project, so that a slot keeps one
//     project (SM 5.1) when a heartbeat after the input that ends the run
//     gives an editor slot to another one, and no lookup shares a returned
//     slot with another project.
//
// # What WP-SC4 builds
//
// What the agent uploads and shows (SM 5, DESIGN 3.7). Minutes groups the
// credit slots by UTC minute, and Minute.Bucket summarizes a minute as SM
// 5.1's bucket. Revisions writes a new revision of a minute's bucket each
// time a Step changes it, pending until PENDING_WINDOW_S after the minute
// and final after. Rollups sums minutes into SM 5.3's work sessions, local
// days and projects, and Submission.Totals gives a submission's totals (SM
// 5.3, 5.4). Each timeline under testdata/timelines has a golden.jsonl with
// its buckets, and TestGolden holds the output to it (DESIGN 6.6).
//
// # What WP-ADE1 builds
//
// Relayed prompts, SM 2.2 rule 9 as D-relayed decides it
// (docs/errata/2026-11-22-relayed-prompts.md), with
// testdata/timelines/relayed as the oracle. An agent app such as T3 Code
// runs agents here for messages a person types on another device, and its
// reader writes a relay.message for each message the app says a person
// sent. Step reads them with the rest (relay.go):
//
//   - Pairing is the app's structure, never text: a record pairs with the
//     prompt.submit its link names, the record's prompt or the prompt's
//     relay, when the prompt came by a hook or a log reader, in the
//     record's session or with the record's session none, no earlier than
//     the stamp sent_at and at most RELAY_MATCH_S after it, and the record
//     at most RELAY_SEEN_LAG_S after the stamp. A record that pairs with
//     nothing opens nothing and raises no flag. Every relay.message and
//     what became of it is in Derived.Relays.
//   - SM 2.2's rules with an app: a record of declared cron makes its
//     prompt scheduled (rule 1), one of cross_session a subagent's (rule
//     3). Rule 4 lists parent_class agent_app, an agent app's own server,
//     and takes a prompt marked guest whatever its session. A record of
//     declared none in a session of parent_class agent_app witnesses its
//     prompt, which then skips rule 4 unless it is marked guest. Rules 6
//     and 7 read a witnessed prompt at the stamp, and skip it for a message
//     from the app's phone app; when they hold, the prompt is typed or
//     pasted at its usual level, anchored at its own t with its lead ending
//     at the stamp. Otherwise it is relayed (rule 9), with the length and
//     class of the app's own text.
//   - A relayed anchor is its relay.message, standing at the stamp: a lead
//     of typing time only, cut to the time since the thread's previous
//     relay.message of declared none, level C with via_relay. It is no
//     human anchor for anything but its own lead and its own session's
//     reading: it resolves no slot, qualifies no other reading, ends no
//     returned run and brackets no lookup.
//   - Relayed reading (SM 2.4): a relayed prompt resets its session's
//     bucket to its base at pickup, as any person's prompt does. In a
//     session an agent app's server runs, relayed reading draws on at most
//     UNKNOWN_BUCKET_MAX_S of the bucket from the session's first fill on,
//     whatever reset came last, while local reading there keeps
//     BUCKET_MAX_S (bucket.capped). Output fills it, an automated turn's
//     included. It drains, as READ and never SUP, from one of the session's
//     stops until the relayed anchor that vouches for the stop is sent or
//     the session's next turn starts. That anchor is the first of the
//     thread's relayed anchors sent after the stop among those that pair
//     within PENDING_WINDOW_S - 60 s of it, and it vouches only when it is
//     sent within READ_HORIZON_S after the stop. It gives the reading its
//     labels.
//   - Relayed credit comes after every other candidate, in a slot of any
//     presence but one the machine slept through, with no focus or
//     keyboard test (SM 2.3, 2.7). Between two relayed sessions SM 2.7's
//     rule decides: the next relayed lead that holds the second, then the
//     anchor sent soonest, then the latest relayed anchor, then the lower
//     project_key and session_key (relaycredit.go). Every second it
//     credits is level C with via_relay, and Submission.Totals counts them
//     in via_relay_s.
//   - A device that never samples while its agent runs, such as a server,
//     derives whole minutes (presence.live), from its first record's
//     minute or, when it is earlier, the minute of the earliest lead a
//     relay record can open, COMPOSE_MAX_S before its stamp, since the app
//     starts a message's run after the stamp. An uncredited second
//     there takes SM 5.2's codes for a live slot whose presence is
//     unknown: X.bucket_cap where only the 120 s cap kept a session from
//     reading, else X.agent_only while a turn a person started runs, a
//     relayed one included, or a no_input one, else X.automated while an
//     automated turn runs, else X.no_anchor, turns of every project
//     counting.
//
// Readings of D-relayed:
//
//   - The order of links is record order, by t and then seq, the order the
//     agent wrote them in. The first link claims both records it names,
//     whether or not it passes the checks, and a later link naming either
//     is void.
//   - DESIGN 5.2 lets a relay record witness only a hook or log prompt, and
//     no pairing reads a prompt by any other path: a plugin's prompt
//     carrying relay pairs with nothing.
//   - Only the record's own t is bounded, at most RELAY_SEEN_LAG_S after the
//     stamp, as SM 2.2 states it.
//   - Rule 6's focus test at the stamp takes the os.focus record at or
//     before it, since the stamp has no record position.
//   - A relayed prompt's class is SM 2.2's, over the app's text: answer
//     when the prompt answers a question or the session's last output asked
//     one, low_effort by the record's go-ahead test or under
//     SUBSTANTIVE_CHARS typed characters, else substantive. The record
//     carries no error signature, so it is never error_paste.
//   - A device counts as never sampling when it has no os.presence,
//     os.focus or os.power record and some record came by a path other
//     than a log reader's. Lookback's transcripts come only by a log reader,
//     and lookback's own range is WP-SC7's.
//   - The thread's next relayed anchor after a stop is the first one sent
//     after the stop of those that pair by the deadline, PENDING_WINDOW_S -
//     60 s after the stop. One that pairs later keeps its own lead but is
//     no next relayed anchor for that stop: it neither vouches for the
//     reading nor ends it. So a pairing that comes when the stop's minute
//     may already be final changes nothing about the reading after the
//     stop. SM 2.4 says so, and Replay in
//     testdata/timelines/relayed/relayed.py follows it.
//   - The rule between two relayed sessions reads "latest relayed message"
//     as the latest relayed anchor sent at or before the second, and
//     "next relayed anchor's lead" as the lead of the session's anchor sent
//     soonest after the second of those whose leads hold it.
//   - A slot holds one project (SM 5.1), and relayed candidates skip SM 3.3
//     rule 1's focus test. In a slot that resolves to a project they are
//     weighed a second at a time after this device's own candidates when
//     they are of that project, and a relayed candidate of another project
//     credits the slot only if nothing else credited any second of it,
//     once the slot is done; then the slot is that project's. In a slot
//     that resolves to none they are weighed at once, and in an ambiguous
//     one once the slot is done (attribution.relayNow).
//   - A lookup beats relayed credit, and takes the present seconds of its
//     run though relayed credit took them first, since a lookup's brackets
//     come after the run. What a relayed reading second drained from its
//     bucket stays drained.
//   - X.bucket_cap for relayed reading counts what UNKNOWN_BUCKET_MAX_S held
//     back while it and the bucket stay within BUCKET_MAX_S, one second for
//     each session reading in a second nothing credited, as Replay does.
//     For local reading it counts, as anywhere else, what BUCKET_MAX_S
//     evicted.
//   - A rollup counts each relayed anchor as a relayed message, apart from
//     the anchor counts by kind (SM 5.4).
//
// # Final and pending
//
// What Step derives for a time stays pending until now is PENDING_WINDOW_S
// later, then becomes final and is output once (DESIGN 3.7, 6.2 D1). No
// record changes what was derived for a time more than READ_HORIZON_S before
// its own t: a lead reaches back at most THINK_LEAD_S + COMPOSE_MAX_S,
// reading needs a human act within READ_HORIZON_S, and the return rule an
// act on record within READ_HORIZON_S of the silence's start. So a record
// that arrives up to PENDING_WINDOW_S - READ_HORIZON_S late changes only
// what is still pending. That bound and the return rule's horizon are the
// code half of E-return-lookahead
// (docs/errata/2026-11-19-return-rule-lookahead.md, decided 2026-11-20),
// which lets its code land before its text: SM 2.1, DESIGN 3.7 and D1 state
// them once P10 lands, and main's SM 2.1 still counts the return rule's
// horizon from the run's end. An anchor is decided at the latest t of its
// records; where that is later than its windows allow, as a long typed
// command's record is at its end, the windows it would open before then
// credit nothing, in every Step alike. A browser run with credit just before
// it may be a lookup, whose credit waits for the run's end, so nothing from
// such a run on is final until its end bracket is. SM 2.5 sets no bound on
// that (docs/errata/2026-11-19-return-rule-lookahead.md,
// docs/errata/2026-11-20-lookup-lookahead.md). A pairing of an agent app's
// message with its prompt reaches back from the message's stamp instead of
// from either record's t (SM 2.3): the prompt can come up to RELAY_MATCH_S
// after the stamp, and the lead it opens ends at the stamp. Step holds
// FinalBefore at the start of the longest lead an unpaired message could
// open until it pairs or RELAY_MATCH_S after its stamp passes, and a
// pairing changes nothing in a minute that is already final. The reading
// a relayed anchor vouches for needs no hold: the anchor must pair within
// PENDING_WINDOW_S - 60 s of the stop the reading starts at, while that
// stop's minute is still pending. Until WP-SC5
// adds checkpoints, a State holds every record it was given.
//
// # Readings of the spec
//
// Where SM and DESIGN leave a choice open, the scorer takes these, each
// stated again where it is implemented:
//
//   - A lead's typing part, and the reading part of RETURN_CREDIT, are
//     rounded to the nearest second, halves up, as SM 6.4 (161 s) and 6.5
//     (143 s) round theirs. Other lengths, such as a permission wait or a
//     command's run, keep their milliseconds. E-6.4-prompt-lengths decided
//     this reading, and SM 1.3 gains it with P11
//     (docs/errata/2026-11-19-sm-6.4-prompt-lengths.md).
//   - A silent run's length is its slots' length, from the end of the last
//     input slot to the start of the next, because samples show when the
//     last input before a silence came but not when the first one after it
//     did. A tail is timed from the last input, to the millisecond.
//   - A gap between samples longer than SLEEP_GAP_S is a hard break, except
//     one that follows CONTEXT_IDLE_S without input and lasts at most twice
//     SLOW_SAMPLE_S: the agent samples once a minute then, which DESIGN 6.2
//     D14 says changes no credit. D-d14-cadence decided that D14 states
//     this rule, and P07 lands that text.
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
//   - One window decides a `pairtime track` call from a session pairtime
//     sees through hooks or a log reader: within TRACK_DEDUPE_S of one of
//     the session's hook or log prompts, before or after, the call is
//     track_matched, and with none it raises track_without_prompt, where
//     DESIGN 5.4 still flags a call with no prompt within
//     TRACK_LOG_MATCH_S. The session counts as seen only from the call's
//     log_match or from a hook or log record dated at most TRACK_DEDUPE_S
//     after the call, so no later record changes what the call did. This
//     is the code half of E-track-windows
//     (docs/errata/2026-11-19-track-match-windows.md, decided 2026-11-20),
//     whose DESIGN 5.4 text lands with P10.
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
//     window edges are applied to whole seconds (SM 1.3). The same holds
//     for a turn's run, a tail and the reading horizon, as
//     E-6.4-prompt-lengths decided; SM 1.3 says so once P11 lands.
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
//     for silences are docs/errata/2026-11-20-hooked-turn-runs.md, which
//     gives the silence as 1,200 s: RETURN_GAP_MAX_S + READ_HORIZON_S, the
//     reach before E-return-lookahead, whose P10 updates it.
//   - A queued message resets the bucket when the agent picks it up, which
//     no record says, so at the session's first agent record after the
//     enqueue, the next point an agent takes up queued input, or at the
//     enqueue when none follows within READ_HORIZON_S.
//   - A lookup second needs presence, and a run of browser or chat-app
//     slots ends at a hard break. Its brackets are credited seconds of the
//     same project, other than lookups, ending at most LOOKUP_BRACKET_S
//     before the run or starting at most LOOKUP_BRACKET_S after it.
//   - An uncredited second takes the first code of SM 5.2's order as
//     E-6.6-codes decides it, which WP-SC3 applies. Codes for credit a rule
//     withheld come first: X.lookup_cap for a lookup's present seconds past
//     LOOKUP_MAX_S, and X.bucket_cap for a present, focused second within
//     READ_HORIZON_S of an act whose bucket is empty only because
//     BUCKET_MAX_S evicted output that hasn't expired. Then X.absent,
//     X.other_focus, and X.ambiguous, which includes a slot before the
//     client's first os.focus record. Then, while a turn of the slot's
//     project runs, X.agent_only if a person started one of the running
//     turns, else X.automated, else X.no_input. X.no_anchor comes last.
//     That settles docs/errata/2026-11-20-uncredited-code-placement.md.
//     Seconds before the presence sampler ran are X.absent until lookback
//     (WP-SC7), but on a device that never samples, where SM 5.2's codes
//     for a live slot whose presence is unknown apply (WP-ADE1 above).
package score
