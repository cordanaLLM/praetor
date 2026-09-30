# Astro Starlight Preset (`docs/presets/starlight`)

Production-ready documentation preset powered by [Astro Starlight](https://starlight.astro.build/) pre-configured with:

- **Schema.org JSON-LD Structured Data**: a site-wide `SoftwareSourceCode` block from the `head` entry in `astro.config.mjs`, and a per-page `TechArticle` (page title, description, canonical URL, language, author, `dateModified`) from `src/components/SEOHead.astro`. Neither names a project of its own: the author is the site `title` (Starlight has no author option), and the `SoftwareSourceCode` block and the GitHub social link read the `sourceCode` constant at the top of `astro.config.mjs`. An empty `repository` or `programmingLanguage` there leaves the block out; `runtimePlatform` and `license` are optional. The shipped values are placeholders (`example-org/example-repo`, `PlaceholderLang`): replace them with your project's. `dateModified` is Starlight's `lastUpdated` for the page, or the build time when Starlight has none. That component is registered as Starlight's `components.Head` override and renders Starlight's default head first, so canonical, OpenGraph and Twitter tags are emitted once, by Starlight.
- **Core Web Vitals Optimization** (`src/styles/custom.css`):
  - System font stacks only, so no web font is downloaded and no text waits on one.
  - `content-visibility: auto` on `article` and `section` to skip rendering offscreen content.
  - No layout-shift rules are needed: Starlight's hero image carries `width`/`height` attributes and Markdown images get their intrinsic size from `astro:assets`.
- **Automated XML Sitemap**: Generated using `@astrojs/sitemap` once `DOCS_SITE_URL` is set (see [Site URL](#site-url)).
- **Search and Accessibility**: Full keyboard navigation, dark/light contrast conformity, and WCAG AA compliance.
- **Interactive Figures**: figures of your code drawn from TypeScript specs, with a static SVG and a text description for readers without JavaScript (see [Figures](#figures)). The integration adds no npm package.

## Quickstart

The preset imports the figure engine from `tools/figures/`, which `praetorctl adopt` writes with
the `docs:seo-portal` facet (part of the default facet set). Copy the preset into the root of an
adopted repository, so that `astro.config.mjs` sits beside `tools/figures/`, then render the
example figure and start the development server. The preset ships the figure's spec without its
outputs, so render it before the first site build or `make docs-figures`; until then both fail
and name `node tools/figures/build.mjs build`:

```bash
cp -R path/to/docs/presets/starlight/. .
node tools/figures/build.mjs build
npm ci
npm run dev
```

Built where it sits, without `tools/figures/` beside it, the configuration fails to import
`./tools/figures/astro.mjs`.

## Build & Verify

```bash
DOCS_SITE_URL=https://<owner>.github.io/<repo>/ npm run build
# Built artifacts in dist/ with sitemap-index.xml and sitemap-0.xml
praetorctl seo audit ./dist
```

`praetorctl seo audit ./dist` checks the JSON-LD in every built page head and validates both sitemap
files, and fails while a page head still carries a placeholder; the
[MkDocs preset README](../mkdocs/README.md) lists what it enforces.

## Site URL

`site` in `astro.config.mjs` reads `DOCS_SITE_URL` at build time; the preset ships no domain.
Set it to the URL the site is served from. Without it the build still passes, but canonical
links carry no URL, the `TechArticle` JSON-LD from `src/components/SEOHead.astro` has no `url`
fields, and `@astrojs/sitemap` skips generation with a warning, so `dist/` has no sitemap.

A URL with a path, such as a GitHub project page (`https://<owner>.github.io/<repo>/`), also
sets Astro's `base` to that path (`/<repo>/`). Astro and Starlight then prefix it to page routes,
asset URLs, sidebar links, canonical links, the `TechArticle` URLs and the sitemap entries. A
root host sets `base` to `/`. Links you write inside page content get no prefix: use a relative
link, as the hero action in `src/content/docs/index.mdx` does (`guides/onboarding/`), not one
starting with `/`. The `docs-presets` job in `.github/workflows/ci.yml` builds the preset under
a path and fails when any of those URLs leaves it.

## Figures

`astro.config.mjs` adds the figures integration, `figures()` from `tools/figures/astro.mjs`, and
lists `tools/figures/figures.css` in Starlight's `customCss`. The integration imports only Node
built-ins and the figure engine, so `package.json` and `package-lock.json` name no figure package.
It turns each `figure` code block in a `.md` or `.mdx` page into the figure, loads the player on
every page under the site's `base`, and copies the figure and player files into `dist/`. It runs
on Astro's default Markdown processor, Sätteri, so the preset sets no `markdown.processor`. The
[figures guide](../../guides/figures.md#figures-on-an-astro-starlight-site) describes it, and
`tools/figures/README.md` in an adopted repository covers the spec format.

<!-- praetor:docs-references:off paths relative to the preset, as it sits in an adopter's repository root -->

The example figure is `docs/figures/site-build.ts`, shown on `src/content/docs/guides/figures.mdx`.
Its evidence anchors point into the preset's own files (`astro.config.mjs` and
`src/content.config.ts`), so the checks pass on a fresh copy; replace it with figures of your own
code. The preset ships the spec without its outputs: each figure's JSON records the hash of the
engine that rendered it, so `node tools/figures/build.mjs build` renders them with the engine your
adoption wrote. Commit the spec with its three outputs under `docs/assets/figures/`.

<!-- praetor:docs-references:on -->

Check the figures, then the built site under the base it was built for (`/`, or the path of
`DOCS_SITE_URL`, such as `/<repo>/`):

```bash
make docs-figures
npm run build
node tools/figures/build.mjs site --config astro.config.mjs --docs src/content/docs --site dist --base /
```

`make docs-figures`, which adoption attaches to `verify-all`, runs `node tools/figures/build.mjs
check` and `node tools/figures/build.mjs sources`; run those two on a machine without make.
`sources` finds `astro.config.mjs` at the repository root and reads the pages under
`src/content/docs/`, so it fails a `figure` block that names an unknown figure; its success line
names the pages it read. `npm run build` also fails on a `figure` block in an `.mdx` page that
names a figure without a JSON file; in an `.md` page Astro logs the error and builds the page
without its content, so keep `make docs-figures` in the gate.

The `docs-presets` job in `.github/workflows/ci.yml` builds the preset this way (its
`Build Starlight Preset In An Adopter Fixture` step): a temporary repository adopted with
`docs:seo-portal`, the preset copied into its root, `build` and `make docs-figures`, then three
site builds (without `DOCS_SITE_URL`, with a root host and with a path), each followed by the
`site` check and the Chromium smoke test, `tools/figures/smoke.mjs`, under that build's base.

## Pinned Dependencies

`package.json` pins each dependency to an exact version and `package-lock.json` locks the whole
tree, including every platform's optional binaries. `npm ci` installs exactly that tree and fails
when `package.json` and the lock disagree; use `npm install` only to change a version, and commit
both files. Renovate keeps the exact pins (`rangeStrategy: pin` in `renovate.json`) and groups the
updates as `starlight docs preset`. The `docs-presets` job in `.github/workflows/ci.yml` runs
`npm ci` from this lock and builds the preset in an adopter fixture (see [Figures](#figures))
whenever a file under `docs/presets/` or `tools/figures/`, or the adoption code that writes the
engine, changes.

The lock installs no package with a known high or critical advisory: the
`Go Vulnerability & AST Security Scan` job in `.github/workflows/security.yml` runs
`npm audit --package-lock-only --audit-level=high` on it for every pull request and daily. Run the
same command here before you commit a changed lock.

The preset targets Astro 7 and Starlight 0.42. The content collection is configured in
`src/content.config.ts` with Starlight's `docsLoader()` (the Content Layer layout Starlight 0.30+
requires). The sidebar groups **Standards & Invariants** and **Guides** each list the pages of
`src/content/docs/standards/` and `src/content/docs/guides/` through an `autogenerate` entry in
their `items` (the group shape Starlight 0.39+ requires); a group whose directory holds no page
renders empty, so keep at least one page in each. Social links are an array of `icon`, `label`
and `href` entries (Starlight 0.33+); the GitHub link reads `sourceCode.repository`.
