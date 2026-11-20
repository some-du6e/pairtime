// SPDX-License-Identifier: AGPL-3.0-or-later

// What the fake agent answers, for vitest and Playwright, in the shapes
// `pairtime today` and `recap` read (cmd/pairtime/localapi.go): a week of
// Sam's from SM 6.3, whose Friday is the five-project afternoon with SM's
// credited totals (207 minutes) and agent-only times (93 minutes), a little
// more for the last 30 days, and a last work session. Thursday has a Claude
// session with no time of its own, which agent_only_by_harness lists at 0,
// so its reading still says Claude's name. tests/support/fake.test.ts holds
// every answer to docs/api/local.openapi.yaml and to the sums it promises.

import type { components } from '../../src/gen/local-api.ts';

type Schemas = components['schemas'];
type Day = Schemas['Day'];
type DayProject = Schemas['DayProject'];

/** The fake agent's today, a Friday. */
export const today = '2026-11-20';

const names: Record<string, string> = {
	p_portfolio: 'portfolio',
	p_bot: 'discord-bot',
	p_homework: 'homework-helper',
	p_plant: 'plant-sensor',
	p_rusty: 'rusty-cli',
};

interface Spec {
	project: string;
	byEvidence: Schemas['ByEvidence'];
	pending?: number;
	byKind: Schemas['ByKind'];
	agents?: Schemas['AgentOnlyByHarness'];
	/** Uncredited seconds other than the agent's, which X.agent_only adds. */
	other?: Schemas['Uncredited'];
}

/** A project's day, with credited_s and agent_only_s added up from the parts. */
function project(s: Spec): DayProject {
	const credited = (s.byEvidence.A ?? 0) + (s.byEvidence.B ?? 0) + (s.byEvidence.C ?? 0);
	const agents = s.agents ?? {};
	const agentOnly = Object.values(agents).reduce((a: number, b) => a + (b ?? 0), 0);
	return {
		project: s.project,
		name: names[s.project] ?? '',
		credited_s: credited,
		pending_s: s.pending ?? 0,
		credited_by_kind: s.byKind,
		credited_by_evidence: s.byEvidence,
		agent_only_s: agentOnly,
		agent_only_by_harness: agents,
		uncredited: agentOnly > 0 ? { ...s.other, 'X.agent_only': agentOnly } : { ...s.other },
	};
}

function day(date: string, specs: Spec[], shadow = false): Day {
	return { date, shadow, projects: specs.map(project) };
}

const days: Record<string, Day> = {
	'2026-10-27': day('2026-10-27', [{ project: 'p_plant', byEvidence: { A: 2100 }, byKind: { hand_edit: 2100 } }]),
	'2026-11-03': day('2026-11-03', [
		{ project: 'p_rusty', byEvidence: { A: 1500 }, byKind: { hand_edit: 900, testing: 600 }, agents: { cursor: 600 } },
	]),
	'2026-11-10': day('2026-11-10', [
		{
			project: 'p_portfolio',
			byEvidence: { A: 2400 },
			byKind: { prompting: 1500, reading_output: 900 },
			agents: { claude_code: 3000 },
			other: { 'X.same_error': 120 },
		},
	]),
	'2026-11-16': day('2026-11-16', [
		{
			project: 'p_portfolio',
			byEvidence: { A: 1800 },
			byKind: { prompting: 1200, reading_output: 600 },
			agents: { claude_code: 2400 },
			other: { 'X.absent': 600 },
		},
	]),
	'2026-11-17': day('2026-11-17', []),
	'2026-11-18': day('2026-11-18', [
		{
			project: 'p_bot',
			byEvidence: { B: 2700 },
			byKind: { prompting: 1500, reading_output: 900, supervising: 300 },
			agents: { codex: 600 },
			other: { 'X.reroll': 240 },
		},
		{ project: 'p_rusty', byEvidence: { A: 900 }, byKind: { hand_edit: 900 }, agents: { cursor: 300 } },
	]),
	'2026-11-19': day('2026-11-19', [
		{ project: 'p_plant', byEvidence: { A: 1200 }, byKind: { hand_edit: 1200 } },
		{
			project: 'p_homework',
			byEvidence: { A: 300, C: 300 },
			byKind: { prompting: 300, reading_output: 300 },
			agents: { copilot: 900 },
			other: { 'X.noprog': 150 },
		},
		// Reading what Claude wrote the night before: a Claude session, and no
		// time of Claude's own today.
		{ project: 'p_portfolio', byEvidence: { A: 600 }, byKind: { reading_output: 600 }, agents: { claude_code: 0 } },
	]),
	// SM 6.3's afternoon, with 9 minutes still pending.
	'2026-11-20': day('2026-11-20', [
		{
			project: 'p_portfolio',
			byEvidence: { A: 3120 },
			pending: 240,
			byKind: { prompting: 1500, reading_output: 1080, supervising: 540 },
			agents: { claude_code: 2460 },
			other: { 'X.absent': 900 },
		},
		{
			project: 'p_bot',
			byEvidence: { A: 1800, B: 480 },
			byKind: { prompting: 1200, reading_output: 780, supervising: 300 },
			agents: { codex: 1320 },
			other: { 'X.other_focus': 360 },
		},
		{
			project: 'p_homework',
			byEvidence: { A: 2160, C: 300 },
			byKind: { prompting: 900, reviewing: 600, hand_edit: 960 },
			agents: { copilot: 1080 },
			other: { 'X.no_anchor': 180 },
		},
		{ project: 'p_plant', byEvidence: { A: 1740 }, byKind: { hand_edit: 1740 } },
		{
			project: 'p_rusty',
			byEvidence: { A: 2820 },
			pending: 300,
			byKind: { hand_edit: 1620, testing: 600, prompting: 360, reviewing: 240 },
			agents: { cursor: 720 },
			other: { 'X.lookup_cap': 300 },
		},
	]),
};

/**
 * GET /v1/days/{date}: date is YYYY-MM-DD or "today". A day the fixture
 * has nothing for is empty, as the agent answers for it.
 */
export function dayReply(date: string, shadow = false): Day {
	const d = date === 'today' ? today : date;
	const found = days[d] ?? day(d, []);
	return { ...found, shadow };
}

/** Today with every pending minute turned final. */
export function todayFinal(): Day {
	const t = dayReply('today');
	return { ...t, projects: t.projects.map((p) => ({ ...p, pending_s: 0 })) };
}

/** GET /v1/sessions/last: rusty-cli's nine minutes this afternoon, or none before the first. */
export function lastSession(options: { shadow?: boolean; none?: boolean; open?: boolean } = {}): Schemas['LastSession'] {
	if (options.none) {
		return { shadow: options.shadow ?? false, session: null };
	}
	return {
		shadow: options.shadow ?? false,
		session: {
			project: 'p_rusty',
			name: 'rusty-cli',
			start: '2026-11-20T14:05:00Z',
			end: '2026-11-20T14:14:00Z',
			open: options.open ?? false,
			credited_s: 540,
			pending_s: 0,
			credited_by_kind: { hand_edit: 420, testing: 90, prompting: 20, reading_output: 10 },
			credited_by_evidence: { A: 540 },
			agent_only_s: 60,
			agent_only_by_harness: { cursor: 60 },
			uncredited: { 'X.agent_only': 60 },
		},
	};
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

/** The dates the fixture has, for the tests that check every answer. */
export const fixtureDates = Object.keys(days);
