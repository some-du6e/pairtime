// SPDX-License-Identifier: AGPL-3.0-or-later

// The words only the dashboard says: what pairtime counts, why a minute
// counts at level A, B or C, what pending means, and D-focus-unknown's
// sentence. Everything the CLI views say too, a kind, a reason, a level's
// short words, the chess clock, comes from internal/coach/text/en.json
// through text.ts, so both say a minute the same way (DESIGN 3.8).
// copy.test.ts holds these to the same rules as en.json: nothing scolds,
// sets a target or counts a streak (PA 4.3, 4.4).

import type { Focus, Level } from './api.ts';

/** What pairtime counts, for the explainer on Today. */
export const counts = [
	'pairtime counts the time you spend building with AI: thinking up and typing prompts, reading what the agent wrote, watching it work, answering its questions, running tests and commands, and editing code yourself.',
	"When the agent works on its own, that time is the agent's. It goes on the agent's clock, next to yours. The two clocks aren't a race. A long agent clock usually means the agent did big jobs while you steered.",
];

/** Why a minute counts at each level, beside en.json's short words for it (SM 5.1, DESIGN 6.4, 6.2 D11). */
export const levelWhy: Record<Level, string> = {
	A: 'Your computer saw you typing around the prompt, edit or command that started this time, and the tool you used reported that act itself.',
	B: "Your computer saw your keyboard or mouse, but it can't tell typing apart from other input. Most Windows and Linux computers work this way. The tool you used reported the act itself.",
	C: "pairtime had less to go on. The act came from pairtime track or a plugin with no typing seen before it, or from an editor pairtime couldn't check, or the desktop doesn't say which window is in front. Level C time shows here on your dashboard, but not on your profile, your build days or most programs.",
};

/**
 * What pending means (DESIGN 3.7, 6.2 D1). PENDING_WINDOW_S is 900 s in
 * every parameter table so far; copy.test.ts fails if a table changes it,
 * so these words change with it.
 */
export const pendingWords =
	"Lighter minutes are pending. pairtime waits 15 minutes after each minute in case more of what you did arrives late, so these can still change. Then they're final.";

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
