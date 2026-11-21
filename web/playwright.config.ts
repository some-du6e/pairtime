// SPDX-License-Identifier: AGPL-3.0-or-later

import { mkdtempSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { defineConfig, type PlaywrightTestConfig } from '@playwright/test';
import { agentPort, dashboardPort, galleryPort } from './tests/support/env.ts';

// Chromium only (DESIGN 3.13). Run `npm run build` first. Three servers:
// `vite preview` for the component gallery; `vite preview` for the built
// dashboard, whose API the specs answer with a fake agent
// (tests/support/fake-agent.ts); and, when PAIRTIME_BIN names a pairtime
// binary built after `npm run build`, that agent with the real dashboard
// embedded. web.yml sets PAIRTIME_BIN; in CI the run fails without it
// rather than skip the smoke test.
const bin = process.env.PAIRTIME_BIN;
if (process.env.CI && !bin) {
	throw new Error('set PAIRTIME_BIN to the pairtime binary built after `npm run build`');
}

// The agent gets a data directory of its own, never the teen's. The runner
// makes it once; worker processes load this file too and inherit it.
if (bin && !process.env.PAIRTIME_E2E_HOME) {
	const home = mkdtempSync(join(tmpdir(), 'pairtime-e2e-'));
	writeFileSync(join(home, 'config.toml'), `[http]\nport = ${agentPort}\n`);
	process.env.PAIRTIME_E2E_HOME = home;
}

type WebServer = Extract<NonNullable<PlaywrightTestConfig['webServer']>, unknown[]>[number];

const servers: WebServer[] = [
	{
		command: `node_modules/.bin/vite preview --mode gallery --port ${galleryPort} --strictPort`,
		url: `http://127.0.0.1:${galleryPort}/`,
		reuseExistingServer: !process.env.CI,
	},
	{
		command: `node_modules/.bin/vite preview --mode agent --port ${dashboardPort} --strictPort`,
		url: `http://127.0.0.1:${dashboardPort}/`,
		reuseExistingServer: !process.env.CI,
	},
];
if (bin && process.env.PAIRTIME_E2E_HOME) {
	servers.push({
		command: `"${bin}" agent`,
		env: { PAIRTIME_HOME: process.env.PAIRTIME_E2E_HOME },
		url: `http://127.0.0.1:${agentPort}/`,
		reuseExistingServer: false,
		gracefulShutdown: { signal: 'SIGTERM', timeout: 5000 },
	});
}

export default defineConfig({
	testDir: 'tests',
	testMatch: '**/*.spec.ts',
	forbidOnly: Boolean(process.env.CI),
	retries: 0,
	reporter: process.env.CI ? [['list'], ['github']] : 'list',
	projects: [{ name: 'chromium', use: { browserName: 'chromium' } }],
	webServer: servers,
});
