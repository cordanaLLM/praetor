"""MkDocs hook: render each ```figure fence as the figure markup, and serve the figure stylesheet.

Listed under `hooks:` in mkdocs.yml as tools/figures/mkdocs_hook.py
(docs/adr/0015-interactive-figures-from-vendored-interfig.md, section 4). The fence work lives in
docs_diagrams.py beside this file, whose fence scanner the checker uses too, so a fence nested
inside a longer fence is left alone here exactly as the checker expects. A fence naming a figure
without docs/assets/figures/<slug>.json is logged as a warning, which fails `mkdocs build --strict`.

figures.css sits beside this file, outside docs_dir, so the hook publishes it: `on_files` adds it
to the site as a generated file at CSS_URI and `on_config` links it from every page. A site needs
no `extra_css` entry for it.
"""

from __future__ import annotations

import importlib.util
import logging
from pathlib import Path

_SPEC = importlib.util.spec_from_file_location("docs_diagrams", Path(__file__).with_name("docs_diagrams.py"))
if _SPEC is None or _SPEC.loader is None:
    raise ImportError("docs_diagrams.py cannot be loaded beside the figures hook")
docs_diagrams = importlib.util.module_from_spec(_SPEC)
_SPEC.loader.exec_module(docs_diagrams)

log = logging.getLogger("mkdocs.plugins.praetor_figures")

CSS_FILE = Path(__file__).with_name("figures.css")
CSS_URI = "assets/stylesheets/figures.css"


def on_config(config):
    """Link the figure stylesheet from every page, once."""
    if CSS_URI not in config["extra_css"]:
        config["extra_css"].append(CSS_URI)
    return config


def on_files(files, config):
    """Add figures.css to the site at CSS_URI; a docs_dir file already there is kept and reported."""
    if CSS_URI in files.src_uris:
        log.warning("%s: docs_dir already holds this path, so the figures hook does not publish %s over it",
                    CSS_URI, CSS_FILE.name)
        return files
    from mkdocs.structure.files import File  # imported here so the hook loads without MkDocs in tests

    files.append(File.generated(config, CSS_URI, abs_src_path=str(CSS_FILE)))
    return files


def on_page_markdown(markdown, page, config, files):  # noqa: ARG001 - MkDocs hook signature
    """Replace the page's figure fences before MkDocs converts the Markdown."""
    figures = Path(config["docs_dir"]) / "assets" / "figures"
    text, errors = docs_diagrams.expand(markdown, docs_diagrams.site_base(page.url) + "/assets/figures", figures)
    for error in errors:
        log.warning("%s: %s", page.file.src_uri, error)
    return text
