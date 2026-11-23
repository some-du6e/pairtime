// SPDX-License-Identifier: AGPL-3.0-or-later

package t3code

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/adapters"
)

//go:embed testdata/schema.sql
var schemaSQL string

// sqlite3Tool returns the sqlite3 tool the tests build stores with. CI
// must have it, so a missing tool fails a test there and skips it
// elsewhere.
func sqlite3Tool(t *testing.T) string {
	t.Helper()
	p, err := exec.LookPath("sqlite3")
	if err == nil {
		return p
	}
	if os.Getenv("CI") != "" {
		t.Fatalf("sqlite3 isn't installed, and the T3 Code reader's tests must run in CI: %v", err)
	}
	t.Skip("sqlite3 isn't installed, so this test can't build a T3 Code store; CI runs it")
	return ""
}

// base is the stamp of a store's first row.
var base = time.Date(2026, 11, 23, 4, 0, 0, 0, time.UTC)

// at is base plus d, as T3 stamps it.
func at(d time.Duration) string { return stamp(base.Add(d).UnixMilli()) }

// store is a T3 Code store a test builds: T3's schema from
// testdata/schema.sql, with invented rows, held open as T3's server holds
// its own, so its -wal and -shm files exist.
type store struct {
	t       *testing.T
	sqlite3 string
	home    string // the T3 home
	path    string
	pending []string
	hold    *exec.Cmd
	holdIn  io.WriteCloser
	// runs counts the runs written, so each gets an ordinal of its own.
	runs int
}

// newStore makes a store with T3's schema, migrations 55 and the head 85,
// a project rooted at /work/alpha and its thread th1, which T3 runs on
// Claude Code in session sess-1 (provider thread pt1), and holds it open.
func newStore(t *testing.T) *store {
	t.Helper()
	tool := sqlite3Tool(t)
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "userdata"), 0o700); err != nil {
		t.Fatal(err)
	}
	s := &store{t: t, sqlite3: tool, home: home, path: filepath.Join(home, "userdata", "statev2.sqlite")}
	s.add("PRAGMA journal_mode=WAL;")
	s.add(schemaSQL)
	s.migration(55, "OrchestrationV2")
	s.migration(85, "TurnItemCounts")
	s.project("p1", "/work/alpha")
	s.thread("th1", "p1", "")
	s.providerThread("pt1", "th1", "claudeAgent", "sess-1")
	s.flush()
	s.open()
	return s
}

// add queues SQL for the next flush.
func (s *store) add(sql string) { s.pending = append(s.pending, sql) }

// flush writes what add queued, as T3's server would, through a short
// read-write sqlite3 of its own.
func (s *store) flush() {
	s.t.Helper()
	if len(s.pending) == 0 {
		return
	}
	sql := strings.Join(s.pending, "\n")
	s.pending = nil
	cmd := exec.Command(s.sqlite3, "-batch", "-bail", "-init", os.DevNull, s.path)
	cmd.Stdin = strings.NewReader(sql)
	if out, err := cmd.CombinedOutput(); err != nil {
		s.t.Fatalf("writing the store: %v\n%s", err, out)
	}
}

// open holds the store open, as T3's server does while it runs, and waits
// for its -wal and -shm files.
func (s *store) open() {
	s.t.Helper()
	if s.hold != nil {
		return
	}
	cmd := exec.Command(s.sqlite3, "-batch", "-init", os.DevNull, s.path)
	in, err := cmd.StdinPipe()
	if err != nil {
		s.t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		s.t.Fatal(err)
	}
	s.hold, s.holdIn = cmd, in
	s.t.Cleanup(s.close)
	// A read makes the -wal and -shm files, which stay while the
	// connection does.
	if _, err := io.WriteString(in, "SELECT count(*) FROM sqlite_master;\n"); err != nil {
		s.t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for !running(s.path) {
		if time.Now().After(deadline) {
			s.t.Fatal("the store's -wal and -shm files never appeared")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// close lets the store go, as T3's server does when it stops: its last
// connection checkpoints and removes the -wal and -shm files.
func (s *store) close() {
	if s.hold == nil {
		return
	}
	s.holdIn.Close()
	_ = s.hold.Wait()
	s.hold, s.holdIn = nil, nil
}

// files lists the files in the store's folder.
func (s *store) files() []string {
	s.t.Helper()
	entries, err := os.ReadDir(filepath.Dir(s.path))
	if err != nil {
		s.t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

// q is s as a SQL string literal.
func q(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

// qn is s as a SQL string literal, or NULL when it is empty.
func qn(s string) string {
	if s == "" {
		return "NULL"
	}
	return q(s)
}

// payload is v as JSON.
func payload(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}

func (s *store) migration(id int, name string) {
	s.add(fmt.Sprintf("INSERT INTO effect_sql_migrations (migration_id, name) VALUES (%d, %s);", id, q(name)))
}

func (s *store) project(id, root string) {
	s.add(fmt.Sprintf("INSERT INTO projection_projects (project_id, title, workspace_root, scripts_json, created_at, updated_at)"+
		" VALUES (%s, 'invented project', %s, '[]', %s, %s);", q(id), q(root), q(at(0)), q(at(0))))
}

// thread adds a thread of project, running in worktree, or in the
// project's root when worktree is "".
func (s *store) thread(id, project, worktree string) {
	var wt any
	if worktree != "" {
		wt = worktree
	}
	p := payload(map[string]any{"id": id, "projectId": project, "title": "invented thread", "worktreePath": wt,
		"guestShare": map[string]any{"messages": []any{}}})
	s.add(fmt.Sprintf("INSERT INTO orchestration_v2_projection_threads (thread_id, project_id, title, default_provider, runtime_mode,"+
		" interaction_mode, created_at, updated_at, payload_json) VALUES (%s, %s, 'invented thread', 'claudeAgent', 'full-access',"+
		" 'default', %s, %s, %s);", q(id), q(project), q(at(0)), q(at(0)), q(p)))
}

// providerThread adds a provider thread of driver whose current session
// is native, or none when native is "".
func (s *store) providerThread(id, thread, driver, native string) {
	var ref any
	if native != "" {
		ref = map[string]any{"driver": driver, "nativeId": native, "strength": "strong"}
	}
	p := payload(map[string]any{"id": id, "driver": driver, "appThreadId": thread, "nativeThreadRef": ref})
	s.add(fmt.Sprintf("INSERT INTO orchestration_v2_projection_provider_threads (provider_thread_id, thread_id, provider, status,"+
		" updated_at, payload_json, driver) VALUES (%s, %s, 'claude', 'active', %s, %s, %s)"+
		" ON CONFLICT(provider_thread_id) DO UPDATE SET payload_json = excluded.payload_json, driver = excluded.driver;",
		q(id), q(thread), q(at(0)), q(p), q(driver)))
}

// run is a T3 run, as the tests write one.
type run struct {
	id, thread, message string
	ordinal             int
	providerThread      string
	// requested is when T3 took the run's message: the message's stamp for
	// a run of its own.
	requested time.Duration
	// started is when its provider turn started; nil for a run that hasn't.
	started *time.Duration
	// completed is when it ended; nil while it runs.
	completed *time.Duration
	status    string
	// steerWhenReady says T3 turns the queued run into a steer.
	steerWhenReady bool
}

// run adds the run, or replaces it, as T3's projection upserts runs. A run
// written again keeps its first ordinal.
func (s *store) run(r run) {
	if r.ordinal == 0 {
		s.runs++
		r.ordinal = s.runs
	}
	status := r.status
	if status == "" {
		status = "queued"
		if r.started != nil {
			status = "running"
		}
		if r.completed != nil {
			status = "completed"
		}
	}
	pt := r.providerThread
	if pt == "" {
		pt = "pt1"
	}
	p := map[string]any{"id": r.id, "threadId": r.thread, "ordinal": r.ordinal, "userMessageId": r.message,
		"activeAttemptId": nil, "providerThreadId": pt, "status": status, "requestedAt": at(r.requested),
		"startedAt": nil, "completedAt": nil}
	completed := "NULL"
	if r.started != nil {
		p["startedAt"] = at(*r.started)
	}
	if r.completed != nil {
		p["completedAt"] = at(*r.completed)
		completed = q(at(*r.completed))
	}
	if r.steerWhenReady {
		p["steerWhenReadyRunId"] = "run:thread:th1:ordinal:1"
	}
	s.add(fmt.Sprintf("INSERT INTO orchestration_v2_projection_runs (run_id, thread_id, ordinal, provider, provider_thread_id, status,"+
		" requested_at, completed_at, payload_json) VALUES (%s, %s, %d, 'claude', %s, %s, %s, %s, %s)"+
		" ON CONFLICT(run_id) DO UPDATE SET status = excluded.status, completed_at = excluded.completed_at,"+
		" payload_json = excluded.payload_json;",
		q(r.id), q(r.thread), r.ordinal, q(pt), q(status), q(at(r.requested)), completed, q(payload(p))))
}

// attempt adds a run's attempt, which started in Claude Code session
// native, or hasn't when native is "".
func (s *store) attempt(runID string, ordinal int, native, providerThread string) {
	if providerThread == "" {
		providerThread = "pt1"
	}
	p := map[string]any{"id": fmt.Sprintf("run-attempt:%s:attempt:%d", runID, ordinal), "runId": runID,
		"attemptOrdinal": ordinal, "reason": "initial", "status": "running", "startedAt": nil}
	status := "pending"
	if native != "" {
		p["nativeThreadId"] = native
		status = "running"
	}
	s.add(fmt.Sprintf("INSERT INTO orchestration_v2_projection_run_attempts (attempt_id, thread_id, run_id, attempt_ordinal, root_node_id,"+
		" provider, provider_thread_id, status, payload_json) VALUES (%s, 'th1', %s, %d, 'node', 'claude', %s, %s, %s)"+
		" ON CONFLICT(attempt_id) DO UPDATE SET status = excluded.status, payload_json = excluded.payload_json;",
		q(p["id"].(string)), q(runID), ordinal, q(providerThread), q(status), q(payload(p))))
}

// message is a T3 message row, as the tests write one.
type message struct {
	id, thread, run string
	role            string // "user" when ""
	by, source      string // "user" and "web" when ""
	created         time.Duration
	// updated is created when nil.
	updated *time.Duration
	text    string
	// extra holds payload keys to set, such as T3's marks.
	extra map[string]any
}

// message adds the row, or upserts it by message_id as T3 does, which
// keeps its rowid.
func (s *store) message(m message) {
	role, by, source := m.role, m.by, m.source
	if role == "" {
		role = "user"
	}
	if by == "" {
		by = "user"
	}
	if source == "" {
		source = "web"
	}
	updated := m.created
	if m.updated != nil {
		updated = *m.updated
	}
	var runID any
	if m.run != "" {
		runID = m.run
	}
	p := map[string]any{"id": m.id, "threadId": m.thread, "runId": runID, "nodeId": nil, "role": role, "text": m.text,
		"attachments": []any{}, "streaming": false, "createdBy": by, "creationSource": source,
		"createdAt": at(m.created), "updatedAt": at(updated)}
	for k, v := range m.extra {
		p[k] = v
	}
	s.add(fmt.Sprintf("INSERT INTO orchestration_v2_projection_messages (message_id, thread_id, run_id, role, streaming, created_at,"+
		" updated_at, payload_json) VALUES (%s, %s, %s, %s, 0, %s, %s, %s)"+
		" ON CONFLICT(message_id) DO UPDATE SET run_id = excluded.run_id, created_at = excluded.created_at,"+
		" updated_at = excluded.updated_at, payload_json = excluded.payload_json;",
		q(m.id), q(m.thread), qn(m.run), q(role), q(at(m.created)), q(at(updated)), q(payload(p))))
}

// sent adds a message a person sent from T3's web client in thread th1, at
// created, with the run of its own T3 started for it 100 ms later in
// session sess-1, the thread's: the common case.
func (s *store) sent(id string, created time.Duration, text string) {
	s.sentWith(message{id: id, created: created, text: text})
}

// sentWith is sent for a message with more set: its thread, source, marks
// and so on. The run is r-<id>, and the attempt goes to providerThread, pt1
// when "".
func (s *store) sentWith(m message) {
	if m.thread == "" {
		m.thread = "th1"
	}
	if m.run == "" {
		m.run = "r-" + m.id
	}
	s.message(m)
	started := m.created + 100*time.Millisecond
	s.run(run{id: m.run, thread: m.thread, message: m.id, requested: m.created, started: &started})
	s.attempt(m.run, 1, "sess-1", "")
}

// assistant adds rows that are no person's: an assistant message, as the
// rows between a person's messages are.
func (s *store) assistant(id string, created time.Duration) {
	s.message(message{id: id, thread: "th1", role: "assistant", by: "agent", source: "provider", created: created,
		text: "an invented answer"})
}

// clock is a reader's clock a test sets.
type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

// set puts the clock at base plus d.
func (c *clock) set(d time.Duration) { c.t = base.Add(d) }

// folders is a resolver over a fixed table, with every other folder out.
type folders map[string]Folder

func (f folders) resolve(dir string) Folder {
	if v, ok := f[dir]; ok {
		return v
	}
	return Folder{State: FolderOut}
}

// tracked is a resolver that tracks /work/alpha as project p_alpha.
func tracked() folders {
	return folders{"/work/alpha": {State: FolderTracked, Project: "p_alpha"}}
}

// reader returns a Reader of s with the fixture keys, the clock and the
// folders.
func (s *store) reader(c *clock, f folders, st State) *Reader {
	s.t.Helper()
	r, err := New(Options{
		Stores: []Store{{Path: s.path, From: FromDefault}},
		SQLite: &Runner{Path: s.sqlite3},
		Keys:   adapters.FixtureKeys(),
		Folder: f.resolve,
		Now:    c.now,
		Zone:   time.UTC,
		State:  st,
	})
	if err != nil {
		s.t.Fatal(err)
	}
	return r
}

// mustPoll is r.Poll that fails the test on an error.
func mustPoll(t *testing.T, r *Reader) Batch {
	t.Helper()
	b, err := r.Poll(context.Background())
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	return b
}

// ptr returns a pointer to d.
func ptr(d time.Duration) *time.Duration { return &d }

// session is the session key the hook gives Claude Code session id.
func session(id string) string { return adapters.FixtureKeys().Session(id) }

// spy is a sqlite3 that writes the SQL each run reads to a log, then runs
// the real one with it: a test reads what the reader asked. It needs sh.
type spy struct {
	path string // the spy, as Runner.Path
	log  string
}

func newSpy(t *testing.T, sqlite3 string) *spy {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the sqlite3 spy is a shell script")
	}
	dir := t.TempDir()
	s := &spy{path: filepath.Join(dir, "sqlite3"), log: filepath.Join(dir, "sql.log")}
	script := "#!/bin/sh\n" +
		"in=$(mktemp)\n" +
		"cat > \"$in\"\n" +
		"{ printf '%s\\n' \"-- args: $*\"; cat \"$in\"; printf '%s\\n' '-- end'; } >> " + shellQuote(s.log) + "\n" +
		shellQuote(sqlite3) + " \"$@\" < \"$in\"\n" +
		"code=$?\n" +
		"rm -f \"$in\"\n" +
		"exit $code\n"
	if err := os.WriteFile(s.path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return s
}

// shellQuote quotes s for sh.
func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// runs returns each run's arguments and SQL so far.
func (s *spy) runs(t *testing.T) []string {
	t.Helper()
	b, err := os.ReadFile(s.log)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, r := range strings.Split(string(b), "-- end\n") {
		if strings.TrimSpace(r) != "" {
			out = append(out, r)
		}
	}
	return out
}

// shim is a sqlite3 a test writes, which runs some sh of its own before the
// real one: it fails a run when the test armed it, say. It needs sh.
type shim struct {
	path string // the shim, as Runner.Path
	mark string // the file arm makes, which the sh can test as $mark
}

// The shims' sh. busyOnce answers as a store that t3 doctor's compaction
// holds, once per arm. noJSON answers as a sqlite3 built without JSON
// functions does, to the probe's first statement.
const (
	busyOnce = `if [ -e "$mark" ]; then rm -f "$mark"; cat >/dev/null; echo 'Error: database is locked' >&2; exit 5; fi`
	noJSON   = `cat >/dev/null; echo 'Error: in prepare, no such function: json_type' >&2; exit 1`
)

func newShim(t *testing.T, sqlite3, body string) *shim {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the sqlite3 shim is a shell script")
	}
	dir := t.TempDir()
	s := &shim{path: filepath.Join(dir, "sqlite3"), mark: filepath.Join(dir, "mark")}
	script := "#!/bin/sh\n" +
		"mark=" + shellQuote(s.mark) + "\n" +
		body + "\n" +
		"exec " + shellQuote(sqlite3) + " \"$@\"\n"
	if err := os.WriteFile(s.path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return s
}

// arm makes the shim's mark.
func (s *shim) arm(t *testing.T) {
	t.Helper()
	if err := os.WriteFile(s.mark, nil, 0o600); err != nil {
		t.Fatal(err)
	}
}

// armed reports whether the mark is still there.
func (s *shim) armed() bool { return exists(s.mark) }
