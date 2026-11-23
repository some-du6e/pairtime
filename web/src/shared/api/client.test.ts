// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, test } from 'vitest';
import { type Client, createClient } from './client.ts';

// Shaped like openapi-typescript's output, so these calls are type-checked
// the way the UI's are.
interface TestPaths {
	'/v1/thing': {
		get: {
			responses: {
				200: { content: { 'application/json': { n: number } } };
				401: { content: { 'text/plain': string } };
			};
		};
		post?: never;
	};
	'/v1/do': {
		get?: never;
		post: { responses: { 202: { content: { 'application/json': { status: 'stopping' } } } } };
	};
	'/v1/days/{date}': {
		get: { responses: { 200: { content: { 'application/json': { date: string } } } } };
		post?: never;
	};
	'/v1/things/{key}/name': {
		get?: never;
		post?: never;
		put: { responses: { 200: { content: { 'application/json': { state: string } } } } };
	};
}

function json(status: number, body: string): Response {
	return new Response(body, { status, headers: { 'Content-Type': 'application/json' } });
}

function text(status: number, body: string): Response {
	return new Response(body, { status, headers: { 'Content-Type': 'text/plain; charset=utf-8' } });
}

describe('answers', () => {
	const cases: { name: string; reply: () => Response; want: object }[] = [
		{
			name: 'a 200 JSON answer is the data',
			reply: () => json(200, '{"n":1}'),
			want: { ok: true, status: 200, data: { n: 1 } },
		},
		{
			name: 'a 401 means this browser is signed out',
			reply: () => text(401, 'unauthorized: send the bearer token from port.json\n'),
			want: { ok: false, problem: 'signed-out', status: 401, detail: 'unauthorized: send the bearer token from port.json' },
		},
		{
			name: 'a 403 means the agent refused the Host or Origin',
			reply: () => text(403, 'forbidden: requests from other sites are refused'),
			want: { ok: false, problem: 'forbidden', status: 403 },
		},
		{
			name: 'any other error status is unexpected and keeps the status and body',
			reply: () => text(500, 'boom'),
			want: { ok: false, problem: 'unexpected', status: 500, detail: 'boom' },
		},
		{
			name: 'a 2xx answer that is not JSON is unexpected',
			reply: () => text(200, 'hello'),
			want: { ok: false, problem: 'unexpected', status: 200 },
		},
		{
			name: 'a 2xx answer whose JSON is broken is unexpected',
			reply: () => json(200, '{'),
			want: { ok: false, problem: 'unexpected', status: 200 },
		},
		{
			name: 'no answer at all means the agent is unreachable',
			reply: () => {
				throw new TypeError('Failed to fetch');
			},
			want: { ok: false, problem: 'unreachable', status: 0 },
		},
	];
	for (const c of cases) {
		test(c.name, async () => {
			const api = createClient<TestPaths>({ fetch: async () => c.reply() });
			expect(await api.get('/v1/thing')).toMatchObject(c.want);
		});
	}
});

describe('requests', () => {
	const cases: { name: string; send: (api: Client<TestPaths>) => Promise<unknown>; want: object }[] = [
		{
			name: 'a GET goes to the page origin with same-origin credentials, no cache and no redirects',
			send: (api) => api.get('/v1/thing'),
			want: {
				url: '/v1/thing',
				method: 'GET',
				credentials: 'same-origin',
				cache: 'no-store',
				redirect: 'error',
				body: undefined,
			},
		},
		{
			name: "a GET's query goes into the URL, leaving out parameters without a value",
			send: (api) => api.get('/v1/thing', { query: { from: '2026-10-22', to: '2026-11-20', skip: undefined } }),
			want: { url: '/v1/thing?from=2026-10-22&to=2026-11-20', method: 'GET' },
		},
		{
			name: 'a GET with only empty parameters has no query at all',
			send: (api) => api.get('/v1/thing', { query: { skip: undefined } }),
			want: { url: '/v1/thing' },
		},
		{
			name: "a path's {name} segment takes its value, URL-encoded",
			send: (api) => api.get('/v1/days/{date}', { params: { date: '2026-11-20' } }),
			want: { url: '/v1/days/2026-11-20', method: 'GET' },
		},
		{
			name: 'a value with a slash stays inside its segment',
			send: (api) => api.get('/v1/days/{date}', { params: { date: '../status' } }),
			want: { url: '/v1/days/..%2Fstatus' },
		},
		{
			name: 'a POST sends its body as JSON',
			send: (api) => api.post('/v1/do', { body: { why: 'test' } }),
			want: { url: '/v1/do', method: 'POST', body: '{"why":"test"}', contentType: 'application/json' },
		},
		{
			name: "a PUT fills its path's segments and sends its body as JSON, with same-origin credentials",
			send: (api) => api.put('/v1/things/{key}/name', { params: { key: 'g7c41e9' }, body: { online: true, label: 'chip8' } }),
			want: {
				url: '/v1/things/g7c41e9/name',
				method: 'PUT',
				body: '{"online":true,"label":"chip8"}',
				contentType: 'application/json',
				credentials: 'same-origin',
			},
		},
	];
	for (const c of cases) {
		test(c.name, async () => {
			let seen: object | undefined;
			const api = createClient<TestPaths>({
				fetch: async (input, init) => {
					const headers = new Headers(init?.headers);
					seen = {
						url: String(input),
						method: init?.method,
						credentials: init?.credentials,
						cache: init?.cache,
						redirect: init?.redirect,
						body: init?.body,
						contentType: headers.get('Content-Type') ?? undefined,
					};
					return json(202, '{"status":"stopping"}');
				},
			});
			await c.send(api);
			expect(seen).toMatchObject(c.want);
		});
	}
});

describe('path parameters', () => {
	test('a path whose {name} has no value sends nothing and says which', async () => {
		let sent = false;
		const api = createClient<TestPaths>({
			fetch: async () => {
				sent = true;
				return json(200, '{}');
			},
		});
		expect(await api.get('/v1/days/{date}')).toMatchObject({ ok: false, problem: 'unexpected', detail: '/v1/days/{date} needs a value for {date}' });
		expect(sent).toBe(false);
	});
});

describe('aborts', () => {
	test('a request its caller aborted rejects instead of reporting the agent down', async () => {
		const stop = new AbortController();
		const api = createClient<TestPaths>({
			fetch: async () => {
				stop.abort();
				throw new DOMException('aborted', 'AbortError');
			},
		});
		await expect(api.get('/v1/thing', { signal: stop.signal })).rejects.toThrow('aborted');
	});
});
