// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, test } from 'vitest';
import { dayReply } from '../../tests/support/fake-days.ts';
import type { Tally } from './api.ts';
import {
	addDays,
	agentOf,
	buildProject,
	clockLine,
	dates,
	dayLabel,
	monday,
	notes,
	projects,
	scaleOf,
	segments,
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

const friday = dayReply('today');
const thursday = dayReply('2026-11-19');

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
			name: "calibrating leaves your time out and keeps the agent's and the rest",
			check: () => {
				const v = buildProject('p', 'remind-bot', remindBot, true);
				expect(v.you).toBeUndefined();
				expect(clockLine(v)).toBe('you calibrating · Cursor 20s');
				expect(v.other).toHaveLength(1);
			},
		},
		{
			name: 'a project the agent has no name for goes by its key, as the CLI shows it',
			check: () => expect(buildProject('p_be4901c04a84', '', remindBot, false).title).toBe('p_be4901c04a84'),
		},
		{
			name: "a harness listed at 0 still names the agent whose answers were read",
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
				expect(whole([friday], false, 'today').you?.kinds.map((k) => k.words)).toContain("reading your agent's answers");
			},
		},
	];
	for (const c of cases) {
		test(c.name, c.check);
	}
});

describe("SM 6.3's afternoon", () => {
	const cases: { name: string; check: () => void }[] = [
		{
			name: "each project's clock, the most of your time first",
			check: () =>
				expect(projects([friday], false).map((p) => `${p.title}: ${clockLine(p)}`)).toEqual([
					'portfolio: you 52m · Claude 41m',
					'rusty-cli: you 47m · Cursor 12m',
					'homework-helper: you 41m · Copilot 18m',
					'discord-bot: you 38m · Codex 22m',
					'plant-sensor: you 29m',
				]),
		},
		{
			name: "the day's clock adds up SM's 207 minutes of yours and each agent's time",
			check: () =>
				expect(clockLine(whole([friday], false, 'today'))).toBe('you 3h 27m · Claude 41m · Codex 22m · Copilot 18m · Cursor 12m'),
		},
		{
			name: "the day's levels split 100 as the CLI rounds them",
			check: () => expect(whole([friday], false, 'today').you?.levels.map((l) => [l.level, l.share])).toEqual([
				['A', 94],
				['B', 4],
				['C', 2],
			]),
		},
		{
			name: 'the notes say how much is level C and how much may still change',
			check: () => {
				const you = whole([friday], false, 'today').you;
				expect(you && notes(you).map((n) => n.words)).toEqual([
					'5m of it is level C: it counts only on your own dashboard',
					'9m of it may still change',
				]);
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
				});
			},
		},
		{
			name: 'a harness at 0 stays in the sum, so it still names the agent',
			check: () => expect(sum(thursday.projects.filter((p) => p.project === 'p_portfolio')).agent_only_by_harness).toEqual({ claude_code: 0 }),
		},
		{
			name: "the week's clock adds up each agent's days",
			check: () => {
				const week = dates('2026-11-16', '2026-11-20').map((d) => dayReply(d));
				expect(clockLine(whole(week, false, 'this week'))).toBe('you 5h 37m · Claude 1h 21m · Copilot 33m · Codex 32m · Cursor 17m');
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
