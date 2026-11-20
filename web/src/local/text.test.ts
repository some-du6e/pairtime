// SPDX-License-Identifier: AGPL-3.0-or-later

// text.ts against internal/coach/text's own test cases, read from the file
// text_test.go reads them from, testdata/cases.json. So the dashboard
// spells time, names agents and rounds shares as the CLI does, and a case
// changed for Go fails here too until text.ts follows. Only the cases that
// file can't hold, and what text.ts does that the Go package doesn't, are
// written here.

import { readFileSync } from 'node:fs';
import { describe, expect, test } from 'vitest';
import en from '../../../internal/coach/text/en.json';
import * as text from './text.ts';

/** internal/coach/text/testdata/cases.json, the tables text.ts shares with text_test.go. */
interface Cases {
	duration: { name: string; s: number; want: string }[];
	clock: { name: string; you: number; level_c: number; calibrating: boolean; agents: text.AgentClock[]; want: string }[];
	percentages: { name: string; parts: number[]; want: number[] }[];
	words: { name: string; func: string; args: string[]; want: string }[];
}

const shared = JSON.parse(
	readFileSync(new URL('../../../internal/coach/text/testdata/cases.json', import.meta.url), 'utf8'),
) as Cases;

type PhraseKey = Parameters<typeof text.phrase>[0];

/** k as one of en.json's phrases, which is what text.phrase takes. */
function phraseKey(k: string): PhraseKey {
	if (!Object.hasOwn(en.phrases, k)) {
		throw new Error(`en.json has no phrase ${k}`);
	}
	return k as PhraseKey;
}

/** Pairs of name and value, as text.Fill takes them, in the record text.ts's fill and phrase take. */
function named(pairs: string[]): Record<string, string> {
	const out: Record<string, string> = {};
	for (let i = 0; i + 1 < pairs.length; i += 2) {
		const name = pairs[i];
		const value = pairs[i + 1];
		if (name !== undefined && value !== undefined) {
			out[name] = value;
		}
	}
	return out;
}

/** The argument count of a function that takes one argument and then pairs of name and value, as Phrase and Fill do. */
const oneThenPairs = -1;

/**
 * text.ts's port of each Go function a words case names, called as
 * text_test.go's wordFuncs calls the Go one: how many arguments it takes,
 * and the call. Day takes a YYYY-MM-DD date and Clocktime an RFC 3339 time.
 */
const wordFuncs: Record<string, { args: number; call: (...a: string[]) => string }> = {
	Agent: { args: 1, call: (h: string) => text.agent(h) },
	Kind: { args: 2, call: (k: string, agentName: string) => text.kind(k, agentName) },
	AgentTime: { args: 1, call: (agentName: string) => text.agentTime(agentName) },
	Other: { args: 2, call: (r: string, agentName: string) => text.other(r, agentName) },
	OnHold: { args: 0, call: () => text.onHold() },
	Level: { args: 1, call: (e: string) => text.level(e) },
	Printable: { args: 1, call: (s: string) => text.printable(s) },
	Phrase: { args: oneThenPairs, call: (key: string, ...vars: string[]) => text.phrase(phraseKey(key), named(vars)) },
	Fill: { args: oneThenPairs, call: (s: string, ...vars: string[]) => text.fill(s, named(vars)) },
	Sentence: { args: 1, call: (s: string) => text.sentence(s) },
	Day: { args: 1, call: (date: string) => text.day(date) },
	Clocktime: { args: 1, call: (at: string) => text.clocktime(new Date(at)) },
};

describe('internal/coach/text/testdata/cases.json', () => {
	const tables: [string, unknown[]][] = [
		['duration', shared.duration],
		['clock', shared.clock],
		['percentages', shared.percentages],
		['words', shared.words],
	];
	for (const [name, rows] of tables) {
		test(`its ${name} table has cases, so the tests that read it check something`, () => {
			expect(Array.isArray(rows) && rows.length > 0).toBe(true);
		});
	}
});

describe('duration, as text.Duration', () => {
	for (const c of shared.duration) {
		test(c.name, () => {
			expect(text.duration(c.s)).toBe(c.want);
		});
	}
});

describe('clock, as text.Clock', () => {
	for (const c of shared.clock) {
		test(c.name, () => {
			const got = text.clock(c.you, c.level_c, c.calibrating, c.agents);
			expect(got).toBe(c.want);
			expect(got, 'the clock shows no ratio').not.toMatch(/[%/]/);
		});
	}
});

describe("the catalog's words, as text_test.go's TestWords", () => {
	for (const c of shared.words) {
		test(c.name, () => {
			const f = wordFuncs[c.func];
			if (f === undefined) {
				throw new Error(`text.ts has no port of ${c.func} for this case`);
			}
			const fits = f.args === oneThenPairs ? c.args.length % 2 === 1 : c.args.length === f.args;
			expect(fits, `${c.func} takes the arguments ${JSON.stringify(c.args)}`).toBe(true);
			expect(f.call(...c.args)).toBe(c.want);
		});
	}
});

describe('percentages, as text.Percentages', () => {
	for (const c of shared.percentages) {
		test(c.name, () => {
			const got = text.percentages(c.parts);
			expect(got).toEqual(c.want);
			const sum = got.reduce((a, b) => a + b, 0);
			expect(sum === 100 || sum === 0, `adds up to ${sum}`).toBe(true);
		});
	}
});

describe("text.ts's own cases: what cases.json can't hold, and what the Go package doesn't do", () => {
	const replacement = String.fromCodePoint(0xfffd);
	const own: { name: string; got: () => string; want: string }[] = [
		{ name: 'NaN is 0m', got: () => text.duration(Number.NaN), want: '0m' },
		{
			name: "Printable replaces a lone surrogate, as Go's Printable replaces bytes that aren't UTF-8",
			got: () => text.printable(`a${String.fromCharCode(0xd800)}b`),
			want: `a${replacement}b`,
		},
		{
			name: 'Printable keeps a character outside the Basic Multilingual Plane whole',
			got: () => text.printable(`a${String.fromCodePoint(0x1f600)}b`),
			want: `a${String.fromCodePoint(0x1f600)}b`,
		},
		{ name: "a date that doesn't parse stays as it is", got: () => text.day('2026-02-30'), want: '2026-02-30' },
		{
			name: "X.agent_only and X.automated are the agent's time",
			got: () => String(text.isAgentTime('X.agent_only') && text.isAgentTime('X.automated')),
			want: 'true',
		},
		{ name: 'a share is its whole percent, as recap prints it', got: () => text.share(54), want: '54%' },
		{ name: 'a share that rounds to 0 is <1%, as recap prints it', got: () => text.share(0), want: '<1%' },
	];
	for (const c of own) {
		test(c.name, () => {
			expect(c.got()).toBe(c.want);
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
