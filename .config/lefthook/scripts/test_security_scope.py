#!/usr/bin/env python3
"""Regression tests for exact Go package security scope."""

import shutil
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch
sys.dont_write_bytecode = True

SCRIPT = Path(__file__).with_name("security_scope.py")
sys.path.insert(0, str(SCRIPT.parent))
from security_scope import package_patterns  # noqa: E402
from common import HookError


class SecurityScopeTests(unittest.TestCase):
    def test_private_corpus_is_excluded_and_first_party_md5_is_scanned(self):
        if shutil.which("gosec") is None:
            self.fail("gosec is required for security scope regression")
        with tempfile.TemporaryDirectory(prefix="praetor-security-scope-") as temp:
            root = Path(temp)
            (root / "go.mod").write_text("module example.test/security\ngo 1.27\n")
            config = root / ".gosec.json"
            config.write_text('{"global":{"exclude":""}}\n')
            source = "package real\nimport \"crypto/md5\"\nfunc Hash() { md5.New() }\n"
            (root / "real.go").write_text(source)
            private = root / ".workingdir/corpus/private.go"
            private.parent.mkdir(parents=True)
            private.write_text(source)
            patterns = package_patterns(root)
            self.assertIn(".", patterns)
            self.assertTrue(all(".workingdir" not in pattern for pattern in patterns))
            result = subprocess.run(["gosec", "-conf", str(config), *patterns],
                                    cwd=root, text=True, stdout=subprocess.PIPE,
                                    stderr=subprocess.STDOUT, check=False, timeout=120)
            self.assertNotEqual(result.returncode, 0, result.stdout)
            self.assertRegex(result.stdout, "G401|G501")
            self.assertNotIn(str(private), result.stdout)
            (root / "real.go").write_text("package real\nfunc Value() int { return 1 }\n")
            clean = subprocess.run(["gosec", "-conf", str(config), *patterns],
                                   cwd=root, text=True, stdout=subprocess.PIPE,
                                   stderr=subprocess.STDOUT, check=False, timeout=120)
            self.assertEqual(clean.returncode, 0, clean.stdout)

    def test_empty_or_failed_listing_is_not_a_security_pass(self):
        with patch("security_scope.go_packages", return_value=[]):
            with self.assertRaises(HookError):
                package_patterns(Path.cwd())
        with patch("security_scope.go_packages", side_effect=HookError("listing failed")):
            with self.assertRaisesRegex(HookError, "listing failed"):
                package_patterns(Path.cwd())


if __name__ == "__main__":
    unittest.main()
