// SPDX-License-Identifier: AGPL-3.0-or-later

package git

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
)

// entry is one HEAD reflog entry: HEAD moved from Old to New at Time.
// Msg is git's reflog message, such as "commit: <subject>" or "checkout:
// moving from main to feature". It can hold a commit subject and branch
// names, so it is read only to classify the entry and is never kept.
type entry struct {
	Old, New string
	Time     int64 // Unix seconds, from git's own clock reading
	TZ       string
	Msg      string
}

// sum is a short digest of the entry's message, which a Cursor keeps to
// tell apart two entries with the same commit and second. It stays in
// state.json, on the device.
func (e entry) sum() string {
	h := sha256.Sum256([]byte(e.Msg))
	return hex.EncodeToString(h[:8])
}

// zeroID reports whether id is git's null object id, the Old of the first
// entry of a new ref.
func zeroID(id string) bool {
	return id != "" && strings.Trim(id, "0") == ""
}

// step is what an entry did, from its reflog message.
type step int

const (
	stepUnknown      step = iota // a message the adapter doesn't know
	stepNone                     // nothing to report, like a clone's first entry
	stepCommit                   // a new commit: commit, cherry-pick, am
	stepAmend                    // commit --amend
	stepMerge                    // merge or pull: a merge commit or a fast-forward
	stepRevert                   // revert
	stepRebaseStart              // rebase (start)
	stepRebaseStep               // a pick, reword, edit, squash, fixup or continue
	stepRebaseFinish             // rebase (finish)
	stepRebaseAbort              // rebase (abort)
	stepCheckout                 // checkout or switch
	stepReset                    // reset
)

// action is what classify makes of a reflog message.
type action struct {
	step step
	// fold is a squash or fixup step, which folds an original commit into
	// the one before it.
	fold bool
}

// classify reads a reflog message. The part before ": " names the command
// and, in parentheses, its step: "commit (amend)", "rebase (pick)", "pull
// --rebase (finish)". GIT_REFLOG_ACTION lets a tool replace the command's
// name, which is why a rebase is recognized by "rebase" anywhere in it.
// What follows ": " is a subject or branch names and is never looked at.
func classify(msg string) action {
	act, _, _ := strings.Cut(msg, ": ")
	act = strings.TrimSpace(act)
	base, paren := act, ""
	if i := strings.LastIndex(act, " ("); i >= 0 && strings.HasSuffix(act, ")") {
		base, paren = act[:i], act[i+2:len(act)-1]
	}
	fields := strings.Fields(base)
	if len(fields) == 0 {
		return action{step: stepUnknown}
	}
	switch {
	case base == "commit":
		switch paren {
		case "amend":
			return action{step: stepAmend}
		case "merge":
			return action{step: stepMerge}
		default: // "", "initial", "cherry-pick"
			return action{step: stepCommit}
		}
	case strings.Contains(base, "rebase"):
		switch {
		case paren == "start", base == "rebase" && strings.HasPrefix(msg, "rebase: checkout "):
			return action{step: stepRebaseStart}
		case paren == "finish", strings.HasPrefix(base, "rebase finished"):
			return action{step: stepRebaseFinish}
		case paren == "abort":
			return action{step: stepRebaseAbort}
		case paren == "squash", paren == "fixup":
			return action{step: stepRebaseStep, fold: true}
		default:
			return action{step: stepRebaseStep}
		}
	case fields[0] == "merge", fields[0] == "pull":
		return action{step: stepMerge}
	case base == "cherry-pick", base == "am":
		return action{step: stepCommit}
	case base == "revert":
		return action{step: stepRevert}
	case base == "checkout", base == "switch":
		return action{step: stepCheckout}
	case base == "reset":
		return action{step: stepReset}
	case base == "clone", base == "initial pull", fields[0] == "branch", fields[0] == "Branch":
		return action{step: stepNone}
	}
	return action{step: stepUnknown}
}

// reflogFormat prints, per entry, the commit HEAD moved to, the reflog
// selector with its timestamp (--date=raw makes it "HEAD@{<unix> <tz>}"),
// and the reflog message. Neither the reflog's name nor its email is
// asked for.
const reflogFormat = "%H%x00%gd%x00%gs%x1e"

// readReflog returns up to n of the newest HEAD reflog entries of the
// worktree in dir, newest first. Each entry's Old is the next older
// entry's New, which is what git wrote, since every HEAD update logs the
// value it replaced; the oldest entry read gets "" unless it is the first
// entry of the log. Reading through `git log -g` serves both ref formats,
// files and reftable.
func readReflog(ctx context.Context, g *Runner, dir string, n int) ([]entry, error) {
	out, err := g.Run(ctx, dir, "log", "-g", "--date=raw", "--format="+reflogFormat, "-n", strconv.Itoa(n+1), "HEAD", "--")
	if err != nil {
		if exitCode(err) == 128 {
			// No reflog yet, or HEAD names no commit: nothing has happened.
			return nil, nil
		}
		return nil, err
	}
	entries, err := parseReflog(string(out))
	if err != nil {
		return nil, err
	}
	for i := range entries {
		if i+1 < len(entries) {
			entries[i].Old = entries[i+1].New
		}
	}
	if len(entries) > n {
		entries = entries[:n]
	}
	return entries, nil
}

// parseReflog reads reflogFormat's output.
func parseReflog(out string) ([]entry, error) {
	var entries []entry
	for _, rec := range strings.Split(out, "\x1e") {
		rec = strings.TrimLeft(rec, "\r\n")
		if rec == "" {
			continue
		}
		parts := strings.SplitN(rec, "\x00", 3)
		if len(parts) != 3 || !isObjectID(parts[0]) {
			return nil, fmt.Errorf("unexpected reflog line")
		}
		t, tz, err := parseSelectorTime(parts[1])
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry{New: parts[0], Time: t, TZ: tz, Msg: parts[2]})
	}
	return entries, nil
}

// parseSelectorTime reads "HEAD@{1795134483 -0600}".
func parseSelectorTime(sel string) (int64, string, error) {
	open := strings.LastIndex(sel, "@{")
	if open < 0 || !strings.HasSuffix(sel, "}") {
		return 0, "", fmt.Errorf("unexpected reflog selector %q", sel)
	}
	inner := sel[open+2 : len(sel)-1]
	secs, tz, _ := strings.Cut(inner, " ")
	t, err := strconv.ParseInt(secs, 10, 64)
	if err != nil {
		return 0, "", fmt.Errorf("unexpected reflog selector %q", sel)
	}
	return t, tz, nil
}

// Cursor is where a Scanner stopped in a worktree's HEAD reflog: the last
// entry it handled, and HEAD at the time, for repos whose reflog is off.
// It is local state (state.json).
type Cursor struct {
	New  string `json:"new,omitempty"`
	Time int64  `json:"time,omitempty"`
	Sum  string `json:"sum,omitempty"`
	HEAD string `json:"head,omitempty"`
	// Set is false until the first scan, which handles nothing and only
	// sets the cursor: commits from before pairtime saw the repo aren't
	// observations.
	Set bool `json:"set"`
}

func (c Cursor) is(e entry) bool {
	return c.New == e.New && c.Time == e.Time && c.Sum == e.sum()
}

func cursorAt(e entry) Cursor {
	return Cursor{New: e.New, Time: e.Time, Sum: e.sum(), HEAD: e.New, Set: true}
}

// after returns the entries of a newest-first window that came after the
// cursor, oldest first, and whether the cursor was in the window.
func (c Cursor) after(window []entry) ([]entry, bool) {
	for i, e := range window {
		if c.is(e) {
			return reversed(window[:i]), true
		}
	}
	return nil, false
}

// since returns the entries newer than the cursor's second, oldest first:
// the fallback when the cursor's own entry is gone, because the reflog
// expired or was rewritten.
func (c Cursor) since(window []entry) []entry {
	var out []entry
	for _, e := range window {
		if e.Time > c.Time {
			out = append(out, e)
		}
	}
	return reversed(out)
}

func reversed(es []entry) []entry {
	out := make([]entry, len(es))
	for i, e := range es {
		out[len(es)-1-i] = e
	}
	return out
}
