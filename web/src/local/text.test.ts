// SPDX-License-Identifier: AGPL-3.0-or-later

// text.ts against internal/coach/text's own test cases (text_test.go), so
// the dashboard spells time, names agents and rounds shares as the CLI
// does.

import { describe, expect, test } from 'vitest';
import * as text from './text.ts';

describe('duration, as text.Duration', () => {
	const cases: [string, number, string][] = [
		['nothing is 0m', 0, '0m'],
		['less than a second is 0m', 0.5, '0m'],
		['a negative number is 0m', -30, '0m'],
		['seconds alone below a minute', 45, '45s'],
		['a half second rounds down', 45.5, '45s'],
		['minutes and seconds below an hour', 265, '4m 25s'],
		['whole minutes drop the seconds', 720, '12m'],
		['hours and minutes from an hour up, seconds dropped', 4339, '1h 12m'],
		['whole hours drop the minutes', 7200, '2h'],
		['an hour and a few seconds is the hour', 3605, '1h'],
		['59 minutes 59 seconds stays below the hour', 3599, '59m 59s'],
		["SM 6.4's agent-only night", 23180, '6h 26m'],
		["a huge number doesn't overflow", 1e300, '277777777h 46m'],
		['NaN is 0m', Number.NaN, '0m'],
	];
	for (const [name, s, want] of cases) {
		test(name, () => {
			expect(text.duration(s)).toBe(want);
		});
	}
});

describe('clock, as text.Clock', () => {
	const claude = { name: 'Claude', s: 9600 };
	const cases: [string, number, boolean, text.AgentClock[], string][] = [
		["your time then the agent's, as DESIGN 3.8 writes it", 4320, false, [claude], 'you 1h 12m · Claude 2h 40m'],
		['no agent time shows your time alone', 265, false, [], 'you 4m 25s'],
		['an agent with no time is left out', 265, false, [{ name: 'Cursor', s: 0.5 }], 'you 4m 25s'],
		['two agents keep their order', 60, false, [claude, { name: 'Codex', s: 300 }], 'you 1m · Claude 2h 40m · Codex 5m'],
		["calibrating hides your time and keeps the agent's", 4320, true, [claude], 'you calibrating · Claude 2h 40m'],
		['nothing yet is 0m', 0, false, [], 'you 0m'],
	];
	for (const [name, you, calibrating, agents, want] of cases) {
		test(name, () => {
			const got = text.clock(you, calibrating, agents);
			expect(got).toBe(want);
			expect(got).not.toMatch(/[%/]/);
		});
	}
});

describe("the catalog's words, as text_test.go's TestWords", () => {
	const cases: [string, () => string, string][] = [
		["claude_code's agent is Claude", () => text.agent('claude_code'), 'Claude'],
		["a harness en.json doesn't know is your agent", () => text.agent('zed'), 'your agent'],
		['reading names the agent', () => text.kind('reading_output', 'Claude'), "reading Claude's answers"],
		['an unknown kind shows its code', () => text.kind('juggling', 'Claude'), 'juggling'],
		['agent-only time names the agent', () => text.agentTime('Cursor'), 'Cursor, working on its own'],
		['X.absent is away (D5)', () => text.other('X.absent', 'Claude'), 'away'],
		['X.noprog is on hold (D5)', () => text.other('X.noprog', 'Claude'), 'on hold: these come back when you fix it'],
		['the rewrite loop names the agent', () => text.other('X.rewrite_loop', 'Codex'), 'Codex rewriting the same lines'],
		["X.agent_only isn't other time", () => text.other('X.agent_only', 'Claude'), ''],
		['level C says where it counts', () => text.level('C'), 'this counts only on your own dashboard'],
		[
			'a phrase fills its placeholders',
			() => text.phrase('session_span', { day: 'Tuesday, November 24', start: '07:40', end: '07:45' }),
			'Tuesday, November 24, 07:40 to 07:45',
		],
		["a value with braces isn't filled again", () => text.phrase('session_last', { project: '{time}' }), 'Your last session: {time}'],
		['a placeholder with no value stays', () => text.fill('you {time}'), 'you {time}'],
		['Sentence capitalizes the first letter', () => text.sentence('your agent, working on its own'), 'Your agent, working on its own'],
		['Sentence leaves an empty string', () => text.sentence(''), ''],
		['Day heads a day', () => text.day('2026-11-24'), 'Tuesday, November 24'],
		["a date that doesn't parse stays as it is", () => text.day('2026-02-30'), '2026-02-30'],
		['X.agent_only and X.automated are the agent\'s time', () => String(text.isAgentTime('X.agent_only') && text.isAgentTime('X.automated')), 'true'],
		['a share that rounds to 0 is <1%, as recap prints it', () => text.share(0), '<1%'],
	];
	for (const [name, got, want] of cases) {
		test(name, () => {
			expect(got()).toBe(want);
		});
	}
});

describe('percentages, as text.Percentages', () => {
	const cases: [string, number[], number[]][] = [
		['one part is 100', [265], [100]],
		['thirds give the extra point to the first', [1, 1, 1], [34, 33, 33]],
		['the largest remainder gets the extra point', [1, 2, 3.5], [15, 31, 54]],
		['a zero part stays 0 and the rest add up to 100', [0, 3, 1], [0, 75, 25]],
		['a tiny part can round to 0', [1, 999], [0, 100]],
		['nothing positive is all zeros', [0, -1], [0, 0]],
		['no parts is no percentages', [], []],
		["SM 6.3's afternoon by level", [11640, 480, 300], [94, 4, 2]],
	];
	for (const [name, parts, want] of cases) {
		test(name, () => {
			const got = text.percentages(parts);
			expect(got).toEqual(want);
			const sum = got.reduce((a, b) => a + b, 0);
			expect(sum === 100 || sum === 0).toBe(true);
		});
	}
});

describe('the session span, as recap prints it', () => {
	// In UTC, as web.yml's runners and the specs run (vitest.config.ts).
	const cases: [string, Date, Date, boolean, string][] = [
		['a session on one day gives its day, start and end', new Date('2026-11-24T07:40:00Z'), new Date('2026-11-24T07:45:00Z'), false, 'Tuesday, November 24, 07:40 to 07:45'],
		[
			'a session past midnight names the day it ended too',
			new Date('2026-11-24T23:50:00Z'),
			new Date('2026-11-25T00:20:00Z'),
			false,
			'Tuesday, November 24, 23:50 to Wednesday, November 25, 00:20',
		],
		['a session still going says since when', new Date('2026-11-24T07:40:00Z'), new Date('2026-11-24T07:45:00Z'), true, 'Tuesday, November 24, since 07:40'],
	];
	for (const [name, start, end, open, want] of cases) {
		test(name, () => {
			expect(text.sessionSpan(start, end, open)).toBe(want);
		});
	}
});
