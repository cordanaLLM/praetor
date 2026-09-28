---
title: Project documentation
description: Start page of the project documentation, with an example figure of how this site is built.
---

# Project Documentation

Replace this page with your project's overview. The front matter's `title` and `description`
become the page title and the description in its head.

## How This Site Is Built

```figure
site-build
```

The figure above is an example. Its spec is `docs/figures/site-build.ts`, and
`node tools/figures/build.mjs build` renders it into `docs/assets/figures/`. Replace it with a
figure of your own code, or delete the spec, its outputs and this section.

## Page Features

- **Structured data**: every page head carries Schema.org JSON-LD from `overrides/main.html`.
- **Sitemap**: MkDocs writes `sitemap.xml` from the navigation when `DOCS_SITE_URL` is set.
- **Interactive figures**: a `figure` fence names a spec under `docs/figures/`, and the figure hook
  renders it with scenario tabs, narration and a text description.
