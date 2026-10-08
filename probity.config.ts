// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>
//
// SPDX-License-Identifier: EUPL-1.2

import { defineConfig, enforceTdd } from '@nizos/probity'

// Note: In @nizos/probity 1.10.1, the AI validator pairs with the host agent CLI flag
// (--agent claude-code) and neither defineConfig nor enforceTdd exposes a documented
// option to select or pin the judge model (e.g. claude-haiku-5-5).
export default defineConfig({
  rules: [
    {
      files: ['**/internal/**/*.go', '**/cmd/**/*.go', '**/tools/**/*.go'],
      rules: [enforceTdd()],
    },
  ],
})
