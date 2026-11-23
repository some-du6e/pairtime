// SPDX-License-Identifier: AGPL-3.0-or-later

// Package t3code is T3 Code's reader: it reads the messages T3 Code's own
// store says a person sent, for the relay.message records SM 2.2 rule 9
// reads, and the runs a guest's message started, whose prompts keep no text
// (DESIGN 3.3's apps row, 5.1; D-relayed, in
// docs/errata/2026-11-22-relayed-prompts.md). It knows nothing of the agent:
// it returns plain values, and the agent's apps component writes the
// records and marks the prompts.
//
// # The store
//
// T3 Code (pingdotgg/t3code) keeps its state in
// <T3 home>/userdata/statev2.sqlite, a SQLite store in WAL mode that its
// server holds open while it runs. Locate finds it: T3CODE_HOME, else the
// home T3's background service sets, else <home>/.t3, never a WSL distro's
// share. The client links no SQLite (DESIGN 3.5), so the reader runs the
// sqlite3 tool (Runner): read-only, with no .sqliterc, its SQL on stdin and
// every field hex-encoded, one short process per read at low priority
// under a timeout, and only while the store's -wal and -shm files exist,
// since a read-only open makes them when they don't. Every query is a rowid
// range or an index lookup, and none touches T3's search table.
//
// # A read
//
// The schema probe runs first and again every few minutes: JSON functions,
// migration 55, the columns, the index and the payload keys the reader
// needs. Then, from the cursor in state.json (a rowid, the key of the
// message id there and its stamp; the newest row on a first run, since
// nothing is backfilled), the reader checks that the cursor still stands
// where it was, resyncing by stamp when T3's VACUUM moved it, and reads the
// next window of rows:
//
//  1. Each new user row's folder, which pairtime's own resolver sorts into
//     tracked, out, or not known yet, whose rows wait for it up to
//     RELAY_SEEN_LAG_S; and each new guest message, without its text.
//  2. The decision, with the guest test, T3's own test for a message a
//     person sent from a client (createdBy user, creationSource web or
//     mobile) and the folder test in its WHERE clause, reading no text. In
//     Go it then needs none of T3's marks of a message nobody typed, a run
//     of its own, a first read at most RELAY_SEEN_LAG_S after the stamp,
//     and an updated_at no later than the stamp. scheduledTaskId makes the
//     record declared cron, senderThreadId or linkedSender cross_session.
//  3. The text of each row left, by primary key, which is measured (its
//     typed length and the go-ahead test, without T3's machine strings) and
//     dropped; and the Claude Code sessions each run went to.
//
// # Pairing
//
// The reader watches each recorded message's row until its run delivers a
// prompt or RELAY_MATCH_S passes, and each guest's run until it delivers
// one, reading the row again on every read: a row that changed after the
// first read witnesses nothing. The agent asks about each prompt of a
// session T3's server started that a hook or a log reader saw, once per
// turn (Pair), before it keeps the prompt's text (internal/agent's
// apps.go). A run pairs with the first prompt it was asked about of a
// session the run went to, at or after the run's start and before its end;
// a message's, within RELAY_MATCH_S of its stamp too. The record that
// comes second names the other: a record in Pair's own batch names the
// prompt, and a prompt pairing with an earlier record names it
// (Pairing.Relay). A prompt a guest's run delivered is a guest's
// (Pairing.Guest).
//
// # Privacy
//
// The text is read only after the WHERE clause has checked that the row
// isn't a guest's and that its folder is one pairtime tracks and doesn't
// ignore, only to measure it, and nothing of it is kept, logged or returned.
// T3's thread, run and message ids leave the reader only as keys under
// K_session. Errors name no path, and drop every literal of the SQL.
package t3code
