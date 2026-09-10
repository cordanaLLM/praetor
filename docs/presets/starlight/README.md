# Astro Starlight Preset (`docs/presets/starlight`)

Production-ready documentation preset powered by [Astro Starlight](https://starlight.astro.build/) pre-configured with:
- **Schema.org JSON-LD Structured Data**: `TechArticle` and `SoftwareSourceCode` embedded in `<head>`.
- **Core Web Vitals Optimization**:
  - `font-display: swap` and preconnect hints for minimal FOIT and fast LCP.
  - `content-visibility: auto` for offscreen rendering speed and sub-100ms INP.
  - Zero CLS styling with layout containment and aspect-ratio bounds.
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
