#!/usr/bin/env python3
"""Tests for the diagram checker (tools/figures/docs_diagrams.py) and the MkDocs figures hook
(tools/figures/mkdocs_hook.py): Mermaid and figure rendering, figure sources, portable blocks."""

import contextlib
import hashlib
import io
import json
from pathlib import Path
import shutil
import sys
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[1]
FIGURES = ROOT / "tools" / "figures"
sys.path.insert(0, str(FIGURES))

import docs_diagrams  # noqa: E402
import mkdocs_hook  # noqa: E402
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


def mermaid_count(text):
    return len(docs_diagrams.fences(text, "mermaid"))


def write(path, text):
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(text, encoding="utf-8")


class ConfigDeclaration(unittest.TestCase):
    def test_preset_declares_the_mermaid_fence_and_the_site_does_not(self):
        """The adopter preset keeps Mermaid, so without the fence its diagram ships as code. The
        root site retired Mermaid for figures (ADR-0015, section 9) and declares no fence."""
        preset = (ROOT / "docs/presets/mkdocs/mkdocs.yml").read_text(encoding="utf-8")
        self.assertIsNone(docs_diagrams.config_error(preset))
        site = (ROOT / "mkdocs.yml").read_text(encoding="utf-8")
        self.assertIn("no custom fence", docs_diagrams.config_error(site))

    def test_declared_fence_passes(self):
        self.assertIsNone(docs_diagrams.config_error(DECLARED))
        self.assertEqual(docs_diagrams.declared_fences(DECLARED), [docs_diagrams.EXPECTED_FENCE])

    def test_bare_superfences_is_rejected(self):
        """The shape the site shipped with: superfences enabled, no custom fence."""
        bare = "markdown_extensions:\n  - md_in_html\n  - pymdownx.superfences\n  - pymdownx.highlight\n"
        self.assertIn("no custom fence", docs_diagrams.config_error(bare))
        self.assertIn("no custom fence", docs_diagrams.config_error(""))

    def test_wrong_format_or_class_is_rejected(self):
        """A div fence or another class is never drawn: Material only reads pre.mermaid."""
        for old, new in ((FENCE_FORMAT, FENCE_FORMAT.replace("code", "div")),
                         ("class: mermaid", "class: diagram"),
                         ("name: mermaid", "name: flow")):
            with self.subTest(changed=new):
                self.assertIsNotNone(docs_diagrams.config_error(DECLARED.replace(old, new)))

    def test_fence_under_another_extension_is_rejected(self):
        """custom_fences belongs to superfences; the same keys under a sibling do not count."""
        moved = DECLARED.replace("  - pymdownx.superfences:\n", "  - pymdownx.superfences\n"
                                 "  - pymdownx.tabbed:\n")
        self.assertIsNotNone(docs_diagrams.config_error(moved))

    def test_quoting_comments_and_key_order_are_accepted(self):
        text = (f"markdown_extensions:\n  - pymdownx.superfences:  # diagrams\n"
                f"      custom_fences:\n        # Material reference/diagrams\n"
                f"        - class: 'mermaid'\n          name: \"mermaid\"\n"
                f"          format: {FENCE_FORMAT}\n")
        self.assertIsNone(docs_diagrams.config_error(text))

    def test_second_fence_is_read_separately(self):
        extra = DECLARED.replace("          format: " + FENCE_FORMAT + "\n",
                                 "          format: " + FENCE_FORMAT + "\n"
                                 "        - name: math\n          class: arithmatex\n")
        self.assertEqual(len(docs_diagrams.declared_fences(extra)), 2)
        self.assertIsNone(docs_diagrams.config_error(extra))


class SourceFences(unittest.TestCase):
    def test_counts_mermaid_fences(self):
        self.assertEqual(mermaid_count(DIAGRAM), 1)
        self.assertEqual(mermaid_count(DIAGRAM + "\ntext\n\n" + DIAGRAM), 2)
        self.assertEqual(mermaid_count("~~~ Mermaid\ngraph LR\n~~~\n"), 1)
        self.assertEqual(mermaid_count("- item\n\n    " + DIAGRAM.replace("\n", "\n    ")), 1)

    def test_other_languages_and_prose_are_not_counted(self):
        self.assertEqual(mermaid_count("```python\nprint('mermaid')\n```\n"), 0)
        self.assertEqual(mermaid_count("Write `mermaid` diagrams.\n"), 0)
        self.assertEqual(mermaid_count("```mermaidx\ngraph\n```\n"), 0)

    def test_fence_nested_in_a_longer_fence_is_literal(self):
        """A template showing a diagram's source renders as code by design."""
        nested = "````markdown\n" + DIAGRAM + "````\n"
        self.assertEqual(mermaid_count(nested), 0)
        self.assertEqual(mermaid_count(nested + DIAGRAM), 1)

    def test_closer_boundaries(self):
        # A closer with an info string does not close, so the second opener is content.
        self.assertEqual(mermaid_count("```text\n```mermaid\n```\n" + DIAGRAM), 1)
        # A shorter closer does not close a longer opener.
        self.assertEqual(mermaid_count("````\n```\n" + DIAGRAM + "````\n"), 0)
        # An unclosed fence still opened a block.
        self.assertEqual(mermaid_count("```mermaid\ngraph LR\n"), 1)
        self.assertEqual(mermaid_count(""), 0)

    def test_line_bound(self):
        with self.assertRaises(docs_diagrams.CheckError):
            mermaid_count("\n" * (docs_diagrams.MAX_LINES + 1))


class RenderedDiagrams(unittest.TestCase):
    def test_quoted_minified_and_multi_class_pre_is_counted(self):
        self.assertEqual(docs_diagrams.rendered_diagrams(RENDERED), 1)
        self.assertEqual(docs_diagrams.rendered_diagrams("<pre class=mermaid><code>x</code></pre>"), 1)
        self.assertEqual(docs_diagrams.rendered_diagrams('<pre id="d" class="big mermaid">x</pre>'), 1)
        self.assertEqual(docs_diagrams.rendered_diagrams(RENDERED * 3), 3)

    def test_code_listing_and_look_alikes_are_not_counted(self):
        self.assertEqual(docs_diagrams.rendered_diagrams(LISTING), 0)
        self.assertEqual(docs_diagrams.rendered_diagrams('<div class="mermaid">graph</div>'), 0)
        self.assertEqual(docs_diagrams.rendered_diagrams('<pre class="mermaid-src">graph</pre>'), 0)
        self.assertEqual(docs_diagrams.rendered_diagrams('<code class="language-mermaid">x</code>'), 0)

    def test_empty_and_unclassed(self):
        self.assertEqual(docs_diagrams.rendered_diagrams(""), 0)
        self.assertEqual(docs_diagrams.rendered_diagrams("<pre class>x</pre><pre>y</pre>"), 0)


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
                self.assertEqual(docs_diagrams.page_output(docs, site, docs / page).as_posix(), output)


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
            code = docs_diagrams.main(["site", "--config", str(self.config), "--docs", str(self.docs),
                                      "--site", str(self.site)])
        return code, stdout.getvalue() + stderr.getvalue()

    def test_rendered_site_passes(self):
        write(self.site / "spec" / "index.html", RENDERED)
        code, output = self.run_main()
        self.assertEqual(code, 0, output)
        self.assertIn("1 diagram(s)", output)

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
        self.assertIn("does not enable mermaid diagrams", output)

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
        self.assertGreaterEqual(mermaid_count(spec) + len(docs_diagrams.figure_slugs(spec)), 1)


# ---------------------------------------------------------------------------------------------
# Figures
# ---------------------------------------------------------------------------------------------

HOOKED = DECLARED + "\nhooks:\n  - tools/figures/mkdocs_hook.py\n"
# The root site's shape: the figures hook, superfences without the mermaid fence.
FIGURES_ONLY = "hooks:\n  - tools/figures/mkdocs_hook.py\nmarkdown_extensions:\n  - pymdownx.superfences\n"
FIGURE = "```figure\ndemo\n```\n"
META = {"slug": "demo", "title": "Demo & co", "alt": 'A "demo" figure.', "text": ["A → B."],
        "width": 600.4, "height": 300, "static_width": 600, "static_height": 180,
        "evidence": ["src/app.go:Serve"]}


def sha(data):
    return hashlib.sha256(data).hexdigest()


class FigureRepo:
    """A throwaway repository with one consistent figure, built the way build.mjs builds one."""

    def __init__(self, root):
        self.root = root
        upstream = "tools/figures/third_party/interfig/upstream/src"
        shutil.copytree(ROOT / upstream, root / upstream)
        write(root / "tools/figures/core.mjs", (FIGURES / "core.mjs").read_text(encoding="utf-8"))
        write(root / "src/app.go", "package app\n\nfunc Serve() {}\n")
        write(root / "docs/figures/demo.ts", "export default {};\n")
        write(root / "docs/assets/figures/demo.svg", "<svg>animated</svg>\n")
        write(root / "docs/assets/figures/demo.static.svg", "<svg>still</svg>\n")
        write(root / "docs/index.md", "# Home\n")
        write(root / "mkdocs.yml", HOOKED)
        self.meta = dict(META)
        self.rebind()

    def rebind(self):
        figures = self.root / "docs/assets/figures"
        self.meta.update(
            spec_sha256=sha((self.root / "docs/figures/demo.ts").read_bytes()),
            svg_sha256=sha((figures / "demo.svg").read_bytes()),
            static_sha256=sha((figures / "demo.static.svg").read_bytes()),
            engine={"commit": "c", "sha256": docs_diagrams.engine_hash(self.root)},
        )
        write(figures / "demo.json", json.dumps(self.meta))

    def sources(self):
        return docs_diagrams.sources(self.root, Path("docs"), Path("mkdocs.yml"), Path("README.md"))


class ConfigKinds(unittest.TestCase):
    def test_hook_enables_figures_and_fence_enables_mermaid(self):
        self.assertEqual(docs_diagrams.enabled_kinds(HOOKED), {"mermaid", "figure"})
        self.assertEqual(docs_diagrams.enabled_kinds(DECLARED), {"mermaid"})
        self.assertEqual(docs_diagrams.enabled_kinds("hooks:\n  - ../../tools/figures/mkdocs_hook.py\n"), {"figure"})
        self.assertEqual(docs_diagrams.enabled_kinds(""), set())

    def test_only_the_figures_hook_path_enables_figures(self):
        """The hook is named by its directory and file: a same-named hook elsewhere enables nothing, and
        the path before the tree moved (scripts/mkdocs_figures_hook.py) no longer counts."""
        self.assertEqual(docs_diagrams.enabled_kinds("hooks:\n  - figures/mkdocs_hook.py\n"), {"figure"})
        for other in ("mkdocs_hook.py", "scripts/mkdocs_hook.py", "scripts/mkdocs_figures_hook.py",
                      "tools/figures/mkdocs_hook.pyc", "tools/figure/mkdocs_hook.py"):
            with self.subTest(hook=other):
                self.assertEqual(docs_diagrams.enabled_kinds(f"hooks:\n  - {other}\n"), set())

    def test_hook_list_forms(self):
        self.assertEqual(docs_diagrams.declared_hooks("hooks: [a.py, 'b.py']\n"), ["a.py", "b.py"])
        self.assertEqual(docs_diagrams.declared_hooks("hooks:\n  # note\n  - a.py\n  - \"b.py\"\nnav:\n  - x.md\n"),
                         ["a.py", "b.py"])
        self.assertEqual(docs_diagrams.declared_hooks("nav:\n  - hooks.md\n"), [])
        self.assertEqual(docs_diagrams.declared_hooks("hooks: []\n"), [])

    def test_repository_site_enables_only_figures_and_preset_only_mermaid(self):
        """Root: the figures hook without the mermaid fence. Preset: the mermaid fence, no hook."""
        self.assertEqual(docs_diagrams.enabled_kinds((ROOT / "mkdocs.yml").read_text(encoding="utf-8")), {"figure"})
        preset = (ROOT / "docs/presets/mkdocs/mkdocs.yml").read_text(encoding="utf-8")
        self.assertEqual(docs_diagrams.enabled_kinds(preset), {"mermaid"})
        self.assertEqual(docs_diagrams.enabled_kinds(FIGURES_ONLY), {"figure"})

    def test_disabled_kind_is_reported(self):
        page = Path("p.md")
        self.assertEqual(docs_diagrams.kind_errors(page, FIGURE + DIAGRAM, {"mermaid", "figure"}), [])
        errors = docs_diagrams.kind_errors(page, FIGURE + DIAGRAM, {"mermaid"})
        self.assertEqual(len(errors), 1)
        self.assertIn("does not enable figure diagrams", errors[0])
        # A nested fence is source text, so it needs no enabled kind.
        self.assertEqual(docs_diagrams.kind_errors(page, "````markdown\n" + FIGURE + "````\n", set()), [])

    def test_mermaid_on_a_figures_only_site_names_the_figure_fence(self):
        """The root site: a Mermaid fence fails and the finding says what to write instead."""
        errors = docs_diagrams.kind_errors(Path("p.md"), DIAGRAM, docs_diagrams.enabled_kinds(FIGURES_ONLY))
        self.assertEqual(len(errors), 1)
        self.assertIn("does not enable mermaid diagrams; draw it as a ```figure fence", errors[0])
        self.assertIn("docs/guides/figures.md", errors[0])
        # With neither kind enabled there is no replacement to name.
        bare = docs_diagrams.kind_errors(Path("p.md"), DIAGRAM, set())
        self.assertEqual(bare, ["p.md: ```mermaid fence, but the configuration does not enable mermaid diagrams"])
        # The preset keeps Mermaid, and a figure there names no replacement either.
        preset = docs_diagrams.kind_errors(Path("p.md"), FIGURE, {"mermaid"})
        self.assertNotIn("draw it as", preset[0])


class ExcludeDocs(unittest.TestCase):
    """The exclude_docs patterns MkDocs honours, so the checks skip what the site does not build."""

    def excluded(self, relative, *patterns):
        return docs_diagrams.is_excluded(Path(relative).parts, list(patterns))

    def test_patterns_are_read_from_a_block_scalar_or_one_line(self):
        block = "site_name: x\nexclude_docs: |\n  a/b/\n  # note\n\n  /c/\nnav:\n  - index.md\n"
        self.assertEqual(docs_diagrams.excluded_patterns(block), ["a/b/", "/c/"])
        self.assertEqual(docs_diagrams.excluded_patterns("exclude_docs: '/drafts/'  # wip\n"), ["/drafts/"])
        self.assertEqual(docs_diagrams.excluded_patterns("exclude_docs: >-\n  one.md\n"), ["one.md"])
        self.assertEqual(docs_diagrams.excluded_patterns("nav:\n  - exclude_docs.md\n"), [])
        self.assertEqual(docs_diagrams.excluded_patterns("exclude_docs:\n"), [])
        self.assertEqual(docs_diagrams.excluded_patterns(""), [])

    def test_anchored_unanchored_and_directory_patterns(self):
        self.assertTrue(self.excluded("presets/mkdocs/docs/index.md", "/presets/mkdocs/docs/"))
        self.assertTrue(self.excluded("presets/mkdocs/overrides/x.md", "presets/mkdocs/overrides/"))
        self.assertTrue(self.excluded("a/drafts/x.md", "drafts/"))
        self.assertTrue(self.excluded("guides/wip.md", "wip.md"))
        self.assertTrue(self.excluded("guides/x.tmp.md", "*.tmp.md"))
        # Anchored patterns match from docs_dir only.
        self.assertFalse(self.excluded("other/presets/mkdocs/docs/index.md", "/presets/mkdocs/docs/"))
        self.assertFalse(self.excluded("presets/mkdocs/README.md", "/presets/mkdocs/docs/"))
        # A directory pattern never matches a file of the same name.
        self.assertFalse(self.excluded("drafts", "drafts/"))
        self.assertFalse(self.excluded("guides/figures.md", "/figures/"))

    def test_wildcards_stay_within_one_path_component(self):
        """BUG-1030: `*`, `?` and `[...]` never match a `/`, as in gitignore and pathspec."""
        self.assertTrue(self.excluded("a/x.md", "/a/*.md"))
        self.assertTrue(self.excluded("a/b.md", "/a/?.md"))
        self.assertTrue(self.excluded("guides/x.md", "/*/x.md"))
        self.assertTrue(self.excluded("a/c/x.md", "/a/[bc]/"))
        self.assertFalse(self.excluded("a/b/x.md", "/a/*.md"))
        self.assertFalse(self.excluded("a/b.md", "/a?b.md"))
        self.assertFalse(self.excluded("a/b.md", "a?b.md"))
        self.assertFalse(self.excluded("b/a/x.md", "*/x.md"))
        self.assertFalse(self.excluded("a/b.md", "a[/]b.md"))
        # A matched directory excludes everything under it, however deep.
        self.assertTrue(self.excluded("a/b/c/x.md", "/a/*"))
        self.assertTrue(self.excluded("a/b/c/x.md", "/a/*/"))
        # gitignore negates a bracket with `^` as well as `!`.
        self.assertTrue(self.excluded("b.md", "[^a].md"))
        self.assertFalse(self.excluded("a.md", "[^a].md"))
        self.assertFalse(self.excluded("a.md", "[!a].md"))

    def test_a_leading_or_middle_slash_anchors(self):
        self.assertTrue(self.excluded("x.md", "/x.md"))
        self.assertFalse(self.excluded("sub/x.md", "/x.md"))
        self.assertTrue(self.excluded("sub/x.md", "x.md"))
        self.assertTrue(self.excluded("a/b.md", "a/b.md"))
        self.assertFalse(self.excluded("x/a/b.md", "a/b.md"))
        # Boundaries: a bare or doubled slash names no path; an empty component matches none.
        self.assertFalse(self.excluded("x.md", "/"))
        self.assertFalse(self.excluded("a/x.md", "//"))
        self.assertFalse(self.excluded("a/b.md", "a//b.md"))

    def test_matcher_agrees_with_the_pathspec_mkdocs_uses(self):
        """Replays a pattern and path matrix against pathspec's GitIgnoreSpec when it is installed."""
        try:
            from pathspec.gitignore import GitIgnoreSpec
        except ImportError:
            self.skipTest("pathspec is not installed (it comes with mkdocs)")
        patterns = ("/a/*.md", "/a/?.md", "a?b.md", "*/x.md", "/*/x.md", "/a/*", "/a/[bc]/", "[^a].md", "[!a].md",
                    "x.md", "/x.md", "a/b.md", "drafts/", "/presets/mkdocs/docs/", "*.tmp.md", "/", "a//b.md")
        paths = ("a/x.md", "a/b.md", "a/b/x.md", "a/c/x.md", "a/b/c/x.md", "b/a/x.md", "x.md", "sub/x.md", "x/a/b.md",
                 "a.md", "b.md", "drafts/x.md", "p/drafts/x.md", "drafts", "presets/mkdocs/docs/index.md",
                 "presets/mkdocs/README.md", "g/x.tmp.md")
        for pattern in patterns:
            spec = GitIgnoreSpec.from_lines([pattern])
            for path in paths:
                with self.subTest(pattern=pattern, path=path):
                    self.assertEqual(docs_diagrams.pattern_matches(Path(path).parts, pattern), spec.match_file(path))

    def test_mkdocs_defaults_always_apply(self):
        self.assertTrue(self.excluded(".drafts/wip.md"))
        self.assertTrue(self.excluded("guides/.hidden.md"))
        self.assertTrue(self.excluded("templates/page.md"))
        self.assertFalse(self.excluded("guides/templates/page.md"))
        self.assertFalse(self.excluded("index.md"))

    def test_unsupported_gitignore_syntax_is_refused(self):
        for pattern in ("!keep.md", "**/x.md", "a\\ b.md"):
            with self.subTest(pattern=pattern), self.assertRaises(docs_diagrams.CheckError):
                self.excluded("index.md", pattern)

    def test_repository_site_excludes_the_preset_docs_but_not_its_readme(self):
        docs = ROOT / "docs"
        patterns = docs_diagrams.excluded_patterns((ROOT / "mkdocs.yml").read_text(encoding="utf-8"))
        pages = {page.relative_to(docs).as_posix() for page in docs_diagrams.markdown_pages(docs, patterns)}
        self.assertNotIn("presets/mkdocs/docs/index.md", pages)
        self.assertIn("presets/mkdocs/README.md", pages)
        self.assertIn("wiki/Home.md", pages)


class FigureFences(unittest.TestCase):
    def test_slugs_and_nesting(self):
        self.assertEqual(docs_diagrams.figure_slugs(FIGURE + "\n```figure\n\n  other-one  \n```\n"), ["demo", "other-one"])
        self.assertEqual(docs_diagrams.figure_slugs("````markdown\n" + FIGURE + "````\n"), [])
        self.assertEqual(docs_diagrams.figure_slugs("```figure\n```\n"), [""])
        blocks = docs_diagrams.fence_blocks("text\n" + FIGURE + "```mermaid\nx\n")
        self.assertEqual([(b.start, b.end, b.info) for b in blocks], [(1, 4, "figure"), (4, 6, "mermaid")])

    def test_site_base_follows_page_depth(self):
        cases = {"": ".", "adr/": "..", "architecture/c4-models/": "../..", "a/b/c.html": "../.."}
        for url, base in cases.items():
            with self.subTest(url=url):
                self.assertEqual(docs_diagrams.site_base(url), base)

    def test_render_block_escapes_and_links(self):
        block = docs_diagrams.render_block("demo", "../..", META)
        self.assertIn('<figure class="praetor-figure" id="fig-demo" data-figure="demo" aria-describedby="fig-demo-text">', block)
        self.assertIn('<source media="(prefers-reduced-motion: reduce)" srcset="../../demo.static.svg" '
                      'width="600" height="180">', block)
        self.assertIn('alt="A &quot;demo&quot; figure." width="600" height="300" loading="lazy"', block)
        self.assertIn("<figcaption>Demo &amp; co</figcaption>", block)
        self.assertIn("<li>A → B.</li>", block)
        self.assertNotIn("<p><a", block)
        self.assertNotIn("\n\n", block)
        linked = docs_diagrams.render_block("demo", "https://x/assets/figures", META, "https://x/wiki/Home/#fig-demo")
        self.assertIn('<p><a href="https://x/wiki/Home/#fig-demo">Open the interactive figure</a></p>', linked)

    def test_each_image_carries_its_own_recorded_size(self):
        """BUG-1002: the static SVG is shorter than the animated one; a <source> without a size
        made the browser reserve the animated size and letterbox the static image."""
        block = docs_diagrams.render_block("demo", ".", META)
        source = next(line for line in block.split("\n") if line.startswith("<source"))
        image = next(line for line in block.split("\n") if line.startswith("<img"))
        self.assertIn('width="600" height="180"', source)
        self.assertIn('width="600" height="300"', image)
        # Boundary: equal sizes still go on both, so the choice never depends on a missing value.
        same = docs_diagrams.render_block("demo", ".", dict(META, static_height=300))
        self.assertEqual(same.count('width="600" height="300"'), 2)

    def test_a_json_without_an_image_size_is_refused(self):
        for missing in ({"static_width": None}, {"static_height": 0}, {"width": "wide"}, {"height": True}):
            meta = {key: value for key, value in dict(META, **missing).items() if value is not None}
            with self.subTest(missing=missing), self.assertRaises(docs_diagrams.CheckError) as raised:
                docs_diagrams.render_block("demo", ".", meta)
            self.assertIn("rebuild with", str(raised.exception))
        with tempfile.TemporaryDirectory() as directory:
            stale = {key: value for key, value in META.items() if not key.startswith("static_")}
            write(Path(directory) / "demo.json", json.dumps(stale))
            text, errors = docs_diagrams.expand(FIGURE, ".", Path(directory))
        self.assertEqual(text, FIGURE)
        self.assertIn("no positive static_width and static_height", errors[0])

    def test_expand_replaces_top_level_fences_only(self):
        with tempfile.TemporaryDirectory() as directory:
            figures = Path(directory)
            write(figures / "demo.json", json.dumps(META))
            source = "# Page\n\n" + FIGURE + "\n````markdown\n" + FIGURE + "````\n"
            text, errors = docs_diagrams.expand(source, "..", figures)
            self.assertEqual(errors, [])
            self.assertEqual(text.count('<figure class="praetor-figure"'), 1)
            self.assertIn("````markdown\n```figure\ndemo\n```\n````", text)
            indented, _ = docs_diagrams.expand("- item\n\n    " + FIGURE.replace("\n", "\n    "), ".", figures)
            self.assertIn("\n    <picture>", indented)

    def test_expand_keeps_line_separators_markdown_does_not_split_on(self):
        with tempfile.TemporaryDirectory() as directory:
            figures = Path(directory)
            write(figures / "demo.json", json.dumps(META))
            for separator in ("\x0c", " ", "\x85"):
                source = f"intro{separator}line\n\n" + FIGURE + "after\n"
                text, errors = docs_diagrams.expand(source, "..", figures)
                self.assertEqual(errors, [])
                self.assertTrue(text.startswith(f"intro{separator}line\n\n<figure"), repr(text[:40]))
                self.assertNotIn("```figure", text)
                self.assertTrue(text.endswith("</details>\nafter\n"), repr(text[-40:]))
            crlf, errors = docs_diagrams.expand("intro\r\n\r\n" + FIGURE.replace("\n", "\r\n") + "after\r\n", "..", figures)
            self.assertEqual(errors, [])
            self.assertNotIn("```figure", crlf)
            self.assertIn("after", crlf)

    def test_expand_leaves_an_unknown_figure_and_reports_it(self):
        with tempfile.TemporaryDirectory() as directory:
            text, errors = docs_diagrams.expand("```figure\nmissing\n```\n```figure\nBad Slug\n```\n", ".", Path(directory))
        self.assertEqual(text, "```figure\nmissing\n```\n```figure\nBad Slug\n```\n")
        self.assertEqual(len(errors), 2)
        self.assertIn("figure 'missing' has no", errors[0])
        self.assertIn("not lowercase kebab-case", errors[1])


class FigureSite(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        root = Path(self.tmp.name)
        self.config, self.docs, self.site = root / "mkdocs.yml", root / "docs", root / "site"
        write(self.config, HOOKED)
        write(self.docs / "index.md", "# Home\n")
        write(self.docs / "guide.md", "# Guide\n\n" + FIGURE)
        write(self.site / "index.html", "<html></html>")
        write(self.site / "assets/figures/demo.svg", "<svg/>")
        write(self.site / "assets/figures/demo.static.svg", "<svg/>")
        write(self.site / "assets/javascripts/figures/loader.js", "")
        write(self.site / "assets/javascripts/figures/registry.json", '{"demo": "specs/demo.js"}')
        self.page = self.site / "guide" / "index.html"
        self.loader = '<script src="../assets/javascripts/figures/loader.js" type="module"></script>'
        write(self.page, docs_diagrams.render_block("demo", "../assets/figures", META) + self.loader)

    def check(self):
        return docs_diagrams.check(self.config, self.docs, self.site)

    def test_rendered_figure_passes(self):
        self.assertEqual(self.check(), ([], 1))

    def test_figure_rendered_as_code_fails(self):
        write(self.page, '<pre><code class="language-figure">demo</code></pre>' + self.loader)
        errors, _ = self.check()
        self.assertIn("figure fence(s) ['demo'], but", errors[0])

    def test_figure_without_hook_fails_as_a_disabled_kind(self):
        write(self.config, DECLARED)
        errors, _ = self.check()
        self.assertTrue(any("does not enable figure diagrams" in e for e in errors), errors)

    def test_unresolved_image_fails(self):
        (self.site / "assets/figures/demo.static.svg").unlink()
        errors, _ = self.check()
        self.assertEqual(len(errors), 1)
        self.assertIn("demo.static.svg does not resolve", errors[0])

    def test_absolute_image_url_fails(self):
        write(self.page, docs_diagrams.render_block("demo", "https://example.org", META) + self.loader)
        errors, _ = self.check()
        self.assertEqual(len(errors), 2)

    def test_page_without_loader_fails(self):
        write(self.page, docs_diagrams.render_block("demo", "../assets/figures", META))
        errors, _ = self.check()
        self.assertIn("loads no assets/javascripts/figures/loader.js", errors[0])

    def test_registry_must_list_every_slug(self):
        write(self.site / "assets/javascripts/figures/registry.json", "{}")
        self.assertIn("does not list figure 'demo'", self.check()[0][0])
        (self.site / "assets/javascripts/figures/registry.json").unlink()
        self.assertIn("registry.json is missing", self.check()[0][0])

    def test_mermaid_on_the_figures_only_site_fails_with_the_figure_fence_named(self):
        """The root site's shape: a Mermaid fence fails even when the page drew it."""
        write(self.config, FIGURES_ONLY)
        self.assertEqual(self.check(), ([], 1))
        write(self.docs / "old.md", DIAGRAM)
        write(self.site / "old" / "index.html", RENDERED)
        errors, _ = self.check()
        self.assertEqual(len(errors), 1, errors)
        self.assertIn("draw it as a ```figure fence", errors[0])

    def test_pages_the_configuration_excludes_are_skipped(self):
        """The preset's docs_dir sits inside the root docs_dir, excluded, with its Mermaid page."""
        write(self.docs / "preset/docs/index.md", DIAGRAM)
        write(self.config, FIGURES_ONLY + "exclude_docs: |\n  /preset/docs/\n")
        self.assertEqual(self.check(), ([], 1))
        # Not excluded: a disabled kind, and a page MkDocs was expected to write but did not.
        write(self.config, FIGURES_ONLY)
        errors, _ = self.check()
        self.assertEqual(len(errors), 2, errors)
        self.assertIn("does not enable mermaid diagrams", errors[0])
        self.assertIn("MkDocs wrote no", errors[1])

    def test_nested_figure_fence_needs_no_render(self):
        write(self.docs / "guide.md", "# Guide\n\n````markdown\n" + FIGURE + "````\n")
        write(self.page, "<pre><code>```figure</code></pre>")
        self.assertEqual(self.check(), ([], 0))


class FigureSources(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.repo = FigureRepo(Path(self.tmp.name))

    def test_consistent_figure_passes(self):
        self.assertEqual(self.repo.sources(), [])

    def test_edited_spec_is_stale(self):
        write(self.repo.root / "docs/figures/demo.ts", "export default { changed: true };\n")
        errors = self.repo.sources()
        self.assertEqual(len(errors), 1)
        self.assertIn("is stale: the spec docs/figures/demo.ts changed; rebuild with: node tools/figures/build.mjs build",
                      errors[0])

    def test_hand_edited_svg_is_stale(self):
        write(self.repo.root / "docs/assets/figures/demo.svg", "<svg>edited</svg>\n")
        self.assertIn("the SVG docs/assets/figures/demo.svg changed", self.repo.sources()[0])

    def test_engine_change_is_stale(self):
        svg = self.repo.root / "tools/figures/third_party/interfig/upstream/src/svg.ts"
        svg.write_text(svg.read_text(encoding="utf-8") + "// local patch\n", encoding="utf-8")
        self.assertIn("the figure engine changed", self.repo.sources()[0])

    def test_missing_svg(self):
        (self.repo.root / "docs/assets/figures/demo.static.svg").unlink()
        self.assertIn("docs/assets/figures/demo.static.svg is missing", self.repo.sources()[0])

    def test_orphan_json_and_spec_without_json(self):
        write(self.repo.root / "docs/assets/figures/gone.json", "{}")
        write(self.repo.root / "docs/figures/new-one.ts", "export default {};\n")
        errors = self.repo.sources()
        self.assertIn("docs/figures/new-one.ts has no docs/assets/figures/new-one.json", errors[0])
        self.assertIn("docs/assets/figures/gone.json has no spec docs/figures/gone.ts", errors[1])

    def test_fence_naming_an_unknown_figure(self):
        write(self.repo.root / "docs/page.md", "```figure\nnope\n```\n" + FIGURE)
        errors = self.repo.sources()
        self.assertEqual(len(errors), 1)
        self.assertIn("names 'nope', which has no spec and JSON", errors[0])

    def test_disabled_kind_in_sources(self):
        write(self.repo.root / "mkdocs.yml", DECLARED)
        write(self.repo.root / "docs/page.md", FIGURE)
        self.assertIn("does not enable figure diagrams", self.repo.sources()[0])

    def test_mermaid_fails_on_a_figures_only_site_unless_excluded(self):
        write(self.repo.root / "mkdocs.yml", FIGURES_ONLY)
        write(self.repo.root / "docs/presets/demo/docs/index.md", DIAGRAM)
        errors = self.repo.sources()
        self.assertEqual(len(errors), 1)
        self.assertIn("draw it as a ```figure fence", errors[0])
        write(self.repo.root / "mkdocs.yml", FIGURES_ONLY + "exclude_docs: |\n  /presets/demo/docs/\n")
        self.assertEqual(self.repo.sources(), [])

    def test_json_without_the_static_size_is_reported(self):
        del self.repo.meta["static_height"]
        self.repo.rebind()
        errors = self.repo.sources()
        self.assertEqual(len(errors), 1)
        self.assertIn("demo.json: figure 'demo': its JSON records no positive static_width and static_height",
                      errors[0])

    def test_evidence_must_resolve(self):
        self.repo.meta["evidence"] = ["src/app.go:Serve", "src/app.go:Handle", "src/gone.go:X", "../outside.go:Y"]
        self.repo.rebind()
        errors = self.repo.sources()
        self.assertEqual(len(errors), 3)
        self.assertIn("Handle no longer occurs in src/app.go", errors[0])
        self.assertIn("'src/gone.go:X' names no file", errors[1])
        self.assertIn("'../outside.go:Y' names no file", errors[2])

    def test_readme_block_matches_the_renderer(self):
        readme = self.repo.root / "README.md"
        write(readme, "# Project\n\n<!-- figure:demo -->\nstale\n<!-- /figure -->\n")
        self.assertIn("portable figure block differs from the renderer", self.repo.sources()[0])
        self.assertEqual(docs_diagrams.portable([readme], None, self.repo.root), [])
        text = readme.read_text(encoding="utf-8")
        self.assertIn('<img src="docs/assets/figures/demo.svg"', text)
        self.assertEqual(self.repo.sources(), [])
        write(readme, "<!-- figure:nope -->\nx\n<!-- /figure -->\n")
        self.assertIn("figure 'nope' has no", self.repo.sources()[0])

    def test_engine_files_come_from_core_mjs(self):
        """core.mjs holds the one list; the command-line wrapper build.mjs is not hashed."""
        self.assertEqual(docs_diagrams.engine_files(ROOT), [
            "tools/figures/third_party/interfig/upstream/src/svg.ts",
            "tools/figures/third_party/interfig/upstream/src/geometry.ts",
            "tools/figures/third_party/interfig/upstream/src/model.ts",
            "tools/figures/core.mjs",
        ])
        write(self.repo.root / "tools/figures/core.mjs", "// no list\n")
        with self.assertRaises(docs_diagrams.CheckError):
            docs_diagrams.engine_files(self.repo.root)

    def test_wrapper_edit_keeps_figures_current(self):
        """Editing build.mjs, which is not in ENGINE_FILES, leaves the figure consistent."""
        write(self.repo.root / "tools/figures/build.mjs", "// an edited wrapper\n")
        self.assertEqual(self.repo.sources(), [])

    def test_python_and_node_agree_on_the_engine_hash(self):
        """The committed JSON was written by build.mjs; the checker must compute the same value."""
        meta = json.loads((ROOT / "docs/assets/figures/gating-pipeline.json").read_text(encoding="utf-8"))
        self.assertEqual(meta["engine"]["sha256"], docs_diagrams.engine_hash(ROOT))

    def test_repository_sources_are_consistent(self):
        self.assertEqual(docs_diagrams.main(["sources", "--root", str(ROOT)]), 0)


class _Files:
    """The part of MkDocs' Files the hook reads before it appends: src_uris and append."""

    def __init__(self, uris=()):
        self.src_uris = {uri: object() for uri in uris}
        self.appended = []

    def append(self, file):
        self.appended.append(file)
        self.src_uris[file.src_uri] = file


class HookStylesheet(unittest.TestCase):
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

    def test_a_site_build_publishes_and_links_the_stylesheet(self):
        """A real MkDocs build with only the hook listed: the CSS lands at CSS_URI and every page links it."""
        try:
            from mkdocs.commands.build import build
            from mkdocs.config import load_config
        except ImportError:
            self.skipTest("mkdocs is not installed; the hook runs only inside MkDocs")
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            write(root / "docs/index.md", "# Home\n")
            write(root / "mkdocs.yml", f"site_name: Fixture\nhooks:\n  - {(FIGURES / 'mkdocs_hook.py').as_posix()}\n")
            with contextlib.redirect_stderr(io.StringIO()):
                build(load_config(str(root / "mkdocs.yml"), site_dir=str(root / "site")))
            published = root / "site" / mkdocs_hook.CSS_URI
            self.assertEqual(published.read_bytes(), mkdocs_hook.CSS_FILE.read_bytes())
            self.assertIn(mkdocs_hook.CSS_URI, (root / "site/index.html").read_text(encoding="utf-8"))

    def test_on_files_keeps_a_docs_file_at_the_same_path_and_warns(self):
        files = _Files([mkdocs_hook.CSS_URI])
        with self.assertLogs("mkdocs.plugins.praetor_figures", level="WARNING") as logs:
            self.assertIs(mkdocs_hook.on_files(files, {}), files)
        self.assertEqual(files.appended, [])
        self.assertIn("docs_dir already holds this path", logs.output[0])


class Portable(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)
        write(self.root / "docs/assets/figures/demo.json", json.dumps(META))

    def test_wiki_page_gets_absolute_images_and_an_interactive_link(self):
        page = self.root / "clone" / "Home.md"
        write(page, "# Home\n\n" + FIGURE)
        self.assertEqual(docs_diagrams.portable([page], "https://example.org/praetor/", self.root), [])
        text = page.read_text(encoding="utf-8")
        self.assertIn('<img src="https://example.org/praetor/assets/figures/demo.svg"', text)
        self.assertIn('href="https://example.org/praetor/wiki/Home/#fig-demo"', text)
        self.assertNotIn("```figure", text)

    def test_one_unknown_figure_writes_nothing(self):
        good, bad = self.root / "Good.md", self.root / "Bad.md"
        write(good, FIGURE)
        write(bad, "```figure\nnope\n```\n")
        errors = docs_diagrams.portable([good, bad], "https://example.org/", self.root)
        self.assertEqual(len(errors), 1)
        self.assertEqual(good.read_text(encoding="utf-8"), FIGURE)

    def test_page_without_figures_is_unchanged(self):
        page = self.root / "Plain.md"
        write(page, "# Plain\n")
        self.assertEqual(docs_diagrams.portable([page], "https://example.org/", self.root), [])
        self.assertEqual(page.read_text(encoding="utf-8"), "# Plain\n")

    def test_cli_requires_a_mode(self):
        with contextlib.redirect_stderr(io.StringIO()), self.assertRaises(SystemExit):
            docs_diagrams.main(["portable", "README.md"])


if __name__ == "__main__":
    unittest.main()
