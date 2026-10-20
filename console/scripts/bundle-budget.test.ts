// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>
// SPDX-License-Identifier: EUPL-1.2
// @vitest-environment node
import { mkdtemp, mkdir, writeFile, rm } from 'node:fs/promises';
import { randomBytes } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import path from 'node:path';
import { build } from 'vite';
import { afterEach, expect, test } from 'vitest';
import { LIMIT, measure } from './bundle-budget.mjs';

const roots: string[] = [];
afterEach(async () => {
	for (const root of roots) await rm(root, { recursive: true, force: true });
	roots.length = 0;
});

async function fixture(): Promise<string> {
	await mkdir('test-results', { recursive: true });
	const root = await mkdtemp(path.resolve('test-results/budget-'));
	roots.push(root);
	return root;
}

test('counts modulepreloads, transitive imports and inline JS once; rejects missing modules', async () => {
	const root = await fixture();
	await writeFile(
		path.join(root, 'index.html'),
		'<link rel="modulepreload" href="/entry.js"><script type="module" src="entry.js"></script><script>window.ready = true;</script>'
	);
	await writeFile(path.join(root, 'entry.js'), 'import "./dep.js";');
	await writeFile(path.join(root, 'dep.js'), 'window.loaded = true;');
	const result = await measure(root);
	expect(result.files).toEqual(['entry.js', 'dep.js']);
	expect(result.bytes).toBeGreaterThan(0);
	expect(result.bytes).toBeLessThan(LIMIT);
	await rm(path.join(root, 'dep.js'));
	await expect(measure(root)).rejects.toThrow('ENOENT');
});

test('planted overrun: a real fixture build importing a large module fails the budget CLI', async () => {
	const root = await fixture();
	await writeFile(path.join(root, 'index.html'), '<script type="module" src="/entry.js"></script>');
	await writeFile(
		path.join(root, 'entry.js'),
		'import payload from "./payload.js"; document.body.textContent = payload;'
	);
	await writeFile(
		path.join(root, 'payload.js'),
		`export default ${JSON.stringify(randomBytes(220000).toString('base64'))};`
	);
	await build({ configFile: false, root, logLevel: 'error', build: { outDir: 'dist' } });
	const output = path.join(root, 'dist');
	const result = await measure(output);
	expect(result.bytes).toBeGreaterThan(LIMIT);
	const cli = spawnSync(process.execPath, ['scripts/bundle-budget.mjs', output], {
		encoding: 'utf8',
		timeout: 30000
	});
	expect(cli.status, cli.stderr).toBe(1);
	expect(cli.stderr).toContain('Bundle budget exceeded');
	expect(cli.stdout).toContain(`${result.bytes} bytes gzip`);
	process.stdout.write(
		`Planted budget proof: ${result.bytes} > ${LIMIT} bytes; CLI exit ${cli.status}\n`
	);
});
