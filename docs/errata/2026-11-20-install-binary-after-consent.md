# Erratum: `pairtime install` puts the binary in place after the yes

Date 2026-11-20. Raised by the WP-IN2 review fix-up (#106, issue #124). Status: decided 2026-11-20 by the orchestrator for Karim: `pairtime install` puts the binary in place after the consent screen's yes, as `cmd/pairtime` already does, so nothing lands on the computer before the teen agrees. DESIGN 5.9 step 1 and PLAN's WP-RL2 take the wording below in the next docs PR. Applied to DESIGN 5.9 on 2026-11-21 by #160. PLAN's WP-RL2 took its wording in #114.

## Input

- DESIGN 5.9 step 1: "Put the binary at the per-user path (CP 7) and verify the signed release manifest's SHA-256 (CP 4.6). No admin rights anywhere." Step 2 shows the consent screen, and step 3 writes autostart and starts the agent.
- PLAN's WP-RL2 Build: the install scripts "put the binary at the per-user path (`%LOCALAPPDATA%\pairtime\bin\pairtime.exe`, `~/.local/bin/pairtime`; CP 7) and run `pairtime install`".
- PLAN's WP-RL2 CI checks: "uninstall leaves nothing".

## Expected

Step 1 puts the binary in place before the consent screen.

## What install does

Nothing changes before the teen's yes, so `pairtime install` puts the binary in place in step 3, after the screen and before autostart. The screen says where it goes ("pairtime goes in ..."). install copies the binary beside its place, runs the hook self-test on the copy, and renames the copy into place only if it passes or no hooks will run it. On Windows the old `pairtime.exe` moves aside to `pairtime.old.exe` first (CP 4.6).

When the binary install runs from is the one at the per-user path already, as after an install script put it there, install copies nothing. A script can't leave the binary's folders for install to make, though, so install takes the hidden, repeatable flag `--made <dir>`. The script passes each folder it made for the binary: the binary's folder or one above it, such as `~/.local` and `~/.local/bin`, or `%LOCALAPPDATA%\pairtime` and its `bin`. install records them in `install/setup.json` beside the folders it made, and uninstall removes them once they're empty, so "uninstall leaves nothing" holds after a script install. When the data directory is among them, as on Windows, install gives it the DACL `home` gives the directories it makes, since `home.Dirs.Create` leaves a folder that's already there as it is. A `--made` that isn't an absolute path, or isn't the binary's folder or above it, is a usage error (exit 2).

Like everything install records, `--made` counts only in an install that gets past the consent screen. After a no (exit 3), or a refusal before anything changed, install has recorded nothing, since nothing changes before a yes. So the install command a script tells the teen to run next, after a non-zero exit or with `--no-install`, carries the same `--made` flags, which works whatever the exit status was. After a no, removing the binary and the folders it made, as the scripts do when the version check fails, works too.

## For Karim

DESIGN 5.9's steps 1 and 3 should say what install does. Suggested wording:

1. "Verify the signed release manifest's SHA-256 (CP 4.6). No admin rights anywhere."
3. "Put the binary at the per-user path (CP 7), write autostart and start the agent."

PLAN's WP-RL2 Build should say that the scripts run `pairtime install` with `--made <dir>` for each folder they made for the binary, and put the same flags in any install command they tell the teen to run next.
