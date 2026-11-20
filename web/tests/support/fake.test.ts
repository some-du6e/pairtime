// SPDX-License-Identifier: AGPL-3.0-or-later

// The fake agent's answers are answers the real agent may give: each passes
// docs/api/local.openapi.yaml's schema, and the sums the spec promises
// hold. So a dashboard tested against them works against an agent that
// keeps to the spec, which smoke.spec.ts checks the real one does.

import { readFileSync } from 'node:fs';
import { describe, expect, test } from 'vitest';
import { parse } from 'yaml';
import type { components } from '../../src/gen/local-api.ts';
import { monthDays, status, weekDays, weekFinal } from './fake-days.ts';
import { check, type Schema } from './schema.ts';

type Days = components['schemas']['Days'];

const spec = parse(readFileSync(new URL('../../../docs/api/local.openapi.yaml', import.meta.url), 'utf8')) as Schema;
const ref = (name: string): Schema => ({ $ref: `#/components/schemas/${name}` });

function dates(from: string, to: string): string[] {
	const out: string[] = [];
	for (let t = Date.parse(`${from}T00:00:00Z`); t <= Date.parse(`${to}T00:00:00Z`); t += 86_400_000) {
		out.push(new Date(t).toISOString().slice(0, 10));
	}
	return out;
}

/** Every way days breaks a sum the spec promises. */
function sums(days: Days): string[] {
	const problems: string[] = [];
	const named = new Set(days.projects.map((p) => p.key));
	if (JSON.stringify(days.days.map((d) => d.date)) !== JSON.stringify(dates(days.from, days.to))) {
		problems.push('days isn\'t one entry per date from `from` to `to`');
	}
	for (const d of days.days) {
		const fromProjects: Record<string, number> = {};
		for (const p of d.projects) {
			const at = `${d.date} ${p.project}`;
			if (!named.has(p.project)) {
				problems.push(`${at}: the project isn't in projects`);
			}
			const levels = p.by_evidence.A + p.by_evidence.B + p.by_evidence.C;
			if (levels + p.pending_s !== p.credited_s) {
				problems.push(`${at}: by_evidence and pending_s add up to ${levels + p.pending_s}, not credited_s ${p.credited_s}`);
			}
			const kinds = Object.values(p.by_kind).reduce((a: number, b) => a + (b ?? 0), 0);
			if (kinds !== p.credited_s) {
				problems.push(`${at}: by_kind adds up to ${kinds}, not credited_s ${p.credited_s}`);
			}
			for (const a of p.agents) {
				if (a.running_s > p.agent_running_s) {
					problems.push(`${at}: ${a.harness} ran longer than agent_running_s`);
				}
			}
			for (const [reason, s] of Object.entries(p.uncredited)) {
				fromProjects[reason] = (fromProjects[reason] ?? 0) + (s ?? 0);
			}
		}
		for (const [reason, s] of Object.entries(fromProjects)) {
			const all = (d.uncredited as Record<string, number | undefined>)[reason] ?? 0;
			if (s > all) {
				problems.push(`${d.date}: the projects' ${reason} is more than the day's`);
			}
		}
	}
	return problems;
}

describe("the fake agent's answers keep to the spec", () => {
	const cases: { name: string; value: () => unknown; schema: string; days?: () => Days }[] = [
		{ name: 'GET /v1/status is a Status', value: status, schema: 'Status' },
		{
			name: 'GET /v1/status with focus unknown on GNOME is a Status',
			value: () => status({ focus: { known: false, gnome_extension: 'needs_login' } }),
			schema: 'Status',
		},
		{ name: 'this week is a Days whose sums hold', value: weekDays, schema: 'Days', days: weekDays },
		{ name: 'this week with nothing pending is a Days whose sums hold', value: weekFinal, schema: 'Days', days: weekFinal },
		{ name: 'the last 30 days is a Days whose sums hold', value: monthDays, schema: 'Days', days: monthDays },
	];
	for (const c of cases) {
		test(c.name, () => {
			expect(check(c.value(), ref(c.schema), spec)).toEqual([]);
			if (c.days) {
				expect(sums(c.days())).toEqual([]);
			}
		});
	}
});

describe('the sums check can fail', () => {
	const cases: { name: string; days: () => Days; problem: string }[] = [
		{
			name: 'pending seconds that are also counted in a level',
			days: () => {
				// A copy: the fixtures' objects are shared.
				const d = structuredClone(weekDays());
				const p = d.days[4]?.projects[0];
				if (p) {
					p.by_evidence = { ...p.by_evidence, A: p.by_evidence.A + p.pending_s };
				}
				return d;
			},
			problem: 'by_evidence and pending_s add up to',
		},
		{
			name: 'a day missing from the range',
			days: () => ({ ...weekDays(), days: weekDays().days.slice(1) }),
			problem: "days isn't one entry per date",
		},
	];
	for (const c of cases) {
		test(c.name, () => {
			expect(sums(c.days()).join('\n')).toContain(c.problem);
		});
	}
});
