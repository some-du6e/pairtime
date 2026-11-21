// SPDX-License-Identifier: AGPL-3.0-or-later

// The words only the dashboard says: what pairtime counts, why a minute
// counts at level A, B or C, what pending means, D-focus-unknown's
// sentence, and what a page says when the agent's answer didn't come back.
// Everything the CLI views say too, a kind, a reason, a level's short
// words, the chess clock, comes from internal/coach/text/en.json through
// text.ts, so both say a minute the same way (DESIGN 3.8). copy.test.ts
// holds every sentence here, allSentences(), to the same rules as en.json:
// nothing scolds, sets a target or counts a streak (PA 4.3, 4.4).

import type { Focus, Level } from './api.ts';
import * as text from './text.ts';

/** What pairtime counts, for the explainer on Today. */
export const counts = [
	'pairtime counts the time you spend building with AI: thinking up and typing prompts, reading what the agent wrote, watching it work, answering its questions, running tests and commands, and editing code yourself.',
	"When the agent works on its own, that time is the agent's. It goes on the agent's clock, next to yours. The two clocks aren't a race. A long agent clock usually means the agent did big jobs while you steered.",
];

/**
 * Why a minute counts at each level, beside en.json's short words for it
 * (SM 5.1, DESIGN 6.4, 6.2 D11). Level C leaves out D-focus-unknown's
 * desktop that hides which window is in front: until WP-SC8 the scorer
 * credits such time nothing, so saying it counts at level C would be
 * untrue (D-design-5.9). WP-SC8 adds it here, with Focus.level_c.
 */
export const levelWhy: Record<Level, string> = {
	A: 'Your computer saw you typing around the prompt, edit or command that started this time, and the tool you used reported that act itself.',
	B: "Your computer saw your keyboard or mouse, but it can't tell typing apart from other input. Most Windows and Linux computers work this way. The tool you used reported the act itself.",
	C: "pairtime had less to go on. The act came from pairtime track or a plugin with no typing seen before it, or from an editor pairtime couldn't check. Level C time shows here on your dashboard, but not on your profile, your build days or most programs.",
};

/**
 * What pending means (DESIGN 3.7, 6.2 D1). PENDING_WINDOW_S is 900 s in
 * every parameter table so far; copy.test.ts fails if a table changes it,
 * so these words change with it.
 */
export const pendingWords =
	"Lighter minutes are pending. pairtime waits 15 minutes after each minute in case more of what you did arrives late, so these can still change. Then they're final.";

/**
 * What Today says while /v1/status says the desktop doesn't tell pairtime
 * which window is in front (D-focus-unknown), with its GNOME additions.
 * Where such time counts it says only once focus.level_c is true, which
 * WP-SC8 sets, the first WP in which that time is level C (D-design-5.9).
 * Until then the sentence stops at its first clause: the agent's
 * `known: false` alone doesn't make the claim true.
 */
export function focusWords(focus: Focus): string[] {
	const words = [
		focus.level_c === true
			? "This desktop doesn't tell pairtime which window is in front, so your time here counts only on your own dashboard, not on your profile, build days or most programs."
			: "This desktop doesn't tell pairtime which window is in front.",
	];
	if (focus.gnome_extension === 'needs_login') {
		words.push("Log out and back in once to turn on pairtime's GNOME extension.");
	} else if (focus.gnome_extension === 'unticked') {
		words.push("Run pairtime install again and keep pairtime's GNOME extension.");
	}
	return words;
}

/** Days as a sentence names them: "Monday, November 16", "A and B", "A; B; and C", since each has a comma of its own. */
function daysInWords(dates: string[]): string {
	const days = dates.map((d) => text.day(d));
	if (days.length <= 2) {
		return days.join(' and ');
	}
	return `${days.slice(0, -1).join('; ')}; and ${days.at(-1) ?? ''}`;
}

/**
 * What the week on Today and the Projects page say of the earlier days the
 * agent's answer didn't come back for, YYYY-MM-DD in date order: their
 * times are left out of what the page shows until the page's next ask, a
 * minute later. "" for none.
 */
export function unreadDays(dates: string[]): string {
	if (dates.length === 0) {
		return '';
	}
	const which = dates.length === 1 ? 'that day' : 'those days';
	return `pairtime couldn't read ${daysInWords(dates)} from its agent, so the times here leave ${which} out. It asks again in a minute.`;
}

/** What the session card says when GET /v1/sessions/last didn't come back. */
export function unreadSession(): string {
	return "pairtime couldn't read your last session from its agent. It asks again in a minute.";
}

/**
 * Every sentence copy.ts can say, each once, with sample dates where a
 * sentence names some: copy.test.ts holds them all to en.json's rules.
 */
export function allSentences(): string[] {
	const focus: Focus[] = [
		{ known: false },
		{ known: false, gnome_extension: 'needs_login' },
		{ known: false, gnome_extension: 'unticked' },
		{ known: false, level_c: true },
		{ known: false, level_c: true, gnome_extension: 'needs_login' },
		{ known: false, level_c: true, gnome_extension: 'unticked' },
	];
	return [
		...new Set([
			...counts,
			...Object.values(levelWhy),
			pendingWords,
			...focus.flatMap((f) => focusWords(f)),
			unreadDays(['2026-11-18']),
			unreadDays(['2026-11-17', '2026-11-18']),
			unreadDays(['2026-11-16', '2026-11-17', '2026-11-18']),
			unreadSession(),
		]),
	];
}
