// SPDX-License-Identifier: AGPL-3.0-or-later

package claudecode

import (
	"path"
	"strings"

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

// tool maps PostToolUse: one agent.tool, which ends as the hook fires and
// started duration_ms before.
func (c *call) tool() {
	name, _ := c.f.str("tool_name")
	var in toolInput
	var resp toolResponse
	c.f.object("tool_input", &in)
	c.f.object("tool_response", &resp)
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
		r = c.edit(t, in, resp)
	case "Write":
		t.ToolClass = event.ToolWrite
		r = c.edit(t, in, resp)
	case "Bash":
		r = c.bash(t, in, resp)
	}
	rec := c.add(t, nil)
	if r != nil {
		rec.Edits, rec.Command = r.edits, r.command
	}
	if id, _ := c.f.str("tool_use_id"); id != "" {
		agent, _ := c.f.str("agent_id")
		c.s.post(post{t: c.env.T, toolUseID: id, tool: name, agent: agent})
	}
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
func (c *call) edit(t *event.AgentTool, in toolInput, resp toolResponse) *recordContent {
	file := resp.FilePath
	if file == "" {
		file = in.FilePath
	}
	lp, ok := c.ledgerPath(file)
	if !ok {
		if file != "" {
			c.perr.Problem("the edited file's path isn't one the ledger can key")
		}
		lp = ""
	}
	if lp != "" && c.a.opts.Features != nil {
		if v, err := c.a.opts.Features.Path(lp); err == nil {
			t.Files = []string{v.String()}
		}
	}
	var edits []features.Edit
	for _, h := range resp.StructuredPatch {
		es, err := features.EditsFromPatch(lp, h.OldStart, h.OldLines, h.NewStart, h.NewLines, h.Lines)
		if err != nil {
			c.perr.Problem("a structuredPatch hunk is malformed")
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
func (c *call) ledgerPath(file string) (string, bool) {
	p, ok := slashPath(file)
	if !ok {
		return "", false
	}
	if root, ok := slashPath(c.proj.Root); ok && c.proj.Root != "" {
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
// compare without case, as Windows does.
func under(root, p string) (string, bool) {
	if root == "/" || strings.HasSuffix(root, ":/") {
		return "", false
	}
	prefix := root + "/"
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
// so exit_code is left out.
func (c *call) bash(t *event.AgentTool, in toolInput, resp toolResponse) *recordContent {
	t.ToolClass = commandClass(in.Command)
	t.VisibleOutputLines = countLines(resp.Stdout) + countLines(resp.Stderr)
	if k := c.a.opts.Features; k != nil {
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
