// SPDX-License-Identifier: AGPL-3.0-or-later

// A typed fetch for pairtime's own APIs. The types come from the OpenAPI
// files in docs/api through openapi-typescript (`npm run gen`, SP 7.3), so a
// route or field that changes in the spec fails `npm run check` wherever the
// UI uses it. Requests go to the page's own origin only.

/** Why a call returned no data, in terms a page can explain. */
export type Problem =
	/** 401: this browser isn't signed in to the agent or server. */
	| 'signed-out'
	/** 403: the agent refused the request's Host or Origin. */
	| 'forbidden'
	/** No answer at all: the agent isn't running, or the network is down. */
	| 'unreachable'
	/** Any other status, or a body that isn't the JSON the spec promises. */
	| 'unexpected';

export type Result<T> =
	| { ok: true; status: number; data: T }
	| { ok: false; problem: Problem; status: number; detail: string };

type Method = 'get' | 'post' | 'put' | 'patch' | 'delete';

/**
 * The paths in an openapi-typescript `paths` type that have an operation
 * for method M. openapi-typescript writes a missing method as `get?: never`,
 * which this leaves out.
 */
export type PathsFor<Paths, M extends Method> = {
	[P in keyof Paths]: Paths[P] extends Record<M, unknown> ? P : never;
}[keyof Paths] &
	string;

/** The operation for method M at path P. */
export type Operation<Paths, P extends keyof Paths, M extends Method> =
	Paths[P] extends Record<M, infer Op> ? Op : never;

type Success = 200 | 201 | 202;

/** The JSON body of an operation's 2xx answers. */
export type SuccessBody<Op> = Op extends { responses: infer R }
	? {
			[S in keyof R & Success]: R[S] extends { content: { 'application/json': infer B } } ? B : never;
		}[keyof R & Success]
	: never;

export interface Client<Paths> {
	get<P extends PathsFor<Paths, 'get'>>(
		path: P,
		init?: { signal?: AbortSignal },
	): Promise<Result<SuccessBody<Operation<Paths, P, 'get'>>>>;
	post<P extends PathsFor<Paths, 'post'>>(
		path: P,
		init?: { body?: unknown; signal?: AbortSignal },
	): Promise<Result<SuccessBody<Operation<Paths, P, 'post'>>>>;
}

export interface ClientOptions {
	/** Prefix for every path. "" (the default) is the page's own origin. */
	base?: string;
	/** For tests. */
	fetch?: typeof fetch;
}

export function createClient<Paths>(options: ClientOptions = {}): Client<Paths> {
	const base = options.base ?? '';
	const send = options.fetch ?? ((input: RequestInfo | URL, init?: RequestInit) => fetch(input, init));

	async function call<T>(method: string, path: string, body: unknown, signal: AbortSignal | undefined): Promise<Result<T>> {
		const headers: Record<string, string> = { Accept: 'application/json' };
		const init: RequestInit = {
			method,
			headers,
			// The dashboard's sign-in is a SameSite=Strict, HttpOnly cookie
			// (DESIGN 3.9), which only same-origin requests carry.
			credentials: 'same-origin',
			cache: 'no-store',
			redirect: 'error',
		};
		if (body !== undefined) {
			headers['Content-Type'] = 'application/json';
			init.body = JSON.stringify(body);
		}
		if (signal) {
			init.signal = signal;
		}
		let res: Response;
		try {
			res = await send(base + path, init);
		} catch (err) {
			// A caller that aborts has moved on; tell it so rather than report
			// the agent as down.
			if (signal?.aborted) {
				throw err;
			}
			return { ok: false, problem: 'unreachable', status: 0, detail: String(err) };
		}
		if (!res.ok) {
			const problem: Problem = res.status === 401 ? 'signed-out' : res.status === 403 ? 'forbidden' : 'unexpected';
			return { ok: false, problem, status: res.status, detail: await textOf(res) };
		}
		const type = res.headers.get('Content-Type') ?? '';
		if (!/^application\/json\b/i.test(type)) {
			return { ok: false, problem: 'unexpected', status: res.status, detail: `expected JSON, got ${type || 'no Content-Type'}` };
		}
		try {
			return { ok: true, status: res.status, data: (await res.json()) as T };
		} catch (err) {
			return { ok: false, problem: 'unexpected', status: res.status, detail: `the body isn't JSON: ${String(err)}` };
		}
	}

	const client: Client<Paths> = {
		get<P extends PathsFor<Paths, 'get'>>(path: P, init?: { signal?: AbortSignal }) {
			return call<SuccessBody<Operation<Paths, P, 'get'>>>('GET', path, undefined, init?.signal);
		},
		post<P extends PathsFor<Paths, 'post'>>(path: P, init?: { body?: unknown; signal?: AbortSignal }) {
			return call<SuccessBody<Operation<Paths, P, 'post'>>>('POST', path, init?.body, init?.signal);
		},
	};
	return client;
}

async function textOf(res: Response): Promise<string> {
	try {
		return (await res.text()).trim();
	} catch {
		return '';
	}
}
