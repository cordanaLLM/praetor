// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// The ESLint policy scripts/test_emitted_hook_lint.py holds the shipped JavaScript assets to: the
// recommended rule set of @eslint/js with Node's globals, which is what an adopter's flat config
// starts from (#1247). The harness runs ESLint with --no-config-lookup, so no project or user
// configuration loosens it.
import js from "@eslint/js";
import globals from "globals";

export default [
  {
    ...js.configs.recommended,
    languageOptions: { ecmaVersion: "latest", sourceType: "module", globals: { ...globals.node } },
  },
];
