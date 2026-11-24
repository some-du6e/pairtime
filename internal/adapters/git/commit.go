// SPDX-License-Identifier: AGPL-3.0-or-later

package git

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/some-du6e/pairtime/pkg/event"
)

// Commit is a commit record (DESIGN 4.3, 5.8; SP 4.3). The exported JSON
// fields are the ones DESIGN 4.3 lets the client upload, apart from
// credited_seconds_since_parent, which the scorer adds, and path_hmacs,
// which wait for WP-AD2's file-key form, as does the file list that SM
// 4.3's edit_survives reads on the device (PLAN, WP-SC6). Everything in
// Local stays on the device and is never marshaled.
type Commit struct {
	SHA     string   `json:"sha"`
	Parents []string `json:"parents"`
	Project string   `json:"project"`
	// ObservedAt is when the adapter first saw the commit, in Unix
	// milliseconds of the device's clock, as every time in pkg/event is.
	ObservedAt int64 `json:"observed_at"`
	// AuthorTime and CommitterTime are the commit's own dates in Unix
	// seconds, git's unit and SP 4.3's "epoch s", not milliseconds. They
	// are whatever the commit says, which anyone can set (HS 13.3).
	AuthorTime    int64 `json:"author_time"`
	CommitterTime int64 `json:"committer_time"`
	// FilesChanged, Insertions and Deletions are `git diff --shortstat`
	// against the first parent, with rename detection, as a forge counts
	// them; against the empty tree for a root commit.
	FilesChanged int `json:"files_changed"`
	Insertions   int `json:"insertions"`
	Deletions    int `json:"deletions"`
	// Rewrites are the commits this one replaced, through an amend or a
	// rebase, so credited time can move to it (SP 4.4).
	Rewrites []Rewrite `json:"rewrites,omitempty"`
	// Actor is ActorAgent when an agent tool ran the git command that made
	// the commit within 2 s of it (SM 1.2), ActorUnknown when nothing
	// says who made it or the evidence disagrees, and ActorHuman otherwise.
	Actor event.Actor `json:"actor"`
	// AgentTrailer names the agent a trailer or a bot author credits, as
	// a key such as "claude" or "aider", never the trailer itself, which
	// can hold a person's name and email address.
	AgentTrailer string `json:"agent_trailer,omitempty"`

	Local CommitLocal `json:"-"`
}

// MarshalJSON writes c as the commit record DESIGN 4.3 lets the client
// upload: its exported fields in their order, with files_changed,
// insertions and deletions left out when the shortstat couldn't be read
// (Local.NoStat), as ingest-v1 2.3.3 says, so their 0s can't pass for a
// commit that changed nothing.
func (c Commit) MarshalJSON() ([]byte, error) {
	type record struct {
		SHA           string      `json:"sha"`
		Parents       []string    `json:"parents"`
		Project       string      `json:"project"`
		ObservedAt    int64       `json:"observed_at"`
		AuthorTime    int64       `json:"author_time"`
		CommitterTime int64       `json:"committer_time"`
		FilesChanged  *int        `json:"files_changed,omitempty"`
		Insertions    *int        `json:"insertions,omitempty"`
		Deletions     *int        `json:"deletions,omitempty"`
		Rewrites      []Rewrite   `json:"rewrites,omitempty"`
		Actor         event.Actor `json:"actor"`
		AgentTrailer  string      `json:"agent_trailer,omitempty"`
	}
	r := record{SHA: c.SHA, Parents: c.Parents, Project: c.Project, ObservedAt: c.ObservedAt,
		AuthorTime: c.AuthorTime, CommitterTime: c.CommitterTime, Rewrites: c.Rewrites,
		Actor: c.Actor, AgentTrailer: c.AgentTrailer}
	if !c.Local.NoStat {
		r.FilesChanged, r.Insertions, r.Deletions = &c.FilesChanged, &c.Insertions, &c.Deletions
	}
	return json.Marshal(r)
}

// Rewrite says that NewSHA replaced OldSHA.
type Rewrite struct {
	OldSHA string `json:"old_sha"`
	NewSHA string `json:"new_sha"`
}

// Origin says whether a commit was made on this device.
type Origin string

const (
	// MadeHere is a commit a git command on this device created: a
	// commit, amend, merge commit, cherry-pick, revert or rebase.
	MadeHere Origin = "here"
	// MadeElsewhere is a commit that reached HEAD through a pull or a
	// merge, made on another machine or by a cloud agent. It shows on the
	// project story as work pairtime couldn't see (DESIGN 5.1).
	MadeElsewhere Origin = "elsewhere"
)

// CommitLocal is what pairtime keeps about a commit only on the device.
type CommitLocal struct {
	Op     event.GitOp
	Origin Origin
	// AuthorTZ and CommitterTZ are the commits' UTC offsets, like
	// "-0600". A time zone says where someone is, so it stays here.
	AuthorTZ, CommitterTZ string
	// ReflogTime is when git wrote the reflog entry, in Unix seconds.
	ReflogTime int64
	// AI is what git-ai's note on the commit says (HS 13.3), or nil.
	AI *AINote
	// NoStat means the shortstat couldn't be read, so the counts are 0.
	NoStat bool
}

// AINote is the part of a git-ai authorship note (refs/notes/ai, git-ai
// standard v3.0.0) that pairtime keeps: which tools it names and how many
// lines it attests to agents and to people. The note's paths, its prompt
// ids and its authors' names and emails are read past and dropped.
type AINote struct {
	Schema string
	// Tools are the agent_id.tool values of its sessions and prompts, such
	// as "claude" or "cursor", sorted.
	Tools []string
	// AILines counts lines attested to an agent session (s_ keys and the
	// legacy prompt keys); HumanLines lines attested to a known human (h_
	// keys).
	AILines, HumanLines int
}

// meta is what the adapter reads about a commit, before it becomes a
// record. Author holds the author's name and email for matching a
// rewritten commit with its original; it is never kept.
type meta struct {
	SHA            string
	Parents        []string
	AuthorTime     int64
	AuthorTZ       string
	CommitterTime  int64
	CommitterTZ    string
	author         string
	subject        string
	agent          string
	ai             *AINote
	files, ins, dl int
	noStat         bool
}

// metaFormat prints a commit's id, parents, author and committer dates
// ("<unix> <tz>" with --date=raw), author name and email, subject, its
// trailers and its git-ai note. log.showSignature is off for every call
// (run.go), so nothing comes before the fields.
const metaFormat = "%H%x00%P%x00%ad%x00%cd%x00%an%x00%ae%x00%s%x00%(trailers:only,unfold)%x00%N%x1e"

// readMeta reads the commits in shas, in one git call per 500, with their
// git-ai notes when notes is set. Missing commits are left out of the
// result.
func readMeta(ctx context.Context, g *Runner, dir string, shas []string, notes bool) (map[string]*meta, error) {
	out := make(map[string]*meta, len(shas))
	if len(shas) == 0 {
		return out, nil
	}
	notesArg := "--notes=refs/notes/ai"
	if !notes {
		notesArg = "--no-notes" // %N then prints nothing
	}
	for start := 0; start < len(shas); start += 500 {
		batch := shas[start:min(start+500, len(shas))]
		args := append([]string{"log", "--no-walk=unsorted", "--date=raw", notesArg, "--format=" + metaFormat}, batch...)
		args = append(args, "--")
		text, err := g.Run(ctx, dir, args...)
		var ms []*meta
		if err == nil {
			ms, err = parseMeta(string(text))
		}
		if err != nil {
			if notes {
				// A note can hold any byte, the record separator too, and a
				// notes tree git can't read fails the call. The commits
				// matter more than their notes: read them again without.
				return readMeta(ctx, g, dir, shas, false)
			}
			return nil, err
		}
		for _, m := range ms {
			out[m.SHA] = m
		}
	}
	return out, nil
}

func parseMeta(text string) ([]*meta, error) {
	var ms []*meta
	for _, rec := range strings.Split(text, "\x1e") {
		rec = strings.TrimLeft(rec, "\r\n")
		if rec == "" {
			continue
		}
		f := strings.Split(rec, "\x00")
		if len(f) != 9 || !isObjectID(f[0]) {
			return nil, fmt.Errorf("unexpected commit record")
		}
		m := &meta{SHA: f[0], author: f[4] + "\x00" + strings.ToLower(f[5]), subject: f[6]}
		for _, p := range strings.Fields(f[1]) {
			if !isObjectID(p) {
				return nil, fmt.Errorf("unexpected parent id")
			}
			m.Parents = append(m.Parents, p)
		}
		var err error
		if m.AuthorTime, m.AuthorTZ, err = parseRawDate(f[2]); err != nil {
			return nil, err
		}
		if m.CommitterTime, m.CommitterTZ, err = parseRawDate(f[3]); err != nil {
			return nil, err
		}
		m.agent = agentOf(f[7], f[4], f[5])
		m.ai = parseAINote(f[8])
		ms = append(ms, m)
	}
	return ms, nil
}

// parseRawDate reads git's raw date, "1795134483 -0600".
func parseRawDate(s string) (int64, string, error) {
	secs, tz, _ := strings.Cut(strings.TrimSpace(s), " ")
	t, err := strconv.ParseInt(secs, 10, 64)
	if err != nil {
		return 0, "", fmt.Errorf("unexpected raw date %q", s)
	}
	return t, tz, nil
}

// trailerAgents maps the address in a co-author trailer to the agent it
// names. HS 13.3 says to match addresses rather than names, which people
// and models vary.
var trailerAgents = map[string]string{
	"noreply@anthropic.com": "claude", // Claude Code: "Co-Authored-By: Claude <model> <noreply@anthropic.com>"
	"aider@aider.chat":      "aider",  // Aider: "Co-authored-by: aider (<model>) <aider@aider.chat>"
}

// botAuthors maps the author name of a commit a cloud tool made itself.
var botAuthors = map[string]string{
	"gpt-engineer-app[bot]": "lovable", // Lovable (HS 13.3)
}

// agentOf returns the agent key that a commit's trailers or bot author
// name, or "". trailers is `%(trailers:only,unfold)`: one "Key: value" per
// line.
func agentOf(trailers, authorName, authorEmail string) string {
	var found []string
	for _, line := range strings.Split(trailers, "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key, value = strings.ToLower(strings.TrimSpace(key)), strings.TrimSpace(value)
		switch key {
		case "co-authored-by":
			if a := trailerAgents[strings.ToLower(addressOf(value))]; a != "" {
				found = append(found, a)
			}
		case "replit-commit-author": // "Agent" or "Deployment" (HS 13.3)
			found = append(found, "replit")
		}
	}
	if a := botAuthors[strings.TrimSpace(authorName)]; a != "" {
		found = append(found, a)
	} else if name, ok := strings.CutSuffix(strings.ToLower(authorEmail), "@users.noreply.github.com"); ok {
		// A bot account's noreply address ends its name part in "[bot]",
		// after an optional "<id>+".
		if _, login, ok := strings.Cut(name, "+"); ok {
			name = login
		}
		if a := botAuthors[name]; a != "" {
			found = append(found, a)
		}
	}
	if len(found) == 0 {
		return ""
	}
	slices.Sort(found)
	return found[0]
}

// addressOf returns the part of "Name <address>" between the brackets.
func addressOf(v string) string {
	open := strings.LastIndex(v, "<")
	end := strings.LastIndex(v, ">")
	if open < 0 || end < open {
		return ""
	}
	return strings.TrimSpace(v[open+1 : end])
}

// maxNote bounds the git-ai note the adapter parses. A note lists line
// ranges per file, so even a large commit's is a few kilobytes.
const maxNote = 1 << 20

// parseAINote reads a git-ai authorship note: attestation lines, a "---"
// line, then one JSON object (git-ai standard v3.0.0). It returns nil for
// no note, or for text that isn't a git-ai note.
func parseAINote(note string) *AINote {
	note = strings.TrimSpace(note)
	if note == "" || len(note) > maxNote {
		return nil
	}
	lines := strings.Split(strings.ReplaceAll(note, "\r\n", "\n"), "\n")
	div := slices.Index(lines, "---")
	if div < 0 {
		return nil
	}
	var doc struct {
		Schema   string `json:"schema_version"`
		Prompts  map[string]agentRecord
		Sessions map[string]agentRecord
	}
	if err := json.Unmarshal([]byte(strings.Join(lines[div+1:], "\n")), &doc); err != nil || !strings.HasPrefix(doc.Schema, "authorship/") {
		return nil
	}
	n := &AINote{Schema: doc.Schema}
	for _, recs := range []map[string]agentRecord{doc.Sessions, doc.Prompts} {
		for _, rec := range recs {
			if t := strings.ToLower(strings.TrimSpace(rec.AgentID.Tool)); t != "" && isLabel(t) && !slices.Contains(n.Tools, t) {
				n.Tools = append(n.Tools, t)
			}
		}
	}
	slices.Sort(n.Tools)
	for _, line := range lines[:div] {
		// An entry line is two spaces, a key, a space and ranges; a path
		// line starts in column 0 and is skipped.
		rest, ok := strings.CutPrefix(line, "  ")
		if !ok {
			continue
		}
		key, ranges, ok := strings.Cut(rest, " ")
		if !ok {
			continue
		}
		count := countRanges(ranges)
		if strings.HasPrefix(key, "h_") {
			n.HumanLines += count
		} else {
			n.AILines += count
		}
	}
	return n
}

type agentRecord struct {
	AgentID struct {
		Tool string `json:"tool"`
	} `json:"agent_id"`
}

// countRanges counts the lines in "1,4-6,9".
func countRanges(s string) int {
	total := 0
	for _, part := range strings.Split(strings.TrimSpace(s), ",") {
		lo, hi, isSpan := strings.Cut(part, "-")
		a, err := strconv.Atoi(lo)
		if err != nil || a < 1 {
			continue
		}
		b := a
		if isSpan {
			if b, err = strconv.Atoi(hi); err != nil || b < a {
				continue
			}
		}
		total += b - a + 1
	}
	return total
}

// isLabel reports whether s can be a label: a short run of letters,
// digits, '_', '-' or '.'.
func isLabel(s string) bool {
	if len(s) == 0 || len(s) > 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !('a' <= c && c <= 'z' || '0' <= c && c <= '9' || c == '_' || c == '-' || c == '.') {
			return false
		}
	}
	return true
}

// readStats fills in each commit's shortstat against its first parent, or
// against the empty tree for a root commit, in one `git diff-tree
// --stdin` call. git prints nothing for a commit that changes nothing.
// Only the counts are read: git computes them from the objects, and no
// diff, path or content reaches pairtime.
func readStats(ctx context.Context, g *Runner, dir string, ms []*meta) error {
	if len(ms) == 0 {
		return nil
	}
	var in strings.Builder
	for _, m := range ms {
		in.WriteString(m.SHA)
		if len(m.Parents) > 0 {
			in.WriteString(" ")
			in.WriteString(m.Parents[0])
		}
		in.WriteString("\n")
	}
	out, err := g.RunInput(ctx, dir, []byte(in.String()), "diff-tree", "--stdin", "-r", "-M", "--root", "--shortstat", "--no-ext-diff", "--no-textconv")
	if err != nil {
		for _, m := range ms {
			m.noStat = true
		}
		return err
	}
	stats := parseStats(string(out))
	for _, m := range ms {
		if s, ok := stats[m.SHA]; ok {
			m.files, m.ins, m.dl = s[0], s[1], s[2]
		}
	}
	return nil
}

// parseStats reads diff-tree's output: a commit id line, then its
// shortstat line, such as " 3 files changed, 10 insertions(+), 1
// deletion(-)".
func parseStats(out string) map[string][3]int {
	stats := make(map[string][3]int)
	cur := ""
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		if id, _, _ := strings.Cut(line, " "); isObjectID(id) && !strings.HasPrefix(line, " ") {
			cur = id
			continue
		}
		if cur == "" || !strings.HasPrefix(line, " ") {
			continue
		}
		var s [3]int
		for _, part := range strings.Split(strings.TrimSpace(line), ", ") {
			num, word, _ := strings.Cut(part, " ")
			n, err := strconv.Atoi(num)
			if err != nil {
				continue
			}
			switch {
			case strings.HasPrefix(word, "file"):
				s[0] = n
			case strings.HasPrefix(word, "insertion"):
				s[1] = n
			case strings.HasPrefix(word, "deletion"):
				s[2] = n
			}
		}
		stats[cur] = s
		cur = ""
	}
	return stats
}
