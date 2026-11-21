// SPDX-License-Identifier: AGPL-3.0-or-later

// The dashboard's pages rendered to HTML with svelte/server from the fake
// agent's answers. Playwright checks the built pages in Chromium
// (tests/dashboard.spec.ts); these catch a broken template sooner.

import { render } from 'svelte/server';
import { describe, expect, test } from 'vitest';
import { dayReply, lastSession, status } from '../../../tests/support/fake-days.ts';
import type { Day, Focus, LastSession, Tally } from '../api.ts';
import Clock from '../components/Clock.svelte';
import { buildProject, dates } from '../model.ts';
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

/** The text of the element with data-testid id, which has no element of its own name inside it. */
function part(html: string, tag: string, id: string): string {
	const m = new RegExp(`<${tag}[^>]*data-testid="${id}"[\\s\\S]*?</${tag}>`).exec(html);
	if (!m) {
		throw new Error(`no ${tag} with data-testid="${id}"`);
	}
	return text(m[0]);
}

const week = (shadow = false): Day[] => dates('2026-11-16', '2026-11-20').map((d) => dayReply(d, shadow));

interface TodayOptions {
	shadow?: boolean;
	focus?: Focus;
	session?: LastSession;
	/** GET /v1/sessions/last didn't come back. */
	unreadSession?: boolean;
	/** Earlier dates of the week that didn't come back. */
	unread?: string[];
	empty?: boolean;
}

function today(options: TodayOptions = {}): string {
	const shadow = options.shadow ?? false;
	const t = options.empty ? { ...dayReply('today', shadow), projects: [] } : dayReply('today', shadow);
	const unread = options.unread ?? [];
	const earlier = week(shadow)
		.slice(0, 4)
		.filter((d) => !unread.includes(d.date));
	return render(Today, {
		props: {
			today: t,
			week: [...earlier, t],
			unread,
			session: options.unreadSession ? undefined : (options.session ?? lastSession({ shadow })),
			focus: options.focus,
		},
	}).body;
}

const levelC = 'it shows on your own dashboard, not on your profile, build days or most programs';
const focusClause = "This desktop doesn't tell pairtime which window is in front.";
const focusSentence =
	"This desktop doesn't tell pairtime which window is in front, so your time here counts only on your own dashboard, not on your profile, build days or most programs.";
const wednesdayUnread =
	"pairtime couldn't read Wednesday, November 18 from its agent, so the times here leave that day out. It asks again in a minute.";

describe('Today', () => {
	const cases: { name: string; html: () => string; check: (html: string) => void }[] = [
		{
			name: "it shows the day's chess clock as `pairtime statusline` would, its level-C time labeled and every agent named",
			html: () => today(),
			check: (html) =>
				expect(part(html, 'p', 'today-clock')).toBe('you 3h 27m, 5m level C · Claude 41m · Codex 22m · Copilot 18m · Cursor 12m'),
		},
		{
			name: "each project's clock line is `pairtime today`'s",
			html: () => today(),
			check: (html) => {
				const t = text(html);
				for (const line of ['portfolio you 52m · Claude 41m', 'homework-helper you 41m, 5m level C · Copilot 18m', 'plant-sensor you 29m']) {
					expect(t).toContain(line);
				}
			},
		},
		{
			name: 'it labels level-C time and pending time with the CLI notes',
			html: () => today(),
			check: (html) => {
				const t = part(html, 'section', 'today');
				expect(t).toContain(`5m of it is level C: ${levelC}`);
				expect(t).toContain('9m of it may still change');
				expect(t).toContain('94% level A: pairtime saw your typing');
				expect(t).toContain('2% level C: this shows on your own dashboard, not on your profile, build days or most programs');
			},
		},
		{
			name: 'pending minutes are a part of their own on the bars, which the stylesheet draws lighter',
			html: () => today(),
			check: (html) => expect(html).toMatch(/<rect[^>]*class="part pending[^"]*"[^>]*data-part="pending"/),
		},
		{
			name: "each agent has a hand of its own, in en.json's words",
			html: () => today(),
			check: (html) => expect(text(html)).toContain('Claude, working on its own 41m'),
		},
		{
			name: "the rest of the time is in en.json's words, most first",
			html: () => today(),
			check: (html) => {
				const t = part(html, 'section', 'today');
				const order = ['Away 15m', 'In other apps 6m', 'Looking things up, past what counts 5m', 'Here, between steps 3m'];
				const at = order.map((s) => t.indexOf(s));
				expect(at.every((i) => i >= 0), t).toBe(true);
				expect([...at].sort((a, b) => a - b)).toEqual(at);
			},
		},
		{
			name: 'the last session reads as `pairtime recap` heads it, and Cursor, with no time of its own in it, has no hand',
			html: () => today(),
			check: (html) => {
				const t = part(html, 'section', 'session');
				expect(t).toContain('Your last session: rusty-cli');
				expect(t).toContain('Friday, November 20, 14:05 to 14:14');
				expect(part(html, 'p', 'session-clock')).toBe('you 9m');
				expect(t).not.toContain('Cursor');
			},
		},
		{
			name: 'before the first session it says when one starts',
			html: () => today({ session: lastSession({ none: true }) }),
			check: (html) =>
				expect(part(html, 'section', 'session')).toContain(
					'No work sessions yet. The first one starts when pairtime counts some of your time.',
				),
		},
		{
			name: 'a session still going says so',
			html: () => today({ session: lastSession({ open: true }) }),
			check: (html) => {
				const t = part(html, 'section', 'session');
				expect(t).toContain('Your session so far: rusty-cli');
				expect(t).toContain('Friday, November 20, since 14:05');
			},
		},
		{
			name: "a session the agent's answer didn't come back with is a card that says so and that pairtime asks again",
			html: () => today({ unreadSession: true }),
			check: (html) =>
				expect(part(html, 'section', 'session')).toBe(
					"Your last session pairtime couldn't read your last session from its agent. It asks again in a minute.",
				),
		},
		{
			name: "with focus unknown it says D-focus-unknown's first clause before your time, and nothing of where that time counts",
			html: () => today({ focus: { known: false } }),
			check: (html) => {
				const t = text(html);
				expect(part(html, 'div', 'focus-unknown')).toBe(focusClause);
				expect(t.indexOf(focusClause)).toBeLessThan(t.indexOf('Your time today'));
				expect(t).not.toContain('counts only on your own dashboard');
			},
		},
		{
			name: 'once the agent says such time is level C, the sentence says where it counts',
			html: () => today({ focus: { known: false, level_c: true } }),
			check: (html) => expect(part(html, 'div', 'focus-unknown')).toBe(focusSentence),
		},
		{
			name: 'with focus known, or not reported, there is no such sentence',
			html: () => today({ focus: { known: true } }) + today(),
			check: (html) => expect(text(html)).not.toContain("doesn't tell pairtime which window"),
		},
		{
			name: "while calibrating it shows none of your time and keeps the agents'",
			html: () => today({ shadow: true }),
			check: (html) => {
				const t = text(html);
				expect(t).toContain('Your time shows as calibrating while pairtime checks how it counts.');
				expect(t).toContain('you calibrating · Claude 41m');
				for (const figure of ['3h 27m', '52m', '47m', '38m', '29m', '5h 37m', '9m']) {
					expect(t, figure).not.toContain(figure);
				}
				expect(html).not.toContain('data-testid="legend"');
				expect(part(html, 'section', 'week')).not.toContain('level C');
			},
		},
		{
			name: 'while calibrating the rest of the time is only SM 4.2\'s loops, so no other reason shows',
			html: () => today({ shadow: true }),
			check: (html) => {
				const t = text(html);
				for (const words of ['Away', 'In other apps', 'Here, between steps', 'Looking things up, past what counts', 'The rest of the time']) {
					expect(t, words).not.toContain(words);
				}
			},
		},
		{
			name: 'while calibrating the session is its project and day, with no start, end or time since',
			html: () => today({ shadow: true }),
			check: (html) => {
				const t = part(html, 'section', 'session');
				expect(t).toContain('Your last session: rusty-cli');
				expect(part(html, 'p', 'session-span')).toBe('Friday, November 20');
				for (const when of ['14:05', '14:14', 'since', 'so far']) {
					expect(t, when).not.toContain(when);
				}
			},
		},
		{
			name: 'a calibrating day hides the times of a session the agent sent with them, and that it goes on',
			html: () => today({ shadow: true, session: lastSession({ open: true }) }),
			check: (html) => {
				const t = part(html, 'section', 'session');
				expect(t).toContain('Your last session: rusty-cli');
				expect(part(html, 'p', 'session-span')).toBe('Friday, November 20');
				for (const when of ['14:05', 'since', 'so far', '9m']) {
					expect(t, when).not.toContain(when);
				}
			},
		},
		{
			name: "a day with nothing on it yet says so in en.json's words",
			html: () => today({ empty: true }),
			check: (html) => expect(text(html)).toContain('Nothing tracked yet today.'),
		},
		{
			name: 'the week lists only the days with time on them',
			html: () => today(),
			check: (html) => {
				const rows = [...html.matchAll(/<li[^>]* data-date="([^"]+)"/g)].map((m) => m[1]);
				expect(rows).toEqual(['2026-11-16', '2026-11-18', '2026-11-19', '2026-11-20']);
			},
		},
		{
			name: "the week's clock labels its level-C time, and its notes say how much and what may still change",
			html: () => today(),
			check: (html) => {
				expect(part(html, 'p', 'week-clock')).toBe('you 5h 37m, 10m level C · Claude 1h 21m · Copilot 33m · Codex 32m · Cursor 17m');
				expect(part(html, 'div', 'week-notes')).toBe(`10m of it is level C: ${levelC} 9m of it may still change`);
			},
		},
		{
			name: 'a day of the week that didn\'t come back is named as left out, and the week adds up without it',
			html: () => today({ unread: ['2026-11-18'] }),
			check: (html) => {
				expect(part(html, 'p', 'unread-days')).toBe(wednesdayUnread);
				expect(part(html, 'p', 'week-clock')).toBe('you 4h 37m, 10m level C · Claude 1h 21m · Copilot 33m · Codex 22m · Cursor 12m');
				const rows = [...html.matchAll(/<li[^>]* data-date="([^"]+)"/g)].map((m) => m[1]);
				expect(rows).toEqual(['2026-11-16', '2026-11-19', '2026-11-20']);
			},
		},
		{
			name: 'with every day there, nothing is named as left out',
			html: () => today(),
			check: (html) => expect(html).not.toContain('data-testid="unread-days"'),
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

describe('a clock and its hands', () => {
	const tally = (byHarness: Tally['agent_only_by_harness']): Tally => ({
		credited_s: 265,
		pending_s: 0,
		credited_by_kind: { hand_edit: 265 },
		credited_by_evidence: { B: 265 },
		agent_only_s: Object.values(byHarness).reduce((a: number, b) => a + (b ?? 0), 0),
		agent_only_by_harness: byHarness,
		uncredited: {},
	});
	const clock = (byHarness: Tally['agent_only_by_harness']) =>
		render(Clock, { props: { view: buildProject('p', 'remind-bot', tally(byHarness), false), scale: 265 } }).body;
	const cases: { name: string; html: () => string; check: (html: string) => void }[] = [
		{
			name: 'an agent with under a second, which would read 0m, has no hand, as the clock line leaves it out',
			html: () => clock({ cursor: 0.5 }),
			check: (html) => {
				expect(part(html, 'p', 'clock-line')).toBe('you 4m 25s');
				expect(html).not.toContain('data-testid="agent"');
				expect(text(html)).not.toContain('Cursor');
			},
		},
		{
			name: 'an agent with a second or more has its hand',
			html: () => clock({ cursor: 20 }),
			check: (html) => {
				expect(part(html, 'p', 'clock-line')).toBe('you 4m 25s · Cursor 20s');
				expect(text(html)).toContain('Cursor, working on its own 20s');
			},
		},
	];
	for (const c of cases) {
		test(c.name, () => c.check(c.html()));
	}
});

describe('Projects', () => {
	const page = (options: { shadow?: boolean; unread?: string[] } = {}) => {
		const unread = options.unread ?? [];
		const days = week(options.shadow).filter((d) => !unread.includes(d.date));
		return render(Projects, { props: { days, unread, range: 'week' as const, from: '2026-11-16', to: '2026-11-20' } }).body;
	};
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
			name: "X.noprog's minutes on hold are a row of their own, beside the minutes that can't come back",
			html: () => page(),
			check: (html) => {
				const t = text(html);
				expect(t).toContain('homework-helper you 51m, 10m level C · Copilot 33m');
				const order = [
					'Here, between steps 3m',
					'On hold: these come back when you fix it 1m 30s',
					'Past two hours without progress, counted at half 1m',
				];
				const at = order.map((s) => t.indexOf(s));
				expect(at.every((i) => i >= 0), t).toBe(true);
				expect([...at].sort((a, b) => a - b)).toEqual(at);
				expect(html).toContain('data-key="X.noprog:on_hold"');
				expect(html).toContain('data-key="X.noprog"');
			},
		},
		{
			name: 'while calibrating no project shows your time or what it went on',
			html: () => page({ shadow: true }),
			check: (html) => {
				const t = text(html);
				expect(t).not.toContain('What your time went on');
				expect(t).toContain('you calibrating · Claude 1h 21m');
			},
		},
		{
			name: "while calibrating the rest of the time is SM 4.2's loops alone",
			html: () => page({ shadow: true }),
			check: (html) => {
				const t = text(html);
				expect(t).toContain('Re-rolling the same prompt 4m');
				for (const words of ['In other apps', 'Away', 'Here, between steps', 'Looking things up, past what counts', 'On hold', 'Past two hours']) {
					expect(t, words).not.toContain(words);
				}
			},
		},
		{
			name: "a day that didn't come back is named under the span, and the totals leave it out",
			html: () => page({ unread: ['2026-11-18'] }),
			check: (html) => {
				const t = text(html);
				const span = 'From Monday, November 16 to Friday, November 20';
				expect(t).toContain(`${span} ${wednesdayUnread}`);
				expect(t).toContain('discord-bot you 38m · Codex 22m');
			},
		},
		{
			name: 'two days that didn\'t come back are both named',
			html: () => page({ unread: ['2026-11-17', '2026-11-18'] }),
			check: (html) =>
				expect(part(html, 'p', 'unread-days')).toBe(
					"pairtime couldn't read Tuesday, November 17 and Wednesday, November 18 from its agent, so the times here leave those days out. It asks again in a minute.",
				),
		},
	];
	for (const c of cases) {
		test(c.name, () => c.check(c.html()));
	}
});

describe('Settings', () => {
	const cases: { name: string; html: () => string; check: (html: string) => void }[] = [
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
