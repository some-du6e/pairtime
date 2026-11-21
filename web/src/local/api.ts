// SPDX-License-Identifier: AGPL-3.0-or-later

// The agent's local API (docs/api/local.openapi.yaml), on the page's own
// origin. $gen/local-api.ts is what `npm run gen` writes from the spec; the
// imports are types only, so `vite build` doesn't need it.

import type { components, paths } from '$gen/local-api.ts';
import { createClient } from '$shared/api/client.ts';

export type Status = components['schemas']['Status'];
export type Mode = components['schemas']['Mode'];

export const api = createClient<paths>();

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
