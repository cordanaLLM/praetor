#!/usr/bin/env python3
# SPDX-FileCopyrightText: 2026 Lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2
"""Positive, negative and boundary fixtures for Platform Neutrality scope."""

import contextlib
import io
import os
import shutil
import sys
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

sys.dont_write_bytecode = True
sys.path.insert(0, str(Path(__file__).resolve().parent))

import ci_scope
from common import HookError, clean_env, run


class CIScopeTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="praetor-ci-scope-")
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name).resolve()
        self.env = patch.dict(
            os.environ, {"GOPROXY": "off", "GOSUMDB": "off", "GOWORK": "off"}
        )
        self.env.start()
        self.addCleanup(self.env.stop)
        files = {
            "go.mod": "module example.test/scope\ngo 1.27\n",
            "a/a.go": "package a\nconst Value = 1\n",
            "importer/importer.go": 'package importer\nimport "example.test/scope/a"\nconst Value = a.Value\n',
            "other/other.go": "package other\n",
            "cmd/standardsctl/main.go": "package main\nfunc main() {}\n",
            "internal/adopt/adopt.go": "package adopt\n",
            "only/only_test.go": 'package only\nimport "testing"\nfunc TestOnly(t *testing.T) {}\n',
            "tools/schemacheck/go.mod": "module example.test/schema\ngo 1.27\nrequire example.test/scope v0.0.0\nreplace example.test/scope => ../..\n",
            "tools/schemacheck/schema.go": 'package schema\nimport "example.test/scope/a"\nconst Value = a.Value\n',
            "embed/embed.go": 'package embed\nimport _ "embed"\n//go:embed input.txt\nvar Input string\n',
            "embed/input.txt": "input\n",
            "a/testdata/input.txt": "fixture\n",
        }
        for name, text in files.items():
            self.write(name, text)
        self.git("init", "-q")
        self.commit()
        self.base = self.git("rev-parse", "HEAD").decode().strip()

    def write(self, name, text):
        path = self.root / name
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(text, encoding="utf-8")

    def git(self, *args):
        return run(["git", *args], cwd=self.root, env=clean_env(), timeout=30)

    def commit(self):
        self.git("add", ".")
        self.git(
            "-c",
            "user.name=Scope Fixture",
            "-c",
            "user.email=fixture@example.test",
            "-c",
            "commit.gpgsign=false",
            "commit",
            "-qm",
            "fixture",
        )

    def changed(self, name, text="changed\n", event="pull_request"):
        self.write(name, text)
        self.commit()
        return ci_scope.scope(self.root, event, self.base, "HEAD")

    def test_go_file_selects_own_package_and_reverse_importers(self):
        mode, count, _, flags, packages = self.changed(
            "a/a.go", "package a\nconst Value = 2\n"
        )
        self.assertEqual((mode, count), ("selected", 1))
        self.assertEqual(
            packages, ["example.test/scope/a", "example.test/scope/importer"]
        )
        self.assertTrue(flags["go"])
        self.assertTrue(flags["schemacheck"])
        self.assertFalse(flags["harness"])

    def test_unmatched_path_runs_full_scope_with_path_reason(self):
        result = self.changed("unknown/settings.toml")
        self.assertEqual(result[0], "full")
        self.assertIn("unknown/settings.toml", result[2])
        self.assertTrue(all(result[3].values()))
        self.assertIn("example.test/scope/other", result[4])

    def test_push_and_dispatch_keep_every_step(self):
        for event in ("push", "workflow_dispatch"):
            with self.subTest(event=event):
                result = ci_scope.scope(self.root, event, self.base, "HEAD")
                self.assertEqual(result[0], "full")
                self.assertIn(event, result[2])
                self.assertTrue(all(result[3].values()))

    def test_git_failure_reports_full_scope(self):
        result = ci_scope.scope(self.root, "pull_request", "absent", "HEAD")
        self.assertEqual(result[0], "full")
        self.assertIn("git diff failed", result[2])
        self.assertIn("count unavailable", result[2])

    def test_go_failure_reports_full_scope_and_explicit_pattern(self):
        self.write("a/a.go", "package a\nconst Value = 3\n")
        self.commit()
        with patch(
            "ci_scope.go_packages", side_effect=HookError("go list fixture failure")
        ):
            result = ci_scope.scope(self.root, "pull_request", self.base, "HEAD")
        self.assertEqual(result[0], "full")
        self.assertIn("Go scope discovery failed", result[2])
        self.assertIn("go list fixture failure", result[2])
        self.assertEqual(result[4], ["./..."])
        self.assertTrue(all(result[3].values()))

    def test_zero_files_selects_no_steps(self):
        result = ci_scope.scope(self.root, "pull_request", self.base, "HEAD")
        self.assertEqual(result[0:2], ("selected", 0))
        self.assertFalse(any(result[3].values()))
        self.assertEqual(result[4], [])
        self.assertIn("Documentation References: run", ci_scope.report(result))
        self.assertIn("Test Every Package: skipped; reason:", ci_scope.report(result))

    def test_docs_only_selects_figures(self):
        result = self.changed("docs/guide.md")
        self.assertEqual(result[0:2], ("selected", 1))
        self.assertEqual(
            [name for name, value in result[3].items() if value], ["figures"]
        )
        self.assertEqual(result[4], [])

    def test_nested_module_does_not_select_main_module(self):
        result = self.changed("tools/schemacheck/schema.go", "package schema\n")
        self.assertEqual(result[0], "selected")
        self.assertEqual(
            [name for name, value in result[3].items() if value], ["schemacheck"]
        )
        self.assertEqual(result[4], [])

    def test_go_config_selects_every_package(self):
        result = self.changed(
            "go.mod", "module example.test/scope\ngo 1.27\n// changed\n"
        )
        self.assertEqual(result[0], "full")
        self.assertEqual(len(result[4]), 7)
        self.assertTrue(all(result[3].values()))

    def test_embedded_and_testdata_inputs_reuse_package_selector(self):
        for name, package in (
            ("embed/input.txt", "embed"),
            ("a/testdata/input.txt", "a"),
        ):
            with self.subTest(name=name):
                result = self.changed(name)
                self.assertEqual(result[0], "selected")
                self.assertIn("example.test/scope/" + package, result[4])

    def test_step_inputs_and_global_inputs(self):
        for name, expected in (
            ("scripts/sync_interfig.py", {"harness", "interfig"}),
            ("tools/figures/third_party/interfig/a.ts", {"figures", "interfig"}),
            ("tools/markdownlint/verify.mjs", {"markdown"}),
            ("README.md", {"figures"}),
            ("changelog.d/change.md", set()),
            ("LICENSES/EUPL-1.2.txt", set()),
        ):
            with self.subTest(name=name):
                result = ci_scope.selected_scope(self.root, [name])
                self.assertEqual(result[0], "selected")
                self.assertEqual(
                    {key for key, value in result[3].items() if value}, expected
                )
        for name in (
            ".github/workflows/x.yml",
            ".config/foo",
            "tools/go/go.sum",
            "tools/lefthook/go.mod",
            "scripts/portability_selftest.py",
        ):
            with self.subTest(name=name):
                self.assertEqual(ci_scope.selected_scope(self.root, [name])[0], "full")

    def test_cli_and_adopt_flags_follow_selected_packages(self):
        for name, flag in (
            ("cmd/standardsctl/main.go", "cli"),
            ("internal/adopt/adopt.go", "adopt"),
            ("only/only_test.go", "go"),
        ):
            with self.subTest(name=name):
                result = ci_scope.selected_scope(self.root, [name])
                self.assertTrue(result[3][flag])
                self.assertEqual(result[3]["harness"], flag == "cli")
                self.assertEqual(len(result[4]), 1)

    def test_cli_writes_actions_outputs_summary_and_packages(self):
        self.changed("a/a.go", "package a\nconst Value = 4\n")
        output, summary, packages = [
            self.root / name for name in ("output", "summary", "packages")
        ]
        with (
            patch("ci_scope.Path.cwd", return_value=self.root),
            contextlib.redirect_stdout(io.StringIO()) as log,
        ):
            code = ci_scope.main(
                [
                    "--event",
                    "pull_request",
                    "--base",
                    self.base,
                    "--head",
                    "HEAD",
                    "--output",
                    str(output),
                    "--summary",
                    str(summary),
                    "--packages",
                    str(packages),
                ]
            )
        self.assertEqual(code, 0)
        self.assertIn("files=1\n", output.read_text())
        self.assertIn("go=true\n", output.read_text())
        self.assertIn("adopt=false\n", output.read_text())
        self.assertEqual(summary.read_text(), log.getvalue())
        self.assertIn("changed files: 1", log.getvalue())
        self.assertEqual(
            packages.read_text().splitlines(),
            ["example.test/scope/a", "example.test/scope/importer"],
        )
        # Bytes, not read_text: universal newlines would hide the carriage return that
        # bash `read -r` keeps on the Windows leg, where this suite also runs.
        self.assertNotIn(b"\r", packages.read_bytes())
        self.assertNotIn(b"\r", output.read_bytes())

    def test_removed_package_selects_full_scope(self):
        (self.root / "other/other.go").unlink()
        self.commit()
        result = ci_scope.scope(self.root, "pull_request", self.base, "HEAD")
        self.assertEqual(result[0], "full")
        self.assertIn("other/other.go", result[2])


# The workflow's run steps use `shell: bash`, which Actions resolves on PATH (Git Bash on the
# Windows image). A bare "bash" given to CreateProcess searches System32 before PATH and starts
# the WSL launcher instead, which has no distribution on the runner; resolve it the way Actions does.
BASH = shutil.which("bash")


@unittest.skipUnless(
    BASH, "no bash on PATH: the workflow's bash steps cannot be replayed on this host"
)
class WorkflowCommandsTests(unittest.TestCase):
    def step(self, name):
        workflow = (
            Path(__file__).resolve().parents[3] / ".github/workflows/portability.yml"
        )
        text = workflow.read_text(encoding="utf-8").split(
            "      - name: " + name + "\n", 1
        )[1]
        text = text.split("\n      - name:", 1)[0]
        body = text.split("        run: |\n", 1)[1].split("        env:", 1)[0]
        return "\n".join(
            line[10:] for line in body.splitlines() if line.startswith("          ")
        )

    def execute(self, temp, step, packages, adopt="false", cli="false", fail="false"):
        root = Path(temp)
        (root / "portability-packages.txt").write_text(
            "".join(p + "\n" for p in packages), encoding="utf-8"
        )
        # An in-process shell stub exercises the actual workflow command text, including
        # its array handling and all four runs, without installing tools or running suites.
        stub = r"""go() {
          printf '%s\n' "$*" >> "$RUNNER_TEMP/calls"
          case "$1" in
            list)
              shift
              case "$1" in
                ./internal/adopt) echo example.test/scope/internal/adopt ;;
                ./cmd/standardsctl) echo example.test/scope/cmd/standardsctl ;;
                -f) shift 2; for package in "$@"; do
                      if [ "$package" != example.test/scope/only ]; then echo "$package"; fi
                    done ;;
                *) printf '%s\n' "$@" ;;
              esac ;;
            test)
              TEST_COUNT=$(( ${TEST_COUNT:-0} + 1 ))
              if [ "$FAIL_GO" = true ] || { [ "$FAIL_GO" = first ] && [ "$TEST_COUNT" -eq 1 ]; }; then return 9; fi ;;
          esac
        }
        """
        env = dict(
            clean_env(),
            RUNNER_TEMP=root.as_posix(),
            RUNNER_OS="fixture",
            PYTHON=sys.executable,
            ADOPT=adopt,
            CLI=cli,
            FAIL_GO=fail,
        )
        setup = (
            self.step("Select Buildable Go Packages")
            if step in {"Compile Every Package", "Vet Every Package"}
            else ""
        )
        output = run(
            [
                BASH,
                "-e",
                "-o",
                "pipefail",
                "-c",
                stub + setup + "\n" + self.step(step),
            ],
            env=env,
            timeout=30,
        )
        return output.decode(), (root / "calls").read_text().splitlines()

    def test_only_selected_partition_runs(self):
        for package, flag, runs in (
            ("other", "", 1),
            ("internal/adopt", "adopt", 1),
            ("cmd/standardsctl", "cli", 2),
        ):
            with self.subTest(package=package), tempfile.TemporaryDirectory() as temp:
                output, calls = self.execute(
                    temp,
                    "Test Every Package",
                    ["example.test/scope/" + package],
                    adopt=str(flag == "adopt").lower(),
                    cli=str(flag == "cli").lower(),
                )
                tests = [call for call in calls if call.startswith("test ")]
                self.assertEqual(len(tests), runs)
                self.assertTrue(
                    all("example.test/scope/" + package in call for call in tests)
                )
                self.assertIn("skipped:", output)
                self.assertTrue(all("./..." not in call for call in tests))

    def test_failures_do_not_drop_later_runs(self):
        with tempfile.TemporaryDirectory() as temp:
            with self.assertRaisesRegex(HookError, "exited 9"):
                self.execute(
                    temp,
                    "Test Every Package",
                    ["example.test/scope/other"],
                    adopt="true",
                    cli="true",
                    fail="true",
                )
            calls = (Path(temp) / "calls").read_text().splitlines()
            self.assertEqual(
                len([call for call in calls if call.startswith("test ")]), 4
            )

    def test_build_and_vet_exclude_test_only_packages(self):
        for step, command in (
            ("Compile Every Package", "build"),
            ("Vet Every Package", "vet"),
        ):
            for packages in (
                ["example.test/scope/only"],
                ["example.test/scope/only", "example.test/scope/other"],
            ):
                with (
                    self.subTest(step=step, packages=packages),
                    tempfile.TemporaryDirectory() as temp,
                ):
                    output, calls = self.execute(temp, step, packages)
                    commands = [
                        call for call in calls if call.startswith(command + " ")
                    ]
                    if len(packages) == 1:
                        self.assertEqual(commands, [])
                        self.assertIn(
                            "skipped: selected packages contain tests only", output
                        )
                    else:
                        self.assertEqual(
                            commands, [command + " example.test/scope/other"]
                        )


if __name__ == "__main__":
    unittest.main()
