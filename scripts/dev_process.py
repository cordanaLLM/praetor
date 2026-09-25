"""Stop a praetor process group without leaving the commands praetor runs behind."""

import contextlib
import os
import signal
import subprocess

# Longer than praetor's util.CommandWaitDelay (5 s). A praetor binary asked to stop by a
# catchable signal forwards it to the process group of each git or go command it runs, waits up
# to that delay for them, and kills the rest before it exits
# (internal/util/command_interrupt_unix.go).
STOP_GRACE = 10


def stop_process_group(process, grace=None):
    """SIGTERM the process group ``process`` leads; SIGKILL it if ``process`` outlasts ``grace``.

    SIGKILL alone ends praetor, which cannot catch it, and misses the commands praetor runs in
    process groups of their own: they run on, with git's index lock held. ``grace`` defaults to
    STOP_GRACE; an interrupted wait (Ctrl-C) kills the group at once and is re-raised. The caller
    reaps ``process``. POSIX-only, like the ``start_new_session`` children it stops.

    The git hooks carry their own copy (.config/lefthook/scripts/common.py): they are copied
    into adopting repositories as a self-contained set, and dev_repair.py runs from a snapshot of
    these scripts, so neither can import the other. The hooks start praetor through ``go run``
    and also wait for the rest of the group; these scripts start the praetor binary itself,
    which exits only after its commands.
    """
    try:
        os.killpg(process.pid, signal.SIGTERM)
    except ProcessLookupError:
        return
    try:
        process.wait(timeout=STOP_GRACE if grace is None else grace)
    except subprocess.TimeoutExpired:
        _kill_group(process)
    except BaseException:
        _kill_group(process)
        raise


def _kill_group(process):
    with contextlib.suppress(ProcessLookupError):
        os.killpg(process.pid, signal.SIGKILL)
