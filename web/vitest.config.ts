// SPDX-License-Identifier: AGPL-3.0-or-later

import { fileURLToPath } from 'node:url';
import { svelte } from '@sveltejs/vite-plugin-svelte';
import { defineConfig } from 'vitest/config';

const here = (path: string) => fileURLToPath(new URL(path, import.meta.url));

// Unit tests run in Node, with no DOM. Components are rendered with
// svelte/server and checked as HTML; Playwright checks them in Chromium
// (tests/*.spec.ts).
export default defineConfig({
	plugins: [svelte({ configFile: here('svelte.config.js') })],
	resolve: {
		alias: { $shared: here('src/shared'), $gen: here('src/gen') },
	},
	test: {
		include: ['src/**/*.test.ts', 'tests/**/*.test.ts'],
		environment: 'node',
		// Times of day are written in the local zone, as the CLI writes
		// them; the tests read them in UTC wherever they run.
		env: { TZ: 'UTC' },
	},
});
