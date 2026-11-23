// SPDX-License-Identifier: AGPL-3.0-or-later

package t3code

import (
	"encoding/hex"
	"strconv"
	"strings"
)

// The tables and the index the reader reads, as T3 d8f3feff's migration 55
// and the later ones leave them (testdata/schema.sql holds their CREATE
// statements; DESIGN 5.1's T3 Code row and docs/errata/2026-11-23-t3-reader.md
// say what the reader reads of them). It reads nothing else: never
// orchestration_v2_message_search, the search table, nor
// messages.search_text, threads.title, projects.title or a thread's whole
// payload, which hold text.
const (
	tMessages        = "orchestration_v2_projection_messages"
	tRuns            = "orchestration_v2_projection_runs"
	tAttempts        = "orchestration_v2_projection_run_attempts"
	tProviderThreads = "orchestration_v2_projection_provider_threads"
	tThreads         = "orchestration_v2_projection_threads"
	tProjects        = "projection_projects"
	tMigrations      = "effect_sql_migrations"
	// iThreadCreated is the (thread_id, created_at, message_id) index the
	// resync walks.
	iThreadCreated = "orchestration_v2_projection_messages_thread_created_idx"
)

// windowRows is how many rowids one window covers. A read further behind
// than this takes several windows.
const windowRows = 500

// maxTextChars is how many characters of a message's text the text query
// reads. A longer text counts the rest by its length alone: a relayed lead
// is at most COMPOSE_MAX_S of typing, far below this.
const maxTextChars = 65536

// hx is expr hex-encoded, or NULL, which prints as N: hex(NULL) prints
// nothing, as the empty string's hex does, so hx keeps the two apart.
func hx(expr string) string {
	return "CASE WHEN (" + expr + ") IS NULL THEN NULL ELSE hex(" + expr + ") END"
}

// tag is a row's first field, which says which statement wrote it.
func tag(name string) string { return "hex('" + name + "')" }

// lit is s as a SQL text literal. Every string the reader puts in its SQL,
// T3's ids, stamps and folders, goes in as hex, so no quote or byte in one
// can end the literal, and none can be read off the SQL by eye.
func lit(s string) string {
	return "CAST(X'" + strings.ToUpper(hex.EncodeToString([]byte(s))) + "' AS TEXT)"
}

// lits is the strings as a list of literals, for IN.
func lits(ss []string) string {
	parts := make([]string, len(ss))
	for i, s := range ss {
		parts[i] = lit(s)
	}
	return strings.Join(parts, ", ")
}

// ints is the numbers as a list, for IN.
func ints(ns []int64) string {
	parts := make([]string, len(ns))
	for i, n := range ns {
		parts[i] = strconv.FormatInt(n, 10)
	}
	return strings.Join(parts, ", ")
}

func num(n int64) string { return strconv.FormatInt(n, 10) }

// The opening of a message's text, as typed and OpensAsGuest read it, in
// SQL: the text from its first character that isn't white space, past a
// leading Ultrathink: line with nothing else on it. spaces are the
// characters Go's strings.TrimSpace trims, Unicode's White_Space. The
// expression is long, but sqlite3 works it out only for the user rows of a
// window, and reads none of the text out.
const (
	textOf = "json_extract(m.payload_json, '$.text')"
	spaces = "char(9, 10, 11, 12, 13, 32, 133, 160, 5760, 8192, 8193, 8194, 8195, 8196, 8197, 8198, 8199, 8200, 8201, 8202," +
		" 8232, 8233, 8239, 8287, 12288)"
	// lead is the text from its first character that isn't white space.
	lead = "ltrim(" + textOf + ", " + spaces + ")"
	// thinkRest is what follows a leading Ultrathink:, thinkEOL where its
	// line ends in it (0 when it doesn't), and thinkLine and thinkAfter the
	// rest of that line and what follows the line.
	thinkRest  = "substr(" + lead + ", 12)"
	thinkEOL   = "instr(" + thinkRest + ", char(10))"
	thinkLine  = "CASE " + thinkEOL + " WHEN 0 THEN " + thinkRest + " ELSE substr(" + thinkRest + ", 1, " + thinkEOL + " - 1) END"
	thinkAfter = "CASE " + thinkEOL + " WHEN 0 THEN '' ELSE ltrim(substr(" + thinkRest + ", " + thinkEOL + " + 1), " + spaces + ") END"
	opening    = "CASE WHEN lower(substr(" + lead + ", 1, 11)) = 'ultrathink:' AND trim(" + thinkLine + ", " + spaces + ") = ''" +
		" THEN " + thinkAfter + " ELSE " + lead + " END"
	// opensAsGuest is OpensAsGuest in SQL: 1 when the text opens as T3 opens
	// a guest's message when it sends it on, NULL for a row with no text.
	opensAsGuest = "substr(" + opening + ", 1, 19) = '" + guestOpening + "'"
)

// The tests every read of a message row makes, in its WHERE clause.
const (
	// isUser keeps user-role rows: a person's messages, and T3's and
	// agents' messages in a person's place.
	isUser = "m.role = 'user'"
	// notGuest keeps a guest's message out: one T3 marked with
	// context.guest, and one whose text opens as T3 opens a guest's when
	// sending it on (opensAsGuest), which catches one that lost the mark, as
	// a guest's message T3 sends again after a declined steer does. The
	// opening is read inside sqlite3 only, and only loses credit: an owner
	// who types those words is taken for a guest. A guest's row the reader
	// already watches stays a guest's whatever T3 changes in it.
	notGuest = "json_type(m.payload_json, '$.context.guest') IS NULL AND (" + opensAsGuest + ") IS NOT 1"
	// isGuest is notGuest's opposite.
	isGuest = "(json_type(m.payload_json, '$.context.guest') IS NOT NULL OR (" + opensAsGuest + ") IS 1)"
	// byPerson is the part of T3's own test for a message a person sent from
	// a client that the store keeps: createdBy user, from the web client
	// (the browser and the desktop app) or the phone app.
	byPerson = "json_extract(m.payload_json, '$.createdBy') = 'user'" +
		" AND json_extract(m.payload_json, '$.creationSource') IN ('web', 'mobile')"
	// folderOf is the folder the message's thread runs in: its worktree,
	// else its project's root (T3's RuntimePolicy).
	folderOf = "COALESCE(json_extract(t.payload_json, '$.worktreePath'), p.workspace_root)"
	// joinFolder joins a message to its thread and its project.
	joinFolder = " JOIN " + tThreads + " AS t ON t.thread_id = m.thread_id" +
		" JOIN " + tProjects + " AS p ON p.project_id = t.project_id"
	// marks is the message's marks as a bitmask: 1 scheduledTaskId (declared
	// cron); 2 senderThreadId and 4 linkedSender (declared cross_session);
	// 8 idleCompaction, 16 workflowScript, 32 notification and 64
	// delegatedCompletion, which no person typed. json_type, never
	// json_extract, so a JSON null still counts as there, and linkedSender's
	// title, which is text, is never read.
	marks = "(json_type(m.payload_json, '$.scheduledTaskId') IS NOT NULL)" +
		" + 2 * (json_type(m.payload_json, '$.senderThreadId') IS NOT NULL)" +
		" + 4 * (json_type(m.payload_json, '$.linkedSender') IS NOT NULL)" +
		" + 8 * (json_type(m.payload_json, '$.idleCompaction') IS NOT NULL)" +
		" + 16 * (json_type(m.payload_json, '$.workflowScript') IS NOT NULL)" +
		" + 32 * (json_type(m.payload_json, '$.notification') IS NOT NULL)" +
		" + 64 * (json_type(m.payload_json, '$.delegatedCompletion') IS NOT NULL)"
)

// The marks' bits.
const (
	markScheduled    = 1
	markSender       = 2
	markLinkedSender = 4
	// markNobody are the marks of a message no person typed.
	markNobody = 8 | 16 | 32 | 64
)

// startSQL reads where a first run's cursor goes: the newest row, since the
// reader backfills nothing (DESIGN 5.1).
func startSQL() string {
	top := "(SELECT max(rowid) FROM " + tMessages + ")"
	return "SELECT " + tag("at") + ", " + hx(top) + ", " +
		hx("(SELECT message_id FROM "+tMessages+" WHERE rowid = "+top+")") + ", " +
		hx("(SELECT created_at FROM "+tMessages+" WHERE rowid = "+top+")") + ";\n"
}

// windowSQL is a read's first run, in one read transaction so that all of
// it sees one state of the store. With the cursor at c it reads:
//
//   - cur: the newest rowid, and the message id and stamp at c, which the
//     cursor check compares with what it holds;
//   - end: the row that ends the window, at most windowRows rowids past c;
//   - fold: each new user row's folder, its thread's worktree or its
//     project's root, with its stamp, for the rows the decision may record
//     (by a person, from a client, not a guest's), so that pairtime's own
//     resolver can say which folders the decision may read;
//   - guest: each new guest message, by mark or by opening, with its
//     message id, which the reader keys, and whether it started a run of
//     its own (1), is a steer in another message's run (0), or has no run
//     stored yet (NULL);
//   - it: the rows and runs the reader still watches, by rowid (itemSQL).
//
// No text is read, and every lookup is a rowid range or a primary key.
func windowSQL(c int64, watched []int64) string {
	hi := "(SELECT min(" + num(c) + " + " + num(windowRows) + ", max(rowid)) FROM " + tMessages + ")"
	in := "m.rowid > " + num(c) + " AND m.rowid <= " + hi
	var b strings.Builder
	b.WriteString("BEGIN;\n")
	b.WriteString("SELECT " + tag("cur") + ", " + hx("(SELECT max(rowid) FROM "+tMessages+")") + ", " +
		hx("(SELECT message_id FROM "+tMessages+" WHERE rowid = "+num(c)+")") + ", " +
		hx("(SELECT created_at FROM "+tMessages+" WHERE rowid = "+num(c)+")") + ";\n")
	b.WriteString("SELECT " + tag("end") + ", " + hx("rowid") + ", " + hx("message_id") + ", " + hx("created_at") +
		" FROM " + tMessages + " WHERE rowid = " + hi + ";\n")
	b.WriteString("SELECT " + tag("fold") + ", " + hx("m.rowid") + ", " + hx("m.created_at") + ", " +
		hx("json_extract(t.payload_json, '$.worktreePath')") + ", " + hx("p.workspace_root") +
		" FROM " + tMessages + " AS m" + joinFolder +
		" WHERE " + in + " AND " + isUser + " AND " + notGuest + " AND " + byPerson +
		" ORDER BY m.rowid;\n")
	b.WriteString("SELECT " + tag("guest") + ", " + hx("m.rowid") + ", " + hx("m.created_at") + ", " +
		hx("json_extract(r.payload_json, '$.userMessageId') = m.message_id") + ", " + hx("m.message_id") +
		" FROM " + tMessages + " AS m LEFT JOIN " + tRuns + " AS r ON r.run_id = m.run_id" +
		" WHERE " + in + " AND " + isUser + " AND " + isGuest +
		" ORDER BY m.rowid;\n")
	if len(watched) > 0 {
		b.WriteString(itemSQL(watched))
	}
	b.WriteString("COMMIT;\n")
	return b.String()
}

// itemSQL reads the rows the reader watches, by rowid: the row as it is now
// (its message id, run, stamp and updated_at, which the re-check compares
// with what the reader saw first), its run's status, start and end and
// whether the run still names the message, and the Claude Code sessions the
// run went to: each attempt's nativeThreadId, and the run's provider
// thread's current nativeThreadRef.nativeId for a run whose attempts name
// none yet. One row per attempt, in attempt order, or one with no attempt.
func itemSQL(rowids []int64) string {
	return "SELECT " + tag("it") + ", " + hx("m.rowid") + ", " + hx("m.message_id") + ", " + hx("m.run_id") + ", " +
		hx("m.created_at") + ", " + hx("m.updated_at") + ", " +
		hx("r.status") + ", " + hx("json_extract(r.payload_json, '$.userMessageId') = m.message_id") + ", " +
		hx("json_extract(r.payload_json, '$.startedAt')") + ", " + hx("r.completed_at") + ", " +
		hx("rpt.driver") + ", " + hx("json_extract(rpt.payload_json, '$.nativeThreadRef.nativeId')") + ", " +
		hx("a.attempt_ordinal") + ", " + hx("a.status") + ", " + hx("json_extract(a.payload_json, '$.nativeThreadId')") + ", " +
		hx("apt.driver") +
		" FROM " + tMessages + " AS m" +
		" LEFT JOIN " + tRuns + " AS r ON r.run_id = m.run_id" +
		" LEFT JOIN " + tProviderThreads + " AS rpt ON rpt.provider_thread_id = r.provider_thread_id" +
		" LEFT JOIN " + tAttempts + " AS a ON a.run_id = r.run_id" +
		" LEFT JOIN " + tProviderThreads + " AS apt ON apt.provider_thread_id = a.provider_thread_id" +
		" WHERE m.rowid IN (" + ints(rowids) + ")" +
		" ORDER BY m.rowid, a.attempt_ordinal;\n"
}

// decideSQL is the decision (DESIGN 5.1): what decides each new row of
// (lo, hi] and each held row of held, with the guest test, T3's test for a
// message a person sent from a client and the folder test in the WHERE
// clause, so it reads nothing of a guest's message or of a folder pairtime
// doesn't track. It reads no text: the stamp and updated_at, the source, the
// marks, whether the run m.run_id names has the message as its
// userMessageId and was stamped with it, whether T3 means to turn the run
// into a steer, its provider thread's driver, and the folder.
func decideSQL(lo, hi int64, held []int64, folders []string) string {
	sel := "SELECT " + tag("row") + ", " + hx("m.rowid") + ", " + hx("m.message_id") + ", " + hx("m.thread_id") + ", " +
		hx("m.run_id") + ", " + hx("m.created_at") + ", " + hx("m.updated_at") + ", " +
		hx("json_extract(m.payload_json, '$.creationSource')") + ", " + hx(marks) + ", " +
		hx("r.run_id IS NOT NULL") + ", " + hx("r.requested_at = m.created_at") + ", " +
		hx("json_type(r.payload_json, '$.steerWhenReadyRunId') IS NOT NULL") + ", " +
		hx("pt.driver") + ", " + hx(folderOf) +
		" FROM " + tMessages + " AS m" + joinFolder +
		" LEFT JOIN " + tRuns + " AS r ON r.run_id = m.run_id AND json_extract(r.payload_json, '$.userMessageId') = m.message_id" +
		" LEFT JOIN " + tProviderThreads + " AS pt ON pt.provider_thread_id = r.provider_thread_id" +
		" WHERE "
	tests := " AND " + isUser + " AND " + notGuest + " AND " + byPerson + " AND " + folderOf + " IN (" + lits(folders) + ")" +
		" ORDER BY m.rowid;\n"
	var b strings.Builder
	if hi > lo {
		b.WriteString(sel + "m.rowid > " + num(lo) + " AND m.rowid <= " + num(hi) + tests)
	}
	if len(held) > 0 {
		b.WriteString(sel + "m.rowid IN (" + ints(held) + ")" + tests)
	}
	return b.String()
}

// textReq is one row whose text the reader measures: the exact version it
// decided, and its folder.
type textReq struct {
	id               string
	created, updated string
	folder           string
}

// textSQL reads a row's text by primary key, to measure and class it. It
// repeats the decision's guest, author, source and folder tests and pins
// the version the decision saw, so a row edited or rewritten between the
// two reads gives nothing. It reads at most maxTextChars characters, and the
// text's length.
func textSQL(q textReq) string {
	text := "json_extract(m.payload_json, '$.text')"
	return "SELECT " + tag("txt") + ", " + hx("m.rowid") + ", " + hx("substr("+text+", 1, "+num(maxTextChars)+")") + ", " +
		hx("length("+text+")") +
		" FROM " + tMessages + " AS m" + joinFolder +
		" WHERE m.message_id = " + lit(q.id) + " AND m.created_at = " + lit(q.created) + " AND m.updated_at = " + lit(q.updated) +
		" AND " + isUser + " AND " + notGuest + " AND " + byPerson + " AND " + folderOf + " = " + lit(q.folder) + ";\n"
}

// resyncSQL finds where the cursor goes once the row at it no longer holds
// the message it held, or the newest rowid is below it: T3's VACUUM, which
// can renumber rows, a rollback that copied an older store over the live
// one, or another store. It walks the (thread_id, created_at, message_id)
// index thread by thread, seeking each thread's rows stamped after floor,
// so nothing is scanned, and INDEXED BY makes it fail rather than scan if
// T3 drops the index. The cursor goes just before the oldest such row, or
// to the newest row when there is none.
func resyncSQL(floor string) string {
	by := " INDEXED BY " + iThreadCreated
	cur := "(SELECT v FROM cur)"
	return "WITH RECURSIVE th(thread_id) AS (" +
		"SELECT (SELECT min(thread_id) FROM " + tMessages + by + ")" +
		" UNION ALL SELECT (SELECT min(x.thread_id) FROM " + tMessages + " AS x" + by + " WHERE x.thread_id > th.thread_id)" +
		" FROM th WHERE th.thread_id IS NOT NULL)," +
		" lo(v) AS (SELECT min(m.rowid) FROM th JOIN " + tMessages + " AS m" + by +
		" ON m.thread_id = th.thread_id AND m.created_at > " + lit(floor) + " WHERE th.thread_id IS NOT NULL)," +
		" cur(v) AS (SELECT COALESCE((SELECT v FROM lo) - 1, (SELECT max(rowid) FROM " + tMessages + "), 0))" +
		" SELECT " + tag("at") + ", " + hx(cur) + ", " +
		hx("(SELECT message_id FROM "+tMessages+" WHERE rowid = "+cur+")") + ", " +
		hx("(SELECT created_at FROM "+tMessages+" WHERE rowid = "+cur+")") + ";\n"
}
