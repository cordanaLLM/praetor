"""Tests for the MkDocs figures hook (tools/figures/mkdocs_hook.py): its fence scanner, which replays
the fixtures the Node checks replay, the slot filling, the fence expansion, and the stylesheet and
player files it publishes. The exclude_docs fixture the Node matcher replays is also replayed here
against pathspec, the matcher MkDocs uses, so the recorded matrix cannot drift from it.

The figure checks themselves are Node (tools/figures/checks.mjs) and are tested by
tools/figures/checks.test.mjs."""

import ast
import contextlib
import gzip
import hashlib
import io
import json
import sys
import tarfile
import tempfile
import unittest
from pathlib import Path
from unittest import mock
from urllib.error import HTTPError

FIGURES = Path(__file__).resolve().parent
ROOT = FIGURES.parents[1]
sys.path.insert(0, str(FIGURES))

import mkdocs_hook

FIGURE = "```figure\ndemo\n```\n"
# The fixtures tools/figures/checks.test.mjs replays against checks.mjs.
FENCES = json.loads((FIGURES / "fence-fixtures.json").read_text(encoding="utf-8"))
MARKUP = json.loads((FIGURES / "markup-fixtures.json").read_text(encoding="utf-8"))
EXCLUDE = json.loads((FIGURES / "exclude-fixtures.json").read_text(encoding="utf-8"))
META = dict(MARKUP["meta"], html=MARKUP["html"])


def write(path, text):
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(text, encoding="utf-8")


class SelfContained(unittest.TestCase):
    def test_the_hook_imports_only_the_standard_library_and_mkdocs(self):
        """ADR-0016, section 6: the hook imports no checker, so a site build needs Python and MkDocs only."""
        tree = ast.parse((FIGURES / "mkdocs_hook.py").read_text(encoding="utf-8"))
        imported = {
            alias.name.split(".")[0]
            for node in ast.walk(tree)
            if isinstance(node, ast.Import)
            for alias in node.names
        }
        imported |= {
            node.module.split(".")[0]
            for node in ast.walk(tree)
            if isinstance(node, ast.ImportFrom) and node.module
        }
        foreign = {
            name
            for name in imported
            if name not in sys.stdlib_module_names
            and name not in ("__future__", "mkdocs")
        }
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
                blocks = [
                    [
                        b.start,
                        b.end,
                        b.indent,
                        b.info,
                        b.body,
                        mkdocs_hook.figure_slug(b.body),
                    ]
                    for b in mkdocs_hook.fence_blocks(case["markdown"])
                ]
                self.assertEqual(blocks, case["blocks"])

    def test_line_bound(self):
        self.assertEqual(
            mkdocs_hook.bounded_lines("\n" * mkdocs_hook.MAX_LINES),
            [""] * mkdocs_hook.MAX_LINES,
        )
        with self.assertRaises(mkdocs_hook.CheckError):
            mkdocs_hook.bounded_lines("\n" * (mkdocs_hook.MAX_LINES + 1))

    def test_site_base_follows_page_depth(self):
        cases = {
            "": ".",
            "adr/": "..",
            "architecture/c4-models/": "../..",
            "a/b/c.html": "../..",
        }
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
                self.assertEqual(
                    mkdocs_hook.render_block(META, case["base"]), case["html"]
                )

    def test_only_the_link_line_is_dropped(self):
        block = mkdocs_hook.render_block(META, ".")
        self.assertNotIn("<p><a", block)
        self.assertNotIn("{{", block)
        self.assertEqual(len(block.split("\n")), len(MARKUP["html"].split("\n")) - 1)
        self.assertEqual(
            block.count('src="./demo.svg"') + block.count('srcset="./demo.static.svg"'),
            2,
        )

    def test_a_base_that_looks_like_a_slot_stays_literal(self):
        block = mkdocs_hook.render_block(META, "{{link}}")
        self.assertIn('srcset="{{link}}/demo.static.svg"', block)
        # Boundary: a template without slots comes back as it is.
        self.assertEqual(
            mkdocs_hook.render_block({"html": "<p>plain</p>"}, "b"), "<p>plain</p>"
        )

    def test_a_json_without_html_is_refused(self):
        for html_value in (None, "", 42, ["<figure>"]):
            meta = dict(META, html=html_value)
            if html_value is None:
                del meta["html"]
            with (
                self.subTest(html=html_value),
                self.assertRaises(mkdocs_hook.CheckError) as raised,
            ):
                mkdocs_hook.render_block(meta, ".")
            self.assertIn(
                "records no html; rebuild with: node tools/figures/build.mjs build",
                str(raised.exception),
            )


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
        indented, _ = mkdocs_hook.expand(
            "- item\n\n    " + FIGURE.replace("\n", "\n    "), ".", self.figures
        )
        self.assertIn("\n    <picture>", indented)

    def test_line_separators_markdown_does_not_split_on_are_kept(self):
        for separator in ("\x0c", " ", "\x85"):
            source = f"intro{separator}line\n\n" + FIGURE + "after\n"
            text, errors = mkdocs_hook.expand(source, "..", self.figures)
            self.assertEqual(errors, [])
            self.assertTrue(
                text.startswith(f"intro{separator}line\n\n<figure"), repr(text[:40])
            )
            self.assertTrue(text.endswith("</details>\nafter\n"), repr(text[-40:]))
        crlf, errors = mkdocs_hook.expand(
            "intro\r\n\r\n" + FIGURE.replace("\n", "\r\n") + "after\r\n",
            "..",
            self.figures,
        )
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


class ExcludeFixture(unittest.TestCase):
    """exclude-fixtures.json records what pathspec matches; checks.test.mjs replays it against checks.mjs."""

    def test_the_recorded_matrix_is_what_pathspec_matches(self):
        try:
            import pathspec
        except ImportError:
            self.skipTest(
                "pathspec is not installed; MkDocs brings it (pip install -r docs/presets/mkdocs/requirements.txt)"
            )
        self.assertGreaterEqual(len(EXCLUDE["patterns"]), 15)
        for case in EXCLUDE["patterns"]:
            spec = pathspec.GitIgnoreSpec.from_lines([case["pattern"]])
            with self.subTest(pattern=case["pattern"]):
                self.assertEqual(
                    [path for path in EXCLUDE["paths"] if spec.match_file(path)],
                    case["matches"],
                )

    def test_every_recorded_match_is_a_listed_path(self):
        """Boundary: a match outside `paths` would never be replayed in either direction."""
        for case in EXCLUDE["patterns"]:
            with self.subTest(pattern=case["pattern"]):
                self.assertLessEqual(set(case["matches"]), set(EXCLUDE["paths"]))


class _Files:
    """The part of MkDocs' Files the hook reads before it appends: src_uris and append."""

    def __init__(self, uris=()):
        self.src_uris = {uri: object() for uri in uris}
        self.appended = []

    def append(self, file):
        self.appended.append(file)
        self.src_uris[file.src_uri] = file


class Hook(unittest.TestCase):
    """figures.css and player/ sit beside the hook, outside docs_dir, so the hook publishes them."""

    def test_the_stylesheet_and_the_player_sit_beside_the_hook(self):
        self.assertEqual(mkdocs_hook.CSS_FILE, FIGURES / "figures.css")
        self.assertTrue(mkdocs_hook.CSS_FILE.is_file())
        self.assertFalse((ROOT / "docs/stylesheets/figures.css").exists())
        self.assertEqual(mkdocs_hook.PLAYER_DIR, FIGURES / "player")
        self.assertEqual(mkdocs_hook.LOADER_URI, "assets/javascripts/figures/loader.js")

    def test_published_files_are_the_stylesheet_and_every_player_file(self):
        published = mkdocs_hook.published_files()
        self.assertEqual(published[0], (mkdocs_hook.CSS_URI, mkdocs_hook.CSS_FILE))
        self.assertEqual(
            [uri for uri, _ in published[1:]],
            [
                "assets/javascripts/figures/THIRD-PARTY-LICENSES.txt",
                "assets/javascripts/figures/loader.js",
                "assets/javascripts/figures/player.js",
            ],
        )
        self.assertTrue(all(source.is_file() for _, source in published))

    def test_committed_player_files_are_all_published(self):
        committed = sorted(
            path.name for path in (FIGURES / "player").iterdir() if path.is_file()
        )
        self.assertEqual(
            committed, ["THIRD-PARTY-LICENSES.txt", "loader.js", "player.js"]
        )
        published = [
            source.name
            for _, source in mkdocs_hook.published_files()
            if source.parent == mkdocs_hook.PLAYER_DIR
        ]
        self.assertEqual(published, committed)

    def test_published_files_without_player_or_with_too_many_files(self):
        with tempfile.TemporaryDirectory() as directory:
            original = mkdocs_hook.PLAYER_DIR
            self.addCleanup(setattr, mkdocs_hook, "PLAYER_DIR", original)
            mkdocs_hook.PLAYER_DIR = Path(directory) / "absent"
            self.assertEqual(
                mkdocs_hook.published_files(),
                [(mkdocs_hook.CSS_URI, mkdocs_hook.CSS_FILE)],
            )
            mkdocs_hook.PLAYER_DIR = Path(directory)
            for index in range(mkdocs_hook.MAX_PLAYER_FILES):
                write(Path(directory) / f"f{index:02}.js", "")
            (Path(directory) / "sub").mkdir()
            self.assertEqual(
                len(mkdocs_hook.published_files()), mkdocs_hook.MAX_PLAYER_FILES + 1
            )
            write(Path(directory) / "one-more.js", "")
            with self.assertRaises(mkdocs_hook.CheckError):
                mkdocs_hook.published_files()

    def test_on_config_links_the_stylesheet_once_and_keeps_a_listed_loader(self):
        config = {
            "extra_css": ["extra.css"],
            "extra_javascript": ["other.js", mkdocs_hook.LOADER_URI],
        }
        self.assertIs(mkdocs_hook.on_config(config), config)
        mkdocs_hook.on_config(config)
        self.assertEqual(config["extra_css"], ["extra.css", mkdocs_hook.CSS_URI])
        self.assertEqual(
            config["extra_javascript"], ["other.js", mkdocs_hook.LOADER_URI]
        )

    def test_on_config_adds_the_loader_as_a_module_script_once(self):
        try:
            import mkdocs  # noqa: F401 - on_config builds MkDocs' own script value
        except ImportError:
            self.skipTest("mkdocs is not installed; the hook runs only inside MkDocs")
        config = mkdocs_hook.on_config({"extra_css": [], "extra_javascript": []})
        mkdocs_hook.on_config(config)
        self.assertEqual(config["extra_css"], [mkdocs_hook.CSS_URI])
        self.assertEqual(
            [(str(script), script.type) for script in config["extra_javascript"]],
            [(mkdocs_hook.LOADER_URI, "module")],
        )

    def test_fallback_build_links_no_loader_and_logs_once(self):
        for release in (None, "", "v1.4.0"):
            with (
                self.subTest(release=release),
                tempfile.TemporaryDirectory() as directory,
            ):
                lock = self.use_absent_local_player(Path(directory))
                if release is not None:
                    write(lock, json.dumps({"release": release}))
                with (
                    mock.patch.object(
                        mkdocs_hook,
                        "urlopen",
                        side_effect=HTTPError(
                            "https://fixture", 404, "Not Found", None, None
                        ),
                    ) as download,
                    self.assertLogs(
                        "mkdocs.plugins.praetor_figures", level="INFO"
                    ) as logs,
                ):
                    config = {
                        "extra_css": [],
                        "extra_javascript": ["other.js", mkdocs_hook.LOADER_URI],
                    }
                    mkdocs_hook.on_config(config)
                    files = _Files([mkdocs_hook.CSS_URI])
                    mkdocs_hook.on_files(files, config)
                self.assertEqual(config["extra_javascript"], ["other.js"])
                self.assertEqual(files.appended, [])
                self.assertEqual(
                    sum("figures render as SVG" in line for line in logs.output), 1
                )
                self.assertEqual(download.call_count, int(bool(release)))

    def test_available_archive_links_loader_and_downloads_once_per_build(self):
        try:
            import mkdocs  # noqa: F401 - on_config builds MkDocs' own script value
        except ImportError:
            self.skipTest("mkdocs is not installed; the hook runs only inside MkDocs")
        raw, digest = self.make_player_archive()
        with tempfile.TemporaryDirectory() as directory:
            lock = self.use_absent_local_player(Path(directory))
            write(lock, json.dumps({"release": "v1.4.0", "sha256": digest}))
            with mock.patch.object(
                mkdocs_hook,
                "urlopen",
                side_effect=lambda *args, **kwargs: self.fake_response(raw),
            ) as download:
                for build in range(2):
                    config = mkdocs_hook.on_config(
                        {"extra_css": [], "extra_javascript": []}
                    )
                    self.assertEqual(download.call_count, build + 1)
                    self.assertEqual(
                        [str(script) for script in config["extra_javascript"]],
                        [mkdocs_hook.LOADER_URI],
                    )
                    files = _Files(
                        [
                            mkdocs_hook.CSS_URI,
                            *[
                                f"{mkdocs_hook.PLAYER_URI}/{name}"
                                for name in mkdocs_hook.EXPECTED_PLAYER_FILES
                            ],
                        ]
                    )
                    with self.assertLogs(
                        "mkdocs.plugins.praetor_figures", level="WARNING"
                    ):
                        mkdocs_hook.on_files(files, config)
                    self.assertEqual(download.call_count, build + 1)

    def test_a_site_build_renders_figures_and_publishes_the_stylesheet_and_the_player(
        self,
    ):
        """A real MkDocs build with only the hook listed: the fence becomes the figure with a base
        relative to its page, the CSS lands at CSS_URI, the committed player files land under
        PLAYER_URI byte for byte, and every page links the stylesheet and loads the loader as a
        module."""
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
            write(
                root / "mkdocs.yml",
                f"site_name: Fixture\nhooks:\n  - {(FIGURES / 'mkdocs_hook.py').as_posix()}\n",
            )
            with contextlib.redirect_stderr(io.StringIO()):
                build(
                    load_config(str(root / "mkdocs.yml"), site_dir=str(root / "site"))
                )
            for uri, source in mkdocs_hook.published_files():
                with self.subTest(uri=uri):
                    self.assertEqual(
                        (root / "site" / uri).read_bytes(), source.read_bytes()
                    )
            self.assertIn(
                mkdocs_hook.CSS_URI,
                (root / "site/index.html").read_text(encoding="utf-8"),
            )
            self.assertFalse(
                (root / "docs/assets/javascripts").exists(),
                "nothing is copied into docs_dir",
            )
            guide = (root / "site/guide/index.html").read_text(encoding="utf-8")
            self.assertIn('<img src="../assets/figures/demo.svg"', guide)
            self.assertIn(
                '<script src="../assets/javascripts/figures/loader.js" type="module"></script>',
                guide,
            )
            self.assertNotIn("Open the interactive figure", guide)

    def test_unreleased_site_build_renders_svg_without_player_references(self):
        try:
            from mkdocs.commands.build import build
            from mkdocs.config import load_config
        except ImportError:
            self.skipTest("mkdocs is not installed; the hook runs only inside MkDocs")

        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            hook = root / "tools/figures/mkdocs_hook.py"
            write(hook, (FIGURES / "mkdocs_hook.py").read_text(encoding="utf-8"))
            write(
                hook.with_name("figures.css"),
                mkdocs_hook.CSS_FILE.read_text(encoding="utf-8"),
            )
            write(hook.with_name("player.lock.json"), '{"release":""}')
            write(root / "docs/index.md", "# Home\n\n" + FIGURE)
            write(root / "docs/assets/figures/demo.json", json.dumps(META))
            write(
                root / "mkdocs.yml",
                "site_name: Fixture\nhooks:\n  - tools/figures/mkdocs_hook.py\n",
            )
            with self.assertLogs(
                "mkdocs.plugins.praetor_figures", level="INFO"
            ) as logs:
                build(load_config(str(root / "mkdocs.yml"), strict=True))
            html = (root / "site/index.html").read_text(encoding="utf-8")
            self.assertIn('<img src="./assets/figures/demo.svg"', html)
            self.assertNotIn(mkdocs_hook.PLAYER_URI, html)
            self.assertFalse((root / "site" / mkdocs_hook.PLAYER_URI).exists())
            self.assertEqual(len(logs.output), 1)

    def test_on_page_markdown_warns_for_a_figure_it_cannot_render(self):
        class Page:
            url = "guide/"

            class file:
                src_uri = "guide.md"

        with (
            tempfile.TemporaryDirectory() as directory,
            self.assertLogs("mkdocs.plugins.praetor_figures", level="WARNING") as logs,
        ):
            text = mkdocs_hook.on_page_markdown(
                "```figure\nnope\n```\n", Page, {"docs_dir": directory}, None
            )
        self.assertEqual(text, "```figure\nnope\n```\n")
        self.assertIn("guide.md: figure 'nope' has no", logs.output[0])

    def test_on_files_keeps_a_docs_file_at_the_same_path_and_warns(self):
        uris = [uri for uri, _ in mkdocs_hook.published_files()]
        files = _Files(uris)
        with self.assertLogs("mkdocs.plugins.praetor_figures", level="WARNING") as logs:
            self.assertIs(mkdocs_hook.on_files(files, {}), files)
        self.assertEqual(files.appended, [])
        self.assertEqual(len(logs.output), len(uris))
        self.assertIn(
            f"{mkdocs_hook.LOADER_URI}: docs_dir already holds this path",
            "\n".join(logs.output),
        )

    @staticmethod
    def make_player_archive(entries=None):
        if entries is None:
            entries = [
                ("loader.js", (FIGURES / "player/loader.js").read_bytes()),
                ("player.js", (FIGURES / "player/player.js").read_bytes()),
                (
                    "THIRD-PARTY-LICENSES.txt",
                    (FIGURES / "player/THIRD-PARTY-LICENSES.txt").read_bytes(),
                ),
            ]
        tar_buf = io.BytesIO()
        with tarfile.open(fileobj=tar_buf, mode="w") as archive:
            for name, payload in entries:
                info = tarfile.TarInfo(name=name)
                info.size = len(payload)
                info.mode = 0o644
                info.mtime = 0
                archive.addfile(info, io.BytesIO(payload))
        gz_buf = io.BytesIO()
        with gzip.GzipFile(filename="", mode="wb", fileobj=gz_buf, mtime=0) as gz:
            gz.write(tar_buf.getvalue())
        raw = gz_buf.getvalue()
        return raw, hashlib.sha256(raw).hexdigest()

    def use_absent_local_player(self, directory: Path) -> Path:
        original_player = mkdocs_hook.PLAYER_DIR
        original_lock = mkdocs_hook.LOCK_FILE
        self.addCleanup(setattr, mkdocs_hook, "PLAYER_DIR", original_player)
        self.addCleanup(setattr, mkdocs_hook, "LOCK_FILE", original_lock)
        mkdocs_hook.PLAYER_DIR = directory / "absent-player"
        lock_file = directory / "player.lock.json"
        mkdocs_hook.LOCK_FILE = lock_file
        return lock_file

    def test_lock_with_empty_release_warns_and_publishes_no_player(self):
        with tempfile.TemporaryDirectory() as directory:
            lock_file = self.use_absent_local_player(Path(directory))
            write(
                lock_file,
                json.dumps(
                    {
                        "asset": "figures-player.tar.gz",
                        "sha256": "0" * 64,
                        "release": "",
                    }
                ),
            )
            with self.assertNoLogs("mkdocs.plugins.praetor_figures", level="WARNING"):
                mkdocs_hook.published_files()
            with self.assertLogs(
                "mkdocs.plugins.praetor_figures", level="INFO"
            ) as logs:
                published = mkdocs_hook.published_files()
            self.assertEqual(published, [(mkdocs_hook.CSS_URI, mkdocs_hook.CSS_FILE)])
            self.assertEqual(len(logs.output), 1)
            self.assertTrue(logs.output[0].startswith("INFO:"), logs.output[0])
            self.assertIn(
                "figures: engine pin unreleased has no release; "
                "figures render as SVG (no interactive player)",
                logs.output[0],
            )

    def test_missing_lock_warns_and_publishes_no_player(self):
        with tempfile.TemporaryDirectory() as directory:
            self.use_absent_local_player(Path(directory))
            with self.assertLogs(
                "mkdocs.plugins.praetor_figures", level="WARNING"
            ) as logs:
                published = mkdocs_hook.published_files()
            self.assertEqual(published, [(mkdocs_hook.CSS_URI, mkdocs_hook.CSS_FILE)])
            self.assertEqual(len(logs.output), 1)
            self.assertIn("player.lock.json is missing", logs.output[0])

    def test_lock_repository_over_plain_http_is_refused(self):
        with self.assertRaises(mkdocs_hook.CheckError):
            mkdocs_hook.release_archive_url(
                {"repository": "http://example.test/fork"},
                "v1.0.0",
                "figures-player.tar.gz",
            )
        with self.assertRaises(mkdocs_hook.CheckError):
            mkdocs_hook.download_player_archive(
                "http://example.test/figures-player.tar.gz"
            )

    @staticmethod
    def fake_response(payload: bytes):
        class FakeResponse(io.BytesIO):
            def __enter__(self):
                return self

            def __exit__(self, *args):
                self.close()

        return FakeResponse(payload)

    def test_lock_fetches_verifies_and_extracts_good_archive(self):
        raw, digest = self.make_player_archive()
        seen_urls = []
        with tempfile.TemporaryDirectory() as directory:
            lock_file = self.use_absent_local_player(Path(directory))
            original_urlopen = mkdocs_hook.urlopen
            self.addCleanup(setattr, mkdocs_hook, "urlopen", original_urlopen)
            mkdocs_hook.urlopen = lambda url, timeout=0: (
                seen_urls.append((url, timeout)) or self.fake_response(raw)
            )
            write(
                lock_file,
                json.dumps(
                    {
                        "asset": "figures-player.tar.gz",
                        "sha256": digest,
                        "release": "v1.4.0",
                    }
                ),
            )
            published = mkdocs_hook.published_files()
            self.assertEqual(
                seen_urls,
                [
                    (
                        "https://github.com/cordanaLLM/praetor/releases/download/v1.4.0/figures-player.tar.gz",
                        30,
                    )
                ],
            )
            self.assertEqual(
                [uri for uri, _ in published],
                [
                    mkdocs_hook.CSS_URI,
                    "assets/javascripts/figures/THIRD-PARTY-LICENSES.txt",
                    "assets/javascripts/figures/loader.js",
                    "assets/javascripts/figures/player.js",
                ],
            )
            for _, source in published[1:]:
                self.assertEqual(
                    source.read_bytes(), (FIGURES / "player" / source.name).read_bytes()
                )
            write(
                lock_file,
                json.dumps(
                    {
                        "asset": "figures-player.tar.gz",
                        "sha256": digest,
                        "release": "v1.4.0",
                        "repository": "adopter/praetor-fork",
                    }
                ),
            )
            mkdocs_hook.published_files()
            self.assertEqual(
                seen_urls[-1][0],
                "https://github.com/adopter/praetor-fork/releases/download/v1.4.0/figures-player.tar.gz",
            )

    def test_lock_refuses_archive_with_one_changed_byte(self):
        raw, digest = self.make_player_archive()
        corrupted = bytes([raw[0] ^ 0xFF]) + raw[1:]
        corrupted_digest = hashlib.sha256(corrupted).hexdigest()
        with tempfile.TemporaryDirectory() as directory:
            lock_file = self.use_absent_local_player(Path(directory))
            original_urlopen = mkdocs_hook.urlopen
            self.addCleanup(setattr, mkdocs_hook, "urlopen", original_urlopen)
            mkdocs_hook.urlopen = lambda url, timeout=0: self.fake_response(corrupted)
            write(
                lock_file,
                json.dumps(
                    {
                        "asset": "figures-player.tar.gz",
                        "sha256": digest,
                        "release": "v1.4.0",
                    }
                ),
            )
            with self.assertRaises(mkdocs_hook.CheckError) as raised:
                mkdocs_hook.published_files()
            message = str(raised.exception)
            self.assertIn(digest, message)
            self.assertIn(corrupted_digest, message)

    def test_lock_refuses_archive_with_extra_or_unsafe_member(self):
        base_entries = [
            ("loader.js", b"loader"),
            ("player.js", b"player"),
            ("THIRD-PARTY-LICENSES.txt", b"licenses"),
        ]
        bad_cases = [
            [*base_entries, ("extra.js", b"extra")],
            [("../x", b"escape"), *base_entries[1:]],
            [("/loader.js", b"abs"), *base_entries[1:]],
        ]
        with tempfile.TemporaryDirectory() as directory:
            lock_file = self.use_absent_local_player(Path(directory))
            original_urlopen = mkdocs_hook.urlopen
            self.addCleanup(setattr, mkdocs_hook, "urlopen", original_urlopen)
            for entries in bad_cases:
                raw, digest = self.make_player_archive(entries)
                mkdocs_hook.urlopen = lambda url, timeout=0, data=raw: (
                    self.fake_response(data)
                )
                write(
                    lock_file,
                    json.dumps(
                        {
                            "asset": "figures-player.tar.gz",
                            "sha256": digest,
                            "release": "v1.4.0",
                        }
                    ),
                )
                with (
                    self.subTest(entries=[name for name, _ in entries]),
                    self.assertRaises(mkdocs_hook.CheckError),
                ):
                    mkdocs_hook.published_files()

    def test_lock_404_download_error_warns_and_publishes_no_player(self):
        def raise_404(url, timeout=0):
            raise HTTPError(url, 404, "Not Found", hdrs=None, fp=None)

        with tempfile.TemporaryDirectory() as directory:
            lock_file = self.use_absent_local_player(Path(directory))
            original_urlopen = mkdocs_hook.urlopen
            self.addCleanup(setattr, mkdocs_hook, "urlopen", original_urlopen)
            mkdocs_hook.urlopen = raise_404
            write(
                lock_file,
                json.dumps(
                    {
                        "asset": "figures-player.tar.gz",
                        "sha256": "a" * 64,
                        "release": "v1.4.0",
                    }
                ),
            )
            with self.assertLogs(
                "mkdocs.plugins.praetor_figures", level="WARNING"
            ) as logs:
                published = mkdocs_hook.published_files()
            self.assertEqual(published, [(mkdocs_hook.CSS_URI, mkdocs_hook.CSS_FILE)])
            self.assertEqual(len(logs.output), 1)
            self.assertIn("v1.4.0", logs.output[0])
            self.assertIn("404", logs.output[0])
            self.assertIn(
                "has no release; figures render as SVG (no interactive player)",
                logs.output[0],
            )


if __name__ == "__main__":
    unittest.main()
