// SPDX-License-Identifier: AGPL-3.0-or-later

// The decision components rendered to HTML with svelte/server. Playwright
// checks the same rules on the styled page in Chromium (tests/consent.spec.ts,
// tests/profile-choice.spec.ts); these catch a broken template sooner.

import { createRawSnippet } from 'svelte';
import { render } from 'svelte/server';
import { describe, expect, test } from 'vitest';
import type { ProfileMode } from '../choices.ts';
import ConsentCard from './ConsentCard.svelte';
import ProfileChoice from './ProfileChoice.svelte';

interface Found {
	text: string;
	variant: string | undefined;
	type: string | undefined;
}

function buttons(html: string): Found[] {
	return [...html.matchAll(/<button\b([^>]*)>([\s\S]*?)<\/button>/g)].map((m) => {
		const attrs = m[1] ?? '';
		return {
			text: (m[2] ?? '')
				.replace(/<!--[\s\S]*?-->/g, '')
				.replace(/<[^>]*>/g, '')
				.trim(),
			variant: /\bdata-variant="([^"]*)"/.exec(attrs)?.[1],
			type: /\btype="([^"]*)"/.exec(attrs)?.[1],
		};
	});
}

// Attributes that would preselect or focus a choice, looked for inside tags
// only, so the copy can say what it likes.
const preselects = /\s(checked|selected|autofocus)(=|\s|>|\/)|\saria-(pressed|checked|selected)="true"/;

function tags(html: string): string[] {
	return html.match(/<[a-z][^>]*>/gi) ?? [];
}

const what = createRawSnippet(() => ({ render: () => '<p>Counts keys, never which keys.</p>' }));

const lostComputer = 'Without a backup, if you lose this computer or wipe it, the evidence for the time you tracked on it is gone.';

function consentCard(declining?: string): string {
	return render(ConsentCard, {
		props: {
			title: 'Typing evidence',
			turnOff: 'Settings, under Typing evidence',
			accept: 'Turn on typing evidence',
			declining,
			onanswer: () => {},
			children: what,
		},
	}).body;
}

function profileChoice(mode: ProfileMode): () => string {
	return () => render(ProfileChoice, { props: { mode, onchoose: () => {} } }).body;
}

describe('ConsentCard (DESIGN 8.4)', () => {
	const cases: { name: string; html?: () => string; check: (html: string) => void }[] = [
		{
			name: 'the only primary-styled button is "Not now"',
			check: (html) => {
				expect(buttons(html).filter((b) => b.variant === 'primary').map((b) => b.text)).toEqual(['Not now']);
			},
		},
		{
			name: '"Not now" comes first, then the yes button with the caller\'s label',
			check: (html) => {
				expect(buttons(html).map((b) => [b.text, b.variant])).toEqual([
					['Not now', 'primary'],
					['Turn on typing evidence', 'secondary'],
				]);
			},
		},
		{
			name: 'nothing is preselected or focused',
			check: (html) => {
				expect(tags(html).filter((t) => preselects.test(t))).toEqual([]);
			},
		},
		{
			name: 'every button is type="button", so Enter in a form picks nothing',
			check: (html) => {
				expect(buttons(html).map((b) => b.type)).toEqual(['button', 'button']);
			},
		},
		{
			name: "the card says what the item does, that saying no doesn't stop anyone using pairtime, and where to turn it off",
			check: (html) => {
				expect(html).toContain('Counts keys, never which keys.');
				expect(html).toContain('stop you from using pairtime.</p>');
				expect(html).toContain('turn it off any time in Settings, under Typing evidence');
			},
		},
		{
			name: 'without a declining sentence, the card claims nothing about what else saying no changes',
			check: (html) => {
				expect(html).not.toContain('nothing else changes');
				expect(html).not.toContain('data-declining');
			},
		},
		{
			name: "the caller's declining sentence comes after the fixed one and before where to turn it off",
			html: () => consentCard(lostComputer),
			check: (html) => {
				const fixed = html.indexOf('stop you from using pairtime.</p>');
				const declining = html.indexOf(lostComputer);
				const turnOff = html.indexOf('turn it off any time in');
				expect(fixed).toBeGreaterThan(-1);
				expect(declining).toBeGreaterThan(fixed);
				expect(turnOff).toBeGreaterThan(declining);
			},
		},
	];
	for (const c of cases) {
		test(c.name, () => c.check((c.html ?? consentCard)()));
	}
});

describe('ProfileChoice (DESIGN 8.3)', () => {
	const cases: { name: string; html: () => string; check: (html: string) => void }[] = [
		{
			name: 'in public mode both choices have the same style, so neither outweighs the other',
			html: profileChoice('public'),
			check: (html) => {
				expect(buttons(html).map((b) => [b.text, b.variant])).toEqual([
					['Keep my profile public', 'secondary'],
					['Make it private', 'secondary'],
				]);
			},
		},
		{
			name: 'in ask mode "Private profile" is the one primary-styled button, and comes first',
			html: profileChoice('ask'),
			check: (html) => {
				expect(buttons(html).map((b) => [b.text, b.variant])).toEqual([
					['Private profile', 'primary'],
					['Public profile', 'secondary'],
				]);
			},
		},
		{
			name: 'in public mode nothing is preselected or focused',
			html: profileChoice('public'),
			check: (html) => {
				expect(tags(html).filter((t) => preselects.test(t))).toEqual([]);
			},
		},
		{
			name: 'in ask mode nothing is preselected or focused',
			html: profileChoice('ask'),
			check: (html) => {
				expect(tags(html).filter((t) => preselects.test(t))).toEqual([]);
			},
		},
		{
			name: 'the card says nothing is public until the teen chooses',
			html: profileChoice('public'),
			check: (html) => {
				expect(html).toContain('Nothing is public until you choose.');
			},
		},
	];
	for (const c of cases) {
		test(c.name, () => c.check(c.html()));
	}
});
