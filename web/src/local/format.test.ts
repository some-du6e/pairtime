// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, test } from 'vitest';
import { addDays, agentName, dayLabel, duration, longDate, minutesWords, parseDate } from './format.ts';

describe('duration', () => {
	const cases: { name: string; s: number; want: string }[] = [
		{ name: 'nothing at all is 0m', s: 0, want: '0m' },
		{ name: 'a negative or broken number is 0m, never a minus', s: -5, want: '0m' },
		{ name: 'NaN is 0m', s: Number.NaN, want: '0m' },
		{ name: 'a few seconds are under a minute', s: 20, want: '<1m' },
		{ name: 'a half-second credit still shows', s: 0.5, want: '<1m' },
		{ name: 'a minute is 1m', s: 60, want: '1m' },
		{ name: 'seconds round to the nearest minute', s: 89, want: '1m' },
		{ name: 'half a minute rounds up', s: 90, want: '2m' },
		{ name: 'under an hour is minutes alone', s: 3120, want: '52m' },
		{ name: 'whole hours drop the minutes', s: 7200, want: '2h' },
		{ name: 'a minute short of an hour that rounds up is 1h', s: 3599, want: '1h' },
		{ name: 'hours and minutes read like a chess clock', s: 12420, want: '3h 27m' },
		{ name: 'past a day it stays in hours', s: 90_000, want: '25h' },
	];
	for (const c of cases) {
		test(c.name, () => {
			expect(duration(c.s)).toBe(c.want);
		});
	}
});

describe('minutesWords', () => {
	const cases: { name: string; s: number; want: string }[] = [
		{ name: "PENDING_WINDOW_S's 900 s is 15 minutes", s: 900, want: '15 minutes' },
		{ name: 'one minute is singular', s: 60, want: '1 minute' },
		{ name: 'nothing is 0 minutes', s: 0, want: '0 minutes' },
	];
	for (const c of cases) {
		test(c.name, () => {
			expect(minutesWords(c.s)).toBe(c.want);
		});
	}
});

describe('dates', () => {
	const cases: { name: string; check: () => void }[] = [
		{
			name: 'a YYYY-MM-DD date parses to its UTC midnight',
			check: () => expect(parseDate('2026-11-20')?.toISOString()).toBe('2026-11-20T00:00:00.000Z'),
		},
		{ name: 'a day the month lacks is no date', check: () => expect(parseDate('2026-02-30')).toBeUndefined() },
		{ name: 'a date-time is no date', check: () => expect(parseDate('2026-11-20T10:00:00Z')).toBeUndefined() },
		{ name: 'adding days crosses month ends', check: () => expect(addDays('2026-11-20', -29)).toBe('2026-10-22') },
		{ name: 'adding days crosses year ends', check: () => expect(addDays('2026-12-31', 1)).toBe('2027-01-01') },
		{
			name: 'adding days ignores daylight saving, because dates are calendar days',
			check: () => expect(addDays('2026-11-02', -1)).toBe('2026-11-01'),
		},
		{
			name: 'the long date names the weekday, in the locale asked for',
			check: () => expect(longDate('2026-11-20', 'en-US')).toBe('Friday, November 20'),
		},
		{
			name: 'the long date is the calendar day whatever the time zone',
			check: () => expect(longDate('2026-11-16', 'en-GB')).toBe('Monday 16 November'),
		},
		{ name: "today's label is Today", check: () => expect(dayLabel('2026-11-20', '2026-11-20', 'en-US')).toBe('Today') },
		{
			name: "yesterday's label is Yesterday",
			check: () => expect(dayLabel('2026-11-19', '2026-11-20', 'en-US')).toBe('Yesterday'),
		},
		{
			name: 'an earlier day is labeled with its weekday and date',
			check: () => expect(dayLabel('2026-11-16', '2026-11-20', 'en-US')).toBe('Monday, November 16'),
		},
	];
	for (const c of cases) {
		test(c.name, c.check);
	}
});

describe('agentName', () => {
	const cases: { name: string; harness: Parameters<typeof agentName>[0]; want: string }[] = [
		{ name: "Claude Code's agent is Claude, as DESIGN 3.8's chess clock says", harness: 'claude_code', want: 'Claude' },
		{ name: "Codex's agent is Codex", harness: 'codex', want: 'Codex' },
		{ name: 'an agent pairtime has no name for is your agent', harness: 'other', want: 'Your agent' },
	];
	for (const c of cases) {
		test(c.name, () => {
			expect(agentName(c.harness)).toBe(c.want);
		});
	}
});
