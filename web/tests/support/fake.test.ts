// SPDX-License-Identifier: AGPL-3.0-or-later

// The fake agent's answers are answers the real agent may give: each passes
// docs/api/local.openapi.yaml's schema, the sums its fields promise hold,
// Friday is SM 6.3's afternoon, and a shadow answer leaves out what
// D-shadow-scope hides. So a dashboard tested against them works against an
// agent that keeps to the spec, which smoke.spec.ts checks the real one
// does.

import { readFileSync } from 'node:fs';
import { describe, expect, test } from 'vitest';
import { parse } from 'yaml';
import type { components } from '../../src/gen/local-api.ts';
import {
	dayReply,
	fixtureDates,
	lastSession,
	shadowCodes,
	shadowHides,
	shadowHidesOfSession,
	status,
	todayFinal,
} from './fake-days.ts';
import { check, type Schema } from './schema.ts';

type Day = components['schemas']['Day'];
type Tally = Omit<components['schemas']['DayProject'], 'project' | 'name'>;

const spec = parse(readFileSync(new URL('../../../docs/api/local.openapi.yaml', import.meta.url), 'utf8')) as Schema;
const ref = (name: string): Schema => ({ $ref: `#/components/schemas/${name}` });

/** A map's seconds added up; a map that is left out adds up to 0. */
function total(m: Record<string, number | undefined> | undefined): number {
	return Object.values(m ?? {}).reduce((a: number, b) => a + (b ?? 0), 0);
}

/** Every way a tally breaks a sum the spec promises. A number it leaves out is 0, as the dashboard reads it. */
function sums(at: string, t: Tally): string[] {
	const problems: string[] = [];
	const credited = t.credited_s ?? 0;
	if (total(t.credited_by_kind) !== credited) {
		problems.push(`${at}: credited_by_kind adds up to ${total(t.credited_by_kind)}, not credited_s ${credited}`);
	}
	if (total(t.credited_by_evidence) !== credited) {
		problems.push(`${at}: credited_by_evidence adds up to ${total(t.credited_by_evidence)}, not credited_s ${credited}`);
	}
	if ((t.pending_s ?? 0) > credited) {
		problems.push(`${at}: pending_s is more than credited_s`);
	}
	if (total(t.agent_only_by_harness) !== t.agent_only_s) {
		problems.push(`${at}: agent_only_by_harness adds up to ${total(t.agent_only_by_harness)}, not agent_only_s ${t.agent_only_s}`);
	}
	const agentCodes = (t.uncredited['X.agent_only'] ?? 0) + (t.uncredited['X.automated'] ?? 0);
	if (agentCodes > t.agent_only_s) {
		problems.push(`${at}: X.agent_only and X.automated are more than agent_only_s`);
	}
	if ((t.on_hold_s ?? 0) > (t.uncredited['X.noprog'] ?? 0)) {
		problems.push(`${at}: on_hold_s is more than X.noprog`);
	}
	return problems;
}

/**
 * Every project of a day whose agent time doesn't fit. A project's
 * agent_only_s are seconds its turns ran that another project credited or
 * nothing credited (SM 6.3), so they are at most the other projects' credit
 * and the day's uncredited seconds together.
 */
function agentTimeFits(d: Day): string[] {
	const uncredited = d.projects.reduce((a, p) => a + total(p.uncredited), 0);
	const credited = d.projects.reduce((a, p) => a + (p.credited_s ?? 0), 0);
	return d.projects
		.filter((p) => p.agent_only_s > credited - (p.credited_s ?? 0) + uncredited)
		.map((p) => `${d.date} ${p.project}: agent_only_s ${p.agent_only_s} is more than the other projects' credit and the day's uncredited seconds`);
}

/**
 * Every way a day breaks SM 6.3's afternoon: 207 minutes credited, 93 of
 * the projects' agent time, and 33 uncredited, 4 of them X.agent_only, in
 * the 240 minutes from 13:00 to 17:00. The agents ran while Sam's credited
 * minutes went to other projects, so their 93 minutes are no part of the
 * 240.
 */
function afternoon(d: Day): string[] {
	const add = (f: (t: Tally) => number) => d.projects.reduce((a, p) => a + f(p), 0);
	const credited = add((t) => t.credited_s ?? 0);
	const agentTime = add((t) => t.agent_only_s);
	const uncredited = add((t) => total(t.uncredited));
	const agentOnly = add((t) => t.uncredited['X.agent_only'] ?? 0);
	const problems: string[] = [];
	if (credited !== 12_420) {
		problems.push(`credited_s adds up to ${credited}, not SM's 207 minutes, 12,420 s`);
	}
	if (agentTime !== 5_580) {
		problems.push(`agent_only_s adds up to ${agentTime}, not SM's 93 minutes, 5,580 s`);
	}
	if (uncredited !== 1_980) {
		problems.push(`uncredited adds up to ${uncredited}, not SM's 33 minutes, 1,980 s`);
	}
	if (agentOnly !== 240) {
		problems.push(`X.agent_only adds up to ${agentOnly}, not SM's 4 minutes, 240 s`);
	}
	if (credited + uncredited !== 14_400) {
		problems.push(`credited and uncredited add up to ${credited + uncredited} s, not the afternoon's 240 minutes, 14,400 s`);
	}
	return problems;
}

/** Every field of a shadow answer that D-shadow-scope hides. */
function hidden(at: string, value: Record<string, unknown>, fields: readonly string[]): string[] {
	const problems = fields.filter((f) => Object.hasOwn(value, f)).map((f) => `${at}: has ${f}`);
	const uncredited = value.uncredited;
	if (typeof uncredited === 'object' && uncredited !== null) {
		const kept: readonly string[] = shadowCodes;
		for (const code of Object.keys(uncredited)) {
			if (!kept.includes(code)) {
				problems.push(`${at}: has the uncredited code ${code}`);
			}
		}
	}
	return problems;
}

describe("the fake agent's answers keep to the spec", () => {
	const cases: { name: string; value: () => unknown; schema: string; tallies?: () => [string, Tally][]; fits?: () => string[] }[] = [
		{ name: 'GET /v1/status is a Status', value: status, schema: 'Status' },
		{
			name: 'GET /v1/status with focus unknown on GNOME is a Status',
			value: () => status({ focus: { known: false, gnome_extension: 'needs_login' } }),
			schema: 'Status',
		},
		{
			name: 'GET /v1/status with focus unknown once such time is level C is a Status',
			value: () => status({ focus: { known: false, level_c: true } }),
			schema: 'Status',
		},
		...fixtureDates.map((date) => ({
			name: `GET /v1/days/${date} is a Day whose sums hold`,
			value: () => dayReply(date),
			schema: 'Day',
			tallies: () => dayReply(date).projects.map((p): [string, Tally] => [`${date} ${p.project}`, p]),
		})),
		...fixtureDates.map((date) => ({
			name: `GET /v1/days/${date}: each project's agent time fits in another project's credit and the day's uncredited time`,
			value: () => dayReply(date),
			schema: 'Day',
			fits: () => agentTimeFits(dayReply(date)),
		})),
		...fixtureDates.map((date) => ({
			name: `GET /v1/days/${date} with display.shadow on is a Day whose sums hold`,
			value: () => dayReply(date, true),
			schema: 'Day',
			tallies: () => dayReply(date, true).projects.map((p): [string, Tally] => [`${date} ${p.project}`, p]),
		})),
		{ name: 'an empty day is a Day', value: () => dayReply('2026-01-01'), schema: 'Day' },
		{
			name: 'today with nothing pending is a Day whose sums hold',
			value: todayFinal,
			schema: 'Day',
			tallies: () => todayFinal().projects.map((p): [string, Tally] => [p.project, p]),
		},
		{
			name: 'GET /v1/sessions/last is a LastSession whose sums hold',
			value: lastSession,
			schema: 'LastSession',
			tallies: () => {
				const s = lastSession().session;
				return s ? [['session', s]] : [];
			},
		},
		{
			name: 'GET /v1/sessions/last with display.shadow on is a LastSession whose sums hold',
			value: () => lastSession({ shadow: true }),
			schema: 'LastSession',
			tallies: () => {
				const s = lastSession({ shadow: true }).session;
				return s ? [['session', s]] : [];
			},
		},
		{ name: 'GET /v1/sessions/last before the first session is a LastSession', value: () => lastSession({ none: true }), schema: 'LastSession' },
		{ name: 'an open session is a LastSession', value: () => lastSession({ open: true }), schema: 'LastSession' },
	];
	for (const c of cases) {
		test(c.name, () => {
			expect(check(c.value(), ref(c.schema), spec)).toEqual([]);
			for (const [at, t] of c.tallies?.() ?? []) {
				expect(sums(at, t)).toEqual([]);
			}
			expect(c.fits?.() ?? []).toEqual([]);
		});
	}
});

describe("Friday is SM 6.3's afternoon", () => {
	const friday = dayReply('today');
	const add = (f: (t: Tally) => number) => friday.projects.reduce((a, p) => a + f(p), 0);
	const cases: { name: string; got: () => number; want: number }[] = [
		{ name: "it credits SM's 207 minutes", got: () => add((t) => t.credited_s ?? 0), want: 12_420 },
		{ name: "its projects' agent time is SM's 93 minutes", got: () => add((t) => t.agent_only_s), want: 5_580 },
		{ name: "it leaves SM's 33 minutes uncredited", got: () => add((t) => total(t.uncredited)), want: 1_980 },
		{
			name: "X.agent_only is SM's 4 of those minutes, a small part of the agent time",
			got: () => add((t) => t.uncredited['X.agent_only'] ?? 0),
			want: 240,
		},
		{
			name: 'credited and uncredited time fill the 240 minutes from 13:00 to 17:00',
			got: () => add((t) => (t.credited_s ?? 0) + total(t.uncredited)),
			want: 14_400,
		},
	];
	for (const c of cases) {
		test(c.name, () => {
			expect(c.got()).toBe(c.want);
		});
	}
	test('every check of the afternoon holds at once', () => {
		expect(afternoon(friday)).toEqual([]);
	});
});

describe('a shadow answer leaves out what D-shadow-scope hides', () => {
	const cases: { name: string; problems: () => string[] }[] = [
		...fixtureDates.map((date) => ({
			name: `GET /v1/days/${date} has no credited number, no on_hold_s, and no code but the agent's and the loops`,
			problems: () =>
				dayReply(date, true).projects.flatMap((p) => hidden(`${date} ${p.project}`, p, shadowHides)),
		})),
		{
			name: 'the last session has no credited number, start, end or open, and no code but the agent\'s and the loops',
			problems: () => {
				const s = lastSession({ shadow: true }).session;
				return s ? hidden('session', s, [...shadowHides, ...shadowHidesOfSession]) : ['no session'];
			},
		},
		{
			name: 'the session still going has none of them either',
			problems: () => {
				const s = lastSession({ shadow: true, open: true }).session;
				return s ? hidden('session', s, [...shadowHides, ...shadowHidesOfSession]) : ['no session'];
			},
		},
		{
			name: 'the last session keeps its project and local date',
			problems: () => {
				const s = lastSession({ shadow: true }).session;
				return s?.project === 'p_rusty' && s.name === 'rusty-cli' && s.date === '2026-11-20' ? [] : [JSON.stringify(s)];
			},
		},
		{
			name: "Wednesday keeps its loop and the agents' time",
			problems: () => {
				const bot = dayReply('2026-11-18', true).projects.find((p) => p.project === 'p_bot');
				return bot?.uncredited['X.reroll'] === 240 && bot.agent_only_s === 600 ? [] : [JSON.stringify(bot)];
			},
		},
	];
	for (const c of cases) {
		test(c.name, () => {
			expect(c.problems()).toEqual([]);
		});
	}
});

describe('the checks can fail', () => {
	const cases: { name: string; problems: () => string[]; want: string }[] = [
		{
			name: 'a level that leaves out the pending seconds breaks the evidence sum',
			problems: () => {
				const p = dayReply('today').projects[0];
				if (!p) {
					return [];
				}
				p.credited_by_evidence = { A: (p.credited_by_evidence?.A ?? 0) - (p.pending_s ?? 0) };
				return sums('portfolio', p);
			},
			want: 'credited_by_evidence adds up to',
		},
		{
			name: "a harness the schema doesn't name fails the schema",
			problems: () => {
				const d = dayReply('today');
				const p = d.projects[0];
				if (p) {
					(p.agent_only_by_harness as Record<string, number>).zed = 1;
				}
				return check(d, ref('Day'), spec);
			},
			want: 'zed',
		},
		{
			name: 'reading X.agent_only as all of agent_only_s breaks the 240-minute afternoon',
			problems: () => {
				const d = dayReply('today');
				for (const p of d.projects) {
					p.uncredited['X.agent_only'] = p.agent_only_s;
				}
				return afternoon(d);
			},
			want: "not the afternoon's 240 minutes",
		},
		{
			name: 'a shadow day with a credited number in it is caught',
			problems: () => {
				const p = dayReply('today', true).projects[0];
				return p ? hidden('portfolio', { ...p, credited_s: 3120 }, shadowHides) : [];
			},
			want: 'has credited_s',
		},
		{
			name: 'a shadow day that keeps X.absent is caught',
			problems: () => {
				const p = dayReply('today', true).projects[0];
				return p ? hidden('portfolio', { ...p, uncredited: { ...p.uncredited, 'X.absent': 900 } }, shadowHides) : [];
			},
			want: 'has the uncredited code X.absent',
		},
		{
			name: "a day with one project whose agent time nothing holds is caught, as nothing else credited it",
			problems: () => {
				const d = dayReply('2026-11-10');
				const p = d.projects[0];
				if (p) {
					p.uncredited = { 'X.same_error': 120 };
				}
				return agentTimeFits(d);
			},
			want: 'p_portfolio: agent_only_s 3000 is more than',
		},
		{
			name: "a shadow day that keeps X.agent_only is caught, since with agent_only_s it says how much another project credited",
			problems: () => {
				const p = dayReply('today', true).projects[0];
				return p ? hidden('portfolio', { ...p, uncredited: { ...p.uncredited, 'X.agent_only': 120 } }, shadowHides) : [];
			},
			want: 'has the uncredited code X.agent_only',
		},
		{
			name: 'a shadow session with its start is caught',
			problems: () => {
				const s = lastSession({ shadow: true }).session;
				return s ? hidden('session', { ...s, start: '2026-11-20T14:05:00Z' }, shadowHidesOfSession) : [];
			},
			want: 'has start',
		},
		{
			name: 'on_hold_s past X.noprog breaks the on-hold sum',
			problems: () => {
				const p = dayReply('2026-11-19').projects.find((q) => q.project === 'p_homework');
				return p ? sums('homework-helper', { ...p, on_hold_s: 200 }) : [];
			},
			want: 'on_hold_s is more than X.noprog',
		},
	];
	for (const c of cases) {
		test(c.name, () => {
			expect(c.problems().join('\n')).toContain(c.want);
		});
	}
});
