// SPDX-License-Identifier: AGPL-3.0-or-later

// Numbers and names as the dashboard shows them: durations the way a chess
// clock reads, dates the way a person says them, and agents by name. Pure
// functions, so vitest checks them without a browser.

import type { Harness } from './api.ts';

/**
 * Seconds as a clock shows them, to the nearest minute: "1h 12m", "45m",
 * "2h". Under a minute is "<1m", and nothing at all is "0m".
 */
export function duration(s: number): string {
	if (!(s > 0)) {
		return '0m';
	}
	if (s < 60) {
		return '<1m';
	}
	const minutes = Math.round(s / 60);
	const h = Math.floor(minutes / 60);
	const m = minutes % 60;
	if (h === 0) {
		return `${m}m`;
	}
	return m === 0 ? `${h}h` : `${h}h ${m}m`;
}

/** Whole minutes in words, for sentences: "15 minutes", "1 minute". */
export function minutesWords(s: number): string {
	const n = Math.max(0, Math.round(s / 60));
	return n === 1 ? '1 minute' : `${n} minutes`;
}

/** A YYYY-MM-DD date as UTC midnight, or undefined when it isn't one. */
export function parseDate(date: string): Date | undefined {
	const m = /^(\d{4})-(\d{2})-(\d{2})$/.exec(date);
	if (!m) {
		return undefined;
	}
	const d = new Date(Date.UTC(Number(m[1]), Number(m[2]) - 1, Number(m[3])));
	// 2026-02-30 rolls over into March; it isn't a date.
	return d.toISOString().slice(0, 10) === date ? d : undefined;
}

/** date plus n days, as YYYY-MM-DD. A date that isn't one comes back as it is. */
export function addDays(date: string, n: number): string {
	const d = parseDate(date);
	if (!d) {
		return date;
	}
	d.setUTCDate(d.getUTCDate() + n);
	return d.toISOString().slice(0, 10);
}

/**
 * The weekday and date, as the teen's browser says them: "Friday,
 * November 20" in American English. Dates are calendar days, so they are
 * written in UTC, where the parsed date sits.
 */
export function longDate(date: string, locale?: string): string {
	const d = parseDate(date);
	if (!d) {
		return date;
	}
	return new Intl.DateTimeFormat(locale, { weekday: 'long', month: 'long', day: 'numeric', timeZone: 'UTC' }).format(d);
}

/** "Today", "Yesterday", or the weekday and date, for a list of days. */
export function dayLabel(date: string, today: string, locale?: string): string {
	if (date === today) {
		return 'Today';
	}
	if (date === addDays(today, -1)) {
		return 'Yesterday';
	}
	return longDate(date, locale);
}

const agentNames: Record<Harness, string> = {
	claude_code: 'Claude',
	cursor: 'Cursor',
	copilot: 'Copilot',
	codex: 'Codex',
	gemini: 'Gemini',
	devin: 'Devin',
	cline: 'Cline',
	opencode: 'OpenCode',
	other: 'Your agent',
};

/**
 * The name an agent's clock goes by (DESIGN 3.8): "Claude" for Claude
 * Code's, as in "You 1h 12m, Claude 2h 40m". A harness the spec adds fails
 * `npm run check` here until it has a name.
 */
export function agentName(harness: Harness): string {
	return agentNames[harness] ?? 'Your agent';
}
