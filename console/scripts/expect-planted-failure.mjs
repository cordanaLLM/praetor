// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>
// SPDX-License-Identifier: EUPL-1.2
import { spawnSync } from 'node:child_process';
import { randomUUID } from 'node:crypto';
import { createRequire } from 'node:module';
import { mkdir, readFile, rm } from 'node:fs/promises';
import { resolve } from 'node:path';

const require = createRequire(import.meta.url);
const cli = require.resolve('@playwright/test/cli');
const REPORT_IO_TIMEOUT_MS = 1000;
const MAX_REPORT_SUITES = 256;
const MAX_REPORT_SPECS = 1024;
const MAX_REPORT_TESTS = 1024;
const MAX_REPORT_RESULTS = 2048;
const reportPath = resolve(`test-results/planted-results-${process.pid}-${randomUUID()}.json`);
await boundedIO(mkdir('test-results', { recursive: true }));
const run = spawnSync(process.execPath, [cli, 'test', '--reporter=json'], {
	encoding: 'utf8',
	timeout: 120000,
	maxBuffer: 8 * 1024 * 1024,
	// Playwright 1.64.1: PLAYWRIGHT_JSON_OUTPUT_FILE keeps JSON separate from runner diagnostics.
	// https://playwright.dev/docs/test-reporters#json-reporter
	env: {
		...process.env,
		CONSOLE_A11Y_PLANTED: '1',
		FORCE_COLOR: '0',
		PLAYWRIGHT_JSON_OUTPUT_FILE: reportPath
	}
});
/** Bounds an I/O promise and clears its deadline timer on every outcome.
 * @template T
 * @param {Promise<T>} operation
 * @returns {Promise<T>}
 */
async function boundedIO(operation) {
	/** @type {ReturnType<typeof setTimeout> | undefined} */
	let timer;
	try {
		return await Promise.race([
			operation,
			new Promise((_, reject) => {
				timer = setTimeout(
					() => reject(new Error('Report I/O deadline exceeded')),
					REPORT_IO_TIMEOUT_MS
				);
			})
		]);
	} finally {
		clearTimeout(timer);
	}
}

/** Reads this run's report with cancellation; always attempts bounded cleanup. */
async function readReport() {
	try {
		if (run.error) throw new Error('Planted Playwright execution failed', { cause: run.error });
		return await boundedIO(
			readFile(reportPath, { encoding: 'utf8', signal: AbortSignal.timeout(REPORT_IO_TIMEOUT_MS) })
		);
	} finally {
		await boundedIO(rm(reportPath, { force: true }));
	}
}

/** Quotes a bounded excerpt of runner output. @param {string | null} output */
function firstLines(output) {
	return JSON.stringify((output ?? '').split(/\r?\n/, 3).join('\n').slice(0, 1000));
}
let report;
let failed;
try {
	report = JSON.parse(await readReport());
	reportArray(report.errors);
	failed = results(report.suites);
} catch (cause) {
	throw new Error(
		`Cannot read or parse planted Playwright report ${reportPath}\n` +
			`stdout first lines: ${firstLines(run.stdout)}\n` +
			`stderr first lines: ${firstLines(run.stderr)}`,
		{ cause }
	);
}
/** Validates an array-valued report field before traversing it. @param {unknown} value */
function reportArray(value) {
	if (!Array.isArray(value)) throw new TypeError('Expected an array in planted Playwright report');
	return value;
}
/** Appends validated entries only when the cumulative traversal budget permits them.
 * @param {unknown[]} target
 * @param {unknown} value
 * @param {number} maximum
 * @param {string} field
 */
function appendWithin(target, value, maximum, field) {
	const entries = reportArray(value);
	if (target.length + entries.length > maximum) throw new TypeError(`${field} limit exceeded`);
	target.push(...entries);
}

/** Collects suites, specs and tests with separate cumulative limits. @param {unknown} suites */
function results(suites) {
	/** @type {import('@playwright/test/reporter').JSONReportSuite[]} */
	const queue = [];
	/** @type {import('@playwright/test/reporter').JSONReportSpec[]} */
	const specs = [];
	/** @type {import('@playwright/test/reporter').JSONReportTest[]} */
	const tests = [];
	appendWithin(queue, suites, MAX_REPORT_SUITES, 'suites');
	for (let i = 0; i < MAX_REPORT_SUITES && i < queue.length; i++) {
		const suite = queue[i];
		if (!suite) throw new TypeError('Expected a suite in planted Playwright report');
		appendWithin(
			queue,
			suite.suites === undefined ? [] : suite.suites,
			MAX_REPORT_SUITES,
			'suites'
		);
		appendWithin(specs, suite.specs === undefined ? [] : suite.specs, MAX_REPORT_SPECS, 'specs');
	}
	for (let i = 0; i < MAX_REPORT_SPECS && i < specs.length; i++) {
		appendWithin(tests, specs[i]?.tests, MAX_REPORT_TESTS, 'tests');
	}
	return collectResults(tests);
}

/** Collects test results without exceeding their cumulative budget.
 * @param {import('@playwright/test/reporter').JSONReportTest[]} tests
 */
function collectResults(tests) {
	/** @type {import('@playwright/test/reporter').JSONReportTestResult[]} */
	const found = [];
	for (let i = 0; i < MAX_REPORT_TESTS && i < tests.length; i++) {
		appendWithin(found, tests[i]?.results, MAX_REPORT_RESULTS, 'results');
	}
	return found;
}
const attachment = failed[0]?.attachments?.find(
	/** @param {{name: string}} item */
	(item) => item.name === 'planted-violations'
);
const violations = attachment?.body
	? JSON.parse(Buffer.from(attachment.body, 'base64').toString('utf8'))
	: [];
if (
	run.status !== 1 ||
	report.errors.length !== 0 ||
	failed.length !== 1 ||
	failed[0]?.status !== 'failed' ||
	failed[0]?.errors.length !== 1 ||
	!failed[0]?.error?.message?.includes('toEqual') ||
	!violations.includes('image-alt') ||
	!violations.includes('color-contrast')
) {
	process.stderr.write(
		`Planted proof rejected: exit=${run.status}, violations=${JSON.stringify(violations)}, runnerErrors=${report.errors.length}, testErrors=${failed[0]?.errors?.length}\nstdout first lines: ${firstLines(run.stdout)}\nstderr first lines: ${firstLines(run.stderr)}\n`
	);
	process.exitCode = 1;
} else {
	process.stdout.write(
		`Planted proof: Playwright exit 1; axe violations: ${violations.join(', ')}\n`
	);
}
