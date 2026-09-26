#!/usr/bin/env python3
"""Fail a built MkDocs site in which a Mermaid block shipped as a code listing.

Material for MkDocs draws a diagram only from a `<pre class="mermaid">` element, and
pymdownx.superfences emits one only when mkdocs.yml declares the mermaid custom fence. Without
that entry the block falls through to the highlighter and the published page shows raw Mermaid
source. The HISS specification page shipped that way because the site's mkdocs.yml never
declared the fence and no build step looked at the output.

Two checks, standard library only so they run on whichever interpreter a CI job sets up:

* `--config` declares the fence exactly as Material's reference/diagrams page gives it;
* every Mermaid fence under `--docs` appears as a mermaid `<pre>` in the page `--site` holds
  for it, so a fence that stops rendering fails even when the configuration reads right.

The page mapping assumes MkDocs' default `use_directory_urls: true`, which the site and the
preset both use. The configuration reader handles the block-style YAML both files are written
in; it is a check on a third-party tool's file, not a loader for praetor configuration.
"""

from __future__ import annotations

import argparse
from html.parser import HTMLParser
from pathlib import Path
import re
import sys

# Bounded so a pathological tree cannot make the check unbounded (HISS-02).
MAX_PAGES = 4096
MAX_FILE_BYTES = 8 * 1024 * 1024
MAX_LINES = 100_000

EXPECTED_FENCE = {
    "name": "mermaid",
    "class": "mermaid",
    "format": "!!python/name:pymdownx.superfences.fence_code_format",
}
SUPERFENCES_ITEM = re.compile(r"^(\s*)-\s+pymdownx\.superfences\s*:\s*(?:#.*)?$")
FENCE_KEY = re.compile(r"^\s*(-\s+)?(name|class|format)\s*:\s*(\S+)\s*(?:#.*)?$")
# A fence opener or closer: three or more backticks or tildes, then the info string.
FENCE_LINE = re.compile(r"^\s*(`{3,}|~{3,})\s*([^\s`{]*)(.*)$")


class CheckError(Exception):
    """An input the check cannot read; reported as a usage failure, never as a pass."""


def read_text(path: Path) -> str:
    """The UTF-8 text of `path`, refusing files over MAX_FILE_BYTES."""
    try:
        if path.stat().st_size > MAX_FILE_BYTES:
            raise CheckError(f"{path} exceeds {MAX_FILE_BYTES} bytes")
        return path.read_text(encoding="utf-8")
    except (OSError, UnicodeDecodeError) as error:
        raise CheckError(f"cannot read {path}: {error}") from error


def bounded_lines(text: str) -> list[str]:
    """The lines of `text`, refusing more than MAX_LINES."""
    lines = text.splitlines()
    if len(lines) > MAX_LINES:
        raise CheckError(f"more than {MAX_LINES} lines")
    return lines


def superfences_body(lines: list[str]) -> list[str]:
    """The lines nested under the `- pymdownx.superfences:` entry, or [] when there is none."""
    for index, line in enumerate(lines):
        match = SUPERFENCES_ITEM.match(line)
        if match is None:
            continue
        indent = len(match.group(1))
        body = []
        for nested in lines[index + 1:]:
            stripped = nested.strip()
            if stripped and not stripped.startswith("#") and len(nested) - len(nested.lstrip()) <= indent:
                break
            body.append(nested)
        return body
    return []


def declared_fences(text: str) -> list[dict[str, str]]:
    """The name/class/format of every custom fence the superfences entry declares."""
    fences: list[dict[str, str]] = []
    for line in superfences_body(bounded_lines(text)):
        match = FENCE_KEY.match(line)
        if match is None:
            continue
        if match.group(1) or not fences:
            fences.append({})
        fences[-1][match.group(2)] = match.group(3).strip("'\"")
    return fences


def config_error(text: str) -> str | None:
    """Why an mkdocs.yml does not declare the mermaid fence, or None when it does."""
    if EXPECTED_FENCE in declared_fences(text):
        return None
    wanted = ", ".join(f"{key}: {value}" for key, value in EXPECTED_FENCE.items())
    return f"pymdownx.superfences declares no custom fence with {wanted}"


def mermaid_fences(text: str) -> int:
    """How many fenced blocks in a Markdown page open with the `mermaid` info string.

    A fence nested inside a longer or different fence is literal text, not a block, so it is
    not counted: a closer must use the opener's character, be at least as long, and carry no
    info string.
    """
    count, opener = 0, ""
    for line in bounded_lines(text):
        match = FENCE_LINE.match(line)
        if match is None:
            continue
        marker, info, rest = match.groups()
        if not opener:
            opener = marker
            count += info.lower() == "mermaid"
        elif marker[0] == opener[0] and len(marker) >= len(opener) and not info and not rest.strip():
            opener = ""
    return count


class _MermaidBlocks(HTMLParser):
    """Counts `<pre>` elements carrying the `mermaid` class, quoted or minified."""

    def __init__(self) -> None:
        super().__init__(convert_charrefs=True)
        self.count = 0

    def handle_starttag(self, tag: str, attrs: list[tuple[str, str | None]]) -> None:
        if tag == "pre" and "mermaid" in (dict(attrs).get("class") or "").split():
            self.count += 1


def rendered_diagrams(html: str) -> int:
    """How many Mermaid diagrams Material will draw from a built page."""
    parser = _MermaidBlocks()
    parser.feed(html)
    parser.close()
    return parser.count


def page_output(docs_dir: Path, site_dir: Path, page: Path) -> Path:
    """The HTML file MkDocs writes for `page` with directory URLs."""
    relative = page.relative_to(docs_dir)
    if relative.stem in ("index", "README"):
        return site_dir / relative.parent / "index.html"
    return site_dir / relative.parent / relative.stem / "index.html"


def markdown_pages(docs_dir: Path) -> list[Path]:
    """Every page MkDocs builds from `docs_dir` under its default `exclude_docs`."""
    pages = []
    for page in sorted(docs_dir.rglob("*.md")):
        parts = page.relative_to(docs_dir).parts
        if any(part.startswith(".") for part in parts) or parts[0] == "templates":
            continue
        pages.append(page)
        if len(pages) > MAX_PAGES:
            raise CheckError(f"more than {MAX_PAGES} Markdown pages under {docs_dir}")
    return pages


def page_error(docs_dir: Path, site_dir: Path, page: Path, expected: int) -> str | None:
    """Why the built page for `page` does not draw its `expected` diagrams, or None."""
    output = page_output(docs_dir, site_dir, page)
    if not output.is_file():
        return f"{page}: {expected} mermaid block(s) but MkDocs wrote no {output}"
    rendered = rendered_diagrams(read_text(output))
    if rendered == expected:
        return None
    return (f"{page}: {expected} mermaid block(s), {rendered} rendered as diagrams in {output}; "
            "a block without <pre class=\"mermaid\"> ships as a code listing")


def site_errors(docs_dir: Path, site_dir: Path) -> tuple[list[str], int]:
    """Every page whose diagrams did not render, and how many diagrams were checked."""
    errors, diagrams = [], 0
    for page in markdown_pages(docs_dir):
        expected = mermaid_fences(read_text(page))
        if expected == 0:
            continue
        diagrams += expected
        error = page_error(docs_dir, site_dir, page, expected)
        if error:
            errors.append(error)
    return errors, diagrams


def check(config: Path, docs_dir: Path, site_dir: Path) -> tuple[list[str], int]:
    """Configuration and rendered-page findings for one built site."""
    if not docs_dir.is_dir():
        raise CheckError(f"{docs_dir} is not a directory")
    if not (site_dir / "index.html").is_file():
        raise CheckError(f"{site_dir} holds no built site (no index.html); run mkdocs build first")
    errors = []
    declared = config_error(read_text(config))
    if declared:
        errors.append(f"{config}: {declared}")
    found, diagrams = site_errors(docs_dir, site_dir)
    return errors + found, diagrams


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("--config", type=Path, required=True, help="the mkdocs.yml that built the site")
    parser.add_argument("--docs", type=Path, required=True, help="the site's docs_dir")
    parser.add_argument("--site", type=Path, required=True, help="the built site directory")
    args = parser.parse_args(argv)
    try:
        errors, diagrams = check(args.config, args.docs, args.site)
    except CheckError as error:
        print(f"docs-mermaid: {error}", file=sys.stderr)
        return 2
    if errors:
        print("docs-mermaid: Mermaid diagrams do not render:", file=sys.stderr)
        for message in errors:
            print(f"  - {message}", file=sys.stderr)
        return 1
    print(f"docs-mermaid: {diagrams} Mermaid block(s) under {args.docs} render as diagrams.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
