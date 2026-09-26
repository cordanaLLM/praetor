#!/usr/bin/env python3
"""The docs presets' JSON-LD names the site that renders it, never a project of its own.

docs/presets/mkdocs/overrides/main.html is the template for this repository's own site (the
root mkdocs.yml points theme.custom_dir at it) and the one adopters copy into their fork. It
used to hard-code the TechArticle author (an Organization with a URL on a domain that does not
resolve) and a SoftwareSourceCode block for this repository, so every site that rendered it
described this project. The template now reads every identity value from mkdocs.yml.

The source check runs everywhere. The rendered checks build a one-page site with MkDocs and
Material for MkDocs, and skip with the reason when either is not installed
(`pip install -r docs/presets/mkdocs/requirements.txt` provides both).
"""

import importlib.util
import json
from pathlib import Path
import re
import subprocess
import sys
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[1]
OVERRIDES = ROOT / "docs" / "presets" / "mkdocs" / "overrides"
TEMPLATES = (OVERRIDES / "main.html",
             ROOT / "docs" / "presets" / "starlight" / "src" / "components" / "SEOHead.astro")
# Values only a template that names a project of its own would carry.
IDENTITY_MARKERS = ("cordana", "github.com", "spdx.org")
JSONLD = re.compile(r'<script type="?application/ld\+json"?>(.*?)</script>', re.S)
BUILD_TIMEOUT = 180
HAS_MKDOCS = all(importlib.util.find_spec(name) is not None for name in ("mkdocs", "material"))
BASE_CONFIG = f"""\
site_name: Example Docs
site_url: https://docs.example.org/
theme:
  name: material
  custom_dir: {json.dumps(str(OVERRIDES))}
"""


def build(extra_config):
    """Build a one-page site with `extra_config` appended; return {page: [JSON-LD objects]}."""
    with tempfile.TemporaryDirectory() as tmp:
        root = Path(tmp)
        (root / "docs").mkdir()
        (root / "docs" / "index.md").write_text("# Welcome\n", encoding="utf-8")
        (root / "mkdocs.yml").write_text(BASE_CONFIG + extra_config, encoding="utf-8")
        result = subprocess.run(
            [sys.executable, "-m", "mkdocs", "build", "--strict", "--quiet",
             "-f", str(root / "mkdocs.yml"), "-d", str(root / "site")],
            capture_output=True, text=True, timeout=BUILD_TIMEOUT, check=False)
        if result.returncode != 0:
            raise AssertionError(f"mkdocs build failed:\n{result.stdout}\n{result.stderr}")
        pages = {}
        for name in ("index.html", "404.html"):
            html = (root / "site" / name).read_text(encoding="utf-8")
            pages[name] = [json.loads(block) for block in JSONLD.findall(html)]
        return pages


def of_type(blocks, schema_type):
    return [block for block in blocks if block.get("@type") == schema_type]


class TemplateSource(unittest.TestCase):
    def test_templates_name_no_project(self):
        """Author, repository and license come from the site's config, not the template."""
        for template in TEMPLATES:
            text = template.read_text(encoding="utf-8").lower()
            for marker in IDENTITY_MARKERS:
                with self.subTest(template=template.name, marker=marker):
                    self.assertNotIn(marker, text)


@unittest.skipUnless(HAS_MKDOCS, "mkdocs and mkdocs-material are not installed; "
                     "pip install -r docs/presets/mkdocs/requirements.txt")
class RenderedMkDocsTemplate(unittest.TestCase):
    def assert_no_foreign_identity(self, pages):
        rendered = json.dumps(pages).lower()
        self.assertNotIn("cordana", rendered)
        self.assertNotIn("praetor", rendered)

    def test_author_is_site_author_and_language_gates_source_code(self):
        """repo_url alone does not emit SoftwareSourceCode: its language would be a guess."""
        pages = build("site_author: Jane Doe\nrepo_url: https://git.example.org/acme/docs\n")
        articles = of_type(pages["index.html"], "TechArticle")
        self.assertEqual(len(articles), 1)
        self.assertEqual(articles[0]["author"], "Jane Doe")
        self.assertEqual(of_type(pages["index.html"], "SoftwareSourceCode"), [])
        # 404.html has no page, so no TechArticle; without SoftwareSourceCode it has no JSON-LD.
        self.assertEqual(pages["404.html"], [])
        self.assert_no_foreign_identity(pages)

    def test_author_falls_back_to_site_name_and_source_code_reads_extra(self):
        pages = build("extra:\n  source_code:\n"
                      "    name: acme/docs\n"
                      "    repository: https://git.example.org/acme/docs\n"
                      "    programming_language: Rust\n"
                      "    runtime_platform: Linux\n"
                      "    license: https://example.org/license\n")
        self.assertEqual(of_type(pages["index.html"], "TechArticle")[0]["author"], "Example Docs")
        for name in ("index.html", "404.html"):
            with self.subTest(page=name):
                code = of_type(pages[name], "SoftwareSourceCode")
                self.assertEqual(len(code), 1)
                self.assertEqual(code[0], {
                    "@context": "https://schema.org",
                    "@type": "SoftwareSourceCode",
                    "name": "acme/docs",
                    "programmingLanguage": "Rust",
                    "runtimePlatform": "Linux",
                    "license": "https://example.org/license",
                    "codeRepository": "https://git.example.org/acme/docs",
                })
        self.assert_no_foreign_identity(pages)

    def test_source_code_minimum_uses_repo_url_and_site_name(self):
        """Only the two required values set: optional fields are left out, not written empty."""
        pages = build("repo_url: https://git.example.org/acme/docs\n"
                      "extra:\n  source_code:\n    programming_language: Python\n")
        code = of_type(pages["404.html"], "SoftwareSourceCode")
        self.assertEqual(code, [{
            "@context": "https://schema.org",
            "@type": "SoftwareSourceCode",
            "name": "Example Docs",
            "programmingLanguage": "Python",
            "codeRepository": "https://git.example.org/acme/docs",
        }])
        self.assert_no_foreign_identity(pages)

    def test_praetorctl_seo_audit_validates_placeholders_end_to_end(self):
        """The CLI fails on PlaceholderLang/example-org unless --allow-placeholders is set."""
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            (root / "docs").mkdir()
            (root / "docs" / "index.md").write_text("---\ndescription: test desc\n---\n# Welcome to example-org/example-repo", encoding="utf-8")
            # Build with PlaceholderLang to ensure it's in the output.
            (root / "mkdocs.yml").write_text(BASE_CONFIG + "extra:\n  source_code:\n    repository: https://git.example.org/acme/docs\n    programming_language: PlaceholderLang\n", encoding="utf-8")

            subprocess.run([sys.executable, "-m", "mkdocs", "build", "--strict", "--quiet",
                            "-f", str(root / "mkdocs.yml"), "-d", str(root / "site")], check=True)

            # The test preset does not generate a sitemap by default (mkdocs needs a real URL or plugin sometimes, or it just generates one).
            # We'll just provide a dummy one if it doesn't exist so audit passes the sitemap check.
            if not (root / "site" / "sitemap.xml").exists():
                (root / "site" / "sitemap.xml").write_text('<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9"><url><loc>https://docs.example.org/</loc></url></urlset>', encoding="utf-8")

            praetorctl = ROOT / "bin" / "praetorctl"
            result = subprocess.run([str(praetorctl), "seo", "audit", str(root / "site")], capture_output=True, text=True)
            self.assertNotEqual(result.returncode, 0)
            self.assertIn("PlaceholderLang", result.stderr)
            self.assertIn("example-org/example-repo", result.stderr)

            result_lax = subprocess.run([str(praetorctl), "seo", "audit", "--allow-placeholders", str(root / "site")], capture_output=True, text=True)
            self.assertEqual(result_lax.returncode, 0)


if __name__ == "__main__":
    unittest.main()
