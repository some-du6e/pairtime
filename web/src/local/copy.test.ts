// SPDX-License-Identifier: AGPL-3.0-or-later

// The dashboard's words held to the same rules as the CLI's: en.json's,
// which internal/coach/text's tests check in Go, and the ones only the
// dashboard says, in copy.ts. The rules, and the scolding words, which
// come from internal/coach/text/testdata/cases.json as text_test.go's do,
// are tests/support/copy-rules.ts's.

import { readdirSync, readFileSync } from 'node:fs';
import { describe, expect, test } from 'vitest';
import { parse } from 'yaml';
import en from '../../../internal/coach/text/en.json';
import type { Focus } from './api.ts';
import { allSentences, focusWords, levelWhy, pendingWords } from './copy.ts';
import * as text from './text.ts';
import { rules, scolding, scolds } from '../../tests/support/copy-rules.ts';

interface Spec {
	components: { schemas: Record<string, { properties?: Record<string, unknown> }> };
}

const spec = parse(readFileSync(new URL('../../../docs/api/local.openapi.yaml', import.meta.url), 'utf8')) as Spec;

function names(schema: string): string[] {
	return Object.keys(spec.components.schemas[schema]?.properties ?? {});
}

describe("the scolding matcher finds what text_test.go's finds, in TestScolds' cases", () => {
	for (const c of scolding.cases) {
		test(c.name, () => {
			expect(scolds(c.s)).toEqual(c.want);
		});
	}
});

describe("the dashboard's words keep the CLI's rules", () => {
	for (const s of [...allSentences(), ...text.entries().map((e) => e.s)]) {
		// Long enough that the two focus sentences get names of their own.
		const shown = s.length > 64 ? `${s.slice(0, 64)}…` : s;
		for (const r of rules) {
			test(`"${shown}" ${r.name}`, () => {
				expect(r.breaks(s)).toEqual([]);
			});
		}
	}
});

describe('the words cover the spec', () => {
	const cases: { name: string; covered: (code: string) => boolean; schema: string }[] = [
		// Some words are their code, as "testing" is, so the catalog's keys decide.
		{ name: 'every activity kind has words in en.json', covered: (k) => Object.hasOwn(en.kinds, k), schema: 'ByKind' },
		{ name: 'every harness has a name in en.json', covered: (h) => Object.hasOwn(en.agents, h), schema: 'AgentOnlyByHarness' },
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

const desktopHides = "This desktop doesn't tell pairtime which window is in front.";
const desktopHidesLevelC =
	"This desktop doesn't tell pairtime which window is in front, so your time here counts only on your own dashboard, not on your profile, build days or most programs.";
const logInAgain = "Log out and back in once to turn on pairtime's GNOME extension.";
const installAgain = "Run pairtime install again and keep pairtime's GNOME extension.";

/** What the Today page says while /v1/status's focus.known is false (D-focus-unknown, D-design-5.9). */
const focusCases: { name: string; focus: Focus; want: string[] }[] = [
	{
		name: "until level_c is true, it says only that this desktop doesn't tell pairtime which window is in front (D-design-5.9)",
		focus: { known: false },
		want: [desktopHides],
	},
	{ name: 'level_c false says no more than a level_c left out', focus: { known: false, level_c: false }, want: [desktopHides] },
	{
		name: 'once level_c is true, it adds where that time counts, word for word as D-focus-unknown decided',
		focus: { known: false, level_c: true },
		want: [desktopHidesLevelC],
	},
	{
		name: 'on GNOME before the extension runs, it says to log out and back in',
		focus: { known: false, gnome_extension: 'needs_login' },
		want: [desktopHides, logInAgain],
	},
	{
		name: 'on GNOME with the extension left out, it says to install again',
		focus: { known: false, gnome_extension: 'unticked' },
		want: [desktopHides, installAgain],
	},
	{
		name: 'with level_c true, the log-in line follows the sentence that says where the time counts',
		focus: { known: false, level_c: true, gnome_extension: 'needs_login' },
		want: [desktopHidesLevelC, logInAgain],
	},
	{
		name: 'with level_c true, the install line follows the sentence that says where the time counts',
		focus: { known: false, level_c: true, gnome_extension: 'unticked' },
		want: [desktopHidesLevelC, installAgain],
	},
];

describe("the focus sentence claims level C only once it's true", () => {
	for (const c of focusCases) {
		test(c.name, () => {
			expect(focusWords(c.focus)).toEqual(c.want);
		});
	}
});

describe('the words say what is true', () => {
	const cases: { name: string; check: () => void }[] = [
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
		{
			name: "until WP-SC8 credits such time at level C, level C's reasons leave out a desktop that hides which window is in front (D-design-5.9)",
			check: () => expect(levelWhy.C).not.toMatch(/window is in front|which window/),
		},
		{
			name: 'allSentences() holds every sentence the pages take from copy.ts, so the rules above read each one',
			check: () => {
				const all = new Set(allSentences());
				const shown = [...Object.values(levelWhy), pendingWords, ...focusCases.flatMap((c) => focusWords(c.focus))];
				expect(shown.filter((s) => !all.has(s))).toEqual([]);
			},
		},
	];
	for (const c of cases) {
		test(c.name, c.check);
	}
});
