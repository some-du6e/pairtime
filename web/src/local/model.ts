// SPDX-License-Identifier: AGPL-3.0-or-later

// The dashboard's views of the agent's tallies (GET /v1/days/{date}, GET
// /v1/sessions/last): a project's time as `pairtime today` shows it
// (cmd/pairtime/views.go's buildProject, one for one), tallies summed over
// days and across projects, and the geometry of every bar. Pure functions,
// so vitest checks the arithmetic the pages show.

import type { Day, Harness, Kind, Level, Reason, Tally } from './api.ts';
import * as text from './text.ts';

/** A project's time over a span, as the views show it. */
export interface ProjectView {
	key: string;
	/** Its name, or its key when the agent has none, as the CLI shows it. */
	title: string;
	/** Your time; absent while calibrating (display.shadow, DESIGN 6.7). */
	you?: YourTime;
	/** Each agent's time on its own, the most first. */
	agents: AgentTime[];
	/** The rest of the uncredited time by reason, the most first, without the agent's. */
	other: OtherTime[];
}

export interface YourTime {
	s: number;
	/** The part of s that may still change. */
	pending: number;
	/** s by activity kind, the most first. */
	kinds: { kind: Kind; words: string; s: number }[];
	/** s by evidence level, in the order A, B, C, with whole-percent shares that add up to 100. */
	levels: { level: Level; words: string; s: number; share: number }[];
}

export interface AgentTime {
	harness: Harness;
	name: string;
	s: number;
}

export interface OtherTime {
	reason: Reason;
	words: string;
	s: number;
}

const levelOrder: Level[] = ['A', 'B', 'C'];

/** Go's strings.Compare, which the CLI breaks ties with. */
function byCode(a: string, b: string): number {
	return a < b ? -1 : a > b ? 1 : 0;
}

/** The most seconds first, then by code (views.go's most). */
function most(sa: number, sb: number, ca: string, cb: string): number {
	return sb - sa || byCode(ca, cb);
}

function entries<K extends string>(m: Partial<Record<K, number>>): [K, number][] {
	return (Object.entries(m) as [K, number | undefined][]).filter((e): e is [K, number] => typeof e[1] === 'number');
}

/**
 * The agent whose answers a project's credited time went with: the one
 * harness that had a session there, or "your agent" when there were none
 * or several (views.go's agentOf).
 */
export function agentOf(byHarness: Tally['agent_only_by_harness']): string {
	const harnesses = Object.keys(byHarness);
	return harnesses.length === 1 ? text.agent(harnesses[0] ?? 'other') : text.agent('other');
}

/** Each agent's time on its own, the most first, leaving out harnesses at 0 (views.go's agentList). */
export function agentList(byHarness: Tally['agent_only_by_harness']): AgentTime[] {
	const sum = new Map<Harness, number>();
	for (const [h, s] of entries(byHarness)) {
		if (s > 0) {
			sum.set(h, (sum.get(h) ?? 0) + s);
		}
	}
	return [...sum]
		.map(([harness, s]) => ({ harness, name: text.agent(harness), s }))
		.sort((a, b) => most(a.s, b.s, a.harness, b.harness));
}

/** A project's tally as the views show it (views.go's buildProject). */
export function buildProject(key: string, name: string, t: Tally, calibrating: boolean): ProjectView {
	const agentName = agentOf(t.agent_only_by_harness);
	const view: ProjectView = { key, title: name !== '' ? name : key, agents: agentList(t.agent_only_by_harness), other: [] };
	if (!calibrating) {
		const kinds = entries(t.credited_by_kind)
			.filter(([, s]) => s > 0)
			.map(([k, s]) => ({ kind: k, words: text.kind(k, agentName), s }))
			.sort((a, b) => most(a.s, b.s, a.kind, b.kind));
		const shown = levelOrder
			.map((l) => ({ level: l, s: t.credited_by_evidence[l] ?? 0 }))
			.filter((l) => l.s > 0);
		const shares = text.percentages(shown.map((l) => l.s));
		view.you = {
			s: t.credited_s,
			pending: t.pending_s,
			kinds,
			levels: shown.map((l, i) => ({ ...l, words: text.level(l.level), share: shares[i] ?? 0 })),
		};
	}
	view.other = entries(t.uncredited)
		.filter(([r, s]) => s > 0 && !text.isAgentTime(r))
		.map(([r, s]) => ({ reason: r, words: text.other(r, agentName) || r, s }))
		.sort((a, b) => most(a.s, b.s, a.reason, b.reason));
	return view;
}

/** An empty tally. */
export function emptyTally(): Tally {
	return {
		credited_s: 0,
		pending_s: 0,
		credited_by_kind: {},
		credited_by_evidence: {},
		agent_only_s: 0,
		agent_only_by_harness: {},
		uncredited: {},
	};
}

function addInto<K extends string>(sum: Partial<Record<K, number>>, more: Partial<Record<K, number>>): void {
	for (const [k, v] of entries(more)) {
		// A harness at 0 still names the agent, so a key is kept at 0.
		sum[k] = (sum[k] ?? 0) + Math.max(0, v);
	}
}

/** The sum of tallies: a project's over several days, or several projects' at once. */
export function sum(tallies: Tally[]): Tally {
	const out = emptyTally();
	for (const t of tallies) {
		out.credited_s += t.credited_s;
		out.pending_s += t.pending_s;
		out.agent_only_s += t.agent_only_s;
		addInto(out.credited_by_kind, t.credited_by_kind);
		addInto(out.credited_by_evidence, t.credited_by_evidence);
		addInto(out.agent_only_by_harness, t.agent_only_by_harness);
		addInto(out.uncredited, t.uncredited);
	}
	return out;
}

/**
 * Every project the days name, summed over them into one view each, in
 * `pairtime today`'s order (today.go's buildToday): the most of your time
 * first, then the most agent time, then by title. While calibrating, your
 * time is left out of the order too, so it says nothing about the hidden
 * numbers.
 */
export function projects(days: Day[], calibrating: boolean): ProjectView[] {
	const byKey = new Map<string, { name: string; tallies: Tally[] }>();
	for (const d of days) {
		for (const p of d.projects) {
			const entry = byKey.get(p.project) ?? { name: p.name, tallies: [] };
			if (entry.name === '' && p.name !== '') {
				entry.name = p.name;
			}
			entry.tallies.push(p);
			byKey.set(p.project, entry);
		}
	}
	const summed = [...byKey].map(([key, e]) => ({ key, name: e.name, t: sum(e.tallies) }));
	summed.sort(
		(a, b) =>
			(calibrating ? 0 : b.t.credited_s - a.t.credited_s) ||
			b.t.agent_only_s - a.t.agent_only_s ||
			byCode(a.name !== '' ? a.name : a.key, b.name !== '' ? b.name : b.key) ||
			byCode(a.key, b.key),
	);
	return summed.map((p) => buildProject(p.key, p.name, p.t, calibrating));
}

/** All of the days' projects as one view: the day's or the week's whole clock. */
export function whole(days: Day[], calibrating: boolean, title: string): ProjectView {
	return buildProject('all', title, sum(days.flatMap((d) => d.projects)), calibrating);
}

/** The chess clock line of a view: "you 1h 12m · Claude 2h 40m" (views.go's clock). */
export function clockLine(v: ProjectView): string {
	return text.clock(v.you?.s ?? 0, v.you === undefined, v.agents);
}

/** A bar's parts: your time by level, pending, or an agent's time. */
export type Part = Level | 'pending' | 'agent';

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

/**
 * The parts of a bar of your time: the final part by level, then the
 * pending part, drawn lighter. The agent counts pending seconds at the level
 * they have so far, so each level's final part is its share of the final
 * seconds.
 */
export function yourParts(you: YourTime): [Part, number][] {
	const final = Math.max(0, you.s - you.pending);
	const leveled = you.levels.reduce((a, l) => a + l.s, 0);
	const k = leveled > 0 ? final / leveled : 0;
	return [...you.levels.map((l): [Part, number] => [l.level, l.s * k]), ['pending', you.pending]];
}

/** The one part of a bar of an agent's time. */
export function agentParts(s: number): [Part, number][] {
	return [['agent', s]];
}

/** The seconds that fill a bar on a page: the longest time any view on it shows, yours or an agent's. */
export function scaleOf(views: ProjectView[]): number {
	let longest = 0;
	for (const v of views) {
		longest = Math.max(longest, v.you?.s ?? 0, ...v.agents.map((a) => a.s));
	}
	return longest;
}

/** The note a view of your time ends with: how much is level C, and how much may still change. */
export function notes(you: YourTime): { key: 'level-c' | 'pending'; words: string }[] {
	const out: { key: 'level-c' | 'pending'; words: string }[] = [];
	const c = you.levels.find((l) => l.level === 'C');
	if (c && c.s >= 1) {
		out.push({ key: 'level-c', words: text.phrase('level_c_note', { time: text.duration(c.s) }) });
	}
	if (you.pending >= 1) {
		out.push({ key: 'pending', words: text.phrase('pending_note', { time: text.duration(you.pending) }) });
	}
	return out;
}

/** The dates from first to last, both included, YYYY-MM-DD. */
export function dates(first: string, last: string): string[] {
	const out: string[] = [];
	const start = Date.parse(`${first}T00:00:00Z`);
	const end = Date.parse(`${last}T00:00:00Z`);
	if (Number.isNaN(start) || Number.isNaN(end)) {
		return out;
	}
	for (let t = start; t <= end && out.length < 400; t += 86_400_000) {
		out.push(new Date(t).toISOString().slice(0, 10));
	}
	return out;
}

/** date plus n days, YYYY-MM-DD. */
export function addDays(date: string, n: number): string {
	const t = Date.parse(`${date}T00:00:00Z`);
	return Number.isNaN(t) ? date : new Date(t + n * 86_400_000).toISOString().slice(0, 10);
}

/** The Monday on or before date, YYYY-MM-DD: the first day of this week. */
export function monday(date: string): string {
	const t = Date.parse(`${date}T00:00:00Z`);
	if (Number.isNaN(t)) {
		return date;
	}
	const back = (new Date(t).getUTCDay() + 6) % 7;
	return addDays(date, -back);
}

function round(n: number): number {
	return Math.round(n * 100) / 100;
}

/** "Today", "Yesterday", or the day as the views head it, for a list of days. */
export function dayLabel(date: string, today: string): string {
	if (date === today) {
		return 'Today';
	}
	if (date === addDays(today, -1)) {
		return 'Yesterday';
	}
	return text.day(date);
}
