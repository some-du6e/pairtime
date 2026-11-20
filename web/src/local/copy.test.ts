// SPDX-License-Identifier: AGPL-3.0-or-later

// The dashboard's words held to the spec and to DESIGN 3.8's copy rules.

import { readFileSync } from 'node:fs';
import { describe, expect, test } from 'vitest';
import { parse } from 'yaml';
import { agentOnly, calibrating, counts, focusWords, kinds, levels, pendingWords, reasons, shadowWords } from './copy.ts';
import { agentName } from './format.ts';

interface Spec {
	components: { schemas: Record<string, { properties?: Record<string, unknown>; enum?: string[] }> };
}

const spec = parse(readFileSync(new URL('../../../docs/api/local.openapi.yaml', import.meta.url), 'utf8')) as Spec;
const schemas = spec.components.schemas;

function names(schema: string): string[] {
	const s = schemas[schema];
	return s?.enum ?? Object.keys(s?.properties ?? {});
}

/** Every sentence the dashboard can say about your time. */
const everything = [
	...counts,
	...levels.flatMap((l) => [l.name, l.why]),
	pendingWords('15 minutes'),
	shadowWords,
	calibrating,
	...focusWords({ known: false, gnome_extension: 'needs_login' }),
	...focusWords({ known: false, gnome_extension: 'unticked' }),
	...kinds.flatMap((k) => [k.label, k.about]),
	...reasons.flatMap((r) => [r.label, r.about]),
	agentOnly('Claude'),
];

describe('the words cover the spec', () => {
	const cases: { name: string; got: () => string[]; want: () => string[] }[] = [
		{ name: 'every activity kind has words, in the spec\'s order', got: () => kinds.map((k) => k.kind), want: () => names('ByKind') },
		{
			name: 'every uncredited reason has words, in the spec\'s order',
			got: () => reasons.map((r) => r.reason),
			want: () => names('Uncredited'),
		},
		{ name: 'every evidence level has words, in the spec\'s order', got: () => levels.map((l) => l.level), want: () => names('ByEvidence') },
		{
			name: 'every harness has a name for its clock that is more than its code',
			got: () => names('Harness').filter((h) => agentName(h as Parameters<typeof agentName>[0]) !== h),
			want: () => names('Harness'),
		},
	];
	for (const c of cases) {
		test(c.name, () => {
			expect(c.want().length).toBeGreaterThan(0);
			expect(c.got()).toEqual(c.want());
		});
	}
});

describe("DESIGN 3.8's copy rules", () => {
	const cases: { name: string; check: () => void }[] = [
		{
			name: 'nothing talks about streaks, goals, targets, ratios or percentages',
			check: () => {
				for (const s of everything) {
					expect(s, s).not.toMatch(/streak|goal|target|ratio|percent|%|score\b|rank/i);
				}
			},
		},
		{
			name: 'nothing scolds: no "only", "failed", "lazy", "cheat", "wasted" or "should have"',
			check: () => {
				for (const s of everything) {
					expect(s, s).not.toMatch(/\bonly\s+\d|fail|lazy|cheat|wast|should have|you didn't|not enough/i);
				}
			},
		},
		{
			name: 'half-weighted time says DESIGN 3.8\'s words: "on hold: these come back when you fix it"',
			check: () => {
				const noprog = reasons.find((r) => r.reason === 'X.noprog');
				expect(noprog?.label).toBe('On hold');
				expect(noprog?.about).toContain('On hold: these come back when you fix it.');
			},
		},
		{
			name: "agent-only time is named after the agent that ran",
			check: () => {
				expect(agentOnly('Claude')).toBe('Claude working on its own');
			},
		},
		{
			name: 'every loop says what ends it, so it reads as a lesson',
			check: () => {
				for (const reason of ['X.reroll', 'X.rewrite_loop', 'X.same_error', 'X.autopilot'] as const) {
					expect(reasons.find((r) => r.reason === reason)?.about, reason).toMatch(/ends it|ends the loop|starts the count over/);
				}
			},
		},
		{
			name: 'level C says where its time counts and where it doesn\'t (DESIGN 6.2 D11)',
			check: () => {
				expect(levels.find((l) => l.level === 'C')?.why).toContain(
					'counts here on your dashboard, but not on your profile, your build days or most programs',
				);
			},
		},
		{
			name: "no sentence uses an em dash or a curly quote",
			check: () => {
				for (const s of everything) {
					expect(s, s).not.toMatch(/[\u2014\u2013\u2018\u2019\u201c\u201d]/);
				}
			},
		},
	];
	for (const c of cases) {
		test(c.name, c.check);
	}
});

describe("D-focus-unknown's sentence", () => {
	const sentence =
		"This desktop doesn't tell pairtime which window is in front, so your time here counts only on your own dashboard, not on your profile, build days or most programs.";
	const cases: { name: string; focus: Parameters<typeof focusWords>[0]; want: string[] }[] = [
		{ name: 'on a desktop that hides focus it is the decided sentence, word for word', focus: { known: false }, want: [sentence] },
		{
			name: "on GNOME before the extension runs it says to log out and back in",
			focus: { known: false, gnome_extension: 'needs_login' },
			want: [sentence, "Log out and back in once to turn on pairtime's GNOME extension."],
		},
		{
			name: 'on GNOME with the extension left out it says to install again',
			focus: { known: false, gnome_extension: 'unticked' },
			want: [sentence, "Run pairtime install again and keep pairtime's GNOME extension."],
		},
	];
	for (const c of cases) {
		test(c.name, () => {
			expect(focusWords(c.focus)).toEqual(c.want);
		});
	}
});
