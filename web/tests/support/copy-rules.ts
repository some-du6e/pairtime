// SPDX-License-Identifier: AGPL-3.0-or-later

// The rules every sentence pairtime shows a teen keeps, for the tests that
// read the dashboard's words (src/local/copy.test.ts, and
// src/local/pages/privacy.test.ts for the Privacy page's, retention.json's
// included). The scolding words, and the cases that pin down what they
// match, come from internal/coach/text/testdata/cases.json, which
// text_test.go builds its own matcher from, so a word added there holds
// every page and the CLI alike.

import { readFileSync } from 'node:fs';

/** cases.json's scolding table, which text_test.go reads too. */
export interface Scolding {
	alternatives: string[];
	marks: string[];
	cases: { name: string; s: string; want: string[] }[];
}

export const { scolding } = JSON.parse(
	readFileSync(new URL('../../../internal/coach/text/testdata/cases.json', import.meta.url), 'utf8'),
) as { scolding: Scolding };

// text_test.go's matcher, built the same way from the same list: words that
// would make a view nag, shame or set a target (DESIGN 3.8; PA 4.3, 4.4),
// as whole words in any case, but not right after a slash, where a word is
// a command's name, such as Claude Code's /goal (SM 6.4).
const scoldingWord = new RegExp('(?:^|[^\\w/])(' + scolding.alternatives.join('|') + ')\\b', 'gi');

/** What in s scolds, as text_test.go's scolds finds it: each scolding word, then each mark, "%" or "!". */
export function scolds(s: string): string[] {
	const out = [...s.matchAll(scoldingWord)].map((m) => m[1] ?? m[0]);
	for (const mark of scolding.marks) {
		if (s.includes(mark)) {
			out.push(mark);
		}
	}
	return out;
}

// The em dash, the en dash and the four curly quotes, U+2014, U+2013,
// U+2018, U+2019, U+201C and U+201D, made from their numbers so that this
// file holds none of them.
const typography = String.fromCodePoint(0x2014, 0x2013, 0x2018, 0x2019, 0x201c, 0x201d);

/** The rules every sentence on the dashboard keeps, each with what in a sentence breaks it. */
export const rules: { name: string; breaks: (s: string) => string[] }[] = [
	{ name: "doesn't scold, set a target or count a streak", breaks: scolds },
	{ name: 'has no em dash, en dash or curly quote', breaks: (s) => [...s].filter((c) => typography.includes(c)) },
];
