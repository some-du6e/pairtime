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
// web.yml runs when it changes. The inbox's row is the file's device.inbox,
// whose period the agent's inbox.SpoolKeep keeps (D-inbox-age-cap).

import retention from '../../../docs/retention.json';

/** One row of Privacy's table. */
export interface KeptRow {
	/** The entry's id in retention.json. */
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

const entries: readonly Entry[] = retention.entries;

/** Privacy's table: retention.json's device entries in the file's order. */
export const keptOnDevice: readonly KeptRow[] = entries
	.filter((e) => e.where === 'device')
	.map((e) => ({ id: e.id, data: e.data, kept: keptText(e, retention.spot_check_window) }));
