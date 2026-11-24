# T3 Code capture

These scripts record what T3 Code's store and Claude Code's hooks show while a script drives T3 Code's web client. Their output becomes `testdata/fixtures/t3_code/<version>/linux/`, the fixture `TestCapture` in `internal/adapters/t3code` replays (docs/PLAN.md, WP-ADE1; DESIGN 5.1; docs/errata/2026-11-23-t3-reader.md, The capture). Nobody types into it: every message is a probe text from the plan below, typed by a script, and a guest's message is a stand-in the owner types with T3's guest wrapper in front.

The capture runs on a Linux machine with T3 Code installed, as root, so that it can make a throwaway account. It never touches the T3 Code or Claude Code that a person uses there.

## Rules

- **A throwaway account.** Make it for the capture and delete it after. Give it a uid that no account or group has and that owns none of the files you copy (check with `stat -c %u`).
- **Real copies.** `prepare.py` copies T3 Code's runtime folder and the Claude Code binary into the scratch home and checks that no copy shares an inode with its source. Never run T3 Code or Claude Code from a hard link to an installed copy: a hard link is the installed file, so making or removing it changes that file's change time, and a chown or chmod through it changes the installed file.
- **Nothing in the person's T3 or Claude homes.** Don't change, chown, run or install anything under the installed T3 Code's folders (`~/.t3`, or its `T3CODE_HOME`) or the person's Claude Code home, and don't read their T3 settings. The scratch T3 Code gets its own `T3CODE_HOME` and Claude Code its own `CLAUDE_CONFIG_DIR`.
- **Telemetry off.** `runas.py` sets `T3CODE_TELEMETRY_ENABLED=false`, T3 Code's own switch (its privacy policy names it), and `DISABLE_AUTOUPDATER=1`.
- **No secrets in output.** The model's credentials reach Claude Code only through `runas.py`'s environment, which never prints them. The pairing URL `t3 serve` prints holds a token: write it to a file only the capture reads, and don't print it.

## Steps

1. Make the account and the scratch home, then copy the runtimes:

   ```sh
   useradd -m -u <uid> -U -d /home/<account> -s /bin/bash <account>
   python3 -I prepare.py --home /home/<account> --uid <uid> \
     --t3-runtime <T3 Code's installed runtime folder> --claude <the claude binary>
   ```

   `prepare.py` writes `bin/` (the copies), `t3home/userdata/`, `claude-home/settings.json` (this kit's `logger.py` for every event of `tools/capture/prepare.py`'s `CLAUDE_CODE_HOOKS`), `cap/logger.py`, `cap/log/` and `repo/`, a two-file git repository. `cap/`, `cap/logger.py` and `settings.json` stay root's, `cap/` with mode 0755, so the account can't rename anything in `cap/` or edit the logger or the hook commands in place. `cap/log/`, where the logger writes `hooks.jsonl` and `snap.jsonl`, is the account's, as are `claude-home/`, `t3home/` and `repo/`, and so is the home itself, so the account can still move `cap/` or `settings.json` aside and put its own in their place, and so could a hostile agent T3 runs as the account. That is a stated limit, not a defense: the capture is a supervised, one-off run, and `mkfixture.py` (step 6) takes nothing from `hooks.jsonl` that the logger doesn't write: it stops at an event `prepare.py` doesn't register, a `t_ns` that isn't a whole number, a `session_id` or `prompt_id` that is neither text nor null, and a `guest_prefix` that is neither true, false nor null.

2. Start the scratch T3 Code on a loopback port, and point its Claude Code home at the scratch one (in T3 Code's settings, Claude Code's home is `/home/<account>/claude-home`):

   ```sh
   python3 -I runas.py --account <account> --uid <uid> -- \
     t3 serve --port <port> --host 127.0.0.1 --no-browser /home/<account>/repo > serve.log 2>&1 &
   ```

3. Open the pairing URL from `serve.log` in a browser driven by agent-browser (`agent-browser --session <name> open "$(cat pair.url)"`). Then send the probes, one at a time, by filling T3 Code's composer and pressing Enter, waiting for each turn's `Stop` in `cap/log/hooks.jsonl` where the plan says to.
4. For a steer timed around a turn's end, load `steer-helper.js` into the page: first its recorder, then one steer by hand so it has a template, then its helper. `steer.sh` then sends a short probe and the steer at a set delay after the turn's `Stop`.
5. Snapshot the store while T3 Code runs, as the account, with `sqlite3 -readonly /home/<account>/t3home/userdata/statev2.sqlite ".backup <copy>"`. Stop T3 Code.
6. Build the fixture and its probe list:

   ```sh
   python3 -I mkfixture.py --store <copy> --hooks /home/<account>/cap/log/hooks.jsonl \
     --home /home/<account> --out <fixture folder>/web
   python3 -I mkprobes.py --fixture <fixture folder>/web
   ```

   `mkfixture.py` keeps only the tables `internal/adapters/t3code/testdata/schema.sql` creates, replaces every UUID, `msg_` id and hex run with a numbered placeholder, the scratch home with `/home/capture`, and assistant, notification and title text with a placeholder that gives its length, and moves every time by the same whole number of days. It writes its map of old to new values next to the store copy, never into the fixture.
7. Run the CI-built `pairtime fixtures scrub -n` over the fixture folder, then search it for the machine's host name, `/root` and `/home` paths other than `/home/capture`, the account's name, T3 settings names and values, and install or telemetry ids. Write the results into the fixture's `PROVENANCE.md`.
8. Delete the account and its home (`userdel -r <account>`), and the scratch copies.

## The probe plan

`mkprobes.py`'s `PLAN` lists the 2026-11-23 capture's 28 prompts in order, each with its kind and the store row of its message. The kinds are typed messages, queued messages, steers into a running turn, steers sent as a turn ends or during its `Stop` hook, a steer that T3 Code ran as its own run, T3 Code's notification follow-up, the first message of a new worktree thread, and three guest stand-ins: messages that open with T3 Code's guest wrapper, `Message from guest Probe Guest, sent by the owner:` and a blank line, typed by the owner, since T3 Code makes a guest's link only through a T3 Connect tunnel. A new capture edits `PLAN` to match what it sent.

## The 2026-11-23 capture

The capture that made `testdata/fixtures/t3_code/0.1.10-nightly.20261112.4606/linux` ran these scripts' first versions, with three differences that these versions remove: it ran T3 Code and Claude Code from hard links to the installed copies, its first account's uid broke the rule above, and its T3 Code ran with telemetry on. #222's description lists what that changed on the machine: no owner, mode or content, but the installed runtime's change times.
