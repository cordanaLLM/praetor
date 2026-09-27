#!/usr/bin/env python3
"""Tests for scripts/sync_interfig.py: positive, negative and boundary cases per subcommand.

No test reaches the network: every online path runs against DummyFetcher, and the Fetcher
tests mock urllib.request.urlopen. Run with `python3 -B scripts/test_sync_interfig.py`.
"""

from __future__ import annotations

import contextlib
import io
import json
import os
import shutil
import subprocess
import sys
import tempfile
import unittest
import urllib.error
from pathlib import Path
from unittest.mock import MagicMock, patch

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

import sync_interfig  # noqa: E402

API = sync_interfig.API
RAW = sync_interfig.RAW
COMMITS_URL = f"{API}/commits?path=hindsight-interfig&sha=main&per_page=1"
LICENSE = b"MIT License\n"
CONTENT = b"content"
COMPARE_BODY = json.dumps({"commits": [{"sha": "0123456789", "commit": {"message": "feat: x\n\nbody"}}]}).encode()

REUSE_OK = """
[[annotations]]
path = ["**"]
SPDX-FileCopyrightText = "2026 Example"
SPDX-License-Identifier = "EUPL-1.2"

[[annotations]]
path = ["third_party/interfig/upstream/**"]
precedence = "override"
SPDX-FileCopyrightText = "2025 Vectorize AI, Inc."
SPDX-License-Identifier = "MIT"
"""

REUSE_OVERRIDE_FIRST = """
[[annotations]]
path = ["third_party/interfig/upstream/**"]
precedence = "override"
SPDX-License-Identifier = "MIT"

[[annotations]]
path = ["**"]
SPDX-License-Identifier = "EUPL-1.2"
"""


class DummyFetcher:
    """Serves canned (body, status) pairs or raises a canned exception; records every URL."""

    def __init__(self, responses=None):
        self.responses = responses or {}
        self.urls = []

    def get(self, url):
        self.urls.append(url)
        resp = self.responses.get(url, (b"", 404))
        if isinstance(resp, Exception):
            raise resp
        return resp


def listing(*entries):
    return json.dumps([{"path": f"hindsight-interfig/{p}", "type": t} for p, t in entries]).encode(), 200


class TreeCase(unittest.TestCase):
    """Builds a scratch repository with a vendored tree and points the module at it."""

    def setUp(self):
        self.root = Path(tempfile.mkdtemp())
        self.addCleanup(shutil.rmtree, self.root, ignore_errors=True)
        self.vendor_dir = self.root / "third_party" / "interfig"
        self.upstream = self.vendor_dir / "upstream"
        (self.upstream / "src").mkdir(parents=True)
        self.figures = self.root / "tools" / "figures"
        self.figures.mkdir(parents=True)
        for name, value in {
            "REPO_ROOT": self.root,
            "VENDOR_DIR": self.vendor_dir,
            "UPSTREAM_DIR": self.upstream,
            "VENDOR_JSON": self.vendor_dir / "vendor.json",
            "REUSE_TOML": self.root / "REUSE.toml",
            "TOOLS_FIGURES": self.figures,
        }.items():
            patcher = patch.object(sync_interfig, name, value)
            patcher.start()
            self.addCleanup(patcher.stop)

        (self.root / "REUSE.toml").write_text(REUSE_OK, encoding="utf-8")
        (self.figures / "package.json").write_text('{"devDependencies": {"react": "19.3.0"}}', encoding="utf-8")
        (self.upstream / "src" / "a.ts").write_bytes(CONTENT)
        (self.upstream / "LICENSE").write_bytes(LICENSE)
        self.vendor = {
            "path_commit": "abc",
            "commit": "abc",
            "fetched": "2026-01-01",
            "license_sha256": sync_interfig.sha256_bytes(LICENSE),
            "include": ["src/a.ts"],
            "exclude": ["demo/", "src/fixtures/"],
            "files": {"LICENSE": sync_interfig.sha256_bytes(LICENSE), "src/a.ts": sync_interfig.sha256_bytes(CONTENT)},
        }
        self.write_vendor()

    def write_vendor(self):
        (self.vendor_dir / "vendor.json").write_text(json.dumps(self.vendor, indent=2) + "\n", encoding="utf-8")

    def read_vendor(self):
        return json.loads((self.vendor_dir / "vendor.json").read_text(encoding="utf-8"))

    def assertFails(self, fn, *args, contains):
        with self.assertRaises(sync_interfig.SyncError) as cm:
            fn(*args)
        self.assertIn(contains, str(cm.exception))
        return cm.exception


class TestVerify(TreeCase):
    def run_verify(self):
        with contextlib.redirect_stdout(io.StringIO()) as out:
            sync_interfig.verify()
        return out.getvalue()

    def test_ok(self):
        self.assertIn("Verify OK", self.run_verify())

    def test_tampered_hash(self):
        (self.upstream / "src" / "a.ts").write_bytes(b"tampered")
        self.assertFails(sync_interfig.verify, contains="Hash mismatch for src/a.ts")

    def test_unexpected_file(self):
        (self.upstream / "src" / "extra.ts").write_bytes(CONTENT)
        self.assertFails(sync_interfig.verify, contains="Unexpected file in upstream/: src/extra.ts")

    def test_missing_file(self):
        (self.upstream / "src" / "a.ts").unlink()
        self.assertFails(sync_interfig.verify, contains="Missing file in upstream/: src/a.ts")

    def test_license_rehashed_without_review(self):
        # LICENSE altered and files.LICENSE re-hashed, license_sha256 left at the reviewed value.
        (self.upstream / "LICENSE").write_bytes(b"Proprietary\n")
        self.vendor["files"]["LICENSE"] = sync_interfig.sha256_bytes(b"Proprietary\n")
        self.write_vendor()
        self.assertFails(sync_interfig.verify, contains="differs from license_sha256")

    def test_file_dropped_from_include(self):
        self.vendor["include"] = []
        self.write_vendor()
        self.assertFails(sync_interfig.verify, contains="File on disk not in include list: src/a.ts")

    def test_include_names_absent_file(self):
        self.vendor["include"] = ["src/a.ts", "src/gone.ts"]
        self.write_vendor()
        self.assertFails(sync_interfig.verify, contains="File in include list not on disk: src/gone.ts")

    def test_include_overlaps_exclude(self):
        self.vendor["exclude"] = ["src/"]
        self.write_vendor()
        self.assertFails(sync_interfig.verify, contains="both included and excluded: src/a.ts")

    def test_reuse_missing_override(self):
        (self.root / "REUSE.toml").write_text('[[annotations]]\npath = ["**"]\n', encoding="utf-8")
        self.assertFails(sync_interfig.verify, contains="missing override")

    def test_reuse_override_before_star_star(self):
        (self.root / "REUSE.toml").write_text(REUSE_OVERRIDE_FIRST, encoding="utf-8")
        self.assertFails(sync_interfig.verify, contains="must sit after the ** table")

    def test_reuse_override_relicensed(self):
        (self.root / "REUSE.toml").write_text(REUSE_OK.replace('"MIT"', '"EUPL-1.2"'), encoding="utf-8")
        self.assertFails(sync_interfig.verify, contains="must be SPDX-License-Identifier MIT")

    def test_reuse_override_without_precedence(self):
        (self.root / "REUSE.toml").write_text(REUSE_OK.replace('precedence = "override"\n', ""), encoding="utf-8")
        self.assertFails(sync_interfig.verify, contains='precedence = "override"')

    def test_reuse_later_narrow_table_shadows_override(self):
        shadow = REUSE_OK + '\n[[annotations]]\npath = ["third_party/interfig/upstream/src/*.ts"]\n'
        shadow += 'SPDX-License-Identifier = "EUPL-1.2"\n'
        (self.root / "REUSE.toml").write_text(shadow, encoding="utf-8")
        self.assertFails(sync_interfig.verify, contains="upstream/src/a.ts")

    def test_file_cap_boundary(self):
        # Exactly MAX_FILES files pass the cap; one more fails it.
        with patch.object(sync_interfig, "MAX_FILES", 2):
            self.assertIn("Verify OK", self.run_verify())
            (self.upstream / "src" / "b.ts").write_bytes(CONTENT)
            self.assertFails(sync_interfig.verify, contains="exceed the cap of 2")

    def test_reuse_glob(self):
        match = sync_interfig.reuse_glob_matches
        self.assertTrue(match("**", "third_party/interfig/upstream/src/a.ts"))
        self.assertTrue(match("third_party/interfig/upstream/**", "third_party/interfig/upstream/src/a.ts"))
        self.assertTrue(match("third_party/*/upstream/**", "third_party/interfig/upstream/LICENSE"))
        self.assertFalse(match("third_party/*", "third_party/interfig/upstream/LICENSE"))
        self.assertFalse(match("docs/**", "third_party/interfig/upstream/LICENSE"))


class TestCheck(TreeCase):
    def test_no_drift(self):
        fetcher = DummyFetcher({COMMITS_URL: (b'[{"sha": "abc"}]', 200)})
        with contextlib.redirect_stdout(io.StringIO()) as out:
            sync_interfig.check(fetcher)
        self.assertIn("No drift", out.getvalue())

    def test_drift_reports_compare_url_and_command(self):
        fetcher = DummyFetcher({COMMITS_URL: (b'[{"sha": "def"}]', 200)})
        with self.assertRaises(sync_interfig.DriftError) as cm:
            sync_interfig.check(fetcher)
        message = str(cm.exception)
        self.assertIn("https://github.com/vectorize-io/hindsight/compare/abc...def", message)
        self.assertIn("python3 scripts/sync_interfig.py update --commit def", message)

    def test_empty_commit_list(self):
        fetcher = DummyFetcher({COMMITS_URL: (b"[]", 200)})
        self.assertFails(sync_interfig.check, fetcher, contains="No commit found")

    def test_exit_codes_tell_drift_from_outage(self):
        cases = {
            0: DummyFetcher({COMMITS_URL: (b'[{"sha": "abc"}]', 200)}),
            sync_interfig.EXIT_DRIFT: DummyFetcher({COMMITS_URL: (b'[{"sha": "def"}]', 200)}),
            1: DummyFetcher({COMMITS_URL: (b"rate limited", 403)}),
        }
        for want, fetcher in cases.items():
            with self.subTest(want=want), contextlib.redirect_stdout(io.StringIO()), \
                    contextlib.redirect_stderr(io.StringIO()):
                self.assertEqual(sync_interfig.main(["check"], fetcher), want)

    def test_http_error_is_an_outage_not_drift(self):
        err = urllib.error.HTTPError(COMMITS_URL, 503, "Unavailable", {}, io.BytesIO(b""))
        self.addCleanup(err.close)
        with contextlib.redirect_stderr(io.StringIO()) as errout:
            self.assertEqual(sync_interfig.main(["check"], DummyFetcher({COMMITS_URL: err})), 1)
        self.assertIn("503", errout.getvalue())
        self.assertNotIn("Drift", errout.getvalue())

    def test_update_without_commit(self):
        with contextlib.redirect_stderr(io.StringIO()) as errout:
            self.assertEqual(sync_interfig.main(["update"], DummyFetcher()), 1)
        self.assertIn("--commit", errout.getvalue())


class TestFetcher(unittest.TestCase):
    def serve(self, mock_urlopen, body, status=200):
        response = MagicMock()
        response.read.side_effect = lambda n: body[:n]
        response.status = status
        mock_urlopen.return_value.__enter__.return_value = response

    @patch("urllib.request.urlopen")
    def test_exact_cap_passes(self, mock_urlopen):
        self.serve(mock_urlopen, b"x" * sync_interfig.MAX_FILE_SIZE)
        body, status = sync_interfig.Fetcher().get("https://example.com/max")
        self.assertEqual((len(body), status), (sync_interfig.MAX_FILE_SIZE, 200))

    @patch("urllib.request.urlopen")
    def test_one_byte_over_cap_fails(self, mock_urlopen):
        self.serve(mock_urlopen, b"x" * (sync_interfig.MAX_FILE_SIZE + 1))
        with self.assertRaises(sync_interfig.SyncError) as cm:
            sync_interfig.Fetcher().get("https://example.com/large")
        self.assertIn("exceeds the 1 MiB limit", str(cm.exception))

    @patch("urllib.request.urlopen")
    def test_every_request_carries_the_timeout(self, mock_urlopen):
        mock_urlopen.side_effect = TimeoutError("timed out")
        with self.assertRaises(TimeoutError):
            sync_interfig.Fetcher().get("https://example.com/slow")
        self.assertEqual(mock_urlopen.call_args.kwargs["timeout"], sync_interfig.TIMEOUT)

    @patch("urllib.request.urlopen")
    def test_404_is_returned(self, mock_urlopen):
        mock_urlopen.side_effect = urllib.error.HTTPError("u", 404, "Not Found", {}, io.BytesIO(b""))
        self.assertEqual(sync_interfig.Fetcher().get("https://example.com/missing"), (b"", 404))

    @patch("urllib.request.urlopen")
    def test_token_only_sent_to_api(self, mock_urlopen):
        self.serve(mock_urlopen, b"{}")
        with patch.dict(os.environ, {"GITHUB_TOKEN": "t0ken"}):
            sync_interfig.Fetcher().get(f"{API}/commits")
            api_request = mock_urlopen.call_args.args[0]
            sync_interfig.Fetcher().get(f"{RAW}/abc/LICENSE")
            raw_request = mock_urlopen.call_args.args[0]
        self.assertEqual(api_request.get_header("Authorization"), "Bearer t0ken")
        self.assertIsNone(raw_request.get_header("Authorization"))


class TestReactRange(TreeCase):
    def test_satisfies(self):
        cases = [
            ("19.3.0", ">=18", True),
            ("19.3.0", "^18 || ^19", True),
            ("19.3.0", "19.x", True),
            ("19.3.0", "*", True),
            ("19.3.0", "~19.3", True),
            ("19.3.0", ">=19.3.0", True),
            ("19.3.0", "^18", False),
            ("19.3.0", ">=18 <19", False),
            ("19.3.0", "<19", False),
            ("19.3.0", "~19.2", False),
            ("19.3.0", ">19.3.0", False),
            ("19.3.0", "<=19", True),
            ("19.3.0", ">18", True),
            ("19.3.0", ">19", False),
            ("19.3.0", ">= 18 < 20", True),
            ("0.2.5", "^0.2.3", True),
            ("0.3.0", "^0.2.3", False),
        ]
        for version, spec, want in cases:
            with self.subTest(version=version, spec=spec):
                self.assertEqual(sync_interfig.satisfies(version, spec), want)

    def test_unreadable_range_fails_closed(self):
        for spec in ("18.0.0 - 19.0.0", "latest", "workspace:*", "^19 || latest"):
            with self.subTest(spec=spec), self.assertRaises(sync_interfig.SyncError):
                sync_interfig.satisfies("19.3.0", spec)

    def peer(self, body):
        return DummyFetcher({f"{RAW}/new/hindsight-interfig/package.json": (body, 200)})

    def test_compatible_peer(self):
        sync_interfig.check_react_compat(self.peer(b'{"peerDependencies": {"react": ">=18"}}'), "new")

    def test_incompatible_peer(self):
        fetcher = self.peer(b'{"peerDependencies": {"react": "^18"}}')
        self.assertFails(sync_interfig.check_react_compat, fetcher, "new", contains="React version mismatch")

    def test_missing_upstream_peer(self):
        fetcher = self.peer(b'{"dependencies": {}}')
        self.assertFails(sync_interfig.check_react_compat, fetcher, "new", contains="no react peer range")

    def test_missing_local_react(self):
        (self.figures / "package.json").write_text('{"devDependencies": {}}', encoding="utf-8")
        fetcher = self.peer(b'{"peerDependencies": {"react": ">=18"}}')
        self.assertFails(sync_interfig.check_react_compat, fetcher, "new", contains="declares no react dependency")


class TestUpdate(TreeCase):
    NEW = b"new content"

    def responses(self, **overrides):
        base = {
            f"{RAW}/new/LICENSE": (LICENSE, 200),
            f"{RAW}/new/hindsight-interfig/package.json": (b'{"peerDependencies": {"react": ">=18"}}', 200),
            f"{API}/contents/hindsight-interfig/src?ref=new": listing(("src/a.ts", "file"), ("src/fixtures", "dir")),
            f"{API}/contents/hindsight-interfig/scripts?ref=new": listing(),
            f"{RAW}/new/hindsight-interfig/src/a.ts": (self.NEW, 200),
            f"{API}/compare/abc...new": (COMPARE_BODY, 200),
        }
        base.update(overrides)
        return base

    def run_update(self, fetcher):
        with contextlib.redirect_stdout(io.StringIO()) as out:
            sync_interfig.update(fetcher, "new")
        return out.getvalue()

    def assertTreeUnchanged(self):
        self.assertEqual((self.upstream / "src" / "a.ts").read_bytes(), CONTENT)
        self.assertEqual(self.read_vendor()["path_commit"], "abc")
        self.assertFalse((self.vendor_dir / "upstream.new").exists())
        self.assertFalse((self.vendor_dir / "upstream.old").exists())

    def test_same_pin_is_noop(self):
        fetcher = DummyFetcher()
        with contextlib.redirect_stdout(io.StringIO()):
            sync_interfig.update(fetcher, "abc")
        self.assertEqual(fetcher.urls, [])

    @patch("sync_interfig.shutil.which", side_effect=lambda name: f"/usr/bin/{name}")
    @patch("sync_interfig.subprocess.run")
    def test_happy_path_installs_new_bytes_and_pin(self, mock_run, _which):
        out = self.run_update(DummyFetcher(self.responses()))
        self.assertEqual((self.upstream / "src" / "a.ts").read_bytes(), self.NEW)
        self.assertEqual((self.upstream / "LICENSE").read_bytes(), LICENSE)
        vendor = self.read_vendor()
        self.assertEqual((vendor["path_commit"], vendor["commit"]), ("new", "new"))
        self.assertEqual(vendor["files"]["src/a.ts"], sync_interfig.sha256_bytes(self.NEW))
        self.assertEqual(list(vendor["files"]), ["LICENSE", "src/a.ts"])
        self.assertFalse((self.vendor_dir / "upstream.new").exists())
        self.assertFalse((self.vendor_dir / "upstream.old").exists())
        commands = [c.args[0] for c in mock_run.call_args_list]
        self.assertEqual(commands[0][:2], ["/usr/bin/node", "--test"])
        self.assertEqual(commands[1][-2:], ["run", "build"])
        self.assertIn("01234567 feat: x", out)
        with contextlib.redirect_stdout(io.StringIO()):
            sync_interfig.verify()

    @patch("sync_interfig.write_json_atomic", side_effect=OSError("disk full"))
    def test_failed_install_restores_previous_tree(self, _write):
        fetcher = DummyFetcher(self.responses())
        self.assertFails(self.run_update, fetcher, contains="previous tree restored")
        self.assertTreeUnchanged()

    def test_license_changed(self):
        fetcher = DummyFetcher(self.responses(**{f"{RAW}/new/LICENSE": (b"new license", 200)}))
        self.assertFails(self.run_update, fetcher, contains="LICENSE changed!")
        self.assertNotIn(f"{RAW}/new/hindsight-interfig/src/a.ts", fetcher.urls)
        self.assertTreeUnchanged()

    def test_react_mismatch(self):
        peer = {f"{RAW}/new/hindsight-interfig/package.json": (b'{"peerDependencies": {"react": "^18"}}', 200)}
        self.assertFails(self.run_update, DummyFetcher(self.responses(**peer)), contains="React version mismatch")
        self.assertTreeUnchanged()

    def test_unlisted_file(self):
        src = {f"{API}/contents/hindsight-interfig/src?ref=new": listing(("src/a.ts", "file"), ("src/new.ts", "file"))}
        self.assertFails(self.run_update, DummyFetcher(self.responses(**src)), contains="Unlisted new file: src/new.ts")
        self.assertTreeUnchanged()

    def test_unlisted_file_in_subdirectory(self):
        overrides = {
            f"{API}/contents/hindsight-interfig/src?ref=new": listing(("src/a.ts", "file"), ("src/sub", "dir")),
            f"{API}/contents/hindsight-interfig/src/sub?ref=new": listing(("src/sub/b.ts", "file")),
        }
        fetcher = DummyFetcher(self.responses(**overrides))
        self.assertFails(self.run_update, fetcher, contains="Unlisted new file: src/sub/b.ts")

    def test_excluded_subdirectory_is_not_listed(self):
        with patch("sync_interfig.swap_in"), patch("sync_interfig.run_upstream_checks"):
            fetcher = DummyFetcher(self.responses())
            self.run_update(fetcher)
        self.assertNotIn(f"{API}/contents/hindsight-interfig/src/fixtures?ref=new", fetcher.urls)

    def test_missing_listing_fails_closed(self):
        gone = {f"{API}/contents/hindsight-interfig/scripts?ref=new": (b"", 404)}
        self.assertFails(self.run_update, DummyFetcher(self.responses(**gone)), contains="returned HTTP 404")
        self.assertTreeUnchanged()

    def test_directory_cap(self):
        with patch.object(sync_interfig, "MAX_DIRS", 1):
            fetcher = DummyFetcher(self.responses())
            self.assertFails(self.run_update, fetcher, contains="More than 1 upstream directories")

    @patch("sync_interfig.shutil.which", return_value=None)
    def test_missing_tool(self, _which):
        self.assertFails(self.run_update, DummyFetcher(self.responses()), contains="node not found on PATH")

    @patch("sync_interfig.shutil.which", side_effect=lambda name: f"/usr/bin/{name}")
    @patch("sync_interfig.subprocess.run", side_effect=subprocess.CalledProcessError(1, ["npm"]))
    def test_failed_build_says_how_to_revert(self, _run, _which):
        fetcher = DummyFetcher(self.responses())
        err = self.assertFails(self.run_update, fetcher, contains="git checkout third_party/interfig")
        self.assertIsInstance(err.__cause__, sync_interfig.SyncError)

    def test_safe_target_rejects_escape(self):
        for rel in ("../outside.ts", "/etc/passwd"):
            with self.subTest(rel=rel), self.assertRaises(sync_interfig.SyncError):
                sync_interfig.safe_target(self.root, rel)
        self.assertEqual(sync_interfig.safe_target(self.root, "src/a.ts"), self.root / "src" / "a.ts")


if __name__ == "__main__":
    unittest.main()
