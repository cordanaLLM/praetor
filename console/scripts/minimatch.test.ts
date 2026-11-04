// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>
// SPDX-License-Identifier: EUPL-1.2
// @vitest-environment node
import { mkdir, mkdtemp, rm, writeFile } from 'node:fs/promises';
import { createRequire } from 'node:module';
import path from 'node:path';
import { ESLint } from 'eslint';
import { afterEach, expect, test } from 'vitest';

const require = createRequire(import.meta.url);
const eslintRequire = createRequire(require.resolve('eslint'));
const typescriptRequire = createRequire(require.resolve('typescript-eslint'));
const parserRequire = createRequire(typescriptRequire.resolve('@typescript-eslint/parser'));
const estreeEntry = parserRequire.resolve('@typescript-eslint/typescript-estree');
const estree = parserRequire('@typescript-eslint/typescript-estree');
const roots: string[] = [];

afterEach(async () => {
	estree.clearCaches();
	for (const root of roots) await rm(root, { recursive: true, force: true });
	roots.length = 0;
});

/** Creates an isolated temporary directory for test fixtures and tracks it for cleanup. */
async function fixture(): Promise<string> {
	await mkdir('test-results', { recursive: true });
	const root = await mkdtemp(path.resolve('test-results/minimatch-'));
	roots.push(root);
	return root;
}

test.each([
	['eslint', require.resolve('eslint')],
	['@eslint/config-array', eslintRequire.resolve('@eslint/config-array')],
	['@typescript-eslint/typescript-estree', estreeEntry]
])('%s resolves a minimatch version patched for GHSA-g3hv-75wg-m249', (_, entry) => {
	const parentRequire = createRequire(entry);
	const { version } = parentRequire('minimatch/package.json');
	const semver = createRequire(estreeEntry)('semver');
	expect(semver.gte(version, '11.0.1')).toBe(true);
});

test('ESLint and config-array select nested files and exclude ignored files', async () => {
	const root = await fixture();
	await mkdir(path.join(root, 'src/deep'), { recursive: true });
	await mkdir(path.join(root, 'src/ignored'), { recursive: true });
	await writeFile(path.join(root, 'src/deep/keep.js'), 'export const answer = 42;');
	await writeFile(path.join(root, 'src/ignored/skip.js'), 'const unused = 1;');
	const eslint = new ESLint({
		cwd: root,
		overrideConfigFile: true,
		overrideConfig: [
			{ ignores: ['**/ignored/**'] },
			{ files: ['src/**/*.{js,mjs}'], rules: { 'no-unused-vars': 'error' } }
		]
	});
	const results = await eslint.lintFiles(['src/**/*.js']);
	expect(results.map((result) => path.relative(root, result.filePath))).toEqual([
		path.join('src', 'deep', 'keep.js')
	]);
	expect(results[0]?.errorCount).toBe(0);
	await expect(eslint.lintFiles(['missing/**/*.js'])).rejects.toThrow('No files matching');
});

test('typescript-estree matches dot files in allowDefaultProject and rejects other files', async () => {
	const root = await fixture();
	await writeFile(path.join(root, 'tsconfig.json'), '{"include":["included.ts"]}');
	await writeFile(path.join(root, 'included.ts'), 'export {};');
	const options = {
		tsconfigRootDir: root,
		projectService: { allowDefaultProject: ['.*.ts'] }
	};
	const source = 'const answer: number = 42;';
	const result = estree.parseAndGenerateServices(source, {
		...options,
		filePath: path.join(root, '.lint.ts')
	});
	expect(result.services.program).toBeDefined();
	expect(() =>
		estree.parseAndGenerateServices(source, {
			...options,
			filePath: path.join(root, 'outside.ts')
		})
	).toThrow('allowDefaultProject');
});
