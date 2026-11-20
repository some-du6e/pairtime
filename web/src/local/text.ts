// SPDX-License-Identifier: AGPL-3.0-or-later

// The words pairtime shows a teen about their time, from
// internal/coach/text/en.json, which the coach and the CLI views read too
// (WP-CO0), so a minute reads the same in the terminal and on the
// dashboard (DESIGN 3.8). These functions are internal/coach/text's, one
// for one: text.test.ts runs that package's own test cases against them.

import en from '../../../internal/coach/text/en.json';
import type { Harness, Kind, Level, Reason } from './api.ts';

/** en.json's phrases, by key. */
export type Key = keyof typeof en.phrases;

/** The catalog, with its maps open to codes a newer agent may send. */
const catalog = {
	agents: en.agents as Record<string, string>,
	kinds: en.kinds as Record<string, string>,
	agentTime: en.agent_time,
	other: en.other as Record<string, string>,
	levels: en.levels as Record<string, string>,
	phrases: en.phrases,
};

/**
 * Replaces each {name} in s with vars[name], in one pass, so a value that
 * holds braces, such as a project's name, stays as it is. A placeholder
 * with no value stays in place (text.Fill).
 */
export function fill(s: string, vars: Record<string, string> = {}): string {
	return s.replace(/\{(\w+)\}/g, (m, k: string) => (Object.hasOwn(vars, k) ? (vars[k] ?? m) : m));
}

/** Phrase key with its placeholders filled (text.Phrase). */
export function phrase(key: Key, vars: Record<string, string> = {}): string {
	return fill(catalog.phrases[key], vars);
}

/** The name the views give harness h's agent: "Claude" for claude_code (text.Agent). */
export function agent(h: Harness | string): string {
	return catalog.agents[h] ?? catalog.agents.other ?? 'your agent';
}

/** Credited time of kind k, with the agent named agentName: "reading Claude's answers" (text.Kind). */
export function kind(k: Kind | string, agentName: string): string {
	const s = catalog.kinds[k];
	return s === undefined ? k : fill(s, { agent: agentName });
}

/** The agent-only time of the agent named agentName: "Claude, working on its own" (text.AgentTime). */
export function agentTime(agentName: string): string {
	return fill(catalog.agentTime, { agent: agentName });
}

/**
 * Whether uncredited seconds with reason r are the agent's time, which the
 * views name after the agent rather than list as other time (text.IsAgentTime).
 */
export function isAgentTime(r: Reason | string): boolean {
	return r === 'X.agent_only' || r === 'X.automated';
}

/** Uncredited time with reason r: "away", "Claude rewriting the same lines"; "" for the agent's time (text.Other). */
export function other(r: Reason | string, agentName: string): string {
	const s = catalog.other[r];
	return s === undefined ? '' : fill(s, { agent: agentName });
}

/** Evidence level e in words, or "" for a level that isn't one (text.Level). */
export function level(e: Level | string): string {
	return catalog.levels[e] ?? '';
}

const maxSeconds = 1e12;

/**
 * Seconds as every view shows time: "45s", "4m 25s", "12m", "1h 12m",
 * "2h", and "0m" under a second. It rounds down, to the second below an
 * hour and to the minute from an hour up, so it never shows more time than
 * was counted (text.Duration).
 */
export function duration(s: number): string {
	if (Number.isNaN(s) || s < 1) {
		return '0m';
	}
	const n = Math.floor(Math.min(s, maxSeconds));
	const h = Math.floor(n / 3600);
	const m = Math.floor(n / 60) % 60;
	const sec = n % 60;
	if (h > 0) {
		return m > 0 ? `${h}h ${m}m` : `${h}h`;
	}
	if (m > 0) {
		return sec > 0 ? `${m}m ${sec}s` : `${m}m`;
	}
	return `${sec}s`;
}

/** One agent's side of the chess clock. */
export interface AgentClock {
	name: string;
	s: number;
}

/**
 * The chess clock (DESIGN 3.8): "you 1h 12m · Claude 2h 40m", your time
 * and then each agent's time on its own, as information and never as a
 * ratio or a target. Calibrating, your time reads "calibrating" and the
 * agents' times still show. An agent with less than a second is left out
 * (text.Clock).
 */
export function clock(you: number, calibrating: boolean, agents: AgentClock[]): string {
	const parts = [calibrating ? phrase('you_calibrating') : phrase('you', { time: duration(you) })];
	for (const a of agents) {
		if (a.s >= 1) {
			parts.push(phrase('agent', { agent: a.name, time: duration(a.s) }));
		}
	}
	return parts.join(phrase('separator'));
}

/** s with its first letter in upper case, for words that start a line (text.Sentence). */
export function sentence(s: string): string {
	const first = s.codePointAt(0);
	if (first === undefined) {
		return s;
	}
	const ch = String.fromCodePoint(first);
	return ch.toUpperCase() + s.slice(ch.length);
}

/**
 * Splits 100 among parts in proportion to their sizes, in whole numbers
 * that add up to exactly 100: each part gets its share rounded down, and
 * the rest goes one at a time to the parts that lost the most to rounding,
 * the earlier part first on a tie. Parts that aren't positive get 0
 * (text.Percentages), so the dashboard's shares are the CLI's.
 */
export function percentages(parts: number[]): number[] {
	const out = parts.map(() => 0);
	// Whole milliseconds, as in Go; BigInt keeps the products exact.
	const units = parts.map((p) => (p > 0 ? BigInt(Math.round(Math.min(p, maxSeconds) * 1000)) : 0n));
	const total = units.reduce((a, b) => a + b, 0n);
	if (total === 0n) {
		return out;
	}
	const rests: { i: number; rem: bigint }[] = [];
	let left = 100;
	units.forEach((u, i) => {
		if (u === 0n) {
			return;
		}
		out[i] = Number((u * 100n) / total);
		left -= out[i] ?? 0;
		rests.push({ i, rem: (u * 100n) % total });
	});
	// A stable sort, the largest remainder first.
	rests.sort((a, b) => (b.rem > a.rem ? 1 : b.rem < a.rem ? -1 : 0));
	for (let k = 0; k < left; k++) {
		const r = rests[k];
		if (r) {
			out[r.i] = (out[r.i] ?? 0) + 1;
		}
	}
	return out;
}

const dayFormat = new Intl.DateTimeFormat('en-US', { weekday: 'long', month: 'long', day: 'numeric', timeZone: 'UTC' });
const timeFormat = new Intl.DateTimeFormat('en-GB', { hour: '2-digit', minute: '2-digit', hourCycle: 'h23' });

/**
 * A local date, YYYY-MM-DD, as the views head a day: "Tuesday, November
 * 24" (text.Day). A date that doesn't parse comes back as it is.
 */
export function day(date: string): string {
	const m = /^(\d{4})-(\d{2})-(\d{2})$/.exec(date);
	if (!m) {
		return date;
	}
	const d = new Date(Date.UTC(Number(m[1]), Number(m[2]) - 1, Number(m[3])));
	return d.toISOString().slice(0, 10) === date ? dayFormat.format(d) : date;
}

const localDayFormat = new Intl.DateTimeFormat('en-US', { weekday: 'long', month: 'long', day: 'numeric' });

/** The time of day of t in this browser's time zone, as the views give a session's start and end: "07:40" (text.Clocktime). */
export function clocktime(t: Date): string {
	return timeFormat.format(t);
}

/** The day t falls on in this browser's time zone, headed as text.Day heads it: "Tuesday, November 24". */
export function localDay(t: Date): string {
	return localDayFormat.format(t);
}

/**
 * Where and when a work session ran, as `pairtime recap` says it
 * (recap.go's writeRecap): "Tuesday, November 24, 07:40 to 07:45", with the
 * end's day too when the session ran past midnight, or "Tuesday, November
 * 24, since 07:40" while it goes on.
 */
export function sessionSpan(start: Date, end: Date, open: boolean): string {
	if (open) {
		return phrase('session_since', { day: localDay(start), start: clocktime(start) });
	}
	const sameDay =
		start.getFullYear() === end.getFullYear() && start.getMonth() === end.getMonth() && start.getDate() === end.getDate();
	const until = sameDay ? clocktime(end) : `${localDay(end)}, ${clocktime(end)}`;
	return phrase('session_span', { day: localDay(start), start: clocktime(start), end: until });
}

/** A level's share as `pairtime recap` prints it: "54%", or "<1%" for a share that rounds to 0. */
export function share(n: number): string {
	return n === 0 ? '<1%' : `${n}%`;
}

/** Every string the catalog holds, for the tests that read all of it. */
export function entries(): { where: string; s: string }[] {
	const out: { where: string; s: string }[] = [];
	const add = (where: string, m: Record<string, string>) => {
		for (const [k, s] of Object.entries(m)) {
			out.push({ where: `${where}.${k}`, s });
		}
	};
	add('agents', catalog.agents);
	add('kinds', catalog.kinds);
	out.push({ where: 'agent_time', s: catalog.agentTime });
	add('other', catalog.other);
	add('levels', catalog.levels);
	add('phrases', catalog.phrases);
	return out;
}
