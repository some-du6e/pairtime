// SPDX-License-Identifier: AGPL-3.0-or-later

package wakatime

import (
	"math"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/some-du6e/pairtime/internal/adapters"
	"github.com/some-du6e/pairtime/pkg/event"
	"github.com/some-du6e/pairtime/pkg/ledger/features"
	"github.com/some-du6e/pairtime/pkg/wakacompat"
)

// AgentEditWindow is how near, in milliseconds, an editor edit comes to an
// agent's edit of the same file to count as the agent's: edit_source
// agent, which opens no anchor (DESIGN 5.7). The plugin sends a heartbeat
// when the agent's write changes a file it has open, and it can't tell
// that change from typing.
const AgentEditWindow = 120_000

// lateRead is how long after its time, in milliseconds, a record may reach
// the agent and still carry a reading of the run's clock, as for the lines
// internal/adapters/claudecode reads from a log (logLine.env).
const lateRead = 10_000

// maxSessions bounds the AI sessions the bridge remembers a project for.
const maxSessions = 10_000

// Clock is the agent's clock when it took a batch in: its wall time in
// Unix milliseconds, its run clock in milliseconds and the boot id (SM
// 1.1; internal/platform/clock).
type Clock struct {
	T, Mono int64
	BootID  string
}

// Bridge turns the heartbeats the endpoint took into events, by DESIGN
// 5.7's table:
//
//   - A heartbeat with ai_session is transcript-derived, since wakatime-cli's
//     AI sync sets it on every heartbeat it makes and no plugin can
//     (docs/errata/2026-11-23-wakatime-cli-2.28.4.md, item 1). With
//     ai_prompt_length above 0 it becomes a prompt.submit with that many
//     characters and no text, at the heartbeat's time; with ai_line_changes
//     above 0, an agent.tool edit of its file. One heartbeat can be both, and
//     the prompt comes first.
//   - Any other heartbeat is an editor heartbeat: an editor.activity save
//     when is_write, else an edit when human_line_changes is above 0, else a
//     navigate. Its category is never read, because wakatime-cli relabels
//     human edits near agent activity ai coding (HC 2.2).
//
// The via comes from the batch's peer check (DESIGN 5.7;
// docs/errata/2026-11-19-bridge-unverified.md): editor and bridge when it
// passed, editor_unverified and bridge_unverified when it failed or
// couldn't find the client, on every OS.
//
// Paths go no further than the bridge. A file's path becomes its key as
// the ledger keys it, relative to its project's root (features.Key.Path),
// the same key internal/adapters/claudecode gives the agent's own edit of
// the file, so the two compare equal. A transcript-derived heartbeat's
// ai_session is the harness's session id, keyed as the hook path keys it
// (adapters.Keys.Session), so a session a hook or log reader also reports
// is one session, where the bridge's records are a cross-check (DESIGN
// 5.2, 5.7). A folder out of pairtime makes no record (DESIGN 8.8).
//
// It also maps WakaTime project names to project keys, from the file
// heartbeats whose folders resolve to a project, for program linking
// (DESIGN 5.10). The names are content and stay on the device. It is safe
// for concurrent use.
type Bridge struct {
	opts adapters.Options

	mu sync.Mutex
	// agentEdits holds the times of the agent's edits of each file, by
	// project and file key, for AgentEditWindow, as long as Horizon
	// reaches. A file's key is its path relative to its project's root,
	// so the project tells main.go in one project from main.go in another.
	agentEdits map[fileOf][]int64
	// names maps WakaTime project names to project keys, and sessions AI
	// sessions to the project of their latest file heartbeat.
	names    map[string]string
	sessions map[string]string
}

// NewBridge returns a Bridge that keys ids and resolves projects with
// opts. It panics when opts.Keys is nil, as an adapter does, since every
// session and turn it records is keyed under the device's K_session.
func NewBridge(opts adapters.Options) *Bridge {
	if opts.Keys == nil {
		panic("wakatime: NewBridge needs Options.Keys, the device's K_session")
	}
	return &Bridge{opts: opts, agentEdits: map[fileOf][]int64{}, names: map[string]string{}, sessions: map[string]string{}}
}

// fileOf names a file by its project's key and its own.
type fileOf struct{ project, file string }

// place is where a heartbeat is: its project's key and root, its file's
// key, and whether its folder is out of pairtime.
type place struct {
	project, file string
	ignored       bool
}

// Records returns the records batch b makes, in the order of its
// heartbeats, read at the agent's clock reading at. It first places every
// file heartbeat, which teaches it the projects of WakaTime names and AI
// sessions, then the other heartbeats, then notes the batch's agent edits,
// so an editor edit finds an agent edit of the same batch whichever came
// first. An agent edit that arrives in a later batch can't change an
// editor record already made.
//
// Each record's LogKey is the heartbeat's Key and the record's role, so
// the agent can make a replay's records the same event_ids, as it does for
// a log line read again.
func (br *Bridge) Records(b Batch, at Clock) []adapters.Record {
	verified := b.Peer.Pass()
	br.mu.Lock()
	defer br.mu.Unlock()
	places := make([]place, len(b.Heartbeats))
	for i, h := range b.Heartbeats {
		if isFile(h) {
			places[i] = br.placeFile(h)
		}
	}
	for i, h := range b.Heartbeats {
		if !isFile(h) {
			places[i] = br.placeOther(h)
		}
	}
	for i, h := range b.Heartbeats {
		if transcript(h) && aiLines(h) > 0 && places[i].file != "" && !places[i].ignored {
			br.noteAgentEdit(fileOf{places[i].project, places[i].file}, ms(h.Time))
		}
	}
	var out []adapters.Record
	for i, h := range b.Heartbeats {
		if places[i].ignored {
			continue
		}
		if transcript(h) {
			out = append(out, br.bridgeRecords(h, places[i], verified, at)...)
		} else {
			out = append(out, br.editorRecord(h, places[i], verified, at))
		}
	}
	br.prune(at.T)
	return out
}

// NoteAgentEdit tells the bridge of an agent's edit, at t in Unix
// milliseconds, of the file with key file in the project with key project,
// that came another way, such as a hook's agent.tool, so an editor edit
// near it counts as the agent's too.
func (br *Bridge) NoteAgentEdit(project, file string, t int64) {
	br.mu.Lock()
	defer br.mu.Unlock()
	br.noteAgentEdit(fileOf{project, file}, t)
}

// ProjectNames returns the WakaTime project names the bridge has seen
// resolve to a project, each with its project key.
func (br *Bridge) ProjectNames() map[string]string {
	br.mu.Lock()
	defer br.mu.Unlock()
	out := make(map[string]string, len(br.names))
	for n, k := range br.names {
		out[n] = k
	}
	return out
}

// transcript reports whether h is transcript-derived.
func transcript(h wakacompat.Heartbeat) bool { return h.AISession != "" }

// isFile reports whether h names a file by an absolute path, which alone
// the project resolver and the ledger's path key can take.
func isFile(h wakacompat.Heartbeat) bool {
	_, ok := slashPath(h.Entity)
	return h.Type == "file" && ok
}

// aiLines is the lines h says an AI agent changed.
func aiLines(h wakacompat.Heartbeat) int64 {
	if h.AILineChanges == nil {
		return 0
	}
	return *h.AILineChanges
}

func (br *Bridge) placeFile(h wakacompat.Heartbeat) place {
	var p place
	var root string
	if br.opts.Project != nil {
		if proj, ok := br.opts.Project(filepath.Dir(h.Entity)); ok {
			if proj.Ignored {
				return place{ignored: true}
			}
			p.project, root = proj.Key, proj.Root
		}
	}
	if lp, ok := ledgerPath(root, h.Entity); ok && br.opts.Features != nil {
		if v, err := br.opts.Features.Path(lp); err == nil {
			p.file = v.String()
		}
	}
	if p.project != "" {
		if h.Project != nil && *h.Project != "" {
			br.names[*h.Project] = p.project
		}
		if h.AISession != "" {
			if len(br.sessions) >= maxSessions {
				clear(br.sessions)
			}
			br.sessions[h.AISession] = p.project
		}
	}
	return p
}

// placeOther places a heartbeat with no file: in its AI session's project,
// or else in the project its WakaTime name was seen with.
func (br *Bridge) placeOther(h wakacompat.Heartbeat) place {
	if k := br.sessions[h.AISession]; h.AISession != "" && k != "" {
		return place{project: k}
	}
	if h.Project != nil {
		return place{project: br.names[*h.Project]}
	}
	return place{}
}

func (br *Bridge) noteAgentEdit(f fileOf, t int64) {
	br.agentEdits[f] = append(br.agentEdits[f], t)
}

// nearAgentEdit reports whether an agent edited f within AgentEditWindow
// of t, before it or after it.
func (br *Bridge) nearAgentEdit(f fileOf, t int64) bool {
	for _, a := range br.agentEdits[f] {
		if d := t - a; -AgentEditWindow <= d && d <= AgentEditWindow {
			return true
		}
	}
	return false
}

// prune forgets agent edits from further back than Horizon.
func (br *Bridge) prune(now int64) {
	cut := now - Horizon.Milliseconds()
	for f, ts := range br.agentEdits {
		kept := ts[:0]
		for _, t := range ts {
			if t >= cut {
				kept = append(kept, t)
			}
		}
		if len(kept) == 0 {
			delete(br.agentEdits, f)
		} else {
			br.agentEdits[f] = kept
		}
	}
}

func (br *Bridge) editorRecord(h wakacompat.Heartbeat, p place, verified bool, at Clock) adapters.Record {
	t := ms(h.Time)
	act, lines := event.ActNavigate, 0
	if h.HumanLineChanges != nil && *h.HumanLineChanges > 0 {
		act, lines = event.ActEdit, count(*h.HumanLineChanges)
	}
	if h.IsWrite != nil && *h.IsWrite {
		act = event.ActSave
	}
	source := event.EditSourceUnknown
	if act != event.ActNavigate && p.file != "" && br.nearAgentEdit(fileOf{p.project, p.file}, t) {
		source = event.EditSourceAgent
	}
	via := event.ViaEditorUnverified
	if verified {
		via = event.ViaEditor
	}
	env := event.Envelope{
		T: t, Mono: monoAt(at, t), BootID: at.BootID,
		Source: event.SourceEditor, Via: via, Harness: event.HarnessNone, SessionKey: event.NoSession,
		ProjectKey: p.project,
	}
	data := &event.EditorActivity{Editor: editorOf(h.UserAgent), Act: act, FileHash: p.file, LinesChanged: lines, EditSource: source}
	return adapters.Record{Event: event.Event{Envelope: env, Data: data}, LogKey: h.Key().String() + ".editor"}
}

func (br *Bridge) bridgeRecords(h wakacompat.Heartbeat, p place, verified bool, at Clock) []adapters.Record {
	t := ms(h.Time)
	via := event.ViaBridgeUnverified
	if verified {
		via = event.ViaBridge
	}
	harness, version := harnessOf(h.UserAgent)
	env := event.Envelope{
		T: t, Mono: monoAt(at, t), BootID: at.BootID,
		Source: event.SourceEditor, Via: via, Harness: harness, HarnessVersion: version,
		SessionKey: br.opts.Keys.Session(h.AISession), ProjectKey: p.project,
	}
	key := h.Key().String()
	var out []adapters.Record
	if h.AIPromptLength > 0 {
		n := count(h.AIPromptLength)
		prompt := &event.PromptSubmit{
			TurnKey:  br.opts.Keys.Turn(h.AISession, strconv.FormatInt(t, 10)),
			LenChars: n, TypedLenEst: n, OriginDeclared: event.DeclaredNone,
		}
		out = append(out, adapters.Record{Event: event.Event{Envelope: env, Data: prompt}, LogKey: key + ".prompt"})
	}
	if n := aiLines(h); n > 0 {
		// WakaTime counts the lines an agent added and removed as one
		// number, and the scorer reads only their sum (pkg/score's turn
		// diff lines and bucket fill), so they all go in lines_added.
		tool := &event.AgentTool{ToolClass: event.ToolEdit, LinesAdded: count(n), TStart: t, TEnd: t}
		if p.file != "" {
			tool.Files = []string{p.file}
		}
		out = append(out, adapters.Record{Event: event.Event{Envelope: env, Data: tool}, LogKey: key + ".tool"})
	}
	return out
}

// ms is a heartbeat's time in Unix milliseconds.
func ms(t float64) int64 { return int64(t * 1000) }

// count bounds a count to what an event's int holds on every target,
// windows/386's 32 bits included.
func count(n int64) int { return int(min(max(n, 0), math.MaxInt32)) }

// monoAt is the run clock's reading at t: the arrival's reading taken back
// by how much earlier t is, never below 0, and none for a record that
// reached the agent more than lateRead after its time.
func monoAt(at Clock, t int64) int64 {
	if at.T-t > lateRead {
		return 0
	}
	return max(at.Mono+min(t-at.T, 0), 0)
}

// products returns a user agent's product tokens, name/version, after
// wakatime-cli's own and the platform in parentheses: for
// "wakatime/v2.28.4 (linux-7.0.0-1020-azure-unknown) go1.27.3
// opus/5-5 claude-code/2.1.327", opus/5-5 and claude-code/2.1.327. The Go
// runtime token has no slash and is left out.
func products(ua string) []string {
	if strings.HasPrefix(strings.ToLower(ua), "wakatime/") {
		if i := strings.Index(ua, ")"); i >= 0 {
			ua = ua[i+1:]
		}
	}
	var out []string
	for _, f := range strings.Fields(ua) {
		if strings.Contains(f, "/") {
			out = append(out, f)
		}
	}
	return out
}

// agentHarnesses maps the product names wakatime-cli's AI sync gives AI
// agents in a user agent to the harnesses pkg/event names. "claude" isn't
// one: wakatime-cli writes some models as claude/<version>.
var agentHarnesses = map[string]event.Harness{
	"claude-code": event.HarnessClaudeCode, "claudecode": event.HarnessClaudeCode,
	"codex": event.HarnessCodex, "codex-cli": event.HarnessCodex,
	"cursor": event.HarnessCursor, "cursor-agent": event.HarnessCursor,
	"copilot": event.HarnessCopilot, "github-copilot": event.HarnessCopilot, "github-copilot-cli": event.HarnessCopilot,
	"gemini": event.HarnessGemini, "gemini-cli": event.HarnessGemini,
	"devin": event.HarnessDevin,
	"cline": event.HarnessCline, "cline-cli": event.HarnessCline,
	"opencode": event.HarnessOpenCode, "opencode-cli": event.HarnessOpenCode, "opencode-desktop": event.HarnessOpenCode,
}

// harnessOf is the harness a transcript-derived heartbeat's user agent
// names, the last of its products that names one, with that product's
// version when it is one IsVersion takes, or other.
func harnessOf(ua string) (event.Harness, string) {
	ps := products(ua)
	for i := len(ps) - 1; i >= 0; i-- {
		name, version, _ := strings.Cut(ps[i], "/")
		if h, ok := agentHarnesses[strings.ToLower(name)]; ok {
			if !event.IsVersion(version) {
				version = ""
			}
			return h, version
		}
	}
	return event.HarnessOther, ""
}

// editorOf is the editor an editor heartbeat's user agent names, as a key:
// its first product's name in lower case, such as vscode, or unknown.
func editorOf(ua string) string {
	ps := products(ua)
	if len(ps) == 0 {
		return "unknown"
	}
	name, _, _ := strings.Cut(ps[0], "/")
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		if 'a' <= r && r <= 'z' || '0' <= r && r <= '9' || r == '_' || r == '-' {
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return "unknown"
	}
	return b.String()[:min(b.Len(), 64)]
}

// ledgerPath returns file in the ledger's form (ledger-v1 10.6): relative
// to root when it is inside it, else absolute, with '/' between components
// and a Windows drive letter upper case, as internal/adapters/claudecode's
// ledgerPathUnder makes it, so a file keys alike in both.
func ledgerPath(root, file string) (string, bool) {
	p, ok := slashPath(file)
	if !ok {
		return "", false
	}
	if r, ok := slashPath(root); ok && root != "" {
		if rel, ok := under(r, p); ok {
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
		return strings.ToUpper(p[:1]) + ":" + path.Clean("/"+strings.ReplaceAll(p[2:], `\`, "/")), true
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
