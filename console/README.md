<!-- SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me> -->
<!-- SPDX-License-Identifier: EUPL-1.2 -->

# Console

The static console for Praetor (Latin for a magistrate, pronounced “PREE-tor”) reads `cards.json`, `graph.json` and `status.json` beside `index.html`. Development, tests and screenshots use only the synthetic files in `fixtures/`. Production deployment supplies its own three files; the build contains no fleet data.

Use Node.js 24 or newer and pnpm 10.28.0. From `console/`:

```bash
pnpm install --frozen-lockfile
pnpm dev
```

Build and preview the static output with synthetic data:

```bash
pnpm build
pnpm fixtures:copy
pnpm preview
```

`fixtures:copy` explicitly copies the three JSON fixtures into `build/`. Preview serves the built files without a fixture middleware, so missing copies surface as loading errors. Rebuild before production deployment to remove fixture copies.

Run the quality gates:

```bash
pnpm lint
pnpm check
pnpm test
pnpm build
pnpm fixtures:copy
pnpm budget
pnpm exec playwright install --with-deps chromium
pnpm test:a11y
pnpm test:a11y:planted
```

`pnpm test` includes the planted bundle overrun: Vite builds an entry importing a large synthetic module, then the test requires the budget CLI to exit 1. To run that proof alone:

```bash
pnpm test scripts/bundle-budget.test.ts
```

The bundle gate counts executable inline scripts, script sources, modulepreloads and their static module imports from `build/index.html`, deduplicating URLs. Literal imports in the inline bootstrap also count. It compresses each resource separately with gzip level 9 and fails above 153600 bytes. Missing modules, external JavaScript and missing entry modules fail closed.

The Chromium accessibility suite scans all six tabs at 390×844 and 1440×900 in light and dark themes with axe's WCAG 2.0/2.1 A/AA and 2.2 AA tags, including contrast. It also checks keyboard order, arrow navigation, a 3 px focus outline, the command palette and zero motion under the reduced-motion preference. Automated axe checks cover machine-testable criteria; manual assistive-technology review remains necessary for a full accessibility assessment.

Every tab scan saves a fixture screenshot to `screenshots/<tab>-<theme>-<phone|desktop>.png`. The 24 PNGs are review artifacts covered by the root `REUSE.toml`; full axe results are attached to `test-results/a11y-results.json`. Tests start a fresh preview server and copy fixtures automatically. The planted suite uses a separate Vite HTML build (`tests/planted/` → `build-planted/`) at `/__a11y-planted/`; no planted route enters `build/`. Its wrapper succeeds only when the real zero-violation assertion fails with both `image-alt` and `color-contrast` findings. Browser crashes, server failures and an unexpectedly passing page fail the proof.

Playwright uses its pinned Chromium revision by default. To use a preinstalled Chromium explicitly, set `PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH` to its executable path when running either accessibility command. The suite prints the executed browser version. Set `CONSOLE_TEST_PORT` to a dedicated loopback port if the defaults (4173 for pages, 4174 for the planted build) are occupied. An occupied port always fails; tests never reuse an existing server.

The palette uses the component library's command registry and combobox. Concordia (Latin for harmony, pronounced “kon-KOR-dee-ah”), from Aegis-OS (“EE-jis”, a protective shield), supplies the colour, type, spacing and 3 px focus tokens in `src/concordia-tokens.css`; `src/app.css` maps those tokens to the component library and supplies dark-theme overrides. The copied file records the upstream path, commit and EUPL-1.2 notice; `docs/credits.yaml` records its source.
