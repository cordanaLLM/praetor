# Astro Starlight Preset (`docs/presets/starlight`)

Production-ready documentation preset powered by [Astro Starlight](https://starlight.astro.build/) pre-configured with:

- **Schema.org JSON-LD Structured Data**: a site-wide `SoftwareSourceCode` block from the `head` entry in `astro.config.mjs`, and a per-page `TechArticle` (page title, description, canonical URL, language) from `src/components/SEOHead.astro`. That component is registered as Starlight's `components.Head` override and renders Starlight's default head first, so canonical, OpenGraph and Twitter tags are emitted once, by Starlight.
- **Core Web Vitals Optimization** (`src/styles/custom.css`):
  - System font stacks only, so no web font is downloaded and no text waits on one.
  - `content-visibility: auto` on `article` and `section` to skip rendering offscreen content.
  - No layout-shift rules are needed: Starlight's hero image carries `width`/`height` attributes and Markdown images get their intrinsic size from `astro:assets`.
- **Automated XML Sitemap**: Generated using `@astrojs/sitemap`.
- **Search and Accessibility**: Full keyboard navigation, dark/light contrast conformity, and WCAG AA compliance.

## Quickstart

```bash
cd docs/presets/starlight
npm install
npm run dev
```

## Build & Verify

```bash
npm run build
# Built artifacts in dist/ with sitemap-index.xml and sitemap-0.xml
```

The content collection is configured in `src/content.config.ts` with Starlight's `docsLoader()`
(the Content Layer layout Starlight 0.30+ requires). The sidebar groups **Standards & Invariants**
and **Guides** autogenerate from `src/content/docs/standards/` and `src/content/docs/guides/`; a
group whose directory holds no page renders empty, so keep at least one page in each.
