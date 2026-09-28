#!/usr/bin/env python3
"""Check and render the documentation's diagrams: Mermaid fences and interactive figures.

One checker for both diagram kinds (docs/adr/0015-interactive-figures-from-vendored-interfig.md,
section 7). Standard library only, so it runs on whichever interpreter a CI job sets up.

* `site --config --docs --site` runs after `mkdocs build`. The kinds a build accepts come from its
  configuration: the declared mermaid custom fence enables ```mermaid, and the listed
  tools/figures/mkdocs_hook.py enables ```figure. A fence of a kind the configuration does not
  enable is an error; the root site enables figures only, so a Mermaid fence there is told to
  become a figure fence, while the adopter preset keeps Mermaid. Every Mermaid fence must appear
  as a `<pre class="mermaid">`; every figure fence as a `figure.praetor-figure[data-figure]`
  whose images resolve under the site, on a page that loads the figure loader, with its slug in
  the bundle's registry.json. Pages the configuration's `exclude_docs` leaves out are skipped,
  as MkDocs skips them.
* `sources` needs no site and no Node. It fails when a figure's JSON no longer matches its spec,
  the vendored engine files, tools/figures/core.mjs or its SVGs; when a spec has no JSON or a JSON
  no spec; when a fence names a figure that does not exist; when the README's portable block
  differs from the renderer; and when an evidence anchor's path or symbol is gone.
* `portable` renders figures for surfaces that run no JavaScript. `--base URL PAGE...` replaces
  the figure fences in wiki pages with absolute image URLs and a link to the interactive version;
  `--wiki PAGE...` does the same with the `site_url` of mkdocs.yml as the base. `--write FILE...`
  refreshes `<!-- figure:SLUG -->` blocks with repository-relative paths.

The figure markup itself comes from tools/figures/core.mjs, which writes it into each figure's JSON
as `html`; `portable` and the MkDocs hook only fill its `{{base}}` and `{{link}}` slots
(docs/adr/0016-figures-for-adopters.md, section 4).

The page mapping assumes MkDocs' default `use_directory_urls: true`, which the site and the preset
both use. The configuration reader handles the block-style YAML both files are written in; it is a
check on a third-party tool's file, not a loader for praetor configuration. Its `exclude_docs`
matcher follows the gitignore rules MkDocs applies through pathspec for the subset it covers: a
slash at the start or in the middle anchors a pattern at docs_dir, a trailing slash matches
directories only, and `*`, `?` and `[...]` (negated with `!` or `^`) match within one path
component, never across a `/`. It refuses the rest (a leading `!`, `**` and `\\`) instead of
guessing.
"""

from __future__ import annotations

import argparse
import fnmatch
import hashlib
import html
from html.parser import HTMLParser
import json
from pathlib import Path
import re
import sys
from typing import NamedTuple

# Bounded so a pathological tree cannot make the check unbounded (HISS-02).
MAX_PAGES = 4096
MAX_FILE_BYTES = 8 * 1024 * 1024
MAX_LINES = 100_000
MAX_FIGURES = 256

ROOT = Path(__file__).resolve().parents[2]
SPEC_DIR = Path("docs/figures")
FIGURE_DIR = Path("docs/assets/figures")
# The render core declares ENGINE_FILES, the files the engine hash covers.
ENGINE_SOURCE = Path("tools/figures/core.mjs")
LOADER = "assets/javascripts/figures/loader.js"
REGISTRY = Path("assets/javascripts/figures/registry.json")
# The figures hook is tools/figures/mkdocs_hook.py; a hooks entry naming it from any directory counts.
HOOK = ("figures", "mkdocs_hook.py")
REBUILD = "node tools/figures/build.mjs build"
KINDS = ("mermaid", "figure")

EXPECTED_FENCE = {
    "name": "mermaid",
    "class": "mermaid",
    "format": "!!python/name:pymdownx.superfences.fence_code_format",
}
SUPERFENCES_ITEM = re.compile(r"^(\s*)-\s+pymdownx\.superfences\s*:\s*(?:#.*)?$")
FENCE_KEY = re.compile(r"^\s*(-\s+)?(name|class|format)\s*:\s*(\S+)\s*(?:#.*)?$")
HOOKS_BLOCK = re.compile(r"^hooks\s*:\s*(?:#.*)?$")
HOOKS_FLOW = re.compile(r"^hooks\s*:\s*\[(.*)\]\s*(?:#.*)?$")
SITE_URL = re.compile(r"^site_url\s*:\s*['\"]?([^'\"#\s]+)")
LIST_ITEM = re.compile(r"^\s+-\s+['\"]?([^'\"#\s]+)")
EXCLUDE_BLOCK = re.compile(r"^exclude_docs\s*:\s*[|>][-+]?\s*(?:#.*)?$")
EXCLUDE_LINE = re.compile(r"^exclude_docs\s*:\s*['\"]?([^'\"#]*?)['\"]?\s*(?:#.*)?$")
# MkDocs adds these to every exclude_docs (mkdocs/structure/files.py, _default_exclude).
DEFAULT_EXCLUDE = (".*", "/templates/")
# gitignore syntax the exclude_docs matcher does not implement; a pattern using it is refused. A `!`
# negates only at the start of a pattern; inside a bracket expression it negates the bracket.
NEGATION = "!"
UNSUPPORTED_EXCLUDE = ("**", "\\")
# gitignore negates a bracket expression with `!` or `^`; fnmatch knows only `!`.
CARET_BRACKET = re.compile(r"\[\^")
# What to write instead of a fence whose kind the configuration does not enable, when the
# configuration enables the kind that replaces it.
REPLACEMENT = {"mermaid": ("figure", "draw it as a ```figure fence naming a spec under docs/figures/ "
                                     "(docs/guides/figures.md)")}
# A fence opener or closer: three or more backticks or tildes, then the info string.
FENCE_LINE = re.compile(r"^(\s*)(`{3,}|~{3,})\s*([^\s`{]*)(.*)$")
SLUG = re.compile(r"^[a-z0-9]+(?:-[a-z0-9]+)*$")
MARKER = re.compile(r"<!-- figure:([a-z0-9-]+) -->\n(.*?)<!-- /figure -->", re.DOTALL)
# The slots of a figure's JSON `html` (SLOTS and markup in tools/figures/core.mjs).
SLOT = re.compile(r"\{\{(base|link)\}\}")
LINK_SLOT = "{{link}}"
ENGINE_BLOCK = re.compile(r"export const ENGINE_FILES = \[(.*?)\];", re.DOTALL)


class CheckError(Exception):
    """An input the check cannot read; reported as a usage failure, never as a pass."""


class Fence(NamedTuple):
    """One top-level fenced block: its line span (end exclusive), info string and body."""

    start: int
    end: int
    indent: str
    info: str
    body: list[str]


def read_text(path: Path) -> str:
    """The UTF-8 text of `path` with LF line ends, refusing files over MAX_FILE_BYTES."""
    try:
        return read_bytes(path).decode("utf-8").replace("\r\n", "\n")
    except UnicodeDecodeError as error:
        raise CheckError(f"cannot read {path}: {error}") from error


def read_bytes(path: Path) -> bytes:
    """The bytes of `path`, refusing files over MAX_FILE_BYTES."""
    try:
        if path.stat().st_size > MAX_FILE_BYTES:
            raise CheckError(f"{path} exceeds {MAX_FILE_BYTES} bytes")
        return path.read_bytes()
    except OSError as error:
        raise CheckError(f"cannot read {path}: {error}") from error


def read_json(path: Path) -> dict:
    """A JSON object from `path`."""
    try:
        data = json.loads(read_text(path))
    except (UnicodeDecodeError, json.JSONDecodeError) as error:
        raise CheckError(f"cannot parse {path}: {error}") from error
    if not isinstance(data, dict):
        raise CheckError(f"{path} does not hold a JSON object")
    return data


def bounded_lines(text: str) -> list[str]:
    """The lines of `text`, refusing more than MAX_LINES.

    Lines end at "\n" only, as Python-Markdown splits them, with a trailing "\r" dropped.
    str.splitlines() also breaks at form feeds, U+2028 and other separators, which would
    shift every index expand() uses to splice a rendered figure into the page.
    """
    lines = [line.removesuffix("\r") for line in text.split("\n")]
    if lines and lines[-1] == "":
        lines.pop()  # a final newline ends the last line; it does not start another
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


def declared_hooks(text: str) -> list[str]:
    """The paths the top-level `hooks:` key lists, in block or flow style."""
    lines = bounded_lines(text)
    for index, line in enumerate(lines):
        flow = HOOKS_FLOW.match(line)
        if flow:
            return [item.strip().strip("'\"") for item in flow.group(1).split(",") if item.strip()]
        if not HOOKS_BLOCK.match(line):
            continue
        hooks = []
        for nested in lines[index + 1:]:
            item = LIST_ITEM.match(nested)
            if item is None and nested.strip() and not nested.lstrip().startswith("#"):
                break
            if item:
                hooks.append(item.group(1))
        return hooks
    return []


def excluded_patterns(text: str) -> list[str]:
    """The patterns of the top-level `exclude_docs` key: a block scalar, or one plain value."""
    lines = bounded_lines(text)
    for index, line in enumerate(lines):
        if EXCLUDE_BLOCK.match(line):
            return block_scalar_lines(lines[index + 1:])
        single = EXCLUDE_LINE.match(line)
        if single:
            return [single.group(1).strip()] if single.group(1).strip() else []
    return []


def block_scalar_lines(following: list[str]) -> list[str]:
    """The non-blank, non-comment lines of a block scalar: the indented lines that follow its key."""
    patterns = []
    for line in following:
        if line.strip() and not line[0].isspace():
            break
        entry = line.strip()
        if entry and not entry.startswith("#"):
            patterns.append(entry)
    return patterns


def pattern_matches(parts: tuple[str, ...], pattern: str) -> bool:
    """Whether a gitignore-style `pattern` excludes the docs-relative path split into `parts`.

    The pattern is matched component by component, so no wildcard crosses a `/`. A pattern with a
    slash at its start or in its middle is anchored at docs_dir and matches a leading run of path
    components (a matched directory excludes everything under it); one without matches any single
    component. A trailing slash matches a directory only, so it never matches the page's own file
    name. A bare `/` matches nothing, as in pathspec.
    """
    if pattern.startswith(NEGATION) or any(token in pattern for token in UNSUPPORTED_EXCLUDE):
        raise CheckError(f"exclude_docs pattern {pattern!r} uses gitignore syntax this check does not implement "
                         f"(a leading {NEGATION}, {', '.join(UNSUPPORTED_EXCLUDE)})")
    body = pattern.removesuffix("/")
    globs = [CARET_BRACKET.sub("[!", glob) for glob in body.removeprefix("/").split("/")]
    if globs == [""]:
        return False
    names = parts[:-1] if pattern.endswith("/") else parts
    if "/" not in body:
        return any(fnmatch.fnmatchcase(name, globs[0]) for name in names)
    return len(globs) <= len(names) and all(map(fnmatch.fnmatchcase, names, globs))


def is_excluded(parts: tuple[str, ...], patterns: list[str]) -> bool:
    """Whether MkDocs leaves the page at `parts` out: its default exclusions or `patterns`."""
    return any(pattern_matches(parts, pattern) for pattern in (*DEFAULT_EXCLUDE, *patterns))


def site_url(text: str) -> str:
    """The top-level `site_url` of an mkdocs.yml, or CheckError when it declares none."""
    for line in bounded_lines(text):
        match = SITE_URL.match(line)
        if match:
            return match.group(1)
    raise CheckError("the MkDocs configuration declares no site_url")


def enabled_kinds(text: str) -> set[str]:
    """The diagram kinds an mkdocs.yml renders: the mermaid fence and the figures hook."""
    kinds = set()
    if config_error(text) is None:
        kinds.add("mermaid")
    if any(Path(hook).parts[-2:] == HOOK for hook in declared_hooks(text)):
        kinds.add("figure")
    return kinds


def fence_blocks(text: str) -> list[Fence]:
    """Every top-level fenced block in a Markdown page.

    A fence nested inside a longer or different fence is literal text, not a block: a closer
    must use the opener's character, be at least as long, and carry no info string. An unclosed
    fence runs to the end of the page.
    """
    lines = bounded_lines(text)
    blocks: list[Fence] = []
    opener: tuple[int, str, str, str] | None = None
    for index, line in enumerate(lines):
        match = FENCE_LINE.match(line)
        if match is None:
            continue
        indent, marker, info, rest = match.groups()
        if opener is None:
            opener = (index, indent, marker, info.lower())
        elif marker[0] == opener[2][0] and len(marker) >= len(opener[2]) and not info and not rest.strip():
            blocks.append(Fence(opener[0], index + 1, opener[1], opener[3], lines[opener[0] + 1:index]))
            opener = None
    if opener is not None:
        blocks.append(Fence(opener[0], len(lines), opener[1], opener[3], lines[opener[0] + 1:]))
    return blocks


def fences(text: str, info: str) -> list[list[str]]:
    """The body lines of every top-level fence that opens with the `info` string."""
    return [block.body for block in fence_blocks(text) if block.info == info]


def figure_slug(body: list[str]) -> str:
    """The slug a ```figure fence names: its first non-blank line."""
    return next((line.strip() for line in body if line.strip()), "")


def figure_slugs(text: str) -> list[str]:
    """The slug of every top-level ```figure fence in a page."""
    return [figure_slug(body) for body in fences(text, "figure")]


def site_base(page_url: str) -> str:
    """The relative path from a built page back to the site root, for MkDocs' page.url."""
    depth = page_url.count("/")
    return "/".join([".."] * depth) if depth else "."


def figure_meta(figures_dir: Path, slug: str) -> dict:
    """The build's JSON for `slug`, or CheckError when there is none."""
    if not SLUG.match(slug):
        raise CheckError(f"figure slug {slug!r} is not lowercase kebab-case")
    path = figures_dir / f"{slug}.json"
    if not path.is_file():
        raise CheckError(f"figure {slug!r} has no {path.as_posix()}; add docs/figures/{slug}.ts and run {REBUILD}")
    return read_json(path)


def image_size(meta: dict, prefix: str = "") -> tuple[int, int]:
    """The width and height the build recorded for the animated SVG, or for the static one with
    `prefix` "static_"; CheckError when the JSON records no positive size (a stale build)."""
    size = (meta.get(f"{prefix}width"), meta.get(f"{prefix}height"))
    if not all(isinstance(value, (int, float)) and not isinstance(value, bool) and value > 0 for value in size):
        raise CheckError(f"figure {meta.get('slug')!r}: its JSON records no positive {prefix}width and "
                         f"{prefix}height; rebuild with: {REBUILD}")
    return int(size[0]), int(size[1])


def render_block(meta: dict, base: str, link: str | None = None) -> str:
    """The figure's markup: its JSON `html`, rendered by tools/figures/core.mjs, with the slots filled.

    This fills slots and renders nothing itself; `markup` in core.mjs documents the slots. `base`
    replaces `{{base}}` as given, `link` replaces `{{link}}` HTML-escaped, and without a `link` the
    line that holds `{{link}}` is dropped. The slots are filled in one pass, so a value that looks
    like a slot stays literal.
    """
    template = meta.get("html")
    if not isinstance(template, str) or not template:
        raise CheckError(f"figure {meta.get('slug')!r}: its JSON records no html; rebuild with: {REBUILD}")
    if not link:
        template = "\n".join(line for line in template.split("\n") if LINK_SLOT not in line)
    values = {"base": base, "link": html.escape(link or "")}
    return SLOT.sub(lambda match: values[match.group(1)], template)


def expand(markdown: str, base: str, figures_dir: Path, link: str | None = None) -> tuple[str, list[str]]:
    """`markdown` with every top-level ```figure fence replaced by its rendered block.

    `link` is a format string with `{slug}`, or None. A fence whose figure cannot be rendered is
    left as it is and reported, so a strict MkDocs build fails on the hook's warning.
    """
    lines = markdown.split("\n")
    errors: list[str] = []
    for block in reversed([b for b in fence_blocks(markdown) if b.info == "figure"]):
        slug = figure_slug(block.body)
        try:
            rendered = render_block(figure_meta(figures_dir, slug), base, link.format(slug=slug) if link else None)
        except CheckError as error:
            errors.append(str(error))
            continue
        lines[block.start:block.end] = [block.indent + line for line in rendered.split("\n")]
    return "\n".join(lines), list(reversed(errors))


class _PageScan(HTMLParser):
    """Mermaid <pre> elements, figure elements with their image URLs, and script sources."""

    def __init__(self) -> None:
        super().__init__(convert_charrefs=True)
        self.mermaid = 0
        self.figures: list[dict] = []
        self.scripts: list[str] = []
        self._depth = 0

    def handle_starttag(self, tag: str, attrs: list[tuple[str, str | None]]) -> None:
        attributes = dict(attrs)
        classes = (attributes.get("class") or "").split()
        if tag == "pre" and "mermaid" in classes:
            self.mermaid += 1
        elif tag == "script" and attributes.get("src"):
            self.scripts.append(attributes["src"] or "")
        elif tag == "figure":
            self._figure_start(attributes, classes)
        elif self._depth and tag in ("img", "source"):
            url = attributes.get("src") if tag == "img" else attributes.get("srcset")
            self.figures[-1]["urls"].append(url or "")

    def _figure_start(self, attributes: dict[str, str | None], classes: list[str]) -> None:
        if self._depth:
            self._depth += 1
        elif "praetor-figure" in classes and attributes.get("data-figure") and len(self.figures) < MAX_FIGURES:
            self.figures.append({"slug": attributes["data-figure"], "urls": []})
            self._depth = 1

    def handle_endtag(self, tag: str) -> None:
        if tag == "figure" and self._depth:
            self._depth -= 1


def scan_page(page_html: str) -> _PageScan:
    """What a built page holds that the diagram checks read."""
    parser = _PageScan()
    parser.feed(page_html)
    parser.close()
    return parser


def rendered_diagrams(page_html: str) -> int:
    """How many Mermaid diagrams Material will draw from a built page."""
    return scan_page(page_html).mermaid


def page_output(docs_dir: Path, site_dir: Path, page: Path) -> Path:
    """The HTML file MkDocs writes for `page` with directory URLs."""
    relative = page.relative_to(docs_dir)
    if relative.stem in ("index", "README"):
        return site_dir / relative.parent / "index.html"
    return site_dir / relative.parent / relative.stem / "index.html"


def markdown_pages(docs_dir: Path, excluded: list[str] | None = None) -> list[Path]:
    """Every page MkDocs builds from `docs_dir`: its default exclusions and `excluded` left out."""
    pages = []
    for page in sorted(docs_dir.rglob("*.md")):
        if is_excluded(page.relative_to(docs_dir).parts, excluded or []):
            continue
        pages.append(page)
        if len(pages) > MAX_PAGES:
            raise CheckError(f"more than {MAX_PAGES} Markdown pages under {docs_dir}")
    return pages


def kind_errors(page: Path, text: str, kinds: set[str]) -> list[str]:
    """A finding for every diagram fence on `page` whose kind the configuration does not enable."""
    found = {block.info for block in fence_blocks(text)} & set(KINDS)
    return [kind_error(page, kind, kinds) for kind in sorted(found - kinds)]


def kind_error(page: Path, kind: str, kinds: set[str]) -> str:
    """The finding for a disabled `kind`, naming its replacement when the configuration enables it."""
    message = f"{page}: ```{kind} fence, but the configuration does not enable {kind} diagrams"
    replacement, advice = REPLACEMENT.get(kind, ("", ""))
    return f"{message}; {advice}" if replacement in kinds else message


def resolves(output: Path, site_dir: Path, url: str) -> bool:
    """Whether a relative URL on the page at `output` names a file inside `site_dir`."""
    if not url or "://" in url or url.startswith(("/", "data:")):
        return False
    target = (output.parent / url.split("#", 1)[0].split("?", 1)[0]).resolve()
    return target.is_file() and target.is_relative_to(site_dir.resolve())


def mermaid_error(page: Path, output: Path, expected: int, scanned: _PageScan) -> str | None:
    """Why the built page does not draw its `expected` Mermaid diagrams, or None."""
    if scanned.mermaid == expected:
        return None
    return (f"{page}: {expected} mermaid block(s), {scanned.mermaid} rendered as diagrams in {output}; "
            "a block without <pre class=\"mermaid\"> ships as a code listing")


def figure_errors(page: Path, output: Path, site_dir: Path, slugs: list[str], scanned: _PageScan) -> list[str]:
    """Why the built page does not show its figures: count, slugs, image URLs and the loader."""
    built = [figure["slug"] for figure in scanned.figures]
    if built != slugs:
        return [f"{page}: figure fence(s) {slugs}, but {output} holds figure.praetor-figure {built}"]
    errors = [f"{page}: figure {figure['slug']}: {url or '(empty URL)'} does not resolve to a file under {site_dir}"
              for figure in scanned.figures for url in figure["urls"] if not resolves(output, site_dir, url)]
    if not any(src.endswith(LOADER) and resolves(output, site_dir, src) for src in scanned.scripts):
        errors.append(f"{page}: holds figures but loads no {LOADER} (bundle before mkdocs build: "
                      "npm --prefix tools/figures run bundle)")
    return errors


def page_errors(docs_dir: Path, site_dir: Path, page: Path, kinds: set[str]) -> tuple[list[str], int, list[str]]:
    """Findings for one page, how many diagrams it holds, and the figure slugs it names."""
    text = read_text(page)
    expected, slugs = len(fences(text, "mermaid")), figure_slugs(text)
    if not expected and not slugs:
        return [], 0, []
    errors = kind_errors(page, text, kinds)
    output = page_output(docs_dir, site_dir, page)
    if not output.is_file():
        return errors + [f"{page}: {expected + len(slugs)} diagram(s) but MkDocs wrote no {output}"], expected + len(slugs), slugs
    scanned = scan_page(read_text(output))
    mermaid = mermaid_error(page, output, expected, scanned) if expected else None
    errors += [mermaid] if mermaid else []
    if slugs:
        errors += figure_errors(page, output, site_dir, slugs, scanned)
    return errors, expected + len(slugs), slugs


def registry_errors(site_dir: Path, slugs: set[str]) -> list[str]:
    """Slugs the bundle's registry.json does not list."""
    if not slugs:
        return []
    path = site_dir / REGISTRY
    if not path.is_file():
        return [f"{path} is missing; run npm --prefix tools/figures run bundle before mkdocs build"]
    listed = read_json(path)
    return [f"{path} does not list figure {slug!r}" for slug in sorted(slugs - set(listed))]


def check(config: Path, docs_dir: Path, site_dir: Path) -> tuple[list[str], int]:
    """Configuration and rendered-page findings for one built site, and the diagrams checked."""
    if not docs_dir.is_dir():
        raise CheckError(f"{docs_dir} is not a directory")
    if not (site_dir / "index.html").is_file():
        raise CheckError(f"{site_dir} holds no built site (no index.html); run mkdocs build first")
    text = read_text(config)
    kinds = enabled_kinds(text)
    errors, diagrams, slugs = [], 0, set()
    for page in markdown_pages(docs_dir, excluded_patterns(text)):
        found, count, named = page_errors(docs_dir, site_dir, page, kinds)
        errors += found
        diagrams += count
        slugs.update(named)
    return errors + registry_errors(site_dir, slugs), diagrams


def sha256(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


def engine_files(root: Path) -> list[str]:
    """ENGINE_FILES as tools/figures/core.mjs declares it; core.mjs is the one list."""
    match = ENGINE_BLOCK.search(read_text(root / ENGINE_SOURCE))
    files = re.findall(r"'([^']+)'", match.group(1)) if match else []
    if not files:
        raise CheckError(f"{ENGINE_SOURCE} declares no ENGINE_FILES")
    return files


def engine_hash(root: Path) -> str:
    """The same value as engineHash() in tools/figures/build.mjs: a sha256sum-style manifest, hashed."""
    lines = "".join(f"{sha256(read_bytes(root / rel))}  {rel}\n" for rel in engine_files(root))
    return sha256(lines.encode("utf-8"))


def evidence_errors(root: Path, slug: str, evidence: list) -> list[str]:
    """Evidence anchors whose path is missing or whose symbol no longer occurs in it."""
    errors = []
    for anchor in evidence[:MAX_FIGURES]:
        path, _, symbol = str(anchor).partition(":")
        target = (root / path).resolve()
        if not symbol or not target.is_relative_to(root.resolve()) or not target.is_file():
            errors.append(f"{FIGURE_DIR.as_posix()}/{slug}.json: evidence {anchor!r} names no file in the repository")
        elif not all(part in read_text(target) for part in symbol.split(".")):
            errors.append(f"{FIGURE_DIR.as_posix()}/{slug}.json: evidence {anchor!r}: {symbol} no longer occurs in {path}")
    return errors


def hash_errors(root: Path, slug: str, meta: dict, engine: str) -> list[str]:
    """Why a figure's JSON no longer matches its spec, the engine or its SVGs."""
    where = f"{FIGURE_DIR.as_posix()}/{slug}.json"
    bound = [("spec", SPEC_DIR / f"{slug}.ts", meta.get("spec_sha256")),
             ("SVG", FIGURE_DIR / f"{slug}.svg", meta.get("svg_sha256")),
             ("static SVG", FIGURE_DIR / f"{slug}.static.svg", meta.get("static_sha256"))]
    errors = []
    for what, path, recorded in bound:
        if not (root / path).is_file():
            errors.append(f"{where}: {path.as_posix()} is missing; rebuild with: {REBUILD}")
        elif sha256(read_bytes(root / path)) != recorded:
            errors.append(f"{where} is stale: the {what} {path.as_posix()} changed; rebuild with: {REBUILD}")
    if (meta.get("engine") or {}).get("sha256") != engine:
        errors.append(f"{where} is stale: the figure engine changed; rebuild with: {REBUILD}")
    return errors


def figure_source_errors(root: Path) -> tuple[list[str], set[str]]:
    """Spec/JSON pairing, hash binding and evidence for every figure, and the known slugs."""
    specs = {path.stem for path in (root / SPEC_DIR).glob("*.ts")} if (root / SPEC_DIR).is_dir() else set()
    metas = {path.stem for path in (root / FIGURE_DIR).glob("*.json")} if (root / FIGURE_DIR).is_dir() else set()
    if len(specs | metas) > MAX_FIGURES:
        raise CheckError(f"more than {MAX_FIGURES} figures")
    errors = [f"{SPEC_DIR.as_posix()}/{slug}.ts has no {FIGURE_DIR.as_posix()}/{slug}.json; rebuild with: {REBUILD}"
              for slug in sorted(specs - metas)]
    errors += [f"{FIGURE_DIR.as_posix()}/{slug}.json has no spec {SPEC_DIR.as_posix()}/{slug}.ts; delete it or restore the spec"
               for slug in sorted(metas - specs)]
    engine = engine_hash(root) if specs & metas else ""
    for slug in sorted(specs & metas):
        meta = read_json(root / FIGURE_DIR / f"{slug}.json")
        errors += hash_errors(root, slug, meta, engine) + evidence_errors(root, slug, meta.get("evidence") or [])
        errors += size_errors(slug, meta)
    return errors, specs & metas


def size_errors(slug: str, meta: dict) -> list[str]:
    """A finding for each image, animated and static, whose size the figure's JSON lacks."""
    errors = []
    for prefix in ("", "static_"):
        try:
            image_size(meta, prefix)
        except CheckError as error:
            errors.append(f"{FIGURE_DIR.as_posix()}/{slug}.json: {error}")
    return errors


def relative_base(root: Path, document: Path) -> str:
    """The repository-relative URL prefix of docs/assets/figures as seen from `document`."""
    depth = len(document.resolve().relative_to(root.resolve()).parts) - 1
    return "/".join([".."] * depth + list(FIGURE_DIR.parts))


def refresh_markers(text: str, base: str, figures_dir: Path) -> tuple[str, list[str]]:
    """`text` with every `<!-- figure:SLUG -->` block re-rendered, and the slugs that failed."""
    errors: list[str] = []

    def replace(match: re.Match) -> str:
        try:
            block = render_block(figure_meta(figures_dir, match.group(1)), base)
        except CheckError as error:
            errors.append(str(error))
            return match.group(0)
        return f"<!-- figure:{match.group(1)} -->\n{block}\n<!-- /figure -->"

    return MARKER.sub(replace, text, count=MAX_FIGURES), errors


def page_source_errors(root: Path, docs: Path, config: Path, known: set[str]) -> list[str]:
    """Disabled fence kinds and unknown figure slugs on every page the configuration builds.

    Without a configuration file every kind is accepted and only MkDocs' default exclusions apply.
    """
    settings = read_text(root / config) if (root / config).is_file() else None
    kinds = set(KINDS) if settings is None else enabled_kinds(settings)
    errors = []
    for page in markdown_pages(root / docs, excluded_patterns(settings or "")):
        text = read_text(page)
        errors += kind_errors(page.relative_to(root), text, kinds)
        errors += [f"{page.relative_to(root)}: ```figure fence names {slug!r}, which has no spec and JSON"
                   for slug in figure_slugs(text) if slug not in known]
    return errors


def sources(root: Path, docs: Path, config: Path, readme: Path) -> list[str]:
    """Every source-side finding: figure bindings, fence slugs, fence kinds and the README block."""
    errors, known = figure_source_errors(root)
    errors += page_source_errors(root, docs, config, known)
    if (root / readme).is_file():
        text = read_text(root / readme)
        refreshed, failed = refresh_markers(text, relative_base(root, root / readme), root / FIGURE_DIR)
        errors += [f"{readme}: {message}" for message in failed]
        if refreshed != text:
            errors.append(f"{readme}: a portable figure block differs from the renderer; refresh it with: "
                          f"python3 -B tools/figures/docs_diagrams.py portable --write {readme}")
    return errors


def portable(files: list[Path], base: str | None, root: Path) -> list[str]:
    """Rewrites `files` for JavaScript-free readers; writes nothing when any figure fails."""
    updates, errors = [], []
    for path in files:
        text = read_text(path)
        if base is None:
            updated, failed = refresh_markers(text, relative_base(root, path), root / FIGURE_DIR)
        else:
            link = f"{base.rstrip('/')}/wiki/{path.stem}/#fig-{{slug}}"
            updated, failed = expand(text, f"{base.rstrip('/')}/assets/figures", root / FIGURE_DIR, link)
        errors += [f"{path}: {message}" for message in failed]
        updates.append((path, updated, text))
    if errors:
        return errors
    for path, updated, text in updates:
        if updated != text:
            path.write_text(updated, encoding="utf-8", newline="\n")
    return []


def report(label: str, errors: list[str], success: str) -> int:
    if errors:
        print(f"docs-diagrams: {label}:", file=sys.stderr)
        for message in errors:
            print(f"  - {message}", file=sys.stderr)
        return 1
    print(f"docs-diagrams: {success}")
    return 0


def parser() -> argparse.ArgumentParser:
    cli = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    commands = cli.add_subparsers(dest="command", required=True)
    site = commands.add_parser("site", help="check a built MkDocs site")
    site.add_argument("--config", type=Path, required=True, help="the mkdocs.yml that built the site")
    site.add_argument("--docs", type=Path, required=True, help="the site's docs_dir")
    site.add_argument("--site", type=Path, required=True, help="the built site directory")
    src = commands.add_parser("sources", help="check figure sources without a site or Node")
    src.add_argument("--root", type=Path, default=ROOT, help="the repository root")
    src.add_argument("--docs", type=Path, default=Path("docs"), help="docs_dir, relative to --root")
    src.add_argument("--config", type=Path, default=Path("mkdocs.yml"), help="mkdocs.yml, relative to --root")
    src.add_argument("--readme", type=Path, default=Path("README.md"), help="README, relative to --root")
    port = commands.add_parser("portable", help="render figures for surfaces without JavaScript")
    mode = port.add_mutually_exclusive_group(required=True)
    mode.add_argument("--base", help="site URL: replace ```figure fences with absolute images (wiki pages)")
    mode.add_argument("--wiki", action="store_true", help="as --base, with the site_url of --config")
    mode.add_argument("--write", action="store_true", help="refresh <!-- figure:SLUG --> blocks in place")
    port.add_argument("--config", type=Path, default=Path("mkdocs.yml"), help="mkdocs.yml, relative to --root")
    port.add_argument("--root", type=Path, default=ROOT, help="the repository root")
    port.add_argument("files", type=Path, nargs="+", help="the Markdown files to rewrite")
    return cli


def run(args: argparse.Namespace) -> int:
    if args.command == "site":
        errors, diagrams = check(args.config, args.docs, args.site)
        return report("diagrams do not render", errors, f"{diagrams} diagram(s) under {args.docs} render.")
    if args.command == "sources":
        errors = sources(args.root.resolve(), args.docs, args.config, args.readme)
        return report("figure sources are inconsistent", errors, "figure sources, hashes and evidence are consistent.")
    root = args.root.resolve()
    base = site_url(read_text(root / args.config)) if args.wiki else args.base
    errors = portable(args.files, None if args.write else base, root)
    return report("figures cannot be rendered", errors, f"rendered figures in {len(args.files)} file(s).")


def main(argv: list[str] | None = None) -> int:
    args = parser().parse_args(argv)
    try:
        return run(args)
    except CheckError as error:
        print(f"docs-diagrams: {error}", file=sys.stderr)
        return 2


if __name__ == "__main__":
    sys.exit(main())
