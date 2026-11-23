// SPDX-License-Identifier: AGPL-3.0-or-later

// The dashboard's views of the agent's tallies (GET /v1/days/{date}, GET
// /v1/sessions/last): a project's time as `pairtime today` shows it
// (cmd/pairtime/views.go's buildProject, one for one), tallies summed over
// days and across projects, and the geometry of every bar. Pure functions,
// so vitest checks the arithmetic the pages show.
//
// While display.shadow is on, the agent leaves out every credited number,
// every uncredited code but SM 4.2's loops, and a session's start, end and
// open (D-shadow-scope; the spec's Shadow). A number it leaves out
// reads as 0 here and a map as {}, as the CLI reads them, and the views say
// "calibrating".

import en from '../../../internal/coach/text/en.json';
import type { Day, Harness, Kind, Level, NameState, ProjectEntry, Reason, Tally, WorkSession } from './api.ts';
import * as text from './text.ts';

/** A project's time over a span, as the views show it. */
export interface ProjectView {
	key: string;
	/** Its name, or its key when the agent has none, made printable, as the CLI shows it. */
	title: string;
	/** Your time; absent while calibrating (display.shadow, DESIGN 6.7). */
	you?: YourTime;
	/** Each agent's time on its own, the most first. */
	agents: AgentTime[];
	/**
	 * The rest of the uncredited time by reason, the most first, without the
	 * agent's. While calibrating, SM 4.2's loops alone.
	 */
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
	/**
	 * Set on the part of X.noprog that progress can still bring back (SM 4.3;
	 * approach-teen.md D5). The rest of X.noprog has an entry without it.
	 */
	onHold?: boolean;
}

const levelOrder: Level[] = ['A', 'B', 'C'];

/**
 * SM 4.2's dark-flow loops (pkg/event's Reason.DarkFlow): while calibrating,
 * the only uncredited time a view shows besides the agent's. With the rest,
 * and what the day or session took, your hidden time could be worked out
 * (D-shadow-scope; DESIGN 6.7 step 2; SM 7.5).
 */
const loops: ReadonlySet<string> = new Set(['X.reroll', 'X.rewrite_loop', 'X.same_error', 'X.autopilot']);

/** Go's strings.Compare, which the CLI breaks ties with. */
function byCode(a: string, b: string): number {
	return a < b ? -1 : a > b ? 1 : 0;
}

/** The most seconds first, then by code (views.go's most). */
function most(sa: number, sb: number, ca: string, cb: string): number {
	return sb - sa || byCode(ca, cb);
}

/** A map's numbers; a map the agent left out has none. */
function entries<K extends string>(m: Partial<Record<K, number>> | undefined): [K, number][] {
	return (Object.entries(m ?? {}) as [K, number | undefined][]).filter((e): e is [K, number] => typeof e[1] === 'number');
}

/**
 * The agent whose answers a project's credited time went with: the one
 * harness that had a session there, or "your agent" when there were none
 * or several (views.go's agentOf).
 */
export function agentOf(byHarness: Tally['agent_only_by_harness'] | undefined): string {
	const harnesses = Object.keys(byHarness ?? {});
	return harnesses.length === 1 ? text.agent(harnesses[0] ?? 'other') : text.agent('other');
}

/**
 * Each agent's time on its own, the most first, leaving out harnesses at 0
 * (views.go's agentList). Time under a harness en.json has no name for is
 * "your agent"'s.
 */
export function agentList(byHarness: Tally['agent_only_by_harness'] | undefined): AgentTime[] {
	const sum = new Map<Harness, number>();
	for (const [h, s] of entries(byHarness)) {
		if (s > 0) {
			const named: Harness = Object.hasOwn(en.agents, h) ? h : 'other';
			sum.set(named, (sum.get(named) ?? 0) + s);
		}
	}
	return [...sum]
		.map(([harness, s]) => ({ harness, name: text.agent(harness), s }))
		.sort((a, b) => most(a.s, b.s, a.harness, b.harness));
}

/**
 * A project's tally as the views show it (views.go's buildProject). With
 * calibrating set (display.shadow), your time is left out, and so is the
 * other time but SM 4.2's loops. A kind or a reason en.json has no words
 * for, from a newer agent, is left out too, so no raw code reaches the
 * page; its seconds stay in your time and the agent's.
 */
export function buildProject(key: string, name: string, t: Tally, calibrating: boolean): ProjectView {
	const agentName = agentOf(t.agent_only_by_harness);
	const view: ProjectView = {
		key,
		title: text.printable(name !== '' ? name : key),
		agents: agentList(t.agent_only_by_harness),
		other: [],
	};
	if (!calibrating) {
		const kinds = entries(t.credited_by_kind)
			.filter(([k, s]) => s > 0 && Object.hasOwn(en.kinds, k))
			.map(([k, s]) => ({ kind: k, words: text.kind(k, agentName), s }))
			.sort((a, b) => most(a.s, b.s, a.kind, b.kind));
		const shown = levelOrder
			.map((l) => ({ level: l, s: t.credited_by_evidence?.[l] ?? 0 }))
			.filter((l) => l.s > 0);
		const shares = text.percentages(shown.map((l) => l.s));
		view.you = {
			s: t.credited_s ?? 0,
			pending: t.pending_s ?? 0,
			kinds,
			levels: shown.map((l, i) => ({ ...l, words: text.level(l.level), share: shares[i] ?? 0 })),
		};
	}
	for (const [r, s] of entries(t.uncredited)) {
		const words = text.other(r, agentName);
		if (!(s > 0) || text.isAgentTime(r) || words === '' || (calibrating && !loops.has(r))) {
			continue;
		}
		if (r !== 'X.noprog') {
			view.other.push({ reason: r, words, s });
			continue;
		}
		// The part progress can still bring back is on hold; the rest can't
		// come back any more, and says what it was.
		const held = Math.min(Math.max(t.on_hold_s ?? 0, 0), s);
		if (held > 0) {
			view.other.push({ reason: r, words: text.onHold(), s: held, onHold: true });
		}
		if (s > held) {
			view.other.push({ reason: r, words, s: s - held });
		}
	}
	// The most first, then by code, and of X.noprog's two parts with the
	// same seconds, the one on hold first.
	view.other.sort((a, b) => most(a.s, b.s, a.reason, b.reason) || (b.onHold ? 1 : 0) - (a.onHold ? 1 : 0));
	return view;
}

/**
 * A view's other time as the rows a page lists, those of a second or more.
 * X.noprog can be two rows, so each part has a key of its own.
 */
export function otherRows(v: ProjectView): { key: string; words: string; s: number }[] {
	return v.other
		.filter((o) => o.s >= 1)
		.map((o) => ({ key: o.onHold ? `${o.reason}:on_hold` : o.reason, words: o.words, s: o.s }));
}

/** An empty tally, every field there. */
export function emptyTally(): Required<Tally> {
	return {
		credited_s: 0,
		pending_s: 0,
		credited_by_kind: {},
		credited_by_evidence: {},
		agent_only_s: 0,
		agent_only_by_harness: {},
		uncredited: {},
		on_hold_s: 0,
	};
}

function addInto<K extends string>(sum: Partial<Record<K, number>>, more: Partial<Record<K, number>> | undefined): void {
	for (const [k, v] of entries(more)) {
		// A harness at 0 still names the agent, so a key is kept at 0.
		sum[k] = (sum[k] ?? 0) + Math.max(0, v);
	}
}

/**
 * The sum of tallies: a project's over several days, or several projects'
 * at once. A number a tally leaves out adds 0, and a map adds nothing.
 */
export function sum(tallies: Tally[]): Required<Tally> {
	const out = emptyTally();
	for (const t of tallies) {
		out.credited_s += t.credited_s ?? 0;
		out.pending_s += t.pending_s ?? 0;
		out.agent_only_s += t.agent_only_s ?? 0;
		out.on_hold_s += t.on_hold_s ?? 0;
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
	const summed = [...byKey].map(([key, e]) => ({
		key,
		name: e.name,
		title: text.printable(e.name !== '' ? e.name : key),
		t: sum(e.tallies),
	}));
	summed.sort(
		(a, b) =>
			(calibrating ? 0 : b.t.credited_s - a.t.credited_s) ||
			b.t.agent_only_s - a.t.agent_only_s ||
			byCode(a.title, b.title) ||
			byCode(a.key, b.key),
	);
	return summed.map((p) => buildProject(p.key, p.name, p.t, calibrating));
}

/**
 * Whether a project's name switch is on: while its name shows on the
 * owner's dashboard, and while it is on its way there (GET /v1/projects).
 */
export function nameShown(state: NameState): boolean {
	return state === 'on' || state === 'sending';
}

/**
 * What the Projects page calls a project: its name from the day's answer,
 * or, while the agent's days carry none, the local name GET /v1/projects
 * gives it, or "Project <code>" as the owner's dashboard calls it, and its
 * key when neither answer names it and the server gave it no code.
 */
export function projectTitle(view: ProjectView, entry: ProjectEntry | undefined): string {
	if (entry === undefined || view.title !== text.printable(view.key)) {
		return view.title;
	}
	return entryTitle(entry);
}

/**
 * What the list of other projects calls one: its local name, or "Project
 * <code>", or its key while the server has given it no code.
 */
export function entryTitle(entry: ProjectEntry): string {
	if (entry.local_name !== '') {
		return text.printable(entry.local_name);
	}
	return text.printable(entry.code !== '' ? `Project ${entry.code}` : entry.project);
}

/**
 * What `pairtime projects show-name` takes for a project: the code the
 * server gave it, or its key while it gave none.
 */
export function projectArg(entry: ProjectEntry): string {
	return entry.code !== '' ? entry.code : entry.project;
}

/** All of the days' projects as one view: the day's or the week's whole clock. */
export function whole(days: Day[], calibrating: boolean, title: string): ProjectView {
	return buildProject('all', title, sum(days.flatMap((d) => d.projects)), calibrating);
}

/** The part of your time at evidence level C (views.go's levelC). */
function levelC(you: YourTime): number {
	return you.levels.find((l) => l.level === 'C')?.s ?? 0;
}

/**
 * The chess clock line of a view, with its level-C time labeled: "you 1h
 * 12m, 20m level C · Claude 2h 40m" (views.go's clock).
 */
export function clockLine(v: ProjectView): string {
	return v.you ? text.clock(v.you.s, levelC(v.you), false, v.agents) : text.clock(0, 0, true, v.agents);
}

/**
 * Whether the page asks the agent for an earlier day again (App.svelte's
 * refresh). A day changes while a project of it has pending minutes, which
 * late records may still change (DESIGN 3.7). Progress restores
 * half-weighted X.noprog seconds up to PROGRESS_RESTORE_S, 86,400 s, back
 * (SM 4.3), so yesterday can change too: while it has X.noprog seconds or
 * seconds on hold, and while calibrating, which hides both. A day read with
 * display.shadow set otherwise than today's is asked again as well, so its
 * numbers show, or hide, with the rest.
 */
export function mayChange(d: Day, yesterday: boolean, shadow: boolean): boolean {
	if (d.shadow !== shadow || d.projects.some((p) => (p.pending_s ?? 0) > 0)) {
		return true;
	}
	return (
		yesterday &&
		(d.shadow || d.projects.some((p) => (p.uncredited?.['X.noprog'] ?? 0) > 0 || (p.on_hold_s ?? 0) > 0))
	);
}

/** An RFC 3339 time as a Date, or undefined when there is none or it doesn't parse. */
function instant(s: string | undefined): Date | undefined {
	if (s === undefined) {
		return undefined;
	}
	const t = new Date(s);
	return Number.isNaN(t.getTime()) ? undefined : t;
}

/**
 * A session's heading, as `pairtime recap` heads it (recap.go's
 * writeRecap): "Your session so far: rusty-cli" while it goes on, and
 * "Your last session: rusty-cli" otherwise and always while calibrating,
 * since whether it goes on says whether the last minute counted.
 */
export function sessionHead(s: WorkSession, title: string, calibrating: boolean): string {
	return text.phrase(!calibrating && s.open === true ? 'session_open' : 'session_last', { project: title });
}

/**
 * When a session was, as `pairtime recap` says it (recap.go's writeRecap):
 * its day, start and end in this browser's zone, or since when while it
 * goes on. While calibrating it is the session's local day alone
 * (D-shadow-scope): its date, or the day its start falls on here when an
 * agent leaves the date out, and "" when there is neither.
 */
export function sessionWhen(s: Pick<WorkSession, 'start' | 'end' | 'open'> & { date?: string }, calibrating: boolean): string {
	const start = instant(s.start);
	const end = instant(s.end);
	if (!calibrating && start && end) {
		return text.sessionSpan(start, end, s.open === true);
	}
	if (s.date !== undefined && s.date !== '') {
		return text.day(s.date);
	}
	return start ? text.localDay(start) : '';
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

/** The notes a view of your time ends with: how much is level C, and how much may still change. */
export function notes(you: YourTime): { key: 'level-c' | 'pending'; words: string }[] {
	const out: { key: 'level-c' | 'pending'; words: string }[] = [];
	const c = levelC(you);
	if (c >= 1) {
		out.push({ key: 'level-c', words: text.phrase('level_c_note', { time: text.duration(c) }) });
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
