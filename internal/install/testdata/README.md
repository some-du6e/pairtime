# Installer golden files

Every file here is synthetic: written for these tests, never copied from a real machine. The binary paths (`/home/teen/.local/bin/pairtime`, `C:\Users\teen\...`) and the other tools' commands are made up. `.gitattributes` turns off line-ending conversion, so the CRLF files reach a Windows checkout with their CRLF endings.

## claudecode/

One directory per case: `before.json` is `~/.claude/settings.json` before `pairtime install` (no file for `new-file`) and `after.json` is what install must leave there. Uninstall must give back `before.json` byte for byte, or remove the file when there was none.

The "before" shapes:

- `settings-lf`, `settings-crlf`: settings as Claude Code writes them (two-space indent), with git-ai's hooks as git-ai's `install-hooks` adds them, a `git-ai checkpoint claude --hook-input stdin` command under `PreToolUse` and `PostToolUse` with matcher `Write|Edit|MultiEdit` (HS 13.3; PA 1.3), and other tools' `Notification` and `Stop` hooks.
- `jsonc`: comments, trailing commas and tabs, as a person editing the file by hand writes it, with git-ai's hook under an absolute path.
- `compact`: one line with no spaces, as some tools write it.
- `four-spaces`, `tabs-bom`: other indents; `tabs-bom` starts with a UTF-8 byte order mark, as Windows editors write.
- `old-install`: hooks from an older pairtime install that the manifest doesn't know, at another path and one for an event pairtime no longer installs, beside another tool's `Stop` hook. Install replaces them. Uninstall takes out every pairtime hook, old and new, and gives back the rest of the file, `uninstalled.json`, because an old hook left behind would run a binary uninstall deletes.
- `new-file`, `empty-object`, `blank`, `comment-only`: no file, `{}`, a blank line, and a comment without a final newline.
- `windows-exe`: a Windows binary path, whose backslashes JSON escapes.

How the "after" files were made, so they don't depend on the code they test: for the cases whose `before.json` is what Python's `json.dumps(v, indent=n)` writes (`settings-*`, `four-spaces`, `tabs-bom`, `old-install`, `new-file`, `empty-object`, `windows-exe`), `after.json` is `json.dumps` of the merged object, with pairtime's groups after the existing ones and its new events after the existing events. A merge that keeps every other byte has to give exactly that. For `jsonc`, `compact`, `blank` and `comment-only`, `after.json` is `before.json` with pairtime's text inserted by hand at the end of each list and object.

## autostart/

The LaunchAgent property list for `/Users/teen/.local/bin/pairtime` and the XDG autostart entry for `/home/teen/.local/bin/pairtime`.
