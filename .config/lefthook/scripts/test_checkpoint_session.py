# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2
"""Exercise session locking with temporary files and no external configuration."""

import contextlib
import io
import json
import os
import subprocess
import sys
import tempfile
import time
import unittest
from pathlib import Path
from unittest import mock

import checkpoint_session as sessions
from common import HookBudget, HookError


class OwnedSessionLock(unittest.TestCase):
    """Positive, negative and boundary cases for owned session transactions."""

    def setUp(self):
        """Create an isolated session record and lock path."""
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.directory = Path(self.temp.name)
        self.record = self.directory / "session.json"
        self.lock = self.record.with_suffix(".lock")

    def age_lock(self, age):
        """Set the lock age and return the exact wall clock used."""
        now = time.time()
        os.utime(self.lock, (now - age, now - age))
        return now

    def test_sweep_failure_preserves_committed_write(self):
        """Report cleanup failure without turning committed ownership into failure."""
        for error in (
            HookError("hook budget exhausted"),
            HookError("checkpoint session lock ownership lost"),
            OSError("session sweep unavailable"),
        ):
            with self.subTest(error=error), mock.patch.object(
                sessions, "_prune_session_records", side_effect=error,
            ), contextlib.redirect_stderr(io.StringIO()) as errors:
                self.record.unlink(missing_ok=True)
                sessions._update_session_record(
                    self.directory, self.record, {str(self.directory)}, HookBudget(2)
                )
                self.assertEqual(json.loads(self.record.read_text()), [str(self.directory)])
                self.assertEqual(len(errors.getvalue().splitlines()), 1)
                self.assertIn(str(error), errors.getvalue())

    def test_transaction_release_failure_still_raises(self):
        """Keep transaction lock errors fatal even when replacement has committed."""
        with mock.patch.object(
            sessions, "_release_session_lock", side_effect=HookError("transaction lock lost"),
        ), self.assertRaisesRegex(HookError, "transaction lock lost"):
            sessions._update_session_record(
                self.directory, self.record, {str(self.directory)}, HookBudget(2)
            )

    def test_normal_acquire_names_owner_and_release_removes_lock(self):
        """Publish an owner token and remove it with its lock on normal exit."""
        with sessions._session_update_lock(self.directory, self.record, HookBudget(2)):
            self.assertEqual(len(list(self.lock.iterdir())), 1)
        self.assertFalse(self.lock.exists())

    def test_second_process_cannot_remove_live_holder(self):
        """Refuse contention in a separate interpreter without changing the holder."""
        with sessions._session_update_lock(self.directory, self.record, HookBudget(2)):
            before = list(self.lock.iterdir())
            code = ("import sys; from pathlib import Path; import checkpoint_session as s; "
                    "assert not s._acquire_session_lock(Path(sys.argv[1]))")
            result = subprocess.run(
                [sys.executable, "-B", "-c", code, str(self.lock)],
                cwd=Path(__file__).parent, timeout=2, check=False, capture_output=True,
            )
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual(list(self.lock.iterdir()), before)
            self.assertEqual(len(before), 1)

    def test_old_holder_cannot_remove_reclaimer_lock(self):
        """Fail the old holder while preserving the replacement owner's token."""
        with (
            self.assertRaisesRegex(HookError, "lock.*lost"),
            sessions._session_update_lock(self.directory, self.record, HookBudget(2)),
        ):
            self.age_lock(sessions.SESSION_LOCK_STALE_SECONDS + 1)
            self.assertTrue(sessions._acquire_session_lock(self.lock))
            replacement = list(self.lock.iterdir())
        self.assertEqual(list(self.lock.iterdir()), replacement)
        self.assertEqual(len(replacement), 1)

    def test_lost_lock_refuses_record_write(self):
        """Leave ownership data untouched when the lock disappears during reading."""
        self.record.write_text("[]")

        def lose_lock(_record, *args, **kwargs):
            """Replace the lock after the caller has acquired it."""
            for owner in list(self.lock.iterdir())[:2]:
                owner.unlink()
            self.lock.rmdir()
            self.assertTrue(sessions._acquire_session_lock(self.lock))
            return set()

        with mock.patch.object(sessions, "_read_session_record", side_effect=lose_lock), \
                self.assertRaisesRegex(HookError, "lock.*lost"):
            sessions._update_session_record(
                self.directory, self.record, {str(self.directory)}, HookBudget(2)
            )
        self.assertEqual(json.loads(self.record.read_text()), [])

    def test_exact_stale_age_remains_owned(self):
        """Keep a lock at the exact stale threshold."""
        with sessions._session_update_lock(self.directory, self.record, HookBudget(2)):
            now = self.age_lock(sessions.SESSION_LOCK_STALE_SECONDS)
            with mock.patch.object(sessions.time, "time", return_value=now):
                self.assertFalse(sessions._acquire_session_lock(self.lock))
            self.assertEqual(len(list(self.lock.iterdir())), 1)

    def test_leftover_tombstone_does_not_block_reclaim(self):
        """Recover an expired lock beside an abandoned reclaim token."""
        tombstone = self.directory / "session.lock.abandoned.reclaimed"
        tombstone.write_text("old owner")
        token = sessions._acquire_session_lock(self.lock)
        self.assertTrue(token)
        self.age_lock(sessions.SESSION_LOCK_STALE_SECONDS + 1)
        self.assertTrue(sessions._acquire_session_lock(self.lock))
        self.assertEqual(len(list(self.lock.iterdir())), 1)
        self.assertEqual(tombstone.read_text(), "old owner")

    def test_two_reclaimers_cannot_both_claim_observed_owner(self):
        """Interleave stale observations so only the first reclaimer acquires."""
        self.assertTrue(sessions._acquire_session_lock(self.lock))
        self.age_lock(sessions.SESSION_LOCK_STALE_SECONDS + 1)
        lstat = Path.lstat
        replacement = []

        def competing_reclaimer(path, *args, **kwargs):
            """Acquire a replacement after the outer reclaimer observed stale state."""
            info = lstat(path, *args, **kwargs)
            if path == self.lock and not replacement:
                with mock.patch.object(Path, "lstat", lstat):
                    replacement.append(sessions._acquire_session_lock(self.lock))
            return info

        with mock.patch.object(Path, "lstat", competing_reclaimer):
            self.assertFalse(sessions._acquire_session_lock(self.lock))
        self.assertTrue(replacement[0])
        self.assertEqual(len(list(self.lock.iterdir())), 1)

    def test_loss_during_temporary_write_refuses_replace(self):
        """Recheck the owner after temporary output and before atomic replacement."""
        self.record.write_text("[]")
        temporary = sessions.tempfile.NamedTemporaryFile

        def lose_lock(*args, **kwargs):
            """Steal an expired lock while the writer prepares its temporary file."""
            handle = temporary(*args, **kwargs)
            self.age_lock(sessions.SESSION_LOCK_STALE_SECONDS + 1)
            self.assertTrue(sessions._acquire_session_lock(self.lock))
            return handle

        with mock.patch.object(sessions.tempfile, "NamedTemporaryFile", side_effect=lose_lock), \
                self.assertRaisesRegex(HookError, "lock.*lost"):
            sessions._update_session_record(
                self.directory, self.record, {str(self.directory)}, HookBudget(2)
            )
        self.assertEqual(self.record.read_text(), "[]")
        self.assertEqual(len(list(self.directory.glob("*.json"))), 1)

    def test_spent_budget_refuses_write_while_holding_lock(self):
        """Apply the caller's deadline to held work before committing ownership."""
        budget = HookBudget(2)
        self.record.write_text("[]")

        def spend_budget(_record, *args, **kwargs):
            """Spend the caller's budget during the record read."""
            budget.expires = 0
            return set()

        with mock.patch.object(sessions, "_read_session_record", side_effect=spend_budget), \
                self.assertRaises(HookError):
            sessions._update_session_record(
                self.directory, self.record, {str(self.directory)}, budget
            )
        self.assertEqual(self.record.read_text(), "[]")

    def test_maximum_hold_refuses_write_before_stale_age(self):
        """Refuse a slow holder even with a larger caller budget remaining."""
        self.record.write_text("[]")

        def expire_hold(_record, *args, **kwargs):
            """Advance the monotonic clock while the record is being read."""
            clock.return_value += sessions.SESSION_LOCK_STALE_SECONDS - 1
            return set()

        with mock.patch.object(sessions.time, "monotonic", return_value=100) as clock, \
                mock.patch.object(sessions, "_read_session_record", side_effect=expire_hold), \
                self.assertRaises(HookError):
            sessions._update_session_record(
                self.directory, self.record, {str(self.directory)}, HookBudget(100)
            )
        self.assertEqual(self.record.read_text(), "[]")


if __name__ == "__main__":
    unittest.main()
