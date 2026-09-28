#!/usr/bin/env python3
"""Tests for the MkDocs figures hook (tools/figures/mkdocs_hook.py): its fence scanner, which replays
the fixtures the Node checks replay, the slot filling, the fence expansion and the stylesheet.

The figure checks themselves are Node (tools/figures/checks.mjs) and are tested by
tools/figures/checks.test.mjs."""

import ast
import contextlib
import io
import json
from pathlib import Path
import sys
import tempfile
import unittest

FIGURES = Path(__file__).resolve().parent
ROOT = FIGURES.parents[1]
sys.path.insert(0, str(FIGURES))

import mkdocs_hook  # noqa: E402

FIGURE = "```figure\ndemo\n```\n"
# The fixtures tools/figures/checks.test.mjs replays against checks.mjs.
FENCES = json.loads((FIGURES / "fence-fixtures.json").read_text(encoding="utf-8"))
MARKUP = json.loads((FIGURES / "markup-fixtures.json").read_text(encoding="utf-8"))
META = dict(MARKUP["meta"], html=MARKUP["html"])


def write(path, text):
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(text, encoding="utf-8")


class SelfContained(unittest.TestCase):
    def test_the_hook_imports_only_the_standard_library_and_mkdocs(self):
        """ADR-0016, section 6: the hook imports no checker, so a site build needs Python and MkDocs only."""
        tree = ast.parse((FIGURES / "mkdocs_hook.py").read_text(encoding="utf-8"))
        imported = {alias.name.split(".")[0] for node in ast.walk(tree) if isinstance(node, ast.Import) for alias in node.names}
        imported |= {node.module.split(".")[0] for node in ast.walk(tree) if isinstance(node, ast.ImportFrom) and node.module}
        foreign = {name for name in imported if name not in sys.stdlib_module_names and name not in ("__future__", "mkdocs")}
        self.assertEqual(foreign, set())
        self.assertNotIn("importlib", imported)
        # Boundary: the Python checker it once loaded is gone; the checks are Node.
        self.assertFalse((FIGURES / "docs_diagrams.py").exists())


class FenceScanner(unittest.TestCase):
    def test_the_shared_fixtures_replay(self):
        """fence-fixtures.json holds the blocks the Node scanner (fenceBlocks) must find too."""
        self.assertGreaterEqual(len(FENCES["cases"]), 20)
        for case in FENCES["cases"]:
            with self.subTest(case=case["name"]):
                blocks = [[b.start, b.end, b.indent, b.info, b.body, mkdocs_hook.figure_slug(b.body)]
                          for b in mkdocs_hook.fence_blocks(case["markdown"])]
                self.assertEqual(blocks, case["blocks"])

    def test_line_bound(self):
        self.assertEqual(mkdocs_hook.bounded_lines("\n" * mkdocs_hook.MAX_LINES), [""] * mkdocs_hook.MAX_LINES)
        with self.assertRaises(mkdocs_hook.CheckError):
            mkdocs_hook.bounded_lines("\n" * (mkdocs_hook.MAX_LINES + 1))

    def test_site_base_follows_page_depth(self):
        cases = {"": ".", "adr/": "..", "architecture/c4-models/": "../..", "a/b/c.html": "../.."}
        for url, base in cases.items():
            with self.subTest(url=url):
                self.assertEqual(mkdocs_hook.site_base(url), base)


class RenderBlock(unittest.TestCase):
    def test_the_markup_fixture_fills_as_recorded_without_a_link(self):
        """A site drops the link line: every fixture case without a link comes out byte for byte."""
        cases = [case for case in MARKUP["cases"] if not case["link"]]
        self.assertGreaterEqual(len(cases), 2)
        for case in cases:
            with self.subTest(base=case["base"]):
                self.assertEqual(mkdocs_hook.render_block(META, case["base"]), case["html"])

    def test_only_the_link_line_is_dropped(self):
        block = mkdocs_hook.render_block(META, ".")
        self.assertNotIn("<p><a", block)
        self.assertNotIn("{{", block)
        self.assertEqual(len(block.split("\n")), len(MARKUP["html"].split("\n")) - 1)
        self.assertEqual(block.count('src="./demo.svg"') + block.count('srcset="./demo.static.svg"'), 2)

    def test_a_base_that_looks_like_a_slot_stays_literal(self):
        block = mkdocs_hook.render_block(META, "{{link}}")
        self.assertIn('srcset="{{link}}/demo.static.svg"', block)
        # Boundary: a template without slots comes back as it is.
        self.assertEqual(mkdocs_hook.render_block({"html": "<p>plain</p>"}, "b"), "<p>plain</p>")

    def test_a_json_without_html_is_refused(self):
        for html_value in (None, "", 42, ["<figure>"]):
            meta = dict(META, html=html_value)
            if html_value is None:
                del meta["html"]
            with self.subTest(html=html_value), self.assertRaises(mkdocs_hook.CheckError) as raised:
                mkdocs_hook.render_block(meta, ".")
            self.assertIn("records no html; rebuild with: node tools/figures/build.mjs build", str(raised.exception))


class Expand(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.figures = Path(self.tmp.name)
        write(self.figures / "demo.json", json.dumps(META))

    def test_top_level_fences_only(self):
        source = "# Page\n\n" + FIGURE + "\n````markdown\n" + FIGURE + "````\n"
        text, errors = mkdocs_hook.expand(source, "..", self.figures)
        self.assertEqual(errors, [])
        self.assertEqual(text.count('<figure class="praetor-figure"'), 1)
        self.assertIn("````markdown\n```figure\ndemo\n```\n````", text)
        indented, _ = mkdocs_hook.expand("- item\n\n    " + FIGURE.replace("\n", "\n    "), ".", self.figures)
        self.assertIn("\n    <picture>", indented)

    def test_line_separators_markdown_does_not_split_on_are_kept(self):
        for separator in ("\x0c", " ", "\x85"):
            source = f"intro{separator}line\n\n" + FIGURE + "after\n"
            text, errors = mkdocs_hook.expand(source, "..", self.figures)
            self.assertEqual(errors, [])
            self.assertTrue(text.startswith(f"intro{separator}line\n\n<figure"), repr(text[:40]))
            self.assertTrue(text.endswith("</details>\nafter\n"), repr(text[-40:]))
        crlf, errors = mkdocs_hook.expand("intro\r\n\r\n" + FIGURE.replace("\n", "\r\n") + "after\r\n", "..", self.figures)
        self.assertEqual(errors, [])
        self.assertNotIn("```figure", crlf)
        self.assertIn("after", crlf)

    def test_an_unknown_or_unreadable_figure_is_left_and_reported(self):
        write(self.figures / "broken.json", "[]")
        source = "```figure\nmissing\n```\n```figure\nBad Slug\n```\n```figure\nbroken\n```\n"
        text, errors = mkdocs_hook.expand(source, ".", self.figures)
        self.assertEqual(text, source)
        self.assertEqual(len(errors), 3)
        self.assertIn("figure 'missing' has no", errors[0])
        self.assertIn("not lowercase kebab-case", errors[1])
        self.assertIn("does not hold a JSON object", errors[2])


class _Files:
    """The part of MkDocs' Files the hook reads before it appends: src_uris and append."""

    def __init__(self, uris=()):
        self.src_uris = {uri: object() for uri in uris}
        self.appended = []

    def append(self, file):
        self.appended.append(file)
        self.src_uris[file.src_uri] = file


class Hook(unittest.TestCase):
    """figures.css sits beside the hook, outside docs_dir, so the hook links and publishes it."""

    def test_the_stylesheet_sits_beside_the_hook(self):
        self.assertEqual(mkdocs_hook.CSS_FILE, FIGURES / "figures.css")
        self.assertTrue(mkdocs_hook.CSS_FILE.is_file())
        self.assertFalse((ROOT / "docs/stylesheets/figures.css").exists())

    def test_on_config_links_the_stylesheet_once(self):
        config = {"extra_css": ["extra.css"]}
        self.assertIs(mkdocs_hook.on_config(config), config)
        mkdocs_hook.on_config(config)
        self.assertEqual(config["extra_css"], ["extra.css", mkdocs_hook.CSS_URI])
        empty = mkdocs_hook.on_config({"extra_css": []})
        self.assertEqual(empty["extra_css"], [mkdocs_hook.CSS_URI])

    def test_a_site_build_renders_figures_and_publishes_the_stylesheet(self):
        """A real MkDocs build with only the hook listed: the fence becomes the figure with a base
        relative to its page, the CSS lands at CSS_URI, and every page links it."""
        try:
            from mkdocs.commands.build import build
            from mkdocs.config import load_config
        except ImportError:
            self.skipTest("mkdocs is not installed; the hook runs only inside MkDocs")
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            write(root / "docs/index.md", "# Home\n")
            write(root / "docs/guide.md", "# Guide\n\n" + FIGURE)
            write(root / "docs/assets/figures/demo.json", json.dumps(META))
            write(root / "mkdocs.yml", f"site_name: Fixture\nhooks:\n  - {(FIGURES / 'mkdocs_hook.py').as_posix()}\n")
            with contextlib.redirect_stderr(io.StringIO()):
                build(load_config(str(root / "mkdocs.yml"), site_dir=str(root / "site")))
            published = root / "site" / mkdocs_hook.CSS_URI
            self.assertEqual(published.read_bytes(), mkdocs_hook.CSS_FILE.read_bytes())
            self.assertIn(mkdocs_hook.CSS_URI, (root / "site/index.html").read_text(encoding="utf-8"))
            guide = (root / "site/guide/index.html").read_text(encoding="utf-8")
            self.assertIn('<img src="../assets/figures/demo.svg"', guide)
            self.assertNotIn("Open the interactive figure", guide)

    def test_on_page_markdown_warns_for_a_figure_it_cannot_render(self):
        class Page:
            url = "guide/"

            class file:  # noqa: N801 - the attribute MkDocs' Page carries
                src_uri = "guide.md"

        with (tempfile.TemporaryDirectory() as directory,
              self.assertLogs("mkdocs.plugins.praetor_figures", level="WARNING") as logs):
            text = mkdocs_hook.on_page_markdown("```figure\nnope\n```\n", Page, {"docs_dir": directory}, None)
        self.assertEqual(text, "```figure\nnope\n```\n")
        self.assertIn("guide.md: figure 'nope' has no", logs.output[0])

    def test_on_files_keeps_a_docs_file_at_the_same_path_and_warns(self):
        files = _Files([mkdocs_hook.CSS_URI])
        with self.assertLogs("mkdocs.plugins.praetor_figures", level="WARNING") as logs:
            self.assertIs(mkdocs_hook.on_files(files, {}), files)
        self.assertEqual(files.appended, [])
        self.assertIn("docs_dir already holds this path", logs.output[0])


if __name__ == "__main__":
    unittest.main()
