# Astro Starlight Preset (`docs/presets/starlight`)

Production-ready documentation preset powered by [Astro Starlight](https://starlight.astro.build/) pre-configured with:

- **Schema.org JSON-LD Structured Data**: a site-wide `SoftwareSourceCode` block from the `head` entry in `astro.config.mjs`, and a per-page `TechArticle` (page title, description, canonical URL, language, `dateModified`) from `src/components/SEOHead.astro`. `dateModified` is Starlight's `lastUpdated` for the page, or the build time when Starlight has none. That component is registered as Starlight's `components.Head` override and renders Starlight's default head first, so canonical, OpenGraph and Twitter tags are emitted once, by Starlight.
- **Core Web Vitals Optimization** (`src/styles/custom.css`):
  - System font stacks only, so no web font is downloaded and no text waits on one.
  - `content-visibility: auto` on `article` and `section` to skip rendering offscreen content.
  - No layout-shift rules are needed: Starlight's hero image carries `width`/`height` attributes and Markdown images get their intrinsic size from `astro:assets`.
- **Automated XML Sitemap**: Generated using `@astrojs/sitemap` once `DOCS_SITE_URL` is set (see [Site URL](#site-url)).
- **Search and Accessibility**: Full keyboard navigation, dark/light contrast conformity, and WCAG AA compliance.

## Quickstart

```bash
cd docs/presets/starlight
npm ci
npm run dev
```

## Build & Verify

```bash
DOCS_SITE_URL=https://<owner>.github.io/<repo>/ npm run build
# Built artifacts in dist/ with sitemap-index.xml and sitemap-0.xml
praetorctl seo audit dist
```

`praetorctl seo audit dist` checks the JSON-LD in every built page head and validates both sitemap
files; the [MkDocs preset README](../mkdocs/README.md) lists what it enforces.

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

## Pinned Dependencies

`package.json` pins each dependency to an exact version and `package-lock.json` locks the whole
tree, including every platform's optional binaries. `npm ci` installs exactly that tree and fails
when `package.json` and the lock disagree; use `npm install` only to change a version, and commit
both files. Renovate keeps the exact pins (`rangeStrategy: pin` in `renovate.json`) and groups the
updates as `starlight docs preset`. The `docs-presets` job in `.github/workflows/ci.yml` runs
`npm ci` and builds this preset whenever a file under `docs/presets/` changes.

The content collection is configured in `src/content.config.ts` with Starlight's `docsLoader()`
(the Content Layer layout Starlight 0.30+ requires). The sidebar groups **Standards & Invariants**
and **Guides** autogenerate from `src/content/docs/standards/` and `src/content/docs/guides/`; a
group whose directory holds no page renders empty, so keep at least one page in each.
