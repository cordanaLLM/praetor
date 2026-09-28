# ADR-0016: Interactive Figures for Adopters Through a Managed Engine Family

## Status

Proposed — 2026-09-27; updated 2026-09-28 with two further operator decisions.

This record amends ADR-0015 while ADR-0015 is still Proposed (`docs/adr/README.md`, rule 4). The
amended passages are listed under "Amendments to ADR-0015" below and are marked in ADR-0015
itself. The owner decided the three questions the draft of this record left open on 2026-09-27
and two further questions on 2026-09-28; "Operator decisions" under Decision records all five.

Citations: a decision record is cited by section heading and a short quoted phrase, never by line
number, so a later edit to the cited record cannot silently stale the citation. A repository file
is cited by symbol, workflow step or make target where it has one. The few remaining `path:line`
references point into commit 54812c7d.

## Context

ADR-0015 built an interactive figure engine for praetor's own site and left adopters to a separate
decision (ADR-0015 "Neutral": "left to a separate decision", now amended). The operator has since
asked that every adopter can draw the same figures of its own code and that Mermaid leaves the
documentation presets. This record is that decision.

### What exists on main (54812c7d)

- **Engine.** `tools/figures/build.mjs` renders `docs/figures/<slug>.ts` into committed
  `docs/assets/figures/<slug>.{svg,static.svg,json}` (its `build` and `check` commands, through
  `render`).
  - Paths are fixed relative to praetor's tree: `ROOT` is `../../` from the script, and the
    vendored engine is imported from `../../third_party/interfig/upstream/src/svg.ts`.
  - `ENGINE_FILES`, the list the engine hash covers, holds the three vendored render files and
    `build.mjs` itself, so any edit to the command-line wrapper, even a comment, marks every
    figure stale.
- **Rendering needs no npm package.** `build.mjs` imports only `node:` builtins and the vendored
  `svg.ts`. `svg.ts` imports only `./geometry.ts` and `./model.ts`, and `model.ts` imports React
  as a type only (`import type`, erased at run time; `third_party/interfig/upstream/src/model.ts:2`).
  Measured for this record on a `git archive` copy of 54812c7d without `node_modules`, on Node
  26.10.0: `node tools/figures/build.mjs build` rewrote all 30 committed outputs byte-identical.
  Node 22.18, the declared floor (`engines` in `tools/figures/package.json`), was not measured.
- **`check` does need esbuild.** `runCheck` calls `bundle` to hold the player to its size budget.
  The same copy failed with `Cannot find package 'esbuild'`.
- **Both SVG variants carry the full spec.** `decorate()` writes `{props}` into
  `<metadata id="figure-spec">`, and `render()` decorates the static variant with the same figure,
  steps included.
- **The player is repository-specific** only because of `registry.json` and one bundle chunk per
  spec (`bundle()` in `build.mjs`; `fetchRegistry` in `tools/figures/loader.ts`). The player chunk
  itself (React, react-dom, interfig, the keyboard shim) is the same for every site: 241.0 kB
  minified and 77.1 kB gzip against the 250 kB `PLAYER_BUDGET`, as
  `npm --prefix tools/figures run check` reports it at 54812c7d. esbuild names the shared chunks
  `chunks/[name]-[hash]` (`chunkNames` in `bundle()`).
- **The checker and the markup renderer are Python.** `scripts/docs_diagrams.py` holds the `site`,
  `sources` and `portable` commands and `render_block`, which builds the `<figure><picture>` block
  with the `<details>` text description. The MkDocs hook (`on_page_markdown` in
  `scripts/mkdocs_figures_hook.py`) imports that module and calls `expand`, which calls
  `render_block`. No Go code renders a figure: `figureFence` in `internal/forge/wiki.go` writes a
  raw ` ```figure ` fence, so ADR-0015's claim that adopter wikis resolve through `portable` did
  not hold (ADR-0015 §5 "Outside the site: README and wiki": "praetor-engine pages", amended).
- **A managed asset family registry already carries engine files to adopters.**
  `internal/managedasset` declares each family: its embedding package, its facet, its inventory
  with a `MaxAssets` bound, an optional hosted workflow with its status context, and
  `RefuseForeign`, which refuses to overwrite a pre-existing file at a managed path on first
  adoption. Its package comment states the rule this record relies on: a new family is one
  embedding package plus one entry in `Families`, and no new code path. Three consumers read the
  registry and name no family:
  - adoption: `internal/adopt/managed_family.go` emits a family (`reconcileManagedFamily`),
    refuses foreign files (`refuseForeignFamilyFiles`) and removes the canonical copies of a
    disabled facet (`removeManagedFamilies`); `DocumentationEnabled` and `DocumentationFamilies`
    in `internal/adopt/documentation.go` select the families;
  - audit: `auditManagedFamily` and `auditExactManagedFile` in
    `cmd/standardsctl/audit_documentation.go` (a line-ending-normalised compare that reports
    "differs from the locked Praetor asset; run 'praetorctl adopt --force'");
  - the devcontainer bootstrap: `bootstrapAssetFamilies` in
    `internal/devcontainer/bootstrap_source.go`.

  The Markdown gate of the `docs:seo-portal` facet is the only family in `Families`. Its package,
  `tools/markdownlint/assets.go`, also holds the hosted workflow text (`Workflow`, written to
  `.github/workflows/praetor-docs.yml`, whose job and status context is `Documentation
  Governance`). The managed Makefile block (`DocumentationMakefileBlock` in
  `internal/adopt/verification_makefile.go`) attaches `docs-lint` to `verify-all`, and
  `managedTailBlock` (`internal/adopt/managed_block.go`) merges adoption-owned blocks into
  `.gitignore` and names a `.gitattributes` block as the same shape.
- **Presets.** The MkDocs preset declares only a Mermaid fence (`custom_fences` in
  `docs/presets/mkdocs/mkdocs.yml`), and its README says it stays on Mermaid because offering
  figures to adopters is a separate decision (`docs/presets/mkdocs/README.md`, "Mermaid
  Diagrams"). The Starlight preset has no diagram support
  (`docs/presets/starlight/astro.config.mjs`). Presets are reference directories that adoption
  never emits (ADR-0015 "License and credit obligations", item 7: "presets stay reference
  directories").
- **Default facets are defined three times** and all three include `docs:seo-portal`:
  `resolveFacets` in `internal/adopt/adopt.go`, the `facets` flag default in
  `cmd/standardsctl/init.go`, and `internal/harvester/onboard.go`. They differ only in
  `agent:sandboxed`.
- **Delivery state**, observed on 2026-09-27 at f9f1931b and not re-run for this record, because it
  needs forge calls (`gh release list`, `gh run list --workflow pages.yml`): no release had been
  published, and `pages.yml` failed at its smoke step on every main push from 024848ad on.
  54812c7d has since changed the smoke test: a figure with scenario tabs must advance its active
  step under autoplay before a packet from clicking its tabs counts (BUG-1031). Whether `pages.yml`
  passes now was not checked.

### Constraints

- HISS-19: praetor's own site and every adopter use one engine path and one markup renderer, and
  a new managed family reuses the registry instead of a second emission path.
- HISS-21: every gate runs on Linux, macOS and Windows or skips with a stated reason.
- HISS-02, HISS-04, HISS-15 and HISS-20: bounded loops and I/O, complexity caps, positive,
  negative and boundary tests replayed both ways.
- The root module keeps exactly one external dependency (`go.mod`; ADR-0008 "Decision", item 3
  "Offline compiler and the dependency decision": "keeps exactly `gopkg.in/yaml.v3`"; ADR-0009
  "Accepted constraints", item 3: "Keep one root Go module").
- ADR-0014: text in committed adopter artifacts is neutral and carries no operator data (ADR-0014
  §1 "Layering rule", last row: "the text itself becomes neutral").
- The MIT notice duty for interfig and React travels with every copy (ADR-0015 "License and credit
  obligations").

## Decision

Deliver the figure engine to adopters as a managed, go:embed'ed asset family of the existing
`docs:seo-portal` facet: a second entry in the registry the Markdown gate already uses. The adopter
tree has the same layout as praetor's, so the same bytes run in both. The player becomes generic:
it reads the spec from the SVG it replaces, so it is prebuilt once in praetor, checked for
reproducibility, and emitted byte for byte. Adopters render and check figures with plain Node and
no npm install; Python remains only in the MkDocs hook.

### Operator decisions (2026-09-27 and 2026-09-28)

The owner settled the three questions the draft left open on 2026-09-27 and two more on
2026-09-28. Each is binding on the sections below.

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
4. **The figure check is a step of the documentation workflow** (2026-09-28). In every adopter the
   figure check runs as a step of the emitted `.github/workflows/praetor-docs.yml`, under that
   job's single required status context, `Documentation Governance`. A repository with no figure
   spec passes the step, which prints why it skipped (section 5).
5. **Figure checks run in the Node engine** (2026-09-28). The `sources`, `site` and `portable`
   checks are ported from `scripts/docs_diagrams.py` into the Node figure engine, so an adopter
   needs Node only. Python keeps only the thin MkDocs hook (sections 1 and 6).

### 1. One engine tree, identical in praetor and in adopters

Praetor consolidates the figure engine into `tools/figures/` and ports the Python checker into it
(operator decision 5):

| Today | After |
| :-- | :-- |
| `scripts/docs_diagrams.py`: the `site`, `sources` and `portable` commands | the `site`, `sources` and `portable` commands of `tools/figures/build.mjs`, implemented in `tools/figures/checks.mjs` |
| `scripts/test_docs_diagrams.py`: the checker's fixtures | `tools/figures/checks.test.mjs`, replayed in both directions |
| `scripts/mkdocs_figures_hook.py`, which imports `docs_diagrams` | `tools/figures/mkdocs_hook.py`, self-contained (section 6) |
| `docs/stylesheets/figures.css` | `tools/figures/figures.css` |
| `third_party/interfig/` | `tools/figures/third_party/interfig/` |
| the pure half of `build.mjs` (`walkLayout`, `validate`, `describe`, `normalizedEdges`, `decorate`, `render`, `LIMITS`) | `tools/figures/core.mjs` |
| the `bundle` command (`bundle()`, `playerBytes()` and `runBundle()` in `build.mjs`) | `tools/figures/bundle.mjs` (praetor only) |

- `third_party` stays a path segment, so the HISS scanner keeps skipping the vendored code
  (`ignoredDirNames` in `internal/hiss/hiss.go`).
- One tree lets one Go file embed it: go:embed cannot reach parent or sibling directories.
- `ENGINE_FILES` becomes the three vendored render files plus `core.mjs`. Edits to the
  command-line wrapper or to the checks no longer invalidate figures. The ported `sources` imports
  the list from `core.mjs`; the Python checker parses it out of `build.mjs` with a regular
  expression (`ENGINE_BLOCK` in `scripts/docs_diagrams.py`), a second reading of one list that the
  port removes.
- Every path the scripts resolve stays relative to the script's own location, so adopters need no
  relocation logic.
- The port keeps every check of the Python checker, including the `exclude_docs` matching and the
  Mermaid-fence rejection that 54812c7d added. It fixes one parse on the way: `SITE_URL` in
  `scripts/docs_diagrams.py` takes `!ENV` as the URL, and the MkDocs preset declares
  `site_url: !ENV DOCS_SITE_URL`.
- The ported commands need no npm package, like `build`. `scripts/sync_interfig.py` is praetor-only
  maintenance tooling, not a figure check, and stays Python.

### 2. What adopters receive and what they own

Managed by praetor (emitted, audited byte for byte, replaced by `praetorctl adopt --force`), as an
explicit list, never a directory glob:

- render and check: `core.mjs`, `checks.mjs`, `build.mjs` (`build`, `check`, `sources`, `site`,
  `portable`), `types.ts` (self-contained, with the `FigTone` type inlined),
  `third_party/interfig/{vendor.json,VENDOR.md}`,
  `third_party/interfig/upstream/{LICENSE,src/svg.ts,src/geometry.ts,src/model.ts}`;
- view: `dist/loader.js`, `dist/player.js`, `dist/THIRD-PARTY-LICENSES.txt`, `figures.css`;
- generators: `mkdocs_hook.py`, `astro.mjs`, `serve.mjs`;
- `README.md`: a neutral authoring guide.

`dist/` holds exactly those three files and no chunk: `bundle.mjs` builds the loader and the
player without code splitting, and the loader imports the player by the fixed name `./player.js`
(section 3). No output name carries a hash, so a React, interfig or esbuild bump changes bytes but
never the list, and the go:embed patterns and asset names need no hand edit.

Praetor-only, never emitted: `package.json`, `package-lock.json`, `loader.ts`, `player.tsx`,
`keyboard.ts`, `bundle.mjs`, `smoke.mjs`, `figures.test.mjs`, `checks.test.mjs`,
`astro.test.mjs`, `testkit.mjs`, `test_mkdocs_hook.py`, `fence-fixtures.json`,
`markup-fixtures.json`, `exclude-fixtures.json`, `tsconfig.json`.

Owned by the adopter: `docs/figures/<slug>.ts` (specs), `docs/assets/figures/*` (committed
outputs), and one line of site configuration (the MkDocs `hooks:` entry or the Astro integration
import).

Adopters install no npm package for figures, so no figure lockfile is emitted and a dependency bot
cannot put a managed lockfile out of step with the audit.

### 3. Generic player

- `loader.ts` fetches the `<img>`'s `currentSrc` (same origin, normally an HTTP-cache hit) with a
  byte cap and the existing 10 s timeout (`FETCH_TIMEOUT_MS` in `tools/figures/loader.ts`), parses
  `<metadata id="figure-spec">`, and mounts the player with those props. The title comes from the
  figure's `<figcaption>`.
- `registry.json` and the per-spec chunks are removed.
- `bundle.mjs` writes `tools/figures/dist/`, which is committed (operator decision 2). It builds
  `loader.js` and `player.js` as two entry points with splitting off, and marks the loader's
  `import('./player.js')` external, so the player is neither inlined into the loader nor split
  into hashed chunks.
- `bundle.mjs --check` rebuilds `dist/` from the pinned lock with `npm ci --ignore-scripts`,
  compares bytes, fails on any output file outside the list in section 2, and holds the 250 kB
  budget, which now applies to `player.js` alone. It runs on Linux, macOS and Windows.
  `build.mjs check` no longer bundles, so it needs no esbuild.
- A change that moves the lock or the interfig pin rebuilds `dist/` in the same pull request,
  because `bundle.mjs --check` fails otherwise. `sync_interfig.py update` gains that step; today
  `run_upstream_checks` in `scripts/sync_interfig.py` rebuilds only the figures. A dependency-bot
  pull request that bumps the lock needs the rebuild pushed to its branch.
- `dist/THIRD-PARTY-LICENSES.txt` carries the full MIT texts of interfig, React, react-dom and
  scheduler. The interfig banner (`BANNER` in `build.mjs`) and React's `@license` comments
  (`legalComments: 'eof'`) stay in the bundle.

### 4. One markup source

`core.mjs` renders the `<figure><picture>…</figure>` block and the `<details>` text description
into each `<slug>.json` as `html`, with `{{base}}` and `{{link}}` slots and an escaper equivalent
to Python's `html.escape`. The MkDocs hook, the `portable` command, the Astro remark plugin and Go
`forge sync-wiki` only substitute the slots. Acceptance: the README portable block and the built
site's figure HTML are byte-identical to the output of `render_block` in
`scripts/docs_diagrams.py` before the change.

### 5. Delivery channel

- `tools/figures/assets.go` embeds the managed list with explicit patterns and exposes
  `Directory`, `SourceFile`, `MaxAssets`, `FS`, `Names` and `Read`, the shape of
  `tools/markdownlint/assets.go`.
- The figure family is one more entry in `Families` (`internal/managedasset/family.go`), with
  `docs:seo-portal` as its facet. Adoption, audit and the devcontainer bootstrap pick it up through
  the registry with no new code path; `MaxFamilies` leaves room for it.
- The family sets `RefuseForeign`: on first adoption, a pre-existing file at a managed path that
  does not match the canonical bytes is refused, even under `--force`, because `tools/figures/` is
  a generic name a repository may already use. The Markdown family leaves the flag off.
- The family declares no hosted workflow of its own: `WorkflowFile`, `StatusContext` and
  `Workflow` stay empty, as the registry allows for a family without a hosted gate. The one
  documentation workflow gains a figure step instead (operator decision 4): the `Workflow` text in
  `tools/markdownlint/assets.go` runs `node tools/figures/build.mjs check` and
  `node tools/figures/build.mjs sources` after its `Verify public Markdown` step. The text stays
  with the Markdown family because one facet enables and removes both families together.
  - The job keeps its one required status context, `Documentation Governance`, so
    branch-protection reconciliation does not change.
  - The job runs on Linux only. Praetor's "Figure Build Check" step in
    `.github/workflows/portability.yml` certifies the same bytes on macOS and Windows, as its
    "Markdown Gate Self-Test" step does for the Markdown runner.
- No spec, no work: in a repository without `docs/figures/*.ts`, `check` and `sources` exit 0 and
  print that the repository has no figure spec. The skip lives in the engine, so the workflow step,
  the managed Makefile target and a direct run skip alike on every platform (HISS-21).
- No facet file is added. `DocumentationEnabled` decides both families. With `docs:seo-portal`
  enabled, adoption also writes:
  - a managed `.gitattributes` block through `managedTailBlock`: `eol=lf` for `tools/figures/**`,
    `docs/figures/*.ts` and `docs/assets/figures/*`, and `-text` for
    `tools/figures/third_party/interfig/upstream/**`, mirroring praetor's own figure rules in
    `.gitattributes`;
  - a `docs-figures` target in the managed documentation Makefile block
    (`DocumentationMakefileBlock`), attached to `verify-all` beside `docs-lint`, which runs the
    same two Node commands.
- With the facet disabled, canonical copies of both families are removed and drifted copies are
  refused, as for the Markdown gate today.
- Default membership follows the facet: `docs:seo-portal` is in all three default lists, so every
  default adopter receives the figure family. Unifying the three lists remains a HISS-19 task, but
  this record does not depend on it.

### 6. Generators

- **MkDocs.** `mkdocs_hook.py` uses only the Python standard library and the MkDocs API and imports
  no checker.
  - `on_page_markdown` replaces each fence with the JSON `html`, with the base derived from the
    page URL.
  - `on_config` appends the loader (as a module) and `figures.css`.
  - `on_files` adds the `dist/` files and the CSS as generated files, so nothing is copied into the
    adopter's `docs_dir`. `File.generated(config, src_uri, *, content, abs_src_path, inclusion)`
    exists in MkDocs 1.6.1
    (`python3 -c "import inspect; from mkdocs.structure.files import File; print(inspect.signature(File.generated))"`).
  - An adopter's `mkdocs.yml` needs `hooks: [tools/figures/mkdocs_hook.py]` and
    `exclude_docs: /figures/`. An unknown slug logs a warning and fails `--strict`, as today. The
    site build needs Python only (operator decision 3).
- **One fence scanner per language.** The hook must find fences in Python, because MkDocs runs
  hooks in-process and the site build stays Python-only (operator decisions 3 and 5), and the Node
  checker must find the same fences. That makes fence scanning the one behaviour implemented twice,
  stated here as HISS-19 requires.
  - Both scanners replay `fence-fixtures.json` in both directions (HISS-20): nested fences, longer
    closing fences, tilde fences, indented fences and an unclosed fence at the end of a page.
  - The `site` check compares each page's fence count with its rendered figures, so a divergence
    on a real page also fails the site build.
- **Starlight.** `astro.mjs` is an Astro integration that uses `node:` builtins only, so the
  preset's lockfile does not change.
  - A remark plugin turns a ` ```figure ` code node into the JSON `html` with the Astro base. It
    works on the Markdown syntax tree, so it needs no fence scanner of its own.
  - It adds a head script for the loader, a development middleware served by `serve.mjs` (the
    static-serve helper extracted from `serve` in `tools/figures/smoke.mjs`), and a copy of
    `dist/` and `docs/assets/figures/` in `astro:build:done`.
  - `figures.css` resolves `var(--md-…, var(--sl-color-…))`, so one file serves both generators.
    The loader also listens for `astro:page-load`.
- **README.** `node tools/figures/build.mjs portable --write README.md` refreshes
  `<!-- figure:<slug> -->` blocks with repository-relative SVG paths.

### 7. Adopter authoring

1. Run `praetorctl adopt` with the `docs:seo-portal` facet, which the default facet set includes.
2. Write `docs/figures/<slug>.ts`: `import type { PraetorFigure } from '../../tools/figures/types.ts'`
   and a default export with `title`, `alt` (at most 125 characters), `evidence` (`path:Symbol`
   anchors in the adopter's own code, checked as a language-independent substring, as
   `evidence_errors` in `scripts/docs_diagrams.py` does today), optional `describe`, and `props`.
   The limits are those of `LIMITS` in `tools/figures/build.mjs`, which moves to `core.mjs`.
3. Run `node tools/figures/build.mjs build` (Node 22.18 or later, no npm install). Commit the spec
   and its three outputs.
4. Reference the figure with a ` ```figure ` fence naming the slug.
5. Run `make docs-figures`, or on a machine without make run `node tools/figures/build.mjs check`
   and `node tools/figures/build.mjs sources`.

Type-checking a spec is optional and happens in the editor; `validate` in `core.mjs` is the
authoritative check (the header comment of `tools/figures/types.ts`).

### 8. Praetor is the first adopter

Praetor declares `docs:seo-portal` (`facets` in `.standards.yaml`), so it receives the managed
family like any adopter:

- Praetor's own site loads the committed `dist/` through the same hook, so no workflow bundles for
  a site build:
  - `pages.yml` replaces its "Build Figure Player Bundle" step, and the comment above it that calls
    the bundle gitignored, with `bundle.mjs --check`, and its "Verify Diagrams Render" step runs
    the Node `site` and `sources` commands;
  - the "Documentation Integrity Audit" step of `.github/workflows/ci.yml` drops its
    `npm --prefix tools/figures run bundle` line, runs `make docs-figures` after
    `make docs-figures-check`, because a documentation-only pull request skips verify-all, and
    runs the Node `site` command;
  - `.gitignore` drops the `/docs/assets/javascripts/figures/` entry and its comment.
- The "Figure Build Check" step of `.github/workflows/portability.yml` runs the commands of both
  targets inline, since make is not on the Windows image (the comment above that step), and so
  certifies the exact bytes adopters receive. Its Python `sources` line becomes the Node command.
- Praetor's own `docs-figures-check` target (`Makefile`) keeps only the praetor-only steps (npm
  install, tests, type check, `bundle.mjs --check`) and leaves `build.mjs check` and `sources` to
  the managed `docs-figures` target, so each command runs once in `verify-all`. The
  `docs-diagrams-test` target runs the hook's Python tests, `test_mkdocs_hook.py`; the checker's
  tests move into `npm --prefix tools/figures test`.
- `scripts/sync_github_wiki.sh` calls `node tools/figures/build.mjs portable --wiki` in place of
  `docs_diagrams.py portable --wiki`.

### 9. Presets

54812c7d retired Mermaid on the root site and moved the generated Home page to the
`governance-lifecycle` figure. The MkDocs preset is the last Mermaid surface praetor keeps, and it
moves to figures in one change.

- The MkDocs preset drops its Mermaid fence, lists the hook, excludes `/figures/`, and replaces the
  Mermaid example with a neutral example figure whose evidence points into preset files. The
  praetor name in the example page's front matter and heading
  (`docs/presets/mkdocs/docs/index.md`) gives way to neutral text, because an adopter's site
  carries its own identity.
- The checks and texts that name the preset's Mermaid move with it, in the same change:
  - **BUG-680 guard.** `TestHomeWiki_Positive_CompileContextFlowsFromAGENTS`
    (`internal/forge/wiki_test.go`) runs `compileContextFlowViolations` over two inputs: the
    `governance-lifecycle` JSON edges (`figureEdges`), which the generated Home page draws since
    54812c7d, and the preset's Mermaid (`flowEdges`). The preset's example figure draws the
    preset's own files, not the compile-context flow, so the test drops its preset read and keeps
    the JSON edges. Retargeting it would fail: `compileContextFlowViolations` reports two
    violations for an edge list without the flow (`TestHomeWiki_Boundary_EmptyEdgesProducesViolations`).
  - **Mermaid parser.** With the preset read gone, `flowEdges` and its two patterns,
    `mermaidNode` and `mermaidEdge`, are removed in the same change. Their only other caller,
    `TestHomeWiki_Negative_RejectsManifestToAGENTSFlow`, replays the diagram Home.md shipped before
    the BUG-680 fix; it is restated as that diagram's three edges, with the same three expected
    violations. `TestHomeWiki_Negative_FigureEdgesWithCompileContextToAGENTS` already covers the
    single inverted edge.
  - **Preset fence pins.** `test_preset_declares_the_mermaid_fence_and_the_site_does_not` and
    `test_repository_site_enables_only_figures_and_preset_only_mermaid`
    (`scripts/test_docs_diagrams.py`) require the preset to declare the mermaid fence and to enable
    `mermaid` only. Both flip to require `figure` only for the preset, keep their root-site
    assertions, and move with the checker into `tools/figures/checks.test.mjs` (section 1).
  - **Preset README.** Its "Mermaid Diagrams" section (`docs/presets/mkdocs/README.md`) describes
    the hook and the adopted engine instead of the `mermaid` custom fence. Its closing paragraph,
    added by 54812c7d ("The preset stays on Mermaid" … "a separate decision"), contradicts this
    record and is replaced: the preset needs the figure family that `praetorctl adopt` writes under
    `docs:seo-portal`, and its site build still needs Python only.
  - **Other texts.** The statements that the preset keeps Mermaid change in the same pull request:
    the "Mermaid: the adopter preset" item and the `make docs-diagrams-test` paragraph ("Mermaid
    only in the preset") in `docs/guides/documentation-governance.md`, the `exclude_docs` comment
    in the root `mkdocs.yml`, the comments in the "Documentation Integrity Audit" and "Build MkDocs
    Preset From Its Hashed Lock" steps of `.github/workflows/ci.yml`, the module docstring of
    `scripts/docs_diagrams.py` (or of its Node successor), and the comment above the BUG-680
    positive test.
- The Starlight preset adds the integration, the CSS and an example figure.
- The `docs-presets` CI job builds an adopter fixture instead of the preset in place: a temporary
  repository, `praetorctl adopt` with `docs:seo-portal`, `build`, `check`, the MkDocs and Astro
  builds, the Node `site` check, and the Chromium smoke test. Its "Build MkDocs Preset From Its
  Hashed Lock" step stops checking the preset's Mermaid fence mode.

### 10. Wiki (later, optional)

`forge sync-wiki` renders figure fences by substituting the JSON `html`. The SVGs it references are
written next to the wiki pages from copies embedded in the binary, so an adopter's wiki shows the
figures that match the praetorctl version that wrote it, with no runtime dependency on praetor's
Pages deploy. This lands only after the smoke test is green and the devcontainer bootstrap
headroom test (`TestRepositoryBootstrapSourceKeepsHeadroom` in
`internal/devcontainer/bootstrap_source_test.go`) still passes.

### 11. License and neutrality

- `upstream/LICENSE` ships verbatim in every copy; the SVG credit comment (written by `decorate()`)
  and the bundle banner (`BANNER`) are kept.
- `THIRD-PARTY-NOTICES.md` follows the binary, which now carries interfig source and the React
  bundle. `NoticeSources` (`internal/supplychain/notices_sources.go`) gains the `tools/figures`
  lock, from which the committed player is built, and `vendor.json` for interfig, so
  `praetorctl sbom notices` lists React, react-dom, scheduler and interfig. The paragraph saying
  that only the documentation site ships the interfig engine is rewritten.
  `internal/supplychain/notices_test.go` holds the file to the new sources as it does today.
- `REUSE.toml` moves the interfig override to the new path, after the `**` table, and adds a
  `tools/figures/dist/**` annotation (EUPL-1.2 AND MIT). `praetorctl audit` warns when an adopter's
  `REUSE.toml` has no override for the vendored MIT files.
- Text in managed files is neutral and carries no operator data (ADR-0014 §1 "Layering rule"). The
  header of `build.mjs`, which says it builds praetor's documentation figures, is rewritten to
  address the repository it runs in. `package.json` and the tests name praetor, but they are
  praetor-only and never emitted (section 2).

### Amendments to ADR-0015

Each passage is cited by its ADR-0015 section heading and a phrase that occurs in the amended
ADR-0015, and carries "Amended by ADR-0016" (or "added by", for a new command) there.

| ADR-0015 passage | Amended to |
| :-- | :-- |
| Status: "while still Proposed" | Notes the amendment, that the paths and file names predate the move to one tree, and the checker port (section 1). |
| Decision, opening paragraph: "Bundle it with esbuild" | The player is bundled once and committed; no docs build bundles (section 3, operator decision 2). |
| 2. Build tool: "The engine hash covers" | Covers the vendored render files and `core.mjs` (section 1). |
| 2. Build tool, Bundle: "esbuild builds ES modules with splitting", "gave each spec its own chunk", "Size budget" | The generic player is committed under `tools/figures/dist/` as `loader.js` and `player.js`, without spec chunks, hashed chunks or registry; `bundle.mjs --check` holds the budget (sections 2 and 3). |
| 2. Build tool: "The JS bundle is committed" | The bundle is committed and reproducibility-checked (operator decision 2). |
| 2. Build tool: "verifies them by hash without Node" | The hash check runs in Node, with no npm package (section 1, operator decision 5). |
| 4. Mounting on the site, Config: "`mkdocs.yml` adds" | The hook adds the loader and CSS itself (section 6). |
| 4. Mounting on the site, Hook: "reuses the checker's fence scanner" | The hook imports no checker; it keeps its own fence scanner, held equal to the Node one by shared fixtures (section 6, operator decision 5). |
| 4. Mounting on the site, `loader.ts`: "imports the player and renders" | The loader reads props from the SVG metadata (section 3). |
| 5. Outside the site: README and wiki: "One renderer." | One markup source in `core.mjs`; callers substitute; `portable` is a Node command (sections 1 and 4). |
| 5. Outside the site: README and wiki, Adopters: "praetor-engine pages" | The `portable` claim did not hold (`figureFence` writes a raw fence); replaced by section 10. |
| 7. Checks: "One checker, per HISS-19" | The `site`, `sources` and `portable` commands move into the Node engine; Python keeps the hook (section 1, operator decision 5). |
| 7. Checks, `site`: "lists every slug" | The site check looks for the SVG metadata (section 3). |
| 7. Checks, `sources`: "needs no site and no Node" | It needs Node, but no npm package (section 1, operator decision 5). |
| 7. Checks, `sources`: "no longer matches its spec" | `core.mjs` replaces `build.mjs` among the hashed engine files (section 1). |
| 7. Checks, Wiring: "`make verify-all` gains" | `docs-figures-check` keeps the praetor-only steps; `build.mjs check` and `sources` move to the managed `docs-figures` target (sections 5 and 8). |
| 7. Checks, Wiring, `pages.yml`: "steps: setup-node 24" | `bundle.mjs --check` replaces `bundle` (section 8). |
| 7. Checks, Wiring, `ci.yml`: "the Documentation Integrity Audit bundles before" | No bundle step; the audit runs `docs-figures-check` and `docs-figures` (section 8). |
| 7. Checks, Wiring: "on all three operating systems" | Also runs the `docs-figures` commands (section 8). |
| 8. Sync automation: "rebuilds figures, because the engine hash changed" | Also rebuilds the committed player (section 3). |
| 9. Migration: "14 sites become 10 specs", the table row "neutral example figure", and "After the migration" | The preset uses figures (section 9). |
| 9. Migration: "BUG-680 guard" | The guard drops its preset input and keeps the `governance-lifecycle` JSON edges (section 9). |
| License and credit obligations, item 3: "Append to `REUSE.toml`" | `tools/figures/dist/**` is annotated EUPL-1.2 AND MIT (section 11). |
| License and credit obligations, item 7: "Any copy handed to adopters carries the LICENSE" | Copies reach adopters through adoption, with the LICENSE (section 11). |
| Negative / Trade-offs: "The docs build gains Node" | The MkDocs build needs Python only; Node stays in the checks (operator decision 3). |
| Neutral: "left to a separate decision" | Decided by this record (sections 5 and 9). |
| Verification & Compliance, command block: "make docs-figures-check", "make docs-diagrams-test", "npm --prefix tools/figures run bundle", "scripts/docs_diagrams.py site" | `docs-figures-check` runs the praetor-only steps, `docs-figures` is added, `docs-diagrams-test` tests the hook, the bundle is committed, and `site` is a Node command (sections 1, 3 and 8). |
| Verification & Compliance, CI list: "the CI Documentation Integrity Audit runs" | It also runs `make docs-figures` (section 8). |

## Alternatives considered

| Option | Why not |
| :-- | :-- |
| A separate `docs:figures` facet (the draft of this record), with its own workflow and status context | Lets a repository keep the Markdown gate without the figure engine, but adds one more facet that each of the three default facet lists must carry, a second emitted workflow and a second required status context for one documentation concern. Rejected by operator decision 1. |
| A separate `praetor-figures.yml` workflow within `docs:seo-portal` | Keeps the facet single but adds a second emitted workflow and a second required status context, which branch-protection reconciliation would have to learn. Rejected by operator decision 4. |
| Keep the figure checks in Python (`docs_diagrams.py` shipped beside the Node renderer) | No port, but every adopter's verify-all and documentation workflow would need Python 3, a Starlight adopter included, and the checker and the renderer would stay in two languages. Rejected by operator decision 5. |
| Managed engine where adopters bundle the player themselves (npm install of esbuild and React in every adopter) | Keeps ADR-0015's no-committed-bundle rule, but every adopter carries the full toolchain (`node_modules` of about 70 MB, or about 19.7 MB for esbuild plus React alone) and an emitted lockfile that dependency bots can push out of step with the audit, and `check` needs esbuild in adopter CI. Rejected by operator decision 2. |
| Engine inside praetorctl: the esbuild Go API plus the goja JavaScript VM render specs in-process, with no Node for adopters | A prototype produced byte-identical output, but the stripped praetorctl grew from 19.2 MB to 29.9 MB (+55.7%, measured at 7e7746a3), and the root module would go from one external dependency to eight, against ADR-0008 and ADR-0009 (Constraints). goja publishes no tagged releases, and the checker and renderer would be ported to Go. Rejected by operator decision 3. |
| Signed figures kit: an npm-pack tarball published as a release asset, pinned by sha512 in an emitted lockfile and in `.standards.lock` | Adopters install about 0.4 MB, but the design depends on a release pipeline that has never published, needs `allow-remote=root` against npm 12's default, adds a second trust chain and two pins of one artifact, and makes praetor's own site build from source while adopters use the kit: two paths held equal only by a test. |
| Adopters link to SVGs on praetor's Pages site | Works only for praetor's own figures, depends on praetor's deploy, and shows the latest main rather than the adopter's version. |
| Static SVG only for adopters | No JavaScript and no player, but drops the requested tabs, pause and narration. Remains the fallback when JavaScript or the bundle is missing. |

## Consequences

### Positive

- Adopters get figures of their own code with no npm install: Node 22.18 or later for authoring and
  every figure check. A Starlight adopter needs no Python for figures; an MkDocs site build needs
  Python only, as MkDocs does anyway.
- One engine path: praetor and adopters run the same bytes, and the three-platform leg that already
  proves praetor's figures proves the adopters' engine.
- No new facet, workflow or required status context (operator decisions 1 and 4): repositories on
  `docs:seo-portal` receive figures on their next `praetorctl adopt`.
- No new delivery code: the family is one registry entry, and adoption, audit and the devcontainer
  bootstrap follow it.
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
- The player bundle, about 241 kB of minified code, is committed in praetor and rewritten in each
  adopter on `praetorctl adopt --force` after a React, react-dom, interfig or esbuild bump. The
  reproducibility check stands in for reading the minified code.
- After a praetorctl upgrade that adds or changes managed figure files, `praetorctl audit` fails in
  an existing `docs:seo-portal` repository until `praetorctl adopt --force` runs, the remedy
  `auditExactManagedFile` already prints.
- praetorctl grows by about 0.35 MB of embedded files, about 1.8% of the 19.4 MB stripped binary
  (`go build -trimpath -ldflags='-s -w' ./cmd/standardsctl` at 54812c7d; the growth is an estimate
  from the file sizes). `TestRepositoryBootstrapSourceKeepsHeadroom` reports 37% of the archive
  frames and 52% of the uncompressed source bytes at 54812c7d; the family raises the uncompressed
  share to an estimated 57%, and the test decides.
- An interfig or core change still marks every adopter figure stale, by design; the adopter runs
  `praetorctl adopt --force`, `build`, and commits.
- Two generator adapters exist, a Python hook and an Astro integration, because each generator's
  plugin API has its own language, and fence scanning exists in Python and in Node (section 6).
  Rendering stays single (section 4).
- The checker port rewrites about 760 lines of Python and its fixtures in Node. Until it lands the
  Python checker stays authoritative in praetor, and the figure family does not ship.
- Nothing reaches adopters through a released binary until a `v*` release is published; until then
  adopters pin the `praetor-adopt` action to a commit.
- The emitted gate cannot run the Chromium smoke test, because praetor emits no site build.
  Praetor smoke-tests the identical player on the fixture builds.

### Neutral

- Mermaid leaves the presets. The `site` check keeps rejecting a ` ```mermaid ` fence a site does
  not enable.
- Unverified and unchanged by this record: whether GitHub animates an SVG in a README and honours
  `<source media="(prefers-reduced-motion: reduce)">` (ADR-0015 §5 "Outside the site: README and
  wiki": "Not verified yet"); the SVG palette follows the operating system, not the site toggle
  (same section: "Colours").

## Open questions

None. The draft's three questions (default or opt-in membership, how the prebuilt player reaches
adopters, and Node or Go for authoring) and the two that followed (where the adopter figure check
runs, and whether the checks stay in Python) are decided under "Operator decisions".

## Verification & Compliance

```bash
node --test 'tools/figures/third_party/interfig/upstream/src/*.test.ts'
npm ci --prefix tools/figures --ignore-scripts --no-audit --no-fund
npm --prefix tools/figures test                 # core, checks and the fence fixtures
python3 -B tools/figures/test_mkdocs_hook.py    # the hook replays the same fence fixtures
node tools/figures/build.mjs check              # regenerate and byte-compare; no esbuild needed
node tools/figures/build.mjs sources
node tools/figures/bundle.mjs --check           # rebuild dist/ from the lock: bytes, file list, 250 kB budget
python3 -B scripts/sync_interfig.py verify
go test ./tools/figures/... ./internal/managedasset/... ./internal/adopt/... ./cmd/standardsctl/... ./internal/devcontainer/... ./internal/supplychain/...
mkdocs build --strict -d site
node tools/figures/build.mjs site --config mkdocs.yml --docs docs --site site
npm --prefix tools/figures run smoke -- --site site --require-browser
reuse lint
make verify-all
```

- `tools/figures/assets_test.go`: the embedded list equals `git ls-files tools/figures` minus the
  praetor-only files, and `dist/` holds exactly the three listed files.
- `internal/managedasset` and `internal/adopt` tests: the figure family is registered under
  `docs:seo-portal`; write, refuse a foreign pre-existing file, remove, refuse drift, facet off;
  positive, negative and boundary cases.
- Engine tests: `check` and `sources` pass with a stated reason in a repository without
  `docs/figures/` and with an empty one, and fail on one malformed spec.
- `praetorctl audit` on a temporary repository adopted with `docs:seo-portal`: passes after
  adoption, fails with the `adopt --force` remedy after a managed figure file is edited, and finds
  no figure file after the facet is disabled.
- `docs-presets` CI: the adopter-fixture build (section 9), on Linux, with the smoke test.
- `.github/workflows/portability.yml`: `build.mjs check`, `build.mjs sources` and
  `bundle.mjs --check` on Linux, macOS and Windows.
- Not yet measured: `build.mjs build` and `check` on Node 22.18, the declared floor; the
  implementation adds that leg before the floor is relied on.
