// SPDX-License-Identifier: AGPL-3.0-or-later

// The dashboard's views of GET /v1/days: each project's chess clock summed
// over a span of days, the day's and the week's clocks across projects,
// the reasons and kinds in plain words, and the geometry of every bar.
// Pure functions over the API's types, so vitest checks the arithmetic the
// pages show.

import type { ByEvidence, Day, Harness, Kind, Project, ProjectDay, Reason } from './api.ts';
import { agentOnly, kinds, reasons } from './copy.ts';
import { agentName } from './format.ts';

/** What the page calls a project the agent has no name for. */
export const unnamed = 'A project without a name';

/** One project's chess clock over a span of days (DESIGN 3.8), or several projects' together. */
export interface ProjectClock {
	key: string;
	name: string;
	/** Your time, pending minutes included. */
	credited: number;
	/** The part of credited that isn't final yet. */
	pending: number;
	/** The final part of credited, by level. */
	evidence: ByEvidence;
	byKind: Partial<Record<Kind, number>>;
	uncredited: Partial<Record<Reason, number>>;
	/** Seconds in which any agent of the project ran; across projects, each project's added up. */
	agentRunning: number;
	/** Each agent's time, longest first. */
	agents: { harness: Harness; running: number }[];
}

/** A project's name, or what the page calls one without a name. */
export function nameOf(key: string, projects: Project[]): string {
	const name = projects.find((p) => p.key === key)?.name ?? '';
	return name.trim() === '' ? unnamed : name;
}

function newClock(key: string, name: string): ProjectClock {
	return {
		key,
		name,
		credited: 0,
		pending: 0,
		evidence: { A: 0, B: 0, C: 0 },
		byKind: {},
		uncredited: {},
		agentRunning: 0,
		agents: [],
	};
}

function addTo<K extends string>(sum: Partial<Record<K, number>>, more: Partial<Record<K, number>>): void {
	for (const [k, v] of Object.entries(more) as [K, number | undefined][]) {
		if (typeof v === 'number' && v > 0) {
			sum[k] = (sum[k] ?? 0) + v;
		}
	}
}

function addAgent(c: ProjectClock, harness: Harness, running: number): void {
	const same = c.agents.find((x) => x.harness === harness);
	if (same) {
		same.running += running;
	} else {
		c.agents.push({ harness, running });
	}
}

function addDay(c: ProjectClock, pd: ProjectDay): void {
	c.credited += pd.credited_s;
	c.pending += pd.pending_s;
	c.evidence = {
		A: c.evidence.A + pd.by_evidence.A,
		B: c.evidence.B + pd.by_evidence.B,
		C: c.evidence.C + pd.by_evidence.C,
	};
	addTo(c.byKind, pd.by_kind);
	addTo(c.uncredited, pd.uncredited);
	c.agentRunning += pd.agent_running_s;
	for (const a of pd.agents) {
		addAgent(c, a.harness, a.running_s);
	}
}

function sortAgents(c: ProjectClock): ProjectClock {
	c.agents.sort((a, b) => b.running - a.running || a.harness.localeCompare(b.harness));
	return c;
}

/**
 * Every project the days name, summed into one clock each. The busiest
 * come first: most of your time, then most agent time. While shadow is on
 * they go by name instead, so the order doesn't give away the hidden
 * numbers (DESIGN 6.7).
 */
export function clocks(days: Day[], projects: Project[], shadow = false): ProjectClock[] {
	const byKey = new Map<string, ProjectClock>();
	for (const day of days) {
		for (const pd of day.projects) {
			let c = byKey.get(pd.project);
			if (!c) {
				c = newClock(pd.project, nameOf(pd.project, projects));
				byKey.set(pd.project, c);
			}
			addDay(c, pd);
		}
	}
	return [...byKey.values()]
		.map(sortAgents)
		.sort(
			(a, b) =>
				(shadow ? 0 : b.credited - a.credited || b.agentRunning - a.agentRunning) ||
				a.name.localeCompare(b.name) ||
				a.key.localeCompare(b.key),
		);
}

/**
 * Several clocks as one, such as all of today's projects: your time added
 * up, and each agent's time added up across the projects it ran in.
 */
export function combine(cs: ProjectClock[], key = 'all', name = 'All projects'): ProjectClock {
	const all = newClock(key, name);
	for (const c of cs) {
		all.credited += c.credited;
		all.pending += c.pending;
		all.evidence = {
			A: all.evidence.A + c.evidence.A,
			B: all.evidence.B + c.evidence.B,
			C: all.evidence.C + c.evidence.C,
		};
		addTo(all.byKind, c.byKind);
		addTo(all.uncredited, c.uncredited);
		all.agentRunning += c.agentRunning;
		if (c.agents.length === 0 && c.agentRunning > 0) {
			addAgent(all, 'other', c.agentRunning);
		}
		for (const a of c.agents) {
			addAgent(all, a.harness, a.running);
		}
	}
	return sortAgents(all);
}

/** One agent's hand on a clock, by the name its clock goes by. */
export interface Hand {
	key: string;
	name: string;
	running: number;
}

/**
 * The agents' hands on a clock. When the agent couldn't tell which agent
 * ran, they share one hand, "Your agents".
 */
export function agentHands(c: ProjectClock): Hand[] {
	const named = c.agents
		.filter((a) => a.running > 0)
		.map((a) => ({ key: a.harness, name: agentName(a.harness), running: a.running }));
	if (named.length === 0 && c.agentRunning > 0) {
		return [{ key: 'agents', name: 'Your agents', running: c.agentRunning }];
	}
	return named;
}

/** The name of a clock's one agent, when it has exactly one. */
export function soleAgent(c: ProjectClock): string | undefined {
	const hands = agentHands(c);
	return hands.length === 1 ? hands[0]?.name : undefined;
}

/** One day of the week list. */
export interface DayRow {
	date: string;
	clock: ProjectClock;
}

/**
 * The days with your time or an agent's on them, each as one clock, in
 * date order. Days without any are left out, so the week shows what you
 * did and never a gap to fill.
 */
export function dayRows(days: Day[]): DayRow[] {
	return days
		.map((d) => ({ date: d.date, clock: combine(clocks([d], []), d.date, d.date) }))
		.filter((r) => r.clock.credited > 0 || r.clock.agentRunning > 0);
}

/** Seconds of one kind or reason, in plain words. */
export interface Row<K extends string> {
	key: K;
	label: string;
	about: string;
	seconds: number;
}

/**
 * The uncredited seconds in plain words, most first, with only reasons
 * that have any. X.agent_only is named after the agent when one agent ran
 * (DESIGN 3.8).
 */
export function reasonRows(uncredited: Partial<Record<Reason, number>>, agent?: string): Row<Reason>[] {
	return reasons
		.map((w) => ({
			key: w.reason,
			label: w.reason === 'X.agent_only' && agent ? agentOnly(agent) : w.label,
			about: w.about,
			seconds: uncredited[w.reason] ?? 0,
		}))
		.filter((r) => r.seconds > 0)
		.sort((a, b) => b.seconds - a.seconds);
}

/** Your time by kind in plain words, most first, with only kinds that have any. */
export function kindRows(byKind: Partial<Record<Kind, number>>): Row<Kind>[] {
	return kinds
		.map((w) => ({ key: w.kind, label: w.label, about: w.about, seconds: byKind[w.kind] ?? 0 }))
		.filter((r) => r.seconds > 0)
		.sort((a, b) => b.seconds - a.seconds);
}

/** A bar's parts: your time by level, pending, or an agent's time. */
export type Part = 'A' | 'B' | 'C' | 'pending' | 'agent';

/** One part of a bar, in a viewBox 100 units wide. */
export interface Segment {
	part: Part;
	x: number;
	width: number;
}

/**
 * Lays out a bar's parts left to right, each as wide as its seconds are of
 * scale, the seconds that fill the bar. Parts with no seconds are left out,
 * and nothing runs past the end.
 */
export function segments(parts: [Part, number][], scale: number): Segment[] {
	const out: Segment[] = [];
	if (!(scale > 0)) {
		return out;
	}
	let x = 0;
	for (const [part, s] of parts) {
		if (!(s > 0) || x >= 100) {
			continue;
		}
		const width = Math.min(100 - x, (s / scale) * 100);
		out.push({ part, x: round(x), width: round(width) });
		x += width;
	}
	return out;
}

/** The parts of a bar of your time: final time by level, then pending. */
export function yourParts(c: { evidence: ByEvidence; pending: number }): [Part, number][] {
	return [
		['A', c.evidence.A],
		['B', c.evidence.B],
		['C', c.evidence.C],
		['pending', c.pending],
	];
}

/** The one part of a bar of an agent's time. */
export function agentParts(running: number): [Part, number][] {
	return [['agent', running]];
}

/**
 * The seconds that fill a bar on a page of clocks: the longest hand on any
 * of them, so every bar on the page is drawn to the same scale.
 */
export function scaleOf(cs: ProjectClock[]): number {
	let most = 0;
	for (const c of cs) {
		most = Math.max(most, c.credited, ...agentHands(c).map((h) => h.running));
	}
	return most;
}

function round(n: number): number {
	return Math.round(n * 100) / 100;
}
