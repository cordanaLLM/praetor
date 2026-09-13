#!/usr/bin/env python3
"""Focused tests for the bounded, data-only planning importer."""

import hashlib
import json
import os
from pathlib import Path
import sys
import tempfile
import unittest
from unittest import mock

sys.dont_write_bytecode = True
sys.path.insert(0, str(Path(__file__).parent))
import planning_import


class PlanningImportTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.root = Path(self.temp.name)
        self.source = self.root / "export"
        self.source.mkdir()
        (self.root / ".workingdir").mkdir(mode=0o700)

    def tearDown(self):
        self.temp.cleanup()

    def manifest(self, entries):
        path = self.root / "manifest.json"
        path.write_text(json.dumps({"version": 1, "entries": entries}), encoding="utf-8")
        return path

    def entry(self, name, destination=None):
        data = name.encode() * 3
        (self.source / name).write_bytes(data)
        return {"source": name, "destination": destination or name,
                "sha256": hashlib.sha256(data).hexdigest()}

    def test_import_is_untrusted_private_and_bounded(self):
        entry = self.entry("plan.txt", "nested/plan.txt")
        output = self.root / ".workingdir" / "import-1"
        report = planning_import.import_plan(self.manifest([entry]), self.source, output)
        self.assertFalse(report["verified"])
        self.assertTrue(report["untrusted"])
        self.assertEqual((output / "nested/plan.txt").read_bytes(), b"plan.txt" * 3)
        self.assertEqual((output / "nested/plan.txt").stat().st_mode & 0o777, 0o600)
        self.assertEqual((output / "report.json").stat().st_mode & 0o777, 0o600)
        self.assertEqual(output.stat().st_mode & 0o777, 0o700)

    def test_dry_run_does_not_create_output(self):
        output = self.root / ".workingdir" / "dry"
        report = planning_import.import_plan(self.manifest([self.entry("a.txt")]),
                                              self.source, output, dry_run=True)
        self.assertTrue(report["dry_run"])
        self.assertFalse(output.exists())

    def test_rejects_inventory_digest_paths_and_output(self):
        entry = self.entry("a.txt")
        bad = dict(entry, sha256="0" * 64)
        with self.assertRaises(planning_import.ImportErrorValue):
            planning_import.import_plan(self.manifest([bad]), self.source,
                                         self.root / ".workingdir" / "bad")
        with self.assertRaises(planning_import.ImportErrorValue):
            planning_import.import_plan(self.manifest([dict(entry, destination="../x")]),
                                         self.source, self.root / ".workingdir" / "bad2")
        (self.source / "extra.txt").write_text("extra", encoding="utf-8")
        with self.assertRaises(planning_import.ImportErrorValue):
            planning_import.import_plan(self.manifest([entry]), self.source,
                                         self.root / ".workingdir" / "bad3")
        output = self.root / ".workingdir" / "existing"
        output.mkdir()
        with self.assertRaises(planning_import.ImportErrorValue):
            planning_import.import_plan(self.manifest([entry]), self.source, output)

    def test_rejects_symlink_directory_limits_and_manifest_duplicates(self):
        self.entry("a.txt")
        (self.source / "link.txt").symlink_to(self.source / "a.txt")
        with self.assertRaises(planning_import.ImportErrorValue):
            planning_import.import_plan(self.manifest([self.entry("a.txt")]), self.source,
                                         self.root / ".workingdir" / "link")
        duplicate = '{"version":1,"version":1,"entries":[]}'
        path = self.root / "duplicate.json"
        path.write_text(duplicate, encoding="utf-8")
        with self.assertRaises(planning_import.ImportErrorValue):
            planning_import.import_plan(path, self.source, self.root / ".workingdir" / "dup")
        (self.source / "a.txt").unlink()
        (self.source / "link.txt").unlink()
        huge = b"x" * (planning_import.MAX_FILE_BYTES + 1)
        (self.source / "huge").write_bytes(huge)
        huge_entry = {"source": "huge", "destination": "huge",
                      "sha256": hashlib.sha256(huge).hexdigest()}
        with self.assertRaises(planning_import.ImportErrorValue):
            planning_import.import_plan(self.manifest([huge_entry]), self.source,
                                         self.root / ".workingdir" / "huge-out")

    def test_rejects_entry_and_total_limits(self):
        entries = []
        for index in range(planning_import.MAX_ENTRIES + 1):
            name = f"f{index}"
            entries.append(self.entry(name))
        with self.assertRaises(planning_import.ImportErrorValue):
            planning_import.import_plan(self.manifest(entries), self.source,
                                         self.root / ".workingdir" / "too-many")
        for path in self.source.iterdir():
            path.unlink()
        size = planning_import.MAX_FILE_BYTES
        entries = []
        for name in ("left", "right", "over"):
            data = (name.encode() * (size // len(name) + 1))[:size]
            (self.source / name).write_bytes(data)
            entries.append({"source": name, "destination": name,
                            "sha256": hashlib.sha256(data).hexdigest()})
        data = b"zz"
        (self.source / "over").write_bytes(data)
        entries[-1]["sha256"] = hashlib.sha256(data).hexdigest()
        with self.assertRaises(planning_import.ImportErrorValue):
            planning_import.import_plan(self.manifest(entries), self.source,
                                         self.root / ".workingdir" / "too-large")

    def test_rejects_outside_and_source_symlink(self):
        entry = self.entry("a.txt")
        with self.assertRaises(planning_import.ImportErrorValue):
            planning_import.import_plan(self.manifest([entry]), self.source,
                                         self.root / "outside")
        link = self.root / "source-link"
        link.symlink_to(self.source, target_is_directory=True)
        with self.assertRaises(planning_import.ImportErrorValue):
            planning_import.import_plan(self.manifest([entry]), link,
                                         self.root / ".workingdir" / "symlink")

    def test_private_source_and_destination_collision(self):
        private_source = self.root / ".workingdir" / "export"
        private_source.mkdir()
        data = b"one"
        (private_source / "one").write_bytes(data)
        manifest = self.root / "private-manifest.json"
        manifest.write_text(json.dumps({"version": 1, "entries": [
            {"source": "one", "destination": "one", "sha256": hashlib.sha256(data).hexdigest()}]}),
                            encoding="utf-8")
        output = self.root / ".workingdir" / "private-import"
        report = planning_import.import_plan(manifest, private_source, output)
        self.assertEqual(report["entries"], 1)
        self.assertEqual((output / "one").read_bytes(), data)
        collision_source = self.root / "collision"
        collision_source.mkdir()
        entries = []
        for name, destination in (("a", "foo"), ("b", "foo/bar")):
            value = name.encode()
            (collision_source / name).write_bytes(value)
            entries.append({"source": name, "destination": destination,
                            "sha256": hashlib.sha256(value).hexdigest()})
        collision_manifest = self.root / "collision.json"
        collision_manifest.write_text(json.dumps({"version": 1, "entries": entries}), encoding="utf-8")
        with self.assertRaises(planning_import.ImportErrorValue):
            planning_import.import_plan(collision_manifest, collision_source,
                                         self.root / ".workingdir" / "collision-out")

    def test_preflight_payload_survives_source_change(self):
        entry = self.entry("stable.txt")
        original = planning_import.load_entries

        def mutate(manifest, source):
            planned, total = original(manifest, source)
            (source / "stable.txt").write_bytes(b"changed")
            return planned, total

        output = self.root / ".workingdir" / "stable-out"
        with mock.patch.object(planning_import, "load_entries", side_effect=mutate):
            planning_import.import_plan(self.manifest([entry]), self.source, output)
        self.assertEqual((output / "stable.txt").read_bytes(), b"stable.txt" * 3)

    def test_write_failure_rolls_back_fresh_output(self):
        entry = self.entry("rollback.txt")
        output = self.root / ".workingdir" / "rollback-out"
        with mock.patch.object(planning_import, "write_checked",
                               side_effect=OSError("synthetic write failure")):
            with self.assertRaises(planning_import.ImportErrorValue):
                planning_import.import_plan(self.manifest([entry]), self.source, output)
        self.assertFalse(output.exists())

    def test_relative_cli_paths_and_reserved_report_directory(self):
        entry = self.entry("a.txt")
        self.manifest([entry])
        output = self.root / ".workingdir" / "relative"
        with mock.patch("os.getcwd", return_value=str(self.root)):
            planning_import.import_plan("manifest.json", "export", ".workingdir/relative")
        self.assertTrue((output / "a.txt").is_file())
        bad = self.manifest([dict(entry, destination="report.json/child")])
        with self.assertRaises(planning_import.ImportErrorValue):
            planning_import.import_plan(bad, self.source, self.root / ".workingdir" / "reserved")

    def test_fifo_manifest_is_rejected_without_waiting_for_writer(self):
        path = self.root / "pipe.json"
        os.mkfifo(path)
        with self.assertRaises(planning_import.ImportErrorValue):
            planning_import.read_manifest(path)


if __name__ == "__main__":
    unittest.main()
