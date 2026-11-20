// SPDX-License-Identifier: AGPL-3.0-or-later

// What the dashboard says, in one place, so a reviewer can read all of it
// and vitest can hold it to the spec and to DESIGN 3.8's copy rules: the
// teen's credited time is "your time", the agent's time is named after the
// agent, and other uncredited time is described for what it was, never as
// the teen's failure (PA 4.4). No streaks, no targets, no ratios: the chess
// clock is information (PA 4.3).

import type { Focus, Kind, Level, Reason } from './api.ts';

/** What pairtime counts, for the explainer on Today. */
export const counts = [
	'pairtime counts the time you spend building with AI: thinking up and typing prompts, reading what the agent wrote, watching it work, answering its questions, running tests and commands, and editing code yourself.',
	"Time the agent spends working while nobody needs to be there is the agent's own. It goes on the agent's clock, next to yours. The two clocks aren't a race. A long agent clock is fine, and usually means the agent did big jobs while you steered.",
];

export interface LevelWords {
	level: Level;
	/** A short name for the legend. */
	name: string;
	/** Why a minute counts at this level, in a sentence or two. */
	why: string;
}

/** Why a minute counts at level A, B or C (SM 5.1, DESIGN 6.4, D11). */
export const levels: LevelWords[] = [
	{
		level: 'A',
		name: 'Typing seen',
		why: 'Your computer saw you typing around the prompt, edit or command that started this time, and the tool you used reported that act itself.',
	},
	{
		level: 'B',
		name: 'Input seen',
		why: "Your computer saw your keyboard or mouse, but it can't tell typing apart from other input. Most Windows and Linux computers work this way. The tool you used reported the act itself.",
	},
	{
		level: 'C',
		name: 'Less to go on',
		why: "pairtime had less to go on. The act came from pairtime track or a plugin with no typing seen before it, or from an editor pairtime couldn't check, or the desktop doesn't say which window is in front. Level C time counts here on your dashboard, but not on your profile, your build days or most programs.",
	},
];

/** Words for minutes that aren't final yet (DESIGN 3.7, 6.2 D1). */
export function pendingWords(windowMinutes: string): string {
	return `Lighter minutes are pending. pairtime waits ${windowMinutes} after each minute in case more of what you did arrives late, so these can still change. Then they're final.`;
}

/** What calibrating means, while display.shadow is on (DESIGN 6.7). */
export const shadowWords =
	"pairtime is calibrating. You're one of the people helping tune its numbers, so it counts your time but keeps it hidden until the numbers are ready. Your agent's time still shows.";

/** The word that stands in for credited time while display.shadow is on. */
export const calibrating = 'Calibrating';

/**
 * D-focus-unknown's sentence for the Today page while /v1/status says the
 * desktop doesn't tell pairtime which window is in front, word for word,
 * with its GNOME additions.
 */
export function focusWords(focus: Focus): string[] {
	const words = [
		"This desktop doesn't tell pairtime which window is in front, so your time here counts only on your own dashboard, not on your profile, build days or most programs.",
	];
	if (focus.gnome_extension === 'needs_login') {
		words.push("Log out and back in once to turn on pairtime's GNOME extension.");
	} else if (focus.gnome_extension === 'unticked') {
		words.push("Run pairtime install again and keep pairtime's GNOME extension.");
	}
	return words;
}

export interface KindWords {
	kind: Kind;
	label: string;
	about: string;
}

/** Activity kinds (SM 5.1, 5.2) in plain words, in the spec's order. */
export const kinds: KindWords[] = [
	{ kind: 'prompting', label: 'Writing prompts', about: 'Thinking up and typing what you asked the agent.' },
	{ kind: 'reading_output', label: "Reading the agent's answers", about: 'Reading what the agent wrote, after it stopped.' },
	{ kind: 'supervising', label: 'Watching the agent work', about: 'Reading what the agent showed while it was still running.' },
	{
		kind: 'reviewing',
		label: 'Reviewing and deciding',
		about: "Answering the agent's permission questions, stopping it, looking through code, and committing.",
	},
	{ kind: 'testing', label: 'Running tests and builds', about: 'Tests and builds you typed, and the time they ran.' },
	{ kind: 'debugging', label: 'Running commands', about: 'Other commands you typed, and the time they ran.' },
	{ kind: 'hand_edit', label: 'Editing code yourself', about: 'Changes you typed in your editor.' },
	{ kind: 'lookup', label: 'Looking things up', about: 'Short trips to a browser or a chat between two things you did in the project.' },
];

export interface ReasonWords {
	reason: Reason;
	label: string;
	about: string;
}

/**
 * Uncredited reasons (SM 5.2) in plain words, in the spec's order. Each
 * says what the time was, and a loop's says what ends it, so it reads as a
 * lesson (DESIGN 3.8). X.agent_only's label names the agent; agentOnly
 * gives it.
 */
export const reasons: ReasonWords[] = [
	{ reason: 'X.absent', label: 'Away', about: 'No keyboard or mouse for a while, a locked screen, or a sleeping computer.' },
	{ reason: 'X.other_focus', label: 'In another app', about: "Your keyboard and mouse were in an app that isn't part of a project." },
	{
		reason: 'X.ambiguous',
		label: 'Not sure which project',
		about: "Two projects shared the window in front, and pairtime couldn't tell which one you were on.",
	},
	{
		reason: 'X.no_anchor',
		label: 'Nothing to count it from',
		about: 'You were here, but nothing you did, such as a prompt, an edit or a command, was close enough in time to count it.',
	},
	{
		reason: 'X.agent_only',
		label: 'The agent working on its own',
		about: "The agent ran, and nothing you did needed you then. That's the agent's time, and it's on the agent's clock.",
	},
	{
		reason: 'X.automated',
		label: 'Started without you',
		about: 'Turns that a schedule, a script or another agent started, such as headless runs and subagents.',
	},
	{
		reason: 'X.no_input',
		label: 'A prompt with no typing before it',
		about: 'A prompt arrived with no keyboard or mouse before it, as a scripted one does.',
	},
	{
		reason: 'X.reroll',
		label: 'Asking again',
		about: 'The same request a third time with nothing changed in between. A change of yours, such as an edit, a command or a commit, starts the count over.',
	},
	{
		reason: 'X.rewrite_loop',
		label: 'The agent going in circles',
		about: 'The agent rewrote the same lines again and again. An edit, a command or a commit of yours ends the loop.',
	},
	{
		reason: 'X.same_error',
		label: 'The same error coming back',
		about: 'The same error kept coming back after short retries. Saying what you think is wrong, or fixing it by hand, ends the loop.',
	},
	{
		reason: 'X.autopilot',
		label: 'Replies faster than reading',
		about: "Short replies sent before there was time to read the agent's long answers. Reading the answer, or a change of yours, ends it.",
	},
	{
		reason: 'X.noprog',
		label: 'On hold',
		about: 'On hold: these come back when you fix it. After two hours of your time with no commit, passing test or fixed error, pairtime counts half until one arrives.',
	},
	{ reason: 'X.lookup_cap', label: 'Long lookups', about: 'Time in a browser or a chat past what one lookup counts.' },
	{
		reason: 'X.bucket_cap',
		label: 'Reading past the answer',
		about: "Reading time longer than the agent's answers take to read.",
	},
	{ reason: 'X.day_cap', label: 'Past 12 hours', about: 'pairtime counts at most 12 hours in a day.' },
];

/** X.agent_only's label for a project whose one agent has a name. */
export function agentOnly(agent: string): string {
	return `${agent} working on its own`;
}
