// SPDX-License-Identifier: AGPL-3.0-or-later

// Where the servers playwright.config.ts starts listen, shared by the config
// and the specs.

import { readFileSync } from 'node:fs';
import { join } from 'node:path';

/** `vite preview --mode gallery` serves the component gallery here. */
export const galleryPort = 4173;
export const galleryURL = `http://127.0.0.1:${galleryPort}/`;

/** The agent under test listens here; config.toml in its home says so. */
export const agentPort = Number(process.env.PAIRTIME_E2E_PORT ?? '17247');

/** The agent under test, or undefined when PAIRTIME_BIN isn't set. */
export const agentURL: string | undefined = process.env.PAIRTIME_BIN ? `http://127.0.0.1:${agentPort}/` : undefined;

/**
 * The bearer token the agent under test wrote to port.json in its data
 * directory, which playwright.config.ts made and named in PAIRTIME_E2E_HOME.
 */
export function agentToken(): string {
	const home = process.env.PAIRTIME_E2E_HOME;
	if (!home) {
		throw new Error('PAIRTIME_E2E_HOME is not set; playwright.config.ts sets it when PAIRTIME_BIN is');
	}
	const portFile = JSON.parse(readFileSync(join(home, 'port.json'), 'utf8')) as { token?: unknown };
	if (typeof portFile.token !== 'string' || portFile.token === '') {
		throw new Error(`${join(home, 'port.json')} has no token`);
	}
	return portFile.token;
}
