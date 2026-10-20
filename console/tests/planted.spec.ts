// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>
// SPDX-License-Identifier: EUPL-1.2
import { expect, test } from '@playwright/test';
import { scan } from './axe';

test('planted page must fail the real zero-violation assertion', async ({ page }, info) => {
	await page.goto('/__a11y-planted/');
	const results = await scan(page);
	await info.attach('planted-violations', {
		body: JSON.stringify(results.violations.map(({ id }) => id)),
		contentType: 'application/json'
	});
	expect(results.violations).toEqual([]);
});
