// SPDX-License-Identifier: AGPL-3.0-or-later

// The words only the dashboard says: what pairtime counts, why a minute
// counts at level A, B or C, what pending means, and what a page says when
// the agent's answer didn't come back. Everything the CLI views say too, a
// kind, a reason, a level's short words, the chess clock, comes from
// internal/coach/text/en.json through text.ts, so both say a minute the
// same way (DESIGN 3.8). D-focus-unknown's sentence, which pairtime today
// says too, is the exception: focusWords below and en.json each hold it,
// and text_test.go's TestFocusWordsAreTheDashboards keeps the two the same.
// copy.test.ts holds every sentence here, allSentences(), to the same
// rules as en.json: nothing scolds, sets a target or counts a streak (PA
// 4.3, 4.4).

import type { Focus, Level, NameState } from './api.ts';
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
 * untrue (D-design-5.9). WP-SC8 adds it here, with signals.focus_level_c.
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
 * which window is in front (signals.focus.unknown; D-focus-unknown), with
 * its GNOME additions. Where such time counts it says only once
 * focus.level_c is true, signals.focus_level_c, which WP-SC8 sets, the
 * first WP in which that time is level C (D-design-5.9). Until then the
 * sentence stops at its first clause: focus being unknown alone doesn't
 * make the claim true.
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
 * Who sees a project's name the owner shows on their dashboard, beside each
 * switch (D-owner-names): them alone, and nothing else about the project
 * goes with it.
 */
export const nameWho =
	"Only you see it, on your own pairtime dashboard while you're signed in. Nothing else about the project leaves this computer.";

/**
 * Where a project's name stands on the owner's dashboard, by its state in
 * GET /v1/projects. code is the project's short code, as the server gave
 * it, "" while it gave none, online says the names turned on go to the
 * server now, and why says, for a hidden name, why the dashboard doesn't
 * show it when the owner didn't hide it there.
 */
export function nameWords(state: NameState, code: string, online: boolean, why?: string): string {
	const called = code !== '' ? `Project ${code}` : 'by its code';
	switch (state) {
		case 'on':
			return 'Your dashboard shows it.';
		case 'sending':
			return online
				? "It goes to your dashboard with pairtime's next upload."
				: 'It goes to your dashboard once this computer is linked and online.';
		case 'removing':
			return online
				? `Your dashboard shows it until pairtime's next upload, then calls the project ${called}.`
				: `Your dashboard shows it until this computer is online again, then calls the project ${called}.`;
		case 'hidden':
			switch (why) {
				case 'dropped':
					return 'The server no longer has it, as after this computer was signed out a long time or its data was restored, so pairtime turned it off here too. Turn it on to show it again.';
				case 'signed_in':
					return "This computer signed in to a server that never showed it, so pairtime turned it off here. Turn it on to show it on this server's dashboard.";
			}
			return "You hid it on your dashboard, so it's off here too. Turn it on to show it again.";
		default:
			return code !== ''
				? `Your dashboard calls the project ${called}.`
				: 'Your dashboard gives the project a code once its first minutes reach the server, and pairtime shows the code here within a day, or when the agent next starts.';
	}
}

/**
 * What a project with no name on this computer says in place of its
 * switch. arg is what `pairtime projects show-name` takes for it: its
 * code, or its key while it has none (model.ts's projectArg).
 */
export function noNameWords(arg: string): string {
	return `This project has no name on this computer. To show one on your dashboard, run pairtime projects show-name ${arg} with the name.`;
}

/** What a switch says when the agent didn't take its change, with the agent's words. */
export function nameRefused(detail: string): string {
	return `pairtime couldn't change this: ${detail}`;
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
			nameWho,
			...(['off', 'on', 'sending', 'removing', 'hidden'] as const).flatMap((state) => [
				nameWords(state, '7c41e9', true),
				nameWords(state, '7c41e9', false),
				nameWords(state, '', true),
				nameWords(state, '', false),
			]),
			nameWords('hidden', '7c41e9', true, 'dropped'),
			nameWords('hidden', '7c41e9', true, 'signed_in'),
			noNameWords('d2a8f6'),
			noNameWords('dd2a8f61c94e07b35a8c1f6e29d4b7a03'),
			nameRefused('config.toml has an error'),
		]),
	];
}
