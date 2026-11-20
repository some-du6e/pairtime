// SPDX-License-Identifier: AGPL-3.0-or-later

package git

import (
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/some-du6e/pairtime/pkg/event"
)

// ActorWindow is how close an agent's git command must be to a git
// operation for the operation to be the agent's (SM 1.2): the operation
// has to fall within the shell call that ran the command, widened by
// ActorWindow on each side. git stamps its reflog in whole seconds, so an
// entry stamped s was written somewhere in [s, s+1).
const ActorWindow = 2 * time.Second

// AgentCommand is one shell command an agent's tool ran, as the agent.tool
// event of a bash call records it (SM 1.2).
type AgentCommand struct {
	// Call tells one tool call from another, like Claude Code's
	// tool_use_id, so a call reported when it starts and again when it
	// ends counts once. It is kept in memory only, while the call may
	// still explain a change.
	Call string
	// Start and End are the call's t_start and t_end. A call reported as
	// it starts has no End yet, and changes in its repo wait for it, up to
	// 10 minutes. Claude Code's PostToolUse hook gives only the end: no
	// hook payload carries a time (HS 3.1), so the hook's arrival is End,
	// and Start has to come from the transcript's tool_use entry, or from
	// End less the call's duration where a harness reports one, as
	// Cursor's postToolUse does (HS 4.1). With no Start, a git operation
	// matches only when it ended within ActorWindow of End, so in `git
	// commit && git push` the commit is the agent's only when the push
	// took under 2 s.
	Start, End time.Time
	// Dir is the folder the command ran in, the session's cwd.
	Dir string
	// Line is the command line. It is parsed in memory and never kept.
	Line string
}

// gitRun is one git command an agent's tool ran, as the adapter keeps it:
// the tool call's span, the folder git ran in, and the operations the
// command can produce. The command line itself is never kept.
type gitRun struct {
	call string
	// start is zero when only the call's end is known; end is zero while
	// the call runs.
	start, end time.Time
	dir        string
	ops        []event.GitOp
}

// subcommandOps says which git.change operations each git subcommand can
// write to the HEAD reflog.
var subcommandOps = map[string][]event.GitOp{
	"commit":      {event.GitCommit, event.GitAmend, event.GitMerge},
	"merge":       {event.GitMerge},
	"pull":        {event.GitMerge, event.GitRebase, event.GitCheckout},
	"rebase":      {event.GitRebase},
	"cherry-pick": {event.GitCommit},
	"am":          {event.GitCommit},
	"revert":      {event.GitRevert},
	"checkout":    {event.GitCheckout},
	"switch":      {event.GitCheckout},
	"reset":       {event.GitReset},
}

// parseGitRuns finds the git commands in a shell command line that an
// agent's tool ran in cwd: `git commit -m "..."`, `cd app && git rebase
// main`, `git -C ../lib reset --hard`, `GIT_EDITOR=true git rebase
// --continue`. It reads the line in memory, keeps only the folder and the
// subcommand of each, and leaves anything it can't follow, such as
// aliases, subshells and scripts, unmatched.
func parseGitRuns(c AgentCommand) []gitRun {
	var runs []gitRun
	dir := c.Dir
	for _, simple := range splitCommands(c.Line) {
		words := shellWords(simple)
		for len(words) > 0 && isAssignment(words[0]) {
			words = words[1:]
		}
		if len(words) == 0 {
			continue
		}
		switch name := words[0]; {
		case name == "cd" || name == "pushd":
			if len(words) > 1 && words[1] != "-" {
				dir = joinDir(dir, words[1])
			}
		case isGitExe(name):
			runDir := dir
			sub := ""
			for i := 1; i < len(words); i++ {
				w := words[i]
				switch {
				case w == "-C" && i+1 < len(words):
					runDir = joinDir(runDir, words[i+1])
					i++
				case w == "-c" && i+1 < len(words):
					i++
				case strings.HasPrefix(w, "-"):
					// --no-pager, --git-dir=..., --work-tree=... and the like
				default:
					sub = w
				}
				if sub != "" {
					break
				}
			}
			if ops := subcommandOps[sub]; ops != nil {
				runs = append(runs, gitRun{call: c.Call, start: c.Start, end: c.End, dir: runDir, ops: ops})
			}
		}
	}
	return runs
}

func isGitExe(name string) bool {
	base := strings.ToLower(filepath.Base(strings.ReplaceAll(name, `\`, "/")))
	return base == "git" || base == "git.exe"
}

func isAssignment(w string) bool {
	name, _, ok := strings.Cut(w, "=")
	return ok && name != "" && isEnvName(name)
}

func isEnvName(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c == '_' || 'A' <= c && c <= 'Z' || 'a' <= c && c <= 'z' || i > 0 && '0' <= c && c <= '9') {
			return false
		}
	}
	return s != ""
}

func joinDir(base, rel string) string {
	if rel == "" {
		return base
	}
	if strings.HasPrefix(rel, "~") {
		return "" // the agent's home; not followed
	}
	if filepath.IsAbs(rel) {
		return filepath.Clean(rel)
	}
	if base == "" {
		return ""
	}
	return filepath.Join(base, rel)
}

// splitCommands splits a command line at ;, &&, ||, | and newlines that
// aren't quoted.
func splitCommands(line string) []string {
	var out []string
	var cur strings.Builder
	quote := byte(0)
	flush := func() {
		if s := strings.TrimSpace(cur.String()); s != "" {
			out = append(out, s)
		}
		cur.Reset()
	}
	for i := 0; i < len(line); i++ {
		c := line[i]
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			} else if c == '\\' && quote == '"' && i+1 < len(line) {
				cur.WriteByte(c)
				i++
				c = line[i]
			}
			cur.WriteByte(c)
		case c == '\'' || c == '"':
			quote = c
			cur.WriteByte(c)
		case c == ';' || c == '\n' || c == '|' || c == '&':
			flush()
			if i+1 < len(line) && (c == '|' || c == '&') && line[i+1] == c {
				i++
			}
		case c == '(' || c == ')':
			flush() // a subshell's commands are read as if they ran here
		default:
			cur.WriteByte(c)
		}
	}
	flush()
	return out
}

// shellWords splits one simple command into words, removing quotes.
func shellWords(s string) []string {
	var words []string
	var cur strings.Builder
	quote := byte(0)
	inWord := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			} else if c == '\\' && quote == '"' && i+1 < len(s) {
				i++
				cur.WriteByte(s[i])
			} else {
				cur.WriteByte(c)
			}
		case c == '\'' || c == '"':
			quote = c
			inWord = true
		case c == ' ' || c == '\t':
			if inWord {
				words = append(words, cur.String())
				cur.Reset()
				inWord = false
			}
		case c == '\\' && i+1 < len(s):
			i++
			cur.WriteByte(s[i])
			inWord = true
		default:
			cur.WriteByte(c)
			inWord = true
		}
	}
	if inWord {
		words = append(words, cur.String())
	}
	return words
}

// runMatches reports whether an agent's finished git run explains a
// change in the repo with common directory commonDir whose reflog entries
// are stamped gitStart to gitEnd. runCommon is the common directory the
// run's folder resolved to. The operation has to fall within the call's
// span widened by ActorWindow on each side; when the call's start isn't
// known, its end has to be within ActorWindow of the operation's end.
func runMatches(r gitRun, runCommon, commonDir string, op event.GitOp, gitStart, gitEnd int64) bool {
	if r.end.IsZero() || runCommon == "" || foldPath(runCommon) != foldPath(commonDir) || !slices.Contains(r.ops, op) {
		return false
	}
	first, lo := gitStart, r.start.Add(-ActorWindow)
	if r.start.IsZero() || r.start.After(r.end) {
		first, lo = gitEnd, r.end.Add(-ActorWindow)
	}
	hi := r.end.Add(ActorWindow)
	return time.Unix(first+1, 0).After(lo) && !time.Unix(gitEnd, 0).After(hi)
}
