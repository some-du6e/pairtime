// SPDX-License-Identifier: AGPL-3.0-or-later

// Checks a JSON value against an OpenAPI 3.1 schema, for the subset of JSON
// Schema that docs/api/*.openapi.yaml uses. Playwright uses it to hold the
// running agent's answers to the spec the UI's types come from. It fails
// closed: a keyword or format it doesn't know throws, so nothing passes
// unchecked. Add a keyword here, with a case in schema.test.ts, before the
// spec uses it.

export type Schema = { readonly [keyword: string]: unknown };

// Keywords that only describe.
const annotations = new Set(['description', 'title', 'summary', 'examples', 'deprecated']);
const assertions = new Set([
	'$ref',
	'type',
	'properties',
	'required',
	'additionalProperties',
	'items',
	'enum',
	'const',
	'minimum',
	'maximum',
	'format',
]);

const rfc3339 = /^\d{4}-\d{2}-\d{2}[Tt]\d{2}:\d{2}:\d{2}(\.\d+)?([Zz]|[+-]\d{2}:\d{2})$/;

/** Returns every way value fails schema; [] means it conforms. */
export function check(value: unknown, schema: Schema, root: Schema = schema, at = '$'): string[] {
	const errors: string[] = [];
	for (const keyword of Object.keys(schema)) {
		if (!annotations.has(keyword) && !assertions.has(keyword)) {
			throw new Error(`${at}: the schema uses "${keyword}", which tests/support/schema.ts doesn't check`);
		}
	}
	if (schema.$ref !== undefined) {
		errors.push(...check(value, resolve(root, String(schema.$ref)), root, at));
	}
	if (schema.type !== undefined) {
		const types = Array.isArray(schema.type) ? (schema.type as string[]) : [String(schema.type)];
		if (!types.some((t) => isType(value, t))) {
			errors.push(`${at}: want ${types.join(' or ')}, got ${kind(value)}`);
			return errors;
		}
	}
	if (schema.enum !== undefined && !(schema.enum as unknown[]).some((v) => same(v, value))) {
		errors.push(`${at}: ${JSON.stringify(value)} isn't one of ${JSON.stringify(schema.enum)}`);
	}
	if (schema.const !== undefined && !same(schema.const, value)) {
		errors.push(`${at}: want ${JSON.stringify(schema.const)}, got ${JSON.stringify(value)}`);
	}
	if (typeof value === 'number') {
		if (typeof schema.minimum === 'number' && value < schema.minimum) {
			errors.push(`${at}: ${value} is below the minimum ${schema.minimum}`);
		}
		if (typeof schema.maximum === 'number' && value > schema.maximum) {
			errors.push(`${at}: ${value} is above the maximum ${schema.maximum}`);
		}
	}
	if (schema.format !== undefined) {
		if (schema.format !== 'date-time') {
			throw new Error(`${at}: tests/support/schema.ts doesn't check format "${String(schema.format)}"`);
		}
		if (typeof value === 'string' && !(rfc3339.test(value) && !Number.isNaN(Date.parse(value)))) {
			errors.push(`${at}: ${JSON.stringify(value)} isn't an RFC 3339 date-time`);
		}
	}
	if (Array.isArray(value) && schema.items !== undefined) {
		value.forEach((item, i) => errors.push(...check(item, schema.items as Schema, root, `${at}[${i}]`)));
	}
	if (isObject(value)) {
		const properties = (schema.properties ?? {}) as Record<string, Schema>;
		for (const name of (schema.required ?? []) as string[]) {
			if (!Object.hasOwn(value, name)) {
				errors.push(`${at}: the required field "${name}" is missing`);
			}
		}
		for (const [name, v] of Object.entries(value)) {
			// Own properties only: properties is a plain object, so
			// properties["constructor"] would find Object.prototype's.
			const sub = Object.hasOwn(properties, name) ? properties[name] : undefined;
			if (sub !== undefined) {
				errors.push(...check(v, sub, root, `${at}.${name}`));
			} else if (schema.additionalProperties === false) {
				errors.push(`${at}: the field "${name}" isn't in the spec`);
			} else if (isObject(schema.additionalProperties)) {
				errors.push(...check(v, schema.additionalProperties as Schema, root, `${at}.${name}`));
			}
		}
	}
	return errors;
}

/** Follows a "#/..." JSON pointer within root. */
export function resolve(root: Schema, ref: string): Schema {
	if (!ref.startsWith('#/')) {
		throw new Error(`only same-document references are supported, not ${ref}`);
	}
	let node: unknown = root;
	for (const raw of ref.slice(2).split('/')) {
		const key = raw.replaceAll('~1', '/').replaceAll('~0', '~');
		if (!isObject(node) || !Object.hasOwn(node, key)) {
			throw new Error(`${ref} doesn't resolve`);
		}
		node = node[key];
	}
	if (!isObject(node)) {
		throw new Error(`${ref} isn't a schema`);
	}
	return node;
}

function isObject(v: unknown): v is Record<string, unknown> {
	return typeof v === 'object' && v !== null && !Array.isArray(v);
}

function kind(v: unknown): string {
	if (v === null) return 'null';
	if (Array.isArray(v)) return 'array';
	if (typeof v === 'number') return Number.isInteger(v) ? 'integer' : 'number';
	return typeof v;
}

function isType(v: unknown, type: string): boolean {
	const k = kind(v);
	return k === type || (type === 'number' && k === 'integer');
}

function same(a: unknown, b: unknown): boolean {
	return JSON.stringify(a) === JSON.stringify(b);
}
