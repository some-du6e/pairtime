// SPDX-License-Identifier: AGPL-3.0-or-later

// Package install plans and makes the changes `pairtime install` makes on
// a teen's machine, and undoes them for `pairtime uninstall` (DESIGN 5.9;
// docs/PLAN.md, WP-IN1).
//
// Everything pairtime writes outside its own directories belongs to
// another program: a harness's settings, the desktop's login items, the
// registry's Run key. A broken edit there can break the teen's harness or
// their Hackatime, so the package works in three steps that never mix:
//
//  1. Plan reads every target and computes, without writing anything, the
//     bytes each one will hold. The plan prints as one unified diff per
//     target, which is what `--dry-run` and the consent screen show.
//  2. Apply writes exactly the planned bytes, and only if no target changed
//     since it was read. Before it touches a target it saves what the
//     target held and records, in a manifest in pairtime's data directory,
//     what pairtime wrote.
//  3. Uninstall plans the reverse from the manifest. A target that still
//     holds what pairtime wrote gets its old bytes back, byte for byte, and
//     a file pairtime created is removed with the directories it made for
//     it. A target that changed since, because git-ai rewrote its hooks or
//     the teen edited the file, loses only pairtime's entries.
//
// Edits merge: a hook entry is added beside the ones already there and
// never replaces them (DESIGN 5.1; HS 13.3), and every byte pairtime didn't
// write, comments and line endings included, stays as it was.
//
// The hooks pairtime installs in a harness that has hooks of its own pass
// that harness's own event name: Claude Code's run `pairtime hook
// --harness claude_code --event UserPromptSubmit`, and the same for each
// of DESIGN 5.1's ten events. DESIGN 5.3's generic names, prompt, stop,
// tool, permission, session_start and session_end, belong to the generic
// hook mode, which passes --map. They have no name for StopFailure,
// SubagentStart, SubagentStop or Notification, and Claude Code's payload
// carries hook_event_name anyway. So `pairtime hook` (WP-AG3) takes
// --event as the harness's name for the event unless --map is given.
package install
