#!/usr/bin/env python3
"""The docs presets' JSON-LD names the site that renders it, never a project of its own.

docs/presets/mkdocs/overrides/main.html is the template for this repository's own site (the
root mkdocs.yml points theme.custom_dir at it) and the one adopters copy into their fork. It
used to hard-code the TechArticle author (an Organization with a URL on a domain that does not
resolve) and a SoftwareSourceCode block for this repository, so every site that rendered it
described this project. The template now reads every identity value from mkdocs.yml.

The source check runs everywhere. The rendered checks build a one-page site with MkDocs and
Material for MkDocs, and skip with the reason when either is not installed
(`pip install -r docs/presets/mkdocs/requirements.txt` provides both). The end-to-end
`seo audit` check also builds praetorctl from this checkout into a temporary directory, so it
never runs a stale bin/praetorctl, and skips when no Go toolchain is on PATH.
"""

import importlib.util
import json
from pathlib import Path
import os
import re
import shutil
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
# Compiling the whole CLI from a cold build cache takes longer than one MkDocs build.
GO_BUILD_TIMEOUT = 600
GO = shutil.which("go")
PLACEHOLDER_CONFIG = ("extra:\n  source_code:\n"
                      "    repository: https://git.example.org/acme/docs\n"
                      "    programming_language: PlaceholderLang\n")
PLACEHOLDER_PAGE = "---\ndescription: Placeholder page\n---\n# Welcome to example-org/example-repo\n"
HAS_MKDOCS = all(importlib.util.find_spec(name) is not None for name in ("mkdocs", "material"))
BASE_CONFIG = f"""\
site_name: Example Docs
site_url: https://docs.example.org/
theme:
  name: material
  custom_dir: {json.dumps(str(OVERRIDES))}
"""


def render(root, extra_config, index_md="# Welcome\n"):
    """Build a one-page site below `root` with `extra_config` appended; return its site dir."""
    (root / "docs").mkdir()
    (root / "docs" / "index.md").write_text(index_md, encoding="utf-8")
    (root / "mkdocs.yml").write_text(BASE_CONFIG + extra_config, encoding="utf-8")
    result = subprocess.run(
        [sys.executable, "-m", "mkdocs", "build", "--strict", "--quiet",
         "-f", str(root / "mkdocs.yml"), "-d", str(root / "site")],
        capture_output=True, text=True, timeout=BUILD_TIMEOUT, check=False)
    if result.returncode != 0:
        raise AssertionError(f"mkdocs build failed:\n{result.stdout}\n{result.stderr}")
    return root / "site"


def build(extra_config):
    """Build a one-page site with `extra_config` appended; return {page: [JSON-LD objects]}."""
    with tempfile.TemporaryDirectory() as tmp:
        root = Path(tmp)
        render(root, extra_config)
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

@unittest.skipUnless(HAS_MKDOCS and GO, "needs mkdocs, mkdocs-material and a Go toolchain on PATH; "
                     "pip install -r docs/presets/mkdocs/requirements.txt")
class PraetorctlSEOAudit(unittest.TestCase):
    """`praetorctl seo audit` fails on unedited preset placeholders unless allowed."""

    @classmethod
    def setUpClass(cls):
        cls.tmp = tempfile.TemporaryDirectory()
        exe = ".exe" if os.name == "nt" else ""
        cls.praetorctl = Path(cls.tmp.name) / f"praetorctl{exe}"
        result = subprocess.run(
            [GO, "build", "-o", str(cls.praetorctl), "./cmd/standardsctl"],
            cwd=ROOT, capture_output=True, text=True, timeout=GO_BUILD_TIMEOUT, check=False)
        if result.returncode != 0:
            cls.tmp.cleanup()
            raise AssertionError(f"go build failed:\n{result.stdout}\n{result.stderr}")

    @classmethod
    def tearDownClass(cls):
        cls.tmp.cleanup()

    def audit(self, *args):
        return subprocess.run([str(self.praetorctl), "seo", "audit", *args],
                              capture_output=True, text=True, timeout=BUILD_TIMEOUT, check=False)

    def test_placeholders_fail_unless_allowed(self):
        with tempfile.TemporaryDirectory() as tmp:
            site = render(Path(tmp), PLACEHOLDER_CONFIG, PLACEHOLDER_PAGE)
            self.assertTrue((site / "sitemap.xml").is_file(), "mkdocs wrote no sitemap.xml")
            strict = self.audit(str(site))
            self.assertNotEqual(strict.returncode, 0, strict.stdout + strict.stderr)
            self.assertIn("unedited PlaceholderLang placeholder", strict.stderr)
            self.assertIn("unedited example-org/example-repo placeholder", strict.stderr)
            lax = self.audit("--allow-placeholders", str(site))
            self.assertEqual(lax.returncode, 0, lax.stdout + lax.stderr)

    def test_edited_site_passes_without_the_flag(self):
        """Boundary: the same site with real values needs no --allow-placeholders."""
        config = PLACEHOLDER_CONFIG.replace("PlaceholderLang", "Rust")
        page = PLACEHOLDER_PAGE.replace("example-org/example-repo", "acme/docs")
        with tempfile.TemporaryDirectory() as tmp:
            clean = self.audit(str(render(Path(tmp), config, page)))
            self.assertEqual(clean.returncode, 0, clean.stdout + clean.stderr)

if __name__ == "__main__":
    unittest.main()
