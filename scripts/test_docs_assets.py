#!/usr/bin/env python3
"""Tracked images hold the format their extension names, and referenced images exist.

Five files under docs/assets/ were named .svg while holding PNG bytes, so every renderer that
trusted the extension (a browser serving image/svg+xml, an SVG optimiser, a reviewer) was told
the wrong format. This pins both halves: the bytes of every tracked image match its extension,
and every image the README, the docs landing page and the MkDocs theme name is one of them.
"""

from pathlib import Path
import re
import subprocess
import unittest

ROOT = Path(__file__).resolve().parents[1]
IMAGE_SUFFIXES = (".png", ".svg", ".jpg", ".jpeg", ".gif", ".webp", ".ico")
SIGNATURES = {
    ".png": (b"\x89PNG\r\n\x1a\n",),
    ".jpg": (b"\xff\xd8\xff",),
    ".jpeg": (b"\xff\xd8\xff",),
    ".gif": (b"GIF87a", b"GIF89a"),
    ".ico": (b"\x00\x00\x01\x00",),
}
# An SVG may open with a byte-order mark, an XML prolog, comments or a doctype before <svg.
SVG_HEAD_BYTES = 4096
MAX_IMAGES = 256
MAX_IMAGE_BYTES = 16 * 1024 * 1024
# (Markdown file, directory its relative image paths resolve against)
PAGES = (("README.md", ""), ("docs/index.md", "docs"))
HTML_IMAGE = re.compile(r'<img\s[^>]*?src="([^"]+)"')
MARKDOWN_IMAGE = re.compile(r"!\[[^\]]*\]\(([^)\s]+)")
THEME_IMAGE = re.compile(r"^\s+(?:favicon|logo):\s*(\S+)\s*$", re.M)


def format_error(name, data):
    """Why `data` is not the image format `name`'s extension names, or None when it is."""
    suffix = Path(name).suffix.lower()
    if suffix == ".svg":
        head = data[:SVG_HEAD_BYTES].removeprefix(b"\xef\xbb\xbf").lstrip()
        if head.startswith(b"<") and b"<svg" in head:
            return None
        return f"{name} is named .svg but does not hold SVG markup"
    if suffix == ".webp":
        if data[:4] == b"RIFF" and data[8:12] == b"WEBP":
            return None
        return f"{name} is named .webp but does not hold WebP data"
    signatures = SIGNATURES.get(suffix)
    if signatures is None:
        return f"{name} has an extension this check does not know"
    if data.startswith(signatures):
        return None
    return f"{name} is named {suffix} but does not start with that format's signature"


def tracked_images():
    """Repository-relative names of every tracked image file."""
    patterns = [f"*{suffix}" for suffix in IMAGE_SUFFIXES]
    result = subprocess.run(["git", "ls-files", "-z", "--", *patterns], cwd=ROOT,
                            capture_output=True, timeout=20, check=True)
    names = [name for name in result.stdout.decode().split("\0") if name]
    if len(names) > MAX_IMAGES:
        raise ValueError(f"more than {MAX_IMAGES} tracked images")
    return names


def local_references():
    """(referring file, repository-relative image path) for every local image reference."""
    references = []
    for page, base in PAGES:
        text = (ROOT / page).read_text(encoding="utf-8")
        for target in HTML_IMAGE.findall(text) + MARKDOWN_IMAGE.findall(text):
            references.append((page, target, base))
    theme = (ROOT / "mkdocs.yml").read_text(encoding="utf-8")
    references.extend(("mkdocs.yml", target, "docs") for target in THEME_IMAGE.findall(theme))
    return [(page, Path(base, target).as_posix()) for page, target, base in references
            if not target.startswith(("http://", "https://", "data:", "//"))]


class DocsAssets(unittest.TestCase):
    def test_every_tracked_image_holds_the_format_its_extension_names(self):
        names = tracked_images()
        self.assertTrue(names, "no tracked images found; the check would pass vacuously")
        for name in names:
            with self.subTest(image=name):
                path = ROOT / name
                self.assertLessEqual(path.stat().st_size, MAX_IMAGE_BYTES)
                self.assertIsNone(format_error(name, path.read_bytes()))

    def test_every_referenced_image_is_a_tracked_image(self):
        references = local_references()
        pages = {page for page, _ in references}
        self.assertEqual(pages, {"README.md", "docs/index.md", "mkdocs.yml"},
                         "a page lost every image reference; the parser no longer sees them")
        tracked = set(tracked_images())
        for page, target in references:
            with self.subTest(page=page, image=target):
                self.assertIn(target, tracked)

    def test_mislabelled_image_bytes_are_rejected(self):
        png = b"\x89PNG\r\n\x1a\n" + b"\x00" * 16
        svg = b'<svg xmlns="http://www.w3.org/2000/svg"/>'
        self.assertIn("does not hold SVG markup", format_error("logo.svg", png))
        self.assertIn("signature", format_error("logo.png", svg))
        self.assertIn("signature", format_error("photo.jpg", png))
        self.assertIn("WebP", format_error("hero.webp", png))
        self.assertIn("does not know", format_error("icon.bmp", png))

    def test_format_boundaries(self):
        self.assertIsNone(format_error("exact.png", b"\x89PNG\r\n\x1a\n"))
        self.assertIsNotNone(format_error("short.png", b"\x89PNG\r\n\x1a"))
        for name in ("empty.png", "empty.svg", "empty.webp"):
            self.assertIsNotNone(format_error(name, b""))
        prolog = b'\xef\xbb\xbf<?xml version="1.0"?>\n<!-- art -->\n<svg viewBox="0 0 1 1"/>'
        self.assertIsNone(format_error("prolog.SVG", prolog))
        late = b"<?xml version='1.0'?>" + b" " * SVG_HEAD_BYTES + b"<svg/>"
        self.assertIsNotNone(format_error("late.svg", late))
        self.assertIsNone(format_error("still.webp", b"RIFF\x00\x00\x00\x00WEBPVP8 "))


if __name__ == "__main__":
    unittest.main()
