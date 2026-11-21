// SPDX-License-Identifier: AGPL-3.0-or-later

// What the fake agent answers, for vitest and Playwright, in the shapes
// docs/api/local.openapi.yaml gives GET /v1/days/{date} and GET
// /v1/sessions/last: a week of Sam's from SM 6.3, whose Friday is the
// five-project afternoon with SM's credited totals (207 minutes), each
// project's agent time (93 minutes) and the 33 uncredited minutes, a
// little more for the last 30 days, and a last work session. A project's
// agent time is SM 5.3's agent_only_s, the seconds its turns ran that it
// didn't credit. On SM 6.3's afternoon most of it another project credited,
// and only the small part nothing credited is X.agent_only; on a day with
// one project nothing else credited it, so all of it is uncredited. Thursday has a Claude
// session with no time of its own, which agent_only_by_harness lists at
// 0, so its reading still says Claude's name, and minutes past two hours
// without progress, some still on hold. While display.shadow is on the
// agent leaves out what D-shadow-scope hides (shadowDay, shadowSession).
// tests/support/fake.test.ts holds every answer to the spec and to the
// sums it promises.

import type { components } from '../../src/gen/local-api.ts';
import type { Focus } from '../../src/local/api.ts';

type Schemas = components['schemas'];
type Day = Schemas['Day'];
type DayProject = Schemas['DayProject'];
type LastSession = Schemas['LastSession'];
type Uncredited = Schemas['Uncredited'];

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
	/** The project's agent time, agent_only_s, by harness. */
	agents?: Schemas['AgentOnlyByHarness'];
	/** The part of the agent time that no project credited, X.agent_only. */
	agentOnly?: number;
	/** Uncredited seconds other than X.agent_only. */
	other?: Uncredited;
	/** The part of X.noprog that progress can still bring back. */
	onHold?: number;
}

/** A project's day, with credited_s and agent_only_s added up from the parts. */
function project(s: Spec): DayProject {
	const credited = (s.byEvidence.A ?? 0) + (s.byEvidence.B ?? 0) + (s.byEvidence.C ?? 0);
	const agents = s.agents ?? {};
	const agentOnly = Object.values(agents).reduce((a: number, b) => a + (b ?? 0), 0);
	const p: DayProject = {
		project: s.project,
		name: names[s.project] ?? '',
		credited_s: credited,
		pending_s: s.pending ?? 0,
		credited_by_kind: s.byKind,
		credited_by_evidence: s.byEvidence,
		agent_only_s: agentOnly,
		agent_only_by_harness: agents,
		uncredited: s.agentOnly ? { ...s.other, 'X.agent_only': s.agentOnly } : { ...s.other },
	};
	if (s.onHold !== undefined) {
		p.on_hold_s = s.onHold;
	}
	return p;
}

/** The start of date's day, and of the next, in UTC: the fake agent's zone is UTC. */
function bounds(date: string): { start: string; end: string } {
	const t = Date.parse(`${date}T00:00:00Z`);
	const next = Number.isNaN(t) ? date : new Date(t + 86_400_000).toISOString().slice(0, 10);
	return { start: `${date}T00:00:00Z`, end: `${next}T00:00:00Z` };
}

function day(date: string, specs: Spec[]): Day {
	return { date, zone: 'Local', ...bounds(date), shadow: false, projects: specs.map(project) };
}

const days: Record<string, Day> = {
	'2026-10-27': day('2026-10-27', [{ project: 'p_plant', byEvidence: { A: 2100 }, byKind: { hand_edit: 2100 } }]),
	'2026-11-03': day('2026-11-03', [
		{
			project: 'p_rusty',
			byEvidence: { A: 1500 },
			byKind: { hand_edit: 900, testing: 600 },
			agents: { cursor: 600 },
			// The day's only project, so nothing else credited Cursor's
			// seconds: all 600 are uncredited.
			agentOnly: 600,
		},
	]),
	'2026-11-10': day('2026-11-10', [
		{
			project: 'p_portfolio',
			byEvidence: { A: 2400 },
			byKind: { prompting: 1500, reading_output: 900 },
			agents: { claude_code: 3000 },
			// The day's only project: Claude's 3,000 s are all uncredited,
			// 120 of them while the same error came back.
			agentOnly: 2880,
			other: { 'X.same_error': 120 },
		},
	]),
	'2026-11-16': day('2026-11-16', [
		{
			project: 'p_portfolio',
			byEvidence: { A: 1800 },
			byKind: { prompting: 1200, reading_output: 600 },
			agents: { claude_code: 2400 },
			// The day's only project: Claude's 2,400 s are all uncredited,
			// 600 of them while you were away.
			agentOnly: 1800,
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
			agentOnly: 60,
			// Past two hours without progress: 150 s the half rate didn't
			// credit, 90 of them in minutes still provisional, which progress
			// within PROGRESS_RESTORE_S can still bring back (SM 4.3). This
			// bends SM 4.3, so that a page shows the split: Friday credits
			// homework-helper at full weight, which takes progress, and an
			// agent that applies the rule would have restored these by then.
			other: { 'X.noprog': 150 },
			onHold: 90,
		},
		// Reading what Claude wrote the night before: a Claude session, and no
		// time of Claude's own today.
		{ project: 'p_portfolio', byEvidence: { A: 600 }, byKind: { reading_output: 600 }, agents: { claude_code: 0 } },
	]),
	// SM 6.3's afternoon, with 9 minutes still pending. The 33 uncredited
	// minutes are SM's: away 15, other apps 6, past the lookup cap 5,
	// X.agent_only 4 and between steps 3.
	'2026-11-20': day('2026-11-20', [
		{
			project: 'p_portfolio',
			byEvidence: { A: 3120 },
			pending: 240,
			byKind: { prompting: 1500, reading_output: 1080, supervising: 540 },
			agents: { claude_code: 2460 },
			agentOnly: 120,
			other: { 'X.absent': 900 },
		},
		{
			project: 'p_bot',
			byEvidence: { A: 1800, B: 480 },
			byKind: { prompting: 1200, reading_output: 780, supervising: 300 },
			agents: { codex: 1320 },
			agentOnly: 60,
			other: { 'X.other_focus': 360 },
		},
		{
			project: 'p_homework',
			byEvidence: { A: 2160, C: 300 },
			byKind: { prompting: 900, reviewing: 600, hand_edit: 960 },
			agents: { copilot: 1080 },
			agentOnly: 60,
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
 * The uncredited codes an answer keeps while display.shadow is on
 * (D-shadow-scope): SM 4.2's loops alone, as the agent's tallyOf keeps
 * them (internal/agent/scorer.go).
 */
export const shadowCodes: readonly (keyof Uncredited)[] = ['X.reroll', 'X.rewrite_loop', 'X.same_error', 'X.autopilot'];

/** The fields a tally leaves out while display.shadow is on (D-shadow-scope). */
export const shadowHides = [
	'credited_s',
	'pending_s',
	'credited_by_kind',
	'credited_by_evidence',
	'credited_by_reason',
	'on_hold_s',
] as const;

/** A session's fields that a shadow answer leaves out too (D-shadow-scope). */
export const shadowHidesOfSession = ['start', 'end', 'open'] as const;

function shadowUncredited(u: Uncredited): Uncredited {
	const out: Uncredited = {};
	for (const code of shadowCodes) {
		const s = u[code];
		if (s !== undefined) {
			out[code] = s;
		}
	}
	return out;
}

/**
 * A day as the agent gives it while display.shadow is on (D-shadow-scope):
 * each project's agent time and the uncredited codes shadowCodes keeps, and
 * nothing credited.
 */
export function shadowDay(d: Day): Day {
	return {
		date: d.date,
		zone: d.zone,
		start: d.start,
		end: d.end,
		shadow: true,
		projects: d.projects.map((p) => ({
			project: p.project,
			name: p.name,
			agent_only_s: p.agent_only_s,
			agent_only_by_harness: { ...p.agent_only_by_harness },
			uncredited: shadowUncredited(p.uncredited),
		})),
	};
}

/**
 * The last session as the agent gives it while display.shadow is on
 * (D-shadow-scope): its project, name and local date, and the tally
 * shadowDay keeps, with no start, end or open.
 */
export function shadowSession(r: LastSession): LastSession {
	const s = r.session;
	if (!s) {
		return { shadow: true, session: null };
	}
	return {
		shadow: true,
		session: {
			project: s.project,
			name: s.name,
			date: s.date,
			agent_only_s: s.agent_only_s,
			agent_only_by_harness: { ...s.agent_only_by_harness },
			uncredited: shadowUncredited(s.uncredited),
		},
	};
}

/**
 * GET /v1/days/{date}: date is YYYY-MM-DD or "today". A day the fixture
 * has nothing for is empty, as the agent answers for it. Each call gives a
 * copy of its own, so a test can change it.
 */
export function dayReply(date: string, shadow = false): Day {
	const d = date === 'today' ? today : date;
	const found = structuredClone(days[d] ?? day(d, []));
	return shadow ? shadowDay(found) : found;
}

/** Today with every pending minute turned final. */
export function todayFinal(): Day {
	const t = dayReply('today');
	return { ...t, projects: t.projects.map((p) => ({ ...p, pending_s: 0 })) };
}

/**
 * GET /v1/sessions/last: rusty-cli's nine minutes this afternoon, or none
 * before the first. Cursor's turns fall inside credited minutes, so
 * Cursor has no time of its own in it (testdata/timelines/6.3's
 * agent_only_s for rusty-cli is 0), and agent_only_by_harness names it at
 * 0, so its reading says Cursor's name.
 */
export function lastSession(options: { shadow?: boolean; none?: boolean; open?: boolean } = {}): LastSession {
	const reply: LastSession = options.none
		? { shadow: false, session: null }
		: {
				shadow: false,
				session: {
					project: 'p_rusty',
					name: 'rusty-cli',
					date: '2026-11-20',
					start: '2026-11-20T14:05:00Z',
					end: '2026-11-20T14:14:00Z',
					open: options.open ?? false,
					credited_s: 540,
					pending_s: 0,
					credited_by_kind: { hand_edit: 420, testing: 90, prompting: 20, reading_output: 10 },
					credited_by_evidence: { A: 540 },
					agent_only_s: 0,
					agent_only_by_harness: { cursor: 0 },
					uncredited: {},
				},
			};
	return options.shadow ? shadowSession(reply) : reply;
}

/**
 * GET /v1/status's signals (WP-OS9) from a Linux desktop on X11, where each
 * has a source, or with focus as given: unknown while focus.known is false,
 * as on GNOME on Wayland without pairtime's extension, with the GNOME
 * extension's state and WP-SC8's level-C flag when focus has them.
 */
export function signalsFor(focus: Focus = { known: true }): Schemas['Signals'] {
	const s: Schemas['Signals'] = {
		idle: { via: 'MIT-SCREEN-SAVER' },
		focus: focus.known ? { via: '_NET_ACTIVE_WINDOW' } : { unknown: 'this Wayland desktop tells other programs nothing about the window in front' },
		power: { via: 'logind PrepareForSleep' },
		lock: { via: 'logind Lock and Unlock' },
		session: focus.known ? 'x11' : 'wayland',
	};
	if (focus.gnome_extension !== undefined) {
		s.gnome_extension = focus.gnome_extension;
	}
	if (focus.level_c !== undefined) {
		s.focus_level_c = focus.level_c;
	}
	return s;
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
		signals: signalsFor(),
		adapters: {
			harnesses: { claude_code: { calls: 118, records: 131, parse_failures: 0, last_record: 1795248000000 } },
			other: { calls: 0, records: 0, parse_failures: 0 },
			waiting: 0,
			git: { watched: 1, changes: 3 },
		},
		...overrides,
	};
}

/** The dates the fixture has, for the tests that check every answer. */
export const fixtureDates = Object.keys(days);
