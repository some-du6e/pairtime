// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, test } from 'vitest';
import { check, type Schema } from './schema.ts';

const root: Schema = {
	components: {
		schemas: {
			Thing: {
				type: 'object',
				additionalProperties: false,
				required: ['n', 'at'],
				properties: {
					n: { type: 'integer', minimum: 0, maximum: 10 },
					at: { type: 'string', format: 'date-time' },
					mode: { type: 'string', enum: ['local'] },
					status: { type: 'string', const: 'stopping' },
					list: { type: 'array', items: { type: 'number' } },
					notes: { type: 'object', additionalProperties: { type: 'string' } },
				},
			},
		},
	},
};

const thing: Schema = { $ref: '#/components/schemas/Thing' };

describe('check', () => {
	const cases: { name: string; value: unknown; errors: number }[] = [
		{ name: 'an object with every required field passes', value: { n: 1, at: '2026-11-19T10:49:00Z' }, errors: 0 },
		{ name: 'a missing required field fails', value: { n: 1 }, errors: 1 },
		{ name: 'a field the spec lacks fails when additionalProperties is false', value: { n: 1, at: '2026-11-19T10:49:00Z', x: 1 }, errors: 1 },
		{ name: 'a field named after an Object.prototype member fails when the spec lacks it', value: { n: 1, at: '2026-11-19T10:49:00Z', constructor: 1 }, errors: 1 },
		{ name: 'a __proto__ field in parsed JSON fails when the spec lacks it', value: JSON.parse('{"n": 1, "at": "2026-11-19T10:49:00Z", "__proto__": 1}'), errors: 1 },
		{ name: 'additionalProperties as a schema checks every extra value', value: { n: 1, at: '2026-11-19T10:49:00Z', notes: { a: 'ok', b: 2 } }, errors: 1 },
		{ name: 'integer refuses 1.5', value: { n: 1.5, at: '2026-11-19T10:49:00Z' }, errors: 1 },
		{ name: 'number takes an integer', value: { n: 1, at: '2026-11-19T10:49:00Z', list: [1, 2.5] }, errors: 0 },
		{ name: 'items checks every element', value: { n: 1, at: '2026-11-19T10:49:00Z', list: [1, 'two'] }, errors: 1 },
		{ name: 'minimum refuses a smaller number', value: { n: -1, at: '2026-11-19T10:49:00Z' }, errors: 1 },
		{ name: 'maximum refuses a larger number', value: { n: 11, at: '2026-11-19T10:49:00Z' }, errors: 1 },
		{ name: 'enum refuses a value it does not list', value: { n: 1, at: '2026-11-19T10:49:00Z', mode: 'cloud' }, errors: 1 },
		{ name: 'const refuses any other value', value: { n: 1, at: '2026-11-19T10:49:00Z', status: 'stopped' }, errors: 1 },
		{ name: 'date-time takes RFC 3339 with an offset and fractions', value: { n: 1, at: '2026-11-19T10:49:00.123+01:00' }, errors: 0 },
		{ name: 'date-time refuses a date without a time', value: { n: 1, at: '2026-11-19' }, errors: 1 },
		{ name: 'a wrong type at the top fails once', value: [], errors: 1 },
	];
	for (const c of cases) {
		test(c.name, () => {
			expect(check(c.value, thing, root)).toHaveLength(c.errors);
		});
	}
});

describe('fails closed', () => {
	const cases: { name: string; schema: Schema; message: string }[] = [
		{ name: 'a keyword the checker does not know throws', schema: { type: 'string', pattern: '^a' }, message: '"pattern"' },
		{ name: 'a format the checker does not know throws', schema: { type: 'string', format: 'uri' }, message: 'format "uri"' },
		{ name: 'a reference outside the document throws', schema: { $ref: 'other.yaml#/x' }, message: 'same-document' },
		{ name: 'a reference that leads nowhere throws', schema: { $ref: '#/components/schemas/Nope' }, message: "doesn't resolve" },
	];
	for (const c of cases) {
		test(c.name, () => {
			expect(() => check('a', c.schema, root)).toThrow(c.message);
		});
	}
});
