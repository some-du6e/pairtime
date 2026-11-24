// SPDX-License-Identifier: AGPL-3.0-or-later

package claudecode

import (
	"path"
	"strconv"
	"strings"

	"github.com/some-du6e/pairtime/internal/adapters"
	"github.com/some-du6e/pairtime/pkg/event"
	"github.com/some-du6e/pairtime/pkg/ledger/features"
)

// toolInput holds the tool_input fields the adapter reads. Other fields
// are the tool's own and not read.
type toolInput struct {
	FilePath string `json:"file_path"`
	Content  string `json:"content"`
	Command  string `json:"command"`
}

// toolResponse holds the tool_response fields the adapter reads.
type toolResponse struct {
	// Edit and Write.
	Type            string  `json:"type"`
	FilePath        string  `json:"filePath"`
	Content         string  `json:"content"`
	StructuredPatch []patch `json:"structuredPatch"`
	// Bash.
	Stdout string `json:"stdout"`
	Stderr string `json:"stderr"`
}

// patch is one hunk of structuredPatch: a unified diff hunk with context.
type patch struct {
	OldStart int      `json:"oldStart"`
	OldLines int      `json:"oldLines"`
	NewStart int      `json:"newStart"`
	NewLines int      `json:"newLines"`
	Lines    []string `json:"lines"`
}

// tool maps PostToolUse, or PostToolUseFailure when failed: one agent.tool,
// which ends as the hook fires and started duration_ms before. A failed
// call is one that started and then failed, such as a command that exited
// non-zero. It has no tool_response, only its error's text, which is what
// Claude Code shows for it (the hooks reference;
// docs/errata/2026-11-20-claude-code-tool-failure.md). A failed edit
// changed nothing, so it names no file and counts no lines: DESIGN 5.7
// takes the person's own edits of a file the agent's edit names, within
// 120 s, for the agent's. An aborted call's text, with is_interrupt set,
// is Claude Code's own, not the command's, so it shows no output.
func (c *call) tool(failed bool) {
	name, _ := c.f.str("tool_name")
	var in toolInput
	var resp toolResponse
	c.f.object("tool_input", &in)
	if failed {
		resp.Stdout, _ = c.f.str("error")
	} else {
		c.f.object("tool_response", &resp)
	}
	t := &event.AgentTool{ToolClass: event.ToolOther, TStart: c.env.T, TEnd: c.env.T}
	if d, ok := c.f.integer("duration_ms"); ok {
		if d >= 0 && d <= c.env.T-minUnixMS {
			t.TStart = c.env.T - d
		} else {
			c.perr.Problem("duration_ms is out of range")
		}
	}
	var r *recordContent
	switch name {
	case "Edit", "MultiEdit", "NotebookEdit":
		t.ToolClass = event.ToolEdit
		if !failed {
			r = c.kit().edit(t, in, resp)
		}
	case "Write":
		t.ToolClass = event.ToolWrite
		if !failed {
			r = c.kit().edit(t, in, resp)
		}
	case "Bash":
		if failed {
			code, out, ok := exitLine(resp.Stdout)
			switch {
			case c.f.boolean("is_interrupt"):
				resp.Stdout = ""
			case ok:
				t.ExitCode, resp.Stdout = &code, out
			}
		}
		r = c.kit().bash(t, in, resp)
	}
	if agent, _ := c.f.str("agent_id"); agent != "" {
		// A subagent's tool output never reaches the conversation the
		// person reads, and SM 2.4 fills the reading bucket only with what
		// the harness shows (WP-AD1's step 10 ran its subagent in the
		// background). Its edits still count, through their lines.
		t.VisibleOutputLines = 0
	}
	rec := c.add(t, nil)
	if r != nil {
		rec.Edits, rec.Command = r.edits, r.command
	}
	id, _ := c.f.str("tool_use_id")
	if rec.Command != "" {
		// For the git watcher, which tells an agent's commit from the
		// person's by the command that made it (NoteAgentCommand).
		rec.Dir, rec.Call = c.cwd, id
	}
	if id != "" {
		agent, _ := c.f.str("agent_id")
		// native says the call is via hook, so a transcript's copy of it
		// would count it twice (logLine.toolResult).
		c.s.post(post{t: c.env.T, toolUseID: id, tool: name, agent: agent, native: c.raw.Arrival.Via == event.ViaHook})
	}
}

// toolKit is what turning a tool call into its agent.tool needs, whether
// a hook or a transcript reported the call: K_feat, the project root the
// call's paths are keyed relative to, and where problems go.
type toolKit struct {
	feat *features.Key
	root string
	perr *adapters.ParseError
}

func (c *call) kit() toolKit {
	return toolKit{feat: c.a.opts.Features, root: c.proj.Root, perr: c.perr}
}

// minUnixMS is pkg/event's earliest time, 2020-01-01.
const minUnixMS = 1_577_836_800_000

// recordContent is what a tool's record commits beside its event.
type recordContent struct {
	edits   []features.Edit
	command string
}

// edit fills an Edit or Write call's file and lines from its
// structuredPatch, or for a Write that created the file, which has none,
// from the content it wrote.
func (k toolKit) edit(t *event.AgentTool, in toolInput, resp toolResponse) *recordContent {
	file := resp.FilePath
	if file == "" {
		file = in.FilePath
	}
	lp, ok := ledgerPathUnder(k.root, file)
	if !ok {
		if file != "" {
			k.perr.Problem("the edited file's path isn't one the ledger can key")
		}
		lp = ""
	}
	if lp != "" && k.feat != nil {
		if v, err := k.feat.Path(lp); err == nil {
			t.Files = []string{v.String()}
		}
	}
	var edits []features.Edit
	for _, h := range resp.StructuredPatch {
		es, err := features.EditsFromPatch(lp, h.OldStart, h.OldLines, h.NewStart, h.NewLines, h.Lines)
		if err != nil {
			k.perr.Problem("a structuredPatch hunk is malformed")
			continue
		}
		edits = append(edits, es...)
	}
	if len(resp.StructuredPatch) == 0 && resp.Type == "create" {
		content := resp.Content
		if content == "" {
			content = in.Content
		}
		if e, ok := features.EditFromTexts(lp, "", content); ok {
			edits = append(edits, e)
		}
	}
	for _, e := range edits {
		t.LinesAdded += e.NewLines
		t.LinesRemoved += e.OldLines
	}
	if lp == "" {
		// The hunks still count lines, but with no path the ledger can't
		// commit them.
		edits = nil
	}
	return &recordContent{edits: edits}
}

// ledgerPath returns file in the ledger's form (ledger-v1 10.6): relative
// to the call's project root when it is inside it, else absolute, with '/'
// between components and a Windows drive letter upper case.
func (c *call) ledgerPath(file string) (string, bool) { return ledgerPathUnder(c.proj.Root, file) }

// ledgerPathUnder is ledgerPath for a call whose project root is projRoot,
// "" for none.
func ledgerPathUnder(projRoot, file string) (string, bool) {
	p, ok := slashPath(file)
	if !ok {
		return "", false
	}
	if root, ok := slashPath(projRoot); ok && projRoot != "" {
		if rel, ok := under(root, p); ok {
			p = rel
		}
	}
	if features.CheckPath(p) != nil {
		return "", false
	}
	return p, true
}

// slashPath returns an absolute path with '/' between its components and
// '.' and '..' resolved: a Windows path with its drive letter in upper
// case, or a POSIX one.
func slashPath(p string) (string, bool) {
	switch {
	case len(p) >= 3 && isLetter(p[0]) && p[1] == ':' && (p[2] == '\\' || p[2] == '/'):
		p = strings.ToUpper(p[:1]) + ":" + path.Clean("/"+strings.ReplaceAll(p[2:], `\`, "/"))
		return p, true
	case strings.HasPrefix(p, "/") && !strings.Contains(p, `\`):
		return path.Clean(p), true
	}
	return "", false
}

func isLetter(b byte) bool { return 'a' <= b && b <= 'z' || 'A' <= b && b <= 'Z' }

// under returns p relative to root when p is inside root. Windows paths
// compare without case, as Windows does. A root that already ends in '/',
// a root of / or a drive root such as C:/, is its own prefix, so every
// file on it is inside it (ledger-v1 10.6).
func under(root, p string) (string, bool) {
	prefix := root
	if !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	if len(p) <= len(prefix) {
		return "", false
	}
	if isLetter(p[0]) && p[1] == ':' {
		if !strings.EqualFold(p[:len(prefix)], prefix) {
			return "", false
		}
	} else if p[:len(prefix)] != prefix {
		return "", false
	}
	return p[len(prefix):], true
}

// bash fills a Bash call's class, output lines and error signature. The
// command line goes in the record's content, and in cmd_hash keyed as the
// ledger keys it. Claude Code 2.1.327's PostToolUse gives no exit status,
// and fires for a background command as it starts, so exit_code is left
// out; a command that exited non-zero gives PostToolUseFailure instead,
// whose error states its exit code (exitLine).
func (kit toolKit) bash(t *event.AgentTool, in toolInput, resp toolResponse) *recordContent {
	t.ToolClass = commandClass(in.Command)
	t.VisibleOutputLines = countLines(resp.Stdout) + countLines(resp.Stderr)
	if k := kit.feat; k != nil {
		if v, ok := k.Command(in.Command); ok {
			t.CmdHash = v.String()
		}
		out := resp.Stdout
		if resp.Stderr != "" {
			out += "\n" + resp.Stderr
		}
		if v, ok := k.ErrorSig(out); ok {
			t.ErrorSig = v.String()
		}
	}
	return &recordContent{command: in.Command}
}

// exitLine reads the line Claude Code puts first in the error of a command
// that ran and failed, "Exit code N", and returns N and the rest: what the
// command printed, stdout and stderr together. An error without that line,
// as when the shell itself didn't start, gives false.
func exitLine(msg string) (code int64, out string, ok bool) {
	first, rest, _ := strings.Cut(msg, "\n")
	v, ok := strings.CutPrefix(first, "Exit code ")
	if !ok {
		return 0, "", false
	}
	code, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return 0, "", false
	}
	return code, rest, true
}

// countLines counts text's lines: its pieces up to each LF, and a last
// piece without one.
func countLines(text string) int {
	if text == "" {
		return 0
	}
	n := strings.Count(text, "\n")
	if !strings.HasSuffix(text, "\n") {
		n++
	}
	return n
}
