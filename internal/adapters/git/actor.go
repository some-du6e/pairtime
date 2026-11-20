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
// operation for the operation to be the agent's (SM 1.2). git stamps its
// reflog in whole seconds, so the window is measured from the start of the
// operation's first second to the end of its last.
const ActorWindow = 2 * time.Second

// gitRun is one git command an agent's tool ran, as the adapter keeps it:
// when the tool event arrived, the folder git ran in, and the operations
// the command can produce. The command line itself is never kept.
type gitRun struct {
	t   time.Time
	dir string
	ops []event.GitOp
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
func parseGitRuns(t time.Time, cwd, line string) []gitRun {
	var runs []gitRun
	dir := cwd
	for _, simple := range splitCommands(line) {
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
				runs = append(runs, gitRun{t: t, dir: runDir, ops: ops})
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

// runMatches reports whether an agent's git run explains a change in the
// repo with common directory commonDir. runCommon is the common directory
// the run's folder resolved to.
func runMatches(r gitRun, runCommon, commonDir string, op event.GitOp, gitStart, gitEnd int64) bool {
	if runCommon == "" || foldPath(runCommon) != foldPath(commonDir) || !slices.Contains(r.ops, op) {
		return false
	}
	lo := time.Unix(gitStart, 0).Add(-ActorWindow)
	hi := time.Unix(gitEnd+1, 0).Add(ActorWindow)
	return !r.t.Before(lo) && r.t.Before(hi)
}
