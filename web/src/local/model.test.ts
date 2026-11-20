// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, test } from 'vitest';
import { weekDays } from '../../tests/support/fake-days.ts';
import type { Day } from './api.ts';
import {
	agentHands,
	clocks,
	combine,
	dayRows,
	kindRows,
	nameOf,
	reasonRows,
	scaleOf,
	segments,
	soleAgent,
	unnamed,
	yourParts,
} from './model.ts';

const week = weekDays();
const friday = week.days.find((d) => d.date === '2026-11-20') as Day;

describe('clocks', () => {
	const cases: { name: string; check: () => void }[] = [
		{
			name: "SM 6.3's afternoon credits A 52, B 38, C 41, D 29 and E 47 minutes, busiest first",
			check: () => {
				const got = clocks([friday], week.projects).map((c) => [c.name, Math.round(c.credited / 60)]);
				expect(got).toEqual([
					['portfolio', 52],
					['rusty-cli', 47],
					['homework-helper', 41],
					['discord-bot', 38],
					['plant-sensor', 29],
				]);
			},
		},
		{
			name: "each project's agent time is its agent's, named after the agent",
			check: () => {
				const got = clocks([friday], week.projects).map((c) => [c.name, agentHands(c).map((h) => [h.name, h.running / 60])]);
				expect(got).toEqual([
					['portfolio', [['Claude', 41]]],
					['rusty-cli', [['Cursor', 12]]],
					['homework-helper', [['Copilot', 18]]],
					['discord-bot', [['Codex', 22]]],
					['plant-sensor', []],
				]);
			},
		},
		{
			name: 'while calibrating, clocks go by name so their order hides the numbers',
			check: () => {
				expect(clocks([friday], week.projects, true).map((c) => c.name)).toEqual([
					'discord-bot',
					'homework-helper',
					'plant-sensor',
					'portfolio',
					'rusty-cli',
				]);
			},
		},
		{
			name: "a project's days add up, by level, by kind and by reason",
			check: () => {
				const bot = clocks(week.days, week.projects).find((c) => c.key === 'p_bot');
				expect(bot).toMatchObject({
					credited: 4980,
					pending: 0,
					evidence: { A: 1800, B: 3180, C: 0 },
					byKind: { prompting: 2700, reading_output: 1680, supervising: 600 },
					uncredited: { 'X.reroll': 240 },
					agentRunning: 1920,
					agents: [{ harness: 'codex', running: 1920 }],
				});
			},
		},
		{
			name: 'final seconds by level and pending seconds add up to your time',
			check: () => {
				for (const c of clocks(week.days, week.projects)) {
					expect(c.evidence.A + c.evidence.B + c.evidence.C + c.pending, c.name).toBe(c.credited);
				}
			},
		},
		{
			name: 'a project without a name is never called by its key',
			check: () => {
				expect(nameOf('p_portfolio', [])).toBe(unnamed);
				expect(nameOf('p_x', [{ key: 'p_x', name: '  ' }])).toBe(unnamed);
				expect(clocks([friday], []).map((c) => c.name)).toEqual(Array(5).fill(unnamed));
			},
		},
	];
	for (const c of cases) {
		test(c.name, c.check);
	}
});

describe('combine', () => {
	const cases: { name: string; check: () => void }[] = [
		{
			name: "the day's clock is SM 6.3's 207 credited minutes beside the agents' 93",
			check: () => {
				const all = combine(clocks([friday], week.projects));
				expect(all.credited / 60).toBe(207);
				expect(all.agentRunning / 60).toBe(93);
				expect(all.pending / 60).toBe(9);
				expect(all.evidence).toEqual({ A: 11100, B: 480, C: 300 });
			},
		},
		{
			name: "the day's agents each keep their own hand, longest first",
			check: () => {
				const hands = agentHands(combine(clocks([friday], week.projects)));
				expect(hands.map((h) => [h.name, h.running / 60])).toEqual([
					['Claude', 41],
					['Codex', 22],
					['Copilot', 18],
					['Cursor', 12],
				]);
			},
		},
		{
			name: 'agent time the agent couldn\'t split by agent goes on one hand, "Your agents"',
			check: () => {
				const day: Day = {
					date: '2026-11-20',
					uncredited: {},
					projects: [
						{
							project: 'p',
							credited_s: 60,
							pending_s: 0,
							by_evidence: { A: 60, B: 0, C: 0 },
							by_kind: { prompting: 60 },
							uncredited: {},
							agent_running_s: 300,
							agents: [],
						},
					],
				};
				const [only] = clocks([day], []);
				expect(only && agentHands(only)).toEqual([{ key: 'agents', name: 'Your agents', running: 300 }]);
			},
		},
	];
	for (const c of cases) {
		test(c.name, c.check);
	}
});

describe('dayRows', () => {
	const cases: { name: string; check: () => void }[] = [
		{
			name: 'the week lists only days with time on them, so it never shows a gap to fill',
			check: () => {
				expect(dayRows(week.days).map((r) => r.date)).toEqual(['2026-11-16', '2026-11-18', '2026-11-19', '2026-11-20']);
			},
		},
		{
			name: "each day's row is that day's clock",
			check: () => {
				const rows = dayRows(week.days).map((r) => [r.date, r.clock.credited / 60, r.clock.agentRunning / 60]);
				expect(rows).toEqual([
					['2026-11-16', 30, 40],
					['2026-11-18', 60, 15],
					['2026-11-19', 30, 15],
					['2026-11-20', 207, 93],
				]);
			},
		},
	];
	for (const c of cases) {
		test(c.name, c.check);
	}
});

describe('rows in plain words', () => {
	const cases: { name: string; check: () => void }[] = [
		{
			name: "what pairtime didn't count comes most first, and only reasons with time",
			check: () => {
				expect(reasonRows(friday.uncredited).map((r) => [r.label, r.seconds])).toEqual([
					['Away', 900],
					['In another app', 360],
					['Long lookups', 300],
					['The agent working on its own', 240],
					['Nothing to count it from', 180],
				]);
			},
		},
		{
			name: "a project's agent-only time is named after its one agent",
			check: () => {
				const portfolio = clocks([friday], week.projects).find((c) => c.key === 'p_portfolio');
				expect(portfolio && soleAgent(portfolio)).toBe('Claude');
				expect(reasonRows({ 'X.agent_only': 240 }, 'Claude').map((r) => r.label)).toEqual(['Claude working on its own']);
			},
		},
		{
			name: 'your time by kind comes most first, in plain words',
			check: () => {
				expect(kindRows({ hand_edit: 1620, testing: 600, prompting: 360, reviewing: 240 }).map((r) => r.label)).toEqual([
					'Editing code yourself',
					'Running tests and builds',
					'Writing prompts',
					'Reviewing and deciding',
				]);
			},
		},
		{
			name: 'a kind or reason with no time leaves no row',
			check: () => {
				expect(kindRows({ lookup: 0 })).toEqual([]);
				expect(reasonRows({ 'X.day_cap': 0 })).toEqual([]);
			},
		},
	];
	for (const c of cases) {
		test(c.name, c.check);
	}
});

describe('segments', () => {
	const cases: { name: string; check: () => void }[] = [
		{
			name: 'parts sit side by side, each as wide as its share of the scale',
			check: () => {
				expect(segments(yourParts({ evidence: { A: 30, B: 10, C: 0 }, pending: 10 }), 100)).toEqual([
					{ part: 'A', x: 0, width: 30 },
					{ part: 'B', x: 30, width: 10 },
					{ part: 'pending', x: 40, width: 10 },
				]);
			},
		},
		{
			name: 'pending comes last, after the final levels',
			check: () => {
				const parts = segments(yourParts({ evidence: { A: 1, B: 1, C: 1 }, pending: 1 }), 4).map((s) => s.part);
				expect(parts).toEqual(['A', 'B', 'C', 'pending']);
			},
		},
		{
			name: 'nothing runs past the end of the bar',
			check: () => {
				const drawn = segments([['A', 80], ['pending', 80]], 100);
				expect(drawn).toEqual([
					{ part: 'A', x: 0, width: 80 },
					{ part: 'pending', x: 80, width: 20 },
				]);
			},
		},
		{
			name: 'a scale of nothing draws nothing, rather than dividing by zero',
			check: () => {
				expect(segments([['A', 10]], 0)).toEqual([]);
			},
		},
		{
			name: "a page's scale is its longest hand, yours or an agent's",
			check: () => {
				expect(scaleOf(clocks([friday], week.projects))).toBe(3120);
				expect(scaleOf(clocks(week.days.slice(0, 1), week.projects))).toBe(2400);
			},
		},
	];
	for (const c of cases) {
		test(c.name, c.check);
	}
});
