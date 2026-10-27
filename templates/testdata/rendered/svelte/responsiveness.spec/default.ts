// Order: engine-fr-rule-checks-20261027
import { expect, type Page, test } from '@playwright/test';

export const LATENCY_ENV_VAR = 'PRAETOR_UI_INPUT_LATENCY_MS';

export const UI_ROUTES: readonly string[] = [
	"/",
];

const SLOW_API_DELAY_MS = 5000;
const PENDING_STATE_DEADLINE_MS = SLOW_API_DELAY_MS / 2;
const SERVICE_UNAVAILABLE_STATUS = 503;

export function readInputLatencyThresholdMs(
	env: Record<string, string | undefined> = process.env,
): number {
	const raw = env[LATENCY_ENV_VAR]?.trim() ?? '';
	const parsed = Number(raw);
	if (!raw || !Number.isInteger(parsed) || parsed <= 0) {
		throw new Error(
			`Missing or invalid ${LATENCY_ENV_VAR}: set ui.input_latency_ms in .standards.yaml`,
		);
	}
	return parsed;
}

async function installEventLatencyObserver(page: Page): Promise<void> {
	await page.addInitScript(() => {
		const bucket: number[] = [];
		(window as unknown as { __praetorEventDurations: number[] }).__praetorEventDurations =
			bucket;
		const observer = new PerformanceObserver((list) => {
			for (const entry of list.getEntries()) {
				bucket.push(entry.duration);
			}
		});
		observer.observe({
			type: 'event',
			durationThreshold: 16,
			buffered: true,
		} as PerformanceObserverInit);
	});
}

async function exerciseClickAndKeyInput(page: Page): Promise<number> {
	const target = page.locator('button, a, input, [tabindex], body').first();
	await target.click({ trial: false });
	await page.keyboard.press('Tab');
	await page.keyboard.press('Escape');
	return page.evaluate(() => {
		const win = window as unknown as { __praetorEventDurations?: number[] };
		const recorded = win.__praetorEventDurations ?? [];
		let maxMeasured = 0;
		for (const sample of recorded) {
			if (sample > maxMeasured) {
				maxMeasured = sample;
			}
		}
		return maxMeasured;
	});
}

test('missing PRAETOR_UI_INPUT_LATENCY_MS fails naming variable', () => {
	expect(() => readInputLatencyThresholdMs({})).toThrow(
		/PRAETOR_UI_INPUT_LATENCY_MS/,
	);
});

for (const route of UI_ROUTES) {
	test(`route ${route} input-to-next-frame latency stays within threshold`, async ({
		page,
	}) => {
		const thresholdMs = readInputLatencyThresholdMs(process.env);
		await installEventLatencyObserver(page);
		await page.goto(route);
		const maxDurationMs = await exerciseClickAndKeyInput(page);
		expect(
			maxDurationMs,
			`Input latency on ${route} exceeded ${LATENCY_ENV_VAR}`,
		).toBeLessThanOrEqual(thresholdMs);
	});

	test(`route ${route} slow API (5 s delay) keeps pending state visible while request is outstanding and input live`, async ({
		page,
	}) => {
		const thresholdMs = readInputLatencyThresholdMs(process.env);
		await installEventLatencyObserver(page);
		await page.route("**/api/**", async (intercepted) => {
			await new Promise((resolve) => setTimeout(resolve, SLOW_API_DELAY_MS));
			await intercepted.fulfill({
				contentType: 'application/json',
				body: JSON.stringify({ ok: true }),
			});
		});
		await page.goto(route, { waitUntil: 'commit' });
		const pendingState = page
			.locator('[aria-busy="true"], [data-pending], [role="progressbar"], [role="status"]')
			.first();
		await expect(pendingState).toBeVisible({ timeout: PENDING_STATE_DEADLINE_MS });
		const maxDurationMs = await exerciseClickAndKeyInput(page);
		expect(
			maxDurationMs,
			`Input latency during pending API state on ${route} exceeded ${LATENCY_ENV_VAR}`,
		).toBeLessThanOrEqual(thresholdMs);
	});

	test(`route ${route} API HTTP 503 surfaces visible error state and keeps input live`, async ({
		page,
	}) => {
		const thresholdMs = readInputLatencyThresholdMs(process.env);
		await installEventLatencyObserver(page);
		await page.route("**/api/**", async (intercepted) => {
			await intercepted.fulfill({
				status: SERVICE_UNAVAILABLE_STATUS,
				contentType: 'application/json',
				body: JSON.stringify({ error: 'Service Unavailable' }),
			});
		});
		await page.goto(route);
		const errorState = page
			.locator('[role="alert"], [data-error], [aria-invalid="true"]')
			.first();
		await expect(errorState).toBeVisible();
		const maxDurationMs = await exerciseClickAndKeyInput(page);
		expect(
			maxDurationMs,
			`Input latency during HTTP 503 error state on ${route} exceeded ${LATENCY_ENV_VAR}`,
		).toBeLessThanOrEqual(thresholdMs);
	});
}
