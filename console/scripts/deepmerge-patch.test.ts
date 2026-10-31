// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>
// SPDX-License-Identifier: EUPL-1.2
// @vitest-environment node
// patches/deepmerge@4.3.1.patch (GHSA-gcqx-w48m-w2h4): the copy of deepmerge that
// @sveltejs/vite-plugin-svelte loads must refuse an own "__proto__" key, which upstream 4.3.1
// assigns onto the result when the merge target has no prototype.
import { createRequire } from 'node:module';
import { fileURLToPath } from 'node:url';
import { expect, test } from 'vitest';

type Merge = (target: object, source: object, options?: object) => Record<string, unknown>;

const plugin = fileURLToPath(import.meta.resolve('@sveltejs/vite-plugin-svelte'));
const deepmerge = createRequire(plugin)('deepmerge') as Merge;
const poisoned = (): object => JSON.parse('{"__proto__":{"polluted":"yes"},"kept":{"x":1}}');

test('null-prototype target: own __proto__ key does not set the result prototype', () => {
	const result = deepmerge(Object.create(null), poisoned());
	expect(Object.getPrototypeOf(result)).toBe(Object.prototype);
	expect(result.polluted).toBeUndefined();
	expect(result.kept).toEqual({ x: 1 });
});

test('plain target: own __proto__ key is dropped, Object.prototype untouched', () => {
	const result = deepmerge({}, poisoned());
	expect(result.polluted).toBeUndefined();
	expect(({} as Record<string, unknown>).polluted).toBeUndefined();
});

test('ordinary merge still merges nested keys and replaces arrays as the plugin asks', () => {
	const result = deepmerge(
		{ a: { b: 1 }, list: [1, 2] },
		{ a: { c: 2 }, list: [3] },
		{ arrayMerge: (_t: unknown[], s: unknown[]) => s }
	);
	expect(result).toEqual({ a: { b: 1, c: 2 }, list: [3] });
});

test('boundary: empty source and a key merely named like the prototype are handled', () => {
	expect(deepmerge(Object.create(null), {})).toEqual({});
	const result = deepmerge({}, { proto: 1, ['__proto__x']: 2 });
	expect(result).toEqual({ proto: 1, __proto__x: 2 });
});
