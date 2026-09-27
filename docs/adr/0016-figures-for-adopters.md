# ADR-0016: Interactive Figures for Adopters Through a Managed Engine Family

## Status

Proposed — 2026-09-27.

This record amends ADR-0015 while ADR-0015 is still Proposed (`docs/adr/README.md`, rule 4). The
amended passages are listed under "Amendments to ADR-0015" below and are marked in ADR-0015
itself. The owner decided the three questions the draft of this record left open on 2026-09-27;
"Operator decisions" under Decision records them.

## Context

ADR-0015 built an interactive figure engine for praetor's own site and left adopters to a separate
decision (its Neutral consequences, now amended). The operator has since asked that every adopter
can draw the same figures of its own code and that Mermaid leaves the documentation presets. This
record is that decision.

### What exists on main (7e7746a3)

- **Engine.** `tools/figures/build.mjs` renders `docs/figures/<slug>.ts` into committed
  `docs/assets/figures/<slug>.{svg,static.svg,json}` (`tools/figures/build.mjs:5-9,471-477`).
  - Paths are fixed relative to praetor's tree: `ROOT` is `../../` from the script, and the
    vendored engine is imported from `../../third_party/interfig/upstream/src/svg.ts`
    (`tools/figures/build.mjs:21-27`).
  - The engine hash covers the three vendored render files and `build.mjs` itself
    (`tools/figures/build.mjs:29-34`), so any edit to the command-line wrapper, even a comment,
    marks every figure stale.
- **Rendering needs no npm package.** `build.mjs` imports only `node:` builtins and the vendored
  `svg.ts` (`tools/figures/build.mjs:15-21`). `svg.ts` imports only `./geometry.ts` and
  `./model.ts`, and `model.ts` imports React as a type only (`import type`, erased at run time;
  `third_party/interfig/upstream/src/model.ts:2`). Measured for this record on a `git archive`
  copy of 7e7746a3 without `node_modules`, on Node 26.10.0: `node tools/figures/build.mjs build`
  rewrote all 30 committed outputs byte-identical. Node 22.18, the declared floor
  (`tools/figures/package.json:7-9`), was not measured.
- **`check` does need esbuild.** It bundles the player into a temporary directory to hold the
  250 kB budget (`tools/figures/build.mjs:479-493`). The same copy failed with
  `Cannot find package 'esbuild'`.
- **Both SVG variants carry the full spec.** `decorate()` writes `{props}` into
  `<metadata id="figure-spec">` (`tools/figures/build.mjs:329-331`), and the static variant is
  decorated with the same figure, steps included (`tools/figures/build.mjs:343-344`).
- **The player is repository-specific** only because of `registry.json` and one bundle chunk per
  spec (`tools/figures/build.mjs:446-458`; `tools/figures/loader.ts:24-52`). The player chunk
  itself (React, react-dom, interfig, the keyboard shim) is the same for every site: about 240 kB
  minified and 76 kB gzip against a 250 kB budget (`tools/figures/build.mjs:41`), as
  `npm --prefix tools/figures run check` reports it.
- **One markup renderer, in Python.** `render_block` builds the `<figure><picture>` block with the
  `<details>` text description (`scripts/docs_diagrams.py:264-288`). The MkDocs hook and
  `portable` call it (`scripts/mkdocs_figures_hook.py:25-31`; `scripts/docs_diagrams.py:565-582`).
  No Go code renders a figure: `internal/forge/wiki.go:445` writes a raw ` ```figure ` fence, so
  ADR-0015's claim that adopter wikis resolve through `portable` did not hold (amended at
  `docs/adr/0015-interactive-figures-from-vendored-interfig.md:329-333`).
- **The only channel that already carries engine files to adopters** is go:embed plus an adopt
  step plus an audit byte-compare, used by the Markdown gate of the `docs:seo-portal` facet:
  - `tools/markdownlint/assets.go:15-33` (embedded inventory, `MaxAssets` bound, workflow file
    `.github/workflows/praetor-docs.yml`, status context `Documentation Governance`);
  - `internal/adopt/documentation.go:22-26` (the facet decides the gate), `:67-96` (emission with
    `force: true`) and `:169-225` (removal when the facet is off, refusal of drifted copies);
  - `internal/adopt/verification_makefile.go:18-26` (the managed Makefile block attaching
    `docs-lint` to `verify-all`);
  - `cmd/standardsctl/audit_documentation.go:18-35,196-213` (line-ending-normalised compare,
    "differs from the locked Praetor asset; run 'praetorctl adopt --force'");
  - `internal/devcontainer/bootstrap_source.go:309-318` (the embed families the devcontainer
    bootstrap captures).
- **Presets.** The MkDocs preset declares only a Mermaid fence
  (`docs/presets/mkdocs/mkdocs.yml:62-66`); the Starlight preset has no diagram support
  (`docs/presets/starlight/astro.config.mjs`). Presets are reference directories and adoption
  never emits them (`docs/adr/0015-interactive-figures-from-vendored-interfig.md:530-534`).
- **Default facets are defined three times** and all three include `docs:seo-portal`:
  `internal/adopt/adopt.go:307-312`, `cmd/standardsctl/init.go:29`,
  `internal/harvester/onboard.go:62`. They differ only in `agent:sandboxed`.
- **Delivery state**, observed on 2026-09-27 at f9f1931b and not re-run for this record
  (`gh release list`, `gh run list --workflow pages.yml`): no release has been published, and
  `pages.yml` failed at its smoke step on every main push from 024848ad on. The smoke test requires
  a packet within 8 s on a page whose figure has tabs (`tools/figures/smoke.mjs:23,103-105`), and
  the first four steps of `docs/figures/lattice-join.ts` carry no edge (the first edge hop is at
  `docs/figures/lattice-join.ts:166`), which is the likely cause.

### Constraints

- HISS-19: praetor's own site and every adopter use one engine path and one markup renderer.
- HISS-21: every gate runs on Linux, macOS and Windows or skips with a stated reason.
- HISS-02, HISS-04, HISS-15 and HISS-20: bounded loops and I/O, complexity caps, positive,
  negative and boundary tests replayed both ways.
- The root module keeps exactly one external dependency (`go.mod`;
  `docs/adr/0008-spec-driven-provider-integration.md:38`;
  `docs/adr/0009-structural-unification.md:44`).
- ADR-0014: committed adopter artifacts carry project identity only, never operator data
  (`docs/adr/0014-operator-neutral-defaults.md:48-53`).
- The MIT notice duty for interfig and React travels with every copy
  (`docs/adr/0015-interactive-figures-from-vendored-interfig.md:500-534`).

## Decision

Deliver the figure engine to adopters as a managed, go:embed'ed asset family of the existing
`docs:seo-portal` facet, through the channel the Markdown gate already uses. The adopter tree has
the same layout as praetor's, so the same bytes run in both. The player becomes generic: it reads
the spec from the SVG it replaces, so it is prebuilt once in praetor, checked for reproducibility,
and emitted byte for byte. Adopters render and check figures with plain Node and no npm install.

### Operator decisions (2026-09-27)

The owner settled the three questions the draft left open. Each is binding on the sections below.

1. **No new facet.** The figure engine family belongs to the existing `docs:seo-portal` facet
   (section 5). A repository cannot take the Markdown gate without the figure engine, or the
   reverse: opting out of figures means disabling documentation governance as a whole.
2. **The prebuilt player is committed and embedded.** The generic player bundle is committed in
   praetor under `tools/figures/dist/`, rebuilt byte for byte by `bundle.mjs --check` on Linux,
   macOS and Windows, embedded in praetorctl, and written byte for byte by `praetorctl adopt`
   (section 3). This amends ADR-0015 section 2, which kept the bundle out of the tree.
3. **Node for authoring, nothing new elsewhere.** Adopters need Node 22.18 or later with zero npm
   packages to author and check figures. The MkDocs site build stays Python-only, and the
   single-external-dependency policy for the root Go module (ADR-0008, ADR-0009) stays; figures
   are not rendered in Go.

### 1. One engine tree, identical in praetor and in adopters

Praetor consolidates the figure engine into `tools/figures/`:

| Today | After |
| :-- | :-- |
| `scripts/docs_diagrams.py` | `tools/figures/docs_diagrams.py` |
| `scripts/mkdocs_figures_hook.py` | `tools/figures/mkdocs_hook.py` |
| `docs/stylesheets/figures.css` | `tools/figures/figures.css` |
| `third_party/interfig/` | `tools/figures/third_party/interfig/` |
| the pure half of `build.mjs` (`walkLayout`, `validate`, `describe`, `normalizedEdges`, `decorate`, `render`; `tools/figures/build.mjs:78-360`) | `tools/figures/core.mjs` |
| the `bundle` command (`tools/figures/build.mjs:419-461`) | `tools/figures/bundle.mjs` (praetor only) |

- `third_party` stays a path segment, so the HISS scanner keeps skipping the vendored code
  (`internal/hiss/hiss.go:405-411`).
- One tree lets one Go file embed it: go:embed cannot reach parent or sibling directories.
- `ENGINE_FILES` becomes the three vendored render files plus `core.mjs`. Edits to the
  command-line wrapper no longer invalidate figures.
- Every path the scripts resolve stays relative to the script's own location, so adopters need no
  relocation logic.

### 2. What adopters receive and what they own

Managed by praetor (emitted, audited byte for byte, replaced by `praetorctl adopt --force`), as an
explicit list, never a directory glob:

- render: `core.mjs`, `build.mjs` (`build`, `check`), `types.ts` (self-contained, with the
  `FigTone` type inlined), `third_party/interfig/{vendor.json,VENDOR.md}`,
  `third_party/interfig/upstream/{LICENSE,src/svg.ts,src/geometry.ts,src/model.ts}`;
- view: `dist/loader.js`, `dist/player.js`, the player's static chunks,
  `dist/THIRD-PARTY-LICENSES.txt`, `figures.css`;
- generators and checks: `mkdocs_hook.py`, `astro.mjs`, `serve.mjs`, `docs_diagrams.py`;
- `README.md`: a neutral authoring guide.

Praetor-only, never emitted: `package.json`, `package-lock.json`, `loader.ts`, `player.tsx`,
`keyboard.ts`, `bundle.mjs`, `smoke.mjs`, `figures.test.mjs`, `tsconfig.json`.

Owned by the adopter: `docs/figures/<slug>.ts` (specs), `docs/assets/figures/*` (committed
outputs), and one line of site configuration (the MkDocs `hooks:` entry or the Astro integration
import).

Adopters install no npm package for figures, so no figure lockfile is emitted and a dependency bot
cannot put a managed lockfile out of step with the audit.

### 3. Generic player

- `loader.ts` fetches the `<img>`'s `currentSrc` (same origin, normally an HTTP-cache hit) with a
  byte cap and the existing 10 s timeout (`tools/figures/loader.ts:17`), parses
  `<metadata id="figure-spec">`, and mounts the player with those props. The title comes from the
  figure's `<figcaption>`.
- `registry.json` and the per-spec chunks are removed.
- `bundle.mjs` writes `tools/figures/dist/`, which is committed (operator decision 2).
  `bundle.mjs --check` rebuilds it from the pinned lock with `npm ci --ignore-scripts`, compares
  bytes, and holds the 250 kB budget. It runs on Linux, macOS and Windows. `build.mjs check` no
  longer bundles, so it needs no esbuild.
- A change that moves the lock or the interfig pin rebuilds `dist/` in the same pull request,
  because `bundle.mjs --check` fails otherwise. `sync_interfig.py update` gains that step; today
  it rebuilds only the figures (`scripts/sync_interfig.py:540-546`). A dependency-bot pull request
  that bumps the lock needs the rebuild pushed to its branch.
- `dist/THIRD-PARTY-LICENSES.txt` carries the full MIT texts of interfig, React, react-dom and
  scheduler. The interfig banner and React's `@license` comments stay in the bundle
  (`tools/figures/build.mjs:419,452`).

### 4. One markup source

`core.mjs` renders the `<figure><picture>…</figure>` block and the `<details>` text description
into each `<slug>.json` as `html`, with `{{base}}` and `{{link}}` slots and an escaper equivalent
to Python's `html.escape`. The MkDocs hook, `docs_diagrams.py portable`, the Astro remark plugin
and Go `forge sync-wiki` only substitute the slots. Acceptance: the README portable block and the
built site's figure HTML are byte-identical to the output of `render_block` before the change.

### 5. Delivery channel

- `tools/figures/assets.go` embeds the managed list with explicit patterns and a bounded
  `Names`/`Read` API, the shape of `tools/markdownlint/assets.go`.
- The Markdown gate's emit, remove, refuse and audit code is generalised into one family registry
  that both families use; it is not copied. A family declares its directory, embedded file system,
  names, bound and first-adoption policy. The facet, the workflow and the status context are
  shared, because both families belong to `docs:seo-portal` (operator decision 1).
- New behaviour in the registry: on first adoption, a pre-existing file at a managed path that does
  not match the canonical bytes is refused, not overwritten. `tools/figures/` is a generic name,
  and the Markdown family writes with `force: true` today (`internal/adopt/documentation.go:83`).
- No facet file is added. `DocumentationEnabled` (`internal/adopt/documentation.go:22-26`) decides
  both families. With `docs:seo-portal` enabled, adoption also writes:
  - a managed `.gitattributes` block: `eol=lf` for `tools/figures/**`, `docs/figures/*.ts` and
    `docs/assets/figures/*`; `-text` for `tools/figures/third_party/interfig/upstream/**`,
    mirroring praetor's own `.gitattributes:105-115`;
  - a `docs-figures` target in the managed documentation Makefile block, attached to `verify-all`
    beside `docs-lint` (`internal/adopt/verification_makefile.go:18-26`);
  - a figure step in the emitted documentation workflow (`.github/workflows/praetor-docs.yml`,
    `internal/adopt/documentation.go:35-64`): `node tools/figures/build.mjs check` and
    `python3 -B tools/figures/docs_diagrams.py sources`. With no spec present, the step exits 0
    and prints why. The job keeps its one required status context, `Documentation Governance`,
    so branch-protection reconciliation does not change. The job runs on Linux only; praetor's
    three-platform figure leg (`.github/workflows/portability.yml:280-288`) certifies the same
    bytes on macOS and Windows, as its Markdown gate leg does for the Markdown runner
    (`.github/workflows/portability.yml:254-259`).
- With the facet disabled, canonical copies of both families are removed and drifted copies are
  refused, as for the Markdown gate today.
- Default membership follows the facet: `docs:seo-portal` is in all three default lists, so every
  default adopter receives the figure family. Unifying the three lists remains a HISS-19 task, but
  this record does not depend on it.

### 6. Generators

- **MkDocs.** `mkdocs_hook.py` keeps `on_page_markdown` (fence to JSON `html`, base from
  `site_base(page.url)`). `on_config` appends the loader (as a module) and `figures.css`;
  `on_files` adds the `dist/` files and the CSS as generated files, so nothing is copied into the
  adopter's `docs_dir`. `File.generated(config, src_uri, *, content, abs_src_path, inclusion)`
  exists in MkDocs 1.6.1
  (`python3 -c "import inspect; from mkdocs.structure.files import File; print(inspect.signature(File.generated))"`).
  An adopter's `mkdocs.yml` needs `hooks: [tools/figures/mkdocs_hook.py]` and
  `exclude_docs: /figures/`. An unknown slug logs a warning and fails `--strict`, as today. The
  site build needs Python only (operator decision 3).
- **Starlight.** `astro.mjs` is an Astro integration that uses `node:` builtins only, so the
  preset's lockfile does not change. It adds a remark plugin (a ` ```figure ` code node becomes the
  JSON `html` with the Astro base), a head script for the loader, a development middleware, and a
  copy of `dist/` and `docs/assets/figures/` in `astro:build:done`, through the static-serve
  helper extracted from `smoke.mjs` (`tools/figures/smoke.mjs:26-30`). `figures.css` resolves
  `var(--md-…, var(--sl-color-…))`, so one file serves both generators. The loader also listens
  for `astro:page-load`.
- **README.** `python3 -B tools/figures/docs_diagrams.py portable --write README.md` refreshes
  `<!-- figure:<slug> -->` blocks with repository-relative SVG paths.

### 7. Adopter authoring

1. Run `praetorctl adopt` with the `docs:seo-portal` facet, which the default facet set includes.
2. Write `docs/figures/<slug>.ts`: `import type { PraetorFigure } from '../../tools/figures/types.ts'`
   and a default export with `title`, `alt` (at most 125 characters), `evidence` (`path:Symbol`
   anchors in the adopter's own code, checked as a language-independent substring,
   `scripts/docs_diagrams.py:478-490`), optional `describe`, and `props`. The limits are those of
   `tools/figures/build.mjs:36-39`.
3. Run `node tools/figures/build.mjs build` (Node 22.18 or later, no npm install). Commit the spec
   and its three outputs.
4. Reference the figure with a ` ```figure ` fence naming the slug.
5. Run `make docs-figures`, or on a machine without make:
   `node tools/figures/build.mjs check && python3 -B tools/figures/docs_diagrams.py sources`.

Type-checking a spec is optional and happens in the editor; `validate` in `core.mjs` is the
authoritative check (`tools/figures/types.ts:1-6`).

### 8. Praetor is the first adopter

Praetor declares `docs:seo-portal` (`.standards.yaml:33-38`), so it receives the managed family
like any adopter:

- Praetor's own site loads the committed `dist/` through the same hook, so no workflow bundles
  for a site build:
  - `pages.yml` drops its `bundle` step and the comment that calls the bundle gitignored
    (`.github/workflows/pages.yml:72-75,87`) and runs `bundle.mjs --check` instead;
  - the CI Documentation Integrity Audit drops its `bundle` step
    (`.github/workflows/ci.yml:188-189`) and runs `make docs-figures` after
    `make docs-figures-check` (`.github/workflows/ci.yml:183-187`), because a documentation-only
    pull request skips verify-all;
  - `.gitignore` drops the bundle entry and its comment (`.gitignore:40-43`).
- The three-platform figure leg (`.github/workflows/portability.yml:280-288`) runs the commands of
  both targets inline, since make is not on the Windows image
  (`.github/workflows/portability.yml:274-275`), and therefore certifies the exact bytes adopters
  receive.
- Praetor's own `docs-figures-check` target (`Makefile:203-210`) keeps only the praetor-only steps
  (npm install, tests, type check, `bundle.mjs --check`) and leaves `build.mjs check` and `sources`
  to the managed `docs-figures` target, so each command runs once in `verify-all`.

### 9. Presets

- The MkDocs preset drops its Mermaid fence, lists the hook, excludes `/figures/`, and replaces the
  Mermaid example with a neutral example figure whose evidence points into preset files. The
  praetor name in the example page's front matter and heading
  (`docs/presets/mkdocs/docs/index.md:2-8`) gives way to neutral text, because an adopter's site
  carries its own identity.
- The checks that read the preset's Mermaid move with it, in the same change:
  - **BUG-680 guard.** `TestHomeWiki_Positive_CompileContextFlowsFromAGENTS` reads the preset's
    Mermaid through `flowEdges` (`internal/forge/wiki_test.go:357-363`), and
    `compileContextFlowViolations` reports two violations for an empty edge list
    (`TestHomeWiki_Boundary_EmptyEdgesProducesViolations`, `internal/forge/wiki_test.go:402-411`),
    so a preset without Mermaid would fail the positive test. The example figure draws the
    preset's own files, not the compile-context flow, so the guard drops its preset input rather
    than retargeting it and keeps the `governance-lifecycle` JSON edges
    (`internal/forge/wiki_test.go:364-366`).
  - **Mermaid parser.** The generated Home page is the only other Mermaid input
    (`internal/forge/wiki_test.go:354`), and it moves to the figure under ADR-0015 §9 ("Generated
    pages"). Once both inputs are gone, the Mermaid replay
    `TestHomeWiki_Negative_RejectsManifestToAGENTSFlow` (`internal/forge/wiki_test.go:371-385`)
    is restated as an edge list with the same three expected violations, and `flowEdges` with
    its two Mermaid patterns (`internal/forge/wiki_test.go:280-300`) is removed.
  - **Preset fence pins.** `test_repository_configs_declare_the_mermaid_fence`
    (`scripts/test_docs_diagrams.py:47-51`) requires the preset to declare the `mermaid` fence,
    and `test_repository_site_enables_figures_and_preset_does_not`
    (`scripts/test_docs_diagrams.py:286-289`) requires it to enable `mermaid` only. Both now
    require the preset to enable `figure` only.
  - **Preset README.** Its "Mermaid Diagrams" section (`docs/presets/mkdocs/README.md:70-75`)
    describes the hook and the adopted engine instead of the `mermaid` custom fence.
- The Starlight preset adds the integration, the CSS and an example figure.
- The `docs-presets` CI job builds an adopter fixture instead of the preset in place: a temporary
  repository, `praetorctl adopt` with `docs:seo-portal`, `build`, `check`, the MkDocs and Astro
  builds, `docs_diagrams.py site`, and the Chromium smoke test.
- `docs_diagrams.py` reads `site_url` with a pattern that takes `!ENV` as the URL
  (`scripts/docs_diagrams.py:64`); the preset declares `site_url: !ENV DOCS_SITE_URL`
  (`docs/presets/mkdocs/mkdocs.yml:6`). The parse is fixed.

### 10. Wiki (later, optional)

`forge sync-wiki` renders figure fences by substituting the JSON `html`. The SVGs it references are
written next to the wiki pages from copies embedded in the binary, so an adopter's wiki shows the
figures that match the praetorctl version that wrote it, with no runtime dependency on praetor's
Pages deploy. This lands only after the smoke test is green and the devcontainer bootstrap
headroom test (`TestRepositoryBootstrapSourceKeepsHeadroom`,
`internal/devcontainer/bootstrap_source_test.go:165`) still passes.

### 11. License and neutrality

- `upstream/LICENSE` ships verbatim in every copy; the SVG credit comment
  (`tools/figures/build.mjs:328-331`) and the bundle banner are kept.
- `THIRD-PARTY-NOTICES.md:19-20` is rewritten: the binary now carries interfig source and the
  React bundle. `internal/supplychain/notices_test.go` reads the `tools/figures` lock as it reads
  the Markdown lock today (`internal/supplychain/notices_test.go:279-285`).
- `REUSE.toml` moves the interfig override to the new path, after the `**` table, and adds a
  `tools/figures/dist/**` annotation (EUPL-1.2 AND MIT). `praetorctl audit` warns when an adopter's
  `REUSE.toml` has no override for the vendored MIT files.
- Managed files carry project identity only. The `build.mjs` header
  (`tools/figures/build.mjs:2-3`), the `package.json` description and praetor-specific test
  assertions leave the shipped set.

### Amendments to ADR-0015

Line numbers are those of the amended ADR-0015.

| ADR-0015 passage | Amended to |
| :-- | :-- |
| Status, `:7-12` | Notes the amendment and that its paths and file names predate the move to one tree (section 1). |
| Decision, `:112-117` (bundle during the docs build) | The player is bundled once and committed; no docs build bundles (section 3, operator decision 2). |
| §2, engine hash, `:177-179` | Covers the vendored render files and `core.mjs` (section 1). |
| §2, bundle, `:184-194` and `:199-200` (gitignored bundle; spec chunks and `registry.json`; budget in `check`) | The generic player is committed under `tools/figures/dist/`; no spec chunks, no registry; `bundle.mjs --check` holds the budget (section 3). |
| §2, `:206-210` (why the bundle is not committed) | The bundle is committed and reproducibility-checked (operator decision 2). |
| §4, config, `:257-259` | The hook adds the loader and CSS itself (section 6). |
| §4, loader, `:285-287` | The loader reads props from the SVG metadata (section 3). |
| §5, `:309-313` (Python renderer) | One markup source in `core.mjs`; callers substitute (section 4). |
| §5, `:329-333` (adopter wikis resolve through `portable`) | Did not hold (`internal/forge/wiki.go:445`); replaced by section 10. |
| §7, `:387-388` (`registry.json` check) | The site check looks for the SVG metadata (section 3). |
| §7, `:391-393` (`sources` hashes `build.mjs`) | `core.mjs` replaces `build.mjs` among the hashed engine files (section 1). |
| §7, `:408-412` (`docs-figures-check` runs `check` and `sources`) | It keeps the praetor-only steps; `build.mjs check` and `sources` move to the managed `docs-figures` target (sections 5 and 8). |
| §7, `:416-418` (`pages.yml` bundles) | `bundle.mjs --check` replaces `bundle` (section 8). |
| §7, `:421-423` (CI docs audit bundles) | No bundle step; the audit runs `docs-figures-check` and `docs-figures` (section 8). |
| §7, `:424-426` (portability leg) | Also runs the `docs-figures` commands (section 8). |
| §8, `:449-450` (`update` rebuilds figures) | Also rebuilds the committed player (section 3). |
| §9, `:467-468`, `:482`, `:495-498` (preset keeps Mermaid) | The preset uses figures (section 9). |
| §9, `:486-489` (BUG-680 guard reads the preset's Mermaid) | The guard drops its preset input and keeps the `governance-lifecycle` JSON edges (section 9). |
| License item 3, `:519-522` (`tools/figures/` stays EUPL-1.2) | `tools/figures/dist/**` is annotated EUPL-1.2 AND MIT (section 11). |
| License item 7, `:530-534` | Copies reach adopters through adoption, with the LICENSE (section 11). |
| Negative, `:567-570` (the docs build gains Node) | The MkDocs build needs Python only; Node stays in the checks (operator decision 3). |
| Neutral, `:581-583` | Decided by this record (sections 5 and 9). |
| Verification, `:591-595` | `docs-figures-check` runs the praetor-only steps, `docs-figures` is added, and the bundle is committed (sections 3 and 8). |
| Verification, `:605-606` (CI docs audit) | It also runs `make docs-figures` (section 8). |

## Alternatives considered

| Option | Why not |
| :-- | :-- |
| A separate `docs:figures` facet (the draft of this record), with its own workflow and status context | Lets a repository keep the Markdown gate without the figure engine, but adds a fourth facet definition, a second emitted workflow and a second required status context for one documentation concern. Rejected by operator decision 1. |
| Managed engine where adopters bundle the player themselves (npm install of esbuild and React in every adopter) | Keeps ADR-0015's no-committed-bundle rule, but every adopter carries the full toolchain (`node_modules` of about 70 MB, or about 19.7 MB for esbuild plus React alone) and an emitted lockfile that dependency bots can push out of step with the audit, and `check` needs esbuild in adopter CI. Rejected by operator decision 2. |
| Engine inside praetorctl: the esbuild Go API plus the goja JavaScript VM render specs in-process, with no Node for adopters | A prototype produced byte-identical output, but the stripped praetorctl grows from 19.2 MB to 29.9 MB (+55.7%), and the root module goes from one external dependency to eight, against `docs/adr/0008-spec-driven-provider-integration.md:38` and `docs/adr/0009-structural-unification.md:44`. goja publishes no tagged releases, and the Python checker and renderer would be ported to Go. Rejected by operator decision 3. |
| Signed figures kit: an npm-pack tarball published as a release asset, pinned by sha512 in an emitted lockfile and in `.standards.lock` | Adopters install about 0.4 MB, but the design depends on a release pipeline that has never published, needs `allow-remote=root` against npm 12's default, adds a second trust chain and two pins of one artifact, and makes praetor's own site build from source while adopters use the kit: two paths held equal only by a test. |
| Adopters link to SVGs on praetor's Pages site | Works only for praetor's own figures, depends on praetor's deploy, and shows the latest main rather than the adopter's version. |
| Static SVG only for adopters | No JavaScript and no player, but drops the requested tabs, pause and narration. Remains the fallback when JavaScript or the bundle is missing. |

## Consequences

### Positive

- Adopters get figures of their own code with no npm install: Node 22.18 or later for authoring
  and `check`, the Python 3 standard library for the hook and the source check. An MkDocs site
  build needs Python only.
- One engine path: praetor and adopters run the same bytes, and the three-platform leg that already
  proves praetor's figures proves the adopters' engine.
- No new facet, workflow or required status context: repositories on `docs:seo-portal` receive
  figures on their next `praetorctl adopt`.
- No new trust channel and no network fetch at adopter build time: the engine is pinned by the
  praetorctl version, verified by `praetorctl audit`, and signed by the binary release chain once
  releases exist (`.goreleaser.yaml`, `.github/workflows/release-binaries.yml`).
- The loader serves everything from the adopter's own origin; nothing loads from a CDN.
- Adopter figures stop going stale on wrapper edits: only the render core is hashed.

### Negative / Trade-offs

- Opting out of figures means disabling `docs:seo-portal`, which also removes the Markdown gate
  (operator decision 1).
- Every `docs:seo-portal` repository carries the figure family, about 0.35 MB, whether or not it
  writes a figure; the default facet set includes the facet.
- The player bundle, about 240 kB of minified code, is committed in praetor and rewritten in each
  adopter on `praetorctl adopt --force` after a React, react-dom, interfig or esbuild bump. The
  reproducibility check stands in for reading the minified code.
- After a praetorctl upgrade that adds or changes managed figure files, `praetorctl audit` fails
  in an existing `docs:seo-portal` repository until `praetorctl adopt --force` runs, the remedy the
  audit already prints (`cmd/standardsctl/audit_documentation.go:32`).
- praetorctl grows by about 0.35 MB of embedded files, about 1.8% of the 19.2 MB stripped binary
  (`go build -trimpath -ldflags='-s -w' ./cmd/standardsctl` at 7e7746a3; the growth is an estimate
  from the file sizes). The devcontainer bootstrap frames grow from about 58% to an estimated 65%
  of their limit; `TestRepositoryBootstrapSourceKeepsHeadroom` decides.
- An interfig or core change still marks every adopter figure stale, by design; the adopter runs
  `praetorctl adopt --force`, `build`, and commits.
- Two generator adapters exist, a Python hook and an Astro integration, because each generator's
  plugin API has its own language. Rendering stays single (section 4).
- Nothing reaches adopters through a released binary until a `v*` release is published; until then
  adopters pin the `praetor-adopt` action to a commit.
- The emitted gate cannot run the Chromium smoke test, because praetor emits no site build.
  Praetor smoke-tests the identical player on the fixture builds.

### Neutral

- Mermaid leaves the presets. `docs_diagrams.py site` rejects a ` ```mermaid ` fence the site does
  not enable.
- Unverified and unchanged by this record: whether GitHub animates an SVG in a README and honours
  `<source media="(prefers-reduced-motion: reduce)">`
  (`docs/adr/0015-interactive-figures-from-vendored-interfig.md:327-328`); the SVG palette follows
  the operating system, not the site toggle
  (`docs/adr/0015-interactive-figures-from-vendored-interfig.md:334-336`).

## Open questions

None. The draft's three questions (default or opt-in membership, how the prebuilt player reaches
adopters, and Node or Go for authoring) are decided under "Operator decisions (2026-09-27)".

## Verification & Compliance

```bash
node --test 'tools/figures/third_party/interfig/upstream/src/*.test.ts'
npm ci --prefix tools/figures --ignore-scripts --no-audit --no-fund
npm --prefix tools/figures test
node tools/figures/build.mjs check          # regenerate and byte-compare; no esbuild needed
node tools/figures/bundle.mjs --check       # rebuild dist/ from the lock, byte-compare, 250 kB budget
python3 -B tools/figures/docs_diagrams.py sources
python3 -B scripts/sync_interfig.py verify
go test ./tools/figures/... ./internal/adopt/... ./cmd/standardsctl/... ./internal/devcontainer/... ./internal/supplychain/...
mkdocs build --strict -d site
python3 -B tools/figures/docs_diagrams.py site --config mkdocs.yml --docs docs --site site
npm --prefix tools/figures run smoke -- --site site --require-browser
reuse lint
make verify-all
```

- `tools/figures/assets_test.go`: the embedded list equals `git ls-files tools/figures` minus the
  praetor-only files.
- `internal/adopt` tests: write, refuse a foreign pre-existing file, remove, refuse drift, facet
  off; positive, negative and boundary cases.
- `praetorctl audit` on a temporary repository adopted with `docs:seo-portal`: passes after
  adoption, fails with the `adopt --force` remedy after a managed figure file is edited, and finds
  no figure file after the facet is disabled.
- `docs-presets` CI: the adopter-fixture build (section 9), on Linux, with the smoke test.
- `.github/workflows/portability.yml`: `build.mjs check`, `bundle.mjs --check` and `sources` on
  Linux, macOS and Windows.
- Not yet measured: `build.mjs build` and `check` on Node 22.18, the declared floor; the
  implementation adds that leg before the floor is relied on.
