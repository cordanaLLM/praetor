# ADR-0015: Interactive Documentation Figures from Vendored interfig

## Status

Proposed — 2026-09-27.

Amended 2026-09-27 by [ADR-0016](0016-figures-for-adopters.md) while still Proposed
(`docs/adr/README.md`, rule 4). Each changed passage below says "Amended by ADR-0016" ("added
by" for a new command) and names the ADR-0016 section or operator decision; the ADR-0016 table
"Amendments to ADR-0015" lists them all. ADR-0016 §1 also moves the engine files into one
`tools/figures/` tree and the pure functions of `build.mjs` into `core.mjs`. The paths and file
names in this record are those before that move.

Number note: ADR-0013 is the container image and Helm chart record and ADR-0014 the
operator-neutral defaults record, so this record takes 0015.

## Context

The operator asked for the documentation site's Mermaid diagrams to be replaced by interactive,
animated architecture figures like the ones on the Hindsight documentation site
(<https://hindsight.vectorize.io/>). That means dark nested group cards, database-cylinder nodes,
labelled edges with moving packets, and scenario tabs with play, pause, speed and narration.

### What praetor has today

- **14 Mermaid diagrams**: 13 fences under `docs/` and one in `README.md`
  (`git grep -n -E '^\s*(```|~~~)mermaid' -- docs README.md`).
  - `internal/forge/wiki.go` generates five of the `docs/wiki/` pages: Home, HISS-Invariants,
    HISS-Matrix, Architecture-Lattice and API-Reference.
    `TestCheckedInWiki_Boundary_MatchesGenerator` (`internal/forge/wiki_test.go`) holds the
    checked-in copies byte-equal to the generator's output.
  - `internal/forge/forge_test.go` requires a Mermaid fence in every generated page.
  - Agent skill text under `.agents/` that mentions Mermaid is output guidance, not a diagram, and
    is out of scope.
- **Mermaid rendering.** Material for MkDocs draws a `mermaid` fence only because `mkdocs.yml`
  declares the custom fence.
  - `scripts/docs_mermaid.py` fails a build in which a fence ships as a code listing.
  - It runs in `make docs-mermaid-test`, in `pages.yml` and in the CI Documentation Integrity
    Audit.
- **Where the docs render.**
  - The published site is built from `mkdocs.yml` by `.github/workflows/pages.yml` and deployed
    to GitHub Pages.
  - The adopter preset `docs/presets/mkdocs/` has its own `mkdocs.yml`, built in CI.
  - The GitHub wiki (`scripts/sync_github_wiki.sh`) and `README.md` render on GitHub, which runs
    no JavaScript.
- **One diagram is wrong.** `docs/wiki/API-Reference.md` shows GitLab reconciling protections
  and Gitea syncing labels. `internal/forge/forge.go:62-64` says only the GitHub driver enforces:
  the GitLab and Gitea drivers authenticate, then return `ErrNotImplemented` for every
  enforcement method.

### The upstream engine

The Hindsight figures come from interfig (`@vectorize-io/interfig`). Its source is under
`hindsight-interfig/` in <https://github.com/vectorize-io/hindsight>. At commit
`ccfe85b4851957ac2adf88b4a9ddf9668b2882f1` (main, 2026-09-26):

- **License.** The repository-root `LICENSE` is MIT, "Copyright (c) 2025 Vectorize AI, Inc.".
  - `hindsight-interfig/` has no LICENSE or NOTICE of its own, no SPDX headers, and no `license`
    field in `package.json`, so the root license covers it.
  - It ships no third-party assets: fonts are system stacks and icons are inline path data.
- **Packaging.** `package.json` is private at version 0.1.0 and describes the package as used
  from source and never published. The README adds that it is never built.
  - The peer dependency is `react >=18`. There are no runtime dependencies.
  - `exports` points at the raw `src/index.tsx`.
- **Model.** A figure is a declarative object:
  - a `layout` tree of groups and boxes (the `store` shape draws a cylinder, `decision` a
    diamond);
  - `edges`;
  - `steps`, each made of beats: edge hops, `say` narration, `show` content cards, `light`.
- **Player.** `Flow` in `src/index.tsx` is a React component.
  - It has tabs with a progress line, pause, 1×/2× speed, and a full-screen view that Esc
    closes.
  - It honours `prefers-reduced-motion`: no packets, no autoplay, and the active step shows its
    last beat.
  - Gaps:
    - box highlighting is mouse-only;
    - the tabs do not respond to arrow keys;
    - there is no `aria-controls` or tab panel;
    - the narration has no `aria-live`;
    - edges and packets are invisible to screen readers.
- **SVG exporter.** `toSvg` in `src/svg.ts` needs no React.
  - Animation is CSS `@keyframes` plus SMIL `<animateMotion>`.
  - Output is deterministic: no clock, no randomness.
  - The dark palette follows `prefers-color-scheme`. A custom theme comes only from
    `opts.theme`.
  - The exported SVG carries no `<title>`/`<desc>` and no reduced-motion rule.
  - Class names are unscoped (`.label`, `.row`, a bare `svg {}` rule), so an exported SVG must be
    embedded as `<img>` and never inlined into a page.
  - The 8 upstream figures export at 53.9–178.9 kB.
  - `scripts/figure-svg.mjs` wraps `toSvg` and stores the spec in `<metadata id="figure-spec">`.
    It also calls `module.register`, which Node 26 reports as deprecated (DEP0205).
- **Upstream's own docs.** Vectorize replaced the Mermaid diagrams in its own documentation with
  interfig (vectorize-io/hindsight#4621).
- **Maturity.** The first commit is from 2026-09-22: 5 commits, one author, no tags. The last
  change under `hindsight-interfig/` at the pin is `db03d5448ff1290cc6e501aa7f62d5e4a1e979a3`
  (2026-09-24).
- **Bundle size** (esbuild, measured):
  - interfig alone, React external: 15.5 kB minified, 6.4 kB gzip;
  - with React 19.3 and the react-dom client: 234.1 kB minified, 75.1 kB gzip.

### Invariants in play

- HISS-19: an engine that already does this exists, so reuse it.
- HISS-21: every build step must run on Linux, macOS and Windows.
- HISS-02: new scripts use bounded loops and I/O timeouts.
- HISS-10: no warnings.
- HISS-15 and HISS-20: positive, negative and boundary tests, with fixtures replayed both ways.
- REUSE compliance: `REUSE.toml`, `LICENSES/`, and the CI REUSE gate.

## Decision

Vendor interfig at a pinned commit and never modify it. Bundle it with esbuild during the docs
build. Pages reference typed figure specs through a ` ```figure ` fence. Animated and static SVG
fallbacks are committed. One extended checker verifies all of it.

Amended by ADR-0016 §3 (its operator decision 2): esbuild bundles the player once, in praetor,
into the committed `tools/figures/dist/`. No docs build bundles.

### 1. Vendoring

```text
third_party/interfig/
  VENDOR.md      praetor-owned: source, pin, license, credit, include list, local adaptations, sync steps
  vendor.json    praetor-owned: {repo, path, commit, path_commit, fetched, license_sha256,
                 include, exclude, files: {path: sha256}}
  upstream/      byte-identical subset of hindsight-interfig/ at the pin
    LICENSE      verbatim repository-root LICENSE at the pin
    README.md  package.json
    src/         index.tsx model.ts geometry.ts svg.ts node-figures.ts *.test.ts
    scripts/     figure-svg.mjs figure-loader.mjs
```

- **Excluded:**
  - `demo/`;
  - `figures/`, which is Hindsight product content;
  - `scripts/export.mjs`, which needs Playwright and ffmpeg;
  - the Vite, tsconfig and Prettier files, and `index.html`.
- **Why `third_party/`:**
  - The HISS scanner already skips that path segment (`internal/hiss/hiss.go:397`).
  - A root `vendor/` would switch Go into `-mod=vendor`.
  - The dedupe scan reads Go only.
- **No patches.** Files under `upstream/` are never edited. Praetor's adaptations live outside
  that directory and are listed in `VENDOR.md`:
  - SVG title and description injection;
  - the static variant;
  - the keyboard shim;
  - the theme CSS.

  Each adaptation is offered upstream (section 6), and a later sync that brings in the upstream
  fix retires the local shim.

### 2. Build tool

`tools/figures/` is praetor-owned, laid out like `tools/markdownlint/`:

- **`package.json`** is private, with `"type": "module"` and `engines.node >=22.18`.
  - devDependencies are pinned exactly: `esbuild 0.28.2`, `react 19.3.0`, `react-dom 19.3.0`,
    `typescript 7.0.2`, `@types/react`, `@types/react-dom`, and `playwright` (smoke test only).
  - A `package-lock.json` is committed, and installs use `npm ci --ignore-scripts`.
- **Why esbuild:**
  - Upstream has no library build.
  - esbuild is a single native binary, published as npm optional dependencies for Linux, macOS
    and Windows (HISS-21).
  - It compiles TSX (`jsx: automatic`) and splits ES modules without a plugin chain.
- **`build.mjs`** imports `toSvg` from `third_party/interfig/upstream/src/svg.ts` directly. Plain
  Node strips the types.
  - It does not call `figure-svg.mjs`, which triggers DEP0205.
  - It embeds the spec with the same `<metadata id="figure-spec">` markers, so upstream's
    `--spec` reader still works.
  - For each spec it writes, under `docs/assets/figures/`:
    - `<slug>.svg`: animated; `role="img"`, `aria-labelledby`, `<title>`, `<desc>` and a credit
      comment are inserted after the opening `<svg>` tag;
    - `<slug>.static.svg`: the same props with `steps: []`, for reduced motion;
    - `<slug>.json`: title, alt, the derived text description, evidence, `spec_sha256`,
      `engine {commit, sha256}`, `svg_sha256`, `static_sha256`, width, height, and normalized
      edges.
  - The engine hash covers `svg.ts`, `geometry.ts`, `model.ts` and `build.mjs`. Amended by
    ADR-0016 §1: it covers the three vendored render files and `tools/figures/core.mjs`, so an
    edit to the command-line wrapper no longer marks every figure stale.
  - In normalized edges, `{from, to, label}` carry box labels; an edge to a group expands to each
    box inside it.
  - There is one JSON file per slug and no shared manifest, so figure pull requests never
    collide.
- **Bundle.** esbuild builds ES modules with splitting. Amended by ADR-0016 §3:
  `tools/figures/bundle.mjs` writes the generic player to `tools/figures/dist/`, which is
  committed, rebuilt byte for byte by `bundle.mjs --check` on Linux, macOS and Windows, and
  emitted to adopters. The first version wrote `docs/assets/javascripts/figures/`, gitignored and
  rebuilt in CI.
  - `loader.js` is small and loaded on every page.
  - The player chunk holds React and interfig and is imported only when a figure nears the
    viewport.
  - Amended by ADR-0016 §3: there are no spec chunks and no `registry.json`. The loader reads
    each figure's props from the `<metadata id="figure-spec">` of the SVG it replaces. The first
    version gave each spec its own chunk, listed in a generated `registry.json`.
  - `nodePaths: ["tools/figures/node_modules"]` is required, because React cannot be resolved
    from `third_party/` otherwise.
  - A `--banner:js` adds the interfig MIT notice. React's `@license` comments are kept as legal
    comments at the end of the file.
  - Size budget: at most 250 kB minified for the player chunk. Amended by ADR-0016 §3:
    `bundle.mjs --check` enforces it; `build.mjs check` no longer bundles and needs no esbuild.
- **Why the SVGs and JSON are committed:**
  - README and wiki need stable URLs.
  - The docs-only CI path verifies them by hash without Node.
  - A `mkdocs serve` without Node still shows every figure.
  - The output does not depend on npm versions, because `toSvg` has no dependencies.
- **The JS bundle is committed.** Amended by ADR-0016 §3 (its operator decision 2). The first
  version kept the bundle out of the tree so that a dependency bump would not require committing
  regenerated minified code. Adopters now receive the player byte for byte from praetorctl, so
  the bytes live in the tree; `bundle.mjs --check` rebuilds them from the pinned lock and
  compares, which stands in for reading the minified code.

### 3. Authoring

A page names a figure by slug:

````markdown
```figure
gating-pipeline
```
````

The spec lives in `docs/figures/<slug>.ts`:

```ts
import type { PraetorFigure } from '../../tools/figures/types.ts';
export default {
  title: 'Gated pipeline',
  alt: 'Six gate stages run in order; any failure rejects the change before a receipt is signed.',
  evidence: ['internal/gating/pipeline.go:executeStages'],
  props: { layout: { children: [] }, edges: [], steps: [] },
} satisfies PraetorFigure;
```

- Node's type stripping erases the type import and `satisfies`, so no loader is needed.
- Specs are `.ts` only: no JSX, no React-node content, no `MiniGraph`.
- **Validation** (in `build.mjs` and `npm run check`):
  - ids are unique;
  - every edge `from`/`to` is a box or group id, and every beat edge exists;
  - every `show` key is a box id;
  - every label, `sub`, `say`, `caption` and `data` value is a string, so the player and the SVG
    show the same content;
  - `alt` is at most 125 characters and `evidence` is not empty;
  - at most 40 boxes and 12 steps (HISS-02 bounds).
- **Content comes from code.** Each spec is written from the code at its evidence anchors, not
  from the Mermaid diagram it replaces. Figures that show the same flow share one spec.
- **Branding.** Figures use no Hindsight names or branding.

### 4. Mounting on the site

- **Config.** `mkdocs.yml` adds:
  - `exclude_docs: /figures/`;
  - `hooks: [scripts/mkdocs_figures_hook.py]`;
  - `extra_javascript` with `assets/javascripts/figures/loader.js` as `type: module`;
  - `extra_css: [stylesheets/figures.css]`;
  - a credit line in `copyright`.

  Amended by ADR-0016 §6: the hook adds the loader (as a module) and `figures.css` in
  `on_config` and serves the committed player files in `on_files`, so a site's `mkdocs.yml` lists
  only the hook and the `exclude_docs` entry, and nothing is copied into `docs_dir`.
- **Hook.** The hook is thin. In `on_page_markdown` it calls
  `docs_diagrams.expand(markdown, page.url)`, which reuses the checker's fence scanner, so a fence
  nested in a longer fence is left alone. An unknown slug logs a warning, which fails
  `mkdocs build --strict`.
- **Markup.** Each fence becomes:

  ```html
  <figure class="praetor-figure" id="fig-SLUG" data-figure="SLUG" aria-describedby="fig-SLUG-text">
    <picture>
      <source media="(prefers-reduced-motion: reduce)" srcset="REL/assets/figures/SLUG.static.svg">
      <img src="REL/assets/figures/SLUG.svg" alt="ALT" width="W" height="H" loading="lazy">
    </picture>
    <figcaption>TITLE</figcaption>
  </figure>
  <details class="praetor-figure__text" id="fig-SLUG-text"><summary>Text description</summary>…</details>
  ```

  `REL` is derived from the depth of `page.url` (`use_directory_urls: true`).
- **`loader.ts`:**
  - It subscribes to Material's `document$` observable, because `navigation.instant` swaps page
    content without a reload. Pages outside Material fall back to `DOMContentLoaded`.
  - On each emission it unmounts React roots whose host has left the DOM and closes full screen.
  - It watches each `figure.praetor-figure[data-figure]` with an `IntersectionObserver`, at most
    32 per page. When one nears the viewport it imports the player and renders `Flow` with
    `autoplay` off under reduced motion.
  - Amended by ADR-0016 §3: the props come from the figure's own SVG. The loader fetches the
    `<img>`'s `currentSrc` with a byte cap and the 10 s timeout, parses
    `<metadata id="figure-spec">`, and takes the title from the `<figcaption>`.
  - The player replaces the `<picture>`. The caption and text description stay.
- **Theme.** `docs/stylesheets/figures.css` maps each `--fig-*` variable to a Material variable
  (confirm the names against the installed mkdocs-material 9.7.7 CSS):

  | interfig | Material |
  | :-- | :-- |
  | `--fig-accent` | `--md-accent-fg-color` |
  | `--fig-fg` | `--md-default-fg-color` |
  | `--fig-muted` | `--md-default-fg-color--light` |
  | `--fig-border` | `--md-default-fg-color--lightest` |
  | `--fig-bg` | `--md-default-bg-color` |
  | `--fig-surface` | `--md-code-bg-color` |
  | `--fig-font` | `--md-text-font-family` |

  The palette toggle then carries through without JavaScript, and the default slate scheme gives
  the dark cards. Tone colours, chip text and shadows stay hard-coded upstream.

### 5. Outside the site: README and wiki

- **No JavaScript.** Without JavaScript or the bundle, the `<picture>` stays: the animated SVG,
  or the static one under reduced motion. The caption and text description stay visible.
- **One renderer.** Amended by ADR-0016 §4: `tools/figures/core.mjs` renders the
  `<figure><picture>` block and the `<details>` text description into each `<slug>.json` as
  `html`, with `{{base}}` and `{{link}}` slots, and every caller only substitutes the slots. The
  first version rendered in Python, in `docs_diagrams.render_block(slug, base, link)`. It serves
  three callers:
  - the MkDocs hook, with a relative base;
  - the wiki: `scripts/sync_github_wiki.sh` runs
    `python3 -B scripts/docs_diagrams.py portable --base https://cordanallm.github.io/praetor/`
    on the cloned pages after copying them.
    - Fences become `<figure><picture>` blocks with absolute GitHub Pages URLs, which are served
      as `image/svg+xml`.
    - A link to the interactive version, `…/wiki/<Page>/#fig-<slug>`, follows each figure.
  - `README.md`: a portable block between `<!-- figure:<slug> -->` and `<!-- /figure -->`, with
    repository-relative paths so pull-request previews show the new SVG.
    - `docs_diagrams.py portable --write README.md` refreshes it.
    - The checker compares it.
- **GitHub markup.** GitHub keeps `figure`, `figcaption`, `picture` and `img`, and strips `class`
  and `data-*`.
  - Not verified yet: whether GitHub honours `prefers-reduced-motion` on `<source>`, and whether
    it animates an SVG in a README. The fallback is readable either way.
- **Adopters.** `praetorctl forge sync-wiki` writes the same praetor-engine pages in any
  repository. Amended by ADR-0016 §10: the first version said their fences resolve through
  `portable` against praetor's published SVGs, but `internal/forge/wiki.go` writes a raw
  ` ```figure ` fence and no Go code renders one. ADR-0016 §10 substitutes the JSON `html` and
  writes the referenced SVGs from copies embedded in the binary.
- **Colours.** The exported SVGs keep upstream's default palette, because `toSvg` takes its theme
  only from `opts.theme` and its dark block follows the operating system, not the site toggle.
  This is a known gap, offered upstream (6c).

### 6. Accessibility

- **Text alternatives:**
  - an authored one-sentence `alt`;
  - `title` as the `<figcaption>`;
  - a long description derived from the spec by `build.mjs`, so it cannot drift.
    - It lists groups with their boxes, edges as sentences ("A → B (label)"), then each step
      with its label, caption and ordered narration, plus the optional `describe[]`.
    - It is capped at 2,500 characters, rendered in `<details>`, linked by `aria-describedby`,
      and indexed by site search. The pilot's description alone runs to about 1,900
      characters, so a 1,500 cap would have cut two of its four scenarios. A longer text keeps
      whole lines and ends with a note naming the spec file.
- **Reduced motion:**
  - the player follows upstream behaviour;
  - the loader passes `autoplay: false`;
  - the fallback uses the static `<source>`, because SMIL `<animateMotion>` ignores CSS
    animation rules.
- **Motion control** (WCAG 2.2.2): upstream's pause button. Figures mount only near the viewport,
  so nothing animates offscreen.
- **Keyboard:**
  - Tabs and buttons are native and focusable, and Esc closes full screen.
  - Praetor's `tools/figures/keyboard.ts` adds Left, Right, Home and End roving across
    `[role=tab]` and an `aria-label` on the tab list.
  - `figures.css` adds a `:focus-visible` outline.
- **Upstream offers** (issues or pull requests to vectorize-io/hindsight; drop the local shim once
  a sync brings the fix in):
  - a) roving tabindex, `aria-controls` with tab panels, keyboard parity for box highlighting,
    and `aria-live` narration;
  - b) SVG `<title>`/`<desc>`/`role="img"` and a reduced-motion rule for the keyframes;
  - c) `toSvg` honouring `props.theme`, or a `--theme` flag on the CLI, plus an exported
    spec-embedding helper.

### 7. Checks

One checker, per HISS-19:

- **Rename.** `git mv scripts/docs_mermaid.py scripts/docs_diagrams.py`, and its test, and
  `make docs-mermaid-test` becomes `make docs-diagrams-test`.
- **Fence kinds.** The fence scanner becomes `fences(text, info)`. The kinds a build accepts are
  derived from its config:
  - the declared mermaid fence enables `mermaid`;
  - the listed hook enables `figure`;
  - a fence of a kind the config does not enable is an error.
- **`docs_diagrams.py site --config --docs --site`** runs after `mkdocs build`:
  - Mermaid: the existing check.
  - Figures, per page:
    - the fence count equals the number of `figure.praetor-figure[data-figure]` elements;
    - every `img`/`source` URL resolves to a file under `site/`;
    - the page loads `loader.js`;
    - every figure's SVG carries the `<metadata id="figure-spec">` the loader reads. Amended by
      ADR-0016 §3; the first version checked that `registry.json` lists every slug.
- **`docs_diagrams.py sources`** needs no site and no Node. It runs in verify-all and in the
  docs-only CI audit, and fails on:
  - a JSON hash that no longer matches its spec, the vendored engine files, `build.mjs` or the
    SVGs (reported as stale, with the rebuild command). Amended by ADR-0016 §1: `core.mjs`
    replaces `build.mjs` among the hashed engine files;
  - a spec without a JSON file, or a JSON file without a spec;
  - a fence slug with no spec;
  - a README portable block that differs from the renderer;
  - an `evidence` path that is missing, or whose symbol does not occur in the file.
- **`npm --prefix tools/figures run check`** validates the specs, regenerates into a temporary
  directory and compares bytes with the committed files. A hand-edited JSON file fails here.
- **`npm --prefix tools/figures run smoke`** uses Playwright Chromium against `site/` served by a
  bounded `node:http` server (module scripts do not load from `file://`). After scrolling, each
  page must have as many `.praetor-figure .interfig` as `figure.praetor-figure`, and no
  `pageerror` or console error. Autoplay must advance the active step of every figure with
  scenario tabs without input, and each such figure must show a packet, under autoplay or after
  its tabs are started. A reduced-motion run shows no packets. Locally the target skips
  with a stated reason when no browser is installed (HISS-21).
- **cifilter.** `internal/cifilter/filter.go` `isCode` gains `.tsx` and `.jsx`. Without them a
  sync touching only `index.tsx` runs no gates.
- **Wiring:**
  - `make verify-all` gains `docs-diagrams-test`, `docs-figures-check` (Node check plus
    `sources`) and `interfig-verify`. Amended by ADR-0016 §8: `docs-figures-check` keeps the
    praetor-only steps (npm install, tests, type check, `bundle.mjs --check`), and
    `build.mjs check` and `sources` move to the managed `docs-figures` target, which verify-all
    also runs.
  - `pages.yml`:
    - paths add `third_party/interfig/**`, `tools/figures/**`, `scripts/docs_diagrams.py` and
      `scripts/mkdocs_figures_hook.py`;
    - steps: setup-node 24, `npm ci`, `check`, `bundle`, `mkdocs build --strict`, `site`,
      `sources`, `smoke`, upload. Amended by ADR-0016 §8: `bundle.mjs --check` replaces
      `bundle`, and the site serves the committed player.
  - `ci.yml`:
    - the lock file joins the npm cache paths;
    - the Documentation Integrity Audit bundles before `mkdocs build` and runs `site` and
      `sources`. Amended by ADR-0016 §8: it no longer bundles; it runs `docs-figures-check` and
      `docs-figures`, and MkDocs serves the committed player.
  - `portability.yml` runs `docs-figures-check` on all three operating systems. Amended by
    ADR-0016 §8: it also runs `docs-figures`, so `build.mjs check`, `bundle.mjs --check` and
    `sources` all run on the three systems.

### 8. Sync automation

`scripts/sync_interfig.py` uses the standard library only, with a timeout on every request and
bounded loops:

- **`verify`** is offline and runs in verify-all:
  - `upstream/**` matches the `vendor.json` hashes;
  - the include and exclude lists cover every file;
  - the LICENSE hash is unchanged;
  - `REUSE.toml` carries the MIT override annotation for `third_party/interfig/upstream/**`.
    Without it the `**` EUPL annotation silently relabels the files and `reuse lint` still
    passes.
- **`check`** is online. It reads the newest commit touching `hindsight-interfig/` on upstream
  `main` (`GET /repos/vectorize-io/hindsight/commits?path=hindsight-interfig&sha=main&per_page=1`)
  and reports drift from `path_commit` with the compare URL and the update command.
- **`update --commit <sha>`:**
  - fetches the include list at that commit, with a size cap;
  - fails on a LICENSE change, an unlisted new file under `src/` or `scripts/`, or a React peer
    range that no longer matches `tools/figures`;
  - rewrites `upstream/` and `vendor.json`;
  - runs upstream's `node --test`;
  - rebuilds figures, because the engine hash changed. Amended by ADR-0016 §3: it also rebuilds
    the committed player with `bundle.mjs`, because the player bundles interfig;
  - prints the upstream commits between the two pins for the pull-request body.
- **Workflow.** `.github/workflows/interfig-sync.yml` runs weekly and on `workflow_dispatch`,
  guarded to the canonical repository, with `permissions: contents: read`.
  - It runs `check` and fails with the commit and the update command on drift.
  - An operator or the agent pipeline then runs `update` into a reviewed, signed pull request.
  - The workflow does not open pull requests itself: `GITHUB_TOKEN` may not, and `main` requires
    signed commits (the `sync-models.yml` pattern).
- **Renovate:**
  - It does not track the interfig commit: `git-refs` has no path filter and upstream `main`
    moves daily for unrelated reasons.
  - It does track `tools/figures/package.json` through the npm manager, with one package rule
    grouping `react`, `react-dom` and `@types/react*`.
  - Renovate has not run on this repository yet (#325).

### 9. Migration

14 sites become 10 specs. 13 sites convert. The first version kept Mermaid in the MkDocs preset;
amended by ADR-0016 §9, the preset moves to figures with a neutral example figure of its own.

| Spec | Sites | Anchors to read first |
| :-- | :-- | :-- |
| `gating-pipeline` (pilot) | `docs/architecture/c4-models.md` L3 | `internal/gating/pipeline.go` (`executeStages`, `runTestStage`, `runReceiptStage`) |
| `c4-system-context` | c4-models L1 (static) | `deploy/`, `internal/runner`, fork notes in c4-models.md |
| `c4-containers` | c4-models L2 | `internal/{compiler,gating,hiss,runner,bump,lockdown}`, `praetorctl serve` |
| `governance-lifecycle` | `README.md`, `docs/wiki/Home.md` | `internal/agentcontext/render.go`, `cmd/standardsctl/compile_context.go`, `cmd/standardsctl/audit.go`, Makefile `verify-all` |
| `lattice-join` | `docs/guides/archetype-authoring.md`, `docs/wiki/Architecture-Lattice.md` | `internal/config/config.go` (`Join`, `ApplyOverrides`), `internal/config/effective.go` (`ResolvePolicy`) |
| `onboarding-path` | `docs/guides/onboarding.md` | `cmd/standardsctl/{init,baseline,compile_context}.go` |
| `hiss-taxonomy` | `docs/standards/hiss-spec.md` (static) | `internal/hiss/rules.go`, the spec's rule table |
| `model-routing` | `docs/standards/model-routing-and-fanout.md` | `cmd/standardsctl/models_route.go`, `internal/router/` |
| `forge-federation` (fact fix) | `docs/wiki/API-Reference.md` | `internal/forge/forge.go:50-64`, `gitlab.go`, `gitea.go` |
| `verification-ladder` | `docs/wiki/HISS-Invariants.md`, `docs/wiki/HISS-Matrix.md` | `lefthook.yml`, `cmd/standards-lsp`, `internal/gating`, `forge validate-pr` |
| neutral example figure (amended by ADR-0016 §9) | `docs/presets/mkdocs/docs/index.md` | preset files only; built as an adopter fixture |

- **Generated pages.** `internal/forge/wiki.go` emits ` ```figure ` fences for the generated
  pages, and `forge_test.go` expects them.
- **BUG-680 guard.** `compileContextFlowViolations` checks an edge list with two inputs: the
  `governance-lifecycle` JSON edges and the preset's Mermaid. Amended by ADR-0016 §9: the
  preset's example figure draws no compile-context flow, so the guard drops its preset input and
  keeps the JSON edges.
- **Order.** Delivery runs in three waves, tracked in the private program plan:
  - the engine and the pilot;
  - page conversions and the sync automation, which touch disjoint files;
  - generated wiki pages, retiring Mermaid on the root site, and moving the preset example out of
    the root navigation (`exclude_docs: /presets/mkdocs/docs/`).
- **After the migration**, a Mermaid fence in the root `docs/` fails the checker. Amended by
  ADR-0016 §9: the preset moves to figures as well. The first version kept Mermaid there because
  the preset's build needs no Node; with the committed SVGs, the committed player and the Python
  hook, it still needs none.

## License and credit obligations

MIT code inside a EUPL-1.2 work keeps its own terms. MIT allows sublicensing as long as the notice
is kept. This follows from the two license texts and is not legal advice.

1. Keep the upstream `LICENSE` verbatim at `third_party/interfig/upstream/LICENSE`. REUSE ignores
   files named LICENSE, so this does not change the lint.
2. Add `LICENSES/MIT.txt` (`reuse download MIT`).
3. Append to `REUSE.toml`, after the `**` table. In REUSE 3.3, only the last matching table
   applies.

   ```toml
   [[annotations]]
   path = ["third_party/interfig/upstream/**"]
   precedence = "override"
   SPDX-FileCopyrightText = "2025 Vectorize AI, Inc."
   SPDX-License-Identifier = "MIT"
   ```

   `VENDOR.md`, `vendor.json`, `tools/figures/`, the specs and the generated SVGs stay EUPL-1.2
   through the `**` table. `sync_interfig.py verify` fails when the override is missing.
   Amended by ADR-0016 §11: `tools/figures/dist/**` is the exception, annotated EUPL-1.2 AND MIT,
   because the committed player bundles interfig and React.
4. Never add EUPL headers to vendored files and never relicense them.
5. The deployed bundle carries both notices: an injected
   `/*! interfig (c) 2025 Vectorize AI, Inc. MIT <upstream URL at the pin> */` banner, because the
   upstream source has none of its own, and React's retained `@license` comments.
6. Credit Vectorize without implying endorsement and without Hindsight branding: a line in the
   site footer (`copyright` in `mkdocs.yml`), `docs/guides/figures.md`, `VENDOR.md` and this
   record. Neither `figures/` nor the demo or export tooling is vendored.
7. Any copy handed to adopters carries the LICENSE. Amended by ADR-0016 §11: copies reach
   adopters through `praetorctl adopt` under the `docs:seo-portal` facet, with `upstream/LICENSE`
   verbatim and `dist/THIRD-PARTY-LICENSES.txt` beside the player. The first version named
   `docs/presets/` as the only channel; presets stay reference directories and are not embedded
   in the binary.

## Alternatives considered

Registry facts were read from npm on 2026-09-26.

| Option | Why not |
| :-- | :-- |
| React Flow (`@xyflow/react` 12.12.0, MIT) | A node-graph editor (drag, zoom, connect). Scenario playback, packets and narration are not part of it, so we would write the playback engine on top of it, plus a layout engine for nested groups. It still brings React. |
| Svelte Flow (`@xyflow/svelte` 1.7.0, MIT) | The same model as React Flow, and it adds a Svelte compiler to a Python docs toolchain that uses no Svelte. |
| elkjs (0.12.0, EPL-2.0 OR GPL-3.0-or-later) | Layout only, with no rendering or animation. It would add a second copyleft license family beside EUPL-1.2, with REUSE entries and a compatibility review. |
| Cytoscape.js (3.34.3) | Graph analysis and visualization on a canvas. It has no step or narration model and no standalone animated SVG export for README and wiki. |
| D3 (7.9.0, ISC) | A low-level toolkit. Layout, edges, playback, accessibility and export would all be ours. |
| A new `<praetor-flow>` web component (the research report's recommendation) | It rewrites a tested MIT engine that already has the requested look (HISS-19), starts with no tests, and cuts praetor off from upstream fixes. |
| Static SVG only (`toSvg` without the player) | Needs no JavaScript and remains the fallback, but drops the requested tabs, pause and narration. |
| Keep Mermaid | Does not meet the request. |
| Preact via `preact/compat` alias | 33.6 kB minified / 13.6 kB gzip instead of 75.1 kB gzip. It compiles, but it is untested at runtime. Deferred until measured, not rejected. |

## Consequences

### Positive

- Figures are interactive on the site, readable without JavaScript, and identical in content
  across the player, the SVG and the text description.
- A figure cannot drift silently:
  - specs cite code anchors that `sources` checks;
  - generated files are hash-bound;
  - the smoke test proves every figure mounts.
- The API-Reference diagram's false GitLab and Gitea claims are corrected from code.
- Upstream fixes arrive through a verified, reviewed sync instead of a fork.

### Negative / Trade-offs

- The docs build gains Node, npm, esbuild, React and Playwright, in `pages.yml`, the CI docs audit
  and verify-all. Amended by ADR-0016 (its operator decision 3): the MkDocs build itself needs
  Python only; Node and the npm toolchain stay in the checks around it (`bundle.mjs --check`, the
  tests, the type check and the smoke test).
- Pages with figures load about 75 kB gzip of React and interfig. The chunk loads lazily, and only
  near a figure.
- interfig is 4 days old, with one author and no tags. The pin, the hash manifest and the
  zero-patch rule contain the churn, but upstream API changes can force spec edits during a sync.
- The SVG fallback follows the OS colour scheme, not the site toggle, and uses upstream's palette
  until item 6c lands.
- Accessibility gaps in the player (section 6) remain until the shims or the upstream fixes land.

### Neutral

- Offering figures to adopters was left to a separate decision, with Mermaid kept in the MkDocs
  preset meanwhile. Amended by ADR-0016 §5 and §9, which make that decision: figures reach
  adopters through the `docs:seo-portal` facet, and the preset moves to figures.
- Five generated wiki pages emit ` ```figure ` fences, and the wiki sync turns them into portable
  blocks.

## Verification & Compliance

```bash
node --test third_party/interfig/upstream/src/*.test.ts
make docs-figures-check   # npm ci, tests, typecheck, check, sources
                          # amended by ADR-0016 §8: npm ci, tests, typecheck, bundle.mjs --check
make docs-figures         # added by ADR-0016 §8: build.mjs check, sources
make docs-diagrams-test
npm --prefix tools/figures run bundle   # amended by ADR-0016 §3: committed; bundle.mjs --check rebuilds it
mkdocs build --strict -d site
python3 -B scripts/docs_diagrams.py site --config mkdocs.yml --docs docs --site site
npm --prefix tools/figures run smoke
reuse lint
```

CI runs the same commands:

- `pages.yml` builds and smoke-tests the site;
- the CI Documentation Integrity Audit runs `make docs-figures-check` on docs-only pull requests
  (amended by ADR-0016 §8: and `make docs-figures`);
- `make verify-all` runs on every other change.

`scripts/sync_interfig.py verify` and the weekly `interfig-sync.yml` drift check are section 8;
they land with the figures-sync change, not with this one.
