// SPDX-License-Identifier: AGPL-3.0-or-later

// What the fake agent answers, for vitest and Playwright: a week of Sam's
// from SM 6.3, whose Friday is the five-project afternoon with SM's own
// totals (207 credited minutes, 93 of the agents', 33 left out), plus a
// little more for the last 30 days. tests/support/fake.test.ts holds every
// answer to docs/api/local.openapi.yaml and to the sums the spec promises,
// so the dashboard is tested against answers the agent may really give.

import type { components } from '../../src/gen/local-api.ts';

type Schemas = components['schemas'];
type Days = Schemas['Days'];
type Day = Schemas['Day'];
type ProjectDay = Schemas['ProjectDay'];

/** The fake agent's today, a Friday. */
export const today = '2026-11-20';

export const projects: Schemas['Project'][] = [
	{ key: 'p_portfolio', name: 'portfolio' },
	{ key: 'p_bot', name: 'discord-bot' },
	{ key: 'p_homework', name: 'homework-helper' },
	{ key: 'p_plant', name: 'plant-sensor' },
	{ key: 'p_rusty', name: 'rusty-cli' },
];

interface Spec {
	project: string;
	A?: number;
	B?: number;
	C?: number;
	pending?: number;
	byKind: Schemas['ByKind'];
	uncredited?: Schemas['Uncredited'];
	agents?: [Schemas['Harness'], number][];
}

/** A project's day, with credited_s and agent_running_s worked out from the parts. */
function projectDay(s: Spec): ProjectDay {
	const byEvidence = { A: s.A ?? 0, B: s.B ?? 0, C: s.C ?? 0 };
	const agents = (s.agents ?? []).map(([harness, running_s]) => ({ harness, running_s }));
	return {
		project: s.project,
		credited_s: byEvidence.A + byEvidence.B + byEvidence.C + (s.pending ?? 0),
		pending_s: s.pending ?? 0,
		by_evidence: byEvidence,
		by_kind: s.byKind,
		uncredited: s.uncredited ?? {},
		// One agent each, so the union is that agent's time.
		agent_running_s: Math.max(0, ...agents.map((a) => a.running_s)),
		agents,
	};
}

function day(date: string, uncredited: Schemas['Uncredited'], specs: Spec[]): Day {
	return { date, uncredited, projects: specs.map(projectDay) };
}

/** Friday: SM 6.3's afternoon, with 9 minutes still pending. */
const friday = day(
	'2026-11-20',
	{ 'X.absent': 900, 'X.other_focus': 360, 'X.lookup_cap': 300, 'X.agent_only': 240, 'X.no_anchor': 180 },
	[
		{
			project: 'p_portfolio',
			A: 2880,
			pending: 240,
			byKind: { prompting: 1500, reading_output: 1080, supervising: 540 },
			uncredited: { 'X.agent_only': 240 },
			agents: [['claude_code', 2460]],
		},
		{
			project: 'p_bot',
			A: 1800,
			B: 480,
			byKind: { prompting: 1200, reading_output: 780, supervising: 300 },
			agents: [['codex', 1320]],
		},
		{
			project: 'p_homework',
			A: 2160,
			C: 300,
			byKind: { prompting: 900, reviewing: 600, hand_edit: 960 },
			uncredited: { 'X.no_anchor': 180 },
			agents: [['copilot', 1080]],
		},
		{ project: 'p_plant', A: 1740, byKind: { hand_edit: 1740 } },
		{
			project: 'p_rusty',
			A: 2520,
			pending: 300,
			byKind: { hand_edit: 1620, testing: 600, prompting: 360, reviewing: 240 },
			uncredited: { 'X.lookup_cap': 300 },
			agents: [['cursor', 720]],
		},
	],
);

const week: Day[] = [
	day('2026-11-16', { 'X.absent': 600, 'X.agent_only': 300 }, [
		{
			project: 'p_portfolio',
			A: 1800,
			byKind: { prompting: 1200, reading_output: 600 },
			uncredited: { 'X.agent_only': 300 },
			agents: [['claude_code', 2400]],
		},
	]),
	day('2026-11-17', {}, []),
	day('2026-11-18', { 'X.reroll': 240, 'X.other_focus': 120 }, [
		{
			project: 'p_bot',
			B: 2700,
			byKind: { prompting: 1500, reading_output: 900, supervising: 300 },
			uncredited: { 'X.reroll': 240 },
			agents: [['codex', 600]],
		},
		{ project: 'p_rusty', A: 900, byKind: { hand_edit: 900 }, agents: [['cursor', 300]] },
	]),
	day('2026-11-19', { 'X.noprog': 150 }, [
		{ project: 'p_plant', A: 1200, byKind: { hand_edit: 1200 } },
		{
			project: 'p_homework',
			A: 300,
			C: 300,
			byKind: { prompting: 300, reading_output: 300 },
			uncredited: { 'X.noprog': 150 },
			agents: [['copilot', 900]],
		},
	]),
	friday,
];

/** GET /v1/days without parameters: this week, Monday to today. */
export function weekDays(overrides: Partial<Days> = {}): Days {
	return {
		today,
		as_of: '2026-11-20T16:42:00Z',
		from: '2026-11-16',
		to: today,
		pending_window_s: 900,
		shadow: false,
		projects,
		days: week,
		...overrides,
	};
}

/** This week with every pending minute turned final at level A. */
export function weekFinal(): Days {
	return weekDays({
		as_of: '2026-11-20T16:58:00Z',
		days: week.map((d) => ({
			...d,
			projects: d.projects.map((p) => ({
				...p,
				pending_s: 0,
				by_evidence: { ...p.by_evidence, A: p.by_evidence.A + p.pending_s },
			})),
		})),
	});
}

/** Days before this week that the last 30 days take in, by date. */
const earlier: Record<string, Day> = {
	'2026-10-27': day('2026-10-27', {}, [{ project: 'p_plant', A: 2100, byKind: { hand_edit: 2100 } }]),
	'2026-11-03': day('2026-11-03', {}, [
		{ project: 'p_rusty', A: 1500, byKind: { hand_edit: 900, testing: 600 }, agents: [['cursor', 600]] },
	]),
	'2026-11-10': day('2026-11-10', { 'X.same_error': 120 }, [
		{
			project: 'p_portfolio',
			A: 2400,
			byKind: { prompting: 1500, reading_output: 900 },
			uncredited: { 'X.same_error': 120 },
			agents: [['claude_code', 3000]],
		},
	]),
};

/** The 30 days up to today, 2026-10-22 to 2026-11-20, as GET /v1/days?from=2026-10-22&to=2026-11-20 gives them. */
export function monthDays(): Days {
	const days: Day[] = [];
	const byDate = new Map(week.map((d) => [d.date, d]));
	for (let t = Date.UTC(2026, 9, 22); t <= Date.UTC(2026, 10, 20); t += 86_400_000) {
		const date = new Date(t).toISOString().slice(0, 10);
		days.push(byDate.get(date) ?? earlier[date] ?? { date, uncredited: {}, projects: [] });
	}
	return weekDays({ from: '2026-10-22', days });
}

/** GET /v1/status from an agent that has run since 08:00. */
export function status(overrides: Partial<Schemas['Status']> = {}): Schemas['Status'] {
	return {
		version: { version: 'v0.1.0', go_version: 'go1.26.0', platform: 'linux/amd64' },
		pid: 4242,
		started: '2026-11-20T08:00:00Z',
		uptime_s: 31320,
		mode: 'local',
		port: 7247,
		wakeups: 522,
		jobs: [{ name: 'inbox-drain', every_s: 60, runs: 522 }],
		ipc: { via: 'socket', stored: 118, duplicates: 0, refused: 0 },
		...overrides,
	};
}
