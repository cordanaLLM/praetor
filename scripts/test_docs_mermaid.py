#!/usr/bin/env python3
"""Tests for the Mermaid rendering check on the MkDocs site and preset."""

import contextlib
import io
import os
from pathlib import Path
import sys
import tempfile
import unittest

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

import docs_mermaid  # noqa: E402

ROOT = Path(__file__).resolve().parents[1]
FENCE_FORMAT = "!!python/name:pymdownx.superfences.fence_code_format"
DECLARED = f"""\
markdown_extensions:
  - attr_list
  - pymdownx.superfences:
      custom_fences:
        - name: mermaid
          class: mermaid
          format: {FENCE_FORMAT}
  - pymdownx.highlight:
      anchor_linenums: true
"""
DIAGRAM = "```mermaid\nflowchart TD\n    A --> B\n```\n"
RENDERED = '<pre class="mermaid"><code>flowchart TD\n    A --&gt; B</code></pre>'
LISTING = '<div class="highlight"><pre><span></span><code>flowchart TD</code></pre></div>'


def write(path, text):
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(text, encoding="utf-8")


class ConfigDeclaration(unittest.TestCase):
    def test_repository_configs_declare_the_mermaid_fence(self):
        """The site and the adopter preset: without the fence every diagram ships as code."""
        for config in ("mkdocs.yml", "docs/presets/mkdocs/mkdocs.yml"):
            with self.subTest(config=config):
                text = (ROOT / config).read_text(encoding="utf-8")
                self.assertIsNone(docs_mermaid.config_error(text))

    def test_declared_fence_passes(self):
        self.assertIsNone(docs_mermaid.config_error(DECLARED))
        self.assertEqual(docs_mermaid.declared_fences(DECLARED), [docs_mermaid.EXPECTED_FENCE])

    def test_bare_superfences_is_rejected(self):
        """The shape the site shipped with: superfences enabled, no custom fence."""
        bare = "markdown_extensions:\n  - md_in_html\n  - pymdownx.superfences\n  - pymdownx.highlight\n"
        self.assertIn("no custom fence", docs_mermaid.config_error(bare))
        self.assertIn("no custom fence", docs_mermaid.config_error(""))

    def test_wrong_format_or_class_is_rejected(self):
        """A div fence or another class is never drawn: Material only reads pre.mermaid."""
        for old, new in ((FENCE_FORMAT, FENCE_FORMAT.replace("code", "div")),
                         ("class: mermaid", "class: diagram"),
                         ("name: mermaid", "name: flow")):
            with self.subTest(changed=new):
                self.assertIsNotNone(docs_mermaid.config_error(DECLARED.replace(old, new)))

    def test_fence_under_another_extension_is_rejected(self):
        """custom_fences belongs to superfences; the same keys under a sibling do not count."""
        moved = DECLARED.replace("  - pymdownx.superfences:\n", "  - pymdownx.superfences\n"
                                 "  - pymdownx.tabbed:\n")
        self.assertIsNotNone(docs_mermaid.config_error(moved))

    def test_quoting_comments_and_key_order_are_accepted(self):
        text = (f"markdown_extensions:\n  - pymdownx.superfences:  # diagrams\n"
                f"      custom_fences:\n        # Material reference/diagrams\n"
                f"        - class: 'mermaid'\n          name: \"mermaid\"\n"
                f"          format: {FENCE_FORMAT}\n")
        self.assertIsNone(docs_mermaid.config_error(text))

    def test_second_fence_is_read_separately(self):
        extra = DECLARED.replace("          format: " + FENCE_FORMAT + "\n",
                                 "          format: " + FENCE_FORMAT + "\n"
                                 "        - name: math\n          class: arithmatex\n")
        self.assertEqual(len(docs_mermaid.declared_fences(extra)), 2)
        self.assertIsNone(docs_mermaid.config_error(extra))


class SourceFences(unittest.TestCase):
    def test_counts_mermaid_fences(self):
        self.assertEqual(docs_mermaid.mermaid_fences(DIAGRAM), 1)
        self.assertEqual(docs_mermaid.mermaid_fences(DIAGRAM + "\ntext\n\n" + DIAGRAM), 2)
        self.assertEqual(docs_mermaid.mermaid_fences("~~~ Mermaid\ngraph LR\n~~~\n"), 1)
        self.assertEqual(docs_mermaid.mermaid_fences("- item\n\n    " + DIAGRAM.replace("\n", "\n    ")), 1)

    def test_other_languages_and_prose_are_not_counted(self):
        self.assertEqual(docs_mermaid.mermaid_fences("```python\nprint('mermaid')\n```\n"), 0)
        self.assertEqual(docs_mermaid.mermaid_fences("Write `mermaid` diagrams.\n"), 0)
        self.assertEqual(docs_mermaid.mermaid_fences("```mermaidx\ngraph\n```\n"), 0)

    def test_fence_nested_in_a_longer_fence_is_literal(self):
        """A template showing a diagram's source renders as code by design."""
        nested = "````markdown\n" + DIAGRAM + "````\n"
        self.assertEqual(docs_mermaid.mermaid_fences(nested), 0)
        self.assertEqual(docs_mermaid.mermaid_fences(nested + DIAGRAM), 1)

    def test_closer_boundaries(self):
        # A closer with an info string does not close, so the second opener is content.
        self.assertEqual(docs_mermaid.mermaid_fences("```text\n```mermaid\n```\n" + DIAGRAM), 1)
        # A shorter closer does not close a longer opener.
        self.assertEqual(docs_mermaid.mermaid_fences("````\n```\n" + DIAGRAM + "````\n"), 0)
        # An unclosed fence still opened a block.
        self.assertEqual(docs_mermaid.mermaid_fences("```mermaid\ngraph LR\n"), 1)
        self.assertEqual(docs_mermaid.mermaid_fences(""), 0)

    def test_line_bound(self):
        with self.assertRaises(docs_mermaid.CheckError):
            docs_mermaid.mermaid_fences("\n" * (docs_mermaid.MAX_LINES + 1))


class RenderedDiagrams(unittest.TestCase):
    def test_quoted_minified_and_multi_class_pre_is_counted(self):
        self.assertEqual(docs_mermaid.rendered_diagrams(RENDERED), 1)
        self.assertEqual(docs_mermaid.rendered_diagrams("<pre class=mermaid><code>x</code></pre>"), 1)
        self.assertEqual(docs_mermaid.rendered_diagrams('<pre id="d" class="big mermaid">x</pre>'), 1)
        self.assertEqual(docs_mermaid.rendered_diagrams(RENDERED * 3), 3)

    def test_code_listing_and_look_alikes_are_not_counted(self):
        self.assertEqual(docs_mermaid.rendered_diagrams(LISTING), 0)
        self.assertEqual(docs_mermaid.rendered_diagrams('<div class="mermaid">graph</div>'), 0)
        self.assertEqual(docs_mermaid.rendered_diagrams('<pre class="mermaid-src">graph</pre>'), 0)
        self.assertEqual(docs_mermaid.rendered_diagrams('<code class="language-mermaid">x</code>'), 0)

    def test_empty_and_unclassed(self):
        self.assertEqual(docs_mermaid.rendered_diagrams(""), 0)
        self.assertEqual(docs_mermaid.rendered_diagrams("<pre class>x</pre><pre>y</pre>"), 0)


class PageOutput(unittest.TestCase):
    def test_directory_url_mapping(self):
        docs, site = Path("docs"), Path("site")
        cases = {
            "index.md": "site/index.html",
            "adr/README.md": "site/adr/index.html",
            "standards/hiss-spec.md": "site/standards/hiss-spec/index.html",
            "wiki/Home.md": "site/wiki/Home/index.html",
        }
        for page, output in cases.items():
            with self.subTest(page=page):
                self.assertEqual(docs_mermaid.page_output(docs, site, docs / page).as_posix(), output)


class EndToEnd(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        root = Path(self.tmp.name)
        self.config, self.docs, self.site = root / "mkdocs.yml", root / "docs", root / "site"
        write(self.config, DECLARED)
        write(self.docs / "index.md", "# Home\n")
        write(self.site / "index.html", "<html></html>")
        write(self.docs / "spec.md", "# Spec\n\n" + DIAGRAM)

    def run_main(self):
        stdout, stderr = io.StringIO(), io.StringIO()
        with contextlib.redirect_stdout(stdout), contextlib.redirect_stderr(stderr):
            code = docs_mermaid.main(["--config", str(self.config), "--docs", str(self.docs),
                                      "--site", str(self.site)])
        return code, stdout.getvalue() + stderr.getvalue()

    def test_rendered_site_passes(self):
        write(self.site / "spec" / "index.html", RENDERED)
        code, output = self.run_main()
        self.assertEqual(code, 0, output)
        self.assertIn("1 Mermaid block(s)", output)

    def test_code_listing_fails(self):
        """The published failure: the page exists, the diagram is a highlighted listing."""
        write(self.site / "spec" / "index.html", LISTING)
        code, output = self.run_main()
        self.assertEqual(code, 1, output)
        self.assertIn("0 rendered as diagrams", output)

    def test_missing_fence_declaration_fails_even_when_pages_render(self):
        write(self.site / "spec" / "index.html", RENDERED)
        write(self.config, "markdown_extensions:\n  - pymdownx.superfences\n")
        code, output = self.run_main()
        self.assertEqual(code, 1, output)
        self.assertIn("no custom fence", output)

    def test_missing_built_page_fails(self):
        code, output = self.run_main()
        self.assertEqual(code, 1, output)
        self.assertIn("wrote no", output)

    def test_excluded_and_diagram_free_pages_are_skipped(self):
        """MkDocs does not build dot-directories or templates/, so neither is expected."""
        write(self.site / "spec" / "index.html", RENDERED)
        write(self.docs / ".drafts" / "wip.md", DIAGRAM)
        write(self.docs / "templates" / "page.md", DIAGRAM)
        write(self.docs / "plain.md", "# Plain\n")
        code, output = self.run_main()
        self.assertEqual(code, 0, output)

    def test_unbuilt_site_or_missing_docs_is_a_usage_failure(self):
        (self.site / "index.html").unlink()
        self.assertEqual(self.run_main()[0], 2)
        write(self.site / "index.html", "<html></html>")
        self.docs = self.docs / "absent"
        self.assertEqual(self.run_main()[0], 2)

    def test_unreadable_config_is_a_usage_failure(self):
        self.config = self.config.with_name("missing.yml")
        code, output = self.run_main()
        self.assertEqual(code, 2, output)
        self.assertIn("cannot read", output)

    def test_repository_docs_carry_diagrams(self):
        """The rendered check on the real site is not vacuous: the spec page holds a diagram."""
        spec = (ROOT / "docs" / "standards" / "hiss-spec.md").read_text(encoding="utf-8")
        self.assertGreaterEqual(docs_mermaid.mermaid_fences(spec), 1)


if __name__ == "__main__":
    unittest.main()
