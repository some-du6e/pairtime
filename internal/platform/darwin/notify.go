// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build darwin

package darwin

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os/exec"
	"strconv"
	"strings"
)

// ErrNoOsascript means there is no osascript to run, so pairtime can't show
// a notification on this Mac.
var ErrNoOsascript = errors.New("osascript: not found")

// osascriptPath is the AppleScript runner that macOS ships.
const osascriptPath = "/usr/bin/osascript"

// Osascript shows notifications by running osascript once per
// notification, never by polling AppleScript (CP 3.3). The zero value runs
// /usr/bin/osascript.
type Osascript struct {
	// Path is the osascript to run. "" means /usr/bin/osascript.
	Path string
}

// DisplayNotification shows a notification with AppleScript's display
// notification: title, then subtitle, then body, with no sound. The script
// reaches osascript on standard input, so none of the text is on a command
// line, and NotificationScript writes every string in it in ASCII, so the
// script's encoding can't change the text.
//
// macOS shows the notification as Script Editor's, the app that display
// notification belongs to, and the user's notification settings for Script
// Editor apply: its notifications may be off, or go only to Notification
// Center. DisplayNotification returns nil either way, because macOS doesn't
// tell osascript. Errors don't repeat what osascript printed, which can
// quote the script.
func (o Osascript) DisplayNotification(ctx context.Context, title, subtitle, body string) error {
	path := o.Path
	if path == "" {
		path = osascriptPath
	}
	cmd := exec.CommandContext(ctx, path)
	cmd.Stdin = strings.NewReader(NotificationScript(title, subtitle, body))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()
	switch {
	case err == nil:
		return nil
	case errors.Is(err, exec.ErrNotFound), errors.Is(err, fs.ErrNotExist):
		return fmt.Errorf("%w: %s", ErrNoOsascript, path)
	case ctx.Err() != nil:
		return ctx.Err()
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return fmt.Errorf("osascript exited with status %d%s", exit.ExitCode(), appleScriptErrorNumber(stderr.String()))
	}
	return fmt.Errorf("running osascript: %w", err)
}

// NotificationScript returns the AppleScript that DisplayNotification runs.
func NotificationScript(title, subtitle, body string) string {
	return "display notification (" + appleScriptText(body) +
		") with title (" + appleScriptText(title) +
		") subtitle (" + appleScriptText(subtitle) + ")\n"
}

// appleScriptText returns an AppleScript expression for the text s, written
// in printable ASCII only. Runs of printable ASCII become string literals,
// with backslash and double quote escaped, and every other character
// becomes (character id N), N being its Unicode code point. The parts are
// joined with &.
func appleScriptText(s string) string {
	if s == "" {
		return `""`
	}
	var parts []string
	var lit strings.Builder
	flush := func() {
		if lit.Len() > 0 {
			parts = append(parts, `"`+lit.String()+`"`)
			lit.Reset()
		}
	}
	for _, r := range s {
		if r < 0x20 || r > 0x7e {
			flush()
			parts = append(parts, "(character id "+strconv.Itoa(int(r))+")")
			continue
		}
		if r == '"' || r == '\\' {
			lit.WriteByte('\\')
		}
		lit.WriteRune(r)
	}
	flush()
	return strings.Join(parts, " & ")
}

// appleScriptErrorNumber returns ", AppleScript error N" for the number
// osascript puts in parentheses at the end of its message, as in
// "execution error: ... (-1743)", or "" when there is none. The rest of the
// message isn't kept.
func appleScriptErrorNumber(stderr string) string {
	s := strings.TrimSpace(stderr)
	open := strings.LastIndexByte(s, '(')
	if open < 0 || !strings.HasSuffix(s, ")") {
		return ""
	}
	num := strings.TrimPrefix(s[open+1:len(s)-1], "-")
	if num == "" || strings.Trim(num, "0123456789") != "" {
		return ""
	}
	return ", AppleScript error " + s[open+1:len(s)-1]
}
