"""MkDocs hook: render each ```figure fence as the praetor figure markup.

Listed under `hooks:` in mkdocs.yml (docs/adr/0015-interactive-figures-from-vendored-interfig.md,
section 4). The work lives in scripts/docs_diagrams.py, whose fence scanner the checker uses too,
so a fence nested inside a longer fence is left alone here exactly as the checker expects. A fence
naming a figure without docs/assets/figures/<slug>.json is logged as a warning, which fails
`mkdocs build --strict`.
"""

from __future__ import annotations

import importlib.util
import logging
from pathlib import Path

_SPEC = importlib.util.spec_from_file_location("docs_diagrams", Path(__file__).with_name("docs_diagrams.py"))
if _SPEC is None or _SPEC.loader is None:
    raise ImportError("scripts/docs_diagrams.py cannot be loaded beside the figures hook")
docs_diagrams = importlib.util.module_from_spec(_SPEC)
_SPEC.loader.exec_module(docs_diagrams)

log = logging.getLogger("mkdocs.plugins.praetor_figures")


def on_page_markdown(markdown, page, config, files):  # noqa: ARG001 - MkDocs hook signature
    """Replace the page's figure fences before MkDocs converts the Markdown."""
    figures = Path(config["docs_dir"]) / "assets" / "figures"
    text, errors = docs_diagrams.expand(markdown, docs_diagrams.site_base(page.url) + "/assets/figures", figures)
    for error in errors:
        log.warning("%s: %s", page.file.src_uri, error)
    return text
