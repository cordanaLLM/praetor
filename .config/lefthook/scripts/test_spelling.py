"""Spelling checks against exported staged bytes, without invoking repository gates."""

from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest import mock

import checks
from common import HookError


ENGINE_CONFIG = Path(__file__).resolve().parents[3] / ".codespellrc"


class StagedSpelling(unittest.TestCase):
    def setUp(self):
        """Set up temporary repo and export directories with an initialized git tree and codespell configuration."""
        temporary = tempfile.TemporaryDirectory(prefix="praetor-spelling-")
        self.addCleanup(temporary.cleanup)
        self.repo = Path(temporary.name) / "repo"
        self.repo.mkdir()
        self.export = Path(temporary.name) / "export"
        self.export.mkdir()
        self.git("init", "--quiet")
        self.stage(".codespellrc", "[codespell]\ncheck-hidden =\nskip = */testdata/*\n")

    def git(self, *args):
        """Execute a git command within the test repository and return captured stdout bytes."""
        return subprocess.run(
            ["git", *args], cwd=self.repo, capture_output=True, check=True, timeout=30
        ).stdout

    def stage(self, name, text):
        """Write file contents into the test repository and stage the path in the git index."""
        path = self.repo / name
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_bytes(text if isinstance(text, bytes) else text.encode("utf-8"))
        self.git("add", "--", name)

    def check(self, *names):
        """Export staged file versions from the git index to the export directory and run text checks."""
        # Export the real index, preserving partially staged worktree edits.
        for name in (".codespellrc", *names):
            path = self.export / name
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_bytes(self.git("show", f":{name}"))
        checks.text_checks(self.export, list(names))

    def test_staged_typos_fail_with_file_line_and_word(self):
        """Negative test proving planted typos in staged files trigger check failures with file, line, and word details."""
        # codespell:ignore-next-line occured,seperate
        self.stage("notes with spaces.md", "An event occured.\nKeep items seperate.\n")
        (self.repo / "notes with spaces.md").write_text(
            "Clean worktree.\n", encoding="utf-8"
        )
        with self.assertRaises(HookError) as raised:
            self.check("notes with spaces.md")
        self.assertIn("exited 65", str(raised.exception))
        # codespell:ignore-next-line occured
        self.assertIn("notes with spaces.md:1: occured", str(raised.exception))
        # codespell:ignore-next-line seperate
        self.assertIn("notes with spaces.md:2: seperate", str(raised.exception))

    def test_clean_staged_file_passes_with_unstaged_typo(self):
        """Positive test proving clean staged content passes verification when unstaged modifications contain typos."""
        self.stage("notes.md", "An event occurred.\nKeep items separate.\n")
        # codespell:ignore-next-line occured,seperate
        (self.repo / "notes.md").write_text("occured seperate\n", encoding="utf-8")
        self.check("notes.md")

    def test_skipped_staged_fixture_passes(self):
        """Boundary test proving paths matching configured skip patterns pass despite planted typos in staged content."""
        # codespell:ignore-next-line occured,seperate
        self.stage("internal/testdata/spelling.golden", "occured seperate\n")
        self.check("internal/testdata/spelling.golden")

    @unittest.skipUnless(ENGINE_CONFIG.is_file(), "vendored policy without engine config")
    def test_engine_config_skips_generated_paths_and_checks_owned_prose(self):
        """Positive and negative test proving engine config skips generated assets while rejecting typos in owned documentation."""
        self.stage(".codespellrc", ENGINE_CONFIG.read_text(encoding="utf-8"))
        names = (
            "internal/clientschema/upstream/mcp/schema.json",
            "internal/testdata/spelling.golden",
            "testdata/spelling.golden",
            "tools/figures/player/player.js",
            "tools/figures/third_party/library.js",
            ".devcontainer/praetor-source.000.b64",
            "package-lock.json",
            "pnpm-lock.yaml",
            "go.sum",
            "CHANGELOG.md",
        )
        for name in names:
            # codespell:ignore-next-line occured,seperate
            self.stage(name, '{"note": "occured seperate"}\n')
        self.check(*names)
        # codespell:ignore-next-line occured
        self.stage("docs/guide.md", "occured\n")
        with self.assertRaisesRegex(HookError, r"docs/guide.md:1:"):
            self.check("docs/guide.md")

    def test_hidden_staged_text_is_checked(self):
        """Negative test proving staged dotfiles are inspected and fail when planted typos are present."""
        # codespell:ignore-next-line occured
        self.stage(".notes.md", "occured\n")
        with self.assertRaisesRegex(HookError, r"\.notes\.md:1:"):
            self.check(".notes.md")

    def test_binary_staged_bytes_are_not_checked(self):
        """Boundary test proving files containing null bytes are treated as binary and skipped by spelling checks."""
        # codespell:ignore-next-line occured,seperate
        self.stage("asset.bin", b"\x00occured seperate\n")
        self.check("asset.bin")

    def test_empty_file_list_never_scans_the_tree(self):
        """Boundary test proving passing an empty file list executes no commands and avoids scanning the filesystem tree."""
        with mock.patch("checks.run") as execute:
            checks.text_checks(self.export, [])
        execute.assert_not_called()

    def test_unconfigured_vendored_policy_does_not_require_codespell(self):
        """Positive test proving repositories lacking a codespellrc configuration bypass checks without invoking codespell."""
        (self.export / "notes.md").write_text("Clean text.\n", encoding="utf-8")
        with mock.patch("checks.run") as execute:
            checks.text_checks(self.export, ["notes.md"])
        execute.assert_not_called()

    def test_missing_codespell_fails_closed(self):
        """Negative test proving configured spelling checks fail closed when the codespell binary is missing from PATH."""
        self.stage("notes.md", "Clean text.\n")
        self.check("notes.md")
        with mock.patch("common.subprocess.Popen", side_effect=FileNotFoundError("missing")):
            with self.assertRaisesRegex(HookError, "codespell: missing"):
                checks.text_checks(self.export, ["notes.md"])

    def test_spelling_batches_keep_every_filename_and_stop_on_failure(self):
        """Boundary test proving file lists exceeding batch size partition into batches and halt immediately on failure."""
        (self.export / ".codespellrc").write_text("[codespell]\n", encoding="utf-8")
        names = [f"notes {number}.md" for number in range(checks.GOFMT_BATCH + 1)]
        with mock.patch("checks.run") as execute:
            checks.spelling_check(self.export, names)
        self.assertEqual(execute.call_count, 2)
        self.assertEqual(execute.call_args_list[0].args[0][4:], names[:-1])
        self.assertEqual(execute.call_args_list[1].args[0][4:], names[-1:])
        with mock.patch("checks.run", side_effect=HookError("spelling refused")) as execute:
            with self.assertRaisesRegex(HookError, "spelling refused"):
                checks.spelling_check(self.export, names)
        self.assertEqual(execute.call_count, 1)


if __name__ == "__main__":
    unittest.main()
