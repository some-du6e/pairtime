// SPDX-License-Identifier: AGPL-3.0-or-later

// Read by vite-plugin-svelte (vite.config.ts and vitest.config.ts name this
// file) and by svelte-check. Svelte 5 compiles TypeScript itself, so there
// is no preprocessor.

/** @type {import('@sveltejs/vite-plugin-svelte').SvelteConfig} */
export default {
	compilerOptions: {
		// Every component uses runes ($props, $state); the legacy syntax is
		// an error, not a second way to write a component.
		runes: true,
	},
};
