// SPDX-License-Identifier: AGPL-3.0-or-later

// The Privacy page rendered to HTML with svelte/server, held to the files
// it speaks for: docs/retention.json, the one source for how long pairtime
// keeps each kind of data (DESIGN 4.4), and internal/agent/inbox, whose
// KeepDays says when the agent deletes a file of hook calls it can't read
// (DESIGN 3.5). Playwright checks the built page (tests/dashboard.spec.ts).

import { readdirSync, readFileSync } from 'node:fs';
import { render } from 'svelte/server';
import { describe, expect, test } from 'vitest';
import type { Mode } from '../api.ts';
import { inboxKeepDays, keptText } from '../retention.ts';
import Privacy from './Privacy.svelte';
import { rules } from '../../../tests/support/copy-rules.ts';

/** The text a page shows, without tags, comments or entities. */
function text(html: string): string {
	return html
		.replace(/<!--[\s\S]*?-->/g, '')
		.replace(/<[^>]*>/g, ' ')
		.replaceAll('&#39;', "'")
		.replaceAll('&quot;', '"')
		.replaceAll('&lt;', '<')
		.replaceAll('&amp;', '&')
		.replace(/\s+/g, ' ')
		.trim();
}

/** The rows of the table of what pairtime keeps: each row's data-id, and the text of its two cells. */
function rows(html: string): { id: string; data: string; kept: string }[] {
	const body = /<tbody\b[^>]*>([\s\S]*?)<\/tbody>/.exec(html)?.[1] ?? '';
	return [...body.matchAll(/<tr\b[^>]*\sdata-id="([^"]*)"[^>]*>([\s\S]*?)<\/tr>/g)].map((m) => {
		const cells = [...(m[2] ?? '').matchAll(/<(th|td)\b[^>]*>([\s\S]*?)<\/\1>/g)].map((c) => text(c[2] ?? ''));
		return { id: m[1] ?? '', data: cells[0] ?? '', kept: cells[1] ?? '' };
	});
}

/** The page's sentences: those of each heading, paragraph, table cell and list item. */
function sentences(html: string): string[] {
	return [...html.matchAll(/<(h1|h2|p|th|td|li)\b[^>]*>([\s\S]*?)<\/\1>/g)]
		.flatMap((m) => text(m[2] ?? '').split(/\.\s+/))
		.filter((s) => s !== '');
}

interface Entry {
	id: string;
	where: string;
	data: string;
	kept: string;
	days?: number;
}

const retention = JSON.parse(readFileSync(new URL('../../../../docs/retention.json', import.meta.url), 'utf8')) as {
	spot_check_window: { months: number; provisional?: string };
	entries: Entry[];
};

/** retention.json's entries for this computer, in the file's order. */
const device = retention.entries.filter((e) => e.where === 'device');

/** inbox.KeepDays, read from the Go source of internal/agent/inbox. */
function keepDays(): number {
	const dir = new URL('../../../../internal/agent/inbox/', import.meta.url);
	const found = readdirSync(dir)
		.filter((f) => f.endsWith('.go') && !f.endsWith('_test.go'))
		.flatMap((f) => [...readFileSync(new URL(f, dir), 'utf8').matchAll(/^(?:const)?\s*KeepDays\s*=\s*(\d+)\s*(?:\/\/.*)?$/gm)])
		.map((m) => Number(m[1]));
	expect(found, 'the KeepDays constant in internal/agent/inbox').toHaveLength(1);
	return found[0] ?? 0;
}

describe('Privacy', () => {
	const page = (mode: Mode = 'local') => render(Privacy, { props: { mode } }).body;
	const cases: { name: string; check: (html: string) => void }[] = [
		{
			name: 'in local mode it says nothing has left this computer',
			check: (html) => expect(text(html)).toContain('Nothing. pairtime is in local-only mode'),
		},
		{
			name: "each device entry of docs/retention.json is a row, in the file's order, with its data and how long it is kept",
			check: (html) => {
				expect(device.length).toBeGreaterThan(0);
				expect(rows(html).slice(0, device.length)).toEqual(
					device.map((e) => ({ id: e.id, data: e.data, kept: keptText(e, retention.spot_check_window) })),
				);
			},
		},
		{
			name: 'every row has its period filled in, with no {days} or {window} left, and an id of its own',
			check: (html) => {
				const all = rows(html);
				expect(all.length).toBe(device.length + 1);
				for (const r of all) {
					expect(r.kept, r.id).not.toMatch(/\{\w*\}/);
					expect(r.kept, r.id).not.toBe('');
				}
				expect(new Set(all.map((r) => r.id)).size).toBe(all.length);
			},
		},
		{
			name: "the table says pairtime deletes each kind once its time is up, which the agent's retention sweeps do (WP-LG6)",
			check: (html) => expect(text(html)).toContain('pairtime deletes each of these once it has been kept as long as the table says.'),
		},
		{
			name: "the prompt similarity sketches (MinHash) are on the list, as in DESIGN 4.4's row for them",
			check: (html) => expect(rows(html).filter((r) => r.data.includes('MinHash'))).toHaveLength(1),
		},
		{
			name: "the inbox's row comes last, and says its hook calls hold your prompts as plain text, not encrypted",
			check: (html) => {
				const inbox = rows(html).at(-1);
				expect(inbox?.id).toBe('device.inbox');
				expect(inbox?.data).toContain('your prompts, its replies, the commands it ran with their output, and the files it edited');
				expect(inbox?.data).toContain('plain text, not encrypted');
				expect(inbox?.kept).toContain('Until the agent has read it');
			},
		},
		{
			name: "the inbox's days are inbox.KeepDays in the Go source, so the words change with it",
			check: (html) => {
				const n = keepDays();
				expect(inboxKeepDays).toBe(n);
				expect(rows(html).at(-1)?.kept).toContain(`deletes it ${n === 1 ? '1 day' : `${n} days`} later`);
			},
		},
		{
			name: 'it says only the vault is encrypted: every other sentence about encryption says not encrypted',
			check: (html) => {
				const said = sentences(html).filter((s) => /encrypt/i.test(s));
				expect(said.filter((s) => /\bvault, which it encrypts\b/.test(s))).toHaveLength(1);
				for (const s of said.filter((s) => !/\bvault, which it encrypts\b/.test(s))) {
					expect(s).toMatch(/\b(?:not|isn't) encrypted\b/i);
				}
			},
		},
		{
			name: 'Who can read it says the vault keeps prompts, replies and recap paths encrypted, but not from a program running as you, and how to keep no prompt text: the command, then the edit by hand for either kind of config.toml',
			check: (html) => {
				const t = text(html);
				expect(t).toContain(
					"pairtime keeps the text of your prompts, the agent's final reply to each one and the file paths for your recaps in a vault, which it encrypts",
				);
				expect(t).toContain('but not from a program running as you');
				// The command first, which edits either kind of config.toml
				// (internal/agent/config's TestKeepTextEdits).
				expect(t).toContain('To keep no prompt text at all, type pairtime privacy keep-text off in a terminal.');
				expect(t).toContain(
					"Or, by hand, set keep_prompt_text = false under [privacy] in pairtime's config.toml; if the file has no [privacy] line, as one written before this version has, add these two lines at the end",
				);
				expect(t).toContain('pairtime then deletes the text it kept');
				// The table's header too: a config.toml from before [privacy]
				// ends in [notifications], where the switch alone would land.
				const block = /data-testid="keep-off"[^>]*>([\s\S]*?)<\/pre>/.exec(html)?.[1]?.replace(/<[^>]*>/g, '');
				expect(block).toBe('[privacy]\nkeep_prompt_text = false');
			},
		},
		{
			name: 'Who can read it says the inbox and the hook calls the agent holds keep prompts as plain text until the agent has read them, which a program running as you can read',
			check: (html) => {
				const t = text(html);
				expect(t).toContain('Until the agent has read them, the inbox and the hook calls the agent holds keep your prompts too');
				expect(t).toContain('Those two are plain text, not encrypted.');
				expect(t).toContain('so can any program running as you');
			},
		},
		{
			name: 'Delete everything says pairtime privacy delete deletes the key and everything kept, but for the names of projects this computer doesn\'t know of, that it sets pairtime back to local-only mode, and that the button comes later',
			check: (html) => {
				const t = text(html);
				expect(t).toContain("pairtime privacy delete , typed in a terminal, deletes pairtime's key and everything pairtime keeps on this computer");
				expect(t).toContain(
					"apart from pairtime itself, its settings without the names you gave the projects this computer knows of, and install's record of what it changed, with its copies of the files it edited, so that uninstall can put them back.",
				);
				expect(t).toContain(
					'Names under projects this computer doesn\'t know of stay, such as those another computer sharing your config.toml wrote or an older version of pairtime left behind, and pairtime says how many.',
				);
				expect(t).toContain('It also sets pairtime back to local-only mode, so pairtime uploads nothing until you sign in again.');
				expect(t).toContain('Copies a backup or a synced folder already holds stay as they are');
				expect(t).not.toContain('Nobody can bring');
				expect(t).toContain('A button here comes in a later version of pairtime.');
			},
		},
		{
			name: "Who can read it says the MinHash sketches are kept unencrypted, as DESIGN 4.4 keeps them in clear",
			check: (html) =>
				expect(text(html)).toContain('The prompt similarity sketches (MinHash) in the table are not encrypted either.'),
		},
		{
			name: "What pairtime never records says that while T3 Code runs it notes once a minute whether you're at the computer, and nothing about which app is in front (D-relayed-context, as #225 amended it)",
			check: (html) => {
				const t = text(html);
				expect(t).toContain("Anything while you aren't working in a project");
				expect(t).toContain("One exception: while T3 Code runs, pairtime notes once a minute whether you're at the computer");
				expect(t).toContain('It notes nothing about which app is in front.');
			},
		},
		{
			name: "What pairtime never records says the Claude Code T3 Code keeps open between your messages isn't work in a project, but a reply it's writing is, and so is T3 Code's own window in front, as DESIGN 3.3's presence row and doctor say (D-relayed-context)",
			check: (html) =>
				expect(text(html)).toContain(
					"The Claude Code that T3 Code keeps open between your messages isn't work in a project, but a reply it's writing is, and so is T3 Code's own window in front, until 10 minutes after the reply ends or you switch away.",
				),
		},
		{
			name: 'Who can read it says the dashboard opens only in the browser pairtime dashboard signed in',
			check: (html) =>
				expect(text(html)).toContain('This dashboard opens only on this computer, in the browser that pairtime dashboard signed in.'),
		},
		{
			name: "its words keep the CLI's rules: nothing scolds, sets a target or counts a streak, and no dash or curly quote",
			check: (html) => {
				for (const s of sentences(html)) {
					for (const r of rules) {
						expect(r.breaks(s), `${r.name}: ${s}`).toEqual([]);
					}
				}
			},
		},
		{
			name: 'no element has a style attribute, which the CSP would block',
			check: (html) => expect(html).not.toMatch(/\sstyle=/),
		},
	];
	for (const c of cases) {
		test(c.name, () => c.check(page()));
	}
});

describe("each period reads as pairtimed's notice says it (retention.Policy.Kept)", () => {
	const twelve = { months: 12 };
	const cases: { name: string; kept: string; days?: number; window: { months: number; provisional?: string }; want: string }[] = [
		{ name: "{days} becomes the entry's days", kept: '{days}, then deleted', days: 30, window: twelve, want: '30 days, then deleted' },
		{ name: 'one day is singular', kept: '{days}', days: 1, window: twelve, want: '1 day' },
		{
			name: '{window} counts months after the decision',
			kept: 'until ({window})',
			window: twelve,
			want: 'until (12 months after the decision)',
		},
		{
			name: 'a provisional window says it is a default',
			kept: 'until ({window})',
			window: { months: 12, provisional: 'DESIGN 13.2' },
			want: 'until (12 months after the decision, a default until Hack Club sets the window)',
		},
		{ name: 'one month is singular', kept: '{window}', window: { months: 1 }, want: '1 month after the decision' },
		{ name: 'text without placeholders is unchanged', kept: 'While your account exists', window: twelve, want: 'While your account exists' },
		{
			name: 'both placeholders in one text are filled',
			kept: '{days}, or longer while a submission uses them ({window})',
			days: 180,
			window: { months: 12, provisional: 'DESIGN 13.2' },
			want: '180 days, or longer while a submission uses them (12 months after the decision, a default until Hack Club sets the window)',
		},
	];
	for (const c of cases) {
		test(c.name, () => expect(keptText({ kept: c.kept, days: c.days }, c.window)).toBe(c.want));
	}
});
