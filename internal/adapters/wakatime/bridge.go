// SPDX-License-Identifier: AGPL-3.0-or-later

package wakatime

import (
	"math"
	"path"
	"path/filepath"
	"slices"
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

// maxMarks bounds the AI sessions the bridge keeps a mark for, and the
// WakaTime project names apart from them (Bridge.keep).
const maxMarks = 10_000

// mark is what the bridge knows of a WakaTime project name or an AI
// session from its file heartbeats: whether the latest of them, by the
// heartbeats' own times, was in a folder out of pairtime (DESIGN 8.8), and
// the project of the latest one that resolved to a project. The times
// decide, not the order heartbeats come in, since wakatime-cli sends the
// newest first, and no time counts as later than the agent's clock when
// its batch came in, plus lateRead (remember).
type mark struct {
	// t is the latest file heartbeat's time, and out says it was in a
	// folder out of pairtime.
	t   float64
	out bool
	// key is the project of the latest file heartbeat that resolved to
	// one, and keyT that heartbeat's time. A name keeps it while its
	// latest heartbeat is out of pairtime, as in an excluded subfolder of
	// its repo, so ProjectNames still lists it.
	key  string
	keyT float64
}

// note returns m with a file heartbeat at t, out of pairtime or in the
// project with key key, from a batch whose clock bound is now (remember).
// At equal times a heartbeat out of pairtime wins, and of two tracked ones
// the smaller key, so the order of a batch never decides. A heartbeat
// stamped later than now counts as at now: for heartbeats stamped ahead of
// the agent's clock, arrival is all the bridge has to go by, so of two of
// them the one in the later batch wins. A time later than now, left from
// before the agent's clock stepped back, gives way to any heartbeat.
func (m mark) note(t float64, out bool, key string, now float64) mark {
	if t > m.t || (t == m.t && out) || m.t > now {
		m.t, m.out = t, out
	}
	if !out && (t > m.keyT || (t == m.keyT && key < m.key) || m.keyT > now) {
		m.key, m.keyT = key, t
	}
	return m
}

// place is where m puts a heartbeat with no file: out of pairtime when the
// latest file heartbeat was, else in its project.
func (m mark) place() place {
	if m.out {
		return place{ignored: true}
	}
	return place{project: m.key}
}

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
//     ai_line_changes above 0 it becomes an agent.tool edit of its file;
//     with ai_prompt_length above 0, a prompt.submit with that many
//     characters and no text, at the heartbeat's time. One heartbeat can be
//     both, and the edit comes first, because the prompt such a heartbeat
//     carries is most often the next turn's. wakatime-cli stamps a prompt
//     with the time of a transcript entry it batches it with, and can sum
//     several prompts' lengths, so a bridge prompt's time and length are
//     approximate, and stay as wakatime-cli gives them
//     (docs/errata/2026-11-23-bridge-prompt-times.md).
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
// the file, so the two compare equal. A file on a Windows share, which
// wakatime-cli sends as \\server/share/dir/file, is placed by its folder
// like any other, but neither adapter can key it, so its records name no
// file and the 120 s rule passes it by. A transcript-derived heartbeat's
// ai_session is the harness's session id, keyed as the hook path keys it
// (adapters.Keys.Session), so a session a hook or log reader also reports
// is one session, where the bridge's records are a cross-check (DESIGN
// 5.2, 5.7).
//
// A heartbeat out of pairtime makes no record (DESIGN 8.8): one whose file
// is in a folder out of it, one of a session the agent keeps out
// (BridgeOptions.SessionIgnored), and one with no file whose session's
// latest file heartbeat was in such a folder, or, for a session the bridge
// has seen no file of, whose WakaTime project name's was. The session goes
// first because its own latest file heartbeat is the better witness: a
// tracked repo and an excluded subfolder of it can share a name. "Latest"
// is by the heartbeats' own times, none counted later than the agent's
// clock when its batch came in. A session the agent keeps out still has
// its file heartbeats placed, so an excluded folder marks their names and
// their agent edits count for the 120 s rule; they make no record.
//
// It also maps WakaTime project names to project keys, from the file
// heartbeats whose folders resolve to a project, for program linking
// (DESIGN 5.10). The names are content and stay on the device. It is safe
// for concurrent use.
type Bridge struct {
	opts BridgeOptions

	mu sync.Mutex
	// agentEdits holds the times of the agent's edits of each file, by
	// project and file key, for AgentEditWindow, as long as Horizon
	// reaches. A file's key is its path relative to its project's root,
	// so the project tells main.go in one project from main.go in another.
	agentEdits map[fileOf][]int64
	// names holds a mark for each WakaTime project name, and sessions one
	// for each AI session, markLimit in each at most (keep).
	names     map[string]mark
	sessions  map[string]mark
	markLimit int
}

// BridgeOptions are what a Bridge needs from the agent: the adapters'
// Options, which key ids and resolve folders, and a test of the sessions
// the agent keeps out of pairtime.
type BridgeOptions struct {
	adapters.Options
	// SessionIgnored reports whether the agent keeps the harness session
	// with key session out of pairtime, because a hook call or a log line
	// of it named a folder out of it (hookTies.ignored,
	// LogReader.LogIgnored; DESIGN 8.8). Every heartbeat of such a session
	// makes no record, the prompts that come before its first file
	// heartbeat included. Its file heartbeats are still placed first, so an
	// excluded folder still marks their WakaTime names, and their agent
	// edits still make an editor's echo of them the agent's. It is called
	// with the bridge's lock held, so it must not call the bridge. Nil keeps
	// no session out.
	SessionIgnored func(session string) bool
}

// NewBridge returns a Bridge that keys ids, resolves projects and keeps
// sessions out with opts. It panics when opts.Keys is nil, as an adapter
// does, since every session and turn it records is keyed under the
// device's K_session.
func NewBridge(opts BridgeOptions) *Bridge {
	if opts.Keys == nil {
		panic("wakatime: NewBridge needs Options.Keys, the device's K_session")
	}
	return &Bridge{opts: opts, agentEdits: map[fileOf][]int64{}, names: map[string]mark{}, sessions: map[string]mark{}, markLimit: maxMarks}
}

// fileOf names a file by its project's key and its own.
type fileOf struct{ project, file string }

// place is where a heartbeat is: its project's key, its file's key, and
// whether it is out of pairtime.
type place struct {
	project, file string
	ignored       bool
}

// Records returns the records batch b makes, in the order of its
// heartbeats, read at the agent's clock reading at. It first places every
// file heartbeat, which marks where WakaTime names and AI sessions are,
// then places the other heartbeats by those marks, then notes the batch's
// agent edits, so an editor edit finds an agent edit of the same batch
// whichever came first, and last drops the heartbeats of the sessions the
// agent keeps out. An agent edit that arrives in a later batch can't
// change an editor record already made. Before it runs, the agent resolves
// the batch's Folders.
//
// Each record's LogKey is the heartbeat's Key and the record's role, so
// the agent can make a replay's records the same event_ids, as it does for
// a log line read again.
func (br *Bridge) Records(b Batch, at Clock) []adapters.Record {
	verified := b.Peer.Pass()
	br.mu.Lock()
	defer br.mu.Unlock()
	places := make([]place, len(b.Heartbeats))
	now := float64(at.T+lateRead) / 1000
	for i, h := range b.Heartbeats {
		if isFile(h) {
			places[i] = br.placeFile(h, now)
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
	for i, h := range b.Heartbeats {
		if br.sessionIgnored(h.AISession) {
			places[i].ignored = true
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

// Folders returns the folders Records asks the project resolver about for
// batch b, each once, in order: the folder of each file its heartbeats
// name. The agent makes each one known first (gitHost.known) and holds the
// batch until their Tracks end, as it holds a hook call whose folder it is
// still resolving (D-park-unresolved), so no heartbeat goes without its
// project because a resolve was slow. The git adapter maps a subfolder to
// its repo's project.
func (br *Bridge) Folders(b Batch) []string {
	var out []string
	seen := map[string]bool{}
	for _, h := range b.Heartbeats {
		if d, ok := folder(h); ok && !seen[d] {
			seen[d] = true
			out = append(out, d)
		}
	}
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

// ProjectNames returns each WakaTime project name a file heartbeat has
// resolved to a project, with the project of the latest such heartbeat. A
// name keeps its project after a later heartbeat in a folder out of
// pairtime, such as an excluded subfolder of its repo, and a name seen only
// in such folders isn't among them.
func (br *Bridge) ProjectNames() map[string]string {
	br.mu.Lock()
	defer br.mu.Unlock()
	out := make(map[string]string, len(br.names))
	for n, m := range br.names {
		if m.key != "" {
			out[n] = m.key
		}
	}
	return out
}

// sessionIgnored reports whether the agent keeps the AI session with id
// out of pairtime.
func (br *Bridge) sessionIgnored(id string) bool {
	return id != "" && br.opts.SessionIgnored != nil && br.opts.SessionIgnored(br.opts.Keys.Session(id))
}

// transcript reports whether h is transcript-derived.
func transcript(h wakacompat.Heartbeat) bool { return h.AISession != "" }

// isFile reports whether h names a file by an absolute path, which alone
// the project resolver can take (folder).
func isFile(h wakacompat.Heartbeat) bool {
	_, ok := folder(h)
	return ok
}

// folder returns the folder of the file h names, for the project resolver,
// when h is a file heartbeat with an absolute path: one slashPath takes,
// one this OS takes, such as a Linux file whose name holds a backslash, or
// a file on a Windows share, which wakatime-cli v2.28.4 sends as
// \\server/share/dir/file (FormatFilePath in its pkg/windows). The ledger
// can't key the last two, so they are placed by their folder alone.
func folder(h wakacompat.Heartbeat) (string, bool) {
	if h.Type != "file" {
		return "", false
	}
	if _, ok := slashPath(h.Entity); ok || filepath.IsAbs(h.Entity) {
		return filepath.Dir(h.Entity), true
	}
	return shareDir(h.Entity)
}

// shareDir returns the folder of p when p is a file on a Windows share,
// \\server\share\file, with '\' or '/' between its parts after the first
// two. A server of "." or "?" begins one of Windows' device paths instead.
func shareDir(p string) (string, bool) {
	rest, ok := strings.CutPrefix(p, `\\`)
	if !ok {
		return "", false
	}
	parts := strings.Split(strings.ReplaceAll(rest, `\`, "/"), "/")
	if len(parts) < 3 || parts[0] == "?" || slices.ContainsFunc(parts, func(s string) bool {
		return s == "" || s == "." || s == ".."
	}) {
		return "", false
	}
	return filepath.Clean(p[:strings.LastIndexAny(p, `/\`)]), true
}

// aiLines is the lines h says an AI agent changed.
func aiLines(h wakacompat.Heartbeat) int64 {
	if h.AILineChanges == nil {
		return 0
	}
	return *h.AILineChanges
}

// placeFile places file heartbeat h by its folder and notes it on its
// marks, bounded by now (remember).
func (br *Bridge) placeFile(h wakacompat.Heartbeat, now float64) place {
	var p place
	var root string
	if br.opts.Project != nil {
		// A resolver reports a folder out of pairtime with ok false
		// (adapters.Project.Ignored), so Ignored is read on any answer.
		dir, _ := folder(h)
		proj, ok := br.opts.Project(dir)
		if proj.Ignored {
			br.remember(h, true, "", now)
			return place{ignored: true}
		}
		if ok {
			p.project, root = proj.Key, proj.Root
		}
	}
	if lp, ok := ledgerPath(root, h.Entity); ok && br.opts.Features != nil {
		if v, err := br.opts.Features.Path(lp); err == nil {
			p.file = v.String()
		}
	}
	if p.project != "" {
		br.remember(h, false, p.project, now)
	}
	return p
}

// remember notes file heartbeat h on the marks of its WakaTime project
// name and its AI session: out of pairtime, or in the project with key key.
//
// now bounds the time the marks take: the agent's clock when h's batch
// came in, plus lateRead, in seconds. A heartbeat stamped later counts as
// at now, so one stamped an hour ahead can't hold its marks for that hour
// against the file heartbeats that follow it.
func (br *Bridge) remember(h wakacompat.Heartbeat, out bool, key string, now float64) {
	t := min(h.Time, now)
	if h.Project != nil && *h.Project != "" {
		br.keep(br.names, *h.Project, br.names[*h.Project].note(t, out, key, now))
	}
	if h.AISession != "" {
		br.keep(br.sessions, h.AISession, br.sessions[h.AISession].note(t, out, key, now))
	}
}

// keep stores mark m under k in marks, which holds markLimit marks at
// most. A name or session the bridge forgets places nothing, and its
// heartbeats with no file make records again. That costs a tracked mark
// its project, but a forgotten mark out of pairtime would let excluded
// work through. So past the limit a new mark takes the place of the
// oldest tracked one. When every mark is out of pairtime, a new tracked
// mark isn't kept, and a new one out of pairtime takes the place of the
// oldest.
func (br *Bridge) keep(marks map[string]mark, k string, m mark) {
	if _, held := marks[k]; !held && len(marks) >= br.markLimit {
		old, ok := oldest(marks, false)
		if !ok {
			if !m.out {
				return
			}
			old, _ = oldest(marks, true)
		}
		delete(marks, old)
	}
	marks[k] = m
}

// oldest returns the key of the mark in marks with the earliest time, of
// the tracked marks only unless all is true, and of equal times the
// smaller key.
func oldest(marks map[string]mark, all bool) (string, bool) {
	var key string
	var t float64
	found := false
	for k, m := range marks {
		if m.out && !all {
			continue
		}
		if !found || m.t < t || (m.t == t && k < key) {
			key, t, found = k, m.t, true
		}
	}
	return key, found
}

// placeOther places a heartbeat with no file by its AI session's mark, or,
// for a session with none, by its WakaTime name's.
func (br *Bridge) placeOther(h wakacompat.Heartbeat) place {
	if h.AISession != "" {
		if m, ok := br.sessions[h.AISession]; ok {
			return m.place()
		}
	}
	if h.Project != nil {
		if m, ok := br.names[*h.Project]; ok {
			return m.place()
		}
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
	// The edit first: the prompt a heartbeat with both carries is most
	// often the next turn's (docs/errata/2026-11-23-bridge-prompt-times.md).
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
	if h.AIPromptLength > 0 {
		n := count(h.AIPromptLength)
		// The turn is the heartbeat's own. Its time alone could give two
		// prompts of one session one turn, and the scorer starts a turn
		// for each session and turn key. A replay keeps its key.
		prompt := &event.PromptSubmit{
			TurnKey:  br.opts.Keys.Turn(h.AISession, key),
			LenChars: n, TypedLenEst: n, OriginDeclared: event.DeclaredNone,
		}
		out = append(out, adapters.Record{Event: event.Event{Envelope: env, Data: prompt}, LogKey: key + ".prompt"})
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
