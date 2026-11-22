// SPDX-License-Identifier: AGPL-3.0-or-later

package claudecode

import (
	"cmp"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/some-du6e/pairtime/pkg/event"
)

// stateVersion is the format of the memory SaveState writes.
const stateVersion = 1

// Bounds on what SaveState keeps, which the agent writes to state.json
// about once a minute: the sessions heard from most recently, and in each
// the turns that started last. A queued message names the turn that is
// running (WP-AD1's step 21), or one a call of the session named before it
// (E-cc-queued-turn), so a few turns back is plenty.
const (
	maxSavedSessions = 64
	maxSavedTurns    = 16
	// maxSavedFiles and maxSavedLogSessions bound the transcript files and
	// sessions whose memory the transcript reader saves: those read from
	// last. A file whose memory isn't saved starts afresh where the
	// tailers resume it.
	maxSavedFiles       = 64
	maxSavedLogSessions = 64
)

// savedState is the adapter's memory as SaveState writes it. Like the
// memory, it holds Claude Code's own session, prompt, tool and subagent
// ids, which never leave the device, and keyed values, but no content: a
// scheduled prompt is its cron key and its length (scheduled).
type savedState struct {
	V        int            `json:"v"`
	Clock    uint64         `json:"clock"`
	Sessions []savedSession `json:"sessions,omitempty"`
	// Files and LogSessions are the transcript reader's (logparse.go):
	// each file's content-free state at the point a restart delivers it
	// from again (txFile.snap), and what it wrote for each session.
	Files       []savedFile       `json:"files,omitempty"`
	LogSessions []savedLogSession `json:"log_sessions,omitempty"`
}

type savedFile struct {
	Key   string  `json:"key"`
	At    int64   `json:"at"`
	State txState `json:"state"`
	Seen  uint64  `json:"seen"`
}

type savedLogSession struct {
	ID       string `json:"id"`
	Token    string `json:"token,omitempty"`
	Started  bool   `json:"started,omitempty"`
	NativeAt int64  `json:"native_at,omitempty"`
	Opened   bool   `json:"opened,omitempty"`
	Cleared  bool   `json:"cleared,omitempty"`
	// Hold is how long past startGrace the host holds the session's first
	// session.start, in ms (LogHoldStart), so a restart while a procs tie
	// waits holds the start its replay makes again.
	Hold int64  `json:"hold,omitempty"`
	Seen uint64 `json:"seen"`
}

type savedSession struct {
	ID        string   `json:"id"`
	HookStart bool     `json:"hook_start,omitempty"`
	Started   []string `json:"started,omitempty"`
	// Named is when a call first named each of Started's ids, in Unix ms
	// (session.named). Memory saved before it existed has none, and an id
	// with no time counts as named before any call.
	Named    map[string]int64 `json:"named,omitempty"`
	Turn     string           `json:"turn,omitempty"`
	Unnamed  int              `json:"unnamed,omitempty"`
	Crons    []savedCron      `json:"crons,omitempty"`
	Requests []savedRequest   `json:"requests,omitempty"`
	Posts    []savedPost      `json:"posts,omitempty"`
	// HookStops are session.hookStops' last maxSavedTurns.
	HookStops []string `json:"hook_stops,omitempty"`
	// NativeAt is session.nativeAt; memory saved before it existed has
	// none, and a session with HookStart is then native from its start.
	NativeAt int64  `json:"native_at,omitempty"`
	Seen     uint64 `json:"seen"`
}

type savedCron struct {
	Key string `json:"key"`
	N   int    `json:"n"`
	Cut bool   `json:"cut,omitempty"`
}

// savedRequest is a held PermissionRequest, with the parts of its
// envelope an agent.permission record takes (Decide).
type savedRequest struct {
	T          int64         `json:"t"`
	Mono       int64         `json:"mono,omitempty"`
	BootID     string        `json:"boot_id,omitempty"`
	Source     event.Source  `json:"source"`
	Via        event.Via     `json:"via"`
	Harness    event.Harness `json:"harness"`
	SessionKey string        `json:"session_key"`
	ProjectKey string        `json:"project_key,omitempty"`
	Tool       string        `json:"tool,omitempty"`
	Agent      string        `json:"agent,omitempty"`
	Prompt     string        `json:"prompt,omitempty"`
}

type savedPost struct {
	T         int64  `json:"t"`
	ToolUseID string `json:"tool_use_id"`
	Tool      string `json:"tool,omitempty"`
	Agent     string `json:"agent,omitempty"`
	Native    bool   `json:"native,omitempty"`
}

// SaveState returns what the adapter remembers of the sessions it parsed,
// for the agent to keep in state.json across a restart (adapters.Stateful):
// which sessions have a session.start from the walk, the turn ids the
// session's calls named and when each was first named, the scheduled
// prompts each session's last stop listed, the permission requests held
// for Decide, which tool calls came via hook, and since when the session's
// hooks are via hook. It keeps the
// maxSavedSessions sessions heard from last, and of each the maxSavedTurns
// turn ids named last. Of the transcript reader it keeps each file's state
// where a restart delivers the file from again, which holds no content,
// and the session.start it wrote for each session (logparse.go). The same
// memory gives the same bytes.
func (a *Adapter) SaveState() (json.RawMessage, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	all := make([]*session, 0, len(a.sessions))
	for _, s := range a.sessions {
		all = append(all, s)
	}
	// Newest first; seen is unique, since every call advances the clock.
	slices.SortFunc(all, func(x, y *session) int { return cmp.Compare(y.seen, x.seen) })
	if len(all) > maxSavedSessions {
		all = all[:maxSavedSessions]
	}
	st := savedState{V: stateVersion, Clock: a.clock}
	for _, s := range all {
		ss := savedSession{ID: s.id, HookStart: s.hookStart, Turn: s.turn, Unnamed: s.unnamed, Seen: s.seen}
		ss.Started = slices.Clone(s.started[max(0, len(s.started)-maxSavedTurns):])
		ss.HookStops = slices.Clone(s.hookStops[max(0, len(s.hookStops)-maxSavedTurns):])
		for _, id := range ss.Started {
			if t, ok := s.named[id]; ok {
				if ss.Named == nil {
					ss.Named = make(map[string]int64, len(ss.Started))
				}
				ss.Named[id] = t
			}
		}
		for _, sc := range s.crons {
			ss.Crons = append(ss.Crons, savedCron{Key: sc.key, N: sc.n, Cut: sc.cut})
		}
		for _, r := range s.requests {
			ss.Requests = append(ss.Requests, savedRequest{
				T: r.env.T, Mono: r.env.Mono, BootID: r.env.BootID, Source: r.env.Source, Via: r.env.Via,
				Harness: r.env.Harness, SessionKey: r.env.SessionKey, ProjectKey: r.env.ProjectKey,
				Tool: r.tool, Agent: r.agent, Prompt: r.prompt,
			})
		}
		for _, p := range s.posts {
			ss.Posts = append(ss.Posts, savedPost{T: p.t, ToolUseID: p.toolUseID, Tool: p.tool, Agent: p.agent, Native: p.native})
		}
		ss.NativeAt = s.nativeAt
		st.Sessions = append(st.Sessions, ss)
	}
	files := make([]*txFile, 0, len(a.files))
	for _, f := range a.files {
		files = append(files, f)
	}
	// A file whose state holds something back always goes, past the
	// bound: without it a restart would start the file afresh in the
	// middle of a turn. The rest go newest first.
	slices.SortFunc(files, func(x, y *txFile) int {
		if px, py := x.snapAt < x.at, y.snapAt < y.at; px != py {
			if px {
				return -1
			}
			return 1
		}
		return cmp.Compare(y.seen, x.seen)
	})
	for i, f := range files {
		if i >= maxSavedFiles && f.snapAt >= f.at {
			break
		}
		st.Files = append(st.Files, savedFile{Key: f.key, At: f.snapAt, State: f.snap, Seen: f.seen})
	}
	logs := make([]*txSession, 0, len(a.logSessions))
	for _, s := range a.logSessions {
		logs = append(logs, s)
	}
	slices.SortFunc(logs, func(x, y *txSession) int { return cmp.Compare(y.seen, x.seen) })
	for _, s := range logs[:min(len(logs), maxSavedLogSessions)] {
		st.LogSessions = append(st.LogSessions, savedLogSession{ID: s.id, Token: s.token, Started: s.started,
			NativeAt: s.nativeAt, Opened: s.opened, Cleared: s.cleared, Hold: s.hold, Seen: s.seen})
	}
	return json.Marshal(st)
}

// LoadState replaces the adapter's memory with what SaveState returned
// (adapters.Stateful). The agent calls it before the adapter's first
// Parse. Memory of another format is an error, and leaves the adapter's
// own memory as it was.
func (a *Adapter) LoadState(raw json.RawMessage) error {
	var st savedState
	if err := json.Unmarshal(raw, &st); err != nil {
		return fmt.Errorf("claudecode: the saved memory doesn't read: %w", err)
	}
	if st.V != stateVersion {
		return fmt.Errorf("claudecode: the saved memory is format %d, and this build reads %d", st.V, stateVersion)
	}
	sessions := make(map[string]*session, len(st.Sessions))
	clock := st.Clock
	for _, ss := range st.Sessions {
		if ss.ID == "" || sessions[ss.ID] != nil || len(sessions) >= maxSessions {
			continue
		}
		s := &session{id: ss.ID, hookStart: ss.HookStart, turn: ss.Turn, unnamed: max(ss.Unnamed, 0), seen: ss.Seen}
		s.started = slices.Clone(ss.Started[max(0, len(ss.Started)-maxTurns):])
		s.hookStops = slices.Clone(ss.HookStops[max(0, len(ss.HookStops)-maxTurns):])
		for _, id := range s.started {
			if t, ok := ss.Named[id]; ok {
				if s.named == nil {
					s.named = make(map[string]int64, len(s.started))
				}
				s.named[id] = t
			}
		}
		for _, c := range ss.Crons {
			s.crons = append(s.crons, scheduled{key: c.Key, n: c.N, cut: c.Cut})
		}
		for _, r := range ss.Requests[max(0, len(ss.Requests)-maxHeld):] {
			s.requests = append(s.requests, &request{
				env: event.Envelope{T: r.T, Mono: r.Mono, BootID: r.BootID, Source: r.Source, Via: r.Via,
					Harness: r.Harness, SessionKey: r.SessionKey, ProjectKey: r.ProjectKey},
				tool: r.Tool, agent: r.Agent, prompt: r.Prompt,
			})
		}
		for _, p := range ss.Posts[max(0, len(ss.Posts)-maxHeld):] {
			s.posts = append(s.posts, post{t: p.T, toolUseID: p.ToolUseID, tool: p.Tool, agent: p.Agent, native: p.Native})
		}
		if ss.HookStart {
			s.nativeAt = max(ss.NativeAt, 0)
		}
		sessions[ss.ID] = s
		clock = max(clock, ss.Seen)
	}
	files := make(map[string]*txFile, len(st.Files))
	for _, sf := range st.Files {
		if sf.Key == "" || files[sf.Key] != nil || len(files) >= maxLogFiles || sf.At < 0 {
			continue
		}
		files[sf.Key] = &txFile{key: sf.Key, at: sf.At, st: sf.State, snap: sf.State, snapAt: sf.At, seen: sf.Seen}
		clock = max(clock, sf.Seen)
	}
	logs := make(map[string]*txSession, len(st.LogSessions))
	for _, sl := range st.LogSessions {
		if sl.ID == "" || logs[sl.ID] != nil || len(logs) >= maxLogSessions {
			continue
		}
		logs[sl.ID] = &txSession{id: sl.ID, token: sl.Token, started: sl.Started, nativeAt: max(sl.NativeAt, 0),
			opened: sl.Opened, cleared: sl.Cleared, hold: max(sl.Hold, 0), seen: sl.Seen}
		clock = max(clock, sl.Seen)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.sessions, a.clock = sessions, clock
	a.files, a.logSessions = files, logs
	return nil
}
