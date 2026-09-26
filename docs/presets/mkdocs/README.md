# MkDocs Material Preset (`docs/presets/mkdocs`)

Documentation preset powered by [Material for MkDocs](https://squidfunk.github.io/mkdocs-material/) pre-configured with:

- **Schema.org JSON-LD Structured Data**: Injected automatically into the HTML `<head>` via template overrides.
- **Automated Sitemap Generation**: Configured with daily change frequency and priority mapping via `mkdocs-sitemap-plugin`.
- **HTML/CSS/JS Minification**: Configured with `mkdocs-minify-plugin`.
- **Mermaid Diagrams & PyMdown SuperFences**: Native diagrams rendered directly in documentation markdown.

## Quickstart

```bash
cd docs/presets/mkdocs
python3 -m venv .venv
source .venv/bin/activate
pip install -r requirements.txt

# Run development server
mkdocs serve
```

## Build & Verify

```bash
mkdocs build --strict
# Built artifacts in site/ with sitemap.xml and minified HTML
```
