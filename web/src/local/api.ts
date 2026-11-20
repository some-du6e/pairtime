// SPDX-License-Identifier: AGPL-3.0-or-later

// The agent's local API (docs/api/local.openapi.yaml), on the page's own
// origin. $gen/local-api.ts is what `npm run gen` writes from the spec; the
// imports are types only, so `vite build` doesn't need it.

import type { components, paths } from '$gen/local-api.ts';
import { createClient } from '$shared/api/client.ts';

type Schemas = components['schemas'];

export type Status = Schemas['Status'];
export type Mode = Schemas['Mode'];
export type Focus = Schemas['Focus'];
export type Days = Schemas['Days'];
export type Day = Schemas['Day'];
export type ProjectDay = Schemas['ProjectDay'];
export type Project = Schemas['Project'];
export type ByEvidence = Schemas['ByEvidence'];
export type ByKind = Schemas['ByKind'];
export type Uncredited = Schemas['Uncredited'];
export type AgentTime = Schemas['AgentTime'];
export type Harness = Schemas['Harness'];

/** An activity kind (SM 5.1), as ByKind names it. */
export type Kind = keyof ByKind;
/** An uncredited reason code (SM 5.2), as Uncredited names it. */
export type Reason = keyof Uncredited;
/** An evidence level (SM 5.1, DESIGN 6.4). */
export type Level = keyof ByEvidence;

export const api = createClient<paths>();

/** The mode in words for the teen (DESIGN 4.2). */
export function describeMode(mode: Mode): string {
	switch (mode) {
		case 'local':
			return 'Local only: nothing leaves this computer';
		default:
			// The UI ships inside the agent, so this is a mode added in Go
			// without words here yet.
			return String(mode);
	}
}
