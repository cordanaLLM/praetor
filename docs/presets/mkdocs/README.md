# MkDocs Material Preset (`docs/presets/mkdocs`)

Documentation preset powered by [Material for MkDocs](https://squidfunk.github.io/mkdocs-material/) pre-configured with:

- **Schema.org JSON-LD Structured Data**: `overrides/main.html` adds a per-page `TechArticle` (title, description, canonical URL, author, `dateModified`) and a site-wide `SoftwareSourceCode` block to every page `<head>`. `dateModified` is `page.update_date`, the build date MkDocs also writes as the page's sitemap `<lastmod>`. This repository's own `mkdocs.yml` points `theme.custom_dir` at the same directory, so the live site renders this template rather than a copy.
- **Identity from your `mkdocs.yml`**: the template names no project of its own. The `TechArticle` author is `site_author`, or `site_name` when that is unset. The `SoftwareSourceCode` block needs a repository (`extra.source_code.repository`, else `repo_url`) and `extra.source_code.programming_language`; `extra.source_code.name` (default `site_name`), `license` and `runtime_platform` are optional. Without a language the block is left out, and `404.html`, which has no page and therefore no `TechArticle`, then carries no JSON-LD. The shipped `mkdocs.yml` holds placeholders (`example-org/example-repo`, `PlaceholderLang`): replace them with your project's values. `scripts/test_docs_seo_presets.py` builds the template against sample configs.
- **Automated Sitemap Generation**: `site/sitemap.xml` and `site/sitemap.xml.gz` are written by MkDocs core from the `nav` tree, so no sitemap plugin is installed or configured. Entries need `DOCS_SITE_URL` (see [Site URL](#site-url)).
- **HTML/CSS/JS Minification**: Configured with `mkdocs-minify-plugin`.
- **Interactive Figures**: `mkdocs.yml` lists the figure hook, so a `figure` fence becomes an interactive figure with a static SVG and a text description (see [Figures](#figures)).

## Quickstart

The preset is the root of your documentation: copy its files into the root of a repository that
`praetorctl adopt` has adopted with the `docs:seo-portal` facet, which the default facet set
includes. Adoption writes the figure engine to `tools/figures/`, where `mkdocs.yml` expects its
hook. Copy every file but this README: the copy replaces a file of the same name, and your
repository keeps its own `README.md`.

```bash
# from your adopted repository root, with this directory at <preset>
cp -R <preset>/mkdocs.yml <preset>/requirements.in <preset>/requirements.txt \
  <preset>/docs <preset>/overrides .
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
node tools/figures/build.mjs site --config mkdocs.yml --docs docs --site site
praetorctl seo audit site
```

`site` fails when a `figure` fence did not become a figure, which a strict build does not catch
(`tools/figures/README.md`, "Checks").

`praetorctl seo audit` (`cmd/standardsctl/seo.go`) reads the built `site/`: every page head must carry
valid JSON-LD, and every root `sitemap*.xml` and any `robots.txt` must validate. It exits non-zero on
any finding; `--json` prints the full report and `--require-robots` fails a site without `robots.txt`.
A page head (title, meta tags, JSON-LD) that still carries a preset placeholder
(`example-org/example-repo` or `PlaceholderLang`) is a finding too; body text may name them.
`--allow-placeholders` skips that check, which is how CI audits the unedited preset.
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
`.github/workflows/ci.yml` installs the lock and builds this preset with `--strict`, in a
temporary repository adopted with `docs:seo-portal` and set up with the Quickstart copy, whenever
a file under `docs/presets/` or `tools/figures/` changes.

## Figures

`mkdocs.yml` lists `tools/figures/mkdocs_hook.py` under `hooks:` and excludes `/figures/` from
the pages. The hook replaces each `figure` fence with the figure rendered from
`docs/assets/figures/<slug>.json` and publishes the figure stylesheet and the player, so the
configuration needs no `extra_css`, `extra_javascript` or Mermaid fence. `tools/figures/README.md`,
which adoption writes beside the hook, explains how to write a spec and render it.

`docs/index.md` draws the example figure `site-build`: its spec is `docs/figures/site-build.ts`,
its committed outputs are `docs/assets/figures/site-build.{svg,static.svg,json}`, and its evidence
anchors name this preset's `mkdocs.yml`, `docs/index.md` and `overrides/main.html`. Replace it with
a figure of your own code, or delete the spec, its three outputs and the page section.

The preset needs the figure engine that `praetorctl adopt` writes under `docs:seo-portal`
([ADR-0016](../../adr/0016-figures-for-adopters.md), section 9). Its site build still needs
Python and MkDocs only: rendering and checking a figure needs Node 22.18 or later, with no npm
package. In the praetor repository, the root site does not build this preset's `docs/` directory
(`exclude_docs` in the root `mkdocs.yml`), and the
[documentation governance guide](../../guides/documentation-governance.md#site-build-and-diagrams)
describes how CI builds the preset and how the example figure is rebuilt.
