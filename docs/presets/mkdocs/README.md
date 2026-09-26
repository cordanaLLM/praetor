# MkDocs Material Preset (`docs/presets/mkdocs`)

Documentation preset powered by [Material for MkDocs](https://squidfunk.github.io/mkdocs-material/) pre-configured with:

- **Schema.org JSON-LD Structured Data**: `overrides/main.html` adds a per-page `TechArticle` (title, description, canonical URL, author, `dateModified`) and a site-wide `SoftwareSourceCode` block to every page `<head>`. `dateModified` is `page.update_date`, the build date MkDocs also writes as the page's sitemap `<lastmod>`. This repository's own `mkdocs.yml` points `theme.custom_dir` at the same directory, so the live site renders this template rather than a copy.
- **Identity from your `mkdocs.yml`**: the template names no project of its own. The `TechArticle` author is `site_author`, or `site_name` when that is unset. The `SoftwareSourceCode` block needs a repository (`extra.source_code.repository`, else `repo_url`) and `extra.source_code.programming_language`; `extra.source_code.name` (default `site_name`), `license` and `runtime_platform` are optional. Without a language the block is left out, and `404.html`, which has no page and therefore no `TechArticle`, then carries no JSON-LD. The shipped `mkdocs.yml` holds this repository's values as the example: replace them with your project's. `scripts/test_docs_seo_presets.py` builds the template against sample configs.
- **Automated Sitemap Generation**: `site/sitemap.xml` and `site/sitemap.xml.gz` are written by MkDocs core from the `nav` tree, so no sitemap plugin is installed or configured. Entries need `DOCS_SITE_URL` (see [Site URL](#site-url)).
- **HTML/CSS/JS Minification**: Configured with `mkdocs-minify-plugin`.
- **Mermaid Diagrams & PyMdown SuperFences**: Native diagrams rendered directly in documentation markdown.

## Quickstart

```bash
cd docs/presets/mkdocs
python3 -m venv .venv
source .venv/bin/activate
pip install --require-hashes -r requirements.txt

# Run development server
mkdocs serve
```

## Build & Verify

```bash
DOCS_SITE_URL=https://<owner>.github.io/<repo>/ mkdocs build --strict
# Built artifacts in site/ with sitemap.xml and minified HTML
praetorctl seo audit site
```

`praetorctl seo audit` (`cmd/standardsctl/seo.go`) reads the built `site/`: every page head must carry
valid JSON-LD, and every root `sitemap*.xml` and any `robots.txt` must validate. It exits non-zero on
any finding; `--json` prints the full report and `--require-robots` fails a site without `robots.txt`.
The rules are the `internal/seo` validators, tested in `internal/seo/site_test.go`.

## Site URL

`site_url` in `mkdocs.yml` reads `DOCS_SITE_URL` at build time; the preset ships no domain.
Set it to the URL the site is served from. Without it the build still passes, but it writes no
canonical links, no URLs in the JSON-LD blocks (`overrides/main.html`) and an empty
`sitemap.xml`, and Material's `navigation.instant` needs `site_url` because it reads that sitemap.

## Pinned Dependencies

`requirements.in` lists the four direct dependencies, each with an exact pin. `requirements.txt`
is the lock compiled from it: every package the preset installs, pinned with `==` and its hashes,
with environment markers for other Python versions and platforms. `pip install --require-hashes`
refuses an entry without both, so a hand-added range fails the install.

To change a version, edit `requirements.in` and recompile the lock from this directory with the
command recorded in the `requirements.txt` header:

```bash
uv pip compile --universal --generate-hashes --python-version=3.10 requirements.in --output-file=requirements.txt
```

Keep the `=` between each option and its argument. Renovate's `pip-compile` manager re-runs the
command it reads from this header, and it skips the lock with only a log warning when an option
takes its argument after a space or uses a short form such as `-o` (`extractHeaderCommand` in
Renovate's `lib/modules/manager/pip-compile/common.ts`). The `docs-presets` job rejects both forms.

Renovate runs the same command: `renovate.json` enables its `pip-compile` manager for this lock
and groups the updates as `mkdocs docs preset`. The `docs-presets` job in
`.github/workflows/ci.yml` installs the lock and builds this preset with `--strict` whenever a
file under `docs/presets/` changes.

## Mermaid Diagrams

Diagrams render only because `mkdocs.yml` declares the `mermaid` custom fence under
`pymdownx.superfences`. In the praetor repository, CI checks the built preset with
`scripts/docs_mermaid.py` (see the
[documentation governance guide](../../guides/documentation-governance.md#site-build-and-mermaid-diagrams)).
