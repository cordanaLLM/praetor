// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>
// SPDX-License-Identifier: EUPL-1.2
// @vitest-environment node
import { createRequire } from 'node:module';
import * as fs from 'node:fs/promises';
import path from 'node:path';
import { ESLint } from 'eslint';
import { afterEach, expect, test, vi } from 'vitest';
import { fixtureDeadline, fixtureIO, temporaryFixtures } from './fixtures';

/** Keeps filesystem behavior real while allowing deterministic delays of mkdtemp completion. */
vi.mock('node:fs/promises', async (importOriginal) => {
	const original = await importOriginal<typeof fs>();
	return { ...original, mkdtemp: vi.fn(original.mkdtemp) };
});

const require = createRequire(import.meta.url);
const eslintRequire = createRequire(require.resolve('eslint'));
const typescriptRequire = createRequire(require.resolve('typescript-eslint'));
const parserRequire = createRequire(typescriptRequire.resolve('@typescript-eslint/parser'));
const estreeEntry = parserRequire.resolve('@typescript-eslint/typescript-estree');
const estree = parserRequire('@typescript-eslint/typescript-estree');
const fixtures = temporaryFixtures('minimatch-');
const { mkdir, writeFile } = fixtureIO;

afterEach(async () => {
	estree.clearCaches();
	await fixtures.cleanup();
});

test('fixture deadlines reject stalled I/O by operation name', async () => {
	await expect(
		fixtureDeadline('planted stalled I/O', () => new Promise<never>(() => {}))
	).rejects.toThrow('Fixture planted stalled I/O deadline exceeded');
});

test('fixture root bound refuses concurrent creation and cleanup permits reuse', async () => {
	const pending = fixtures.create();
	expect(() => fixtures.create()).toThrow('Fixture root count exceeds 1');
	const root = await pending;
	const file = path.join(root, 'boundary.txt');
	await writeFile(file, 'fixture boundary');
	expect(await fixtureIO.readFile(file)).toBe('fixture boundary');
	await fixtures.cleanup();
	await expect(fixtureIO.readFile(file)).rejects.toThrow('ENOENT');
	expect(await fixtures.create()).not.toBe(root);
});

/** Verifies cleanup removes a real directory returned after the caller's creation deadline. */
test('fixture cleanup removes a late mkdtemp directory after the creation deadline', async () => {
	const lateFixtures = temporaryFixtures('minimatch-late-');
	const creation = Promise.withResolvers<string>();
	const mkdtemp = vi.mocked(fs.mkdtemp).mockReturnValueOnce(creation.promise);
	let root: string | undefined;
	try {
		await expect(lateFixtures.create()).rejects.toThrow('Fixture mkdtemp deadline exceeded');
		mkdtemp.mockReset();
		root = await fs.mkdtemp(path.resolve('test-results', 'minimatch-late-'));
		const file = path.join(root, 'late.txt');
		await writeFile(file, 'late creation');
		const cleanup = lateFixtures.cleanup();
		const outcome = Promise.allSettled([cleanup]);
		creation.resolve(root);
		await outcome;
		await expect(fixtureIO.readFile(file)).rejects.toThrow('ENOENT');
		await expect(cleanup).resolves.toBeUndefined();
	} finally {
		mkdtemp.mockReset();
		if (root !== undefined) await fixtureIO.rm(root);
	}
});

/** Verifies cleanup reports stalled creation by name rather than waiting without a bound. */
test('fixture cleanup rejects a mkdtemp that never resolves by operation name', async () => {
	const stalledFixtures = temporaryFixtures('minimatch-stalled-');
	const creation = Promise.withResolvers<string>();
	const mkdtemp = vi.mocked(fs.mkdtemp).mockReturnValueOnce(creation.promise);
	try {
		await expect(stalledFixtures.create()).rejects.toThrow('Fixture mkdtemp deadline exceeded');
		await expect(stalledFixtures.cleanup()).rejects.toThrow(
			'Fixture mkdtemp cleanup deadline exceeded'
		);
	} finally {
		mkdtemp.mockReset();
	}
});

test.each([
	['eslint', require.resolve('eslint')],
	['@eslint/config-array', eslintRequire.resolve('@eslint/config-array')],
	['@typescript-eslint/typescript-estree', estreeEntry]
])('%s resolves a minimatch version patched for GHSA-g3hv-75wg-m249', (_, entry) => {
	const parentRequire = createRequire(entry);
	const { version } = parentRequire('minimatch/package.json');
	const minimatchOverride = require('../package.json').pnpm.overrides.minimatch;
	const semver = createRequire(estreeEntry)('semver');
	expect(version).toBe(minimatchOverride);
	expect(semver.gte(version, '11.0.1')).toBe(true);
});

test('ESLint and config-array select nested files and exclude ignored files', async () => {
	const root = await fixtures.create();
	await mkdir(path.join(root, 'src/deep'));
	await mkdir(path.join(root, 'src/ignored'));
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
	const root = await fixtures.create();
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
