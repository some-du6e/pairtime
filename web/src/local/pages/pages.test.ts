// SPDX-License-Identifier: AGPL-3.0-or-later

// The dashboard's pages rendered to HTML with svelte/server from the fake
// agent's answers. Playwright checks the built pages in Chromium
// (tests/dashboard.spec.ts); these catch a broken template sooner.

import { render } from 'svelte/server';
import { describe, expect, test } from 'vitest';
import { dayReply, lastSession, status } from '../../../tests/support/fake-days.ts';
import type { Day, Focus, LastSession } from '../api.ts';
import { dates } from '../model.ts';
import Privacy from './Privacy.svelte';
import Projects from './Projects.svelte';
import Settings from './Settings.svelte';
import Today from './Today.svelte';

/** The text a page shows, without tags, comments or entities. */
function text(html: string): string {
	return html
		.replace(/<!--[\s\S]*?-->/g, '')
		.replace(/<[^>]*>/g, ' ')
		.replaceAll('&#39;', "'")
		.replaceAll('&quot;', '"')
		.replaceAll('&lt;', '<')
		.replaceAll('&amp;', '&')
		.replace(/\s+/g, ' ')
		.trim();
}

const week = (shadow = false): Day[] => dates('2026-11-16', '2026-11-20').map((d) => dayReply(d, shadow));

function today(options: { shadow?: boolean; focus?: Focus; session?: LastSession; empty?: boolean } = {}): string {
	const shadow = options.shadow ?? false;
	const t = options.empty ? { ...dayReply('today', shadow), projects: [] } : dayReply('today', shadow);
	return render(Today, {
		props: { today: t, week: [...week(shadow).slice(0, 4), t], session: options.session ?? lastSession({ shadow }), focus: options.focus },
	}).body;
}

const focusSentence =
	"This desktop doesn't tell pairtime which window is in front, so your time here counts only on your own dashboard, not on your profile, build days or most programs.";

describe('Today', () => {
	const cases: { name: string; html: () => string; check: (html: string) => void }[] = [
		{
			name: "it shows the day's chess clock as `pairtime statusline` would, every agent named",
			html: () => today(),
			check: (html) => expect(text(html)).toContain('you 3h 27m · Claude 41m · Codex 22m · Copilot 18m · Cursor 12m'),
		},
		{
			name: "each project's clock line is `pairtime today`'s",
			html: () => today(),
			check: (html) => {
				const t = text(html);
				for (const line of ['portfolio you 52m · Claude 41m', 'plant-sensor you 29m']) {
					expect(t).toContain(line);
				}
			},
		},
		{
			name: 'it labels level-C time and pending time with the CLI notes',
			html: () => today(),
			check: (html) => {
				const t = text(html);
				expect(t).toContain('5m of it is level C: it counts only on your own dashboard');
				expect(t).toContain('9m of it may still change');
				expect(t).toContain('94% level A: pairtime saw your typing');
			},
		},
		{
			name: 'pending minutes are a part of their own on the bars, which the stylesheet draws lighter',
			html: () => today(),
			check: (html) => expect(html).toMatch(/<rect[^>]*class="part pending[^"]*"[^>]*data-part="pending"/),
		},
		{
			name: 'each agent has a hand of its own, in en.json\'s words',
			html: () => today(),
			check: (html) => expect(text(html)).toContain('Claude, working on its own 41m'),
		},
		{
			name: 'the rest of the time is in en.json\'s words, most first',
			html: () => today(),
			check: (html) => {
				const t = text(html);
				const order = ['Away 15m', 'In other apps 6m', 'Looking things up, past what counts 5m', 'Here, between steps 3m'];
				const at = order.map((s) => t.indexOf(s));
				expect(at.every((i) => i >= 0), t).toBe(true);
				expect([...at].sort((a, b) => a - b)).toEqual(at);
			},
		},
		{
			name: 'the last session reads as `pairtime recap` heads it',
			html: () => today(),
			check: (html) => {
				const t = text(html);
				expect(t).toContain('Your last session: rusty-cli');
				expect(t).toContain('Friday, November 20, 14:05 to 14:14');
				expect(t).toContain('you 9m · Cursor 1m');
			},
		},
		{
			name: 'before the first session it says when one starts',
			html: () => today({ session: lastSession({ none: true }) }),
			check: (html) =>
				expect(text(html)).toContain('No work sessions yet. The first one starts when pairtime counts some of your time.'),
		},
		{
			name: 'a session still going says so',
			html: () => today({ session: lastSession({ open: true }) }),
			check: (html) => {
				expect(text(html)).toContain('Your session so far: rusty-cli');
				expect(text(html)).toContain('Friday, November 20, since 14:05');
			},
		},
		{
			name: "with focus unknown it says D-focus-unknown's sentence before your time",
			html: () => today({ focus: { known: false } }),
			check: (html) => {
				const t = text(html);
				expect(t).toContain(focusSentence);
				expect(t.indexOf(focusSentence)).toBeLessThan(t.indexOf('Your time today'));
			},
		},
		{
			name: 'with focus known, or not reported, there is no such sentence',
			html: () => today({ focus: { known: true, via: 'SetWinEventHook' } }) + today(),
			check: (html) => expect(text(html)).not.toContain("doesn't tell pairtime which window"),
		},
		{
			name: "while calibrating it shows none of your time and keeps the agents'",
			html: () => today({ shadow: true }),
			check: (html) => {
				const t = text(html);
				expect(t).toContain('Your time shows as calibrating while pairtime checks how it counts.');
				expect(t).toContain('you calibrating · Claude 41m');
				for (const figure of ['3h 27m', '52m', '47m', '38m', '29m', '5h 37m']) {
					expect(t, figure).not.toContain(figure);
				}
				expect(html).not.toContain('data-testid="legend"');
			},
		},
		{
			name: 'a day with nothing on it yet says so in en.json\'s words',
			html: () => today({ empty: true }),
			check: (html) => expect(text(html)).toContain('Nothing tracked yet today.'),
		},
		{
			name: 'the week lists only the days with time on them',
			html: () => today(),
			check: (html) => {
				const rows = [...html.matchAll(/<tr data-date="([^"]+)"/g)].map((m) => m[1]);
				expect(rows).toEqual(['2026-11-16', '2026-11-18', '2026-11-19', '2026-11-20']);
			},
		},
		{
			name: 'no bar is drawn with a style attribute, which the CSP would block',
			html: () => today(),
			check: (html) => expect(html).not.toMatch(/\sstyle=/),
		},
	];
	for (const c of cases) {
		test(c.name, () => c.check(c.html()));
	}
});

describe('Projects', () => {
	const page = (shadow = false) =>
		render(Projects, { props: { days: week(shadow), range: 'week' as const, from: '2026-11-16', to: '2026-11-20' } }).body;
	const cases: { name: string; html: () => string; check: (html: string) => void }[] = [
		{
			name: "each project lists what your time went on and the rest, in en.json's words",
			html: () => page(),
			check: (html) => {
				const t = text(html);
				expect(t).toContain('discord-bot you 1h 23m · Codex 32m');
				expect(t).toContain('Writing prompts 45m');
				expect(t).toContain('Re-rolling the same prompt 4m');
			},
		},
		{
			name: "a project's reading names its agent",
			html: () => page(),
			check: (html) => expect(text(html)).toContain("Reading Claude's answers"),
		},
		{
			name: 'while calibrating no project shows your time or what it went on',
			html: () => page(true),
			check: (html) => {
				const t = text(html);
				expect(t).not.toContain('What your time went on');
				expect(t).toContain('you calibrating · Claude 1h 21m');
			},
		},
	];
	for (const c of cases) {
		test(c.name, () => c.check(c.html()));
	}
});

describe('Privacy and Settings', () => {
	const cases: { name: string; html: () => string; check: (html: string) => void }[] = [
		{
			name: 'Privacy says nothing has left this computer in local mode',
			html: () => render(Privacy, { props: { mode: 'local' as const } }).body,
			check: (html) => expect(text(html)).toContain('Nothing. pairtime is in local-only mode'),
		},
		{
			name: 'Privacy says how long pairtime keeps each kind of record, as DESIGN 4.4 does',
			html: () => render(Privacy, { props: { mode: 'local' as const } }).body,
			check: (html) => {
				const t = text(html);
				for (const kept of ['90 days', '30 days', '180 days', 'Until you delete them', 'Until you uninstall pairtime']) {
					expect(t).toContain(kept);
				}
			},
		},
		{
			name: 'Settings shows the mode, calibration and the agent',
			html: () => render(Settings, { props: { status: status(), shadow: false } }).body,
			check: (html) => {
				const t = text(html);
				expect(t).toContain('Local only: nothing leaves this computer');
				expect(t).toContain('Off. Your time shows as pairtime counts it.');
				expect(t).toContain('v0.1.0');
			},
		},
		{
			name: "Settings lists the agent's problems in its own words",
			html: () =>
				render(Settings, { props: { status: status({ problems: { port: 'port 7247 was taken, so the agent listens on 7248' } }), shadow: true } })
					.body,
			check: (html) => {
				const t = text(html);
				expect(t).toContain('port 7247 was taken, so the agent listens on 7248');
				expect(t).toContain('On. You');
			},
		},
	];
	for (const c of cases) {
		test(c.name, () => c.check(c.html()));
	}
});
