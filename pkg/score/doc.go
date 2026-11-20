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
// The reading bucket, focus attribution and per-second credit (WP-SC2),
// one timeline across projects (WP-SC3), minute buckets (WP-SC4),
// checkpoints (WP-SC5) and dark flow (WP-SC6) build on these.
//
// # Final and pending
//
// Records can arrive late, up to PENDING_WINDOW_S (DESIGN 6.2 D1), and some
// rules look ahead: the return rule up to RETURN_GAP_MAX_S plus
// READ_HORIZON_S. So what Step derives for a time stays pending until the
// latest record is that much later, then becomes final and is output once.
// Until WP-SC5 adds checkpoints, a State holds every record it was given.
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
//   - Level B is any anchor from a hook, log, shell, git or editor whose
//     evidence shows input but no key press, whether or not the OS reports
//     key presses (DESIGN 6.4 says "keyboard-specific input unavailable").
//   - A message from another session (origin_declared cross_session) is
//     origin subagent: SM 2.2 has no rule for it, and like a subagent's it
//     is one agent prompting another (TP DI 26).
//   - A `pairtime track` call 10 to 30 s from a prompt of its session is
//     track_matched and raises no flag, as
//     docs/errata/2026-11-19-track-match-windows.md proposes.
//   - The editor in focus on a session's project hosts a session the
//     WakaTime bridge reports, as it does an IDE session, since the bridge
//     reads it from the editor's wakatime-cli (DESIGN 5.7).
package score
