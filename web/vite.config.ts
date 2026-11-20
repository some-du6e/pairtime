// SPDX-License-Identifier: AGPL-3.0-or-later

import { mkdirSync, writeFileSync } from 'node:fs';
import { relative, sep } from 'node:path';
import { fileURLToPath } from 'node:url';
import { svelte } from '@sveltejs/vite-plugin-svelte';
import { defineConfig, type Plugin } from 'vite';

const here = (path: string) => fileURLToPath(new URL(path, import.meta.url));

// Each UI builds on its own, `vite build --mode <name>`, so the local
// dashboard carries only the code it uses. All three share src/shared/
// (DESIGN 3.11). Vite reserves the mode name "local" for .env.local files,
// so the local dashboard's mode is "agent", after the binary that embeds it.
const apps = {
	// The local dashboard, src/local. The agent embeds internal/webui/dist
	// with go:embed and serves it on 127.0.0.1, so this build replaces the
	// checked-in placeholder there (DESIGN 3.9, 3.12).
	agent: { root: 'src/local', outDir: '../internal/webui/dist' },
	// The server UI, which pairtimed will embed once it has pages
	// (WP-SV8, WP-SV9).
	server: { root: 'src/server', outDir: 'dist/server' },
	// Every shared component on one page, for Playwright and for whoever
	// works on them. Never shipped.
	gallery: { root: 'src/gallery', outDir: 'dist/gallery' },
} as const;

type AppName = keyof typeof apps;

function isApp(mode: string): mode is AppName {
	return Object.hasOwn(apps, mode);
}

// The page may load and fetch only from the agent or server that sent it:
// no CDN, no web font, no inline script or style. A <meta> carries it, so
// the policy travels with the files whichever binary serves them. Only
// builds get it, because the dev server injects inline styles.
// frame-ancestors can't be set from a <meta>; the agent sends
// X-Frame-Options: DENY instead (internal/agent/localhttp). TestFS in
// internal/webui holds the built dashboard to exactly these directives, so
// a change here changes cspWant there too.
const contentSecurityPolicy = [
	"default-src 'none'",
	"script-src 'self'",
	"style-src 'self'",
	"img-src 'self'",
	"font-src 'self'",
	"connect-src 'self'",
	"manifest-src 'self'",
	"base-uri 'none'",
	"form-action 'self'",
].join('; ');

function csp(): Plugin {
	return {
		name: 'pairtime-csp',
		apply: 'build',
		transformIndexHtml: () => [
			{
				tag: 'meta',
				attrs: { 'http-equiv': 'Content-Security-Policy', content: contentSecurityPolicy },
				injectTo: 'head-prepend',
			},
		],
	};
}

// Writes dist/bundle-<mode>.json, the record of where a build's code came
// from: every module in its chunks with the length of the code it rendered
// there, and every file it emitted as an asset. Paths are from web/, and
// the modules a bundler or plugin writes itself keep their ids, which start
// with \0. tools/ci/npm_notices.py reads the dashboard's record to check
// that release archives carry the license of every npm package with code in
// it. The record sits beside the builds, outside every outDir, so no binary
// embeds it.
function bundleReport(mode: AppName): Plugin {
	const web = here('.');
	const name = (id: string) => (id.startsWith('\0') ? id : relative(web, id).split(sep).join('/'));
	return {
		name: 'pairtime-bundle-report',
		apply: 'build',
		writeBundle(_, bundle) {
			const modules: { id: string; rendered: number }[] = [];
			const assets = new Set<string>();
			for (const file of Object.values(bundle)) {
				if (file.type === 'chunk') {
					for (const [id, { renderedLength }] of Object.entries(file.modules)) {
						modules.push({ id: name(id), rendered: renderedLength });
					}
				} else {
					for (const original of file.originalFileNames) {
						assets.add(name(original));
					}
				}
			}
			modules.sort((a, b) => (a.id < b.id ? -1 : a.id > b.id ? 1 : 0));
			const report = { build: mode, modules, assets: [...assets].sort() };
			mkdirSync(here('dist'), { recursive: true });
			writeFileSync(here(`dist/bundle-${mode}.json`), `${JSON.stringify(report, null, '\t')}\n`);
		},
	};
}

export default defineConfig(({ command, mode, isPreview }) => {
	// A build or preview names its UI, so a typo can't overwrite the
	// dashboard the agent embeds. The dev server, and svelte-check, which
	// loads this file to find the Svelte plugin, default to the dashboard.
	if (!isApp(mode) && (command === 'build' || isPreview)) {
		throw new Error(`vite: --mode must be one of ${Object.keys(apps).join(', ')}, not "${mode}"`);
	}
	const appName: AppName = isApp(mode) ? mode : 'agent';
	const app = apps[appName];
	return {
		root: here(app.root),
		// Assets are referenced from the site root; the agent serves the
		// dashboard at /.
		base: '/',
		publicDir: false,
		plugins: [svelte({ configFile: here('svelte.config.js') }), csp(), bundleReport(appName)],
		resolve: {
			alias: { $shared: here('src/shared'), $gen: here('src/gen') },
		},
		build: {
			outDir: here(app.outDir),
			// outDir is outside root for the local build, where Vite empties it
			// only when asked.
			emptyOutDir: true,
			// Vite 8's "baseline widely available" set, written out so a Vite
			// upgrade can't raise it quietly. The oldest browser that matters is
			// Safari on macOS 12, where 2015-2016 Macs top out (CP 1.5), and it
			// updates to 16.4 and later.
			target: ['chrome111', 'edge111', 'firefox114', 'safari16.4', 'ios16.4'],
			// No data: URIs, so img-src 'self' holds.
			assetsInlineLimit: 0,
			// No polyfill for <link rel="modulepreload">. It would put Rolldown's
			// copy of the one Vite adapted from es-module-shims at the top of
			// every page, and no package in node_modules holds that code's
			// license as a file, which the notices check needs
			// (tools/ci/npm_notices.py). The pages have no use for it: each loads
			// one script and no modulepreload links, and a preload from 127.0.0.1
			// saves nothing. On Firefox 114 and Safari 16.4, which lack
			// modulepreload, it would also watch every node added to the page.
			modulePreload: { polyfill: false },
			sourcemap: false,
			reportCompressedSize: false,
		},
		server: { host: '127.0.0.1', port: 5173, strictPort: true },
		preview: { host: '127.0.0.1', port: 4173, strictPort: true },
	};
});
