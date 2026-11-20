// SPDX-License-Identifier: AGPL-3.0-or-later

// The dashboard's pages rendered to HTML with svelte/server from the fake
// agent's answers. Playwright checks the built pages in Chromium
// (tests/dashboard.spec.ts); these catch a broken template sooner.

import { render } from 'svelte/server';
import { describe, expect, test } from 'vitest';
import { status, weekDays } from '../../../tests/support/fake-days.ts';
import type { Days, Focus } from '../api.ts';
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

function today(days: Days, focus?: Focus): string {
	return render(Today, { props: { days, focus } }).body;
}

const focusSentence =
	"This desktop doesn't tell pairtime which window is in front, so your time here counts only on your own dashboard, not on your profile, build days or most programs.";

// Your time on SM 6.3's Friday, per project and in all, and this week's.
const credited = ['3h 27m', '52m', '47m', '38m', '29m', '5h 27m'];

describe('Today', () => {
	const cases: { name: string; html: () => string; check: (html: string) => void }[] = [
		{
			name: "it shows the day's clock: your time, and each agent's named after the agent",
			html: () => today(weekDays()),
			check: (html) => {
				const t = text(html);
				expect(t).toContain('Your time today 3h 27m');
				for (const hand of ['Claude 41m', 'Codex 22m', 'Copilot 18m', 'Cursor 12m']) {
					expect(t).toContain(hand);
				}
			},
		},
		{
			name: 'it labels level-C time as level C, and says how much is pending',
			html: () => today(weekDays()),
			check: (html) => {
				const t = text(html);
				expect(t).toContain('Level C, less to go on 5m');
				expect(t).toContain('Pending 9m');
			},
		},
		{
			name: 'pending minutes are a part of their own on the bars, which the stylesheet draws lighter',
			html: () => today(weekDays()),
			check: (html) => {
				expect(html).toMatch(/<rect[^>]*class="part pending[^"]*"[^>]*data-part="pending"/);
			},
		},
		{
			name: "it says why a minute counts at level A, B or C, and how long minutes stay pending",
			html: () => today(weekDays()),
			check: (html) => {
				const t = text(html);
				expect(t).toContain('Why a minute counts at level A, B or C');
				expect(t).toContain('pairtime waits 15 minutes after each minute');
			},
		},
		{
			name: "what pairtime didn't count is in plain words, most first",
			html: () => today(weekDays()),
			check: (html) => {
				const t = text(html);
				const order = ['Away 15m', 'In another app 6m', 'Long lookups 5m', 'The agent working on its own 4m', 'Nothing to count it from 3m'];
				const at = order.map((s) => t.indexOf(s));
				expect(at.every((i) => i >= 0), t).toBe(true);
				expect([...at].sort((a, b) => a - b)).toEqual(at);
			},
		},
		{
			name: "with focus unknown it says D-focus-unknown's sentence before your time",
			html: () => today(weekDays(), { known: false }),
			check: (html) => {
				const t = text(html);
				expect(t).toContain(focusSentence);
				expect(t.indexOf(focusSentence)).toBeLessThan(t.indexOf('Your time today'));
			},
		},
		{
			name: 'with focus known, or not reported, there is no such sentence',
			html: () => today(weekDays(), { known: true, via: 'SetWinEventHook' }) + today(weekDays()),
			check: (html) => {
				expect(text(html)).not.toContain("doesn't tell pairtime which window");
			},
		},
		{
			name: "while calibrating it shows none of your time, and keeps the agents' time",
			html: () => today(weekDays({ shadow: true })),
			check: (html) => {
				const t = text(html);
				expect(t).toContain('Calibrating');
				for (const figure of credited) {
					expect(t, figure).not.toContain(` ${figure} `);
				}
				// No legend of your time by level; the explainer still says what the levels are.
				expect(html).not.toContain('data-testid="legend"');
				expect(t).not.toContain("What pairtime didn't count");
				expect(t).toContain('Claude 41m');
				expect(t).toContain('your agents 2h 43m');
			},
		},
		{
			name: 'a day with nothing on it yet says so, with no judgment',
			html: () => today(weekDays({ days: weekDays().days.map((d) => (d.date === '2026-11-20' ? { ...d, projects: [] } : d)) })),
			check: (html) => {
				expect(text(html)).toContain('Nothing yet today.');
			},
		},
		{
			name: 'the week lists only the days with time on them',
			html: () => today(weekDays()),
			check: (html) => {
				const rows = [...html.matchAll(/<tr data-date="([^"]+)"/g)].map((m) => m[1]);
				expect(rows).toEqual(['2026-11-16', '2026-11-18', '2026-11-19', '2026-11-20']);
			},
		},
		{
			name: 'no bar is drawn with a style attribute, which the CSP would block',
			html: () => today(weekDays()),
			check: (html) => {
				expect(html).not.toMatch(/\sstyle=/);
			},
		},
	];
	for (const c of cases) {
		test(c.name, () => c.check(c.html()));
	}
});

describe('Projects', () => {
	const projects = () => render(Projects, { props: { days: weekDays(), range: 'week' as const } }).body;
	const cases: { name: string; html: () => string; check: (html: string) => void }[] = [
		{
			name: 'each project has its clock, your time by kind and what pairtime didn\'t count, in plain words',
			html: projects,
			check: (html) => {
				const t = text(html);
				expect(t).toContain('discord-bot');
				expect(t).toContain('Writing prompts 45m');
				expect(t).toContain('Asking again 4m');
				expect(t).toContain('A change of yours, such as an edit, a command or a commit, starts the count over.');
			},
		},
		{
			name: "a project's agent-only time is named after its agent",
			html: projects,
			check: (html) => {
				expect(text(html)).toContain('Claude working on its own 9m');
			},
		},
		{
			name: 'while calibrating, no project shows your time by kind or by reason',
			html: () => render(Projects, { props: { days: weekDays({ shadow: true }), range: 'week' as const } }).body,
			check: (html) => {
				const t = text(html);
				expect(t).not.toContain('Your time by kind');
				expect(t).not.toContain("What pairtime didn't count");
				expect(t).toContain('Calibrating');
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
			check: (html) => {
				expect(text(html)).toContain('Nothing. pairtime is in local-only mode');
			},
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
			html: () => render(Settings, { props: { status: status({ problems: { port: 'port 7247 was taken, so the agent listens on 7248' } }), shadow: true } }).body,
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
