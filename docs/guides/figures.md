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
| `core.mjs` | The render core: spec validation, the text description, the figure markup and the three outputs of one figure. Hashed. |
| `build.mjs` | The command line: `build`, `check`, `sources`, `site` and `portable`. Node 22.18 or later, no npm package. |
| `checks.mjs` | The `sources`, `site` and `portable` checks, and the slot filling of the figure markup. Node builtins only. |
| `loader.ts`, `player.tsx`, `keyboard.ts` | The player source: the loader reads each figure's props from its SVG and mounts the React player. |
| `bundle.mjs` | Builds the committed player in `dist/` with esbuild, checks it byte for byte against a rebuild from the lock, and holds its size budget. Needs the locked npm install. |
| `dist/` | The committed player: `loader.js`, `player.js` and `THIRD-PARTY-LICENSES.txt`. The same files serve every site. |
| `types.ts` | The spec type a `docs/figures/<slug>.ts` file checks against. |
| `mkdocs_hook.py` | The MkDocs hook that renders each `figure` fence and publishes `figures.css` and `dist/`. Python standard library and MkDocs only. |
| `astro.mjs` | The Astro integration for a Starlight site: renders each `figure` code block, loads the loader, and serves and publishes the figure and player files. Node builtins only. |
| `serve.mjs` | Serves and copies the figure and player files for `astro.mjs`, and serves a built site for the smoke test. Node builtins only. |
| `figures.css` | The figure stylesheet, reading Material's theme variables with Starlight's as the fallback. |
| `third_party/interfig/` | The vendored engine, byte-identical to its pin. |
| `README.md` | The neutral authoring guide that travels with the engine to adopting repositories. |
| `assets.go` | Embeds the engine files an adopting repository receives into `praetorctl`, as an explicit list ([ADR-0016](../adr/0016-figures-for-adopters.md), section 2). `assets_test.go` fails when a tracked file is neither in that list nor named repository-only, or when a listed script imports a file outside it. |

## In adopting repositories

The engine is the second managed asset family of the `docs:seo-portal` facet, beside the
Markdown gate (`figureEngine` in `internal/managedasset/family.go`;
[ADR-0016](../adr/0016-figures-for-adopters.md), section 5). The facet is in the default facet
set, and removing it is the only way to opt out of figures. With the facet enabled,
`praetorctl adopt` writes:

- the 18 files `assets.go` lists, under `tools/figures/`. A file the repository already had at
  one of those paths stops the first adoption, with or without `--force`, because
  `tools/figures/` is a name a repository may use for its own code;
- a block at the end of `.gitattributes` that keeps the engine, `docs/figures/*.ts` and
  `docs/assets/figures/*` at LF and the vendored interfig files unconverted
  (`internal/adopt/gitattributes.go`, rules from `Attributes` in `assets.go`);
- a `docs-figures` target in the managed Makefile block, attached to `verify-all` beside
  `docs-lint`, running `build.mjs check` and `build.mjs sources`;
- a "Verify figures" step in `.github/workflows/praetor-docs.yml`, after "Verify public
  Markdown", under the same required `Documentation Governance` context.

In a repository with no spec and no committed output, `check` and `sources` print that they
skipped and why, and exit 0, so the target and the workflow step cost nothing there. With the
facet disabled, adoption removes the canonical engine files, the `.gitattributes` block, the
Makefile block and the documentation workflow, and refuses to remove an engine file that was
edited. `praetorctl audit` compares every engine file byte for byte,
requires the target and the block, and warns when the repository's `REUSE.toml` has no
annotation labelling `tools/figures/third_party/interfig/upstream/**` MIT, or has one that a
later table covering the same files, such as `**`, overrides (`cmd/standardsctl/audit_reuse.go`).
An edited `.gitattributes` block fails a plain `praetorctl adopt`; `--force` restores it and
keeps a backup.

Every change to an embedded file reaches adopters on their next `praetorctl adopt`. The
shipped-text ledger, `internal/managedasset/testdata/shipped/figure-engine.sha256`, records every
text the family ever shipped; after such a change, append the new digests with
`PRAETOR_UPDATE_SHIPPED_TEXTS=1 go test ./internal/managedasset -run 'TestShippedTextLedger$'`
and add each outgoing digest to `priorDigests` in `assets.go`, so plain adoption refreshes an
unedited copy. The family holds at most 64 earlier texts; a React bump costs two, one for
`dist/player.js` and one for `dist/THIRD-PARTY-LICENSES.txt`, and an esbuild bump two, one for
each bundle under `dist/`.

## Mermaid is retired on the root site

Every diagram the root site builds is a figure, the generated wiki pages included, so the root
`mkdocs.yml` declares no mermaid fence. A Mermaid fence on a page the root site builds fails
`node tools/figures/build.mjs sources` and `site`, and the finding says to draw it as a `figure`
fence instead. The one exception is the adopter preset: `docs/presets/mkdocs/` builds its own
`docs/` with its own `mkdocs.yml`, keeps Mermaid, and needs no Node to build. The root
`mkdocs.yml` excludes that directory (`exclude_docs`), and the checks skip it too. The
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
- `evidence` lists at least one `path:Symbol` anchor. `node tools/figures/build.mjs sources` fails
  when the path is gone or the symbol no longer occurs in it.
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
| `<slug>.json` | Title, alt, text description, evidence, the edges with box labels, the size of each SVG in whole pixels (`width`/`height` animated, `static_width`/`static_height` static), the SHA-256 of the spec, the engine and both SVGs, and the figure markup as `html`. |

A build also deletes the outputs of a spec that no longer exists. Each figure has its own JSON
file, so figure changes in parallel branches do not collide.

The engine hash covers the three vendored render files and `tools/figures/core.mjs`
(`ENGINE_FILES` in `core.mjs`). An edit to one of them marks every figure stale; an edit to
`build.mjs`, `checks.mjs` or `bundle.mjs` does not.

## The committed player

The player is the same for every site, because it reads each figure's props from the SVG the
figure shows. `tools/figures/bundle.mjs` builds it once into `tools/figures/dist/`, which is
committed ([ADR-0016](../adr/0016-figures-for-adopters.md), section 3):

- `loader.js` loads on every page;
- `player.js` holds React and interfig and loads only when a figure nears the viewport; the loader
  imports it by that fixed name from beside itself;
- `THIRD-PARTY-LICENSES.txt` holds the full license texts of interfig and of every npm package
  the player bundles.

esbuild builds the two scripts without code splitting, so no file name carries a hash and a
dependency bump changes bytes, never the file list. No docs build bundles. After a change to the
player source, the lock or the interfig pin, rebuild and commit `dist/`:

```bash
npm ci --prefix tools/figures --ignore-scripts
node tools/figures/bundle.mjs
```

`node tools/figures/bundle.mjs --check` rebuilds in memory and fails when a committed file differs,
is missing or is not a bundle output, when `player.js` exceeds 250 kB minified, or when the
installed esbuild or a bundled package differs from `package-lock.json`. It runs on Linux, macOS
and Windows; the managed block at the end of `.gitattributes` pins `tools/figures/**` to LF, so
the comparison holds on a Windows checkout.
`build.mjs check` does not bundle.

## How a page shows a figure

`tools/figures/mkdocs_hook.py` replaces each `figure` fence with a `<figure>` holding a
`<picture>` of both SVGs, the caption and a `<details>` text description. A fence nested inside a
longer fence stays source text. A slug without JSON logs a warning, which fails
`mkdocs build --strict`.

The hook imports no checker: MkDocs runs hooks in Python, so the hook keeps its own fence scanner
beside the Node one in `checks.mjs`. Both replay `tools/figures/fence-fixtures.json` (in
`tools/figures/test_mkdocs_hook.py` and `tools/figures/checks.test.mjs`), and the `site` check
compares each page's fences with its rendered figures, so the two cannot drift apart unnoticed
([ADR-0016](../adr/0016-figures-for-adopters.md), section 6).

The markup has one source: `markup` in `core.mjs` writes it into each figure's JSON as `html`,
with a `{{base}}` slot for the URL prefix of the SVGs and a `{{link}}` slot for the interactive
figure. The hook (`render_block`) and `build.mjs portable` (`fillSlots` in `checks.mjs`) only fill
the slots; a caller without a link drops the line that holds `{{link}}`, as the hook always does.
`tools/figures/markup-fixtures.json` pins the markup for both languages:
`tools/figures/figures.test.mjs` renders it, and `tools/figures/checks.test.mjs` and
`tools/figures/test_mkdocs_hook.py` fill it. `build.mjs sources` fails a JSON whose `html` is not
what `markup` renders from the rest of it.

The static SVG has no scenario area, so it is usually shorter than the animated one. The
reduced-motion `<source>` carries the static size and the `<img>` the animated size, so the
browser reserves the height of the image it shows: no letterboxing and no layout shift
(the BUG-1002 test in `tools/figures/figures.test.mjs`).

When a figure nears the viewport, the loader fetches the SVG its `<img>` shows (`currentSrc`, from
the same origin and normally already cached), reads the props from the SVG's
`<metadata id="figure-spec">`, and mounts the player in place of the `<picture>`, with the caption
as the name of its scenario tabs. The fetch reads at most 4 MiB and gives up after 10 seconds
(`MAX_SVG_BYTES` and `FETCH_TIMEOUT_MS` in `tools/figures/loader.ts`); a figure whose SVG cannot be
read keeps it. Both SVGs carry the full props, so under reduced motion, where the browser shows the
static SVG, the player mounts all the same and starts paused on each step's last beat. The caption
and the text description stay. The loader runs again after Material's instant navigation and on
Astro's `astro:page-load` event.

`tools/figures/figures.css` maps the player's `--fig-*` colours to Material's variables, falling
back to Starlight's (`var(--md-…, var(--sl-color-…))`), so the palette toggle carries through on
either site. It and `dist/` sit outside `docs_dir`, so the hook publishes them,
the CSS at `assets/stylesheets/figures.css` and the player files under
`assets/javascripts/figures/`, and links the stylesheet and the loader (as a module script) from
every page; `mkdocs.yml` lists only the hook
(`test_a_site_build_renders_figures_and_publishes_the_stylesheet_and_the_player` in
`tools/figures/test_mkdocs_hook.py`). The
SVGs keep interfig's own palette and follow the operating system's colour scheme, not the site
toggle.

The keyboard reaches every tab, button and the full-screen toggle, and Esc closes full screen.
`tools/figures/keyboard.ts` adds Left, Right, Home and End across the scenario tabs.

## Figures on an Astro Starlight site

A Starlight site draws the same figures through `tools/figures/astro.mjs`, an Astro integration
that imports only Node builtins and the engine's own modules, so the site's lockfile does not
change ([ADR-0016](../adr/0016-figures-for-adopters.md), section 6). Add it and the stylesheet to
`astro.config.mjs`:

```js
import figures from './tools/figures/astro.mjs';

export default defineConfig({
  integrations: [starlight({ customCss: ['./tools/figures/figures.css'] }), figures()],
});
```

- A remark plugin turns each `figure` code block, in `.md` and `.mdx` pages alike, into the
  figure's JSON `html`, with `{{base}}` set to the root-absolute URL of the figures under the
  site's `base` (`/assets/figures`, or `/docs/assets/figures` for base `/docs/`). A block naming a
  figure without JSON fails the build.
- Astro keeps the rendered `.md` pages of a content collection in `node_modules/.astro/` and renders
  one again only when the page or the Astro configuration changes. The plugin's options carry a
  digest of `docs/assets/figures/*.json` (`figuresDigest`), so after `build.mjs build` a warm
  `astro build` renders every page again, and `astro dev` restarts when a figure JSON changes, is
  added or is removed (`watchFigures`).
- A head script on every page imports `assets/javascripts/figures/loader.js` under the same base.
- `astro dev` answers the figure files and `dist/` from the repository, and `astro build` copies
  them into the built site at `assets/figures/` and `assets/javascripts/figures/`, the paths the
  MkDocs hook uses. A file the built site already holds at one of those paths is kept and reported.
- `figures({ root })` names the repository root that holds `docs/assets/figures/` when it is not the
  directory two levels above `astro.mjs`.
- The loader gives the player host Starlight's `not-content` class, so Starlight's Markdown
  typography does not reach into the player.

`tools/figures/astro.test.mjs` covers the plugin, the hooks and `serve.mjs`.

Check a Starlight build with its configuration, its docs collection and the base it was built for;
serve it to the smoke test under the same base:

```bash
node tools/figures/build.mjs site --config astro.config.mjs --docs src/content/docs --site dist --base /docs/
node tools/figures/smoke.mjs --site dist --base /docs/ --require-browser
```

The configuration's name tells the checks which generator built the site: `astro.config.*` is
Astro, any other file MkDocs (`siteFlavor` in `tools/figures/checks.mjs`). For Astro they read the
pages Starlight's docs loader reads (every Markdown extension it accepts, without files whose name
starts with an underscore), map each page to the directory of its slug (the front matter's `slug`,
else the path as Astro slugs it), enable figures when the configuration names
`tools/figures/astro.mjs`, and never enable Mermaid. `sources` takes the same `--config` and
`--docs`. `--base` applies to an Astro site only; the MkDocs hook writes relative URLs.

## Outside the site

- **GitHub wiki.** The wiki sync runs `node tools/figures/build.mjs portable --wiki` on the copied
  pages, which reads the site URL from `site_url` in `mkdocs.yml` (a `!ENV NAME` value is read from
  that environment variable). Each fence becomes the same `<picture>` markup with absolute URLs to
  the published SVGs, followed by a link to the interactive figure on the site (see the
  [wiki sync guide](github-wiki-sync.md#figures)).
- **README.** A figure sits between `<!-- figure:<slug> -->` and `<!-- /figure -->`, with
  repository-relative image paths so pull-request previews show the new SVG. Refresh the block
  with `node tools/figures/build.mjs portable --write README.md`.

GitHub keeps `figure`, `figcaption`, `picture`, `img` and `details`, and strips `class` and
`data-*` attributes.

## Checks

| Command | Fails when |
| :-- | :-- |
| `npm --prefix tools/figures test` | a validation rule, the text derivation, the figure markup, the stale check, a figure check, the loader's spec reader, the committed player, the keyboard shim, the Astro integration or the file server regresses |
| `npm --prefix tools/figures run typecheck` | a spec or the player code does not type-check against `tools/figures/types.ts` and interfig |
| `node tools/figures/build.mjs check` | a spec breaks a rule, or a committed output differs from a fresh build; needs no npm package |
| `node tools/figures/bundle.mjs --check` | a file in `tools/figures/dist/` differs from a rebuild from the lock, is missing or is not a bundle output; `player.js` exceeds 250 kB minified; the install differs from the lock; needs the locked npm install |
| `node tools/figures/build.mjs sources` | a JSON hash no longer matches its spec, the engine or its SVGs; a JSON lacks a positive whole-number size for either SVG; its `html` is not the markup `core.mjs` renders from it; a spec or JSON is missing its pair; a fence names an unknown figure; a root-site page holds a Mermaid fence; the README block differs; an evidence anchor is gone; needs no npm package |
| `node tools/figures/build.mjs site --config mkdocs.yml --docs docs --site site` | after `mkdocs build` (or `astro build`, with `--config astro.config.mjs` and `--base`): a figure did not render, an image does not resolve or embeds no usable `<metadata id="figure-spec">`, the page does not load the loader or no `player.js` sits beside it, or a page holds a Mermaid fence; needs no npm package |
| `python3 -B tools/figures/test_mkdocs_hook.py` | the hook's fence scanner stops matching the fence fixtures, its slot filling stops matching the markup fixtures, or a site build no longer renders a figure or publishes `figures.css` |
| `npm --prefix tools/figures run smoke -- --site <dir> [--base <path>]` | in Chromium, against a built site served under its base path: a figure did not mount the player; a figure with scenario tabs did not advance its active step under autoplay within 8 s (another selected tab or a longer progress line, since a paused player still draws its first packets), or showed no packet under autoplay or after starting any of its tabs; a packet showed under reduced motion; or a page logged an error |

`make docs-figures-check` runs the steps only this repository runs, because they need the npm
lock: the tests, the type check and `bundle.mjs --check`. The managed `make docs-figures` target,
which every adopting repository receives, runs `check` and `sources`; both skip, saying why, in a
repository without a spec or output. `make docs-diagrams-test` runs the hook's tests. All three
are part of `make verify-all`. Each check exits 0 on a pass, 1 on findings and 2 on a usage error or an input it
cannot read. The
Platform Neutrality workflow runs the same figure commands on Linux, macOS and Windows. The Pages
workflow runs `check` and `bundle.mjs --check`, builds the site with the committed player, runs
`site` and `sources`, then the smoke test with `--require-browser`, before it uploads the site. Locally the smoke test needs Chromium
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
range before it touches the tree, then swaps in the new files, runs the upstream tests,
rebuilds every figure, because the engine hash changed, and rebuilds the committed player from the
locked install, because it bundles interfig. Commit `tools/figures/third_party/interfig/`,
`docs/assets/figures/` and `tools/figures/dist/` together. The full procedure is in
[VENDOR.md](https://github.com/cordanaLLM/praetor/blob/main/tools/figures/third_party/interfig/VENDOR.md#updating-the-pin).

## Credit

The engine is interfig by Vectorize AI, Inc., MIT-licensed, vendored unmodified at a pinned
commit under `tools/figures/third_party/interfig/`
([VENDOR.md](https://github.com/cordanaLLM/praetor/blob/main/tools/figures/third_party/interfig/VENDOR.md)).
The credit does not imply endorsement. The site footer, every exported SVG and both player scripts
carry the notice, and `tools/figures/dist/THIRD-PARTY-LICENSES.txt` carries the full license texts
of interfig, React, react-dom and scheduler. `REUSE.toml` labels `dist/` EUPL-1.2 AND MIT. In an
adopting repository that declares its licensing in `REUSE.toml`, `praetorctl audit` warns until
an annotation labels the vendored interfig files MIT.
[Credits](../credits.md) names the React, react-dom and scheduler versions the player bundles;
`CheckCredits` in `internal/supplychain/credits.go` fails `make test` when that row stops
matching `tools/figures/package-lock.json`.
