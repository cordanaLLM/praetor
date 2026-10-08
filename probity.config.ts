// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>
//
// SPDX-License-Identifier: EUPL-1.2

import { defineConfig, enforceTdd } from '@nizos/probity'

// Note: Probity 1.10.1 supports an `ai` override (Config.ai?: Agent), but importing
// @anthropic-ai/claude-agent-sdk here fails because jiti cannot resolve the SDK
// from a repository root without node_modules. Instead, the judge model is pinned
// to claude-haiku-5-5 via ANTHROPIC_MODEL=claude-haiku-5-5 in the hook command,
// which the Claude Agent SDK query process inherits when no model option is passed.
export default defineConfig({
  rules: [
    {
      files: ['**/internal/**/*.go', '**/cmd/**/*.go', '**/tools/**/*.go'],
      rules: [enforceTdd()],
    },
  ],
})
