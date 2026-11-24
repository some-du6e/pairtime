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
// since a read-only open makes them when they don't. A read starts no
// sqlite3 while those files' sizes and times say T3 has written nothing
// since the last one, unless a row waits for its folder or a prompt asks.
// Every query is a rowid range or an index lookup, and none touches T3's
// search table. T3 runs, for the agent's cadence, presence and doctor, when
// the agent's process scan finds its server (ServerIn) and the files are
// there (Status.Running); the files alone don't say so, since a T3 killed
// while it held them leaves them behind.
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
//     RELAY_SEEN_LAG_S; and each new guest message, without its text. The
//     resolver hears of no row read more than RELAY_SEEN_LAG_S after its
//     stamp, read again by a resync, holding a message the reader watches,
//     or whose run, its own or the running turn it was steered into, T3
//     runs on another harness than Claude Code. Such a row counts as the
//     decision would count it, whatever its folder.
//  2. The decision, with the guest test (T3's mark, or a text that opens as
//     T3 opens a guest's, past white space and an Ultrathink: line), T3's
//     own test for a message a person sent from a client (createdBy user,
//     creationSource web or mobile) and the folder test in its WHERE
//     clause, reading no text. In Go it then needs none of T3's marks of a
//     message nobody typed, a run of its own, a deciding read at most
//     RELAY_SEEN_LAG_S after the stamp and at most 2 s before it, an
//     updated_at no later than the stamp, and Claude Code as the thread's
//     provider. scheduledTaskId makes the record declared cron,
//     senderThreadId or linkedSender cross_session.
//  3. The text of each row left that declares none, by primary key, which
//     is measured (its typed length and the go-ahead test, without T3's
//     machine strings) and dropped; a cron or cross_session record reads no
//     text and gets length 0. And the Claude Code sessions each run went
//     to.
//
// # Pairing
//
// The reader watches each recorded message's row until RELAY_MATCH_S and a
// margin have passed since its stamp and its run's end can bound no prompt
// it remembers, a day after the stamp at most while no read has seen the run
// end, and reads it again on every read until its run has delivered a
// prompt and a read has seen the run end: a row that changed after the
// first read witnesses nothing, and the run's end bounds the prompts the
// session's next run may take, however long the run was. It watches each
// guest's message, by mark or by opening, for a day whatever T3 does to
// it, in state.json too: every run of its own T3 gives the row, the first
// and each one after a declined steer, delivers a guest's prompt. Claude
// Code takes a steer as a prompt of its own in the running turn's session,
// so a running turn of another message that the owner steers the row into
// is a Steer, and the agent marks guest every prompt of its sessions from
// the later of the row's stamp and the turn's start to its end that no run
// claimed (Reader.Steers). A resync reads again from the oldest watched
// row's stamp, and finds each watched row by its message's key. The agent
// asks about each prompt of a session T3's server started that a hook or a
// log reader saw, once per turn (Pair), before it keeps the prompt's text
// (internal/agent's apps.go). A run pairs with the first prompt it was
// asked about of a session the run went to, from 2 s before the run's start
// to its end, but none stamped at or before the end of an earlier watched
// run of that session; a message's, within RELAY_MATCH_S of its stamp too.
// Guests' runs pair first. The record that comes second names the other: a
// record in Pair's own batch names the prompt, and a prompt pairing with an
// earlier record names it (Pairing.Relay), unless the agent marks it guest
// for its opening (Prompt.Guest); a pairing found once both are written
// names nothing. A pairing counts once a record names the other
// (Counts.Paired). A prompt a guest's run delivered is a guest's
// (Pairing.Guest).
//
// # Privacy
//
// The text is read only after the WHERE clause has checked that the row
// isn't a guest's and that its folder is one pairtime tracks and doesn't
// ignore, only to measure it, and nothing of it is kept, logged or returned.
// pairtime's resolver, which starts watching a repo it hasn't seen, hears
// of no row read too late, read again by a resync or holding a message the
// reader watches, nor of one whose run, its own or the running turn it was
// steered into, T3 runs on another harness. It still hears of the folder
// of a row the decision turns away for T3's marks, for having no run of
// its own, for an edit or for a stamp ahead of the read.
// T3's thread, run and message ids leave the reader only as keys under
// K_session. Errors name no path, and drop every literal of the SQL.
package t3code
