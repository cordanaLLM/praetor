// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>
// SPDX-License-Identifier: EUPL-1.2
import { spawnSync } from 'node:child_process';
import { createRequire } from 'node:module';
import { mkdir, writeFile } from 'node:fs/promises';

const require = createRequire(import.meta.url);
const cli = require.resolve('@playwright/test/cli');
const run = spawnSync(process.execPath, [cli, 'test', '--reporter=json'], {
	encoding: 'utf8',
	timeout: 120000,
	maxBuffer: 8 * 1024 * 1024,
	env: { ...process.env, CONSOLE_A11Y_PLANTED: '1', FORCE_COLOR: '0' }
});
if (run.error) throw new Error('Planted Playwright execution failed', { cause: run.error });
const report = JSON.parse(run.stdout);
await mkdir('test-results', { recursive: true });
await writeFile('test-results/planted-results.json', JSON.stringify(report, null, 2));
/** @param {typeof report.suites} suites */
function results(suites) {
	const queue = [...suites];
	const found = [];
	for (let i = 0; i < queue.length; i++) {
		const suite = queue[i];
		queue.push(...(suite.suites ?? []));
		for (const spec of suite.specs ?? []) {
			for (const test of spec.tests) found.push(...test.results);
		}
	}
	return found;
}
const failed = results(report.suites);
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
	failed[0].status !== 'failed' ||
	failed[0].errors.length !== 1 ||
	!failed[0].error?.message?.includes('toEqual') ||
	!violations.includes('image-alt') ||
	!violations.includes('color-contrast')
) {
	process.stderr.write(
		`Planted proof rejected: exit=${run.status}, violations=${JSON.stringify(violations)}, runnerErrors=${report.errors.length}, testErrors=${failed[0]?.errors?.length}\n${run.stderr}\n`
	);
	process.exitCode = 1;
} else {
	process.stdout.write(
		`Planted proof: Playwright exit 1; axe violations: ${violations.join(', ')}\n`
	);
}
