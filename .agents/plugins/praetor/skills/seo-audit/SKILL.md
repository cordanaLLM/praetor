---
name: seo-audit
description: Audit and validate web pages, documentation portals, and static sites for Schema.org JSON-LD structured data, XML sitemaps, robots.txt directives, and Core Web Vitals performance.
---

# SEO & Web Vitals Audit (`seo-audit`)

Audit documentation sites and web applications for technical search engine optimization, semantic structured data, and Core Web Vitals (CWV) compliance.

## Audit Workflow

### 1. Schema.org JSON-LD Structured Data Validation
- Ensure all technical documentation pages embed valid `TechArticle` or `SoftwareSourceCode` JSON-LD in `<head>`.
- Validate required fields:
  - **TechArticle**: `@context: "https://schema.org"`, `@type: "TechArticle"`, `headline`, `description`, `author`, `dateModified`.
  - **SoftwareSourceCode**: `@type: "SoftwareSourceCode"`, `name`, `programmingLanguage`, `codeRepository` (must be absolute URL).
- Run the validator:
  ```bash
  go test -v -race ./internal/seo/...
  ```

### 2. XML Sitemap Audit (`sitemap.xml`)
- Verify the document root is `<urlset>` or `<sitemapindex>` with namespace `xmlns="http://www.sitemaps.org/schemas/sitemap/0.9"`.
- Assert URL count per sitemap $\le 50,000$.
- Assert all `<loc>` tags contain valid absolute URLs starting with `https://`.
- Assert `<priority>` values are bounded within $[0.0, 1.0]$.
- Assert `<changefreq>` values use standard frequencies (`always`, `hourly`, `daily`, `weekly`, `monthly`, `yearly`, `never`).

### 3. Crawl Directive Audit (`robots.txt`)
- Assert at least one `User-agent:` directive is defined.
- Confirm `Sitemap:` directives point to valid absolute URLs (`https://.../sitemap.xml`).
- Check that disallowed paths match real internal/ephemeral endpoints.

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
