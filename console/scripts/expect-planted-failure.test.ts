// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>
// SPDX-License-Identifier: EUPL-1.2
// @vitest-environment node
import { spawnSync, type SpawnSyncReturns } from 'node:child_process';
import { copyFileSync, mkdirSync, mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { afterEach, describe, it, expect } from 'vitest';

const roots: string[] = [];
afterEach(() => {
	for (const root of roots) rmSync(root, { recursive: true, force: true });
	roots.length = 0;
});

const report = {
	errors: [] as unknown[],
	suites: [
		{
			specs: [
				{
					tests: [
						{
							results: [
								{
									status: 'failed',
									errors: [{ message: 'expect(received).toEqual(expected)' }],
									error: { message: 'expect(received).toEqual(expected)' },
									attachments: [
										{
											name: 'planted-violations',
											body: Buffer.from('["image-alt","color-contrast"]').toString('base64')
										}
									]
								}
							]
						}
					]
				}
			]
		}
	]
};

function runHarness(content: string | null, stale = false): SpawnSyncReturns<string> {
	const resultsRoot = fileURLToPath(new URL('../test-results/', import.meta.url));
	mkdirSync(resultsRoot, { recursive: true });
	const root = mkdtempSync(join(resultsRoot, 'harness-'));
	roots.push(root);
	const packageRoot = join(root, 'node_modules/@playwright/test');
	mkdirSync(packageRoot, { recursive: true });
	writeFileSync(join(packageRoot, 'package.json'), JSON.stringify({ exports: { './cli': './cli.cjs' } }));
	writeFileSync(
		join(packageRoot, 'cli.cjs'),
		`const { writeFileSync } = require('node:fs');
const content = ${JSON.stringify(content)};
if (content !== null && process.env.PLAYWRIGHT_JSON_OUTPUT_FILE) {
  writeFileSync(process.env.PLAYWRIGHT_JSON_OUTPUT_FILE, content);
}
process.stdout.write('GitCommitInfo: timeout of 3000ms exceeded\\nsecond stdout line\\n');
process.stderr.write('runner diagnostic\\nsecond stderr line\\n');
process.exitCode = 1;
`
	);
	if (stale) {
		mkdirSync(join(root, 'test-results'), { recursive: true });
		writeFileSync(join(root, 'test-results/planted-results.json'), JSON.stringify(report));
	}
	const harnessSrc = fileURLToPath(new URL('./expect-planted-failure.mjs', import.meta.url));
	copyFileSync(harnessSrc, join(root, 'harness.mjs'));
	const run = spawnSync(process.execPath, [join(root, 'harness.mjs')], {
		cwd: root,
		encoding: 'utf8',
		timeout: 10000
	});
	if (run.error) throw run.error;
	return run;
}

describe('expect-planted-failure', () => {
	it('reads the report file despite non-JSON stdout', () => {
		const run = runHarness(JSON.stringify(report));
		expect(run.status, run.stderr).toBe(0);
		expect(run.stdout).toMatch(/Planted proof: Playwright exit 1/);
	});

	it('missing report names the file and quotes stdout and stderr first lines', () => {
		const run = runHarness(null);
		expect(run.status).toBe(1);
		expect(run.stderr).toMatch(/Cannot read or parse planted Playwright report .*planted-results\.json/);
		expect(run.stderr).toMatch(/stdout first lines: "GitCommitInfo: timeout of 3000ms exceeded/);
		expect(run.stderr).toMatch(/stderr first lines: "runner diagnostic/);
	});

	it('invalid report JSON includes runner diagnostics', () => {
		const run = runHarness('{broken JSON');
		expect(run.status).toBe(1);
		expect(run.stderr).toMatch(/Cannot read or parse planted Playwright report/);
		expect(run.stderr).toMatch(/stdout first lines: "GitCommitInfo: timeout of 3000ms exceeded/);
		expect(run.stderr).toMatch(/stderr first lines: "runner diagnostic/);
	});

	it('a missing new report cannot reuse a stale successful proof', () => {
		const run = runHarness(null, true);
		expect(run.status).toBe(1);
		expect(run.stderr).toMatch(/Cannot read or parse planted Playwright report/);
	});
});
