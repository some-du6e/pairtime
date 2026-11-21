// SPDX-License-Identifier: AGPL-3.0-or-later

// What pairtime keeps on this computer, and for how long, as the Privacy
// page lists it. docs/retention.json is the one source for those periods
// (DESIGN 4.4; AGENTS.md, Retention): pairtimed's notice at /privacy shows
// the same entries, and its retention jobs run them. So the dashboard
// imports the file, as text.ts imports en.json, and writes no period of
// its own: each entry whose where is "device" becomes a row, in the file's
// order, its kept text filled in by internal/server/retention's rule
// (Policy.Kept), so both notices say each period in the same words.
// tools/ci/npm_notices.py counts the file as pairtime's own code, and
// web.yml runs when it changes.
//
// The one row the file lacks is the inbox's (DESIGN 3.5). retention.json
// is DESIGN 4.4's table, which pairtimed's public notice shows to
// everyone, and the inbox's period comes from the agent's code rather
// than from that table: a hook call made while the agent wasn't running,
// or one it didn't take within ipc.HookTimeout, waits in inbox/ as plain
// text until a drain has read it, and a file of them that drains can't
// read is set aside and deleted inbox.KeepDays later
// (internal/agent/hookpath, internal/agent/inbox). A spooled call is the
// hook's whole input: a prompt, the agent's last reply in its Stop, and in
// a tool's call the command with its output, or the file it edited.

import retention from '../../../docs/retention.json';

/** One row of Privacy's table. */
export interface KeptRow {
	/** The entry's id in retention.json, or "device.inbox" for the inbox's row. */
	id: string;
	/** What pairtime keeps. */
	data: string;
	/** For how long, in words. */
	kept: string;
}

/** As much of a retention.json entry as the dashboard reads. */
interface Entry {
	id: string;
	where: string;
	data: string;
	/** For how long, with {days} for days and {window} for the spot-check window. */
	kept: string;
	days?: number;
}

/** retention.json's spot_check_window. */
interface SpotCheckWindow {
	months: number;
	/** Set while Hack Club hasn't chosen the window, and the months are a default. */
	provisional?: string;
}

/** n days in words: "30 days", "1 day" (retention.Days). */
function days(n: number): string {
	return n === 1 ? '1 day' : `${n} days`;
}

/**
 * The spot-check window in words: "12 months after the decision", with
 * ", a default until Hack Club sets the window" while it is provisional
 * (retention.Window.String).
 */
function spotCheckWindow(w: SpotCheckWindow): string {
	const s = w.months === 1 ? '1 month after the decision' : `${w.months} months after the decision`;
	return w.provisional ? `${s}, a default until Hack Club sets the window` : s;
}

/**
 * An entry's kept text with each {days} and {window} filled in, in one
 * pass, as retention.Policy.Kept fills them for pairtimed's notice.
 */
export function keptText(e: Pick<Entry, 'kept' | 'days'>, w: SpotCheckWindow): string {
	return e.kept.replace(/\{(days|window)\}/g, (_placeholder, name: string) =>
		name === 'days' ? days(e.days ?? 0) : spotCheckWindow(w),
	);
}

/**
 * How many days the agent keeps a file of hook calls it set aside because
 * it couldn't read it: inbox.KeepDays in internal/agent/inbox/journal.go.
 * pages/privacy.test.ts reads that constant from the Go source and fails
 * when this one differs, so the inbox row's words change with it.
 */
export const inboxKeepDays = 7;

const entries: readonly Entry[] = retention.entries;

/** Privacy's table: retention.json's device entries in the file's order, then the inbox. */
export const keptOnDevice: readonly KeptRow[] = [
	...entries
		.filter((e) => e.where === 'device')
		.map((e) => ({ id: e.id, data: e.data, kept: keptText(e, retention.spot_check_window) })),
	{
		id: 'device.inbox',
		data: "The inbox: what your AI tool sent pairtime while the agent wasn't running or didn't answer in time. That can be your prompts, its replies, the commands it ran with their output, and the files it edited. It's plain text, not encrypted",
		kept: `Until the agent has read it. If the agent can't read a file there, it sets the file aside and deletes it ${days(inboxKeepDays)} later`,
	},
];
