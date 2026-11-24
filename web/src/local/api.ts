// SPDX-License-Identifier: AGPL-3.0-or-later

// The agent's local API (docs/api/local.openapi.yaml), on the page's own
// origin. $gen/local-api.ts is what `npm run gen` writes from the spec; the
// imports are types only, so `vite build` doesn't need it.

import type { components, paths } from '$gen/local-api.ts';
import { createClient } from '$shared/api/client.ts';

type Schemas = components['schemas'];

export type Status = Schemas['Status'];
export type Mode = Schemas['Mode'];
export type Signals = Schemas['Signals'];
/** GET /v1/days/{date}. */
export type Day = Schemas['Day'];
export type DayProject = Schemas['DayProject'];
/** GET /v1/sessions/last. */
export type LastSession = Schemas['LastSession'];
/** GET /v1/projects: every project this computer knows, and whether its name shows on the owner's dashboard. */
export type Projects = Schemas['Projects'];
/** One project of GET /v1/projects, and PUT /v1/projects/{key}/name's answer. */
export type ProjectEntry = Schemas['ProjectEntry'];
/** Where a project's name stands on the owner's dashboard. */
export type NameState = ProjectEntry['state'];
/** PUT /v1/projects/{key}/name's body. */
export type NameChoice = Schemas['NameChoice'];
export type WorkSession = NonNullable<Schemas['WorkSession']>;
export type ByKind = Schemas['ByKind'];
export type ByEvidence = Schemas['ByEvidence'];
export type ByHarness = Schemas['AgentOnlyByHarness'];
export type Uncredited = Schemas['Uncredited'];

/** An activity kind (SM 5.1). */
export type Kind = keyof ByKind;
/** An uncredited reason code (SM 5.2). */
export type Reason = keyof Uncredited;
/** An evidence level (SM 5.1, DESIGN 6.4). */
export type Level = keyof ByEvidence;
/** A harness (SM 1.1). */
export type Harness = keyof ByHarness;

/**
 * What the agent counts for one project over a span, a local day or a work
 * session: the fields DayProject and WorkSession share. While display.shadow
 * is on the agent leaves the credited fields and on_hold_s out
 * (D-shadow-scope), so each is optional, and model.ts reads a missing
 * number as 0 and a missing map as {}.
 */
export type Tally = Pick<
	DayProject,
	| 'credited_s'
	| 'pending_s'
	| 'credited_by_kind'
	| 'credited_by_evidence'
	| 'agent_only_s'
	| 'agent_only_by_harness'
	| 'uncredited'
	| 'on_hold_s'
>;

export const api = createClient<paths>();

/**
 * What Today says about focus (D-focus-unknown), as GET /v1/status's
 * signals give it (WP-OS9; D-design-5.9).
 */
export interface Focus {
	/** False while signals.focus.unknown is set: the desktop doesn't say which window is in front. */
	known: boolean;
	/** On GNOME, why pairtime's Shell extension isn't reporting. */
	gnome_extension?: Signals['gnome_extension'];
	/** True while the agent reads idle time, where the scorer credits such time at level C (WP-SC8). */
	level_c?: boolean;
}

/**
 * The status's focus as Today reads it. An agent always serves the
 * dashboard it embeds, so its status has signals; one that somehow lacks
 * them reads as focus known, and the page says nothing about focus.
 */
export function focusOf(status: Status): Focus {
	const s = status.signals as Signals | undefined;
	return { known: s?.focus?.unknown === undefined, gnome_extension: s?.gnome_extension, level_c: s?.focus_level_c };
}

/** The mode in words for the teen (DESIGN 4.2). */
export function describeMode(mode: Mode): string {
	switch (mode) {
		case 'local':
			return 'Local only: nothing leaves this computer';
		case 'online':
			return 'Online: the minutes pairtime counts go to your pairtime server, as numbers';
		default:
			// The UI ships inside the agent, so this is a mode added in Go
			// without words here yet.
			return String(mode);
	}
}
