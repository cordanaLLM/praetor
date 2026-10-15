#!/usr/bin/env python3
# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
#
# SPDX-License-Identifier: EUPL-1.2
"""Self-tests of scripts/cross_vet.py (#1026). They run go vet; no network is needed."""

import sys
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))

import cross_vet

FIXTURE = Path(__file__).resolve().parent / "testdata" / "cross_vet" / "unixonly"


class CrossVet(unittest.TestCase):
    def test_planted_unix_syscall_fails_for_windows_and_names_the_file(self):
        result = cross_vet.vet_module(FIXTURE, "windows", "amd64")
        self.assertFalse(result.ok, result.output)
        self.assertIn("fifo.go", result.output)
        self.assertIn("Mkfifo", result.output)

    def test_planted_fixture_passes_where_the_syscall_exists(self):
        for goos, goarch in (("linux", "amd64"), ("darwin", "arm64")):
            with self.subTest(goos=goos):
                result = cross_vet.vet_module(FIXTURE, goos, goarch)
                self.assertTrue(result.ok, result.output)

    def test_modules_are_the_tree_without_testdata(self):
        modules = cross_vet.modules(cross_vet.ROOT)
        self.assertIn(cross_vet.ROOT, modules)
        self.assertFalse([m for m in modules if "testdata" in m.parts], modules)

    def test_main_fails_when_one_target_fails(self):
        self.assertEqual(cross_vet.main(["--module", str(FIXTURE)]), 1)


if __name__ == "__main__":
    unittest.main()
