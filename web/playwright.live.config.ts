// SPDX-License-Identifier: AGPL-3.0-or-later

import { defineConfig } from '@playwright/test';

// WP-SV8's live dashboard check (tests/live/stream.live.ts), kept apart
// from playwright.config.ts's specs: it needs no built UI, only pairtimed's
// live harness, TestLiveServer in cmd/pairtimed/live_test.go, which
// rehearsal.yml builds with `go test -c` and names in PAIRTIMED_LIVE. The
// harness serves pairtimed behind a TLS proxy with a certificate of its
// own, so the browser ignores certificate errors. Chromium only.
const bin = process.env.PAIRTIMED_LIVE;
if (!bin) {
	throw new Error('set PAIRTIMED_LIVE to the test binary of cmd/pairtimed: go test -c -o <file> ./cmd/pairtimed');
}

// The harness's control server listens here, and the spec, which runs in
// worker processes, finds it through the same variable.
const control = process.env.PAIRTIME_LIVE_CONTROL ?? '4181';
process.env.PAIRTIME_LIVE_CONTROL = control;

export default defineConfig({
	testDir: 'tests/live',
	testMatch: '**/*.live.ts',
	forbidOnly: Boolean(process.env.CI),
	retries: 0,
	workers: 1,
	reporter: process.env.CI ? [['list'], ['github']] : 'list',
	outputDir: process.env.PAIRTIME_LIVE_OUT ?? 'test-results/live',
	projects: [{ name: 'chromium', use: { browserName: 'chromium', ignoreHTTPSErrors: true } }],
	webServer: {
		command: `"${bin}" -test.run '^TestLiveServer$' -test.timeout 0 -test.v`,
		env: { PAIRTIME_LIVE_CONTROL: control },
		url: `http://127.0.0.1:${control}/ready`,
		reuseExistingServer: false,
		timeout: 120_000,
		stdout: 'pipe',
		stderr: 'pipe',
		gracefulShutdown: { signal: 'SIGTERM', timeout: 10_000 },
	},
});
