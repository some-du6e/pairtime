// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, test } from 'vitest';
import { dayReply, lastSession, projectsReply } from '../../tests/support/fake-days.ts';
import type { Day, NameState, ProjectEntry, Tally, WorkSession } from './api.ts';
import {
	addDays,
	agentList,
	agentOf,
	buildProject,
	clockLine,
	dates,
	dayLabel,
	entryTitle,
	mayChange,
	monday,
	nameShown,
	notes,
	otherRows,
	projectArg,
	projects,
	projectTitle,
	scaleOf,
	segments,
	sessionHead,
	sessionWhen,
	sum,
	whole,
	yourParts,
} from './model.ts';

// SM 6.2's five minutes before school, as #107 shows `pairtime today`
// printing it:
//
//	remind-bot: you 4m 25s · Cursor 20s
//	  Your time                   4m 25s
//	    editing by hand               2m
//	    writing prompts               1m
//	    reviewing and deciding       50s
//	    watching Cursor work         20s
//	    reading Cursor's answers     15s
//	  Cursor, working on its own     20s
//	  Away                           15s
const remindBot: Tally = {
	credited_s: 265,
	pending_s: 0,
	credited_by_kind: { prompting: 60, hand_edit: 120, reviewing: 50, supervising: 20, reading_output: 15 },
	credited_by_evidence: { B: 265 },
	agent_only_s: 20,
	agent_only_by_harness: { cursor: 20 },
	uncredited: { 'X.absent': 15, 'X.agent_only': 20 },
};

/** A tally with nothing credited and the given uncredited seconds. */
function rest(uncredited: Tally['uncredited'], more: Partial<Tally> = {}): Tally {
	return { agent_only_s: 0, agent_only_by_harness: {}, uncredited, ...more };
}

const friday = dayReply('today');
const thursday = dayReply('2026-11-19');
const fridayInShadow = dayReply('today', true);

/** The session rusty-cli's fixture gives, which is never null. */
function session(options: { shadow?: boolean; open?: boolean } = {}): WorkSession {
	const s = lastSession(options).session;
	if (!s) {
		throw new Error('the fixture has no session');
	}
	return s;
}

describe('a project as `pairtime today` shows it', () => {
	const cases: { name: string; check: () => void }[] = [
		{
			name: "SM 6.2's day reads as the CLI's clock line",
			check: () => expect(clockLine(buildProject('p_be4901c04a84', 'remind-bot', remindBot, false))).toBe('you 4m 25s · Cursor 20s'),
		},
		{
			name: "your time by kind comes most first, in en.json's words, with the one agent named",
			check: () =>
				expect(buildProject('p', 'remind-bot', remindBot, false).you?.kinds.map((k) => [k.words, k.s])).toEqual([
					['editing by hand', 120],
					['writing prompts', 60],
					['reviewing and deciding', 50],
					['watching Cursor work', 20],
					["reading Cursor's answers", 15],
				]),
		},
		{
			name: "the agent's time is its own hand, and the rest leaves the agent's codes out",
			check: () => {
				const v = buildProject('p', 'remind-bot', remindBot, false);
				expect(v.agents).toEqual([{ harness: 'cursor', name: 'Cursor', s: 20 }]);
				expect(v.other).toEqual([{ reason: 'X.absent', words: 'away', s: 15 }]);
			},
		},
		{
			name: 'one level is all of your time',
			check: () =>
				expect(buildProject('p', 'remind-bot', remindBot, false).you?.levels).toEqual([
					{ level: 'B', words: 'pairtime saw you at the computer, not your typing', s: 265, share: 100 },
				]),
		},
		{
			name: "calibrating leaves your time out, keeps the agent's, and of the rest only SM 4.2's loops",
			check: () => {
				const v = buildProject('p', 'remind-bot', remindBot, true);
				expect(v.you).toBeUndefined();
				expect(clockLine(v)).toBe('you calibrating · Cursor 20s');
				expect(v.other).toEqual([]);
			},
		},
		{
			name: 'a project the agent has no name for goes by its key, as the CLI shows it',
			check: () => expect(buildProject('p_be4901c04a84', '', remindBot, false).title).toBe('p_be4901c04a84'),
		},
		{
			name: "a name's bidirectional formatting and control characters show as U+FFFD, as text.Printable makes them",
			check: () => {
				const rlo = String.fromCodePoint(0x202e);
				const bell = String.fromCodePoint(0x07);
				const bad = String.fromCodePoint(0xfffd);
				expect(buildProject('p', `bot${rlo}gnp.exe${bell}`, remindBot, false).title).toBe(`bot${bad}gnp.exe${bad}`);
			},
		},
		{
			name: "a kind en.json has no words for is left out of your time's kinds, and its seconds stay in your time",
			check: () => {
				const t: Tally = {
					...remindBot,
					credited_by_kind: { ...remindBot.credited_by_kind, juggling: 30 } as Tally['credited_by_kind'],
					credited_s: 295,
				};
				const you = buildProject('p', 'remind-bot', t, false).you;
				expect(you?.kinds.map((k) => k.kind)).toEqual(['hand_edit', 'prompting', 'reviewing', 'supervising', 'reading_output']);
				expect(you?.s).toBe(295);
			},
		},
		{
			name: "a reason en.json has no words for is left out of the rest, so no raw code reaches the page",
			check: () =>
				expect(buildProject('p', 'remind-bot', rest({ 'X.absent': 15, 'X.someday': 40 } as Tally['uncredited']), false).other).toEqual([
					{ reason: 'X.absent', words: 'away', s: 15 },
				]),
		},
		{
			name: "time under a harness en.json has no name for is your agent's",
			check: () =>
				expect(agentList({ zed: 30, other: 10, claude_code: 20 } as Tally['agent_only_by_harness'])).toEqual([
					{ harness: 'other', name: 'your agent', s: 40 },
					{ harness: 'claude_code', name: 'Claude', s: 20 },
				]),
		},
		{
			name: 'a harness listed at 0 still names the agent whose answers were read',
			check: () => {
				const portfolio = projects([thursday], false).find((p) => p.key === 'p_portfolio');
				expect(portfolio?.you?.kinds.map((k) => k.words)).toEqual(["reading Claude's answers"]);
				expect(portfolio?.agents).toEqual([]);
				expect(portfolio && clockLine(portfolio)).toBe('you 10m');
			},
		},
		{
			name: 'with several agents or none, the words say your agent',
			check: () => {
				expect(agentOf({ claude_code: 1, codex: 2 })).toBe('your agent');
				expect(agentOf({})).toBe('your agent');
				expect(agentOf(undefined)).toBe('your agent');
				expect(whole([friday], false, 'today').you?.kinds.map((k) => k.words)).toContain("reading your agent's answers");
			},
		},
		{
			name: 'level-C time is labeled on the clock line from a second up',
			check: () => {
				const t: Tally = { ...remindBot, credited_by_evidence: { B: 145, C: 120 } };
				expect(clockLine(buildProject('p', 'remind-bot', t, false))).toBe('you 4m 25s, 2m level C · Cursor 20s');
				const under: Tally = { ...remindBot, credited_by_evidence: { B: 264.5, C: 0.5 } };
				expect(clockLine(buildProject('p', 'remind-bot', under, false))).toBe('you 4m 25s · Cursor 20s');
			},
		},
	];
	for (const c of cases) {
		test(c.name, c.check);
	}
});

describe('the rest of the time', () => {
	const onHold = 'on hold: these come back when you fix it';
	const pastTwoHours = 'past two hours without progress, counted at half';
	const cases: { name: string; check: () => void }[] = [
		{
			name: "X.noprog's seconds on hold are a part of their own, beside the ones that can't come back, the most first",
			check: () =>
				expect(buildProject('p', 'x', rest({ 'X.noprog': 600 }, { on_hold_s: 420 }), false).other).toEqual([
					{ reason: 'X.noprog', words: onHold, s: 420, onHold: true },
					{ reason: 'X.noprog', words: pastTwoHours, s: 180 },
				]),
		},
		{
			name: 'of two equal parts of X.noprog, the one on hold comes first',
			check: () =>
				expect(buildProject('p', 'x', rest({ 'X.noprog': 600 }, { on_hold_s: 300 }), false).other).toEqual([
					{ reason: 'X.noprog', words: onHold, s: 300, onHold: true },
					{ reason: 'X.noprog', words: pastTwoHours, s: 300 },
				]),
		},
		{
			name: 'more on hold than X.noprog holds is all of X.noprog on hold',
			check: () =>
				expect(buildProject('p', 'x', rest({ 'X.noprog': 600 }, { on_hold_s: 900 }), false).other).toEqual([
					{ reason: 'X.noprog', words: onHold, s: 600, onHold: true },
				]),
		},
		{
			name: 'nothing on hold, or less than nothing, leaves X.noprog what it was',
			check: () => {
				for (const more of [{}, { on_hold_s: 0 }, { on_hold_s: -5 }]) {
					expect(buildProject('p', 'x', rest({ 'X.noprog': 600 }, more), false).other).toEqual([
						{ reason: 'X.noprog', words: pastTwoHours, s: 600 },
					]);
				}
			},
		},
		{
			name: "the two parts of X.noprog are rows with keys of their own, and rows under a second aren't listed",
			check: () => {
				const v = buildProject('p', 'x', rest({ 'X.noprog': 600, 'X.absent': 0.5 }, { on_hold_s: 420 }), false);
				expect(otherRows(v).map((r) => r.key)).toEqual(['X.noprog:on_hold', 'X.noprog']);
			},
		},
		{
			name: "calibrating keeps SM 4.2's loops alone, X.day_cap and X.noprog left out",
			check: () => {
				const t = rest(
					{
						'X.absent': 60,
						'X.reroll': 120,
						'X.rewrite_loop': 90,
						'X.same_error': 45,
						'X.autopilot': 30,
						'X.day_cap': 300,
						'X.noprog': 200,
						'X.lookup_cap': 20,
						'X.agent_only': 30,
					},
					{ on_hold_s: 100, agent_only_s: 30, agent_only_by_harness: { codex: 30 } },
				);
				expect(buildProject('p', 'x', t, true).other).toEqual([
					{ reason: 'X.reroll', words: 're-rolling the same prompt', s: 120 },
					{ reason: 'X.rewrite_loop', words: 'Codex rewriting the same lines', s: 90 },
					{ reason: 'X.same_error', words: 'the same error coming back', s: 45 },
					{ reason: 'X.autopilot', words: 'replies sent before reading the answer', s: 30 },
				]);
				expect(buildProject('p', 'x', t, false).other.map((o) => [o.reason, o.s])).toEqual([
					['X.day_cap', 300],
					['X.reroll', 120],
					['X.noprog', 100],
					['X.noprog', 100],
					['X.rewrite_loop', 90],
					['X.absent', 60],
					['X.same_error', 45],
					['X.autopilot', 30],
					['X.lookup_cap', 20],
				]);
			},
		},
	];
	for (const c of cases) {
		test(c.name, c.check);
	}
});

describe('a day the agent gave while display.shadow was on', () => {
	const cases: { name: string; check: () => void }[] = [
		{
			name: "it has no credited field, and every view of it reads calibrating with the agents' time",
			check: () => {
				expect(fridayInShadow.projects.every((p) => p.credited_s === undefined && p.credited_by_kind === undefined)).toBe(true);
				expect(clockLine(whole([fridayInShadow], true, 'today'))).toBe(
					'you calibrating · Claude 41m · Codex 22m · Copilot 18m · Cursor 12m',
				);
				expect(projects([fridayInShadow], true).map((p) => clockLine(p))).toEqual([
					'you calibrating · Claude 41m',
					'you calibrating · Codex 22m',
					'you calibrating · Copilot 18m',
					'you calibrating · Cursor 12m',
					'you calibrating',
				]);
			},
		},
		{
			name: 'a number it leaves out sums as 0 and a map as {}',
			check: () => {
				const t = sum(fridayInShadow.projects);
				expect([t.credited_s, t.pending_s, t.on_hold_s, t.agent_only_s]).toEqual([0, 0, 0, 5580]);
				expect(t.credited_by_kind).toEqual({});
				expect(t.credited_by_evidence).toEqual({});
				// Friday has no loops, the only codes a shadow answer keeps.
				expect(t.uncredited).toEqual({});
			},
		},
		{
			name: 'read as not calibrating, it is 0m of your time rather than an error',
			check: () => {
				const p = fridayInShadow.projects[0];
				if (!p) {
					throw new Error('no project');
				}
				const v = buildProject(p.project, p.name, p, false);
				expect(v.you).toEqual({ s: 0, pending: 0, kinds: [], levels: [] });
				expect(clockLine(v)).toBe('you 0m · Claude 41m');
			},
		},
		{
			name: 'a tally with no maps at all reads as empty',
			check: () => {
				const bare = { agent_only_s: 0 } as Tally;
				expect(() => sum([bare])).not.toThrow();
				expect(buildProject('p', '', bare, false)).toEqual({ key: 'p', title: 'p', you: { s: 0, pending: 0, kinds: [], levels: [] }, agents: [], other: [] });
			},
		},
	];
	for (const c of cases) {
		test(c.name, c.check);
	}
});

describe("SM 6.3's afternoon", () => {
	const levelC = 'it shows on your own dashboard, not on your profile, build days or most programs';
	const cases: { name: string; check: () => void }[] = [
		{
			name: "each project's clock, the most of your time first, its level-C time labeled",
			check: () =>
				expect(projects([friday], false).map((p) => `${p.title}: ${clockLine(p)}`)).toEqual([
					'portfolio: you 52m · Claude 41m',
					'rusty-cli: you 47m · Cursor 12m',
					'homework-helper: you 41m, 5m level C · Copilot 18m',
					'discord-bot: you 38m · Codex 22m',
					'plant-sensor: you 29m',
				]),
		},
		{
			name: "the day's clock adds up SM's 207 minutes of yours, 5 of them level C, and each agent's time",
			check: () =>
				expect(clockLine(whole([friday], false, 'today'))).toBe(
					'you 3h 27m, 5m level C · Claude 41m · Codex 22m · Copilot 18m · Cursor 12m',
				),
		},
		{
			name: "the day's levels split 100 as the CLI rounds them",
			check: () =>
				expect(whole([friday], false, 'today').you?.levels.map((l) => [l.level, l.share])).toEqual([
					['A', 94],
					['B', 4],
					['C', 2],
				]),
		},
		{
			name: 'the notes say how much is level C and how much may still change',
			check: () => {
				const you = whole([friday], false, 'today').you;
				expect(you && notes(you).map((n) => n.words)).toEqual([`5m of it is level C: ${levelC}`, '9m of it may still change']);
			},
		},
		{
			name: 'calibrating, the projects go by agent time, then by name',
			check: () =>
				expect(projects([friday], true).map((p) => p.title)).toEqual([
					'portfolio',
					'discord-bot',
					'homework-helper',
					'rusty-cli',
					'plant-sensor',
				]),
		},
		{
			name: "the rest of the day's time, without the agents', most first",
			check: () =>
				expect(whole([friday], false, 'today').other.map((o) => [o.words, o.s])).toEqual([
					['away', 900],
					['in other apps', 360],
					['looking things up, past what counts', 300],
					['here, between steps', 180],
				]),
		},
		{
			name: "X.agent_only is the agent's time, so it is in no project's rest",
			check: () => expect(projects([friday], false).flatMap((p) => p.other.map((o) => o.reason))).not.toContain('X.agent_only'),
		},
	];
	for (const c of cases) {
		test(c.name, c.check);
	}
});

describe('sums over days', () => {
	const cases: { name: string; check: () => void }[] = [
		{
			name: "a project's days add up, field by field",
			check: () => {
				const bot = sum(['2026-11-18', 'today'].flatMap((d) => dayReply(d).projects.filter((p) => p.project === 'p_bot')));
				expect(bot).toMatchObject({
					credited_s: 4980,
					agent_only_s: 1920,
					credited_by_kind: { prompting: 2700, reading_output: 1680, supervising: 600 },
					credited_by_evidence: { A: 1800, B: 3180 },
					agent_only_by_harness: { codex: 1920 },
					uncredited: { 'X.reroll': 240, 'X.other_focus': 360, 'X.agent_only': 60 },
				});
			},
		},
		{
			name: 'a harness at 0 stays in the sum, so it still names the agent',
			check: () => expect(sum(thursday.projects.filter((p) => p.project === 'p_portfolio')).agent_only_by_harness).toEqual({ claude_code: 0 }),
		},
		{
			name: 'seconds on hold add up with the X.noprog they are part of',
			check: () => {
				const homework = sum(['2026-11-19', 'today'].flatMap((d) => dayReply(d).projects.filter((p) => p.project === 'p_homework')));
				expect([homework.on_hold_s, homework.uncredited['X.noprog']]).toEqual([90, 150]);
			},
		},
		{
			name: "the week's clock adds up each agent's days, and labels its level-C time",
			check: () => {
				const week = dates('2026-11-16', '2026-11-20').map((d) => dayReply(d));
				expect(clockLine(whole(week, false, 'this week'))).toBe(
					'you 5h 37m, 10m level C · Claude 1h 21m · Copilot 33m · Codex 32m · Cursor 17m',
				);
			},
		},
		{
			name: "the week's notes say its level-C time and what may still change",
			check: () => {
				const you = whole(dates('2026-11-16', '2026-11-20').map((d) => dayReply(d)), false, 'this week').you;
				expect(you && notes(you).map((n) => n.key)).toEqual(['level-c', 'pending']);
				expect(you && notes(you)[0]?.words).toBe(
					'10m of it is level C: it shows on your own dashboard, not on your profile, build days or most programs',
				);
			},
		},
	];
	for (const c of cases) {
		test(c.name, c.check);
	}
});

describe('which earlier days the page asks for again', () => {
	const withOnHold: Day = {
		...dayReply('2026-11-18'),
		projects: [{ project: 'p', name: '', agent_only_s: 0, agent_only_by_harness: {}, uncredited: {}, on_hold_s: 30 }],
	};
	const cases: { name: string; day: () => Day; yesterday: boolean; shadow: boolean; want: boolean }[] = [
		{ name: 'a day with pending minutes is asked again', day: () => friday, yesterday: false, shadow: false, want: true },
		{ name: 'a final day before yesterday is asked once', day: () => dayReply('2026-11-18'), yesterday: false, shadow: false, want: false },
		{ name: 'yesterday with X.noprog is asked again, since progress can still restore it', day: () => thursday, yesterday: true, shadow: false, want: true },
		{ name: 'yesterday with seconds on hold is asked again', day: () => withOnHold, yesterday: true, shadow: false, want: true },
		{ name: 'X.noprog the day before yesterday is past PROGRESS_RESTORE_S, so asked once', day: () => thursday, yesterday: false, shadow: false, want: false },
		{ name: "yesterday with nothing that can come back is asked once", day: () => dayReply('2026-11-18'), yesterday: true, shadow: false, want: false },
		{ name: 'yesterday while calibrating, which hides X.noprog, is asked again', day: () => dayReply('2026-11-18', true), yesterday: true, shadow: true, want: true },
		{ name: 'an earlier day while calibrating is asked once', day: () => dayReply('2026-11-18', true), yesterday: false, shadow: true, want: false },
		{ name: 'a day read while calibrating is asked again once calibration ends', day: () => dayReply('2026-11-18', true), yesterday: false, shadow: false, want: true },
		{ name: 'a day read before calibration is asked again once it starts', day: () => dayReply('2026-11-18'), yesterday: false, shadow: true, want: true },
	];
	for (const c of cases) {
		test(c.name, () => {
			expect(mayChange(c.day(), c.yesterday, c.shadow)).toBe(c.want);
		});
	}
});

describe('the last session, as `pairtime recap` heads it', () => {
	// In UTC, as web.yml's runners and the specs run (vitest.config.ts).
	const cases: { name: string; check: () => void }[] = [
		{
			name: 'a session that ended gives its day, start and end',
			check: () => {
				expect(sessionHead(session(), 'rusty-cli', false)).toBe('Your last session: rusty-cli');
				expect(sessionWhen(session(), false)).toBe('Friday, November 20, 14:05 to 14:14');
			},
		},
		{
			name: 'a session still going says since when',
			check: () => {
				expect(sessionHead(session({ open: true }), 'rusty-cli', false)).toBe('Your session so far: rusty-cli');
				expect(sessionWhen(session({ open: true }), false)).toBe('Friday, November 20, since 14:05');
			},
		},
		{
			name: "Cursor's turns fell inside credited minutes, so the session's clock is your time alone",
			check: () => {
				const v = buildProject('p_rusty', 'rusty-cli', session(), false);
				expect(clockLine(v)).toBe('you 9m');
				expect(v.you?.kinds.map((k) => k.words)).toContain("reading Cursor's answers");
			},
		},
		{
			name: 'calibrating, a session is its day alone, from its date',
			check: () => {
				expect(sessionHead(session({ shadow: true }), 'rusty-cli', true)).toBe('Your last session: rusty-cli');
				expect(sessionWhen(session({ shadow: true }), true)).toBe('Friday, November 20');
			},
		},
		{
			name: 'calibrating hides a start and end the agent sent, and whether it goes on',
			check: () => {
				expect(sessionHead(session({ open: true }), 'rusty-cli', true)).toBe('Your last session: rusty-cli');
				expect(sessionWhen(session({ open: true }), true)).toBe('Friday, November 20');
			},
		},
		{
			name: 'a session with no date takes its day from its start, in this zone',
			check: () => {
				const { date: _, ...undated } = session();
				expect(sessionWhen(undated, true)).toBe('Friday, November 20');
			},
		},
		{
			name: "a session with neither a date nor a start that parses has no day line, and doesn't throw",
			check: () => {
				const { date: _, ...undated } = session({ shadow: true });
				expect(sessionWhen(undated, true)).toBe('');
				expect(sessionWhen({ ...undated, start: 'soon', end: 'later' }, false)).toBe('');
			},
		},
	];
	for (const c of cases) {
		test(c.name, c.check);
	}
});

describe('bars', () => {
	const cases: { name: string; check: () => void }[] = [
		{
			name: 'your bar is the final time by level, then the pending part, and adds up to your time',
			check: () => {
				const you = whole([friday], false, 'today').you;
				if (!you) {
					throw new Error('no time of yours');
				}
				const parts = yourParts(you);
				expect(parts.map(([p]) => p)).toEqual(['A', 'B', 'C', 'pending']);
				expect(parts.at(-1)).toEqual(['pending', 540]);
				expect(Math.round(parts.reduce((a, [, s]) => a + s, 0))).toBe(12420);
			},
		},
		{
			name: 'parts sit side by side, each as wide as its share of the scale',
			check: () =>
				expect(segments([['A', 30], ['B', 10], ['C', 0], ['pending', 10]], 100)).toEqual([
					{ part: 'A', x: 0, width: 30 },
					{ part: 'B', x: 30, width: 10 },
					{ part: 'pending', x: 40, width: 10 },
				]),
		},
		{
			name: 'nothing runs past the end of the bar',
			check: () =>
				expect(segments([['A', 80], ['pending', 80]], 100)).toEqual([
					{ part: 'A', x: 0, width: 80 },
					{ part: 'pending', x: 80, width: 20 },
				]),
		},
		{ name: 'a scale of nothing draws nothing', check: () => expect(segments([['A', 10]], 0)).toEqual([]) },
		{
			name: "a page's scale is its longest hand, yours or an agent's",
			check: () => {
				expect(scaleOf(projects([friday], false))).toBe(3120);
				expect(scaleOf(projects([dayReply('2026-11-16')], false))).toBe(2400);
			},
		},
	];
	for (const c of cases) {
		test(c.name, c.check);
	}
});

describe('dates', () => {
	const cases: { name: string; check: () => void }[] = [
		{ name: "this week starts on Monday", check: () => expect(monday('2026-11-20')).toBe('2026-11-16') },
		{ name: 'on a Monday the week starts that day', check: () => expect(monday('2026-11-16')).toBe('2026-11-16') },
		{ name: "a Sunday's week started six days before", check: () => expect(monday('2026-11-22')).toBe('2026-11-16') },
		{ name: 'the last 30 days start 29 days back, across months', check: () => expect(addDays('2026-11-20', -29)).toBe('2026-10-22') },
		{ name: 'a span lists every date in it', check: () => expect(dates('2026-11-30', '2026-12-02')).toEqual(['2026-11-30', '2026-12-01', '2026-12-02']) },
		{ name: 'a span that ends before it starts is empty', check: () => expect(dates('2026-11-20', '2026-11-19')).toEqual([]) },
		{ name: 'today and yesterday go by those names', check: () => expect([dayLabel('2026-11-20', '2026-11-20'), dayLabel('2026-11-19', '2026-11-20')]).toEqual(['Today', 'Yesterday']) },
		{ name: 'an earlier day goes by its weekday and date', check: () => expect(dayLabel('2026-11-16', '2026-11-20')).toBe('Monday, November 16') },
	];
	for (const c of cases) {
		test(c.name, c.check);
	}
});

describe("a project's name on the owner's dashboard", () => {
	const states: { name: string; state: NameState; want: boolean }[] = [
		{ name: 'a name the dashboard shows has its switch on', state: 'on', want: true },
		{ name: 'a name on its way has its switch on', state: 'sending', want: true },
		{ name: 'a name on its way off has its switch off', state: 'removing', want: false },
		{ name: 'a name hidden on the dashboard has its switch off', state: 'hidden', want: false },
		{ name: 'a name off has its switch off', state: 'off', want: false },
	];
	for (const c of states) {
		test(c.name, () => {
			expect(nameShown(c.state)).toBe(c.want);
		});
	}
	const entry = (over: Partial<ProjectEntry>): ProjectEntry => ({ project: 'p_x', code: 'p_x', local_name: 'x-cli', state: 'off', label: 'x-cli', sent: false, ...over });
	const view = (key: string, name: string) => buildProject(key, name, { agent_only_s: 0, agent_only_by_harness: {}, uncredited: {} }, false);
	const titles: { name: string; got: () => string; want: string }[] = [
		{ name: "a project the day names keeps the day's name", got: () => projectTitle(view('p_x', 'from-the-day'), entry({})), want: 'from-the-day' },
		{ name: 'a project the day leaves unnamed takes its local name', got: () => projectTitle(view('p_x', ''), entry({})), want: 'x-cli' },
		{
			name: "one with no local name either is called by its code, as the owner's dashboard calls it",
			got: () => projectTitle(view('p_x', ''), entry({ local_name: '' })),
			want: 'Project p_x',
		},
		{
			name: 'one with no local name and no code from the server is called by its key',
			got: () => projectTitle(view('p_x', ''), entry({ local_name: '', code: '' })),
			want: 'p_x',
		},
		{ name: 'one GET /v1/projects lacks is called by its key', got: () => projectTitle(view('p_x', ''), undefined), want: 'p_x' },
		{ name: 'the other projects are called by their local names', got: () => entryTitle(entry({})), want: 'x-cli' },
		{ name: 'or by their codes', got: () => entryTitle(entry({ local_name: '' })), want: 'Project p_x' },
		{ name: 'or by their keys while the server has given them no code', got: () => entryTitle(entry({ local_name: '', code: '' })), want: 'p_x' },
		{ name: 'a local name wins over a missing code too', got: () => entryTitle(entry({ code: '' })), want: 'x-cli' },
		{ name: 'show-name takes the code the server gave', got: () => projectArg(entry({ code: 'p_x1' })), want: 'p_x1' },
		{ name: 'and the key while it gave none', got: () => projectArg(entry({ code: '' })), want: 'p_x' },
		{
			name: "a local name's control characters can't reach the page",
			got: () => entryTitle(entry({ local_name: `a${String.fromCodePoint(0x202e)}b` })),
			want: `a${String.fromCodePoint(0xfffd)}b`,
		},
	];
	for (const c of titles) {
		test(c.name, () => {
			expect(c.got()).toBe(c.want);
		});
	}
	test("the fake agent's list has one project of each state the page shows", () => {
		expect(new Set(projectsReply().projects.map((p) => p.state))).toEqual(new Set(['on', 'sending', 'off', 'hidden']));
	});
});
