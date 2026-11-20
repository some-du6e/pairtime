// SPDX-License-Identifier: AGPL-3.0-or-later

// The fake agent's answers are answers the real agent may give: each passes
// docs/api/local.openapi.yaml's schema, and the sums its fields promise
// hold. So a dashboard tested against them works against an agent that
// keeps to the spec, which smoke.spec.ts checks the real one does.

import { readFileSync } from 'node:fs';
import { describe, expect, test } from 'vitest';
import { parse } from 'yaml';
import type { components } from '../../src/gen/local-api.ts';
import { dayReply, fixtureDates, lastSession, status, todayFinal } from './fake-days.ts';
import { check, type Schema } from './schema.ts';

type Tally = Omit<components['schemas']['DayProject'], 'project' | 'name'>;

const spec = parse(readFileSync(new URL('../../../docs/api/local.openapi.yaml', import.meta.url), 'utf8')) as Schema;
const ref = (name: string): Schema => ({ $ref: `#/components/schemas/${name}` });

function total(m: Record<string, number | undefined>): number {
	return Object.values(m).reduce((a: number, b) => a + (b ?? 0), 0);
}

/** Every way a tally breaks a sum the spec promises. */
function sums(at: string, t: Tally): string[] {
	const problems: string[] = [];
	if (total(t.credited_by_kind) !== t.credited_s) {
		problems.push(`${at}: credited_by_kind adds up to ${total(t.credited_by_kind)}, not credited_s ${t.credited_s}`);
	}
	if (total(t.credited_by_evidence) !== t.credited_s) {
		problems.push(`${at}: credited_by_evidence adds up to ${total(t.credited_by_evidence)}, not credited_s ${t.credited_s}`);
	}
	if (t.pending_s > t.credited_s) {
		problems.push(`${at}: pending_s is more than credited_s`);
	}
	if (total(t.agent_only_by_harness) !== t.agent_only_s) {
		problems.push(`${at}: agent_only_by_harness adds up to ${total(t.agent_only_by_harness)}, not agent_only_s ${t.agent_only_s}`);
	}
	const agentCodes = (t.uncredited['X.agent_only'] ?? 0) + (t.uncredited['X.automated'] ?? 0);
	if (agentCodes > t.agent_only_s) {
		problems.push(`${at}: X.agent_only and X.automated are more than agent_only_s`);
	}
	return problems;
}

describe("the fake agent's answers keep to the spec", () => {
	const cases: { name: string; value: () => unknown; schema: string; tallies?: () => [string, Tally][] }[] = [
		{ name: 'GET /v1/status is a Status', value: status, schema: 'Status' },
		{
			name: 'GET /v1/status with focus unknown on GNOME is a Status',
			value: () => status({ focus: { known: false, gnome_extension: 'needs_login' } }),
			schema: 'Status',
		},
		...fixtureDates.map((date) => ({
			name: `GET /v1/days/${date} is a Day whose sums hold`,
			value: () => dayReply(date),
			schema: 'Day',
			tallies: () => dayReply(date).projects.map((p): [string, Tally] => [`${date} ${p.project}`, p]),
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
		{ name: 'GET /v1/sessions/last before the first session is a LastSession', value: () => lastSession({ none: true }), schema: 'LastSession' },
		{ name: 'an open session is a LastSession', value: () => lastSession({ open: true }), schema: 'LastSession' },
	];
	for (const c of cases) {
		test(c.name, () => {
			expect(check(c.value(), ref(c.schema), spec)).toEqual([]);
			for (const [at, t] of c.tallies?.() ?? []) {
				expect(sums(at, t)).toEqual([]);
			}
		});
	}
});

describe('the checks can fail', () => {
	const cases: { name: string; problems: () => string[]; want: string }[] = [
		{
			name: 'a level that leaves out the pending seconds breaks the evidence sum',
			problems: () => {
				const p = structuredClone(dayReply('today').projects[0]);
				if (!p) {
					return [];
				}
				p.credited_by_evidence = { A: (p.credited_by_evidence.A ?? 0) - p.pending_s };
				return sums('portfolio', p);
			},
			want: 'credited_by_evidence adds up to',
		},
		{
			name: "a harness the schema doesn't name fails the schema",
			problems: () => {
				const d = structuredClone(dayReply('today'));
				const p = d.projects[0];
				if (p) {
					(p.agent_only_by_harness as Record<string, number>).zed = 1;
				}
				return check(d, ref('Day'), spec);
			},
			want: 'zed',
		},
	];
	for (const c of cases) {
		test(c.name, () => {
			expect(c.problems().join('\n')).toContain(c.want);
		});
	}
});
