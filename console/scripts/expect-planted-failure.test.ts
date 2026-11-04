// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>
// SPDX-License-Identifier: EUPL-1.2
// @vitest-environment node
import { spawn, spawnSync, type SpawnSyncReturns } from 'node:child_process';
import {
	copyFileSync,
	existsSync,
	mkdirSync,
	mkdtempSync,
	readFileSync,
	rmSync,
	writeFileSync
} from 'node:fs';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { afterEach, describe, it, expect } from 'vitest';

const roots: string[] = [];
const MAX_ROOTS = 16;
const NO_REPORT = Symbol('missing report');
const MALFORMED_REPORT = /Cannot read or parse planted Playwright report .*planted-results.*\.json/;
type HarnessRun = Pick<SpawnSyncReturns<string>, 'status' | 'stdout' | 'stderr'> & { root: string };

/** Checks rejection and retained runner diagnostics; callers choose the rejection message. */
function expectRejected(run: HarnessRun, message: RegExp): void {
	expect(run.status, run.stderr).toBe(1);
	expect(run.stderr).toMatch(message);
	expect(run.stderr).toMatch(/stdout first lines: "GitCommitInfo: timeout of 3000ms exceeded/);
	expect(run.stderr).toMatch(/stderr first lines: "runner diagnostic/);
	expect(run.stdout).not.toMatch(/Planted proof:/);
}
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
			expectRejected(runHarness(JSON.stringify(content)), MALFORMED_REPORT);
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
		expectRejected(run, /Planted proof rejected:/);
	});

	/** Verifies an empty suite list cannot serve as planted proof. */
	it('rejects a report with an empty suites list', () => {
		const run = runHarness(JSON.stringify({ errors: [], suites: [] }));
		expectRejected(run, /Planted proof rejected:/);
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

const REPORT_LIMITS = [
	{ field: 'suites', maximum: 256 },
	{ field: 'specs', maximum: 1024 },
	{ field: 'tests', maximum: 1024 },
	{ field: 'results', maximum: 2048 }
] as const;

/** Pads exactly one report dimension; empty siblings preserve the original valid proof. */
function reportWithCount(field: (typeof REPORT_LIMITS)[number]['field'], count: number): unknown {
	const suite = report.suites[0];
	const spec = suite?.specs[0];
	const test = spec?.tests[0];
	const result = test?.results[0];
	if (result === undefined) throw new Error('Expected a complete proof fixture');
	const padding = Array.from({ length: count - 1 }, () => ({ tests: [], results: [] }));
	if (field === 'suites') return { errors: [], suites: [suite, ...padding] };
	if (field === 'specs') return { errors: [], suites: [{ specs: [spec, ...padding] }] };
	if (field === 'tests') {
		return { errors: [], suites: [{ specs: [{ tests: [test, ...padding] }] }] };
	}
	return { errors: [], suites: [{ specs: [{ tests: [{ results: [result, ...padding] }] }] }] };
}

/** Verifies each traversal budget at and immediately beyond its boundary. */
describe('report traversal limits', () => {
	it.each(REPORT_LIMITS)(
		'rejects a report over the $field bound',
		/** Requires oversized reports to take the malformed-report diagnostic path. */
		({ field, maximum }) => {
			const run = runHarness(JSON.stringify(reportWithCount(field, maximum + 1)));
			expectRejected(run, MALFORMED_REPORT);
			expect(run.stderr).toContain(`${field} limit exceeded`);
		}
	);
	it.each(REPORT_LIMITS)(
		'permits traversal at the $field bound',
		/** Confirms the inclusive boundary without confusing proof rejection with malformed JSON. */
		({ field, maximum }) => {
			const run = runHarness(JSON.stringify(reportWithCount(field, maximum)));
			expect(run.stderr).not.toMatch(MALFORMED_REPORT);
			expect(run.status, run.stderr).toBe(field === 'results' ? 1 : 0);
		}
	);
});

/** Exercises per-run isolation, report cleanup, and bounded filesystem operations. */
describe('report file lifecycle', () => {
	/** Two live harness processes must never share a proof path. */
	it('uses different report paths for two concurrent runs in the same folder', async () => {
		const root = createHarness(JSON.stringify(report));
		const runs = await Promise.all([runConcurrentHarness(root), runConcurrentHarness(root)]);
		const paths: string[] = readFileSync(join(root, 'report-paths.jsonl'), 'utf8')
			.trim()
			.split('\n')
			.map((line: string) => JSON.parse(line));
		expect(paths).toHaveLength(2);
		expect(new Set(paths).size).toBe(2);
		expect(runs.map((run) => run.status)).toEqual([0, 0]);
		expect(paths.map((path) => existsSync(path))).toEqual([false, false]);
	});
	/** Cancellation must reach the stalled reader before its watchdog rejects. */
	it('aborts a stalled read at the report I/O deadline', () => {
		const run = runHarness(JSON.stringify(report), false, 'readFile');
		expectRejected(run, MALFORMED_REPORT);
		expect(run.stderr).not.toContain('Stub watchdog expired');
		expect(readFileSync(join(run.root, 'read-aborted.txt'), 'utf8')).toBe('aborted');
	});
	/** Removal cannot hang indefinitely or hide the runner diagnostics. */
	it('cuts off a stalled removal at the report I/O deadline', () => {
		const run = runHarness(JSON.stringify(report), false, 'rm');
		expectRejected(run, MALFORMED_REPORT);
		expect(run.stderr).toContain('Report I/O deadline exceeded');
		expect(run.stderr).not.toContain('Stub watchdog expired');
	});
	/** A null fixture must write literal JSON null rather than simulate a missing report. */
	it('writes literal null for the null-report case', () => {
		const run = runHarness(null);
		expectRejected(run, MALFORMED_REPORT);
		expect(readFileSync(join(run.root, 'written-report.json'), 'utf8')).toBe('null');
		expect(run.stderr).not.toContain('ENOENT');
	});
	/** Parsing failures must also remove the report file. */
	it('removes the report after malformed JSON is read', () => {
		const run = runHarness('{broken JSON');
		expectRejected(run, MALFORMED_REPORT);
		const path: string = JSON.parse(readFileSync(join(run.root, 'report-paths.jsonl'), 'utf8'));
		expect(existsSync(path)).toBe(false);
	});
});

/** Creates an isolated harness and runner; only NO_REPORT omits the JSON file. */
function createHarness(content: string | null | typeof NO_REPORT, stale = false): string {
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
		`const { appendFileSync, writeFileSync } = require('node:fs');
const content = ${content === NO_REPORT ? 'undefined' : JSON.stringify(content === null ? 'null' : content)};
const reportPath = process.env.PLAYWRIGHT_JSON_OUTPUT_FILE;
appendFileSync('report-paths.jsonl', JSON.stringify(reportPath) + '\\n');
if (content !== undefined && reportPath) {
  writeFileSync(reportPath, content);
  writeFileSync('written-report.json', content);
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
	return root;
}

/** Stubs one report I/O operation; a watchdog exposes a missing deadline without hanging tests. */
function stubIO(root: string, method: 'readFile' | 'rm'): string {
	const preload = join(root, 'stub-io.mjs');
	writeFileSync(
		preload,
		`import fs from 'node:fs/promises';
import { writeFileSync } from 'node:fs';
import { syncBuiltinESMExports } from 'node:module';
/** Simulates a stalled report operation and records read cancellation. */
fs.${method} = (_file, options) => new Promise((_, reject) => {
  const watchdog = setTimeout(() => reject(new Error('Stub watchdog expired')), 2500);
  ${method === 'rm' ? 'watchdog.unref();' : ''}
  options?.signal?.addEventListener('abort', () => {
    clearTimeout(watchdog);
    writeFileSync('read-aborted.txt', 'aborted');
    reject(options.signal.reason);
  }, { once: true });
});
syncBuiltinESMExports();
`
	);
	return preload;
}

/** Runs an isolated harness, optionally with stalled filesystem I/O. */
function runHarness(
	content: string | null | typeof NO_REPORT,
	stale = false,
	ioStub?: 'readFile' | 'rm'
): HarnessRun {
	const root = createHarness(content, stale);
	const args = ioStub ? ['--import', stubIO(root, ioStub)] : [];
	const run = spawnSync(process.execPath, [...args, join(root, 'harness.mjs')], {
		cwd: root,
		encoding: 'utf8',
		timeout: 10000
	});
	if (run.error) throw run.error;
	return { ...run, root };
}

/** Starts a harness asynchronously so two executions can share the same working directory. */
function runConcurrentHarness(root: string): Promise<HarnessRun> {
	return new Promise((resolve, reject) => {
		const child = spawn(process.execPath, [join(root, 'harness.mjs')], {
			cwd: root,
			timeout: 10000,
			stdio: ['ignore', 'pipe', 'pipe']
		});
		let stdout = '';
		let stderr = '';
		child.stdout.setEncoding('utf8').on('data', (chunk: string) => (stdout += chunk));
		child.stderr.setEncoding('utf8').on('data', (chunk: string) => (stderr += chunk));
		child.on('error', reject);
		child.on('close', (status) => resolve({ status, stdout, stderr, root }));
	});
}

/** Covers absent and malformed optional fields beside valid proof. */
describe('optional planted report fields', () => {
	it.each(['suites', 'specs'])(
		'rejects null %s beside valid proof and retains runner diagnostics',
		/** Verifies null child fields cannot hide beside otherwise valid proof. */
		(field) => {
			const malformed = { ...report, suites: [...report.suites, { [field]: null }] };
			expectRejected(runHarness(JSON.stringify(malformed)), MALFORMED_REPORT);
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
		expectRejected(runHarness(NO_REPORT), MALFORMED_REPORT);
	});

	it('invalid report JSON includes runner diagnostics', () => {
		expectRejected(runHarness('{broken JSON'), MALFORMED_REPORT);
	});

	it('a missing new report cannot reuse a stale successful proof', () => {
		expectRejected(runHarness(NO_REPORT, true), MALFORMED_REPORT);
	});
});
