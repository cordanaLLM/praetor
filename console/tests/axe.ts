// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>
// SPDX-License-Identifier: EUPL-1.2
import AxeBuilder from '@axe-core/playwright';
import type { Page } from '@playwright/test';

// WCAG 2.2 AA includes the applicable 2.0/2.1 A and AA rules as well.
export const WCAG_TAGS = ['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa', 'wcag22aa'];
export const scan = (page: Page) => new AxeBuilder({ page }).withTags(WCAG_TAGS).analyze();
