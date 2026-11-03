// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>
// SPDX-License-Identifier: EUPL-1.2
// @vitest-environment node
import { spawnSync, type SpawnSyncReturns } from 'node:child_process';
import { copyFileSync, mkdirSync, mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { afterEach, describe, it, expect } from 'vitest';

const roots: string[] = [];
const MAX_ROOTS = 16;
/** Bounds temporary-root cleanup and rejects an unexpectedly large fixture set. */
afterEach(() => {
	if (roots.length > MAX_ROOTS) throw new Error(`Temporary root count exceeds ${MAX_ROOTS}`);
	for (let i = 0; i < MAX_ROOTS && i < roots.length; i++) {
		const root = roots[i];
		if (root === undefined) throw new Error(`Missing temporary root at index ${i}`);
		rmSync(root, { recursive: true, force: true });
	}
	roots.length = 0;
});

/** Exercises malformed reports through the same diagnostic path as unreadable JSON. */
describe('malformed planted reports', () => {
	it.each([
		{ label: 'missing suites', content: {} },
		{ label: 'null report', content: null },
		{ label: 'non-array suites', content: { errors: [], suites: {} } },
		{ label: 'missing errors', content: { suites: [] } },
		{ label: 'non-array errors', content: { errors: {}, suites: [] } },
		{ label: 'non-array nested suites', content: { errors: [], suites: [{ suites: {} }] } },
		{ label: 'non-array specs', content: { errors: [], suites: [{ specs: {} }] } },
		{ label: 'missing tests', content: { errors: [], suites: [{ specs: [{}] }] } },
		{ label: 'non-array tests', content: { errors: [], suites: [{ specs: [{ tests: {} }] }] } },
		{ label: 'missing results', content: { errors: [], suites: [{ specs: [{ tests: [{}] }] }] } },
		{
			label: 'non-array results',
			content: { errors: [], suites: [{ specs: [{ tests: [{ results: {} }] }] }] }
		}
	])(
		'$label names the report and quotes runner output',
		/** Verifies malformed JSON shapes retain the report path and runner diagnostics. */
		({ content }) => {
			const run = runHarness(JSON.stringify(content));
			expect(run.status).toBe(1);
			expect(run.stderr).toMatch(
				/Cannot read or parse planted Playwright report .*planted-results\.json/
			);
			expect(run.stderr).toMatch(/stdout first lines: "GitCommitInfo: timeout of 3000ms exceeded/);
			expect(run.stderr).toMatch(/stderr first lines: "runner diagnostic/);
		}
	);
});

/** Covers reports that contain no executed tests. */
describe('empty planted proofs', () => {
	/** Verifies skipped test results cannot serve as planted proof. */
	it('rejects a report with only skipped results', () => {
		const skipped = structuredClone(report);
		const result = skipped.suites[0]?.specs[0]?.tests[0]?.results[0];
		if (result === undefined) throw new Error('Skipped fixture must contain a result');
		result.status = 'skipped';
		result.errors = [];
		const run = runHarness(JSON.stringify(skipped));
		expect(run.status).toBe(1);
		expect(run.stderr).toMatch(/Planted proof rejected:/);
		expect(run.stdout).not.toMatch(/Planted proof:/);
	});

	/** Verifies an empty suite list cannot serve as planted proof. */
	it('rejects a report with an empty suites list', () => {
		const run = runHarness(JSON.stringify({ errors: [], suites: [] }));
		expect(run.status).toBe(1);
		expect(run.stderr).toMatch(/Planted proof rejected:/);
		expect(run.stdout).not.toMatch(/Planted proof:/);
	});
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

/** Runs the planted-failure harness in an isolated temporary root; returns the child process execution result. */
function runHarness(content: string | null, stale = false): SpawnSyncReturns<string> {
	const resultsRoot = fileURLToPath(new URL('../test-results/', import.meta.url));
	mkdirSync(resultsRoot, { recursive: true });
	const root = mkdtempSync(join(resultsRoot, 'harness-'));
	roots.push(root);
	const packageRoot = join(root, 'node_modules/@playwright/test');
	mkdirSync(packageRoot, { recursive: true });
	writeFileSync(
		join(packageRoot, 'package.json'),
		JSON.stringify({ exports: { './cli': './cli.cjs' } })
	);
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

/** Covers absent and malformed optional fields beside valid proof. */
describe('optional planted report fields', () => {
	it.each(['suites', 'specs'])(
		'rejects null %s beside valid proof and retains runner diagnostics',
		/** Verifies null child fields cannot hide beside otherwise valid proof. */
		(field) => {
			const malformed = { ...report, suites: [...report.suites, { [field]: null }] };
			const run = runHarness(JSON.stringify(malformed));
			expect(run.status).toBe(1);
			expect(run.stderr).toMatch(
				/Cannot read or parse planted Playwright report .*planted-results\.json/
			);
			expect(run.stderr).toMatch(/stdout first lines: "GitCommitInfo: timeout of 3000ms exceeded/);
			expect(run.stderr).toMatch(/stderr first lines: "runner diagnostic/);
			expect(run.stdout).not.toMatch(/Planted proof:/);
		}
	);

	/** Verifies omitted optional specs remain valid beside an executed proof. */
	it('accepts omitted specs beside valid proof', () => {
		const omitted = { ...report, suites: [...report.suites, { suites: [] }] };
		const run = runHarness(JSON.stringify(omitted));
		expect(run.status, run.stderr).toBe(0);
		expect(run.stdout).toMatch(/Planted proof: Playwright exit 1/);
	});
});

describe('expect-planted-failure', () => {
	it('reads the report file despite non-JSON stdout', () => {
		const run = runHarness(JSON.stringify(report));
		expect(run.status, run.stderr).toBe(0);
		expect(run.stdout).toMatch(/Planted proof: Playwright exit 1/);
	});

	it('missing report names the file and quotes stdout and stderr first lines', () => {
		const run = runHarness(null);
		expect(run.status).toBe(1);
		expect(run.stderr).toMatch(
			/Cannot read or parse planted Playwright report .*planted-results\.json/
		);
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
