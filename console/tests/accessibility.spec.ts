// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>
// SPDX-License-Identifier: EUPL-1.2
import { mkdir } from 'node:fs/promises';
import { expect, test, type Page } from '@playwright/test';
import { TABS, type Tab } from '../src/lib/view/state.svelte.ts';
import { scan } from './axe';
import { traverse } from './keyboard';

async function ready(page: Page, tab: Tab): Promise<void> {
	await page.goto(`/?tab=${tab}`);
	await expect(page.locator(`#tab-${tab}`)).toHaveAttribute('aria-selected', 'true');
	await expect(page.locator('.live')).toContainText('updated');
	await expect(page.getByTestId('skeleton')).toHaveCount(0);
	await expect(page.getByRole('alert')).toHaveCount(0);
}

for (const tab of TABS) {
	test(`${tab}: WCAG 2.2 AA, contrast and fixture screenshot`, async ({ page, browser }, info) => {
		const errors: string[] = [];
		page.on('pageerror', (error) => errors.push(error.message));
		await ready(page, tab);
		// Exercise expanded card content as well as the default surface.
		if (tab === 'now') await page.locator('.toggle').first().click();
		const results = await scan(page);
		await info.attach('axe-results', {
			body: JSON.stringify(results),
			contentType: 'application/json'
		});
		process.stdout.write(
			`${info.project.name}/${tab}: ${results.violations.length} violations; Chromium ${browser.version()}\n`
		);
		expect(results.violations).toEqual([]);
		expect(errors).toEqual([]);
		await expect(page.locator('html')).toHaveClass(
			info.project.use.colorScheme === 'dark' ? /dark/ : /^(?!.*dark)/
		);
		expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
			true
		);
		if (tab === 'now') await page.locator('.toggle').first().click();
		await mkdir('screenshots', { recursive: true });
		await page.screenshot({ path: `screenshots/${tab}-${info.project.name}.png`, fullPage: false });
	});
}

test('keyboard: every tab and control, palette opens, navigates and closes', async ({ page }) => {
	await ready(page, 'now');
	for (const tab of TABS) {
		await page.locator(`#tab-${tab}`).focus();
		await page.keyboard.press('Enter');
		await expect(page.locator(`#tab-${tab}`)).toHaveAttribute('aria-selected', 'true');
		await traverse(page);
	}
	await page.locator('#tab-history').focus();
	for (const tab of TABS) {
		await page.keyboard.press('ArrowRight');
		await expect(page.locator(`#tab-${tab}`)).toBeFocused();
		await expect(page.locator(`#tab-${tab}`)).toHaveAttribute('aria-selected', 'true');
	}
	await page.keyboard.press('Control+k');
	await expect(page.getByRole('dialog', { name: 'Command palette' })).toBeVisible();
	await expect(page.getByRole('combobox')).toBeFocused();
	await page.keyboard.press('Tab');
	await expect(page.getByRole('combobox')).toBeFocused();
	await page.keyboard.press('Shift+Tab');
	await expect(page.getByRole('combobox')).toBeFocused();
	await page.getByRole('combobox').fill('Go to Graph');
	await page.keyboard.press('Enter');
	await expect(page.locator('#tab-graph')).toHaveAttribute('aria-selected', 'true');
	await page.keyboard.press('Control+k');
	const results = await scan(page);
	expect(results.violations).toEqual([]);
	await page.keyboard.press('Escape');
	await expect(page.getByRole('dialog')).toHaveCount(0);
});

test('reduced motion: loading, all tabs and palette have no animation or transition', async ({
	page
}) => {
	await page.emulateMedia({ reducedMotion: 'reduce' });
	await page.route(/\/(cards|graph|status)\.json$/, (route) =>
		route.fulfill({ path: `fixtures/${route.request().url().split('/').pop()}` })
	);
	for (const tab of TABS) {
		await ready(page, tab);
		await assertNoMotion(page);
	}
	await page.keyboard.press('Control+k');
	await expect(page.getByRole('dialog')).toBeVisible();
	await assertNoMotion(page);
	await page.unrouteAll();
	await page.route(/\/(cards|graph|status)\.json$/, () => {
		/* Keep skeleton visible until the context closes. */
	});
	await page.goto('/');
	await expect(page.getByTestId('skeleton')).toBeVisible();
	await assertNoMotion(page);
});

async function assertNoMotion(page: Page): Promise<void> {
	const moving = await page.evaluate(() =>
		[...document.querySelectorAll('*')].flatMap((el) =>
			[null, '::before', '::after'].flatMap((pseudo) => {
				const style = getComputedStyle(el, pseudo);
				const durations = [style.animationDuration, style.transitionDuration];
				return durations.some((value) => value.split(',').some((time) => parseFloat(time) > 0))
					? [el.tagName]
					: [];
			})
		)
	);
	expect(moving).toEqual([]);
}
