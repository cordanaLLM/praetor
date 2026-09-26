# Vendored interfig

`upstream/` holds a byte-identical subset of interfig, the figure engine behind the
animated architecture figures on the Hindsight documentation site. Praetor uses it to draw
its documentation figures ([figures guide](../../docs/guides/figures.md)). The decision
record is [ADR-0015](../../docs/adr/0015-interactive-figures-from-vendored-interfig.md).

## Source and pin

| Field | Value |
| :-- | :-- |
| Repository | <https://github.com/vectorize-io/hindsight> |
| Path | `hindsight-interfig/` |
| Pinned commit | `ccfe85b4851957ac2adf88b4a9ddf9668b2882f1` |
| Last upstream change under the path | `db03d5448ff1290cc6e501aa7f62d5e4a1e979a3` |
| Fetched | 2026-09-27 |

`vendor.json` records the same pin, the include and exclude lists, and the SHA-256 of every
file under `upstream/`.

## License and credit

interfig is MIT-licensed, Copyright (c) 2025 Vectorize AI, Inc. `upstream/` has no license
file of its own in the upstream tree, so `upstream/LICENSE` is the repository-root
`LICENSE` at the pinned commit, copied verbatim.

- `REUSE.toml` labels `third_party/interfig/upstream/**` as MIT with an override annotation
  placed after the repository-wide `**` table. This file, `vendor.json` and everything
  outside `upstream/` stay EUPL-1.2.
- The deployed player bundle carries an `interfig (c) 2025 Vectorize AI, Inc. MIT` banner,
  because the upstream source has no header of its own (`tools/figures/build.mjs`).
- Every exported SVG carries a credit comment, and the site footer credits Vectorize
  (`copyright` in `mkdocs.yml`).

Credit to Vectorize for the engine does not imply endorsement. No Hindsight figures,
names or branding are vendored or used.

## What is vendored

Included, relative to `hindsight-interfig/`:

- `README.md`, `package.json`;
- `src/index.tsx`, `src/model.ts`, `src/geometry.ts`, `src/svg.ts`,
  `src/node-figures.ts`, and the four `src/*.test.ts` files;
- `scripts/figure-svg.mjs`, `scripts/figure-loader.mjs`.

Excluded:

- `demo/` and `index.html`: the Vite gallery;
- `figures/`: Hindsight product content;
- `scripts/export.mjs`: clip export through Playwright and ffmpeg;
- `.gitignore`, `.prettierrc`, `tsconfig.json`, `vite.config.ts`: upstream tooling.

## Local adaptations

Files under `upstream/` are never edited. Praetor's adaptations live outside it:

| Adaptation | Where |
| :-- | :-- |
| `role="img"`, `<title>`, `<desc>` and a credit comment injected into each exported SVG | `tools/figures/build.mjs` |
| A static SVG variant (`steps: []`) for reduced motion | `tools/figures/build.mjs` |
| Arrow, Home and End keys across the scenario tabs, and a tab-list label | `tools/figures/keyboard.ts` |
| Theme colours mapped to Material for MkDocs variables, and a focus outline | `docs/stylesheets/figures.css` |

Each adaptation is offered upstream (ADR-0015, section 6). A later sync that brings in the
upstream fix retires the local shim.

## Tests

```bash
node --test third_party/interfig/upstream/src/*.test.ts
```

All 13 upstream tests pass at the pin. On Node 26 the run prints
`[DEP0205] DeprecationWarning: module.register() is deprecated`. The warning comes from
upstream's `scripts/figure-svg.mjs`, which `src/figure-svg.test.ts` starts as a child
process. Praetor's build never calls that script: `tools/figures/build.mjs` imports
`toSvg` from `src/svg.ts` directly.

## Updating the pin

Until the sync automation lands, an update is manual:

1. Fetch each included file at the new commit from
   `https://raw.githubusercontent.com/vectorize-io/hindsight/<commit>/hindsight-interfig/<file>`,
   and the repository-root `LICENSE`, into `upstream/`.
2. List `src/` and `scripts/` at the new commit. A new file there must be added to the
   include or exclude list before the update continues.
3. Stop if `LICENSE` changed, or if the `react` peer range no longer matches
   `tools/figures/package.json`.
4. Update `commit`, `path_commit`, `fetched` and the file hashes in `vendor.json`.
5. Run the upstream tests, then `npm --prefix tools/figures run build`: the engine hash
   changes with `src/svg.ts`, `src/geometry.ts` or `src/model.ts`, so every figure is
   regenerated. Commit the regenerated `docs/assets/figures/` files with the update.
