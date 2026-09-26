---
name: seo-audit
description: Audit and validate web pages, documentation portals, and static sites for Schema.org JSON-LD structured data, XML sitemaps, robots.txt directives, and Core Web Vitals performance.
---

# SEO & Web Vitals Audit (`seo-audit`)

Audit documentation sites and web applications for technical search engine optimization, semantic structured data, and Core Web Vitals (CWV) compliance.

## Audit Workflow

### 1. Schema.org JSON-LD Structured Data Validation
- Build site, then audit built output, never source tree:
  ```bash
  mkdocs build                     # writes site/; Starlight: npm run build writes dist/
  praetorctl seo audit site        # --json: machine report; --require-robots: host-root site
  ```
  Exit 0: every HTML page head carries valid JSON-LD; root sitemaps and `robots.txt` validate. Non-zero: findings listed per file. Steps 2 and 3 run in same command.
- Every page `<head>` must embed at least one `TechArticle` or `SoftwareSourceCode` JSON-LD block. Any other `@type` fails.
- Required fields (enforced by `internal/seo`):
  - **TechArticle**: `@context: "https://schema.org"`, `@type: "TechArticle"`, `headline`, `description`, `author` (name, object with `name`, or list of them), `dateModified` (RFC 3339 or `YYYY-MM-DD`).
  - **SoftwareSourceCode**: `@type: "SoftwareSourceCode"`, `name`, `programmingLanguage`, `codeRepository` (absolute `http(s)` URL).
- `go test ./internal/seo/...` re-runs validator's own fixtures only; audits no site.

### 2. XML Sitemap Audit (`sitemap*.xml`)
`praetorctl seo audit` validates every `sitemap*.xml` at site root (MkDocs `sitemap.xml`; Starlight `sitemap-index.xml`, `sitemap-0.xml`). No root sitemap = finding.
- Document root `<urlset>` or `<sitemapindex>` in namespace `http://www.sitemaps.org/schemas/sitemap/0.9`.
- At most 50,000 entries per file.
- Every `<loc>` absolute `http(s)` URL; prefer `https://`.
- `<priority>` within $[0.0, 1.0]$.
- `<changefreq>` one of `always`, `hourly`, `daily`, `weekly`, `monthly`, `yearly`, `never`.

### 3. Crawl Directive Audit (`robots.txt`)
`praetorctl seo audit` validates `robots.txt` at site root when present. Crawlers read it only at host root, so project site under path (GitHub Pages `/<repo>/`) cannot serve one; pass `--require-robots` for host-root site.
- At least one `User-agent:` directive.
- `Sitemap:` directives are absolute URLs (`https://.../sitemap.xml`).
- Manual: disallowed paths match real internal/ephemeral endpoints.

### 4. Core Web Vitals (CWV) Checklist
- **Largest Contentful Paint (LCP $\le 2.5$s)**:
  - Critical fonts preconnected and set to `font-display: swap`.
  - Hero images compressed using WebP/AVIF with explicit dimensions.
- **Cumulative Layout Shift (CLS $\le 0.1$)**:
  - All visual containers, diagrams, and hero blocks define `aspect-ratio` or `min-height`.
  - Zero dynamic DOM insertions pushing existing content downwards.
- **Interaction to Next Paint (INP $\le 200$ms)**:
  - Offscreen content utilizes `content-visibility: auto`.
  - Minimal main-thread blocking JavaScript.
