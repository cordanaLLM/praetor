// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>
// SPDX-License-Identifier: EUPL-1.2
import { defineConfig } from '@playwright/test';

const planted = process.env.CONSOLE_A11Y_PLANTED === '1';
const port = Number(process.env.CONSOLE_TEST_PORT ?? (planted ? 4174 : 4173));
if (!Number.isInteger(port) || port < 1024 || port > 65535) {
	throw new Error('CONSOLE_TEST_PORT must be an integer from 1024 to 65535');
}
const baseURL = `http://127.0.0.1:${port}`;
const executablePath = process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH;
const projects = (['light', 'dark'] as const).flatMap((colorScheme) =>
	[
		{ name: 'phone', viewport: { width: 390, height: 844 } },
		{ name: 'desktop', viewport: { width: 1440, height: 900 } }
	].map(({ name, viewport }) => ({
		name: `${colorScheme}-${name}`,
		use: { colorScheme, viewport }
	}))
);

export default defineConfig({
	testDir: './tests',
	outputDir: planted ? 'test-results/planted' : 'test-results/pages',
	testMatch: planted ? 'planted.spec.ts' : 'accessibility.spec.ts',
	fullyParallel: false,
	forbidOnly: true,
	retries: 0,
	workers: 1,
	timeout: 30000,
	reporter: [['list'], ['json', { outputFile: 'test-results/a11y-results.json' }]],
	projects: planted ? [projects[0]!] : projects,
	use: {
		baseURL,
		browserName: 'chromium',
		launchOptions: { executablePath },
		trace: planted ? 'off' : 'retain-on-failure'
	},
	webServer: {
		command: planted
			? `pnpm exec vite build --config tests/planted/vite.config.ts && pnpm exec vite preview --config tests/planted/vite.config.ts --host 127.0.0.1 --port ${port} --strictPort`
			: `pnpm fixtures:copy && pnpm preview --host 127.0.0.1 --port ${port} --strictPort`,
		url: planted ? `${baseURL}/__a11y-planted/` : baseURL,
		reuseExistingServer: false,
		timeout: 60000
	}
});
