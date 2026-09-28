# Interactive figures

Praetor's documentation draws its architecture figures with interfig, the engine behind the
animated figures on the Hindsight documentation site. On the site a figure plays scenario tabs
with moving packets, narration, pause and 1×/2× speed. Everywhere else, in the README, the GitHub
wiki, or a browser without JavaScript, the same figure is a committed SVG with a caption and a
text description. [ADR-0015](../adr/0015-interactive-figures-from-vendored-interfig.md) records
the design.

The first figure is the gated pipeline on the
[C4 architecture page](../architecture/c4-models.md#3-level-3-component-diagram-gating-engine).

## Where the engine lives

Everything the figures need sits in one tree, `tools/figures/`:

| Path | Role |
| :-- | :-- |
| `core.mjs` | The render core: spec validation, the text description and the three outputs of one figure. Hashed. |
| `build.mjs` | The `build` and `check` commands. Node 22.18 or later, no npm package. |
| `bundle.mjs` | Bundles the player for this site with esbuild and holds its size budget. Needs the locked npm install. |
| `types.ts` | The spec type a `docs/figures/<slug>.ts` file checks against. |
| `docs_diagrams.py` | The `site`, `sources` and `portable` checks. Python standard library only. |
| `mkdocs_hook.py` | The MkDocs hook that renders each `figure` fence and publishes `figures.css`. |
| `third_party/interfig/` | The vendored engine, byte-identical to its pin. |

## Mermaid is retired on the root site

Every diagram the root site builds is a figure, the generated wiki pages included, so the root
`mkdocs.yml` declares no mermaid fence. A Mermaid fence on a page the root site builds fails
`python3 -B tools/figures/docs_diagrams.py sources` and `site`, and the finding says to draw it as a
`figure` fence instead. The one exception is the adopter preset: `docs/presets/mkdocs/` builds
its own `docs/` with its own `mkdocs.yml`, keeps Mermaid, and needs no Node. The root
`mkdocs.yml` excludes that directory (`exclude_docs`), and the checker skips it too. The
[documentation governance guide](documentation-governance.md#site-build-and-diagrams) covers
which configuration enables which kind.

## Add a figure

1. Read the code the figure will show. Old diagrams and prose are hypotheses: every box, edge and
   narration line must match the code it cites.
2. Write the spec as `docs/figures/<slug>.ts`, with a lowercase kebab-case slug:

   ```ts
   import type { PraetorFigure } from '../../tools/figures/types.ts';

   export default {
     title: 'Gated pipeline',
     alt: 'Six gate stages run in order; a failing stage rejects the change before a receipt is signed.',
     evidence: ['internal/gating/pipeline.go:executeStages'],
     props: { layout: { children: [] }, edges: [], steps: [] },
   } satisfies PraetorFigure;
   ```

3. Build the committed outputs. This needs Node 22.18 or later and no npm install:

   ```bash
   node tools/figures/build.mjs build
   ```

4. Name the figure in a page with a `figure` fence that holds only the slug:

   ````markdown
   ```figure
   gating-pipeline
   ```
   ````

5. Commit the spec, `docs/assets/figures/<slug>.svg`, `<slug>.static.svg` and `<slug>.json`.

`docs/figures/gating-pipeline.ts` is a complete example with four scenario tabs.

### Spec rules

`props` takes interfig's figure format, which the
[upstream README](https://github.com/vectorize-io/hindsight/blob/ccfe85b4851957ac2adf88b4a9ddf9668b2882f1/hindsight-interfig/README.md)
describes: a `layout` tree of groups and boxes, `edges`, and `steps` made of beats. Praetor
narrows it (`tools/figures/types.ts`), and `validate` in `tools/figures/core.mjs` enforces the
same rules at build time:

- Every label, `sub`, `say`, `caption`, hop `data` and card row is a string, so the player, the
  SVG and the text description show the same content. There is no JSX in a spec.
- `alt` is one sentence of at most 125 characters. `title` becomes the caption.
- `evidence` lists at least one `path:Symbol` anchor. `docs_diagrams.py sources` fails when the
  path is gone or the symbol no longer occurs in it.
- Ids are unique across boxes and groups. Every edge end is a box or group id, every beat names an
  existing edge, and every `show` and `light` key is a box id.
- At most 40 boxes, 40 groups, 80 edges and 12 steps, with at most 32 beats per step.
- Use `shape: 'store'` for data at rest, and no Hindsight names or branding.
- `describe` adds lines to the text description.

The text description is derived from the spec: the groups with their boxes, the edges as
sentences, each scenario's label, caption and narration, then `describe`. It is capped at 2,500
characters on whole lines.

## What the build writes

For each spec, `node tools/figures/build.mjs build` writes to `docs/assets/figures/`:

| File | Content |
| :-- | :-- |
| `<slug>.svg` | The animated SVG, with `role="img"`, a `<title>`, a `<desc>`, a credit comment and the embedded spec. |
| `<slug>.static.svg` | The same figure without steps, shown under `prefers-reduced-motion`. |
| `<slug>.json` | Title, alt, text description, evidence, the edges with box labels, the size of each SVG (`width`/`height` animated, `static_width`/`static_height` static), and the SHA-256 of the spec, the engine and both SVGs. |

A build also deletes the outputs of a spec that no longer exists. Each figure has its own JSON
file, so figure changes in parallel branches do not collide.

The engine hash covers the three vendored render files and `tools/figures/core.mjs`
(`ENGINE_FILES` in `core.mjs`). An edit to one of them marks every figure stale; an edit to
`build.mjs` or `bundle.mjs` does not.

`npm --prefix tools/figures run bundle` (`tools/figures/bundle.mjs`) writes the player bundle to
`docs/assets/javascripts/figures/`. The directory is gitignored and rebuilt by every docs build:

- `loader.js` loads on every page;
- `player.js` holds React and interfig and loads only when a figure nears the viewport;
- `specs/<slug>.js` holds one spec each, listed in `registry.json`.

The player chunk must stay under 250 kB minified. `node tools/figures/bundle.mjs --check` bundles
into a temporary directory and fails above it, as `run bundle` does; `build.mjs check` does not
bundle.

## How a page shows a figure

`tools/figures/mkdocs_hook.py` replaces each `figure` fence with a `<figure>` holding a
`<picture>` of both SVGs, the caption and a `<details>` text description. A fence nested inside a
longer fence stays source text. A slug without JSON logs a warning, which fails
`mkdocs build --strict`.

The static SVG has no scenario area, so it is usually shorter than the animated one. The
reduced-motion `<source>` carries the static size and the `<img>` the animated size, so the
browser reserves the height of the image it shows: no letterboxing and no layout shift
(`test_each_image_carries_its_own_recorded_size` in `scripts/test_docs_diagrams.py`).

The loader then mounts the player in place of the `<picture>` when the figure nears the viewport.
Under reduced motion it starts paused on each step's last beat. The caption and the text
description stay. Without the bundle, for example under `mkdocs serve` without Node, every figure
keeps its SVG.

`tools/figures/figures.css` maps the player's `--fig-*` colours to Material's variables, so the
palette toggle carries through. It sits outside `docs_dir`, so the hook publishes it at
`assets/stylesheets/figures.css` and links it from every page; `mkdocs.yml` lists only the hook
(`test_a_site_build_publishes_and_links_the_stylesheet` in `scripts/test_docs_diagrams.py`). The
SVGs keep interfig's own palette and follow the operating system's colour scheme, not the site
toggle.

The keyboard reaches every tab, button and the full-screen toggle, and Esc closes full screen.
`tools/figures/keyboard.ts` adds Left, Right, Home and End across the scenario tabs.

## Outside the site

- **GitHub wiki.** The wiki sync runs `docs_diagrams.py portable --wiki` on the copied pages, which
  reads the site URL from `site_url` in `mkdocs.yml`. Each fence becomes the same `<picture>` markup with absolute URLs to the published SVGs,
  followed by a link to the interactive figure on the site (see the
  [wiki sync guide](github-wiki-sync.md#figures)).
- **README.** A figure sits between `<!-- figure:<slug> -->` and `<!-- /figure -->`, with
  repository-relative image paths so pull-request previews show the new SVG. Refresh the block
  with `python3 -B tools/figures/docs_diagrams.py portable --write README.md`.

GitHub keeps `figure`, `figcaption`, `picture`, `img` and `details`, and strips `class` and
`data-*` attributes.

## Checks

| Command | Fails when |
| :-- | :-- |
| `npm --prefix tools/figures test` | a validation rule, the text derivation, the stale check or the keyboard shim regresses |
| `npm --prefix tools/figures run typecheck` | a spec or the player code does not type-check against `tools/figures/types.ts` and interfig |
| `node tools/figures/build.mjs check` | a spec breaks a rule, or a committed output differs from a fresh build; needs no npm package |
| `node tools/figures/bundle.mjs --check` | the player chunk exceeds 250 kB minified; needs the locked npm install |
| `python3 -B tools/figures/docs_diagrams.py sources` | a JSON hash no longer matches its spec, the engine or its SVGs; a JSON lacks the size of either SVG; a spec or JSON is missing its pair; a fence names an unknown figure; a root-site page holds a Mermaid fence; the README block differs; an evidence anchor is gone |
| `python3 -B tools/figures/docs_diagrams.py site --config mkdocs.yml --docs docs --site site` | after `mkdocs build`: a figure did not render, an image does not resolve, the page does not load the loader, `registry.json` lacks the slug, or a page holds a Mermaid fence |
| `npm --prefix tools/figures run smoke -- --site <dir>` | in Chromium, against a built site: a figure did not mount the player; a figure with scenario tabs did not advance its active step under autoplay within 8 s (another selected tab or a longer progress line, since a paused player still draws its first packets), or showed no packet under autoplay or after starting any of its tabs; a packet showed under reduced motion; or a page logged an error |

`make docs-figures-check` runs the tests, the type check, `check`, `bundle.mjs --check` and
`sources`, and `make docs-diagrams-test` replays the checker's fixtures and tests the hook; both
are part of `make verify-all`. The
Platform Neutrality workflow runs the same figure commands on Linux, macOS and Windows. The Pages
workflow bundles, builds, runs `site` and `sources`, then the smoke test with `--require-browser`,
before it uploads the site. Locally the smoke test needs Chromium
(`npx --prefix tools/figures playwright install chromium`); without it, it exits 0 and says it
skipped.

## Updating interfig

The engine is vendored at a pinned commit. The `interfig-sync.yml` workflow runs
`scripts/sync_interfig.py check` weekly and fails with the update command when upstream has
moved; an HTTP error or timeout fails it with a different message, so an outage does not read
as drift. To take the update:

```bash
python3 scripts/sync_interfig.py update --commit <sha>
```

The command refuses a LICENSE change, an unlisted upstream file or an incompatible React peer
range before it touches the tree, then swaps in the new files, runs the upstream tests and
rebuilds every figure, because the engine hash changed. Commit `tools/figures/third_party/interfig/`
and `docs/assets/figures/` together. The full procedure is in
[VENDOR.md](https://github.com/cordanaLLM/praetor/blob/main/tools/figures/third_party/interfig/VENDOR.md#updating-the-pin).

## Credit

The engine is interfig by Vectorize AI, Inc., MIT-licensed, vendored unmodified at a pinned
commit under `tools/figures/third_party/interfig/`
([VENDOR.md](https://github.com/cordanaLLM/praetor/blob/main/tools/figures/third_party/interfig/VENDOR.md)).
The credit does not imply endorsement. The site footer, every exported SVG and the player bundle
carry the notice.
