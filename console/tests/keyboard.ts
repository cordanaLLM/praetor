// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>
// SPDX-License-Identifier: EUPL-1.2
import { expect, type Page } from '@playwright/test';

/** Verify the exact sequential order of every enabled, visible tab stop. */
export async function traverse(page: Page): Promise<void> {
	const count = await page.evaluate(() => {
		for (const el of document.querySelectorAll('[data-keyboard-control]'))
			el.removeAttribute('data-keyboard-control');
		const controls = [
			...document.querySelectorAll<HTMLElement>(
				'a[href], button, input, select, textarea, [tabindex]'
			)
		].filter(
			(el) => el.tabIndex >= 0 && !el.matches(':disabled') && el.getClientRects().length > 0
		);
		controls.forEach((el, i) => el.setAttribute('data-keyboard-control', String(i)));
		document.body.tabIndex = -1;
		document.body.focus();
		document.body.removeAttribute('tabindex');
		window.scrollTo(0, 0);
		return controls.length;
	});
	expect(count).toBeGreaterThan(0);
	for (let i = 0; i < count; i++) {
		await page.keyboard.press('Tab');
		const control = page.locator(`[data-keyboard-control="${i}"]`);
		await expect(control).toBeFocused();
		const ring = await control.evaluate((el) => {
			const css = getComputedStyle(el);
			return { style: css.outlineStyle, width: css.outlineWidth };
		});
		expect(ring).toEqual({ style: 'solid', width: '3px' });
	}
}
