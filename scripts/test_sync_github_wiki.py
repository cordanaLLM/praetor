#!/usr/bin/env python3
"""Hermetic regression tests for the GitHub wiki mirror script."""

from __future__ import annotations

import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import unittest


ROOT = Path(__file__).resolve().parents[1]
SCRIPT = ROOT / "scripts" / "sync_github_wiki.sh"
# The wiki-side record of the pages the previous sync published.
MANIFEST = ".praetor-wiki-manifest"
# Git localizes its diagnostics; pin the message catalogue so assertions on Git
# output hold on developer workstations with non-English locales.
LOCALE_ENV = {"LC_ALL": "C", "LANGUAGE": "C"}


def run(
    *args: str,
    cwd: Path | None = None,
    check: bool = True,
    extra_env: dict[str, str] | None = None,
) -> subprocess.CompletedProcess[str]:
    return subprocess.run(
        args,
        cwd=cwd,
        check=check,
        text=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        env={**os.environ, **LOCALE_ENV, "WIKI_TOKEN": "", **(extra_env or {})},
        timeout=10,
    )


def init_bare(path: Path) -> None:
    run("git", "init", "--bare", "--initial-branch=master", str(path))


def seed_remote(
    remote: Path, files: dict[str, str], links: dict[str, str] | None = None
) -> None:
    work = remote.parent / "seed"
    run("git", "clone", str(remote), str(work))
    for name, content in files.items():
        (work / name).write_text(content, encoding="utf-8")
    for name, target in (links or {}).items():
        (work / name).symlink_to(target)
    run("git", "add", ".", cwd=work)
    run(
        "git",
        "-c",
        "user.name=Wiki Test",
        "-c",
        "user.email=wiki-test@example.invalid",
        "commit",
        "-m",
        "seed",
        cwd=work,
    )
    run("git", "push", "origin", "master", cwd=work)


def remote_files(remote: Path) -> dict[str, str]:
    checkout = remote.parent / "inspection"
    run("git", "clone", str(remote), str(checkout))
    return {
        path.name: path.read_text(encoding="utf-8")
        for path in checkout.iterdir()
        if path.is_file()
    }


def remote_head(remote: Path) -> str:
    return run("git", "--git-dir", str(remote), "rev-parse", "HEAD").stdout


def make_source(root: Path, pages: dict[str, str]) -> Path:
    source = root / "wiki-source"
    source.mkdir()
    for name, content in pages.items():
        (source / name).write_text(content, encoding="utf-8")
    return source


# The sync is a bash script; Windows has no POSIX shell to run it in, so the suite
# states that rather than reporting a pass it did not earn (HISS-21).
@unittest.skipIf(sys.platform == "win32", "bash script; no POSIX shell on Windows")
class WikiSyncTests(unittest.TestCase):
    def test_mirrors_from_absolute_source_and_preserves_unrelated_pages(self) -> None:
        with tempfile.TemporaryDirectory(prefix="praetor-wiki-test-") as directory:
            root = Path(directory)
            source = root / "source" / "docs" / "wiki"
            source.mkdir(parents=True)
            (source / "Home.md").write_text("# Current\n", encoding="utf-8")
            (source / "Guide.md").write_text("# Guide\n", encoding="utf-8")
            remote = root / "wiki.git"
            init_bare(remote)
            seed_remote(remote, {"Home.md": "# Old\n", "Unmanaged.md": "keep me\n"})

            result = run(str(SCRIPT), str(source), str(remote), cwd=root / "source")

            self.assertIn("Wiki sync completed successfully", result.stdout)
            self.assertEqual(
                remote_files(remote),
                {
                    "Guide.md": "# Guide\n",
                    "Home.md": "# Current\n",
                    "Unmanaged.md": "keep me\n",
                    MANIFEST: "Guide.md\nHome.md\n",
                },
            )

    def test_reachable_empty_remote_is_initialized(self) -> None:
        with tempfile.TemporaryDirectory(prefix="praetor-wiki-test-") as directory:
            root = Path(directory)
            source = root / "wiki-source"
            source.mkdir()
            (source / "Home.md").write_text("# First\n", encoding="utf-8")
            remote = root / "wiki.git"
            init_bare(remote)

            run(str(SCRIPT), str(source), str(remote))

            self.assertEqual(
                remote_files(remote), {"Home.md": "# First\n", MANIFEST: "Home.md\n"}
            )

    def test_unchanged_remote_does_not_create_another_commit(self) -> None:
        with tempfile.TemporaryDirectory(prefix="praetor-wiki-test-") as directory:
            root = Path(directory)
            source = root / "wiki-source"
            source.mkdir()
            (source / "Home.md").write_text("# Stable\n", encoding="utf-8")
            remote = root / "wiki.git"
            init_bare(remote)
            seed_remote(remote, {"Home.md": "# Stable\n", MANIFEST: "Home.md\n"})
            before = remote_head(remote)

            result = run(str(SCRIPT), str(source), str(remote))

            self.assertIn("already up to date", result.stdout)
            self.assertEqual(remote_head(remote), before)

    def test_page_removed_from_source_is_deleted_and_unmanaged_page_kept(self) -> None:
        with tempfile.TemporaryDirectory(prefix="praetor-wiki-test-") as directory:
            root = Path(directory)
            source = make_source(root, {"Home.md": "# Home\n"})
            remote = root / "wiki.git"
            init_bare(remote)
            seed_remote(
                remote,
                {
                    "Home.md": "# Home\n",
                    "Retired.md": "# Retired\n",
                    "Unmanaged.md": "keep me\n",
                    MANIFEST: "Home.md\nRetired.md\n",
                },
            )

            result = run(str(SCRIPT), str(source), str(remote))

            self.assertIn("Removing wiki page no longer in source: Retired.md", result.stdout)
            self.assertEqual(
                remote_files(remote),
                {"Home.md": "# Home\n", "Unmanaged.md": "keep me\n", MANIFEST: "Home.md\n"},
            )

    def test_first_run_without_manifest_deletes_nothing_and_records_one(self) -> None:
        with tempfile.TemporaryDirectory(prefix="praetor-wiki-test-") as directory:
            root = Path(directory)
            source = make_source(root, {"Home.md": "# Home\n"})
            remote = root / "wiki.git"
            init_bare(remote)
            seed_remote(remote, {"Home.md": "# Home\n", "Earlier.md": "# Earlier\n"})

            result = run(str(SCRIPT), str(source), str(remote))

            self.assertNotIn("Removing wiki page", result.stdout)
            self.assertEqual(
                remote_files(remote),
                {"Home.md": "# Home\n", "Earlier.md": "# Earlier\n", MANIFEST: "Home.md\n"},
            )

    def test_manifest_entry_is_a_literal_name_never_a_glob(self) -> None:
        with tempfile.TemporaryDirectory(prefix="praetor-wiki-test-") as directory:
            root = Path(directory)
            source = make_source(root, {"Home.md": "# Home\n"})
            remote = root / "wiki.git"
            init_bare(remote)
            seed_remote(
                remote,
                {"Home.md": "# Home\n", "Operator.md": "keep me\n", MANIFEST: "*.md\nHome.md\n"},
            )

            run(str(SCRIPT), str(source), str(remote))

            self.assertEqual(
                remote_files(remote),
                {"Home.md": "# Home\n", "Operator.md": "keep me\n", MANIFEST: "Home.md\n"},
            )

    def test_manifest_over_the_page_limit_is_rejected_without_changes(self) -> None:
        with tempfile.TemporaryDirectory(prefix="praetor-wiki-test-") as directory:
            root = Path(directory)
            source = make_source(root, {"Home.md": "# New\n"})
            remote = root / "wiki.git"
            init_bare(remote)
            listed = "".join(f"Page-{index:03}.md\n" for index in range(257))
            seed_remote(remote, {"Home.md": "# Old\n", MANIFEST: listed})
            before = remote_head(remote)

            result = run(str(SCRIPT), str(source), str(remote), check=False)

            self.assertNotEqual(result.returncode, 0)
            self.assertIn("lists more than 256 pages", result.stderr)
            self.assertEqual(remote_head(remote), before)

    def test_manifest_at_the_page_limit_removes_every_stale_page(self) -> None:
        with tempfile.TemporaryDirectory(prefix="praetor-wiki-test-") as directory:
            root = Path(directory)
            source = make_source(root, {"Home.md": "# Home\n"})
            remote = root / "wiki.git"
            init_bare(remote)
            stale = {f"Page-{index:03}.md": "old\n" for index in range(255)}
            listed = "".join(f"{name}\n" for name in [*stale, "Home.md"])
            seed_remote(remote, {**stale, "Home.md": "# Home\n", MANIFEST: listed})

            run(str(SCRIPT), str(source), str(remote))

            self.assertEqual(
                remote_files(remote), {"Home.md": "# Home\n", MANIFEST: "Home.md\n"}
            )

    def test_manifest_entry_outside_the_wiki_root_is_rejected(self) -> None:
        with tempfile.TemporaryDirectory(prefix="praetor-wiki-test-") as directory:
            root = Path(directory)
            source = make_source(root, {"Home.md": "# New\n"})
            remote = root / "wiki.git"
            init_bare(remote)
            seed_remote(remote, {"Home.md": "# Old\n", MANIFEST: "../Home.md\n"})
            before = remote_head(remote)

            result = run(str(SCRIPT), str(source), str(remote), check=False)

            self.assertNotEqual(result.returncode, 0)
            self.assertIn("not a Markdown page name", result.stderr)
            self.assertEqual(remote_head(remote), before)

    def test_symlinked_manifest_is_rejected(self) -> None:
        with tempfile.TemporaryDirectory(prefix="praetor-wiki-test-") as directory:
            root = Path(directory)
            source = make_source(root, {"Home.md": "# New\n"})
            remote = root / "wiki.git"
            init_bare(remote)
            seed_remote(remote, {"Home.md": "# Old\n"}, links={MANIFEST: "Home.md"})
            before = remote_head(remote)

            result = run(str(SCRIPT), str(source), str(remote), check=False)

            self.assertNotEqual(result.returncode, 0)
            self.assertIn("is not a regular file", result.stderr)
            self.assertEqual(remote_head(remote), before)

    def test_source_name_with_a_line_break_is_rejected_before_clone(self) -> None:
        with tempfile.TemporaryDirectory(prefix="praetor-wiki-test-") as directory:
            root = Path(directory)
            source = make_source(root, {"Home.md": "# Page\n", "Two\nLines.md": "x\n"})

            result = run(str(SCRIPT), str(source), str(root / "missing.git"), check=False)

            self.assertNotEqual(result.returncode, 0)
            self.assertIn("must not contain line breaks", result.stderr)
            self.assertNotIn("could not be cloned", result.stderr)

    def test_missing_remote_fails_without_fallback_initialization(self) -> None:
        with tempfile.TemporaryDirectory(prefix="praetor-wiki-test-") as directory:
            root = Path(directory)
            source = root / "wiki-source"
            source.mkdir()
            (source / "Home.md").write_text("# Page\n", encoding="utf-8")
            missing = root / "missing.git"

            result = run(str(SCRIPT), str(source), str(missing), check=False)

            self.assertNotEqual(result.returncode, 0)
            self.assertIn("could not be cloned", result.stderr)
            self.assertIn("does not exist", result.stderr)
            self.assertIn("initial page", result.stderr)
            self.assertFalse(missing.exists())

    def test_credential_bearing_remote_is_rejected_without_echoing_secret(self) -> None:
        with tempfile.TemporaryDirectory(prefix="praetor-wiki-test-") as directory:
            source = Path(directory) / "wiki-source"
            source.mkdir()
            (source / "Home.md").write_text("# Page\n", encoding="utf-8")

            result = run(
                str(SCRIPT),
                str(source),
                "https://user:test-value@example.invalid/wiki.git",
                check=False,
            )

            self.assertNotEqual(result.returncode, 0)
            self.assertIn("must not contain credentials", result.stderr)
            self.assertNotIn("test-value", result.stdout + result.stderr)

    def test_source_file_limit_is_enforced_before_clone(self) -> None:
        with tempfile.TemporaryDirectory(prefix="praetor-wiki-test-") as directory:
            root = Path(directory)
            source = root / "wiki-source"
            source.mkdir()
            for index in range(257):
                (source / f"Page-{index:03}.md").write_text("page\n", encoding="utf-8")

            result = run(str(SCRIPT), str(source), str(root / "missing.git"), check=False)

            self.assertNotEqual(result.returncode, 0)
            self.assertIn("at most 256", result.stderr)
            self.assertNotIn("could not be cloned", result.stderr)

    def test_token_auth_ignores_ambient_credential_configuration(self) -> None:
        with tempfile.TemporaryDirectory(prefix="praetor-wiki-test-") as directory:
            root = Path(directory)
            source = root / "wiki-source"
            source.mkdir()
            (source / "Home.md").write_text("# Page\n", encoding="utf-8")
            remote = root / "wiki.git"
            init_bare(remote)
            real_git = shutil.which("git")
            self.assertIsNotNone(real_git)
            bin_dir = root / "bin"
            bin_dir.mkdir()
            env_log = root / "git-env.log"
            wrapper = bin_dir / "git"
            wrapper.write_text(
                "#!/usr/bin/env bash\n"
                "printf '%s|%s|%s|%s|%s\\n' "
                '"${GIT_CONFIG_GLOBAL-}" "${GIT_CONFIG_NOSYSTEM-}" '
                '"${GIT_TERMINAL_PROMPT-}" "${GIT_ASKPASS-}" '
                '"${GIT_CONFIG_COUNT-}" >>"$GIT_ENV_LOG"\n'
                f'exec "{real_git}" "$@"\n',
                encoding="utf-8",
            )
            wrapper.chmod(0o700)

            result = run(
                str(SCRIPT),
                str(source),
                str(remote),
                extra_env={
                    "PATH": f"{bin_dir}:{os.environ['PATH']}",
                    "GIT_ENV_LOG": str(env_log),
                    "GIT_ASKPASS": "/ambient/askpass",
                    "GIT_CONFIG_GLOBAL": "/ambient/gitconfig",
                    "GIT_CONFIG_NOSYSTEM": "0",
                    "GIT_CONFIG_COUNT": "1",
                    "GIT_CONFIG_KEY_0": "credential.helper",
                    "GIT_CONFIG_VALUE_0": "ambient-helper",
                    "WIKI_TOKEN": "test-token-value",
                },
            )

            observations = env_log.read_text(encoding="utf-8").splitlines()
            self.assertGreater(len(observations), 0)
            for observation in observations:
                global_config, no_system, prompt, askpass, config_count = observation.split("|")
                self.assertEqual(global_config, "/dev/null")
                self.assertEqual(no_system, "1")
                self.assertEqual(prompt, "0")
                self.assertTrue(askpass.endswith("/git-askpass.sh"))
                self.assertEqual(config_count, "")
            self.assertNotIn("test-token-value", result.stdout + result.stderr)

    def test_invalid_git_timeout_is_rejected_before_clone(self) -> None:
        with tempfile.TemporaryDirectory(prefix="praetor-wiki-test-") as directory:
            root = Path(directory)
            source = root / "wiki-source"
            source.mkdir()
            (source / "Home.md").write_text("# Page\n", encoding="utf-8")

            result = run(
                str(SCRIPT),
                str(source),
                str(root / "missing.git"),
                check=False,
                extra_env={"WIKI_GIT_TIMEOUT_SECONDS": "301"},
            )

            self.assertNotEqual(result.returncode, 0)
            self.assertIn("integer from 1 through 300", result.stderr)
            self.assertNotIn("could not be cloned", result.stderr)

    def test_git_clone_timeout_terminates_the_git_process(self) -> None:
        with tempfile.TemporaryDirectory(prefix="praetor-wiki-test-") as directory:
            root = Path(directory)
            source = root / "wiki-source"
            source.mkdir()
            (source / "Home.md").write_text("# Page\n", encoding="utf-8")
            real_git = shutil.which("git")
            self.assertIsNotNone(real_git)
            bin_dir = root / "bin"
            bin_dir.mkdir()
            wrapper = bin_dir / "git"
            wrapper.write_text(
                "#!/usr/bin/env bash\n"
                "if [[ \" $* \" == *\" clone \"* ]]; then sleep 30; fi\n"
                f'exec "{real_git}" "$@"\n',
                encoding="utf-8",
            )
            wrapper.chmod(0o700)

            result = run(
                str(SCRIPT),
                str(source),
                str(root / "unused.git"),
                check=False,
                extra_env={
                    "PATH": f"{bin_dir}:{os.environ['PATH']}",
                    "WIKI_GIT_TIMEOUT_SECONDS": "1",
                },
            )

            self.assertNotEqual(result.returncode, 0)
            self.assertIn("exceeded the 1-second Git timeout", result.stderr)


if __name__ == "__main__":
    unittest.main()
