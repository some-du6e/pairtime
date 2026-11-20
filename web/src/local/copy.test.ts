// SPDX-License-Identifier: AGPL-3.0-or-later

// The dashboard's words held to the same rules as the CLI's: en.json's,
// which internal/coach/text's tests check in Go, and the ones only the
// dashboard says, in copy.ts.

import { readdirSync, readFileSync } from 'node:fs';
import { describe, expect, test } from 'vitest';
import { parse } from 'yaml';
import { counts, focusWords, levelWhy, pendingWords } from './copy.ts';
import * as text from './text.ts';

// text_test.go's scolding list: words that would make a view nag, shame or
// set a target (DESIGN 3.8; PA 4.3, 4.4).
const scolding = [
	'streak',
	"don't break",
	'keep it up',
	'lose',
	'lost',
	'losing',
	'miss',
	'behind',
	'lazy',
	'wast',
	'only counted',
	'should',
	'must',
	'have to',
	'need to',
	'failed',
	'goal',
	'target',
	'ratio',
	'%',
	'!',
	'uncredited',
	'not counted',
	"doesn't count",
];

/** Every sentence only the dashboard says about your time. */
const ours = [
	...counts,
	...Object.values(levelWhy),
	pendingWords,
	...focusWords({ known: false, gnome_extension: 'needs_login' }),
	...focusWords({ known: false, gnome_extension: 'unticked' }),
];

interface Spec {
	components: { schemas: Record<string, { properties?: Record<string, unknown> }> };
}

const spec = parse(readFileSync(new URL('../../../docs/api/local.openapi.yaml', import.meta.url), 'utf8')) as Spec;

function names(schema: string): string[] {
	return Object.keys(spec.components.schemas[schema]?.properties ?? {});
}

describe("the dashboard's words keep the CLI's rules", () => {
	for (const s of [...ours, ...text.entries().map((e) => e.s)]) {
		test(`"${s.slice(0, 48)}…" doesn't scold, set a target or count a streak`, () => {
			const low = s.toLowerCase();
			expect(scolding.filter((w) => low.includes(w))).toEqual([]);
		});
	}
	test('no sentence uses an em dash or a curly quote', () => {
		for (const s of ours) {
			expect(s, s).not.toMatch(/[\u2014\u2013\u2018\u2019\u201c\u201d]/);
		}
	});
});

describe('the words cover the spec', () => {
	const cases: { name: string; covered: (code: string) => boolean; schema: string }[] = [
		{ name: 'every activity kind has words in en.json', covered: (k) => text.kind(k, 'Claude') !== k, schema: 'ByKind' },
		{ name: 'every harness has a name in en.json', covered: (h) => h === 'other' || text.agent(h) !== text.agent('other'), schema: 'AgentOnlyByHarness' },
		{
			name: "every uncredited reason is the agent's time or has words in en.json",
			covered: (r) => text.isAgentTime(r) || text.other(r, 'Claude') !== '',
			schema: 'Uncredited',
		},
		{ name: 'every evidence level has words in en.json and an explanation here', covered: (l) => text.level(l) !== '' && l in levelWhy, schema: 'ByEvidence' },
	];
	for (const c of cases) {
		test(c.name, () => {
			const codes = names(c.schema);
			expect(codes.length).toBeGreaterThan(0);
			expect(codes.filter((code) => !c.covered(code))).toEqual([]);
		});
	}
});

describe('the words say what is true', () => {
	const cases: { name: string; check: () => void }[] = [
		{
			name: "D-focus-unknown's sentence is the decided one, word for word",
			check: () =>
				expect(focusWords({ known: false })).toEqual([
					"This desktop doesn't tell pairtime which window is in front, so your time here counts only on your own dashboard, not on your profile, build days or most programs.",
				]),
		},
		{
			name: 'on GNOME before the extension runs, it says to log out and back in',
			check: () =>
				expect(focusWords({ known: false, gnome_extension: 'needs_login' })[1]).toBe(
					"Log out and back in once to turn on pairtime's GNOME extension.",
				),
		},
		{
			name: 'on GNOME with the extension left out, it says to install again',
			check: () =>
				expect(focusWords({ known: false, gnome_extension: 'unticked' })[1]).toBe(
					"Run pairtime install again and keep pairtime's GNOME extension.",
				),
		},
		{
			name: "the pending words' 15 minutes are PENDING_WINDOW_S in every parameter table",
			check: () => {
				const dir = new URL('../../../pkg/params/tables/', import.meta.url);
				const files = readdirSync(dir).filter((f) => f.endsWith('.json'));
				expect(files.length).toBeGreaterThan(0);
				for (const f of files) {
					const t = JSON.parse(readFileSync(new URL(f, dir), 'utf8')) as { params: Record<string, unknown> };
					expect(t.params.PENDING_WINDOW_S, f).toBe(900);
				}
				expect(pendingWords).toContain('15 minutes');
			},
		},
		{
			name: 'level C says where its time shows and where it doesn\'t (DESIGN 6.2 D11)',
			check: () =>
				expect(levelWhy.C).toContain('shows here on your dashboard, but not on your profile, your build days or most programs'),
		},
	];
	for (const c of cases) {
		test(c.name, c.check);
	}
});
